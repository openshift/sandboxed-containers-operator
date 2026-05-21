#!/bin/bash
# Add a label to a PR
# Usage: ./label-pr.sh --repo REPO_NAME --pr PR_NUMBER --label LABEL_NAME
#
# Returns: success/failure status

set -euo pipefail

REPO_URL=""
PR_NUMBER=""
LABEL=""

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
        --label)
            LABEL="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER --label LABEL_NAME" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$REPO_URL" || -z "$PR_NUMBER" || -z "$LABEL" ]]; then
    echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER --label LABEL_NAME" >&2
    exit 1
fi

# Validate label
if [[ "$LABEL" != "ok-to-test" && "$LABEL" != "lgtm" ]]; then
    echo "Error: Label must be 'ok-to-test' or 'lgtm'" >&2
    exit 1
fi

# Add label
if gh pr edit "$PR_NUMBER" \
    --repo "$REPO_URL" \
    --add-label "$LABEL"; then
    echo "{\"success\": true, \"repo\": \"$(basename "$REPO_URL")\", \"pr\": $PR_NUMBER, \"label\": \"$LABEL\"}" | jq '.'
else
    echo "{\"success\": false, \"repo\": \"$(basename "$REPO_URL")\", \"pr\": $PR_NUMBER, \"label\": \"$LABEL\"}" | jq '.'
    exit 1
fi
