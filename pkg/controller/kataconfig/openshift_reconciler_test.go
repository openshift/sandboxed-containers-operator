package kataconfig

import (
	"context"
	"testing"
	"time"

	kataconfigurationv1alpha1 "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	"github.com/openshift/kata-operator/pkg/generated/clientset/versioned/scheme"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sFake "k8s.io/client-go/kubernetes/fake"
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
			Name: "example-kataconfig",
		},
	}

	kataconfig.Status.TotalNodesCount = 3
	// Objects to track in the fake client.
	objs := []runtime.Object{kataconfig}

	s := scheme.Scheme

	s.AddKnownTypes(kataconfigurationv1alpha1.SchemeGroupVersion, kataconfig)

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	kataConfigList := &kataconfigurationv1alpha1.KataConfigList{}
	listOpts := []client.ListOption{}
	err := cl.List(context.TODO(), kataConfigList, listOpts...)

	if len(kataConfigList.Items) < 1 {
		t.Fatalf("Unable to find kataconfig")
	}
	if err != nil {
		t.Fatalf("list kataconfig: (%v)", err)
	}

	k := &kataconfigurationv1alpha1.KataConfig{}
	err = cl.Get(context.TODO(), types.NamespacedName{Name: "example-kataconfig"}, k)
	if err != nil {
		t.Fatalf("Error getting kataconfig: (%v)", err)
	}
	if k.Name != kataconfig.Name {
		t.Fatalf("Unexpected kataconfig found (%v)", kataconfig)
	}
}

func TestMultipleKataConfigCreation(t *testing.T) {
	// The oldest CR creation time set at 0001-01-01 00:00:00 +0000 UTC by default
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "example-kataconfig",
		},
	}
	kataconfig.Status.TotalNodesCount = 3

	// This CR's creation time is set at yesterday
	kataconfig2 := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "example-kataconfig2",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-24 * time.Hour)),
		},
	}
	kataconfig2.Status.TotalNodesCount = 3

	// This CR's creation time set at rigt now today
	kataconfig3 := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "example-kataconfig3",
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
	}
	kataconfig3.Status.TotalNodesCount = 3

	// Objects to track in the fake client.
	objs := []runtime.Object{kataconfig, kataconfig2, kataconfig3}

	s := scheme.Scheme

	s.AddKnownTypes(kataconfigurationv1alpha1.SchemeGroupVersion, kataconfig)
	s.AddKnownTypes(appsv1.SchemeGroupVersion, &appsv1.DaemonSet{})
	s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.NodeList{})

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	kataConfigList := &kataconfigurationv1alpha1.KataConfigList{}
	listOpts := []client.ListOption{}
	err := cl.List(context.TODO(), kataConfigList, listOpts...)

	if len(kataConfigList.Items) != 3 {
		t.Fatalf("Unable to find all kataconfig CRs")
	}
	if err != nil {
		t.Fatalf("list kataconfig: (%v)", err)
	}

	k := &kataconfigurationv1alpha1.KataConfig{}
	err = cl.Get(context.TODO(), types.NamespacedName{Name: "example-kataconfig"}, k)
	if err != nil {
		t.Fatalf("Error getting kataconfig: (%v)", err)
	}
	if k.Name != kataconfig.Name {
		t.Fatalf("Unexpected kataconfig found (%v)", kataconfig)
	}

	k2 := &kataconfigurationv1alpha1.KataConfig{}
	err = cl.Get(context.TODO(), types.NamespacedName{Name: "example-kataconfig2"}, k2)
	if err != nil {
		t.Fatalf("Error getting kataconfig: (%v)", err)
	}

	if k2.Name != kataconfig2.Name {
		t.Fatalf("Unexpected kataconfig found (%v)", kataconfig2)
	}

	k3 := &kataconfigurationv1alpha1.KataConfig{}
	err = cl.Get(context.TODO(), types.NamespacedName{Name: "example-kataconfig3"}, k3)
	if err != nil {
		t.Fatalf("Error getting kataconfig: (%v)", err)
	}

	if k3.Name != kataconfig3.Name {
		t.Fatalf("Unexpected kataconfig found (%v)", kataconfig3)
	}

	k1t := k.GetCreationTimestamp()
	k2t := k2.GetCreationTimestamp()
	k3t := k3.GetCreationTimestamp()

	if !k1t.Before(&k2t) {
		t.Fatalf("Incorrect creation time stamps found %+v and %+v", k1t, k2t)
	}

	if !k2t.Before(&k3t) {
		t.Fatalf("Incorrect creation time stamps found %+v and %+v", k2t, k3t)
	}

	r := &ReconcileKataConfigOpenShift{client: cl, scheme: s}

	// Let's reconcile first CR
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: k.Name,
		},
	}

	_, err = r.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	err = cl.Get(context.TODO(), types.NamespacedName{Name: "example-kataconfig"}, k)
	if err != nil {
		t.Fatalf("Error getting kataconfig: (%v)", err)
	}

	// This is the oldest CR, so this should be processed by the reconciliation loop
	if k.Status.InstallationStatus.Failed.FailedNodesCount == -1 {
		t.Fatal("Oldest KataConfig CR is marked duplicate")
	}

	// Let's reconcile the CR after the first one
	req = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: k2.Name,
		},
	}

	_, err = r.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	err = cl.Get(context.TODO(), types.NamespacedName{Name: "example-kataconfig2"}, k2)
	if err != nil {
		t.Fatalf("Error getting kataconfig2: (%v)", err)
	}

	// This CR should be marked duplicate before it's created after the first one
	if k2.Status.InstallationStatus.Failed.FailedNodesCount != -1 {
		t.Fatal("Newer KataConfig CR is not marked duplicate")
	}

	// Now let's reconcile the 3rd CR
	req = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: k3.Name,
		},
	}

	_, err = r.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	err = cl.Get(context.TODO(), types.NamespacedName{Name: "example-kataconfig3"}, k3)
	if err != nil {
		t.Fatalf("Error getting kataconfig3: (%v)", err)
	}

	// Even this CR should be marked duplicate because it's created after the first one
	if k3.Status.InstallationStatus.Failed.FailedNodesCount != -1 {
		t.Fatal("Newer KataConfig CR is not marked duplicate")
	}
}

