#!/bin/bash

ACTION=$1
SSH_KEY_PASSPHRASE=$2

# Define namespace and secret name
NAMESPACE="openshift-sandboxed-containers-operator"
SECRET_NAME="peer-pods-secret"

# Retrieve KVM host details from secret ocp-libvirt-secret which will be defined by user/admin.
if [ -z "$KVM_HOST_ADDRESS" ]; then
    KVM_HOST_ADDRESS=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.LPAR_IP}" | base64 --decode
    )
fi

if [ -z "$KVM_HOST_USERNAME" ]; then
    KVM_HOST_USERNAME=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.LPAR_USER}" | base64 --decode
    )
fi

if [ -z "$KVM_HOST_PASSWORD" ]; then
    KVM_HOST_PASSWORD=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.LPAR_PSWD}" | base64 --decode
    )
fi

if [ -z "$HOST_KEY_CERTS" ]; then
    HOST_KEY_CERTS=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.HOST_KEY_CERTS}" | base64 --decode
    )
fi

if [ -z "$REDHAT_OFFLINE_TOKEN" ]; then
    REDHAT_OFFLINE_TOKEN=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.REDHAT_OFFLINE_TOKEN}" | base64 --decode
    )
fi

# Check if essential KVM host information is available
if [ -z "$KVM_HOST_ADDRESS" ] || [ -z "$KVM_HOST_USERNAME" ] || \
   [ -z "$KVM_HOST_PASSWORD" ]; then
    echo "Error: KVM host IP or credentials are missing."
    exit 1
fi

# Retrieve or create libvirt pool and volume names
if [ -z "$LIBVIRT_POOL" ]; then
    LIBVIRT_POOL=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.LIBVIRT_POOL}" | base64 --decode
    )
    if [ -z "$LIBVIRT_POOL" ]; then
        LIBVIRT_POOL="pool-auto-$(date +"%Y%m%d%H%M%S")"
    fi
fi

if [ -z "$LIBVIRT_VOL_NAME" ]; then
    LIBVIRT_VOL_NAME=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.LIBVIRT_VOLUME}" | base64 --decode
    )
    if [ -z "$LIBVIRT_VOL_NAME" ]; then
        LIBVIRT_VOL_NAME="vol-auto-$(date +"%Y%m%d%H%M%S")"
    fi
fi

# Set the libvirt pool directory
if [ -z "$LIBVIRT_POOL_FOLDER" ]; then
    LIBVIRT_POOL_FOLDER=$(
        oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
        -o jsonpath="{.data.LIBVIRT_VOL_DIRECTORY}" | base64 --decode
    )
    LIBVIRT_POOL_DIRECTORY="/var/lib/libvirt/images/$LIBVIRT_POOL_FOLDER"
    if [ -z "$LIBVIRT_POOL_FOLDER" ]; then
        LIBVIRT_POOL_DIRECTORY="/var/lib/libvirt/images/dir-auto-$(date +"%Y%m%d%H%M%S")"
    fi
else 
    LIBVIRT_POOL_DIRECTORY="/var/lib/libvirt/images/$LIBVIRT_POOL_FOLDER"
fi

# Define secret YAML from secret ocp-libvirt-secret which will be created by user/admin prior.
# if pre-built image/ image pull feature is being used Se_boot & related required flags will not be added.  
create_secret_yaml() {
    HOST_KEY_CERTS_CONTENT=$(echo "$HOST_KEY_CERTS" | sed 's/^/    /')
    if [ -v PODVM_IMAGE_URI ] && [ -n "$PODVM_IMAGE_URI" ]; then
        SECRET_YAML=$(cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: "$SECRET_NAME"
  namespace: "$NAMESPACE"
type: Opaque
stringData:
  CLOUD_PROVIDER: "$CLOUD_PROVIDER"
  LIBVIRT_URI: "$LIBVIRT_URI"
  LIBVIRT_POOL: "$LIBVIRT_POOL"
  LIBVIRT_VOL_NAME: "$LIBVIRT_VOL_NAME"
EOF
        )
    else
        SECRET_YAML=$(cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: "$SECRET_NAME"
  namespace: "$NAMESPACE"
type: Opaque
stringData:
  CLOUD_PROVIDER: "$CLOUD_PROVIDER"
  LIBVIRT_URI: "$LIBVIRT_URI"
  LIBVIRT_POOL: "$LIBVIRT_POOL"
  LIBVIRT_VOL_NAME: "$LIBVIRT_VOL_NAME"
  REDHAT_OFFLINE_TOKEN: "$REDHAT_OFFLINE_TOKEN"
  HOST_KEY_CERTS: |
$HOST_KEY_CERTS_CONTENT
EOF
        )
    fi
}

