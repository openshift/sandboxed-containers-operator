package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	operatorv1 "github.com/openshift/api/operator/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newConsolePluginTestReconciler(t *testing.T, objs ...client.Object) (*ConsolePluginReconciler, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := operatorv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &ConsolePluginReconciler{
		Client:     cl,
		Scheme:     scheme,
		PluginName: "osc-openshift-console-plugin",
		namespace:  "test-ns",
		log:        logr.Discard(),
	}, cl
}

func consoleOperatorConfig(t *testing.T, cl client.Client) *operatorv1.Console {
	t.Helper()

	console := &operatorv1.Console{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "cluster"}, console); err != nil {
		t.Fatalf("reading Console operator CR: %v", err)
	}
	return console
}

func hasPlugin(console *operatorv1.Console, name string) bool {
	for _, p := range console.Spec.Plugins {
		if p == name {
			return true
		}
	}
	return false
}

func TestConsolePluginDisabledByDefault(t *testing.T) {
	// The plugin is deployed but not enabled: only an administrator can enable
	// it, so the operator must never add it to spec.plugins on its own.
	console := &operatorv1.Console{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       operatorv1.ConsoleSpec{Plugins: []string{"monitoring-plugin"}},
	}
	r, cl := newConsolePluginTestReconciler(t, console)

	for i := 0; i < 3; i++ {
		enabled, err := r.syncConsolePluginEnabledState(context.Background())
		if err != nil {
			t.Fatalf("syncConsolePluginEnabledState: %v", err)
		}
		if enabled {
			t.Fatal("reported the plugin as enabled without an administrator action")
		}
	}

	got := consoleOperatorConfig(t, cl)
	if hasPlugin(got, r.PluginName) {
		t.Fatalf("plugin %q enabled without an administrator action, spec.plugins=%v", r.PluginName, got.Spec.Plugins)
	}
	if _, ok := got.Annotations[r.enabledMarkerKey()]; ok {
		t.Fatalf("marker annotation %q set while the plugin is disabled, annotations=%v", r.enabledMarkerKey(), got.Annotations)
	}
}

func TestSyncMarksAdministratorEnablement(t *testing.T) {
	// An administrator enabled the plugin (at install time or from the console
	// UI): the operator records the marker and leaves spec.plugins alone.
	console := &operatorv1.Console{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       operatorv1.ConsoleSpec{Plugins: []string{"monitoring-plugin", "osc-openshift-console-plugin"}},
	}
	r, cl := newConsolePluginTestReconciler(t, console)

	enabled, err := r.syncConsolePluginEnabledState(context.Background())
	if err != nil {
		t.Fatalf("syncConsolePluginEnabledState: %v", err)
	}
	if !enabled {
		t.Fatal("administrator enablement not reported")
	}

	got := consoleOperatorConfig(t, cl)
	if !hasPlugin(got, r.PluginName) {
		t.Fatalf("plugin %q unexpectedly removed, spec.plugins=%v", r.PluginName, got.Spec.Plugins)
	}
	if got.Annotations[r.enabledMarkerKey()] == "" {
		t.Fatalf("marker annotation %q not set, annotations=%v", r.enabledMarkerKey(), got.Annotations)
	}
	if len(got.Spec.Plugins) != 2 {
		t.Fatalf("spec.plugins modified, want exactly the administrator's two entries, got %v", got.Spec.Plugins)
	}
}

func TestSyncMarksAdministratorDisable(t *testing.T) {
	// The administrator removed the plugin: the operator must not add it back
	// and drops the marker.
	console := &operatorv1.Console{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "cluster",
			Annotations: map[string]string{enabledMarkerPrefix + "osc-openshift-console-plugin": "true"},
		},
		Spec: operatorv1.ConsoleSpec{Plugins: []string{"monitoring-plugin"}},
	}
	r, cl := newConsolePluginTestReconciler(t, console)

	for i := 0; i < 3; i++ {
		enabled, err := r.syncConsolePluginEnabledState(context.Background())
		if err != nil {
			t.Fatalf("syncConsolePluginEnabledState: %v", err)
		}
		if enabled {
			t.Fatal("reported the plugin as enabled after the administrator disabled it")
		}
	}

	got := consoleOperatorConfig(t, cl)
	if hasPlugin(got, r.PluginName) {
		t.Fatalf("plugin %q was re-enabled after an administrator disabled it, spec.plugins=%v", r.PluginName, got.Spec.Plugins)
	}
	if _, ok := got.Annotations[r.enabledMarkerKey()]; ok {
		t.Fatalf("marker annotation %q not removed, annotations=%v", r.enabledMarkerKey(), got.Annotations)
	}
}

func TestDisableConsolePluginRemovesMarker(t *testing.T) {
	console := &operatorv1.Console{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "cluster",
			Annotations: map[string]string{enabledMarkerPrefix + "osc-openshift-console-plugin": "true"},
		},
		Spec: operatorv1.ConsoleSpec{Plugins: []string{"osc-openshift-console-plugin"}},
	}
	r, cl := newConsolePluginTestReconciler(t, console)

	if err := r.disableConsolePlugin(context.Background()); err != nil {
		t.Fatalf("disableConsolePlugin: %v", err)
	}

	got := consoleOperatorConfig(t, cl)
	if hasPlugin(got, r.PluginName) {
		t.Fatalf("plugin %q still enabled, spec.plugins=%v", r.PluginName, got.Spec.Plugins)
	}
	if _, ok := got.Annotations[r.enabledMarkerKey()]; ok {
		t.Fatalf("marker annotation %q not removed, annotations=%v", r.enabledMarkerKey(), got.Annotations)
	}

	// Uninstall removed the entry; a reinstall must leave the plugin disabled
	// until an administrator enables it again.
	enabled, err := r.syncConsolePluginEnabledState(context.Background())
	if err != nil {
		t.Fatalf("syncConsolePluginEnabledState: %v", err)
	}
	if enabled {
		t.Fatal("reinstall reported the plugin as enabled without an administrator action")
	}
	if got := consoleOperatorConfig(t, cl); hasPlugin(got, r.PluginName) {
		t.Fatalf("plugin %q re-enabled on reinstall without an administrator action, spec.plugins=%v", r.PluginName, got.Spec.Plugins)
	}
}

func TestEnsureDeploymentFollowsEnablement(t *testing.T) {
	// The backend must not run while the plugin is disabled: the ConsolePlugin
	// CR stays so the plugin is listed, but the Deployment stays at 0 replicas.
	r, cl := newConsolePluginTestReconciler(t)

	deploy := &appsv1.Deployment{}
	key := client.ObjectKey{Name: r.PluginName, Namespace: r.namespace}
	replicas := func() int32 {
		t.Helper()
		if err := cl.Get(context.Background(), key, deploy); err != nil {
			t.Fatalf("reading Deployment: %v", err)
		}
		if deploy.Spec.Replicas == nil {
			t.Fatal("Deployment replicas unset")
		}
		return *deploy.Spec.Replicas
	}

	if err := r.ensureDeployment(context.Background(), "image", "conf-hash", "cert-hash", "4.22", nil, false); err != nil {
		t.Fatalf("ensureDeployment disabled: %v", err)
	}
	if got := replicas(); got != 0 {
		t.Fatalf("disabled plugin runs %d replicas, want 0", got)
	}

	if err := r.ensureDeployment(context.Background(), "image", "conf-hash", "cert-hash", "4.22", nil, true); err != nil {
		t.Fatalf("ensureDeployment enabled: %v", err)
	}
	if got := replicas(); got != 1 {
		t.Fatalf("enabled plugin runs %d replicas, want 1", got)
	}
}
