#!/bin/bash
# FILEPATH: azure-podvm-image-handler.sh

# This script is used to create Azure image for podvm
# The podvm images are organised in the following hierarchy:
# Image gallery -> Image definition -> Image version(s)
# The script will create the image gallery, image definition and image version(s)
# The script will also delete the image gallery, image definition and image version(s)

# The basic assumption is that the required variables are set as environment variables in the pod
# Typically the variables are read from configmaps and set as environment variables in the pod
# The script will be called with one of the following options:
# Create image (-c)
# Delete image (-C)
# Create image gallery (-g)
# Delete image gallery (force option) (-G)
# Create image definition (-d)
# Delete image definition (-D)
# Create image version (-i)
# Delete image version (-I)

set -x

# include common functions from lib.sh
# shellcheck source=/dev/null
# The directory is where azure-podvm-image-handler.sh is located
source "$(dirname "$0")"/lib.sh

# Function to verify that the required variables are set

function verify_vars() {

    echo "Verifying variables"

    # Ensure CLOUD_PROVIDER is set to azure
    [[ -z "${CLOUD_PROVIDER}" || "${CLOUD_PROVIDER}" != "azure" ]] && error_exit "CLOUD_PROVIDER is empty or not set to azure"

    # Ensure that the Azure specific values are set
    [[ -z "${AZURE_CLIENT_ID}" ]] && error_exit "AZURE_CLIENT_ID is empty"
    [[ -z "${AZURE_CLIENT_SECRET}" ]] && error_exit "AZURE_CLIENT_SECRET is empty"
    [[ -z "${AZURE_SUBSCRIPTION_ID}" ]] && error_exit "AZURE_SUBSCRIPTION_ID is empty"
    [[ -z "${AZURE_TENANT_ID}" ]] && error_exit "AZURE_TENANT_ID is empty"

    [[ -z "${AZURE_REGION}" ]] && error_exit "AZURE_REGION is empty"
    [[ -z "${AZURE_RESOURCE_GROUP}" ]] && error_exit "AZURE_RESOURCE_GROUP is empty"

    # Ensure that the image defintion variables are set
    [[ -z "${IMAGE_DEFINITION_PUBLISHER}" ]] && error_exit "IMAGE_DEFINITION_PUBLISHER is empty"
    [[ -z "${IMAGE_DEFINITION_OFFER}" ]] && error_exit "IMAGE_DEFINITION_OFFER is empty"
    [[ -z "${IMAGE_GALLERY_NAME}" ]] && error_exit "IMAGE_GALLERY_NAME is empty"
    [[ -z "${IMAGE_DEFINITION_SKU}" ]] && error_exit "IMAGE_DEFINITION_SKU is empty"
    [[ -z "${IMAGE_DEFINITION_OS_TYPE}" ]] && error_exit "IMAGE_DEFINITION_OS_TYPE is empty"
    [[ -z "${IMAGE_DEFINITION_OS_STATE}" ]] && error_exit "IMAGE_DEFINITION_OS_STATE is empty"
    [[ -z "${IMAGE_DEFINITION_ARCHITECTURE}" ]] && error_exit "IMAGE_DEFINITION_ARCHITECTURE is empty"
    [[ -z "${IMAGE_DEFINITION_NAME}" ]] && error_exit "IMAGE_DEFINITION_NAME is empty"
    [[ -z "${IMAGE_DEFINITION_VM_GENERATION}" ]] && error_exit "IMAGE_DEFINITION_VM_GENERATION is empty"

    # Ensure packer variables are set
    [[ -z "${VM_SIZE}" ]] && error_exit "VM_SIZE is empty"
    [[ -z "${PODVM_DISTRO}" ]] && error_exit "PODVM_DISTRO is empty"

    # Ensure that the image variables are set
    [[ -z "${IMAGE_BASE_NAME}" ]] && error_exit "IMAGE_BASE_NAME is empty"
    [[ -z "${IMAGE_VERSION_MAJ_MIN}" ]] && error_exit "IMAGE_VERSION_MAJ_MIN is empty"

    [[ -z "${CAA_SRC}" ]] && error_exit "CAA_SRC is empty"
    [[ -z "${CAA_REF}" ]] && error_exit "CAA_REF is empty"

    # Ensure booleans are set
    [[ -z "${INSTALL_PACKAGES}" ]] && error_exit "INSTALL_PACKAGES is empty"
    [[ -z "${DOWNLOAD_SOURCES}" ]] && error_exit "DOWNLOAD_SOURCES is empty"
    [[ -z "${CONFIDENTIAL_COMPUTE_ENABLED}" ]] && error_exit "CONFIDENTIAL_COMPUTE_ENABLED is empty"
    [[ -z "${DISABLE_CLOUD_CONFIG}" ]] && error_exit "DISABLE_CLOUD_CONFIG is empty"
    [[ -z "${ENABLE_NVIDIA_GPU}" ]] && error_exit "ENABLE_NVIDIA_GPU is empty"
    [[ -z "${BOOT_FIPS}" ]] && error_exit "BOOT_FIPS is empty"

    echo "All variables are set"

}

