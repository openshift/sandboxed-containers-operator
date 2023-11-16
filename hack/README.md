# CoCo Installtion

This describes a way to customise the installed Kata artifacts in OpenShift cluster
to support confidential containers in Azure.


## Prerequisites

1. OpenShift cluster in Azure, installed with the OSC operator (>=1.5.x)
2. Have your Azure's `AZURE_CLIENT_SECRET`, `AZURE_CLIENT_ID` and `AZURE_TENANT_ID` credentials used for the cluster as environment variables

## Setup

Create `peer-pods-secret` Secret in openshift-sandboxed-containers-operator
```
oc create secret generic peer-pods-secret -n openshift-sandboxed-containers-operator --from-literal=AZURE_CLIENT_SECRET=${AZURE_CLIENT_SECRET} --from-literal=AZURE_CLIENT_ID=${AZURE_CLIENT_ID} --from-literal=AZURE_TENANT_ID=${AZURE_TENANT_ID}
```

Generate SSH keys and create a secret:
```
ssh-keygen -f ./id_rsa -N ""
oc create secret generic ssh-key-secret -n openshift-sandboxed-containers-operator --from-file=id_rsa.pub=./id_rsa.pub --from-file=id_rsa=./id_rsa
```

Create the `peer-pods-cm` ConfigMap using the ConfigMap defaulter Job and wait for the ConfigMap to be created
```
oc apply -f coco-cm-defaulter.yaml
watch oc get cm/peer-pods-cm -n openshift-sandboxed-containers-operator
```

Create the Azure PeerPod CVM Image using the creation Job and wait for it to be created
```
oc apply -f azure-CVM-image-create-job.yaml
oc wait job.batch/azure-confidential-image-creation -n openshift-sandboxed-containers-operator --for=condition=complete --timeout=20m
```

Get the created image ID and update the ConfigMap with it
```
export IMG=$(oc logs job.batch/azure-confidential-image-creation -c result -n openshift-sandboxed-containers-operator)
echo $IMG
oc get cm/peer-pods-cm -n openshift-sandboxed-containers-operator -o json | jq --arg IMG "$IMG" '.data.AZURE_IMAGE_ID = $IMG' | oc replace -f -
```

Create KataConfig with `enablePeerPods: true`
```
oc apply -f-<<EOF
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: example-kataconfig
spec:
  enablePeerPods: true
EOF
```

Copy the custom shim in the worker nodes with Kata installed
```
oc apply -f ds.yaml
```
This will create the daemonset in the `openshift-sandboxed-containers-operator` namespace
and copy the custom shim to `/opt/kata` on all the worker nodes having the label: `node-role.kubernetes.io/kata-oc:`

Create the new MachineConfig to update the Kata configurations

```
oc apply -f mc-coco.yaml
```
The MachineConfig will update the CRIO config for the `kata-remote` runtimeClass to point to the custom shim.
Also it will update the Kata configuration-remote.toml


Wait for nodes to be in READY state

```
oc get mcp kata-oc --watch
```

Patch the CAA deployment to use coco enabled CAA image
```
oc set image ds/peerpodconfig-ctrl-caa-daemon -n openshift-sandboxed-containers-operator cc-runtime-install-pod=quay.io/openshift_sandboxed_containers/cloud-api-adaptor:coco-dev-preview
```

## Deploy KBS

[Setup KBS](kbs/README.md)


## Image Deletion

```
oc apply -f azure-CVM-image-delete-job.yaml
oc wait job.batch/azure-confidential-image-deletion -n openshift-sandboxed-containers-operator --for=condition=complete --timeout=5m
```
