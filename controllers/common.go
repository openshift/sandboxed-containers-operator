package controllers

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// DaemonOperation represents the operation kata daemon is going to perform
type DaemonOperation string

const (
	// InstallOperation denotes kata installation operation
	InstallOperation DaemonOperation = "install"

	// UninstallOperation denotes kata uninstallation operation
	UninstallOperation DaemonOperation = "uninstall"

	// UpgradeOperation denotes kata upgrade operation
	UpgradeOperation DaemonOperation = "upgrade"

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

func getClientSet() (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}
