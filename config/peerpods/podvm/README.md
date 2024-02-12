# Introduction

This is a brief readme explaining the usage of the podvm-builder scripts and related files

## Create PodVM image generation configuration

The configuration used for the podvm image generation is available in the following configmaps:

- Azure: `azure-podvm-image-cm`
- AWS: `aws-podvm-image-cm`

Depending on the cloud provider (eg. aws or azure) create the respective
configmaps. Please review and modify the settings in the configMap as required.

For AWS

```sh
kubectl apply -f aws-podvm-image-cm.yaml
```

For Azure

```sh
kubectl apply -f azure-podvm-image-cm.yaml
```

## Create podvm image

The podvm image is created in a Kubernetes job. To create the job run the following command

```sh
kubectl apply -f osc-podvm-create-job.yaml
```

On successful image creation, the podvm image details will be updated as an annotation in the `peer-pods-cm`
under `openshift-sandboxed-containers-operator` namespace.

The annotation key for AWS is `LATEST_AMI_ID` and for Azure it's `LATEST_IMAGE_ID`

## Delete podvm image

Update the IMAGE_ID for Azure or AMI_ID for AWS that you want to delete and then run the following command

```sh
kubectl delete -f osc-podvm-delete-job.yaml
```
