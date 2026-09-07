# Network Policies

Network policies for OpenShift Sandboxed Containers (OSC) follow a label-scoped default-deny model. Each operand gets its own set of policies selected by its pod labels. Policies are additive and not meant to be edited by users — the operator will revert any external modifications via `.Owns(&NetworkPolicy{})` drift correction.

## Policy summary

| Component | Selector label | NPs | Ingress | Egress | Status |
|-----------|---------------|-----|---------|--------|--------|
| controller-manager | `control-plane=controller-manager` | 5 | webhook 9443, metrics 8443 | DNS 5353, all (apiserver) | Pending (Phase 2 — OLM bundle) |
| kata-monitor | `name=openshift-sandboxed-containers-monitor` | 3 | metrics 8443 | DNS 5353 | Implemented |
| peer-pods-webhook | `app=peer-pods-webhook` | 5 | webhook 9443, metrics 8443 | DNS 5353, all (apiserver) | Implemented |
| kata-install | `name=osc-rpm-install` | 2 | none | all | Implemented |
| kata-uninstall | `name=osc-rpm-uninstall` | 2 | none | all | Implemented |
| podvm-image-creation | `job-name=osc-podvm-image-creation` | 2 | none | all | Implemented |
| podvm-image-deletion | `job-name=osc-podvm-image-deletion` | 2 | none | all | Implemented |
| cloud-api-adaptor (CAA) | `name=osc-caa-ds` | 0 | — | — | N/A (`hostNetwork=true`) |

**Total: 21 policies** (16 operand + 5 operator)

## Implementation

- **Operand NPs (16):** Created by the operator in `controllers/networkpolicy.go` using `controllerutil.CreateOrUpdate`. Lifecycle follows each operand — created when the operand is deployed, deleted when it is removed. Owner references ensure garbage collection on KataConfig deletion.
- **Operator NPs (5):** Will be shipped as static manifests in the OLM bundle under `config/networkpolicy/` (Phase 2).
- **CAA:** Runs with `hostNetwork: true`, so NetworkPolicy does not apply.

## Key annotations

Ingress policies for webhook and metrics ports carry the `policy-group.network.openshift.io/host-network: ""` annotation. This allows traffic from host-network sources (kube-apiserver, Prometheus) which would otherwise be blocked by OVN-Kubernetes.

## Diagrams

Per-component diagrams are in [network-policies.md](network-policies.md) and as individual `.mmd` files in this directory.
