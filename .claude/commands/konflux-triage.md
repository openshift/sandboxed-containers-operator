---
name: konflux-triage
description: |
  Triage failed Konflux build pipelines. Pass a PR number for single-PR mode,
  --pipeline <ID> for a specific PipelineRun, or no args to scan all open PRs.
  Use --post to create/edit PR comments (default is dry-run, stdout only).

  Triggers: "konflux triage", "triage konflux", "triage builds", "check builds",
  "build failures", "konflux failures"
allowed-tools:
  - Bash(gh:*)
  - Bash(oc:*)
  - Bash(tkn-results:*)
  - Bash(python3 scripts/konflux-build-triage/process_pull_requests.py:*)
  - Bash(bash scripts/konflux-build-triage/preflight.sh:*)
  - Read(.claude/data/konflux-triage-knowledge.md)
---

Triage failed Konflux build pipelines for the OSC project.

## Argument Parsing

Parse the arguments from `$@`:
- If `--post` is present: enable posting mode (create/edit PR comments). Remove it from args.
- If `--retest` is present: enable auto-retest mode (requires `--post`). Remove it from args. If `--retest` is set without `--post`, report an error: "--retest requires --post".
- If `--pipeline <ID>` is present: operate in PipelineRun mode. Extract the ID. Remove both tokens from args.
- If a number or GitHub PR URL remains: operate in single-PR mode. Extract the PR number.
- If no arguments remain (after removing flags): operate in all-open-PRs mode.

## Execution

Spawn a `konflux-build-triage` agent with the parsed arguments. Pass the mode and flags clearly in the agent prompt. For example:

- No args: "Scan all open PRs for failed Konflux builds (Mode C). Dry-run only."
- `--post 450`: "Triage PR #450 (Mode A). Post/edit triage comments on the PR."
- `--post --retest`: "Scan all open PRs (Mode C). Post triage comments and auto-retest transient failures."
- `--pipeline abc123`: "Triage PipelineRun abc123 (Mode B). Dry-run only."

The agent definition contains the full workflow, failure categories, and comment format. Do not duplicate any workflow logic here.

## Usage Examples

```
/konflux-triage                                    # scan all open PRs, dry-run
/konflux-triage 450                                # triage PR #450, dry-run
/konflux-triage --pipeline abc123                  # triage PipelineRun abc123, dry-run
/konflux-triage --post                             # scan all open PRs, post comments
/konflux-triage --post 450                         # triage PR #450, post comment
/konflux-triage --post --pipeline abc123           # triage PipelineRun, post comment
/konflux-triage --post --retest 450                # triage + auto-retest transient failures
/konflux-triage --post --retest                    # scan all PRs, auto-retest transient failures
/loop 10m /konflux-triage --post --retest          # continuous monitoring with auto-retest
```
