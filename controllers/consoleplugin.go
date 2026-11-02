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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	consolev1 "github.com/openshift/api/console/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	consolePluginPort = 9443

	// enabledMarkerPrefix is the prefix of an annotation key on the Console
	// operator CR (operator.openshift.io/v1, name "cluster") recording that an
	// administrator has enabled the plugin. Enablement in OpenShift 4.22 is the
	// presence of the plugin name in spec.plugins, which the administrator
	// toggles in the console UI. The operator never edits spec.plugins: the
	// plugins are disabled by default and only an administrator enables them,
	// at first install or later. The annotation mirrors that decision so the
	// state is visible on the CR; it is removed when the plugin is disabled and
	// on uninstall.
	enabledMarkerPrefix = "consoleplugin.openshift.io/enabled-"
)

// ConsolePluginReconciler deploys and maintains a console plugin independently
// of any CR. It starts at operator boot and self-heals if any managed resource
// is modified or deleted. Each instance manages one plugin (osc or coco).
type ConsolePluginReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	PluginName        string
	DisplayName       string
	ImageEnvVar       string
	OperatorCSVPrefix string

	namespace string
	log       logr.Logger
}

//+kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=create;list;watch
//+kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,resourceNames=osc-openshift-console-plugin;coco-openshift-console-plugin,verbs=get;update;delete
//+kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,resourceNames=cluster,verbs=get;update
//+kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,verbs=list;watch
//+kubebuilder:rbac:groups=operators.coreos.com,resources=clusterserviceversions,verbs=list;watch

func (r *ConsolePluginReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	r.log.Info("Reconciling console plugin")

	ocpVersion, err := GetOpenshiftVersion(ctx, r.Client)
	if err != nil {
		r.log.Error(err, "unable to detect OpenShift version")
		return ctrl.Result{}, fmt.Errorf("unable to detect OpenShift version: %w", err)
	}
	r.log.Info("Detected OCP version for console plugins", "version", ocpVersion)

	if !IsConsolePluginSupported(ocpVersion) {
		r.log.Info("OCP < 4.22 — console plugin operands are not supported, skipping",
			"plugin", r.PluginName, "ocpVersion", ocpVersion)
		return ctrl.Result{}, nil
	}

	csv, csvErr := r.findOperatorCSV(ctx)
	if csvErr != nil {
		r.log.Info("Could not find operator CSV, ownership will not be set", "err", csvErr)
	}

	if csv != nil && csv.GetDeletionTimestamp() != nil {
		// No finalizer is set on the CSV: OLM deletes the operator Deployment
		// as soon as the CSV starts terminating, so blocking the CSV on code
		// running in this pod deadlocks the uninstall. Cleanup of resources
		// that OLM cannot garbage collect is done on shutdown, see
		// CleanupConsolePluginsOnUninstall.
		r.log.Info("Operator CSV is being deleted, cleaning up console plugin resources")
		return ctrl.Result{}, r.deleteResources(ctx)
	}

	image := os.Getenv(r.ImageEnvVar)
	if image == "" {
		r.log.Info("Image env var not set, cleaning up console plugin resources", "envVar", r.ImageEnvVar)
		return ctrl.Result{}, r.deleteResources(ctx)
	}

	enabled, err := r.syncConsolePluginEnabledState(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin enablement: %w", err)
	}

	tlsDirectives, err := r.nginxTLSFromCluster(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin TLS profile: %w", err)
	}
	nginxConf := r.buildNginxConf(tlsDirectives)
	confHash := sha256Hash(nginxConf)

	if err := r.ensureService(ctx, csv); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin Service: %w", err)
	}
	// The Deployment mounts the serving-cert Secret, and nginx reads the
	// certificate only at startup, so the cert hash must be part of the pod
	// template from the first creation. Wait for service-ca to generate the
	// Secret (requested by ensureService) instead of creating the Deployment
	// without the hash and rolling it a moment later, which briefly runs two
	// pods for a single-replica plugin.
	certHash, err := r.certSecretHash(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin cert secret: %w", err)
	}
	if certHash == "" {
		r.log.Info("Waiting for console plugin serving cert secret", "secret", r.PluginName+"-cert")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if err := r.ensureConfigMap(ctx, nginxConf, csv); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin ConfigMap: %w", err)
	}
	if err := r.ensureDeployment(ctx, image, confHash, certHash, ocpVersion, csv, enabled); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin Deployment: %w", err)
	}
	if err := r.ensureConsolePluginCR(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("ConsolePlugin CR: %w", err)
	}
	if err := r.ensureNetworkPolicy(ctx, csv); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin NetworkPolicy: %w", err)
	}

	return ctrl.Result{}, nil
}

