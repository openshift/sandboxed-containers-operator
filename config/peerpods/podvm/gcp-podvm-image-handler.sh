#!/bin/bash
# FILEPATH: gcp-podvm-image-handler.sh

# This script is used to create or delete GCP image for podvm
# The basic assumption is that the required variables are set as environment variables in the pod
# Typically the variables are read from configmaps and set as environment variables in the pod
# The script will be called with one of the following options:
# Create image (-c)
# Delete image (-C)

[[ "$DEBUG" == "true" ]] && set -x

# include common functions from lib.sh
# shellcheck source=/dev/null
# The directory is where gcp-podvm-image-handler.sh is located
source "$(dirname "$0")"/lib.sh

# Function to verify that the required variables are set

function verify_vars() {
  # Ensure CLOUD_PROVIDER is set to gcp
  [[ -z "${CLOUD_PROVIDER}" || "${CLOUD_PROVIDER}" != "gcp" ]] && error_exit "CLOUD_PROVIDER is empty or not set to gcp"

  required_vars=(
    # From peer-pods-cm:
    "GCP_PROJECT_ID"
    "GCP_ZONE"

    # From gcp-podvm-image-cm:
    "IMAGE_BASE_NAME"
    "IMAGE_VERSION_MAJ_MIN"
    "INSTALL_PACKAGES"
    "DISABLE_CLOUD_CONFIG"

    # From lib.sh:
    "CAA_SRC_DIR"
  )

  for var in "${required_vars[@]}"; do
    [[ -z "${!var}" ]] && error_exit "$var is not set or empty"
  done
}

# function to download and install gcloud cli

function install_gcloud_cli() {
  # Install gcloud cli
  # If any error occurs, exit the script with an error message

  # Check if gcloud cli is already installed
  if command -v gcloud &>/dev/null; then
    echo "gcloud cli is already installed"
    return
  fi

  # Download gcloud cli
  # TODO
  tee -a /etc/yum.repos.d/google-cloud-sdk.repo <<EOM
[google-cloud-cli]
name=Google Cloud CLI
baseurl=https://packages.cloud.google.com/yum/repos/cloud-sdk-el9-x86_64
enabled=1
gpgcheck=1
repo_gpgcheck=0
gpgkey=https://packages.cloud.google.com/yum/doc/rpm-package-key.gpg
EOM

  dnf install -y google-cloud-cli

  echo "GCP CLI installed successfully"
}

# Function to recreate podvm-images configmap with all the images
# TODO: Not sure if aws, azure is working either.
function recreate_image_configmap() {
  echo "Recreating podvm-images configmap not implemented"
}

# Creates a GCP Image with the PodVM distro.
function create_image() {
  echo "Creating GCP Image"
  # Create the GCP image
  # If any error occurs, exit the script with an error message

  # Install packages if INSTALL_PACKAGES is set to yes
  if [[ "${INSTALL_PACKAGES}" == "yes" ]]; then
    # Install required rpm packages
    install_rpm_packages

    # Install required binary packages
    install_binary_packages
  fi

  # Based on the value of `IMAGE_TYPE` the image is either build from scratch or
  # usingthethe prebuilt artifact.
  if [[ "${IMAGE_TYPE}" == "operator-built" ]]; then
    error_exit "Currently only pre-built is supported for GCP, exiting."
  elif [[ "${IMAGE_TYPE}" == "pre-built" ]]; then
    create_image_from_prebuilt_artifact
  fi

  # Add the image id as annotation to peer-pods-cm configmap
  update_cm_annotation "LATEST_IMAGE_ID" "${IMAGE_NAME}"

  echo "GCP image created successfully"
}

function set_image_version_and_name() {
  # Set the image version
  # It should follow the Major(int).Minor(int).Patch(int)
  IMAGE_VERSION="${IMAGE_VERSION_MAJ_MIN}.$(date +'%Y%m%d%S')"
  export IMAGE_VERSION

  # Set the image name
  IMAGE_NAME="${IMAGE_BASE_NAME}-${IMAGE_VERSION}"
  export IMAGE_NAME
}

