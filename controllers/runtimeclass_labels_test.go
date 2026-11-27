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
	"errors"
	"reflect"
	"strings"
	"testing"

	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
)

type runtimeClassLabelClient struct {
	client.Client
	patches     int
	patchError  error
	listError   error
	onCreate    func(*nodev1.RuntimeClass)
	beforePatch func()
}

func (c *runtimeClassLabelClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patches++
	if c.beforePatch != nil {
		c.beforePatch()
	}
	if c.patchError != nil {
		return c.patchError
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *runtimeClassLabelClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.listError != nil {
		return c.listError
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *runtimeClassLabelClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if rc, ok := obj.(*nodev1.RuntimeClass); ok && c.onCreate != nil {
		c.onCreate(rc)
	}
	return c.Client.Create(ctx, obj, opts...)
}

func newLabelReconciler(t *testing.T) (*KataConfigOpenShiftReconciler, *runtimeClassLabelClient) {
	t.Helper()
	r := newRCReconciler(t, kataconfigurationv1.KataConfigSpec{})
	r.kataConfig.UID = "kataconfig-uid"
	c := &runtimeClassLabelClient{Client: r.Client}
	r.Client = c
	return r, c
}

func createOwnedLabelClass(t *testing.T, r *KataConfigOpenShiftReconciler, name string) *nodev1.RuntimeClass {
	t.Helper()
	rc := &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: name}, Handler: name}
	if err := controllerutil.SetControllerReference(r.kataConfig, rc, r.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	return rc
}

func readLabelClass(t *testing.T, r *KataConfigOpenShiftReconciler, name string) *nodev1.RuntimeClass {
	t.Helper()
	rc := &nodev1.RuntimeClass{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: name}, rc); err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestRuntimeClassLabelsCreateAndRecreate(t *testing.T) {
	r, c := newLabelReconciler(t)
	r.kataConfig.Spec.RuntimeClassLabels = &kataconfigurationv1.RuntimeClassLabels{
		Defaults: map[string]string{"example.com/approved": "true"},
		Overrides: map[string]map[string]string{
			"kata-nvidia-gpu": {"example.com/approved": "gpu", "example.com/device": "gpu"},
			"disabled-class":  {"example.com/approved": "false"},
		},
	}
	c.onCreate = func(rc *nodev1.RuntimeClass) {
		if rc.Labels["example.com/approved"] == "" || rc.Annotations[managedRuntimeClassLabelsAnnotation] == "" {
			t.Errorf("RuntimeClass %s must carry labels in its create request", rc.Name)
		}
	}
	for _, name := range []string{"kata", "kata-nvidia-gpu", "kata-cc", "kata-cc-nvidia-gpu", "kata-remote", "kata-future"} {
		if err := r.createRuntimeClass(name, "250m", "160Mi", "", name, nil); err != nil {
			t.Fatal(err)
		}
		rc := readLabelClass(t, r, name)
		want := "true"
		if name == "kata-nvidia-gpu" {
			want = "gpu"
			if rc.Labels["example.com/device"] != "gpu" {
				t.Error("per-class label missing")
			}
		}
		if rc.Labels["example.com/approved"] != want {
			t.Errorf("%s: labels = %v", name, rc.Labels)
		}
	}
	if err := r.reconcileRuntimeClassLabels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.patches != 0 {
		t.Errorf("unchanged labels caused %d patches", c.patches)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "disabled-class"}, &nodev1.RuntimeClass{}); !apierrors.IsNotFound(err) {
		t.Fatalf("override created an absent class: %v", err)
	}
	rc := readLabelClass(t, r, "kata")
	rc.Finalizers = nil
	if err := r.Update(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if err := r.createRuntimeClass("kata", "250m", "160Mi", "", "kata", nil); err != nil {
		t.Fatal(err)
	}
	if got := readLabelClass(t, r, "kata").Labels["example.com/approved"]; got != "true" {
		t.Errorf("recreated class label = %q", got)
	}
}

func TestRuntimeClassLabelsLifecycle(t *testing.T) {
	r, c := newLabelReconciler(t)
	rc := createOwnedLabelClass(t, r, "kata")
	rc.Labels = map[string]string{"other.example/label": "keep", "example.com/approved": "old"}
	rc.Annotations = map[string]string{"other.example/annotation": "keep"}
	if err := r.Update(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	r.kataConfig.Spec.CheckNodeEligibility = true
	r.kataConfig.Spec.RuntimeClassLabels = &kataconfigurationv1.RuntimeClassLabels{
		Defaults:  map[string]string{"example.com/approved": "true", "example.com/common": "shared"},
		Overrides: map[string]map[string]string{"kata": {"example.com/approved": "override"}},
	}
	reconcile := func() *nodev1.RuntimeClass {
		t.Helper()
		if err := r.reconcileRuntimeClassLabels(context.Background()); err != nil {
			t.Fatal(err)
		}
		return readLabelClass(t, r, "kata")
	}
	rc = reconcile()
	if rc.Labels["example.com/approved"] != "override" || rc.Labels["example.com/common"] != "shared" {
		t.Fatalf("existing class with no eligible nodes: labels = %v", rc.Labels)
	}
	if got := rc.Annotations[managedRuntimeClassLabelsAnnotation]; got != `["example.com/approved","example.com/common"]` {
		t.Errorf("managed keys = %q", got)
	}
	count := c.patches
	reconcile()
	if c.patches != count {
		t.Error("no-op reconciliation patched the class")
	}
	rc.Labels["example.com/approved"] = "drift"
	if err := r.Update(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if got := reconcile().Labels["example.com/approved"]; got != "override" {
		t.Fatalf("drift was not repaired: %q", got)
	}
	r.kataConfig.Spec.RuntimeClassLabels.Overrides = nil
	delete(r.kataConfig.Spec.RuntimeClassLabels.Defaults, "example.com/common")
	rc = reconcile()
	if _, exists := rc.Labels["example.com/common"]; exists || rc.Labels["example.com/approved"] != "true" {
		t.Fatalf("removed override/default: labels = %v", rc.Labels)
	}
	r.kataConfig.Spec.RuntimeClassLabels = nil
	rc = reconcile()
	if !reflect.DeepEqual(rc.Labels, map[string]string{"other.example/label": "keep"}) ||
		!reflect.DeepEqual(rc.Annotations, map[string]string{"other.example/annotation": "keep"}) {
		t.Fatalf("cleanup changed unrelated metadata: labels=%v annotations=%v", rc.Labels, rc.Annotations)
	}
	if rc.Handler != "kata" || len(rc.OwnerReferences) != 1 {
		t.Fatal("label reconciliation changed runtime configuration or ownership")
	}
}

func TestRuntimeClassLabelsOwnershipAndDeletion(t *testing.T) {
	for _, name := range []string{"unowned", "stale-uid", "wrong-group", "wrong-kind", "wrong-name", "non-controller", "deleting-class", "deleting-config", "empty-uid"} {
		t.Run(name, func(t *testing.T) {
			r, c := newLabelReconciler(t)
			r.kataConfig.Spec.RuntimeClassLabels = &kataconfigurationv1.RuntimeClassLabels{Defaults: map[string]string{"example.com/approved": "true"}}
			rc := createOwnedLabelClass(t, r, "kata")
			switch name {
			case "unowned":
				rc.OwnerReferences = nil
			case "stale-uid":
				rc.OwnerReferences[0].UID = "old-config"
			case "wrong-group":
				rc.OwnerReferences[0].APIVersion = "other.example/v1"
			case "wrong-kind":
				rc.OwnerReferences[0].Kind = "OtherConfig"
			case "wrong-name":
				rc.OwnerReferences[0].Name = "other-config"
			case "non-controller":
				rc.OwnerReferences[0].Controller = nil
			case "deleting-class":
				rc.Finalizers = []string{"test.example/hold"}
			case "deleting-config":
				now := metav1.Now()
				r.kataConfig.DeletionTimestamp = &now
			case "empty-uid":
				r.kataConfig.UID = ""
			}
			if err := r.Update(context.Background(), rc); err != nil {
				t.Fatal(err)
			}
			if name == "deleting-class" {
				if err := r.Delete(context.Background(), rc); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.reconcileRuntimeClassLabels(context.Background()); err != nil {
				t.Fatal(err)
			}
			if c.patches != 0 {
				t.Errorf("patched excluded class %d times", c.patches)
			}
		})
	}
}

func TestRuntimeClassLabelsErrors(t *testing.T) {
	for _, name := range []string{"list", "conflict", "annotation"} {
		t.Run(name, func(t *testing.T) {
			r, c := newLabelReconciler(t)
			rc := createOwnedLabelClass(t, r, "kata")
			r.kataConfig.Spec.RuntimeClassLabels = &kataconfigurationv1.RuntimeClassLabels{Defaults: map[string]string{"example.com/approved": "true"}}
			switch name {
			case "list":
				c.listError = errors.New("list failed")
			case "conflict":
				c.patchError = apierrors.NewConflict(schema.GroupResource{Group: "node.k8s.io", Resource: "runtimeclasses"}, "kata", errors.New("changed"))
			case "annotation":
				rc.Annotations = map[string]string{managedRuntimeClassLabelsAnnotation: "broken-json"}
				if err := r.Update(context.Background(), rc); err != nil {
					t.Fatal(err)
				}
			}
			err := r.reconcileRuntimeClassLabels(context.Background())
			if err == nil {
				t.Fatal("expected reconciliation error")
			}
			if name == "conflict" && !apierrors.IsConflict(err) {
				t.Fatalf("lost conflict error: %v", err)
			}
			if name == "annotation" && !strings.Contains(err.Error(), "managed label keys") {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(readLabelClass(t, r, "kata").Labels) != 0 {
				t.Fatal("failed reconciliation changed labels")
			}
		})
	}
}

func TestRuntimeClassLabelsAndSchedulingUpdate(t *testing.T) {
	r, _ := newLabelReconciler(t)
	createOwnedLabelClass(t, r, "kata")
	r.kataConfig.Spec.EnableMixedCluster = true
	r.kataConfig.Spec.RuntimeClassLabels = &kataconfigurationv1.RuntimeClassLabels{Defaults: map[string]string{"example.com/approved": "true"}}
	if err := r.createRuntimeClass("kata", "250m", "160Mi", "", "kata", nil); err != nil {
		t.Fatal(err)
	}
	rc := readLabelClass(t, r, "kata")
	if rc.Scheduling.NodeSelector[kataCapableLabelKey] != "true" || rc.Labels["example.com/approved"] != "true" {
		t.Fatalf("labels and scheduling must both update: %#v", rc)
	}
}

func TestRuntimeClassLabelsPreserveUnmanagedMetadata(t *testing.T) {
	for _, config := range []*kataconfigurationv1.RuntimeClassLabels{nil, {}} {
		r, c := newLabelReconciler(t)
		r.kataConfig.Spec.RuntimeClassLabels = config
		rc := createOwnedLabelClass(t, r, "kata")
		rc.Labels = map[string]string{"example.com/approved": "manual"}
		if err := r.Update(context.Background(), rc); err != nil {
			t.Fatal(err)
		}
		if err := r.reconcileRuntimeClassLabels(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c.patches != 0 || readLabelClass(t, r, "kata").Labels["example.com/approved"] != "manual" {
			t.Fatal("absent or empty configuration changed an unmanaged label")
		}
	}
}

func TestRuntimeClassLabelsRejectStaleOwnership(t *testing.T) {
	r, c := newLabelReconciler(t)
	r.kataConfig.Spec.RuntimeClassLabels = &kataconfigurationv1.RuntimeClassLabels{Defaults: map[string]string{"example.com/approved": "true"}}
	createOwnedLabelClass(t, r, "kata")
	c.beforePatch = func() {
		rc := readLabelClass(t, r, "kata")
		rc.OwnerReferences[0].UID = "replacement-owner"
		if err := c.Client.Update(context.Background(), rc); err != nil {
			t.Fatal(err)
		}
	}
	err := r.reconcileRuntimeClassLabels(context.Background())
	if !apierrors.IsConflict(err) {
		t.Fatalf("patch with stale ownership must conflict: %v", err)
	}
	if len(readLabelClass(t, r, "kata").Labels) != 0 {
		t.Fatal("stale patch changed labels after ownership changed")
	}
}
