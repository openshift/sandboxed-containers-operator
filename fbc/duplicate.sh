#!/usr/bin/env bash

set -xe

# Get command line arguments.
CURRENT_OCP_VERSION=$1
NEW_OCP_VERSION=$2

# Make sure arguments are defined.
test -n "$CURRENT_OCP_VERSION"
test -n "$NEW_OCP_VERSION"

function escape_with_dash () {
    echo "$1" | sed 's/\./-/' | sed 's/v//'
}

CURRENT_OCP_VERSION_DASH=$(escape_with_dash "$CURRENT_OCP_VERSION")
NEW_OCP_VERSION_DASH=$(escape_with_dash "$NEW_OCP_VERSION")

# Define directory variables.
GIT_TOP_DIR=$(git rev-parse --show-toplevel)
TEKTON_DIR="$GIT_TOP_DIR/.tekton"
FBC_DIR="$GIT_TOP_DIR/fbc"

# Create the PipelineRuns to build the new FBC.
function new_tekton () {
    pushd $TEKTON_DIR
        for FILE in $(ls | grep fbc | grep $CURRENT_OCP_VERSION_DASH); do
            NEW_FILE=$(echo $FILE | sed "s/$CURRENT_OCP_VERSION_DASH/$NEW_OCP_VERSION_DASH/g")
            # Copy the file.
            cp $FILE $NEW_FILE
            # Update all occurrences of the version string in the file.
            sed -i "s/$CURRENT_OCP_VERSION/$NEW_OCP_VERSION/g" "$NEW_FILE"
            sed -i "s/$CURRENT_OCP_VERSION_DASH/$NEW_OCP_VERSION_DASH/g" "$NEW_FILE"
        done
    popd
}

# Create the new FBC.
function new_catalog () {
    pushd $FBC_DIR
        # Copy the folder.
        cp -r "$CURRENT_OCP_VERSION" "$NEW_OCP_VERSION"

        # Update the base image version in the Dockerfile.
        sed -i "s/$CURRENT_OCP_VERSION/$NEW_OCP_VERSION/" "$NEW_OCP_VERSION/Dockerfile"
    popd
}

new_tekton
new_catalog

set +x

echo "
Done. Now review the result and run the render script."
