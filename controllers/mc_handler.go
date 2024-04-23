package controllers

/*
This code handles the installation of Kata artifacts using an RHCOS extension or image and deploying it
via MachineConfig
*/

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ignTypes "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/openshift/sandboxed-containers-operator/internal/featuregates"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	extension_mc_name = "50-enable-sandboxed-containers-extension"
	image_mc_name     = "50-enable-sandboxed-containers-image"
)

type DeploymentState struct {
	PreviousMethod string
	PreviousMcName string
}

// If the first return value is 'true' it means that the MC was just created
// by this call, 'false' means that it's already existed.  As usual, the first
// return value is only valid if the second one is nil.
func (r *KataConfigOpenShiftReconciler) createMc(machinePool string) (bool, error) {

	// In case we're returning an error we want to make it explicit that
	// the first return value is "not care".  Unfortunately golang seems
	// to lack syntax for creating an expression with default bool value
	// hence this work-around.
	var dummy bool

	/* Create Machine Config object to deploy sandboxed containers artifacts */
	mcName := extension_mc_name
	if r.FeatureGatesStatus[featuregates.LayeredImageDeployment] {
		mcName = image_mc_name
	}

	r.Log.Info("Checking if MachineConfig exists", "mc.Name", mcName)
	mc := &mcfgv1.MachineConfig{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mcName}, mc)
	if err != nil && (k8serrors.IsNotFound(err) || k8serrors.IsGone(err)) {
		r.Log.Info("creating new MachineConfig")
		mc, err = r.newMCForCR(machinePool)
		if err != nil {
			return dummy, err
		}

		err = r.Client.Create(context.TODO(), mc)
		if err != nil {
			r.Log.Error(err, "Failed to create a new MachineConfig ", "mc.Name", mc.Name)
			return dummy, err
		}
		r.Log.Info("MachineConfig successfully created", "mc.Name", mc.Name)
		return true, nil
	} else if err != nil {
		r.Log.Info("failed to retrieve MachineConfig", "err", err)
		return dummy, err
	} else {
		r.Log.Info("MachineConfig already exists")
		return false, nil
	}
}

// Create a new MachineConfig object for the Custom Resource
func (r *KataConfigOpenShiftReconciler) newMCForCR(machinePool string) (*mcfgv1.MachineConfig, error) {
	r.Log.Info("Creating MachineConfig for Custom Resource")

	ic := ignTypes.Config{
		Ignition: ignTypes.Ignition{
			Version: "3.2.0",
		},
	}

	icb, err := json.Marshal(ic)
	if err != nil {
		return nil, err
	}

	if r.FeatureGatesStatus[featuregates.LayeredImageDeployment] {
		r.Log.Info("Creating MachineConfig for Custom Resource with image")
		return r.newImageMCForCR(machinePool, icb)
	} else {
		r.Log.Info("Creating MachineConfig for Custom Resource with extension")
		return r.newExtensionMCForCR(machinePool, icb)
	}

}

// Create a new MachineConfig object for the Custom Resource with the extension
func (r *KataConfigOpenShiftReconciler) newExtensionMCForCR(machinePool string, icb []byte) (*mcfgv1.MachineConfig, error) {
	extension := getExtensionName()

	mc := mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: extension_mc_name,
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": machinePool,
				"app":                                    r.kataConfig.Name,
			},
			Namespace: OperatorNamespace,
		},
		Spec: mcfgv1.MachineConfigSpec{
			Extensions: []string{extension},
			Config: runtime.RawExtension{
				Raw: icb,
			},
		},
	}

	return &mc, nil
}

// Create a new MachineConfig object for the Custom Resource with the image
func (r *KataConfigOpenShiftReconciler) newImageMCForCR(machinePool string, icb []byte) (*mcfgv1.MachineConfig, error) {

	// Get the LayeredImageDeployment feature gate parameters
	imageParams := r.FeatureGates.GetFeatureGateParams(context.TODO(), featuregates.LayeredImageDeployment)
	// Ensure at least osImageURL is set
	if _, ok := imageParams["osImageURL"]; !ok {
		return nil, fmt.Errorf("osImageURL not set in feature gate %s", featuregates.LayeredImageDeployment)
	}

	mc := mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: image_mc_name,
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": machinePool,
				"app":                                    r.kataConfig.Name,
			},
			Namespace: OperatorNamespace,
		},
		Spec: mcfgv1.MachineConfigSpec{
			OSImageURL: imageParams["osImageURL"],
			Config: runtime.RawExtension{
				Raw: icb,
			},
		},
	}

	if kernelArguments, ok := imageParams["kernelArguments"]; ok {
		// Parse the kernel arguments and set them in the MachineConfig
		// Note that in the configmap the kernel arguments are stored as a single string
		// eg. "a=b c=d ..." and we need to split them into individual arguments
		// eg ["a=b", "c=d", ...]
		// Split the kernel arguments string into individual arguments
		mc.Spec.KernelArguments = strings.Fields(kernelArguments)
	}

	return &mc, nil
}

// Delete the MachineConfig object
func (r *KataConfigOpenShiftReconciler) deleteMc(mcName string) error {
	r.Log.Info("Deleting MachineConfig", "mc.Name", mcName)
	mc := &mcfgv1.MachineConfig{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mcName}, mc)
	if err != nil {
		if k8serrors.IsNotFound(err) || k8serrors.IsGone(err) {
			r.Log.Info("MachineConfig not found. Most likely it was already deleted.")
			return nil
		}
		r.Log.Info("failed to retrieve MachineConfig", "err", err)
		return err
	}

	r.Log.Info("deleting MachineConfig")
	err = r.Client.Delete(context.TODO(), mc)
	if err != nil {
		r.Log.Error(err, "Failed to delete MachineConfig")
		return err
	}

	r.Log.Info("MachineConfig successfully deleted")
	return nil
}
