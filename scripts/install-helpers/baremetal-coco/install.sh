#!/bin/bash

# Defaults
MIRRORING=false
ADD_IMAGE_PULL_SECRET=false
GA_RELEASE=true
KERNEL_CONFIG_MC_FILE="./96-kata-kernel-config-mc.yaml"
SKIP_NFD="${SKIP_NFD:-false}"
TRUSTEE_URL="${TRUSTEE_URL:-"http://kbs-service.trustee-operator-system:8080"}"
CMD_TIMEOUT="${CMD_TIMEOUT:-900}"

# Function to check if a command is available
function check_command() {
    local cmd="$1"
    if ! command -v "$cmd" &>/dev/null; then
        echo "$cmd command not found. Please install the $cmd CLI tool."
        return 1
    fi
}

# Function to wait for the operator deployment object to be ready
function wait_for_deployment() {
    local deployment=$1
    local namespace=$2
    local timeout=$CMD_TIMEOUT
    local interval=5
    local elapsed=0
    local ready=0

    while [ $elapsed -lt "$timeout" ]; do
        ready=$(oc get deployment -n "$namespace" "$deployment" -o jsonpath='{.status.readyReplicas}')
        if [ "$ready" == "1" ]; then
            echo "Operator $deployment is ready"
            return 0
        fi
        sleep $interval
        elapsed=$((elapsed + interval))
    done
    echo "Operator $deployment is not ready after $timeout seconds"
    return 1
}

# Function to wait for service endpoints IP to be available
# Example json for service endpoints. IP is available in the "addresses" field
#  "subsets": [
#    {
#        "addresses": [
#            {
#                "ip": "10.135.0.25",
#                "nodeName": "coco-worker-1.testocp.local",
#                "targetRef": {
#                    "kind": "Pod",
#                    "name": "controller-manager-87ffb6bfd-5zzvf",
#                    "namespace": "openshift-sandboxed-containers-operator",
#                    "uid": "00059394-29fb-44bf-8121-d1df02524ea8"
#                }
#            }
#        ],
#        "ports": [
#            {
#                "port": 443,
#                "protocol": "TCP"
#            }
#        ]
#    }
#]

function wait_for_service_ep_ip() {
    local service=$1
    local namespace=$2
    local timeout=$CMD_TIMEOUT
    local interval=5
    local elapsed=0
    local ip=0

    while [ $elapsed -lt "$timeout" ]; do
        ip=$(oc get endpoints -n "$namespace" "$service" -o jsonpath='{.subsets[0].addresses[0].ip}')
        if [ -n "$ip" ]; then
            echo "Service $service IP ($ip) is available"
            return 0
        fi
        sleep $interval
        elapsed=$((elapsed + interval))
    done
    echo "Service $service IP is not available after $timeout seconds"
    return 1
}

# Function to wait for MachineConfigPool (MCP) to be ready
function wait_for_mcp() {
    local mcp=$1
    local timeout=$CMD_TIMEOUT
    local interval=5
    local elapsed=0
    local timeout_start_updating=30

    # First, wait for sometime to let MCP start updating
    while [ $elapsed -lt "$timeout_start_updating" ]; do
        if [ "$statusUpdating" == "True" ]; then
            echo "MCP $mcp has started updating"
            break
        fi
        sleep $interval
        elapsed=$((elapsed + interval))
        statusUpdating=$(oc get mcp "$mcp" -o=jsonpath='{.status.conditions[?(@.type=="Updating")].status}')
    done

    # Now, wait for MCP to be ready
    statusUpdated=$(oc get mcp "$mcp" -o=jsonpath='{.status.conditions[?(@.type=="Updated")].status}' 2>/dev/null)
    statusDegraded=$(oc get mcp "$mcp" -o=jsonpath='{.status.conditions[?(@.type=="Degraded")].status}' 2>/dev/null)
    while [ $elapsed -lt "$timeout" ]; do
        if [ "$statusUpdated" == "True" ] && [ "$statusUpdating" == "False" ] && [ "$statusDegraded" == "False" ]; then
            echo "MCP $mcp is ready"
            return 0
        elif [ "$statusUpdating" == "True" ]; then
            echo "[$elapsed] MCP $mcp is updating"
        fi
        sleep $interval
        elapsed=$((elapsed + interval))
        statusUpdating=$(oc get mcp "$mcp" -o=jsonpath='{.status.conditions[?(@.type=="Updating")].status}')
        statusUpdated=$(oc get mcp "$mcp" -o=jsonpath='{.status.conditions[?(@.type=="Updated")].status}' 2>/dev/null)
        statusDegraded=$(oc get mcp "$mcp" -o=jsonpath='{.status.conditions[?(@.type=="Degraded")].status}' 2>/dev/null)
    done
    echo "MCP $mcp is not ready after $elapsed seconds"
    return 1
}

