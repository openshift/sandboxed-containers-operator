#!/bin/bash
# Discover Mintmaker or Nudge PRs and fetch their status in one pass.
# Combines list-prs.sh + get-pr-status.sh into a single snapshot.
#
# Usage: ./snapshot-prs.sh --mintmaker|--nudge [--repo REPO_NAME]
#
# Output: JSON array of PR status objects:
#   [{repo, pr, title, components, build_checks_passed, build_checks_failed,
#     pending_checks, has_ok_to_test, has_lgtm, has_mintmaker_skip}, ...]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PR_TYPE=""
REPO_FILTER=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --mintmaker|--nudge|--test-fbc)
            PR_TYPE="$1"
            shift
            ;;
        --repo)
            REPO_FILTER="--repo $2"
            shift 2
            ;;
        *)
            echo "Usage: $0 --mintmaker|--nudge [--repo REPO_NAME]" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$PR_TYPE" ]]; then
    echo "Usage: $0 --mintmaker|--nudge|--test-fbc [--repo REPO_NAME]" >&2
    exit 1
fi

prs=$("$SCRIPT_DIR/list-prs.sh" "$PR_TYPE" $REPO_FILTER)

snapshot='[]'
while IFS= read -r pr; do
    repo=$(echo "$pr" | jq -r '.repo')
    number=$(echo "$pr" | jq -r '.number')
    if ! status=$("$SCRIPT_DIR/get-pr-status.sh" --repo "$repo" --pr "$number" 2>&1); then
        echo "Warning: failed to get status for $repo #$number: $status" >&2
        continue
    fi
    entry=$(echo "$status" | jq --arg repo "$repo" \
        '{repo: $repo, pr: .number, title: .title,
          base_branch: (.base_branch // ""),
          components: (.components // []),
          build_checks_passed,
          build_checks_failed: (.build_checks_failed // []),
          other_checks_failed: (.other_checks_failed // []),
          pending_checks,
          has_ok_to_test,
          has_lgtm,
          has_mintmaker_skip: ((.labels // []) | any(. == "mintmaker-skip"))}')
    snapshot=$(echo "$snapshot" | jq --argjson e "$entry" '. + [$e]')
done < <(echo "$prs" | jq -c '.[]')

echo "$snapshot" | jq '.'