# Function to create the LIBVIRT_PODVM_IMAGE_CM
create_libvirt_podvm_image_cm() {
    if [ -v PODVM_IMAGE_URI ] && [ -n "$PODVM_IMAGE_URI" ]; then
        LIBVIRT_PODVM_IMAGE_CM=$(cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: libvirt-podvm-image-cm
  namespace: $NAMESPACE
data:
  PODVM_DISTRO: "$PODVM_DISTRO" 
  CAA_SRC: "$CAA_SRC"
  CAA_REF: "$CAA_REF"
  DOWNLOAD_SOURCES: "$DOWNLOAD_SOURCES"
  CONFIDENTIAL_COMPUTE_ENABLED: "$CONFIDENTIAL_COMPUTE_ENABLED"
  UPDATE_PEERPODS_CM: "$UPDATE_PEERPODS_CM"
  ORG_ID: "$ORG_ID"
  ACTIVATION_KEY: "$ACTIVATION_KEY"
  BASE_OS_VERSION: "$BASE_OS_VERSION"
  PODVM_IMAGE_URI: "$PODVM_IMAGE_URI"
EOF
        )
    else
        LIBVIRT_PODVM_IMAGE_CM=$(cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: libvirt-podvm-image-cm
  namespace: $NAMESPACE
data:
  PODVM_DISTRO: "$PODVM_DISTRO"
  CAA_SRC: "$CAA_SRC"
  CAA_REF: "$CAA_REF"
  DOWNLOAD_SOURCES: "$DOWNLOAD_SOURCES"
  CONFIDENTIAL_COMPUTE_ENABLED: "$CONFIDENTIAL_COMPUTE_ENABLED"
  UPDATE_PEERPODS_CM: "$UPDATE_PEERPODS_CM"
  ORG_ID: "$ORG_ID"
  ACTIVATION_KEY: "$ACTIVATION_KEY"
  BASE_OS_VERSION: "$BASE_OS_VERSION"
  SE_BOOT: "$SE_BOOT"  # Include SE_BOOT when PODVM_IMAGE_URI is present
  PODVM_IMAGE_URI: "$PODVM_IMAGE_URI"
EOF
        )
    fi
}

# Define ConfigMap for peer pods settings
CONFIGMAP_YAML=$(cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: peer-pods-cm
  namespace: openshift-sandboxed-containers-operator
data:
  CLOUD_PROVIDER: "$CLOUD_PROVIDER"
  PROXY_TIMEOUT: "15m"
EOF
)

install_sshpass() {
    if ! command -v sshpass &> /dev/null; then
        echo "Installing sshpass..."
         dnf install -y sshpass
    fi
}

# Function to generate SSH keys and copy them to the KVM host
# this is required to establish connection with KVM host for managing libvirt resources
generate_ssh_keys() {
    ssh-keygen -t rsa -b 4096 -f /root/.ssh/id_rsa \
        -N "${SSH_KEY_PASSPHRASE}" >/dev/null 2>&1
    
    sshpass -p "${KVM_HOST_PASSWORD}" ssh-copy-id \
        -o StrictHostKeyChecking=no "${KVM_HOST_USERNAME}@${KVM_HOST_ADDRESS}" \
        >/dev/null 2>&1

    oc create secret generic ssh-key-secret \
        -n openshift-sandboxed-containers-operator \
        --from-file=id_rsa.pub=/root/.ssh/id_rsa.pub \
        --from-file=id_rsa=/root/.ssh/id_rsa

    echo "SSH keypair generated and copied to ${KVM_HOST_ADDRESS}, secret created with pub & private keys."
}