function create_image_from_prebuilt_artifact() {
  echo "Creating GCP image from prebuilt artifact"

  # Set the IMAGE_VERSION and IMAGE_NAME
  set_image_version_and_name

  echo "Pulling the podvm image from the provided path"
  image_src="/tmp/image"
  extraction_destination_path="/image"
  image_repo_auth_file="/tmp/regauth/auth.json"

  # Get the PODVM_IMAGE_TYPE, PODVM_IMAGE_TAG and PODVM_IMAGE_SRC_PATH
  get_image_type_url_and_path

  case "${PODVM_IMAGE_TYPE}" in
  oci)
    echo "OCI: Extracting the GCP image from the provided path"
    mkdir -p "${extraction_destination_path}" || error_exit "Failed to create the image directory"

    extract_container_image "${PODVM_IMAGE_URL}" \
      "${PODVM_IMAGE_TAG}" \
      "${image_src}" \
      "${extraction_destination_path}" \
      "${image_repo_auth_file}"

    podvm_image_path="${extraction_destination_path}/rootfs/${PODVM_IMAGE_SRC_PATH}"
    ;;
  bootc)
    echo "Bootc: Extracting the GCP image from the given path"
    bootc_to_qcow2 "${PODVM_IMAGE_URL}" "${PODVM_IMAGE_TAG}"
    podvm_image_path="$(pwd)/output/qcow2/disk.qcow2"
    ;;
  *)
    error_exit "Currently only OCI and bootc image unpacking is supported, exiting."
    ;;
  esac

  # Validate file existence
  [[ -f ${podvm_image_path} ]] || error_exit "No disk file has been found in: ${podvm_image_path}"

  # Convert the podvm image to raw if it's not a raw image
  # This will set the RAW_IMAGE_PATH global variable
  convert_qcow2_to_raw "${podvm_image_path}"

  [[ -f "${RAW_IMAGE_PATH}" ]] || error_exit "RAW image not found at ${RAW_IMAGE_PATH}"

  echo "Uploading the RAW image to GCS"

  # TAR the raw image (GCP expects a compressed archive)
  tar -cvzf "/tmp/${IMAGE_NAME}.tar.gz" -C "$(dirname "${RAW_IMAGE_PATH}")" "$(basename "${RAW_IMAGE_PATH}")" ||
    error_exit "Failed to create tarball for GCP"

  # Create bucket if doesn't exist
  export GCP_BUCKET_NAME="peerpods-bucket"
  export GCP_REGION="${GCP_ZONE%-*}"

  if ! gsutil ls -b "gs://${GCP_BUCKET_NAME}/" &>/dev/null; then
    gsutil mb -p ${GCP_PROJECT_ID} -l ${GCP_REGION} gs://${GCP_BUCKET_NAME}/
  fi


  gsutil cp "/tmp/${IMAGE_NAME}.tar.gz" "gs://${GCP_BUCKET_NAME}/${IMAGE_NAME}.tar.gz" ||
    error_exit "Failed to upload the image to GCS"

  RAW_URL="gs://${GCP_BUCKET_NAME}/${IMAGE_NAME}.tar.gz"
  echo "Successfully uploaded RAW image to ${RAW_URL}"

  # Create the image from the uploaded tarball
  gcloud compute images create "${IMAGE_NAME}" \
    --source-uri="${RAW_URL}" \
    --guest-os-features=UEFI_COMPATIBLE ||
    error_exit "Failed to create GCP image"

  echo "GCP image created successfully from prebuilt artifact"
}

# function to delete the image
# IMAGE_NAME must be set as an environment variable
delete_image_using_id() {
  echo "Deleting GCP image"

  [[ -z "${IMAGE_NAME}" ]] && error_exit "IMAGE_NAME is empty"

  SOURCE_DISK=$(gcloud compute images describe "${IMAGE_NAME}" --format="value(sourceDisk)" 2>/dev/null)

  gcloud compute images delete "${IMAGE_NAME}" --quiet ||
    error_exit "Failed to delete image ${IMAGE_NAME}"

  if [[ -n "${SOURCE_DISK}" ]]; then
    gcloud compute disks delete "${SOURCE_DISK}" --quiet ||
      error_exit "Failed to delete the source disk ${SOURCE_DISK}"
  fi

  delete_cm_annotation "LATEST_IMAGE_ID"

  echo "GCP image deleted successfully"
}

# Display help message
function display_help() {
  echo "This script is used to create GCP image for podvm"
  echo "Usage: $0 [-c|-C|-R] [-- install_binaries|install_rpms|install_cli]"
  echo "Options:"
  echo "-c  Create image"
  echo "-C  Delete image"
  echo "-R  Recreate podvm-images configMap"
  echo "-h  This help"
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
    install_gcloud_cli
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
      # Create the image
      create_image
      ;;
    C)
      # Delete the image
      delete_image_using_id

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
