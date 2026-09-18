package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newTestReconciler creates a KataConfigOpenShiftReconciler backed by a fake client
// with the necessary scheme registrations for NetworkPolicy and KataConfig.
// The KataConfig is created in the fake client so that SetControllerReference works.
func newTestReconciler(t *testing.T, objs ...client.Object) *KataConfigOpenShiftReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := kataconfigurationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	kataConfig := &kataconfigurationv1.KataConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "example-kataconfig",
			UID:  "test-uid-1234",
		},
	}

	allObjs := []client.Object{kataConfig}
	allObjs = append(allObjs, objs...)

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjs...).
		WithStatusSubresource(&kataconfigurationv1.KataConfig{}).
		Build()

	return &KataConfigOpenShiftReconciler{
		Client:     cl,
		Log:        logr.Discard(),
		Scheme:     scheme,
		kataConfig: kataConfig,
	}
}

// fetchNetworkPolicy retrieves a NetworkPolicy by name in the operator namespace.
func fetchNetworkPolicy(t *testing.T, cl client.Client, name string) *networkingv1.NetworkPolicy {
	t.Helper()
	np := &networkingv1.NetworkPolicy{}
	err := cl.Get(context.TODO(), types.NamespacedName{
		Name:      name,
		Namespace: OperatorNamespace,
	}, np)
	if err != nil {
		t.Fatalf("failed to get NetworkPolicy %q: %v", name, err)
	}
	return np
}

// ---- Tests for spec builder functions ----

func TestDefaultDenySpec(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"app": "test-pod"}
	spec := defaultDenySpec(labels)

	// PodSelector must match the labels
	if spec.PodSelector.MatchLabels["app"] != "test-pod" {
		t.Errorf("expected PodSelector label app=test-pod, got %v", spec.PodSelector.MatchLabels)
	}

	// Must have both Ingress and Egress policy types
	if len(spec.PolicyTypes) != 2 {
		t.Fatalf("expected 2 PolicyTypes, got %d", len(spec.PolicyTypes))
	}
	hasIngress := false
	hasEgress := false
	for _, pt := range spec.PolicyTypes {
		if pt == networkingv1.PolicyTypeIngress {
			hasIngress = true
		}
		if pt == networkingv1.PolicyTypeEgress {
			hasEgress = true
		}
	}
	if !hasIngress {
		t.Error("expected PolicyTypeIngress in PolicyTypes")
	}
	if !hasEgress {
		t.Error("expected PolicyTypeEgress in PolicyTypes")
	}

	// No ingress/egress rules means deny-all
	if len(spec.Ingress) != 0 {
		t.Errorf("expected 0 Ingress rules for deny-all, got %d", len(spec.Ingress))
	}
	if len(spec.Egress) != 0 {
		t.Errorf("expected 0 Egress rules for deny-all, got %d", len(spec.Egress))
	}
}

func TestAllowAllEgressSpec(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"name": "egress-pod"}
	spec := allowAllEgressSpec(labels)

	// PodSelector
	if spec.PodSelector.MatchLabels["name"] != "egress-pod" {
		t.Errorf("expected PodSelector label name=egress-pod, got %v", spec.PodSelector.MatchLabels)
	}

	// PolicyTypes must be Egress only
	if len(spec.PolicyTypes) != 1 {
		t.Fatalf("expected 1 PolicyType, got %d", len(spec.PolicyTypes))
	}
	if spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("expected PolicyTypeEgress, got %v", spec.PolicyTypes[0])
	}

	// One empty egress rule = allow all egress
	if len(spec.Egress) != 1 {
		t.Fatalf("expected 1 Egress rule, got %d", len(spec.Egress))
	}
	if len(spec.Egress[0].To) != 0 {
		t.Errorf("expected empty To (allow-all), got %d peers", len(spec.Egress[0].To))
	}
	if len(spec.Egress[0].Ports) != 0 {
		t.Errorf("expected empty Ports (allow-all), got %d ports", len(spec.Egress[0].Ports))
	}

	// No ingress rules
	if len(spec.Ingress) != 0 {
		t.Errorf("expected 0 Ingress rules, got %d", len(spec.Ingress))
	}
}

