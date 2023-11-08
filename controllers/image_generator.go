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
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"

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
Make sure
Ensure that all job YAML files meet the following guidelines:
1. Job files should be located at config/peerpods/
2. all file names must follow this format: <cloud-provider>-[CVM|VM]-image-[create|delete]-job.yaml
3. parallelism: 1
4. completions: 1 (or none)
5. backoffLimit: 1
6. restartPolicy: Never
7. job and pod names describes cloud-provider and operation
8. Get configuration values from peer-pods-secret and peer-pods-cm volumes and predefined enviroment variables
9. make sure create/delete jobs are based on the same configuration sources
10. utilize precreated podvm binaries conatiner if possible
11. all jobs must abort on image creation failure

Every image creation job must obey to the following guidelines:
1. create the podvm image and all other required cloud resouces needed for it
2. Create a container named result that outputs the image ID (and nothing but the image ID) once the image is ready.
3. image ID can be shared between containers using emptyDir volume

Every image deletion job should preform the following:
1. based on the exect same configurtion creation job is using
2. deletes the image and all created resouces
*/

const (
	peerpodsCMName          = "peer-pods-cm"
	peerpodsCMAWSImageKey   = "PODVM_AMI_ID"
	peerpodsCMAzureImageKey = "AZURE_IMAGE_ID"
	fipsCMKey               = "BOOT_FIPS"
	defaultVMType           = "VM"
)

type ImageGenerator struct {
	client    client.Client        // controller-runtime client
	clientset kubernetes.Interface // k8s clientset

	provider     string
	CMimageIDKey string
	fips         bool
}

var ig *ImageGenerator = nil
var igLogger logr.Logger = ctrl.Log.WithName("image-generator")

// ImageCreate creates a podvm image for a cloud provider if not present
func ImageCreate(c client.Client) (bool, ctrl.Result) {
	if ig == nil { // initialize ImageGenerator if not exsits
		var err error
		ig, err = newImageGenerator(c)
		if err != nil || ig == nil {
			igLogger.Info("error initializing ImageGenerator instance", "err", err)
			return false, ctrl.Result{Requeue: true}
		}
	}
	return ig.imageJobRunner("create")
}

// ImageDelete deletes a podvm image for a cloud provider if present
func ImageDelete(c client.Client) (bool, ctrl.Result) {
	if ig == nil { // initialize ImageGenerator if not exsits
		var err error
		ig, err = newImageGenerator(c)
		if err != nil || ig == nil {
			igLogger.Info("error initializing ImageGenerator instance", "err", err)
			return false, ctrl.Result{Requeue: true}
		}
	}
	return ig.imageJobRunner("delete")
}

func newImageGenerator(client client.Client) (*ImageGenerator, error) {
	var ig ImageGenerator
	var procFIPS = "/proc/sys/crypto/fips_enabled"

	ig.client = client
	ig.provider = ""     // set by setupCloudProvider
	ig.CMimageIDKey = "" // set by setupCloudProvider

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s clientset: %v", err)
	}
	ig.clientset = clientset

	content, err := os.ReadFile(procFIPS)
	if err != nil {
		return nil, fmt.Errorf("failed to read FIPS file: %v", err)
	}

	fips, err := strconv.Atoi(strings.Trim(string(content), "\n\t "))
	if err != nil {
		return nil, fmt.Errorf("failed to convert FIPS file content to int: %v", err)
	}
	ig.fips = fips == 1

	err = ig.setupCloudProvider()
	if err != nil {
		return nil, fmt.Errorf("failed to setup cloud provider: %v", err)
	}

	igLogger.Info("ImageGenerator instance has been initialized successfully", "fips", ig.fips)
	return &ig, nil
}

func (r *ImageGenerator) getCloudProviderFromInfra() string {
	// TODO: first check if it's indeed openshift
	infrastructure := &configv1.Infrastructure{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, infrastructure)
	if err != nil {
		igLogger.Info("getCloudProviderInfra: Error getting Infrastructure object", "err", err)
		return ""
	}

	if infrastructure.Status.PlatformStatus == nil {
		igLogger.Info("getCloudProviderInfra: Infrastructure.status.platformStatus is empty")
		return ""
	}

	igLogger.Info("Got cloud provider from infrastructure object")
	return strings.ToLower(string(infrastructure.Status.PlatformStatus.Type))
}

