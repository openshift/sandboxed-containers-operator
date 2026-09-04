#!/bin/bash

# This script builds the project bundle for testing purposes.
# It takes a list of modified images in an environment variable, and modifies
# the CSV for the bundle accordingly. Then it builds this bundle, and finally
# builds a corresponding catalog.
# Both bundle and catalog are pushed to quay and can then be used for testing.
#
# This script needs to be called from the root of the repository.
set -ex

# List of modified images, separated by newlines.
MODIFIED_IMAGES=${MODIFIED_IMAGES:-""}
MODIFIED_IMAGES="quay.io/littlejawa/osc-operator:v1.11.1"

# target image repository: provide the operator's image base. The bundle's image
# will be derived by adding "-bundle" to it.
IMAGE_TAG_BASE=${IMAGE_TAG_BASE:-"quay.io/redhat-user-workloads/ose-osc-tenant/osc-operator"}
IMAGE_TAG_BASE="quay.io/littlejawa/osc-operator"

if [ -z "$MODIFIED_IMAGES" ]; then
    echo "No modified images specified. Exiting."
    exit 1
fi

function get_redhat_url_from_quay_url() {
    local REDHAT_BASE="registry.redhat.io/openshift-sandboxed-containers"
    local QUAY_URL="$1"
    case "$QUAY_URL" in
        *osc-operator*)
            echo "$REDHAT_BASE/osc-rhel9-operator"
            return 0
            ;;
        *osc-caa*)
            echo "$REDHAT_BASE/osc-cloud-api-adaptor-rhel9"
            return 0
            ;;
        *osc-caa-webhook*)
            echo "$REDHAT_BASE/osc-cloud-api-adaptor-webhook-rhel9"
            return 0
            ;;
        *)
            local IMAGE_NAME=$(echo "$QUAY_URL" | sed 's|quay.io/redhat-user-workloads/ose-osc-tenant/||' | cut -d'@' -f1)
            echo "$REDHAT_BASE/${IMAGE_NAME}-rhel9"
            return 0
            ;;
    esac
}

# Edit the config/manager/manager.yaml file to update the image references.
MANAGER_YAML="config/manager/manager.yaml"
for IMAGE in $MODIFIED_IMAGES; do
    IMAGE_NAME=$(echo "$IMAGE" | cut -d':' -f1)
    IMAGE_TAG=$(echo "$IMAGE" | cut -d':' -f2)

    # Convert the quay.io image reference to the redhat registry format
    IMAGE_NAME=$(get_redhat_url_from_quay_url "$IMAGE_NAME")

    sed -i "s|\($IMAGE_NAME:\).*|\1$IMAGE_TAG|g" "$MANAGER_YAML"
done

# build the bundle with modified references
TAG=on-pr-$(date +%Y%m%d%H%M%S)
export BUNDLE_IMG="quay.io/littlejawa/osc-operator-bundle:${TAG}"
export IMAGE_TAG_BASE
make bundle       # generate bundle manifests
make bundle-build # build the bundle image
make bundle-push  # push the image to the repository

# Now we need to build a catalog that references this new bundle image.
# We will use the fbc/test-fbc scripts to do that.
CATALOG_IMAGE="quay.io/redhat-user-workloads/ose-osc-tenant/test-fbc:${TAG}"
CATALOG_IMAGE="quay.io/littlejawa/test-fbc:${TAG}"

echo "Building catalog for bundle image: $BUNDLE_IMG"
cd fbc
# edit the catalog-template.yaml to reference our new bundle image
CATALOG_TEMPLATE="test-fbc/catalog-template.yaml"
sed -i "s|\(image: \).*|\1$BUNDLE_IMAGE|g" "$CATALOG_TEMPLATE"
podman build -f test-fbc/Dockerfile -t "$CATALOG_IMAGE" .
podman push "$CATALOG_IMAGE"
echo "Catalog image pushed: $CATALOG_IMAGE"
