#!/bin/bash
#
# Orchestrate the Konflux PR processing workflow.
#
# This script runs a multi-stage pipeline to process Konflux-generated PRs:
#
# 1. Run process-mintmaker.sh to handle dependency update PRs (UBI images, Konflux
#    references, etc.). These are typically safe and can be labelled/merged quickly.
#
# 2. Run process-nudge.sh to handle component nudge PRs. The nudge script itself
#    skips PRs whose source component is still being rebuilt by an open Mintmaker PR,
#    so both stages run every pass and nudge applies its own per-component blocking.
#
# 3. If the nudge queue is fully drained (all arrays empty) AND no nudges were merged
#    this run, run process-test-fbc.sh to process osc-operator-bundle test PRs.
#
# The script captures JSON output from each stage and formats it into a markdown report
# that is saved as Konflux-PR-report-YYYYMMDD-HHMMSS.md
#
# If SLACK_WEBHOOK_URL is set, a Slack-formatted version of the report is posted to
# that webhook after the markdown report is generated.
#
# Options:
#   --dry-run    Snapshot all PRs without processing (no labels or merges)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Parse command-line arguments
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

# Temporary files to hold JSON results
MINTMAKER_RESULT=$(mktemp)
NUDGE_RESULT=$(mktemp)
TESTFBC_RESULT=$(mktemp)

trap "rm -f $MINTMAKER_RESULT $NUDGE_RESULT $TESTFBC_RESULT" EXIT

# Repository URL mapping
declare -A REPO_URLS=(
  ["kata-containers"]="https://github.com/openshift/kata-containers"
  ["cloud-api-adaptor"]="https://github.com/openshift/cloud-api-adaptor"
  ["compute-artifacts"]="https://github.com/openshift/confidential-compute-artifacts"
  ["podvm-scripts"]="https://github.com/confidential-devhub/coco-podvm-scripts"
  ["osc"]="https://github.com/openshift/sandboxed-containers-operator"
)

# Build script arguments
SCRIPT_ARGS=""
if [ "$DRY_RUN" = true ]; then
  SCRIPT_ARGS="--dry-run"
fi

# Step 1: Run mintmaker
echo "Running mintmaker..." >&2
"$SCRIPT_DIR/process-mintmaker.sh" $SCRIPT_ARGS > "$MINTMAKER_RESULT"

# Step 2: Run nudge
echo "Running nudge..." >&2
"$SCRIPT_DIR/process-nudge.sh" $SCRIPT_ARGS > "$NUDGE_RESULT"

# Check if nudge queues are fully drained (held PRs excluded — they don't block)
NUDGE_LABELLED=$(jq '.labelled | length' "$NUDGE_RESULT")
NUDGE_MERGED=$(jq '.merged | length' "$NUDGE_RESULT")
NUDGE_HELD_BACK=$(jq '.held_back | length' "$NUDGE_RESULT")
NUDGE_SKIPPED=$(jq '.skipped | length' "$NUDGE_RESULT")

NUDGE_TOTAL=$((NUDGE_LABELLED + NUDGE_MERGED + NUDGE_HELD_BACK + NUDGE_SKIPPED))

# Step 3: If nudge queue is empty, run test-fbc
# In normal mode: only run if nothing was merged (to avoid pending on-push pipelines)
# In dry-run: always run if queue would be empty
if [ $NUDGE_TOTAL -eq 0 ]; then
  if [ "$DRY_RUN" = true ] || [ "$NUDGE_MERGED" -eq 0 ]; then
    echo "Nudge queue is empty, running test-fbc..." >&2
    "$SCRIPT_DIR/process-test-fbc.sh" $SCRIPT_ARGS > "$TESTFBC_RESULT"
  fi
fi

format_pr_row() {
  local repo="${1:-}"
  local pr="${2:-}"
  local branch="${3:-}"
  local components="${4:-}"
  local reason="${5:-}"
  # Shorten check names in reason strings:
  #   - Strip the "Red Hat Konflux / " prefix from all check names
  #   - Collapse enterprise-contract check names to "conforma (component)"
  reason=$(echo "$reason" | sed -E \
    -e 's|Red Hat Konflux / ||g' \
    -e 's|openshift-sandboxed-containers-enterprise-contract[^ /]* / ([^ ,]+)|conforma (\1)|g')

  local url="${REPO_URLS[$repo]:-}"
  local link
  if [ -z "$url" ]; then
    link="#$pr"
  else
    link="[#$pr]($url/pull/$pr)"
  fi

  if [ -z "$reason" ]; then
    printf -- '- %s %s/%s (%s)\n' "$link" "$repo" "$branch" "$components"
  else
    printf -- '- %s %s/%s (%s)\n  - %s\n' "$link" "$repo" "$branch" "$components" "$reason"
  fi
}

print_section() {
  local title="$1"
  local json_file="$2"
  local array_key="$3"
  local reason_key="${4:-}"

  if [ ! -f "$json_file" ]; then
    return
  fi

  local count=$(jq ".$array_key | length" "$json_file")
  if [ "$count" -eq 0 ]; then
    return
  fi

  echo ""
  echo "### $title ($count)"
  if [ -z "$reason_key" ]; then
    jq -r ".${array_key}[] | [.repo, .pr, .base_branch, (.components | join(\", \"))] | @tsv" "$json_file" | \
      while IFS=$'\t' read -r repo pr branch components; do
        format_pr_row "$repo" "$pr" "$branch" "$components" ""
      done
  else
    jq -r ".${array_key}[] | [.repo, .pr, .base_branch, (.components | join(\", \")), .${reason_key}] | @tsv" "$json_file" | \
      while IFS=$'\t' read -r repo pr branch components reason; do
        format_pr_row "$repo" "$pr" "$branch" "$components" "$reason"
      done
  fi
}

