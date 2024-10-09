#!/bin/bash
#

# Function to install Azure deps
function install_azure_deps() {
  echo "Installing Azure deps"
  # Install the required packages
  /scripts/azure-podvm-image-handler.sh -- install_cli
  /scripts/azure-podvm-image-handler.sh -- install_binaries
}

# Function to install AWS deps
function install_aws_deps() {
  echo "Installing AWS deps"
  # Install the required packages
  /scripts/aws-podvm-image-handler.sh -- install_cli
  /scripts/aws-podvm-image-handler.sh -- install_binaries
}

# Function to install libvirt deps
function install_libvirt_deps() {
  echo "Installing libvirt deps"
  # Install the required packages
  if [[ "$1" == "pre-config" ]] || [[ "$1" == "config-cleanup" ]]; then
    /scripts/libvirt-podvm-image-handler.sh -- install_pre_config
  else
    /scripts/libvirt-podvm-image-handler.sh -- install_binaries
  fi
}


# Function to check if peer-pods-cm configmap exists
function check_peer_pods_cm_exists() {
  if kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
    return 0
  else
    return 1
  fi
}

# Function to check if the image should be built or use the pre-built artifact.
function set_podvm_image_type() {
    echo "Checking if PODVM_IMAGE_URI is set"

    # If the value of the PODVM_IMAGE_URI is empty or not set, then build the image from scratch else use the prebuilt artifact.
    if [[ -z "${PODVM_IMAGE_URI}" ]]; then
      IMAGE_TYPE="operator-built"
      echo "Initiating the operator to build the podvm image"
    else
      IMAGE_TYPE="pre-built"
      echo "Initiating the operator to use the pre-built podvm image"
    fi
    export IMAGE_TYPE
}

function pre_config_func() {
  case "${CLOUD_PROVIDER}" in
  libvirt)
    /scripts/libvirt-config-manager.sh create
    ;;
  *)
    echo "CLOUD_PROVIDER is not set to libvirt"
    exit 1
    ;;
  esac
}

function config_cleanup_func() {
  # Destory Libvirt pool, volume and neccesary pre-requisites and other resourcses created.
  case "${CLOUD_PROVIDER}" in
  libvirt)
    /scripts/libvirt-config-manager.sh clean
    ;;
  *)
    echo "CLOUD_PROVIDER is not set to libvirt"
    exit 1
    ;;
  esac
}

# Function to create podvm image
function create_podvm_image() {
  case "${CLOUD_PROVIDER}" in
  azure)
    echo "Creating Azure image"
    /scripts/azure-podvm-image-handler.sh -c
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then
      # Check if peer-pods-cm configmap exists
      if ! check_peer_pods_cm_exists; then
        echo "peer-pods-cm configmap does not exist. Skipping the update of peer-pods-cm"
        exit 0
      fi
      # Get the IMAGE_ID from the LATEST_IMAGE_ID annotation key in peer-pods-cm configmap
      IMAGE_ID=$(kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.metadata.annotations.LATEST_IMAGE_ID}')

      # if IMAGE_ID is not set, then exit
      if [ -z "${IMAGE_ID}" ]; then
        echo "IMAGE_ID is not set in peer-pods-cm. Skipping the update of peer-pods-cm"
        exit 1
      fi

      # Update peer-pods-cm configmap with the IMAGE_ID value
      echo "Updating peer-pods-cm configmap with IMAGE_ID=${IMAGE_ID}"
      kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator --type merge -p "{\"data\":{\"AZURE_IMAGE_ID\":\"${IMAGE_ID}\"}}"

    fi
    ;;
  aws)
    echo "Creating AWS AMI"
    /scripts/aws-podvm-image-handler.sh -c
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then
      # Check if peer-pods-cm configmap exists
      if ! check_peer_pods_cm_exists; then
        echo "peer-pods-cm configmap does not exist. Skipping the update of peer-pods-cm"
        exit 0
      fi
      # Get the AMI_ID from the LATEST_AMI_ID annotation key in peer-pods-cm configmap
      AMI_ID=$(kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.metadata.annotations.LATEST_AMI_ID}')

      # if AMI_ID is not set, then exit
      if [ -z "${AMI_ID}" ]; then
        echo "AMI_ID is not set in peer-pods-cm. Skipping the update of peer-pods-cm"
        exit 1
      fi

      # Update peer-pods-cm configmap with the AMI_ID value
      echo "Updating peer-pods-cm configmap with AMI_ID=${AMI_ID}"
      kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator --type merge -p "{\"data\":{\"PODVM_AMI_ID\":\"${AMI_ID}\"}}"
    fi
    ;;
  libvirt)
    echo "Creating Libvirt qcow2"
    /scripts/libvirt-podvm-image-handler.sh -c
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then
      # Check if peer-pods-cm configmap exists
      if ! check_peer_pods_cm_exists; then
        echo "peer-pods-cm configmap does not exist. Skipping the update of peer-pods-cm"
        exit 1
      fi
    fi
    ;;
  *)
    echo "CLOUD_PROVIDER is not set to azure or aws or libvirt"
    exit 1
    ;;
  esac
}

