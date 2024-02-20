/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

/*
The image generator builds and deletes pod VM images for a cloud provider. It uses Kubernetes jobs to do this.
1. All manifests and related deps are located under config/peerpods/podvm
2. The job manifest uses the following format: osc-podvm-[create|delete]-job.yaml
3. The configuration values are taken from provider specific configMap: [provider]-podvm-image-cm.yaml
4. The created image details are updated in the peer-pods-cm configmap
*/

const (
	unsupportedCloudProvider      = "unsupported"
	peerpodsCMName                = "peer-pods-cm"
	peerPodsSecretName            = "peer-pods-secret"
	peerpodsCMAWSImageKey         = "PODVM_AMI_ID"
	peerpodsCMAzureImageKey       = "AZURE_IMAGE_ID"
	fipsCMKey                     = "BOOT_FIPS"
	procFIPS                      = "/proc/sys/crypto/fips_enabled"
	AWSProvider                   = "aws"
	AzureProvider                 = "azure"
	peerpodsImageJobsPathLocation = "/config/peerpods/podvm"
)

// Return values for ImageCreate and ImageDelete
const (
	ImageCreatedSuccessfully = iota
	ImageDeletedSuccessfully
	RequeueNeeded
	ImageJobRunning
	ImageJobCompleted
	ImageJobFailed
	ImageCreationInProgress
	ImageDeletionInProgress
	ImageCreationFailed        = -1
	ImageDeletionFailed        = -1
	CheckingJobStatusFailed    = -1
	ImageCreationStatusUnknown = -2
	ImageDeletionStatusUnknown = -2
)

// Custom error types
var (
	ErrInitializingImageGenerator = errors.New("error initializing ImageGenerator instance")
	ErrUnsupportedCloudProvider   = errors.New("unsupported cloud provider, skipping image creation")
	ErrValidatingPeerPodsConfigs  = errors.New("error validating peer-pods-cm and peer-pods-secret")
	ErrCreatingImageConfigMap     = errors.New("error creating podvm image configMap from file")
	ErrUpdatingImageConfigMap     = errors.New("error updating podvm image configMap")
	ErrCreatingImageJob           = errors.New("error creating image job from yaml file")
	ErrCheckingJobStatus          = errors.New("error checking job status")
	ErrDeletingJob                = errors.New("error deleting job")
)

// Event Constants for the PodVM Image Job
const (
	PodVMImageJobCompleted     = "PodVMImageJobCompleted"
	PodVMImageJobFailed        = "PodVMImageJobFailed"
	PodVMImageJobRunning       = "PodVMImageJobRunning"
	PodVMImageJobStatusUnknown = "PodVMImageJobStatusUnknown"
)

type ImageGenerator struct {
	client    client.Client         // controller-runtime client
	clientset *kubernetes.Clientset // k8s clientset

	provider     string
	CMimageIDKey string
	fips         bool
}

var (
	igOnce   sync.Once
	ig       *ImageGenerator
	igLogger logr.Logger = ctrl.Log.WithName("image-generator")
)

func InitializeImageGenerator(client client.Client) error {
	var err error
	igOnce.Do(func() {
		ig, err = newImageGenerator(client)
	})
	return err
}

// GetImageGenerator returns the global ImageGenerator instance
func GetImageGenerator() *ImageGenerator {
	return ig
}