// deleteResources removes all resources managed by this console plugin instance.
func (r *ConsolePluginReconciler) deleteResources(ctx context.Context) error {
	// Keep going after the first failure: on uninstall RBAC may already be
	// going away, and the cluster-scoped ConsolePlugin CR is the resource that
	// nothing else will remove, so it must still be attempted.
	var errs []error
	if err := r.disableConsolePlugin(ctx); err != nil {
		errs = append(errs, fmt.Errorf("disabling console plugin: %w", err))
	}
	namespaced := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: r.PluginName, Namespace: r.namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: r.PluginName, Namespace: r.namespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: r.PluginName, Namespace: r.namespace}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: r.networkPolicyName(), Namespace: r.namespace}},
	}
	clusterScoped := []client.Object{
		&consolev1.ConsolePlugin{ObjectMeta: metav1.ObjectMeta{Name: r.PluginName}},
	}
	deleted := false
	for _, obj := range append(namespaced, clusterScoped...) {
		if err := r.Delete(ctx, obj); err != nil {
			if !k8serrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("deleting %T %s: %w", obj, obj.GetName(), err))
			}
			continue
		}
		deleted = true
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if deleted {
		r.log.Info("Deleted console plugin resources")
	}
	return nil
}

func (r *ConsolePluginReconciler) disableConsolePlugin(ctx context.Context) error {
	// changed records whether the Console CR was actually modified, so that a
	// cleanup run over already-absent resources does not report a removal.
	changed := false
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		changed = false
		console := &operatorv1.Console{}
		if err := r.Get(ctx, client.ObjectKey{Name: "cluster"}, console); err != nil {
			if k8serrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("reading Console operator CR: %w", err)
		}
		plugins := make([]string, 0, len(console.Spec.Plugins))
		for _, p := range console.Spec.Plugins {
			if p != r.PluginName {
				plugins = append(plugins, p)
			}
		}
		markerKey := r.enabledMarkerKey()
		_, marked := console.Annotations[markerKey]
		if len(plugins) == len(console.Spec.Plugins) && !marked {
			return nil
		}
		console.Spec.Plugins = plugins
		// Drop the marker too: removal leaves the plugin disabled, and a
		// reinstall must not carry a stale enabled marker.
		delete(console.Annotations, markerKey)
		if err := r.Update(ctx, console); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("removing plugin from Console operator CR: %w", err)
	}
	if changed {
		r.log.Info("Removed console plugin from Console operator CR")
	}
	return nil
}

// findOperatorCSV returns the CSV that governs this operator. When a newer CSV
// has been installed next to a terminating one (upgrade), the active CSV wins;
// a terminating CSV is returned only when it is the only one left (uninstall).
func (r *ConsolePluginReconciler) findOperatorCSV(ctx context.Context) (*unstructured.Unstructured, error) {
	csvList := &unstructured.UnstructuredList{}
	csvList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "ClusterServiceVersionList",
	})
	if err := r.List(ctx, csvList, client.InNamespace(r.namespace)); err != nil {
		return nil, err
	}
	var terminating *unstructured.Unstructured
	for i := range csvList.Items {
		csv := &csvList.Items[i]
		if !strings.HasPrefix(csv.GetName(), r.OperatorCSVPrefix) {
			continue
		}
		if csv.GetDeletionTimestamp() == nil {
			return csv, nil
		}
		if terminating == nil {
			terminating = csv
		}
	}
	return terminating, nil
}