func TestAllowDNSEgressSpec(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"app": "dns-pod"}
	spec := allowDNSEgressSpec(labels)

	// PodSelector
	if spec.PodSelector.MatchLabels["app"] != "dns-pod" {
		t.Errorf("expected PodSelector label app=dns-pod, got %v", spec.PodSelector.MatchLabels)
	}

	// PolicyTypes must be Egress only
	if len(spec.PolicyTypes) != 1 {
		t.Fatalf("expected 1 PolicyType, got %d", len(spec.PolicyTypes))
	}
	if spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("expected PolicyTypeEgress, got %v", spec.PolicyTypes[0])
	}

	// One egress rule with DNS details
	if len(spec.Egress) != 1 {
		t.Fatalf("expected 1 Egress rule, got %d", len(spec.Egress))
	}
	egressRule := spec.Egress[0]

	// Peer: namespace selector for openshift-dns
	if len(egressRule.To) != 1 {
		t.Fatalf("expected 1 peer in egress rule, got %d", len(egressRule.To))
	}
	nsSel := egressRule.To[0].NamespaceSelector
	if nsSel == nil {
		t.Fatal("expected NamespaceSelector, got nil")
	}
	if nsSel.MatchLabels["kubernetes.io/metadata.name"] != "openshift-dns" {
		t.Errorf("expected namespace selector to target openshift-dns, got %v", nsSel.MatchLabels)
	}

	// Ports: DNS port 5353 over TCP and UDP
	if len(egressRule.Ports) != 2 {
		t.Fatalf("expected 2 ports in DNS egress rule, got %d", len(egressRule.Ports))
	}

	expectedDNSPort := intstr.FromInt32(5353)
	foundTCP := false
	foundUDP := false
	for _, p := range egressRule.Ports {
		if p.Protocol == nil || p.Port == nil {
			t.Fatal("port entry missing protocol or port value")
		}
		if p.Port.IntValue() != expectedDNSPort.IntValue() {
			t.Errorf("expected port 5353, got %v", p.Port)
		}
		if *p.Protocol == corev1.ProtocolTCP {
			foundTCP = true
		}
		if *p.Protocol == corev1.ProtocolUDP {
			foundUDP = true
		}
	}
	if !foundTCP {
		t.Error("expected TCP protocol in DNS egress ports")
	}
	if !foundUDP {
		t.Error("expected UDP protocol in DNS egress ports")
	}
}

func TestAllowIngressSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		labels   map[string]string
		port     int32
		wantPort int32
	}{
		{
			name:     "webhook port 9443",
			labels:   map[string]string{"app": "webhook"},
			port:     9443,
			wantPort: 9443,
		},
		{
			name:     "metrics port 8443",
			labels:   map[string]string{"name": "monitor"},
			port:     8443,
			wantPort: 8443,
		},
		{
			name:     "arbitrary port",
			labels:   map[string]string{"app": "service"},
			port:     3000,
			wantPort: 3000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := allowIngressSpec(tt.labels, tt.port)

			// PodSelector
			for k, v := range tt.labels {
				if spec.PodSelector.MatchLabels[k] != v {
					t.Errorf("PodSelector: expected %s=%s, got %v", k, v, spec.PodSelector.MatchLabels)
				}
			}

			// PolicyTypes must be Ingress only
			if len(spec.PolicyTypes) != 1 {
				t.Fatalf("expected 1 PolicyType, got %d", len(spec.PolicyTypes))
			}
			if spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
				t.Errorf("expected PolicyTypeIngress, got %v", spec.PolicyTypes[0])
			}

			// One ingress rule
			if len(spec.Ingress) != 1 {
				t.Fatalf("expected 1 Ingress rule, got %d", len(spec.Ingress))
			}
			ingressRule := spec.Ingress[0]

			// One port with TCP
			if len(ingressRule.Ports) != 1 {
				t.Fatalf("expected 1 port, got %d", len(ingressRule.Ports))
			}
			p := ingressRule.Ports[0]
			if p.Protocol == nil || *p.Protocol != corev1.ProtocolTCP {
				t.Errorf("expected TCP protocol, got %v", p.Protocol)
			}
			if p.Port == nil || p.Port.IntValue() != int(tt.wantPort) {
				t.Errorf("expected port %d, got %v", tt.wantPort, p.Port)
			}

			// No egress rules
			if len(spec.Egress) != 0 {
				t.Errorf("expected 0 Egress rules, got %d", len(spec.Egress))
			}
		})
	}
}

// ---- Tests for ensureNetworkPolicy ----

func TestEnsureNetworkPolicy_CreateNew(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	labels := map[string]string{"app": "test"}
	spec := defaultDenySpec(labels)

	err := r.ensureNetworkPolicy(context.TODO(), "test-np", nil, spec)
	if err != nil {
		t.Fatalf("ensureNetworkPolicy failed: %v", err)
	}

	np := fetchNetworkPolicy(t, r.Client, "test-np")

	// Verify namespace
	if np.Namespace != OperatorNamespace {
		t.Errorf("expected namespace %q, got %q", OperatorNamespace, np.Namespace)
	}

	// Verify spec
	if np.Spec.PodSelector.MatchLabels["app"] != "test" {
		t.Errorf("expected spec PodSelector app=test, got %v", np.Spec.PodSelector.MatchLabels)
	}

	// Verify owner reference was set (from SetControllerReference)
	if len(np.OwnerReferences) == 0 {
		t.Fatal("expected OwnerReferences to be set")
	}
	if np.OwnerReferences[0].Name != "example-kataconfig" {
		t.Errorf("expected owner name example-kataconfig, got %q", np.OwnerReferences[0].Name)
	}
}

