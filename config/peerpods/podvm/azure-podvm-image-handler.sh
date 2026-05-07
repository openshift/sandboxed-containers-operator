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
    # Either AZURE_CLIENT_SECRET or AZURE_FEDERATED_TOKEN_FILE must be set
    if [[ -z "${AZURE_CLIENT_SECRET}" && -z "${AZURE_FEDERATED_TOKEN_FILE}" ]]; then
        error_exit "Either AZURE_CLIENT_SECRET or AZURE_FEDERATED_TOKEN_FILE must be set"
    fi
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

    # Ensure that the image variables are set
    [[ -z "${IMAGE_BASE_NAME}" ]] && error_exit "IMAGE_BASE_NAME is empty"
    [[ -z "${IMAGE_VERSION}" ]] && error_exit "IMAGE_VERSION is empty"

    # Ensure booleans are set
    [[ -z "${INSTALL_PACKAGES}" ]] && error_exit "INSTALL_PACKAGES is empty"
    [[ -z "${CONFIDENTIAL_COMPUTE_ENABLED}" ]] && error_exit "CONFIDENTIAL_COMPUTE_ENABLED is empty"

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

function set_image_name() {

    # Set the image name
    IMAGE_NAME="${IMAGE_BASE_NAME}-${IMAGE_VERSION}"
    echo "Image name: ${IMAGE_NAME}"
    export IMAGE_NAME
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

    if [[ -n "${AZURE_CLIENT_SECRET}" ]]; then
        # Use client secret authentication
        echo "Using client secret authentication"
        az login --service-principal \
            --user="${AZURE_CLIENT_ID}" \
            --password="${AZURE_CLIENT_SECRET}" \
            --tenant="${AZURE_TENANT_ID}" ||
            error_exit "Failed to login to Azure"
    elif [[ -n "${AZURE_FEDERATED_TOKEN_FILE}" ]]; then
        # Use federated token authentication
        echo "Using federated token authentication"

        # Read the token from the file
        [[ ! -f "${AZURE_FEDERATED_TOKEN_FILE}" ]] && error_exit "Federated token file ${AZURE_FEDERATED_TOKEN_FILE} does not exist"

        FEDERATED_TOKEN=$(cat "${AZURE_FEDERATED_TOKEN_FILE}") ||
            error_exit "Failed to read federated token from ${AZURE_FEDERATED_TOKEN_FILE}"

        [[ -z "${FEDERATED_TOKEN}" ]] && error_exit "Federated token is empty"

        az login --service-principal \
            --user="${AZURE_CLIENT_ID}" \
            --federated-token="${FEDERATED_TOKEN}" \
            --tenant="${AZURE_TENANT_ID}" ||
            error_exit "Failed to login to Azure using federated token"
    fi

    echo "Selecting subscription"
    az account set --subscription ${AZURE_SUBSCRIPTION_ID} ||
        error_exit "Failed to select subscription"

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
    echo "Creating image gallery ${IMAGE_GALLERY_NAME}"

    az sig create --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" ||
        error_exit "Failed to create Azure image gallery"

    # Update peer-pods-cm configmap with the gallery name
    add_image_gallery_annotation_to_peer_pods_cm

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
# Output is in the form of a list of image version ids

function get_all_image_version_ids() {
    echo "Getting all image version ids"

    # List all image versions in the image gallery
    # If any error occurs, exit the script with an error message

    # List all image versions
    IMAGE_VERSION_ID_LIST=$(az sig image-version list --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" --query "[*].id" -o tsv ||
        error_exit "Failed to list all image ids")
    export IMAGE_VERSION_ID_LIST

    # Display the list of image versions
    [[ -z "${IMAGE_VERSION_ID_LIST}" ]] && echo "No image versions found" ||
        echo "List of images: ${IMAGE_VERSION_ID_LIST}"

}

# Function to add image gallery annotation to peer-pods-cm configmap

function add_image_gallery_annotation_to_peer_pods_cm() {
    echo "Adding IMAGE_GALLERY_NAME annotation to peer-pods-cm configmap"

    # Check if the peer-pods-cm configmap exists
    if ! kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
        echo "peer-pods-cm configmap does not exist. Skipping adding the IMAGE_GALLERY_NAME annotation"
        return
    fi

    # Add IMAGE_GALLERY_NAME annotation to peer-pods-cm configmap
    # Overwrite any existing values
    kubectl annotate --overwrite configmap peer-pods-cm -n openshift-sandboxed-containers-operator \
        "IMAGE_GALLERY_NAME=${IMAGE_GALLERY_NAME}" ||
        error_exit "Failed to add the IMAGE_GALLERY_NAME annotation to peer-pods-cm configmap"

    echo "IMAGE_GALLERY_NAME annotation added to peer-pods-cm configmap successfully"
}

# Function to delete the image gallery annotation from peer-pods-cm configmap

function delete_image_gallery_annotation_from_peer_pods_cm() {
    echo "Deleting IMAGE_GALLERY_NAME annotation from peer-pods-cm configmap"

    # Check if the peer-pods-cm configmap exists
    if ! kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
        echo "peer-pods-cm configmap does not exist. Skipping deleting the IMAGE_GALLERY_NAME annotation"
        return
    fi

    # Delete the IMAGE_GALLERY_NAME annotation from peer-pods-cm configmap
    kubectl annotate configmap peer-pods-cm -n openshift-sandboxed-containers-operator \
        "IMAGE_GALLERY_NAME-" ||
        error_exit "Failed to delete the IMAGE_GALLERY_NAME annotation from peer-pods-cm configmap"

    echo "IMAGE_GALLERY_NAME annotation deleted from peer-pods-cm configmap successfully"
}

# Function to create the image in Azure
# It's assumed you have already logged in to Azure
# It's assumed that the gallery and image defintion exists

function create_image() {
    echo "Creating Azure image"
    # If any error occurs, exit the script with an error message

    # Set the image version and name
    set_image_name

    image_exists
    image_status=$?

    if [[ "${image_status}" -eq 0 ]]; then
        echo "Image exists. Skipping creation"
        return
    elif [[ "${image_status}" -eq 2 ]]; then
        echo "Deleting image version (${IMAGE_VERSION}), before recreating"
        delete_image_version
    fi

    echo "Image does not exist. Proceeding to create the image"

    # Install packages if INSTALL_PACKAGES is set to yes
    if [[ "${INSTALL_PACKAGES}" == "yes" ]]; then
        # Add Azure yum repositories
        add_azure_repositories

        # Install required rpm packages
        install_rpm_packages

        # Install required binary packages
        install_binary_packages
    fi

    create_azure_image_from_prebuilt_artifact

    # Get the image id
    # This will set the IMAGE_ID variable
    get_image_id

    # Add the image id as annotation to peer-pods-cm configmap
    update_cm_annotation "LATEST_IMAGE_ID" "${IMAGE_ID}"

    echo "Azure image created successfully"

}

# Function to create the azure image from prebuilt artifact
# The prebuilt artifact is expected to be a vhd image

function create_azure_image_from_prebuilt_artifact() {
    echo "Creating Azure image from prebuilt artifact"

    echo "Pulling the podvm image from the provided path"
    image_src="/tmp/image"
    extraction_destination_path="/image"
    image_repo_auth_file="/tmp/regauth/auth.json"

    # Get the PODVM_IMAGE_TYPE, PODVM_IMAGE_TAG and PODVM_IMAGE_SRC_PATH
    get_image_type_url_and_path

    case "${PODVM_IMAGE_TYPE}" in
    oci)
        echo "Extracting the Azure image from the given path."

        mkdir -p "${extraction_destination_path}" ||
            error_exit "Failed to create the image directory"

        extract_container_image "${PODVM_IMAGE_URL}" \
            "${PODVM_IMAGE_TAG}" \
            "${image_src}" \
            "${extraction_destination_path}" \
            "${image_repo_auth_file}"

        # Form the path of the podvm vhd image.
        podvm_image_path="${extraction_destination_path}/rootfs/${PODVM_IMAGE_SRC_PATH}"
        ;;
    bootc)
        bootc_to_qcow2 "${PODVM_IMAGE_URL}" \
            "${PODVM_IMAGE_TAG}"

        podvm_image_path="$(pwd)/output/qcow2/disk.qcow2"
        ;;
    *)
        error_exit "Currently only OCI image unpacking is supported, exiting."
        ;;
    esac

    # Validate file existence
    [[ -f ${podvm_image_path} ]] || error_exit "No disk file has been found in: ${podvm_image_path}"

    # Convert the podvm image to vhd if it's not a vhd image
    # This will set the VHD_IMAGE_PATH global variable
    convert_podvm_image_to_vhd "${podvm_image_path}"

    # Upload the vhd to the storage container
    # This will set the VHD_URL global variable
    upload_vhd_image "${VHD_IMAGE_PATH}" "${IMAGE_NAME}"

    echo "Creating the image version from the VHD..."

    # Create the image version from the VHD
    az sig image-version create \
        --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
        --gallery-image-version "${IMAGE_VERSION}" \
        --os-vhd-uri "${VHD_URL}" \
        --os-vhd-storage-account "${STORAGE_ACCOUNT_NAME}" \
        --target-regions "${AZURE_REGION}" ||
        error_exit "Failed to create the image version"

    # Clean up
    rm "${podvm_image_path}"

    # Delete private endpoint before deleting storage account
    delete_storage_private_endpoint

    az storage account delete \
        --name "${STORAGE_ACCOUNT_NAME}" \
        --resource-group "${AZURE_RESOURCE_GROUP}" \
        --yes ||
        error_exit "Failed to delete the storage account"

    echo "Azure image created successfully from prebuilt artifact"
}

