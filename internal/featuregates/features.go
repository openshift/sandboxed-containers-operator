package featuregates

/* Design aspects of implementing feature gates

- feature gate is only for experimental features
- Keep the feature gate as simple boolean.
- Any config specific for a feature gate should be in it’s own configMap. This aligns with our current implementation of peer-pods and image generator feature
- When we decide to make the feature as a stable feature, it should move to kataConfig.spec
*/

const (
	FeatureGatesConfigMapName = "osc-feature-gates"
)

// Sample ConfigMap with Features

/*
apiVersion: v1
kind: ConfigMap
metadata:
  name: osc-feature-gates
  namespace: openshift-sandboxed-containers-operator
data:
  "timeTravel":              false,
  "quantumEntanglementSync": false,
*/
