# Process Konflux PRs - Helper Scripts

This directory contains independent scripts that provide base functionalities for managing Konflux PRs. These scripts are designed to be called by the `process-konflux-prs` skill, but can also be used standalone.

## Scripts Overview

### 1. list-prs.sh
List Konflux PRs from all repositories.

**Usage:**
```bash
./list-prs.sh [--mintmaker|--nudge] [--repo REPO_NAME]
```

**Options:**
- `--mintmaker`: Filter for Mintmaker PRs only
- `--nudge`: Filter for Nudge PRs only
- `--repo REPO_NAME`: Filter for a specific repository

**Output:** JSON array of PRs with number, title, url, repo, and type.

**Example:**
```bash
# List all Konflux PRs
./list-prs.sh

# List only Mintmaker PRs
./list-prs.sh --mintmaker

# List Nudge PRs from the osc repository
./list-prs.sh --nudge --repo osc
```

### 2. get-pr-status.sh
Get the status of a specific PR including checks, labels, and merge state.

**Usage:**
```bash
./get-pr-status.sh --repo REPO_NAME --pr PR_NUMBER
```

**Options:**
- `--repo REPO_NAME`: Repository name (kata-containers, cloud-api-adaptor, compute-artifacts, podvm-scripts, osc)
- `--pr PR_NUMBER`: Pull request number

**Output:** JSON with PR details including:
- `number`, `title`, `state`
- `labels`: Array of label names
- `checks`: Array of status checks with name, status, conclusion, details_url
- `has_ok_to_test`: Boolean indicating if "ok-to-test" label is present
- `has_lgtm`: Boolean indicating if "lgtm" label is present
- `all_checks_passed`: Boolean indicating if all checks have passed
- `pending_checks`: Count of checks still in progress

**Example:**
```bash
./get-pr-status.sh --repo osc --pr 2147
```

### 3. get-pr-components.sh
Identify which components will be rebuilt by a PR based on its Konflux checks.

**Usage:**
```bash
./get-pr-components.sh --repo REPO_NAME --pr PR_NUMBER
```

**Options:**
- `--repo REPO_NAME`: Repository name
- `--pr PR_NUMBER`: Pull request number

**Output:** JSON array of component names that will be rebuilt.

**Note:** This script filters out enterprise-contract validation checks, which are not component builds. It only returns actual component builds that match the pattern "{component-name}-on-pull-request".

**Example:**
```bash
./get-pr-components.sh --repo cloud-api-adaptor --pr 1234
# Output: ["osc-caa", "osc-caa-webhook"]
```

### 4. label-pr.sh
Add a label to a PR.

**Usage:**
```bash
./label-pr.sh --repo REPO_NAME --pr PR_NUMBER --label LABEL_NAME
```

**Options:**
- `--repo REPO_NAME`: Repository name
- `--pr PR_NUMBER`: Pull request number
- `--label LABEL_NAME`: Label to add (must be "ok-to-test" or "lgtm")

**Output:** JSON with success status.

**Example:**
```bash
./label-pr.sh --repo osc --pr 2147 --label ok-to-test
```

### 5. check-pipeline-status.sh
Check the status of Konflux pipelines for a component.

**Usage:**
```bash
# For on-push pipelines (default)
./check-pipeline-status.sh --component COMPONENT_NAME [--namespace NAMESPACE]

# For on-pull-request pipelines (--pr-id is REQUIRED)
./check-pipeline-status.sh --component COMPONENT_NAME --type on-pull-request --pr-id PR_ID [--namespace NAMESPACE]
```

**Options:**
- `--component COMPONENT_NAME`: Component name (e.g., osc-operator, osc-caa)
- `--type TYPE`: Pipeline type (on-pull-request or on-push, default: on-push)
- `--pr-id PR_ID`: PR identifier (REQUIRED for on-pull-request, must match the suffix in the pipelinerun name)
- `--namespace NAMESPACE`: Konflux namespace (default: ose-osc-tenant)

**Output:** JSON with pipeline status including latest run and all matching runs.

**Example:**
```bash
# Check on-push pipeline for osc-operator
./check-pipeline-status.sh --component osc-operator

# Check on-pull-request pipeline (--pr-id is mandatory to avoid matching runs from different PRs)
./check-pipeline-status.sh --component osc-caa --type on-pull-request --pr-id abc123
```

### 6. get-nudge-prs.sh
Find nudge PRs for a given component.

**Usage:**
```bash
./get-nudge-prs.sh --component COMPONENT_NAME
```

**Options:**
- `--component COMPONENT_NAME`: Component name (e.g., osc-operator-bundle, osc-dm-verity-image)

**Output:** JSON array of nudge PRs that update the specified component.

**Important:** Nudge PR titles mention the **source** component (what was rebuilt), not the **target** component (what is being updated). This script handles that automatically by:
1. Finding all components that nudge the target component
2. Searching for nudge PRs with titles mentioning those source components

**Example:**
```bash
./get-nudge-prs.sh --component osc-operator-bundle
# Finds PRs with titles like "chore(deps): update osc-operator" (source component)
# which update osc-operator-bundle (target component)
```

### 7. get-component-info.sh
Get metadata about components (build time, dependencies).

**Usage:**
```bash
./get-component-info.sh [--component COMPONENT_NAME]
```

**Options:**
- `--component COMPONENT_NAME`: Get info for a specific component (optional)

**Output:** JSON with component metadata including repository, build time, and nudged components.

**Example:**
```bash
# Get all components info
./get-component-info.sh

# Get specific component info
./get-component-info.sh --component osc-operator
```

## Repository Mapping

The scripts recognize these repository names:
- `kata-containers` → `https://github.com/openshift/kata-containers`
- `cloud-api-adaptor` → `https://github.com/openshift/cloud-api-adaptor`
- `compute-artifacts` → `https://github.com/openshift/confidential-compute-artifacts`
- `podvm-scripts` → `https://github.com/confidential-devhub/coco-podvm-scripts`
- `osc` → `https://github.com/openshift/sandboxed-containers-operator`

## Implementation Notes

- The Konflux bot appears as `app/red-hat-konflux` in GitHub (not just `red-hat-konflux`)
- All PR queries filter by `--author "app/red-hat-konflux"`
- Enterprise contract checks are excluded from component detection as they are validation checks, not component builds
- Only checks matching the pattern "Red Hat Konflux / {component-name}-on-pull-request" are considered component builds

## Prerequisites

All scripts require:
- `gh` (GitHub CLI) - authenticated and configured
- `jq` - for JSON processing

Scripts that interact with Konflux also require:
- `oc` (OpenShift CLI) - logged into Konflux cluster
- `tkn` (Tekton CLI) - for pipeline operations

## Environment Variables

- `GITHUB_TOKEN`: GitHub personal access token (can be set via `gh auth login`)
- Konflux access: Configure via `oc login` with appropriate service account token

## Integration with process-konflux-prs Skill

These scripts are designed to be called by the `process-konflux-prs` skill.
The skill's allowed-tools section should be updated to reference these scripts
instead of individual `gh`, `oc`, and `tkn` commands.

This provides:
1. **Better isolation**: The skill can only execute these specific scripts
2. **Easier testing**: Each script can be tested independently
3. **Better maintainability**: Script logic is separated from skill orchestration
4. **Consistent output**: All scripts output JSON for easy parsing

## Testing

Test each script individually before using them in the skill:

```bash
# Test listing PRs
./list-prs.sh --mintmaker

# Test getting PR status (replace with actual PR number)
./get-pr-status.sh --repo osc --pr 2147

# Test component info
./get-component-info.sh
```
