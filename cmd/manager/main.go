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

	provider "github.com/confidential-containers/cloud-api-adaptor/src/cloud-providers"
	peerpodcontrollers "github.com/confidential-containers/cloud-api-adaptor/src/peerpod-ctrl/controllers"
	configv1 "github.com/openshift/api/config/v1"
	mcfgapi "github.com/openshift/api/machineconfiguration/v1"
	secv1 "github.com/openshift/api/security/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
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
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), SetTimeEncoderToRfc3339()))

	// Cancellable context: SecurityProfileWatcher cancels it when the TLS profile changes,
	// triggering a graceful shutdown so the operator restarts with the new profile.
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	cfg := ctrl.GetConfigOrDie()

	isOpenshift, err := controllers.IsOpenShift()
	if err != nil {
		setupLog.Error(err, "unable to use discovery client")
		os.Exit(1)
	}

	// Temporary client used only to fetch the initial TLS profile before the manager starts.
	tempClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "unable to create temporary client")
		os.Exit(1)
	}

	// On non-OpenShift clusters the config.openshift.io/APIServer CRD is absent, so skip
	// the fetch and leave tlsOptsList empty to use controller-runtime's TLS defaults.
	var (
		tlsProfileSpec configv1.TLSProfileSpec
		tlsOptsList    []func(*tls.Config)
	)
	if isOpenshift {
		tlsProfileSpec, err = openshifttls.FetchAPIServerTLSProfile(ctx, tempClient)
		if err != nil {
			setupLog.Error(err, "unable to fetch TLS profile from APIServer CR")
			os.Exit(1)
		}
		tlsOpt, unsupportedCiphers := openshifttls.NewTLSConfigFromProfile(tlsProfileSpec)
		if len(unsupportedCiphers) > 0 {
			setupLog.Info("TLS profile contains ciphers not supported by Go crypto/tls, ignoring them", "ciphers", unsupportedCiphers)
		}
		tlsOptsList = []func(*tls.Config){tlsOpt}
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:    metricsAddr,
			SecureServing:  true,
			FilterProvider: filters.WithAuthenticationAndAuthorization,
			TLSOpts:        tlsOptsList,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			TLSOpts: tlsOptsList,
		}),
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: "290f4947.kataconfiguration.openshift.io",
		// The controller runtime can end up caching all objects in the cluster and cause OOM.
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					// Restrict caching to namespaces where we access secrets
					Namespaces: map[string]cache.Config{
						controllers.OperatorNamespace: {},
						"openshift-config":            {}, // for global pull-secrets
					},
				},
				&corev1.ConfigMap{}: {
					// Same for config maps
					Namespaces: map[string]cache.Config{
						controllers.OperatorNamespace:           {},
						controllers.DashboardConfigMapNamespace: {},
					},
				},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if isOpenshift {
		// Watch APIServer CR for TLS profile changes; graceful shutdown triggers a restart
		// so the new profile is applied to all connections.
		watcher := &openshifttls.SecurityProfileWatcher{
			Client:                mgr.GetClient(),
			InitialTLSProfileSpec: tlsProfileSpec,
			OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
				setupLog.Info("TLS profile changed, triggering graceful restart")
				cancel()
			},
		}
		if err := watcher.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to set up TLS profile watcher")
			os.Exit(1)
		}

		err = fixScc(ctx, mgr)
		if err != nil {
			setupLog.Error(err, "unable to create SCC")
			os.Exit(1)
		}

		err = labelNamespace(ctx, mgr)
		if err != nil {
			setupLog.Error(err, "unable to add labels to namespace")
			os.Exit(1)
		}

		setupLog.Info("added labels")

		if err = (&controllers.KataConfigOpenShiftReconciler{
			Client:         mgr.GetClient(),
			Log:            ctrl.Log.WithName("controllers").WithName("KataConfig"),
			Scheme:         mgr.GetScheme(),
			TLSProfileSpec: tlsProfileSpec,
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

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
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
