#!/bin/bash
# Mark a PR as intentionally skipped: post an explanatory comment and add the
# 'mintmaker-skip' label. The label prevents re-posting the comment on subsequent runs.
#
# Usage: ./skip-pr.sh --repo REPO_NAME --pr PR_NUMBER --reason "reason text"
#
# Output: JSON {success, repo, pr, message}

set -euo pipefail

REPO_URL=""
PR_NUMBER=""
REASON=""

declare -A REPOS=(
    ["kata-containers"]="https://github.com/openshift/kata-containers"
    ["cloud-api-adaptor"]="https://github.com/openshift/cloud-api-adaptor"
    ["compute-artifacts"]="https://github.com/openshift/confidential-compute-artifacts"
    ["podvm-scripts"]="https://github.com/confidential-devhub/coco-podvm-scripts"
    ["osc"]="https://github.com/openshift/sandboxed-containers-operator"
)

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
        --reason)
            REASON="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER --reason 'reason'" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$REPO_URL" || -z "$PR_NUMBER" || -z "$REASON" ]]; then
    echo "Usage: $0 --repo REPO_NAME --pr PR_NUMBER --reason 'reason'" >&2
    exit 1
fi

REPO_NAME=$(basename "$REPO_URL")

# Ensure the label exists in the repository (idempotent)
gh label create mintmaker-skip \
    --repo "$REPO_URL" \
    --description "PR intentionally skipped by automated Mintmaker processing" \
    --color "e4e669" 2>/dev/null || true

gh pr edit "$PR_NUMBER" --repo "$REPO_URL" --add-label "mintmaker-skip" 2>/dev/null

COMMENT="**Automated Mintmaker processing**: this PR is being skipped intentionally.

${REASON}

This PR will not be labelled or merged by the automated process."

gh pr comment "$PR_NUMBER" --repo "$REPO_URL" --body "$COMMENT"

echo "{\"success\": true, \"repo\": \"$REPO_NAME\", \"pr\": $PR_NUMBER, \"message\": \"skipped\"}" | jq '.'
