#!/bin/bash
# Identify which components will be rebuilt by a PR
# Usage: ./get-pr-components.sh --repo REPO_NAME --pr PR_NUMBER
#
# Outputs JSON array of components that will be rebuilt

set -euo pipefail

REPO_URL=""
PR_NUMBER=""

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

# Get PR status checks
pr_data=$(gh pr view "$PR_NUMBER" \
    --repo "$REPO_URL" \
    --json statusCheckRollup)

# Extract component names from Konflux checks
# Pattern: "Red Hat Konflux / {component-name}-on-pull-request"
# Exclude: enterprise-contract checks (validation, not component builds)
# Note: statusCheckRollup can be null for fresh PRs without checks
components=$(echo "$pr_data" | jq -r '
    [(.statusCheckRollup // [])[] |
     select(.name != null) |
     select(.name | type == "string") |
     select(.name | startswith("Red Hat Konflux / ")) |
     select(.name | contains("enterprise-contract") | not) |
     select(.name | contains("-on-pull-request")) |
     .name |
     sub("Red Hat Konflux / "; "") |
     sub("-on-pull-request.*"; "")] |
    unique |
    sort
')

echo "$components" | jq '.'
