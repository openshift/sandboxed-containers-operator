# Installing OSC via OLM v1

This guide covers installing the OpenShift Sandboxed Containers operator using OLM v1
(`ClusterExtension`) on OCP 4.22+.

> **Note:** OSC 1.14.x+ supports `AllNamespaces` install mode, enabling OLM v1 without requiring
> `TechPreviewNoUpgrade`. For OSC < 1.14.x, see the note below about the `config.inline.watchNamespace`
> workaround.

## Prerequisites

- OCP 4.22+ cluster
- `oc` CLI with cluster-admin access

## Install

Install using the [OSC Helm chart](https://github.com/confidential-devhub/charts/tree/main/charts/osc-operator):

### Add the Helm repo (or clone it)

```bash
git clone https://github.com/confidential-devhub/charts.git
cd charts
```

### Install using the GA catalog (redhat-operators)

```bash
helm install osc-operator charts/osc-operator \
  --set dev.enabled=false \
  --set olmv1.enabled=true
```

Note: Only supported on OSC v1.14.0+

### Or install using a custom dev catalog image

```bash
helm install osc-operator charts/osc-operator \
  --set dev.enabled=true \
  --set dev.image=quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:latest \
  --set olmv1.enabled=true
```

### Verify the operator is running

```bash
oc get clusterextension sandboxed-containers
oc get pods -n openshift-sandboxed-containers-operator
```

This creates:

- `openshift-sandboxed-containers-operator` namespace
- `sandboxed-containers-installer` ServiceAccount with required RBAC
- `sandboxed-containers` ClusterExtension targeting the `stable` channel

> **Note for OSC < 1.14.x:** If you are installing an older version that only supports
> `OwnNamespace`/`SingleNamespace` modes, you need to set `config.inline.watchNamespace`
> on the ClusterExtension. This requires the `TechPreviewNoUpgrade` feature gate to be
> enabled on your cluster. See [MIGRATION.md](MIGRATION.md) Phase 1 for details.

## Create a KataConfig

Once the operator is installed, create a KataConfig CR to enable the kata runtime:

```bash
oc apply -f - <<EOF
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: example-kataconfig
spec:
  enablePeerPods: false
EOF
```

Monitor installation progress:

```bash
oc get kataconfig example-kataconfig -o jsonpath='{.status}'
```

## Uninstall

```bash
helm uninstall osc-operator
oc delete namespace openshift-sandboxed-containers-operator
```

If operands (KataConfig etc.) were installed separately, delete them first before uninstalling:

```bash
oc delete kataconfig --all
helm uninstall osc-operator
oc delete namespace openshift-sandboxed-containers-operator
```
## Further reading

- [MIGRATION.md](MIGRATION.md) — Phased migration strategy from OLM v0 to OLM v1