# Function to create a private endpoint for the storage account
# This allows access to the storage account when public network access is disabled
# Uses AZURE_SUBNET_ID from peer-pods-cm configmap

function create_storage_private_endpoint() {
    echo "Creating/getting private endpoint for storage account ${STORAGE_ACCOUNT_NAME}"

    PRIVATE_ENDPOINT_NAME="${STORAGE_ACCOUNT_NAME}-pe"

    # Check if private endpoint already exists
    if az network private-endpoint show \
        --name "${PRIVATE_ENDPOINT_NAME}" \
        --resource-group "${AZURE_RESOURCE_GROUP}" &>/dev/null; then
        echo "Private endpoint ${PRIVATE_ENDPOINT_NAME} already exists, reusing"
    else
        local storage_account_id
        storage_account_id=$(az storage account show \
            --name "${STORAGE_ACCOUNT_NAME}" \
            --resource-group "${AZURE_RESOURCE_GROUP}" \
            --query "id" -o tsv) ||
            error_exit "Failed to get storage account ID"

        az network private-endpoint create \
            --name "${PRIVATE_ENDPOINT_NAME}" \
            --resource-group "${AZURE_RESOURCE_GROUP}" \
            --subnet "${AZURE_SUBNET_ID}" \
            --private-connection-resource-id "${storage_account_id}" \
            --group-id blob \
            --connection-name "${STORAGE_ACCOUNT_NAME}-connection" ||
            error_exit "Failed to create private endpoint"
    fi

    # Get the private endpoint IP address for blob operations
    PRIVATE_ENDPOINT_IP=$(az network private-endpoint show \
        --name "${PRIVATE_ENDPOINT_NAME}" \
        --resource-group "${AZURE_RESOURCE_GROUP}" \
        --query "customDnsConfigs[0].ipAddresses[0]" -o tsv) ||
        error_exit "Failed to get private endpoint IP"

    # The SSL certificate is issued for *.blob.core.windows.net, not for IP addresses.
    # We need to use the storage account's FQDN and resolve it to the private IP.
    # Add a hosts entry to resolve the storage account FQDN to the private endpoint IP.
    STORAGE_BLOB_FQDN="${STORAGE_ACCOUNT_NAME}.blob.core.windows.net"

    # Add hosts entry only if not already present
    if ! grep -q "${STORAGE_BLOB_FQDN}" /etc/hosts; then
        echo "${PRIVATE_ENDPOINT_IP} ${STORAGE_BLOB_FQDN}" >> /etc/hosts
        echo "Added hosts entry: ${PRIVATE_ENDPOINT_IP} ${STORAGE_BLOB_FQDN}"
    fi

    # Use the proper FQDN for the blob endpoint
    BLOB_ENDPOINT="https://${STORAGE_BLOB_FQDN}"

    export PRIVATE_ENDPOINT_NAME
    export PRIVATE_ENDPOINT_IP
    export BLOB_ENDPOINT
    export STORAGE_BLOB_FQDN

    echo "Private endpoint ready: ${PRIVATE_ENDPOINT_NAME} (IP: ${PRIVATE_ENDPOINT_IP})"
}

