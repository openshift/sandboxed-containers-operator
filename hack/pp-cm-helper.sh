#!/bin/bash

ENV_FILE=/tmp/cm.env
IS_ARO=false

echo "##### OSC ConfigMap Configurator #####"

# Loop through all the arguments
while getopts ":yc:C" opt; do
    case ${opt} in
        y ) export YES=true;;
        c ) custom_vars=(${OPTARG});;
        C ) DISABLECVM=false;;
        \? ) echo "Invalid option: -$OPTARG" >&2 && exit 1;;
    esac
done

# Expected Configuration Keys
common_vars=("CLOUD_PROVIDER" "VXLAN_PORT" "PROXY_TIMEOUT" "DISABLECVM")

aws_vars=("PODVM_INSTANCE_TYPE" "PODVM_INSTANCE_TYPES" "AWS_REGION" "AWS_SUBNET_ID" "AWS_VPC_ID" "AWS_SG_IDS")
aws_optional=("PODVM_AMI_ID")

azure_vars=("AZURE_INSTANCE_SIZE" "AZURE_INSTANCE_SIZES" "AZURE_SUBNET_ID" "AZURE_NSG_ID" "AZURE_REGION" "AZURE_RESOURCE_GROUP")
azure_optional=("AZURE_IMAGE_ID")


#### Functions

error_exit() {
    echo "Error: $1" >&2
    exit 1
}

function verifyAndSetVars() {
   arr=("$@")
   for i in "${arr[@]}"; do
       local varName="$i"
       local varValue=${!i}
       local userInput
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
          else
              echo "Uknown provider: \"\${provider}\"" && exit 0
          fi
          cat /tmp/cm.env
          oc create cm peer-pods-cm --from-env-file=/tmp/cm.env -n openshift-sandboxed-containers-operator
EOF
    ${CLI} wait --for=condition=complete --timeout=120s job/${name} -n openshift-sandboxed-containers-operator > /dev/null
    export $(${CLI} get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.data}' | jq -r 'to_entries | .[] | "\(.key)=\(.value)"')
    #${CLI} delete job/${name} -n openshift-sandboxed-containers-operator
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
    if [[ "${IS_ARO}" == "yes" ]]; then
        [[ ! ${AZURE_SUBNET_ID} ]] && [[ ${AZURE_SUBSCRIPTION_ID} ]] && net_rg=$(oc get infrastructure/cluster -o jsonpath='{.status.platformStatus.azure.networkResourceGroupName}') && [[ ${net_rg} ]] && \
            AZURE_SUBNET_ID="/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${net_rg}/providers/Microsoft.Network/virtualNetworks/aro-vnet/subnets/worker-subnet"
        [[ ! ${AZURE_NSG_ID} ]] && [[ ${AZURE_SUBSCRIPTION_ID} ]] && [[ ${AZURE_RESOURCE_GROUP} ]] && infra_name=$(oc get infrastructure/cluster -o jsonpath='{.status.infrastructureName}') && [[ ${infra_name} ]] && \
            AZURE_NSG_ID="/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${AZURE_RESOURCE_GROUP}/providers/Microsoft.Network/networkSecurityGroups/${infra_name}-nsg"
    else # self managed azure
        [[ ! ${AZURE_SUBNET_ID} ]] && [[ ${AZURE_SUBSCRIPTION_ID} ]] && [[ ${AZURE_RESOURCE_GROUP} ]] && \
            AZURE_SUBNET_ID="/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${AZURE_RESOURCE_GROUP}/providers/Microsoft.Network/virtualNetworks/${AZURE_RESOURCE_GROUP%-rg}-vnet/subnets/${AZURE_RESOURCE_GROUP%-rg}-worker-subnet"
        [[ ! ${AZURE_NSG_ID} ]] && [[ ${AZURE_SUBSCRIPTION_ID} ]] && [[ ${AZURE_RESOURCE_GROUP} ]] && \
            AZURE_NSG_ID="/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${AZURE_RESOURCE_GROUP}/providers/Microsoft.Network/networkSecurityGroups/${AZURE_RESOURCE_GROUP%-rg}-nsg"
    fi
    [[ "${DISABLECVM}" == true ]] && AZURE_INSTANCE_SIZE=${AZURE_INSTANCE_SIZE:-Standard_B2als_v2} || AZURE_INSTANCE_SIZE=${AZURE_INSTANCE_SIZE:-Standard_DC2as_v5}
    [[ "${DISABLECVM}" == true ]] && AZURE_INSTANCE_SIZES=${AZURE_INSTANCE_SIZES:-Standard_B2als_v2,Standard_D2as_v5,Standard_D4as_v5,Standard_D2ads_v5}
    #AZURE_IMAGE_ID=${AZURE_IMAGE_ID}
}

function userVerification() {
    echo && echo "###### Setting Values (press Enter for the [suggested] value, \"drop\" to remove key)"
    verifyAndSetVars "${common_vars[@]}"
    case $cld in
        "aws")
            verifyAndSetVars "${aws_vars[@]}"
            verifyAndSetVars "${aws_optional[@]}"
            ;;
        "azure")
            verifyAndSetVars "${azure_vars[@]}"
            verifyAndSetVars "${azure_optional[@]}"
            ;;
        *)
            error_exit "Invalid provider";;
    esac
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
        [[ -n $YES ]] || (read -r -p "Restart DeamonSet so that CM will be taken into account? [y/N] " && [[ "$REPLY" =~ ^[Yy]$ ]]) || exit 0
        ${CLI} set env ds/peerpodconfig-ctrl-caa-daemon -n openshift-sandboxed-containers-operator REBOOT="$(date)"
    fi
    echo && echo "###### Done"
}

function initialization() {
    CLI=$(command -v oc) || CLI=$(command -v kubectl) || error_exit "Missing k8s client"

    command -v jq > /dev/null || error_exit "jq is required"

    ${CLI} cluster-info &> /dev/null || error_exit "No reachable cluster"

    ${CLI} get ns openshift-sandboxed-containers-operator &> /dev/null || error_exit "Namespace doesn't exist yet, install OSC first"

    # TODO: allow also k8s clusters
    cld=$(${CLI} get infrastructure -n cluster -o jsonpath='{.items[*].status.platformStatus.type}' | awk '{print tolower($0)}' | tr -d '"' ) && cld=${cld//none/libvirt}
    oc get clusters.aro.openshift.io cluster &> /dev/null && IS_ARO=yes # mark as aro
    echo "Cloud Provider is ${cld}" && [[ "${IS_ARO}" == "yes" ]] && echo "(ARO cluster)"

    rm -f ${ENV_FILE}
}

### Entrypoint

initialization

getIMDSDefaults

getLocalDefaults

userVerification

applyCM
