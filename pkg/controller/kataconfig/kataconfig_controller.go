package kataconfig

import (
	"context"

	kataconfigurationv1alpha1 "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	nodeapi "k8s.io/api/node/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_kataconfig")

// DaemonOperation represents the operation kata daemon is going to perform
type DaemonOperation string

const (
	// InstallOperation denotes kata installation operation
	InstallOperation DaemonOperation = "install"

	// UninstallOperation denotes kata uninstallation operation
	UninstallOperation DaemonOperation = "uninstall"

	// UpgradeOperation denotes kata upgrade operation
	UpgradeOperation DaemonOperation = "upgrade"

	kataConfigFinalizer = "finalizer.kataconfiguration.openshift.io"
)

// Add creates a new KataConfig Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	isOpenshift, err := IsOpenShift()
	if err != nil {
		return err
	}

	rc, err := func(mgr manager.Manager) (reconcile.Reconciler, error) {
		if isOpenshift {
			return &ReconcileKataConfigOpenShift{client: mgr.GetClient(), scheme: mgr.GetScheme()}, nil
		}
		return &ReconcileKataConfigKubernetes{client: mgr.GetClient(), scheme: mgr.GetScheme()}, nil
	}(mgr)

	if err != nil {
		return err
	}

	// add adds a new Controller to mgr with r as the reconcile.Reconciler
	return func(mgr manager.Manager, r reconcile.Reconciler) error {
		// Create a new controller
		c, err := controller.New("kataconfig-controller", mgr, controller.Options{Reconciler: r})
		if err != nil {
			return err
		}

		// Watch for changes to primary resource KataConfig
		err = c.Watch(&source.Kind{Type: &kataconfigurationv1alpha1.KataConfig{}}, &handler.EnqueueRequestForObject{})
		if err != nil {
			return err
		}

		// Watch for changes to secondary resource Pods and requeue the owner KataConfig
		err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &kataconfigurationv1alpha1.KataConfig{},
		})
		if err != nil {
			return err
		}

		if isOpenshift {
			err = c.Watch(&source.Kind{Type: &mcfgv1.MachineConfig{}}, &handler.EnqueueRequestForOwner{
				IsController: true,
				OwnerType:    &kataconfigurationv1alpha1.KataConfig{},
			})
			if err != nil {
				return err
			}

			err = c.Watch(&source.Kind{Type: &mcfgv1.MachineConfigPool{}}, &handler.EnqueueRequestForOwner{
				IsController: true,
				OwnerType:    &kataconfigurationv1alpha1.KataConfig{},
			})
			if err != nil {
				return err
			}
		} else {
			err = c.Watch(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestsFromMapFunc{
				ToRequests: handler.ToRequestsFunc(func(kataConfigObj handler.MapObject) []reconcile.Request {
					kataConfigList := &kataconfigurationv1alpha1.KataConfigList{}
					client := mgr.GetClient()

					err = client.List(context.TODO(), kataConfigList)
					if err != nil {
						return []reconcile.Request{}
					}

					reconcileRequests := make([]reconcile.Request, len(kataConfigList.Items))
					for _, kataconfig := range kataConfigList.Items {
						reconcileRequests = append(reconcileRequests, reconcile.Request{
							NamespacedName: types.NamespacedName{
								Name: kataconfig.Name,
							},
						})
					}
					return reconcileRequests
				}),
			})
			if err != nil {
				return err
			}
		}

		err = c.Watch(&source.Kind{Type: &nodeapi.RuntimeClass{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &kataconfigurationv1alpha1.KataConfig{},
		})
		if err != nil {
			return err
		}

		return nil
	}(mgr, rc)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func getClientSet() (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

// IsOpenShift detects if we are running in OpenShift using the discovery client
func IsOpenShift() (bool, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return false, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false, err
	}

	// Get a list of all API's on the cluster
	apiGroup, _, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		return false, err
	}

	for i := 0; i < len(apiGroup); i++ {
		if apiGroup[i].Name == "config.openshift.io" {
			return true, nil
		}
	}

	return false, nil
}
