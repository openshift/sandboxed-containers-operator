## :information_source: If you are using OCP 4.6 please follow this [README](https://github.com/openshift/kata-operator/blob/release-4.6/README.md)

# Kata Operator

An operator to perform lifecycle management (install/upgrade/uninstall) of [Kata Runtime](https://katacontainers.io/) on Openshift as well as Kubernetes cluster.

## Installing the Kata Runtime on the Cluster using Kata Operator

### Openshift

1. Make sure that `oc` is configured to talk to the cluster

2. Clone the Kata Operator repository and check out the branch matching with the Openshift version. e.g. If you are running Openshift 4.7 then,


   ```
   git clone https://github.com/openshift/kata-operator 
   git checkout -b release-4.7 --track origin/release-4.7
   ```
3. Install the Kata Operator on the cluster,

   ```
   make install && make deploy IMG=quay.io/isolatedcontainers/kata-operator:4.7
   oc adm policy add-scc-to-user privileged -z default -n kata-operator-system
   ```
4. To begin the installation of the kata runtime on the cluster,

   ```
   oc create -f config/samples/kataconfiguration_v1_kataconfig.yaml
   ```

   Please follow [this](#selectively-install-the-kata-runtime-on-specific-workers) section if you wish to install the Kata Runtime only on selected worker nodes.
   
#### Monitoring the Kata Runtime Installation
Watch the description of the Kataconfig custom resource
```
oc describe kataconfig example-kataconfig
```
and look at the field 'Completed nodes' in the status. If the value matches the number of worker nodes the installation is completed.

#### Runtime Class
Once the kata runtime binaries are successfully installed on the intended workers, Kata Operator will create a [runtime class](https://kubernetes.io/docs/concepts/containers/runtime-class/) `kata`. This runtime class can be used to deploy the pods that will use the Kata Runtime.

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
3. Check the logs of the kata-operator controller pod to see detailled messages about what the steps it is executing. To find out the name of the controller pod, `oc get pods -n kata-operator-system | grep kata-operator-controller-manager` and then monitor the logs of the container `manager` in that pod. 

## Components

### Openshift
The kata-operator uses three containers:

Container image name | Description | Repository
---------------| ----------- | ----------
 _kata-operator_ |  It contains the controller part of the operator that watches and manages the kataconfig custom resource. It runs as a cluster scoped container. The operator itself is build with operator-sdk. | https://github.com/isolatedcontainers/kata-operator
 _kata-operator-daemon_ | The daemon part of the operator that runs on the nodes and performs the actual installation. It pulls down the container kata-operator-payload image. | https://github.com/isolatedcontainers/kata-operator-daemon
 _kata-operator-payload_ | The payload that is used by the daemon to install the kata binaries and dependencies (like e.g. QEMU). It's a container image with (currently) RPMs in it that will be installed on the chosen worker nodes by the daemon. | https://github.com/isolatedcontaineres/kata-operator-payload

## Upgrading Kata

### Openshift
Not implemented yet

### Kubernetes
Not implemented yet

# Build from source

1. Install operator-sdk version 1.0 or above
2. make docker-build docker-push IMG=quay.io/<yourusername>/kata-operator:<tag>
