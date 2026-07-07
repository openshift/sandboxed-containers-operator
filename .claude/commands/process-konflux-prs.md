---
name: process-konflux-prs
description: Look at pull requests created by the Konflux bot and manage them, optimizing their merge to avoid repetitive builds.
allowed-tools:
  - Bash(scripts/process-konflux-prs/snapshot-prs.sh*)
  - Bash(scripts/process-konflux-prs/list-prs.sh*)
  - Bash(scripts/process-konflux-prs/get-pr-status.sh*)
  - Bash(scripts/process-konflux-prs/get-pr-components.sh*)
  - Bash(scripts/process-konflux-prs/label-pr.sh*)
  - Bash(scripts/process-konflux-prs/merge-pr.sh*)
  - Bash(scripts/process-konflux-prs/skip-pr.sh*)
  - Bash(scripts/process-konflux-prs/check-pipeline-status.sh*)
  - Bash(scripts/process-konflux-prs/check-merge-safety.sh*)
  - Bash(scripts/process-konflux-prs/get-nudge-prs.sh*)
  - Bash(scripts/process-konflux-prs/get-component-info.sh*)
---

Process the pull requests from Mintmaker and Konflux

# Implementation Requirements

**CRITICAL: Parameter-gated behavior**: The command behavior is strictly controlled by parameters:

