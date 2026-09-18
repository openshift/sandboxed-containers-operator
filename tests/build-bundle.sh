#!/bin/bash

# This script patches the bundle CSV to update image references with
# the exact digests from a Konflux Snapshot.  It is designed to run
# WITHOUT registry credentials; a separate trusted publish step handles
# image building and pushing.
#
# Prerequisites (must be on PATH):
#   - sed, diff
#
# Environment:
#   SNAPSHOT_IMAGES  — newline-separated list of quay.io/...@sha256:... refs
#
# This script needs to be called from the root of the repository.
set -ex

SNAPSHOT_IMAGES=${SNAPSHOT_IMAGES:-""}

if [ -z "$SNAPSHOT_IMAGES" ]; then
    echo "No snapshot images specified. Exiting."
    exit 1
fi

function get_redhat_url_from_quay_url() {
    local REDHAT_BASE="registry.redhat.io/openshift-sandboxed-containers"
    local QUAY_URL="$1"
    case "$QUAY_URL" in
        *osc-operator-bundle*|*osc-test-fbc*)
            ;;
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

CSV_FILE=$(ls bundle/manifests/*clusterserviceversion.yaml 2>/dev/null | head -1)
if [ -z "$CSV_FILE" ]; then
    echo "No ClusterServiceVersion file found in bundle/manifests/. Exiting."
    exit 1
fi
echo "Patching image references in: $CSV_FILE"

cp "$CSV_FILE" "${CSV_FILE}.orig"

for IMAGE in $SNAPSHOT_IMAGES; do
    IMAGE_REPO=$(echo "$IMAGE" | cut -d'@' -f1)
    RH_IMAGE=$(get_redhat_url_from_quay_url "$IMAGE_REPO")
    [ -z "$RH_IMAGE" ] && continue
    IMAGE_DIGEST=$(echo "$IMAGE" | cut -d'@' -f2)
    sed -i -E "s~(${RH_IMAGE})(@sha256:[^ \"]*|:[^ \"]*)~\1@${IMAGE_DIGEST}~g" "$CSV_FILE"
done

diff "${CSV_FILE}.orig" "$CSV_FILE" > modified-images.diff || true
rm -f "${CSV_FILE}.orig"
echo "Modified image references:"
cat modified-images.diff
