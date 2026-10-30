package controllers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newConsolePluginTestReconciler(t *testing.T, objs ...client.Object) (*ConsolePluginReconciler, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := operatorv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	if err := configv1.Install(scheme); err != nil {
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

func TestEnsureDeploymentPreservesForeignLabels(t *testing.T) {
	// OLM labels the Deployments owned by a CSV with olm.managed/olm.owner and
	// repairs them when they drift. The reconciler only owns its own label
	// keys: replacing the map makes OLM's labeller and this controller fight,
	// which leaves olm-operator in its startup loop and blocks CSV uninstall.
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "osc-openshift-console-plugin",
			Namespace: "test-ns",
			Labels: map[string]string{
				"olm.managed":           "true",
				"olm.owner":             "sandboxed-containers-operator.v1.14.0",
				"example.com/unrelated": "keep",
			},
		},
	}
	r, cl := newConsolePluginTestReconciler(t, existing)

	// Reconcile twice: the second pass must not strip the labels either.
	for i := 0; i < 2; i++ {
		if err := r.ensureDeployment(context.Background(), "image", "conf-hash", "cert-hash", "4.22", nil, false); err != nil {
			t.Fatalf("ensureDeployment: %v", err)
		}
	}

	got := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: r.PluginName, Namespace: r.namespace}, got); err != nil {
		t.Fatalf("reading Deployment: %v", err)
	}
	for k, want := range map[string]string{
		"olm.managed":           "true",
		"olm.owner":             "sandboxed-containers-operator.v1.14.0",
		"example.com/unrelated": "keep",
	} {
		if got.Labels[k] != want {
			t.Errorf("foreign label %q = %q, want %q (labels: %v)", k, got.Labels[k], want, got.Labels)
		}
	}
	for k, want := range r.consolePluginLabels() {
		if got.Labels[k] != want {
			t.Errorf("managed label %q = %q, want %q (labels: %v)", k, got.Labels[k], want, got.Labels)
		}
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

func TestConsolePluginNginxDirectivesFollowTLSProfile(t *testing.T) {
	const (
		defaultSuites = "TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256"
		intermediate  = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305"
	)

	cases := []struct {
		name          string
		profile       *configv1.TLSSecurityProfile
		wantProtocols string
		wantCiphers   string
		wantSuites    string
		curveContains string
	}{
		{
			name:          "Intermediate",
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   intermediate,
			wantSuites:    defaultSuites,
		},
		{
			// OpenSSL 3 cannot negotiate TLS 1.0/1.1: the Old profile is clamped
			// to TLS 1.2 instead of advertising protocols that never handshake.
			name:          "Old is clamped to TLS 1.2",
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileOldType},
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantSuites:    defaultSuites,
		},
		{
			name:          "Modern",
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			wantProtocols: "TLSv1.3",
			wantCiphers:   intermediate, // no TLS 1.2 ciphers in the profile, fall back
			wantSuites:    defaultSuites,
		},
		{
			// A Custom profile's TLS 1.3 suite restriction must reach
			// ssl_conf_command Ciphersuites; ssl_ciphers does not affect TLS 1.3.
			name: "Custom restricts TLS 1.3 suites",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS13,
					Ciphers:       []string{"TLS_AES_256_GCM_SHA384", "ECDHE-RSA-AES256-GCM-SHA384"},
				}},
			},
			wantProtocols: "TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES256-GCM-SHA384",
			wantSuites:    "TLS_AES_256_GCM_SHA384",
		},
		{
			// A Custom profile without TLS 1.3 suites keeps working: the
			// standard suite list is pinned rather than left to OpenSSL's defaults.
			name: "Custom without TLS 1.3 suites",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS12,
					Ciphers:       []string{"ECDHE-RSA-AES256-GCM-SHA384"},
				}},
			},
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES256-GCM-SHA384",
			wantSuites:    defaultSuites,
		},
		{
			// Browsers only offer X25519MLKEM768: if it is missing from the group
			// list a PQC-capable client silently falls back to a classical group.
			name:          "unset profile uses the defaults",
			profile:       nil,
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   intermediate,
			wantSuites:    defaultSuites,
			curveContains: "X25519MLKEM768",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nd := consolePluginDefaultTLSDirectives()
			if tc.profile != nil {
				nd = consolePluginTLSProfileToNginx(tc.profile)
			}

			if nd.protocols != tc.wantProtocols {
				t.Errorf("ssl_protocols = %q, want %q", nd.protocols, tc.wantProtocols)
			}
			if tc.wantCiphers != "" && nd.ciphers != tc.wantCiphers {
				t.Errorf("ssl_ciphers = %q, want %q", nd.ciphers, tc.wantCiphers)
			}
			if strings.Contains(nd.ciphers, "TLS_") {
				t.Errorf("ssl_ciphers contains TLS 1.3 suites, which nginx ignores there: %q", nd.ciphers)
			}
			if nd.ciphersuites != tc.wantSuites {
				t.Errorf("Ciphersuites = %q, want %q", nd.ciphersuites, tc.wantSuites)
			}
			if !strings.HasPrefix(nd.ecdhCurve, "X25519MLKEM768:X25519") {
				t.Errorf("ssl_ecdh_curve = %q, want the ML-KEM hybrid first", nd.ecdhCurve)
			}
			if tc.curveContains != "" && !strings.Contains(nd.ecdhCurve, tc.curveContains) {
				t.Errorf("ssl_ecdh_curve = %q, want it to contain %q", nd.ecdhCurve, tc.curveContains)
			}
		})
	}
}

