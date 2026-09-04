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

Apply all OLM v1 manifests from the repo:

```bash
oc apply -f config/olmv1/
```

This creates:
- `openshift-sandboxed-containers-operator` namespace
- `sandboxed-containers-installer` ServiceAccount with required RBAC
- `sandboxed-containers` ClusterExtension targeting the `stable` channel

> **Note for OSC < 1.14.x:** If you are installing an older version that only supports
> `OwnNamespace`/`SingleNamespace` modes, uncomment the `config.inline.watchNamespace`
> section in `config/olmv1/04-clusterextension.yaml`. This requires the
> `TechPreviewNoUpgrade` feature gate to be enabled on your cluster.

## Verify installation

```bash
oc get clusterextension sandboxed-containers -o jsonpath='{.status.conditions}' | jq .
```

Expected output shows:
- `Installed: True, reason: Succeeded`
- `Available: True, reason: ProbesSucceeded`

## Verify webhook CA

```bash
oc get validatingwebhookconfiguration vkataconfig.kb.io \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | base64 -d | openssl x509 -noout -issuer
```

The CA bundle should be issued by the OpenShift Service CA.

## Create a KataConfig

Once the operator is installed, create a KataConfig CR to enable the kata runtime:

```bash
oc apply -f - <<EOF
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: example-kataconfig
spec:
  enablePeerPods: true
EOF
```

Monitor installation progress:

```bash
oc get kataconfig example-kataconfig -o jsonpath='{.status}'
```

## Uninstall

```bash
oc delete -f config/olmv1/
```

## Further reading

- [MIGRATION.md](MIGRATION.md) — Phased migration strategy from OLM v0 to OLM v1
