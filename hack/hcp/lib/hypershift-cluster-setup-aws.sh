#!/bin/bash
# AWS cloud library for hypershift-cluster-setup-multicloud.
# Sourced by bin/hypershift-cluster-setup-multicloud; do not execute directly.

cloud_not_implemented() {
    log_error "$1 is not yet implemented for AWS"
    exit 1
}

cloud_preflight() {
    if ! command -v aws &>/dev/null; then
        log_error "aws CLI not found"; return 1
    fi
    if ! command -v jq &>/dev/null; then
        log_error "jq not found (required for STS credential parsing)"; return 1
    fi
    local caller_arn
    caller_arn=$(aws sts get-caller-identity --query Arn --output text 2>/dev/null) || {
        log_error "AWS credentials not valid (run: aws login --profile ${AWS_PROFILE:-default})"; return 1
    }
    log_success "AWS identity: $caller_arn"
}

# AWS instance types that support nested virt: 8th-gen Intel (C8i, M8i, R8i)
# with CpuOptions.NestedVirtualization, and .metal instances (hardware KVM).
vm_type_supports_nested() {
    local itype="$1"
    [[ "$itype" == *.metal* ]] && return 0
    [[ "$itype" =~ ^(c8i|m8i|r8i) ]] && return 0
    return 1
}

# Workaround for HyperShift < 4.23: enable nested virt by stopping worker
# instances, modifying CpuOptions via the AWS API, and restarting them.
# Skipped when the NodePool CRD supports cpuOptions natively.
cloud_enable_nested_virt_workaround() {
    local region="$1"
    local hosted_kc="${HCP_HOSTED_KUBECONFIG:-${HCP_WORK_DIR}/${HCP_HOSTED_CLUSTER_NAME}-kubeconfig}"

    log_warn "Enabling nested virt via AWS API workaround (stop/modify/start)"
    log_warn "This workaround is for HyperShift versions that lack NodePool cpuOptions support."
    log_warn "It will not be needed once openshift/hypershift#8681 is available in your release."

    local node_names
    node_names=$(KUBECONFIG="${hosted_kc}" oc get nodes -l node-role.kubernetes.io/worker \
        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)
    if [[ -z "$node_names" ]]; then
        log_error "No worker nodes found — cannot enable nested virt"
        return 1
    fi
    log_info "Worker nodes: ${node_names}"

    local instance_ids=""
    for node in $node_names; do
        local iid
        iid=$(aws ec2 describe-instances --region "${region}" \
            --filters "Name=private-dns-name,Values=${node}" "Name=instance-state-name,Values=running" \
            --query 'Reservations[0].Instances[0].InstanceId' --output text 2>&1 | grep -v None)
        if [[ -n "$iid" && "$iid" != *"error"* ]]; then
            instance_ids="${instance_ids} ${iid}"
        else
            log_warn "Could not find EC2 instance for node ${node} (region=${region}): ${iid}"
        fi
    done
    instance_ids="${instance_ids# }"
    if [[ -z "$instance_ids" ]]; then
        log_error "No EC2 instances found for worker nodes"
        return 1
    fi
    log_info "Worker instances: ${instance_ids}"

    log_info "Stopping worker instances..."
    aws ec2 stop-instances --region "${region}" \
        --instance-ids ${instance_ids} --output text >/dev/null 2>&1
    aws ec2 wait instance-stopped --region "${region}" \
        --instance-ids ${instance_ids} 2>&1 || {
        log_error "Timed out waiting for instances to stop"
        return 1
    }
    log_success "Worker instances stopped"

    local _fail=false
    for iid in ${instance_ids}; do
        log_info "Enabling nested virt on ${iid}..."
        if ! aws ec2 modify-instance-cpu-options --region "${region}" \
            --instance-id "${iid}" --nested-virtualization enabled 2>&1; then
            log_error "Failed to modify CPU options for ${iid}"
            _fail=true
        fi
    done

    log_info "Starting worker instances..."
    aws ec2 start-instances --region "${region}" \
        --instance-ids ${instance_ids} --output text >/dev/null 2>&1
    aws ec2 wait instance-running --region "${region}" \
        --instance-ids ${instance_ids} 2>&1
    log_success "Worker instances started"

    if [[ "$_fail" == "true" ]]; then
        log_error "Some instances failed CPU option modification — nested virt may not work"
        return 1
    fi

    log_info "Waiting for worker nodes to become Ready..."
    local _ready_timeout=300 _elapsed=0
    while [[ $_elapsed -lt $_ready_timeout ]]; do
        local _not_ready
        _not_ready=$(KUBECONFIG="${hosted_kc}" oc get nodes -l node-role.kubernetes.io/worker \
            -o jsonpath='{.items[?(@.status.conditions[?(@.type=="Ready")].status!="True")].metadata.name}' 2>/dev/null)
        [[ -z "$_not_ready" ]] && break
        sleep 15
        _elapsed=$((_elapsed + 15))
    done
    if [[ $_elapsed -ge $_ready_timeout ]]; then
        log_warn "Some worker nodes not Ready after ${_ready_timeout}s — check manually"
    else
        log_success "All worker nodes Ready with nested virtualization enabled"
    fi
}

