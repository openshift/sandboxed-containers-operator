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

	// CompletedNodesCounts is the number of nodes that have successfully completed the given operation
	CompletedNodesCount int `json:"completedNodesCount"`

	// CompletedDaemons is the number of kata installation daemons that have successfully completed the given operation
	CompletedDaemons int `json:"completedDaemons"`

	// InProgressNodesCounts is the number of nodes still in progress in completing the given operation
	InProgressNodesCount int `json:"inProgressNodesCount"`

	// FailedNodes is the list of worker nodes failed to complete the given operation
	FailedNodes []FailedNode `json:"failedNodes"`
}

// FailedNode holds the name and the error message of the failed node
type FailedNode struct {
	// Name of the failed node
	Name string `json:"name"`
	// Error message of the failed node reported by the installation daemon
	Error string `json:"error"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KataConfig is the Schema for the kataconfigs API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=kataconfigs,scope=Namespaced
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