# Function to delete the storage private endpoint

function delete_storage_private_endpoint() {
    echo "Deleting private endpoint ${PRIVATE_ENDPOINT_NAME}"

    if [[ -z "${PRIVATE_ENDPOINT_NAME}" ]]; then
        echo "No private endpoint to delete"
        return
    fi

    # Remove the hosts entry for the storage account FQDN
    if [[ -n "${STORAGE_BLOB_FQDN}" ]]; then
        sed -i "/${STORAGE_BLOB_FQDN}/d" /etc/hosts || true
        echo "Removed hosts entry for ${STORAGE_BLOB_FQDN}"
    fi

    az network private-endpoint delete \
        --name "${PRIVATE_ENDPOINT_NAME}" \
        --resource-group "${AZURE_RESOURCE_GROUP}" ||
        echo "Warning: Failed to delete private endpoint ${PRIVATE_ENDPOINT_NAME}"

    echo "Private endpoint deleted"
}

# Function to upload the vhd to the volume

function upload_vhd_image() {
    echo "Uploading the vhd to the storage container"

    local vhd_path="${1}"
    local image_name="${2}"

    [[ -z "${vhd_path}" ]] && error_exit "VHD path is empty"

    # Create deterministic storage account name based on IMAGE_NAME
    # Storage account names: 3-24 chars, lowercase alphanumeric only
    STORAGE_ACCOUNT_NAME="oscsa${IMAGE_NAME//[^a-zA-Z0-9]/}"
    STORAGE_ACCOUNT_NAME="${STORAGE_ACCOUNT_NAME,,}"
    STORAGE_ACCOUNT_NAME="${STORAGE_ACCOUNT_NAME:0:24}"

    # Check if storage account already exists
    if az storage account show --name "${STORAGE_ACCOUNT_NAME}" \
        --resource-group "${AZURE_RESOURCE_GROUP}" &>/dev/null; then
        echo "Storage account ${STORAGE_ACCOUNT_NAME} already exists, reusing"
    else
        # Create a storage account with security best practices:
        # - Minimum TLS version 1.2
        # - Cross-tenant replication disabled
        # - Public network access disabled
        # - HTTPS-only enforced
        # - Public blob access disabled
        echo "Creating storage account ${STORAGE_ACCOUNT_NAME}"
        az storage account create \
            --name "${STORAGE_ACCOUNT_NAME}" \
            --resource-group "${AZURE_RESOURCE_GROUP}" \
            --location "${AZURE_REGION}" \
            --sku Standard_LRS \
            --encryption-services blob \
            --min-tls-version TLS1_2 \
            --allow-cross-tenant-replication false \
            --public-network-access Disabled \
            --allow-blob-public-access false \
            --https-only true ||
            error_exit "Failed to create the storage account"
    fi

    # Create private endpoint to access storage account (public access is disabled)
    create_storage_private_endpoint

    # Get storage account key
    STORAGE_ACCOUNT_KEY=$(az storage account keys list \
        --resource-group "${AZURE_RESOURCE_GROUP}" \
        --account-name "${STORAGE_ACCOUNT_NAME}" \
        --query '[0].value' \
        -o tsv) ||
        error_exit "Failed to get the storage account key"

    # Create a container in the storage account using private endpoint
    CONTAINER_NAME="podvm-artifacts"
    az storage container create \
        --name "${CONTAINER_NAME}" \
        --account-name "${STORAGE_ACCOUNT_NAME}" \
        --account-key "${STORAGE_ACCOUNT_KEY}" \
        --blob-endpoint "${BLOB_ENDPOINT}" ||
        error_exit "Failed to create the storage container"

    # Upload the VHD to the storage container using private endpoint
    # Use --overwrite to make this idempotent for retries
    az storage blob upload --account-name "${STORAGE_ACCOUNT_NAME}" \
        --account-key "${STORAGE_ACCOUNT_KEY}" \
        --container-name "${CONTAINER_NAME}" \
        --file "${vhd_path}" \
        --name "${image_name}" \
        --blob-endpoint "${BLOB_ENDPOINT}" \
        --overwrite true ||
        error_exit "Failed to upload the VHD to the storage container"

    # Get the URL of the uploaded VHD
    # URL follows a well-defined format, so we needn't use az cli to query the URL
    VHD_URL="https://${STORAGE_ACCOUNT_NAME}.blob.core.windows.net/${CONTAINER_NAME}/${image_name}"

    export VHD_URL

    echo "VHD uploaded successfully"
}

