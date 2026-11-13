#!/bin/bash

set -euo pipefail

function print_failure {
  echo "There are unexpected changes to the tree when running 'make generate'. Please"
  echo "run these commands locally and double-check the Git repository for unexpected changes which may"
  echo "need to be committed."
  exit 1
}

# 'make generate' rewrites tracked files; refuse to run on a dirty tree so that
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

# 'make generate' can create new zz_generated files as untracked files; record
# what is already untracked so that only files created by this run fail.
untracked_before="$(git ls-files --others --exclude-standard)"

echo "> generating deepcopy"
make generate

git status
git diff

untracked_after="$(git ls-files --others --exclude-standard)"
new_untracked="$(comm -13 \
  <(printf '%s\n' "${untracked_before}" | sed '/^$/d') \
  <(printf '%s\n' "${untracked_after}" | sed '/^$/d') | sed '/^$/d')"
if [ -n "${new_untracked}" ]; then
  echo "ERROR: 'make generate' created untracked files which must be committed:"
  echo "${new_untracked}"
  print_failure
fi

test -z "$(git status --porcelain --untracked-files=no)" || print_failure
