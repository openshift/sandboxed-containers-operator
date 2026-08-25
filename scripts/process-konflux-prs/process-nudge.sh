#!/bin/bash
# Full Nudge PR processing workflow: Mintmaker-block filter, hold-back grouping,
# labelling, and merging.
# Replaces the manual Phase A / Phase B / Phase C loop that Claude used to run.
#
# Usage: ./process-nudge.sh
#
# Output: JSON
#   {
#     "labelled":  [...],  -- PRs that received a new label (_label field)
#     "merged":    [...],  -- PRs successfully merged this run
#     "held_back": [...],  -- PRs intentionally held (lowest-numbered in group) (_held_reason)
#     "skipped":   [...]   -- PRs blocked for any reason (_skip_reason, optional _blocking)
#   }

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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

empty_result='{"labelled":[],"merged":[],"held_back":[],"skipped":[]}'

# ─── phase a: mintmaker-blocked component set ─────────────────────────────────

mm_snapshot=$("$SCRIPT_DIR/snapshot-prs.sh" --mintmaker)
# Union of all components that open Mintmaker PRs will rebuild
mintmaker_components=$(echo "$mm_snapshot" | jq -r '[.[].components[]] | unique | .[]')

# ─── nudge snapshot ───────────────────────────────────────────────────────────

nudge_snapshot=$("$SCRIPT_DIR/snapshot-prs.sh" --nudge)

if [ "$(echo "$nudge_snapshot" | jq 'length')" -eq 0 ]; then
    echo "$empty_result"
    exit 0
fi

# Component names sorted by length descending to prefer more specific matches
# (e.g. "osc-caa-webhook" before "osc-caa") when searching PR titles.
all_components=$("$SCRIPT_DIR/get-component-info.sh" | \
    jq -r '[.[].name] | sort_by(-length) | .[]')

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

labelled='[]'
merged='[]'
held_back='[]'
skipped='[]'
working='[]'

# ─── filter mintmaker-blocked PRs ─────────────────────────────────────────────

while IFS= read -r pr; do
    title=$(echo "$pr" | jq -r '.title')

    # Identify source component from PR title (longest match first)
    source_comp=""
    while IFS= read -r comp; do
        [ -z "$comp" ] && continue
        if echo "$title" | grep -qiF "$comp"; then
            source_comp="$comp"
            break
        fi
    done <<< "$all_components"

    if [ -n "$source_comp" ] && \
       echo "$mintmaker_components" | grep -qx "$source_comp"; then
        skipped=$(echo "$skipped" | jq --argjson p "$pr" \
            --arg r "source component '$source_comp' is being rebuilt by an open Mintmaker PR" \
            '. + [$p + {"_skip_reason":$r}]')
        continue
    fi

    working=$(echo "$working" | jq --argjson p "$pr" '. + [$p]')
done < <(echo "$nudge_snapshot" | jq -c '.[]')

if [ "$(echo "$working" | jq 'length')" -eq 0 ]; then
    echo "$empty_result" | jq --argjson sk "$skipped" '. + {"skipped":$sk}'
    exit 0
fi

# ─── group by target components, process each group ──────────────────────────

group_keys=$(echo "$working" | \
    jq -r '[.[].components | sort | join(",")] | unique | .[]')

