#!/bin/bash
# Merge a PR using merge strategy
# Usage: ./merge-pr.sh --repo REPO_NAME --pr PR_NUMBER [--head-sha SHA]
#
# When --head-sha is provided, the merge is rejected if the PR's HEAD
# has changed since the snapshot (prevents merging unchecked commits).
#
# Returns JSON: {success, repo, pr, message}

set -euo pipefail

REPO_URL=""
PR_NUMBER=""
HEAD_SHA=""

# Repository mapping
declare -A REPOS=(
    ["kata-containers"]="https://github.com/openshift/kata-containers"
    ["cloud-api-adaptor"]="https://github.com/openshift/cloud-api-adaptor"
    ["compute-artifacts"]="https://github.com/openshift/confidential-compute-artifacts"
    ["podvm-scripts"]="https://github.com/confidential-devhub/coco-podvm-scripts"
    ["osc"]="https://github.com/openshift/sandboxed-containers-operator"
)

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --repo)
            if [[ -n "${REPOS[$2]:-}" ]]; then
                REPO_URL="${REPOS[$2]}"
            else
                echo "Unknown repository: $2" >&2
                exit 1
            fi
            shift 2
            ;;
        --pr)
            PR_NUMBER="$2"
            shift 2
            ;;
        --head-sha)
            HEAD_SHA="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$REPO_URL" || -z "$PR_NUMBER" ]]; then
    echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER" >&2
    exit 1
fi

REPO_NAME=$(basename "$REPO_URL")

MERGE_ARGS=("$PR_NUMBER" --repo "$REPO_URL" --merge --delete-branch)
if [[ -n "$HEAD_SHA" ]]; then
    MERGE_ARGS+=(--match-head-commit "$HEAD_SHA")
fi

if gh pr merge "${MERGE_ARGS[@]}" 2>/dev/null; then
    echo "{\"success\": true, \"repo\": \"$REPO_NAME\", \"pr\": $PR_NUMBER, \"message\": \"merged\"}" | jq '.'
else
    MSG=$(gh pr view "$PR_NUMBER" --repo "$REPO_URL" --json state,mergeStateStatus \
        --jq '"state=\(.state) mergeStateStatus=\(.mergeStateStatus)"' 2>/dev/null || echo "unknown")
    echo "{\"success\": false, \"repo\": \"$REPO_NAME\", \"pr\": $PR_NUMBER, \"message\": \"$MSG\"}" | jq '.'
    exit 1
fi