# Function to wait for runtimeclass to be ready
function wait_for_runtimeclass() {

    local runtimeclass=$1
    local timeout=$CMD_TIMEOUT
    local interval=5
    local elapsed=0
    local ready=0

    # oc get runtimeclass "$runtimeclass" -o jsonpath={.metadata.name} should return the runtimeclass
    while [ $elapsed -lt "$timeout" ]; do
        ready=$(oc get runtimeclass "$runtimeclass" -o jsonpath='{.metadata.name}')
        if [ "$ready" == "$runtimeclass" ]; then
            echo "RuntimeClass $runtimeclass is ready"
            return 0
        fi
        sleep $interval
        elapsed=$((elapsed + interval))
    done

    echo "RuntimeClass $runtimeclass is not ready after $timeout seconds"
    return 1
}

# Function to apply the operator manifests
function apply_operator_manifests() {
    # Apply the manifests
    oc apply -f ns.yaml || return 1
    oc apply -f og.yaml || return 1
    if [[ "$GA_RELEASE" == "true" ]]; then
        oc apply -f subs-ga.yaml || return 1
    else
        oc apply -f osc_catalog.yaml || return 1
        oc apply -f subs-upstream.yaml || return 1
    fi

}

# Function to check if single node OpenShift
function is_single_node_ocp() {
    local node_count
    node_count=$(oc get nodes --no-headers | wc -l)
    if [ "$node_count" -eq 1 ]; then
        return 0
    else
        return 1
    fi
}

# Function to set additional cluster-wide image pull secret
# Requires PULL_SECRET_JSON environment variable to be set
# eg. PULL_SECRET_JSON='{"my.registry.io": {"auth": "ABC"}}'
function add_image_pull_secret() {
    # Check if SECRET_JSON is set
    if [ -z "$PULL_SECRET_JSON" ]; then
        echo "PULL_SECRET_JSON environment variable is not set"
        echo "example PULL_SECRET_JSON='{\"my.registry.io\": {\"auth\": \"ABC\"}}'"
        return 1
    fi

    # Get the existing secret
    oc get -n openshift-config secret/pull-secret -ojson | jq -r '.data.".dockerconfigjson"' | base64 -d | jq '.' >cluster-pull-secret.json ||
        return 1

    # Add the new secret to the existing secret
    jq --argjson data "$PULL_SECRET_JSON" '.auths |= ($data + .)' cluster-pull-secret.json >cluster-pull-secret-mod.json || return 1

    # Set the image pull secret
    oc set data secret/pull-secret -n openshift-config --from-file=.dockerconfigjson=cluster-pull-secret-mod.json || return 1

}

#Function to deploy the OpenShift sandboxed containers (OSC) operator
function deploy_osc_operator() {
    echo "OpenShift sandboxed containers operator | starting the deployment"
    # Apply the operator manifests
    apply_operator_manifests || return 1

    wait_for_deployment controller-manager openshift-sandboxed-containers-operator || return 1

    # Wait for the service endpoints IP to be available
    wait_for_service_ep_ip webhook-service openshift-sandboxed-containers-operator || return 1

    echo "OpenShift sandboxed containers operator | deployment finished successfully"
}

# Function to deploy NodeFeatureDiscovery (NFD) operator
function deploy_nfd_operator() {
    echo "Node Feature Discovery operator | starting the deployment"

    pushd nfd || return 1
    oc apply -f ns.yaml || return 1
    oc apply -f og.yaml || return 1
    oc apply -f subs.yaml || return 1
    popd || return 1

    wait_for_deployment nfd-controller-manager openshift-nfd || return 1
    echo "Node Feature Discovery operator | deployment finished successfully"
}

