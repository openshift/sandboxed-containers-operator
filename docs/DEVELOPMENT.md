# Hacking on the sandboxed-containers-operator

## Using a custom sandboxed-containers-operator-payload image

Sometimes we need to test new builds of RPMs, for example to
verify a bug in QEMU, kata-runtime or other packages.

The ConfigMap will be added to the Pod spec of the installer daemon pods as
an environment variable. If the variable is set the daemon will use it. If not
the default mechanism for choosing the payload image is used.

The purpose of this feature is for development purposes only. When the
ConfigMap is used the operator will print a warning that using self-built RPMs
taints the kataconfig installation.

To set a custom image create a configmap. Open the file deploy/configmap_payload.yaml and
change

    daemon.payload: quay.io/<username>/mykatapayload:mytag

## Payload container images in private repositories

When a payload image is stored in a private repository the daemon
needs to authenticate with the registry to be able to download it.

There are two environment variables defined in the daemons pod specification.
These variables are populated by a Kubernetes secret that the user can create.
It has to be created before the daemon pods are created.

Steps to use a payload image in a private repository:

1. deploy the operator as usual
2. create the payload configmap and set daemon.payload to the path in
   the private repository, for example
   quay.io/jensfr/sandboxed-containers-operator-payload:special
3. create the kubernetes secret with the credentials to above private
   repository. An example:

```
   apiVersion: v1
     kind: Secret
   metadata:
     name: payload-secret   <- has to have this exact name
   data:
     username: ajVXe2ZyCg=y <- base64 encoded
     password: emFmekIaOKMN <- base64 encoded
```

4. create the Kataconfig custom ressource. From here on the
   installation works as usual.

## How to create a custom payload container image

Based on an existing and known to work set of RPMs it is possible to replace
packages.

Note: it is not possible to add additonal RPMs this way
Note: the example below is using podman but docker could be used as well

An example:

1. skopeo copy docker://quay.io/jensfr/sandboxed-containers-operator-payload:4.7.0 oci:/tmp/sandboxed-containers-operator-payload:4.7.0
2. oci-image-tool unpack --ref name=4.7.0  /tmp/sandboxed-containers-operator-payload sandboxed-containers-operator-payload-unpacked-4.7.0
3. cp -r /tmp/sandboxed-containers-operator-payload-unpacked-4.7.0/packages $KATA_OPERATOR_REPO/images/payload
4. cd $KATA_OPERATOR_REPO/images/payload
5. replace RPMs in packages/ with custom RPMs
6. podman build --no-cache -f Dockerfile.custom quay.io/<username>/mykatapayload:mytag
7. podman push quay.io/<username>/mykatapayload:mytag

To use the custom payload container image use the payload-config configmap as described above
