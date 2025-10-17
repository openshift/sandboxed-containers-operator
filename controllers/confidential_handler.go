package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// kata-cc runtime class for CoCo BM
	kataCCRuntimeClassName        = "kata-cc"
	kataCCRuntimeClassCpuOverhead = "0.25"
	kataCCRuntimeClassMemOverhead = "350Mi"

	// TEE node labels
	intelTDXNodeLabel = "intel.feature.node.kubernetes.io/tdx"
	amdSNPNodeLabel   = "amd.feature.node.kubernetes.io/snp"

	// RuntimeClass handlers for TEE
	kataCCIntelHandler = "kata-cc-intel"
	kataCCAmdHandler   = "kata-cc-amd"
)

// When the feature is enabled, handleFeatureConfidential configures confidential computing support.
//
// For peer pods: sets ImageConfigMap and peer pods configMap to enable confidential images and CVM support.
// For baremetal: creates kata-cc runtime classes with TEE-specific handlers (Intel TDX or AMD SNP).
//
// When the feature is disabled, handleFeatureConfidential resets config maps and deletes runtime classes.
func (r *KataConfigOpenShiftReconciler) handleFeatureConfidential(state FeatureGateState) error {
	if r.kataConfig.Spec.EnablePeerPods {
		if err := r.handleConfidentialPeerPods(state); err != nil {
			return err
		}
	}

	if err := r.handleConfidentialBaremetal(state); err != nil {
		return err
	}

	return nil
}

// handleConfidentialPeerPods configures confidential computing for peer pods deployments.
// It manages ImageConfigMap and peer pods configMap to control confidential images and CVM support.
func (r *KataConfigOpenShiftReconciler) handleConfidentialPeerPods(state FeatureGateState) error {
	// ImageConfigMap
	if err := InitializeImageGenerator(r.Client); err != nil {
		return err
	}
	ig := GetImageGenerator()

	if ig.provider == unsupportedCloudProvider {
		r.Log.Info("unsupported cloud provider, skipping confidential image configuration")
	} else {
		if ig.isImageIDSet() {
			r.Log.Info("Image ID is already set, skipping confidential image configuration")
		} else {
			if state == Enabled {
				// Create ImageConfigMap, if it doesn't exist already.
				if err := ig.createImageConfigMapFromFile(r.kataConfig); err != nil {
					return err
				}

				// Patch ImageConfigMap.
				imageConfigMapData := map[string]string{"CONFIDENTIAL_COMPUTE_ENABLED": "yes"}
				if err := updateConfigMap(r.Client, r.Log, ig.getImageConfigMapName(), OperatorNamespace, imageConfigMapData); err != nil {
					return err
				}
			} else {
				// Patch ImageConfigMap.
				imageConfigMapData := map[string]string{"CONFIDENTIAL_COMPUTE_ENABLED": "no"}
				if err := updateConfigMap(r.Client, r.Log, ig.getImageConfigMapName(), OperatorNamespace, imageConfigMapData); err != nil {
					if k8serrors.IsNotFound(err) {
						// Nothing to do, feature is disabled and configMap doesn't exist.
					} else {
						return err
					}
				}
			}
		}
	}

	// Patch peer pods configMap, if it exists.
	var peerpodsCMData map[string]string
	if state == Enabled {
		peerpodsCMData = map[string]string{"DISABLECVM": "false"}
	} else {
		peerpodsCMData = map[string]string{"DISABLECVM": "true"}
	}
	if err := updateConfigMap(r.Client, r.Log, peerpodsCMName, OperatorNamespace, peerpodsCMData); err != nil {
		if k8serrors.IsNotFound(err) {
			// When feature is Enabled: ConfigMap doesn't exist yet, will try again at the next reconcile run.
			// Else: Nothing to do, feature is disabled and configMap doesn't exist.
		} else {
			return err
		}
	}

	return nil
}

// handleConfidentialBaremetal configures confidential computing for baremetal deployments.
// It manages kata-cc runtime classes with TEE-specific handlers (Intel TDX or AMD SNP).
func (r *KataConfigOpenShiftReconciler) handleConfidentialBaremetal(state FeatureGateState) error {
	if state == Enabled {
		r.Log.Info("Creating " + kataCCRuntimeClassName + " runtime class for confidential containers")

		handler, nodeLabel, err := r.computeTEEHandlerAndLabel()
		if err != nil {
			// If peer pods is enabled, just warn and skip baremetal coco
			if r.kataConfig.Spec.EnablePeerPods {
				r.Log.Info("WARNING: No TEE hardware detected, skipping baremetal confidential containers (peer pods CVM will handle confidential workloads)", "err", err)
				return nil
			}
			// If peer pods disabled, this is an error - user wants baremetal coco but no hardware
			r.Log.Info("failed to detect TEE platform", "err", err)
			return err
		}

		// Create kata-cc runtime class restricted to the detected TEE subset
		err = r.createRuntimeClass(kataCCRuntimeClassName, kataCCRuntimeClassCpuOverhead, kataCCRuntimeClassMemOverhead, handler, nodeLabel)
		if err != nil {
			r.Log.Info("Error creating "+kataCCRuntimeClassName+" runtime class", "err", err)
			return fmt.Errorf("Error creating "+kataCCRuntimeClassName+" runtime class: %w", err)
		}

	} else {
		r.Log.Info("Deleting " + kataCCRuntimeClassName + " runtime class for confidential containers")

		// Delete kata-cc runtime class
		err := r.deleteRuntimeClass(kataCCRuntimeClassName)
		if err != nil {
			r.Log.Info("Error deleting "+kataCCRuntimeClassName+" runtime class", "err", err)
			return fmt.Errorf("Error deleting "+kataCCRuntimeClassName+" runtime class: %w", err)
		}
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) computeTEEHandlerAndLabel() (string, string, error) {
	selector, err := r.getKataConfigNodeSelectorAsSelector()
	if err != nil {
		return "", "", fmt.Errorf("failed to build node selector: %w", err)
	}

	nodes := &corev1.NodeList{}
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: selector},
	}
	if err := r.Client.List(context.TODO(), nodes, listOpts...); err != nil {
		return "", "", fmt.Errorf("failed to list nodes: %w", err)
	}

	var hasIntelTDX bool
	var hasAmdSNP bool
	for _, n := range nodes.Items {
		if v, ok := n.Labels[intelTDXNodeLabel]; ok && v == "true" {
			hasIntelTDX = true
		}
		if v, ok := n.Labels[amdSNPNodeLabel]; ok && v == "true" {
			hasAmdSNP = true
		}
	}

	if hasIntelTDX && hasAmdSNP {
		return "", "", fmt.Errorf("multiple TEE platforms detected; only one per cluster supported")
	}

	if hasIntelTDX {
		return kataCCIntelHandler, intelTDXNodeLabel, nil
	}
	if hasAmdSNP {
		return kataCCAmdHandler, amdSNPNodeLabel, nil
	}

	return "", "", fmt.Errorf("no TEE platform labels found (expected %s or %s)", intelTDXNodeLabel, amdSNPNodeLabel)
}
