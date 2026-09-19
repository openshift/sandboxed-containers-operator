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

package controllers

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	nfdNamespace        = "openshift-nfd"
	oscNodeTypeRuleName = "osc-node-type"
)

var nodeFeatureRuleGVR = schema.GroupVersionResource{
	Group:    "nfd.openshift.io",
	Version:  "v1alpha1",
	Resource: "nodefeaturerules",
}

// oscNodeTypeRule constructs the NodeFeatureRule that labels nodes as
// bare-metal or virtual based on the x86 CPUID hypervisor-present bit.
func (r *KataConfigOpenShiftReconciler) oscNodeTypeRule() *unstructured.Unstructured {
	rule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "nfd.openshift.io/v1alpha1",
			"kind":       "NodeFeatureRule",
			"metadata": map[string]interface{}{
				"name":      oscNodeTypeRuleName,
				"namespace": nfdNamespace,
			},
			"spec": map[string]interface{}{
				"rules": []interface{}{
					map[string]interface{}{
						"name": "osc-bare-metal-node",
						"labels": map[string]interface{}{
							"kataconfiguration.openshift.io/node-type": "bare-metal",
						},
						"matchFeatures": []interface{}{
							map[string]interface{}{
								"feature": "cpu.cpuid",
								"matchExpressions": map[string]interface{}{
									"HYPERVISOR": map[string]interface{}{"op": "DoesNotExist"},
								},
							},
						},
					},
					map[string]interface{}{
						"name": "osc-virtual-node",
						"labels": map[string]interface{}{
							"kataconfiguration.openshift.io/node-type": "virtual",
						},
						"matchFeatures": []interface{}{
							map[string]interface{}{
								"feature": "cpu.cpuid",
								"matchExpressions": map[string]interface{}{
									"HYPERVISOR": map[string]interface{}{"op": "Exists"},
								},
							},
						},
					},
				},
			},
		},
	}
	return rule
}

// ensureNodeFeatureRule creates or updates the osc-node-type NodeFeatureRule.
// Sets KataConfig as the owner so the rule is garbage collected on deletion.
func (r *KataConfigOpenShiftReconciler) ensureNodeFeatureRule() error {
	desired := r.oscNodeTypeRule()

	if err := controllerutil.SetControllerReference(r.kataConfig, desired, r.Scheme); err != nil {
		return err
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())

	err := r.Client.Get(context.TODO(), types.NamespacedName{
		Name:      oscNodeTypeRuleName,
		Namespace: nfdNamespace,
	}, existing)

	if k8serrors.IsNotFound(err) {
		r.Log.Info("Creating NodeFeatureRule", "name", oscNodeTypeRuleName)
		return r.Client.Create(context.TODO(), desired)
	}
	if err != nil {
		return err
	}

	// Update spec if it exists but differs
	existing.Object["spec"] = desired.Object["spec"]
	r.Log.Info("Updating NodeFeatureRule", "name", oscNodeTypeRuleName)
	return r.Client.Update(context.TODO(), existing)
}

// nodeFeatureRuleExists returns true if the osc-node-type NodeFeatureRule is
// present in the cluster. Used to avoid unnecessary Delete calls on every
// reconcile when EnableMixedCluster is false.
func (r *KataConfigOpenShiftReconciler) nodeFeatureRuleExists() (bool, error) {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nfd.openshift.io",
		Version: "v1alpha1",
		Kind:    "NodeFeatureRule",
	})
	err := r.Client.Get(context.TODO(), types.NamespacedName{
		Name:      oscNodeTypeRuleName,
		Namespace: nfdNamespace,
	}, existing)
	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// deleteNodeFeatureRule removes the osc-node-type NodeFeatureRule.
// A not-found error is treated as success.
func (r *KataConfigOpenShiftReconciler) deleteNodeFeatureRule() error {
	rule := &unstructured.Unstructured{}
	rule.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nfd.openshift.io",
		Version: "v1alpha1",
		Kind:    "NodeFeatureRule",
	})
	rule.SetName(oscNodeTypeRuleName)
	rule.SetNamespace(nfdNamespace)

	err := r.Client.Delete(context.TODO(), rule)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return err
}

// checkNFDInstalled verifies the NFD Operator is present by checking for the
// NodeFeatureDiscovery CRD. Returns an error with a clear message if absent.
func (r *KataConfigOpenShiftReconciler) checkNFDInstalled() error {
	nfdList := &unstructured.UnstructuredList{}
	nfdList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nfd.openshift.io",
		Version: "v1",
		Kind:    "NodeFeatureDiscoveryList",
	})
	if err := r.Client.List(context.TODO(), nfdList); err != nil {
		if k8serrors.IsNotFound(err) || isNoKindMatchError(err) {
			return fmt.Errorf("NFD Operator is not installed: enableMixedCluster requires the Node Feature Discovery Operator and a NodeFeatureDiscovery CR")
		}
		return err
	}
	if len(nfdList.Items) == 0 {
		return fmt.Errorf("NFD Operator is installed but no NodeFeatureDiscovery CR found: create a NodeFeatureDiscovery CR in %s", nfdNamespace)
	}
	return nil
}

// isNoKindMatchError returns true when the error indicates the CRD does not exist.
func isNoKindMatchError(err error) bool {
	if err == nil {
		return false
	}
	return meta.IsNoMatchError(err)
}
