# Konflux PRs Processing

## The problem

We're getting automated PRs across several of our repositories, to keep
dependencies and component images up to date. Two families of PRs are involved:

**Mintmaker PRs** update source-level dependencies. We're using it for base image
digests, Konflux bundle references, RPM lockfiles, and cross-repository image
digests. These PRs must be approved and merged. Each merge triggers an on-push
pipeline that rebuilds and publishes the component image, generating Nudge PRs.

**Nudge PRs** updates our own image references, whenever one of them is rebuilt.
They also need approval before being merged.
To avoid multiple unneeded rebuilds, it is generally better to merge the Nudge
PRs only when Mintmaker PRs are fully processed.
Also, it is important to be careful when merging multiple nudge PRs in parallel,
as the build time and schedule can be unpredictable, leading to the latest bundle
image not carrying the latest images references.

**Test-FBC PRs** are a special subset of nudge PRs that update `osc-operator-bundle` for
our test catalog. We use this as a way to control when a test catalog, containing
all expected updates, should be created. It should be merged only when all 
Mintmaker and Nudge PRs are processed.
Building the test catalog triggers an automated test on the full image set.

Processing this volume of PRs correctly by hand is time-consuming. The queue spans
five repositories, PRs arrive continuously, pipelines can fail, and merging in
the wrong order wastes rebuild capacity.

## The process

Each run executes three stages in order:

```
Stage 1 — Mintmaker
  For each open Mintmaker PR:
    • label ok-to-test (if missing)
    • label lgtm (if ok-to-test set, all build checks passed, none pending)
    • merge  (if lgtm already set before this run and merge is safe)

Stage 2 — Nudge
  For each open Nudge PR:
    • skip: if the component being refreshed still has a Mintmaker PR opened
    • Group processing: hold back the oldest, process the rest
      We can trigger merges for all Nudges at once, as long as we keep one open.
      This last PR will be merged separately, when no other is oppen, effectively
      prevenging race conditions and controlling the content of the bundle image
      that results from the merge.
    • label ok-to-test / lgtm, merge — same rules as Mintmaker

Stage 3 — Test-FBC   (only when Stage 2 queue is fully drained)
  Same labelling/merge flow as a solo nudge PR.
```

Merge safety is enforced before every merge. The merge is deferred if:
- an on-push pipeline is currently running for any of the components impacted by
  the PR
- the last on-push pipeline for this component on the target branch did not succeed

A markdown report is written at the end of each run.

## Interacting with the process

This set of script is meant to be run in automation, in a stateless manner.
It takes all required information from the PRs it is managing
(labels and test/pipeline status).
It is also looking at pipelines running on our Konflux instance at the time it
is executing, to avoid multiple parallel rebuilds of the same components.

In order to control its execution for select PRs, we can also use the labels:

- **ok-to-test**: this label triggers the build/test automations on the CI
  We can add this label manually to start the tests earlier, and make the script
  catch from there.
  We can remove the label to interupt new tests and delay the time where the PR
  will be merged.
- **lgtm**: this label marks the PR as ready to merge, assuming the builds/tests
  are passing. We can manually add or remove this label to control the PR readiness.
- **do-not-merge/hold**: this label can be used to make the script ignore this PR.
  It will still be reported (and maked "on hold"), but the script will not block
  the processing of all other PRs because of it.
- **mintmaker-skip**: this is applied by these scripts, and used to mark some PRs
  as non-needed. The one case where these scripts will mark a PR like that
  is for go-toolset image updates, where we can be suggested an update to the
  "ubi version tag", while we want to keep using the "go version tag", for clarity
  on the toolchain used.
  When the script marks it with this label, it makes us aware that this PR should
  probably be closed, but it doesn't close it itself. It will then ignore this
  PR from all processing, leaving it to us to do what's needed on the PR.
  We can manually add this label to a PR we want to remove from the script's view
  entirely, but somehow want to keep open still (?). "hold" seems more appropriate
  though.

## Script breakdown

### Entry point

| Script | Purpose |
|--------|---------|
| `process-konflux-prs.sh` | Top-level orchestrator. Runs the three stages in order, captures JSON from each, generates the markdown report. Accepts `--dry-run` to generate a status report with no actual changes. |