function create_intel_node_feature_rules() {
    echo "Node Feature Discovery operator | creating intel node feature rules"

    pushd nfd || return 1
    oc apply -f intel-rules.yaml || return 1
    popd || return 1

    echo "Node Feature Discovery operator | node feature rules successfully created"
}

function deploy_intel_device_plugins() {
    echo "Intel Device Plugins operator | starting the deployment"

    pushd intel-dpo || return 1
    oc apply -f install_operator.yaml || return 1
    wait_for_deployment inteldeviceplugins-controller-manager openshift-operators || return 1
    oc apply -f sgx_device_plugin.yaml || return 1
    popd || return 1

    echo "Intel Device Plugins operator | deployment finished successfully"
}

function create_amd_node_feature_rules() {
    echo "Node Feature Discovery operator | creating amd node feature rules"

    pushd nfd || return 1
    oc apply -f amd-rules.yaml || return 1
    popd || return 1

    echo "Node Feature Discovery operator | node feature rules successfully created"
}

# Function to create KataConfig based on TEE type
function create_kataconfig() {
    local tee_type=${1}
    local label='coco: "true"'

    case $tee_type in
    tdx)
        label='intel.feature.node.kubernetes.io/tdx: "true"'
        ;;
    snp)
        label='amd.feature.node.kubernetes.io/snp: "true"'
        ;;
    esac

    # Create KataConfig object
    oc apply -f - <<EOF || return 1
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: cluster-kataconfig
spec:
  enablePeerPods: false
  logLevel: info
  kataConfigPoolSelector:
    matchLabels:
      $label
EOF
    echo "KataConfig object successfully created"
}

# Function to set aa_kbc_params for teh Kata agent to be used for
# attestation
# Use drop-in configuration via MachineConfig
# This function accepts the TEE type as an argument
# It also accepts the Trustee URL as an argument
# Trustee URL should be complete URL with the form http(s)://<trustee_ip>:<port>
function set_aa_kbc_params_for_kata_agent() {
    local tee_type=${1}
    local trustee_url=${2}
    local source=""
    local filepath=""

    # Create kata configuration toml override for the kernel_params
    # Input kernel_params="agent.aa_kbc_params=cc_kbc::$trustee_url"
    kata_override="[hypervisor.qemu]
kernel_params= \"agent.aa_kbc_params=cc_kbc::$trustee_url\""

    # Create base64 encoding of the drop-in to be used as source
    source=$(echo "$kata_override" | base64 -w0) || return 1

    # This is applied after Kata installation so we should use the kata-oc label
    # for worker nodes
    local mc_label="machineconfiguration.openshift.io/role: kata-oc"

    if is_single_node_ocp; then
        mc_label="machineconfiguration.openshift.io/role: master"
    fi

    case $tee_type in
    tdx)
        filepath=/etc/kata-containers/tdx/config.d/96-kata-kernel-config
        ;;
    snp)
        filepath=/etc/kata-containers/snp/config.d/96-kata-kernel-config
        ;;
    esac

    cat <<EOF >"$KERNEL_CONFIG_MC_FILE"
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    $mc_label
  name: 96-kata-kernel-config
  namespace: openshift-machine-config-operator
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,$source
        mode: 420
        overwrite: true
        path: $filepath
EOF

    oc apply -f "$KERNEL_CONFIG_MC_FILE"

}

# Function to create runtimeClass based on TEE type and
# SNO or regular OCP
# Generic template
#apiVersion: node.k8s.io/v1
#handler: kata-$TEE_TYPE
#kind: RuntimeClass
#metadata:
#  name: kata-$TEE_TYPE
#scheduling:
#  nodeSelector:
#    $label
function create_runtimeclasses() {
    local tee_type=${1}
    local label='node-role.kubernetes.io/kata-oc: ""'
    local ext_resources=''

    if is_single_node_ocp; then
        label='node-role.kubernetes.io/master: ""'
    fi

    case $tee_type in
    tdx)
        ext_resources='tdx.intel.com/keys: 1'
        ;;
    snp)
        ext_resources='sev-snp.amd.com/esids: 1'
        ;;
    esac

    echo "Creating kata-$tee_type RuntimeClass object"

    #Create runtimeClass object
    oc apply -f - <<EOF || return 1
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: kata-$tee_type
handler: kata-$tee_type
overhead:
  podFixed:
    memory: "350Mi"
    cpu: "250m"
    $ext_resources