# function to add Azure yum repositories

function add_azure_repositories() {
    echo "Adding Azure yum repositories"
    # If any error occurs, exit the script with an error message
    # Ref: https://learn.microsoft.com/en-us/cli/azure/install-azure-cli-linux?pivots=dnf

    # Add the package signing key
    rpm --import https://packages.microsoft.com/keys/microsoft.asc ||
        error_exit "Failed to import the Microsoft signing key"

    # Add the Azure CLI repository information
    dnf install -y https://packages.microsoft.com/config/rhel/9.0/packages-microsoft-prod.rpm ||
        error_exit "Failed to add the Azure CLI repository"

    echo "Azure yum repositories added successfully"
}

# function to install azure CLI

function install_azure_cli() {
    echo "Installing Azure CLI"
    # If any error occurs, exit the script with an error message

    # Check if the Azure CLI is already installed
    if command -v az &>/dev/null; then
        echo "Azure CLI is already installed. Skipping installation"
        return
    fi

    # Add azure cli repo
    add_azure_repositories

    # Install Azure CLI
    dnf install -y azure-cli ||
        error_exit "Failed to install Azure CLI"

    echo "Azure CLI installed successfully"
}

# Function to login to Azure

function login_to_azure() {
    echo "Logging in to Azure"
    # If any error occurs, exit the script with an error message

    az login --service-principal \
        --user="${AZURE_CLIENT_ID}" \
        --password="${AZURE_CLIENT_SECRET}" \
        --tenant="${AZURE_TENANT_ID}" ||
        error_exit "Failed to login to Azure"

    echo "Logged in to Azure successfully"
}

# Function to create Azure image gallery
# The gallery name is available in the variable IMAGE_GALLERY_NAME

function create_image_gallery() {
    echo "Creating Azure image gallery"

    # Check if the gallery already exists.
    az sig show --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}"

    return_code=$?

    # If the gallery already exists, then skip creating the gallery
    if [[ "${return_code}" == "0" ]]; then
        echo "Gallery ${IMAGE_GALLERY_NAME} already exists. Skipping creating the gallery"
        return
    fi

    # Create Azure image gallery
    # If any error occurs, exit the script with an error message

    # Create the image gallery
    az sig create --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" ||
        error_exit "Failed to create Azure image gallery"

    echo "Azure image gallery created successfully"

}

# Function to create Azure image definition
# The image definition name is available in the variable IMAGE_DEFINITION_NAME
# The VM generation is available in the variable IMAGE_DEFINITION_VM_GENERATION
# Create gallery to support confidential images if CONFIDENTIAL_COMPUTE_ENABLED is set to yes

