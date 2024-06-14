package controllers

import (
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// When the feature is enabled, handleFeatureConfidential sets config maps to confidential values.
//
// Changes the ImageConfigMap, so that the image creation job will create a confidential image.
// This will happen at the first reconciliation loop, before the image creation job starts.
//
// Changes the peer pods configMap to enable confidential.
// This will happen likely after several reconciliation loops, because it has prerequisites:
//
//   - Peer pods must be enabled in the KataConfig.
//   - The peer pods config map must exist.
//
// When the feature is disabled, handleFeatureConfidential resets the config maps to non-confidential values.
func (r *KataConfigOpenShiftReconciler) handleFeatureConfidential(state FeatureGateState) error {

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
				if err := ig.createImageConfigMapFromFile(); err != nil {
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

	// peer pods config

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
