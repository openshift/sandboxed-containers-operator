#!/bin/bash
# List Konflux PRs from repositories
# Usage: ./list-prs.sh [--mintmaker|--nudge] [--repo REPO_NAME]
#
# Outputs JSON array of PRs with: number, title, url, repo, type (mintmaker/nudge)

set -euo pipefail

PR_TYPE=""
REPO_FILTER=""

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
        --mintmaker)
            PR_TYPE="mintmaker"
            shift
            ;;
        --nudge)
            PR_TYPE="nudge"
            shift
            ;;
        --repo)
            REPO_FILTER="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 [--mintmaker|--nudge] [--repo REPO_NAME]" >&2
            exit 1
            ;;
    esac
done

# Mintmaker title prefixes
MINTMAKER_PREFIXES=(
    "chore(deps): update konflux references"
    "chore(deps): update registry.access.redhat.com/ubi9/ubi"
    "chore(deps): update registry.access.redhat.com/ubi9/go-toolset"
    "chore(deps): update registry.access.redhat.com/ubi10/ubi"
    "chore(deps): update registry.access.redhat.com/ubi10/go-toolset"
)

# Validate repo filter if provided
if [[ -n "$REPO_FILTER" ]]; then
    if [[ -z "${REPOS[$REPO_FILTER]:-}" ]]; then
        echo "Error: Unknown repository '$REPO_FILTER'" >&2
        echo "Valid repositories: ${!REPOS[*]}" >&2
        exit 1
    fi
fi

is_mintmaker_pr() {
    local title="$1"
    for prefix in "${MINTMAKER_PREFIXES[@]}"; do
        if [[ "$title" == "$prefix"* ]]; then
            return 0
        fi
    done
    return 1
}

# Process each repository
all_prs='[]'

for repo_name in "${!REPOS[@]}"; do
    # Skip if repo filter is set and doesn't match
    if [[ -n "$REPO_FILTER" && "$REPO_FILTER" != "$repo_name" ]]; then
        continue
    fi

    repo_url="${REPOS[$repo_name]}"

    # Get open PRs from red-hat-konflux bot
    prs=$(gh pr list \
        --repo "$repo_url" \
        --state open \
        --author "app/red-hat-konflux" \
        --json number,title,url,labels \
        --limit 100)

    # Filter and classify PRs
    while IFS= read -r pr; do
        number=$(echo "$pr" | jq -r '.number')
        title=$(echo "$pr" | jq -r '.title')
        url=$(echo "$pr" | jq -r '.url')
        labels=$(echo "$pr" | jq -r '.labels[].name')

        # Skip empty results
        [[ "$number" == "null" ]] && continue

        # Determine PR type
        pr_type=""

        # Check if it's a Nudge PR
        if echo "$labels" | grep -q "konflux-nudge"; then
            # Skip bundle update PRs
            if [[ "$title" != "chore(deps): update osc-operator-bundle"* ]]; then
                pr_type="nudge"
            fi
        # Check if it's a Mintmaker PR
        elif is_mintmaker_pr "$title"; then
            pr_type="mintmaker"
        fi

        # Skip if type doesn't match filter
        if [[ -n "$PR_TYPE" && "$pr_type" != "$PR_TYPE" ]]; then
            continue
        fi

        # Skip if no type identified
        [[ -z "$pr_type" ]] && continue

        # Add to results
        pr_json=$(jq -n \
            --argjson number "$number" \
            --arg title "$title" \
            --arg url "$url" \
            --arg repo "$repo_name" \
            --arg type "$pr_type" \
            '{number: $number, title: $title, url: $url, repo: $repo, type: $type}')

        all_prs=$(echo "$all_prs" | jq --argjson pr "$pr_json" '. + [$pr]')
    done < <(echo "$prs" | jq -c '.[]')
done

echo "$all_prs" | jq '.'
