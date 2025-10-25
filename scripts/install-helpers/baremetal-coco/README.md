# Introduction

These are helper scripts to setup CoCo on a bare-metal OpenShift worker nodes
using OpenShift sandboxed containers (OSC) operator.

When using regular OpenShift cluster, which has at least a single node in the `worker`
MachineConfigPool, then you must add a label to the target worker nodes before starting the
install. For example, you can set "coco_bm=true" on the target nodes.
Note that label is not needed when using SNO or converged cluster as the installation
happens on all the nodes.

The deployment sequence is described below:

```text
1. If using a regular OpenShift cluster (with worker MachineConfigPool having at least one node),
   then you must label at least one worker node and set BM_NODE_LABEL env variable to the specific label (eg. BM_NODE_LABEL="coco_bm=true")
   If using SNO or converged OpenShift cluster, then you don't need to label any node.
2. Deploy OSC operator
3. Create Kataconfig to install the RHCOS image layer.
   If using SNO or converged OpenShift then the RHCOS image layer will be installed
   on all the nodes
4. Deploy NFD operator
5. Verify if the target nodes have SNP or TDX capabilities
6. Deploy other prerequisites (eg DCAP for TDX)
7. Set TEE specific Kata configuration
8. Create TEE specific runtime class
```

>**Note**
> >
> - CoCo on baremetal requires custom kernel which is not available in standard RHCOS image layer. Hence we create the Kataconfig to install the RHCOS image layer into the target nodes and then use NFD to add required TEE specific labels as exposed by the kernel `amd.feature.node.kubernetes.io/snp: "true"` is set for the SNP nodes and `intel.feature.node.kubernetes.io/tdx: "true"` is set for the TDX nodes.
> >
> - Currently the script only supports installing a single TEE environment.

## Prerequisites

- `oc` and `jq` CLI

- Compute attestation operator (Trustee) should be installed and configured for attestation.

- TRUSTEE_URL env variable to be set with Trustee ingress details

  If using ClusterIP to access Trustee, then use the following command:

  ```sh
  TRUSTEE_HOST=$(oc get svc -n trustee-operator-system kbs-service -o jsonpath={.spec.clusterIP})
  TRUSTEE_PORT=$(oc get svc -n trustee-operator-system kbs-service -o jsonpath="{.spec.ports[0].targetPort}")
  echo $TRUSTEE_HOST:$TRUSTEE_PORT
  export TRUSTEE_URL="http://$TRUSTEE_HOST:$TRUSTEE_PORT"
  ```

  or, if using OpenShift route to access Trustee, then use the following command:

  ```sh
  TRUSTEE_HOST=$(oc get route -n trustee-operator-system kbs-service -o jsonpath={.spec.host})
  export TRUSTEE_URL="https://$TRUSTEE_HOST"
  ```

## Install OSC operator GA release

- The Subscription follows the `stable` channel; no `startingCSV` pinning is required.

- Optional: pin to a specific CSV by setting `OSC_OPERATOR_CSV` before running the script. The script will patch the Subscription's `startingCSV` accordingly.

  Example:

  ```sh
  export OSC_OPERATOR_CSV=sandboxed-containers-operator.v1.10.1
  ./install.sh -t tdx
  ```

- If not using SNO or converged cluster then label at least a single worker node for deployment
  and export the label via the 1BM_NODE_LABEL1 env variable

  ```sh
  export NODENAME=<node>
  oc label node $NODENAME coco_bm=true
  export BM_NODE_LABEL="coco_bm=true"
  ```

- Kickstart the installation by running the following:

> Depending on the time it takes for the nodes to reboot, sometimes the commands may timeout.
> You can use a higher timeout eg. export CMD_TIMEOUT=3000
> or you can re-run the script to complete the installation.

  For TDX hosts:

  ```sh
  ./install.sh -t tdx
  ```

  For SNP hosts:

  ```sh
  ./install.sh -t snp
  ```

  This will install the OSC operator and configure Kata with CoCo support on the bare-metal worker nodes.

## Install OSC operator pre-GA release

- Kickstart the installation by running the following:

  ```sh
  ./install.sh -t tdx -m -b
  ```

  This will deploy the pre-GA release of OSC operator on TDX hosts.

After successful install, the following runtimeclasses will be created:
  - `kata` for non-confidential workloads
  - `kata-cc` for confidential workloads (with handler `kata-tdx` for Intel TDX or `kata-snp` for AMD SNP)


## Adding custom container registry secrets

  To add custom registry secrets, you'll need to set the `PULL_SECRET_JSON` variable with the registry credentials.
  Following is an example:

  ```sh
  export PULL_SECRET_JSON='{"my.custom.registry.io": {"auth": "abcd1234"}, "registry.mydom.io": {"auth": "abcd1234"}}'
  ```

## Un-installation

   Run the following command to uninstall

   For TDX hosts:

   ```sh
   ./install.sh -t tdx -u
   ```

   For SNP hosts:

   ```sh
   ./install.sh -t snp -u
   ```