# Function to create a libvirt pool and volume on the KVM host, and sync SSH keys
create_pool_volume_on_kvm_and_sync_sshid() {
    local kvm_host_user="$1"
    local kvm_host_address="$2"
    local libvirt_pool="$3"
    local libvirt_vol_name="$4"
    local libvirt_pool_directory="$5"

    echo "Creating pool and volume on KVM host '${kvm_host_address}'..."
    ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
        "${KVM_HOST_USERNAME}@${kvm_host_address}" << EOF
        sudo mkdir "$libvirt_pool_directory"
        sudo virsh pool-define-as "${libvirt_pool}" --type dir \
            --target "$libvirt_pool_directory"
        sudo virsh pool-start "${libvirt_pool}"
        
        sudo virsh -c qemu:///system vol-create-as --pool "${libvirt_pool}" \
            --name "${libvirt_vol_name}" --capacity 20G --allocation 2G \
            --prealloc-metadata --format qcow2
        
        sudo cat /home/$kvm_host_user/.ssh/authorized_keys | \
            sudo tee -a /root/.ssh/authorized_keys > /dev/null 2>&1
EOF
}

# checking if the libvirt pool and volume already exist
check_pool_and_volume_existence() {
    local kvm_host_address="$1"
    local libvirt_pool="$2"
    local libvirt_vol_name="$3"

    echo "Checking existence of libvirt pool '${libvirt_pool}' and volume '${libvirt_vol_name}' on KVM host '${kvm_host_address}'..."
    ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
        "${KVM_HOST_USERNAME}@${kvm_host_address}" << EOF
        sudo virsh pool-info "${libvirt_pool}" >/dev/null 2>&1
        POOL_EXISTS=\$?
        sudo virsh vol-info --pool "${libvirt_pool}" "${libvirt_vol_name}" \
            >/dev/null 2>&1
        VOL_EXISTS=\$?

        if [ "\$POOL_EXISTS" -eq 0 ] && [ "\$VOL_EXISTS" -eq 0 ]; then
            echo "A Libvirt pool named '${libvirt_pool}' with volume '${libvirt_vol_name}' already exists on the KVM host. Please choose a different name."
            exit 0
        else
            echo "Libvirt pool '${libvirt_pool}' or volume '${libvirt_vol_name}' does not exist. Proceeding to create..."
        fi
EOF
}

# creating the ConfigMap and secret.
create_configMap_and_secret() {
    create_secret_yaml
    create_libvirt_podvm_image_cm
    echo "$SECRET_YAML" | oc apply -f -
    echo "$CONFIGMAP_YAML" | oc apply -f -
    echo "$LIBVIRT_PODVM_IMAGE_CM" | oc apply -f -
}

