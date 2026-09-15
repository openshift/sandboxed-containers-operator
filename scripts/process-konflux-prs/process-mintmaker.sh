#!/bin/bash
# Full Mintmaker PR processing workflow: skip filter, labelling, merge.
# Replaces the manual Phase 0 / Phase A / Phase B loop that Claude used to run.
#
# Usage: ./process-mintmaker.sh [--dry-run]
#
# With --dry-run: analyzes what would be done without performing labels or merges
#
# Output: JSON
#   {
#     "empty":    true|false,          -- true when no open Mintmaker PRs found
#     "skipped":  [...],               -- PRs matching a skip pattern (_skip_reason field)
#     "labelled": [...],               -- PRs that received a new label (_label field)
#     "merged":   [...],               -- PRs successfully merged this run
#     "waiting":  [...]                -- PRs that could not be advanced (_wait_reason, _blocking)
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

# Returns 0 if the PR title matches a known skip pattern.
is_skip_pattern() {
    local title="$1"
    # go-toolset bare major version (e.g. "v9") — no dots in the version number.
    if echo "$title" | grep -qE 'ubi[0-9]+/go-toolset' && \
       echo "$title" | grep -qE '[Tt]ag to v[0-9]+$'; then
        return 0
    fi
    return 1
}

# ─── snapshot ────────────────────────────────────────────────────────────────

snapshot=$("$SCRIPT_DIR/snapshot-prs.sh" --mintmaker)

if [ "$(echo "$snapshot" | jq 'length')" -eq 0 ]; then
    echo '{"empty":true,"skipped":[],"labelled":[],"merged":[],"waiting":[],"held":[]}'
    exit 0
fi

# ─── phase 0: skip filter ────────────────────────────────────────────────────

skipped='[]'
working='[]'

while IFS= read -r pr; do
    title=$(echo "$pr" | jq -r '.title')
    if is_skip_pattern "$title"; then
        repo=$(echo "$pr" | jq -r '.repo')
        num=$(echo "$pr"  | jq -r '.pr')
        if [ "$(echo "$pr" | jq -r '.has_mintmaker_skip')" = "false" ]; then
            "$SCRIPT_DIR/skip-pr.sh" --repo "$repo" --pr "$num" \
                --reason "go-toolset bare major version tag (e.g. v9) — we pin to full Go version tags like v1.26.x; a correct PR will arrive separately" \
                >/dev/null 2>&1 || true
        fi
        skipped=$(echo "$skipped" | jq --argjson p "$pr" \
            '. + [$p + {"_skip_reason":"go-toolset bare major version tag"}]')
    else
        working=$(echo "$working" | jq --argjson p "$pr" '. + [$p]')
    fi
done < <(echo "$snapshot" | jq -c '.[]')

# ─── hold filter ─────────────────────────────────────────────────────────────

held='[]'
non_held='[]'
while IFS= read -r pr; do
    if [ "$(echo "$pr" | jq -r '.has_hold')" = "true" ]; then
        held=$(echo "$held" | jq --argjson p "$pr" '. + [$p]')
    else
        non_held=$(echo "$non_held" | jq --argjson p "$pr" '. + [$p]')
    fi
done < <(echo "$working" | jq -c '.[]')
working="$non_held"

if [ "$(echo "$working" | jq 'length')" -eq 0 ]; then
    jq -n --argjson sk "$skipped" --argjson h "$held" \
        '{"empty":true,"skipped":$sk,"labelled":[],"merged":[],"waiting":[],"held":$h}'
    exit 0
fi

# ─── phase a: labelling (parallel) ───────────────────────────────────────────

labelled='[]'
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

while IFS= read -r pr; do
    repo=$(echo "$pr" | jq -r '.repo')
    num=$(echo "$pr"  | jq -r '.pr')
    has_ok=$(echo "$pr"      | jq -r '.has_ok_to_test')
    has_lgtm=$(echo "$pr"    | jq -r '.has_lgtm')
    build_ok=$(echo "$pr"    | jq -r '.build_checks_passed')
    pending=$(echo "$pr"     | jq -r '.pending_checks')
    other_n=$(echo "$pr"     | jq -r '.other_checks_failed | length')

    if [ "$has_ok" = "false" ]; then
        label="ok-to-test"
    elif [ "$has_lgtm" = "false" ] && [ "$build_ok" = "true" ] && \
         [ "$pending" -eq 0 ] && [ "$other_n" -eq 0 ]; then
        label="lgtm"
    else
        continue
    fi

    (
        success=false
        if [ "$DRY_RUN" = true ]; then
            success=true
        elif "$SCRIPT_DIR/label-pr.sh" --repo "$repo" --pr "$num" \
               --label "$label" >/dev/null 2>&1; then
            success=true
        fi

        if [ "$success" = true ]; then
            echo "$pr" | jq --arg l "$label" '. + {"_label":$l}' \
                > "$tmpdir/label-${repo}-${num}.json"
        fi
    ) &
done < <(echo "$working" | jq -c '.[]')

wait 2>/dev/null || true

for f in "$tmpdir"/label-*.json; do
    [ -f "$f" ] || continue
    labelled=$(echo "$labelled" | jq --argjson p "$(cat "$f")" '. + [$p]')
done

# ─── phase b: merging ────────────────────────────────────────────────────────

merged='[]'
waiting='[]'

# Eligible = lgtm was already set before this run started
eligible=$(echo "$working" | jq '[.[] | select(
    .has_lgtm == true and
    .build_checks_passed == true and
    .pending_checks == 0 and
    (.other_checks_failed | length) == 0
)]')

