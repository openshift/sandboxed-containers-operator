package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	nodeapiinstall "k8s.io/api/node/v1"
)

func newRCTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = kataconfigurationv1.AddToScheme(s)
	_ = mcfgv1.AddToScheme(s)
	_ = nodeapiinstall.AddToScheme(s)
	return s
}

func newRCReconciler(t *testing.T, spec kataconfigurationv1.KataConfigSpec, objs ...client.Object) *KataConfigOpenShiftReconciler {
	t.Helper()
	s := newRCTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return &KataConfigOpenShiftReconciler{
		Client: c,
		Log:    logr.Discard(),
		Scheme: s,
		kataConfig: &kataconfigurationv1.KataConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-kataconfig"},
			Spec:       spec,
		},
	}
}

func getRCNodeSelector(t *testing.T, r *KataConfigOpenShiftReconciler, name string) map[string]string {
	t.Helper()
	rc := &nodeapi.RuntimeClass{}
	if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: name}, rc); err != nil {
		t.Fatalf("RuntimeClass %q not found: %v", name, err)
	}
	if rc.Scheduling == nil {
		return nil
	}
	return rc.Scheduling.NodeSelector
}

// ── kata nodeSelector combinations ──────────────────────────────────────────

func TestKataRC_BothFlagsOff_OnlyKataOC(t *testing.T) {
	t.Parallel()
	r := newRCReconciler(t, kataconfigurationv1.KataConfigSpec{
		CheckNodeEligibility: false,
		EnableMixedCluster:   false,
	})

	if err := r.createRuntimeClass(kataRuntimeClassName, kataRuntimeClassCpuOverhead,
		kataRuntimeClassMemOverhead, "", kataRuntimeClassName,
		map[string]string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sel := getRCNodeSelector(t, r, kataRuntimeClassName)
	if _, ok := sel["feature.node.kubernetes.io/runtime.kata"]; ok {
		t.Error("runtime.kata label must not be present when CheckNodeEligibility=false")
	}
	if _, ok := sel[nodeTypeLabelKey]; ok {
		t.Error("node-type label must not be present when EnableMixedCluster=false")
	}
}

func TestKataRC_CheckNodeEligibilityOnly(t *testing.T) {
	t.Parallel()
	// CheckNodeEligibility gates RC creation on nodes having runtime.kata label.
	// Add a matching node so the eligibility check passes.
	eligibleNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bm-node-1",
			Labels: map[string]string{
				"node-role.kubernetes.io/kata-oc":         "",
				"feature.node.kubernetes.io/runtime.kata": "true",
			},
		},
	}
	r := newRCReconciler(t, kataconfigurationv1.KataConfigSpec{
		CheckNodeEligibility: true,
		EnableMixedCluster:   false,
	}, eligibleNode)

	labels := map[string]string{"feature.node.kubernetes.io/runtime.kata": "true"}
	if err := r.createRuntimeClass(kataRuntimeClassName, kataRuntimeClassCpuOverhead,
		kataRuntimeClassMemOverhead, "", kataRuntimeClassName, labels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sel := getRCNodeSelector(t, r, kataRuntimeClassName)
	if sel["feature.node.kubernetes.io/runtime.kata"] != "true" {
		t.Error("runtime.kata label must be present when CheckNodeEligibility=true")
	}
	if _, ok := sel[nodeTypeLabelKey]; ok {
		t.Error("node-type label must not be present when EnableMixedCluster=false")
	}
}