// ImageCreate creates a podvm image for a cloud provider if not present
func ImageCreate(c client.Client) (int, error) {
	if err := InitializeImageGenerator(c); err != nil {
		igLogger.Info("error initializing ImageGenerator instance", "err", err)
		return ImageCreationFailed, ErrInitializingImageGenerator
	}

	ig := GetImageGenerator()
	if ig.provider == unsupportedCloudProvider {
		igLogger.Info("unsupported cloud provider, skipping image creation")
		return ImageCreationFailed, ErrUnsupportedCloudProvider
	}

	if err := ig.validatePeerPodsConfigs(); err != nil {
		igLogger.Info("error validating peer-pods-cm and peer-pods-secret", "err", err)
		return ImageCreationFailed, ErrValidatingPeerPodsConfigs
	}

	// Create required podvm image configMap
	if err := ig.createImageConfigMapFromFile(); err != nil {
		igLogger.Info("error creating podvm image configMap from file", "err", err)
		return ImageCreationFailed, ErrCreatingImageConfigMap
	}

	// Update imageConfigMap with FIPS or other required values
	if err := ig.updateImageConfigMap(); err != nil {
		igLogger.Info("error updating podvm image configMap", "err", err)
		return ImageCreationFailed, ErrUpdatingImageConfigMap
	}

	status, err := ig.imageCreateJobRunner()
	if err != nil {
		igLogger.Info("error running image create job", "err", err)
		return ImageCreationFailed, err
	}

	return status, nil

}

// ImageDelete deletes a podvm image for a cloud provider if present
func ImageDelete(c client.Client) (int, error) {
	if err := InitializeImageGenerator(c); err != nil {
		igLogger.Info("error initializing ImageGenerator instance", "err", err)
		return ImageDeletionFailed, ErrInitializingImageGenerator
	}

	ig := GetImageGenerator()
	if ig.provider == unsupportedCloudProvider {
		igLogger.Info("unsupported cloud provider, skipping image deletion")
		return ImageDeletionFailed, ErrUnsupportedCloudProvider
	}

	if err := ig.validatePeerPodsConfigs(); err != nil {
		igLogger.Info("error validating peer-pods configs", "err", err)
		return ImageDeletionFailed, ErrValidatingPeerPodsConfigs
	}

	status, err := ig.imageDeleteJobRunner()
	if err != nil {
		igLogger.Info("error running image delete job", "err", err)
		return ImageDeletionFailed, err
	}

	return status, nil

}

func newImageGenerator(client client.Client) (*ImageGenerator, error) {
	ig := &ImageGenerator{
		client: client,
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %v", err)
	}

	ig.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s clientset: %v", err)
	}

	content, err := os.ReadFile(procFIPS)
	if err != nil {
		return nil, fmt.Errorf("failed to read FIPS file: %v", err)
	}

	fips, err := strconv.Atoi(strings.Trim(string(content), "\n\t "))
	if err != nil {
		return nil, fmt.Errorf("failed to convert FIPS file content to int: %v", err)
	}
	ig.fips = fips == 1

	provider, err := ig.getCloudProviderFromInfra()
	if err != nil {
		return nil, fmt.Errorf("failed to get cloud provider from infra: %v", err)
	}

	switch provider {
	case AWSProvider:
		ig.CMimageIDKey = peerpodsCMAWSImageKey
		ig.provider = provider
	case AzureProvider:
		ig.CMimageIDKey = peerpodsCMAzureImageKey
		ig.provider = provider
	default:
		igLogger.Info("unsupported cloud provider, image creation will be disabled", "provider", ig.provider)
		ig.provider = unsupportedCloudProvider
		return nil, fmt.Errorf("unsupported cloud provider: %s", ig.provider)
	}

	igLogger.Info("ImageGenerator instance has been initialized successfully for cloud provider", "provider", ig.provider)
	return ig, nil
}

func (r *ImageGenerator) getCloudProviderFromInfra() (string, error) {
	// TODO: first check if it's indeed openshift
	infrastructure := &configv1.Infrastructure{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, infrastructure)
	if err != nil {
		return "", err
	}

	if infrastructure.Status.PlatformStatus == nil {
		return "", fmt.Errorf("Infrastructure.status.platformStatus is empty")
	}

	return strings.ToLower(string(infrastructure.Status.PlatformStatus.Type)), nil
}

