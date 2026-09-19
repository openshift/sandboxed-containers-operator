package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
)

// nfdProbeClient stubs List for NodeFeatureDiscovery probes so we can simulate
// NFD being absent (NoKindMatchError) without a real API server.
type nfdProbeClient struct {
	client.Client
	nfdListErr error
}

func (c *nfdProbeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if u, ok := list.(*unstructured.UnstructuredList); ok && u.GetKind() == "NodeFeatureDiscoveryList" {
		if c.nfdListErr != nil {
			return c.nfdListErr
		}
	}
	return c.Client.List(ctx, list, opts...)
}

func newTestReconciler(c client.Client) *KataConfigOpenShiftReconciler {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kataconfigurationv1.AddToScheme(scheme)
	return &KataConfigOpenShiftReconciler{
		Client:     c,
		Log:        logr.Discard(),
		Scheme:     scheme,
		kataConfig: &kataconfigurationv1.KataConfig{ObjectMeta: metav1.ObjectMeta{Name: "test-kataconfig"}},
	}
}

// ── oscNodeTypeRule ──────────────────────────────────────────────────────────

func TestOscNodeTypeRule_Metadata(t *testing.T) {
	t.Parallel()
	r := newTestReconciler(fake.NewClientBuilder().Build())
	rule := r.oscNodeTypeRule()

	if rule.GetName() != oscNodeTypeRuleName {
		t.Errorf("name: got %q, want %q", rule.GetName(), oscNodeTypeRuleName)
	}
	if rule.GetNamespace() != nfdNamespace {
		t.Errorf("namespace: got %q, want %q", rule.GetNamespace(), nfdNamespace)
	}
	if rule.GetAPIVersion() != "nfd.openshift.io/v1alpha1" {
		t.Errorf("apiVersion: got %q", rule.GetAPIVersion())
	}
	if rule.GetKind() != "NodeFeatureRule" {
		t.Errorf("kind: got %q", rule.GetKind())
	}
}

func TestOscNodeTypeRule_TwoRules(t *testing.T) {
	t.Parallel()
	r := newTestReconciler(fake.NewClientBuilder().Build())
	rule := r.oscNodeTypeRule()

	rules, _, _ := unstructured.NestedSlice(rule.Object, "spec", "rules")
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestOscNodeTypeRule_BareMetalRule(t *testing.T) {
	t.Parallel()
	r := newTestReconciler(fake.NewClientBuilder().Build())
	rule := r.oscNodeTypeRule()

	rules, _, _ := unstructured.NestedSlice(rule.Object, "spec", "rules")
	bm := rules[0].(map[string]interface{})

	if bm["name"] != "osc-bare-metal-node" {
		t.Errorf("bare-metal rule name: got %q", bm["name"])
	}
	labels := bm["labels"].(map[string]interface{})
	if labels["kataconfiguration.openshift.io/node-type"] != "bare-metal" {
		t.Errorf("bare-metal label value: got %q", labels["kataconfiguration.openshift.io/node-type"])
	}
	// HYPERVISOR must use DoesNotExist
	features := bm["matchFeatures"].([]interface{})[0].(map[string]interface{})
	exprs := features["matchExpressions"].(map[string]interface{})
	hypervisor := exprs["HYPERVISOR"].(map[string]interface{})
	if hypervisor["op"] != "DoesNotExist" {
		t.Errorf("bare-metal HYPERVISOR op: got %q, want DoesNotExist", hypervisor["op"])
	}
}

func TestOscNodeTypeRule_VirtualRule(t *testing.T) {
	t.Parallel()
	r := newTestReconciler(fake.NewClientBuilder().Build())
	rule := r.oscNodeTypeRule()

	rules, _, _ := unstructured.NestedSlice(rule.Object, "spec", "rules")
	vm := rules[1].(map[string]interface{})

	if vm["name"] != "osc-virtual-node" {
		t.Errorf("virtual rule name: got %q", vm["name"])
	}
	labels := vm["labels"].(map[string]interface{})
	if labels["kataconfiguration.openshift.io/node-type"] != "virtual" {
		t.Errorf("virtual label value: got %q", labels["kataconfiguration.openshift.io/node-type"])
	}
	// HYPERVISOR must use Exists
	features := vm["matchFeatures"].([]interface{})[0].(map[string]interface{})
	exprs := features["matchExpressions"].(map[string]interface{})
	hypervisor := exprs["HYPERVISOR"].(map[string]interface{})
	if hypervisor["op"] != "Exists" {
		t.Errorf("virtual HYPERVISOR op: got %q, want Exists", hypervisor["op"])
	}
}

// ── ensureNodeFeatureRule ────────────────────────────────────────────────────

func TestEnsureNodeFeatureRule_CreatesWhenAbsent(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kataconfigurationv1.AddToScheme(scheme)

	r := &KataConfigOpenShiftReconciler{
		Client:     fake.NewClientBuilder().WithScheme(scheme).Build(),
		Log:        logr.Discard(),
		Scheme:     scheme,
		kataConfig: &kataconfigurationv1.KataConfig{ObjectMeta: metav1.ObjectMeta{Name: "test-kataconfig"}},
	}

	if err := r.ensureNodeFeatureRule(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "nfd.openshift.io", Version: "v1alpha1", Kind: "NodeFeatureRule",
	})
	if err := r.Client.Get(context.TODO(), client.ObjectKey{
		Name: oscNodeTypeRuleName, Namespace: nfdNamespace,
	}, got); err != nil {
		t.Fatalf("NodeFeatureRule not found after ensure: %v", err)
	}
}

