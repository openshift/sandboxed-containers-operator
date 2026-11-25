# hack/hcp — hypershift-cluster-setup

The OSC team's `hypershift-cluster-setup` script: creates and tears down
HyperShift (HCP) environments — an IPI OpenShift management cluster with
MCE/HyperShift, and hosted clusters on top of it — and installs OpenShift
Sandboxed Containers on a hosted cluster. Used from the laptop for
interactive testing and from Prow for the
`sandboxed-containers-operator-e2e-hcp-aws` workflow.

## Layout

- `bin/hypershift-cluster-setup` — the script itself. `--prow` adapts it to a
  Prow multi-stage test step: non-interactive, state under `${SHARED_DIR}`,
  per-job cluster names from `PROW_JOB_ID`, cluster-profile AWS
  credentials/region/pull secret, and the OSC CI variable vocabulary
  (`COMPUTE_NODE_REPLICAS`, `COMPUTE_NODE_TYPE`, `MGMT_COMPUTE_NODE_TYPE`,
  `BASE_DOMAIN`, `ENABLE_NESTED_VIRT`, `MGMT_INSTALL_MAX_RETRIES`,
  `IMAGE_CONTENT_SOURCES`). The Prow steps consume it via `from: src`.
- `lib/hypershift-cluster-setup-aws.sh` — AWS cloud library.
- `env/management-cluster-permissions-*.yaml` — least-privilege IAM
  permissions for the scoped installer role used by `management create`,
  per OCP version.

## Provenance

The script is developed in `osc/scripts` on GitLab (internal). This copy is
the public publishing location for Prow, which cannot reach GitLab. Re-sync
it whenever the script changes:

    cp ../scripts/bin/hypershift-cluster-setup hack/hcp/bin/
    cp ../scripts/lib/hypershift-cluster-setup-aws.sh hack/hcp/lib/
    cp ../scripts/env/management-cluster-permissions-*.yaml hack/hcp/env/
