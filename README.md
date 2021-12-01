## :information_source: If you are using OCP 4.7 please follow this [README](https://github.com/openshift/sandboxed-containers-operator/blob/release-4.7/README.md)
## :information_source: If you are using OCP 4.6 please follow this [README](https://github.com/openshift/sandboxed-containers-operator/blob/release-4.6/README.md)

# OpenShift sandboxed containers operator

An operator to perform lifecycle management (install/upgrade/uninstall) of [Kata Runtime](https://katacontainers.io/) on Openshift clusters.

## Deploy the operator

Starting with OpenShift 4.9, the branch naming is tied to operator version and not OpenShift version. For example `release-1.1`
corresponds to Operator release verson `1.1.x`.

### Using Openshift CLI

- Make sure that `oc` is configured to talk to OpenShift 4.9 cluster
- Deploy the operator by running the following
```yaml
cat << EOF | oc create -f - 
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-sandboxed-containers-operator

---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: openshift-sandboxed-containers-operator
  namespace: openshift-sandboxed-containers-operator
spec:
  targetNamespaces:
  - openshift-sandboxed-containers-operator
---

apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: openshift-sandboxed-containers-operator
  namespace: openshift-sandboxed-containers-operator
spec:
  channel: preview-1.1
  installPlanApproval: Automatic
  name: sandboxed-containers-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  startingCSV: sandboxed-containers-operator.v1.1.0
EOF 
```

- Check if operator pods are up (usually takes few minutes)
  ```
  oc get pods -n openshift-sandboxed-containers-operator
  ```
  Sample output
  ```
  NAME                                                              READY   STATUS    RESTARTS   AGE
  sandboxed-containers-operator-controller-manager-7fbbd95b7swc9b   1/1     Running   0          104s
  ```

### Using Openshift Web Console

- Switch to the `Administrator` perspective and navigate to `Operators → OperatorHub`.
- In the Filter by keyword field, type `OpenShift sandboxed containers`.
- Select the `OpenShift sandboxed containers` tile.
- Click `Install`.

## Install Kata containers runtime

### Create the `KataConfig` CR to start the installation
  
```yaml 
cat << EOF | oc create -f -
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: example-kataconfig
EOF
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
```yaml
cat << EOF | oc create -f -
apiVersion: v1
kind: Pod
metadata:
  name: example-fedora
  labels:
    app: example-fedora-app
  namespace: default
spec:
  containers:
    - name: example-fedora
      image: quay.io/fedora/fedora:35
      ports:
        - containerPort: 8080
      command: ["python3"]
      args: [ "-m", "http.server", "8080"]
  runtimeClassName: kata
EOF
```  

## Selectively Install the Kata Runtime on Specific Workers

### Apply a chosen label to the desired nodes 
```
oc label node <worker_node_name> custom-kata1=test
```

### Create the `KataConfig` CR to start the installation
```yaml 
cat << EOF | oc create -f -
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: example-kataconfig
spec:
  kataConfigPoolSelector:
    matchLabels:
       custom-kata1: test
EOF
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

If using the web console, then navigate to `Operators -> Installed Operators` and uninstall the operator.
If using CLI, then run the following command
```
oc delete ns openshift-sandboxed-containers-operator
```

## Developing

Please check the development [doc](./docs/DEVELOPMENT.md)

## Troubleshooting

### Openshift
1. During the installation you can watch the values of the `KataConfig` CR. Do `watch oc describe kataconfig example-kataconfig`.
2. To check if the nodes in the machine config pool are going through a config update watch the machine config pool resource. For this do `watch oc get mcp kata-oc`
3. Check the logs of the sandboxed containers operator controller pod to see detailled messages about what steps it is executing. 
   To find out the name of the controller pod, `oc get pods -n openshift-sandboxed-containers-operator | grep controller-manager` and 
   then monitor the logs of the container `manager` in that pod.

You can also refer to the following [blog](https://cloud.redhat.com/blog/sandboxed-containers-operator-from-zero-to-hero-the-hard-way-part-2) for additional troubleshooting tips.
