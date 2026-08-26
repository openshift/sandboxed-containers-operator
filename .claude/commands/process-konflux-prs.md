---
name: process-konflux-prs
description: Look at pull requests created by the Konflux bot and manage them, optimizing their merge to avoid repetitive builds.
allowed-tools:
  - Bash(scripts/process-konflux-prs/process-mintmaker.sh)
  - Bash(scripts/process-konflux-prs/process-nudge.sh)
  - Bash(scripts/process-konflux-prs/snapshot-prs.sh *)
  - Bash(scripts/process-konflux-prs/list-prs.sh *)
  - Bash(scripts/process-konflux-prs/get-pr-status.sh *)
  - Bash(scripts/process-konflux-prs/get-pr-components.sh *)
  - Bash(scripts/process-konflux-prs/label-pr.sh *)
  - Bash(scripts/process-konflux-prs/merge-pr.sh *)
  - Bash(scripts/process-konflux-prs/skip-pr.sh *)
  - Bash(scripts/process-konflux-prs/check-pipeline-status.sh *)
  - Bash(scripts/process-konflux-prs/check-merge-safety.sh *)
  - Bash(scripts/process-konflux-prs/get-nudge-prs.sh *)
  - Bash(scripts/process-konflux-prs/process-test-fbc.sh)
  - Bash(scripts/process-konflux-prs/get-component-info.sh)
  - Bash(scripts/process-konflux-prs/get-component-info.sh *)
---

Process the pull requests from Mintmaker and Konflux

# Modes

- **No argument**: run `--mintmaker`; if its output has `"empty": true`, also run `--nudge`.
  After nudge, if **all four** nudge arrays (`labelled`, `merged`, `held_back`, `skipped`)
  are empty (i.e. the nudge queue is fully drained and nothing was merged this run), also
  run `--test-fbc`. The `merged` check is important: a nudge merge triggers on-push
  pipelines that will produce new `osc-operator-bundle` nudge PRs, so test-fbc must not
  run until those have been processed in a future invocation.