function create_image_definition() {
    echo "Creating Azure image definition"

    # Check if the image definition already exists.
    az sig image-definition show --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}"

    return_code=$?

    # Create Azure image definition if it doesn't exist

    if [[ "${return_code}" == "0" ]]; then
        echo "Image definition ${IMAGE_DEFINITION_NAME} already exists. Skipping creating the image definition"
        return
    fi

    if [[ "${CONFIDENTIAL_COMPUTE_ENABLED}" == "yes" ]]; then
        # Create the image definition. Add ConfidentialVmSupported feature
        az sig image-definition create --resource-group "${AZURE_RESOURCE_GROUP}" \
            --gallery-name "${IMAGE_GALLERY_NAME}" \
            --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
            --publisher "${IMAGE_DEFINITION_PUBLISHER}" \
            --offer "${IMAGE_DEFINITION_OFFER}" \
            --sku "${IMAGE_DEFINITION_SKU}" \
            --os-type "${IMAGE_DEFINITION_OS_TYPE}" \
            --os-state "${IMAGE_DEFINITION_OS_STATE}" \
            --hyper-v-generation "${IMAGE_DEFINITION_VM_GENERATION}" \
            --location "${AZURE_REGION}" \
            --architecture "${IMAGE_DEFINITION_ARCHITECTURE}" \
            --features SecurityType=ConfidentialVmSupported ||
            error_exit "Failed to create Azure image definition"

    else
        az sig image-definition create --resource-group "${AZURE_RESOURCE_GROUP}" \
            --gallery-name "${IMAGE_GALLERY_NAME}" \
            --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
            --publisher "${IMAGE_DEFINITION_PUBLISHER}" \
            --offer "${IMAGE_DEFINITION_OFFER}" \
            --sku "${IMAGE_DEFINITION_SKU}" \
            --os-type "${IMAGE_DEFINITION_OS_TYPE}" \
            --os-state "${IMAGE_DEFINITION_OS_STATE}" \
            --hyper-v-generation "${IMAGE_DEFINITION_VM_GENERATION}" \
            --location "${AZURE_REGION}" \
            --architecture "${IMAGE_DEFINITION_ARCHITECTURE}" ||
            error_exit "Failed to create Azure image definition"
    fi

    echo "Azure image definition created successfully"
}

# Function to use packer to create Azure image

function create_image_using_packer() {
    echo "Creating Azure image using packer"

    # If any error occurs, exit the script with an error message
    # The variables are set before calling the function

    # Set the image version
    # It should follow the Major(int).Minor(int).Patch(int)
    IMAGE_VERSION="${IMAGE_VERSION_MAJ_MIN}.$(date +'%Y%m%d%S')"
    export IMAGE_VERSION

    # Set the image name
    IMAGE_NAME="${IMAGE_BASE_NAME}-${IMAGE_VERSION}"
    export IMAGE_NAME

    # Set the base image details

    if [[ "${PODVM_DISTRO}" == "rhel" ]]; then
        export BASE_IMAGE_PUBLISHER="redhat"
        export BASE_IMAGE_OFFER="rhel-raw"

        # If CONFIDENTIAL_COMPUTE_ENABLED is set to yes, then force IMAGE_DEFINITION_VM_GENERATION to V2
        [[ "${CONFIDENTIAL_COMPUTE_ENABLED}" == "yes" ]] &&
            export IMAGE_DEFINITION_VM_GENERATION="V2"

        # If CONFIDENTIAL_COMPUTE_ENABLED is set to yes, CONFIDENTIAL_COMPUTE_TYPE is snp and BASE_IMAGE_SKU is not set,
        # then set BASE_IMAGE_SKU to 9_3_cvm_sev_snp
        [[ "${CONFIDENTIAL_COMPUTE_ENABLED}" == "yes" && "${CONFIDENTIAL_COMPUTE_TYPE}" == "snp" && -z "${BASE_IMAGE_SKU}" ]] &&
            export BASE_IMAGE_OFFER="rhel-cvm" && export BASE_IMAGE_SKU="9_3_cvm_sev_snp"

        # If CONFIDENTIAL_COMPUTE_ENABLED is set to yes, CONFIDENTIAL_COMPUTE_TYPE is tdx and BASE_IMAGE_SKU is not set,
        # then set BASE_IMAGE_SKU to rhel93_tdxpreview
        [[ "${CONFIDENTIAL_COMPUTE_ENABLED}" == "yes" && "${CONFIDENTIAL_COMPUTE_TYPE}" == "tdx" && -z "${BASE_IMAGE_SKU}" ]] &&
            export BASE_IMAGE_OFFER="rhel_test_offers" && export BASE_IMAGE_SKU="rhel93_tdxpreview"

        # If VM_GENERATION is V1 and BASE_IMAGE_SKU is not set, then set BASE_IMAGE_SKU to 9_3
        [[ "${IMAGE_DEFINITION_VM_GENERATION}" == "V1" && -z "${BASE_IMAGE_SKU}" ]] &&
            export BASE_IMAGE_SKU="9_3"
    else
        # TBD Add support for other distros
        # Error out if the distro is not supported
        error_exit "Unsupported distro"
    fi

    export PKR_VAR_client_id="${AZURE_CLIENT_ID}"
    export PKR_VAR_client_secret="${AZURE_CLIENT_SECRET}"
    export PKR_VAR_subscription_id="${AZURE_SUBSCRIPTION_ID}"
    export PKR_VAR_tenant_id="${AZURE_TENANT_ID}"
    export PKR_VAR_resource_group="${AZURE_RESOURCE_GROUP}"
    export PKR_VAR_location="${AZURE_REGION}"
    export PKR_VAR_az_image_name="${IMAGE_NAME}"
    export PKR_VAR_vm_size="${VM_SIZE}"
    export PKR_VAR_ssh_username="${SSH_USERNAME:-peerpod}"
    export PKR_VAR_publisher="${BASE_IMAGE_PUBLISHER}"
    export PKR_VAR_offer="${BASE_IMAGE_OFFER}"
    export PKR_VAR_sku="${BASE_IMAGE_SKU}"
    export PKR_VAR_az_gallery_name="${IMAGE_GALLERY_NAME}"
    export PKR_VAR_az_gallery_image_name="${IMAGE_DEFINITION_NAME}"
    export PKR_VAR_az_gallery_image_version="${IMAGE_VERSION}"

    cd "${CAA_SRC_DIR}"/azure/image ||
        error_exit "Failed to change directory to ${CAA_SRC_DIR}/azure/image"
    packer init "${PODVM_DISTRO}"/
    make BINARIES= PAUSE_BUNDLE= image

    # Wait for the image to be created

    echo "Azure image created successfully"
}

