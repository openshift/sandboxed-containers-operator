#!/bin/bash
# Check the status of Konflux pipelines for a component
# Usage: ./check-pipeline-status.sh --component COMPONENT_NAME [--type on-pull-request|on-push] [--pr-id PR_ID]
#
# Outputs JSON with pipeline status

set -euo pipefail

COMPONENT=""
PIPELINE_TYPE="on-push"
PR_ID=""
NAMESPACE="ose-osc-tenant"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --component)
            COMPONENT="$2"
            shift 2
            ;;
        --type)
            PIPELINE_TYPE="$2"
            shift 2
            ;;
        --pr-id)
            PR_ID="$2"
            shift 2
            ;;
        --namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --component COMPONENT_NAME [--type on-pull-request|on-push] [--pr-id PR_ID] [--namespace NAMESPACE]" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$COMPONENT" ]]; then
    echo "Usage: $0 --component COMPONENT_NAME [--type on-pull-request|on-push] [--pr-id PR_ID] [--namespace NAMESPACE]" >&2
    exit 1
fi

# Construct pipeline name pattern
if [[ "$PIPELINE_TYPE" == "on-pull-request" && -n "$PR_ID" ]]; then
    PATTERN="${COMPONENT}-on-pull-request-${PR_ID}"
else
    PATTERN="${COMPONENT}-${PIPELINE_TYPE}"
fi

# List pipeline runs
pipelineruns=$(tkn pipelinerun list \
    --namespace "$NAMESPACE" \
    --output json 2>/dev/null || echo '{"items":[]}')

# Filter for matching pipeline runs
matching_runs=$(echo "$pipelineruns" | jq --arg pattern "$PATTERN" '
    [.items[] |
     select(.metadata.generateName != null) |
     select(.metadata.generateName | type == "string") |
     select(.metadata.generateName | startswith($pattern)) |
     {
        name: .metadata.name,
        status: (.status.conditions[0].status // "Unknown"),
        reason: (.status.conditions[0].reason // "Unknown"),
        startTime: .status.startTime,
        completionTime: .status.completionTime
     }] |
    sort_by(.startTime) |
    reverse
')

# Get most recent run
latest_run=$(echo "$matching_runs" | jq '.[0] // null')

result=$(jq -n \
    --arg component "$COMPONENT" \
    --arg type "$PIPELINE_TYPE" \
    --argjson latest "$latest_run" \
    --argjson all "$matching_runs" \
    '{
        component: $component,
        pipeline_type: $type,
        latest: $latest,
        all_runs: $all
    }')

echo "$result" | jq '.'