// CleanupConsolePluginsOnUninstall removes the console plugin resources that
// OLM cannot clean up on its own when the operator is uninstalled.
//
// Namespaced operands carry an ownerReference to the CSV and are garbage
// collected with it, but ConsolePlugin is cluster-scoped and Kubernetes does
// not allow a namespaced owner (the CSV) for it. OLMv1 does not create a CSV at
// all, so there the namespaced operands have no owner either and leak too.
// OLM also deletes the operator Deployment as soon as the operator is being
// removed, so relying on a finalizer on the CSV (or on a reconcile completing
// before the pod is killed) deadlocks the uninstall. This is therefore called
// once the manager has stopped, i.e. right before the pod exits, which is
// exactly when OLM tears the operator down.
//
// Nothing is removed on a restart or upgrade, see operatorUninstallInProgress.
func CleanupConsolePluginsOnUninstall(cfg *rest.Config, scheme *runtime.Scheme, namespace, csvPrefix, deploymentName string, pluginNames []string, log logr.Logger) error {
	if len(pluginNames) == 0 {
		return nil
	}
	if namespace == "" {
		namespace = OperatorNamespace
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	uninstalling, err := operatorUninstallInProgress(ctx, cl, namespace, csvPrefix, deploymentName)
	if err != nil {
		return err
	}
	if !uninstalling {
		return nil
	}

	log.Info("Operator is being uninstalled, removing console plugin resources")
	// Attempt every plugin: one failure must not leave the other plugin's
	// cluster-scoped ConsolePlugin CR behind, since nothing else removes it.
	var errs []error
	for _, name := range pluginNames {
		r := &ConsolePluginReconciler{
			Client:     cl,
			Scheme:     scheme,
			PluginName: name,
			namespace:  namespace,
			log:        log.WithValues("plugin", name),
		}
		if err := r.deleteResources(ctx); err != nil {
			errs = append(errs, fmt.Errorf("cleaning up console plugin %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// operatorUninstallInProgress reports whether the operator is being
// uninstalled, as opposed to restarted or upgraded.
//
// OLMv0 records the installation in a ClusterServiceVersion, which OLM marks
// for deletion on uninstall. An active CSV of this operator — possibly next to
// a terminating one during an upgrade — therefore means the operator is still
// installed.
//
// OLMv1 (ClusterExtension) installs the same bundle without creating any CSV,
// so the absence of a CSV no longer distinguishes "not installed" from
// "installed by OLMv1". Both OLM versions delete the operator Deployment when
// the operator is removed and keep it on restart and upgrade, so when no CSV of
// this operator exists the Deployment is what decides. This also covers an
// OLMv0 uninstall whose CSVs are already gone by the time cleanup runs, and
// clusters that do not serve the CSV API at all.
func operatorUninstallInProgress(ctx context.Context, cl client.Client, namespace, csvPrefix, deploymentName string) (bool, error) {
	csvList := &unstructured.UnstructuredList{}
	csvList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "ClusterServiceVersionList",
	})
	switch err := cl.List(ctx, csvList, client.InNamespace(namespace)); {
	case err == nil:
	case meta.IsNoMatchError(err) || k8serrors.IsNotFound(err):
		// The CSV API is not served: this cluster does not use OLMv0, so
		// continue with the Deployment check below.
		csvList.Items = nil
	default:
		return false, fmt.Errorf("listing operator CSVs: %w", err)
	}

	found := false
	for i := range csvList.Items {
		csv := &csvList.Items[i]
		if !strings.HasPrefix(csv.GetName(), csvPrefix) {
			continue
		}
		if csv.GetDeletionTimestamp() == nil {
			return false, nil // operator still installed (restart or upgrade)
		}
		found = true
	}
	if found {
		return true, nil
	}

	// No CSV of this operator. On an OLMv1 restart or upgrade the operator
	// Deployment is still there; on uninstall OLM has deleted or is deleting
	// it.
	dep := &appsv1.Deployment{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deploymentName}, dep); err != nil {
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("checking operator Deployment %s: %w", deploymentName, err)
	}
	return dep.GetDeletionTimestamp() != nil, nil
}

func consolePluginSetCSVOwnerRef(csv *unstructured.Unstructured, obj metav1.Object) {
	if csv == nil {
		return
	}
	ref := metav1.OwnerReference{
		APIVersion: csv.GetAPIVersion(),
		Kind:       csv.GetKind(),
		Name:       csv.GetName(),
		UID:        csv.GetUID(),
	}
	existing := obj.GetOwnerReferences()
	for i, r := range existing {
		if r.UID == ref.UID {
			existing[i] = ref
			obj.SetOwnerReferences(existing)
			return
		}
	}
	obj.SetOwnerReferences(append(existing, ref))
}

func (r *ConsolePluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.namespace = os.Getenv("POD_NAMESPACE")
	if r.namespace == "" {
		r.namespace = OperatorNamespace
	}
	r.log = ctrl.Log.WithName("consoleplugin-controller").WithValues("plugin", r.PluginName)

	ch := make(chan event.GenericEvent, 1)
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		if !mgr.GetCache().WaitForCacheSync(ctx) {
			return fmt.Errorf("cache sync failed for consoleplugin-%s", r.PluginName)
		}
		ch <- event.GenericEvent{Object: &consolev1.ConsolePlugin{
			ObjectMeta: metav1.ObjectMeta{Name: r.PluginName},
		}}
		return nil
	})); err != nil {
		return err
	}

	mapToRequest := handler.EnqueueRequestsFromMapFunc(
		func(_ context.Context, _ client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: r.PluginName}}}
		},
	)

	nameFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == r.PluginName
	})
	nsNameFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == r.PluginName && obj.GetNamespace() == r.namespace
	})
	clusterFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == "cluster"
	})
	certSecretName := r.PluginName + "-cert"
	certFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == certSecretName && obj.GetNamespace() == r.namespace
	})
	npName := r.networkPolicyName()
	npFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == npName && obj.GetNamespace() == r.namespace
	})

	csvObj := &unstructured.Unstructured{}
	csvObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "ClusterServiceVersion",
	})
	csvFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == r.namespace && strings.HasPrefix(obj.GetName(), r.OperatorCSVPrefix)
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("consoleplugin-"+r.PluginName).
		WatchesRawSource(source.Channel(ch, &handler.EnqueueRequestForObject{})).
		Watches(&consolev1.ConsolePlugin{}, mapToRequest, builder.WithPredicates(nameFilter)).
		Watches(&appsv1.Deployment{}, mapToRequest, builder.WithPredicates(nsNameFilter)).
		Watches(&corev1.Service{}, mapToRequest, builder.WithPredicates(nsNameFilter)).
		Watches(&corev1.ConfigMap{}, mapToRequest, builder.WithPredicates(nsNameFilter)).
		Watches(&networkingv1.NetworkPolicy{}, mapToRequest, builder.WithPredicates(npFilter)).
		Watches(&configv1.APIServer{}, mapToRequest, builder.WithPredicates(clusterFilter)).
		// Console spec changes are how an administrator enables or disables the
		// plugin; GenerationChangedPredicate skips the frequent status updates.
		Watches(&operatorv1.Console{}, mapToRequest, builder.WithPredicates(clusterFilter, predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Secret{}, mapToRequest, builder.WithPredicates(certFilter)).
		Watches(csvObj, mapToRequest, builder.WithPredicates(csvFilter)).
		Complete(r)
}

