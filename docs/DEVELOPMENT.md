# Hacking on the kata-operator

## Using a custom kata-operator-payload image

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

## How to create a custom payload container image

Based on an existing and known to work set of RPMs it is possible to replace
packages.

Note: it is not possible to add additonal RPMs this way
Note: the example below is using podman but docker could be used as well

An example:

1. skopeo copy docker://quay.io/jensfr/kata-operator-payload:4.6.0 oci:/tmp/kata-operator-payload:4.6.0
2. oci-image-tool unpack --ref name=4.6.0  /tmp/kata-operator-payload kata-operator-payload-unpacked-4.6.0
3. cp -r /tmp/kata-operator-payload-unpacked-4.6.0/packages $KATA_OPERATOR_REPO/images/payload
4. cd $KATA_OPERATOR_REPO/images/payload
5. replace RPMs in packages/ with custom RPMs
6. podman build --no-cache -f Dockerfile.custom quay.io/<username>/mykatapayload:mytag
7. podman push quay.io/<username>/mykatapayload:mytag

To use the custom payload container image use the payload-config configmap as described above