# Function to delete podvm image
# IMAGE_ID or AMI_ID is the input and expected to be set
# These are checked in individual cloud provider scripts and if not set, the script will exit
# Accepts two optional arguments
# -f : force delete the image
# -g : delete the image gallery

function delete_podvm_image() {

  local args=("$@")
  local force=false
  local delete_gallery=false

  for ((i = 0; i < ${#args[@]}; i++)); do
    case "${args[$i]}" in
    -f) force=true ;;
    -g) delete_gallery=true ;;
    esac
  done

  # Check for the existence of peer-pods-cm configmap. If not present, then exit
  if ! check_peer_pods_cm_exists; then
    echo "peer-pods-cm configmap does not exist. Skipping image deletion"
    exit 0
  fi

  case "${CLOUD_PROVIDER}" in
  azure)
    # If IMAGE_ID is not set, then exit
    if [ -z "${IMAGE_ID}" ]; then
      echo "IMAGE_ID is not set. Skipping the deletion of Azure image"
      exit 1
    fi

    AZURE_IMAGE_ID=$(kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.data.AZURE_IMAGE_ID}')

    # If AZURE_IMAGE_ID is not set, then exit
    if [ -z "${AZURE_IMAGE_ID}" ]; then
      echo "AZURE_IMAGE_ID is not set in peer-pods-cm. Skipping the deletion of Azure image"
      exit 1
    fi

    # check if the AZURE_IMAGE_ID value in peer-pods-cm is same as the input IMAGE_ID
    # If yes, then don't delete the image unless force option is provided
    if [ "${AZURE_IMAGE_ID}" == "${IMAGE_ID}" ]; then
      if ! ${force}; then
        echo "AZURE_IMAGE_ID in peer-pods-cm is same as the input image to be deleted. Skipping the deletion of Azure image"
        exit 0
      fi
    fi

    echo "Deleting Azure image $IMAGE_ID"
    /scripts/azure-podvm-image-handler.sh -C

    # Update the peer-pods-cm configmap and remove the AZURE_IMAGE_ID value
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then
      kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator --type merge -p "{\"data\":{\"AZURE_IMAGE_ID\":\"\"}}"
    fi

    # If delete_gallery is set, then delete the image gallery
    if ${delete_gallery}; then
      echo "Deleting Azure image gallery (by force) since -g option is set"
      delete_podvm_image_gallery -f
    fi

    ;;
  aws)
    # If AMI_ID is not set, then exit
    if [ -z "${AMI_ID}" ]; then
      echo "AMI_ID is not set. Skipping the deletion of AWS AMI"
      exit 1
    fi

    PODVM_AMI_ID=$(kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.data.PODVM_AMI_ID}')

    # If PODVM_AMI_ID is not set, then exit
    if [ -z "${PODVM_AMI_ID}" ]; then
      echo "PODVM_AMI_ID is not set in peer-pods-cm. Skipping the deletion of AWS AMI"
      exit 1
    fi

    # check if the PODVM_AMI_ID value in peer-pods-cm is same as the input AMI_ID
    # If yes, then don't delete the image unless force option is provided
    if [ "${PODVM_AMI_ID}" == "${AMI_ID}" ]; then
      if [ "$1" != "-f" ]; then
        echo "PODVM_AMI_ID in peer-pods-cm is same as the input image to be deleted. Skipping the deletion of AWS AMI"
        exit 0
      fi
    fi

    echo "Deleting AWS AMI"
    /scripts/aws-podvm-image-handler.sh -C

    # Update the peer-pods-cm configmap and remove the PODVM_AMI_ID value
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then
      kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator --type merge -p "{\"data\":{\"PODVM_AMI_ID\":\"\"}}"
    fi

    ;;
    libvirt)
    # If LIBVIRT_IMAGE_ID is not set, then exit
    if [ -z "${LIBVIRT_IMAGE_ID}" ]; then
      echo "LIBVIRT_IMAGE_ID is not set. Skipping the deletion of libvirt image"
      exit 1
    fi

    LIBVIRT_IMAGE=$(kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.data.LIBVIRT_IMAGE_ID}')

    # If LIBVIRT_IMAGE is not set, then exit
    if [ -z "${LIBVIRT_IMAGE}" ]; then
      echo "LIBVIRT_IMAGE_ID is not set in peer-pods-cm. Skipping the deletion of Libvirt image"
      exit 1
    fi

    # check if the LIBVIRT_IMAGE value in peer-pods-cm is same as the input LIBVIRT_IMAGE_ID
    # If yes, then don't delete the image unless force option is provided
    if [ "${LIBVIRT_IMAGE_ID}" == "${LIBVIRT_IMAGE}" ]; then
      if [ "$1" != "-f" ]; then
        echo "LIBVIRT_IMAGE_ID in peer-pods-cm is same as the input image to be deleted. Skipping the deletion of Libvirt image"
        exit 0
      fi
    fi

    echo "Deleting Libvirt image id"
    /scripts/libvirt-podvm-image-handler.sh -C

    # Update the peer-pods-cm configmap and remove the LIBVIRT_IMAGE_ID value
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then
      kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator --type merge -p "{\"data\":{\"LIBVIRT_IMAGE_ID\":\"\"}}"
    fi

    ;;
  *)
    echo "CLOUD_PROVIDER is not set to azure or aws or libvirt"
    exit 1
    ;;
  esac
}