func TestOpenShiftKataInstallDaemonset(t *testing.T) {
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

	k := &ReconcileKataConfigOpenShift{client: cl, scheme: s}

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

func TestOpenShiftKataInstallFlow(t *testing.T) {
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
	s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfig{})
	s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfigPool{})

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	k := &ReconcileKataConfigOpenShift{client: cl, scheme: s}

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

	if !res.Requeue {
		t.Errorf("expected reconcile requeue request after creating mcp")
	}

	mcp := &mcfgv1.MachineConfigPool{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata-oc"}, mcp)
	if err != nil {
		t.Fatalf("get mcp: (%v)", err)
	}

	mcp.Status.MachineCount = 3
	err = k.client.Status().Update(context.TODO(), mcp)
	if err != nil {
		t.Fatalf("update mcp machine count: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if !res.Requeue {
		t.Errorf("expected reconcile requeue request after updating mcp machine count")
	}

	mcp.Status.ReadyMachineCount = 1
	err = k.client.Status().Update(context.TODO(), mcp)
	if err != nil {
		t.Fatalf("update mcp ready machine count: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if !res.Requeue {
		t.Errorf("expected reconcile requeue request after updating ready machine count")
	}

	mcp.Status.ReadyMachineCount = 3
	err = k.client.Status().Update(context.TODO(), mcp)
	if err != nil {
		t.Fatalf("update mcp ready machine count to machine count: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	mc, err := k.newMCForCR()
	if err != nil {
		t.Fatalf("Error initializing machine config (%v)", err)
	}

	err = k.client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, mc)
	if err != nil {
		t.Fatalf("get mc: (%v)", err)
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

	rc := &nodeapi.RuntimeClass{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata"}, rc)
	if err != nil {
		t.Fatalf("get runtimeclass: (%v)", err)
	}

	ds := &appsv1.DaemonSet{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, ds)
	if !errors.IsNotFound(err) {
		t.Fatalf("Kata install daemonset is not removed")
	}
}

func TestOpenShiftKataUnInstallFlow(t *testing.T) {
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
				MatchLabels: map[string]string{"custom-label": "test"},
			},
		},
	}

	kataconfig.Status.TotalNodesCount = 3
	// Objects to track in the fake client.
	objs := []runtime.Object{kataconfig}

	s := scheme.Scheme

	s.AddKnownTypes(kataconfigurationv1alpha1.SchemeGroupVersion, kataconfig)
	s.AddKnownTypes(appsv1.SchemeGroupVersion, &appsv1.DaemonSet{})
	s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.PodList{})
	s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.NodeList{})
	s.AddKnownTypes(nodeapi.SchemeGroupVersion, &nodeapi.RuntimeClass{})
	s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfig{})
	s.AddKnownTypes(mcfgv1.SchemeGroupVersion, &mcfgv1.MachineConfigPool{})

	if err := kataconfigurationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add route scheme: (%v)", err)
	}

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClientWithScheme(s, objs...)

	k := &ReconcileKataConfigOpenShift{client: cl, scheme: s, clientset: k8sFake.NewSimpleClientset()}

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

	if !res.Requeue {
		t.Errorf("expected reconcile requeue request after creating mcp")
	}

	mcp := &mcfgv1.MachineConfigPool{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata-oc"}, mcp)
	if err != nil {
		t.Fatalf("get mcp: (%v)", err)
	}

	mcp.Status.MachineCount = 3
	err = k.client.Status().Update(context.TODO(), mcp)
	if err != nil {
		t.Fatalf("update mcp machine count: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if !res.Requeue {
		t.Errorf("expected reconcile requeue request after updating mcp machine count")
	}

	mcp.Status.ReadyMachineCount = 1
	err = k.client.Status().Update(context.TODO(), mcp)
	if err != nil {
		t.Fatalf("update mcp ready machine count: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if !res.Requeue {
		t.Errorf("expected reconcile requeue request after updating ready machine count")
	}

	mcp.Status.ReadyMachineCount = 3
	err = k.client.Status().Update(context.TODO(), mcp)
	if err != nil {
		t.Fatalf("update mcp ready machine count to machine count: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	mc, err := k.newMCForCR()
	if err != nil {
		t.Fatalf("Error initializing machine config (%v)", err)
	}

	err = k.client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, mc)
	if err != nil {
		t.Fatalf("get mc: (%v)", err)
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

	rc := &nodeapi.RuntimeClass{}
	err = k.client.Get(context.TODO(), types.NamespacedName{Name: "kata"}, rc)
	if err != nil {
		t.Fatalf("get runtimeclass: (%v)", err)
	}

	now := metav1.Now()
	kataconfig.SetDeletionTimestamp(&now)
	kataconfig.SetFinalizers([]string{kataConfigFinalizer})
	k.clientset = k8sFake.NewSimpleClientset()

	kataconfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList = []string{"host1", "host2", "host3"}
	for _, nodeName := range kataconfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList {
		node := corev1.Node{}
		node.Name = nodeName
		_, err := k.clientset.CoreV1().Nodes().Create(&node)
		if err != nil {
			t.Errorf("Error creating fake node objects: %+v", err)
		}
	}

	parentMcp := mcp.DeepCopy()
	parentMcp.Name = "worker"

	err = k.client.Create(context.TODO(), parentMcp)
	if err != nil {
		t.Fatalf("Unable to create parent mcp (%v)", err)
	}

	ds := k.processDaemonsetForCR(UninstallOperation)
	err = k.client.Create(context.TODO(), ds)
	if err != nil {
		t.Fatalf("Unable to create ds (%v)", err)
	}

	err = k.client.Status().Update(context.TODO(), kataconfig)
	if err != nil {
		t.Fatalf("update kataconfig: (%v)", err)
	}

	res, err = k.Reconcile(req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	err = k.client.Get(context.TODO(), types.NamespacedName{Name: kataconfig.Name}, kataconfig)
	if kataconfig.Status.UnInstallationStatus.Completed.CompletedNodesCount != 3 {
		t.Errorf("Unexpected number of completed nodes found %d", kataconfig.Status.UnInstallationStatus.Completed.CompletedNodesCount)
	}

}