- **`--mintmaker`**: call `process-mintmaker.sh`, format the result as the Mintmaker report.
- **`--nudge`**: call `process-nudge.sh`, format the result as the Nudge report.
- **`--test-fbc`**: call `process-test-fbc.sh`, format the result as the Test-FBC report.
- **`--dry-run`**: call `snapshot-prs.sh --mintmaker` and `snapshot-prs.sh --nudge` in
  parallel, format all PRs as a status table, and save to a timestamped markdown file
  (see [Status output format](#status-output-format)).

`--mintmaker`, `--nudge`, `--test-fbc`, and `--dry-run` are mutually exclusive.

# Orchestration scripts

All logic (skip filter, label decisions, merge eligibility, component conflict tracking,
safety checks) is implemented in two scripts. Call them from the repo root.

## process-mintmaker.sh

```bash
./scripts/process-konflux-prs/process-mintmaker.sh
```

Runs the full Mintmaker workflow and exits. Output JSON:

```json
{
  "empty":    false,
  "skipped":  [{ "repo":"…", "pr":123, "title":"…", "_skip_reason":"…" }],
  "labelled": [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                 "components":[…], "_label":"ok-to-test|lgtm" }],
  "merged":   [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                 "components":[…] }],
  "waiting":  [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                 "components":[…], "_wait_reason":"…",
                 "_blocking":[{"component":"…","reason":"…"}] }]
}
```

`_blocking` is present only on safety-blocked waiting entries.

## process-nudge.sh

```bash
./scripts/process-konflux-prs/process-nudge.sh
```

Runs the full Nudge workflow and exits. Output JSON:

```json
{
  "labelled":  [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                  "components":[…], "_label":"ok-to-test|lgtm" }],
  "merged":    [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                  "components":[…] }],
  "held_back": [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                  "components":[…], "_held_reason":"…" }],
  "skipped":   [{ "repo":"…", "pr":123, "title":"…", "_skip_reason":"…",
                  "_blocking":[{"component":"…","reason":"…"}] }]
}
```

`_blocking` is present only on safety-blocked skipped entries.

## process-test-fbc.sh

```bash
./scripts/process-konflux-prs/process-test-fbc.sh
```

Runs the test-FBC workflow and exits. Processes `chore(deps): update osc-operator-bundle`
PRs from the `osc` repo using the same label→lgtm→merge flow as nudge, without
grouping/hold-back logic (each PR is on a distinct base branch).

Output JSON:

```json
{
  "empty":    false,
  "labelled": [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                 "components":[…], "_label":"ok-to-test|lgtm" }],
  "merged":   [{ "repo":"…", "pr":123, "title":"…", "base_branch":"…",
                 "components":[…] }],
  "skipped":  [{ "repo":"…", "pr":123, "title":"…", "_skip_reason":"…",
                 "_blocking":[{"component":"…","reason":"…"}] }]
}
```

`_blocking` is present only on safety-blocked skipped entries.

# Report format

Format the JSON output as four grouped tables. Use the PR `repo` and `pr` number to
build the GitHub URL (see [List of repositories](#list-of-repositories)).

**Mintmaker report sections:** Skipped | Labelled | Merged | Waiting

**Nudge report sections:** Labelled | Merged | Held back | Skipped

**Test-FBC report sections:** Labelled | Merged | Skipped

For each PR include: PR link, repo, branch, components, and the relevant reason/label field.
Append a one-line summary (counts of each group) at the end.

# List of repositories

| Repo name         | GitHub URL                                                    |
| ----------------- | ------------------------------------------------------------- |
| kata-containers   | `https://github.com/openshift/kata-containers`                |
| cloud-api-adaptor | `https://github.com/openshift/cloud-api-adaptor`              |
| compute-artifacts | `https://github.com/openshift/confidential-compute-artifacts` |
| podvm-scripts     | `https://github.com/confidential-devhub/coco-podvm-scripts`   |
| osc               | `https://github.com/openshift/sandboxed-containers-operator`  |

# List of components

Available via `get-component-info.sh`. Needed for `--dry-run` recommended-action output.

| Component             | Repository        | Build time | Nudges                                                |
| --------------------- | ----------------- | ---------- | ----------------------------------------------------- |
| osc-monitor           | kata-containers   | 10 min     | osc-operator-bundle                                   |
| osc-caa               | cloud-api-adaptor | 15 min     | osc-operator-bundle                                   |
| osc-caa-webhook       | cloud-api-adaptor | 10 min     | osc-operator-bundle                                   |
| osc-podvm-payload     | cloud-api-adaptor | 45 min     | osc-operator-bundle, osc-dm-verity-image, osc-initrds |
| osc-pccs              | compute-artifacts |  8 min     | osc-operator-bundle                                   |
| osc-tdx-qgs           | compute-artifacts |  8 min     | osc-operator-bundle                                   |
| osc-storage-helper    | compute-artifacts |  8 min     | osc-operator-bundle                                   |
| osc-operator          | osc               | 12 min     | osc-operator-bundle                                   |
| osc-operator-bundle   | osc               |  3 min     | osc-test-fbc                                          |
| osc-podvm-builder     | osc               | 10 min     | osc-operator-bundle                                   |
| osc-must-gather       | osc               | 10 min     | osc-operator-bundle                                   |
| osc-dm-verity-image   | podvm-scripts     | 50 min     | osc-operator-bundle                                   |
| build-dm-verity-image | podvm-scripts     |  2 min     | osc-dm-verity-image                                   |
| osc-initrds           | compute-artifacts | 15 min     |                                                       |
| osc-test-fbc          | osc               | 15 min     |                                                       |

# Mintmaker PRs filter

Implemented in `list-prs.sh --mintmaker`. PRs must be open, authored by
`app/red-hat-konflux`, and title must match one of:
- `chore(deps): update konflux references` / `Update Konflux references`
- `chore(deps): update registry.access.redhat.com/ubi9/` (any ubi9 image)
- `chore(deps): update registry.access.redhat.com/ubi10/` / `Update registry.access.redhat.com/ubi9|10/`
- `chore(deps): update registry.redhat.io/rhel9/rhel-bootc` / `Update registry.redhat.io/rhel9/rhel-bootc`
- `chore(deps): refresh rpm lockfiles`
- `Update podvm-payload/kata-containers digest` / `chore(deps): update podvm-payload/kata-containers`
- `Update config/peerpods/podvm/cloud-api-adaptor digest` / `chore(deps): update config/peerpods/podvm/cloud-api-adaptor`

# Nudge PRs filter

Implemented in `list-prs.sh --nudge`. PRs must be open, authored by
`app/red-hat-konflux`, have label `konflux-nudge`, and title must NOT start with
`chore(deps): update osc-operator-bundle` (those are routed to the test-fbc queue
instead, via `list-prs.sh --test-fbc`).

# Test-FBC PRs filter

Implemented in `list-prs.sh --test-fbc`. PRs must be open, authored by
`app/red-hat-konflux`, have label `konflux-nudge`, and title must start with
`chore(deps): update osc-operator-bundle`. Found only in the `osc` repo; they rebuild
the `osc-test-fbc` component.

# Status output format

When run with `--dry-run`, list all open Mintmaker and Nudge PRs. Display the result on
the terminal and save it as a markdown file with a timestamp in the filename for easy
filtering (like: "YYYYMMDD-Konflux-PR-status.md").

The output for each PR should include:

- PR Number (with link to the PR on GitHub)
- Rebuilt component(s)
- Current status
- Recommended action
