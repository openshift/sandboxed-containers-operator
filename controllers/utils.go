package controllers

import (
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
