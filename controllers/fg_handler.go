package controllers

import (
	"github.com/openshift/sandboxed-containers-operator/internal/featuregates"
)

// Create enum to represent the state of the feature gates
type FeatureGateState int

const (
	Enabled FeatureGateState = iota
	Disabled
)

// Function to handle the feature gates
func (r *KataConfigOpenShiftReconciler) processFeatureGates() error {

	fgStatus, err := featuregates.NewFeatureGateStatus(r.Client)
	if err != nil {
		r.Log.Info("There were errors in getting feature gate status.", "err", err)
		return err
	}

	// Check which feature gates are enabled in the FG ConfigMap and
	// perform the necessary actions
	// The feature gates are defined in internal/featuregates/featuregates.go
	// and are fetched from the ConfigMap in the namespace
	// Eg. TimeTravelFeatureGate

	if featuregates.IsEnabled(fgStatus, featuregates.TimeTravelFeatureGate) {
		r.Log.Info("Feature gate is enabled", "featuregate", featuregates.TimeTravelFeatureGate)
		// Perform the necessary actions
		r.handleTimeTravelFeature(Enabled)
	} else {
		r.Log.Info("Feature gate is disabled", "featuregate", featuregates.TimeTravelFeatureGate)
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
