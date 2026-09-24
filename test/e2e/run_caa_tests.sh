#!/bin/bash
# Run upstream cloud-api-adaptor (CAA) e2e tests
# Requires: go, git, kubectl, go-junit-report, base64
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS_DIR="${RESULTS_DIR:-${SCRIPT_DIR}/results}"
RESULTS_DIR="${RESULTS_DIR}/$(date +"%Y-%m-%d_%H%M%S")"

# OSC runs the cloud-api-adaptor daemonset in its operator namespace with the pod
# label name=osc-caa-ds, whereas the framework looks for app=cloud-api-adaptor in
# confidential-containers-system. The pod-VM assessments read the CAA pod logs,
# so we redirect the framework's namespace (TEST_CAA_NAMESPACE) and add the label
# it expects to the OSC pods.
CAA_DS_SELECTOR="${CAA_DS_SELECTOR:-name=osc-caa-ds}"

usage() {
    cat <<EOF
Usage: $(basename "$0") -p PROVIDER [options]

Run cloud-api-adaptor (CAA) e2e tests against a cluster that already has OSC
(peer-pods/CAA) installed. Provisioning, CAA install and teardown are disabled.

Requires: go, git, kubectl, go-junit-report, base64

Options:
  -p, --provider PROVIDER   Cloud provider: azure or aws
  -t, --test PROFILE        Test profile or a raw '-run' regex (default: full)
                            Profiles: sanity, full, coco
  --peerpods-namespace NS   Namespace holding peer-pods-cm/peer-pods-secret
                            (default: openshift-sandboxed-containers-operator)
  --provision-file PATH     Use this properties file as-is instead of building
                            one from peer-pods-cm/peer-pods-secret
  --tests-repo URL|DIR      CAA repo URL or local directory
                            (default: https://github.com/openshift/cloud-api-adaptor)
  --tests-repo-ref REF      Git ref to checkout (default: osc-release)
  --preserve-tests-repo     Do not delete the cloned test repo after execution
  --timeout DURATION        go test -timeout value (default: 90m)
  -h, --help                Show this help
EOF
    exit "${1:-1}"
}

PROVIDER=""
PROFILE="full"
PEERPODS_NAMESPACE="openshift-sandboxed-containers-operator"
PROVISION_FILE=""
TESTS_REPO="https://github.com/openshift/cloud-api-adaptor"
TESTS_REPO_REF="osc-release"
PRESERVE_TESTS_REPO=false
TIMEOUT="90m"

while [ $# -gt 0 ]; do
    case "$1" in
        -p|--provider) PROVIDER="$2"; shift 2;;
        -t|--test) PROFILE="$2"; shift 2;;
        --peerpods-namespace) PEERPODS_NAMESPACE="$2"; shift 2;;
        --provision-file) PROVISION_FILE="$2"; shift 2;;
        --tests-repo) TESTS_REPO="$2"; shift 2;;
        --tests-repo-ref) TESTS_REPO_REF="$2"; shift 2;;
        --preserve-tests-repo) PRESERVE_TESTS_REPO=true; shift;;
        --timeout) TIMEOUT="$2"; shift 2;;
        -h|--help) usage 0;;
        *) echo "Unknown argument: $1"; usage;;
    esac
done

[[ -z "$PROVIDER" ]] && { echo "ERROR: --provider is required"; usage; }
export KUBECONFIG="${KUBECONFIG:?KUBECONFIG must be set}"

# --- Prereq checks ---
for cmd in go git kubectl go-junit-report base64; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: $cmd is required but not found in PATH"
        exit 1
    fi
done

# --- Test lists (curated per provider + profile) -----------------------------
# Go test function names, joined into an anchored -run regex. Excluded tests are
# kept as comments with the reason, so the active set is a stable baseline of
# tests that actually run and pass on OSC.
AZURE_FULL=(
    TestCreateSimplePodAzure
    TestDeletePodAzure
    TestCreatePodWithConfigMapAzure
    TestCreatePodWithSecretAzure
    TestCreateNginxDeploymentAzure
    TestPodToServiceCommunicationAzure
    TestPodsMTLSCommunicationAzure
    TestPodWithCrioDeviceAnnotationAzure
    TestPodWithIncorrectDeviceAnnotationAzure
    TestPodWithInitContainerAzure
    TestPodToDownloadExternalFileAzure
    # TestPodVMwithAnnotationsInvalidInstanceTypeAzure — expects the pod to fail
    #   with an "invalid" instance type, but OSC lists that size in
    #   AZURE_INSTANCE_SIZES, so the pod VM is created and the test hangs.
    # TestPodVMwithAnnotationsInstanceTypeAzure — self-skips under CI=true
    # TestCreatePeerPodContainerWithExternalIPAccessAzure — self-skips under CI=true
)

