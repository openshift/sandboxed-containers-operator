#!/usr/bin/env bash

ENV_FILE=$(mktemp -t pp-cm-env-XXXX.env)
IS_ARO=false
IS_STS=false
CTYPE=none

echo "##### OSC ConfigMap Configurator #####"

function usage() {
cat <<EOF
Usage: $0 [options]
  options:
   -c <sev/tdx>    Use CoCo defaults for the specified trusted platform type
   -h              Print this help message
   -v <KEY=value>  Set a known or custom variable explicitly
   -y              Automatically answer yes for all questions

  * Defaults are fetched according to the following order:
    1. Explicitly set CLI custom vars
    2. Explicitly defined enviroment vars
    3. Fixed/Fetched/Existing values
EOF
}

error_exit() {
    echo "Error: $1" >&2
    exit 1
}

# Loop through all the arguments
while getopts ":yv:c:h" opt; do
    case ${opt} in
        c ) [[ ${OPTARG} =~ ^(sev|tdx)$ ]] && DISABLECVM=false && CTYPE="${OPTARG}" || error_exit "unknown trusted platform type" ;;
        h ) usage && exit 0;;
        v ) [[ "${OPTARG}" == *"="* ]] && export ${OPTARG} && custom_vars+=(${OPTARG%%=*});;
        y ) export YES=true;;
	\? ) echo "Invalid option: -$OPTARG" >&2 && usage && exit 1;;
    esac
done

# Expected Configuration Keys
common_vars=("CLOUD_PROVIDER" "VXLAN_PORT" "PROXY_TIMEOUT" "DISABLECVM")
[[ "${DISABLECVM}" == "false" ]] && common_vars+=("INITDATA")

aws_vars=("PODVM_INSTANCE_TYPE" "PODVM_INSTANCE_TYPES" "AWS_REGION" "AWS_SUBNET_ID" "AWS_VPC_ID" "AWS_SG_IDS")
aws_optional=("PODVM_AMI_ID")

azure_vars=("AZURE_INSTANCE_SIZE" "AZURE_INSTANCE_SIZES" "AZURE_SUBNET_ID" "AZURE_NSG_ID" "AZURE_REGION" "AZURE_RESOURCE_GROUP")
azure_optional=("AZURE_IMAGE_ID")

gcp_vars=("GCP_PROJECT_ID" "GCP_ZONE" "GCP_NETWORK" "GCP_MACHINE_TYPE")
gcp_optional=("GCP_IMAGE_NAME")

libvirt_vars=("LIBVIRT_POOL" "LIBVIRT_VOL_NAME" "LIBVIRT_DIR_NAME")
libvirt_optional=("LIBVIRT_IMAGE_ID")

#### Functions

function exportVars() {
    for pair in "$@"; do
        key=${pair%%=*}
        [[ -z "${!key}" ]] && export "$pair" # && echo "Defined: $pair" || echo "Skip: $key"
    done
}

function verifyAndSetVars() {
   arr=("$@")
   for i in "${arr[@]}"; do
       local varName="$i"
       local varValue=${!i}
       local userInput

       # skip if verfied already
       [[ -f  "$ENV_FILE" ]] && grep -q "^$varName=" "$ENV_FILE" && continue
       if [[ -n $YES ]]; then
           echo "${varName} [${varValue}]: ${varValue}"
       else
           read -p "${varName} [${varValue}]: " userInput
       fi
       if [[ "${userInput}" != "drop" ]]; then
           echo "$varName=${userInput:-$varValue}" >> ${ENV_FILE}
       else
           echo "dropping ${varName}"
       fi
   done

}