# Run safety checks in parallel
while IFS= read -r pr; do
    repo=$(echo "$pr"   | jq -r '.repo')
    num=$(echo "$pr"    | jq -r '.pr')
    comps=$(echo "$pr"  | jq -r '.components | join(" ")')
    branch=$(echo "$pr" | jq -r '.base_branch')
    gh_repo=$(repo_to_github "$repo")
    (
        result=$("$SCRIPT_DIR/check-merge-safety.sh" \
            --components "$comps" --repo "$gh_repo" --branch "$branch" \
            2>/dev/null) || \
            result='{"safe_to_merge":false,"blocking":[{"reason":"safety check failed"}]}'
        echo "$pr" | jq --argjson s "$result" '. + {"_safety":$s}' \
            > "$tmpdir/safety-${repo}-${num}.json"
    ) &
done < <(echo "$eligible" | jq -c '.[]')

wait 2>/dev/null || true

# Merge sequentially; track rebuilt components as a space-separated string
rebuilt=""

while IFS= read -r pr; do
    repo=$(echo "$pr" | jq -r '.repo')
    num=$(echo "$pr"  | jq -r '.pr')
    safety_file="$tmpdir/safety-${repo}-${num}.json"

    if [ ! -f "$safety_file" ]; then
        waiting=$(echo "$waiting" | jq --argjson p "$pr" \
            '. + [$p + {"_wait_reason":"safety check unavailable"}]')
        continue
    fi

    pr_full=$(cat "$safety_file")
    safe=$(echo "$pr_full"     | jq -r '._safety.safe_to_merge')
    blocking=$(echo "$pr_full" | jq '._safety.blocking')
    comps=$(echo "$pr_full"    | jq -r '.components | join(" ")')

    # Component conflict: skip if any component already rebuilt this run
    conflict=false
    for comp in $comps; do
        case " $rebuilt " in
            *" $comp "*) conflict=true; break ;;
        esac
    done

    if [ "$conflict" = "true" ]; then
        waiting=$(echo "$waiting" | jq --argjson p "$pr_full" \
            '. + [$p + {"_wait_reason":"component already being rebuilt this run"}]')
        continue
    fi

    if [ "$safe" != "true" ]; then
        waiting=$(echo "$waiting" | jq --argjson p "$pr_full" --argjson b "$blocking" \
            '. + [$p + {"_wait_reason":"blocked by running on-push pipeline","_blocking":$b}]')
        continue
    fi

    merge_success=false
    merge_msg=""
    if [ "$DRY_RUN" = true ]; then
        merge_success=true
    else
        result=$("$SCRIPT_DIR/merge-pr.sh" --repo "$repo" --pr "$num" 2>/dev/null) || \
            result='{"success":false,"message":"merge command failed"}'
        if [ "$(echo "$result" | jq -r '.success')" = "true" ]; then
            merge_success=true
        else
            merge_msg=$(echo "$result" | jq -r '.message // "unknown error"')
        fi
    fi

    if [ "$merge_success" = true ]; then
        merged=$(echo "$merged" | jq --argjson p "$pr" '. + [$p]')
        rebuilt="$rebuilt $comps"
    else
        waiting=$(echo "$waiting" | jq --argjson p "$pr" --arg m "$merge_msg" \
            '. + [$p + {"_wait_reason":"merge failed: \($m)"}]')
    fi
done < <(echo "$eligible" | jq -c '.[]')

# Remaining PRs: not labelled this run, not eligible → waiting
labelled_keys=$(echo "$labelled" | jq -r '.[] | "\(.repo)-\(.pr)"')
eligible_keys=$(echo "$eligible" | jq -r '.[] | "\(.repo)-\(.pr)"')

while IFS= read -r pr; do
    repo=$(echo "$pr" | jq -r '.repo')
    num=$(echo "$pr"  | jq -r '.pr')
    key="${repo}-${num}"

    echo "$labelled_keys" | grep -qx "$key" && continue
    echo "$eligible_keys"  | grep -qx "$key" && continue

    pending=$(echo "$pr"       | jq -r '.pending_checks')
    build_ok=$(echo "$pr"      | jq -r '.build_checks_passed')
    build_names=$(echo "$pr"   | jq -r '.build_checks_failed | join(", ")')
    other_n=$(echo "$pr"       | jq -r '.other_checks_failed | length')
    other_names=$(echo "$pr"   | jq -r '.other_checks_failed | join(", ")')

    if [ "$pending" -gt 0 ]; then
        reason="${pending} check(s) still pending"
    elif [ "$build_ok" = "false" ]; then
        reason="build checks failed: ${build_names}"
    elif [ "$other_n" -gt 0 ]; then
        reason="developer verification needed — other checks failed: ${other_names}"
    else
        reason="not yet eligible"
    fi

    waiting=$(echo "$waiting" | jq --argjson p "$pr" --arg r "$reason" \
        '. + [$p + {"_wait_reason":$r}]')
done < <(echo "$working" | jq -c '.[]')

# ─── output ──────────────────────────────────────────────────────────────────

jq -n \
    --argjson skipped  "$skipped"  \
    --argjson labelled "$labelled" \
    --argjson merged   "$merged"   \
    --argjson waiting  "$waiting"  \
    --argjson held     "$held"     \
    '{"empty":false,"skipped":$skipped,"labelled":$labelled,"merged":$merged,"waiting":$waiting,"held":$held}'
