package daemon

import (
	"fmt"
	kataClient "github.com/openshift/kata-operator/pkg/generated/clientset/versioned"
)

// KataKubernetes is used for KataActions on vanilla kubernetes nodes
type KataKubernetes struct {
	KataClientSet *kataClient.Clientset
}

// Install the kata binaries and configure the runtime on vanilla kubernetes
func (k *KataKubernetes) Install(kataConfigResourceName string) error {
	return fmt.Errorf("Not Implemented Yet")
}

// Upgrade the kata binaries and configure the runtime on vanilla kubernetes
func (k *KataKubernetes) Upgrade() error {
	return fmt.Errorf("Not Implemented Yet")
}

// Uninstall the kata binaries and configure the runtime on vanilla kubernetes
func (k *KataKubernetes) Uninstall() error {
	return fmt.Errorf("Not Implemented Yet")
}