AZURE_SANITY=(
    TestCreateSimplePodAzure
    TestCreatePodWithConfigMapAzure
)

# CoCo tests need Trustee/KBS. Only TestRemoteAttestation can use a pre-installed
# Trustee (via KBS_ENDPOINT); the rest need the framework to deploy Trustee
# itself, so none are enabled yet.
AZURE_COCO=(
)

# AWS tests are all non-CoCo.
AWS_FULL=(
    TestAwsCreateSimplePod
    TestAwsDeletePod
    TestAwsCreatePodWithConfigMap
    TestAwsCreatePeerPodWithJob
    TestAwsCreatePeerPodAndCheckWorkDirLogs
    TestAwsCreatePeerPodAndCheckEnvVariableLogsWithImageOnly
    TestAwsCreatePeerPodAndCheckEnvVariableLogsWithDeploymentOnly
    TestAwsCreatePeerPodAndCheckEnvVariableLogsWithImageAndDeployment
    TestAwsPodWithInitContainer
    # The following self-skip upstream and add no coverage:
    # TestAwsCreatePeerPodAndCheckUserLogs — t.Skip (kata-containers#5732)
    # TestAwsCreatePodWithSecret, TestAwsCreatePeerPodContainerWithExternalIPAccess,
    #   TestAwsCreateNginxDeployment — t.Skip("Test not passing")
    # TestAwsCreatePeerPodWithPVC and the AuthenticatedImage tests — t.Skip("To be implemented")
    # TestAwsCreatePeerPodWithLargeImage, TestAwsCreatePeerPodContainerWithInvalidAlternateImage — skip under CI=true
)

AWS_SANITY=(
    TestAwsCreateSimplePod
    TestAwsCreatePodWithConfigMap
)

select_tests() {
    case "$PROVIDER" in
        azure)
            case "$PROFILE" in
                sanity)   TESTS=("${AZURE_SANITY[@]}") ;;
                full)     TESTS=("${AZURE_FULL[@]}") ;;
                coco)     TESTS=("${AZURE_COCO[@]}") ;;
                *)        TESTS=(); RUN_REGEX="$PROFILE" ;;   # raw -run regex
            esac
            ;;
        aws)
            case "$PROFILE" in
                sanity)   TESTS=("${AWS_SANITY[@]}") ;;
                full)     TESTS=("${AWS_FULL[@]}") ;;
                coco)     echo "ERROR: no CoCo tests for aws"; exit 1 ;;
                *)        TESTS=(); RUN_REGEX="$PROFILE" ;;   # raw -run regex
            esac
            ;;
        gcp)
            echo "ERROR: provider 'gcp' is not supported"; exit 1 ;;
        *)
            echo "ERROR: unknown provider '$PROVIDER'"; usage ;;
    esac
}

# Read a key from peer-pods-cm (plain) / peer-pods-secret (base64-decoded).
cm_get() {
    kubectl get configmap peer-pods-cm -n "$PEERPODS_NAMESPACE" \
        -o "jsonpath={.data.$1}" 2>/dev/null
}
secret_get() {
    local b64
    b64="$(kubectl get secret peer-pods-secret -n "$PEERPODS_NAMESPACE" \
        -o "jsonpath={.data.$1}" 2>/dev/null)"
    [[ -n "$b64" ]] && printf '%s' "$b64" | base64 -d
}

# Abort the runner if a required value is empty. Call directly (not in $()) so
# the exit stops the whole runner.
need() {
    [[ -z "$2" ]] && { echo "ERROR: could not read '$1' from peer-pods-cm/secret in ns $PEERPODS_NAMESPACE" >&2; exit 1; }
    return 0
}

