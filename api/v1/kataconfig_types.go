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
	// For more information on how the check works, please refer to the sandboxed containers documentation - https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.6/html-single/user_guide/index#about-node-eligibility-checks_about-osc
	// +kubebuilder:default:=false
	CheckNodeEligibility bool `json:"checkNodeEligibility"`

	// Sets log level on kata-equipped nodes.  Valid values are the same as for `crio --log-level`.
	// +kubebuilder:default:="info"
	LogLevel string `json:"logLevel,omitempty"`

	// EnablePeerPods is used to transparently create pods on a remote system.
	// For more information on how this works, please refer to the sandboxed containers documentation - https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.6/html/user_guide/deploying-public-cloud#deploying-public-cloud
	// +optional
	// +kubebuilder:default:=false
	EnablePeerPods bool `json:"enablePeerPods"`
}

// KataConfigStatus defines the observed state of KataConfig
type KataConfigStatus struct {
	// RuntimeClasses is the names of the RuntimeClasses created by this controller
	// +optional
	RuntimeClasses []string `json:"runtimeClasses"`

	// +optional
	KataNodes KataNodesStatus `json:"kataNodes,omitempty"`

	// +optional
	Conditions []KataConfigCondition `json:"conditions,omitempty"`

	// Used internally to persist state between reconciliations
	// +optional
	// +kubebuilder:default:=false
	WaitingForMcoToStart bool `json:"waitingForMcoToStart,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KataConfig is the Schema for the kataconfigs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=kataconfigs,scope=Cluster
// +kubebuilder:printcolumn:name="InProgress",type=string,JSONPath=".status.conditions[?(@.type=='InProgress')].status",description="Status of Kata runtime installation"
// +kubebuilder:printcolumn:name="Completed",type=integer,JSONPath=".status.kataNodes.readyNodeCount",description="Number of nodes with Kata runtime installed"
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=".status.kataNodes.nodeCount",description="Total number of nodes"
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

type KataNodesStatus struct {
	// Number of cluster nodes that have kata installed on them including
	// those queued for installation and currently installing, though
	// excluding nodes that have a kata installation but are queued for
	// uninstallation or currently uninstalling.
	// +optional
	NodeCount int `json:"nodeCount"`

	// Number of cluster nodes that have kata installed on them and are
	// currently ready to run kata workloads.
	// +optional
	ReadyNodeCount int `json:"readyNodeCount"`

	// +optional
	Installed []string `json:"installed,omitempty"`
	// +optional
	Installing []string `json:"installing,omitempty"`
	// +optional
	WaitingToInstall []string `json:"waitingToInstall,omitempty"`
	// +optional
	FailedToInstall []string `json:"failedToInstall,omitempty"`

	// +optional
	Uninstalling []string `json:"uninstalling,omitempty"`
	// +optional
	WaitingToUninstall []string `json:"waitingToUninstall,omitempty"`
	// +optional
	FailedToUninstall []string `json:"failedToUninstall,omitempty"`
}

type KataConfigConditionType string

const (
	KataConfigInProgress KataConfigConditionType = "InProgress"
)

type KataConfigCondition struct {
	Type               KataConfigConditionType `json:"type"`
	Status             corev1.ConditionStatus  `json:"status"`
	LastTransitionTime metav1.Time             `json:"lastTransitionTime"`
	Reason             string                  `json:"reason"`
	Message            string                  `json:"message"`
}
