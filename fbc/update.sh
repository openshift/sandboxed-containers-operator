#!/usr/bin/env sh

# Print what you're doing, exit on error.
set -xe

# Which OCP versions are we running this for.
versions="$@"

[ -n "${versions}" ] || versions="v4.*"

# Check that a folder exists for the versions you set.
ls ${versions} > /dev/null

# Make script fail when API requests fail.
alias curl="curl --fail"

# Get the latest merge commit for the bundle. This is the string the bundle image is tagged with.
get_tag () {
    local commits_url="https://api.github.com/repos/openshift/sandboxed-containers-operator/commits?per_page=1&path=bundle"
    local commit=$(curl "${commits_url}" | jq -r '.[0].sha')
    local pulls_url="https://api.github.com/repos/openshift/sandboxed-containers-operator/commits/${commit}/pulls"
    curl "${pulls_url}" | jq -r '.[0].merge_commit_sha'
}

# Get the digest for a tagged image. Pass the <TAG> as the first argument.
# Quay API docs are at https://docs.quay.io/api/swagger/#!/tag/listRepoTags.
get_digest () {
    local tag="$1"
    local url="https://quay.io/api/v1/repository/redhat-user-workloads/ose-osc-tenant/osc-operator-bundle/tag?specificTag=${tag}"
    curl -L "${url}" | jq -r '.tags[0].manifest_digest'
}

# Update the last bundle image digest in the <FILE> with the new <DIGEST>
replace_digest_last () {
    local digest="$1"
    local file="$2"

    local old_digest=$(yq '.entries[] | select(.schema == "olm.bundle") | .image' "${file}" | tail -n1 | sed 's/.*@//')

    sed -i "s/${old_digest}/${digest}/" "${file}"
}

tag="$(get_tag)"
digest="$(get_digest ${tag})"

for version in ${versions}; do
    file="${version}/catalog-template.yaml"
    replace_digest_last "${digest}" "${file}"
done

# No more debug. All went good.
set +x

echo "
Done."