# Cleaning up resources (volumes, pools, secrets, configmaps based on secrets availabel in OSC namespace)
cleanup() {
    echo "Cleaning up..."

    # Attempting to retrieve LIBVIRT_POOL and LIBVIRT_VOL_NAME from the secret
    LIBVIRT_POOL=$(
        oc get secret "$SECRET_NAME" -n "$NAMESPACE" \
        -o jsonpath="{.data.LIBVIRT_POOL}" | base64 --decode 2>/dev/null
    )

    LIBVIRT_VOL_NAME=$(
        oc get secret "$SECRET_NAME" -n "$NAMESPACE" \
        -o jsonpath="{.data.LIBVIRT_VOL_NAME}" | base64 --decode 2>/dev/null
    )

    # Checking if the values were successfully retrieved; if not, retrieving from secret ocp-libvirt-secret
    if [ -z "$LIBVIRT_POOL" ] || [ -z "$LIBVIRT_VOL_NAME" ]; then
        echo "Values not found in $SECRET_NAME. Checking ocp-libvirt-secret..."

        LIBVIRT_POOL=$(
            oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
            -o jsonpath="{.data.LIBVIRT_POOL}" | base64 --decode 2>/dev/null
        )

        LIBVIRT_VOL_NAME=$(
            oc get secret ocp-libvirt-secret -n "$NAMESPACE" \
            -o jsonpath="{.data.LIBVIRT_VOLUME}" | base64 --decode 2>/dev/null
        )
    fi
    # Ensuring values are retrieved
    if [ -z "$LIBVIRT_POOL" ] || [ -z "$LIBVIRT_VOL_NAME" ]; then
        echo "Error: Both LIBVIRT_POOL and LIBVIRT_VOL_NAME could not be retrieved from either secret."
        exit 1
    fi

    if ! ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
        "${KVM_HOST_USERNAME}@${KVM_HOST_ADDRESS}" sudo virsh pool-info "$LIBVIRT_POOL" >/dev/null 2>&1; then
        echo "Pool '$LIBVIRT_POOL' does not exist on KVM host."
        return
    fi

    VOLUMES=$(
        ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
        "${KVM_HOST_USERNAME}@${KVM_HOST_ADDRESS}" sudo virsh vol-list "$LIBVIRT_POOL" | \
        awk 'NR>2 {print $1}'
    )
    if [ "$VOLUMES" == "$LIBVIRT_VOL_NAME" ]; then
        echo "Volume '$LIBVIRT_VOL_NAME' is the only volume in the pool. Deleting the volume."

        if ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
            "${KVM_HOST_USERNAME}@${KVM_HOST_ADDRESS}" \
            sudo virsh vol-delete "$LIBVIRT_VOL_NAME" --pool "$LIBVIRT_POOL"; then
            echo "Volume '$LIBVIRT_VOL_NAME' deleted successfully."

            VOLUMES=$(
                ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
                "${KVM_HOST_USERNAME}@${KVM_HOST_ADDRESS}" sudo virsh vol-list "$LIBVIRT_POOL" | \
                awk 'NR>2 {print $1}'
            )
            if [ -z "$VOLUMES" ]; then
                echo "No volumes found in the pool. Proceeding to delete the pool & libvirt directory"
                if ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
                    "${KVM_HOST_USERNAME}@${KVM_HOST_ADDRESS}" sudo virsh pool-destroy "$LIBVIRT_POOL"; then
                    echo "Pool '$LIBVIRT_POOL' destroyed successfully."
                    if ssh -i /root/.ssh/id_rsa -o StrictHostKeyChecking=no \
                        "${KVM_HOST_USERNAME}@${KVM_HOST_ADDRESS}" sudo virsh pool-undefine "$LIBVIRT_POOL"; then
                        echo "Pool '$LIBVIRT_POOL' undefined successfully."
                    else
                        echo "Failed to undefine the pool '$LIBVIRT_POOL'."
                    fi
                else
                    echo "Failed to destroy the pool '$LIBVIRT_POOL'."
                fi
                sudo rm -rf "$LIBVIRT_POOL_DIRECTORY" 2>/dev/null || \
                    echo "Directory '${LIBVIRT_POOL_DIRECTORY}' could not be removed."

            else
                echo "Error: Volume '$LIBVIRT_VOL_NAME' was deleted, but other volumes remain in the pool."
            fi
        else
            echo "Failed to delete the volume '$LIBVIRT_VOL_NAME'."
        fi
    else
        echo "Volume '$LIBVIRT_VOL_NAME' is not the only volume in the pool. Not deleting the volume or pool."
        echo "Volumes in the pool:"
        echo "$VOLUMES"
    fi

    oc delete secret ssh-key-secret -n "$NAMESPACE"
    oc delete configmap peer-pods-cm -n "$NAMESPACE"
    oc delete secret "$SECRET_NAME" -n "$NAMESPACE"
    oc delete configmap "libvirt-podvm-image-cm" -n "$NAMESPACE"

    echo "Cleanup completed."
}

if [ "$ACTION" == "create" ]; then
    install_sshpass
    generate_ssh_keys
    check_pool_and_volume_existence "${KVM_HOST_ADDRESS}" "${LIBVIRT_POOL}" "${LIBVIRT_VOL_NAME}"
    create_pool_volume_on_kvm_and_sync_sshid "${KVM_HOST_USERNAME}" "${KVM_HOST_ADDRESS}" \
        "${LIBVIRT_POOL}" "${LIBVIRT_VOL_NAME}" "${LIBVIRT_POOL_DIRECTORY}"
    create_configMap_and_secret
elif [ "$ACTION" == "clean" ]; then
    install_sshpass
    generate_ssh_keys
    cleanup
else
    echo "Invalid action. Please use 'create' or 'clean'."
    exit 1
fi