# The framework's getCaaPod() selects CAA pods by app=cloud-api-adaptor with no
# way to override the label, so add it to the OSC daemonset pods. Labels the live
# pods; that is enough for a run (they are not recreated meanwhile).
label_caa_pods() {
    local pods
    pods="$(kubectl get pods -n "$PEERPODS_NAMESPACE" -l "$CAA_DS_SELECTOR" -o name 2>/dev/null)"
    if [[ -z "$pods" ]]; then
        echo "ERROR: no CAA pods found (ns=$PEERPODS_NAMESPACE selector=$CAA_DS_SELECTOR); is OSC/peer-pods installed?" >&2
        exit 1
    fi
    kubectl label pods -n "$PEERPODS_NAMESPACE" -l "$CAA_DS_SELECTOR" \
        app=cloud-api-adaptor --overwrite >/dev/null || {
        echo "ERROR: failed to label CAA pods with app=cloud-api-adaptor" >&2; exit 1
    }
    echo "Labeled OSC CAA pods (${CAA_DS_SELECTOR}) with app=cloud-api-adaptor"
}

# Best-effort report of the OSC credential mode, from the peer-pods-secret labels.
detect_cred_mode() {
    local sts cr
    sts="$(kubectl get secret peer-pods-secret -n "$PEERPODS_NAMESPACE" \
        -o "jsonpath={.metadata.labels.kataconfiguration\.openshift\.io/sts}" 2>/dev/null)"
    cr="$(kubectl get secret peer-pods-secret -n "$PEERPODS_NAMESPACE" \
        -o "jsonpath={.metadata.labels.kataconfiguration\.openshift\.io/credentials-request-based}" 2>/dev/null)"
    if [[ -n "$sts" ]]; then echo "sts";
    elif [[ "$cr" == "true" ]]; then echo "cco";
    else echo "manual"; fi
}

# Build the CAA properties file. Infrastructure config comes from peer-pods-cm;
# credentials are resolved env-first (so a caller can inject them) with a
# peer-pods-secret fallback. This keeps the runner working across OSC credential
# modes: manual and CCO store a usable client secret, while the short-lived modes
# (workload identity / STS) do not — there we fail fast unless creds are provided
# in the environment. The framework authenticates from the environment
# (azidentity.NewDefaultAzureCredential), so the resolved values are exported.
build_provision_file_azure() {
    local out="$1"
    local sub client secret tenant rg region image subnet vxlan instsize cluster mode

    mode="$(detect_cred_mode)"
    echo "peer-pods-secret credential mode: ${mode}"

    sub="${AZURE_SUBSCRIPTION_ID:-$(secret_get AZURE_SUBSCRIPTION_ID)}"
    client="${AZURE_CLIENT_ID:-$(secret_get AZURE_CLIENT_ID)}"
    secret="${AZURE_CLIENT_SECRET:-$(secret_get AZURE_CLIENT_SECRET)}"
    tenant="${AZURE_TENANT_ID:-$(secret_get AZURE_TENANT_ID)}"

    need AZURE_SUBSCRIPTION_ID "$sub"
    need AZURE_CLIENT_ID "$client"
    if [[ -z "$secret" ]]; then
        echo "ERROR: no usable Azure client secret found (mode '${mode}')." >&2
        echo "  Provide AZURE_CLIENT_ID, AZURE_CLIENT_SECRET, AZURE_TENANT_ID and" >&2
        echo "  AZURE_SUBSCRIPTION_ID via the environment (e.g. from the cluster profile)." >&2
        exit 1
    fi
    need AZURE_TENANT_ID "$tenant"
    region="$(cm_get AZURE_REGION)"; need AZURE_REGION "$region"
    image="$(cm_get AZURE_IMAGE_ID)"; need AZURE_IMAGE_ID "$image"
    rg="$(cm_get AZURE_RESOURCE_GROUP)"
    subnet="$(cm_get AZURE_SUBNET_ID)"
    vxlan="$(cm_get VXLAN_PORT)"
    instsize="$(cm_get AZURE_INSTANCE_SIZE)"
    cluster="$(kubectl get infrastructure cluster -o jsonpath='{.status.infrastructureName}' 2>/dev/null)"

    export AZURE_SUBSCRIPTION_ID="$sub"
    export AZURE_CLIENT_ID="$client"
    export AZURE_CLIENT_SECRET="$secret"
    export AZURE_TENANT_ID="$tenant"

    # IS_CI_MANAGED_CLUSTER / IS_SELF_MANAGED_CLUSTER stay false so no code path
    # tries to touch or create a cluster.
    {
        echo "AZURE_SUBSCRIPTION_ID=\"${sub}\""
        echo "AZURE_CLIENT_ID=\"${client}\""
        echo "AZURE_CLIENT_SECRET=\"${secret}\""
        echo "AZURE_TENANT_ID=\"${tenant}\""
        echo "LOCATION=\"${region}\""
        echo "AZURE_IMAGE_ID=\"${image}\""
        [[ -n "$rg" ]]       && echo "RESOURCE_GROUP_NAME=\"${rg}\""
        [[ -n "$subnet" ]]   && echo "AZURE_SUBNET_ID=\"${subnet}\""
        [[ -n "$instsize" ]] && echo "AZURE_INSTANCE_SIZE=\"${instsize}\""
        [[ -n "$vxlan" ]]    && echo "VXLAN_PORT=\"${vxlan}\""
        [[ -n "$cluster" ]]  && echo "CLUSTER_NAME=\"${cluster}\""
        echo "CONTAINER_RUNTIME=\"crio\""
        echo "AZURE_CLI_AUTH=\"false\""
        echo "IS_CI_MANAGED_CLUSTER=\"false\""
        echo "IS_SELF_MANAGED_CLUSTER=\"false\""
    } > "$out"

    echo "Built Azure properties (cred mode=${mode}, region=${region})"
}

