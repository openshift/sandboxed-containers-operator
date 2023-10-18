package controllers

import (
	"os"
	"path/filepath"

	yaml "github.com/ghodss/yaml"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

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