func (r *ImageGenerator) setupCloudProvider() error {
	provider := r.getCloudProviderFromInfra()

	switch provider {
	case "aws":
		r.CMimageIDKey = peerpodsCMAWSImageKey
	case "azure":
		r.CMimageIDKey = peerpodsCMAzureImageKey
	default:
		return fmt.Errorf("getCloudProvider: Unsupported cloud provider: %s", provider)
	}
	r.provider = provider

	igLogger.Info("Cloud provider fetched successfully", "provider", r.provider, "keyID", r.CMimageIDKey)
	return nil
}

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

	if err := r.client.Create(context.TODO(), job); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			igLogger.Info("Image Job already exists, get existing object", "jobFileName", jobFileName)
			latest := &batchv1.Job{}
			if err := r.client.Get(context.TODO(), types.NamespacedName{
				Name:      job.Name,
				Namespace: job.Namespace,
			}, latest); err != nil {
				igLogger.Info("Image Job already exists, getting existing object failed", "jobFileName", jobFileName)
				return nil, err
			}
			return latest, nil
		} else {
			return nil, err
		}
	}
	igLogger.Info("Job file has been processed successfully and Job has been created")
	return job, nil
}

func (r *ImageGenerator) deleteJobFromFile(jobFileName string, keepPods bool) error {
	igLogger.Info("delete job from YAML file", "jobFileName", jobFileName)
	yamlData, err := readJobYAML(jobFileName)
	if err != nil {
		return err
	}

	job, err := parseJobYAML(yamlData)
	if err != nil {
		return err
	}

	var deletePolicy metav1.DeletionPropagation
	if keepPods {
		deletePolicy = metav1.DeletePropagationOrphan // this leaves the job's pods around for tracking
	} else {
		deletePolicy = metav1.DeletePropagationBackground // this deletes the job's pods as well
	}

	if err := r.client.Delete(context.TODO(), job, &client.DeleteOptions{PropagationPolicy: &deletePolicy}); err != nil {
		if k8serrors.IsNotFound(err) {
			igLogger.Info("Image Job doesn't exist, nothing to delete", "jobFileName", jobFileName)
		} else {
			return err
		}
	}

	igLogger.Info("Job file has been processed successfully and Job has been deleted", "keepPods", keepPods)
	return nil
}

func (r *ImageGenerator) getPeerPodsCM() (*corev1.ConfigMap, error) {
	igLogger.Info("Getting peer-pods ConfigMap")
	peerPodsCM := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      peerpodsCMName,
		Namespace: "openshift-sandboxed-containers-operator",
	}, peerPodsCM)
	if err != nil {
		return nil, err
	}
	// TODO: check in peer-pods-secret as well?

	return peerPodsCM, nil
}