// --- labels ---

// consolePluginSelectorLabels returns the minimal, immutable set used by Deployment.Spec.Selector.
func (r *ConsolePluginReconciler) consolePluginSelectorLabels() map[string]string {
	return map[string]string{
		"app": r.PluginName,
	}
}

// consolePluginLabels returns the full label set for metadata and pod templates.
func (r *ConsolePluginReconciler) consolePluginLabels() map[string]string {
	return map[string]string{
		"app":                          r.PluginName,
		"app.kubernetes.io/name":       r.PluginName,
		"app.kubernetes.io/managed-by": "sandboxed-containers-operator",
		"app.kubernetes.io/part-of":    "sandboxed-containers",
		"app.kubernetes.io/component":  "console-plugin",
	}
}

// --- ConfigMap (nginx.conf) ---

func (r *ConsolePluginReconciler) ensureConfigMap(ctx context.Context, nginxConf string, csv *unstructured.Unstructured) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.PluginName,
			Namespace: r.namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = r.consolePluginLabels()
		cm.Data = map[string]string{
			"nginx.conf": nginxConf,
		}
		consolePluginSetCSVOwnerRef(csv, cm)
		return nil
	})
	return err
}

// buildNginxConf renders the plugin's nginx configuration from the resolved TLS
// directives. http2 and ssl_conf_command need nginx >= 1.25.1 and
// OpenSSL >= 1.1.1 respectively; the plugin images (ubi9/nginx-126) satisfy both.
func (r *ConsolePluginReconciler) buildNginxConf(nd consolePluginNginxTLSDirectives) string {
	return fmt.Sprintf(`error_log /dev/stdout info;
events {}
http {
  access_log         off;
  include            /etc/nginx/mime.types;
  default_type       application/octet-stream;
  keepalive_timeout  65;
  server {
    listen              %d ssl;
    listen              [::]:%d ssl;
    http2               on;
    ssl_certificate     /var/cert/tls.crt;
    ssl_certificate_key /var/cert/tls.key;
    ssl_protocols       %s;
    ssl_ciphers         %s;
    ssl_conf_command    Ciphersuites %s;
    ssl_prefer_server_ciphers on;
    ssl_ecdh_curve      %s;
    server_tokens       off;
    root                /usr/share/nginx/html;
  }
}
`, consolePluginPort, consolePluginPort, nd.protocols, nd.ciphers, nd.ciphersuites, nd.ecdhCurve)
}