scheduling:
  nodeSelector:
     $label
EOF
    echo "kata-$tee_type RuntimeClass object successfully created"
}

function display_help() {
    echo "Usage: install.sh -t <tee_type> [-h] [-m] [-s] [-b] [-u]"
    echo "Options:"
    echo "  -t <tee_type> Specify the TEE type (tdx or snp)"
    echo "  -h Display help"
    echo "  -m Install the image mirroring config"
    echo "  -s Set additional cluster-wide image pull secret."
    echo "     Requires the secret to be set in PULL_SECRET_JSON environment variable"
    echo "     Example PULL_SECRET_JSON='{\"my.registry.io\": {\"auth\": \"ABC\"}}'"
    echo "  -b Use pre-ga operator bundles"
    echo "  -u Uninstall the installed artifacts"
    echo " "
    echo "Some environment variables that can be set:"
    echo "SKIP_NFD: Skip NFD operator installation and CR creation (default: false)"
    echo "TRUSTEE_URL: Trustee URL to be used in the kernel config (default: http://kbs-service.trustee-operator-system:8080)"
    echo "CMD_TIMEOUT: Timeout for the commands (default: 900)"
    # Add some example usage options
    echo " "
    echo "Example usage:"
    echo "# Install the GA operator for snp"
    echo " ./install.sh -t snp"
    echo " "
    echo "# Install the GA operator for tdx"
    echo " ./install.sh -t tdx"
    echo " "
    echo "# Install the GA operator with image mirroring for snp"
    echo " ./install.sh -m -t snp"
    echo " "
    echo "# Install the GA operator with additional cluster-wide image pull secret for tdx"
    echo " export PULL_SECRET_JSON='{"brew.registry.redhat.io": {"auth": "abcd1234"}, "registry.redhat.io": {"auth": "abcd1234"}}'"
    echo " ./install.sh -s -t tdx"
    echo " "
    echo "# Install the pre-GA operator with image mirroring and additional cluster-wide image pull secret for snp"
    echo " ./install.sh -m -s -b -t snp"
    echo " "
    echo "# Deploy the pre-GA OSC operator with image mirroring and additional cluster-wide image pull secret for tdx"
    echo " export PULL_SECRET_JSON='{"brew.registry.redhat.io": {"auth": "abcd1234"}, "registry.redhat.io": {"auth": "abcd1234"}}'"
    echo " ./install.sh -m -s -b -t tdx"
    echo " "
    echo "# Uninstall the installed artifacts for tdx"
    echo " ./install.sh -t tdx -u"
    echo " "
    echo "# Uninstall the installed artifacts for snp"
    echo " ./install.sh -t snp -u"
    echo " "
}

# Function to verify all required variables are set and
# required files exist

function verify_params() {

    # Check if TEE_TYPE is provided
    if [ -z "$TEE_TYPE" ]; then
        echo "Error: TEE type (-t) is mandatory"
        display_help
        return 1
    fi

    # Verify TEE_TYPE is valid
    if [ "$TEE_TYPE" != "tdx" ] && [ "$TEE_TYPE" != "snp" ]; then
        echo "Error: Invalid TEE type. It must be 'tdx' or 'snp'"
        display_help
        return 1
    fi

    # If ADD_IMAGE_PULL_SECRET is true,  then check if PULL_SECRET_JSON is set
    if [ "$ADD_IMAGE_PULL_SECRET" = true ] && [ -z "$PULL_SECRET_JSON" ]; then
        echo "ADD_IMAGE_PULL_SECRET is set but required environment variable: PULL_SECRET_JSON is not set"
        return 1
    fi

}