function getIMDSDefaults() {
    local name=imds-defaulter
    ${CLI} apply -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${name}
  namespace: openshift-sandboxed-containers-operator
spec:
  ttlSecondsAfterFinished: 30
    #completions: 1 # same as default
    #parallelism: 1 # same as default
  template:
    spec:
      hostNetwork: true
      restartPolicy: Never
      containers:
      - image: 'registry.redhat.io/openshift4/ose-cli'
        name: ${name}
        command:
        - 'bash'
        - '-c'
        - |
          [[ -n \$(oc get cm peer-pods-cm -n openshift-sandboxed-containers-operator 2>/dev/null) ]] && echo "ConfigMap already exist, skipping..." && exit 0
          provider=\$(oc get infrastructure -n cluster -o jsonpath='{.items[*].status.platformStatus.type}' | awk '{print tolower(\$0)}' | tr -d '"') && echo "cloud provider: \${provider}"
          if [ \${provider} == "aws" ]; then
              export MAC=\$(curl -m 30 -s --show-error "http://169.254.169.254/latest/meta-data/mac")
              cat <<EOS >> /tmp/cm.env
          AWS_REGION=\$(curl -m 15 -s "http://169.254.169.254/latest/meta-data/placement/region")
          AWS_VPC_ID=\$(curl -m 15 -s "http://169.254.169.254/latest/meta-data/network/interfaces/macs/\${MAC}/vpc-id")
          AWS_SUBNET_ID=\$(curl -m 15 -s "http://169.254.169.254/latest/meta-data/network/interfaces/macs/\${MAC}/subnet-id")
          AWS_SG_IDS=\$(SGS=(\$(curl -m 15 -s "http://169.254.169.254/latest/meta-data/network/interfaces/macs/\${MAC}/security-group-ids")) && IFS=, && echo "\${SGS[*]}")
          EOS
          elif [ \${provider} == "azure" ]; then
              cat <<EOS >> /tmp/cm.env
          AZURE_REGION=\$(curl -s -m 15 -H Metadata:true --noproxy "*" "http://169.254.169.254/metadata/instance/compute/location?api-version=2017-08-01&format=text")
          AZURE_RESOURCE_GROUP=\$(curl -s -m 15 -H Metadata:true --noproxy "*" "http://169.254.169.254/metadata/instance/compute/resourceGroupName?api-version=2017-08-01&format=text")
          AZURE_SUBSCRIPTION_ID=\$(curl -s -m 15 -H Metadata:true --noproxy "*" "http://169.254.169.254/metadata/instance/compute/subscriptionId?api-version=2017-08-01&format=text")
          EOS
          elif [ \${provider} == "gcp" ]; then
              cat <<EOS >> /tmp/cm.env
          GCP_PROJECT_ID=\$(curl -s -m 15 -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/project/project-id)
          GCP_ZONE=\$(curl -s -m 15 -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/zone | awk -F/ '{print \$(NF)}')
          EOS
          else
              echo "Unknown provider: \"\${provider}\"" && exit 0
          fi
          cat /tmp/cm.env
          oc create cm peer-pods-cm --from-env-file=/tmp/cm.env -n openshift-sandboxed-containers-operator
EOF
    ${CLI} wait --for=condition=complete --timeout=120s job/${name} -n openshift-sandboxed-containers-operator > /dev/null
    mapfile -t cm_vals < <(${CLI} get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.data}' | jq -r 'to_entries[] | "\(.key)=\(.value)"')
    exportVars ${cm_vals[@]}
}

function getLocalDefaults() {
    # common
    CLOUD_PROVIDER=${CLOUD_PROVIDER:-${cld}}
    VXLAN_PORT=${VXLAN_PORT:-9000}
    PROXY_TIMEOUT=${PROXY_TIMEOUT:-5m}
    DISABLECVM=${DISABLECVM:-true}

    # aws
    PODVM_INSTANCE_TYPE=${PODVM_INSTANCE_TYPE:-t3.medium}
    PODVM_INSTANCE_TYPES=${PODVM_INSTANCE_TYPES:-t2.small,t2.medium,t3.large}
    #PODVM_AMI_ID=${PODVM_AMI_ID}

    # azure
    local cloud_conf=$(oc get configmap cloud-conf -n openshift-cloud-controller-manager -o jsonpath='{.data.cloud\.conf}')
    local azure_vnet_resource_group=$(echo "$cloud_conf" | grep -o '"vnetResourceGroup":"[^"]*"' | cut -d'"' -f4)
    local azure_vnet_name=$(echo "$cloud_conf" | grep -o '"vnetName":"[^"]*"' | cut -d'"' -f4)
    local azure_subnet_name=$(echo "$cloud_conf" | grep -o '"subnetName":"[^"]*"' | cut -d'"' -f4)
    local azure_resource_group=$(echo "$cloud_conf" | grep -o '"resourceGroup":"[^"]*"' | cut -d'"' -f4)
    local azure_security_group_name=$(echo "$cloud_conf" | grep -o '"securityGroupName":"[^"]*"' | cut -d'"' -f4)
    local azure_subscription_id=$(echo "$cloud_conf" | grep -o '"subscriptionId":"[^"]*"' | cut -d'"' -f4)

    AZURE_SUBNET_ID=/subscriptions/${azure_subscription_id}/resourceGroups/${azure_vnet_resource_group}/providers/Microsoft.Network/virtualNetworks/${azure_vnet_name}/subnets/${azure_subnet_name}
    AZURE_NSG_ID=/subscriptions/${azure_subscription_id}/resourceGroups/${azure_resource_group}/providers/Microsoft.Network/networkSecurityGroups/${azure_security_group_name}
    [[ "${IS_ARO}" == "yes" ]] && [[ "${IS_STS}" == "yes" ]] && export AZURE_RESOURCE_GROUP=${azure_vnet_resource_group} # user's rg needed for sts mode on ARO
    [[ "${CTYPE}" == "tdx" ]] && AZURE_INSTANCE_SIZE_default=Standard_DC2eds_v5
    [[ "${CTYPE}" == "sev" ]] && AZURE_INSTANCE_SIZE_default=Standard_DC2as_v5
    [[ "${CTYPE}" == "none" ]] && AZURE_INSTANCE_SIZE_default=Standard_B2als_v2
    AZURE_INSTANCE_SIZE=${AZURE_INSTANCE_SIZE:-${AZURE_INSTANCE_SIZE_default}}
    [[ "${DISABLECVM}" == true ]] && AZURE_INSTANCE_SIZES=${AZURE_INSTANCE_SIZES:-Standard_B2als_v2,Standard_D2as_v5,Standard_D4as_v5,Standard_D2ads_v5}
    #AZURE_IMAGE_ID=${AZURE_IMAGE_ID}

    # gcp
    GCP_MACHINE_TYPE=${GCP_MACHINE_TYPE:-n2d-standard-2}
    GCP_NETWORK=${GCP_NETWORK:-global/networks/default}
    #GCP_IMAGE_NAME=${GCP_IMAGE_NAME}

    # libvirt
    LIBVIRT_POOL=${LIBVIRT_POOL:-default}
    LIBVIRT_VOL_NAME=${LIBVIRT_VOL_NAME:-default}
    LIBVIRT_DIR_NAME=${LIBVIRT_DIR_NAME:-default}
}

