package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
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

	// RuntimeClass handlers for TEE (legacy single-TEE mode)
	kataCCIntelHandler = "kata-tdx"
	kataCCAmdHandler   = "kata-snp"
	kataCCIbmHandler   = "kata-se"

	// Unified handler for heterogeneous TEE clusters
	kataCCUnifiedHandler = "kata-cc"

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
// When UnifiedKataCCHandler is true, a single kata-cc handler maps to per-node CRI-O configs.
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

		if r.kataConfig.Spec.UnifiedKataCCHandler {
			// Unified mode: single kata-cc handler, per-TEE CRI-O configs and MCPs
			if err := r.handleConfidentialBaremetalUnified(); err != nil {
				return err
			}
		} else {
			// Legacy mode: single TEE type, TEE-specific handler
			if err := r.handleConfidentialBaremetalLegacy(); err != nil {
				return err
			}
		}

	} else {
		r.Log.Info("Deleting " + kataCCRuntimeClassName + " runtime class for confidential containers")

		// Clean up TEE-specific MCs and MCPs (idempotent)
		for _, tee := range []string{"tdx", "snp", "se"} {
			if err := r.deleteTEEPoolAndMC(tee); err != nil {
				r.Log.Info("Error cleaning up TEE resources", "tee", tee, "err", err)
				// Continue with other TEEs
			}
		}

		// Delete kata-cc runtime class
		err := r.deleteRuntimeClass(kataCCRuntimeClassName)
		if err != nil {
			r.Log.Info("Error deleting "+kataCCRuntimeClassName+" runtime class", "err", err)
			return fmt.Errorf("Error deleting "+kataCCRuntimeClassName+" runtime class: %w", err)
		}
	}

	return nil
}

// handleConfidentialBaremetalUnified implements unified kata-cc: single handler, per-TEE CRI-O configs.
func (r *KataConfigOpenShiftReconciler) handleConfidentialBaremetalUnified() error {
	teeTypes, err := r.getPresentTEETypes()
	if err != nil {
		if r.kataConfig.Spec.EnablePeerPods {
			r.Log.Info("WARNING: No TEE hardware detected, skipping baremetal confidential containers", "err", err)
			return nil
		}
		return err
	}
	if len(teeTypes) == 0 {
		if r.kataConfig.Spec.EnablePeerPods {
			r.Log.Info("WARNING: No TEE hardware detected, skipping baremetal confidential containers")
			return nil
		}
		return fmt.Errorf("no TEE platform labels found (expected %s, %s or %s)", intelTDXNodeLabel, amdSNPNodeLabel, ibmSENodeLabel)
	}

	// TEE type -> (node label, kata config path)
	teeConfig := map[string]struct{ label, configPath string }{
		"tdx": {intelTDXNodeLabel, "/etc/kata-containers/kata-tdx/configuration.toml"},
		"snp": {amdSNPNodeLabel, "/etc/kata-containers/kata-snp/configuration.toml"},
		"se":  {ibmSENodeLabel, "/etc/kata-containers/kata-se/configuration.toml"},
	}

	for _, tee := range teeTypes {
		cfg, ok := teeConfig[tee]
		if !ok {
			continue
		}
		mcRole := kataCCTEEMCPPrefix + tee
		mc, err := r.createKataCCCRIODropInMC(tee, cfg.configPath, mcRole)
		if err != nil {
			return fmt.Errorf("failed to create kata-cc CRI-O MachineConfig for %s: %w", tee, err)
		}
		found := &mcfgv1.MachineConfig{}
		if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, found); err != nil {
			if k8serrors.IsNotFound(err) {
				if err := r.Client.Create(context.TODO(), mc); err != nil {
					return fmt.Errorf("failed to create MachineConfig %s: %w", mc.Name, err)
				}
				r.Log.Info("Created kata-cc CRI-O MachineConfig", "tee", tee, "mc", mc.Name)
			} else {
				return err
			}
		}
		if err := r.createOrUpdateTEEPool(tee, cfg.label); err != nil {
			return fmt.Errorf("failed to create/update TEE pool for %s: %w", tee, err)
		}
	}

	// Create RuntimeClass with unified handler, no TEE-specific node selector, no extended resources
	return r.createRuntimeClass(kataCCRuntimeClassName, kataCCRuntimeClassCpuOverhead, kataCCRuntimeClassMemOverhead, "", kataCCUnifiedHandler, "")
}

// handleConfidentialBaremetalLegacy implements legacy single-TEE mode.
func (r *KataConfigOpenShiftReconciler) handleConfidentialBaremetalLegacy() error {
	handler, nodeLabel, err := r.computeTEEHandlerAndLabel()
	if err != nil {
		if r.kataConfig.Spec.EnablePeerPods {
			r.Log.Info("WARNING: No TEE hardware detected, skipping baremetal confidential containers (peer pods CVM will handle confidential workloads)", "err", err)
			return nil
		}
		r.Log.Info("failed to detect TEE platform", "err", err)
		return err
	}

	var kataCCRuntimeClassExtResOverhead string
	if handler == kataCCIntelHandler {
		kataCCRuntimeClassExtResOverhead = intelTDXExtendedResource
	} else if handler == kataCCAmdHandler {
		kataCCRuntimeClassExtResOverhead = amdSNPExtendedResource
	}

	return r.createRuntimeClass(kataCCRuntimeClassName, kataCCRuntimeClassCpuOverhead, kataCCRuntimeClassMemOverhead, kataCCRuntimeClassExtResOverhead, handler, nodeLabel)
}

// getPresentTEETypes returns the list of TEE types present on nodes matching KataConfigPoolSelector.
func (r *KataConfigOpenShiftReconciler) getPresentTEETypes() ([]string, error) {
	selector, err := r.getKataConfigNodeSelectorAsSelector()
	if err != nil {
		return nil, fmt.Errorf("failed to build node selector: %w", err)
	}

	nodes := &corev1.NodeList{}
	if err := r.Client.List(context.TODO(), nodes, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	var tees []string
	seen := map[string]bool{}
	for _, n := range nodes.Items {
		if v, ok := n.Labels[intelTDXNodeLabel]; ok && v == "true" && !seen["tdx"] {
			tees = append(tees, "tdx")
			seen["tdx"] = true
		}
		if v, ok := n.Labels[amdSNPNodeLabel]; ok && v == "true" && !seen["snp"] {
			tees = append(tees, "snp")
			seen["snp"] = true
		}
		if v, ok := n.Labels[ibmSENodeLabel]; ok && v == "true" && !seen["se"] {
			tees = append(tees, "se")
			seen["se"] = true
		}
	}
	return tees, nil
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
