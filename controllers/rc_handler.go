package controllers

/*
This code handles the creation and deletion of additional runtime classes based on the
additionalRuntimeClasses feature gate configuration.
Typically the additionalRuntimeClasses will be used with other feature gates like the
imageBasedDeployment. Since multiple features have a need to create additional runtimeClasses
this functionality is made into a separate feature rather then being embedded into multiple different
features.

The feature gate configuration is read from the ConfigMap osc-feature-gate-additional-rc-config.
The runtimeClassConfig key in the ConfigMap contains the configuration for additional runtime classes.
The configuration is in the format:
runtimeClassConfig="name1:cpuOverHead1:memOverHead1, name2:cpuOverHead2:memOverHead2"
or
runtimeClassConfig="name1, name2"

The runtimeClassConfig key is read and the runtime classes are created based on the configuration.
If the feature gate is disabled, the additional runtime classes are deleted.

The reconcile loop should ensure the following:
1. Create or delete runtimeClasses and update r.kataConfig.Status.RuntimeClasses field
2. The runtimeClasses to create or delete consists of the following (in order)
	- default runtimeClass "kata"
	- runtimeClass "kata-remote" if peer pods is enabled
	- runtimeClasses from the feature-gate config runtimeClassConfig key.

	runtimeClassConfig is an array of the RuntimeClassConfig struct, consisting of the following fields:
	- ClassName: Name of the runtimeClass
	- CPUOverhead: CPU overhead for the runtimeClass
	- MemoryOverhead: Memory overhead for the runtimeClass

	The r.kataConfig.Status.RuntimeClasses is a string array of runtimeClass names

	So at any given point in time, the runtimeClasses on the system should consists of
	- default runtimeClass "kata"
	- runtimeClass "kata-remote" if peer pods is enabled
	- runtimeClasses from the feature-gate config runtimeClassConfig key if present.

*/

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift/sandboxed-containers-operator/internal/featuregates"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cri-api/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type RuntimeClassConfig struct {
	ClassName      string
	CPUOverhead    string
	MemoryOverhead string
}

const (
	defaultRuntimeClassName         = "kata"
	defaultCPUOverhead              = "0.25"
	defaultMemoryOverhead           = "350Mi"
	peerpodsRuntimeClassName        = "kata-remote"
	peerpodsRuntimeClassCpuOverhead = "0.25"
	peerpodsRuntimeClassMemOverhead = "350Mi"
)

// Method to get additional runtimeClass configuration from the
// feature-gate configmap.
// additionalRuntimeClasses.params: |
//
//	runtimeClassConfig="name1:cpuOverHead1:memOverHead1, name2:cpuOverHead2:memOverHead2"
//
// or
// runtimeClassConfig="name1, name2"
func (r *KataConfigOpenShiftReconciler) getRuntimeClassConfigs() []RuntimeClassConfig {
	runtimeClassConfigs := []RuntimeClassConfig{}

	// Get the AdditionalRuntimeClasses feature gate parameters
	additionalRuntimeClassesParams := r.FeatureGates.GetFeatureGateParams(context.TODO(), featuregates.AdditionalRuntimeClasses)
	// If the runtimeClassConfig key is not present, return empty runtimeClassConfig
	if _, ok := additionalRuntimeClassesParams["runtimeClassConfig"]; !ok {
		return runtimeClassConfigs
	}

	runtimeClassConfig := additionalRuntimeClassesParams["runtimeClassConfig"]
	for _, runtimeClass := range strings.Split(runtimeClassConfig, ",") {
		// It is expected that the runtimeClass is in the format name:cpuOverhead:memOverhead
		// Or it can be just the name of the runtimeClass
		// If the cpuOverhead and memOverhead are not provided, default to defaultCPUOverhead and defaultMemOverhead
		runtimeClassParts := strings.Split(runtimeClass, ":")
		cpuOverhead := defaultCPUOverhead
		memoryOverhead := defaultMemoryOverhead
		if len(runtimeClassParts) == 3 {
			cpuOverhead = runtimeClassParts[1]
			memoryOverhead = runtimeClassParts[2]
		}

		runtimeClassConfigs = append(runtimeClassConfigs, RuntimeClassConfig{
			// Trim white spaces in the runtimeClass name
			ClassName:      strings.TrimSpace(runtimeClassParts[0]),
			CPUOverhead:    cpuOverhead,
			MemoryOverhead: memoryOverhead,
		})
	}

	// Filter out duplicates if any
	runtimeClassConfigs = removeDuplicates(runtimeClassConfigs)

	return runtimeClassConfigs
}

