package controllers

const (
	// https://sdk.operatorframework.io/docs/upgrading-sdk-version/v1.4.0/
	// https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#finalizers
	kataConfigFinalizer = "kataconfiguration.openshift.io/finalizer"
)

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