func TestEnsureNetworkPolicy_WithAnnotations(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	labels := map[string]string{"app": "annotated"}
	spec := allowIngressSpec(labels, 8443)

	annotations := map[string]string{
		"policy-group.network.openshift.io/host-network": "",
	}
	err := r.ensureNetworkPolicy(context.TODO(), "annotated-np", annotations, spec)
	if err != nil {
		t.Fatalf("ensureNetworkPolicy failed: %v", err)
	}

	np := fetchNetworkPolicy(t, r.Client, "annotated-np")

	if _, ok := np.Annotations["policy-group.network.openshift.io/host-network"]; !ok {
		t.Error("expected host-network annotation to be present")
	}
}

func TestEnsureNetworkPolicy_UpdateExisting(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	labels := map[string]string{"app": "update-test"}

	// Create initial policy
	specV1 := defaultDenySpec(labels)
	err := r.ensureNetworkPolicy(context.TODO(), "update-np", nil, specV1)
	if err != nil {
		t.Fatalf("initial ensureNetworkPolicy failed: %v", err)
	}

	// Verify initial state
	np := fetchNetworkPolicy(t, r.Client, "update-np")
	if len(np.Spec.PolicyTypes) != 2 {
		t.Fatalf("expected 2 PolicyTypes initially, got %d", len(np.Spec.PolicyTypes))
	}

	// Update to allow-all-egress spec
	specV2 := allowAllEgressSpec(labels)
	err = r.ensureNetworkPolicy(context.TODO(), "update-np", nil, specV2)
	if err != nil {
		t.Fatalf("update ensureNetworkPolicy failed: %v", err)
	}

	// Verify updated state
	np = fetchNetworkPolicy(t, r.Client, "update-np")
	if len(np.Spec.PolicyTypes) != 1 {
		t.Fatalf("expected 1 PolicyType after update, got %d", len(np.Spec.PolicyTypes))
	}
	if np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("expected PolicyTypeEgress after update, got %v", np.Spec.PolicyTypes[0])
	}
	if len(np.Spec.Egress) != 1 {
		t.Errorf("expected 1 Egress rule after update, got %d", len(np.Spec.Egress))
	}
}

func TestEnsureNetworkPolicy_UpdateAnnotations(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	labels := map[string]string{"app": "annot-update"}
	spec := defaultDenySpec(labels)

	// Create without annotations
	if err := r.ensureNetworkPolicy(context.TODO(), "annot-update-np", nil, spec); err != nil {
		t.Fatalf("initial ensureNetworkPolicy failed: %v", err)
	}
	np := fetchNetworkPolicy(t, r.Client, "annot-update-np")
	if _, ok := np.Annotations["policy-group.network.openshift.io/host-network"]; ok {
		t.Error("expected no host-network annotation initially")
	}

	// Update with annotations
	if err := r.ensureNetworkPolicy(context.TODO(), "annot-update-np", hostNetworkAnnotation, spec); err != nil {
		t.Fatalf("update ensureNetworkPolicy failed: %v", err)
	}
	np = fetchNetworkPolicy(t, r.Client, "annot-update-np")
	if _, ok := np.Annotations["policy-group.network.openshift.io/host-network"]; !ok {
		t.Error("expected host-network annotation after update")
	}
}

// ---- Tests for deleteNetworkPolicy ----

func TestDeleteNetworkPolicy_Existing(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	labels := map[string]string{"app": "delete-test"}
	spec := defaultDenySpec(labels)

	// Create, then delete
	if err := r.ensureNetworkPolicy(context.TODO(), "delete-me", nil, spec); err != nil {
		t.Fatalf("ensureNetworkPolicy failed: %v", err)
	}

	err := r.deleteNetworkPolicy(context.TODO(), "delete-me")
	if err != nil {
		t.Fatalf("deleteNetworkPolicy failed: %v", err)
	}

	// Verify gone
	np := &networkingv1.NetworkPolicy{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{
		Name:      "delete-me",
		Namespace: OperatorNamespace,
	}, np)
	if err == nil {
		t.Fatal("expected NetworkPolicy to be deleted, but Get succeeded")
	}
}

func TestDeleteNetworkPolicy_NotFound(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)

	// Deleting a non-existent NP should return nil (not an error)
	err := r.deleteNetworkPolicy(context.TODO(), "nonexistent-np")
	if err != nil {
		t.Fatalf("expected nil error for not-found NP, got: %v", err)
	}
}

// ---- Tests for deleteNetworkPolicies ----