// Method to create a Kubernetes Job from yaml file
func (r *ImageGenerator) createJobFromFile(jobFileName string) (*batchv1.Job, error) {
	igLogger.Info("Create Job out of YAML file", "jobFileName", jobFileName)

	yamlData, err := readJobYAML(jobFileName)
	if err != nil {
		return nil, err
	}

	job, err := parseJobYAML(yamlData)
	if err != nil {
		return nil, err
	}

	if jobFileName == "osc-podvm-delete-job.yaml" {
		// If delete job, then set env var IMAGE_ID or AMI_ID to the current image ID
		imageId, err := r.getImageID()
		if err != nil {
			return nil, err
		}
		igLogger.Info("Setting IMAGE_ID environment variable for delete job", "imageId", imageId)

		// If provider is Azure set IMAGE_ID, if provider is AWS set AMI_ID
		if r.provider == AzureProvider {

			job.Spec.Template.Spec.Containers[0].Env = append(job.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "IMAGE_ID",
				Value: imageId,
			})
		} else if r.provider == AWSProvider {
			job.Spec.Template.Spec.Containers[0].Env = append(job.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "AMI_ID",
				Value: imageId,
			})
		}

	}

	// If RELATED_PODVM_BUILDER_IMAGE environment variable is set, use it
	// Otherwise, use the default podvm image
	// There is only one container in the job, so we don't need to check the container name
	podvmBuilderImage := os.Getenv("RELATED_IMAGE_PODVM_BUILDER")
	if podvmBuilderImage != "" {
		igLogger.Info("Using podvm builder image from environment variable", "image", podvmBuilderImage)
		job.Spec.Template.Spec.Containers[0].Image = podvmBuilderImage
	}

	return job, nil
}

// Method to create job given job object
func (r *ImageGenerator) createJob(job *batchv1.Job) error {
	igLogger.Info("Creating Job", "jobName", job.Name, "namespace", job.Namespace)

	if err := r.client.Create(context.TODO(), job); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			igLogger.Info("Image Job already exists", "jobName", job.Name, "namespace", job.Namespace)
			return nil
		}
		return nil
	}
	igLogger.Info("Image job successfully created", "jobName", job.Name, "namespace", job.Namespace)
	return nil
}

// Method to delete job given job name and namespace
func (r *ImageGenerator) deleteJob(job *batchv1.Job) error {
	igLogger.Info("Deleting Job", "jobName", job.Name, "namespace", job.Namespace)

	// If the job has already been deleted, return nil
	// This is to avoid the error "job.batch "jobName" not found"
	// when the job has already been deleted

	// Intentionally not using metav1.DeletePropagationBackground as the delete policy
	// This will keep the completed job pods so that the logs can be checked if needed
	if err := r.client.Delete(context.TODO(), job, &client.DeleteOptions{}); err != nil {
		if k8serrors.IsNotFound(err) {
			igLogger.Info("Job has already been deleted", "jobName", job.Name, "namespace", job.Namespace)
			return nil
		}
		return err
	}

	igLogger.Info("Job has been deleted successfully", "jobName", job.Name, "namespace", job.Namespace)
	return nil
}

// Method to get peer-pods-cm object
func (r *ImageGenerator) getPeerPodsCM() (*corev1.ConfigMap, error) {
	peerPodsCM := &corev1.ConfigMap{}

	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      peerpodsCMName,
		Namespace: "openshift-sandboxed-containers-operator",
	}, peerPodsCM)

	if err != nil {
		return nil, err
	}

	return peerPodsCM, nil
}

// Method to get peer-pods-secret object
func (r *ImageGenerator) getPeerPodsSecret() (*corev1.Secret, error) {
	peerPodsSecret := &corev1.Secret{}

	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      peerPodsSecretName,
		Namespace: "openshift-sandboxed-containers-operator",
	}, peerPodsSecret)

	if err != nil {
		return nil, err
	}

	return peerPodsSecret, nil
}

// Method to run image creation job
// Calling this method assumes the following already done by the caller:
// peer-pods-cm and peer-pods-secret objects are valid
// cloud provider specific podvm image config is present
// azure-podvm-image-cm.yaml for Azure
// aws-podvm-image-cm.yaml for AWS

