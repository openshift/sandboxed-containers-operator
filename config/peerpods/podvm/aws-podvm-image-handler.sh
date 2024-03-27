#!/bin/bash
# FILEPATH: aws-podvm-image-handler.sh

# This script is used to create or delete AWS AMI for podvm
# The basic assumption is that the required variables are set as environment variables in the pod
# Typically the variables are read from configmaps and set as environment variables in the pod
# The script will be called with one of the following options:
# Create image (-c)
# Delete image (-C)

set -x
# include common functions from lib.sh
# shellcheck source=/dev/null
# The directory is where aws-podvm-image-handler.sh is located
source "$(dirname "$0")"/lib.sh

# Function to verify that the required variables are set

function verify_vars() {
    # Ensure CLOUD_PROVIDER is set to aws
    [[ -z "${CLOUD_PROVIDER}" || "${CLOUD_PROVIDER}" != "aws" ]] && error_exit "CLOUD_PROVIDER is empty or not set to aws"

    [[ -z "${AWS_ACCESS_KEY_ID}" ]] && error_exit "AWS_ACCESS_KEY_ID is not set"
    [[ -z "${AWS_SECRET_ACCESS_KEY}" ]] && error_exit "AWS_SECRET_ACCESS_KEY is not set"

    # Packer variables
    [[ -z "${INSTANCE_TYPE}" ]] && error_exit "INSTANCE_TYPE is not set"
    [[ -z "${PODVM_DISTRO}" ]] && error_exit "PODVM_DISTRO is not set"

    # if AWS_REGION is empty, try to get it from the metadata service
    if [[ ! "${AWS_REGION}" ]]; then
        AWS_REGION=$(curl -m 30 -s --show-error http://169.254.169.254/latest/meta-data/placement/region ||
            error_exit "Failed to get AWS_REGION from metadata service")
        export AWS_REGION
    fi
    [[ ! "${AWS_REGION}" ]] && error_exit "AWS_REGION is not set"

    # If AWS_VPC_ID is empty, try to get it from the metadata service
    if [[ ! "${AWS_VPC_ID}" ]]; then
        MAC=$(curl -m 3 -s http://169.254.169.254/latest/meta-data/mac ||
            error_exit "Failed to get MAC from metadata service")

        [[ "${MAC}" ]] && error_exit "MAC is not set"

        AWS_VPC_ID=$(curl -m 30 -s --show-error http://169.254.169.254/latest/meta-data/network/interfaces/macs/"${MAC}"/vpc-id ||
            error_exit "Failed to get AWS_VPC_ID from metadata service")
        export AWS_VPC_ID
    fi
    [[ ! "${AWS_VPC_ID}" ]] && error_exit "AWS_VPC_ID is not set"

    # If AWS_SUBNET_ID is empty, try to get it from the metadata service
    if [[ ! "${AWS_SUBNET_ID}" ]]; then
        MAC=$(curl -m 3 -s http://169.254.169.254/latest/meta-data/mac ||
            error_exit "Failed to get MAC from metadata service")

        [[ "${MAC}" ]] && error_exit "MAC is not set"

        AWS_SUBNET_ID=$(curl -m 30 -s --show-error http://169.254.169.254/latest/meta-data/network/interfaces/macs/"${MAC}"/subnet-id ||
            error_exit "Failed to get AWS_SUBNET_ID from metadata service")
        export AWS_SUBNET_ID
    fi
    [[ ! "${AWS_SUBNET_ID}" ]] && error_exit "AWS_SUBNET_ID is not set"

    [[ -z "${AMI_BASE_NAME}" ]] && error_exit "AMI_BASE_NAME is not set"
    [[ -z "${AMI_VERSION_MAJ_MIN}" ]] && error_exit "AMI_VERSION_MAJ_MIN is not set"
    [[ -z "${AMI_VOLUME_SIZE}" ]] && error_exit "AMI_VOLUME_SIZE is not set"

    [[ -z "${PODVM_DISTRO}" ]] && error_exit "PODVM_DISTRO is not set"
    [[ -z "${DISABLE_CLOUD_CONFIG}" ]] && error_exit "DISABLE_CLOUD_CONFIG is not set"

    [[ -z "${CAA_SRC}" ]] && error_exit "CAA_SRC is empty"
    [[ -z "${CAA_REF}" ]] && error_exit "CAA_REF is empty"

    # Ensure booleans are set
    [[ -z "${INSTALL_PACKAGES}" ]] && error_exit "INSTALL_PACKAGES is empty"
    [[ -z "${DOWNLOAD_SOURCES}" ]] && error_exit "DOWNLOAD_SOURCES is empty"
    [[ -z "${CONFIDENTIAL_COMPUTE_ENABLED}" ]] && error_exit "CONFIDENTIAL_COMPUTE_ENABLED is empty"
    [[ -z "${DISABLE_CLOUD_CONFIG}" ]] && error_exit "DISABLE_CLOUD_CONFIG is empty"
    [[ -z "${ENABLE_NVIDIA_GPU}" ]] && error_exit "ENABLE_NVIDIA_GPU is empty"

}

