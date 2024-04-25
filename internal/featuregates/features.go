package featuregates

import "strings"

/* Design aspects of implementing feature gates
- feature gate is only for experimental features
- Keep the feature gate as simple boolean.
- Any config specific for a feature gate should be in it’s own configMap. This aligns with our current implementation of peer-pods and image generator feature
- When we decide to make the feature as a stable feature, it should move to kataConfig.spec
*/

const (
	FeatureGatesStatusConfigMapName = "osc-feature-gates"
	LayeredImageDeployment          = "LayeredImageDeployment"
	LayeredImageDeploymentConfig    = "layeredimagedeployment-config"
	AdditionalRuntimeClasses        = "AdditionalRuntimeClasses"
	AdditionalRuntimeClassesConfig  = "additionalruntimeclasses-config"
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
  AdditionalRuntimeClasses: "false"
*/

// Sample ConfigMap with configs for individual features
/*
apiVersion: v1
kind: ConfigMap
metadata:
  name: layeredimagedeployment-config
  namespace: openshift-sandboxed-containers-operator
data:
  osImageURL: "quay.io/...."
  kernelArguments: "a=b c=d ..."

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: additionalruntimeclasses-config
  namespace: openshift-sandboxed-containers-operator
data:
  runtimeClassConfig: "name1:cpuOverHead1:memOverHead1, name2:cpuOverHead2:memOverHead2"
  #runtimeClassConfig: "name1, name2"
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
	case FeatureGatesStatusConfigMapName, LayeredImageDeploymentConfig, AdditionalRuntimeClassesConfig:
		return true
	default:
		return false
	}
}
