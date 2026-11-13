#!/usr/bin/env bash

set -o nounset
set -o pipefail
set -o errexit

# Tidies the dependencies in all Go modules to verify there are no
# uncommitted changes. Untracked files are ignored unless tidying creates
# them.
function update_deps {
  # Clear GOFLAGS so inherited flags (e.g. GOFLAGS=-mod=vendor) do not
  # interfere with tidying the modules.
  export GOFLAGS=""

  # Record what is already untracked so that only files created by this run
  # are treated as drift.
  untracked_before="$(git ls-files --others --exclude-standard)"

  # Tidy all modules
  go mod tidy
  go mod tidy -C ./test/e2e

  # Check for changes to tracked files.
  changes="$(git status --porcelain --untracked-files=no)"
  if [[ -n "${changes}" ]]; then
    echo "ERROR: 'go mod tidy' changed tracked files. Please commit them:"
    echo "${changes}"
    exit 1
  fi

  # Check for files created by tidying.
  untracked_after="$(git ls-files --others --exclude-standard)"
  new_untracked="$(comm -13 \
    <(printf '%s\n' "${untracked_before}" | sed '/^$/d') \
    <(printf '%s\n' "${untracked_after}" | sed '/^$/d') | sed '/^$/d')"
  if [[ -n "${new_untracked}" ]]; then
    echo "ERROR: 'go mod tidy' created untracked files which must be committed:"
    echo "${new_untracked}"
    exit 1
  fi
}

# Rewrites go.mod/go.sum; refuse to run on a dirty tree so that in-progress
# work is not overwritten. Untracked files are ignored.
function require_clean_tree {
  local changes
  changes=$(git status --porcelain --untracked-files=no)
  if [[ -n "${changes}" ]]; then
    echo "ERROR: the working tree has uncommitted changes; commit or stash them first:"
    echo "${changes}"
    exit 1
  fi
}

##############################################
###############  MAIN  #######################
##############################################

# Check all dependencies are committed
require_clean_tree
update_deps
