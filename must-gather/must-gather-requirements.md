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


