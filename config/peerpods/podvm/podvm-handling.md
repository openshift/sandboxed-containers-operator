# Understanding pod VM image creation and deletion workflow in OSC operator

The `ImageGenerator` code builds and deletes pod VM images for a cloud
provider. It uses K8s jobs to perform these tasks.

For Azure, an image gallery is required to host the pod VM image. This is not
the case for AWS.

The configuration for the K8s jobs are provided via either `aws-podvm-image-cm`
configMap for AWS or `azure-podvm-image-cm` configMap for Azure respectively.
These configMaps can be created before `kataConfig` creation to enable custom
pod VM image creation. Otherwise the default configuration will be used when
generating the image.

If the `PODVM_AMI_ID` key for AWS provider or the  `AZURE_IMAGE_ID` key for
Azure provider is non-empty in the `peer-pods-cm` configMap then the pod VM
image creation process is triggered during `kataConfig` creation.  So if you
don't want to trigger the pod VM image creation process, then you can set the
respective keys to any dummy value (eg. "****"). Or you can set it to a
pre-created pod VM image.

Note that the OSC operator controller doesn't watch for changes to the
`peer-pods-cm` configMap.  However if the OSC operator reconcile loop is
entered due to the changes in `kataConfig` or node label changes, then the
image creation process may be re-triggered.

## Brief description of the K8s job manifests

`osc-podvm-image-creation.yaml`: This job manifest is used to create the pod VM
image.

The job is configured to run one pod at a time (`parallelism: 1`), and is
considered complete when one pod finishes successfully (`completions: 1`). If
the pod fails, K8s will retry the job once before marking it as failed
(`backoffLimit: 1`).

The job uses `peer-pods-secret` for cloud-provider credentials, and three
configMaps - `peer-pods-cm`, `azure-podvm-image-cm`, `aws-podvm-image-cm`.

The job's pod specification has an init container (**copy**) which uses the
`osc-podvm-payload-rhel9:latest` image. It runs a shell command to copy a file
(`/podvm-binaries.tar.gz`) from its own filesystem to a shared volume
(`/payload`). This shared volume is of `emptyDir` type.

The main container (create), uses the `osc-podvm-builder-rhel9:latest` image.
This image contains scripts and sources for handling pod VM image creation and
deletion in Azure and AWS. The container runs as root (`runAsUser: 0`). It also
mounts the shared volume (`/payload`) to access the file copied by the init
container.

The job is created by the OSC operator as part of the pod VM image creation
process. Note that OSC operator doesn't delete a failed or completed job pod
and it's available to view the logs if needed. The job will be automatically
garbage collected by K8s.

Note that the job manifest can also be used by admins to manually kickstart pod
VM image creation (eg. `oc apply -f osc-podvm-image-creation.yaml`)

`osc-podvm-image-deletion.yaml`: This job manifest is used to delete the pod VM
image.

The job is configured to run one pod at a time (`parallelism: 1`), and is
considered complete when one pod finishes successfully (`completions: 1`). If
the pod fails, K8s will retry the job once before marking it as failed
(`backoffLimit: 1`).

The job uses `peer-pods-secret` for cloud-provider credentials, and three
configMaps - `peer-pods-cm`, `azure-podvm-image-cm`, `aws-podvm-image-cm`.

The job's pod specification includes a single container (**delete**), which
uses the `osc-podvm-builder-rhel9:latest` image. The container runs as root
(`runAsUser: 0`) and has two environment variables, `AMI_ID` and `IMAGE_ID`,
which are used to specify the AWS AMI ID and Azure Image ID to delete,
respectively depending on the provider.

For Azure, when the job is executed by the OSC operator, the job deletes the
pod VM gallery hosting the image as well. This is to ensure that if the pod VM
image and gallery is created by the OSC operator during `kataConfig` creation,
then the same resources will be deleted during `kataConfig` deletion.

`osc-podvm-gallery-deletion.yaml`:  This job manifest is used to delete the
Azure image gallery.

The job is configured to run one pod at a time (`parallelism: 1`), and is
considered complete when one pod finishes successfully (`completions: 1`). If
the pod fails, K8s will retry the job once before marking it as failed
(`backoffLimit: 1`).

The job uses `peer-pods-secret` for cloud-provider credentials, and three
configMaps - `peer-pods-cm`, `azure-podvm-image-cm`, `aws-podvm-image-cm`.

The job's pod specification includes a single container (delete-gallery), which
uses the `osc-podvm-builder-rhel9:latest` image. The container runs as root
(`runAsUser: 0`).

This manifest is not used by the OSC operator. It's there as a helper manifest
to delete an image gallery manually. The default command is to forcefully
delete the image gallery defined in the `IMAGE_GALLERY_NAME` key in
`azure-podvm-image-cm` configMap.

## Pod VM image creation flow via OSC operator

* The code verifies all the required config parameters
* For Azure, if the `IMAGE_GALLERY_NAME` key in the `azure-podvm-image-cm`
  configMap is empty, then the OSC operator will update the value of the
  `IMAGE_GALLERY_NAME` key with the pattern `PodVMGallery_$clusterId` and this
  will be used as the gallery name. Note that only the first 8 chars of the
  OCP `clusterId` is used.
* Create the pod VM image creation job
* On successful pod VM image creation, the `PODVM_AMI_ID` key for AWS provider
  or the  `AZURE_IMAGE_ID` key for Azure provider in the `peer-pods-cm`
  configMap is updated. Also LATEST_AMI_ID (for AWS) or LATEST_IMAGE_ID and
  `IMAGE_GALLERY_NAME` (for Azure) annotations are added to the `peer-pods-cm`
  configMap

## Pod VM image deletion flow via OSC operator

* The code verifies all the required config parameters
* Create the pod VM image deletion job
* For Azure, the gallery is also deleted (by force). The gallery name is taken from
  the annotation `IMAGE_GALLERY_NAME` in the `peer-pods-cm` configMap
* On successful pod VM image deletion, the `PODVM_AMI_ID` key for AWS provider
  or the  `AZURE_IMAGE_ID` key for Azure provider in the `peer-pods-cm`
  configMap is updated with empty value (""). Also the annotations
  LATEST_AMI_ID (for AWS) or LATEST_IMAGE_ID and `IMAGE_GALLERY_NAME` (for
  Azure) are removed from the `peer-pods-cm` configMap