// --- TLS profile → nginx directives ---

type consolePluginNginxTLSDirectives struct {
	protocols    string
	ciphers      string
	ciphersuites string
	ecdhCurve    string
}

// consolePluginECDHCurves is the TLS 1.3 key exchange group preference. The
// ML-KEM hybrids come first: X25519MLKEM768 is the one browsers actually offer,
// and a group list without it makes a PQC-capable client fall back to a
// classical group, so the hybrid is never negotiated. The classical groups keep
// clients without ML-KEM support working.
var consolePluginECDHCurves = []string{
	"X25519MLKEM768", "X25519",
	"SecP256r1MLKEM768", "SecP384r1MLKEM1024",
	"secp256r1", "secp384r1",
}

var consolePluginDefaultCiphers = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305"

// consolePluginDefaultCipherSuites is the TLS 1.3 suite list shared by the API's
// Old, Intermediate and Modern profiles. TLS 1.3 suites are not controlled by
// ssl_ciphers; they only take effect through ssl_conf_command Ciphersuites.
var consolePluginDefaultCipherSuites = "TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256"

func consolePluginDefaultTLSDirectives() consolePluginNginxTLSDirectives {
	return consolePluginNginxTLSDirectives{
		protocols:    "TLSv1.2 TLSv1.3",
		ciphers:      consolePluginDefaultCiphers,
		ciphersuites: consolePluginDefaultCipherSuites,
		ecdhCurve:    strings.Join(consolePluginECDHCurves, ":"),
	}
}

// nginxTLSFromCluster resolves the plugin's nginx TLS directives from the
// cluster-wide TLS security profile.
//
// The plugin has no TLS configuration of its own: it always serves with the
// cluster's profile. That leaves nothing legitimate to fall back to when the
// profile cannot be read, because the operator's defaults can be weaker than a
// Modern or Custom profile asks for. Under tlsAdherence=StrictAllComponents
// that is a compliance violation, and even under
// LegacyAdheringComponentsOnly this plugin is one of the components that
// already honors the profile. A failed read is therefore an error instead of a
// silent downgrade: the running pods keep the configuration rendered from the
// last profile that could be read, and the reconcile retries.
func (r *ConsolePluginReconciler) nginxTLSFromCluster(ctx context.Context) (consolePluginNginxTLSDirectives, error) {
	apiServer := &configv1.APIServer{}
	if err := r.Get(ctx, client.ObjectKey{Name: "cluster"}, apiServer); err != nil {
		return consolePluginNginxTLSDirectives{}, fmt.Errorf("reading the cluster TLS security profile: %w", err)
	}

	adherence := apiServer.Spec.TLSAdherence
	strict := consolePluginTLSAdherenceIsStrict(adherence)
	if strict && adherence != configv1.TLSAdherencePolicyStrictAllComponents {
		// The API requires components to treat an unknown value as
		// StrictAllComponents so a future enum keeps the stricter behavior.
		r.log.Info("Unrecognized tlsAdherence, treating it as StrictAllComponents", "tlsAdherence", adherence)
	}

	profile := apiServer.Spec.TLSSecurityProfile
	if profile == nil {
		// No opinion: the platform default is Intermediate, which is exactly what
		// the defaults encode, so this still serves the cluster's policy.
		return consolePluginDefaultTLSDirectives(), nil
	}
	if strict && !consolePluginTLSProfileResolvable(profile) {
		return consolePluginNginxTLSDirectives{}, fmt.Errorf(
			"tlsAdherence=%s requires honoring the cluster TLS profile, but %q is not a profile this operator can render",
			adherence, profile.Type)
	}

	return consolePluginTLSProfileToNginx(profile), nil
}

