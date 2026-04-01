---
name: Rehearse Jobs
description: |
  Open a PR on openshift/release to rehearse OSC Prow CI jobs with a custom catalog image
  and optionally a specific kata RPM version. Uses the pj-rehearse workflow.

  Triggers: "rehearse jobs", "rehearse prow jobs", "run prow jobs", "test catalog image",
  "pj-rehearse", "trigger ci jobs", "run ci jobs"
---

# Rehearse Jobs Skill

Open a PR on openshift/release to trigger pj-rehearse for a subset of OSC Prow CI jobs
with a custom catalog image and optionally a specific kata RPM version.

## Prerequisites

- **gh** (GitHub CLI): Must be installed and authenticated (`gh auth status`).
  If not available, ask the user to install it (https://cli.github.com/) and run `gh auth login`.

## Required Input

Ask the user for the following if not provided:

- **Catalog image**: The catalog image to test (e.g. from `/build-catalog`)
- **OCP version(s)**: Which versions to target (4.18, 4.19, 4.20, 4.21)
- **Job(s) to run**: Which jobs from the candidate config to modify. Available jobs
  vary by version but typically include:
  - `azure-ipi-kata` (bare metal kata)
  - `azure-ipi-peerpods` (peer pods on Azure)
  - `azure-ipi-coco` (confidential containers on Azure)
  - `aro-ipi-peerpods` (peer pods on ARO)
  - `aro-ipi-coco` (confidential containers on ARO)
  - `aws-ipi-peerpods` (peer pods on AWS)
  - `aws-ipi-coco` (confidential containers on AWS)
- **Kata RPM**: Whether to install a custom kata RPM. If yes, help find the version
  (see Step 3). If no, leave `INSTALL_KATA_RPM=false`.
- **CoCo parameters** (only if any selected job is a `coco` job):
  - `INITDATA`: The initdata value
  - `TRUSTEE_URL`: The trustee URL

## Workflow

### Step 1: Clone openshift/release

Clone the repo to a temporary directory:

```bash
RELEASE_DIR=$(mktemp -d -t openshift-release-XXXXXX)
git clone --depth=1 https://github.com/openshift/release.git "$RELEASE_DIR"
```

### Step 2: Fork and create a branch

Check if the user already has a fork of openshift/release. If not, create one named
`openshift-release`:

```bash
cd "$RELEASE_DIR"
GH_USER=$(gh api user -q .login)
FORK_NAME=$(gh api repos/openshift/release/forks --paginate \
  -q ".[] | select(.owner.login == \"$GH_USER\") | .name" | head -1)
if [ -n "$FORK_NAME" ]; then
  git remote rename origin upstream
  git remote add origin "https://github.com/$GH_USER/$FORK_NAME.git"
else
  gh repo fork --remote --fork-name openshift-release
fi
git fetch upstream
git rebase upstream/main
git checkout -b rehearse-osc-$(date +%Y%m%d-%H%M%S)
```

### Step 3: Find kata RPM versions (if user wants to install one)

Clone Daniel Kreling's auxiliary-scripts repo inside the release directory and run
the `latest_builds_per_package.py` script:

```bash
git clone https://gitlab.cee.redhat.com/dkreling/auxiliary-scripts.git "$RELEASE_DIR/auxiliary-scripts"
python3 "$RELEASE_DIR/auxiliary-scripts/openshift-sandboxed-containers/latest_builds_per_package.py"
```

This requires the `koji` Python module (`pip install koji`) and `krb5-devel` package.
If `koji` is not available, ask the user to install it or provide the RPM version manually.

The script outputs NVRs like `kata-containers-3.25.0-4.rhaos4.19.el9`. Present the list
to the user filtered by their selected OCP version(s) and let them pick.

The `KATA_RPM_VERSION` is the NVR without the `kata-containers-` prefix, e.g. for
`kata-containers-3.25.0-4.rhaos4.19.el9` the version is `3.25.0-4.rhaos4.19.el9`.

If the user does **not** want to install a kata RPM, skip this step entirely.

### Step 4: Edit the candidate configs

The config files are at:
```
$RELEASE_DIR/ci-operator/config/openshift/sandboxed-containers-operator/openshift-sandboxed-containers-operator-devel__downstream-candidate<OCP_VERSION_NO_DOT>.yaml
```

For example, OCP 4.19 -> `downstream-candidate419.yaml`.

For each selected job in the config, update **only** these env vars:
- `CATALOG_SOURCE_IMAGE`: Set to the user's catalog image
- `INSTALL_KATA_RPM`: Set to `"true"` if user chose a kata RPM, leave as `"false"` otherwise
- `KATA_RPM_VERSION`: Set to the selected version (e.g. `3.25.0-4.rhaos4.19.el9`),
  or leave empty if not installing

For **coco jobs only** (job name contains `coco`), also update:
- `INITDATA`: Set to the user-provided initdata value
- `TRUSTEE_URL`: Set to the user-provided trustee URL

Also flip `restrict_network_access` from `false` to `true` on the modified jobs.

**IMPORTANT**: Never change `cron` values. Never change jobs the user didn't select.
Only modify the environment variables listed above and `restrict_network_access`.

### Step 5: Regenerate prow configs

From the release repo root:
```bash
cd "$RELEASE_DIR" && make jobs
```

This regenerates the prow job definitions from the ci-operator configs.

### Step 6: Commit and push

Commit format:
- **Title**: `[DO NOT MERGE] Test catalog <catalog-image>`
- **Body**: List OCP versions, variants, and kata RPM versions (if any) being updated

```bash
cd "$RELEASE_DIR"
git add -A
git commit -m "[DO NOT MERGE] Test catalog <catalog-image>

Updated <job-name> job in the following variants:

- downstream-candidate<ver> (OCP <ver>, kata RPM <rpm-version>)
..."
git push -u origin HEAD
```

### Step 7: Open a PR

```bash
gh pr create --repo openshift/release \
  --title "[DO NOT MERGE] Test catalog <catalog-image>" \
  --body "$(cat <<'EOF'
## Summary

Testing custom OSC catalog image.

**Catalog:** `<catalog-image>`

Updated `<job-name>` job in the following variants:

| Variant | OCP | Kata RPM |
|---------|-----|----------|
| downstream-candidate<ver> | <ver> | `<rpm-version>` |
...

## Next steps
Comment `/pj-rehearse <job-name>` to trigger the modified jobs.

**Do NOT merge this PR.**
EOF
)"
```

### Step 8: Wait for PR checks to pass

Wait for all CI checks to pass before triggering rehearsals. Only `tide` should remain
pending (that is expected). Poll every 2 minutes for up to 20 minutes:

```bash
SECONDS=0
while [ $SECONDS -lt 1200 ]; do
  pending=""
  while IFS=$'\t' read -r name status _; do
    [ "$name" = "tide" ] && continue
    [ "$status" != "pass" ] && pending="$pending  $name: $status\n"
  done < <(gh pr checks <PR_NUMBER> --repo openshift/release 2>&1 | sed 's/\t\+/\t/g')
  if [ -z "$pending" ]; then
    echo "All checks passed!"
    break
  fi
  echo -e "$pending--- Waiting 2 min ---"
  sleep 120
done
```

### Step 9: Trigger pj-rehearse

After the PR is created, a `[REHEARSALNOTIFIER]` comment is posted listing the changed
jobs. Read that comment to get the exact job names.

Send one `/pj-rehearse <full-job-name>` comment per job. Do NOT send a bare `/pj-rehearse`
as that triggers all jobs.

```bash
gh pr comment <PR_NUMBER> --repo openshift/release \
  --body "/pj-rehearse <full-job-name>"
```

Job names follow the pattern:
`periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate<ver>-<job-name>`

### Step 10: Report to user

Tell the user:
1. The PR URL
2. The rehearsal jobs that were triggered
3. That the PR should **not be merged** -- it's only for rehearsal
4. They can monitor job status via the PR's CI checks or the Prow PR dashboard
