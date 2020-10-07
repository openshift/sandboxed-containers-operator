# Kata Operator

An operator to perform lifecycle management (install/upgrade/uninstall) of [Kata Runtime](https://katacontainers.io/) on Openshift as well as Kubernetes cluster.

## Installing the Kata Runtime on the Cluster using Kata Operator

### Openshift

1. Make sure that `oc` is configured to talk to the cluster

2. Run to install the Kata Operator: 

   ```
   curl https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/deploy.sh | bash
   ```
3. And finally create a custom resource to install the Kata Runtime on all workers,

   ```
   oc apply -f https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr.yaml
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
oc apply -f https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/example-fedora.yaml
```  

### Kubernetes

1. Make sure that `kubectl` is configured to talk to the cluster

2. Run to install the Kata Operator: 

   ```
   curl https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/deploy-k8s.sh | bash
   ```
3. And finally create a custom resource to install the Kata Runtime on all workers,
   
   ```
   kubectl apply -f https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr_k8s.yaml
   ```

   Please follow [this](#selectively-install-the-kata-runtime-on-specific-workers) section if you wish to install the Kata Runtime only on selected worker nodes.
   
#### Install custom Kata Runtime version

Download the following file that contains the `KataConfig` custom resource. 
```
curl -o kataconfig_cr.yaml https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr_k8s.yaml
``` 

Kata Binaries artifacts are copied to the worker node from the source image (spec.config.sourceImage). You can modify that field to use a different [kata-deploy](https://github.com/kata-containers/packaging/tree/master/kata-deploy) image to install a specific version of the Kata Runtime binaries.

```yaml
apiVersion: kataconfiguration.openshift.io/v1alpha1
kind: KataConfig
metadata:
   name: example-kataconfig 
spec:
   config:
      sourceImage: docker.io/katadocker/kata-deploy:latest 
#   kataConfigPoolSelector:
#      matchLabels:
#        custom-kata1: test
``` 
```
kubectl apply -f  kataconfig_cr.yaml
```

   This will start the [kata-deploy](https://github.com/kata-containers/packaging/tree/master/kata-deploy) DaemonSet that runs pods on all nodes where Kata Runtime is to be installed

#### Monitoring the Kata Runtime Installation
Watch the description of the Kataconfig custom resource
```
kubectl describe kataconfig example-kataconfig
```
and look at the field 'Completed nodes' in the status. If the value matches the number of worker nodes the installation is completed.

#### Runtime Class
Once the kata runtime binaries are successfully installed on the intended workers, Kata Operator will create following [runtime classes](https://kubernetes.io/docs/concepts/containers/runtime-class/),

* kata 
* kata-clh
* kata-fc 
* kata-qemu
* kata-qemu-virtiofs 

Any of these runtime classes can be used to deploy the pods that will use the Kata Runtime.

#### Run an Example Pod using the Kata Runtime
```
kubectl apply -f https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/example-fedora.yaml
``` 
   
## Selectively Install the Kata Runtime on Specific Workers

### Openshift

1. Download the `KataConfig` custom resource file, 
   ```
   curl -o kataconfig_cr.yaml https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr.yaml
   ```
2. edit the custom resource file `kataconfig_cr.yaml`
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
   oc create -f kataconfig_cr.yaml
   ```

### Kubernetes

1. Download the `KataConfig` custom resource file, 
   ```
   curl -o kataconfig_cr.yaml https://raw.githubusercontent.com/openshift/kata-operator/release-4.6/deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr_k8s.yaml
   ```
2. edit the custom resource file `kataconfig_cr.yaml`
   and uncomment the kata pool selector fields in the spec as follows,

   ```yaml
   apiVersion: kataconfiguration.openshift.io/v1alpha1
   kind: KataConfig
   metadata:
      name: example-kataconfig 
   spec:
      config:
         sourceImage: docker.io/katadocker/kata-deploy:latest 
      kataConfigPoolSelector:
         matchLabels:
           custom-kata1: test
   ```

   If you wish, you can change the label "custom-kata1:test" to something of your choice.

3. Apply the chosen label to the desired nodes. e.g. `kubectl label node <worker_node_name> custom-kata1=test`
4. Create the custom resource to start the installation,
   ```
   kubectl create -f kataconfig_cr.yaml
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

### Kubernetes
```
kubectl delete kataconfig <KataConfig_CR_Name>
```
e.g.
```
kubectl delete kataconfig example-kataconfig
```

## Troubleshooting

### Openshift
1. deploy.sh will stop execution if it find that a namespace 'kata-operator' already exists. If you're running deploy.sh and it complains that the kata-operator namespace already exists a) make sure kata-operator is not already installed (check 'oc get kataconfig') and b) delete the namespace so that deploy.sh can create it. 
2. During the installation you can watch the values of the kataconfig CR. Do `watch oc describe kataconfig example-kataconfig`.
3. To check if the nodes in the machine config pool are going through a config update watch the machine config pool resource. For this do `watch oc get mcp kata-oc`
4. Check the logs of the kata-operator controller pod to see detailled messages about what the steps it is executing.

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

1. Install operator-sdk
2. generate-groups.sh all github.com/openshift/kata-operator/pkg/generated github.com/openshift/kata-operator/pkg/apis kataconfiguration:v1alpha1
3. operator-sdk build quay.io/<yourusername>/kata-operator:v1.0
4. podman push quay.io/<yourusername>/kata-operator:v1.0
