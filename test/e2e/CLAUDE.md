# Kata E2E Test Suite

Standalone Ginkgo-based e2e tests for OpenShift Sandboxed Containers.
Uses a self-contained `oc` CLI wrapper (`CLI` type in `oc_client.go`)
instead of the origin `exutil` framework, to avoid pulling in ~7000
unrelated k8s/origin test registrations.

## oc CLI Namespace Rules

The `CLI` wrapper (`oc`) auto-injects `--namespace=<test-namespace>`
into every command via `Run()`. This is the per-test namespace (e.g.
`e2e-kata-xxxxx`), NOT the namespace you want to target.

**When passing `-n <namespace>` explicitly, you MUST use `WithoutNamespace()`**
to prevent double namespace injection. Without it, the command gets two `-n` flags
and works only by accident (last flag wins).

```go
// WRONG — gets --namespace=e2e-kata-xxxxx AND -n deploy.namespace
oc.AsAdmin().Run("get").Args("pods", "-n", deploy.namespace).Output()

// CORRECT — only -n deploy.namespace
oc.AsAdmin().WithoutNamespace().Run("get").Args("pods", "-n", deploy.namespace).Output()

// CORRECT — uses the test namespace (no explicit -n needed)
oc.AsAdmin().Run("get").Args("pods").Output()
```

| You want to target... | Pattern |
|---|---|
| Test namespace (`e2e-kata-xxxxx`) | `oc.Run("get").Args("pods")` |
| Specific namespace (`default`, operator ns) | `oc.WithoutNamespace().Run("get").Args("pods", "-n", ns)` |
| Cluster-scoped resource (nodes, infrastructure) | `oc.WithoutNamespace().Run("get").Args("nodes")` |

## Build and Lint

```bash
cd test/e2e
go build ./...
go vet ./...
golangci-lint run ./...
```

## Coding Conventions

- Use `wait.PollUntilContextTimeout` — `wait.Poll` and `wait.PollImmediate` are deprecated
- Never discard errors with `_ = err` — log or propagate
- Use `ginkgo.Serial` decorator, not just `[Serial]` in test name text
- Add language identifiers to markdown fenced code blocks (MD040)
