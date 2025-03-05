# Image Mode (bootc) PodVM Builds

Image Mode podVM builds enable OSC users to create PodVM images based
on a RHEL bootc container image. The resulting artifact can be either
a podVM bootc container image or a podVM disk generated from that image.
OSC includes updates to the Image Creation mechanism, supporting the
conversion and upload of podVM images derived from pre-created bootc
(image mode) container images.
The following instructions outline both local and in-cluster options
for image creation.

## Create PodVM Bootc Container Image

### **Local** PodVM Bootc Container Image Build

Use Podman to build a podVM bootc image locally (push it to
a container registry of your choice if needed).
**NOTE:** setting CLOUD_PROVIDER & RHEL Subscription credentials are required only for Azure
```
IMG=quay.io/example/podvm-bootc
AUTHFILE=/path/to/pull-secret
podman build --authfile ${AUTHFILE} --build-arg CLOUD_PROVIDER=azure --build-arg ORG_ID=<org-id> --build-arg ACTIVATION_KEY=<key> -f Containerfile.rhel -t ${IMG}
#podman push ${IMG}
```

### **In-Cluster** PodVM Bootc Container Image Build

Use an existing OpenShift cluster to execute the container build
in-cluster and push the container image to the cluster's internal
registry:
**NOTE:** setting CLOUD_PROVIDER & RHEL Subscription credentials are required only for Azure
    see also: [Using RH subscriptions in builds]https://docs.openshift.com/container-platform/4.17/cicd/builds/running-entitled-builds.html
```
oc apply -f podvm-git-buildconfig.yaml
IMG_URI=bootc::image-registry.openshift-image-registry.svc:5000/openshift-sandboxed-containers-operator/podvm-bootc
```

## Convert Bootc Container Image to a PodVM Disk

### **Local** PodVM Disk Creation

Use [Bootc Image Builder](https://github.com/osbuild/bootc-image-builder)
to convert the created podVM bootc container image to a podVM disk file
**config.toml:** Use it to set custom bootc build configuration: https://osbuild.org/docs/bootc/#-build-config
```
# podman pull ${IMG} # optional
mkdir output
sudo podman run \
       -it --rm \
       --privileged \
       --security-opt label=type:unconfined_t \
       --authfile ${AUTHFILE} \
       -v $(pwd)/config.toml:/config.toml:ro \
       -v $(pwd)/output:/output \
       -v /var/lib/containers/storage:/var/lib/containers/storage \
       registry.redhat.io/rhel9/bootc-image-builder:latest \
       --type qcow2 \
       --rootfs xfs \
       --local \ # if pulled locally
       "${IMG}"
```
Artifact will be located at  output/qcow2/disk.qcow2
Upload it to your cloud-provider.

#### Leverge OSC to upload the disk to your cloud provider
1. bake an OCI container image with you disk file
2. push it to some container registry
3. set in the `<cloud-provider>-podvm-image-cm` IMAGE_TYPE: pre-built and PODVM_IMAGE_URI
that follows this format: `oci::<container-image-uri>::</absolute/path/to/disk>`

**AWS:** See [Bootc Image Builder Instructions for AMI artifact](https://github.com/osbuild/bootc-image-builder?tab=readme-ov-file#amazon-machine-images-amis)

### **In-Cluster** Podvm Disk & Image Creation

Provide the podVM bootc Container image and allow the operator
to handle the conversion, upload, creation, and configuration
of the podVM image within the cluster where your OSC is installed.

Once you have OSC operator installed and before applying KataConfig,
ensure your `<cloud-provider>-podvm-image-cm` values are configured
correctly:
```
IMAGE_TYPE: pre-built
PODVM_IMAGE_URI: ${IMG_URI}
# Custom bootc build configuration: https://osbuild.org/docs/bootc/#-build-config
# default is used if not set
BOOTC_BUILD_CONFIG: |  # Optional, custom bootc build configuration: https://osbuild.org/docs/bootc/#-build-config
  [[customizations.user]]
  name = "peerpod"
  password = "peerpod"
  key = "ssh-rsa AAAA..."
  groups = ["wheel", "root"]

  [[customizations.filesystem]]
  mountpoint = "/"
  minsize = "5 GiB"

  [[customizations.filesystem]]
  mountpoint = "/var/kata-containers"
  minsize = "15 GiB"
```

#### AWS specifics

In order to convert image to AMI (Amazon Machine Image) in-cluster you'll need:
* An existing s3 bucket in the region of your cluster
* Your cluster's AWS credntials needs to have the following [permissions](https://docs.aws.amazon.com/vm-import/latest/userguide/required-permissions.html#iam-permissions-image)
* [vmimport service role](https://docs.aws.amazon.com/vm-import/latest/userguide/required-permissions.html#vmimport-role) set
* The created bucket name needs to be specified in the aws-podvm-image-cm as follows:
```
BUCKET_NAME=<existing-bucket-name>
```

**NOTE:** you may use the [ami-helper.sh](../../../../scripts/ami-helper/ami-helper.sh) script to help and set the above requirements
