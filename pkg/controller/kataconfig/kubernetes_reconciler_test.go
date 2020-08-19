package kataconfig

import (
	"context"
	"testing"

	kataconfigurationv1alpha1 "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	"github.com/openshift/kata-operator/pkg/generated/clientset/versioned/scheme"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestK8sKataInstallDaemonset(t *testing.T) {
	var (
		name = "example-kataconfig"
		// namespace = "kata-operator"
	)
	// A KataConfig object with metadata and spec.
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	kataconfig.Status.TotalNodesCount = 3
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

	k := &ReconcileKataConfigKubernetes{client: cl, scheme: s}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
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
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata-operator-daemon-install", Namespace: "kata-operator"}, ds)
	if err != nil {
		t.Fatalf("get daemonset: (%v)", err)
	}

	dName := ds.GetName()
	if dName != "kata-operator-daemon-install" {
		t.Errorf("ds name is not the expected one (%s)", dName)
	}

}

func TestKataConfigInstallFlow(t *testing.T) {
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
			Name: name,
		},
		Spec: kataconfigurationv1alpha1.KataConfigSpec{
			KataConfigPoolSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"aa": "bb"},
			},
		},
	}

	kataconfig.Status.TotalNodesCount = 3
	// Objects to track in the fake client.
	objs := []runtime.Object{kataconfig}

	s := scheme.Scheme

	s.AddKnownTypes(kataconfigurationv1alpha1.SchemeGroupVersion, kataconfig)
	s.AddKnownTypes(appsv1.SchemeGroupVersion, &appsv1.DaemonSet{})
	s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.NodeList{})
	s.AddKnownTypes(nodeapi.SchemeGroupVersion, &nodeapi.RuntimeClass{})
	// s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfig{})
	// s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfigPool{})

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	k := &ReconcileKataConfigKubernetes{client: cl, scheme: s}

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

	kataconfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList = []string{"host1", "host2", "host3"}
	kataconfig.Status.InstallationStatus.InProgress.InProgressNodesCount = 3

	err = k.client.Status().Update(context.TODO(), kataconfig)
	if err != nil {
		t.Fatalf("update kataconfig: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if res.Requeue {
		t.Errorf("expected reconcile requeue request after binaries installation")
	}

	kataconfig.Status.InstallationStatus.InProgress.InProgressNodesCount = 0
	kataconfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList = []string{}
	kataconfig.Status.InstallationStatus.Completed.CompletedNodesCount = 3
	err = k.client.Status().Update(context.TODO(), kataconfig)
	if err != nil {
		t.Fatalf("update kataconfig: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	runtimeClassNames := []string{"kata-qemu-virtiofs", "kata-qemu", "kata-clh", "kata-fc", "kata"}
	for _, runtimeClassName := range runtimeClassNames {
		rc := &nodeapi.RuntimeClass{}
		err = k.client.Get(context.TODO(), types.NamespacedName{Name: runtimeClassName}, rc)
		if err != nil {
			t.Fatalf("get runtimeclass: (%v)", err)
		}
	}
}
