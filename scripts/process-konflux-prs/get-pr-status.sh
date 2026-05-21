#!/bin/bash
# Get PR status including checks, labels, and merge state
# Usage: ./get-pr-status.sh --repo REPO_NAME --pr PR_NUMBER
#
# Outputs JSON with: number, title, labels, checks (name, status, conclusion, details_url)

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

# Get PR details
pr_data=$(gh pr view "$PR_NUMBER" \
    --repo "$REPO_URL" \
    --json number,title,state,labels,statusCheckRollup)

# Extract status checks
# Note: statusCheckRollup can be null for fresh PRs without checks
checks=$(echo "$pr_data" | jq '[(.statusCheckRollup // [])[] | {
    name: .name,
    status: .status,
    conclusion: .conclusion,
    details_url: .detailsUrl
}]')

# Extract labels
# Note: labels can be null for PRs without labels
labels=$(echo "$pr_data" | jq '[(.labels // [])[].name]')

# Build result
result=$(echo "$pr_data" | jq \
    --argjson checks "$checks" \
    --argjson labels "$labels" \
    '{
        number: .number,
        title: .title,
        state: .state,
        labels: $labels,
        checks: $checks,
        has_ok_to_test: ($labels | any(. == "ok-to-test")),
        has_lgtm: ($labels | any(. == "lgtm")),
        all_checks_passed: ([$checks[] | select(.conclusion != "SUCCESS")] | length == 0),
        pending_checks: ([$checks[] | select(.status == "IN_PROGRESS" or .status == "PENDING")] | length)
    }')

echo "$result" | jq '.'
