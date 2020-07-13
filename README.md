# kata-operator

An operator to enhance an Openshift/Kubernetes cluster to support running Kata containers.

## Deploying

1. Make sure that oc is configured to talk to the cluster

2. Run 

   ```
   curl https://raw.githubusercontent.com/jensfr/kata-operator/master/deploy/deploy.sh | bash
   ```

   This will create the serviceaccount, role and role binding used by the operator

3. And finally create a custom resource for kata

   ```
   oc apply -f deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr.yaml
   ```

   This will start the daemonset that runs pods on all nodes where Kata is to be installed
   
### Only install Kata on specific pool of worker nodes

By default Kata will be installed on all worker nodes. To choose a subset of nodes, 

1. edit the custom resource file `deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr.yaml`
   and comment out the pool selector fields.

   ```yaml
   apiVersion: kataconfiguration.openshift.io/v1alpha1
   kind: KataConfig
   metadata:
     name: example-kataconfig
   #spec:
   #  kataConfigPoolSelector:
   #    matchLabels:
   #       custom-kata1: test
   ```

   Change the label "custom-kata1:test" to something of your choice.

2. Apply the chosen label to the nodes with `oc label node <myworker0> custom-kata1=test`

Start the installation. The kata-operator will create a [machine config pool!](https://www.redhat.com/en/blog/openshift-container-platform-4-how-does-machine-config-pool-work)

## Uninstall

To delete the support for Kata containers you have to delete the custom resource, in our example: `oc delete -f deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr.yaml`

Watch the CR description (for example `watch oc describe kataconfig example-kataconfig` to see the progress and current status of the uninstall operation. Once completed it will show the full number of affected nodes as uninstalled in the `Completed nodes` field. This will delete the machineconfig entry for the CRIO configuration drop-in file, the runtimeclass, the RPMs from the worker nodes, the labels from the selected worker nodes and eventually the custom resource. 

## Troubleshooting

1. During the installation you can watch the values of the kataconfig CR. Do `watch oc describe kataconfig example-kataconfig`.
2. To check if the nodes in the machine config pool are going through a config update watch the machine config pool resource. For this do `watch oc get mcp kata-oc`
3. Check the logs of the kata-operator controller pod to see detailled messages about what the steps it is executing.

## Components

The kata-operator uses three containers:

Container image name | Description | Repository
---------------| ----------- | ----------
 _kata-operator_ |  It contains the controller part of the operator that watches and manages the kataconfig custom resource. It runs as a cluster scoped container. The operator itself is build with operator-sdk. | https://github.com/isolatedcontainers/kata-operator
 _kata-operator-daemon_ | The daemon part of the operator that runs on the nodes and performs the actual installation. It pulls down the container kata-operator-payload image. | https://github.com/isolatedcontainers/kata-operator-daemon
 _kata-operator-payload_ | The payload that is used by the daemon to install the kata binaries and dependencies (like e.g. QEMU). It's a container image with (currently) RPMs in it that will be installed on the chosen worker nodes by the daemon. | https://github.com/isolatedcontaineres/kata-operator-payload

## Upgrading Kata

Not implemented yet

# Build from source

1. Install operator-sdk
2. generate-groups.sh all github.com/openshift/kata-operator/pkg/generated github.com/openshift/kata-operator/pkg/apis kataconfiguration:v1alpha1
3. operator-sdk build quay.io/<yourusername>/kata-operator:v1.0
4. podman push quay.io/<yourusername>/kata-operator:v1.0
