# Requirements for OpenShift sandboxed containers must-gather

`must-gather` for OpenShift sandboxed contains (OSC) should gather all information and logs needed for debugging OSC.

### Usage
```sh
oc adm must-gather --image=registry.redhat.io/openshift-sandboxed-containers/osc-must-gather-rhel9:latest
```

The command above will create a local directory with a dump of the OpenShift sandboxed-containers state.
Note that this command will only get data related to the sandboxed-containers part of the OpenShift cluster.

### Contents of the dump
- All namespaces (and their children objects) that belong to any sandboxed containers resources

In order to get data about other parts of the cluster (not specific to sandboxed containers) you should
run `oc adm must-gather` (without passing a custom image). Run `oc adm must-gather -h` to see more options.

#### General
- Resource definitions
- Service logs

#### OSC
- All namespaces and their child objects that belong to any OpenShift sandboxed containers resources
- All OpenShift sandboxed containers custom resource definitions (CRDs)
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/**_*\_description_**

- Kata agent logs **???????**
- Kata runtime logs **??????**

- QEMU logs
  - part of the **crio** logs as subsystem=qemu and subsystem=qmp
- Audit logs
  - audit_logs/**_nodename_**-audit.log.gz
- CRI-O logs
  - nodes/**_nodename_**/**_nodename_**_logs_**crio**
- QEMU versions
  - nodes/**_nodename_**/**_nodename_**/version
- Essential
  - apiservices/v1.kataconfiguration.openshift.io.yaml
  - cluster-scoped-resources/apiextensions.k8s.io/customresourcedefinitions/kataconfigs.kataconfiguration.openshift.io.yaml
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/controller-manager-**_*\_logs_**
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/install-**_*\_logs_**
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/openshift-sandboxed-containers-monitor-**_*\_logs_**
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/peerpodconfig-ctrl-caa-daemon-**_*\_logs_**
  - sandboxed-containers/namespaces/openshift-sandboxed-containers-operator/peer-pods-webhook-**_*\_logs_**
  - sandboxed-containers/clusterserviceversion_description
  - sandboxed-containers/kataconfig_description
  - sandboxed-containers/services_description
  - sandboxed-containers/subscription_description
  - sandboxed-containers/validatingwebhookconfigurations_description

#### Debug level
Setting kataconfig `loglevel: debug` will get more info

- Kata agent **???????**
- Kata runtime (containerd-shim-kata-v2_description
  - shim logs are part of the **crio** logs
- virtiofsd
  - virtiofsd logs are part of the **crio** logs