# AWS counterpart of build_provision_file_azure. The framework authenticates with
# awsConfig.LoadDefaultConfig, which reads AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY
# (and AWS_SESSION_TOKEN/AWS_REGION) from the environment, so the resolved
# credentials are exported. cluster_type=onprem selects the no-op cluster.
build_provision_file_aws() {
    local out="$1"
    local akid asak region vpc subnet sg ami instype vxlan disablecvm mode

    mode="$(detect_cred_mode)"
    echo "peer-pods-secret credential mode: ${mode}"

    akid="${AWS_ACCESS_KEY_ID:-$(secret_get AWS_ACCESS_KEY_ID)}"
    asak="${AWS_SECRET_ACCESS_KEY:-$(secret_get AWS_SECRET_ACCESS_KEY)}"
    if [[ -z "$akid" || -z "$asak" ]]; then
        echo "ERROR: no usable AWS access key found (mode '${mode}')." >&2
        echo "  Provide AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (+ AWS_SESSION_TOKEN" >&2
        echo "  if temporary) via the environment (e.g. from the cluster profile)." >&2
        exit 1
    fi

    region="$(cm_get AWS_REGION)"; need AWS_REGION "$region"
    vpc="$(cm_get AWS_VPC_ID)"
    subnet="$(cm_get AWS_SUBNET_ID)"
    # AWS_SG_IDS may be a comma-separated list; the provisioner's Vpc takes a
    # single id, so pass the first one.
    sg="$(cm_get AWS_SG_IDS)"; sg="${sg%%,*}"
    ami="$(cm_get PODVM_AMI_ID)"
    instype="$(cm_get PODVM_INSTANCE_TYPE)"
    vxlan="$(cm_get VXLAN_PORT)"
    disablecvm="$(cm_get DISABLECVM)"

    export AWS_ACCESS_KEY_ID="$akid"
    export AWS_SECRET_ACCESS_KEY="$asak"
    export AWS_REGION="$region"
    token="${AWS_SESSION_TOKEN:-$(secret_get AWS_SESSION_TOKEN)}"
    [[ -n "$token" ]] && export AWS_SESSION_TOKEN="$token"

    {
        echo "aws_region=\"${region}\""
        echo "cluster_type=\"onprem\""
        [[ -n "$vpc" ]]        && echo "aws_vpc_id=\"${vpc}\""
        [[ -n "$subnet" ]]     && echo "aws_vpc_subnet_id=\"${subnet}\""
        [[ -n "$sg" ]]         && echo "aws_vpc_sg_id=\"${sg}\""
        [[ -n "$ami" ]]        && echo "podvm_aws_ami_id=\"${ami}\""
        [[ -n "$instype" ]]    && echo "podvm_instance_type=\"${instype}\""
        [[ -n "$vxlan" ]]      && echo "vxlan_port=\"${vxlan}\""
        [[ -n "$disablecvm" ]] && echo "disablecvm=\"${disablecvm}\""
        echo "container_runtime=\"crio\""
    } > "$out"

    echo "Built AWS properties (cred mode=${mode}, region=${region})"
}

# --- Cleanup ---
CLONE_DIR=""
GENERATED_PROPS=""
cleanup() {
    [[ -n "$GENERATED_PROPS" && -f "$GENERATED_PROPS" ]] && rm -f "$GENERATED_PROPS"
    if [[ -n "$CLONE_DIR" && -d "$CLONE_DIR" ]]; then
        if [[ "$PRESERVE_TESTS_REPO" == "true" ]]; then
            echo "Test repo preserved at: $CLONE_DIR"
        else
            rm -rf "$CLONE_DIR"
        fi
    fi
}
trap cleanup EXIT