func (r *ImageGenerator) imageCreateJobRunner() (int, error) {
	igLogger.Info("imageCreateJobRunner: Start")

	// We create the job first irrespective of the image ID being set or not
	// This helps to handle the job deletion if the image ID is already set and entering here on requeue

	filename := "osc-podvm-create-job.yaml"

	job, err := r.createJobFromFile(filename)
	if err != nil {
		igLogger.Info("error creating the image creation job object from yaml file", "err", err)
		return ImageCreationFailed, ErrCreatingImageJob
	}

	// Handle job deletion if the image ID is already set and entering here on requeue
	if r.isImageIDSet() {
		igLogger.Info("Image ID is already set, skipping image creation")
		// Delete the job if it still exists
		if err := r.deleteJob(job); err != nil {
			igLogger.Info("Error deleting job", "err", err)
			return RequeueNeeded, err
		}

		return ImageCreatedSuccessfully, nil
	}

	// Create the job
	if err = r.createJob(job); err != nil {
		igLogger.Info("error creating the image creation job", "err", err)
		return RequeueNeeded, ErrCreatingImageJob
	}

	status, err := r.checkJobStatus(job.Name, job.Namespace)
	if err != nil {
		igLogger.Info("error checking job status", "err", err)
		return ImageCreationStatusUnknown, ErrCheckingJobStatus
	}

	// Handle different job statuses
	switch status {
	case ImageJobRunning:
		igLogger.Info("Image creation job is still running")
		return RequeueNeeded, nil
	case ImageJobCompleted:
		// If job completed successfully but image ID is not set, requeue
		if !r.isImageIDSet() {
			igLogger.Info("Image creation job has completed and image ID is not set, requeueing")
			return RequeueNeeded, nil
		}
		// Delete the job as it's no longer needed
		if err := r.deleteJob(job); err != nil {
			igLogger.Info("Error deleting job", "err", err)
			return RequeueNeeded, err
		}
		igLogger.Info("Image creation job has completed successfully and image ID is set")
		return ImageCreatedSuccessfully, nil
	case ImageJobFailed:
		// If job failed, don't requeue
		igLogger.Info("Image creation job has failed")

		// Delete the job as it's no longer needed
		if err := r.deleteJob(job); err != nil {
			igLogger.Info("Error deleting job", "err", err)
			return RequeueNeeded, err
		}
		return ImageCreationFailed, nil
	default:
		// Handle unknown job status
		igLogger.Info("Unknown job status", "status", status)
		return RequeueNeeded, nil
	}
}

// Method to run image deletion job
// Calling this method assumes the following already done by the caller:
// peer-pods-cm and peer-pods-secret objects are valid
// cloud provider specific podvm image config is present
// azure-podvm-image-cm.yaml for Azure
// aws-podvm-image-cm.yaml for AWS
// Successful deletion removes the image ID from peer-pods-cm

