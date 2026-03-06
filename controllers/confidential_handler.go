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
	ibmSENodeLabel    = "ibm.feature.node.kubernetes.io/se"

	// RuntimeClass handlers for TEE
	kataCCIntelHandler = "kata-tdx"
	kataCCAmdHandler   = "kata-snp"
	kataCCIbmHandler   = "kata-se"

	// Extended resources for TEE
	intelTDXExtendedResource = "tdx.intel.com/keys"
	amdSNPExtendedResource   = "sev-snp.amd.com/esids"

	// INITDATA value for non-confidential peer pods, this is required in order
	// to override the default restrictive CoCo agent policy
	// created from sourced plaintxt: cat config/peerpods/default-non-cc-initdata.toml | gzip | base64 -w0
	defaultNonCCInitdata = "H4sIAAAAAAAAA42UwW7bMAyG734KwT3ktKDDehgG9NAl2VZgWQw7bQ7DMDAWYxOVRU+i03pPP7lGhx0muwddyE8U+ZPUhdrX5BVZEg0C6kQG1SN4VToEQa2OvZIaFfvyDbfoQNglYCp2JHWjrlXqa3j3/ipNzug8sR1Ml8u3y8s0Sb4PIX8kKcBSuDFp8C0Wi2Q4SdqyobJfOqz4xdFC+QAVqnCs/ByBJNF4gs6IutH6Js++IVX1kZ3P8VeHXtSHayWuw3+x4hHamHtl2GMhmmyU4Lb/FGSI+p+VWbEVIItuGivA6iM/xaB1MDruZ6jNE5aZ4xJ9tOrPKFsUR+UUsdttN+cgbRQZrGsMdZlomK/k5dYKuhOEfKaonDuJE1tsvrC0pqs+9qG2Y1TunTVB5lV2F27EmAw6P9+RrDPmtgnDFQNyBF1I6Fv0oRwbPs+/NGKFgMF7ckJ88kUNDrfcWYlKkqNH1HmYBW7WeJ7AumY+hwJl7GeYwj010aIDlz1vWhSgyoKZmb9Qq5P5nAZq76AkW00w4l8RiduZQHvpD8OWe/odLf6u1a/Z5RHbtDU24Qs0020c4b87Mo1NL8kBSGaEP4SPGP8/tMOX+geD8J3Z4AUAAA=="
)

// When the feature is enabled, handleFeatureConfidential configures confidential computing support.
//
// For peer pods: sets ImageConfigMap and peer pods configMap to enable confidential images and CVM support.
// For baremetal: creates kata-cc runtime classes with TEE-specific handlers (Intel TDX, AMD SNP or IBM SE).
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
				if err := updateConfigMap(r.Client, r.Log, ig.getImageConfigMapName(), OperatorNamespace, imageConfigMapData, nil); err != nil {
					return err
				}
			} else {
				// Patch ImageConfigMap.
				imageConfigMapData := map[string]string{"CONFIDENTIAL_COMPUTE_ENABLED": "no"}
				if err := updateConfigMap(r.Client, r.Log, ig.getImageConfigMapName(), OperatorNamespace, imageConfigMapData, nil); err != nil {
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
	var keysToRemove map[string]string
	if state == Enabled {
		peerpodsCMData = map[string]string{"DISABLECVM": "false"}
		// Remove INITDATA if it matches the default value
		keysToRemove = map[string]string{"INITDATA": defaultNonCCInitdata}
	} else {
		peerpodsCMData = map[string]string{
			"DISABLECVM": "true",
			"INITDATA":   defaultNonCCInitdata,
		}
		keysToRemove = nil
	}
	if err := updateConfigMap(r.Client, r.Log, peerpodsCMName, OperatorNamespace, peerpodsCMData, keysToRemove); err != nil {
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
// It manages kata-cc runtime classes with TEE-specific handlers (Intel TDX, AMD SNP or IBM SE).
func (r *KataConfigOpenShiftReconciler) handleConfidentialBaremetal(state FeatureGateState) error {
	if state == Enabled {
		if !r.kataConfig.Spec.EnablePeerPods {
			isLess, version, err := r.isOCPVersionLessThan("4.20.6")
			if err != nil {
				// Return error to trigger reconcile retry (cluster version not available yet or API error)
				return err
			}
			if isLess {
				r.Log.Info("WARNING: OpenShift version does not support CoCo bare metal", "version", version, "minVersion", "4.20.6")
				cond := r.retrieveInProgressConditionForChange()
				cond.Status = corev1.ConditionFalse
				cond.Reason = "UnsupportedOCPVersion"
				cond.Message = fmt.Sprintf("OpenShift version %s does not support CoCo bare metal (minimum required: 4.20.6)", version)
				return nil
			}
		}

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

		// Determine extended resource based on TEE type
		var kataCCRuntimeClassExtResOverhead string
		if handler == kataCCIntelHandler {
			kataCCRuntimeClassExtResOverhead = intelTDXExtendedResource
		} else if handler == kataCCAmdHandler {
			kataCCRuntimeClassExtResOverhead = amdSNPExtendedResource
		}

		// Create kata-cc runtime class restricted to the detected TEE subset
		err = r.createRuntimeClass(kataCCRuntimeClassName, kataCCRuntimeClassCpuOverhead, kataCCRuntimeClassMemOverhead, kataCCRuntimeClassExtResOverhead, handler, map[string]string{nodeLabel: "true"})
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

	var hasIntelTDX, hasAmdSNP, hasIbmSE bool

	for _, n := range nodes.Items {
		if v, ok := n.Labels[intelTDXNodeLabel]; ok && v == "true" {
			hasIntelTDX = true
		}
		if v, ok := n.Labels[amdSNPNodeLabel]; ok && v == "true" {
			hasAmdSNP = true
		}
		if v, ok := n.Labels[ibmSENodeLabel]; ok && v == "true" {
			hasIbmSE = true
		}
	}

	count := 0
	if hasIntelTDX {
		count++
	}
	if hasAmdSNP {
		count++
	}
	if hasIbmSE {
		count++
	}

	if count >= 2 {
		return "", "", fmt.Errorf("multiple TEE platforms detected; only one per cluster supported")
	}

	if hasIntelTDX {
		return kataCCIntelHandler, intelTDXNodeLabel, nil
	}
	if hasAmdSNP {
		return kataCCAmdHandler, amdSNPNodeLabel, nil
	}
	if hasIbmSE {
		return kataCCIbmHandler, ibmSENodeLabel, nil
	}

	return "", "", fmt.Errorf("no TEE platform labels found (expected %s, %s or %s)", intelTDXNodeLabel, amdSNPNodeLabel, ibmSENodeLabel)
}
