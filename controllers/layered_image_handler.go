package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ignTypes "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	LayeredImageDeployCm = "layered-image-deploy-cm"
	image_mc_name        = "50-enable-sandboxed-containers-image"
)

// Process the LayeredImageDeployment feature gate (FG)
// This method will be called by the processFeatureGates method during the beginning of the reconcile loop
// The method will check for any existing MachineConfig related to KataConfig and return to the caller
// by setting r.ImgMc to the image MachineConfig if present. This will allow the remainder of the reconcile loop
// to use the image MachineConfig as needed.
// If neither extension or image MachineConfig exists, and layeredImageDeployment feature is enabled,
// then this method will create the image MachineConfig from the fg ConfigMap and set r.ImgMc to the
// newly created image MachineConfig.
// If layeredImageDeployment feature is disabled, then the method will reset r.ImgMc to nil
// The key design aspect of this FG is that it has effect only during the creation of the KataConfig.
// After creation of the KataConfig this FG has no effect.
func (r *KataConfigOpenShiftReconciler) handleLayeredImageDeploymentFeature(state FeatureGateState) error {

	// Check if MachineConfig exists and return the same without changing anything
	mc, err := r.getExistingMachineConfig()
	if err != nil {
		r.Log.Info("Error in getting existing MachineConfig", "err", err)
		return err
	}

	if mc != nil {
		r.Log.Info("MachineConfig is already present. No changes will be done")
		// If the MachineConfig is imageMachineConfig, then set r.ImgMc to the same
		if mc.Name == image_mc_name {
			r.ImgMc = mc
		}
		return nil
	}

	if state == Enabled {
		r.Log.Info("LayeredImageDeployment feature is enabled")

		cm := &corev1.ConfigMap{}
		err := r.Client.Get(context.Background(), types.NamespacedName{
			Name:      LayeredImageDeployCm,
			Namespace: OperatorNamespace,
		}, cm)
		if err != nil {
			r.Log.Info("Error in retrieving LayeredImageDeployment ConfigMap", "err", err)
			return err
		}

		// Set the ImgMc here
		r.ImgMc, err = r.createMachineConfigFromConfigMap(cm)
		if err != nil {
			r.Log.Info("Error in creating MachineConfig for LayeredImageDeployment from ConfigMap", "err", err)
			return err
		}

	} else {
		r.Log.Info("LayeredImageDeployment feature is disabled. Resetting ImgMc")
		// Reset ImgMc
		r.ImgMc = nil

	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) getExistingMachineConfig() (*mcfgv1.MachineConfig, error) {
	r.Log.Info("Getting any existing MachineConfigs related to KataConfig")

	// Retrieve the existing MachineConfig for Kata - either extension or image
	// Check for label "app":r.kataConfig.Name
	// and name "50-enable-sandboxed-containers-extension" or name "50-enable-sandboxed-containers-image"
	mcList := &mcfgv1.MachineConfigList{}
	err := r.Client.List(context.Background(), mcList)
	if err != nil {
		r.Log.Info("Error in listing MachineConfigs", "err", err)
		return nil, err
	}

	for _, mc := range mcList.Items {
		if mc.Labels["app"] == r.kataConfig.Name &&
			(mc.Name == extension_mc_name || mc.Name == image_mc_name) {
			return &mc, nil
		}
	}

	r.Log.Info("No existing MachineConfigs related to KataConfig found")

	return nil, nil
}

// Method to create a new MachineConfig object from configMap data
// The configMap data will have two keys: "osImageURL" and "kernelArgs"
func (r *KataConfigOpenShiftReconciler) createMachineConfigFromConfigMap(cm *corev1.ConfigMap) (*mcfgv1.MachineConfig, error) {

	// Get the osImageURL from the ConfigMap
	// osImageURL is mandatory for creating a MachineConfig
	osImageURL, exists := cm.Data["osImageURL"]
	if !exists {
		return nil, fmt.Errorf("osImageURL not found in ConfigMap")
	}

	ic := ignTypes.Config{
		Ignition: ignTypes.Ignition{
			Version: "3.2.0",
		},
	}

	icb, err := json.Marshal(ic)
	if err != nil {
		return nil, err
	}
	mc := &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      image_mc_name,
			Namespace: OperatorNamespace,
		},
		Spec: mcfgv1.MachineConfigSpec{
			Config: runtime.RawExtension{
				Raw: icb,
			},
			OSImageURL: osImageURL,
		},
	}

	if kernelArguments, ok := cm.Data["kernelArguments"]; ok {
		// Parse the kernel arguments and set them in the MachineConfig
		// Note that in the configmap the kernel arguments are stored as a single string
		// eg. "a=b c=d ..." and we need to split them into individual arguments
		// eg ["a=b", "c=d", ...]
		// Split the kernel arguments string into individual arguments
		mc.Spec.KernelArguments = strings.Fields(kernelArguments)
	}

	// Set the required labels
	mcp, err := r.getMcpName()
	if err != nil {
		return nil, err
	}
	mc.Labels = map[string]string{
		"machineconfiguration.openshift.io/role": mcp,
		"app":                                    r.kataConfig.Name,
	}

	return mc, nil
}
