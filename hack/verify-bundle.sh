#!/bin/bash

set -euo pipefail

readonly bundle_csv="bundle/manifests/sandboxed-containers-operator.clusterserviceversion.yaml"

function print_failure {
  echo "There are unexpected changes to the tree when running 'make bundle'. Please"
  echo "run these commands locally and double-check the Git repository for unexpected changes which may"
  echo "need to be committed."
  exit 1
}

# 'make bundle' rewrites tracked files; refuse to run on a dirty tree so that
# in-progress work is not overwritten. Untracked files are ignored.
function require_clean_tree {
  local changes
  changes="$(git status --porcelain --untracked-files=no)"
  if [ -n "${changes}" ]; then
    echo "ERROR: the working tree has uncommitted changes; commit or stash them first:"
    echo "${changes}"
    exit 1
  fi
}

require_clean_tree

# 'make bundle' injects IMG into the CSV; keep the operator image pinned
# in the committed CSV instead of drifting to the default dev image.
export IMG="$(sed -n 's/^                image: //p' "${bundle_csv}" | head -1)"
if [ -z "${IMG}" ]; then
  echo "ERROR: could not find the manager image in ${bundle_csv}"
  exit 1
fi

# operator-sdk stamps createdAt with the current time on every run, so the
# regenerated CSV always differs in that line; remember the committed value
# and restore it after generation so that only real drift shows up.
committed_created_at="$(sed -n 's/^    createdAt: "\(.*\)"$/\1/p' "${bundle_csv}" | head -1)"
if [ -z "${committed_created_at}" ]; then
  echo "ERROR: could not find createdAt in ${bundle_csv}"
  exit 1
fi

# 'make bundle' can create new manifests (for example a new CRD or CSV) as
# untracked files; record what is already untracked under bundle/ so that only
# files created by this run fail the check.
untracked_before="$(git ls-files --others --exclude-standard bundle/)"

echo "> generating the OLM bundle"
make bundle

# Undo the timestamp restamp before checking for drift.
sed -i "s|^    createdAt:.*|    createdAt: \"${committed_created_at}\"|" "${bundle_csv}"

git status
git diff

untracked_after="$(git ls-files --others --exclude-standard bundle/)"
new_untracked="$(comm -13 \
  <(printf '%s\n' "${untracked_before}" | sed '/^$/d') \
  <(printf '%s\n' "${untracked_after}" | sed '/^$/d') | sed '/^$/d')"
if [ -n "${new_untracked}" ]; then
  echo "ERROR: 'make bundle' created untracked files which must be committed:"
  echo "${new_untracked}"
  print_failure
fi

test -z "$(git status --porcelain --untracked-files=no)" || print_failure
