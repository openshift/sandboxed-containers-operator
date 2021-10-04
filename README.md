## :information_source: If you are using OCP 4.7 please follow this [README](https://github.com/openshift/sandboxed-containers-operator/blob/release-4.7/README.md)
## :information_source: If you are using OCP 4.6 please follow this [README](https://github.com/openshift/sandboxed-containers-operator/blob/release-4.6/README.md)

# OpenShift sandboxed containers operator

An operator to perform lifecycle management (install/upgrade/uninstall) of [Kata Runtime](https://katacontainers.io/) on Openshift clusters.

## Deploy the operator

### Using Openshift CLI

- Make sure that `oc` is configured to talk to the cluster
- Deploy the operator by running the following
  ```
  oc apply -f https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/config/samples/deploy.yaml 
  ```

- Check if operator pods are up (usually takes few minutes)
  ```
  oc get pods -n openshift-sandboxed-containers-operator
  ```
  Sample output
  ```
  NAME                                 READY   STATUS    RESTARTS   AGE
  controller-manager-64888847f-j6256   2/2     Running   0          35s
  ```

### Using Openshift Web Console

- Switch to `Administrator` perspective and navigate to `Admininstration -> Cluster Settings`.
- In the `Cluster Settings` page, switch to `Configuration` tab and search for `OperatorHub`
- Click `OperatorHub` and navigate to `Sources` tab.
- Click `Create CatalogSource` and use the following `Image`
  ```
  quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-operator-catalog:v1.2.0
  ```

- Ensure `Cluster-wide CatalogSource` is selected.

- Click `Create` to create the catalog. 

The sandboxed container operator will be available under `Operators -> OperatorHub`


## Install Kata containers runtime

### Create the `KataConfig` CR to start the installation
  
``` 
oc apply -f https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/config/samples/kataconfiguration_v1_kataconfig.yaml
```

Please follow [this](#selectively-install-the-kata-runtime-on-specific-workers) section if you wish to install the Kata Runtime only on selected worker nodes.

#### Monitoring the Kata Runtime Installation
Watch the description of the `KataConfig` custom resource
```
oc describe kataconfig example-kataconfig
```
and look at the field `Completed nodes` in the status. If the value matches the number of worker nodes the installation is completed.

#### Runtime Class
Once the sandboxed-containers extension is enabled successfully on the intended workers, the sandboxed containers operator will create a [runtime class](https://kubernetes.io/docs/concepts/containers/runtime-class/) `kata`. This runtime class can be used to deploy the pods that will use the Kata Runtime.

#### Run an Example Pod using the Kata Runtime
```
oc apply -f https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/config/samples/example-fedora.yaml
```  

## Selectively Install the Kata Runtime on Specific Workers

### Edit the custom resource file `config/samples/kataconfiguration_v1_kataconfig.yaml`
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

### Apply the chosen label to the desired nodes. e.g. `oc label node <worker_node_name> custom-kata1=test`

### Create the custom resource to start the installation,
   ```
   oc apply -f config/samples/kataconfiguration_v1_kataconfig.yaml
   ```

## Uninstall

### Uninstall Kata runtime
Delete the `KataConfig` CR
```
oc delete kataconfig <KataConfig_CR_Name>
```
e.g.
```
oc delete kataconfig example-kataconfig
```

### Uninstall the Operator

If using the web console, then navigate to `Operators -> Install Operators` and uninstall the operator
If using CLI, then run the following command
```
oc delete -f https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/config/samples/deploy.yaml
```

## Developing
Please check the development [doc](./docs/DEVELOPMENT.md)

## Troubleshooting

### Openshift
1. During the installation you can watch the values of the `KataConfig` CR. Do `watch oc describe kataconfig example-kataconfig`.
2. To check if the nodes in the machine config pool are going through a config update watch the machine config pool resource. For this do `watch oc get mcp kata-oc`
3. Check the logs of the sandboxed containers operator controller pod to see detailled messages about what steps it is executing. To find out the name of the controller pod, `oc get pods -n openshift-sandboxed-containers-operator | grep controller-manager` and then monitor the logs of the container `manager` in that pod.


