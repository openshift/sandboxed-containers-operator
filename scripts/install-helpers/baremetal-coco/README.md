# Introduction

These are helper scripts to setup CoCo on a bare-metal OpenShift worker nodes
using OpenShift sandboxed containers (OSC) operator.

It's expected to label the target nodes with a suitable label before starting the
install. For example, you can set "coco_bm=true" on the target nodes.

The deployment sequence is described below:

```text
1. Label target nodes and set BM_NODE_LABEL env variable (eg. BM_NODE_LABEL="coco_bm=true")
2. Deploy OSC operator
3. Create Kataconfig to install the RHCOS image layer on the nodes with BM_NODE_LABEL. 
   If using SNO or converged OpenShift then the RHCOS image layer will be installed
   on all the nodes.
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

- Update `startingCSV` key in the `subs-ga.yaml` file to use the GA release you need.

- Kickstart the installation by running the following:

> Depending on the time it takes for the nodes to reboot, sometimes the commands may timeout.
> You can use a higher timeout eg. export CMD_TIMEOUT=1200
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

- Update osc_catalog.yaml to point to the pre-GA catalog
  For example if you want to install the pre-GA 0.0.1-24 build, then change the
  image entry to the following

  ```sh
  image: quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-operator-catalog:0.0.1-22
  ```

- The pre-GA build images are in an authenticated registry, so you'll need to
  set the `PULL_SECRET_JSON` variable with the registry credentials. Following is an example:

  ```sh
  export PULL_SECRET_JSON='{"brew.registry.redhat.io": {"auth": "abcd1234"}, "registry.redhat.io": {"auth": "abcd1234"}}'
  ```

- Kickstart the installation by running the following:

  ```sh
  ./install.sh -t tdx -m -s -b
  ```

  This will deploy the pre-GA release of OSC operator on TDX hosts.

After successful install `kata`, `kata-tdx` or `kata-snp` runtimeclasses will be created based on the TEE type.

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