// Method to create the runtimeClass objects
func (r *KataConfigOpenShiftReconciler) createRuntimeClasses(runtimeClassConfigs []RuntimeClassConfig) error {
	for _, config := range runtimeClassConfigs {
		rc := func() *nodeapi.RuntimeClass {
			rc := &nodeapi.RuntimeClass{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "node.k8s.io/v1",
					Kind:       "RuntimeClass",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: config.ClassName,
				},
				Handler: config.ClassName,
				Overhead: &nodeapi.Overhead{
					PodFixed: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(config.CPUOverhead),
						corev1.ResourceMemory: resource.MustParse(config.MemoryOverhead),
					},
				},
			}

			nodeSelector := r.getNodeSelectorAsMap()

			rc.Scheduling = &nodeapi.Scheduling{
				NodeSelector: nodeSelector,
			}

			r.Log.Info("RuntimeClass NodeSelector:", "nodeSelector", nodeSelector)

			return rc
		}()

		if err := controllerutil.SetControllerReference(r.kataConfig, rc, r.Scheme); err != nil {
			return err
		}

		foundRc := &nodeapi.RuntimeClass{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: rc.Name}, foundRc)
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return err
			}

			r.Log.Info("Creating a new RuntimeClass", "rc.Name", rc.Name)
			err = r.Client.Create(context.TODO(), rc)
			// We are not checking for k8serrors.IsAlreadyExists error here
			// since we are checking for existing runtimeClass above.
			if err != nil {
				return err
			}

		}

		// If the runtimeClass is not present in the KataConfig status field, add it
		r.Log.Info("RuntimeClass created", "rc.Name", rc.Name)

		if !contains(r.kataConfig.Status.RuntimeClasses, config.ClassName) {
			r.kataConfig.Status.RuntimeClasses = append(r.kataConfig.Status.RuntimeClasses, config.ClassName)
		}

	}

	return nil
}

// Method to create default runtimeClass object
func (r *KataConfigOpenShiftReconciler) createDefaultRuntimeClass() error {
	return r.createRuntimeClasses([]RuntimeClassConfig{
		{
			ClassName:      defaultRuntimeClassName,
			CPUOverhead:    defaultCPUOverhead,
			MemoryOverhead: defaultMemoryOverhead,
		},
	})

}

// Method to create default runtimeClass for peer pods
func (r *KataConfigOpenShiftReconciler) createPeerPodsRuntimeClass() error {
	return r.createRuntimeClasses([]RuntimeClassConfig{
		{
			ClassName:      peerpodsRuntimeClassName,
			CPUOverhead:    peerpodsRuntimeClassCpuOverhead,
			MemoryOverhead: peerpodsRuntimeClassMemOverhead,
		},
	})
}

// Method to delete the runtimeClass objects
func (r *KataConfigOpenShiftReconciler) deleteRuntimeClasses(runtimeClassNames []string) error {
	for _, rcName := range runtimeClassNames {
		rc := &nodeapi.RuntimeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: rcName,
			},
		}
		r.Log.Info("Deleting RuntimeClass", "rcName", rcName)
		if err := r.Client.Delete(context.TODO(), rc); err != nil {
			if errors.IsNotFound(err) {
				// If the RuntimeClass is not found, it may have been deleted already. Continue to the next one.
				continue
			}
			return fmt.Errorf("failed to delete RuntimeClass %s: %v", rcName, err)
		}
		// Remove the runtimeClass from the r.kataConfig.Status.RuntimeClasses field
		if contains(r.kataConfig.Status.RuntimeClasses, rcName) {
			r.kataConfig.Status.RuntimeClasses = remove(r.kataConfig.Status.RuntimeClasses, rcName)
		}

	}
	return nil
}

// Method to check and handle additional runtimeClasses
func (r *KataConfigOpenShiftReconciler) handleAdditionalRuntimeClasses() error {
	// If additionalRuntimeClass feature is enabled, create additional runtime classes
	if r.FeatureGatesStatus[featuregates.AdditionalRuntimeClasses] {
		// Check if AdditionalRuntimeClasses config is provided
		// If not log an error and return
		if !r.FeatureGates.IsFeatureConfigMapPresent(context.TODO(), featuregates.AdditionalRuntimeClasses) {
			r.Log.Info("AdditionalRuntimeClasses feature is enabled but no configuration provided")
			err := fmt.Errorf("AdditionalRuntimeClasses feature is enabled but no configuration provided")
			return err
		}

		r.Log.Info("create additional runtime classes")
		runtimeClassConfigs := r.getRuntimeClassConfigs()
		if len(runtimeClassConfigs) > 0 {

			// Get the runtimeClassConfigs that should exist on the cluster
			finalRuntimeClassConfigs := r.getRuntimeClassConfigsThatShouldExist(runtimeClassConfigs)

			// Create or delete runtimeClasses
			err := r.createOrDeleteRuntimeClasses(finalRuntimeClassConfigs)
			if err != nil {
				return err
			}

		} else {
			r.Log.Info("no additional runtime classes to create")
		}
		r.Log.Info("additional runtime classes created")
	} else {
		// If AdditionalRuntimeClasses is disabled and KataConfig status field has multiple runtimeClasses then
		// we need to delete those additional runtimeClasses

		// Get the runtimeClassConfigs that should exist on the cluster
		finalRuntimeClassConfigs := r.getRuntimeClassConfigsThatShouldExist(nil)

		// Create or delete runtimeClasses
		err := r.createOrDeleteRuntimeClasses(finalRuntimeClassConfigs)
		if err != nil {
			return err
		}

		r.Log.Info("additional runtime classes removed from KataConfig status")

	}
	return nil
}