# Delete the podvm image gallery in Azure
# It accepts an optional argument
# -f : force delete the image gallery

function delete_podvm_image_gallery() {
  echo "Deleting Azure image gallery"
  # Check if CLOUD_PROVIDER is set to azure, otherwise return
  if [ "${CLOUD_PROVIDER}" != "azure" ]; then
    echo "CLOUD_PROVIDER is not Azure"
    return
  fi

  # Check if peer-pods-cm configmap exists
  if ! check_peer_pods_cm_exists; then
    echo "peer-pods-cm configmap does not exist. Skipping image gallery deletion"
    exit 0
  fi

  # Get the IMAGE_GALLERY_NAME from the IMAGE_GALLERY_NAME annotation key in peer-pods-cm configmap
  IMAGE_GALLERY_NAME=$(kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator -o jsonpath='{.metadata.annotations.IMAGE_GALLERY_NAME}')

  # If IMAGE_GALLERY_NAME is not set, then exit
  if [ -z "${IMAGE_GALLERY_NAME}" ]; then
    echo "IMAGE_GALLERY_NAME is not set in peer-pods-cm. Skipping image gallery deletion"
    exit 0
  fi

  if [ "$1" == "-f" ]; then
    /scripts/azure-podvm-image-handler.sh -G force
  else
    /scripts/azure-podvm-image-handler.sh -G
  fi
}

function display_usage() {
  echo "Usage: $0 {create|delete [-f] [-g]|delete-gallery [-f]}"
}

# Set the PodVM image type based on the `PODVM_IMAGE_URI`
set_podvm_image_type

# Check if CLOUD_PROVIDER is set to azure or aws or libvirt
# Install the required dependencies
case "${CLOUD_PROVIDER}" in
azure)
  install_azure_deps
  ;;
aws)
  install_aws_deps
  ;;
libvirt)
  install_libvirt_deps
  ;;
*)
  echo "CLOUD_PROVIDER is not set to azure or aws or libvirt"
  display_usage
  exit 1
  ;;
esac

# Call the function to create or delete podvm image based on argument
case "$1" in
create)
  create_podvm_image
  ;;
delete)
  # Pass the arguments to delete_podvm_image function except the first argument
  shift
  delete_podvm_image "$@"
  ;;
delete-gallery)
  delete_podvm_image_gallery "$2"
  ;;
pre-config)
  pre_config_func
  ;;
config-cleanup)
  config_cleanup_func
  ;;
*)
  display_usage
  exit 1
  ;;
esac