func (r *ImageGenerator) imageDeleteJobRunner() (int, error) {
	igLogger.Info("imageDeleteJobRunner: Start")

	// file format: osc-podvm-[create|delete]-job.yaml
	filename := "osc-podvm-delete-job.yaml"

	job, err := r.createJobFromFile(filename)
	if err != nil {
		igLogger.Info("error creating image deletion job from yaml file", "err", err)
		return ImageDeletionFailed, ErrCreatingImageJob
	}

	// Handle job deletion if the image ID is not set and entering here on requeue
	if !r.isImageIDSet() {
		igLogger.Info("Image ID is not set, skipping image deletion")
		// Delete the job if it still exists
		if err := r.deleteJob(job); err != nil {
			igLogger.Info("Error deleting job", "err", err)
			return RequeueNeeded, err
		}

		return ImageDeletedSuccessfully, nil
	}

	// Create the job
	if err = r.createJob(job); err != nil {
		igLogger.Info("error creating the image deletion job", "err", err)
		return RequeueNeeded, ErrCreatingImageJob
	}

	status, err := r.checkJobStatus(job.Name, job.Namespace)
	if err != nil {
		igLogger.Info("error checking job status", "err", err)
		return ImageDeletionStatusUnknown, ErrCheckingJobStatus
	}

	// Handle different job statuses
	switch status {
	case ImageJobRunning:
		igLogger.Info("Image deletion job is still running")
		return RequeueNeeded, nil
	case ImageJobCompleted:
		// If job completed successfully but image ID is still set, requeue
		if r.isImageIDSet() {
			igLogger.Info("Image deletion job has completed and image ID is still set, requeueing")
			return RequeueNeeded, nil
		}
		// Delete the job as it's no longer needed
		if err := r.deleteJob(job); err != nil {
			igLogger.Info("Error deleting job", "err", err)
			return RequeueNeeded, err
		}
		igLogger.Info("Image deletion job has completed successfully and image ID is not set")
		return ImageDeletedSuccessfully, nil
	case ImageJobFailed:
		// If job failed, don't requeue
		igLogger.Info("Image deletion job has failed")

		// Delete the job as it's no longer needed
		if err := r.deleteJob(job); err != nil {
			igLogger.Info("Error deleting job", "err", err)
			return RequeueNeeded, err
		}
		return ImageDeletionFailed, nil
	default:
		// Handle unknown job status
		igLogger.Info("Unknown job status", "status", status)
		return RequeueNeeded, nil
	}
}

func (r *ImageGenerator) isImageIDSet() bool {
	peerPodsCM, err := r.getPeerPodsCM()
	if peerPodsCM == nil || err != nil {
		igLogger.Info("error getting peer-pods-cm ConfigMap", "err", err)
		return false
	}

	return peerPodsCM.Data[r.CMimageIDKey] != ""
}

// Method to get ImageID from peer-pods-cm
func (r *ImageGenerator) getImageID() (string, error) {
	peerPodsCM, err := r.getPeerPodsCM()
	if peerPodsCM == nil || err != nil {
		igLogger.Info("error getting peer-pods-cm ConfigMap", "err", err)
		return "", err
	}

	return peerPodsCM.Data[r.CMimageIDKey], nil
}

func (r *ImageGenerator) validatePeerPodsConfigs() error {
	peerPodsCM, err := r.getPeerPodsCM()
	if err != nil || peerPodsCM == nil {
		return fmt.Errorf("validatePeerPodsConfigs: %v", err)
	}

	peerPodsSecret, err := r.getPeerPodsSecret()
	if err != nil || peerPodsSecret == nil {
		return fmt.Errorf("validatePeerPodsConfigs: %v", err)
	}

	// aws Secret Keys
	awsSecretKeys := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}
	// aws ConfigMap Keys
	awsConfigMapKeys := []string{"AWS_REGION", "AWS_SUBNET_ID", "AWS_VPC_ID", "AWS_SG_IDS", "CLOUD_PROVIDER"}
	// azure Secret Keys
	azureSecretKeys := []string{"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_SUBSCRIPTION_ID"}
	// azure ConfigMap Keys
	azureConfigMapKeys := []string{"AZURE_RESOURCE_GROUP", "AZURE_REGION", "CLOUD_PROVIDER"}

	// Check for each cloud provider if respective ConfigMap keys are present in the peerPodsConfigMap
	switch r.provider {
	case "aws":
		// Check if aws Secret Keys are present in the peerPodsSecret by calling checkKeysPresentAndNotEmpty
		if !checkKeysPresentAndNotEmpty(peerPodsSecret.Data, awsSecretKeys) {
			return fmt.Errorf("validatePeerPodsConfigs: cannot find the required keys in peer-pods-secret Secret")
		}

		// Check if aws ConfigMap Keys are present in the peerPodsConfigMap by calling checkKeysPresentAndNotEmpty
		if !checkKeysPresentAndNotEmpty(peerPodsCM.Data, awsConfigMapKeys) {
			return fmt.Errorf("validatePeerPodsConfigs: cannot find the required keys in peer-pods-cm ConfigMap")
		}

	case "azure":
		// Check if azure Secret Keys are present in the peerPodsSecret
		if !checkKeysPresentAndNotEmpty(peerPodsSecret.Data, azureSecretKeys) {
			return fmt.Errorf("validatePeerPodsConfigs: cannot find the required keys in peer-pods-secret Secret")
		}

		// Check if azure ConfigMap Keys are present in the peerPodsConfigMap
		if !checkKeysPresentAndNotEmpty(peerPodsCM.Data, azureConfigMapKeys) {
			return fmt.Errorf("validatePeerPodsConfigs: cannot find the required keys in peer-pods-cm ConfigMap")
		}

	default:
		return fmt.Errorf("validatePeerPodsConfigs: unsupported cloud provider %s", r.provider)
	}

	return nil
}