# function to download and install aws cli

function install_aws_cli() {
    # Install aws cli
    # If any error occurs, exit the script with an error message

    # Check if aws cli is already installed
    if command -v aws &>/dev/null; then
        echo "aws cli is already installed"
        return
    fi

    # Download aws cli v2
    curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "/tmp/awscliv2.zip" ||
        error_exit "Failed to download aws cli"

    # Install aws cli v2
    unzip -o "/tmp/awscliv2.zip" -d "/tmp" ||
        error_exit "Failed to unzip aws cli"
    /tmp/aws/install ||
        error_exit "Failed to install aws cli"

    # Clean up temporary files
    rm -f "/tmp/awscliv2.zip"
}

# Function to use packer to create AWS ami

function create_ami_using_packer() {
    echo "Creating AWS AMI using packer"

    # Create AWS image using packer
    # If any error occurs, exit the script with an error message
    # The variables are set before calling the function

    # Set the AMI version
    # It should follow the Major(int).Minor(int).Patch(int)
    AMI_VERSION="${AMI_VERSION_MAJ_MIN}.$(date +'%Y%m%d%S')"
    export AMI_VERSION

    # Set the image name
    AMI_NAME="${AMI_BASE_NAME}-${AMI_VERSION}"
    export AMI_NAME

    # If PODVM_DISTRO is not set to rhel then exit
    [[ "${PODVM_DISTRO}" != "rhel" ]] && error_exit "unsupport distro"

    # Set the packer variables

    export PKR_VAR_instance_type="${INSTANCE_TYPE}"
    export PKR_VAR_region="${AWS_REGION}"
    export PKR_VAR_vpc_id="${AWS_VPC_ID}"
    export PKR_VAR_subnet_id="${AWS_SUBNET_ID}"
    export PKR_VAR_ami_name="${AMI_NAME}"
    export PKR_VAR_volume_size="${AMI_VOLUME_SIZE}"

    # The makefile in aws/image directory is explicitly using these
    # TBD: fix the makefile
    export IMAGE_NAME="${AMI_NAME}"
    export VOL_SIZE="${AMI_VOLUME_SIZE}"

    cd "${CAA_SRC_DIR}"/aws/image ||
        error_exit "Failed to change directory to ${CAA_SRC_DIR}/aws/image"
    packer init "${PODVM_DISTRO}"/
    make BINARIES= PAUSE_BUNDLE= image

    # Wait for the ami to be created

}

# Function to get the ami id of the newly created image

function get_ami_id() {
    echo "Getting the image id"

    # Get the ami id of the newly created image
    # If any error occurs, exit the script with an error message

    # Get the ami id
    AMI_ID=$(aws ec2 describe-images --region "${AWS_REGION}" --filters "Name=name,Values=${AMI_NAME}" --query 'Images[*].ImageId' --output text) ||
        error_exit "Failed to get the ami id"

    # Set the image id as an environment variable
    export AMI_ID

    echo "ID of the newly created ami: ${AMI_ID}"

}

# Function to get all the ami ids for the given base name

function get_all_ami_ids() {
    echo "Getting all ami ids"

    # Get all the ami ids for the given base name
    # If any error occurs, exit the script with an error message

    # Get the ami id list
    AMI_ID_LIST=$(aws ec2 describe-images --region "${AWS_REGION}" --filters "Name=name,Values=${AMI_BASE_NAME}-*" --query 'Images[*].ImageId' --output text) ||
        error_exit "Failed to get the ami id list"

    # Set the image id list as an environment variable
    export AMI_ID_LIST

    # Display the list of amis
    echo "${AMI_ID_LIST}"

}

# Function to create or update podvm-images configmap with all the amis
# Input AMI_ID_LIST is a list of image ids