// consolePluginTLSAdherenceIsStrict reports whether the cluster requires every
// component to serve with exactly the configured TLS profile. An unset value
// means no opinion (the current default is LegacyAdheringComponentsOnly); an
// unrecognized value must be treated as StrictAllComponents.
func consolePluginTLSAdherenceIsStrict(adherence configv1.TLSAdherencePolicy) bool {
	switch adherence {
	case configv1.TLSAdherencePolicyNoOpinion, configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly:
		return false
	default:
		return true
	}
}

// consolePluginTLSProfileResolvable reports whether the profile can be
// rendered without substituting a different one. Type is a validated enum, so
// this only rejects a Custom profile whose spec is missing and a profile type
// added after this operator was built.
func consolePluginTLSProfileResolvable(profile *configv1.TLSSecurityProfile) bool {
	switch profile.Type {
	case "", configv1.TLSProfileOldType, configv1.TLSProfileIntermediateType, configv1.TLSProfileModernType:
		return true
	case configv1.TLSProfileCustomType:
		return profile.Custom != nil
	default:
		return false
	}
}

func consolePluginTLSProfileToNginx(profile *configv1.TLSSecurityProfile) consolePluginNginxTLSDirectives {
	var spec configv1.TLSProfileSpec
	switch profile.Type {
	case configv1.TLSProfileCustomType:
		if profile.Custom != nil {
			spec = profile.Custom.TLSProfileSpec
		} else {
			spec = *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		}
	default:
		profileType := profile.Type
		if profileType == "" {
			profileType = configv1.TLSProfileIntermediateType
		}
		s, ok := configv1.TLSProfiles[profileType]
		if !ok {
			s = configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		}
		spec = *s
	}

	nd := consolePluginNginxTLSDirectives{}
	nd.protocols = consolePluginNginxProtocols(spec.MinTLSVersion)

	legacyCiphers, tls13Suites := consolePluginSplitCipherSuites(spec.Ciphers)
	legacyCiphers = consolePluginFilterWeakCiphers(consolePluginFilterDHECiphers(legacyCiphers))
	if len(legacyCiphers) > 0 {
		nd.ciphers = strings.Join(legacyCiphers, ":")
	} else {
		nd.ciphers = consolePluginDefaultCiphers
	}
	if len(tls13Suites) > 0 {
		nd.ciphersuites = strings.Join(tls13Suites, ":")
	} else {
		nd.ciphersuites = consolePluginDefaultCipherSuites
	}

	nd.ecdhCurve = strings.Join(consolePluginECDHCurves, ":")
	return nd
}

func consolePluginNginxProtocols(minVersion configv1.TLSProtocolVersion) string {
	switch minVersion {
	case configv1.VersionTLS13:
		return "TLSv1.3"
	default:
		// OpenSSL 3 in UBI 9 disables TLS 1.0/1.1, so the Old profile's lower
		// minTLSVersion is clamped to TLS 1.2: enabling them would only put
		// protocols in the rendered config that can never be negotiated. The
		// profile's TLS 1.2 ciphers are still applied.
		return "TLSv1.2 TLSv1.3"
	}
}