// Function which takes the following input
// map[string][]byte or map[string]string
// A list of keys to be searched in the map
// Checks if keys are present and value is not an empty string
// Returns true if all keys are present and value is not an empty string

func checkKeysPresentAndNotEmpty(data interface{}, keys []string) bool {
	// Convert the input map to a map[string]string
	var strMap map[string]string

	switch v := data.(type) {
	case map[string]string:
		strMap = v
	case map[string][]byte:
		strMap = make(map[string]string)
		for key, value := range v {
			strMap[key] = string(value)
		}
	default:
		// Unsupported type
		return false
	}

	// Check if the keys are present and have non-empty values
	for _, key := range keys {
		value, ok := strMap[key]
		if !ok || value == "" {
			// Log the key which is not present or has an empty value
			igLogger.Info("checkKeysPresentAndNotEmpty: key not present or has an empty value", "key", key)
			return false
		}
	}

	return true
}

// Function to create ConfigMap from a YAML file based on cloud provider
// azure-podvm-image-cm.yaml for Azure
// aws-podvm-image-cm.yaml for AWS
// Returns error if the ConfigMap creation fails

func (r *ImageGenerator) createImageConfigMapFromFile() error {
	// file format: [azure|aws]-podvm-image-cm.yaml
	// ConfigMap name: [azure|aws]-podvm-image-cm

	// Check if the ConfigMap already exists
	// If it exists, return nil
	// If it doesn't exist, create the ConfigMap

	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      r.provider + "-podvm-image-cm",
		Namespace: "openshift-sandboxed-containers-operator",
	}, &corev1.ConfigMap{}); err == nil {
		igLogger.Info("ConfigMap already exists", "name", r.provider+"-podvm-image-cm")
		return nil
	}

	filename := r.provider + "-podvm-image-cm.yaml"
	yamlData, err := readConfigMapYAML(filename)
	if err != nil {
		return err
	}

	cm, err := parseConfigMapYAML(yamlData)
	if err != nil {
		return err
	}

	if err := r.client.Create(context.TODO(), cm); err != nil {
		return err
	}

	igLogger.Info("ConfigMap has been created successfully", "filename", filename)
	return nil
}

// Method to update image configmap with FIPS or other required values
// azure-podvm-image-cm.yaml for Azure
// aws-podvm-image-cm.yaml for AWS
// Returns error if the update fails

