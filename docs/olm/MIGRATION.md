# OLM v0 → OLM v1 Migration Strategy

OSC cannot make a hard cutover to OLM v1 because `config.inline.watchNamespace`
(which maps OwnNamespace/SingleNamespace semantics into OLM v1) is backed by the
`SingleOwnNamespaceInstallSupport` feature gate — Alpha upstream, TechPreview-only on OCP.
The migration is therefore phased across operator versions.

## Phase overview

| Phase | OSC version | OLM v0 | OLM v1 | TechPreview required |
|---|---|---|---|---|
| 1 — Parallel, opt-in | 1.13.x | Default, fully supported | Supported via `config/olmv1/` | Yes |
| 2 — AllNamespaces support | 1.14.x (current) | Supported | Supported without TechPreview | No |
| 3 — Full support | TBD | Legacy | Recommended | No |

---

## Phase 1 — Parallel support, TechPreview only (current: v1.13.x)

OLM v0 is the default and fully supported install path. OLM v1 is usable on clusters
with `TechPreviewNoUpgrade` enabled via the `config/olmv1/` manifests in this repo.

**OLM v0 users:** no action required.

**OLM v1 early adopters (TechPreview clusters only):**
```bash
oc apply -f config/olmv1/
```

No in-place migration from OLM v0 → OLM v1 is supported or required in this phase.

---

## Phase 2 — Optional opt-in, no TechPreview required

**Prerequisite (one of):**

**Option A — `SingleOwnNamespaceInstallSupport` graduates to GA upstream**

Track [operator-controller#2268](https://github.com/operator-framework/operator-controller/pull/2268).
Once GA, `config.inline.watchNamespace` works on production OCP without TechPreview.
No CSV changes required; `config/olmv1/` manifests become production-supported.

**Option B — Add `AllNamespaces` install mode support to OSC** ✅ In progress (v1.14.x)

The operator's controllers are already architecturally cluster-scoped — no code changes
are needed. The only change is flipping `AllNamespaces: true` in the CSV installModes.
`config.inline.watchNamespace` is no longer needed in the ClusterExtension.

**OLM v0 users in Phase 2:** no action required; OLM v0 continues to work.

---

## Phase 3 — Full OLM v1 support (TBD)

Phase 3 is still under refinement and depends on how Phase 2 is resolved.

**OCP's OLM v0 → OLM v1 migration tool (OCPSTRAT-2692)**

OCP is building a CLI migration tool for bulk OLM v0 → OLM v1 migration. It only supports
operators with `AllNamespaces` install mode. OSC is currently ineligible:

> "OwnNamespace and SingleNamespace install modes are out of scope for the migration tool."

**Impact of Phase 2 choice on Phase 3:**

| Phase 2 path | Phase 3 migration tool eligibility |
|---|---|
| Option A — `SingleOwnNamespaceInstallSupport` GA | OSC remains ineligible; manual migration needed |
| Option B — Add `AllNamespaces` support to OSC | OSC becomes eligible; platform migration tool applies |

If Option B is chosen in Phase 2, the platform migration tool (OCPSTRAT-2692) can handle
the OLM v0 → OLM v1 transition for existing installs automatically.

This section will be updated once Phase 2 direction and OCPSTRAT-2692 are finalized.
