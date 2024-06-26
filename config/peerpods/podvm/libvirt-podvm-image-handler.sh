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
# The directory is where aws-podvm-image-handler.sh is located
source /scripts/lib.sh

# Function to verify that the required variables are set

function verify_vars() {
    # Ensure CLOUD_PROVIDER is set to libvirt
    [[ -z "${CLOUD_PROVIDER}" || "${CLOUD_PROVIDER}" != "libvirt" ]] && error_exit "CLOUD_PROVIDER is empty or not set to libvirt"

    [[ -z "${ORG_ID}" ]] && error_exit "ORG_ID is not set"
    [[ -z "${ACTIVATION_KEY}" ]] && error_exit "ACTIVATION_KEY is not set"

    [[ -z "${PODVM_DISTRO}" ]] && error_exit "PODVM_DISTRO is not set"
    [[ -z "${ARCH}" ]] && error_exit "ARCH is not set"

    [[ -z "${CAA_SRC}" ]] && error_exit "CAA_SRC is empty"
    [[ -z "${CAA_REF}" ]] && error_exit "CAA_REF is empty"

    # Ensure booleans are set
    [[ -z "${INSTALL_PACKAGES}" ]] && error_exit "INSTALL_PACKAGES is empty"
    [[ -z "${DOWNLOAD_SOURCES}" ]] && error_exit "DOWNLOAD_SOURCES is empty"
}



