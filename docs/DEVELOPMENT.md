# Hacking on the sandboxed-containers-operator

## Prerequisites
- Golang - 1.21.x
- Operator SDK version - 1.28.0
```shell
export ARCH=$(case $(uname -m) in x86_64) echo -n amd64 ;; aarch64) echo -n arm64 ;; *) echo -n $(uname -m) ;; esac)
export OS=$(uname | awk '{print tolower($0)}')
export OPERATOR_SDK_DL_URL=https://github.com/operator-framework/operator-sdk/releases/download/v1.28.0
curl -LO ${OPERATOR_SDK_DL_URL}/operator-sdk_${OS}_${ARCH}
install -m 755 operator-sdk_linux_amd64 ${SOME_DIR_IN_YOUR_PATH}/operator-sdk
```
- podman, podman-docker or docker
- Access to OpenShift cluster (4.12+)
- Container registry to store images

### Get a token on registry.ci.openshift.org
Our builder and base images are curated images from OpenShift.
They are pulled from registry.ci.openshift.org, which require an authentication.
To get access to these images, you have to login and retrieve a token, following [these steps](https://docs.ci.openshift.org/docs/how-tos/use-registries-in-build-farm/#how-do-i-log-in-to-pull-images-that-require-authentication)

##### Details on token
- You should go to _app.ci_ in the above doc, login with SSO
- Go to _copy login_ on your _username_ in the top right
- You will login and see _view token_.  Click on that to see
- **Your API token is**.  The next line is your full token
- _podman login_ with your username and the token as your password should put the token in your AUTH_FILE

#### Using the token
If docker build does not automatically use the token in the default authfile, it can be forces:
```shell
export AUTH_FILE='~/.docker/config.json'
```

In summary:
- login to one of the clusters' console
- use the console's shortcut to get the commandline login command
- log in from the command line with the provided command
- use "podman login" to save the token locally
- set AUTH_FILE to force docker-build to use the token from your file if needed

## Change catalog for private quay registry
### Set Environment Variables

Set your quay.io userid
```shell
export QUAY_USERID=<user>
export IMAGE_TAG_BASE=quay.io/${QUAY_USERID}/openshift-sandboxed-containers-operator
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
### Skipping tests in Make
Add SKIP_TESTS=1 when calling make.  ex:
```
make SKIP_TESTS=1 docker-build
```

## Building Operator bundle image

### If you are deploying in an OpenShift cluster
Modify SANDBOXED_CONTAINERS_EXTENSION in config/manager/manager.yaml before you __Make the bundle__
```shell
sed -ie 's/kata-containers/sandboxed-containers/' config/manager/manager.yaml
```

### Make the bundle
```
make bundle CHANNELS=candidate
make bundle-build
make bundle-push
```

## Building Catalog image
#### Overwrite/create catalog/index.yaml if needed
If `IMAGE_TAG_BASE` was set for your private registry, `catalog/index.yaml` will need to be rewritten.  Before running `make catalog-build`, run this:

```
DEFAULT_CHANNEL="stable"  # from Makefile
VERSION=1.7.0             # from Makefile
BUNDLE_IMG=${IMAGE_TAG_BASE}-bundle:v${VERSION}
make opm                  # to get bin/opm
rm -rf catalog && mkdir -p catalog
bin/opm init  sandboxed-containers-operator --default-channel=${DEFAULT_CHANNEL} --description ./README.md --output yaml > catalog/index.yaml
bin/opm render ${BUNDLE_IMG} --output=yaml >> catalog/index.yaml
printf -- "---\n\
schema: olm.channel\n\
package: sandboxed-containers-operator\n\
name: ${DEFAULT_CHANNEL}\n\
entries:\n\
  - name: sandboxed-containers-operator.v${VERSION}\n\
" >> "catalog/index.yaml"
```
#### Creating catalog.Dockerfile
If `catalog.Dockerfile` doesn't exist, it can be created with `opm generate dockerfile catalog`.  It should not need to be recreated or changed.


```
make catalog-build
make catalog-push
```

## Installing the Operator using OpenShift Web console

### Create Custom Operator Catalog

Create a new `CatalogSource` yaml. Replace `${QUAY_USERID}` with your quay.io user and
`${VERSION}` with the operator VERSION from the Makefile

```shell
cat > my_catalog.yaml <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
 name:  my-operator-catalog
 namespace: openshift-marketplace
spec:
 displayName: My Operator Catalog
 sourceType: grpc
 image:  quay.io/${QUAY_USERID}/openshift-sandboxed-containers-operator-catalog:v${VERSION}
 updateStrategy:
   registryPoll:
      interval: 5m

EOF
```
Deploy the catalog
```
oc apply -f my_catalog.yaml
```

The new operator should become available for installation from the OpenShift web console


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



