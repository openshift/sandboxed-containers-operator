---
name: process-konflux-prs
description: Look at pull requests created by the Konflux bot and manage them, optimizing their merge to avoid repetitive builds.
allowed-tools:
  - Bash(scripts/process-konflux-prs/list-prs.sh*)
  - Bash(scripts/process-konflux-prs/get-pr-status.sh*)
  - Bash(scripts/process-konflux-prs/get-pr-components.sh*)
  - Bash(scripts/process-konflux-prs/label-pr.sh*)
  - Bash(scripts/process-konflux-prs/check-pipeline-status.sh*)
  - Bash(scripts/process-konflux-prs/get-nudge-prs.sh*)
  - Bash(scripts/process-konflux-prs/get-component-info.sh*)
---

Process the pull requests from Mintmaker and Konflux

# Implementation Requirements

**CRITICAL**: Always query fresh data. Never use PR numbers from previous runs or earlier in the conversation.

When implementing this skill:

1. **Always start fresh**: Use `list-prs.sh` at the beginning of execution to get current PRs
2. **Process only discovered PRs**: Only process PR numbers returned from `list-prs.sh`
3. **Never hardcode PR numbers**: Do not use PR numbers from memory, previous runs, or earlier conversation turns
4. **Verify state before processing**: Use `get-pr-status.sh` to verify PR state before taking action

Example correct workflow:
```bash
# CORRECT: Discover PRs dynamically
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
./list-prs.sh [--mintmaker|--nudge] [--repo REPO_NAME]
```
Output: JSON array with {number, title, url, repo, type}

## get-pr-status.sh
Get PR status including checks and labels.
```bash
./get-pr-status.sh --repo REPO_NAME --pr PR_NUMBER
```
Output: JSON with {number, title, state, labels, checks, has_ok_to_test, has_lgtm, all_checks_passed, pending_checks}

## get-pr-components.sh
Identify components that will be rebuilt (excludes enterprise-contract validation checks).
```bash
./get-pr-components.sh --repo REPO_NAME --pr PR_NUMBER
```
Output: JSON array of component names

## label-pr.sh
Add a label to a PR.
```bash
./label-pr.sh --repo REPO_NAME --pr PR_NUMBER --label LABEL_NAME
```
Labels: "ok-to-test" or "lgtm"
Output: JSON with {success, repo, pr, label}

## check-pipeline-status.sh
Check Konflux pipeline status.
```bash
# For on-push (default)
./check-pipeline-status.sh --component COMPONENT_NAME

# For on-pull-request (--pr-id REQUIRED)
./check-pipeline-status.sh --component COMPONENT_NAME --type on-pull-request --pr-id PR_ID
```
Output: JSON with {component, pipeline_type, latest, all_runs}
Note: --pr-id is mandatory for on-pull-request to avoid matching runs from different PRs

## get-nudge-prs.sh
Find nudge PRs for a component.
```bash
./get-nudge-prs.sh --component COMPONENT_NAME
```
Output: JSON array of nudge PRs

## get-component-info.sh
Get component metadata.
```bash
./get-component-info.sh [--component COMPONENT_NAME]
```
Output: JSON with {name, repository, build_time_minutes, nudges}

# Usage

Without any parameter, the command will list the PRs from the different repositories,
and state for each PR which images will be rebuilt if the PR is merged. It will
also provide the suggested action for each PR.
See [Status output format](#status-output-format) for details.

With the --label parameter, the command will only perform the "labelling" steps,
and not merge any PR.

With the --dry-run parameter, the command will show the commands it would execute,
but will not label nor merge any PR.

At this point, this skill is considered to be "in development" and should NEVER
merge any PR.

# Workflow definition

We receive two types of pull requests from Konflux:

- Mintmaker PRs: update of dependencies
- Nudge PRs: updated reference to a newly built image from our own pipelines

The first type comes on a regular basis from Mintmaker.
The other comes from merging pull requests: each PR we merge will generate the
build of (at least) one image. When an image is rebuilt, its reference is updated
on some other component(s) automatically, with a nudge PR.
See the [list of components](#list-of-components) for our nudge relationship.

The workflow requires to process Mintmaker PRs first, then Nudge PRs.
The processing of each PR is done in two steps: labelling, and merging.

## Labelling

Using labels allows to mark the progression of PRs towards merging, making it
possible to run the command multiple time, and continue the processing without
having to keep a separate status.
It also allows developers to mark the PRs themselves, and have this automated
process take their labels into account.

We're using two labels:
- `ok-to-test` is used to make our automated tests run on the PR.
- `lgtm` is used to mark the PR as ready to merge.

The rule typically is:

- if a PR don't have "ok-to-test", set the "ok-to-test" label
- if a PR has "ok-to-test" AND all the checks are passing, set the "lgtm" label

Labelling can be done in parallel for all PRs from all repositories, as there
is no conflict with "on-pull-request" pipelines running in parallel.

Note that Nudge PRs in the osc repository are configured to auto-merge when
the tests are passing. Setting the "ok-to-test" label on those PRs is then the
only required labelling. But it means we have to look for the merge pipeline
when the PR auto-merges, and make sure it succeeds (see [merging](#merging)).

## Merging

PRs are ready to merge when they have the label "lgtm" and all the checks are
passing.

We need to be careful when merging a PR: each time we merge a PR, we need to verify
that the on-push pipelines (triggered when we merge) are successful before merging
another PR that rebuilds the same component.

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
We then let the Nudge PRs opened as long as we are processing Mintmaker PRs, and
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
- title starts with one of the following:
  - "chore(deps): update konflux references"
  - "chore(deps): update registry.access.redhat.com/ubi9/ubi"
  - "chore(deps): update registry.access.redhat.com/ubi9/go-toolset"
  - "chore(deps): update registry.access.redhat.com/ubi10/ubi"
  - "chore(deps): update registry.access.redhat.com/ubi10/go-toolset"

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
