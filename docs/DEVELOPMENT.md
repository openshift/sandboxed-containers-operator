# Hacking on the sandboxed-containers-operator


## Prerequisites
- Golang - 1.15
- make
- podman or docker
- Access to OpenShift cluster
- [opm CLI](https://docs.openshift.com/container-platform/4.7/cli_reference/opm-cli.html)
- Container registry to storage images

## Set Container Registry

```
export REGISTRY="quay.io"
export REGISTRY_USER="quay_user"
```

## Building Operator image
```
make podman-build IMG=$REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator:latest
make podman-push IMG=$REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator:latest
```

## Building Operator bundle

```
sed -i "s/IMAGE_REG/$REGISTRY\/$REGISTRY_USER/" bundle/manifests/sandboxed-containers-operator.clusterserviceversion.yaml
```

```
make bundle-build BUNDLE_IMG=$REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator-bundle:latest
podman push $REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator-bundle:latest
opm index add --bundles $REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator-bundle:latest  --tag $REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator-index:latest
podman push $REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator-index:latest
```

## Create Custom Operator Catalog

Create a new `CatalogSource` yaml. Replace `<user>` with your quay.io user
```
cat > my_catalog.yaml <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
 name:  my-operator-catalog
 namespace: openshift-marketplace
spec:
 DisplayName: My Operator Catalog
 sourceType: grpc
 image: $REGISTRY/$REGISTRY_USER/openshift-sandboxed-containers-operator-index:latest
 updateStrategy:
   registryPoll:
      interval: 5m

EOF

```
Deploy the catalog
```
$ oc create -f my_catalog.yaml
```

The new operator should be now available for installation
