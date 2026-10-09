package controllers

const (
	// https://sdk.operatorframework.io/docs/upgrading-sdk-version/v1.4.0/
	// https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#finalizers
	kataConfigFinalizer = "kataconfiguration.openshift.io/finalizer"

	// kataConfigCleanupFinalizer is placed on the operator's own CSV while the
	// KataConfig finalizer is present.  It blocks OLM from tearing down the
	// controller before node cleanup completes (KATA-6228).
	kataConfigCleanupFinalizer = "kataconfiguration.openshift.io/kataconfig-cleanup"
)

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
