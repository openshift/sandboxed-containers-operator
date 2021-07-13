# OpenShift sandboxed containers must-gather

`must-gather` is a tool built on top of [OpenShift must-gather](https://github.com/openshift/must-gather)
that expands its capabilities to gather sandboxed containers information.

### Usage
```sh
oc adm must-gather --image=quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-must-gather
```

The command above will create a local directory with a dump of the OpenShift sandboxed-containers state.
Note that this command will only get data related to the sandboxed-containers part of the OpenShift cluster.

You will get a dump of:
- All namespaces (and their children objects) that belong to any sandboxed containers resources

In order to get data about other parts of the cluster (not specific to sandboxed containers) you should
run `oc adm must-gather` (without passing a custom image). Run `oc adm must-gather -h` to see more options.

### Development
You can build the image locally using the Dockerfile included.

A `makefile` is also provided. To use it, you must pass a repository via the command-line using the variable `MUST_GATHER_IMAGE`.
You can also specify the registry using the variable `IMAGE_REGISTRY` (default is [quay.io](https://quay.io)) and the tag via `IMAGE_TAG` (default is `latest`).

The targets for `make` are as follows:
- `build`: builds the image with the supplied name and pushes it
- `podman-build`: builds the image but does not push it
- `podman-push`: pushes an already-built image

For example:
```sh
make build MUST_GATHER_IMAGE=openshift_sandboxed_containers/openshift-sandboxed-containers-must-gather
```
would build the local repository as `quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-must-gather:latest` and then push it.
