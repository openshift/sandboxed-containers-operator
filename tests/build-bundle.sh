#!/bin/bash

# This script builds the project bundle for testing purposes.
# It patches the bundle CSV to update image references, then builds
# the bundle and catalog images with buildah and pushes them to quay.io.
#
# Note that we're not running the "make bundle" and associated calls, because
# they rely on podman, which may not be possible to do from the CI environment
# as it requires some additional privileges.
#
# We also avoid running the controller-gen tool: all we need is to replace the
# existing image references in the existing CSV - sed does that very well, without
# re-generating everything.
#
# This script needs to be called from the root of the repository.
set -ex

# List of modified images, separated by spaces.
MODIFIED_IMAGES=${MODIFIED_IMAGES:-""}

if [ -z "$MODIFIED_IMAGES" ]; then
    echo "No modified images specified. Exiting."
    exit 1
fi

function get_redhat_url_from_quay_url() {
    local REDHAT_BASE="registry.redhat.io/openshift-sandboxed-containers"
    local QUAY_URL="$1"
    case "$QUAY_URL" in
        *osc-caa-webhook*)
            echo "$REDHAT_BASE/osc-cloud-api-adaptor-webhook-rhel9"
            ;;
        *osc-caa*)
            echo "$REDHAT_BASE/osc-cloud-api-adaptor-rhel9"
            ;;
        *osc-operator*)
            echo "$REDHAT_BASE/osc-rhel9-operator"
            ;;
        *osc-dm-verity-image*)
            echo "$REDHAT_BASE/osc-dm-verity-image"
            ;;
        *osc-storage-helper*)
            echo "$REDHAT_BASE/osc-storage-helper"
            ;;
        *)
            local IMAGE_NAME
            IMAGE_NAME=$(echo "$QUAY_URL" | sed 's|quay.io/redhat-user-workloads/ose-osc-tenant/||')
            echo "$REDHAT_BASE/${IMAGE_NAME}-rhel9"
            ;;
    esac
}

# Find the bundle ClusterServiceVersion
CSV_FILE=$(ls bundle/manifests/*clusterserviceversion.yaml 2>/dev/null | head -1)
if [ -z "$CSV_FILE" ]; then
    echo "No ClusterServiceVersion file found in bundle/manifests/. Exiting."
    exit 1
fi
echo "Patching image references in: $CSV_FILE"

# Patch image references directly in the CSV (no Go toolchain needed).
# Matches both @sha256:... digest and :tag forms.
for IMAGE in $MODIFIED_IMAGES; do
    IMAGE_REPO=$(echo "$IMAGE" | cut -d':' -f1)
    IMAGE_TAG=$(echo "$IMAGE" | cut -d':' -f2)

    RH_IMAGE=$(get_redhat_url_from_quay_url "$IMAGE_REPO")

    sed -i -E "s~($RH_IMAGE)(@sha256:[^ \"]*|:[^ \"]*)~\1:$IMAGE_TAG~g" "$CSV_FILE"
done

# Build and push the bundle image with buildah (no podman/docker daemon needed)
TAG=${VERSION:-on-pr-$(date +%Y%m%d%H%M%S)}
BUNDLE_IMG="quay.io/redhat-user-workloads/ose-osc-tenant/osc-operator-bundle:${TAG}"

buildah bud --storage-driver=vfs -f bundle.Dockerfile -t "${BUNDLE_IMG}" .
buildah push --storage-driver=vfs "${BUNDLE_IMG}"

# Build and push a catalog image referencing the new bundle
CATALOG_IMAGE="quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:${TAG}"

echo "Building catalog for bundle image: ${BUNDLE_IMG}"
cd fbc
CATALOG_TEMPLATE="test-fbc/catalog-template.yaml"
sed -i "s|\(image: \).*|\1${BUNDLE_IMG}|g" "$CATALOG_TEMPLATE"
buildah bud --storage-driver=vfs -f test-fbc/Dockerfile -t "${CATALOG_IMAGE}" .
buildah push --storage-driver=vfs "${CATALOG_IMAGE}"
echo "Catalog image pushed: ${CATALOG_IMAGE}"