while IFS= read -r group_key; do
    [ -z "$group_key" ] && continue

    # PRs in this group, sorted by PR number (ascending); lowest = held-back
    group=$(echo "$working" | jq --arg k "$group_key" \
        '[.[] | select((.components | sort | join(",")) == $k)] | sort_by(.pr)')
    count=$(echo "$group" | jq 'length')

    hb_pr=$(echo "$group"     | jq '.[0]')
    hb_repo=$(echo "$hb_pr"   | jq -r '.repo')
    hb_num=$(echo "$hb_pr"    | jq -r '.pr')
    hb_branch=$(echo "$hb_pr" | jq -r '.base_branch')
    comps_str=$(echo "$hb_pr" | jq -r '.components | join(" ")')
    gh_repo=$(repo_to_github "$hb_repo")

    if [ "$count" -eq 1 ]; then
        # ── single-PR group (solo or held-back whose peers have all merged) ──

        pr="$hb_pr"
        has_ok=$(echo "$pr"    | jq -r '.has_ok_to_test')
        has_lgtm=$(echo "$pr"  | jq -r '.has_lgtm')
        build_ok=$(echo "$pr"  | jq -r '.build_checks_passed')
        pending=$(echo "$pr"   | jq -r '.pending_checks')
        other_n=$(echo "$pr"   | jq -r '.other_checks_failed | length')
        is_osc_devel=false
        [ "$hb_repo" = "osc" ] && [ "$hb_branch" = "devel" ] && is_osc_devel=true

        # Merge safety must pass before we label or merge a solo PR
        safety=$("$SCRIPT_DIR/check-merge-safety.sh" \
            --components "$comps_str" \
            --repo "$gh_repo" --branch "$hb_branch" \
            2>/dev/null) || \
            safety='{"safe_to_merge":false,"blocking":[{"reason":"safety check failed"}]}'
        safe=$(echo "$safety" | jq -r '.safe_to_merge')

        if [ "$safe" != "true" ]; then
            blocking=$(echo "$safety" | jq '.blocking')
            skipped=$(echo "$skipped" | jq --argjson p "$pr" --argjson b "$blocking" \
                '. + [$p + {"_skip_reason":"waiting for on-push pipelines to complete","_blocking":$b}]')
            continue
        fi

        if [ "$has_ok" = "false" ]; then
            "$SCRIPT_DIR/label-pr.sh" --repo "$hb_repo" --pr "$hb_num" \
                --label ok-to-test >/dev/null 2>&1 || true
            labelled=$(echo "$labelled" | jq --argjson p "$pr" \
                '. + [$p + {"_label":"ok-to-test"}]')
            continue
        fi

        # ok-to-test is already set; check if checks are passing
        if [ "$pending" -gt 0 ] || [ "$build_ok" != "true" ] || [ "$other_n" -gt 0 ]; then
            pending_str=$(echo "$pr" | jq -r '.pending_checks')
            build_failed=$(echo "$pr" | jq -r '.build_checks_failed | join(", ")')
            other_names=$(echo "$pr" | jq -r '.other_checks_failed | join(", ")')
            if [ "$pending_str" -gt 0 ]; then
                reason="${pending_str} check(s) still pending"
            elif [ "$build_ok" != "true" ]; then
                reason="build checks failed: ${build_failed}"
            else
                reason="developer verification needed — other checks failed: ${other_names}"
            fi
            skipped=$(echo "$skipped" | jq --argjson p "$pr" --arg r "$reason" \
                '. + [$p + {"_skip_reason":$r}]')
            continue
        fi

        if [ "$is_osc_devel" = "true" ]; then
            # osc/devel PRs auto-merge once tests pass; ok-to-test is sufficient
            : # nothing more to do
        elif [ "$has_lgtm" = "false" ]; then
            "$SCRIPT_DIR/label-pr.sh" --repo "$hb_repo" --pr "$hb_num" \
                --label lgtm >/dev/null 2>&1 || true
            labelled=$(echo "$labelled" | jq --argjson p "$pr" \
                '. + [$p + {"_label":"lgtm"}]')
        else
            result=$("$SCRIPT_DIR/merge-pr.sh" --repo "$hb_repo" --pr "$hb_num" \
                2>/dev/null) || result='{"success":false,"message":"merge command failed"}'
            if [ "$(echo "$result" | jq -r '.success')" = "true" ]; then
                merged=$(echo "$merged" | jq --argjson p "$pr" '. + [$p]')
            else
                msg=$(echo "$result" | jq -r '.message // "unknown error"')
                skipped=$(echo "$skipped" | jq --argjson p "$pr" --arg m "$msg" \
                    '. + [$p + {"_skip_reason":"merge failed: \($m)"}]')
            fi
        fi

    else
        # ── multi-PR group ────────────────────────────────────────────────────

        # Label ok-to-test on all non-held-back PRs that need it.
        # The held-back skip inside the loop prevents labelling the lowest-numbered PR.
        while IFS= read -r pr; do
            repo=$(echo "$pr" | jq -r '.repo')
            num=$(echo "$pr"  | jq -r '.pr')
            [ "$repo" = "$hb_repo" ] && [ "$num" = "$hb_num" ] && continue
            if [ "$(echo "$pr" | jq -r '.has_ok_to_test')" = "false" ]; then
                "$SCRIPT_DIR/label-pr.sh" --repo "$repo" --pr "$num" \
                    --label ok-to-test >/dev/null 2>&1 || true
                labelled=$(echo "$labelled" | jq --argjson p "$pr" \
                    '. + [$p + {"_label":"ok-to-test"}]')
            fi
        done < <(echo "$group" | jq -c '.[]')

        # Safety check once for the whole group (uses held-back PR's branch/repo)
        safety=$("$SCRIPT_DIR/check-merge-safety.sh" \
            --components "$comps_str" \
            --repo "$gh_repo" --branch "$hb_branch" \
            2>/dev/null) || \
            safety='{"safe_to_merge":false,"blocking":[{"reason":"safety check failed"}]}'
        safe=$(echo "$safety"    | jq -r '.safe_to_merge')
        blocking=$(echo "$safety" | jq '.blocking')

        # Process each PR in the group
        while IFS= read -r pr; do
            repo=$(echo "$pr"    | jq -r '.repo')
            num=$(echo "$pr"     | jq -r '.pr')
            branch=$(echo "$pr"  | jq -r '.base_branch')
            has_ok=$(echo "$pr"  | jq -r '.has_ok_to_test')
            has_lgtm=$(echo "$pr" | jq -r '.has_lgtm')
            build_ok=$(echo "$pr" | jq -r '.build_checks_passed')
            pending=$(echo "$pr"  | jq -r '.pending_checks')
            other_n=$(echo "$pr"  | jq -r '.other_checks_failed | length')
            is_osc_devel=false
            [ "$repo" = "osc" ] && [ "$branch" = "devel" ] && is_osc_devel=true

            # Held-back PR: register and skip
            if [ "$repo" = "$hb_repo" ] && [ "$num" = "$hb_num" ]; then
                held_back=$(echo "$held_back" | jq --argjson p "$pr" \
                    '. + [$p + {"_held_reason":"waiting for peer PRs to merge first"}]')
                continue
            fi

            # Skip PRs that aren't ready (labelling already handled above)
            if [ "$has_ok" != "true" ] || [ "$build_ok" != "true" ] || \
               [ "$pending" -gt 0 ] || [ "$other_n" -gt 0 ]; then
                continue
            fi

            # osc/devel: auto-merge, only ok-to-test needed
            [ "$is_osc_devel" = "true" ] && continue

            if [ "$safe" != "true" ]; then
                skipped=$(echo "$skipped" | jq --argjson p "$pr" --argjson b "$blocking" \
                    '. + [$p + {"_skip_reason":"blocked by running on-push pipeline","_blocking":$b}]')
                continue
            fi

            # Apply lgtm + merge in same run (intentional for multi-PR groups)
            if [ "$has_lgtm" = "false" ]; then
                "$SCRIPT_DIR/label-pr.sh" --repo "$repo" --pr "$num" \
                    --label lgtm >/dev/null 2>&1 || true
                labelled=$(echo "$labelled" | jq --argjson p "$pr" \
                    '. + [$p + {"_label":"lgtm"}]')
            fi
            result=$("$SCRIPT_DIR/merge-pr.sh" --repo "$repo" --pr "$num" \
                2>/dev/null) || result='{"success":false,"message":"merge command failed"}'
            if [ "$(echo "$result" | jq -r '.success')" = "true" ]; then
                merged=$(echo "$merged" | jq --argjson p "$pr" '. + [$p]')
            else
                msg=$(echo "$result" | jq -r '.message // "unknown error"')
                skipped=$(echo "$skipped" | jq --argjson p "$pr" --arg m "$msg" \
                    '. + [$p + {"_skip_reason":"merge failed: \($m)"}]')
            fi
        done < <(echo "$group" | jq -c '.[]')
    fi
done <<< "$group_keys"

# ─── output ──────────────────────────────────────────────────────────────────

jq -n \
    --argjson labelled  "$labelled"  \
    --argjson merged    "$merged"    \
    --argjson held_back "$held_back" \
    --argjson skipped   "$skipped"   \
    '{"labelled":$labelled,"merged":$merged,"held_back":$held_back,"skipped":$skipped}'