### Stage scripts

| Script | Purpose |
|--------|---------|
| `process-mintmaker.sh` | Full Mintmaker workflow. Returns a JSON array with the processed PRs status. |
| `process-nudge.sh` | Full Nudge workflow. Returns a JSON array with the processed PRs status. |
| `process-test-fbc.sh` | Test-FBC workflow: Performs the validation and merge of our Test-fbc nudge PR, which will trigger a new catalog build, and automated CI run on it. |

### Data collection

| Script | Purpose |
|--------|---------|
| `snapshot-prs.sh` | Combines `list-prs.sh` + `get-pr-status.sh` into a single JSON array snapshot. Used by all three stage scripts. |
| `list-prs.sh` | Lists open Konflux PRs from all repositories, classified as `mintmaker`, `nudge`, or `test-fbc`. |
| `get-pr-status.sh` | Fetches full status for one PR: labels, build check results, pending count, components. |
| `get-component-info.sh` | Static registry of all known components: name, repository, build time, and which components each one nudges. |
| `get-pr-components.sh` | Identifies which components a PR will rebuild by parsing its Konflux check-run names. |
| `get-nudge-prs.sh` | Finds open nudge PRs for a given component (ad-hoc helper, not used by the main pipeline). |
| `check-pipeline-status.sh` | Queries Tekton (`tkn`) for the latest on-push or on-pull-request pipeline run for a component. |
| `check-merge-safety.sh` | Combines pipeline status (no running on-push) and GitHub check history (last on-push succeeded) to produce a `safe_to_merge` verdict. |

### Actions

| Script | Purpose |
|--------|---------|
| `label-pr.sh` | Adds `ok-to-test` or `lgtm` to a PR; also posts `/ok-to-test` comment when adding `ok-to-test`, so that other automations can be triggered. |
| `merge-pr.sh` | Merges a PR (merge strategy, branch deleted). |
| `skip-pr.sh` | Marks a PR as permanently skipped: adds `mintmaker-skip` label and posts an explanatory comment. |

### Monitoring

| Script | Purpose |
|--------|---------|
| `watcher.sh` | Prints a compact status table of all open Mintmaker and Nudge PRs (repo, PR number, type, branch, OK2TEST, LGTM, HOLD, checks, title). Can be used to monitor and troubleshoot the execution of the main scripts |

## Repositories in scope

| Short name | GitHub repository |
|------------|------------------|
| `osc` | openshift/sandboxed-containers-operator |
| `kata-containers` | openshift/kata-containers |
| `cloud-api-adaptor` | openshift/cloud-api-adaptor |
| `compute-artifacts` | openshift/confidential-compute-artifacts |
| `podvm-scripts` | confidential-devhub/coco-podvm-scripts |

## Usage

### Prerequisites

- `gh` (GitHub CLI) authenticated with write access to all repositories above
- `jq`
- `tkn` (Tekton CLI) with access to the `ose-osc-tenant` namespace on the build cluster

> **TODO**: at this time, both `gh` and `tkn` rely on the user's credentials.
> We need to modify the scripts to use a separate GitHub and Konflux service account.

### Run a full processing pass

```bash
cd scripts/process-konflux-prs
./process-konflux-prs.sh
```

This will run the full workflow. A report `Konflux-PR-report-YYYYMMDD-HHMMSS.md`
is written in the current directory.

### Dry run (analyse without modifying anything)

```bash
./process-konflux-prs.sh --dry-run
```

Performs a fake run, reporting what would be labelled, merged, or skipped, without
actually doing anything.

### Monitor the queue continuously

```bash
watch -n 300 ./watcher.sh
```

### Running individual scripts

Each script is self-contained and can be called directly:

```bash
# Run the nudge workflow separately (with or without "--dry-run")
./process-nudge.sh [--dry-run]

# Full status of one PR
./get-pr-status.sh --repo osc --pr 2801

# All open Mintmaker PRs (raw JSON)
./list-prs.sh --mintmaker

# Snapshot of all Nudge PRs with status
./snapshot-prs.sh --nudge

# Check whether merging a component is safe right now
./check-merge-safety.sh --components osc-operator --repo openshift/sandboxed-containers-operator --branch devel
```
