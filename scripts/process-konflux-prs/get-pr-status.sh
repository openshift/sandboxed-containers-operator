#!/bin/bash
# Get PR status including checks, labels, and merge state
# Usage: ./get-pr-status.sh --repo REPO_NAME --pr PR_NUMBER
#
# Outputs JSON with:
#   number, title, state
#   labels: array of strings (e.g. ["ok-to-test", "lgtm"])
#   has_ok_to_test, has_lgtm: booleans
#   components: array of component names that will be rebuilt (from build pipeline checks)
#   build_checks_passed: true only when all build pipeline checks completed with SUCCESS
#                        (enterprise-contract checks with NEUTRAL conclusion are excluded)
#   build_checks_failed: array of names of build checks that failed (FAILURE conclusion)
#   pending_checks: integer count of checks still IN_PROGRESS, QUEUED, or PENDING
#   checks: raw array of all checks (name, status, conclusion, details_url); nulls filtered out

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
            [[ $# -ge 2 ]] || { echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER" >&2; exit 1; }
            if [[ -n "${REPOS[$2]:-}" ]]; then
                REPO_URL="${REPOS[$2]}"
            else
                echo "Unknown repository: $2" >&2
                exit 1
            fi
            shift 2
            ;;
        --pr)
            [[ $# -ge 2 ]] || { echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER" >&2; exit 1; }
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
    --json number,title,state,labels,statusCheckRollup,baseRefName)

# All checks, with null entries filtered out
checks=$(echo "$pr_data" | jq '[(.statusCheckRollup // [])[] |
    select(.name != null) |
    {
        name: .name,
        status: .status,
        conclusion: (.conclusion // ""),
        details_url: .detailsUrl
    }]')

# Labels: array of strings
labels=$(echo "$pr_data" | jq '[(.labels // [])[].name]')

# Build pipeline checks only: "Red Hat Konflux / {component}-on-pull-request[…]"
# Enterprise-contract checks (containing "enterprise-contract") are excluded —
# they return NEUTRAL when not required, which is not a failure.
build_checks=$(echo "$checks" | jq '[.[] |
    select(.name | startswith("Red Hat Konflux / ")) |
    select(.name | test("-on-pull-request")) |
    select(.name | contains("enterprise-contract") | not)]')

echo "$pr_data" | jq \
    --argjson checks "$checks" \
    --argjson labels "$labels" \
    --argjson build_checks "$build_checks" \
    '{
        number: .number,
        title: .title,
        state: .state,
        base_branch: .baseRefName,
        labels: $labels,
        has_ok_to_test: ($labels | any(. == "ok-to-test")),
        has_lgtm: ($labels | any(. == "lgtm")),
        components: ($build_checks | [.[].name |
            sub("Red Hat Konflux / "; "") |
            sub("-on-pull-request.*"; "")] | sort | unique),
        build_checks_passed: ($build_checks | length > 0 and
            (map(.conclusion == "SUCCESS") | all)),
        build_checks_failed: [$build_checks[] |
            select(.conclusion == "FAILURE") | .name],
        pending_checks: ([$checks[] |
            select(.status == "IN_PROGRESS" or
                   .status == "QUEUED" or
                   .status == "PENDING")] | length),
        checks: $checks
    }'