func TestEnsureNodeFeatureRule_UpdatesWhenExists(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kataconfigurationv1.AddToScheme(scheme)

	// Pre-existing rule with stale/empty spec
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "nfd.openshift.io", Version: "v1alpha1", Kind: "NodeFeatureRule",
	})
	existing.SetName(oscNodeTypeRuleName)
	existing.SetNamespace(nfdNamespace)
	_ = unstructured.SetNestedMap(existing.Object, map[string]interface{}{}, "spec")

	r := &KataConfigOpenShiftReconciler{
		Client:     fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build(),
		Log:        logr.Discard(),
		Scheme:     scheme,
		kataConfig: &kataconfigurationv1.KataConfig{ObjectMeta: metav1.ObjectMeta{Name: "test-kataconfig"}},
	}

	if err := r.ensureNodeFeatureRule(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "nfd.openshift.io", Version: "v1alpha1", Kind: "NodeFeatureRule",
	})
	if err := r.Client.Get(context.TODO(), client.ObjectKey{
		Name: oscNodeTypeRuleName, Namespace: nfdNamespace,
	}, got); err != nil {
		t.Fatalf("get after update: %v", err)
	}
	rules, _, _ := unstructured.NestedSlice(got.Object, "spec", "rules")
	if len(rules) != 2 {
		t.Errorf("expected 2 rules after update, got %d", len(rules))
	}
}

// ── deleteNodeFeatureRule ────────────────────────────────────────────────────

func TestDeleteNodeFeatureRule_DeletesWhenExists(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kataconfigurationv1.AddToScheme(scheme)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "nfd.openshift.io", Version: "v1alpha1", Kind: "NodeFeatureRule",
	})
	existing.SetName(oscNodeTypeRuleName)
	existing.SetNamespace(nfdNamespace)

	r := &KataConfigOpenShiftReconciler{
		Client:     fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build(),
		Log:        logr.Discard(),
		Scheme:     scheme,
		kataConfig: &kataconfigurationv1.KataConfig{ObjectMeta: metav1.ObjectMeta{Name: "test-kataconfig"}},
	}

	if err := r.deleteNodeFeatureRule(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "nfd.openshift.io", Version: "v1alpha1", Kind: "NodeFeatureRule",
	})
	err := r.Client.Get(context.TODO(), client.ObjectKey{
		Name: oscNodeTypeRuleName, Namespace: nfdNamespace,
	}, got)
	if err == nil {
		t.Fatal("expected NodeFeatureRule to be deleted but it still exists")
	}
}