REPORT_FILE="Konflux-PR-report-$(date +%Y%m%d-%H%M%S).md"

# Generate report
{
  echo "# Konflux PR Processing Report"
  echo ""
  echo "*Generated: $(date '+%Y-%m-%d %H:%M:%S')*"
  echo ""

  # Mintmaker section
  echo "## Mintmaker"
  print_section "Labelled" "$MINTMAKER_RESULT" "labelled" "_label"
  print_section "Merged" "$MINTMAKER_RESULT" "merged"
  print_section "Waiting" "$MINTMAKER_RESULT" "waiting" "_wait_reason"
  print_section "Skipped" "$MINTMAKER_RESULT" "skipped" "_skip_reason"
  print_section "On hold (do-not-merge/hold)" "$MINTMAKER_RESULT" "held"

  mm_labelled=$(jq '.labelled | length' "$MINTMAKER_RESULT")
  mm_merged=$(jq '.merged | length' "$MINTMAKER_RESULT")
  mm_waiting=$(jq '.waiting | length' "$MINTMAKER_RESULT")
  mm_skipped=$(jq '.skipped | length' "$MINTMAKER_RESULT")
  mm_held=$(jq '.held | length' "$MINTMAKER_RESULT")
  echo ""
  echo "**Mintmaker Summary:** $mm_labelled labelled, $mm_merged merged, $mm_waiting waiting, $mm_skipped skipped, $mm_held on hold"

  echo ""
  echo "## Nudge"
  print_section "Labelled" "$NUDGE_RESULT" "labelled" "_label"
  print_section "Merged" "$NUDGE_RESULT" "merged"
  print_section "Held back" "$NUDGE_RESULT" "held_back" "_held_reason"
  print_section "Skipped" "$NUDGE_RESULT" "skipped" "_skip_reason"
  print_section "On hold (do-not-merge/hold)" "$NUDGE_RESULT" "held"

  nd_labelled=$(jq '.labelled | length' "$NUDGE_RESULT")
  nd_merged=$(jq '.merged | length' "$NUDGE_RESULT")
  nd_held=$(jq '.held_back | length' "$NUDGE_RESULT")
  nd_skipped=$(jq '.skipped | length' "$NUDGE_RESULT")
  nd_on_hold=$(jq '.held | length' "$NUDGE_RESULT")
  echo ""
  echo "**Nudge Summary:** $nd_labelled labelled, $nd_merged merged, $nd_held held back, $nd_skipped skipped, $nd_on_hold on hold"

  # Test-FBC section (if it ran)
  if [ -f "$TESTFBC_RESULT" ] && [ -s "$TESTFBC_RESULT" ]; then
    echo ""
    echo "## Test-FBC"
    print_section "Labelled" "$TESTFBC_RESULT" "labelled" "_label"
    print_section "Merged" "$TESTFBC_RESULT" "merged"
    print_section "Skipped" "$TESTFBC_RESULT" "skipped" "_skip_reason"
    print_section "On hold (do-not-merge/hold)" "$TESTFBC_RESULT" "held"

    tfbc_labelled=$(jq '.labelled | length' "$TESTFBC_RESULT")
    tfbc_merged=$(jq '.merged | length' "$TESTFBC_RESULT")
    tfbc_skipped=$(jq '.skipped | length' "$TESTFBC_RESULT")
    tfbc_held=$(jq '.held | length' "$TESTFBC_RESULT")
    echo ""
    echo "**Test-FBC Summary:** $tfbc_labelled labelled, $tfbc_merged merged, $tfbc_skipped skipped, $tfbc_held on hold"
  fi

  # Dry-run notice
  if [ "$DRY_RUN" = true ]; then
    echo ""
    echo "---"
    echo ""
    echo "⚠️  **DRY RUN** — No PRs were modified. The operations shown above would be performed in normal mode."
  fi
} | tee "$REPORT_FILE"

# --- Slack notification ---
if [ -n "${SLACK_WEBHOOK_URL:-}" ]; then
  echo "Sending Slack notification..." >&2

  # Convert markdown to Slack mrkdwn:
  #   - Bold: **text** → *text* (via placeholder to avoid clashing with italic)
  #   - Italic: *text* → _text_
  #   - Headers: # text → *text*
  #   - Links: [text](url) → <url|text>
  #   - Strip horizontal rules (---)
  SLACK_MSG=$(sed -E \
    -e 's/\*\*/\x01/g' \
    -e 's/\*([^*]+)\*/_\1_/g' \
    -e 's/\x01/*/g' \
    -e 's/^#{1,3} (.+)$/*\1*/' \
    -e 's/\[([^]]+)\]\(([^)]+)\)/<\2|\1>/g' \
    -e '/^---$/d' \
    "$REPORT_FILE")

  SLACK_RESPONSE=$(jq -n --arg text "$SLACK_MSG" '{text: $text}' | \
    curl -s -X POST -H 'Content-type: application/json' -d @- "$SLACK_WEBHOOK_URL")

  if [ "$SLACK_RESPONSE" = "ok" ]; then
    echo "Slack notification sent." >&2
  else
    echo "Warning: Slack notification may have failed: $SLACK_RESPONSE" >&2
  fi
fi
