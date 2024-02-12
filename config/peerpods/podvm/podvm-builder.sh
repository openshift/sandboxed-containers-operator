#!/bin/bash
#

# Function to check if peer-pods-cm configmap exists
function check_peer_pods_cm_exists() {
  if kubectl get configmap peer-pods-cm -n openshift-sandboxed-containers-operator >/dev/null 2>&1; then
    return 0
  else
    return 1
  fi
}

# function to create podvm image

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
  *)
    echo "CLOUD_PROVIDER is not set to azure or aws"
    exit 1
    ;;
  esac
}

# Function to delete podvm image
# IMAGE_ID or AMI_ID is the input and expected to be set
# These are checked in individual cloud provider scripts and if not set, the script will exit

function delete_podvm_image() {

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
      if [ "$1" != "-f" ]; then
        echo "AZURE_IMAGE_ID in peer-pods-cm is same as the input image to be deleted. Skipping the deletion of Azure image"
        exit 0
      fi
    fi

    echo "Deleting Azure image"
    /scripts/azure-podvm-image-handler.sh -C

    # Update the peer-pods-cm configmap and remove the AZURE_IMAGE_ID value
    if [ "${UPDATE_PEERPODS_CM}" == "yes" ]; then
      kubectl patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator --type merge -p "{\"data\":{\"AZURE_IMAGE_ID\":\"\"}}"
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
  *)
    echo "CLOUD_PROVIDER is not set to azure or aws"
    exit 1
    ;;
  esac
}

# Delete the podvm image gallery in Azure

function delete_podvm_image_gallery() {
  echo "Deleting Azure image gallery"
  # Check if CLOUD_PROVIDER is set to azure, otherwise return
  if [ "${CLOUD_PROVIDER}" != "azure" ]; then
    echo "CLOUD_PROVIDER is not Azure"
    return
  fi

  # Check if force option is passed
  if [ "$1" == "-f" ]; then
    /scripts/azure-podvm-image-handler.sh -G force
  else
    /scripts/azure-podvm-image-handler.sh -G
  fi
}

# Call the function to create or delete podvm image based on argument

case "$1" in
create)
  create_podvm_image
  ;;
delete)
  delete_podvm_image "$2"
  ;;
delete-gallery)
  delete_podvm_image_gallery "$2"
  ;;
*)
  echo "Usage: $0 {create|delete [-f]|delete-gallery [-f]}"
  exit 1
  ;;
esac