function create_or_update_image_configmap() {
    echo "Creating or updating podvm-images configmap"

    # Check if the podvm-images configmap already exists
    # If exists get the current value of the aws key and append the new ami id to it
    # If not exists, create the podvm-images configmap with the new ami id

    # Check if the podvm-images configmap exists
    if kubectl get configmap podvm-images -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
        # Get the current value of the aws key in podvm-images configmap
        AMI_ID_LIST=$(kubectl get configmap podvm-images -n openshift-sandboxed-containers-operator -o jsonpath='{.data.aws}' ||
            error_exit "Failed to get the current value of the aws key in podvm-images configmap")

        # If the current value of the aws key is empty, then set the value to the new ami id
        if [[ -z "${AMI_ID_LIST}" ]]; then
            AMI_ID_LIST="${AMI_ID}"
        else
            # If the current value of the aws key is not empty, then append the new ami id in the beginning
            # The first ami id in the list is the latest ami id
            AMI_ID_LIST="${AMI_ID} ${AMI_ID_LIST}"
        fi
    else
        # If the podvm-images configmap does not exist, set the value to the new ami id
        AMI_ID_LIST="${AMI_ID}"
    fi

    kubectl create configmap podvm-images \
        -n openshift-sandboxed-containers-operator \
        --from-literal=aws="${AMI_ID_LIST}" \
        --dry-run=client -o yaml |
        kubectl apply -f - ||
        error_exit "Failed to create or update podvm-images configmap"

}

# Funtion to recreate podvm-images configmap with all the amis

function recreate_image_configmap() {
    echo "Recreating podvm-images configmap"

    # Get list of all ami ids
    get_all_ami_ids

    # Check if IMAGE_ID_LIST is empty
    [[ -z "${AMI_ID_LIST}" ]] && error_exit "Nothing to recreate in podvm-images configmap"

    kubectl create configmap podvm-images \
        -n openshift-sandboxed-containers-operator \
        --from-literal=aws="${AMI_ID_LIST}" \
        --dry-run=client -o yaml |
        kubectl apply -f - ||
        error_exit "Failed to recreate podvm-images configmap"

    echo "podvm-images configmap recreated successfully"
}

# Function to add the ami id as annotation in the peer-pods-cm configmap

function add_ami_id_annotation_to_peer_pods_cm() {
    echo "Adding ami id to peer-pods-cm configmap"

    # Check if the peer-pods-cm configmap exists
    if ! kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
        echo "peer-pods-cm configmap does not exist. Skipping adding the ami id"
        return
    fi

    # Add the image id as annotation to peer-pods-cm configmap
    kubectl annotate configmap peer-pods-cm -n openshift-sandboxed-containers-operator \
        "LATEST_AMI_ID=${AMI_ID}" ||
        error_exit "Failed to add the ami id as annotation to peer-pods-cm configmap"

    echo "AMI id added as annotation to peer-pods-cm configmap successfully"
}

# Function to create the ami in AWS

function create_ami() {
    echo "Creating AWS AMI"

    # Create the AWS image
    # If any error occurs, exit the script with an error message

    # Install packages if INSTALL_PACKAGES is set to yes

    if [[ "${INSTALL_PACKAGES}" == "yes" ]]; then
        # Add Azure yum repositories
        add_azure_repositories

        # Install required rpm packages
        install_rpm_packages

        # Install required binary packages
        install_binary_packages
    fi

    if [[ "${DOWNLOAD_SOURCES}" == "yes" ]]; then
        # Download source code from GitHub
        download_source_code
    fi

    # Prepare the source code for building the ami
    prepare_source_code

    # Create AWS ami using packer
    create_ami_using_packer

    # Get the ami id of the newly created image
    # This will set the AMI_ID environment variable
    get_ami_id

    # Add the ami id as annotation to peer-pods-cm configmap
    add_ami_id_annotation_to_peer_pods_cm

}

# function to delete the ami
# AMI_ID must be set as an environment variable

function delete_ami_using_id() {
    echo "Deleting AWS AMI"

    # Delete the AMI
    # If any error occurs, exit the script with an error message

    # AMI_ID shouldn't be empty
    [[ -z "${AMI_ID}" ]] && error_exit "AMI_ID is empty"

    # Delete the ami
    aws ec2 deregister-image --region "${AWS_REGION}" --image-id "${AMI_ID}" ||
        error_exit "Failed to delete the ami"

}

# display help message

function display_help() {
    echo "This script is used to create AWS ami for podvm"
    echo "Usage: $0 [-c|-C] [-- install_binaries|install_rpms|install_cli]"
    echo "Options:"
    echo "-c  Create image"
    echo "-C  Delete image"
    echo "-R Recreate podvm-images configMap"
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
        install_binary_packages
        ;;
    install_rpms)
        install_rpm_packages
        ;;
    install_cli)
        install_aws_cli
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
            create_ami
            ;;
        C)
            # Delete the ami
            delete_ami_using_id

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
