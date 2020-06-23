package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KataConfigSpec defines the desired state of KataConfig
type KataConfigSpec struct {
	// KataConfigPoolSelector is used to filer the worker nodes
	// if not specified, all worker nodes are selected
	// +optional
	KataConfigPoolSelector *metav1.LabelSelector `json:"kataConfigPoolSelector"`

	// +optional
	Config KataInstallConfig `json:"config"`
}

// KataInstallConfig is a placeholder struct
type KataInstallConfig struct {
}

// KataConfigStatus defines the observed state of KataConfig
type KataConfigStatus struct {
	// RuntimeClass is the name of the runtime class used in CRIO configuration
	RuntimeClass string `json:"runtimeClass"`

	// KataImage is the image used for delivering kata binaries
	KataImage string `json:"kataImage"`

	// TotalNodesCounts is the total number of worker nodes targeted by this CR
	TotalNodesCount int `json:"totalNodesCount"`

	// InstallationStatus reflects the status of the ongoing kata installation
	// +optional
	InstallationStatus KataInstallationStatus `json:"installationStatus,omitempty"`

	// UnInstallationStatus reflects the status of the ongoing kata uninstallation
	// +optional
	UnInstallationStatus KataUnInstallationStatus `json:"unInstallationStatus,omitempty"`

	// Upgradestatus reflects the status of the ongoing kata upgrade
	// +optional
	Upgradestatus KataUpgradeStatus `json:"upgradeStatus,omitempty"`
}

// KataInstallationStatus reflects the status of the ongoing kata installation
type KataInstallationStatus struct {
	// InProgress reflects the status of nodes that are in the process of kata installation
	InProgress KataInstallationInProgressStatus `json:"inProgress"`

	// Completed reflects the status of nodes that have completed kata installation
	Completed KataInstallationCompletedStatus `json:"completed"`

	// Failed reflects the status of nodes that have failed kata installation
	Failed KataFailedNodeStatus `json:"failed"`
}

// KataInstallationInProgressStatus reflects the status of nodes that are in the process of kata installation
type KataInstallationInProgressStatus struct {
	InProgressNodesCount int `json:"inProgressNodesCount"`
	// +optional
	BinariesInstalledNodesList []string `json:"binaryInstallNodesList,omitempty"`
}

// KataInstallationCompletedStatus reflects the status of nodes that have completed kata installation
type KataInstallationCompletedStatus struct {
	// CompletedNodesCount reflects the number of nodes that have completed kata installation
	CompletedNodesCount int `json:"completedNodesCount"`

	// CompletedNodesList reflects the list of nodes that have completed kata installation
	// +optional
	CompletedNodesList []string `json:"completedNodesList,omitempty"`
}

// KataFailedNodeStatus reflects the status of nodes that have failed kata operation
type KataFailedNodeStatus struct {
	// FailedNodesCount reflects the number of nodes that have failed kata operation
	FailedNodesCount int `json:"failedNodesCount"`

	// FailedNodesList reflects the list of nodes that have failed kata operation
	// +optional
	FailedNodesList []FailedNodeStatus `json:"failedNodesList,omitempty"`
}

// KataUnInstallationStatus reflects the status of the ongoing kata uninstallation
type KataUnInstallationStatus struct {
}

// KataUpgradeStatus reflects the status of the ongoing kata upgrade
type KataUpgradeStatus struct {
}

// FailedNodeStatus holds the name and the error message of the failed node
type FailedNodeStatus struct {
	// Name of the failed node
	Name string `json:"name"`
	// Error message of the failed node reported by the installation daemon
	Error string `json:"error"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KataConfig is the Schema for the kataconfigs API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=kataconfigs,scope=Cluster
type KataConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KataConfigSpec   `json:"spec,omitempty"`
	Status KataConfigStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KataConfigList contains a list of KataConfig
type KataConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KataConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KataConfig{}, &KataConfigList{})
}