func TestDeleteNodeFeatureRule_NoErrorWhenAbsent(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kataconfigurationv1.AddToScheme(scheme)

	r := &KataConfigOpenShiftReconciler{
		Client:     fake.NewClientBuilder().WithScheme(scheme).Build(),
		Log:        logr.Discard(),
		Scheme:     scheme,
		kataConfig: &kataconfigurationv1.KataConfig{ObjectMeta: metav1.ObjectMeta{Name: "test-kataconfig"}},
	}

	if err := r.deleteNodeFeatureRule(); err != nil {
		t.Fatalf("expected no error for absent rule, got: %v", err)
	}
}

// ── nodeFeatureRuleExists ────────────────────────────────────────────────────

func TestNodeFeatureRuleExists_ReturnsFalseWhenAbsent(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kataconfigurationv1.AddToScheme(scheme)

	r := &KataConfigOpenShiftReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Log:    logr.Discard(),
		Scheme: scheme,
	}

	exists, err := r.nodeFeatureRuleExists()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Fatal("expected false when rule is absent")
	}
}

func TestNodeFeatureRuleExists_ReturnsTrueWhenPresent(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kataconfigurationv1.AddToScheme(scheme)

	rule := &unstructured.Unstructured{}
	rule.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "nfd.openshift.io", Version: "v1alpha1", Kind: "NodeFeatureRule",
	})
	rule.SetName(oscNodeTypeRuleName)
	rule.SetNamespace(nfdNamespace)

	r := &KataConfigOpenShiftReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(rule).Build(),
		Log:    logr.Discard(),
		Scheme: scheme,
	}

	exists, err := r.nodeFeatureRuleExists()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("expected true when rule is present")
	}
}

// ── checkNFDInstalled ────────────────────────────────────────────────────────

func TestCheckNFDInstalled_ErrorWhenCRDAbsent(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	noMatch := &meta.NoKindMatchError{
		GroupKind:        schema.GroupKind{Group: "nfd.openshift.io", Kind: "NodeFeatureDiscovery"},
		SearchedVersions: []string{"v1"},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &KataConfigOpenShiftReconciler{
		Client: &nfdProbeClient{Client: base, nfdListErr: noMatch},
		Log:    logr.Discard(),
		Scheme: scheme,
	}

	if err := r.checkNFDInstalled(); err == nil {
		t.Fatal("expected error when NFD CRD is absent")
	}
}

func TestCheckNFDInstalled_ErrorWhenNoCRExists(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	// NFD CRD is registered but no NodeFeatureDiscovery CRs exist
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &KataConfigOpenShiftReconciler{
		Client: base,
		Log:    logr.Discard(),
		Scheme: scheme,
	}

	if err := r.checkNFDInstalled(); err == nil {
		t.Fatal("expected error when no NodeFeatureDiscovery CR exists")
	}
}

func TestCheckNFDInstalled_SuccessWhenCRExists(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	nfdCR := &unstructured.Unstructured{}
	nfdCR.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "nfd.openshift.io", Version: "v1", Kind: "NodeFeatureDiscovery",
	})
	nfdCR.SetName("nfd-instance")
	nfdCR.SetNamespace(nfdNamespace)

	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nfdCR).Build()
	r := &KataConfigOpenShiftReconciler{
		Client: base,
		Log:    logr.Discard(),
		Scheme: scheme,
	}

	if err := r.checkNFDInstalled(); err != nil {
		t.Fatalf("unexpected error when NFD CR exists: %v", err)
	}
}
