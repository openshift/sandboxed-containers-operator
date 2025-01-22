# Introduction

This is a brief readme explaining the usage of the podvm-builder scripts and
related files.  The scripts and related manifest files are primarily used by
the operator to generate a pod VM image.

## PodVM image generation configuration

The configuration used for the podvm image generation is available in the following configmaps:

- Azure: `azure-podvm-image-cm`
- AWS: `aws-podvm-image-cm`

If you want to change the default configuration, then depending on the cloud
provider (eg. aws or azure) you'll need to pre-create the respective
configmaps.  Please review and modify the settings in the configMap as
required.  For example, if you need to add NVIDIA GPU drivers in the podvm
image then set `ENABLE_NVIDIA_GPU: yes`. Likewise if you want to create image
for confidential containers then set `CONFIDENTIAL_COMPUTE_ENABLED: yes`.

Use the following command to create the configMap for AWS:

```sh
kubectl apply -f aws-podvm-image-cm.yaml
```

Use the following command to create the configMap for Azure:

```sh
kubectl apply -f azure-podvm-image-cm.yaml
```

Now when you create a KataConfig with `enablePeerPods: true` with empty
`AZURE_IMAGE_ID` or `AWS_AMI_ID` in `peer-pods-cm`, then depending on the cloud
provider configured, the operator will create the pod VM image based on the
provided config.

## PodVM Image Upload Configuration

The PodVM image can be embedded into a container image. This container image can then be unwrapped and uploaded to the libvirt volume specified in the `peer-pods-cm`. Please note that this feature is currently supported only for the libvirt provider.

To create an OCI image with the PodVM image, you can use the `Dockerfile.podvm-oci` as follows:

```bash
docker build -t podvm-libvirt \
    --build-arg PODVM_IMAGE_SRC=<podvm_image_source> \
    -f Dockerfile.podvm-oci .
```

In this context, `PODVM_IMAGE_SRC` refers to the location of the `qcow2` image on the host. Optionally, you can also set `PODVM_IMAGE_PATH`, which is the path of the qcow2 image inside the container. This path will be used as `<image_path>` in the `PODVM_IMAGE_URI` as described below.

`oci` is the only supported `image_repo_type` at present.

Ensure that `PODVM_IMAGE_URI` is configured in the `libvirt-podvm-image-cm` in the following format:

```bash
PODVM_IMAGE_URI: "<image_repo_type>::<image_repo_url>:<image_tag>::<image_path>"
```

For example:

```bash
PODVM_IMAGE_URI: "oci::quay.io/openshift_sandboxed_containers/libvirt-podvm-image:latest::/image/podvm-390x.qcow2"
```

In this example, `<image_tag>` and `<image_path>` are optional. If not provided, the default values will be `<image_tag>`: `latest` and `<image_path>`: `/image/podvm.qcow2`.

**Note:** When pulling container images from authenticated registries, make sure that the OpenShift `pull-secrets` are updated with the necessary registry credentials.