func TestBuildNginxConfEmitsProfileDirectives(t *testing.T) {
	r := &ConsolePluginReconciler{}
	conf := r.buildNginxConf(consolePluginTLSProfileToNginx(
		&configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType}))

	for _, want := range []string{
		"listen              9443 ssl;",
		"http2               on;",
		"ssl_protocols       TLSv1.2 TLSv1.3;",
		"ssl_ciphers         ECDHE-ECDSA-AES128-GCM-SHA256:",
		"ssl_conf_command    Ciphersuites " + consolePluginDefaultCipherSuites + ";",
		"ssl_ecdh_curve      X25519MLKEM768:X25519:SecP256r1MLKEM768:SecP384r1MLKEM1024:secp256r1:secp384r1;",
		"server_tokens       off;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("nginx.conf is missing %q\n%s", want, conf)
		}
	}
}

func consolePluginAPIServer(adherence configv1.TLSAdherencePolicy, profile *configv1.TLSSecurityProfile) *configv1.APIServer {
	return &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSAdherence:       adherence,
			TLSSecurityProfile: profile,
		},
	}
}

func TestNginxTLSFromClusterFailsClosed(t *testing.T) {
	// The plugin always serves with the cluster's profile and has no TLS
	// configuration of its own, so there is nothing to fall back to when the
	// profile cannot be read: rendering the operator's defaults could serve
	// TLS 1.2 on a cluster that configured Modern. The running pods keep the
	// last configuration rendered from a profile that could be read.
	r, _ := newConsolePluginTestReconciler(t)

	if _, err := r.nginxTLSFromCluster(context.Background()); err == nil {
		t.Fatal("missing APIServer CR resolved to directives instead of failing closed")
	}
}

func TestNginxTLSFromClusterFollowsProfile(t *testing.T) {
	cases := []struct {
		name          string
		adherence     configv1.TLSAdherencePolicy
		profile       *configv1.TLSSecurityProfile
		wantProtocols string
		wantErr       bool
	}{
		{
			// No profile means no opinion from the administrator; the platform
			// default is Intermediate, so the defaults are the cluster's policy.
			name:          "unset profile is the Intermediate default",
			wantProtocols: "TLSv1.2 TLSv1.3",
		},
		{
			name:          "Intermediate",
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			wantProtocols: "TLSv1.2 TLSv1.3",
		},
		{
			name:          "Modern",
			adherence:     configv1.TLSAdherencePolicyStrictAllComponents,
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			wantProtocols: "TLSv1.3",
		},
		{
			// Switching Modern back to Intermediate must restore TLS 1.2.
			name:          "Intermediate after Modern",
			adherence:     configv1.TLSAdherencePolicyStrictAllComponents,
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			wantProtocols: "TLSv1.2 TLSv1.3",
		},
		{
			name:          "unrecognized adherence is treated as strict",
			adherence:     configv1.TLSAdherencePolicy("SomethingNewer"),
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			wantProtocols: "TLSv1.3",
		},
		{
			name:      "strict adherence rejects a profile it cannot render",
			adherence: configv1.TLSAdherencePolicyStrictAllComponents,
			profile:   &configv1.TLSSecurityProfile{Type: configv1.TLSProfileType("FutureProfile")},
			wantErr:   true,
		},
		{
			// Legacy tolerates an unknown profile by substituting Intermediate,
			// which is what the API's Old/Intermediate/Modern defaults encode.
			name:          "legacy adherence substitutes Intermediate for an unknown profile",
			adherence:     configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileType("FutureProfile")},
			wantProtocols: "TLSv1.2 TLSv1.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newConsolePluginTestReconciler(t, consolePluginAPIServer(tc.adherence, tc.profile))

			nd, err := r.nginxTLSFromCluster(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatal("profile was silently substituted, want an error under strict adherence")
				}
				return
			}
			if err != nil {
				t.Fatalf("nginxTLSFromCluster: %v", err)
			}
			if nd.protocols != tc.wantProtocols {
				t.Errorf("ssl_protocols = %q, want %q", nd.protocols, tc.wantProtocols)
			}
		})
	}
}

