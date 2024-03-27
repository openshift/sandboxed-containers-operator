package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	yaml "github.com/ghodss/yaml"
	configv1 "github.com/openshift/api/config/v1"
	ccov1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	peerPodsSecretName = "peer-pods-secret"
)

// Define a struct to represent event information
type eventInfo struct {
	timestamp time.Time
	key       string
}

// Map to store the recently generated events
var eventCache = make(map[string]eventInfo)
var mutex = &sync.Mutex{}

// IsOpenShift detects if we are running in OpenShift using the discovery client
func IsOpenShift() (bool, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return false, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false, err
	}

	// Get a list of all API's on the cluster
	apiGroup, _, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		return false, err
	}

	for i := 0; i < len(apiGroup); i++ {
		if apiGroup[i].Name == "config.openshift.io" {
			return true, nil
		}
	}

	return false, nil
}

// method to use the relative path at test time
func pathFix(path string) string {
	if os.Getenv("TEST_USE_RELATIVE_PATH") == "true" {
		return filepath.Join("../", path)
	}
	return path
}

func parseJobYAML(yamlData []byte) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	err := yaml.Unmarshal(yamlData, job)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func readJobYAML(jobFileName string) ([]byte, error) {
	jobFilePath := filepath.Join(peerpodsImageJobsPathLocation, jobFileName)
	yamlData, err := os.ReadFile(jobFilePath)
	if err != nil {
		return nil, err
	}
	return yamlData, nil
}

func parseMachineConfigYAML(yamlData []byte) (*mcfgv1.MachineConfig, error) {
	machineConfig := &mcfgv1.MachineConfig{}
	err := yaml.Unmarshal(yamlData, machineConfig)
	if err != nil {
		return nil, err
	}
	return machineConfig, nil
}

func readMachineConfigYAML(mcFileName string) ([]byte, error) {
	machineConfigFilePath := filepath.Join(peerpodsMachineConfigPathLocation, mcFileName)
	yamlData, err := os.ReadFile(machineConfigFilePath)
	if err != nil {
		return nil, err
	}
	return yamlData, nil
}

func parseCredentialsRequestYAML(yamlData []byte) (*ccov1.CredentialsRequest, error) {
	credentialsRequest := &ccov1.CredentialsRequest{}
	err := yaml.Unmarshal(yamlData, credentialsRequest)
	if err != nil {
		return nil, err
	}
	return credentialsRequest, nil
}

func readCredentialsRequestYAML(crFileName string) ([]byte, error) {
	credentialsRequestsFilePath := filepath.Join(peerpodsCredentialsRequestsPathLocation, crFileName)
	yamlData, err := os.ReadFile(pathFix(credentialsRequestsFilePath))
	if err != nil {
		return nil, err
	}
	return yamlData, nil
}

// Method to read config map yaml

func readConfigMapYAML(cmFileName string) ([]byte, error) {
	configMapFilePath := filepath.Join(peerpodsImageJobsPathLocation, cmFileName)
	yamlData, err := os.ReadFile(configMapFilePath)
	if err != nil {
		return nil, err
	}
	return yamlData, nil
}

// Method to parse config map yaml
// Returns a pointer to a ConfigMap object and an error

func parseConfigMapYAML(yamlData []byte) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{}
	err := yaml.Unmarshal(yamlData, configMap)
	if err != nil {
		return nil, err
	}
	return configMap, nil
}

// Method to create Kubernetes event
// Input: clientset, event object, cachekey, createoptions
// The cache-key is used to avoid emitting frequent events for the same object
// Returns an error

func createKubernetesEvent(clientset *kubernetes.Clientset, event *corev1.Event, cacheKey string, createOptions metav1.CreateOptions) error {
	// Define the suppression duration for the event
	suppressionDuration := 2 * time.Minute

	// Check if an event with the same reason has been created recently
	mutex.Lock()
	if info, ok := eventCache[cacheKey]; ok {
		// Calculate the time elapsed since the last event with the same reason
		elapsedTime := time.Since(info.timestamp)
		// If less than a certain duration, suppress the event creation
		if elapsedTime < suppressionDuration {
			mutex.Unlock()
			return nil
		}
	}
	// Save the event information to the cache
	eventCache[cacheKey] = eventInfo{timestamp: time.Now(), key: event.Reason}
	mutex.Unlock()

	_, err := clientset.CoreV1().Events(event.InvolvedObject.Namespace).Create(context.TODO(), event, createOptions)
	if err != nil {
		return err
	}
	return nil
}

// Method to get peer-pods-secret object
func getPeerPodsSecret(c client.Client) (*corev1.Secret, error) {
	peerPodsSecret := &corev1.Secret{}

	err := c.Get(context.TODO(), types.NamespacedName{
		Name:      peerPodsSecretName,
		Namespace: "openshift-sandboxed-containers-operator",
	}, peerPodsSecret)

	if err != nil {
		return nil, err
	}

	return peerPodsSecret, nil
}

// Method to get cloud provider from infrastructure (lowercase)
func getCloudProviderFromInfra(c client.Client) (string, error) {
	infrastructure := &configv1.Infrastructure{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, infrastructure)
	if err != nil {
		return "", err
	}

	if infrastructure.Status.PlatformStatus == nil {
		return "", fmt.Errorf("Infrastructure.status.platformStatus is empty")
	}

	return strings.ToLower(string(infrastructure.Status.PlatformStatus.Type)), nil
}
