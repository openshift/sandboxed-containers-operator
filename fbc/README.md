# OSC FBC

The file based catalog (**FBC**) for OpenShift sandboxed containers.

## Install `opm`

You need v1.46.0 or greater.

Download the binary from [Github releases](https://github.com/operator-framework/operator-registry/releases).

## Add a previously released catalog

Set the version of OpenShift you're targeting:
```
OCP_VERSION=v4.17
mkdir ${OCP_VERSION}
cd ${OCP_VERSION}
```

Download the current index from the release registry:
```
opm render registry.redhat.io/redhat/redhat-operator-index:${OCP_VERSION} > index.json
```

Filter and keep just the "sandboxed-containers" part:
```
grep -A 30 -B 30 "sandboxed-containers" index.json > catalog.json
```

Inspect the head and the tail of the file to remove unwanted parts:
```
$EDITOR catalog.json
```

Move the catalog to the usual path:
```
mkdir -p catalog/sandboxed-containers-operator/
mv catalog.json catalog/sandboxed-containers-operator/
```

Generate a catalog template:
```
opm alpha convert-template basic catalog/sandboxed-containers-operator/catalog.json > catalog-template.json
```

Generate a Dockerfile:
```
opm generate dockerfile . \
    --base-image "brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:${OCP_VERSION}" \
    --builder-image "brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:${OCP_VERSION}"
```

Patch the Dockerfile to avoid copying in unwanted files:
```diff
-ADD . /configs
+ADD catalog/ /configs
```

## Further reading

  - [File-based Catalogs](https://olm.operatorframework.io/docs/reference/file-based-catalogs/) in the Operator Lifecycle Manager (OLM) documentation.
