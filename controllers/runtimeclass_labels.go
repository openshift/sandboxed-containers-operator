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
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
)

const managedRuntimeClassLabelsAnnotation = "kataconfiguration.openshift.io/managed-runtimeclass-labels"

func (r *KataConfigOpenShiftReconciler) ownsRuntimeClass(rc *nodev1.RuntimeClass) bool {
	owner := metav1.GetControllerOf(rc)
	return r.kataConfig.UID != "" && owner != nil && owner.UID == r.kataConfig.UID &&
		owner.Name == r.kataConfig.Name &&
		schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind).GroupKind() ==
			(schema.GroupKind{Group: kataconfigurationv1.GroupVersion.Group, Kind: "KataConfig"})
}

// applyRuntimeClassLabels changes only configured labels and keys managed by a
// previous configuration. The annotation records keys so removal survives restarts.
func (r *KataConfigOpenShiftReconciler) applyRuntimeClassLabels(rc *nodev1.RuntimeClass) error {
	if !r.kataConfig.DeletionTimestamp.IsZero() || !rc.DeletionTimestamp.IsZero() || !r.ownsRuntimeClass(rc) {
		return nil
	}

	var previous []string
	if value, ok := rc.Annotations[managedRuntimeClassLabelsAnnotation]; ok {
		if err := json.Unmarshal([]byte(value), &previous); err != nil {
			return fmt.Errorf("read managed label keys on RuntimeClass %s: %w", rc.Name, err)
		}
	}

	desired := map[string]string{}
	if config := r.kataConfig.Spec.RuntimeClassLabels; config != nil {
		maps.Copy(desired, config.Defaults)
		maps.Copy(desired, config.Overrides[rc.Name])
	}
	for _, key := range previous {
		if _, configured := desired[key]; !configured {
			delete(rc.Labels, key)
		}
	}
	if len(desired) == 0 {
		delete(rc.Annotations, managedRuntimeClassLabelsAnnotation)
		return nil
	}
	if rc.Labels == nil {
		rc.Labels = map[string]string{}
	}
	maps.Copy(rc.Labels, desired)
	keys := slices.Sorted(maps.Keys(desired))
	encoded, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("encode managed label keys: %w", err)
	}
	if rc.Annotations == nil {
		rc.Annotations = map[string]string{}
	}
	rc.Annotations[managedRuntimeClassLabelsAnnotation] = string(encoded)
	return nil
}

// reconcileRuntimeClassLabels runs before installation checks so existing classes
// receive label changes even when no nodes currently pass eligibility checks.
func (r *KataConfigOpenShiftReconciler) reconcileRuntimeClassLabels(ctx context.Context) error {
	if !r.kataConfig.DeletionTimestamp.IsZero() {
		return nil
	}
	classes := &nodev1.RuntimeClassList{}
	if err := r.List(ctx, classes); err != nil {
		return fmt.Errorf("list RuntimeClasses for labels: %w", err)
	}
	for i := range classes.Items {
		rc := &classes.Items[i]
		before := rc.DeepCopy()
		if err := r.applyRuntimeClassLabels(rc); err != nil {
			return err
		}
		if reflect.DeepEqual(before.Labels, rc.Labels) && reflect.DeepEqual(before.Annotations, rc.Annotations) {
			continue
		}
		patch := client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})
		if err := r.Patch(ctx, rc, patch); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("patch labels on RuntimeClass %s: %w", rc.Name, err)
		}
	}
	return nil
}
