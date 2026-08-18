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

| Field | Values | Default |
|-------|--------|---------|
| `runtimeClassName` | `kata`, `kata-remote` | `kata` |
| `enablePeerPods` | `true`, `false` | `false` |
| `workloadToTest` | `kata`, `peer-pods`, `coco` | `kata` |
| `workloadImage` | container image URL | `quay.io/openshift/origin-hello-openshift` |

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
| 2 | Major | `kata_test.go` | Default `workloadImage` pulls from `quay.io` — fails in disconnected/air-gapped clusters. Should use an image available via the cluster's internal registry or accept an override via `osc-config`. | 3 |

### Future refactoring

| # | Priority | Description | SP |
|---|----------|-------------|----|
| 3 | High | Embed `SubscriptionDescription` and `KataconfigDescription` into `TestRunDescription` to reduce parameter passing and simplify lifecycle management. | 5 |
| 4 | Medium | Replace `oc` CLI calls with typed client-go operations for resource queries (pods, deployments, configmaps). Reduces flakiness from shell parsing and improves error handling. | 8 |
| 5 | Medium | Add `WORKLOAD_IMAGE` field to `osc-config` configmap so disconnected environments can override the test image without code changes. | 2 |