func (r *ImageGenerator) updateImageConfigMap() error {
	// Check if the ConfigMap exists
	// If it doesn't exist, return an error
	// If it exists, update the ConfigMap

	cmName := r.provider + "-podvm-image-cm"
	cm := &corev1.ConfigMap{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      cmName,
		Namespace: "openshift-sandboxed-containers-operator",
	}, cm); err != nil {
		return err
	}

	if r.fips {
		// Check if the FIPS value is already set to true
		// If it is, return nil
		// If it isn't, update the ConfigMap
		if cm.Data[fipsCMKey] == "true" {
			igLogger.Info("FIPS value is already set to true", "name", cmName)
			return nil
		}

		// Update the ConfigMap with the FIPS value
		cm.Data[fipsCMKey] = "true"
		igLogger.Info("Setting FIPS mode")

		if err := r.client.Update(context.TODO(), cm); err != nil {
			return err
		}
	}

	igLogger.Info("ConfigMap has been updated successfully", "name", cmName)
	return nil

}

// hasJobFailed: checks if the job has failed
func (r *ImageGenerator) hasJobFailed(job *batchv1.Job) bool {
	// Job Conditions.Type: Failed, Status: True and Status.Failed > 0

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue && job.Status.Failed > 0 {
			return true
		}
	}
	return false
}

// isJobActive: checks if the job is still running
func (r *ImageGenerator) isJobActive(job *batchv1.Job) bool {
	// Conditions == empty, Status.Active > 0
	return len(job.Status.Conditions) == 0 && job.Status.Active > 0
}

// hasJobCompleted: checks if the job has completed
func (r *ImageGenerator) hasJobCompleted(job *batchv1.Job) bool {
	// Job Conditions.Type: Complete, Status: True and Status.Succeeded > 0

	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue && job.Status.Succeeded > 0 {
			return true
		}
	}
	return false
}

// Method to check the status of the job
// Success only when the job has completed successfully
func (r *ImageGenerator) checkJobStatus(jobName, namespace string) (int, error) {
	job, err := r.clientset.BatchV1().Jobs(namespace).Get(context.TODO(), jobName, metav1.GetOptions{})
	if err != nil {
		return CheckingJobStatusFailed, err
	}

	if r.hasJobFailed(job) {
		action := "Check the logs for the job"
		err = r.createJobEvent(namespace, jobName, "PodVMImageJobFailed",
			fmt.Sprintf("PodVM image job (%s) failed", jobName), corev1.EventTypeWarning, action)
		if err != nil {
			igLogger.Info("error creating event for failed Job", "job name", job.Name, "err", err)
		}
		return ImageJobFailed, nil
	}

	if r.hasJobCompleted(job) {
		igLogger.Info("JobStatus: Job has completed successfully", "job name", job.Name)
		action := "Check the pod vm image details in peer-pods-cm configmap"
		err = r.createJobEvent(namespace, jobName, "PodVMImageJobCompleted",
			fmt.Sprintf("PodVM image job (%s) completed successfully", jobName), corev1.EventTypeNormal, action)
		if err != nil {
			igLogger.Info("error creating event for completed Job", "job name", job.Name, "err", err)
		}
		return ImageJobCompleted, nil
	}

	if r.isJobActive(job) {
		igLogger.Info("JobStatus: Job is still running", "job name", job.Name)
		action := "Check the logs for the job"
		err = r.createJobEvent(namespace, jobName, "PodVMImageJobRunning",
			fmt.Sprintf("PodVM image job (%s) is running", jobName), corev1.EventTypeNormal, action)
		if err != nil {
			igLogger.Info("error creating event for running Job", "job name", job.Name, "err", err)
		}
		return ImageJobRunning, nil
	}

	return CheckingJobStatusFailed, fmt.Errorf("job status check failed %s", jobName)
}

// Method to create an event for the job
func (r *ImageGenerator) createJobEvent(namespace, jobName, reason, message, eventType, action string) error {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "job-event-",
			Namespace:    namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Job",
			Namespace: namespace,
			Name:      jobName,
		},
		Reason:              reason,
		Message:             message,
		Type:                eventType,
		ReportingController: "image-generator",
		ReportingInstance:   "controller-manager",
		Action:              action,
		EventTime:           metav1.NewMicroTime(time.Now()),
	}

	return createKubernetesEvent(r.clientset, event, reason, metav1.CreateOptions{})

}