# Function to delete a specific image version from Azure

function delete_image_version() {
    echo "Deleting Azure image version (${IMAGE_VERSION})"
    # If any error occurs, exit the script with an error message

    # Delete the image version
    az sig image-version delete --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
        --gallery-image-version "${IMAGE_VERSION}" ||
        error_exit "Failed to delete the image version"

    echo "Azure image version (${IMAGE_VERSION}) deleted successfully"
}

# Function delete all image versions from Azure image-definition
# Input IMAGE_VERSION_ID_LIST is a list of image version ids

function delete_all_image_versions() {
    echo "Deleting all image versions"

    # Ensure IMAGE_VERSION_ID_LIST is set
    [[ -z "${IMAGE_VERSION_ID_LIST}" ]] && error_exit "IMAGE_VERSION_ID_LIST is not set"

    # Delete all the image versions
    az sig image-version delete --ids "${IMAGE_VERSION_ID_LIST}" ||
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
    get_all_image_version_ids

    # If the image definition has image versions, then skip deleting the image definition unless "force" option is passed
    if [[ "${IMAGE_VERSION_ID_LIST}" ]] && [[ "${1}" != "force" ]]; then
        echo "Image definition ${IMAGE_DEFINITION_NAME} has image versions. Skipping deleting the image definition"
        return
    fi

    # Delete all the image versions if IMAGE_VERSION_ID_LIST is not empty and force option is passed as argument
    if [[ "${IMAGE_VERSION_ID_LIST}" ]] && [[ "${1}" == "force" ]]; then
        echo "Deleting all image versions of the image definition ${IMAGE_DEFINITION_NAME}"
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
# IMAGE_GALLERY_NAME is assumed to be populated

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
    # This will set the IMAGE_VERSION_ID_LIST variable
    get_all_image_version_ids

    # If the gallery has image versions, then skip deleting the gallery if "force" option is not passed
    if [[ "${IMAGE_VERSION_ID_LIST}" ]] && [[ "${1}" != "force" ]]; then
        echo "Gallery ${IMAGE_GALLERY_NAME} has image versions. Skipping deleting the gallery"
        return
    fi

    # Delete all the image versions if IMAGE_VERSION_ID_LIST is not empty and force option is passed as argument
    if [[ "${IMAGE_VERSION_ID_LIST}" ]] && [[ "${1}" == "force" ]]; then
        echo "Deleting all image versions of the gallery ${IMAGE_GALLERY_NAME}"
        delete_all_image_versions
    fi

    # Delete the image definition
    delete_image_definition

    # Delete the image gallery
    #az sig delete --resource-group "${AZURE_RESOURCE_GROUP}" \
    #    --gallery-name "${IMAGE_GALLERY_NAME}" ||
    #    error_exit "Failed to delete the image gallery"

    # Sometimes the delete fails with teh following command:
    # "ERROR: (CannotDeleteResource) Cannot delete resource while nested resources exist"
    # This could be temporary due to image definition deletion not being immediate
    # Hence add retry logic to delete the image gallery

    retry_command az sig delete --resource-group "${AZURE_RESOURCE_GROUP}" --gallery-name "${IMAGE_GALLERY_NAME}" ||
        echo "Failed to delete the image gallery."

    # Remove the image gallery annotation from peer-pods-cm configmap
    delete_image_gallery_annotation_from_peer_pods_cm

    echo "Azure image gallery deleted successfully"
}

