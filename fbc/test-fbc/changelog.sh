#!/bin/bash

# This script is used to generate the changelog for an FBC image.
# You can call it locally with 2 commit hashes to see the changes between them.
# e.g:
#  ./changelog.sh <old-commit> <new-commit>
#  Where <old-commit> and <new-commit> are git commit hashes for changes to the
#  catalog-template.yaml file.
#
# If no arguments are provided, it will retrieve the latest built image's git tag
# and generate the changelog since that tag up to HEAD.
# This method is used in the build system to generate a changelog file that will
# be included in the built image.
#

#set -x

# Takes an image reference with digest, and looks at the quay.io repository
# for all the tags that point to the same image digest.
# One of those tags is expected to be the git tag used to build the image.
#
# See quay.io API docs for more details:
# https://docs.quay.io/api/swagger/
function get_commit_for_image() {
    if [ -z "$1"  ]; then
        echo "HEAD"
        return
    fi
    
    # Convert the reference to the quay.io api URL format
    IMAGE_REF=${1/registry.redhat.io\/openshift-sandboxed-containers\//quay.io\/api\/v1\/repository\/redhat-user-workloads\/ose-osc-tenant\/}
    # remove "-rhel9" from image names
    IMAGE_REF=$(echo $IMAGE_REF | sed 's/-rhel9//')

    # Special processing: we have a mismatch between the image name in our registry
    # and the quay.io repository name for cloud-api-adaptor and cloud-api-adaptor-webhook.
    # Fix it to that we can get the right image info from quay.io
    IMAGE_REF=$(echo $IMAGE_REF | sed 's/osc-cloud-api-adaptor/osc-caa/' | sed 's/osc-cloud-api-adaptor-webhook/osc-caa-webhook/')

    IMAGE_NAME=$(echo "$IMAGE_REF" | cut -d "@" -f 1)
    IMAGE_DIGEST=$(echo "$IMAGE_REF" | cut -d "@" -f 2)

    # As the quay.io API is paginated, we need to make a loop until we find
    # the image we're looking for.
    PAGE=1
    while true; do
        RESPONSE=$(curl -s "https://$IMAGE_NAME/tag/?limit=100&page=${PAGE}")
        if [ $? -ne 0 ]; then
            # Image not found?
            echo "Error: Failed to retrieve tags from quay.io for image $IMAGE_NAME" >&2
            break
        fi
        TAGS=$(echo "$RESPONSE" | jq -r '.tags[] | select(.manifest_digest=="'"$IMAGE_DIGEST"'") | .name')
        if [ $? -ne 0 ]; then
            echo "Error: Failed to parse JSON response from quay.io for image $IMAGE_NAME" >&2
            break
        fi
        if [ -n "$TAGS" ]; then
            # found some tags for our image
            for TAG in $TAGS; do
                # Check if the tag looks like a git commit hash (7 to 40 hex characters)
                if [[ $TAG =~ ^[0-9a-f]{7,40}$ ]]; then
                    echo "$TAG"
                    return
                fi
            done
        fi
        HAS_MORE=$(echo "$RESPONSE" | jq -r '.has_additional')
        [[ "$HAS_MORE" != "true" ]] && break
        PAGE=$((PAGE + 1))
    done
}

function get_repo_for_image() {
    COMPONENT=$(echo "$1" | sed 's/registry.redhat.io\/openshift-sandboxed-containers\///' | sed 's/-rhel9//' | cut -d "@" -f 1)
    case "$COMPONENT" in
        "osc-operator" | "osc-must-gather" | "osc-podvm-builder")
            echo "https://github.com/openshift/sandboxed-containers-operator"
            ;;
        "osc-cloud-api-adaptor" | "osc-cloud-api-adaptor-webhook" | "osc-podvm-payload")
            echo "https://github.com/openshift/cloud-api-adaptor"
            ;;
        "osc-monitor")
            echo "https://github.com/openshift/kata-containers"
            ;;
        "osc-dm-verity-image")
            echo "https://github.com/confidential-devhub/coco-podvm-scripts"
            ;;
        "osc-pccs" | "osc-tdx-qgs" | "osc-storage-helper")
            echo "https://github.com/openshift/confidential-compute-artifacts"
            ;;
    esac
}

