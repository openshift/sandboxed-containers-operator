#!/bin/bash
# Check whether it is safe to merge a PR by verifying:
#   1. No on-push pipeline is currently RUNNING for any of its components.
#   2. The last completed on-push pipeline for each component SUCCEEDED.
#      (Checked via GitHub check-runs on recent commits of the target branch.)
#
# Usage: ./check-merge-safety.sh --components "comp1 comp2 ..."
#                                [--repo GITHUB_REPO] [--branch BRANCH]
#                                [--namespace NAMESPACE]
#
# --repo and --branch enable check 2 via the GitHub API (e.g. --repo
# openshift/sandboxed-containers-operator --branch devel).  When omitted,
# only check 1 (running pipeline detection) is performed.
#
# Output: JSON {safe_to_merge: bool, blocking: [{component, pipeline, reason}]}
#   safe_to_merge: true only when all components pass both checks
#   blocking:      list of blocking conditions (empty when safe)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

COMPONENTS=()
NAMESPACE_ARG=""
GH_REPO=""
GH_BRANCH=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --components)
            [[ $# -ge 2 ]] || { echo "Usage: $0 --components 'comp1 comp2 ...' [--repo GITHUB_REPO] [--branch BRANCH] [--namespace NAMESPACE]" >&2; exit 1; }
            IFS=' ' read -ra COMPONENTS <<< "$2"
            shift 2
            ;;
        --namespace)
            [[ $# -ge 2 ]] || { echo "Usage: $0 --components 'comp1 comp2 ...' [--repo GITHUB_REPO] [--branch BRANCH] [--namespace NAMESPACE]" >&2; exit 1; }
            NAMESPACE_ARG="--namespace $2"
            shift 2
            ;;
        --repo)
            [[ $# -ge 2 ]] || { echo "Usage: $0 --components 'comp1 comp2 ...' [--repo GITHUB_REPO] [--branch BRANCH] [--namespace NAMESPACE]" >&2; exit 1; }
            GH_REPO="$2"
            shift 2
            ;;
        --branch)
            [[ $# -ge 2 ]] || { echo "Usage: $0 --components 'comp1 comp2 ...' [--repo GITHUB_REPO] [--branch BRANCH] [--namespace NAMESPACE]" >&2; exit 1; }
            GH_BRANCH="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --components 'comp1 comp2 ...' [--repo GITHUB_REPO] [--branch BRANCH] [--namespace NAMESPACE]" >&2
            exit 1
            ;;
    esac
done

if [[ ${#COMPONENTS[@]} -eq 0 ]]; then
    echo "Usage: $0 --components 'comp1 comp2 ...'" >&2
    exit 1
fi

if [[ ( -n "$GH_REPO" && -z "$GH_BRANCH" ) || ( -z "$GH_REPO" && -n "$GH_BRANCH" ) ]]; then
    echo "Error: --repo and --branch must be supplied together" >&2
    exit 1
fi

# Query GitHub for the most recent on-push check-run conclusion for a component.
# Walks back through the last 20 commits on the branch, stopping at the first
# commit that has the component's on-push check-run.
# Returns the conclusion string ("success", "failure", etc.) or "unknown".
gh_last_push_conclusion() {
    local gh_repo="$1"
    local branch="$2"
    local component="$3"
    local check_name="Red Hat Konflux / ${component}-on-push"

    local shas
    shas=$(gh api "repos/${gh_repo}/commits?sha=${branch}&per_page=20" \
        --jq '.[].sha' 2>/dev/null) || { echo "unknown"; return; }

    while IFS= read -r sha; do
        local conclusion
        conclusion=$(gh api "repos/${gh_repo}/commits/${sha}/check-runs" \
            --jq --arg n "$check_name" \
            '([.check_runs[] | select(.name == $n)] | .[0].conclusion) // ""' \
            2>/dev/null) || continue
        if [[ -n "$conclusion" ]]; then
            echo "$conclusion"
            return
        fi
    done <<< "$shas"

    echo "unknown"
}

blocking='[]'

for component in "${COMPONENTS[@]}"; do
    if ! result=$("$SCRIPT_DIR/check-pipeline-status.sh" --component "$component" $NAMESPACE_ARG 2>&1); then
        echo "Warning: failed to check pipeline status for $component: $result" >&2
        entry=$(jq -n --arg component "$component" \
            '{component: $component, pipeline: "unknown", reason: "status check failed"}')
        blocking=$(echo "$blocking" | jq --argjson e "$entry" '. + [$e]')
        continue
    fi

    latest=$(echo "$result" | jq '.latest')
    completion=$(echo "$result" | jq -r '.latest.completionTime // "null"')

    # Check 1: block if a pipeline is currently running (no completionTime on latest)
    if [[ "$latest" != "null" && "$completion" == "null" ]]; then
        reason=$(echo "$result" | jq -r '.latest.reason // "Running"')
        pipeline=$(echo "$result" | jq -r '.latest.name // "unknown"')
        entry=$(jq -n \
            --arg component "$component" \
            --arg pipeline "$pipeline" \
            --arg reason "$reason" \
            '{component: $component, pipeline: $pipeline, reason: $reason}')
        blocking=$(echo "$blocking" | jq --argjson e "$entry" '. + [$e]')
        # Don't also run check 2 — component is already blocked.
        continue
    fi

    # Check 2: block if the last on-push pipeline on the target branch failed.
    # Requires --repo and --branch; skipped when omitted.
    if [[ -n "$GH_REPO" && -n "$GH_BRANCH" ]]; then
        conclusion=$(gh_last_push_conclusion "$GH_REPO" "$GH_BRANCH" "$component")
        if [[ "$conclusion" != "success" && "$conclusion" != "unknown" ]]; then
            check_name="Red Hat Konflux / ${component}-on-push"
            entry=$(jq -n \
                --arg component "$component" \
                --arg pipeline "$check_name" \
                --arg reason "Last on-push pipeline did not succeed on branch ${GH_BRANCH} (GitHub check: ${check_name}, conclusion: ${conclusion})" \
                '{component: $component, pipeline: $pipeline, reason: $reason}')
            blocking=$(echo "$blocking" | jq --argjson e "$entry" '. + [$e]')
        fi
        # "unknown" → no on-push run found in last 20 commits → don't block
    fi
done

count=$(echo "$blocking" | jq 'length')
safe_to_merge=true
[[ "$count" -gt 0 ]] && safe_to_merge=false

jq -n \
    --argjson safe_to_merge "$safe_to_merge" \
    --argjson blocking "$blocking" \
    '{safe_to_merge: $safe_to_merge, blocking: $blocking}'
