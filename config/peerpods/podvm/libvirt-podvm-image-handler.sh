#!/bin/bash
# FILEPATH: libvirt-podvm-image-handler.sh

# This script is used to create or delete libvirt qcow2 image for podvm
# The basic assumption is that the required variables are set as environment variables in the pod
# Typically the variables are read from configmaps and set as environment variables in the pod
# The script will be called with one of the following options:
# Create image (-c)
# Delete image (-C)

set -x
# include common functions from lib.sh
# shellcheck source=/dev/null
# The directory is where libvirt-podvm-image-handler.sh is located
source /scripts/lib.sh

# Function to verify that the required variables are set

function verify_vars() {
    # Ensure CLOUD_PROVIDER is set to libvirt
    [[ -z "${CLOUD_PROVIDER}" || "${CLOUD_PROVIDER}" != "libvirt" ]] && error_exit "CLOUD_PROVIDER is empty or not set to libvirt"

    [[ -z "${ORG_ID}" ]] && error_exit "ORG_ID is not set"
    [[ -z "${ACTIVATION_KEY}" ]] && error_exit "ACTIVATION_KEY is not set"
    [[ -z "${BASE_OS_VERSION}" ]] && error_exit "BASE_OS_VERSION is not set"

    [[ -z "${PODVM_DISTRO}" ]] && error_exit "PODVM_DISTRO is not set"

    [[ -z "${CAA_SRC}" ]] && error_exit "CAA_SRC is empty"
    [[ -z "${CAA_REF}" ]] && error_exit "CAA_REF is empty"

    [[ -z "${REDHAT_OFFLINE_TOKEN}" ]] && error_exit "Redhat token is not set"

    # Ensure booleans are set
    [[ -z "${DOWNLOAD_SOURCES}" ]] && error_exit "DOWNLOAD_SOURCES is empty"
}



