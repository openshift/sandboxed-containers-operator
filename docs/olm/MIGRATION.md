# OLM v0 → OLM v1 Migration Strategy

OSC cannot make a hard cutover to OLM v1 because `config.inline.watchNamespace`
(which maps OwnNamespace/SingleNamespace semantics into OLM v1) is backed by the
`SingleOwnNamespaceInstallSupport` feature gate — Alpha upstream, TechPreview-only on OCP.
The migration is therefore phased across operator versions.

## Phase overview

| Phase | OSC version | OLM v0 | OLM v1 | TechPreview required |
|---|---|---|---|---|
| 1 — Parallel, opt-in | 1.13.x | Default, fully supported | Supported via [charts](https://github.com/confidential-devhub/charts/tree/main/charts/osc-operator) | Yes |
| 2 — AllNamespaces support | 1.14.x (current) | Supported | Supported without TechPreview | No |
| 3 — Full support | TBD | Legacy | Recommended | No |

---

## Phase 1 — Parallel support, TechPreview only (v1.13.x)

OLM v0 is the default and fully supported install path. OLM v1 is usable on clusters
with `TechPreviewNoUpgrade` enabled via the [OSC Helm chart](https://github.com/confidential-devhub/charts/tree/main/charts/osc-operator).

**OLM v0 users:** no action required.

**OLM v1 early adopters (TechPreview clusters only):**

```bash
git clone https://github.com/confidential-devhub/charts.git
cd charts

helm install osc-operator charts/osc-operator \
  --set olmv1.enabled=true \
  --set dev.image=quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:latest
```

OSC 1.13.x does not support `AllNamespaces` install mode, so the ClusterExtension must
include `config.inline.watchNamespace` (requires `TechPreviewNoUpgrade`):

```bash
oc patch clusterextension sandboxed-containers --type=merge -p '
  {"spec":{"config":{"inline":{"watchNamespace":"openshift-sandboxed-containers-operator"}}}}'
```

No in-place migration from OLM v0 → OLM v1 is supported or required in this phase.

---

## Phase 2 — Optional opt-in, no TechPreview required (current: v1.14.x)

**Prerequisite (one of):**

**Option A — `SingleOwnNamespaceInstallSupport` graduates to GA upstream**

Track [operator-controller#2268](https://github.com/operator-framework/operator-controller/pull/2268).
Once GA, `config.inline.watchNamespace` works on production OCP without TechPreview.
No CSV changes required.

**Option B — Add `AllNamespaces` install mode support to OSC** ✅ Done (v1.14.x)

The operator's controllers are already architecturally cluster-scoped — no code changes
were needed. The CSV installModes now include `AllNamespaces: true`.
`config.inline.watchNamespace` is no longer needed in the ClusterExtension.

**OLM v0 users in Phase 2:** no action required; OLM v0 continues to work.

---

## Phase 3 — Full OLM v1 support (TBD)

Phase 3 is still under refinement and depends on how Phase 2 is resolved.

**OCP's OLM v0 → OLM v1 migration tool (OCPSTRAT-2692)**

OCP is building a CLI migration tool for bulk OLM v0 → OLM v1 migration. It only supports
operators with `AllNamespaces` install mode. With `AllNamespaces` support added in v1.14.x,
OSC is now eligible for this migration tool.

Once OCPSTRAT-2692 is finalized, the platform migration tool can handle the OLM v0 → OLM v1
transition for existing OSC installs automatically.