# Verify /dev/kvm on a worker; run the stop/modify/start workaround if missing.
cloud_ensure_nested_virt() {
    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"
    local hosted_kc="${HCP_WORK_DIR}/${HCP_HOSTED_CLUSTER_NAME}-kubeconfig"
    local _check_node
    _check_node=$(KUBECONFIG="${hosted_kc}" oc get nodes \
        -l node-role.kubernetes.io/worker -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    if [[ -z "$_check_node" ]]; then
        log_warn "No worker nodes found — cannot verify nested virt"
        return 1
    fi
    # Derive region from node FQDN (e.g. ip-10-0-1-2.us-west-2.compute.internal)
    if [[ -z "$region" ]]; then
        region=$(echo "$_check_node" | sed -n 's/.*\.\([a-z]*-[a-z]*-[0-9]*\)\.compute\.internal/\1/p')
        [[ -n "$region" ]] && log_info "Derived region from node name: ${region}"
    fi
    if [[ -z "$region" ]]; then
        log_error "Cannot determine AWS region — set HCP_AWS_REGION or HCP_LOCATION"
        return 1
    fi
    log_info "Verifying /dev/kvm on worker ${_check_node}..."
    if KUBECONFIG="${hosted_kc}" oc debug "node/${_check_node}" \
        -- chroot /host test -c /dev/kvm 2>/dev/null; then
        log_success "Nested virtualization confirmed: /dev/kvm present"
    else
        log_warn "/dev/kvm not found — enabling nested virt via AWS API workaround"
        cloud_enable_nested_virt_workaround "${region}"
    fi
}

# Sets HCP_LOCATION to the AWS region with the most On-Demand Standard vCPU quota.
# Respects HCP_AWS_REGION override (skips quota polling if set).
cloud_select_region() {
    local required_vcpus="${1:-0}"

    if [[ -n "${HCP_AWS_REGION:-}" ]]; then
        log_debug "Using HCP_AWS_REGION override: ${HCP_AWS_REGION}"
        HCP_LOCATION="${HCP_AWS_REGION}"
        return 0
    fi

    local best_region="" best_limit=-1
    local CANDIDATE_REGIONS=(us-east-1 us-east-2 us-west-2 eu-west-1 ap-southeast-1)

    echo "No region specified — checking EC2 On-Demand Standard vCPU quota across candidate regions..."
    for region in "${CANDIDATE_REGIONS[@]}"; do
        local limit
        # L-1216C47A: Running On-Demand Standard (A,C,D,H,I,M,R,T,Z) Instances vCPU quota
        limit=$(aws service-quotas get-service-quota \
            --service-code ec2 --quota-code L-1216C47A \
            --region "$region" \
            --query 'Quota.Value' --output text 2>/dev/null) || {
            echo "  ${region}: could not query quota, skipping"
            continue
        }
        local limit_int="${limit%.*}"
        echo "  ${region}: On-Demand Standard vCPU quota=${limit_int}"
        if (( limit_int > best_limit )); then
            best_limit=$limit_int
            best_region=$region
        fi
    done

    if [[ -z "$best_region" ]]; then
        log_warn "Could not read quota for any candidate region. Falling back to us-east-1."
        HCP_LOCATION="us-east-1"
        return
    fi

    if (( best_limit < required_vcpus )); then
        log_warn "Best region (${best_region}) has vCPU quota ${best_limit}, need ${required_vcpus}. Proceeding anyway."
    fi

    log_success "Selected region: ${best_region} (vCPU quota: ${best_limit})"
    HCP_LOCATION="${best_region}"
}

# Called by shared management_setup() at Step 4.
# Assumes the installer IAM role via STS and injects temporary credentials.
# Checks Route53 zone, ccoctl, hypershift, and pull secret.
cloud_management_prereqs() {
    local base_domain="${HCP_AWS_BASE_DOMAIN:-${HCP_BASE_DOMAIN}}"
    local dns_zone_id="${HCP_AWS_DNS_ZONE_ID:-}"
    local iam_role_arn="${HCP_AWS_IAM_ROLE_ARN:-}"

    echo ""
    echo "=== Step 4: Configuring AWS Prerequisites ==="
    echo ""

    if ! command -v aws &>/dev/null; then
        log_error "aws CLI not found"
        exit 1
    fi
    if ! command -v jq &>/dev/null; then
        log_error "jq not found (required for STS credential parsing)"
        exit 1
    fi

    if [[ -z "$dns_zone_id" ]]; then
        log_info "HCP_AWS_DNS_ZONE_ID not set. Looking for existing Route53 zone for ${base_domain}..."
        dns_zone_id=$(aws route53 list-hosted-zones-by-name \
            --query "HostedZones[?Name=='${base_domain}.'].Id" \
            --output text 2>/dev/null | cut -d'/' -f3)
        if [[ -z "$dns_zone_id" ]]; then
            log_error "HCP_AWS_DNS_ZONE_ID is required — no Route53 zone found for ${base_domain}"
            exit 1
        fi
        log_success "Found Route53 hosted zone: ${base_domain} (ID: ${dns_zone_id})"
        export HCP_AWS_DNS_ZONE_ID="${dns_zone_id}"
    fi

    local caller_arn account_id
    local _cred_ok=false
    if [[ "${HCP_PROW_MODE:-false}" == "true" ]]; then
        # Prow: cluster-profile credentials are static — no SSO to wait for.
        caller_arn=$(aws sts get-caller-identity --query Arn --output text 2>/dev/null) && _cred_ok=true
        [[ "$_cred_ok" == "true" ]] || { log_error "AWS credentials not valid (cluster profile: ${CLUSTER_PROFILE_DIR:-unset})"; exit 1; }
    else
        for _cred_attempt in $(seq 1 5); do
            caller_arn=$(aws sts get-caller-identity --query Arn --output text 2>/dev/null) && { _cred_ok=true; break; }
            log_warn "AWS credentials not valid — refresh with: aws sso login --profile ${AWS_PROFILE:-default}"
            log_info "Waiting 60s for credential refresh (attempt ${_cred_attempt}/5)..."
            sleep 60
        done
        [[ "$_cred_ok" == "true" ]] || { log_error "AWS credentials not valid after 5 attempts"; exit 1; }
    fi
    account_id=$(aws sts get-caller-identity --query Account --output text 2>/dev/null)

    if [[ -n "$iam_role_arn" ]]; then
        log_debug "Assuming IAM role: ${iam_role_arn}"
        local creds
        creds=$(aws sts assume-role \
            --role-arn "${iam_role_arn}" \
            --role-session-name "hypershift-install" \
            --output json 2>/dev/null) || {
            log_error "Failed to assume IAM role: ${iam_role_arn}"
            exit 1
        }
        AWS_ACCESS_KEY_ID=$(echo "$creds" | jq -r '.Credentials.AccessKeyId // empty') || { log_error "Failed to parse STS credentials"; exit 1; }
        AWS_SECRET_ACCESS_KEY=$(echo "$creds" | jq -r '.Credentials.SecretAccessKey // empty') || { log_error "Failed to parse STS credentials"; exit 1; }
        AWS_SESSION_TOKEN=$(echo "$creds" | jq -r '.Credentials.SessionToken // empty') || { log_error "Failed to parse STS credentials"; exit 1; }
        [[ -z "$AWS_ACCESS_KEY_ID" ]] && { log_error "STS credentials missing AccessKeyId"; exit 1; }
        export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
        log_success "AWS account: ${account_id} (assumed role: ${iam_role_arn})"
    else
        log_success "AWS account: ${account_id} (identity: ${caller_arn})"
        log_info "HCP_AWS_IAM_ROLE_ARN not set — using current identity; scoped install role will be created before openshift-install"
    fi

    aws route53 get-hosted-zone --id "${dns_zone_id}" >/dev/null 2>&1 && {
        log_success "Route53 hosted zone: ${dns_zone_id} (${base_domain})"
    } || {
        log_error "Route53 hosted zone ${dns_zone_id} not found"
        exit 1
    }

    echo ""
    echo "=== Step 5: Verifying Required Binaries ==="
    echo ""

    local CCOCTL
    CCOCTL=$(command -v ccoctl-native 2>/dev/null || command -v ccoctl 2>/dev/null)
    if [[ -n "$CCOCTL" ]]; then
        log_success "ccoctl: $CCOCTL"
    else
        log_error "ccoctl-native or ccoctl not found"
        exit 1
    fi

    local HYPERSHIFT
    HYPERSHIFT=$(command -v hypershift 2>/dev/null)
    if [[ -n "$HYPERSHIFT" ]]; then
        log_success "hypershift: $HYPERSHIFT"
    else
        log_error "hypershift CLI not found"
        exit 1
    fi

    if [[ -f "${HCP_PULL_SECRET}" ]]; then
        log_success "Pull secret: ${HCP_PULL_SECRET}"
    else
        log_error "Pull secret not found at ${HCP_PULL_SECRET}"
        exit 1
    fi
}

HCP_INSTALL_SCOPED_ROLE_NAME=""

# Creates a least-privilege IAM role scoped to openshift-install IPI permissions,
# assumes it, and exports the temporary credentials. If no permissions file exists
# for the detected OCP version, warns and optionally falls back to the caller's
# own credentials (which ccoctl and openshift-install will then use unscoped).
cloud_assume_scoped_install_role() {
    local ocp_ver
    ocp_ver=$(echo "${HCP_RELEASE_IMAGE:-}" | grep -oE '[0-9]+\.[0-9]+' | head -1)
    if [[ -z "$ocp_ver" ]]; then
        log_warn "Could not detect OCP version from HCP_RELEASE_IMAGE — skipping scoped install role"
        return 0
    fi

    local perms_file="${SCRIPT_DIR}/../env/management-cluster-permissions-${ocp_ver}.yaml"
    # Resolve symlinks so the version label in the file is accurate
    [[ -L "$perms_file" ]] && perms_file=$(readlink -f "$perms_file")

    if [[ ! -f "$perms_file" ]]; then
        log_warn "No scoped permissions file found for OCP ${ocp_ver} (${perms_file}) — installer credentials will be forwarded unscoped to cluster operators"
        if [[ "${GLOBAL_YES:-false}" != "true" ]]; then
            read -r -p "Proceed without scoped installer role? [y/N] " answer
            [[ "${answer,,}" == "y" ]] || { log_error "Aborted"; exit 1; }
        fi
        return 0
    fi

    log_info "Building scoped install role from ${perms_file} (OCP ${ocp_ver})..."

    local actions_json
    actions_json=$(grep -E '^\s+-\s+[A-Za-z0-9]+:[A-Za-z*]+' "$perms_file" \
        | sed 's/^[[:space:]]*-[[:space:]]*//' | jq -R . | jq -s .)
    local policy_doc
    policy_doc=$(jq -n --argjson actions "$actions_json" \
        '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":$actions,"Resource":"*"}]}')

    local caller_arn account_id
    caller_arn=$(aws sts get-caller-identity --query Arn --output text)
    account_id=$(aws sts get-caller-identity --query Account --output text)

    local trust_doc
    trust_doc=$(jq -n --arg arn "$caller_arn" \
        '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":$arn},"Action":"sts:AssumeRole"}]}')

    # Role name: max 64 chars; use last 4 digits of epoch for uniqueness
    local role_name
    role_name="hcp-install-${HCP_MGMT_CLUSTER_NAME}"
    role_name="${role_name:0:59}-$(date +%s | tail -c 5)"

    local assume_duration="${HCP_INSTALL_ROLE_DURATION:-28800}"

    aws iam create-role \
        --role-name "$role_name" \
        --assume-role-policy-document "$trust_doc" \
        --description "Scoped install role for ${HCP_MGMT_CLUSTER_NAME}" \
        --max-session-duration "${HCP_INSTALL_ROLE_DURATION:-28800}" \
        >/dev/null
    aws iam put-role-policy \
        --role-name "$role_name" \
        --policy-name "install-policy" \
        --policy-document "$policy_doc" \
        >/dev/null

    # Brief wait for IAM propagation before assuming
    sleep 10

    # Try the requested duration; if AWS rejects it due to role chaining
    # (temporary caller creds → role capped at 3600s), skip the scoped role
    # entirely.  CAPI-based installs (4.22+) run controllers locally for the
    # full bootstrap duration (~45 min); a 1-hour session leaves no margin and
    # credentials expire mid-install with "Probable clock skew" errors.
    local creds_json role_arn="arn:aws:iam::${account_id}:role/${role_name}"
    creds_json=$(aws sts assume-role \
        --role-arn "$role_arn" \
        --role-session-name "hcp-install" \
        --duration-seconds "$assume_duration" \
        --query Credentials \
        --output json 2>&1) || {
        if echo "$creds_json" | grep -q "DurationSeconds exceeds"; then
            log_warn "Role chaining detected — using caller credentials directly (1h cap too short for CAPI install)"
            aws iam delete-role-policy --role-name "$role_name" --policy-name "install-policy" 2>/dev/null || true
            aws iam delete-role --role-name "$role_name" 2>/dev/null || true
            return 0
        else
            log_error "Failed to assume scoped install role: $creds_json"
            exit 1
        fi
    }

    export AWS_ACCESS_KEY_ID
    export AWS_SECRET_ACCESS_KEY
    export AWS_SESSION_TOKEN
    AWS_ACCESS_KEY_ID=$(echo "$creds_json" | jq -r .AccessKeyId)
    AWS_SECRET_ACCESS_KEY=$(echo "$creds_json" | jq -r .SecretAccessKey)
    AWS_SESSION_TOKEN=$(echo "$creds_json" | jq -r .SessionToken)
    unset creds_json

    HCP_INSTALL_SCOPED_ROLE_NAME="$role_name"
    log_success "Assumed scoped install role ${role_name} for OCP ${ocp_ver}"
}

cloud_cleanup_scoped_install_role() {
    [[ -z "${HCP_INSTALL_SCOPED_ROLE_NAME:-}" ]] && return 0
    local role="${HCP_INSTALL_SCOPED_ROLE_NAME}"
    HCP_INSTALL_SCOPED_ROLE_NAME=""
    aws iam delete-role-policy --role-name "$role" --policy-name "install-policy" 2>/dev/null || true
    aws iam delete-role --role-name "$role" 2>/dev/null || true
    log_info "Cleaned up scoped install role ${role}"
}

cloud_management_create() {
    log_debug "cloud_management_create() called"
    log_debug "  HCP_MGMT_CLUSTER_NAME=$HCP_MGMT_CLUSTER_NAME"

    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"
    local base_domain="${HCP_AWS_BASE_DOMAIN:-${HCP_BASE_DOMAIN}}"
    local worker_type="${HCP_AWS_MGMT_WORKER_TYPE:-m5.xlarge}"

    if [[ -z "${region}" ]]; then
        log_debug "region empty, auto-selecting..."
        cloud_select_region 36
        region="${HCP_LOCATION:-}"
    fi

    mkdir -p "${HCP_WORK_DIR}"
    cd "${HCP_WORK_DIR}"
    log_debug "Working directory: $(pwd)"

    if ! find_pull_secret; then
        log_error "Pull secret not found. Set HCP_PULL_SECRET or place pull-secret.txt in current directory"
        exit 1
    fi
    augment_pull_secret
    validate_pull_secret
    [[ ! -f "${HCP_SSH_KEY}" ]] && { log_error "SSH key not found: ${HCP_SSH_KEY}"; exit 1; }

    echo ""
    echo "=== Creating Management Cluster: ${HCP_MGMT_CLUSTER_NAME} ==="
    echo "Region: ${region}"
    echo "Base domain: ${base_domain}"
    echo ""

    if [[ -d "./${HCP_MGMT_CLUSTER_NAME}" ]]; then
        if [[ "${GLOBAL_YES:-false}" == "true" ]]; then
            log_info "Installation directory ${HCP_MGMT_CLUSTER_NAME} already exists, deleting (--yes flag provided)..."
            rm -rf "./${HCP_MGMT_CLUSTER_NAME}"
            log_success "Installation directory deleted"
        else
            log_error "Installation directory ${HCP_MGMT_CLUSTER_NAME} already exists"
            echo "To recreate, either:"
            echo "  1. Use a different name: --management-cluster-name other-name"
            echo "  2. Delete existing: rm -rf ${HCP_MGMT_CLUSTER_NAME}"
            echo "  3. Run teardown first: $0 management teardown"
            exit 1
        fi
    fi

    local OPENSHIFT_INSTALL
    if [[ -n "${HCP_OPENSHIFT_INSTALL:-}" ]]; then
        OPENSHIFT_INSTALL="$HCP_OPENSHIFT_INSTALL"
    elif [[ -n "${HCP_TOOLS_DIR:-}" ]] && [[ -x "${HCP_TOOLS_DIR}/openshift-install" ]]; then
        OPENSHIFT_INSTALL="${HCP_TOOLS_DIR}/openshift-install"
    else
        OPENSHIFT_INSTALL=$(command -v openshift-install 2>/dev/null || echo "")
    fi
    if [[ ! -x "$OPENSHIFT_INSTALL" ]]; then
        log_error "openshift-install not found (set HCP_OPENSHIFT_INSTALL to specify a path)"
        exit 1
    fi
    if [[ -n "${HCP_MGMT_RELEASE_IMAGE:-}" ]]; then
        export OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE="${HCP_MGMT_RELEASE_IMAGE}"
        log_info "Release image override: ${HCP_MGMT_RELEASE_IMAGE}"

        # Warn if the installer binary major.minor does not match the release image
        local _oi_version _oi_mm _release_tag _release_mm
        _oi_version=$("$OPENSHIFT_INSTALL" version 2>/dev/null \
            | head -1 | awk '{print $2}' | sed 's/^v//')
        _oi_mm=$(echo "${_oi_version}" | sed -n 's/^1\.\([0-9]*\.[0-9]*\).*/\1/p')
        [[ -z "$_oi_mm" ]] && _oi_mm=$(echo "${_oi_version}" | cut -d. -f1,2)
        _release_tag=$(echo "$HCP_MGMT_RELEASE_IMAGE" \
            | sed -n 's/.*:\([0-9][0-9.]*[0-9]\).*/\1/p')
        _release_mm=$(echo "${_release_tag}" | cut -d. -f1,2)

        if [[ -n "$_release_mm" && -n "$_oi_mm" && "$_oi_mm" != "$_release_mm" ]]; then
            log_error "Installer (${_oi_version}) does not match release (${_release_tag})"
            log_error "Set HCP_OPENSHIFT_INSTALL to a ${_release_mm}.x binary"
            exit 1
        fi
    fi

    local CCOCTL
    CCOCTL=$(command -v ccoctl-native 2>/dev/null || command -v ccoctl 2>/dev/null)
    [[ -z "$CCOCTL" ]] && { log_error "ccoctl-native or ccoctl not found"; exit 1; }

    # Credential setup: prefer profile-based resolution so credentials re-resolve
    # on every API call and never go stale during a long install.
    # Frozen env vars (AWS_ACCESS_KEY_ID etc.) override profiles and DO NOT refresh;
    # warn if they are set and leave them as-is for explicit overrides.
    local aws_profile="${HCP_AWS_PROFILE:-${AWS_PROFILE:-}}"
    if [[ -n "${AWS_ACCESS_KEY_ID:-}" ]]; then
        log_warn "AWS_ACCESS_KEY_ID is already set — using frozen credentials."
        log_warn "These may expire mid-install. For reliable unattended installs:"
        log_warn "  unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN"
        log_warn "  export AWS_PROFILE=<profile>  # or set HCP_AWS_PROFILE"
    elif [[ -n "$aws_profile" ]]; then
        if ! AWS_PROFILE="$aws_profile" aws sts get-caller-identity >/dev/null 2>&1; then
            log_error "AWS profile '$aws_profile' is not authenticated. Run: aws login --profile $aws_profile"
            exit 1
        fi
        export AWS_PROFILE="$aws_profile"
        unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
        log_success "Using AWS profile: $aws_profile"
    else
        if ! aws sts get-caller-identity >/dev/null 2>&1; then
            log_error "No AWS credentials configured."
            echo "  Set HCP_AWS_PROFILE or AWS_PROFILE, or run: aws login --profile <profile>"
            exit 1
        fi
        log_info "Using default AWS credential chain (set HCP_AWS_PROFILE or AWS_PROFILE to use a named profile)"
    fi
    log_debug "AWS identity: $(aws sts get-caller-identity --query Arn --output text 2>/dev/null)"

    # Ensure Route53 hosted zone exists for base domain
    local dns_zone_id="${HCP_AWS_DNS_ZONE_ID:-}"
    if [[ -z "$dns_zone_id" ]]; then
        log_info "HCP_AWS_DNS_ZONE_ID not set. Looking for existing Route53 zone for ${base_domain}..."
        dns_zone_id=$(aws route53 list-hosted-zones-by-name --query "HostedZones[?Name=='${base_domain}.'].Id" --output text 2>/dev/null | cut -d'/' -f3)
        if [[ -z "$dns_zone_id" ]]; then
            log_info "Route53 zone not found for ${base_domain}. Creating..."
            dns_zone_id=$(aws route53 create-hosted-zone \
                --name "${base_domain}" \
                --caller-reference "hcp-$(date +%s)" \
                --query 'HostedZone.Id' --output text 2>/dev/null | cut -d'/' -f3) || {
                log_error "Failed to create Route53 hosted zone for ${base_domain}"
                exit 1
            }
            log_success "Created Route53 hosted zone: ${base_domain} (ID: ${dns_zone_id})"
        else
            log_success "Found existing Route53 hosted zone: ${base_domain} (ID: ${dns_zone_id})"
        fi
        export HCP_AWS_DNS_ZONE_ID="${dns_zone_id}"
    fi

    # Ensure NS delegation exists in parent zone so external resolvers can find our zone
    local ns_servers
    ns_servers=$(aws route53 list-resource-record-sets \
        --hosted-zone-id "${HCP_AWS_DNS_ZONE_ID}" \
        --query "ResourceRecordSets[?Type=='NS'].ResourceRecords[].Value" \
        --output json 2>/dev/null)
    if [[ -n "$ns_servers" && "$ns_servers" != "[]" ]]; then
        # Walk up the domain hierarchy to find the parent zone in Route53
        local parent_domain parent_zone_id=""
        parent_domain="$base_domain"
        while [[ "$parent_domain" == *.* ]]; do
            parent_domain=$(echo "$parent_domain" | cut -d'.' -f2-)
            parent_zone_id=$(aws route53 list-hosted-zones-by-name \
                --dns-name "$parent_domain" \
                --query "HostedZones[?Name=='${parent_domain}.'].Id" \
                --output text 2>/dev/null | cut -d'/' -f3)
            [[ -n "$parent_zone_id" ]] && break
        done
        if [[ -n "$parent_zone_id" ]]; then
            local ns_resource_records
            ns_resource_records=$(echo "$ns_servers" | jq '[.[] | {"Value": .}]')
            local change_batch
            change_batch=$(jq -n \
                --arg name "${base_domain}." \
                --argjson rr "$ns_resource_records" \
                '{"Changes":[{"Action":"UPSERT","ResourceRecordSet":{"Name":$name,"Type":"NS","TTL":300,"ResourceRecords":$rr}}]}')
            log_info "Ensuring NS delegation for ${base_domain} in parent zone ${parent_domain}..."
            aws route53 change-resource-record-sets \
                --hosted-zone-id "$parent_zone_id" \
                --change-batch "$change_batch" >/dev/null 2>&1 && \
                log_success "NS delegation set in ${parent_domain} for ${base_domain}" || \
                log_warn "Could not set NS delegation in ${parent_domain} — DNS may not resolve externally"
        else
            log_debug "Parent zone ${parent_domain} not found in this account — skipping NS delegation"
        fi
    fi

    # With --yes, clean up stale AWS resources from any previous failed run
    # (directory may already be gone but Route53/CF resources can linger)
    if [[ "${GLOBAL_YES:-false}" == "true" ]]; then
        cloud_cleanup_stale_install_resources "${HCP_MGMT_CLUSTER_NAME}"
    fi

    mkdir -p "${HCP_MGMT_CLUSTER_NAME}"
    cd "${HCP_MGMT_CLUSTER_NAME}"

    echo "Generating install-config.yaml..."
    cat > install-config.yaml <<EOF
apiVersion: v1
baseDomain: ${base_domain}
metadata:
  name: ${HCP_MGMT_CLUSTER_NAME}
credentialsMode: Manual
platform:
  aws:
    region: ${region}
    userTags:
      cluster: ${HCP_MGMT_CLUSTER_NAME}
pullSecret: '$(cat "${HCP_EFFECTIVE_PULL_SECRET}" | jq -c .)'
sshKey: '$(cat "${HCP_SSH_KEY}")'
compute:
- name: worker
  platform:
    aws:
      type: ${worker_type}
  replicas: 3
controlPlane:
  name: master
  platform:
    aws:
      type: ${worker_type}
  replicas: 3
EOF
    log_success "Created install-config.yaml"
    cp install-config.yaml install-config.yaml.backup

    echo "Extracting credential requests..."
    mkdir -p credreqs
    oc adm release extract --credentials-requests --cloud=aws \
        --to=./credreqs "${HCP_RELEASE_IMAGE}" 2>/dev/null || \
        log_warn "Could not extract credential requests — credreqs may be empty"

    # ccoctl needs full credentials (S3 OIDC bucket + IAM OIDC provider creation).
    # Assume the scoped install role only for openshift-install below.
    echo "Creating OIDC provider and IAM roles via ccoctl..."
    (umask 077; ${CCOCTL} aws create-all \
        --name="${HCP_MGMT_CLUSTER_NAME}" \
        --region="${region}" \
        --credentials-requests-dir=./credreqs \
        --output-dir=./ccoctl-output > /tmp/ccoctl_$$.log 2>&1) || {
        log_warn "ccoctl aws create-all failed — check /tmp/ccoctl_$$.log"
    }
    log_success "OIDC provider and IAM roles created"

    # ccoctl creates the S3 OIDC bucket with public access blocked by default.
    # AWS STS must be able to fetch the JWKS over HTTPS to verify IRSA tokens.
    # Without this, all IRSA-based operators (machine-api, image-registry, etc.) fail.
    local oidc_bucket="${HCP_MGMT_CLUSTER_NAME}-oidc"
    log_info "Enabling public read access on OIDC S3 bucket ${oidc_bucket}..."
    aws s3api delete-public-access-block --bucket "${oidc_bucket}" 2>/dev/null || true
    aws s3api put-bucket-policy --bucket "${oidc_bucket}" --policy "{
      \"Version\": \"2012-10-17\",
      \"Statement\": [{
        \"Effect\": \"Allow\",
        \"Principal\": \"*\",
        \"Action\": \"s3:GetObject\",
        \"Resource\": \"arn:aws:s3:::${oidc_bucket}/*\"
      }]
    }" 2>/dev/null && log_success "OIDC bucket is publicly readable (STS can verify IRSA tokens)" || \
        log_warn "Could not make OIDC bucket public — IRSA may fail for all operators"
    # Copy the service account signing key to where openshift-install expects it.
    # Without this the installer generates a random key that doesn't match the
    # public key uploaded to the OIDC provider, breaking IRSA for all operators.
    mkdir -p tls
    cp ./ccoctl-output/tls/bound-service-account-signing-key.key tls/ 2>/dev/null || \
        log_warn "Could not copy service account signing key — IRSA may not work"

    # Save the pre-role credential state before assuming the install role.
    # This must happen before cloud_assume_scoped_install_role so the retry
    # loop can restore and re-assume fresh credentials on each attempt.
    local _ORIG_PROFILE="${AWS_PROFILE:-}"

    # Assume the scoped install role now so openshift-install create manifests
    # runs with explicit env-var credentials (AWS_ACCESS_KEY_ID / SECRET /
    # SESSION_TOKEN).  The patched installer embeds a CI release image whose
    # architecture is unknown to the installer binary; when it cannot resolve
    # the architecture it falls back to an interactive AWS credential prompt
    # that blocks unattended runs.  Explicit env-var credentials bypass that
    # code path entirely.
    cloud_assume_scoped_install_role

    echo "Generating manifests..."
    ${OPENSHIFT_INSTALL} create manifests --dir . --log-level=warn
    cp ./ccoctl-output/manifests/* manifests/ 2>/dev/null || true

    # On macOS, mDNSResponder caches negative (NXDOMAIN) responses for the SOA
    # minimum TTL (~300s). The installer creates the Route53 API record mid-install,
    # but the local cache can return NXDOMAIN for up to 5 minutes afterwards,
    # causing the 20-minute API wait to time out.
    # Fix: background watcher polls dig @8.8.8.8 (bypasses local cache, no side
    # effects) and flushes the cache non-interactively once the record appears.
    # sudo -n fails silently if a password is required; the installer's 20-minute
    # window is long enough for the 5-minute negative TTL to expire naturally.
    if [[ "$(uname)" == "Darwin" ]]; then
        local api_hostname="api.${HCP_MGMT_CLUSTER_NAME}.${base_domain}"
        (
            while ! dig +short +time=2 +tries=1 @8.8.8.8 "${api_hostname}" 2>/dev/null | grep -qE '^[0-9]'; do
                sleep 10
            done
            sudo -n dscacheutil -flushcache 2>/dev/null && \
                sudo -n killall -HUP mDNSResponder 2>/dev/null || true
        ) &
        disown $!
    fi

    echo ""
    log_debug "About to enter cluster creation loop"
    echo "Creating cluster (this takes ~40 minutes)..."
    echo "Monitor progress with: tail -f $(pwd)/.openshift_install.log"
    log_debug "Working directory: $(pwd)"

    local INSTALL_OK=false
    # Patched installers embed a CI registry image (registry.ci.openshift.org/origin/release:*)
    # which is ephemeral and may not be pullable from cloud networks. Override with the user-
    # specified release image so bootstrap VMs pull from a stable, accessible registry.
    [[ -n "${RELEASE_IMAGE:-}" ]] && export OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE="${RELEASE_IMAGE}"
    log_debug "Starting cluster creation attempt loop"
    for attempt in 1 2 3; do
        log_debug "Cluster creation attempt $attempt/3"
        # Re-assume the scoped install role at the start of each retry.
        # On attempt 1 the role was already assumed before create manifests;
        # restore + re-assume only for attempts 2+ (previous credentials expired).
        if [[ $attempt -gt 1 ]]; then
            unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
            [[ -n "$_ORIG_PROFILE" ]] && export AWS_PROFILE="$_ORIG_PROFILE"
            cloud_cleanup_scoped_install_role
            cloud_assume_scoped_install_role
        fi
        if ${OPENSHIFT_INSTALL} create cluster --dir . --log-level=info; then
            log_debug "openshift-install succeeded"
            INSTALL_OK=true
            break
        else
            log_debug "openshift-install failed on attempt $attempt"
        fi
        if [[ $attempt -lt 3 ]]; then
            # If bootstrap completed but only cleanup failed, the cluster is actually up —
            # use wait-for install-complete instead of restarting from scratch
            if grep -q "Bootstrap etcd member has been removed\|destroy bootstrap" \
                    .openshift_install.log 2>/dev/null; then
                log_warn "Bootstrap cleanup failed but cluster appears to be up — waiting for install-complete..."
                if ${OPENSHIFT_INSTALL} wait-for install-complete --dir . --log-level=info; then
                    INSTALL_OK=true
                    break
                fi
                # wait-for failed too — fall through to full retry below
            fi
            if grep -q "provision control-plane machines" .openshift_install.log 2>/dev/null; then
                log_warn "Machine provisioning timeout — trying wait-for install-complete..."
                if ${OPENSHIFT_INSTALL} wait-for install-complete --dir . --log-level=info; then
                    INSTALL_OK=true
                    break
                fi
            fi
            cp install-config.yaml.backup install-config.yaml
            rm -rf auth tls .clusterapi_output metadata.json .openshift_install.log
            ${OPENSHIFT_INSTALL} create manifests --dir . --log-level=warn 2>/dev/null || true
            cp ./ccoctl-output/tls/bound-service-account-signing-key.key tls/ 2>/dev/null || true
            cp ./ccoctl-output/manifests/* manifests/ 2>/dev/null || true
            cloud_cleanup_stale_install_resources "${HCP_MGMT_CLUSTER_NAME}"
            log_warn "openshift-install failed (attempt $attempt/3), retrying in 60s..."
            sleep 60
        fi
    done

    if [[ "$INSTALL_OK" != "true" ]]; then
        cloud_cleanup_scoped_install_role
        log_error "openshift-install failed after 3 attempts"
        exit 1
    fi

    cloud_cleanup_scoped_install_role
    mkdir -p "${HCP_WORK_DIR}/auth"
    [[ -f "auth/kubeconfig" ]] || { log_error "Kubeconfig not found after cluster creation"; exit 1; }
    cp auth/kubeconfig "${HCP_WORK_DIR}/auth/kubeconfig"

    echo ""
    log_success "Management cluster created successfully!"
    echo "  Name: ${HCP_MGMT_CLUSTER_NAME}"
    echo "  Kubeconfig: ${HCP_WORK_DIR}/auth/kubeconfig"
    echo ""
    print_next_steps_mgmt_create
}

cloud_management_setup()    { management_setup; }

cloud_management_teardown() {
    cd "${HCP_WORK_DIR}"

    echo ""
    echo "=== Tearing Down Management Cluster: ${HCP_MGMT_CLUSTER_NAME} ==="
    echo ""

    read -p "Delete management cluster ${HCP_MGMT_CLUSTER_NAME} and ALL hosted clusters? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Teardown cancelled"
        exit 0
    fi

    if [[ ! -d "${HCP_MGMT_CLUSTER_NAME}" ]]; then
        log_warn "Installation directory ${HCP_MGMT_CLUSTER_NAME} not found"
        echo "Installation directory not found — cannot run openshift-install destroy. Clean up AWS resources manually."
        exit 1
    fi

    cd "${HCP_MGMT_CLUSTER_NAME}"

    local OPENSHIFT_INSTALL
    if [[ -n "${HCP_OPENSHIFT_INSTALL:-}" ]]; then
        OPENSHIFT_INSTALL="$HCP_OPENSHIFT_INSTALL"
    elif [[ -n "${HCP_TOOLS_DIR:-}" ]] && [[ -x "${HCP_TOOLS_DIR}/openshift-install" ]]; then
        OPENSHIFT_INSTALL="${HCP_TOOLS_DIR}/openshift-install"
    else
        OPENSHIFT_INSTALL=$(command -v openshift-install 2>/dev/null || echo "")
    fi
    if [[ ! -x "$OPENSHIFT_INSTALL" ]]; then
        log_error "openshift-install not found (set HCP_OPENSHIFT_INSTALL to specify a path)"
        exit 1
    fi
    if [[ -n "${HCP_MGMT_RELEASE_IMAGE:-}" ]]; then
        export OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE="${HCP_MGMT_RELEASE_IMAGE}"
    fi
    "${OPENSHIFT_INSTALL}" destroy cluster --dir . --log-level=info

    cd ..
    rm -rf "${HCP_MGMT_CLUSTER_NAME}"

    echo ""
    log_success "Management cluster destroyed"
    echo "  Deleted: ${HCP_MGMT_CLUSTER_NAME}"
}

HCP_HOSTED_SETUP_ROLE_NAME=""
HCP_HOSTED_SETUP_ROLE_ARN=""
HCP_HOSTED_STS_CREDS_FILE=""

# Creates a temporary IAM role and an STS credentials file so that hypershift
# create-iam and create-cluster can pass --sts-creds / --role-arn.
# Skips creation if HCP_AWS_HYPERSHIFT_ROLE_ARN is pre-set.
cloud_hosted_create_setup_role() {
    local account_id caller_arn

    # Export current AWS credentials to the STS file that hypershift reads.
    # Use AWS CLI v2 export-credentials as the canonical source; fall back to
    # env vars for older CLI versions.
    HCP_HOSTED_STS_CREDS_FILE="${HCP_WORK_DIR}/.hcp-sts-creds-$$.json"
    local raw_creds
    if raw_creds=$(aws configure export-credentials --format process 2>/dev/null); then
        local key secret token
        key=$(echo "$raw_creds"    | jq -r '.AccessKeyId     // empty')
        secret=$(echo "$raw_creds" | jq -r '.SecretAccessKey // empty')
        token=$(echo "$raw_creds"  | jq -r '.SessionToken    // empty')
    else
        key="${AWS_ACCESS_KEY_ID:-}"
        secret="${AWS_SECRET_ACCESS_KEY:-}"
        token="${AWS_SESSION_TOKEN:-}"
    fi
    [[ -z "$key" ]] && { log_error "Cannot read AWS credentials for --sts-creds"; exit 1; }
    (umask 077; jq -n \
        --arg k "$key" --arg s "$secret" --arg t "$token" \
        '{"Credentials":{"AccessKeyId":$k,"SecretAccessKey":$s,"SessionToken":$t}}' \
        > "${HCP_HOSTED_STS_CREDS_FILE}")
    unset key secret token raw_creds

    if [[ -n "${HCP_AWS_HYPERSHIFT_ROLE_ARN:-}" ]]; then
        HCP_HOSTED_SETUP_ROLE_ARN="${HCP_AWS_HYPERSHIFT_ROLE_ARN}"
        log_success "Using pre-configured hypershift role: ${HCP_HOSTED_SETUP_ROLE_ARN}"
        return 0
    fi

    account_id=$(aws sts get-caller-identity --query Account --output text)
    caller_arn=$(aws sts get-caller-identity --query Arn --output text)

    local trust_doc
    trust_doc=$(jq -n --arg arn "$caller_arn" \
        '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":$arn},"Action":"sts:AssumeRole"}]}')

    local role_name
    role_name="hcp-hosted-setup-${HCP_HOSTED_CLUSTER_NAME}"
    role_name="${role_name:0:59}-$(date +%s | tail -c 5)"

    aws iam create-role \
        --role-name "$role_name" \
        --assume-role-policy-document "$trust_doc" \
        --description "Temporary hypershift setup role for ${HCP_HOSTED_CLUSTER_NAME}" \
        >/dev/null
    aws iam attach-role-policy \
        --role-name "$role_name" \
        --policy-arn "arn:aws:iam::aws:policy/AdministratorAccess" \
        >/dev/null

    HCP_HOSTED_SETUP_ROLE_NAME="$role_name"
    HCP_HOSTED_SETUP_ROLE_ARN="arn:aws:iam::${account_id}:role/${role_name}"
    log_success "Created hypershift setup role: ${role_name}"
    sleep 10
}

cloud_hosted_cleanup_setup_role() {
    [[ -n "${HCP_HOSTED_STS_CREDS_FILE:-}" ]] && rm -f "${HCP_HOSTED_STS_CREDS_FILE}" 2>/dev/null || true
    [[ -z "${HCP_HOSTED_SETUP_ROLE_NAME:-}" ]] && return 0
    local role="${HCP_HOSTED_SETUP_ROLE_NAME}"
    HCP_HOSTED_SETUP_ROLE_NAME=""
    aws iam detach-role-policy --role-name "$role" \
        --policy-arn "arn:aws:iam::aws:policy/AdministratorAccess" 2>/dev/null || true
    aws iam delete-role --role-name "$role" 2>/dev/null || true
    log_info "Cleaned up hypershift setup role: ${role}"
}

cloud_hosted_setup() {
    export KUBECONFIG="${HCP_MGMT_KUBECONFIG}"
    cd "${HCP_WORK_DIR}"

    if oc get hostedcluster "${HCP_HOSTED_CLUSTER_NAME}" -n "${HCP_NAMESPACE}" \
            >/dev/null 2>&1; then
        log_success "Hosted cluster ${HCP_HOSTED_CLUSTER_NAME} already exists, skipping setup"
        local hosted_worker_type="${HCP_AWS_HOSTED_WORKER_TYPE:-m5.large}"
        if [[ "${HCP_NESTED_VIRT:-no}" == "yes" ]] && vm_type_supports_nested "$hosted_worker_type"; then
            cloud_ensure_nested_virt
        fi
        return 0
    fi

    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"
    local base_domain="${HCP_AWS_BASE_DOMAIN:-${HCP_BASE_DOMAIN}}"
    local dns_zone_id="${HCP_AWS_DNS_ZONE_ID:-}"
    local hosted_worker_type="${HCP_AWS_HOSTED_WORKER_TYPE:-m5.large}"
    if [[ "${HCP_NESTED_VIRT:-no}" == "yes" ]] && ! vm_type_supports_nested "$hosted_worker_type"; then
        hosted_worker_type="m8i.large"
        log_info "HCP_NESTED_VIRT=yes — using ${hosted_worker_type} (supports nested virt)"
    fi
    local s3_bucket="${HCP_AWS_OIDC_S3_BUCKET:-}"

    if [[ -z "${region}" ]]; then
        cloud_select_region $(( HCP_NODE_COUNT * 4 ))
        region="${HCP_LOCATION:-}"
    fi

    local oidc_dir="oidc-${HCP_HOSTED_CLUSTER_NAME}"
    _hosted_oidc_dir="${oidc_dir}"

    require_pull_secret
    augment_pull_secret
    cloud_hosted_create_setup_role

    # On failure, clean up partial OIDC/IAM resources unless --no-teardown.
    _hosted_setup_cleanup() {
        local rc=$?
        cloud_hosted_cleanup_setup_role
        [[ $rc -eq 0 ]] && return 0
        if [[ "${HCP_NO_TEARDOWN:-false}" == "true" ]]; then
            log_warn "Setup failed (exit $rc) — OIDC/IAM resources preserved for retry (--no-teardown)"
            return
        fi
        log_warn "Setup failed (exit $rc) — cleaning up partial OIDC/IAM resources"
        local _cc _odir="${_hosted_oidc_dir:-}"
        _cc=$(command -v ccoctl-native 2>/dev/null || command -v ccoctl 2>/dev/null)
        if [[ -n "$_cc" && -n "$_odir" && -d "./${_odir}" ]]; then
            ${_cc} aws delete \
                --name="${HCP_HOSTED_CLUSTER_NAME}" \
                --region="${region}" \
                --output-dir="./${_odir}" 2>/dev/null || \
                log_warn "ccoctl aws delete failed — OIDC provider may need manual cleanup"
        fi
    }
    trap _hosted_setup_cleanup EXIT

    oc cluster-info >/dev/null 2>&1 || { log_error "Management cluster not accessible"; exit 1; }
    oc get crd hostedclusters.hypershift.openshift.io >/dev/null 2>&1 || \
        { log_error "HyperShift CRDs not found"; exit 1; }
    if [[ -z "$dns_zone_id" ]]; then
        log_info "HCP_AWS_DNS_ZONE_ID not set. Looking for existing Route53 zone for ${base_domain}..."
        dns_zone_id=$(aws route53 list-hosted-zones-by-name \
            --query "HostedZones[?Name=='${base_domain}.'].Id" \
            --output text 2>/dev/null | cut -d'/' -f3)
        [[ -z "$dns_zone_id" ]] && { log_error "HCP_AWS_DNS_ZONE_ID is required — no Route53 zone found for ${base_domain}"; exit 1; }
        log_success "Found Route53 hosted zone: ${base_domain} (ID: ${dns_zone_id})"
        export HCP_AWS_DNS_ZONE_ID="${dns_zone_id}"
    fi
    if [[ -z "$s3_bucket" ]]; then
        s3_bucket="${HCP_HOSTED_CLUSTER_NAME}-oidc"
        log_info "HCP_AWS_OIDC_S3_BUCKET not set, defaulting to ${s3_bucket}"
    fi

    local CCOCTL
    CCOCTL=$(command -v ccoctl-native 2>/dev/null || command -v ccoctl 2>/dev/null)
    local HYPERSHIFT
    HYPERSHIFT=$(command -v hypershift 2>/dev/null)
    [[ -z "$CCOCTL" ]] && { log_error "ccoctl-native or ccoctl not found"; exit 1; }
    [[ -z "$HYPERSHIFT" ]] && { log_error "hypershift CLI not found"; exit 1; }

    echo ""
    echo "=== Setting Up Hosted Cluster: ${HCP_HOSTED_CLUSTER_NAME} ==="
    echo "Region: ${region}"
    echo ""

    echo "=== Step 1: Creating OIDC Key Pair ==="
    mkdir -p "${oidc_dir}"
    ${CCOCTL} aws create-key-pair --output-dir "./${oidc_dir}" || {
        log_error "ccoctl aws create-key-pair failed"
        exit 1
    }
    log_success "OIDC key pair created"
    echo ""

    echo "=== Step 2: Creating OIDC Provider (S3-backed) ==="
    ${CCOCTL} aws create-identity-provider \
        --name="${HCP_HOSTED_CLUSTER_NAME}" \
        --region="${region}" \
        --public-key-file="./${oidc_dir}/serviceaccount-signer.public" \
        --output-dir="./${oidc_dir}" || {
        log_error "ccoctl aws create-identity-provider failed"
        exit 1
    }
    log_success "OIDC provider created"
    echo ""

    echo "=== Step 3: Extracting OIDC Issuer URL ==="
    local OIDC_ISSUER_URL
    OIDC_ISSUER_URL=$(grep "serviceAccountIssuer" \
        "./${oidc_dir}/manifests/cluster-authentication-02-config.yaml" 2>/dev/null | \
        awk '{print $2}')
    if [[ -z "$OIDC_ISSUER_URL" ]]; then
        OIDC_ISSUER_URL="https://${s3_bucket}.s3.${region}.amazonaws.com/${HCP_HOSTED_CLUSTER_NAME}"
        log_debug "OIDC issuer URL not found in manifest, using S3 fallback: ${OIDC_ISSUER_URL}"
    fi
    log_success "OIDC issuer: ${OIDC_ISSUER_URL}"
    local ACCOUNT_ID IDP_ARN
    ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text 2>/dev/null)
    IDP_ARN="arn:aws:iam::${ACCOUNT_ID}:oidc-provider/${OIDC_ISSUER_URL#https://}"
    log_debug "Identity provider ARN: ${IDP_ARN}"
    echo ""

    echo "=== Step 4: Creating IAM Roles and Worker Instance Profile ==="
    ${HYPERSHIFT} create iam aws \
        --infra-id="${HCP_HOSTED_CLUSTER_NAME}" \
        --oidc-issuer-url="${OIDC_ISSUER_URL}" \
        --oidc-storage-provider-s3-bucket-name="${s3_bucket}" \
        --oidc-storage-provider-s3-region="${region}" \
        --region="${region}" \
        --public-zone-id="${dns_zone_id}" \
        --private-zone-id="${dns_zone_id}" \
        --local-zone-id="${dns_zone_id}" \
        --sts-creds="${HCP_HOSTED_STS_CREDS_FILE}" \
        --role-arn="${HCP_HOSTED_SETUP_ROLE_ARN}" \
        --output-file=./iam.json
    log_success "IAM roles and worker instance profile created"
    echo ""

    echo "=== Step 5: Creating Hosted Cluster ==="
    oc create namespace "${HCP_NAMESPACE}" 2>/dev/null || true

    # Optional image content sources (mirroring) for the hosted cluster: the
    # only mechanism by which a hosted cluster honours image mirroring (IDMS
    # applied to a hosted cluster is not reconciled by MCO). Passed straight
    # through to hypershift create cluster aws.
    local -a _ics_args=()
    if [[ -n "${HCP_IMAGE_CONTENT_SOURCES:-}" ]]; then
        [[ -f "${HCP_IMAGE_CONTENT_SOURCES}" ]] || { log_error "HCP_IMAGE_CONTENT_SOURCES does not exist: ${HCP_IMAGE_CONTENT_SOURCES}"; exit 1; }
        _ics_args+=(--image-content-sources "${HCP_IMAGE_CONTENT_SOURCES}")
        log_info "Image content sources: ${HCP_IMAGE_CONTENT_SOURCES}"
    fi

    ${HYPERSHIFT} create cluster aws \
        --name="${HCP_HOSTED_CLUSTER_NAME}" \
        --namespace="${HCP_NAMESPACE}" \
        --base-domain="${base_domain}" \
        --pull-secret="${HCP_EFFECTIVE_PULL_SECRET}" \
        --release-image="${HCP_RELEASE_IMAGE}" \
        --region="${region}" \
        --node-pool-replicas="${HCP_NODE_COUNT}" \
        --instance-type="${hosted_worker_type}" \
        --infra-id="${HCP_HOSTED_CLUSTER_NAME}" \
        --iam-json=./iam.json \
        --oidc-issuer-url="${OIDC_ISSUER_URL}" \
        --sts-creds="${HCP_HOSTED_STS_CREDS_FILE}" \
        --role-arn="${HCP_HOSTED_SETUP_ROLE_ARN}" \
        --sa-token-issuer-private-key-path="./${oidc_dir}/serviceaccount-signer.private" \
        "${_ics_args[@]+"${_ics_args[@]}"}"

    log_success "Hosted cluster created: ${HCP_HOSTED_CLUSTER_NAME}"
    echo "  Namespace: ${HCP_NAMESPACE}"
    echo ""

    if [[ "${HCP_NESTED_VIRT:-no}" == "yes" ]] && vm_type_supports_nested "$hosted_worker_type"; then
        log_info "Instance type ${hosted_worker_type} supports nested virt — patching NodePool cpuOptions..."
        local _np_name
        _np_name=$(KUBECONFIG="${HCP_MGMT_KUBECONFIG}" oc get nodepool -n "${HCP_NAMESPACE}" \
            -o jsonpath='{.items[?(@.spec.clusterName=="'"${HCP_HOSTED_CLUSTER_NAME}"'")].metadata.name}' 2>/dev/null)
        if [[ -n "$_np_name" ]]; then
            KUBECONFIG="${HCP_MGMT_KUBECONFIG}" oc patch nodepool "${_np_name}" -n "${HCP_NAMESPACE}" \
                --type=merge -p '{"spec":{"platform":{"aws":{"cpuOptions":{"nestedVirtualizationPolicy":"Enabled"}}}}}' 2>/dev/null \
                && log_info "NodePool ${_np_name}: cpuOptions patch applied (will verify after nodes are ready)" \
                || log_info "NodePool CRD does not support cpuOptions — will use AWS API workaround"
        fi
    fi

    echo "=== Step 6: Monitoring Hosted Cluster Creation ==="
    oc wait --for=condition=Available "hostedcluster/${HCP_HOSTED_CLUSTER_NAME}" \
        -n "${HCP_NAMESPACE}" --timeout=40m || {
        log_warn "HostedCluster not Available after 40 min"
    }

    if [[ "${HCP_NESTED_VIRT:-no}" == "yes" ]] && vm_type_supports_nested "$hosted_worker_type"; then
        cloud_ensure_nested_virt
    fi

    echo ""
    log_success "Setup complete!"
    echo "  Hosted cluster: ${HCP_HOSTED_CLUSTER_NAME}"
    echo "  OIDC issuer: ${OIDC_ISSUER_URL}"
    echo ""
    print_next_steps_hosted_setup
}

cloud_hosted_teardown() {
    export KUBECONFIG="${HCP_MGMT_KUBECONFIG}"

    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"
    local s3_bucket="${HCP_AWS_OIDC_S3_BUCKET:-}"

    if [[ -z "${region}" ]]; then
        log_error "Region not set — set HCP_AWS_REGION or HCP_LOCATION before teardown"
        exit 1
    fi

    echo ""
    echo "=== Tearing Down Hosted Cluster: ${HCP_HOSTED_CLUSTER_NAME} ==="
    echo ""

    read -p "Delete hosted cluster ${HCP_HOSTED_CLUSTER_NAME} and all AWS resources? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Teardown cancelled"
        exit 0
    fi

    oc delete hostedcluster "${HCP_HOSTED_CLUSTER_NAME}" \
        -n "${HCP_NAMESPACE}" --wait=false 2>/dev/null || true
    oc delete nodepool "${HCP_HOSTED_CLUSTER_NAME}" \
        -n "${HCP_NAMESPACE}" --wait=false 2>/dev/null || true

    echo "Waiting for HostedCluster deletion (up to 15 min)..."
    oc wait --for=delete "hostedcluster/${HCP_HOSTED_CLUSTER_NAME}" \
        -n "${HCP_NAMESPACE}" --timeout=15m 2>/dev/null || \
        log_warn "HostedCluster not deleted after 15 min — proceeding with OIDC/IAM cleanup"

    if [[ "${HCP_NO_TEARDOWN:-false}" == "true" ]]; then
        log_info "Skipping OIDC/IAM deletion (--no-teardown)"
        echo "  To clean up later:"
        echo "    ccoctl aws delete --name=${HCP_HOSTED_CLUSTER_NAME} --region=${region} --output-dir=${HCP_WORK_DIR}/oidc-${HCP_HOSTED_CLUSTER_NAME}"
        echo ""
        log_success "Teardown complete (cloud resources preserved)"
        return 0
    fi

    if [[ -n "$s3_bucket" ]]; then
        echo "Deleting OIDC S3 bucket..."
        aws s3 rb "s3://${s3_bucket}" --force --region "${region}" 2>/dev/null && \
            log_success "OIDC S3 bucket deleted: ${s3_bucket}" || \
            log_warn "Could not delete S3 bucket ${s3_bucket} — may already be deleted"
    fi

    local CCOCTL
    CCOCTL=$(command -v ccoctl-native 2>/dev/null || command -v ccoctl 2>/dev/null)
    if [[ -n "$CCOCTL" ]] && [[ -d "${HCP_WORK_DIR}/oidc-${HCP_HOSTED_CLUSTER_NAME}" ]]; then
        echo "Cleaning up IAM roles via ccoctl..."
        ${CCOCTL} aws delete \
            --name="${HCP_HOSTED_CLUSTER_NAME}" \
            --region="${region}" \
            --output-dir="${HCP_WORK_DIR}/oidc-${HCP_HOSTED_CLUSTER_NAME}" 2>/dev/null || \
            log_warn "ccoctl aws delete failed — IAM roles may need manual cleanup"
    else
        log_warn "ccoctl not found or oidc dir missing — IAM roles may need manual AWS console cleanup"
    fi

    echo ""
    log_success "Teardown complete"
    echo "  Check AWS console for any remaining IAM roles or OIDC providers named ${HCP_HOSTED_CLUSTER_NAME}"
}

cloud_cleanup_stale_install_resources() {
    local mgmt_name="$1"
    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"

    # Clean up stale Route53 records for this cluster left by a previous failed run
    local zone_id="${HCP_AWS_DNS_ZONE_ID:-}"
    if [[ -n "$zone_id" ]]; then
        local stale_records
        stale_records=$(aws route53 list-resource-record-sets \
            --hosted-zone-id "$zone_id" \
            --query "ResourceRecordSets[?Type!='NS' && Type!='SOA' && contains(Name, '${mgmt_name}.')]" \
            --output json 2>/dev/null) || stale_records="[]"
        local record_count
        record_count=$(echo "$stale_records" | jq 'length' 2>/dev/null || echo 0)
        if [[ "$record_count" -gt 0 ]]; then
            log_warn "Deleting ${record_count} stale Route53 record(s) for ${mgmt_name}..."
            local delete_batch
            delete_batch=$(echo "$stale_records" | jq '[.[] | {"Action": "DELETE", "ResourceRecordSet": .}]')
            aws route53 change-resource-record-sets \
                --hosted-zone-id "$zone_id" \
                --change-batch "{\"Changes\": ${delete_batch}}" >/dev/null 2>&1 || \
                log_warn "Some Route53 records could not be deleted — may need manual cleanup"
            log_success "Stale Route53 records deleted"
        fi
    fi

    local stale_stacks
    stale_stacks=$(aws cloudformation list-stacks \
        --region "${region}" \
        --stack-status-filter CREATE_FAILED ROLLBACK_COMPLETE DELETE_FAILED UPDATE_ROLLBACK_COMPLETE \
        --query "StackSummaries[?starts_with(StackName, '${mgmt_name}')].StackName" \
        --output text 2>/dev/null) || return 0

    [[ -z "$stale_stacks" ]] && return 0

    log_warn "Deleting stale CloudFormation stacks from failed install..."
    local -a stack_array
    readarray -t stack_array <<< "$stale_stacks"
    for stack in "${stack_array[@]}"; do
        [[ -z "$stack" ]] && continue
        aws cloudformation delete-stack --stack-name "$stack" --region "${region}" 2>/dev/null || true
        log_debug "Queued CF stack deletion: $stack"
    done
    log_warn "Waiting 60s for stack deletion to begin..."
    sleep 60
}

# Creates a dedicated IAM role for the cloud-api-adaptor (CAA) with IRSA trust
# tied to the hosted cluster's S3-backed OIDC provider. Used by cloud_setup_peer_pods()
# when no static credentials or pre-existing role ARN is available.
# Args: $1=hc_name, $2=region
# Prints the created role ARN on stdout; returns 1 on failure.
cloud_create_caa_iam_role() {
    local hc_name="$1"
    local region="$2"
    local role_name="${hc_name}-caa-role"
    local account_id caller_arn
    account_id=$(aws sts get-caller-identity --query Account --output text 2>/dev/null) || {
        log_error "Cannot determine AWS account ID"; return 1
    }
    caller_arn=$(aws sts get-caller-identity --query Arn --output text 2>/dev/null)
    # OIDC provider is the S3 bucket created during hosted setup.
    local oidc_provider="${hc_name}-oidc.s3.${region}.amazonaws.com"
    local oidc_provider_arn="arn:aws:iam::${account_id}:oidc-provider/${oidc_provider}"

    if ! aws iam get-open-id-connect-provider --open-id-connect-provider-arn "${oidc_provider_arn}" &>/dev/null; then
        log_error "OIDC provider ${oidc_provider_arn} not found — run hosted setup first."
        return 1
    fi

    # OSC operator creates CAA DaemonSet with this service account.
    local caa_ns="openshift-sandboxed-containers-operator"
    local caa_sa="cloud-api-adaptor"

    local trust_policy
    trust_policy=$(printf '{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {"Federated": "%s"},
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "%s:sub": "system:serviceaccount:%s:%s",
          "%s:aud": "openshift"
        }
      }
    },
    {
      "Effect": "Allow",
      "Principal": {"AWS": "%s"},
      "Action": "sts:AssumeRole"
    }
  ]
}' "${oidc_provider_arn}" "${oidc_provider}" "${caa_ns}" "${caa_sa}" "${oidc_provider}" "${caller_arn}")

    if aws iam get-role --role-name "${role_name}" &>/dev/null; then
        aws iam update-assume-role-policy --role-name "${role_name}" \
            --policy-document "${trust_policy}" &>/dev/null
        aws iam update-role --role-name "${role_name}" \
            --max-session-duration 43200 &>/dev/null
    else
        aws iam create-role --role-name "${role_name}" \
            --assume-role-policy-document "${trust_policy}" \
            --max-session-duration 43200 \
            --description "Cloud API Adaptor IRSA role for peer-pods on ${hc_name}" \
            --tags "Key=cluster,Value=${hc_name}" &>/dev/null || return 1
    fi

    aws iam put-role-policy --role-name "${role_name}" \
        --policy-name "${role_name}-policy" \
        --policy-document '{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "ec2:RunInstances",
      "ec2:TerminateInstances",
      "ec2:CreateTags",
      "ec2:Describe*",
      "ec2:CreateNetworkInterface",
      "ec2:DeleteNetworkInterface",
      "ec2:AttachNetworkInterface",
      "ec2:DetachNetworkInterface",
      "ec2:ModifyInstanceAttribute"
    ],
    "Resource": "*"
  }]
}' &>/dev/null || return 1

    aws iam get-role --role-name "${role_name}" --query 'Role.Arn' --output text
}

# Creates peer-pods-cm and peer-pods-secret in the OSC namespace for AWS peer pods.
# Called from osc_install() before KataConfig is applied when enablePeerPods=true.
# Network config is derived from the HostedCluster resource via the management cluster.
# AWS credentials for the cloud-api-adaptor (kata-remote handler) come from the
# environment (AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY), or from
# HCP_PEER_PODS_AWS_ACCESS_KEY_ID / HCP_PEER_PODS_AWS_SECRET_ACCESS_KEY overrides.
# When neither is set, a dedicated IAM role is auto-created (cloud_create_caa_iam_role).
#
# Relevant env vars (all optional overrides):
#   HCP_PEER_PODS_VPC_ID            — VPC ID (auto-derived from HostedCluster if unset)
#   HCP_PEER_PODS_SUBNET_ID         — Subnet ID (auto-derived from HostedCluster if unset)
#   HCP_PEER_PODS_SG_IDS            — Security group IDs (auto-derived from HostedCluster if unset)
#   HCP_PEER_PODS_INSTANCE_TYPE     — Pod VM instance type (default: t3.medium)
#   HCP_PEER_PODS_IMAGE_ID          — Pod VM AMI ID; script searches account if unset, fails if not found
#   HCP_PEER_PODS_AWS_ACCESS_KEY_ID — AWS access key for peer pod VMs (falls back to AWS_ACCESS_KEY_ID)
#   HCP_PEER_PODS_AWS_SECRET_ACCESS_KEY — AWS secret key (falls back to AWS_SECRET_ACCESS_KEY)
#   HCP_PEER_PODS_AWS_SESSION_TOKEN — AWS session token (falls back to AWS_SESSION_TOKEN)
#   HCP_PEER_PODS_AWS_ROLE_ARN      — IAM role ARN for the CAA to assume (auto-created if absent)
cloud_setup_peer_pods() {
    local osc_ns="openshift-sandboxed-containers-operator"
    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"
    local hc_name="${HCP_HOSTED_CLUSTER_NAME}"
    local hc_ns="${HCP_NAMESPACE}"

    echo ""
    echo "=== Configuring Peer Pods (AWS) ==="

    # Derive network config — try AWS CLI tags first (works even when mgmt cluster is unreachable),
    # then fall back to the HostedCluster resource via the management cluster kubeconfig.
    local vpc_id subnet_id sg_id
    local cluster_tag="kubernetes.io/cluster/${hc_name}"

    # Region may not be set for standalone osc-install runs; derive from a node label if needed.
    if [[ -z "$region" ]]; then
        region=$(oc get nodes -o jsonpath='{.items[0].metadata.labels.topology\.kubernetes\.io/region}' \
            2>/dev/null || true)
        [[ -n "$region" ]] && log_info "Derived AWS region from node labels: ${region}"
    fi
    [[ -z "$region" ]] && log_warn "AWS region unknown — AWS CLI lookups may fail; set HCP_AWS_REGION"

    # Primary: AWS CLI tag lookup (fast, no management cluster dependency)
    vpc_id=$(aws ec2 describe-vpcs \
        --filters "Name=tag:${cluster_tag},Values=owned" \
        ${region:+--region "${region}"} \
        --query 'Vpcs[0].VpcId' --output text 2>/dev/null | grep -v None || true)
    subnet_id=$(aws ec2 describe-subnets \
        --filters "Name=tag:${cluster_tag},Values=owned" \
        ${region:+--region "${region}"} \
        --query 'Subnets[0].SubnetId' --output text 2>/dev/null | grep -v None || true)
    sg_id=$(aws ec2 describe-security-groups \
        --filters "Name=tag:Name,Values=${hc_name}-default-sg" \
        ${region:+--region "${region}"} \
        --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null | grep -v None || true)

    # Secondary fallback: management cluster HostedCluster resource
    if [[ -z "$vpc_id" || -z "$subnet_id" ]]; then
        log_info "AWS CLI lookup incomplete — trying management cluster..."
        vpc_id=$(KUBECONFIG="${HCP_MGMT_KUBECONFIG}" oc get hostedcluster "${hc_name}" \
            -n "${hc_ns}" \
            -o jsonpath='{.spec.platform.aws.cloudProviderConfig.vpc}' 2>/dev/null || true)
        subnet_id=$(KUBECONFIG="${HCP_MGMT_KUBECONFIG}" oc get hostedcluster "${hc_name}" \
            -n "${hc_ns}" \
            -o jsonpath='{.spec.platform.aws.cloudProviderConfig.subnet.id}' 2>/dev/null || true)
        [[ -z "$sg_id" ]] && sg_id=$(KUBECONFIG="${HCP_MGMT_KUBECONFIG}" oc get hostedcluster "${hc_name}" \
            -n "${hc_ns}" \
            -o jsonpath='{.status.platform.aws.defaultWorkerSecurityGroupID}' 2>/dev/null || true)
    fi

    # Explicit env var overrides take final precedence
    [[ -n "${HCP_PEER_PODS_VPC_ID:-}" ]]    && vpc_id="${HCP_PEER_PODS_VPC_ID}"
    [[ -n "${HCP_PEER_PODS_SUBNET_ID:-}" ]] && subnet_id="${HCP_PEER_PODS_SUBNET_ID}"
    [[ -n "${HCP_PEER_PODS_SG_IDS:-}" ]]    && sg_id="${HCP_PEER_PODS_SG_IDS}"

    if [[ -z "$vpc_id" || -z "$subnet_id" ]]; then
        log_error "Cannot derive VPC/subnet for peer pods configuration."
        log_error "Set HCP_PEER_PODS_VPC_ID and HCP_PEER_PODS_SUBNET_ID, or ensure AWS credentials are active."
        return 1
    fi

    local instance_type="${HCP_PEER_PODS_INSTANCE_TYPE:-t3.medium}"
    local image_id="${HCP_PEER_PODS_IMAGE_ID:-}"

    if [[ -n "$image_id" ]]; then
        # Validate explicitly-set AMI exists in the target region.
        local ami_state
        ami_state=$(aws ec2 describe-images ${region:+--region "${region}"} \
            --image-ids "${image_id}" \
            --query 'Images[0].State' --output text 2>/dev/null || true)
        if [[ "$ami_state" != "available" ]]; then
            log_error "HCP_PEER_PODS_IMAGE_ID=${image_id} does not exist or is not available in region ${region}."
            log_error "Unset HCP_PEER_PODS_IMAGE_ID to let the script auto-discover a peer pod AMI."
            return 1
        fi
        log_info "Peer pod AMI validated: ${image_id}"
    else
        # If no AMI override provided, try to find an existing peer pod VM AMI in this account.
        # The aws-podvm-ami skill tags AMIs it creates with podvm=true; also check common name patterns.
        log_info "HCP_PEER_PODS_IMAGE_ID not set — searching for existing peer pod AMI in ${region:-us-east-1}..."
        image_id=$(aws ec2 describe-images \
            --owners self \
            --filters "Name=tag:podvm,Values=true" \
            ${region:+--region "${region}"} \
            --query 'reverse(sort_by(Images,&CreationDate))[0].ImageId' \
            --output text 2>/dev/null | grep -v None || true)
        if [[ -z "$image_id" ]]; then
            # Fallback: search by name pattern (podvm-* or kata-podvm-*)
            image_id=$(aws ec2 describe-images \
                --owners self \
                --filters "Name=name,Values=podvm-*" \
                ${region:+--region "${region}"} \
                --query 'reverse(sort_by(Images,&CreationDate))[0].ImageId' \
                --output text 2>/dev/null | grep -v None || true)
        fi
        if [[ -n "$image_id" ]]; then
            log_info "Found existing peer pod AMI: ${image_id}"
        else
            log_warn "No peer pod AMI found in region ${region:-us-east-1} — searching other regions..."
            local search_regions=(us-east-1 us-east-2 us-west-1 us-west-2 eu-west-1 eu-central-1 ap-southeast-1)
            local found_region="" found_ami="" found_name=""
            for sr in "${search_regions[@]}"; do
                [[ "$sr" == "${region}" ]] && continue
                found_ami=$(aws ec2 describe-images \
                    --owners self --region "${sr}" \
                    --filters "Name=tag:podvm,Values=true" \
                    --query 'reverse(sort_by(Images,&CreationDate))[0].[ImageId,Name]' \
                    --output text 2>/dev/null | grep -v None || true)
                if [[ -n "$found_ami" ]]; then
                    found_region="$sr"
                    found_name=$(echo "$found_ami" | awk '{print $2}')
                    found_ami=$(echo "$found_ami" | awk '{print $1}')
                    break
                fi
            done
            if [[ -n "$found_ami" ]]; then
                log_info "Found peer pod AMI ${found_ami} (${found_name}) in ${found_region}."
                log_info "Copy it to ${region} with:"
                echo ""
                echo "  aws ec2 copy-image --source-region ${found_region} --source-image-id ${found_ami} \\"
                echo "    --region ${region} --name \"${found_name}\" \\"
                echo "    --description \"Peer pod VM image copied from ${found_region}\""
                echo "  # Then tag it:  aws ec2 create-tags --region ${region} --resources <new-ami-id> --tags Key=podvm,Value=true"
                echo ""
                log_error "No peer pod AMI available in ${region}. Copy the AMI above and retry."
            else
                log_error "No peer pod AMI found in any region."
                log_error "The operator requires a pod VM AMI to deploy cloud-api-adaptor."
                log_error "Options:"
                log_error "  1. Set HCP_PEER_PODS_IMAGE_ID=<ami-id> to specify an existing AMI"
                log_error "  2. Re-run osc install with --build-podvm-image to build one automatically"
                log_error "  3. Run 'aws-build-podvm-ami upload' to build and upload one"
            fi
            return 1
        fi
    fi

    log_info "Peer pods network: VPC=${vpc_id}, subnet=${subnet_id}, SG=${sg_id:-(auto)}"
    log_info "Peer pods VM: type=${instance_type}${image_id:+, image=${image_id}}"

    if [[ -n "$sg_id" ]]; then
        local _agent_port=15150
        local _has_rule
        _has_rule=$(aws ec2 describe-security-group-rules \
            --filters "Name=group-id,Values=${sg_id}" \
            ${region:+--region "${region}"} \
            --query "SecurityGroupRules[?FromPort==\`${_agent_port}\` && ToPort==\`${_agent_port}\` && IpProtocol==\`tcp\`]" \
            --output text 2>/dev/null || true)
        if [[ -z "$_has_rule" || "$_has_rule" == "None" ]]; then
            log_info "Adding SG rule: TCP ${_agent_port} (kata agent) from ${sg_id}"
            aws ec2 authorize-security-group-ingress \
                --group-id "${sg_id}" \
                ${region:+--region "${region}"} \
                --protocol tcp --port "${_agent_port}" \
                --source-group "${sg_id}" \
                --tag-specifications "ResourceType=security-group-rule,Tags=[{Key=Description,Value=kata-agent-proxy}]" \
                >/dev/null 2>&1 || log_warn "Failed to add SG rule for port ${_agent_port} — peer pod agents may be unreachable"
        else
            log_info "SG rule for TCP ${_agent_port} (kata agent) already exists"
        fi
    fi

    # Build ConfigMap via temp file to handle optional fields cleanly
    local _cm_tmp
    _cm_tmp=$(mktemp)
    {
        echo "apiVersion: v1"
        echo "kind: ConfigMap"
        echo "metadata:"
        echo "  name: peer-pods-cm"
        echo "  namespace: ${osc_ns}"
        echo "data:"
        echo "  CLOUD_PROVIDER: \"aws\""
        echo "  VXLAN_PORT: \"9000\""
        echo "  AWS_REGION: \"${region}\""
        echo "  AWS_VPC_ID: \"${vpc_id}\""
        echo "  AWS_SUBNET_ID: \"${subnet_id}\""
        echo "  PODVM_INSTANCE_TYPE: \"${instance_type}\""
        echo "  DISABLECVM: \"true\""
        [[ -n "$sg_id" ]]    && echo "  AWS_SG_IDS: \"${sg_id}\""
        [[ -n "$image_id" ]] && echo "  PODVM_AMI_ID: \"${image_id}\""
    } > "${_cm_tmp}"
    # peer-pods-secret: AWS credentials for the cloud-api-adaptor running on worker nodes.
    # Applied BEFORE peer-pods-cm because the operator watches the configmap and
    # triggers a reconcile — the secret must already be correct when that happens.
    local access_key="${HCP_PEER_PODS_AWS_ACCESS_KEY_ID:-${AWS_ACCESS_KEY_ID:-}}"
    local secret_key="${HCP_PEER_PODS_AWS_SECRET_ACCESS_KEY:-${AWS_SECRET_ACCESS_KEY:-}}"
    local session_token="${HCP_PEER_PODS_AWS_SESSION_TOKEN:-${AWS_SESSION_TOKEN:-}}"
    local role_arn="${HCP_PEER_PODS_AWS_ROLE_ARN:-}"

    if [[ -z "$role_arn" && -z "$access_key" ]]; then
        log_info "No credentials or role ARN in environment — auto-creating CAA IAM role via IRSA..."
        role_arn=$(cloud_create_caa_iam_role "${hc_name}" "${region}") || {
            log_error "Failed to create CAA IAM role."
            log_error "Set HCP_PEER_PODS_AWS_ROLE_ARN (preferred) or HCP_PEER_PODS_AWS_ACCESS_KEY_ID/SECRET."
            return 1
        }
        log_success "Created CAA IAM role: ${role_arn}"
    fi
    [[ -n "$role_arn" ]] && log_info "Peer pods will use IAM role: ${role_arn}"

    # When we have a role ARN but no static credentials, assume the role to get
    # temporary credentials.  The CAA DaemonSet does not mount a projected SA
    # token volume, so IRSA (AWS_WEB_IDENTITY_TOKEN_FILE) cannot work — the
    # token file simply does not exist inside the pod.  Static STS credentials
    # from assume-role are the only reliable path until the operator adds
    # projected token support.
    if [[ -n "$role_arn" && -z "$access_key" ]]; then
        log_info "Assuming CAA role to generate static credentials for peer-pods-secret..."
        local caa_creds assume_dur=43200 rc=0
        for _attempt in $(seq 1 6); do
            rc=0
            caa_creds=$(aws sts assume-role \
                --role-arn "${role_arn}" \
                --role-session-name "caa-peer-pods" \
                --duration-seconds "${assume_dur}" \
                --query Credentials --output json 2>&1) || rc=$?
            [[ $rc -eq 0 ]] && break
            if echo "$caa_creds" | grep -q "role chaining"; then
                assume_dur=3600
                log_warn "SSO/role-chained session — credentials capped at 1h; refresh SSO before long test runs"
            elif echo "$caa_creds" | grep -q "AccessDenied"; then
                log_info "IAM role not ready yet — waiting 10s (attempt ${_attempt}/6)..."
                sleep 10
                continue
            else
                break
            fi
        done
        if [[ $rc -ne 0 ]]; then
            log_error "Failed to assume CAA role ${role_arn}: ${caa_creds}"
            return 1
        fi
        access_key=$(echo "$caa_creds" | jq -r .AccessKeyId)
        secret_key=$(echo "$caa_creds" | jq -r .SecretAccessKey)
        session_token=$(echo "$caa_creds" | jq -r .SessionToken)
        unset caa_creds
        local dur_label=$(( assume_dur / 3600 ))h
        log_success "Generated temporary credentials (${dur_label}) for CAA"
    fi

    local _secret_tmp
    _secret_tmp=$(mktemp)
    (
        umask 077
        local -a secret_args=(
            --from-literal=AWS_ACCESS_KEY_ID="${access_key}"
            --from-literal=AWS_SECRET_ACCESS_KEY="${secret_key}"
        )
        [[ -n "$session_token" ]] && secret_args+=(--from-literal=AWS_SESSION_TOKEN="${session_token}")
        oc create secret generic peer-pods-secret \
            -n "${osc_ns}" "${secret_args[@]}" \
            --dry-run=client -o yaml > "${_secret_tmp}" 2>/dev/null
    )
    oc apply -f "${_secret_tmp}"
    rm -f "${_secret_tmp}"
    log_success "peer-pods-secret configured"

    # Apply configmap after secret so the operator reconcile triggered by the
    # configmap watch already sees the correct credentials.
    oc apply -f "${_cm_tmp}"
    rm -f "${_cm_tmp}"
    log_success "peer-pods-cm configured"
}

# Uploads a raw peer pod VM disk image to S3, imports it as an EC2 snapshot, and registers as AMI.
# Args: $1=raw_image_path, $2=caa_root (used for git SHA in AMI name)
# Exports HCP_PEER_PODS_IMAGE_ID on success.
#
# Env vars:
#   HCP_PODVM_BUCKET    — S3 bucket (default: ${HCP_USER}-osc-podvm)
#   HCP_AWS_REGION      — AWS region (falls back to HCP_LOCATION)
#   HCP_PODVM_AMI_NAME  — Custom AMI name (default: podvm-<distro>-debug-<git-sha>)
cloud_podvm_upload() {
    local raw_image="$1"
    local git_sha="${2:-unknown}"
    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"
    # Resolve to the effective configured region so we never silently use the wrong one.
    region="${region:-$(aws configure get region 2>/dev/null)}"
    region="${region:-us-east-1}"
    local bucket="${HCP_PODVM_BUCKET:-${HCP_USER}-osc-podvm}"
    local distro="${HCP_PODVM_DISTRO:-ubuntu}"
    local ami_name="${HCP_PODVM_AMI_NAME:-podvm-${distro}-debug-${git_sha}}"
    local image_filename
    image_filename="$(basename "${raw_image}")"

    aws sts get-caller-identity &>/dev/null || {
        log_error "AWS credentials not configured or expired"
        return 1
    }

    log_info "Bucket: ${bucket}, Region: ${region}, AMI name: ${ami_name}"

    local tmpdir
    tmpdir=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '${tmpdir}'" RETURN

    # Create S3 bucket (idempotent)
    if aws s3api head-bucket --bucket "${bucket}" --region "${region}" 2>/dev/null; then
        log_info "S3 bucket ${bucket} already exists"
    else
        log_info "Creating S3 bucket ${bucket}..."
        if [[ "${region}" == "us-east-1" ]]; then
            aws s3api create-bucket --bucket "${bucket}" --region "${region}"
        else
            aws s3api create-bucket --bucket "${bucket}" \
                --region "${region}" \
                --create-bucket-configuration LocationConstraint="${region}"
        fi
    fi

    # Set up vmimport IAM role (idempotent — always refresh inline policy to include this bucket)
    local trust_policy="${tmpdir}/trust-policy.json"
    local role_policy="${tmpdir}/role-policy.json"
    if ! aws iam get-role --role-name vmimport &>/dev/null; then
        log_info "Creating vmimport IAM role..."
        cat > "${trust_policy}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Service": "vmie.amazonaws.com" },
    "Action": "sts:AssumeRole",
    "Condition": { "StringEquals": { "sts:Externalid": "vmimport" } }
  }]
}
EOF
        aws iam create-role --role-name vmimport \
            --assume-role-policy-document "file://${trust_policy}" &>/dev/null
    fi
    cat > "${role_policy}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetBucketLocation","s3:GetObject","s3:ListBucket"],
      "Resource": ["arn:aws:s3:::${bucket}","arn:aws:s3:::${bucket}/*"]
    },
    {
      "Effect": "Allow",
      "Action": ["ec2:ModifySnapshotAttribute","ec2:CopySnapshot","ec2:RegisterImage","ec2:Describe*"],
      "Resource": "*"
    }
  ]
}
EOF
    aws iam put-role-policy --role-name vmimport --policy-name vmimport \
        --policy-document "file://${role_policy}" &>/dev/null

    local bucket_policy="${tmpdir}/bucket-policy.json"
    cat > "${bucket_policy}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Service": "vmie.amazonaws.com" },
    "Action": ["s3:GetBucketLocation","s3:GetObject","s3:ListBucket"],
    "Resource": ["arn:aws:s3:::${bucket}","arn:aws:s3:::${bucket}/*"]
  }]
}
EOF
    aws s3api put-bucket-policy --bucket "${bucket}" \
        --policy "file://${bucket_policy}" ${region:+--region "${region}"} &>/dev/null

    # Upload raw image to S3
    log_info "Uploading ${image_filename} to s3://${bucket}/..."
    aws s3 cp "${raw_image}" "s3://${bucket}/${image_filename}" \
        ${region:+--region "${region}"} --no-progress
    log_success "Upload complete"

    # Import as EC2 snapshot
    log_info "Importing snapshot (this takes ~15 minutes)..."
    local import_json="${tmpdir}/import.json"
    cat > "${import_json}" <<EOF
{
  "Description": "Peer pod VM image (${distro}, TEE=${HCP_PODVM_TEE_PLATFORM:-none})",
  "Format": "RAW",
  "UserBucket": { "S3Bucket": "${bucket}", "S3Key": "${image_filename}" }
}
EOF
    local import_task_id
    import_task_id=$(aws ec2 import-snapshot \
        --disk-container "file://${import_json}" \
        ${region:+--region "${region}"} \
        --output text --query 'ImportTaskId')

    [[ -n "$import_task_id" ]] || { log_error "Failed to start import task"; return 1; }
    log_info "Import task: ${import_task_id}"

    local iteration=0
    local status msg task_json
    while (( iteration < 120 )); do
        task_json=$(aws ec2 describe-import-snapshot-tasks \
            --import-task-ids "${import_task_id}" \
            ${region:+--region "${region}"} --output json)
        status=$(python3 -c \
            "import sys,json; t=json.loads(sys.stdin.read())['ImportSnapshotTasks'][0]['SnapshotTaskDetail']; print(t['Status'])" \
            <<< "$task_json" 2>/dev/null || \
            aws ec2 describe-import-snapshot-tasks \
                --import-task-ids "${import_task_id}" \
                ${region:+--region "${region}"} \
                --output text --query 'ImportSnapshotTasks[0].SnapshotTaskDetail.Status')
        msg=$(python3 -c \
            "import sys,json; t=json.loads(sys.stdin.read())['ImportSnapshotTasks'][0]['SnapshotTaskDetail']; print(t.get('StatusMessage',''))" \
            <<< "$task_json" 2>/dev/null || true)
        echo "  Import: ${status}${msg:+ / ${msg}} (${iteration}/120)"
        [[ "$status" == "completed" ]] && break
        [[ "$status" != "active" && "$status" != "pending" ]] && {
            log_error "Snapshot import failed: ${status}"
            return 1
        }
        (( iteration++ ))
        sleep 15
    done

    local snapshot_id
    snapshot_id=$(aws ec2 describe-import-snapshot-tasks \
        --import-task-ids "${import_task_id}" \
        ${region:+--region "${region}"} \
        --output text \
        --query 'ImportSnapshotTasks[0].SnapshotTaskDetail.SnapshotId')

    [[ -n "$snapshot_id" && "$snapshot_id" != "None" ]] || {
        log_error "Could not retrieve snapshot ID from completed import"
        return 1
    }
    log_info "Snapshot: ${snapshot_id} — waiting for it to be ready..."
    aws ec2 wait snapshot-completed --snapshot-ids "${snapshot_id}" \
        ${region:+--region "${region}"}

    # Register AMI from snapshot
    log_info "Registering AMI..."
    local register_json="${tmpdir}/register.json"
    cat > "${register_json}" <<EOF
{
  "Architecture": "x86_64",
  "BlockDeviceMappings": [{
    "DeviceName": "/dev/xvda",
    "Ebs": { "DeleteOnTermination": true, "SnapshotId": "${snapshot_id}" }
  }],
  "Description": "Peer pod VM image (${distro}, TEE=${HCP_PODVM_TEE_PLATFORM:-none})",
  "RootDeviceName": "/dev/xvda",
  "VirtualizationType": "hvm",
  "EnaSupport": true,
  "BootMode": "uefi"
}
EOF
    local ami_id
    ami_id=$(aws ec2 register-image \
        --name "${ami_name}" \
        --cli-input-json "file://${register_json}" \
        ${region:+--region "${region}"} \
        --output text --query 'ImageId')

    [[ -n "$ami_id" && "$ami_id" != "None" ]] || { log_error "AMI registration failed"; return 1; }

    aws ec2 create-tags --resources "${ami_id}" \
        ${region:+--region "${region}"} \
        --tags \
            Key=Name,Value="${ami_name}" \
            Key=podvm,Value=true \
            Key=TEE_PLATFORM,Value="${HCP_PODVM_TEE_PLATFORM:-none}" \
            Key=Distro,Value="${distro}" \
            Key=GitCommit,Value="${git_sha}" \
            Key=BuildDate,Value="$(date -u +%Y-%m-%d)"

    export HCP_PEER_PODS_IMAGE_ID="${ami_id}"
    log_success "Peer pod AMI registered: ${ami_id}"
    echo ""
    echo "To use this image in your next osc install:"
    echo "  export HCP_PEER_PODS_IMAGE_ID=${ami_id}"
    echo ""
    echo "Add to your env file to make it permanent:"
    echo "  echo \"export HCP_PEER_PODS_IMAGE_ID=${ami_id}\" >> env/hypershift-cluster-setup-environment.sh"
}

# Lists peer pod VM AMIs in the account tagged podvm=true.
# Searches one region (HCP_AWS_REGION > HCP_LOCATION > aws-cli configured default).
# To find an AMI in another region, set HCP_AWS_REGION=<region> and re-run.
cloud_podvm_list() {
    local region="${HCP_AWS_REGION:-${HCP_LOCATION:-}}"
    # Resolve the effective region so we can show it even when not explicitly set.
    local effective_region
    effective_region="${region:-$(aws configure get region 2>/dev/null || true)}"
    effective_region="${effective_region:-us-east-1}"

    local account_id
    account_id=$(aws sts get-caller-identity --query Account --output text 2>/dev/null) || {
        log_error "AWS session expired or credentials not configured — run: aws login --profile redhat"
        return 1
    }
    log_info "Searching account ${account_id} in region ${effective_region} (tag podvm=true)..."

    local result
    result=$(aws ec2 describe-images \
        --owners self \
        --filters "Name=tag:podvm,Values=true" \
        --region "${effective_region}" \
        --query 'sort_by(Images,&CreationDate)[*].{ID:ImageId,Name:Name,Created:CreationDate,TEE:Tags[?Key==`TEE_PLATFORM`]|[0].Value,State:State}' \
        --output table 2>/dev/null) || result=""
    if [[ -z "$result" ]] || echo "$result" | grep -q "^$"; then
        log_warn "No peer pod AMIs found in account (region: ${effective_region})"
        log_warn "If the AMI is in another region, set HCP_AWS_REGION=<region> and re-run."
    else
        echo "$result"
    fi
}