# Function to retrieve the image id given gallery, image definition and image version

function get_image_id() {
    echo "Getting the image id"

    # Get the image id of the newly created image
    # If any error occurs, exit the script with an error message

    # Get the image id
    IMAGE_ID=$(az sig image-version show --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
        --gallery-image-version "${IMAGE_VERSION}" \
        --query "id" --output tsv) ||
        error_exit "Failed to get the image id"
    export IMAGE_ID

    echo "ID of the newly created image: ${IMAGE_ID}"
}

# Function to get all image version ids in the image gallery
# Output is in the form of a list of image ids

function get_all_image_ids() {
    echo "Getting all image ids"

    # List all image versions in the image gallery
    # If any error occurs, exit the script with an error message

    # List all image versions
    IMAGE_ID_LIST=$(az sig image-version list --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" --query "[*].id" -o tsv ||
        error_exit "Failed to list all image ids")
    export IMAGE_ID_LIST

    # Display the list of image versions
    echo "List of images: ${IMAGE_ID_LIST}"

}

# Function to create or update podvm-images configmap with all the images
# Input IMAGE_ID_LIST is a list of image ids

function create_or_update_image_configmap() {
    echo "Creating or updating podvm-images configmap"

    # Check if the podvm-images configmap already exists
    # If exists get the current value of the azure key and append the new image id to it
    # If not exists, create the podvm-images configmap with the new image id

    # Check if the podvm-images configmap exists
    if kubectl get configmap podvm-images -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
        # Get the current value of the azure key in podvm-images configmap
        IMAGE_ID_LIST=$(kubectl get configmap podvm-images -n openshift-sandboxed-containers-operator -o jsonpath='{.data.azure}') ||
            error_exit "Failed to get the current value of the azure key in podvm-images configmap"

        # If the current value of the azure key is empty, then set the value to the new image id
        if [[ -z "${IMAGE_ID_LIST}" ]]; then
            IMAGE_ID_LIST="${IMAGE_ID}"
        else
            # If the current value of the azure key is not empty, then append the new azure image in the beginning
            # The first azure image id in the list is the latest azure image id
            IMAGE_ID_LIST="${IMAGE_ID} ${IMAGE_ID_LIST}"
        fi
    else
        # If the podvm-images configmap does not exist, set the value to the new image id
        IMAGE_ID_LIST="${IMAGE_ID}"
    fi

    # Create or update the value of the azure key in podvm-images configmap with all the images
    # If any error occurs, exit the script with an error message
    kubectl create configmap podvm-images \
        -n openshift-sandboxed-containers-operator \
        --from-literal=azure="${IMAGE_ID_LIST}" \
        --dry-run=client -o yaml |
        kubectl apply -f - ||
        error_exit "Failed to create or update podvm-images configmap"

    echo "podvm-images configmap created or updated successfully"
}