func consolePluginFilterWeakCiphers(ciphers []string) []string {
	weakPatterns := []string{"NULL", "EXPORT", "RC4", "MD5", "DES", "3DES", "BF-", "ECB"}
	filtered := make([]string, 0, len(ciphers))
	for _, c := range ciphers {
		upper := strings.ToUpper(c)
		weak := false
		for _, p := range weakPatterns {
			if strings.Contains(upper, p) {
				weak = true
				break
			}
		}
		if !weak && strings.HasSuffix(upper, "-SHA") {
			weak = true
		}
		if !weak {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// consolePluginSplitCipherSuites separates the TLS 1.3 suites (TLS_*) from the
// TLS 1.2 and below ciphers. nginx configures the two with different directives:
// ssl_ciphers ignores TLS 1.3 suites, and ssl_conf_command Ciphersuites rejects
// the legacy names.
func consolePluginSplitCipherSuites(ciphers []string) (legacy, tls13 []string) {
	legacy = make([]string, 0, len(ciphers))
	for _, c := range ciphers {
		if strings.HasPrefix(c, "TLS_") {
			tls13 = append(tls13, c)
		} else {
			legacy = append(legacy, c)
		}
	}
	return legacy, tls13
}

func consolePluginFilterDHECiphers(ciphers []string) []string {
	filtered := make([]string, 0, len(ciphers))
	for _, c := range ciphers {
		if strings.HasPrefix(c, "DHE-") || strings.HasPrefix(c, "TLS_DHE_") {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
}

// --- Deployment ---

func sha256Hash(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func (r *ConsolePluginReconciler) certSecretHash(ctx context.Context) (string, error) {
	secret := &corev1.Secret{}
	key := client.ObjectKey{Name: r.PluginName + "-cert", Namespace: r.namespace}
	if err := r.Get(ctx, key, secret); err != nil {
		if k8serrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading cert secret: %w", err)
	}
	h := sha256.New()
	for _, k := range []string{"tls.crt", "tls.key"} {
		h.Write(secret.Data[k])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (r *ConsolePluginReconciler) ensureDeployment(ctx context.Context, image string, confHash string, certHash string, ocpVersion string, csv *unstructured.Unstructured, enabled bool) error {
	// The backend only runs while an administrator has the plugin enabled. The
	// Deployment is kept at zero replicas otherwise, so the plugin still appears
	// in the console's plugin list (the ConsolePlugin CR stays) without spending
	// resources on a plugin nobody uses.
	replicas := int32(0)
	if enabled {
		replicas = 1
	}
	selectorLabels := r.consolePluginSelectorLabels()
	labels := r.consolePluginLabels()
	certSecretName := r.PluginName + "-cert"

	annotations := map[string]string{
		"sandboxed-containers.openshift.io/nginx-conf-hash": confHash,
	}
	if certHash != "" {
		annotations["sandboxed-containers.openshift.io/cert-hash"] = certHash
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.PluginName,
			Namespace: r.namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		// Merge rather than replace: OLM labels the Deployments owned by a CSV
		// with olm.managed/olm.owner and re-applies them whenever it starts. A
		// blind assignment deletes those labels, and since this controller
		// watches its own Deployment, OLM's repair is undone immediately. That
		// keeps olm-operator stuck in its startup labelling loop, so it never
		// becomes ready and CSVs cannot be uninstalled.
		if deploy.Labels == nil {
			deploy.Labels = map[string]string{}
		}
		maps.Copy(deploy.Labels, labels)
		deploy.Spec.Replicas = &replicas
		if deploy.Spec.Selector == nil {
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorLabels}
		}
		deploy.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: ptr.To(true),
					SeccompProfile: &corev1.SeccompProfile{
						Type: corev1.SeccompProfileTypeRuntimeDefault,
					},
				},
				Containers: []corev1.Container{{
					Name:            r.PluginName,
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env: []corev1.EnvVar{
						{
							Name:  "OCP_VERSION",
							Value: ocpVersion,
						},
					},
					Ports: []corev1.ContainerPort{{
						ContainerPort: consolePluginPort,
						Protocol:      corev1.ProtocolTCP,
					}},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path:   "/plugin-manifest.json",
								Port:   intstr.FromInt32(consolePluginPort),
								Scheme: corev1.URISchemeHTTPS,
							},
						},
						InitialDelaySeconds: 5,
						PeriodSeconds:       10,
					},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "cert",
							ReadOnly:  true,
							MountPath: "/var/cert",
						},
						{
							Name:      "nginx-conf",
							ReadOnly:  true,
							MountPath: "/etc/nginx/nginx.conf",
							SubPath:   "nginx.conf",
						},
						{
							Name:      "nginx-tmp",
							MountPath: "/tmp/nginx",
						},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  certSecretName,
								DefaultMode: ptr.To(int32(420)),
							},
						},
					},
					{
						Name: "nginx-conf",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: r.PluginName,
								},
								DefaultMode: ptr.To(int32(420)),
							},
						},
					},
					{
						Name:         "nginx-tmp",
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					},
				},
			},
		}
		consolePluginSetCSVOwnerRef(csv, deploy)
		return nil
	})
	return err
}

// --- Service ---

