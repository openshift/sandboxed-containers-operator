#!/bin/bash
# Find nudge PRs for a given component
# Usage: ./get-nudge-prs.sh --component COMPONENT_NAME
#
# Outputs JSON array of nudge PRs that update the given component
# Note: Nudge PR titles mention the SOURCE component (what was rebuilt),
#       not the TARGET component (what is being updated)

set -euo pipefail

COMPONENT=""

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --component)
            COMPONENT="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --component COMPONENT_NAME" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$COMPONENT" ]]; then
    echo "Usage: $0 --component COMPONENT_NAME" >&2
    exit 1
fi

# Get all components and find which ones nudge the target component
components_info=$("$SCRIPT_DIR/get-component-info.sh")

# Find source components that nudge our target component
source_components=$(echo "$components_info" | jq -r --arg target "$COMPONENT" '
    [.[] | select(.nudges[] == $target) | .name] | .[]
')

# If no components nudge this one, return empty
if [[ -z "$source_components" ]]; then
    echo "[]" | jq '.'
    exit 0
fi

# Find which repository should have the nudge PR
# (The repository of the target component)
target_repo=$(echo "$components_info" | jq -r --arg target "$COMPONENT" '
    .[] | select(.name == $target) | .repository
')

if [[ -z "$target_repo" || "$target_repo" == "null" ]]; then
    echo "[]" | jq '.'
    exit 0
fi

# Map repo name to URL
declare -A REPOS=(
    ["kata-containers"]="https://github.com/openshift/kata-containers"
    ["cloud-api-adaptor"]="https://github.com/openshift/cloud-api-adaptor"
    ["compute-artifacts"]="https://github.com/openshift/confidential-compute-artifacts"
    ["podvm-scripts"]="https://github.com/confidential-devhub/coco-podvm-scripts"
    ["osc"]="https://github.com/openshift/sandboxed-containers-operator"
)

repo_url="${REPOS[$target_repo]}"

# Get all nudge PRs from the target repository
prs=$(gh pr list \
    --repo "$repo_url" \
    --state open \
    --author "app/red-hat-konflux" \
    --label "konflux-nudge" \
    --json number,title,url,updatedAt \
    --limit 50)

# Filter for PRs that mention any of the source components
# Nudge PR titles are like "chore(deps): update {source-component}"
all_matching_prs='[]'

while IFS= read -r source_component; do
    [[ -z "$source_component" ]] && continue

    matching=$(echo "$prs" | jq --arg component "$source_component" '
        [.[] |
         select(.title | contains($component))]
    ')

    all_matching_prs=$(echo "$all_matching_prs" "$matching" | jq -s 'add | unique_by(.number) | sort_by(.updatedAt) | reverse')
done <<< "$source_components"

echo "$all_matching_prs" | jq '.'
