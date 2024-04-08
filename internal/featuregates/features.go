package featuregates

import "strings"

/* Design aspects of implementing feature gates

- feature gate is only for experimental features
- Keep the feature gate as simple boolean.
- Any config specific for a feature gate should be in it’s own configMap. This aligns with our current implementation of peer-pods and image generator feature
- When we decide to make the feature as a stable feature, it should move to kataConfig.spec
*/

const (
	FeatureGatesConfigMapName    = "osc-feature-gates"
	LayeredImageDeployment       = "LayeredImageDeployment"
	LayeredImageDeploymentConfig = "layeredimagedeployment-config"
)

// Sample ConfigMap with Features

/*
apiVersion: v1
kind: ConfigMap
metadata:
  name: osc-feature-gates
  namespace: openshift-sandboxed-containers-operator
data:
  LayeredImageDeployment: "false"

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: layeredimagedeployment-config
  namespace: openshift-sandboxed-containers-operator
data:
  osImageURL="quay.io/...."
  kernelArguments="a=b c=d ..."
*/

// Get the feature gate configmap name from the feature gate name
// The feature configmap is lower case of the feature name with -config suffix
// Kubernetes expects the name to be a lowercase RFC 1123 subdomain
func GetFeatureGateConfigMapName(feature string) string {

	return strings.ToLower(feature) + "-config"

}

// Check if the configmap is a feature gate configmap
// We use explicit check for the feature gate configmap
// and avoid reconstructing the feature name from configmap name
// and do the match
func IsFeatureGateConfigMap(configMapName string) bool {
	switch configMapName {
	case FeatureGatesConfigMapName, LayeredImageDeploymentConfig:
		return true
	default:
		return false
	}
}
