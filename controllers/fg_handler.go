package controllers

import (
	"context"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

const (
	FgConfigMapName         = "osc-feature-gates"
	ConfidentialFeatureGate = "confidential"
	LayeredImageDeployment  = "layeredImageDeployment"
	DeploymentModeConfig    = "deploymentMode"
)

var DefaultFeatureGates = FeatureGateStatus{
	Confidential:           false,
	LayeredImageDeployment: false,
	DeploymentModeOption:   MachineConfigOption,
}

type FeatureGateStatus struct {
	Confidential           bool
	LayeredImageDeployment bool
	DeploymentModeOption   DeploymentModeOption
}

// Create enum to represent the state of the feature gates
// While today we just have two states, we retain the flexibility in case we want to introduce some additional states.
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
		Confidential:           DefaultFeatureGates.Confidential,
		LayeredImageDeployment: DefaultFeatureGates.LayeredImageDeployment,
		DeploymentModeOption:   DefaultFeatureGates.DeploymentModeOption,
	}

	cfgMap := &corev1.ConfigMap{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: FgConfigMapName,
		Namespace: OperatorNamespace}, cfgMap)
	if err == nil {
		if value, ok := cfgMap.Data[ConfidentialFeatureGate]; ok {
			confidential, err := strconv.ParseBool(value)
			if err != nil {
				r.Log.Info("Couldn't parse confidential status, using default value", "default", DefaultFeatureGates.Confidential, "error", err)
			} else {
				fgStatus.Confidential = confidential
			}
		}
		if value, ok := cfgMap.Data[LayeredImageDeployment]; ok {
			layeredImageDeployment, err := strconv.ParseBool(value)
			if err != nil {
				r.Log.Info("Couldn't parse layeredImageDeployment status, using default value", "default", DefaultFeatureGates.LayeredImageDeployment, "error", err)
			} else {
				fgStatus.LayeredImageDeployment = layeredImageDeployment
			}
		}
		if value, ok := cfgMap.Data[DeploymentModeConfig]; ok {
			mode, err := ParseDeploymentModeOption(value)
			if err != nil {
				r.Log.Info("Couldn't parse deploymentMode status, using default value", "default", DefaultFeatureGates.DeploymentModeOption, "error", err)
			} else {
				fgStatus.DeploymentModeOption = mode
			}
		}
	}

	if k8serrors.IsNotFound(err) {
		return fgStatus, nil
	} else {
		return fgStatus, err
	}
}

var statusChecker = map[string]func(fgstatus *FeatureGateStatus) bool{
	ConfidentialFeatureGate: func(fgstatus *FeatureGateStatus) bool { return fgstatus.Confidential },
	LayeredImageDeployment:  func(fgstatus *FeatureGateStatus) bool { return fgstatus.LayeredImageDeployment },
}

func (fgstatus *FeatureGateStatus) IsEnabled(key string) bool {
	if checkStatus, ok := statusChecker[key]; ok {
		return checkStatus(fgstatus)
	}
	return false
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
	if r.kataConfig.Spec.EnablePeerPods {
		if fgStatus.IsEnabled(ConfidentialFeatureGate) {
			r.Log.Info("Feature gate is enabled", "featuregate", ConfidentialFeatureGate)
			// Perform the necessary actions
			if err := r.handleFeatureConfidential(Enabled); err != nil {
				return err
			}
		} else {
			r.Log.Info("Feature gate is disabled", "featuregate", ConfidentialFeatureGate)
			// Perform the necessary actions
			if err := r.handleFeatureConfidential(Disabled); err != nil {
				return err
			}
		}
	}

	if err := r.handleDeploymentModeFeature(fgStatus.DeploymentModeOption); err != nil {
		return err
	}

	if r.DeploymentMode == DaemonSetMode {
		r.Log.Info("Skipping layered image deployment feature, not using MCO")
		return nil
	}

	// Check layered Image deployment FG
	if fgStatus.IsEnabled(LayeredImageDeployment) {
		r.Log.Info("Feature gate is enabled", "featuregate", LayeredImageDeployment)
		// Perform the necessary actions
		return r.handleLayeredImageDeploymentFeature(Enabled)
	} else {
		r.Log.Info("Feature gate is disabled", "featuregate", LayeredImageDeployment)
		// Perform the necessary actions
		return r.handleLayeredImageDeploymentFeature(Disabled)
	}

}
