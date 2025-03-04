#!/usr/bin/env bash

set -xe

OCP_VERSION=$1

test -n "${OCP_VERSION}"
mkdir -p ${OCP_VERSION}
cd ${OCP_VERSION}

mkdir -p migrate/
opm migrate registry.redhat.io/redhat/redhat-operator-index:${OCP_VERSION} ./migrate/

# Generate a catalog template:
mkdir -p catalog/sandboxed-containers-operator/
opm alpha convert-template basic migrate/sandboxed-containers-operator/catalog.json > catalog-template.json

# Generate a Dockerfile:
opm generate dockerfile . \
    --base-image "brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:${OCP_VERSION}" \
    --builder-image "brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:${OCP_VERSION}"

mv ./.*Dockerfile Dockerfile

# Patch the Dockerfile to avoid copying in unwanted files
sed -i 's@^ADD . /configs@ADD catalog/ /configs@' Dockerfile

rm -r migrate