if [ "$#" -eq 2 ]; then
    OLD_COMMIT=$1
    NEW_COMMIT=$2
else
    # retrieve the git tag used to build the latest image
    echo "No commit range provided, retrieving latest built image commit"
    OLD_COMMIT=$(skopeo inspect "docker://quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:latest" | jq -r '.Labels["org.opencontainers.image.revision"]')
    NEW_COMMIT=HEAD
fi

# Get the bundle references for old and new commits
OLD_BUNDLE=$(git show "${OLD_COMMIT}" catalog-template.yaml | grep -E '^\+  - image:'| awk '{print $NF}')
NEW_BUNDLE=$(git show "${NEW_COMMIT}" catalog-template.yaml | grep -E '^\+  - image:'| awk '{print $NF}')

# Retrieve the commit hashes for old and new bundles
OLD_BUNDLE_COMMIT=$(get_commit_for_image $OLD_BUNDLE)
NEW_BUNDLE_COMMIT=$(get_commit_for_image $NEW_BUNDLE)

# Generate the list of commits between the two bundle
COMMIT_LIST=$(git log --oneline "${OLD_BUNDLE_COMMIT}..${NEW_BUNDLE_COMMIT}" ../../bundle/manifests/sandboxed-containers-operator.clusterserviceversion.yaml | awk '{print $1}')

echo "Generating changelog between bundle commits $OLD_BUNDLE_COMMIT and $NEW_BUNDLE_COMMIT" | tee CHANGELOG
for COMMIT in $COMMIT_LIST; do
    # each bundle commit should have exactly one image change
    # extract old and new image references, and get their commit hashes
    OLD_IMAGE=$(git show $COMMIT | grep "image:" | grep -E '^-' -m 1 | awk '{print $NF}')
    NEW_IMAGE=$(git show $COMMIT | grep "image:" | grep -E '^\+' -m 1 | awk '{print $NF}')

    # Display the commit message first
    echo "Changes in commit $COMMIT:" | tee -a CHANGELOG
    git show --no-patch --format="%s" $COMMIT | tee -a CHANGELOG

    # if there is no NEW_IMAGES, we can't proceed with image's changelog.
    # Just continue to the next commit.
    if [ -z "$NEW_IMAGE" ]; then
        echo "" | tee -a CHANGELOG
        continue
    fi

    echo "From image: $OLD_IMAGE" | tee -a CHANGELOG
    echo "To image:   $NEW_IMAGE" | tee -a CHANGELOG

    OLD_IMAGE_COMMIT=$(get_commit_for_image $OLD_IMAGE)
    NEW_IMAGE_COMMIT=$(get_commit_for_image $NEW_IMAGE)

    REPO=$(get_repo_for_image $OLD_IMAGE)
    echo "Looking at commits from the repo $REPO" | tee -a CHANGELOG
    echo "between $OLD_IMAGE_COMMIT and $NEW_IMAGE_COMMIT" | tee -a CHANGELOG

    PATH_PREFIX=""
    if [ "$REPO" != "https://github.com/openshift/sandboxed-containers-operator" ]; then
        # Do not clone sandboxed-containers-operator: that's the local repo already!
        git clone $REPO
        pushd $(basename $REPO)
        PATH_PREFIX="../"
    fi

    git log --oneline $OLD_IMAGE_COMMIT..$NEW_IMAGE_COMMIT | tee -a ${PATH_PREFIX}CHANGELOG
    if [ "$REPO" != "https://github.com/openshift/sandboxed-containers-operator" ]; then
        popd
        rm -rf $(basename $REPO)
    fi
    echo "" | tee -a CHANGELOG
done
