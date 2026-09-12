# OpenShift Sandboxed Containers Operator — Project Guide

## General Guidelines for AI Assistants

- **Do not stack unrelated code changes.** If you are working on feature A,
  avoid introducing unrelated changes into the same edits. Add ideas to a
  separate commit or a todo list instead.
- **Keep commits focused and atomic.** Each commit should represent a single
  logical change.
- **Every individual commit must be buildable** for the sake of bisectability.
  Do not leave the tree in a broken state between commits.
- **No narrating comments.** Comments should explain non-obvious intent, not
  describe what the code does.

## Commit Message Format

All commits must follow the conventions adopted by this project. Validate
before pushing:

```bash
./hack/commit-msg-check.sh origin/devel HEAD
```

### Requirements

Each commit message must:

1. **Start with a subsystem prefix** followed by a colon (e.g. `ci:`, `docs:`,
   `feat:`, `fix:`)
2. **Have a subject line** no longer than **80 characters**
3. **Include a body** that describes the changes in detail
4. **Body lines** no longer than **150 characters** (lines starting with
   non-alphabetic characters — indented stack traces, code snippets, URLs — are
   exempt)
5. **Include a `Signed-off-by` tag** (use `git commit -s`)

### Format

```
<subsystem>: <short summary (max 80 chars)>

<Detailed description of the change. Explain what and why, not how.
Body lines should be wrapped at 150 characters.>

Fixes: rhjira#KATA-XXXX

Signed-off-by: Your Name <your.email@example.com>
```

If your commit fixes or implements a JIRA issue, include
`Fixes: rhjira#KATA-XXXX` in the body (replace XXXX with the actual issue
number).

### Common Subsystem Prefixes

- `feat:` — New features
- `fix:` — Bug fixes
- `docs:` — Documentation changes
- `test:` — Test-related changes
- `ci:` — CI/CD pipeline changes
- `build:` — Build system or dependency changes
- `refactor:` — Code refactoring without functional changes
- `perf:` — Performance improvements
- `chore:` — Maintenance tasks (dependency updates, etc.)
- `api:` — API changes (KataConfig CRD, etc.)
- `controller:` — Controller logic changes
- `webhook:` — Webhook-related changes
- `peerpods:` — Peer-pods specific changes
- `monitor:` — Monitor component changes

### Examples

Good:

```
feat: add support for custom kernel parameters in KataConfig

This change allows users to specify additional kernel parameters
through the KataConfig CR. The parameters are passed to the kata
runtime and applied when launching workloads.

Fixes: rhjira#KATA-1234

Signed-off-by: Jane Developer <jane@example.com>
```

```
fix: resolve memory leak in monitor container

The monitor container was not properly releasing resources when
pods were deleted, causing memory usage to grow over time.
Added proper cleanup in the deletion handler.

Signed-off-by: John Developer <john@example.com>
```

Bad:

- `Update code` — missing subsystem prefix, too vague, no body
- `feat: Added a new feature that allows users to configure custom runtime settings which is very useful` — subject exceeds 80 characters
- `fix: bug fix` — no descriptive body explaining what was fixed
- `Fixed the thing` — missing subsystem prefix, no body, vague

## Development Workflow

The main development branch is `devel`. Create feature branches from it:

```bash
git checkout -b feat/your-feature-name
```

Before pushing, validate commits and run tests:

```bash
./hack/commit-msg-check.sh origin/devel HEAD
make test
make docker-build
```

### Pull Request Process

1. Fork the repository and create your branch from `devel`.
2. Follow the commit message format. Use `git commit -s` for Signed-off-by.
3. Test thoroughly: `make test`, `make docker-build`, and on an OpenShift
   cluster if possible.
4. Update documentation when changing functionality or adding features.
5. Fill out the PR template with: problem description, what you did, how to
   verify, and changelog description.
6. Ensure CI passes (tests, commit message validation, no merge conflicts).
7. Each change requires at least two approvals from reviewers before merging.
   When addressing review feedback, amend existing commits and force-push to
   keep history clean.

## Project Structure

```
api/v1/             KataConfig CRD types, validation webhook, DeepCopy
controllers/        Reconciliation logic, pod mutator, peer-pods, feature gates
cmd/manager/        Operator entrypoint (wires controllers and webhooks)
cmd/metrics/        Prometheus metrics server
config/             Kustomize manifests (CRDs, RBAC, webhook, manager, etc.)
bundle/             OLM bundle manifests and metadata
docs/               Documentation
hack/               Boilerplate and build helpers
scripts/            Install helpers, webhook tests, prow job analyzer
tests/              Tekton pipelines for CI
fbc/                File-based catalog definitions (v4.17–v4.21)
.tekton/            Tekton pipelines for Konflux CI
```

## Build System

The project uses a `Makefile` with the following key targets:

