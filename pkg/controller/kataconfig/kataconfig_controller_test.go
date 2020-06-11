package kataconfig

import (
	"context"
	"testing"

	kataconfigurationv1alpha1 "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	"github.com/openshift/kata-operator/pkg/generated/clientset/versioned/scheme"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestKataConfigCreation(t *testing.T) {
	// A KataConfig object with metadata and spec.
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example-kataconfig",
			Namespace: "kata-operator",
			Labels: map[string]string{
				"label-key": "label-value",
			},
		},
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{kataconfig}

	s := scheme.Scheme

	s.AddKnownTypes(kataconfigurationv1alpha1.SchemeGroupVersion, kataconfig)

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	// List KataConfig objects filtering by labels
	opt := client.MatchingLabels(map[string]string{"label-key": "label-value"})
	kataConfigList := &kataconfigurationv1alpha1.KataConfigList{}
	err := cl.List(context.TODO(), kataConfigList, opt)
	if err != nil {
		t.Fatalf("list kataconfig: (%v)", err)
	}
}

func TestKataConfigDaemonset(t *testing.T) {
	var (
		name      = "example-kataconfig"
		namespace = "kata-operator"
	)
	// A KataConfig object with metadata and spec.
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example-kataconfig",
			Namespace: "kata-operator",
			Labels: map[string]string{
				"label-key": "label-value",
			},
		},
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{kataconfig}

	s := scheme.Scheme

	s.AddKnownTypes(kataconfigurationv1alpha1.SchemeGroupVersion, kataconfig)
	s.AddKnownTypes(appsv1.SchemeGroupVersion, &appsv1.DaemonSet{})
	s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.NodeList{})

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	isOpenShift := false
	k := &ReconcileKataConfig{client: cl, scheme: s, isOpenShift: &isOpenShift}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		},
	}

	res, err := k.Reconcile(req)

	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}
	// Check the result of reconciliation to make sure it has the desired state.
	if res.Requeue {
		t.Errorf("unexpected reconcile requeue request after creating DaemonSet  %+v", res)
	}

	ds := &appsv1.DaemonSet{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata-install-daemon", Namespace: "kata-operator"}, ds)
	if err != nil {
		t.Fatalf("get daemonset: (%v)", err)
	}

	dName := ds.GetName()
	if dName != "kata-install-daemon" {
		t.Errorf("ds name is not the expected one (%s)", dName)
	}

}

func TestKataConfigStatusUpdate(t *testing.T) {
	var (
		name = "example-kataconfig"
	)
	// A KataConfig object with metadata and spec.
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "example-kataconfig",
			Labels: map[string]string{
				"label-key": "label-value",
			},
		},
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{kataconfig}

	s := scheme.Scheme

	s.AddKnownTypes(kataconfigurationv1alpha1.SchemeGroupVersion, kataconfig)
	s.AddKnownTypes(appsv1.SchemeGroupVersion, &appsv1.DaemonSet{})
	s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.NodeList{})
	s.AddKnownTypes(nodeapi.SchemeGroupVersion, &nodeapi.RuntimeClass{})
	s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfig{})
	s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfigPool{})

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	isOpenShift := true
	k := &ReconcileKataConfig{client: cl, scheme: s, isOpenShift: &isOpenShift}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
		},
	}

	res, err := k.Reconcile(req)

	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if res.Requeue {
		t.Errorf("unexpected reconcile requeue request after creating DaemonSet %+v", res)
	}

	kataconfig.Status.TotalNodesCount = 3

	kataconfig.Status.CompletedDaemons = 3
	err = k.client.Status().Update(context.TODO(), kataconfig)
	if err != nil {
		t.Fatalf("update kataconfig: (%v)", err)
	}

	res, err = k.Reconcile(req)

	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if res.Requeue {
		t.Errorf("unexpected reconcile requeue request after creating mcp and mc %+v", res)
	}

	mcp := &mcfgv1.MachineConfigPool{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata-oc"}, mcp)
	if err != nil {
		t.Fatalf("get mcp: (%v)", err)
	}

	mc := &mcfgv1.MachineConfig{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "50-kata-crio-dropin"}, mc)
	if err != nil {
		t.Fatalf("get mc: (%v)", err)
	}

	kataconfig.Status.CompletedNodesCount = 3
	kataconfig.Status.RuntimeClass = "kata-oc"

	err = k.client.Status().Update(context.TODO(), kataconfig)
	if err != nil {
		t.Fatalf("update kataconfig: (%v)", err)
	}

	res, err = k.Reconcile(req)

	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}
	if res.Requeue {
		t.Errorf("unexpected reconcile requeue request after updating node count %+v", res)
	}

	rc := &nodeapi.RuntimeClass{}

	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata-oc"}, rc)
	if err != nil {
		t.Fatalf("get runtimeclass: (%v)", err)
	}

}
