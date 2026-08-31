#!/bin/bash
# Test-FBC PR processing workflow.
# Handles "chore(deps): update osc-operator-bundle" PRs, which are deliberately
# excluded from the regular nudge flow and should only run when the Mintmaker
# and Nudge queues are fully drained.
#
# Usage: ./process-test-fbc.sh [--dry-run]
#
# With --dry-run: analyzes what would be done without performing labels or merges
#
# Output: JSON
#   {
#     "empty":    true|false,
#     "labelled": [...],
#     "merged":   [...],
#     "skipped":  [...]
#   }

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Parse options
DRY_RUN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 1
      ;;
  esac
done

repo_to_github() {
    case "$1" in
        osc)               echo "openshift/sandboxed-containers-operator" ;;
        cloud-api-adaptor) echo "openshift/cloud-api-adaptor" ;;
        compute-artifacts) echo "openshift/confidential-compute-artifacts" ;;
        kata-containers)   echo "openshift/kata-containers" ;;
        podvm-scripts)     echo "confidential-devhub/coco-podvm-scripts" ;;
        *)                 echo "" ;;
    esac
}

snapshot=$("$SCRIPT_DIR/snapshot-prs.sh" --test-fbc)

if [ "$(echo "$snapshot" | jq 'length')" -eq 0 ]; then
    echo '{"empty":true,"labelled":[],"merged":[],"skipped":[]}'
    exit 0
fi

labelled='[]'
merged='[]'
skipped='[]'

# Process each PR sorted by number (oldest first)
while IFS= read -r pr; do
    repo=$(echo "$pr"    | jq -r '.repo')
    num=$(echo "$pr"     | jq -r '.pr')
    branch=$(echo "$pr"  | jq -r '.base_branch')
    has_ok=$(echo "$pr"  | jq -r '.has_ok_to_test')
    has_lgtm=$(echo "$pr" | jq -r '.has_lgtm')
    build_ok=$(echo "$pr" | jq -r '.build_checks_passed')
    pending=$(echo "$pr"  | jq -r '.pending_checks')
    other_n=$(echo "$pr"  | jq -r '.other_checks_failed | length')
    comps_str=$(echo "$pr" | jq -r '.components | join(" ")')
    gh_repo=$(repo_to_github "$repo")

    safety=$("$SCRIPT_DIR/check-merge-safety.sh" \
        --components "$comps_str" \
        --repo "$gh_repo" --branch "$branch" \
        2>/dev/null) || \
        safety='{"safe_to_merge":false,"blocking":[{"reason":"safety check failed"}]}'
    safe=$(echo "$safety" | jq -r '.safe_to_merge')

    if [ "$safe" != "true" ]; then
        blocking=$(echo "$safety" | jq '.blocking')
        skipped=$(echo "$skipped" | jq --argjson p "$pr" --argjson b "$blocking" \
            '. + [$p + {"_skip_reason":"blocked by running on-push pipeline","_blocking":$b}]')
        continue
    fi

    if [ "$has_ok" = "false" ]; then
        if [ "$DRY_RUN" = false ]; then
            "$SCRIPT_DIR/label-pr.sh" --repo "$repo" --pr "$num" \
                --label ok-to-test >/dev/null 2>&1 || true
        fi
        labelled=$(echo "$labelled" | jq --argjson p "$pr" \
            '. + [$p + {"_label":"ok-to-test"}]')
        continue
    fi

    if [ "$pending" -gt 0 ] || [ "$build_ok" != "true" ] || [ "$other_n" -gt 0 ]; then
        if [ "$pending" -gt 0 ]; then
            reason="${pending} check(s) still pending"
        elif [ "$build_ok" != "true" ]; then
            failed=$(echo "$pr" | jq -r '.build_checks_failed | join(", ")')
            reason="build checks failed: ${failed}"
        else
            other_names=$(echo "$pr" | jq -r '.other_checks_failed | join(", ")')
            reason="developer verification needed — other checks failed: ${other_names}"
        fi
        skipped=$(echo "$skipped" | jq --argjson p "$pr" --arg r "$reason" \
            '. + [$p + {"_skip_reason":$r}]')
        continue
    fi

    if [ "$has_lgtm" = "false" ]; then
        if [ "$DRY_RUN" = false ]; then
            "$SCRIPT_DIR/label-pr.sh" --repo "$repo" --pr "$num" \
                --label lgtm >/dev/null 2>&1 || true
        fi
        labelled=$(echo "$labelled" | jq --argjson p "$pr" \
            '. + [$p + {"_label":"lgtm"}]')
        continue
    fi

    if [ "$DRY_RUN" = false ]; then
        result=$("$SCRIPT_DIR/merge-pr.sh" --repo "$repo" --pr "$num" \
            2>/dev/null) || result='{"success":false,"message":"merge command failed"}'
        if [ "$(echo "$result" | jq -r '.success')" = "true" ]; then
            merged=$(echo "$merged" | jq --argjson p "$pr" '. + [$p]')
        else
            msg=$(echo "$result" | jq -r '.message // "unknown error"')
            skipped=$(echo "$skipped" | jq --argjson p "$pr" --arg m "$msg" \
                '. + [$p + {"_skip_reason":"merge failed: \($m)"}]')
        fi
    else
        # In dry-run, assume merge would succeed
        merged=$(echo "$merged" | jq --argjson p "$pr" '. + [$p]')
    fi
done < <(echo "$snapshot" | jq -c 'sort_by(.pr) | .[]')

jq -n \
    --argjson labelled "$labelled" \
    --argjson merged   "$merged"   \
    --argjson skipped  "$skipped"  \
    '{"empty":false,"labelled":$labelled,"merged":$merged,"skipped":$skipped}'