function userVerification() {
    echo && echo "###### Setting Values (press Enter for the [suggested] value, \"drop\" to remove key)"
    verifyAndSetVars "${common_vars[@]}"
    case ${CLOUD_PROVIDER} in
        "aws")
            verifyAndSetVars "${aws_vars[@]}"
            verifyAndSetVars "${aws_optional[@]}"
            ;;
        "azure")
            verifyAndSetVars "${azure_vars[@]}"
            verifyAndSetVars "${azure_optional[@]}"
            ;;
        "gcp")
            verifyAndSetVars "${gcp_vars[@]}"
            verifyAndSetVars "${gcp_optional[@]}"
            ;;
        "libvirt")
            verifyAndSetVars "${libvirt_vars[@]}"
            verifyAndSetVars "${libvirt_optional[@]}"
            ;;
        *)
            error_exit "Invalid provider";;
    esac
    echo "Cloud Provider is ${CLOUD_PROVIDER}"
    verifyAndSetVars "${custom_vars[@]}"
}

function applyCM() {
    echo && echo "###### Applying"
    [[ -n $YES ]] || (read -r -p "Apply the Changes to the peer-pods-cm ConfigMap? [y/N] " && [[ "$REPLY" =~ ^[Yy]$ ]]) || exit 0
    ${CLI} delete cm peer-pods-cm -n openshift-sandboxed-containers-operator > /dev/null 2>&1
    ${CLI} create cm peer-pods-cm --from-env-file=${ENV_FILE} -n openshift-sandboxed-containers-operator
    until ${CLI} get cm peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1 >/dev/null ; do
       echo "Waiting for ConfigMap to be created..."
       sleep 1
    done
    ${CLI} get cm peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.data}' | jq
    if ${CLI} get ds/peerpodconfig-ctrl-caa-daemon -n openshift-sandboxed-containers-operator > /dev/null 2>&1; then
        [[ -n $YES ]] || (read -r -p "Restart DaemonSet so that CM will be taken into account? [y/N] " && [[ "$REPLY" =~ ^[Yy]$ ]]) || exit 0
        ${CLI} set env ds/peerpodconfig-ctrl-caa-daemon -n openshift-sandboxed-containers-operator REBOOT="$(date)"
    fi
    echo && echo "###### Done"
}

function infraChecks() {
    oc get clusters.aro.openshift.io cluster &> /dev/null && IS_ARO=yes
    [[ "${IS_ARO}" == "yes" ]] && echo "(ARO cluster)"
    [[ $(oc get cloudcredential cluster -o jsonpath='{.spec.credentialsMode}') == "Manual" && -n $(oc get authentication.config.openshift.io cluster -o jsonpath='{.spec.serviceAccountIssuer}') ]] && IS_STS=yes && echo "(STS mode)"
}

function initialization() {
    CLI=$(command -v oc) || CLI=$(command -v kubectl) || error_exit "Missing k8s client"

    command -v jq > /dev/null || error_exit "jq is required"

    ${CLI} cluster-info &> /dev/null || error_exit "No reachable cluster"

    ${CLI} get ns openshift-sandboxed-containers-operator &> /dev/null || error_exit "Namespace doesn't exist yet, install OSC first"

    # TODO: allow also k8s clusters
    cld=$(${CLI} get infrastructure -n cluster -o jsonpath='{.items[*].status.platformStatus.type}' | awk '{print tolower($0)}' | tr -d '"' ) && cld=${cld//none/libvirt}
    echo "Cluster infrastructure is ${cld}"

    infraChecks

    echo "Env file: ${ENV_FILE}"
}

### Entrypoint

initialization

if [ "$cld" != "libvirt" ]; then
    getIMDSDefaults
else
    echo "Provider is libvirt, skipping getIMDSDefaults."
fi

getLocalDefaults

userVerification

applyCM
