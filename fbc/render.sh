#!/usr/bin/env sh

# Print what you're doing, exit on error.
set -xe

OCP_VERSIONS=$1

test -n "$OCP_VERSIONS" || OCP_VERSIONS="v4.*"

BUILD_REGISTRY="quay.io/redhat-user-workloads/ose-osc-tenant"
RELEASE_REGISTRY="registry.redhat.io"
PACKAGE_NAME="sandboxed-containers-operator"
TEMPLATE_NAME="catalog-template.yaml"
ICON="icon.png"
ICON_BASE64="$ICON.base64"

echo

base64 "$ICON" > "$ICON_BASE64"

for OCP_VERSION in $OCP_VERSIONS
do
    pushd "$OCP_VERSION"
    # Switch to the build registry, so `opm` can pull freely.
    sed -i "s|$RELEASE_REGISTRY|$BUILD_REGISTRY|" "$TEMPLATE_NAME"

    # Add the icon data.
    yq -i ".entries[0].icon.base64data = \"$(cat ../$ICON_BASE64)\"" "$TEMPLATE_NAME"

    # enable migrate params for OCP 4.17 and onwards
    OCP_VERSION_NUMERAL=$(echo $OCP_VERSION | grep -o -E '[0-9.]+')
    if [ "`echo "${OCP_VERSION_NUMERAL} > 4.16" | bc`" -eq 1 ]; then
        MIGRATE_PARAM="--migrate-level bundle-object-to-csv-metadata"
    else
        MIGRATE_PARAM=""
    fi

    # Render that template. It's what we're here for.
    opm $MIGRATE_PARAM alpha render-template basic "$TEMPLATE_NAME" > catalog/${PACKAGE_NAME}/catalog.json

    # Switch back to the release registry.
    sed -i "s|$BUILD_REGISTRY|$RELEASE_REGISTRY|" "$TEMPLATE_NAME"
    sed -i "s|$BUILD_REGISTRY|$RELEASE_REGISTRY|" catalog/${PACKAGE_NAME}/catalog.json
    # Remove the icon base64 data.
    yq -i ".entries[0].icon.base64data = \"\"" "$TEMPLATE_NAME"

    popd
    echo
done

# No more debug. All went good.
set +x

echo "
Done."
