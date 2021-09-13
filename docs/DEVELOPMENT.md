# Hacking on the sandboxed-containers-operator

## Prerequisites
- Golang - 1.16.x
- Operator SDK version - 1.12.0
- podman, podman-docker or docker
- Access to OpenShift cluster (4.8+)
- Container registry to storage images


## Set Environment Variables
```
export IMAGE_TAG_BASE=quay.io/user/openshift-sandboxed-containers-operator
export IMG=quay.io/user/openshift-sandboxed-containers-operator
```

## Viewing available Make targets
```
make help
```

## Building Operator image
```
make docker-build
make docker-push
```

## Building Operator bundle image
```
make bundle-build
make bundle-push
```

## Building Catalog image
```
make catalog-build
make catalog-push
```

## Installing the Operator using OpenShift Web console 

### Create Custom Operator Catalog

Create a new `CatalogSource` yaml. Replace `user` with your quay.io user
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
 image:  quay.io/user/openshift-sandboxed-containers-operator-catalog:v1.0.1
 updateStrategy:
   registryPoll:
      interval: 5m

EOF
```
Deploy the catalog
```
oc create -f my_catalog.yaml
```

The new operator should be now available for installation from the OpenShift web console


## Installing the Operator using CLI

When deploying the Operator using CLI, cert-manager needs to be installed otherwise
webhook will not start. `cert-manager` is not required when deploying via the web console as OLM 
takes care of webhook certificate management. You can read more on this [here]( https://olm.operatorframework.io/docs/advanced-tasks/adding-admission-and-conversion-webhooks/#deploying-an-operator-with-webhooks-using-olm)

### Install cert-manager 
```
 oc apply -f https://github.com/jetstack/cert-manager/releases/download/v1.5.3/cert-manager.yaml
```

### Modify YAMLs
Uncomment all entries marked with `[CERTMANAGER]` in manifest files under `config/*`

### Deploy Operator
```
make install && make deploy
```



