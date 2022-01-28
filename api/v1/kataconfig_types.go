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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// KataConfigSpec defines the desired state of KataConfig
type KataConfigSpec struct {
	// KataConfigPoolSelector is used to filter the worker nodes
	// if not specified, all worker nodes are selected
	// +optional
	// +nullable
	KataConfigPoolSelector *metav1.LabelSelector `json:"kataConfigPoolSelector"`

	// CheckNodeEligibility is used to detect the node(s) eligibility to run Kata containers.
	// This is currently done through the use of the Node Feature Discovery Operator (NFD).
	// For more information on how the check works, please refer to the sandboxed containers documentation - https://docs.openshift.com/container-platform/latest/sandboxed_containers/deploying-sandboxed-container-workloads.html
	// +kubebuilder:default:=false
	CheckNodeEligibility bool `json:"checkNodeEligibility"`

	// KataMonitorImage is used to specify the container image used for kata-monitor
	// +kubebuilder:default:="quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-monitor:latest"
	KataMonitorImage string `json:"kataMonitorImage"`

	// Sets log level on kata-equipped nodes.  Valid values are the same as for `crio --log-level`.
	// +kubebuilder:default:="info"
	LogLevel string `json:"logLevel,omitempty"`
}

// KataConfigStatus defines the observed state of KataConfig
type KataConfigStatus struct {
	// RuntimeClass is the name of the runtime class used in CRIO configuration
	RuntimeClass string `json:"runtimeClass"`

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

	BaseMcpGeneration int64 `json:"prevMcpGeneration"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KataConfig is the Schema for the kataconfigs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=kataconfigs,scope=Cluster
// +kubebuilder:printcolumn:name="InProgress",type=string,JSONPath=".status.installationStatus.IsInProgress",description="Status of Kata runtime installation"
// +kubebuilder:printcolumn:name="Completed",type=integer,JSONPath=".status.installationStatus.completed.completedNodesCount",description="Number of nodes with Kata runtime installed"
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=".status.totalNodesCount",description="Total number of nodes"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp",description="Age of the KataConfig Custom Resource"
type KataConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	// +nullable
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

// KataInstallationStatus reflects the status of the ongoing kata installation
type KataInstallationStatus struct {
	// InProgress reflects the status of nodes that are in the process of kata installation
	InProgress KataInstallationInProgressStatus `json:"inprogress,omitempty"`

	// IsInProgress reflects the current state of installing or not installing
	IsInProgress corev1.ConditionStatus `json:"IsInProgress,omit"`

	// Completed reflects the status of nodes that have completed kata installation
	Completed KataConfigCompletedStatus `json:"completed,omitempty"`

	// Failed reflects the status of nodes that have failed kata installation
	Failed KataFailedNodeStatus `json:"failed,omitempty"`
}

// KataInstallationInProgressStatus reflects the status of nodes that are in the process of kata installation
type KataInstallationInProgressStatus struct {
	// InProgressNodesCount reflects the number of nodes that are in the process of kata installation
	InProgressNodesCount int `json:"inProgressNodesCount,omitempty"`
	// IsInProgress reflects if installation is still in progress
	IsInProgress bool `json:"isInProgress,omitempty"`
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
	FailedNodesCount int    `json:"failedNodesCount,omitempty"`
	FailedReason     string `json:"failedNodesReason,omitempty"`

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

	// Stores an error message if any.  Note that this is currently meant for a single
	// failure source when kata uninstallation is blocked by existing kata-based pods, so
	// handling of this field in the controller code is correspondingly simple.  A review
	// might be necessary if this field were ever to store messages coming from another
	// source.
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// KataUnInstallationInProgressStatus reflects the status of nodes that are in the process of kata installation
type KataUnInstallationInProgressStatus struct {
	InProgressNodesCount int                    `json:"inProgressNodesCount,omitempty"`
	IsInProgress         corev1.ConditionStatus `json:"status"`
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