func TestKataRC_EnableMixedClusterOnly_KataCapableLabel(t *testing.T) {
	t.Parallel()
	r := newRCReconciler(t, kataconfigurationv1.KataConfigSpec{
		CheckNodeEligibility: false,
		EnableMixedCluster:   true,
	})

	labels := map[string]string{kataCapableLabelKey: "true"}
	if err := r.createRuntimeClass(kataRuntimeClassName, kataRuntimeClassCpuOverhead,
		kataRuntimeClassMemOverhead, "", kataRuntimeClassName, labels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sel := getRCNodeSelector(t, r, kataRuntimeClassName)
	if sel[kataCapableLabelKey] != "true" {
		t.Errorf("kata-capable: got %q, want true", sel[kataCapableLabelKey])
	}
	if _, ok := sel["feature.node.kubernetes.io/runtime.kata"]; ok {
		t.Error("runtime.kata label must not be present when CheckNodeEligibility=false")
	}
}

func TestKataRC_BothFlagsOn_BothLabels(t *testing.T) {
	t.Parallel()
	eligibleNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bm-node-1",
			Labels: map[string]string{
				"node-role.kubernetes.io/kata-oc":         "",
				"feature.node.kubernetes.io/runtime.kata": "true",
				kataCapableLabelKey:                       "true",
			},
		},
	}
	r := newRCReconciler(t, kataconfigurationv1.KataConfigSpec{
		CheckNodeEligibility: true,
		EnableMixedCluster:   true,
	}, eligibleNode)

	labels := map[string]string{
		"feature.node.kubernetes.io/runtime.kata": "true",
		kataCapableLabelKey:                       "true",
	}
	if err := r.createRuntimeClass(kataRuntimeClassName, kataRuntimeClassCpuOverhead,
		kataRuntimeClassMemOverhead, "", kataRuntimeClassName, labels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sel := getRCNodeSelector(t, r, kataRuntimeClassName)
	if sel["feature.node.kubernetes.io/runtime.kata"] != "true" {
		t.Error("runtime.kata label must be present when CheckNodeEligibility=true")
	}
	if sel[kataCapableLabelKey] != "true" {
		t.Errorf("kata-capable: got %q, want true", sel[kataCapableLabelKey])
	}
}

// ── kata-remote nodeSelector ─────────────────────────────────────────────────

func TestKataRemoteRC_EnableMixedCluster_VirtualLabel(t *testing.T) {
	t.Parallel()
	r := newRCReconciler(t, kataconfigurationv1.KataConfigSpec{
		EnableMixedCluster: true,
		EnablePeerPods:     true,
	})

	labels := map[string]string{nodeTypeLabelKey: nodeTypeLabelVirtual}
	if err := r.createRuntimeClass(peerpodsRuntimeClassName, peerpodsRuntimeClassCpuOverhead,
		peerpodsRuntimeClassMemOverhead, "", peerpodsRuntimeClassName, labels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sel := getRCNodeSelector(t, r, peerpodsRuntimeClassName)
	if sel[nodeTypeLabelKey] != nodeTypeLabelVirtual {
		t.Errorf("node-type: got %q, want %q", sel[nodeTypeLabelKey], nodeTypeLabelVirtual)
	}
}

func TestKataRemoteRC_MixedClusterOff_NoNodeTypeLabel(t *testing.T) {
	t.Parallel()
	r := newRCReconciler(t, kataconfigurationv1.KataConfigSpec{
		EnableMixedCluster: false,
		EnablePeerPods:     true,
	})

	if err := r.createRuntimeClass(peerpodsRuntimeClassName, peerpodsRuntimeClassCpuOverhead,
		peerpodsRuntimeClassMemOverhead, "", peerpodsRuntimeClassName, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sel := getRCNodeSelector(t, r, peerpodsRuntimeClassName)
	if _, ok := sel[nodeTypeLabelKey]; ok {
		t.Error("node-type label must not be present when EnableMixedCluster=false")
	}
}

// ── IT-3: stale nodeSelector update ─────────────────────────────────────────

func TestCreateRuntimeClass_PatchesStaleNodeSelector(t *testing.T) {
	t.Parallel()
	s := newRCTestScheme(t)

	// Pre-existing kata RC without node-type label (pre-mixed-cluster state)
	existing := &nodeapi.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: kataRuntimeClassName},
		Handler:    kataRuntimeClassName,
		Scheduling: &nodeapi.Scheduling{
			NodeSelector: map[string]string{
				"node-role.kubernetes.io/kata-oc": "",
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	r := &KataConfigOpenShiftReconciler{
		Client: c,
		Log:    logr.Discard(),
		Scheme: s,
		kataConfig: &kataconfigurationv1.KataConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-kataconfig"},
			Spec: kataconfigurationv1.KataConfigSpec{
				EnableMixedCluster: true,
			},
		},
	}

	labels := map[string]string{kataCapableLabelKey: "true"}
	if err := r.createRuntimeClass(kataRuntimeClassName, kataRuntimeClassCpuOverhead,
		kataRuntimeClassMemOverhead, "", kataRuntimeClassName, labels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sel := getRCNodeSelector(t, r, kataRuntimeClassName)
	if sel[kataCapableLabelKey] != "true" {
		t.Errorf("stale nodeSelector not patched: kata-capable got %q, want true",
			sel[kataCapableLabelKey])
	}
}