// getRuntimeClassConfigsThatShouldExist returns the runtimeClasses that should exist on the cluster
func (r *KataConfigOpenShiftReconciler) getRuntimeClassConfigsThatShouldExist(runtimeClassConfigs []RuntimeClassConfig) []RuntimeClassConfig {
	finalRuntimeClassConfigs := make([]RuntimeClassConfig, 0)

	// Always include default runtimeClass "kata"
	finalRuntimeClassConfigs = append(finalRuntimeClassConfigs, RuntimeClassConfig{
		ClassName:      defaultRuntimeClassName,
		CPUOverhead:    defaultCPUOverhead,
		MemoryOverhead: defaultMemoryOverhead,
	})

	// Include runtimeClass "kata-remote" if peer pods are enabled
	if r.kataConfig.Spec.EnablePeerPods {
		finalRuntimeClassConfigs = append(finalRuntimeClassConfigs, RuntimeClassConfig{
			ClassName:      peerpodsRuntimeClassName,
			CPUOverhead:    peerpodsRuntimeClassCpuOverhead,
			MemoryOverhead: peerpodsRuntimeClassMemOverhead,
		})
	}

	// Add runtimeClassConfigs if present
	if runtimeClassConfigs != nil {
		finalRuntimeClassConfigs = append(finalRuntimeClassConfigs, runtimeClassConfigs...)
	}

	return finalRuntimeClassConfigs
}

// Method to create or delete runtimeClasses
// It should delete runtimeClasses that are not in the KataConfig status field
func (r *KataConfigOpenShiftReconciler) createOrDeleteRuntimeClasses(runtimeClassConfigs []RuntimeClassConfig) error {

	// Create the runtimeClasses. If the runtimeClass already exists, it will be skipped
	err := r.createRuntimeClasses(runtimeClassConfigs)
	if err != nil {
		return err
	}

	// Get the list of runtimeClasses that should not exist on the system
	runtimeClassNamesToDelete := r.getRuntimeClassNamesToDelete(runtimeClassConfigs)

	// Delete the runtimeClasses that should not exist on the system
	err = r.deleteRuntimeClasses(runtimeClassNamesToDelete)
	if err != nil {
		return err
	}

	return nil
}

// Method to find out which runtimeClasses to Delete
func (r *KataConfigOpenShiftReconciler) getRuntimeClassNamesToDelete(runtimeClassConfigs []RuntimeClassConfig) []string {
	// Get the runtimeClassNames that should exist on the system
	runtimeClassNames := getRuntimeClassNames(runtimeClassConfigs)

	// Get the runtimeClassNames that are present in the KataConfig status field
	// This is faster than getting the runtimeClasses from the Kube API
	// The runtimeClasses in the KataConfig status field are updated when the runtimeClasses are created or deleted anyway
	runtimeClassNamesInStatus := r.kataConfig.Status.RuntimeClasses

	// Find out which runtimeClasses to delete
	runtimeClassNamesToDelete := []string{}
	for _, rc := range runtimeClassNamesInStatus {
		if !contains(runtimeClassNames, rc) {
			runtimeClassNamesToDelete = append(runtimeClassNamesToDelete, rc)
		}
	}

	return runtimeClassNamesToDelete
}

// Method to remove duplicates in RuntimeClassConfig struct based on ClassName field
// Search for matching ClassName in the array and remove duplicates
func removeDuplicates(runtimeClassConfigs []RuntimeClassConfig) []RuntimeClassConfig {
	encountered := map[string]bool{}
	result := []RuntimeClassConfig{}

	for v := range runtimeClassConfigs {
		if encountered[runtimeClassConfigs[v].ClassName] {
			// Do not add duplicate
		} else {
			encountered[runtimeClassConfigs[v].ClassName] = true
			result = append(result, runtimeClassConfigs[v])
		}
	}
	return result
}

// Method to get runtimeClassNames array from RuntimeClassConfigs
func getRuntimeClassNames(runtimeClassConfigs []RuntimeClassConfig) []string {
	var runtimeClassNames []string
	for _, rc := range runtimeClassConfigs {
		runtimeClassNames = append(runtimeClassNames, rc.ClassName)
	}
	return runtimeClassNames
}

// Method to remove runtimeClassName from the runtimeClassNames array
func remove(runtimeClassNames []string, runtimeClassName string) []string {
	var result []string
	for _, rc := range runtimeClassNames {
		if rc != runtimeClassName {
			result = append(result, rc)
		}
	}
	return result
}
