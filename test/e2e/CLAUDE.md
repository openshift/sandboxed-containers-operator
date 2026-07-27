# Kata E2E Test Suite

Standalone Ginkgo-based e2e tests for OpenShift Sandboxed Containers,
migrated from `openshift-tests-private`.

## oc CLI Namespace Rules

The `exutil.CLI` wrapper (`oc`) auto-injects `--namespace=<framework-namespace>`
into every command via `Run()`. This is the framework's test namespace (e.g.
`e2e-kata-xxxxx`), NOT the namespace you want to target.

**When passing `-n <namespace>` explicitly, you MUST use `WithoutNamespace()`**
to prevent double namespace injection. Without it, the command gets two `-n` flags
and works only by accident (last flag wins).

```go
// WRONG — gets --namespace=e2e-kata-xxxxx AND -n deploy.namespace
oc.AsAdmin().Run("get").Args("pods", "-n", deploy.namespace).Output()

// CORRECT — only -n deploy.namespace
oc.AsAdmin().WithoutNamespace().Run("get").Args("pods", "-n", deploy.namespace).Output()

// CORRECT — uses the framework's test namespace (no explicit -n needed)
oc.AsAdmin().Run("get").Args("pods").Output()
```

| You want to target... | Pattern |
|---|---|
| Framework test namespace (`e2e-kata-xxxxx`) | `oc.Run("get").Args("pods")` |
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
