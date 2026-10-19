# Kata E2E Test Suite (PoC)

Standalone Ginkgo-based end-to-end test suite for OpenShift Sandboxed
Containers. Migrated from `openshift-tests-private` to run independently
against any OpenShift cluster with the OSC operator installed.

## Quick Start

```bash
cd test/e2e
export KUBECONFIG=~/path/to/kubeconfig

# Run all tests
go test -v -timeout 120m ./...

# Run a specific test by ID
go test -v -timeout 30m -ginkgo.focus="66108" ./...
```

## Configuration

Tests read the `osc-config` configmap in the `default` namespace:

| Field | Values | Default | Required |
|-------|--------|---------|----------|
| `runtimeClassName` | `kata`, `kata-remote` | `kata` | yes |
| `enablePeerPods` | `true`, `false` | `false` | yes |
| `workloadToTest` | `kata`, `peer-pods`, `coco` | `kata` | yes |
| `workloadImage` | container image URL | `ghcr.io/nginxinc/nginx-unprivileged:alpine@sha256:e234d4f7e346c098f7b22245690372382aded5be988611eb2c53ae62231eece7` | yes |
| `workloadMessage` | text the image serves over HTTP | `Welcome to nginx!` | yes |
| `enableGPU` | `true`, `false` | `false` | no |

The defaults above apply when the configmap does not exist. If it *does* exist,
every field marked required must be present or the suite fails during setup.

`workloadImage` and `workloadMessage` go together: `workloadMessage` is the string
`C00100` expects in the HTTP response body, so set both when changing the image.
The workload image must serve HTTP on port 8080 and run as a non-root user
(the pod and deployment templates set `runAsNonRoot: true`).

> **Note:** `quay.io/openshift/origin-hello-openshift` (message `Hello OpenShift!`)
> cannot currently be used. Two of its layers contain empty
> `SCHILY.xattr.user.overlay.origin` PAX records, which `astral-tokio-tar` >= 0.6.3
> rejects, so image-rs fails to pull it with "Failed to read entries from the tar".

## Roadmap

Known issues and improvements deferred from the PoC, tracked by severity
and estimated complexity (story points: 1=trivial, 2=small, 3=medium,
5=significant, 8=large).

### Test coverage

| # | Severity | File | Issue | SP |
|---|----------|------|-------|----|
| 1 | Major | `kata_test.go` | Sidecar test (C00192) only checks both containers are running, does not verify log content flows through the shared volume. `tail -F` retries silently even if the path is wrong. | 2 |

### Environment compatibility

| # | Severity | File | Issue | SP |
|---|----------|------|-------|----|
| 2 | Major | `kata_test.go` | Default `workloadImage` pulls from an external registry — fails in disconnected/air-gapped clusters. Should use an image available via the cluster's internal registry or accept an override via `osc-config`. | 3 |

### Future refactoring

| # | Priority | Description | SP |
|---|----------|-------------|----|
| 3 | High | Embed `SubscriptionDescription` and `KataconfigDescription` into `TestRunDescription` to reduce parameter passing and simplify lifecycle management. | 5 |
| 4 | Medium | Replace `oc` CLI calls with typed client-go operations for resource queries (pods, deployments, configmaps). Reduces flakiness from shell parsing and improves error handling. | 8 |
| 5 | Medium | Revert `workloadImage`/`workloadMessage` to `quay.io/openshift/origin-hello-openshift` / `Hello OpenShift!` once image-rs ships a tar reader that accepts empty PAX extension values. | 1 |