func TestDeleteNetworkPolicies_Multiple(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	labels := map[string]string{"app": "multi-delete"}
	spec := defaultDenySpec(labels)

	names := []string{"np-1", "np-2", "np-3"}
	for _, name := range names {
		if err := r.ensureNetworkPolicy(context.TODO(), name, nil, spec); err != nil {
			t.Fatalf("ensureNetworkPolicy(%q) failed: %v", name, err)
		}
	}

	err := r.deleteNetworkPolicies(context.TODO(), names)
	if err != nil {
		t.Fatalf("deleteNetworkPolicies failed: %v", err)
	}

	// Verify all gone
	for _, name := range names {
		np := &networkingv1.NetworkPolicy{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{
			Name:      name,
			Namespace: OperatorNamespace,
		}, np)
		if err == nil {
			t.Errorf("expected NetworkPolicy %q to be deleted, but Get succeeded", name)
		}
	}
}

func TestDeleteNetworkPolicies_EmptyList(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)

	err := r.deleteNetworkPolicies(context.TODO(), []string{})
	if err != nil {
		t.Fatalf("expected nil error for empty list, got: %v", err)
	}
}

func TestDeleteNetworkPolicies_MixedExistence(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	labels := map[string]string{"app": "mixed"}
	spec := defaultDenySpec(labels)

	// Create only some of the NPs
	if err := r.ensureNetworkPolicy(context.TODO(), "exists-1", nil, spec); err != nil {
		t.Fatalf("ensureNetworkPolicy failed: %v", err)
	}

	// Delete a mix of existing and non-existing — should succeed (not-found is ignored)
	err := r.deleteNetworkPolicies(context.TODO(), []string{"exists-1", "does-not-exist"})
	if err != nil {
		t.Fatalf("deleteNetworkPolicies should handle not-found gracefully, got: %v", err)
	}
}

// ---- Tests for per-operand create/delete functions ----

func TestCreateKataMonitorNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createKataMonitorNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createKataMonitorNetworkPolicies failed: %v", err)
	}

	expectedLabels := map[string]string{"name": "openshift-sandboxed-containers-monitor"}

	// 1. deny-all
	np := fetchNetworkPolicy(t, r.Client, "kata-monitor-deny-all")
	verifyDenyAllPolicy(t, np, expectedLabels)
	if np.Annotations != nil && len(np.Annotations) > 0 {
		// deny-all should not have host-network annotation (annotations param was nil)
		if _, ok := np.Annotations["policy-group.network.openshift.io/host-network"]; ok {
			t.Error("kata-monitor-deny-all should not have host-network annotation")
		}
	}

	// 2. allow-metrics-ingress on port 8443
	np = fetchNetworkPolicy(t, r.Client, "kata-monitor-allow-metrics-ingress")
	verifyIngressPolicy(t, np, expectedLabels, 8443)
	if _, ok := np.Annotations["policy-group.network.openshift.io/host-network"]; !ok {
		t.Error("kata-monitor-allow-metrics-ingress should have host-network annotation")
	}

	// 3. allow-dns-egress
	np = fetchNetworkPolicy(t, r.Client, "kata-monitor-allow-dns-egress")
	verifyDNSEgressPolicy(t, np, expectedLabels)
}

func TestDeleteKataMonitorNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createKataMonitorNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createKataMonitorNetworkPolicies failed: %v", err)
	}

	if err := r.deleteKataMonitorNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("deleteKataMonitorNetworkPolicies failed: %v", err)
	}

	for _, name := range []string{
		"kata-monitor-deny-all",
		"kata-monitor-allow-metrics-ingress",
		"kata-monitor-allow-dns-egress",
	} {
		assertNetworkPolicyDeleted(t, r.Client, name)
	}
}

func TestCreatePeerPodsWebhookNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createPeerPodsWebhookNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createPeerPodsWebhookNetworkPolicies failed: %v", err)
	}

	expectedLabels := map[string]string{"app": "peer-pods-webhook"}

	// 1. deny-all (no host-network annotation)
	np := fetchNetworkPolicy(t, r.Client, "peer-pods-webhook-deny-all")
	verifyDenyAllPolicy(t, np, expectedLabels)

	// 2. allow-webhook-ingress on port 9443 (with host-network annotation)
	np = fetchNetworkPolicy(t, r.Client, "peer-pods-webhook-allow-webhook-ingress")
	verifyIngressPolicy(t, np, expectedLabels, 9443)
	if _, ok := np.Annotations["policy-group.network.openshift.io/host-network"]; !ok {
		t.Error("peer-pods-webhook-allow-webhook-ingress should have host-network annotation")
	}

	// 3. allow-metrics-ingress on port 8443 (with host-network annotation)
	np = fetchNetworkPolicy(t, r.Client, "peer-pods-webhook-allow-metrics-ingress")
	verifyIngressPolicy(t, np, expectedLabels, 8443)
	if _, ok := np.Annotations["policy-group.network.openshift.io/host-network"]; !ok {
		t.Error("peer-pods-webhook-allow-metrics-ingress should have host-network annotation")
	}

	// 4. allow-dns-egress (no host-network annotation)
	np = fetchNetworkPolicy(t, r.Client, "peer-pods-webhook-allow-dns-egress")
	verifyDNSEgressPolicy(t, np, expectedLabels)

	// 5. allow-apiserver-egress (allow-all egress)
	np = fetchNetworkPolicy(t, r.Client, "peer-pods-webhook-allow-apiserver-egress")
	verifyAllowAllEgressPolicy(t, np, expectedLabels)
}

func TestDeletePeerPodsWebhookNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createPeerPodsWebhookNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createPeerPodsWebhookNetworkPolicies failed: %v", err)
	}

	if err := r.deletePeerPodsWebhookNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("deletePeerPodsWebhookNetworkPolicies failed: %v", err)
	}

	for _, name := range []string{
		"peer-pods-webhook-deny-all",
		"peer-pods-webhook-allow-webhook-ingress",
		"peer-pods-webhook-allow-metrics-ingress",
		"peer-pods-webhook-allow-dns-egress",
		"peer-pods-webhook-allow-apiserver-egress",
	} {
		assertNetworkPolicyDeleted(t, r.Client, name)
	}
}

func TestCreateKataInstallNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createKataInstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createKataInstallNetworkPolicies failed: %v", err)
	}

	expectedLabels := map[string]string{"name": "osc-rpm-install"}

	np := fetchNetworkPolicy(t, r.Client, "kata-install-deny-all")
	verifyDenyAllPolicy(t, np, expectedLabels)

	np = fetchNetworkPolicy(t, r.Client, "kata-install-allow-egress")
	verifyAllowAllEgressPolicy(t, np, expectedLabels)
}

func TestDeleteKataInstallNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createKataInstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createKataInstallNetworkPolicies failed: %v", err)
	}

	if err := r.deleteKataInstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("deleteKataInstallNetworkPolicies failed: %v", err)
	}

	for _, name := range []string{
		"kata-install-deny-all",
		"kata-install-allow-egress",
	} {
		assertNetworkPolicyDeleted(t, r.Client, name)
	}
}

func TestCreateKataUninstallNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createKataUninstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createKataUninstallNetworkPolicies failed: %v", err)
	}

	expectedLabels := map[string]string{"name": "osc-rpm-uninstall"}

	np := fetchNetworkPolicy(t, r.Client, "kata-uninstall-deny-all")
	verifyDenyAllPolicy(t, np, expectedLabels)

	np = fetchNetworkPolicy(t, r.Client, "kata-uninstall-allow-egress")
	verifyAllowAllEgressPolicy(t, np, expectedLabels)
}

func TestDeleteKataUninstallNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createKataUninstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createKataUninstallNetworkPolicies failed: %v", err)
	}

	if err := r.deleteKataUninstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("deleteKataUninstallNetworkPolicies failed: %v", err)
	}

	for _, name := range []string{
		"kata-uninstall-deny-all",
		"kata-uninstall-allow-egress",
	} {
		assertNetworkPolicyDeleted(t, r.Client, name)
	}
}

func TestCreatePodVMImageCreationNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createPodVMImageCreationNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createPodVMImageCreationNetworkPolicies failed: %v", err)
	}

	expectedLabels := map[string]string{"job-name": "osc-podvm-image-creation"}

	np := fetchNetworkPolicy(t, r.Client, "podvm-image-creation-deny-all")
	verifyDenyAllPolicy(t, np, expectedLabels)

	np = fetchNetworkPolicy(t, r.Client, "podvm-image-creation-allow-egress")
	verifyAllowAllEgressPolicy(t, np, expectedLabels)
}

func TestDeletePodVMImageCreationNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createPodVMImageCreationNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createPodVMImageCreationNetworkPolicies failed: %v", err)
	}

	if err := r.deletePodVMImageCreationNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("deletePodVMImageCreationNetworkPolicies failed: %v", err)
	}

	for _, name := range []string{
		"podvm-image-creation-deny-all",
		"podvm-image-creation-allow-egress",
	} {
		assertNetworkPolicyDeleted(t, r.Client, name)
	}
}

func TestCreatePodVMImageDeletionNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createPodVMImageDeletionNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createPodVMImageDeletionNetworkPolicies failed: %v", err)
	}

	expectedLabels := map[string]string{"job-name": "osc-podvm-image-deletion"}

	np := fetchNetworkPolicy(t, r.Client, "podvm-image-deletion-deny-all")
	verifyDenyAllPolicy(t, np, expectedLabels)

	np = fetchNetworkPolicy(t, r.Client, "podvm-image-deletion-allow-egress")
	verifyAllowAllEgressPolicy(t, np, expectedLabels)
}