func TestNginxConfHashChangesWithProfile(t *testing.T) {
	// The pod template carries the hash of the rendered config, so a profile
	// change has to change the hash; otherwise the Deployment never rolls and
	// the pods keep the TLS settings of the previous profile.
	r := &ConsolePluginReconciler{}
	hash := func(profile *configv1.TLSSecurityProfile) string {
		return sha256Hash(r.buildNginxConf(consolePluginTLSProfileToNginx(profile)))
	}

	modern := hash(&configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType})
	intermediate := hash(&configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType})
	if modern == intermediate {
		t.Fatal("Modern and Intermediate render the same hash: a profile change would not roll the Deployment")
	}
}

const uninstallTestNamespace = "openshift-sandboxed-containers-operator"

func newUninstallTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	csvGV := schema.GroupVersion{Group: "operators.coreos.com", Version: "v1alpha1"}
	scheme.AddKnownTypeWithName(csvGV.WithKind("ClusterServiceVersion"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(csvGV.WithKind("ClusterServiceVersionList"), &unstructured.UnstructuredList{})

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func operatorCSV(name string, terminating bool) *unstructured.Unstructured {
	csv := &unstructured.Unstructured{}
	csv.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "ClusterServiceVersion",
	})
	csv.SetName(name)
	csv.SetNamespace(uninstallTestNamespace)
	csv.SetUID(types.UID(name))
	if terminating {
		now := metav1.Now()
		csv.SetDeletionTimestamp(&now)
		csv.SetFinalizers([]string{"olm.finalizer"})
	}
	return csv
}

func operatorDeployment(terminating bool) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "controller-manager",
			Namespace: uninstallTestNamespace,
		},
	}
	if terminating {
		now := metav1.Now()
		dep.SetDeletionTimestamp(&now)
		dep.SetFinalizers([]string{"test.finalizer"})
	}
	return dep
}

func TestOperatorUninstallInProgress(t *testing.T) {
	const (
		csvPrefix      = "sandboxed-containers-operator"
		deploymentName = "controller-manager"
	)

	cases := []struct {
		name string
		objs []client.Object
		want bool
	}{
		{
			// OLMv1 restart or in-place upgrade: no CSV exists, the operator
			// Deployment is still running.
			name: "OLMv1 restart with live deployment",
			objs: []client.Object{operatorDeployment(false)},
			want: false,
		},
		{
			// OLMv1 uninstall: the ClusterExtension removal has already deleted
			// the operator Deployment (which is what restarted this pod).
			name: "OLMv1 uninstall with deployment gone",
			objs: nil,
			want: true,
		},
		{
			// OLMv1 uninstall still in progress: Deployment is terminating.
			name: "OLMv1 uninstall with terminating deployment",
			objs: []client.Object{operatorDeployment(true)},
			want: true,
		},
		{
			// OLMv0 install that is not going away: an active CSV wins over
			// any Deployment state.
			name: "OLMv0 restart with active CSV",
			objs: []client.Object{
				operatorCSV("sandboxed-containers-operator.v1.14.0", false),
				operatorDeployment(false),
			},
			want: false,
		},
		{
			// OLMv0 upgrade: the new CSV is already there even though the old
			// operator Deployment may be gone.
			name: "OLMv0 upgrade with active and terminating CSV",
			objs: []client.Object{
				operatorCSV("sandboxed-containers-operator.v1.13.1", true),
				operatorCSV("sandboxed-containers-operator.v1.14.0", false),
			},
			want: false,
		},
		{
			name: "OLMv0 uninstall with terminating CSV",
			objs: []client.Object{
				operatorCSV("sandboxed-containers-operator.v1.14.0", true),
				operatorDeployment(true),
			},
			want: true,
		},
		{
			// The terminating CSV alone marks the uninstall; the Deployment
			// may not have been deleted yet when the pod exits.
			name: "OLMv0 uninstall with live deployment",
			objs: []client.Object{
				operatorCSV("sandboxed-containers-operator.v1.14.0", true),
				operatorDeployment(false),
			},
			want: true,
		},
		{
			// The CSVs can be gone before the shutdown cleanup runs; the
			// Deployment check must still detect the uninstall.
			name: "OLMv0 uninstall after CSVs are gone",
			objs: nil,
			want: true,
		},
		{
			// CSVs of a different operator are ignored.
			name: "other operator CSV is ignored",
			objs: []client.Object{operatorCSV("other-operator.v1.0.0", false)},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := newUninstallTestClient(t, tc.objs...)
			got, err := operatorUninstallInProgress(context.Background(), cl, uninstallTestNamespace, csvPrefix, deploymentName)
			if err != nil {
				t.Fatalf("operatorUninstallInProgress: %v", err)
			}
			if got != tc.want {
				t.Errorf("operatorUninstallInProgress = %v, want %v", got, tc.want)
			}
		})
	}
}
