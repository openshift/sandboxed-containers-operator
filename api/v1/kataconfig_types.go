/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// KataConfigSpec defines the desired state of KataConfig
type KataConfigSpec struct {
	// KataConfigPoolSelector is used to filer the worker nodes
	// if not specified, all worker nodes are selected
	// +optional
	KataConfigPoolSelector *metav1.LabelSelector `json:"kataConfigPoolSelector"`

	// +optional
	Config KataInstallConfig `json:"config"`
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

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KataConfig is the Schema for the kataconfigs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=kataconfigs,scope=Cluster
type KataConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec   KataConfigSpec   `json:"spec,omitempty"`
	Status KataConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KataConfigList contains a list of KataConfig
type KataConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KataConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KataConfig{}, &KataConfigList{})
}

// KataInstallConfig is a placeholder struct
type KataInstallConfig struct {
	// SourceImage is the name of the kata-deploy image
	SourceImage string `json:"sourceImage"`
}

// KataInstallationStatus reflects the status of the ongoing kata installation
type KataInstallationStatus struct {
	// InProgress reflects the status of nodes that are in the process of kata installation
	InProgress KataInstallationInProgressStatus `json:"inProgress,omitempty"`

	// Completed reflects the status of nodes that have completed kata installation
	Completed KataConfigCompletedStatus `json:"completed,omitempty"`

	// Failed reflects the status of nodes that have failed kata installation
	Failed KataFailedNodeStatus `json:"failed,omitempty"`
}

// KataInstallationInProgressStatus reflects the status of nodes that are in the process of kata installation
type KataInstallationInProgressStatus struct {
	// InProgressNodesCount reflects the number of nodes that are in the process of kata installation
	InProgressNodesCount int `json:"inProgressNodesCount,omitempty"`
	// +optional
	BinariesInstalledNodesList []string `json:"binariesInstallNodesList,omitempty"`
}

// KataConfigCompletedStatus reflects the status of nodes that have completed kata operation
type KataConfigCompletedStatus struct {
	// CompletedNodesCount reflects the number of nodes that have completed kata operation
	CompletedNodesCount int `json:"completedNodesCount,omitempty"`

	// CompletedNodesList reflects the list of nodes that have completed kata operation
	// +optional
	CompletedNodesList []string `json:"completedNodesList,omitempty"`
}

// KataFailedNodeStatus reflects the status of nodes that have failed kata operation
type KataFailedNodeStatus struct {
	// FailedNodesCount reflects the number of nodes that have failed kata operation
	FailedNodesCount int `json:"failedNodesCount,omitempty"`

	// FailedNodesList reflects the list of nodes that have failed kata operation
	// +optional
	FailedNodesList []FailedNodeStatus `json:"failedNodesList,omitempty"`
}

// KataUnInstallationStatus reflects the status of the ongoing kata uninstallation
type KataUnInstallationStatus struct {
	// InProgress reflects the status of nodes that are in the process of kata uninstallation
	InProgress KataUnInstallationInProgressStatus `json:"inProgress,omitempty"`

	// Completed reflects the status of nodes that have completed kata uninstallation
	Completed KataConfigCompletedStatus `json:"completed,omitempty"`

	// Failed reflects the status of nodes that have failed kata uninstallation
	Failed KataFailedNodeStatus `json:"failed,omitempty"`
}

// KataUnInstallationInProgressStatus reflects the status of nodes that are in the process of kata installation
type KataUnInstallationInProgressStatus struct {
	InProgressNodesCount int `json:"inProgressNodesCount,omitempty"`
	// +optional
	BinariesUnInstalledNodesList []string `json:"binariesUninstallNodesList,omitempty"`
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