func TestDeletePodVMImageDeletionNetworkPolicies(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)
	if err := r.createPodVMImageDeletionNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createPodVMImageDeletionNetworkPolicies failed: %v", err)
	}

	if err := r.deletePodVMImageDeletionNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("deletePodVMImageDeletionNetworkPolicies failed: %v", err)
	}

	for _, name := range []string{
		"podvm-image-deletion-deny-all",
		"podvm-image-deletion-allow-egress",
	} {
		assertNetworkPolicyDeleted(t, r.Client, name)
	}
}

// ---- Tests for idempotency ----

func TestCreateNetworkPoliciesIdempotent(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)

	// Call create twice — second call should succeed (CreateOrUpdate)
	if err := r.createKataMonitorNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("first createKataMonitorNetworkPolicies failed: %v", err)
	}
	if err := r.createKataMonitorNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("second createKataMonitorNetworkPolicies failed: %v", err)
	}

	// Verify policies still exist and are correct
	np := fetchNetworkPolicy(t, r.Client, "kata-monitor-deny-all")
	expectedLabels := map[string]string{"name": "openshift-sandboxed-containers-monitor"}
	verifyDenyAllPolicy(t, np, expectedLabels)
}

func TestDeleteNetworkPoliciesIdempotent(t *testing.T) {
	t.Parallel()

	r := newTestReconciler(t)

	if err := r.createKataInstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("createKataInstallNetworkPolicies failed: %v", err)
	}

	// Delete twice — second call should succeed (not-found is ignored)
	if err := r.deleteKataInstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("first deleteKataInstallNetworkPolicies failed: %v", err)
	}
	if err := r.deleteKataInstallNetworkPolicies(context.TODO()); err != nil {
		t.Fatalf("second deleteKataInstallNetworkPolicies failed: %v", err)
	}
}

// ---- Tests for hostNetworkAnnotation variable ----

func TestHostNetworkAnnotation(t *testing.T) {
	t.Parallel()

	expected := map[string]string{
		"policy-group.network.openshift.io/host-network": "",
	}
	if len(hostNetworkAnnotation) != len(expected) {
		t.Fatalf("expected %d annotation entries, got %d", len(expected), len(hostNetworkAnnotation))
	}
	for k, v := range expected {
		got, ok := hostNetworkAnnotation[k]
		if !ok {
			t.Errorf("expected key %q in hostNetworkAnnotation", k)
		}
		if got != v {
			t.Errorf("expected value %q for key %q, got %q", v, k, got)
		}
	}
}

// ---- Test per-operand policy names and labels via table-driven test ----

func TestPerOperandPolicyConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		createFunc      func(*KataConfigOpenShiftReconciler, context.Context) error
		deleteFunc      func(*KataConfigOpenShiftReconciler, context.Context) error
		expectedNames   []string
		expectedLabels  map[string]string
		ingressPolicies map[string]int32 // policy name -> expected port
		dnsEgress       []string         // policy names that should have DNS egress
		allEgress       []string         // policy names that should allow all egress
		denyAll         []string         // policy names that should be deny-all
	}{
		{
			name:           "kata-monitor",
			createFunc:     (*KataConfigOpenShiftReconciler).createKataMonitorNetworkPolicies,
			deleteFunc:     (*KataConfigOpenShiftReconciler).deleteKataMonitorNetworkPolicies,
			expectedLabels: map[string]string{"name": "openshift-sandboxed-containers-monitor"},
			expectedNames: []string{
				"kata-monitor-deny-all",
				"kata-monitor-allow-metrics-ingress",
				"kata-monitor-allow-dns-egress",
			},
			denyAll:         []string{"kata-monitor-deny-all"},
			ingressPolicies: map[string]int32{"kata-monitor-allow-metrics-ingress": 8443},
			dnsEgress:       []string{"kata-monitor-allow-dns-egress"},
		},
		{
			name:           "peer-pods-webhook",
			createFunc:     (*KataConfigOpenShiftReconciler).createPeerPodsWebhookNetworkPolicies,
			deleteFunc:     (*KataConfigOpenShiftReconciler).deletePeerPodsWebhookNetworkPolicies,
			expectedLabels: map[string]string{"app": "peer-pods-webhook"},
			expectedNames: []string{
				"peer-pods-webhook-deny-all",
				"peer-pods-webhook-allow-webhook-ingress",
				"peer-pods-webhook-allow-metrics-ingress",
				"peer-pods-webhook-allow-dns-egress",
				"peer-pods-webhook-allow-apiserver-egress",
			},
			denyAll: []string{"peer-pods-webhook-deny-all"},
			ingressPolicies: map[string]int32{
				"peer-pods-webhook-allow-webhook-ingress": 9443,
				"peer-pods-webhook-allow-metrics-ingress": 8443,
			},
			dnsEgress: []string{"peer-pods-webhook-allow-dns-egress"},
			allEgress: []string{"peer-pods-webhook-allow-apiserver-egress"},
		},
		{
			name:           "kata-install",
			createFunc:     (*KataConfigOpenShiftReconciler).createKataInstallNetworkPolicies,
			deleteFunc:     (*KataConfigOpenShiftReconciler).deleteKataInstallNetworkPolicies,
			expectedLabels: map[string]string{"name": "osc-rpm-install"},
			expectedNames: []string{
				"kata-install-deny-all",
				"kata-install-allow-egress",
			},
			denyAll:   []string{"kata-install-deny-all"},
			allEgress: []string{"kata-install-allow-egress"},
		},
		{
			name:           "kata-uninstall",
			createFunc:     (*KataConfigOpenShiftReconciler).createKataUninstallNetworkPolicies,
			deleteFunc:     (*KataConfigOpenShiftReconciler).deleteKataUninstallNetworkPolicies,
			expectedLabels: map[string]string{"name": "osc-rpm-uninstall"},
			expectedNames: []string{
				"kata-uninstall-deny-all",
				"kata-uninstall-allow-egress",
			},
			denyAll:   []string{"kata-uninstall-deny-all"},
			allEgress: []string{"kata-uninstall-allow-egress"},
		},
		{
			name:           "podvm-image-creation",
			createFunc:     (*KataConfigOpenShiftReconciler).createPodVMImageCreationNetworkPolicies,
			deleteFunc:     (*KataConfigOpenShiftReconciler).deletePodVMImageCreationNetworkPolicies,
			expectedLabels: map[string]string{"job-name": "osc-podvm-image-creation"},
			expectedNames: []string{
				"podvm-image-creation-deny-all",
				"podvm-image-creation-allow-egress",
			},
			denyAll:   []string{"podvm-image-creation-deny-all"},
			allEgress: []string{"podvm-image-creation-allow-egress"},
		},
		{
			name:           "podvm-image-deletion",
			createFunc:     (*KataConfigOpenShiftReconciler).createPodVMImageDeletionNetworkPolicies,
			deleteFunc:     (*KataConfigOpenShiftReconciler).deletePodVMImageDeletionNetworkPolicies,
			expectedLabels: map[string]string{"job-name": "osc-podvm-image-deletion"},
			expectedNames: []string{
				"podvm-image-deletion-deny-all",
				"podvm-image-deletion-allow-egress",
			},
			denyAll:   []string{"podvm-image-deletion-deny-all"},
			allEgress: []string{"podvm-image-deletion-allow-egress"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/create-and-verify", func(t *testing.T) {
			t.Parallel()
			r := newTestReconciler(t)

			if err := tt.createFunc(r, context.TODO()); err != nil {
				t.Fatalf("create failed: %v", err)
			}

			// Verify all expected NPs exist
			for _, name := range tt.expectedNames {
				fetchNetworkPolicy(t, r.Client, name) // fails test if not found
			}

			// Verify deny-all policies
			for _, name := range tt.denyAll {
				np := fetchNetworkPolicy(t, r.Client, name)
				verifyDenyAllPolicy(t, np, tt.expectedLabels)
			}

			// Verify ingress policies
			for name, port := range tt.ingressPolicies {
				np := fetchNetworkPolicy(t, r.Client, name)
				verifyIngressPolicy(t, np, tt.expectedLabels, port)
			}

			// Verify DNS egress policies
			for _, name := range tt.dnsEgress {
				np := fetchNetworkPolicy(t, r.Client, name)
				verifyDNSEgressPolicy(t, np, tt.expectedLabels)
			}

			// Verify allow-all egress policies
			for _, name := range tt.allEgress {
				np := fetchNetworkPolicy(t, r.Client, name)
				verifyAllowAllEgressPolicy(t, np, tt.expectedLabels)
			}
		})

		t.Run(tt.name+"/delete", func(t *testing.T) {
			t.Parallel()
			r := newTestReconciler(t)

			if err := tt.createFunc(r, context.TODO()); err != nil {
				t.Fatalf("create failed: %v", err)
			}
			if err := tt.deleteFunc(r, context.TODO()); err != nil {
				t.Fatalf("delete failed: %v", err)
			}

			for _, name := range tt.expectedNames {
				assertNetworkPolicyDeleted(t, r.Client, name)
			}
		})
	}
}

// ---- Verification helpers ----

