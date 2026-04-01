---
description: Open a PR on openshift/release to rehearse OSC Prow CI jobs with a custom catalog and optional kata RPM
argument-hint: "<catalog-image> <ocp-versions> <job-name> [kata-rpm: yes|no]"
---

Rehearse OSC Prow CI jobs via a PR on openshift/release using the pj-rehearse workflow.

Follow the instructions in the **Rehearse Jobs** skill (`skills/rehearse-jobs/SKILL.md`) with:
- **Catalog image**: `$1`
- **OCP versions**: `$2` (comma-separated, e.g. `4.18,4.20,4.21`)
- **Job name**: `$3` (e.g. `azure-ipi-peerpods`)
- **Kata RPM**: `$4` (optional, `yes` or `no`, default: ask the user)