function create_libvirt_image() {
    echo "Creating libvirt qcow2 image"

    # If any error occurs, exit the script with an error message

    # Install packages if INSTALL_PACKAGES is set to yes

    if [[ "${INSTALL_PACKAGES}" == "yes" ]]; then

        # Install required rpm packages
        install_rpm_packages

        # Install required binary packages
        install_libvirt_binary_packages
    fi

    if [[ "${DOWNLOAD_SOURCES}" == "yes" ]]; then
        # Download source code from GitHub
        download_source_code
    fi

    # Prepare the source code for building the ami
    prepare_source_code

    export PODVM_DISTRO=${PODVM_DISTRO}
    export CLOUD_PROVIDER=${CLOUD_PROVIDER}
    export ARCH=${ARCH}

    cd "${CAA_SRC_DIR}"/src/cloud-api-adaptor/podvm || \
	    error_exit "Failed to change directory to ${CAA_SRC_DIR}/src/cloud-api-adaptor/podvm"
    make image
    sleep 120s
    cp -pr /src/cloud-api-adaptor/src/cloud-api-adaptor/podvm/output/*.qcow2 /src/cloud-api-adaptor/src/cloud-api-adaptor/podvm/output/libvirt-podvm.qcow2
    sleep 120s
    #mkdir -p /src/cloud-api-adaptor/src/cloud-api-adaptor/podvm/output
    export PODVM_IMAGE_PATH=/src/cloud-api-adaptor/src/cloud-api-adaptor/podvm/output/libvirt-podvm.qcow2
    echo "LIBVIRT_VOL_NAME: $LIBVIRT_VOL_NAME" && echo "LIBVIRT_POOL: $LIBVIRT_POOL" && \
	    echo "LIBVIRT_URI: $LIBVIRT_URI" && echo "PODVM_IMAGE_PATH: $PODVM_IMAGE_PATH" 
    echo "Starting to upload the image."
    virsh -d 0 -c $LIBVIRT_URI vol-upload --vol $LIBVIRT_VOL_NAME $PODVM_IMAGE_PATH --pool $LIBVIRT_POOL --sparse
    if [ $? -eq 0 ]; then
    echo "Uploaded the image successfully" 
    LIBVIRT_POOL_UUID=$(virsh -d 0 -c $LIBVIRT_URI pool-list --name --uuid | grep $LIBVIRT_POOL | awk '{print $1}')
    echo "Updating peer-pods-cm configmap with IMAGE_ID=${LIBVIRT_POOL_UUID}" 
    else 
    echo "Couldn't upload the image." 
    fi
    sleep 120s
}


# function to delete the libvirt
# libvirt_id must be set as an environment variable

function delete_libvirt_using_id() {
    echo "Deleting Libvirt image"

    # Delete the AMI
    # If any error occurs, exit the script with an error message

    # LIBVIRT_ID shouldn't be empty
    [[ -z "${LIBVIRT_ID}" ]] && error_exit "LIBVIRT_ID is empty"

    # Delete the libvirt image
}

# display help message

function display_help() {
    echo "This script is used to create libvirt qcow2 for podvm"
    echo "Usage: $0 [-c|-C] [-- install_binaries|install_rpms|install_cli]"
    echo "Options:"
    echo "-c  Create image"
    echo "-C  Delete image"
    echo "-R Recreate podvm-images configMap"
}

function install_packages(){
    if [[ -n "${ACTIVATION_KEY}" && -n "${ORG_ID}" ]]; then \
    subscription-manager register --org=${ORG_ID} --activationkey=${ACTIVATION_KEY}; \
    fi

    subscription-manager repos --enable codeready-builder-for-rhel-9-${ARCH}-rpms; \
    dnf install -y libvirt-client; \
    dnf groupinstall -y 'Development Tools'; \
    dnf install -y yum-utils gnupg git --allowerasing curl pkg-config clang perl libseccomp-devel gpgme-devel \
    device-mapper-devel qemu-kvm unzip wget libassuan-devel genisoimage cloud-utils-growpart cloud-init;

    curl -L -o /usr/local/bin/yq https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${ARCH/x86_64/amd64} \
    && echo "${YQ_CHECKSUM#sha256:}  /usr/local/bin/yq" | sha256sum -c -

    chmod a+x /usr/local/bin/yq && \
    curl https://dl.google.com/go/go${GO_VERSION}.linux-${ARCH/x86_64/amd64}.tar.gz -o go${GO_VERSION}.linux-${ARCH/x86_64/amd64}.tar.gz && \
    rm -rf /usr/local/go && tar -C /usr/local -xzf go${GO_VERSION}.linux-${ARCH/x86_64/amd64}.tar.gz && \
    rm -f go${GO_VERSION}.linux-${ARCH/x86_64/amd64}.tar.gz

    export PATH="/usr/local/go/bin:${PATH}"

    #Check go version
    go version

# Install packer. Packer doesn't does not have prebuilt s390x arch binaries above Packer version 0.1.5 
    if [ "${ARCH}" == "s390x" ]; then \
    git clone --depth 1 --single-branch https://github.com/hashicorp/packer.git -b ${PACKER_VERSION}; \
    cd packer; \
    sed -i -- "s/ALL_XC_ARCH=.*/ALL_XC_ARCH=\"${ARCH}\"/g" scripts/build.sh; \
	sed -i -- "s/ALL_XC_OS=.*/ALL_XC_OS=\"Linux\"/g" scripts/build.sh; \
    make bin && cp bin/packer /usr/local/bin/; \
    cd $OLDPWD; \
    else \
    yum-config-manager --add-repo https://rpm.releases.hashicorp.com/RHEL/hashicorp.repo && \
    dnf install -y packer; \
    fi

    # set a correspond qemu-system-* named link to qemu-kvm
    ln -s /usr/libexec/qemu-kvm /usr/bin/qemu-system-$(uname -m)

    # cloud-utils package is not available for rhel.
    git clone https://github.com/canonical/cloud-utils
    cd cloud-utils && make install

    curl https://sh.rustup.rs -sSf | sh -s -- -y --default-toolchain "${RUST_VERSION}"

    export PATH="/root/.cargo/bin:/usr/local/go/bin:$PATH"
    export GOPATH="/src"

    wget https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${ARCH/s390x/s390_64}.zip && \
    unzip protoc-${PROTOC_VERSION}-linux-${ARCH/s390x/s390_64}.zip -d /usr/local && rm -f protoc-${PROTOC_VERSION}-linux-${ARCH/s390x/s390_64}.zip
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
    install_rpms)
        install_rpm_packages
        ;;
    *)
        echo "Unknown argument: $1"
        exit 1
        ;;
    esac
else
    while getopts "cCRh" opt; do
        verify_vars
        case ${opt} in
        c)
            # Create the ami
            create_libvirt_image
            ;;
        C)
            # Delete the ami
            delete_libvirt_using_id

            ;;
        R)
            # Recreate the podvm-images configmap
            recreate_image_configmap
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