func verifyDenyAllPolicy(t *testing.T, np *networkingv1.NetworkPolicy, expectedLabels map[string]string) {
	t.Helper()
	verifyPodSelector(t, np, expectedLabels)

	if len(np.Spec.PolicyTypes) != 2 {
		t.Errorf("%s: expected 2 PolicyTypes for deny-all, got %d", np.Name, len(np.Spec.PolicyTypes))
	}
	hasIngress, hasEgress := false, false
	for _, pt := range np.Spec.PolicyTypes {
		if pt == networkingv1.PolicyTypeIngress {
			hasIngress = true
		}
		if pt == networkingv1.PolicyTypeEgress {
			hasEgress = true
		}
	}
	if !hasIngress || !hasEgress {
		t.Errorf("%s: deny-all should have both Ingress and Egress PolicyTypes", np.Name)
	}
	if len(np.Spec.Ingress) != 0 {
		t.Errorf("%s: deny-all should have 0 Ingress rules, got %d", np.Name, len(np.Spec.Ingress))
	}
	if len(np.Spec.Egress) != 0 {
		t.Errorf("%s: deny-all should have 0 Egress rules, got %d", np.Name, len(np.Spec.Egress))
	}
}

func verifyIngressPolicy(t *testing.T, np *networkingv1.NetworkPolicy, expectedLabels map[string]string, expectedPort int32) {
	t.Helper()
	verifyPodSelector(t, np, expectedLabels)

	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("%s: expected PolicyTypes=[Ingress], got %v", np.Name, np.Spec.PolicyTypes)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("%s: expected 1 Ingress rule, got %d", np.Name, len(np.Spec.Ingress))
	}
	if len(np.Spec.Ingress[0].Ports) != 1 {
		t.Fatalf("%s: expected 1 port in Ingress rule, got %d", np.Name, len(np.Spec.Ingress[0].Ports))
	}
	p := np.Spec.Ingress[0].Ports[0]
	if p.Protocol == nil || *p.Protocol != corev1.ProtocolTCP {
		t.Errorf("%s: expected TCP protocol, got %v", np.Name, p.Protocol)
	}
	if p.Port == nil || p.Port.IntValue() != int(expectedPort) {
		t.Errorf("%s: expected port %d, got %v", np.Name, expectedPort, p.Port)
	}
}

func verifyDNSEgressPolicy(t *testing.T, np *networkingv1.NetworkPolicy, expectedLabels map[string]string) {
	t.Helper()
	verifyPodSelector(t, np, expectedLabels)

	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("%s: expected PolicyTypes=[Egress], got %v", np.Name, np.Spec.PolicyTypes)
	}
	if len(np.Spec.Egress) != 1 {
		t.Fatalf("%s: expected 1 Egress rule, got %d", np.Name, len(np.Spec.Egress))
	}
	rule := np.Spec.Egress[0]
	if len(rule.To) != 1 {
		t.Fatalf("%s: expected 1 peer, got %d", np.Name, len(rule.To))
	}
	ns := rule.To[0].NamespaceSelector
	if ns == nil || ns.MatchLabels["kubernetes.io/metadata.name"] != "openshift-dns" {
		t.Errorf("%s: expected namespace selector for openshift-dns", np.Name)
	}
	if len(rule.Ports) != 2 {
		t.Fatalf("%s: expected 2 DNS ports, got %d", np.Name, len(rule.Ports))
	}
	for _, p := range rule.Ports {
		if p.Port == nil || p.Port.IntValue() != 5353 {
			t.Errorf("%s: expected port 5353, got %v", np.Name, p.Port)
		}
	}
}

func verifyAllowAllEgressPolicy(t *testing.T, np *networkingv1.NetworkPolicy, expectedLabels map[string]string) {
	t.Helper()
	verifyPodSelector(t, np, expectedLabels)

	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("%s: expected PolicyTypes=[Egress], got %v", np.Name, np.Spec.PolicyTypes)
	}
	if len(np.Spec.Egress) != 1 {
		t.Fatalf("%s: expected 1 Egress rule, got %d", np.Name, len(np.Spec.Egress))
	}
	if len(np.Spec.Egress[0].To) != 0 {
		t.Errorf("%s: allow-all egress should have empty To, got %d peers", np.Name, len(np.Spec.Egress[0].To))
	}
	if len(np.Spec.Egress[0].Ports) != 0 {
		t.Errorf("%s: allow-all egress should have empty Ports, got %d ports", np.Name, len(np.Spec.Egress[0].Ports))
	}
}

func verifyPodSelector(t *testing.T, np *networkingv1.NetworkPolicy, expectedLabels map[string]string) {
	t.Helper()
	for k, v := range expectedLabels {
		if np.Spec.PodSelector.MatchLabels[k] != v {
			t.Errorf("%s: expected PodSelector %s=%s, got %v", np.Name, k, v, np.Spec.PodSelector.MatchLabels)
		}
	}
}

func assertNetworkPolicyDeleted(t *testing.T, cl client.Client, name string) {
	t.Helper()
	np := &networkingv1.NetworkPolicy{}
	err := cl.Get(context.TODO(), types.NamespacedName{
		Name:      name,
		Namespace: OperatorNamespace,
	}, np)
	if err == nil {
		t.Errorf("expected NetworkPolicy %q to be deleted, but it still exists", name)
	}
}