- **No parameter**: ONLY list PRs. For each PR, show which images will be rebuilt and the
  suggested action (see [Status output format](#status-output-format)). Do NOT label or merge.
- **`--mintmaker`**: Process Mintmaker PRs ONLY. Apply labels (ok-to-test, lgtm), then merge
  PRs that are ready (see [Step 1 - Mintmaker PR processing](#step-1---mintmaker-pr-processing)).
  Do NOT touch Nudge PRs.
- **`--nudge`**: Process Nudge PRs ONLY, with the Mintmaker safeguard (see
  [Step 2 - Nudge PR processing](#step-2---nudge-pr-processing)). Do NOT touch Mintmaker PRs.
  Merges non-osc nudge PRs when checks pass (osc nudge PRs auto-merge on their own).
- **`--dry-run`**: Show what would be done. Do NOT label or merge. Can be combined with
  `--mintmaker` or `--nudge` to preview that specific step.

`--mintmaker` and `--nudge` are mutually exclusive. If neither is given, only list/status output
is produced.

**CRITICAL**: Always query fresh data. Never use PR numbers from previous runs or earlier in the conversation.

When implementing this skill:

1. **Always start fresh**: Use `list-prs.sh` at the beginning of execution to get current PRs
2. **Process only discovered PRs**: Only process PR numbers returned from `list-prs.sh`
3. **Never hardcode PR numbers**: Do not use PR numbers from memory, previous runs, or earlier conversation turns
4. **Verify state before processing**: Use `get-pr-status.sh` to verify PR state before taking action

All scripts must be called from the **repository root** using the full relative path
`./scripts/process-konflux-prs/<script>.sh`. Never `cd` into the scripts directory.

Example correct workflow:
```bash
# CORRECT: call from repo root, discover PRs dynamically
prs=$(./scripts/process-konflux-prs/list-prs.sh --mintmaker)
for pr in $(echo "$prs" | jq -c '.[]'); do
  repo=$(echo "$pr" | jq -r '.repo')
  number=$(echo "$pr" | jq -r '.number')
  # Process each discovered PR
done

# WRONG: Using hardcoded or remembered PR numbers
for pr_num in 2147 2146 2144 2143; do  # ❌ Never do this
  # ...
done
```

**Efficiency**: use `snapshot-prs.sh` to discover PRs and fetch all their statuses in a
single script invocation:

```bash
snapshot=$(./scripts/process-konflux-prs/snapshot-prs.sh --mintmaker)
```

Do NOT call `list-prs.sh` followed by a `get-pr-status.sh` loop — `snapshot-prs.sh`
already does both.

**IMPORTANT**: Do not perform active polling/wait loops when executing.
This skill is intended to be run on a regular basis. Each time it runs, it should
evaluate the status of all PRs, and try to move them forward according to the
workflow. If the workflow states that you "need to wait" for something (like:
wait for the completion of checks), this skill should actually let the PR untouched,
and on next run, look at the status again.
The labels we put on the PRs are used to keep some state information without the
need to maintain any local status.

# Available Helper Scripts

All scripts are located in `scripts/process-konflux-prs/` and output JSON.

## list-prs.sh
List Konflux PRs from repositories.
```bash
./scripts/process-konflux-prs/list-prs.sh [--mintmaker|--nudge] [--repo REPO_NAME]
```
Output: JSON array with `{number, title, url, repo, type}`

## get-pr-status.sh
Get PR status including checks, labels, and rebuilt components — **one call replaces a
separate `get-pr-components.sh` call**.
```bash
./scripts/process-konflux-prs/get-pr-status.sh --repo REPO_NAME --pr PR_NUMBER
```
Output: JSON object — exact shape:
```json
{
  "number": 1234,
  "title": "chore(deps): update …",
  "state": "OPEN",
  "labels": ["ok-to-test"],          // array of strings, NOT objects
  "has_ok_to_test": true,
  "has_lgtm": false,
  "components": ["osc-operator"],    // rebuilt components (from build pipeline checks)
  "build_checks_passed": true,       // true only when all build pipeline checks are SUCCESS
                                     // enterprise-contract checks (conclusion=NEUTRAL) excluded
  "build_checks_failed": [],         // names of build checks with FAILURE conclusion
  "pending_checks": 0,               // integer count of IN_PROGRESS/QUEUED checks
  "checks": [...]                    // raw checks array, null entries filtered
}
```

**Use `build_checks_passed` (not `all_checks_passed`) to determine if a PR is ready.**
Enterprise-contract checks legitimately return `NEUTRAL` and are excluded from
`build_checks_passed`. If `pending_checks > 0`, checks are still running — wait for
the next run.

## snapshot-prs.sh
Discover PRs and fetch their status in one pass — replaces a `list-prs.sh` call followed
by a `get-pr-status.sh` loop.
```bash
./scripts/process-konflux-prs/snapshot-prs.sh --mintmaker|--nudge [--repo REPO_NAME]
```
Output: JSON array of status objects, one per PR:
```json
[{
  "repo": "osc", "pr": 1234, "title": "…",
  "components": ["osc-operator"],
  "build_checks_passed": true,
  "build_checks_failed": [],
  "pending_checks": 0,
  "has_ok_to_test": true,
  "has_lgtm": false
}]
```
**Use this instead of calling `list-prs.sh` then looping over `get-pr-status.sh`.**

## label-pr.sh output and error handling

`label-pr.sh` outputs a single JSON object. Do **not** try to parse anything else
from its output — the underlying `gh` command occasionally prints extra text to
stdout that is now suppressed, but if a future version leaks text before the JSON,
a display-layer `jq` parse error does **not** mean the label was not applied.

**When a batch labeling loop produces jq parse errors:**
1. Do NOT re-run `label-pr.sh` for the same PR — the label may already be set.
2. Continue to the next phase (e.g. apply `lgtm` after the `ok-to-test` batch).
3. If verification is needed, use `get-pr-status.sh` to check the current label
   state — never infer failure from display-layer errors alone.

## get-pr-components.sh
Identify components that will be rebuilt (excludes enterprise-contract validation checks).
```bash
./scripts/process-konflux-prs/get-pr-components.sh --repo REPO_NAME --pr PR_NUMBER
```
Output: JSON array of component names.
**Note:** `get-pr-status.sh` already returns `components` — prefer using that to avoid
a redundant GitHub API call.

## label-pr.sh
Add a label to a PR.
```bash
./scripts/process-konflux-prs/label-pr.sh --repo REPO_NAME --pr PR_NUMBER --label LABEL_NAME
```
Labels: "ok-to-test" or "lgtm"
Output: JSON with {success, repo, pr, label, comment}
Note: when label is "ok-to-test", also posts a `/ok-to-test` comment; `comment` indicates whether it succeeded.

## skip-pr.sh
Mark a PR as intentionally skipped: adds the `mintmaker-skip` label and posts an
explanatory comment. The label prevents re-posting on subsequent runs.
```bash
./scripts/process-konflux-prs/skip-pr.sh --repo REPO_NAME --pr PR_NUMBER --reason "reason"
```
Output: JSON with {success, repo, pr, message}

## merge-pr.sh
Merge a PR using the merge (non-squash) strategy.
```bash
./scripts/process-konflux-prs/merge-pr.sh --repo REPO_NAME --pr PR_NUMBER
```
Output: JSON with {success, repo, pr, message}
Note: deletes the source branch after merging.

## check-pipeline-status.sh
Check Konflux pipeline status.
```bash
# For on-push (default)
./scripts/process-konflux-prs/check-pipeline-status.sh --component COMPONENT_NAME

# For on-pull-request (--pr-id REQUIRED)
./scripts/process-konflux-prs/check-pipeline-status.sh --component COMPONENT_NAME --type on-pull-request --pr-id PR_ID
```
Output: JSON with {component, pipeline_type, latest, all_runs}
Note: --pr-id is mandatory for on-pull-request to avoid matching runs from different PRs

## check-merge-safety.sh
Check whether it is safe to merge a PR by verifying no on-push pipeline is running
for any of its components. Wraps `check-pipeline-status.sh` over a component list.
```bash
./scripts/process-konflux-prs/check-merge-safety.sh --components "comp1 comp2 ..."
```
Output: JSON `{safe_to_merge: bool, blocking: [{component, pipeline, reason}]}`
- `safe_to_merge`: true when no component has a running on-push pipeline
- `blocking`: list of components currently blocked (empty when safe)

**Use this instead of a manual loop over `check-pipeline-status.sh` before each merge.**

## get-nudge-prs.sh
Find nudge PRs for a component.
```bash
./scripts/process-konflux-prs/get-nudge-prs.sh --component COMPONENT_NAME
```
Output: JSON array of nudge PRs

## get-component-info.sh
Get component metadata.
```bash
./scripts/process-konflux-prs/get-component-info.sh [--component COMPONENT_NAME]
```
Output: JSON with {name, repository, build_time_minutes, nudges}

# Workflow definition

We receive two types of pull requests from Konflux:

- **Mintmaker PRs**: dependency updates (base images, konflux references).
- **Nudge PRs**: updated reference to a newly built image from our own pipelines.

The first type comes on a regular basis from Mintmaker.
The second is triggered automatically when a PR is merged: each merge builds (at
least) one image, which then causes Konflux to open or update a Nudge PR in the
downstream component(s).
See the [list of components](#list-of-components) for nudge relationships.

**The workflow is two separate steps, run independently:**

1. **Step 1 — Mintmaker PRs** (`--mintmaker`): label and advance Mintmaker PRs.
2. **Step 2 — Nudge PRs** (`--nudge`): label and advance Nudge PRs, but ONLY
   those that are safe to process (see below).

Merging Mintmaker PRs triggers component rebuilds, which in turn refresh the
existing Nudge PRs. Processing a Nudge PR before the corresponding Mintmaker PRs
are done would merge a stale image reference that gets immediately superseded.
Steps 1 and 2 must therefore stay separate.

## Labelling rules (applies to both steps)

Using labels marks the progression of PRs towards merging, so the command can be
re-run without keeping external state. Developers can also set labels manually and
this process will respect them.

Labels used:

- `ok-to-test`: triggers automated CI on the PR.
- `lgtm`: marks the PR as ready to merge.

Rules:

Both rules are evaluated against the **state returned by `get-pr-status.sh` at the
start of this run** — the snapshot is fixed before any labels are applied. Applying
`ok-to-test` in this run does NOT qualify the same PR for `lgtm` in the same run,
because `ok-to-test` triggers new CI pipelines whose results are not yet known.

- If `has_ok_to_test` is `false` in the snapshot, set `ok-to-test` **and** post a
  `/ok-to-test` comment. Some bots only react to maintainer comments, not label
  additions alone. Stop here for this PR — do NOT also set `lgtm`.
- If `has_ok_to_test` is `true` in the snapshot AND `build_checks_passed` is `true`
  AND `pending_checks` is `0`, set `lgtm`. (`build_checks_passed` excludes
  enterprise-contract checks that return `NEUTRAL` — do NOT use `all_checks_passed`.)

Labelling can be done in parallel for all PRs from all repositories, as there
is no conflict between "on-pull-request" pipelines running concurrently.

Note that Nudge PRs in the osc repository are configured to auto-merge when
tests pass. Setting `ok-to-test` on those PRs is therefore the only required
label. Track the merge pipeline when the PR auto-merges to confirm success
(see [merging](#merging)).

## Step 1 - Mintmaker PR processing

**Only executed when `--mintmaker` is passed.**

### Phase 0 — Skip filter

Before labelling or merging, identify PRs that match a known skip pattern and remove
them from the processing set.

**Known skip patterns:**

- **go-toolset major-version-only tag**: title contains `ubi9/go-toolset` or
  `ubi10/go-toolset` AND the tag is a bare major version (e.g. `v9`, `v10`) with no
  Go version number.
  Detection: title matches `ubi[0-9]+/go-toolset` AND ends with `tag to v<N>` where
  `<N>` is a single integer (no dots). Example: `"Update ubi9/go-toolset Docker tag to v9"`.
  *Why*: we pin `go-toolset` to the Go version tag (e.g. `v1.26.4-...`), not the
  image's own major version. A separate PR with the correct Go version tag will arrive.

For each PR matching a skip pattern:
- If `has_mintmaker_skip` is `false` in the snapshot: call `skip-pr.sh` with an
  appropriate `--reason` explaining the skip. This posts a comment and adds the
  `mintmaker-skip` label so subsequent runs skip it silently.
- Remove the PR from the snapshot used in Phase A and Phase B (do not label or merge it,
  regardless of `has_mintmaker_skip`).

### Phase A — Labelling

1. Call `snapshot-prs.sh --mintmaker` to discover open Mintmaker PRs and fetch their
   status in one pass. Record the result as the **initial snapshot** (this snapshot is
   fixed; it must not be refreshed during the same run).
2. Run Phase 0 to remove skip-pattern PRs from the snapshot.
3. For each remaining entry in the snapshot, apply labelling rules above.

### Phase B — Merging

After all labelling is complete, merge PRs that were **already ready before this run**:

- A PR is eligible to merge if, in the **initial snapshot**: `has_lgtm` is `true`
  AND `build_checks_passed` is `true` AND `pending_checks` is `0`.
- Do NOT merge a PR that received `lgtm` during this run's Phase A — the same
  snapshot principle applies: lgtm was just set, so this is its first eligible run,
  not a confirmed-ready state from a prior run.
- **Pre-merge pipeline check**: Before merging a PR, call:
  ```bash
  ./scripts/process-konflux-prs/check-merge-safety.sh --components "comp1 comp2 ..."
  ```
  using the snapshot's `components` array. If `safe_to_merge` is `false`, **skip this
  PR** and report it as "blocked by running on-push pipeline" (include the `blocking`
  list in the report). Do NOT merge it.
- Merge PRs one at a time using `merge-pr.sh`. After each merge, note which
  components will be rebuilt (from the snapshot's `components` field).
- Within a single run, do NOT merge two PRs that rebuild the same component —
  the second one must wait for the on-push pipeline triggered by the first to complete.
  Leave it for the next run.
- Use `merge-pr.sh --repo REPO_NAME --pr PR_NUMBER` to merge.

### Phase C — Report

Report four groups:
- PRs skipped this run (matched a skip pattern; `skip-pr.sh` called for first-time skips).
- PRs labelled in this run (ok-to-test or lgtm added).
- PRs merged in this run.
- PRs waiting (checks pending, build failed, blocked by a running on-push pipeline, or blocked by a same-component merge in this run).

Do NOT touch Nudge PRs during Step 1.

## Step 2 - Nudge PR processing

**Only executed when `--nudge` is passed.**

Nudge PRs must only be processed when we are certain that no open Mintmaker PR
will trigger a rebuild of the same source component. If a Mintmaker PR would
rebuild component C, and a Nudge PR carries an updated reference for C, merging
the Nudge PR now would be immediately obsoleted by the Mintmaker rebuild.

### Why the hold-back strategy is needed

When multiple Nudge PRs all target the same downstream component (e.g.
`osc-operator-bundle`), merging them triggers one on-push pipeline per merge.
These pipelines run concurrently on their respective HEADs. Because each pipeline
builds and publishes independently, the one that **finishes last** wins — regardless
of merge order. If the last-finishing pipeline was triggered by the first merge, it
produces an image that is missing all the updates from later merges.

The fix: hold one PR back and only merge it once all other on-push pipelines for
that component group have completed. Its merge then triggers a single, uncontested
final pipeline that builds from HEAD containing all previous merges, producing a
complete image with all component updates.

### Step 2 algorithm

**Phase A — Build the "Mintmaker-blocked" component set**

1. Call `snapshot-prs.sh --mintmaker` to get all open Mintmaker PRs with their status.
2. Union the `components` arrays from all entries into a single set:
   `mintmaker_rebuilding_components`.

**Phase B — Filter, group, and label Nudge PRs**

1. Call `snapshot-prs.sh --nudge` to get all open Nudge PRs with their status.
   Record the result as the **initial snapshot** (fixed for this run).
2. For each Nudge PR, identify the **source component** — the component whose
   reference was updated — by matching known component names against the PR title.
   Use `get-component-info.sh` (no args) to get the full list of component names.
3. Remove PRs whose source component is in `mintmaker_rebuilding_components` from
   the working set. Note them as "blocked by open Mintmaker PR". Do NOT label them.
4. **Group** the remaining PRs by their `components` array (exact match on the set
   of components they will rebuild — same components = same group).
5. For each group:

   **Group has multiple open PRs:**
   - Identify PRs with `has_ok_to_test: false` (not yet labeled).
   - If **2 or more** PRs have `has_ok_to_test: false`: apply `ok-to-test` to all
     except the **lowest-numbered** one. The lowest-numbered becomes the **held-back
     PR** for this group. Do NOT label it this run.
   - If **exactly one** PR has `has_ok_to_test: false` and the rest have
     `has_ok_to_test: true` (still open, not yet merged): the held-back PR is waiting
     for its peers to merge first. Do NOT label it — leave it for the next run.
   - If **all** PRs have `has_ok_to_test: true`: all are in flight; nothing to label.

   **Group has exactly one open PR** (all peers have merged — they are no longer in
   the snapshot):
   - This is either a standalone PR or the held-back PR whose peers have all merged.
   - Call `check-merge-safety.sh --components "<components>"`.
   - If `safe_to_merge: true`: apply standard labelling rules — `ok-to-test` if not
     set; `lgtm` if `ok-to-test` is already set and checks pass (for non-osc repos;
     see Phase C).
   - If `safe_to_merge: false`: **do not label**. Report as "waiting for on-push
     pipelines to complete" and include the `blocking` list. Revisit next run.

**Phase C — Merge non-osc Nudge PRs**

Nudge PRs in the osc repository auto-merge once checks pass. Nudge PRs in other
repositories (compute-artifacts, podvm-scripts) do not auto-merge and require
explicit merging.

For each non-osc Nudge PR where the **initial snapshot** shows `has_ok_to_test: true`
AND `build_checks_passed: true` AND `pending_checks: 0` AND the PR is **not currently
designated as held-back** (i.e., it is not the sole unlabeled PR in a multi-PR group):

- Call `check-merge-safety.sh --components "<components>"`. If `safe_to_merge` is
  `false`, skip and report as "blocked by running on-push pipeline".
- Apply `lgtm` label.
- Merge using `merge-pr.sh`. Within a single run, do NOT merge two PRs that rebuild
  the same component — the second must wait for the next run.

**Phase D — Report**

Report four groups:
- **Labeled this run**: Nudge PRs that received `ok-to-test` or `lgtm`.
- **Merged this run**: non-osc Nudge PRs successfully merged.
- **Held back**: PRs designated as held-back (waiting for group peers to merge or
  for pipelines to clear). For each, show which peers are still open or which
  pipelines are blocking.
- **Skipped**: PRs blocked by an open Mintmaker PR, by still-running on-push
  pipelines, or with pending/failed checks. Include the reason for each.

## Merging

PRs are ready to merge when they have `lgtm` and all checks are passing.

We need to be careful when merging a PR: each merge triggers on-push pipelines
that rebuild the component. A second PR for the same component must not be merged
until those pipelines succeed.

The checks that run on the pull request itself include Konflux pipeline builds.
The same pipelines will run on-push when the PR is merged. It is then possible
to know, before the PR is merged, which component it will rebuild.
Use `get-pr-components.sh` to identify which components will be rebuilt.

The build pipelines themselves can be monitored from the Konflux cluster, using
`check-pipeline-status.sh`.
Then when we merge a PR, we can monitor which on-push pipelines are triggered, and
wait for their completion before merging another PR for the same component.

When the on-push pipeline is successful, a Nudge PR can be created.
If the Nudge PR already exists, it is updated every time the same component is rebuilt.
We then let the Nudge PRs open as long as we are processing Mintmaker PRs, and
process the Nudge PRs as a second step, to avoid unnecessary rebuilds.
Use `get-component-info.sh` to see which components are nudged when a component is rebuilt.

Verifying that the nudge PR is created or updated is part of how we verify that
a Mintmaker PR was successfully merged. If the on-push pipeline failed, or if the
expected Nudge PR update doesn't come, we consider the merge as failed, report it,
and stop processing any other PR related to the same component.

# List of repositories

| Repo name         | URL                                                           |
| ----------------- | ------------------------------------------------------------- |
| kata-containers   | `https://github.com/openshift/kata-containers`                |
| cloud-api-adaptor | `https://github.com/openshift/cloud-api-adaptor`              |
| compute-artifacts | `https://github.com/openshift/confidential-compute-artifacts` |
| podvm-scripts     | `https://github.com/confidential-devhub/coco-podvm-scripts`   |
| osc               | `https://github.com/openshift/sandboxed-containers-operator`  |

# Mintmaker PRs filter

Implemented in `list-prs.sh --mintmaker`:

- status is "open"
- author is the "app/red-hat-konflux" bot
- title matches one of the following patterns (note: generalised to avoid missing new image types):

  - starts with `"chore(deps): update konflux references"` or `"Update Konflux references"`
  - starts with `"chore(deps): update registry.access.redhat.com/ubi9/"` (any ubi9 image)
  - starts with `"chore(deps): update registry.access.redhat.com/ubi10/"` (any ubi10 image)
  - starts with `"Update registry.access.redhat.com/ubi9/"` or `"Update registry.access.redhat.com/ubi10/"`

# Nudge PRs filter

Implemented in `list-prs.sh --nudge`:

- status is "open"
- author is the "app/red-hat-konflux" bot
- label "konflux-nudge" is set
- title does NOT start with "chore(deps): update osc-operator-bundle"

# List of components

The following table shows the list of components, the repository they are built from,
an estimation of the time to build the image, and the component(s) that is updated
by a Nudge PR when they are rebuilt.

Available via `get-component-info.sh`.

| Component             | Repository        | Approx. build time | Nudged component(s)                                   |
| --------------------- | ----------------- | ------------------ | ----------------------------------------------------- |
| osc-monitor           | kata-containers   | 10 min             | osc-operator-bundle                                   |
| osc-caa               | cloud-api-adaptor | 15 min             | osc-operator-bundle                                   |
| osc-caa-webhook       | cloud-api-adaptor | 10 min             | osc-operator-bundle                                   |
| osc-podvm-payload     | cloud-api-adaptor | 45 min             | osc-operator-bundle, osc-dm-verity-image, osc-initrds |
| osc-pccs              | compute-artifacts |  8 min             | osc-operator-bundle                                   |
| osc-tdx-qgs           | compute-artifacts |  8 min             | osc-operator-bundle                                   |
| osc-storage-helper    | compute-artifacts |  8 min             | osc-operator-bundle                                   |
| osc-operator          | osc               | 12 min             | osc-operator-bundle                                   |
| osc-operator-bundle   | osc               |  3 min             | osc-test-fbc                                          |
| osc-podvm-builder     | osc               | 10 min             | osc-operator-bundle                                   |
| osc-must-gather       | osc               | 10 min             | osc-operator-bundle                                   |
| osc-dm-verity-image   | podvm-scripts     | 50 min             | osc-operator-bundle                                   |
| build-dm-verity-image | podvm-scripts     |  2 min             | osc-dm-verity-image                                   |
| osc-initrds           | compute-artifacts | 15 min             |                                                       |
| osc-test-fbc          | osc               | 15 min             |                                                       |

Note about the osc-test-fbc component:
This is the file-based catalog that we use for testing. It is updated when the
bundle is rebuilt.
As the bundle gets rebuilt every time any other image is rebuilt, we want to keep
the Nudge PR for the osc-test-fbc opened as long as there are updates going on.
That's why this process should ignore the Nudge PR with title like:
"chore(deps): update osc-operator-bundle"
as these are the PRs that update the catalog.
Merging this PR should only be done manually, when we are ready to start testing
the whole set of images.

# Konflux cluster access

Access to Konflux is granted with a token, generated from a dedicated service account,
with limited access. The token will be provided when setting up the automated process.

Scripts should not use the "oc project" command, as there are limitations
for the service account here. Instead, they should specify the target namespace on
every command (default: ose-osc-tenant).

# GitHub access

A dedicated token should be provided to access GitHub via `gh auth login`.

# How to identify the pipelineruns for a given PR

The pipelines running on the PR can be identified as checks with a name that matches the following pattern:
"Red Hat Konflux / {component-name}-on-pull-request"

**Important:** Enterprise contract checks (validation checks) should be excluded. They follow a different pattern and do not represent component builds.

Multiple component build checks can exist for a given PR, depending on which files are modified by the PR.
The details of the check will show the exact pipelinerun ID, with format {component-name}-on-pull-request-{alphanumeric ID}.
When the PR is merged, a similar pipelinerun will start, with pattern:
{component-name}-on-push-{other alphanumeric ID}

# Status output format

When run with the "--status" parameter, the result should be displayed on the
terminal (if available) and saved under the local folder as a markdown file with
a timestamp in the filename for easy filtering (like: "YYYYMMDD-Konflux-PR-status.md").

The output for each PR should include:

- PR Number (with link to the PR on GitHub)
- Rebuilt component(s)
- Current status
- Recommended action
