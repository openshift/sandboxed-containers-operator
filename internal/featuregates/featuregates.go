package featuregates

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	TimeTravelFeatureGate = "timeTravel"
	FgConfigMapName       = "osc-feature-gates"
	OperatorNamespace     = "openshift-sandboxed-containers-operator"
)

var DefaultFeatureGates = map[string]bool{
	"timeTravel": false,
}

type FeatureGateStatus struct {
	FeatureGates map[string]bool
}

// This method returns a new FeatureGateStatus object
// that contains the status of the feature gates
// defined in the ConfigMap in the namespace
// Return default values if the ConfigMap is not found.
// Return values from the ConfigMap if the ConfigMap is not found. Use default values for missing entries in the ConfigMap.
// Return an error for any other reason, such as an API error.
func NewFeatureGateStatus(client client.Client) (*FeatureGateStatus, error) {
	fgStatus := &FeatureGateStatus{
		FeatureGates: make(map[string]bool),
	}

	cfgMap := &corev1.ConfigMap{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: FgConfigMapName,
		Namespace: OperatorNamespace}, cfgMap)
	if err == nil {
		for feature, value := range cfgMap.Data {
			fgStatus.FeatureGates[feature] = value == "true"
		}
	}

	// Add default values for missing feature gates
	for feature, defaultValue := range DefaultFeatureGates {
		if _, exists := fgStatus.FeatureGates[feature]; !exists {
			fgStatus.FeatureGates[feature] = defaultValue
		}
	}

	if k8serrors.IsNotFound(err) {
		return fgStatus, nil
	} else {
		return fgStatus, err
	}
}

func IsEnabled(fgStatus *FeatureGateStatus, feature string) bool {

	return fgStatus.FeatureGates[feature]
}
