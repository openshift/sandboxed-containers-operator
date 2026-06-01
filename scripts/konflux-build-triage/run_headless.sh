#!/bin/bash
#
# Run konflux-triage headless (e.g. from cron).
# Usage: run_headless.sh
#

set -euo pipefail

# Source token from env file
if [ -f "$HOME/.konflux-env" ]; then
    source "$HOME/.konflux-env"
fi
export KONFLUX_TOKEN

if [ -f "${HOME}/claude_env" ]; then
   source "${HOME}/claude_env"
fi

REPO_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
LOG_DIR="$REPO_DIR/.claude/logs"
mkdir -p "$LOG_DIR"
LOGFILE="$LOG_DIR/konflux-triage-$(date +%Y%m%d-%H%M%S).log"

cd "$REPO_DIR"

export PATH="${HOME}/.local/bin:${PATH}"
timeout 10m claude -p "/konflux-triage --post --retest" >> "$LOGFILE" 2>&1

# Keep only the last 7 days of logs
find "$LOG_DIR" -name "konflux-triage-*.log" -mtime +7 -delete 2>/dev/null || true