| Target | Description |
|--------|-------------|
| `make build` | Build `manager` and `metrics-server` binaries (CGO_ENABLED=1) |
| `make manifests` | Generate CRDs, RBAC, and webhook configs via controller-gen |
| `make generate` | Generate DeepCopy methods via controller-gen |
| `make fmt` | Run `go fmt ./...` |
| `make vet` | Run `go vet ./...` |
| `make test` | Run unit tests (Ginkgo/envtest, excludes e2e) |
| `make lint` | Run golangci-lint |
| `make lint-fix` | Run golangci-lint with `--fix` |
| `make docker-build` | Build operator container image |
| `make docker-push` | Push operator container image |
| `make bundle` | Generate OLM bundle |
| `make install` | Install CRDs into cluster via Kustomize |
| `make deploy` | Deploy controller to cluster via Kustomize |
| `make help` | List all available targets |

Build tags for cloud providers are derived from `BUILTIN_CLOUD_PROVIDERS`
(default: `aws azure`).

## Code Generation

After modifying API types (`api/v1/kataconfig_types.go`), kubebuilder markers,
or RBAC annotations:

```bash
make manifests generate
```

**Never edit generated files directly.** The following are generated:

- `api/v1/zz_generated.deepcopy.go`
- `config/crd/bases/kataconfiguration.openshift.io_kataconfigs.yaml`
- `config/webhook/manifests.yaml`

Kubebuilder markers live in:
- `api/v1/` — CRD validation, defaulting, printcolumns
- `controllers/` — RBAC (`+kubebuilder:rbac`) and webhook path markers
- `cmd/manager/`, `cmd/metrics/` — Additional RBAC markers

## Architecture

This is a **Kubebuilder v4 / Operator SDK** project. The operator manages the
lifecycle of Kata Containers on OpenShift clusters.

### CRD

- **Group**: `kataconfiguration.openshift.io`
- **Version**: `v1`
- **Kind**: `KataConfig`
- **Types**: `api/v1/kataconfig_types.go`

### Controllers

| Controller | File | Purpose |
|------------|------|---------|
| `KataConfigOpenShiftReconciler` | `controllers/openshift_controller.go` | Main reconciler — MachineConfigs, RuntimeClasses, DaemonSets, peer-pods |
| `PodMutator` | `controllers/pod_mutator.go` | Mutating webhook — injects memory overhead annotation for Kata pods |
| `RuntimeClassReconciler` | `controllers/runtimeclass_controller.go` | RuntimeClass finalizer and cleanup |
| `SecretReconciler` | `controllers/credentials_controller.go` | Secret reconciliation for cloud credentials |

### Webhooks

| Webhook | Path | Type |
|---------|------|------|
| KataConfig validation | `/validate-kataconfiguration-openshift-io-v1-kataconfig` | Validating |
| Pod mutation | `/mutate-pods-v1` | Mutating |

### Integration Points

- **MCO** (Machine Config Operator): MachineConfigs, MachineConfigPools,
  ContainerRuntimeConfig for installing Kata on nodes
- **CRI-O**: Container runtime configuration for Kata runtime class
- **PeerPods** (cloud-api-adaptor): Remote VM-based pod execution on cloud
  providers (AWS, Azure)
- **OLM**: Operator Lifecycle Manager for installation and upgrades

### Key Constants

Defined in `controllers/openshift_controller.go`:

- Operator namespace: `openshift-sandboxed-containers-operator`
- Runtime class name: `kata`
- Pod overhead: CPU `0.25`, Memory `350Mi`

## Testing

### Unit Tests

```bash
make test
```

Uses **Ginkgo v2** + **Gomega** with **envtest** for in-cluster simulation.
Test suites:

- `controllers/suite_test.go` — Controller tests with envtest
- `controllers/pod_mutator_test.go` — Pod mutator webhook tests
- `api/v1/kataconfig_webhook_test.go` — KataConfig validation webhook tests

### E2E Tests

E2E tests live in an external repo (`openshift-tests-private`) under
`test/extended/kata/`. They are not part of this repository. Run with:

```bash
make test-e2e
```

### Webhook Tests

Manual webhook test scripts are in `scripts/webhook-tests/`.

## Code Style

- **License header**: All Go source files must include the Apache 2.0 license
  header from `hack/boilerplate.go.txt`.
- **Formatting**: `go fmt` is enforced (`make fmt`).
- **Vetting**: `go vet` is enforced (`make vet`).
- **Linting**: golangci-lint (`make lint`).
- **Comments**: Explain non-obvious intent only. No narrating comments.

## Version Management

Image references tagged with `## OSC_VERSION` are tracked for version bumps.
When starting a new version, use:

```bash
scripts/bump-osc-version.sh
```

This updates all `## OSC_VERSION`-tagged locations, Dockerfile labels, and
the `olm.skipRange` annotation in the CSV.