function uninstall_intel_device_plugins() {
    echo "Intel Device Plugins operator | starting the uninstall"

    pushd intel-dpo || return 1
    oc delete -f sgx_device_plugin.yaml || return 1
    oc delete -f install_operator.yaml || return 1
    popd || return 1

    echo "Intel Device Plugins operator | deployment uninstalled successfully"
}

function uninstall_node_feature_discovery() {
    tee_type="${1:-}"

    oc get deployment nfd-controller-manager -n openshift-nfd &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        pushd nfd || return 1
        case $tee_type in
        tdx)
            oc delete -f intel-rules.yaml || return 1
            ;;
        snp)
            oc delete -f amd-rules.yaml || return 1
            ;;
        esac

        oc delete -f nfd-cr.yaml || return 1
        oc delete -f subs.yaml || return 1
        oc delete -f og.yaml || return 1
        oc delete -f ns.yaml || return 1
        popd || return 1
    fi
}

# Function to uninstall the installed artifacts
# It won't delete the cluster
function uninstall() {
    echo "Uninstalling all the artifacts"

    if [ "$TEE_TYPE" = "tdx" ]; then
        uninstall_intel_device_plugins || exit 1
    fi

    # Uninstall NFD
    uninstall_node_feature_discovery "$TEE_TYPE" || return 1

    # Delete the MachineConfig 96-kata-kernel-config
    oc get mc 96-kata-kernel-config &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        echo "Deleting the MachineConfig 96-kata-kernel-config"
        oc delete -f "$KERNEL_CONFIG_MC_FILE" &>/dev/null
        rm -f "$KERNEL_CONFIG_MC_FILE"

        echo "Waiting for MCP to be READY"
        # If single node OpenShift, then wait for the master MCP to be ready
        # Else wait for kata-oc MCP to be ready
        if is_single_node_ocp; then
            echo "SNO"
            wait_for_mcp master || return 1
        else
            wait_for_mcp kata-oc || return 1
        fi
    fi

    # Delete kataconfig cluster-kataconfig if it exists
    oc get kataconfig cluster-kataconfig &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        echo "Deleting the kataconfig cluster-kataconfig. It may take few minutes to complete the process."
        oc delete kataconfig cluster-kataconfig || return 1
        echo "Waiting for MCP to be READY"
        wait_for_mcp master || return 1
        wait_for_mcp worker || return 1
    fi

    oc get cm osc-feature-gates -n openshift-sandboxed-containers-operator &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        oc delete cm osc-feature-gates -n openshift-sandboxed-containers-operator || return 1
    fi

    # Delete osc-upstream-catalog CatalogSource if it exists
    oc get catalogsource osc-upstream-catalog -n openshift-marketplace &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        oc delete catalogsource osc-upstream-catalog -n openshift-marketplace || return 1
    fi

    # Delete ImageTagMirrorSet osc-brew-registry-tag if it exists
    oc get imagetagmirrorset osc-brew-registry-tag &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        oc delete imagetagmirrorset osc-brew-registry-tag || return 1
    fi

    # Delete ImageDigestMirrorSet osc-brew-registry-digest if it exists
    oc get imagedigestmirrorset osc-brew-registry-digest &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        oc delete imagedigestmirrorset osc-brew-registry-digest || return 1
    fi

    # Delete the namespace openshift-sandboxed-containers-operator if it exists
    oc get ns openshift-sandboxed-containers-operator &>/dev/null
    return_code=$?
    if [ $return_code -eq 0 ]; then
        oc delete ns openshift-sandboxed-containers-operator || return 1
    fi

    echo "Waiting for MCP to be READY"
    wait_for_mcp master || return 1
    wait_for_mcp worker || return 1

    # Delete the runtimeClass
    oc delete runtimeclass kata-"$TEE_TYPE" &>/dev/null

    echo "Uninstall completed successfully"
}

# Function to print all the env variables
function print_env_vars() {
    echo "ADD_IMAGE_PULL_SECRET: $ADD_IMAGE_PULL_SECRET"
    echo "GA_RELEASE: $GA_RELEASE"
    echo "MIRRORING: $MIRRORING"
    echo "TEE_TYPE: $TEE_TYPE"
    echo "SKIP_NFD: $SKIP_NFD"
    echo "TRUSTEE_URL: $TRUSTEE_URL"
}

