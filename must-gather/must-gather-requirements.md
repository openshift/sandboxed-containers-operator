# <center>Requirements for OpenShift Sandboxed Containers (OSC) must-gather</center>


### Usage
The kataconfig must have `logLevel: debug` set before running `must-gather`.

OSC `must-gather` should gather all OCS information and logs needed for debugging in a directory
```sh
oc adm must-gather --image=registry.redhat.io/openshift-sandboxed-containers/osc-must-gather-rhel9:latest
```
Data about other parts of the cluster is gathered with `oc adm must-gather`. Run `oc adm must-gather -h` to see more options.

### Openshift Sandboxed Containers
Kata runtime is the `containerd-shim-kata-v2` process that talks to the kata agent in the VM.
See also the [Official 1.5 documentation](https://access.redhat.com/documentation/en-us/openshift_sandboxed_containers/1.5/html-single/openshift_sandboxed_containers_user_guide/index#troubleshooting-sandboxed-containers)

#### Gathered Data
- Resource definitions
- Service logs
- All namespaces and child objects with OSC resources
- All OSC custom resource definitions (CRDs)
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/**_*_**\_description
- versions in nodes/**_nodename_**/**_nodename_**/version
  - kata-containers
  - qemu


#### Locations
- CRI-O logs - from the kata runtime
  - nodes/**_nodename_**/**_nodename_**\_logs\_crio
- QEMU
  - logs are part of the **CRI-O** logs as _subsystem=qemu_ , _subsystem=qmp_ and/or _qemuPID=**PID**_
- Audits
  - audit_logs/**_nodename_**-audit.log.gz
- Logs
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/controller-manager-**_*_**\_logs
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/install-**_*_**\_logs
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/openshift-sandboxed-containers-monitor-**_*_**\_logs
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/peerpodconfig-ctrl-caa-daemon-**_*_**\_logs
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/peer-pods-webhook-**_*_**\_logs
-  OSC CRDs
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/**_*_**\_description
  - sandboxed-containers/clusterserviceversion_description
  - sandboxed-containers/kataconfig_description
  - sandboxed-containers/services_description
  - sandboxed-containers/subscription_description
  - sandboxed-containers/validatingwebhookconfigurations_description
- apiservices/v1.kataconfiguration.openshift.io.yaml
- cluster-scoped-resources/apiextensions.k8s.io/customresourcedefinitions/kataconfigs.kataconfiguration.openshift.io.yaml
- cluster-scoped-resources/kataconfig/kataconfig-events.txt
- cluster-scoped-resources/kataconfig/controller-manager-status.txt


### Peer Pods
Data collected for peer-pods (cloud-API-adaptor) deployments.

#### Gathered Data
- `peer-pods-cm` ConfigMap (VPC, subnet, security group, AMI/image ID, region)
- `peer-pods-secret` Secret key names (not values — security redacted)
- `cloud-credentials` Secret key names (not values)
- `peerpodconfig` custom resource status
- PodVMImageJob resources (status, conditions)

#### Locations
- sandboxed-containers/peer-pods/peer-pods-cm.yaml
- sandboxed-containers/peer-pods/peer-pods-secret-keys.txt
- sandboxed-containers/peer-pods/cloud-credentials-keys.txt
- sandboxed-containers/peer-pods/peerpodconfig.yaml
- sandboxed-containers/peer-pods/**_podvmimagejob_**.yaml
- sandboxed-containers/peer-pods/**_podvmimagejob_**\_description


### OLM Diagnostics
Additional OLM data beyond subscription and CSV descriptions.

#### Gathered Data
- CatalogSource resources (full YAML)
- OperatorGroup configuration
- InstallPlan resources with conditions
- CredentialsRequest resources
- Controller manager deployment status

#### Locations
- sandboxed-containers/olm/catalogsources.yaml
- sandboxed-containers/olm/operatorgroup.yaml
- sandboxed-containers/olm/**_installplan_**.yaml
- sandboxed-containers/olm/credentialsrequests.yaml
- sandboxed-containers/olm/deployments.yaml


### HCP (HyperShift) Hosted Clusters
When running on an HCP hosted cluster (`controlPlaneTopology: External`), additional
data is collected automatically. MachineConfigPool collection is skipped (MCPs do not
exist on hosted clusters).

**Note:** Management cluster resources (HostedCluster, HostedControlPlane, NodePool,
HyperShift operator, MCE operator) are not collected because must-gather runs in the
hosted cluster context. Run must-gather separately on the management cluster for those.

#### Gathered Data
- Cluster infrastructure object
- ClusterVersion status and conditions
- DNS operator configuration
- IngressController status
- Kubernetes service endpoints
- RuntimeClass definitions
- Events in OSC operator and marketplace namespaces
- Node labels (topology region, nodePool annotations)

#### Locations
- hcp/topology
- hcp/infrastructure.yaml
- hcp/clusterversion.yaml
- hcp/dns-operator.yaml
- hcp/ingresscontrollers.yaml
- hcp/endpoints-kubernetes.yaml
- hcp/runtimeclasses.yaml
- hcp/events-osc-operator.txt
- hcp/events-marketplace.txt
- hcp/node-labels.txt
- hcp/README


