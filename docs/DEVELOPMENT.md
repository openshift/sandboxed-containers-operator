# Hacking on the sandboxed-containers-operator

## Prerequisites
- Golang - 1.22.x
- Operator SDK version - 1.39.1
- podman, podman-docker or docker
- Access to OpenShift cluster (4.12+)
- Container registry to storage images

### Get a token on registry.ci.openshift.org
Our builder and base images are curated images from OpenShift.
They are pulled from registry.ci.openshift.org, which require an authentication.
To get access to these images, you have to login and retrieve a token, following [these steps](https://docs.ci.openshift.org/docs/how-tos/use-registries-in-build-farm/#how-do-i-log-in-to-pull-images-that-require-authentication)

In summary:
- login to one of the clusters' console
- use the console's shortcut to get the commandline login command
- log in from the command line with the provided command
- use "oc registry login" to save the token locally

## Set Environment Variables

Set your quay.io userid
```
export QUAY_USERID=<user>
```

```
export IMAGE_TAG_BASE=quay.io/${QUAY_USERID}/openshift-sandboxed-containers-operator
export IMG=quay.io/${QUAY_USERID}/openshift-sandboxed-containers-operator
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
make bundle CHANNELS=candidate
make bundle-build
make bundle-push
```

## Regenerating generated files

### Bundle manifests

`make bundle` regenerates `bundle/manifests/` from `config/`. It leaves the
hand-crafted `bundle.Dockerfile` and `bundle/metadata/` untouched
(operator-sdk runs with `--overwrite=false`).

Run it whenever anything under `config/` changes: API fields, CRDs, RBAC,
webhooks or the manager deployment. The bundle CRD and CSV RBAC rules are
what OLM installs, so regenerating from a stale `config/` ships an outdated
schema and permissions. Commit the regenerated files with the `config/`
change.

The operator image written into the CSV comes from `IMG`, which defaults to the
development image (`$(IMAGE_TAG_BASE):v$(VERSION)`). If you only want to sync
the manifests with `config/`, pin it to the image already in the bundle:

```
make bundle IMG="$(yq '.spec.install.spec.deployments[0].spec.template.spec.containers[]
    | select(.name == "manager").image' \
    bundle/manifests/sandboxed-containers-operator.clusterserviceversion.yaml)"
```

operator-sdk stamps `spec.createdAt` in the CSV with the current time, so every
run produces a diff in that line. `hack/verify-bundle.sh` restores the
committed value after regenerating, so verification only reports real drift.

### CRDs, RBAC and deepcopy

- `make manifests` regenerates the CRDs in `config/crd/bases` and the RBAC and
  webhook manifests in `config/rbac` and `config/webhook`.
- `make generate` regenerates the `zz_generated.deepcopy.go` files.
- `go mod tidy` (root module) and `go mod tidy -C ./test/e2e` regenerate
  `go.mod` and `go.sum`.

## Verifying generated files

CI runs the same checks a developer should run before pushing: regenerate the
files and fail if the committed versions do not match.

```
make verify
```

`make verify` requires a clean working tree, because the checks regenerate
files in place. Pre-existing untracked files are ignored; the checks fail if
regeneration changes a tracked file or creates a new one. The individual
checks can also be run directly:

- `make verify-fmt` - checks that the Go sources are gofmt-formatted, without
  rewriting them.
- `hack/verify-deepcopy.sh` - runs `make generate`.
- `hack/verify-bundle.sh` - runs `make bundle` with `IMG` pinned to the image
  already in the CSV and ignores the `createdAt` restamp.
- `hack/verify-deps.sh` - runs `go mod tidy` for the root and `test/e2e`
  modules.

If a check reports a diff, review it with `git diff` and commit the regenerated
files.

## Building Catalog image
```
make catalog-build
make catalog-push
```

## Installing the Operator using OpenShift Web console

### Create Custom Operator Catalog

Create a new `CatalogSource` yaml. Replace `user` with your quay.io user and
`version` with the operator version.

```
cat > my_catalog.yaml <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
 name:  my-operator-catalog
 namespace: openshift-marketplace
spec:
 displayName: My Operator Catalog
 sourceType: grpc
 image:  quay.io/${QUAY_USERID}/openshift-sandboxed-containers-operator-catalog:version
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

### Adding new containers to OSC

When adding a new container definition in some pod yaml, make sure to tag the `image`
field with `  ## OSC_VERSION`, e.g.

```
image: registry.redhat.io/openshift-sandboxed-containers/osc-monitor-rhel9:1.14.0  ## OSC_VERSION
```

`RELATED_IMAGE_*` entries in the environment of the controller in
`config/manager/manager.yaml` are pinned by image digest and refreshed by
dependency automation, so they do not carry the marker.

This is a best effort to track locations where OSC version bumps should happen.

### Updating versions

When starting a new version, several locations should be updated with the new version number :
- all the locations tagged with `  ## OSC_VERSION`; generated files under
  `bundle/` are not tagged, they pick up the version when `make bundle` runs
- the `metadata.name` and `spec.version` fields, and the `olm.skipRange`
  annotation, in `config/manifests/bases/sandboxed-containers-operator.clusterserviceversion.yaml`
- version labels in `Dockerfile`,  `config/peerpods/podvm/Dockerfile.podvm-builder` and `must-gather/Dockerfile`

The `spec.replaces` field in `config/manifests/bases/sandboxed-containers-operator.clusterserviceversion.yaml` should
be updated with the number of the latest officialy released version.


You can use this [script](../scripts/bump-osc-version.sh) to bump the version and to regenerate bundle (check usage
with `-h`).
