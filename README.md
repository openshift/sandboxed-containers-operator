## :information_source: If you are using OCP 4.7 please follow this [README](https://github.com/openshift/sandboxed-containers-operator/blob/release-4.7/README.md)
## :information_source: If you are using OCP 4.6 please follow this [README](https://github.com/openshift/sandboxed-containers-operator/blob/release-4.6/README.md)

# OpenShift sandboxed containers operator

An operator to perform lifecycle management (install/upgrade/uninstall) of [Kata Runtime](https://katacontainers.io/) on Openshift as well as Kubernetes cluster.

## Installing the Kata Runtime on the Cluster using OpenShift sandboxed containers operator

### Openshift

#### with a git repo checkout
1. Make sure that `oc` is configured to talk to the cluster

2. Clone the sandboxed containers operator repository and check out the branch matching with the Openshift version. e.g. If you are running Openshift 4.8 then,


   ```
   git clone https://github.com/openshift/sandboxed-containers-operator
   git checkout -b master --track origin/master
   ```
3. Install the sandboxed containers operator on the cluster,

   ```
   make install && make deploy IMG=quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-operator:latest
   ```
4. To begin the installation of the kata runtime on the cluster

   ```
   oc create -f config/samples/kataconfiguration_v1_kataconfig.yaml
   ```

#### without a git repo checkout
1. Make sure that `oc` is configured to talk to the cluster

2. To deploy the operator and create a custom resource (which installs Kata on all worker nodes), run
   ``` curl https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/deploy/install.sh | bash ```

  This will create all necessary resources, deploy the sandboxed-containers-operator and also create a custom resource.
  See deploy/deploy.sh and deploy/deployment.yaml for details.

  To only deploy the operator without automatically creating a kataconfig custom resource just run
  ``` curl https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/deploy/deploy.sh | bash ```

  You can then create the CR and start the installation with
  ``` oc apply -f https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/config/samples/kataconfiguration_v1_kataconfig.yaml ```

   Please follow [this](#selectively-install-the-kata-runtime-on-specific-workers) section if you wish to install the Kata Runtime only on selected worker nodes.

#### Monitoring the Kata Runtime Installation
Watch the description of the Kataconfig custom resource
```
oc describe kataconfig example-kataconfig
```
and look at the field 'Completed nodes' in the status. If the value matches the number of worker nodes the installation is completed.

#### Runtime Class
Once the sandboxed-containers extension is enabled successfully on the intended workers, the sandboxed containers operator will create a [runtime class](https://kubernetes.io/docs/concepts/containers/runtime-class/) `kata`. This runtime class can be used to deploy the pods that will use the Kata Runtime.

#### Run an Example Pod using the Kata Runtime
```
oc apply -f config/samples/example-fedora.yaml
```  

## Selectively Install the Kata Runtime on Specific Workers

### Openshift

1. edit the custom resource file `config/samples/kataconfiguration_v1_kataconfig.yaml`
   and uncomment the kata pool selector fields in the spec as follows,

   ```yaml
   apiVersion: kataconfiguration.openshift.io/v1alpha1
   kind: KataConfig
   metadata:
     name: example-kataconfig
   spec:
     kataConfigPoolSelector:
       matchLabels:
          custom-kata1: test
   ```

   If you wish, you can change the label "custom-kata1:test" to something of your choice.

3. Apply the chosen label to the desired nodes. e.g. `oc label node <worker_node_name> custom-kata1=test`
4. Create the custom resource to start the installation,
   ```
   oc create -f config/samples/kataconfiguration_v1_kataconfig.yaml
   ```


## Uninstall

### Openshift
```
oc delete kataconfig <KataConfig_CR_Name>
```
e.g.
```
oc delete kataconfig example-kataconfig
```

## Troubleshooting

### Openshift
1. During the installation you can watch the values of the kataconfig CR. Do `watch oc describe kataconfig example-kataconfig`.
2. To check if the nodes in the machine config pool are going through a config update watch the machine config pool resource. For this do `watch oc get mcp kata-oc`
3. Check the logs of the sandboxed containers operator controller pod to see detailled messages about what steps it is executing. To find out the name of the controller pod, `oc get pods -n openshift-sandboxed-containers-operator | grep controller-manager` and then monitor the logs of the container `manager` in that pod.

## Components

### Openshift
The sandboxed containers operator uses three containers:

Container image name | Description | Container repository
---------------| ----------- | ----------
 _openshift-sandboxed-containers-operator_ |  It contains the controller part of the operator that watches and manages the kataconfig custom resource. It runs as a cluster scoped container. The operator itself is build with operator-sdk. | https://quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-operator

# Build from source

1. Install operator-sdk version 1.0 or above
2. make podman-build podman-push IMG=quay.io/<yourusername>/sandboxed-containers-operator:<tag>