func (r *ConsolePluginReconciler) ensureService(ctx context.Context, csv *unstructured.Unstructured) error {
	certSecretName := r.PluginName + "-cert"
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.PluginName,
			Namespace: r.namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = r.consolePluginLabels()
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations["service.alpha.openshift.io/serving-cert-secret-name"] = certSecretName
		svc.Spec.Selector = r.consolePluginSelectorLabels()
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "https",
			Protocol:   corev1.ProtocolTCP,
			Port:       consolePluginPort,
			TargetPort: intstr.FromInt32(consolePluginPort),
		}}
		consolePluginSetCSVOwnerRef(csv, svc)
		return nil
	})
	return err
}

// --- ConsolePlugin CR ---

func (r *ConsolePluginReconciler) ensureConsolePluginCR(ctx context.Context) error {
	cp := &consolev1.ConsolePlugin{
		ObjectMeta: metav1.ObjectMeta{
			Name: r.PluginName,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cp, func() error {
		cp.Labels = r.consolePluginLabels()
		cp.Spec = consolev1.ConsolePluginSpec{
			DisplayName: r.DisplayName,
			I18n: consolev1.ConsolePluginI18n{
				LoadType: consolev1.Preload,
			},
			Backend: consolev1.ConsolePluginBackend{
				Type: consolev1.Service,
				Service: &consolev1.ConsolePluginService{
					Name:      r.PluginName,
					Namespace: r.namespace,
					Port:      consolePluginPort,
					BasePath:  "/",
				},
			},
		}
		return nil
	})
	return err
}

func (r *ConsolePluginReconciler) enabledMarkerKey() string {
	return enabledMarkerPrefix + r.PluginName
}

// syncConsolePluginEnabledState mirrors the administrator's enablement decision
// in the enabled- marker annotation so it is visible on the Console CR, logs
// the transition, and reports whether the plugin is currently enabled. The
// operator never edits spec.plugins: the plugin is disabled by default and is
// enabled only by an administrator, either during the first install or later
// from the console UI. The ConsolePlugin CR is deployed regardless, so the
// plugin is available in the console's plugin list to be enabled; the caller
// uses the result to keep the backend scaled down while the plugin is disabled.
func (r *ConsolePluginReconciler) syncConsolePluginEnabledState(ctx context.Context) (bool, error) {
	enabled := false
	state := ""
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		state = ""
		console := &operatorv1.Console{}
		if err := r.Get(ctx, client.ObjectKey{Name: "cluster"}, console); err != nil {
			return fmt.Errorf("reading Console operator CR: %w", err)
		}

		markerKey := r.enabledMarkerKey()
		_, marked := console.Annotations[markerKey]

		enabled = false
		for _, p := range console.Spec.Plugins {
			if p == r.PluginName {
				enabled = true
				break
			}
		}

		switch {
		case enabled && !marked:
			if console.Annotations == nil {
				console.Annotations = map[string]string{}
			}
			console.Annotations[markerKey] = "true"
			state = "enabled"
		case !enabled && marked:
			delete(console.Annotations, markerKey)
			state = "disabled"
		default:
			return nil
		}
		return r.Update(ctx, console)
	})
	if err != nil {
		return false, fmt.Errorf("syncing plugin enablement on Console operator CR: %w", err)
	}
	if state != "" {
		r.log.Info("Console plugin enablement changed by an administrator", "state", state)
	}
	return enabled, nil
}

// --- NetworkPolicy ---

func (r *ConsolePluginReconciler) networkPolicyName() string {
	return "allow-" + r.PluginName
}

func (r *ConsolePluginReconciler) ensureNetworkPolicy(ctx context.Context, csv *unstructured.Unstructured) error {
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(consolePluginPort)

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.networkPolicyName(),
			Namespace: r.namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		np.Labels = r.consolePluginLabels()
		np.Annotations = map[string]string{
			"kubernetes.io/description": "Allows ingress from the OpenShift console to the " + r.PluginName + " backend.",
		}
		np.Spec = networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: r.consolePluginSelectorLabels(),
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"kubernetes.io/metadata.name": "openshift-console",
						},
					},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "console",
						},
					},
				}},
				Ports: []networkingv1.NetworkPolicyPort{{
					Protocol: &tcp,
					Port:     &port,
				}},
			}},
		}
		consolePluginSetCSVOwnerRef(csv, np)
		return nil
	})
	return err
}