# Function to delete the image from Azure given the image id
# Input is of the form
# /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.Compute/galleries/<gallery-name>/images/<image-name>/versions/<image-version>

function delete_image_using_id() {
    echo "Deleting Azure image"
    # If any error occurs, exit the script with an error message

    # IMAGE_ID shouldn't be empty
    [[ -z "${IMAGE_ID}" ]] && error_exit "IMAGE_ID is empty"

    # Rightmost element of the input is <image-version>
    IMAGE_VERSION=${IMAGE_ID##*/}

    # Get the id of the source image
    SOURCE_ID=$(az sig image-version show --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
        --gallery-image-version "${IMAGE_VERSION}" \
        --query "storageProfile.source.id" --output tsv) ||
        error_exit "Failed to get the source id for image ${IMAGE_GALLERY_NAME} version ${IMAGE_VERSION} with definition ${IMAGE_DEFINITION_NAME}"

    # Delete the image version
    az sig image-version delete --ids "${IMAGE_ID}" ||
        error_exit "Failed to delete image version ${IMAGE_ID}"

    # Delete the source image
    az image delete --ids "${SOURCE_ID}" ||
        error_exit "Failed to delete the source image ${SOURCE_ID}"

    # Remove the image id annotation from peer-pods-cm configmap
    delete_cm_annotation LATEST_IMAGE_ID

    echo "Azure image deleted successfully"
}

# Function to check if image already exists
# This checks if IMAGE_VERSION exist in Azure
# and the LATEST_IMAGE_ID annotation is set in the peer-pods-cm configmap
# 0: Image exists and matches
# 1: Image does not exist
# 2: Image mismatch (caller should delete Image)
# The required variables are assumed to be set
function image_exists() {

    local image_id
    # Get the image id
    image_id=$(az sig image-version show --resource-group "${AZURE_RESOURCE_GROUP}" \
        --gallery-name "${IMAGE_GALLERY_NAME}" \
        --gallery-image-definition "${IMAGE_DEFINITION_NAME}" \
        --gallery-image-version "${IMAGE_VERSION}" \
        --query "id" --output tsv)

    img_return_code=$?

    # Get the latest image id from the peer-pods-cm configmap
    local latest_image_id
    latest_image_id=$(kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.metadata.annotations.LATEST_IMAGE_ID}')

    # Handle Azure command failure
    if [[ "${img_return_code}" -ne 0 ]]; then
        # Treating any error as non-existent image as unlike AWS, Azure does not return empty string for non-existent image
        echo "Image does not exist in Azure."
        return 1
    fi

    # Case 1: Image exists and matches the configmap
    if [[ "${image_id}" == "${latest_image_id}" ]]; then
        echo "Image (${image_id}) is up-to-date in configmap."
        return 0
    # Case 2: Image missing in Azure but present in configmap
    elif [[ -z "${image_id}" && -n "${latest_image_id}" ]]; then
        echo "No Image found in Azure, but configmap has record (${latest_image_id}). Image might have been deleted."
        return 1
    # Case 3: Image exists in Azure but does not match configmap (it may be empty or have any other image listed).
    else
        echo "Image mismatch: Azure Image (${image_id}) differs from ConfigMap Image (${latest_image_id})."
        return 2 # Caller should delete Azure image version and recreate
    fi

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
    while getopts ":cCgGdDiIh" opt; do
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