# Funtion to recreate podvm-images configmap with all the images

function recreate_image_configmap() {
    echo "Recreating podvm-images configmap"

    # Get list of all image ids
    get_all_image_ids

    # Check if IMAGE_ID_LIST is empty
    [[ -z "${IMAGE_ID_LIST}" ]] && error_exit "Nothing to recreate in podvm-images configmap"

    kubectl create configmap podvm-images \
        -n openshift-sandboxed-containers-operator \
        --from-literal=azure="${IMAGE_ID_LIST}" \
        --dry-run=client -o yaml |
        kubectl apply -f - ||
        error_exit "Failed to recreate podvm-images configmap"

    echo "podvm-images configmap recreated successfully"
}

# Function to add the image id as annotation in the peer-pods-cm configmap

function add_image_id_annotation_to_peer_pods_cm() {
    echo "Adding image id to peer-pods-cm configmap"

    # Check if the peer-pods-cm configmap exists
    if ! kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
        echo "peer-pods-cm configmap does not exist. Skipping adding the image id"
        return
    fi

    # Add the image id as annotation to peer-pods-cm configmap
    kubectl annotate configmap peer-pods-cm -n openshift-sandboxed-containers-operator \
        "LATEST_IMAGE_ID=${IMAGE_ID}" ||
        error_exit "Failed to add the image id as annotation to peer-pods-cm configmap"

    echo "Image id added as annotation to peer-pods-cm configmap successfully"
}

# Function to create the image in Azure
# It's assumed you have already logged in to Azure
# It's assumed that the gallery and image defintion exists

function create_image() {
    echo "Creating Azure image"
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

    # Prepare the source code for building the image
    prepare_source_code

    # Create Azure image using packer
    create_image_using_packer

    # Get the image id of the newly created image.
    # This will set the IMAGE_ID variable
    get_image_id

    # Add the image id as annotation to peer-pods-cm configmap
    add_image_id_annotation_to_peer_pods_cm

    echo "Azure image created successfully"

}

# Function to delete a specific image version from Azure

function delete_image_version() {
    echo "Deleting Azure image version"
    # If any error occurs, exit the script with an error message

    # Delete the image version
    az sig image-version delete --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
        --gallery-image-version "${IMAGE_VERSION}" ||
        error_exit "Failed to delete the image version"

    echo "Azure image version deleted successfully"
}

# Function delete all image versions from Azure image-definition
# Input IMAGE_ID_LIST is a list of image ids

function delete_all_image_versions() {
    echo "Deleting all image versions"

    # Ensure IMAGE_ID_LIST is set
    [[ -z "${IMAGE_ID_LIST}" ]] && error_exit "IMAGE_ID_LIST is not set"

    # Delete all the image versions
    az sig image-version delete --ids "${IMAGE_ID_LIST}" ||
        error_exit "Failed to delete the image versions"

    echo "All image versions deleted successfully"
}

# Function to delete the image definition from Azure
# It's assumed you have already deleted all the image versions
# It's assumed you have already logged in to Azure

function delete_image_definition() {
    echo "Deleting Azure image definition"
    # If any error occurs, exit the script with an error message

    # Check if the image definition already exists.

    az sig image-definition show --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}"

    return_code=$?

    # If the image definition doesn't exist, then skip deleting the image definition
    if [[ "${return_code}" != "0" ]]; then
        echo "Image definition ${IMAGE_DEFINITION_NAME} doesn't exist. Skipping deleting the image definition"
        return
    fi

    # Check if the image definition has any image versions
    get_all_image_ids

    # If the image definition has image versions, then skip deleting the image definition unless "force" option is passed
    if [[ "${IMAGE_ID_LIST}" ]] && [[ "${1}" != "force" ]]; then
        echo "Image definition ${IMAGE_DEFINITION_NAME} has image versions. Skipping deleting the image definition"
        return
    fi

    # Delete all the image versions if IMAGE_ID_LIST is not empty and force option is passed as argument
    if [[ "${IMAGE_ID_LIST}" ]] && [[ "${1}" == "force" ]]; then
        delete_all_image_versions
    fi

    # Delete the image definition
    az sig image-definition delete --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" ||
        error_exit "Failed to delete the image definition"

    echo "Azure image definition deleted successfully"
}

