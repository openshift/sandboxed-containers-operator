package controllers

const (
	kataConfigFinalizer = "finalizer.kataconfiguration.openshift.io"
)

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