func (r *ImageGenerator) imageJobRunner(op string) (bool, ctrl.Result) {
	var job *batchv1.Job

	igLogger.Info("imageJobRunner", "operation", op)
	latestCM, err := r.getPeerPodsCM()
	if latestCM == nil || err != nil {
		igLogger.Info("error getting peer-pods ConfigMap", "err", err)
		return false, ctrl.Result{Requeue: true}
	}

	if op == "create" && latestCM.Data[r.CMimageIDKey] != "" {
		igLogger.Info("Image ID is already set, skipping image creation", "image-ID", latestCM.Data[r.CMimageIDKey])
		return true, ctrl.Result{Requeue: false}
	}

	if latestCM.Data == nil {
		latestCM.Data = make(map[string]string)
	}

	// set CM with BOOT_FIPS if system is in FIPS mode
	if op == "create" && latestCM.Data[fipsCMKey] == "" && r.fips {
		igLogger.Info("setting BOOT_FIPS in peer-pods ConfigMap")
		latestCM.Data[fipsCMKey] = "true"
		err = r.client.Update(context.TODO(), latestCM)
		if err != nil {
			igLogger.Info("error updating peer-pods ConfigMap with FIPS", "err", err)
			return false, ctrl.Result{Requeue: true}
		}
	}

	// file format: [azure|aws]-[CVM|VM]-image-[create|delete]-job.yaml
	VMtype := defaultVMType
	filename := r.provider + "-" + VMtype + "-image-" + op + "-job.yaml"
	job, err = r.createJobFromFile(filename)
	if err != nil {
		igLogger.Info("error creating image creation job from yaml file", "err", err)
		return false, ctrl.Result{Requeue: true}
	}

	if job.Status.Active == 0 && job.Status.Succeeded == 0 && job.Status.Failed == 0 { // job hasn't started yet
		igLogger.Info("JobStatus: Job hasn't started yet", "job name", job.Name)
		return false, ctrl.Result{RequeueAfter: 60 * time.Second}
	} else if job.Status.Failed > 0 { // job failed, delete job?
		igLogger.Info("JobStatus: Job has failed, delete job, keep pod for tracking", "job name", job.Name)
		if err := r.deleteJobFromFile(filename, true); err != nil {
			igLogger.Info("error deleting the job definition from yaml file", "err", err, "filename", filename)
		}
		return false, ctrl.Result{Requeue: true}
	} else if job.Status.Succeeded > 0 { // job completed, continue
		igLogger.Info("JobStatus: Job has completed successfully, fetch ImageID and add set in CM", "job name", job.Name)
	} else if job.Status.Active > 0 { // job is still running
		igLogger.Info("JobStatus: Job is still running", "job name", job.Name)
		return false, ctrl.Result{RequeueAfter: 60 * time.Second}
	} else {
		igLogger.Info("JobStatus: Unexpected Job Status!!!", "job name", job.Name)
		if err := r.deleteJobFromFile(filename, true); err != nil {
			igLogger.Info("error deleting the job definition from yaml file", "err", err, "filename", filename)
		}
		return false, ctrl.Result{Requeue: true}
	}

	imageID := ""
	if op == "create" {
		logs, err := r.getImageIDFromJobLogs(job)
		if err != nil || logs == "" {
			igLogger.Info("falied to get ImageID from logs", "err", err)
			if err := r.deleteJobFromFile(filename, true); err != nil {
				igLogger.Info("error deleting the job definition from yaml file", "err", err, "filename", filename)
			}
			return false, ctrl.Result{Requeue: true}
		}
		imageID = logs
	}

	latestCM.Data[r.CMimageIDKey] = imageID
	err = r.client.Update(context.TODO(), latestCM) // TODO: consider watching also CM from the controller
	if err != nil {
		igLogger.Info("error updating peer-pods ConfigMap imageID", "err", err)
		return false, ctrl.Result{Requeue: true}
	}

	if op == "delete" {
		igLogger.Info("image deletion job has been complated, deletes jobs and pods")
		if err := r.deleteJobFromFile(r.provider+"-"+VMtype+"-image-create-job.yaml", false); err != nil {
			igLogger.Info("error deleting the job create definition from yaml file", "err", err)
			return false, ctrl.Result{Requeue: true}
		}
		if err := r.deleteJobFromFile(r.provider+"-"+VMtype+"-image-delete-job.yaml", false); err != nil {
			igLogger.Info("error deleting the job delete definition from yaml file", "err", err)
			return false, ctrl.Result{Requeue: true}
		}
	}
	igLogger.Info("image ID has been "+op+"d"+" successfully for cloud provider", "provider", r.provider)
	return true, ctrl.Result{}
}

func (r *ImageGenerator) getImageIDFromJobLogs(job *batchv1.Job) (string, error) {
	labelSelector := labels.SelectorFromSet(map[string]string{"job-name": job.Name})
	listOpts := []client.ListOption{
		client.InNamespace(job.Namespace),
		client.MatchingLabelsSelector{Selector: labelSelector},
	}

	podList := &corev1.PodList{}
	err := r.client.List(context.TODO(), podList, listOpts...)
	if err != nil {
		igLogger.Info("Error in getting pod list", "err", err)
		return "", err
	}

	podLogOpts := corev1.PodLogOptions{Container: "result"}
	req := r.clientset.CoreV1().Pods(job.Namespace).GetLogs(podList.Items[0].Name, &podLogOpts)
	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return "", err
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", err
	}
	str := strings.TrimRight(buf.String(), "\r\n")

	if strings.Contains(str, " ") {
		return "", fmt.Errorf("getImageIDFromJobLogs: logs contains spaces, it's unexpected in image-ID something went wrong: %s", str)
	}

	igLogger.Info("Job's \"result\" container logs", "logs", str, "pod-name", podList.Items[0].Name)
	return str, nil
}