# Function to delete the image gallery from Azure
# Accept force argument to delete the gallery even if image versions exist

function delete_image_gallery() {
    echo "Deleting Azure image gallery"
    # Delete the image gallery from Azure
    # If any error occurs, exit the script with an error message

    # Check if the gallery exists
    az sig show --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}"

    return_code=$?

    # If the gallery doesn't exist, then skip deleting the gallery
    if [[ "${return_code}" != "0" ]]; then
        echo "Gallery ${IMAGE_GALLERY_NAME} doesn't exist. Skipping deleting the gallery"
        return
    fi

    # Check if the gallery has any image versions
    get_all_image_ids

    # If the gallery has image versions, then skip deleting the gallery if "force" option is not passed
    if [[ "${IMAGE_ID_LIST}" ]] && [[ "${1}" != "force" ]]; then
        echo "Gallery ${IMAGE_GALLERY_NAME} has image versions. Skipping deleting the gallery"
        return
    fi

    # Delete all the image versions if IMAGE_ID_LIST is not empty and force option is passed as argument
    if [[ "${IMAGE_ID_LIST}" ]] && [[ "${1}" == "force" ]]; then
        delete_all_image_versions
    fi

    # Delete the image definition
    delete_image_definition

    # Delete the image gallery
    az sig delete --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" ||
        error_exit "Failed to delete the image gallery"

    echo "Azure image gallery deleted successfully"
}

# Function to delete the image from Azure given the image name
# Resource group is must
# Input is of the form /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.Compute/images/<image-name>

function delete_image_using_name() {
    echo "Deleting Azure image"
    # If any error occurs, exit the script with an error message

    # Delete the image
    az image delete --resource-group "${AZURE_RESOURCE_GROUP}" \
        --name "${IMAGE_NAME}" ||
        error_exit "Failed to delete the image"

    echo "Azure image deleted successfully"
}

# Function to delete the image from Azure given the image id
# Input is of the form /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.Compute/images/<image-name>
# or /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.Compute/galleries/<gallery-name>/images/<image-name>/versions/<image-version>

function delete_image_using_id() {
    echo "Deleting Azure image"
    # If any error occurs, exit the script with an error message

    # IMAGE_ID shouldn't be empty
    [[ -z "${IMAGE_ID}" ]] && error_exit "IMAGE_ID is empty"

    # Delete the image
    az image delete --ids "${IMAGE_ID}" ||
        error_exit "Failed to delete the image"

    echo "Azure image deleted successfully"
}

# display help message

function display_help() {
    echo "This script is used to create Azure image for podvm"
    echo "Usage: $0 [-c] [-C] [-g] [-G] [-d] [-D] [-i] [-I] [-h] [-- install_binaries|install_rpms|install_cli]"
    echo "Options:"
    echo "-c Create image"
    echo "-C Delete image"
    echo "-g Create image gallery"
    echo "-G Delete image gallery [force]"
    echo "-d Create image definition"
    echo "-D Delete image definition [force]"
    echo "-i Create image version"
    echo "-I Delete image version"
    echo "-R Recreate podvm-images configMap"
    echo "-h Display help"
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
        install_azure_cli
        ;;
    *)
        echo "Unknown argument: $1"
        exit 1
        ;;
    esac
else
    while getopts ":cCgGdDiIRh" opt; do
        verify_vars
        login_to_azure
        case ${opt} in
        c)
            # Create image gallery
            create_image_gallery

            # Create image definition
            create_image_definition

            # Create image
            create_image
            ;;
        C)
            # Delete image
            delete_image_using_id
            ;;
        g)
            # Create image gallery
            create_image_gallery
            ;;
        G)
            # Delete image gallery
            delete_image_gallery "${2}"
            ;;
        d)
            # Create image definition
            create_image_definition
            ;;
        D)
            # Delete image definition
            delete_image_definition "${2}"
            ;;
        i)
            # Create image version
            create_image_version
            ;;
        I)
            # Delete image version
            delete_image_version
            ;;
        R)
            # Recreate the podvm-images configmap
            recreate_image_configmap
            ;;
        h)
            display_help
            exit 0
            ;;
        \?)
            echo "Invalid option: -$OPTARG" >&2
            display_help
            exit 1
            ;;
        esac
    done

    shift $((OPTIND - 1))

fi
