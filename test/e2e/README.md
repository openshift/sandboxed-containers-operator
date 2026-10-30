# Kata E2E Test Suite (PoC)

End-to-end test tooling for OpenShift Sandboxed Containers. Everything here
runs directly against an OpenShift cluster with the OSC operator installed —
no provisioning is performed by this directory itself:

- **OSC suite** (this Go package) — Ginkgo functional tests for kata,
  peer-pods and confidential containers workloads.
- **[`sanity/`](sanity/README.md)** — a bats smoke check per workload type,
  meant to gate the heavier suites below.
- **`run_upstream_tests.sh`** — runs the upstream kata-containers BATS suite.
- **`run_caa_tests.sh`** — runs the upstream cloud-api-adaptor (CAA) e2e Go
  tests.

## OSC Suite

Standalone Ginkgo-based end-to-end test suite. Migrated from
`openshift-tests-private` to run independently against any OpenShift cluster
with the OSC operator installed.

### Quick Start

```bash
cd test/e2e
export KUBECONFIG=~/path/to/kubeconfig

# Run all tests
go test -v -timeout 120m ./...

# Run a specific test by ID
go test -v -timeout 30m -ginkgo.focus="C00367" ./...
```

### Configuration

Tests read the `osc-config` configmap in the `default` namespace:

| Field | Values | Default | Required |
|-------|--------|---------|----------|
| `runtimeClassName` | `kata`, `kata-remote` | `kata` | yes |
| `enablePeerPods` | `true`, `false` | `false` | yes |
| `workloadToTest` | `kata`, `peer-pods`, `coco` | `kata` | yes |
| `workloadImage` | container image URL | `ghcr.io/nginxinc/nginx-unprivileged:alpine@sha256:e234d4f7e346c098f7b22245690372382aded5be988611eb2c53ae62231eece7` | yes |
| `workloadMessage` | text the image serves over HTTP | `Welcome to nginx!` | yes |
| `enableGPU` | `true`, `false` | `false` | no |
| `operatorVer` | any string | (unset) | no |

The defaults above apply when the configmap does not exist. If it *does* exist,
every field marked required must be present or the suite fails during setup.
`operatorVer` is accepted but not currently read by any test.

`workloadImage` and `workloadMessage` go together: `workloadMessage` is the string
`C00100` expects in the HTTP response body, so set both when changing the image.
The workload image must serve HTTP on port 8080 and run as a non-root user
(the pod and deployment templates set `runAsNonRoot: true`).

> **Note:** `quay.io/openshift/origin-hello-openshift` (message `Hello OpenShift!`)
> cannot currently be used. Two of its layers contain empty
> `SCHILY.xattr.user.overlay.origin` PAX records, which `astral-tokio-tar` >= 0.6.3
> rejects, so image-rs fails to pull it with "Failed to read entries from the tar".

## Sanity Suite

A bats-based smoke check per workload type (kata, peer-pods, coco), meant to
gate the heavier suites: if a plain pod does not run, there is no point
reading the output of hundreds of other tests. See
[`sanity/README.md`](sanity/README.md) for details.

```bash
cd test/e2e
export KUBECONFIG=~/path/to/kubeconfig

./run_sanity_tests.sh --workload kata --runtime_class kata
```

Options and environment variables are documented in
`./run_sanity_tests.sh --help`.

## Upstream kata-containers Tests

Runs the upstream kata-containers BATS suite (cloned from
`openshift/kata-containers`) against the cluster. By default it also performs
cluster setup (node labeling, namespace, SCC) before running tests.

```bash
cd test/e2e
export KUBECONFIG=~/path/to/kubeconfig

./run_upstream_tests.sh --test sanity
```

Options and environment variables are documented in
`./run_upstream_tests.sh --help`.

## Upstream cloud-api-adaptor (CAA) Tests

Runs the upstream cloud-api-adaptor e2e Go tests (cloned from
`openshift/cloud-api-adaptor`) against a cluster that already has OSC
peer-pods/CAA installed. Provisioning, CAA install and teardown are disabled.

```bash
cd test/e2e
export KUBECONFIG=~/path/to/kubeconfig

./run_caa_tests.sh --provider azure
```

Options and environment variables are documented in
`./run_caa_tests.sh --help`.
