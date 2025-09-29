#!/bin/bash
#
# USAGE: bump-osc-versio.sh <new version> [replaced version]
#        bump-osc-versio.sh -h
#

version="$1"
replaced="$2"

if [[ "${version}" = "-h" ]] || [[ -z "${version}" ]]; then
    cat<<EOF>&2
USAGE: bump-osc-versio.sh <new version> [replaced version]
       bump-osc-versio.sh -h

Do the following :
- update version everywhere
- optionally update the version at "replaces:" in the CSV
EOF
    exit 1
fi

sed -Ei "s/[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+([^#]+## OSC_VERSION)/${version}\1/g" \
    $(git grep -El '[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+[^#]+## OSC_VERSION')

sed -Ei \
    "s/(olm.skipRange: '>=1\.1\.0 <)[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+'/\1${version}'/g" \
    config/manifests/bases/sandboxed-containers-operator.clusterserviceversion.yaml

if [[ -n "$replaced" ]]; then
    sed -Ei "s/(replaces: sandboxed-containers-operator\.v)[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+/\1${replaced}/g" \
	config/manifests/bases/sandboxed-containers-operator.clusterserviceversion.yaml
fi

major_minor()
{
    local major minor
    IFS=. read major minor rem <<< "$1"
    echo ${major}.${minor}
}

sed -Ei "s/(version=)\"[[:digit:]]+\.[[:digit:]]+\"/\1\"$(major_minor "${version}")\"/g" \
    $(git grep -El 'version=\"[[:digit:]]+\.[[:digit:]]+\"')

#
# `make bundle` applies some changes that we don't want to the following files.
#
readonly files_to_preserve=(
    "bundle.Dockerfile"
    "config/manager/kustomization.yaml"
    "config/metrics/kustomization.yaml"
    "config/manifests/bases/sandboxed-containers-operator.clusterserviceversion.yaml"
)

readonly backup_dir=`mktemp --directory -t bump-osc-version-XXXXXX`

for f in "${files_to_preserve[@]}"; do
    mkdir -p "$(dirname ${backup_dir}/${f})"
    cp -f "${f}" "${backup_dir}/${f}"
done

# Preserve the operator image
readonly imgpath=".spec.install.spec.deployments[0].spec.template.spec.containers[0].image"
export IMG="$(yq ${imgpath} bundle/manifests/sandboxed-containers-operator.clusterserviceversion.yaml)"

make bundle

# We don't want to merge these changes
for f in "${files_to_preserve[@]}"; do
    cp -f "${backup_dir}/${f}" "${f}"
done

rm -rf "${backup_dir}"