# --- Resolve test repo ---
safe_url="${TESTS_REPO/\/\/*@/\/\/}"
if [[ -d "$TESTS_REPO" ]]; then
    TESTS_REPO="$(realpath "$TESTS_REPO")"
    CAA_DIR="${TESTS_REPO}/src/cloud-api-adaptor"
    echo "Using local test repo: $TESTS_REPO"
else
    CLONE_DIR=$(mktemp -d /tmp/caa-tests-XXXXXX)
    echo "Cloning $safe_url (ref: $TESTS_REPO_REF) to $CLONE_DIR..."
    git clone --depth 1 --branch "$TESTS_REPO_REF" "$TESTS_REPO" "$CLONE_DIR" || {
        echo "ERROR: failed to clone $safe_url at ref $TESTS_REPO_REF" >&2; exit 1
    }
    CAA_DIR="${CLONE_DIR}/src/cloud-api-adaptor"
fi

[[ -d "${CAA_DIR}/test/e2e" ]] || { echo "ERROR: CAA e2e dir not found: ${CAA_DIR}/test/e2e"; exit 1; }

# --- Properties file ---
if [[ -n "$PROVISION_FILE" ]]; then
    [[ -f "$PROVISION_FILE" ]] || { echo "ERROR: --provision-file not found: $PROVISION_FILE"; exit 1; }
    echo "Using supplied properties file: $PROVISION_FILE"
else
    GENERATED_PROPS="$(mktemp /tmp/caa-props-XXXXXX.properties)"
    case "$PROVIDER" in
        azure) build_provision_file_azure "$GENERATED_PROPS" ;;
        aws)   build_provision_file_aws "$GENERATED_PROPS" ;;
        *) echo "ERROR: properties derivation for '$PROVIDER' not implemented"; exit 1 ;;
    esac
    PROVISION_FILE="$GENERATED_PROPS"
fi

# --- Select tests ---
RUN_REGEX=""
select_tests
if [[ -z "$RUN_REGEX" ]]; then
    [[ ${#TESTS[@]} -eq 0 ]] && { echo "ERROR: no tests selected for profile '$PROFILE'"; exit 1; }
    RUN_REGEX="^($(IFS='|'; echo "${TESTS[*]}"))\$"
fi

# --- Environment for the framework (provisioning fully disabled) -------------
export CLOUD_PROVIDER="$PROVIDER"
export TEST_PROVISION="no"
export TEST_INSTALL_CAA="no"
export TEST_TEARDOWN="no"
export TEST_PROVISION_FILE="$PROVISION_FILE"
export CONTAINER_RUNTIME="crio"
export TEST_CAA_NAMESPACE="${TEST_CAA_NAMESPACE:-$PEERPODS_NAMESPACE}"

label_caa_pods

# --- Run tests ---
mkdir -p "$RESULTS_DIR"
profile_label="${PROFILE//[^a-zA-Z0-9]/_}"
junit_file="${RESULTS_DIR}/${PROVIDER}-${profile_label}.xml"
raw_log="${RESULTS_DIR}/${PROVIDER}-${profile_label}.log"

echo ""
echo "=============================="
echo "Provider:     $PROVIDER"
echo "Profile:      $PROFILE"
echo "Run regex:    $RUN_REGEX"
echo "Repo ref:     $TESTS_REPO_REF"
echo "=============================="
echo ""

cd "$CAA_DIR" || exit 1

# Serial (-parallel 1): each peer-pod test boots a cloud VM, so serial keeps the
# VM count and the -json stream sane for go-junit-report.
go test -v -tags="$PROVIDER" -timeout "$TIMEOUT" -count=1 -parallel 1 \
    -run "$RUN_REGEX" ./test/e2e 2>&1 \
    | tee "$raw_log" \
    | go-junit-report -set-exit-code > "$junit_file"
rc_arr=("${PIPESTATUS[@]}")
gotest_rc="${rc_arr[0]}"
junit_rc="${rc_arr[2]}"

echo ""
echo "=============================="
echo "go test exit:        $gotest_rc"
echo "go-junit-report exit: $junit_rc"
echo "JUnit XML:           $junit_file"
echo "Full log:            $raw_log"
echo "=============================="

# Fail if either the test run or the JUnit conversion reported a failure.
[[ "$gotest_rc" -ne 0 || "$junit_rc" -ne 0 ]] && exit 1
exit 0
