package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

const (
	TimeTravelFeatureGate = "timeTravel"
	FgConfigMapName       = "osc-feature-gates"
)

var DefaultFeatureGates = map[string]bool{
	"timeTravel": false,
}

type FeatureGateStatus struct {
	FeatureGates map[string]bool
}

// Create enum to represent the state of the feature gates
type FeatureGateState int

const (
	Enabled FeatureGateState = iota
	Disabled
)

// This method returns a new FeatureGateStatus object
// that contains the status of the feature gates
// defined in the ConfigMap in the namespace
// Return default values if the ConfigMap is not found.
// Return values from the ConfigMap if the ConfigMap is not found. Use default values for missing entries in the ConfigMap.
// Return an error for any other reason, such as an API error.
func (r *KataConfigOpenShiftReconciler) NewFeatureGateStatus() (*FeatureGateStatus, error) {
	fgStatus := &FeatureGateStatus{
		FeatureGates: make(map[string]bool),
	}

	cfgMap := &corev1.ConfigMap{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: FgConfigMapName,
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

// Function to handle the feature gates
func (r *KataConfigOpenShiftReconciler) processFeatureGates() error {

	fgStatus, err := r.NewFeatureGateStatus()
	if err != nil {
		r.Log.Info("There were errors in getting feature gate status.", "err", err)
		return err
	}

	// Check which feature gates are enabled in the FG ConfigMap and
	// perform the necessary actions

	if IsEnabled(fgStatus, TimeTravelFeatureGate) {
		r.Log.Info("Feature gate is enabled", "featuregate", TimeTravelFeatureGate)
		// Perform the necessary actions
		r.handleTimeTravelFeature(Enabled)
	} else {
		r.Log.Info("Feature gate is disabled", "featuregate", TimeTravelFeatureGate)
		// Perform the necessary actions
		r.handleTimeTravelFeature(Disabled)
	}

	return err

}

// Function to handle the TimeTravel feature gate
func (r *KataConfigOpenShiftReconciler) handleTimeTravelFeature(state FeatureGateState) {
	// Perform the necessary actions for the TimeTravel feature gate
	if state == Enabled {
		r.Log.Info("Starting TimeTravel")
	} else {
		r.Log.Info("Stopping TimeTravel")
	}
}
