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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"

	provider "github.com/confidential-containers/cloud-api-adaptor/src/cloud-providers"
	peerpodcontrollers "github.com/confidential-containers/cloud-api-adaptor/src/peerpod-ctrl/controllers"
	configv1 "github.com/openshift/api/config/v1"
	mcfgapi "github.com/openshift/api/machineconfiguration/v1"
	secv1 "github.com/openshift/api/security/v1"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	peerpod "github.com/confidential-containers/cloud-api-adaptor/src/peerpod-ctrl/api/v1alpha1"
	ccov1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	"github.com/openshift/sandboxed-containers-operator/controllers"
	// +kubebuilder:scaffold:imports
)

const (
	OperatorNamespace = "openshift-sandboxed-containers-operator"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(nodeapi.AddToScheme(scheme))

	utilruntime.Must(secv1.AddToScheme(scheme))

	utilruntime.Must(mcfgapi.Install(scheme))

	utilruntime.Must(kataconfigurationv1.AddToScheme(scheme))

	utilruntime.Must(peerpod.AddToScheme(scheme))

	utilruntime.Must(configv1.AddToScheme(scheme))

	utilruntime.Must(ccov1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func SetTimeEncoderToRfc3339() zap.Opts {
	return func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339TimeEncoder
	}
}

func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "", "The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), SetTimeEncoderToRfc3339()))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher
	webhookTLSOpts := tlsOpts

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		// TODO(user): TLSOpts is used to allow configuring the TLS config used for the server. If certificates are
		// not provided, self-signed certificates will be generated by default. This option is not recommended for
		// production environments as self-signed certificates do not offer the same level of trust and security
		// as certificates issued by a trusted Certificate Authority (CA). The primary risk is potentially allowing
		// unauthorized access to sensitive metrics data. Consider replacing with CertDir, CertName, and KeyName
		// to provide certificates, ensuring the server communicates using trusted and secure certificates.
		TLSOpts: tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize metrics certificate watcher")
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer: &webhook.DefaultServer{
			Options: webhook.Options{
				Port:    9443,
				TLSOpts: webhookTLSOpts,
			},
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "290f4947.kataconfiguration.openshift.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	isOpenshift, err := controllers.IsOpenShift()
	if err != nil {
		setupLog.Error(err, "unable to use discovery client")
		os.Exit(1)
	}

	if isOpenshift {

		err = fixScc(context.TODO(), mgr)
		if err != nil {
			setupLog.Error(err, "unable to create SCC")
			os.Exit(1)
		}

		err = labelNamespace(context.TODO(), mgr)
		if err != nil {
			setupLog.Error(err, "unable to add labels to namespace")
			os.Exit(1)
		}

		setupLog.Info("added labels")

		if err = (&controllers.KataConfigOpenShiftReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("KataConfig"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create KataConfig controller for OpenShift cluster", "controller", "KataConfig")
			os.Exit(1)
		}

		if err = (&peerpodcontrollers.PeerPodReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			// setting an empty array will delegate Provider creation to reconcile time, make sure RBAC permits:
			//+kubebuilder:rbac:groups="",resourceNames=peer-pods-cm;peer-pods-secret,resources=configmaps;secrets,verbs=get
			Providers: map[string]provider.Provider{},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create peerpod resources controller", "controller", "PeerPod")
			os.Exit(1)
		}

	}

	if err = (&kataconfigurationv1.KataConfig{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "KataConfig")
		os.Exit(1)
	}

	if err = (&controllers.SecretReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("Credentials"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Credentials")
		os.Exit(1)
	}

	if err = (&controllers.RuntimeClassReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("RuntimeClass"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RuntimeClass")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "Unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "Unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func fixScc(ctx context.Context, mgr manager.Manager) error {

	scc := controllers.GetScc()
	err := mgr.GetAPIReader().Get(ctx, client.ObjectKeyFromObject(scc), scc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Nothing to do.
			err = nil
		}
	} else if scc.SELinuxContext.Type == secv1.SELinuxStrategyMustRunAs {
		// A 1.2-style SCC breaks the MCO. This was fixed by
		// commit d4745883e38f, i.e. OSC >= 1.3 doesn't create
		// broken SCC anymore, but an existing instance still
		// needs to be fixed.
		setupLog.Info("Fixing SCC")
		scc.SELinuxContext = secv1.SELinuxContextStrategyOptions{
			Type: secv1.SELinuxStrategyRunAsAny,
		}
		err = mgr.GetClient().Update(ctx, scc)
	}

	return err
}

func labelNamespace(ctx context.Context, mgr manager.Manager) error {

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: OperatorNamespace,
		},
	}
	err := mgr.GetAPIReader().Get(ctx, client.ObjectKeyFromObject(ns), ns)
	if err != nil {
		setupLog.Error(err, "Unable to add label to the namespace")
		return err
	}

	setupLog.Info("Labelling Namespace")
	setupLog.Info("Labels: ", "Labels", ns.ObjectMeta.Labels)
	// Add namespace label to align with newly introduced Pod Security Admission controller
	ns.ObjectMeta.Labels["openshift.io/cluster-monitoring"] = "true"
	ns.ObjectMeta.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
	ns.ObjectMeta.Labels["pod-security.kubernetes.io/audit"] = "privileged"
	ns.ObjectMeta.Labels["pod-security.kubernetes.io/warn"] = "privileged"

	return mgr.GetClient().Update(ctx, ns)
}