while getopts "t:hmsbu" opt; do
    case $opt in
    t)
        # Convert it to lower case
        TEE_TYPE=$(echo "$OPTARG" | tr '[:upper:]' '[:lower:]')
        echo "TEE type: $TEE_TYPE"
        ;;
    h)
        display_help
        exit 0
        ;;
    m)
        echo "Mirroring option passed"
        # Set global variable to indicate mirroring option is passed
        MIRRORING=true
        ;;
    s)
        echo "Setting additional cluster-wide image pull secret"
        ADD_IMAGE_PULL_SECRET=true
        ;;
    b)
        echo "Using non-ga operator bundles"
        GA_RELEASE=false
        ;;
    u)

        ADD_IMAGE_PULL_SECRET=false
        # Ensure TEE_TYPE is set
        verify_params || exit 1
        echo "Uninstalling"
        uninstall || exit 1
        exit 0
        ;;

    \?)
        echo "Invalid option: -$OPTARG" >&2
        display_help
        exit 1
        ;;
    esac
done

# Verify all required parameters are set
verify_params || exit 1

# Check if oc command is available
check_command "oc" || exit 1

# Display the cluster information
oc cluster-info || exit 1

# If MIRRORING is true, then create the image mirroring config
if [ "$MIRRORING" = true ]; then
    echo "Creating image mirroring config"
    oc apply -f image_mirroring.yaml || exit 1

    echo "Waiting for MCP to be ready"
    wait_for_mcp master || exit 1
    wait_for_mcp worker || exit 1
fi

# If ADD_IMAGE_PULL_SECRET is true, then add additional cluster-wide image pull secret
if [ "$ADD_IMAGE_PULL_SECRET" = true ]; then
    echo "Adding additional cluster-wide image pull secret"
    # Check if jq command is available
    check_command "jq" || exit 1
    add_image_pull_secret || exit 1

    echo "Waiting for MCP to be ready"
    wait_for_mcp master || exit 1
    wait_for_mcp worker || exit 1

fi

# Deploy NFD operator and create NFD CR if SKIP_NFD is false
if [ "$SKIP_NFD" = false ]; then
    deploy_nfd_operator || exit 1

    # Create NFD CR
    oc apply -f nfd/nfd-cr.yaml || exit 1

    case $TEE_TYPE in
    tdx)
        create_intel_node_feature_rules || exit 1
        deploy_intel_device_plugins || exit 1
        ;;
    snp)
        create_amd_node_feature_rules || exit 1
        ;;
    esac
fi

deploy_osc_operator || exit 1

# Create CoCo feature gate ConfigMap
oc apply -f osc-fg-cm.yaml || exit 1

# Create Layered Image FG ConfigMap
case $TEE_TYPE in
tdx)
    oc apply -f layeredimage-cm-tdx.yaml || exit 1
    ;;
snp)
    oc apply -f layeredimage-cm-snp.yaml || exit 1
    ;;
esac

# Create KataConfig
create_kataconfig "$TEE_TYPE" || exit 1

# If single node OpenShift, then wait for the master MCP to be ready
# Else wait for kata-oc MCP to be ready
if is_single_node_ocp; then
    echo "SNO"
    wait_for_mcp master || exit 1
else
    wait_for_mcp kata-oc || exit 1
fi

# Wait for runtimeclass kata to be ready
wait_for_runtimeclass kata || exit 1

# Create runtimeClass kata-tdx or kata-snp based on TEE_TYPE
create_runtimeclasses "$TEE_TYPE" || exit 1

# set the aa_kbc_params config for the kata agent to be used CoCo attestation
set_aa_kbc_params_for_kata_agent "$TEE_TYPE" "$TRUSTEE_URL" || exit 1

# If single node OpenShift, then wait for the master MCP to be ready
# Else wait for kata-oc MCP to be ready
if is_single_node_ocp; then
    echo "SNO"
    wait_for_mcp master || exit 1
else
    wait_for_mcp kata-oc || exit 1
fi

echo "Sandboxed containers operator with CoCo support is installed successfully"

# Print all the env variables values
print_env_vars
