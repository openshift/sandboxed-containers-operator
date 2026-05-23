package controllers

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Create enum to represent the state of the deployment mode
type DeploymentMode int

const (
	MachineConfigMode DeploymentMode = iota
	DaemonSetMode
)

// Create enum to represent the configuration of the deployment mode
type DeploymentModeOption string

const (
	MachineConfigOption     DeploymentModeOption = "MachineConfig"
	DaemonSetOption         DeploymentModeOption = "DaemonSet"
	DaemonSetFallbackOption DeploymentModeOption = "DaemonSetFallback"
)

const (
	machineConfigGroup   = "machineconfiguration.openshift.io"
	machineConfigVersion = "v1"
	machineConfigKind    = "MachineConfig"
)

func ParseDeploymentModeOption(s string) (DeploymentModeOption, error) {
	switch DeploymentModeOption(s) {
	case MachineConfigOption, DaemonSetOption, DaemonSetFallbackOption:
		return DeploymentModeOption(s), nil
	default:
		return "", fmt.Errorf("invalid DeploymentMode: %q", s)
	}
}

func (d DeploymentModeOption) String() string {
	return string(d)
}

// Process the DeploymentMode feature gate (FG).
// This method is invoked by the reconcile loop at its initiation.
// It examines the current state of the FeatureGate and adjusts the deployment mode (DaemonSet or MachineConfig)
// based on the selected DeploymentModeOption and the availability of the MachineConfig Add-on.
//
// The behavior is as follows:
//   - If the mode is MachineConfigOption, the deployment mode is set to MachineConfig.
//   - If the mode is DaemonSetOption, the deployment mode is forcibly set to DaemonSet, regardless of MachineConfig availability.
//   - If the mode is DaemonSetFallbackOption, the deployment mode is set to DaemonSet only if the MachineConfig Add-on is unavailable.
//     Otherwise, it defaults to MachineConfig.
func (r *KataConfigOpenShiftReconciler) handleDeploymentModeFeature(mode DeploymentModeOption) error {
	r.Log.Info("Feature gate", "featuregate", DeploymentModeConfig, "state", mode)

	machineConfigAvailable, err := r.isMachineConfigAvailable()
	if err != nil {
		r.Log.Info("Error checking if MachineConfig is available")
		return err
	}

	if mode == MachineConfigOption {
		if !machineConfigAvailable {
			return fmt.Errorf("deployment mode is set to MachineConfig, but it's not available")
		}

		r.Log.Info("Deployment mode will be set to MachineConfig")
		r.DeploymentMode = MachineConfigMode
		return nil
	}

	if mode == DaemonSetOption {
		r.Log.Info("Deployment mode will be set to DaemonSet")
		r.DeploymentMode = DaemonSetMode
		return nil
	}

	if mode == DaemonSetFallbackOption && !machineConfigAvailable {
		r.Log.Info("MachineConfig is not available, deployment mode will be set to DaemonSet")
		r.DeploymentMode = DaemonSetMode
	} else {
		r.Log.Info("Deployment mode will be set to MachineConfig")
		r.DeploymentMode = MachineConfigMode
	}

	return nil
}

// isMachineConfigAvailable checks if MachineConfig CRD is available.
//
// It returns a boolean indicating availability and an error if any.
func (r *KataConfigOpenShiftReconciler) isMachineConfigAvailable() (bool, error) {
	machineConfig := &unstructured.Unstructured{}
	machineConfig.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   machineConfigGroup,
		Version: machineConfigVersion,
		Kind:    machineConfigKind,
	})

	// Attempt to GET any MachineConfig to verify if the GVK is known.
	// If the CRD isn’t installed, it will return a NoMatchError.
	err := r.Get(context.Background(), client.ObjectKey{Name: "dummy-machine-config"}, machineConfig)
	if err == nil || k8serrors.IsNotFound(err) {
		r.Log.Info("MachineConfig CRD is present")
		return true, nil
	}

	if meta.IsNoMatchError(err) {
		r.Log.Info("MachineConfig CRD not found")
		return false, nil
	}

	return false, err
}
