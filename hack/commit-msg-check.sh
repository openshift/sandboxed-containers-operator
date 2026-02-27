#!/bin/bash
# Validates commit messages against project conventions
# Usage: ./commit-msg-check.sh [BASE_REF] [HEAD_REF]
# Example: ./commit-msg-check.sh origin/devel HEAD

set -euo pipefail

BASE_REF="${1:-origin/devel}"
HEAD_REF="${2:-HEAD}"

FAILED=0
SUBJECT_MAX_LEN=80
BODY_MAX_LEN=150

check_commit() {
    local sha="$1"
    local subject body lines has_body=0 has_signoff=0

    subject=$(git log --format=%s -n 1 "$sha")
    body=$(git log --format=%b -n 1 "$sha")

    # Check subject line length
    if [ ${#subject} -gt $SUBJECT_MAX_LEN ]; then
        echo "ERROR: Subject too long (${#subject} > $SUBJECT_MAX_LEN chars): $sha"
        echo "  $subject"
        FAILED=1
    fi

    # Check subsystem prefix
    if ! echo "$subject" | grep -qE '^[a-zA-Z_/-]+:'; then
        echo "ERROR: Missing subsystem prefix (e.g., 'feat:', 'fix:'): $sha"
        echo "  $subject"
        FAILED=1
    fi

    # Check commit body exists
    if [ -n "$body" ] && [ "$body" != "" ]; then
        has_body=1
    fi

    if [ $has_body -eq 0 ]; then
        echo "ERROR: Missing commit body: $sha"
        echo "  $subject"
        FAILED=1
    fi

    # Check for Signed-off-by
    if echo "$body" | grep -qE '^Signed-off-by:'; then
        has_signoff=1
    fi

    if [ $has_signoff -eq 0 ]; then
        echo "ERROR: Missing Signed-off-by tag: $sha"
        echo "  $subject"
        FAILED=1
    fi

    # Check body line lengths
    if [ $has_body -eq 1 ]; then
        while IFS= read -r line; do
            # Skip empty lines and lines starting with non-alphabetic chars (indented code, stack traces, etc.)
            if [ -z "$line" ] || echo "$line" | grep -qE '^[^a-zA-Z]'; then
                continue
            fi
            # Skip Signed-off-by lines
            if echo "$line" | grep -qE '^Signed-off-by:'; then
                continue
            fi
            # Skip lines containing URLs
            if echo "$line" | grep -qE 'https?://'; then
                continue
            fi
            if [ ${#line} -gt $BODY_MAX_LEN ]; then
                echo "ERROR: Body line too long (${#line} > $BODY_MAX_LEN chars): $sha"
                echo "  $line"
                FAILED=1
            fi
        done <<< "$body"
    fi
}

# Get all commits in range
commits=$(git rev-list "$BASE_REF..$HEAD_REF")

if [ -z "$commits" ]; then
    echo "No commits to check"
    exit 0
fi

echo "Checking commits from $BASE_REF to $HEAD_REF"

for sha in $commits; do
    # Skip revert commits
    if git log --format=%s -n 1 "$sha" | grep -q '^Revert "'; then
        continue
    fi

    # Skip Konflux bot commits
    if git log --format=%b -n 1 "$sha" | grep -qE '^Signed-off-by:.*red-hat-konflux'; then
        continue
    fi

    # Skip merge commits
    if git log --format=%s -n 1 "$sha" | grep -qE 'Merge pull request #[0-9]+'; then
        continue
    fi

    check_commit "$sha"
done

if [ $FAILED -eq 1 ]; then
    echo ""
    echo "Commit message validation failed."
    echo "See CONTRIBUTING.md for commit message format requirements."
    exit 1
fi

echo "All commit messages are valid."