function create_libvirt_image() {
    echo "Creating libvirt qcow2 image"

    # If any error occurs, exit the script with an error message

    if [[ "${DOWNLOAD_SOURCES}" == "yes" ]]; then
        # Download source code from GitHub
        download_source_code
    fi

    # Prepare the source code for building the libvirt
    prepare_source_code

    # Dowload the base rhel image for packer
    download_rhel_kvm_guest_qcow2

    # Prepare the pause image for embedding into the libvirt image
    download_and_extract_pause_image "${PAUSE_IMAGE_REPO}" "${PAUSE_IMAGE_VERSION}" "${PAUSE_IMAGE_REPO_AUTH_FILE}" 

    cd "${CAA_SRC_DIR}"/podvm || \
	    error_exit "Failed to change directory to "${CAA_SRC_DIR}"/podvm"
    LIBC=gnu make BINARIES= PAUSE_BUNDLE= image

    export PODVM_IMAGE_PATH=/payload/podvm-libvirt.qcow2
    cp -pr "${CAA_SRC_DIR}"/podvm/output/*.qcow2 "${PODVM_IMAGE_PATH}"

    # Upload the created qcow2 to the volume
    upload_libvirt_image

    # Add the libvirt_volume_name to peer-pods-cm configmap
    add_libvirt_vol_to_peer_pods_cm
}

# Function to dowload the rhel base image

function download_rhel_kvm_guest_qcow2() {
    ARCH=$(uname -m)
    export ARCH

    # Define the API endpoints
    TOKEN_GENERATOR_URI=https://sso.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token
    IMAGES_URI=https://api.access.redhat.com/management/v1/images/rhel/"${BASE_OS_VERSION}"/"${ARCH}"

    filename="rhel-"${BASE_OS_VERSION}"-"${ARCH}"-kvm.qcow2"

    token=$(curl "${TOKEN_GENERATOR_URI}" \
        -d grant_type=refresh_token -d client_id=rhsm-api -d refresh_token="${REDHAT_OFFLINE_TOKEN}" | jq --raw-output .access_token)
    images=$(curl -X 'GET' "${IMAGES_URI}" \
        -H 'accept: application/json' -H "Authorization: Bearer "${token}"" | jq )

    download_href=$(echo "${images}" | jq -r --arg fn "${filename}" '.body[] | select(.filename == $fn) | .downloadHref')

    download_url=$(curl -X 'GET' "${download_href}" \
        -H "Authorization: Bearer "${token}"" -H 'accept: application/json' | jq -r .body.href )

    curl -X GET "${download_url}" -H "Authorization: Bearer "${token}"" --output rhel-"${BASE_OS_VERSION}"-"${ARCH}"-kvm.qcow2

    cp -pr rhel-"${BASE_OS_VERSION}"-"${ARCH}"-kvm.qcow2 "${CAA_SRC_DIR}"/podvm/rhel-"${BASE_OS_VERSION}"-"${ARCH}"-kvm.qcow2

    export IMAGE_URL="${CAA_SRC_DIR}"/podvm/rhel-"${BASE_OS_VERSION}"-"${ARCH}"-kvm.qcow2
    export IMAGE_CHECKSUM=$(sha256sum "${IMAGE_URL}" | awk '{ print $1 }')

}

# Function to upload the qcow2 image to volume

function upload_libvirt_image() {
    echo "LIBVIRT_VOL_NAME: "${LIBVIRT_VOL_NAME}"" && echo "LIBVIRT_POOL: "${LIBVIRT_POOL}"" && \
	    echo "LIBVIRT_URI: "${LIBVIRT_URI}"" && echo "PODVM_IMAGE_PATH: "${PODVM_IMAGE_PATH}"" 
    echo "Starting to upload the image."
    virsh -d 0 -c "${LIBVIRT_URI}" vol-upload --vol "${LIBVIRT_VOL_NAME}" "${PODVM_IMAGE_PATH}" --pool "${LIBVIRT_POOL}" --sparse
    if [ $? -eq 0 ]; then
        echo "Uploaded the image successfully"
    fi
}

# Function to add the libvirt_volume_name in the peer-pods-cm configmap

function add_libvirt_vol_to_peer_pods_cm(){
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then

        # Check if the peer-pods-cm configmap exists
        if ! kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
            echo "peer-pods-cm configmap does not exist. Skipping adding the libvirt volume name"
            return
        fi

        # Add the libvirt image id to peer-pods-cm configmap
        echo "Updating peer-pods-cm configmap with LIBVIRT_IMAGE_ID=${LIBVIRT_VOL_NAME}"
        kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator \
        --type merge -p "{\"data\":{\"LIBVIRT_IMAGE_ID\":\"${LIBVIRT_VOL_NAME}\"}}" ||
            error_exit "Failed to add the libvirt image id to peer-pods-cm configmap"
    fi
}

# function to delete the libvirt
# LIBVIRT_POOL name must be set as an environment variable

function delete_libvirt_image() {
    echo "Deleting Libvirt image"

    # Delete the Libvirt pool 
    # If any error occurs, exit the script with an error message

    # LIBVIRT_POOL shouldn't be empty
    [[ -z "${LIBVIRT_POOL}" ]] && error_exit "LIBVIRT_POOL is empty"

    # Delete the libvirt pool
    echo "Deleting libvirt pool."
    virsh -d 0 -c "${LIBVIRT_URI}" pool-destroy "${LIBVIRT_POOL}" ||
        error_exit "Failed to destroy the libvirt pool"
    
    virsh -d 0 -c "${LIBVIRT_URI}" pool-undefine "${LIBVIRT_POOL}" ||
        error_exit "Failed to undefine the libvirt pool"

    echo "Deleted libvirt image successfully"

    # Remove the libvirt_volume_name from peer-pods-cm configmap
    delete_libvirt_vol_from_peer_pods_cm

}

# Function to delete the libvirt image id  from the peer-pods-cm configmap

function delete_libvirt_vol_from_peer_pods_cm() {
    echo "Deleting libvirt volume from peer-pods-cm configmap"

    # Check if the peer-pods-cm configmap exists
    if ! kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
        echo "peer-pods-cm configmap does not exist. Skipping deleting the ami id"
        return
    fi

    # Delete the libvirt image id from peer-pods-cm configmap
    kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator \
    --type merge -p "{\"data\":{\"LIBVIRT_IMAGE_ID\":\"\"}}" ||
        error_exit "Failed to delete the libvirt image id from peer-pods-cm configmap"
    echo "libvirt image id deleted from peer-pods-cm configmap successfully"
}

# display help message

function display_help() {
    echo "This script is used to create libvirt qcow2 for podvm"
    echo "Usage: $0 [-c|-C] [-- install_binaries]"
    echo "Options:"
    echo "-c  Create image"
    echo "-C  Delete image"
}

function install_packages(){

    install_binary_packages

    #Install other libvirt specific binaries packages
    ARCH=$(uname -m)
    export ARCH
    if [[ -n "${ACTIVATION_KEY}" && -n "${ORG_ID}" ]]; then
        subscription-manager register --org="${ORG_ID}" --activationkey="${ACTIVATION_KEY}"
    fi
    subscription-manager repos --enable codeready-builder-for-rhel-9-"${ARCH}"-rpms
    dnf install -y gcc genisoimage qemu-kvm libvirt-client

    GO_VERSION="1.21.9"
    curl https://dl.google.com/go/go"${GO_VERSION}".linux-"${ARCH/x86_64/amd64}".tar.gz -o go"${GO_VERSION}".linux-"${ARCH/x86_64/amd64}".tar.gz && \
    rm -rf /usr/local/go && tar -C /usr/local -xzf go"${GO_VERSION}".linux-"${ARCH/x86_64/amd64}".tar.gz && \
    rm -f go"${GO_VERSION}".linux-"${ARCH/x86_64/amd64}".tar.gz
    export PATH="/usr/local/go/bin:"${PATH}""
    export GOPATH="/src"

    if [ "${ARCH}" == "s390x" ]; then
        # Build packer from source for s390x as there are no prebuilt binaries for the required packer version
        PACKER_VERSION="v1.9.4"
        git clone --depth 1 --single-branch https://github.com/hashicorp/packer.git -b "${PACKER_VERSION}"
        cd packer
        sed -i -- "s/ALL_XC_ARCH=.*/ALL_XC_ARCH=\"${ARCH}\"/g" scripts/build.sh
	    sed -i -- "s/ALL_XC_OS=.*/ALL_XC_OS=\"Linux\"/g" scripts/build.sh
        make bin && cp bin/packer /usr/local/bin/

        # Build umoci from source for s390x as there are no prebuilt binaries
        mkdir -p umoci
        git clone https://github.com/opencontainers/umoci.git
        cd umoci
        make
        cp -pr umoci /usr/local/bin/
    fi

    # set a correspond qemu-system-* named link to qemu-kvm
    ln -s /usr/libexec/qemu-kvm /usr/bin/qemu-system-"${ARCH}"

    # Build cloud-utils package from source as prebuilt binary is not available
    git clone https://github.com/canonical/cloud-utils
    cd cloud-utils && make install

}

# main function

if [ "$#" -eq 0 ]; then
    display_help
    exit 1
fi

if [ "$1" = "--" ]; then
    shift
    # Handle positional parameters
    case "$1" in

    install_binaries)
        install_packages
        ;;
    *)
        echo "Unknown argument: $1"
        exit 1
        ;;
    esac
else
    while getopts "cCh" opt; do
        verify_vars
        case ${opt} in
        c)
            # Create the libvirt image
            create_libvirt_image
            ;;
        C)
            # Delete the libvirt image
            delete_libvirt_image
            ;;
        h)
            # Display help
            display_help
            exit 0
            ;;
        *)
            # Invalid option
            display_help
            exit 1
            ;;
        esac
    done
fi
