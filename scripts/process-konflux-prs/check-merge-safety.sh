#!/bin/bash
# Check whether it is safe to merge a PR by verifying that no on-push pipeline
# is currently running for any of the components it would rebuild.
#
# Usage: ./check-merge-safety.sh --components "comp1 comp2 ..." [--namespace NAMESPACE]
#
# Output: JSON {safe_to_merge: bool, blocking: [{component, pipeline, reason}]}
#   safe_to_merge: true when no on-push pipeline is running for any component
#   blocking:      list of components with a running pipeline (empty when safe)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

COMPONENTS=()
NAMESPACE_ARG=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --components)
            IFS=' ' read -ra COMPONENTS <<< "$2"
            shift 2
            ;;
        --namespace)
            NAMESPACE_ARG="--namespace $2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --components 'comp1 comp2 ...' [--namespace NAMESPACE]" >&2
            exit 1
            ;;
    esac
done

if [[ ${#COMPONENTS[@]} -eq 0 ]]; then
    echo "Usage: $0 --components 'comp1 comp2 ...'" >&2
    exit 1
fi

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

    # A pipeline is running when latest is non-null and has no completionTime
    if [[ "$latest" != "null" && "$completion" == "null" ]]; then
        reason=$(echo "$result" | jq -r '.latest.reason // "Running"')
        pipeline=$(echo "$result" | jq -r '.latest.name // "unknown"')
        entry=$(jq -n \
            --arg component "$component" \
            --arg pipeline "$pipeline" \
            --arg reason "$reason" \
            '{component: $component, pipeline: $pipeline, reason: $reason}')
        blocking=$(echo "$blocking" | jq --argjson e "$entry" '. + [$e]')
    fi
done

count=$(echo "$blocking" | jq 'length')
safe_to_merge=true
[[ "$count" -gt 0 ]] && safe_to_merge=false

jq -n \
    --argjson safe_to_merge "$safe_to_merge" \
    --argjson blocking "$blocking" \
    '{safe_to_merge: $safe_to_merge, blocking: $blocking}'
