package kataconfig

import (
	"bytes"
	"context"
	"fmt"

	b64 "encoding/base64"

	"github.com/BurntSushi/toml"

	ignTypes "github.com/coreos/ignition/config/v2_2/types"
	kataconfigurationv1alpha1 "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"

	nodeapi "k8s.io/api/node/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_kataconfig")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new KataConfig Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileKataConfig{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
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

	// TODO - Need to test this on vanilla kubernetes
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

	err = c.Watch(&source.Kind{Type: &nodeapi.RuntimeClass{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &kataconfigurationv1alpha1.KataConfig{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileKataConfig implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileKataConfig{}

// ReconcileKataConfig reconciles a KataConfig object
type ReconcileKataConfig struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme

	isOpenShift *bool
}

// Reconcile reads that state of the cluster for a KataConfig object and makes changes based on the state read
// and what is in the KataConfig.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileKataConfig) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling KataConfig")

	if r.isOpenShift == nil {
		i, err := isOpenShift()
		if err != nil {
			return reconcile.Result{}, err
		}
		r.isOpenShift = &i
	}

	// Fetch the KataConfig instance
	instance := &kataconfigurationv1alpha1.KataConfig{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	ds := processDaemonsetForCR(instance, "install")
	// Set KataConfig instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, ds, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	foundDs := &appsv1.DaemonSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, foundDs)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
		err = r.client.Create(context.TODO(), ds)
		if err != nil {
			return reconcile.Result{}, err
		}

		instance.Status.FailedNodes = []kataconfigurationv1alpha1.FailedNode{}
		if *r.isOpenShift {
			instance.Status.RuntimeClass = "kata-oc"
		} else {
			instance.Status.RuntimeClass = "kata-runtime"
		}

		instance.Status.KataImage = "quay.io/kata-operator/kata-artifacts:1.0"

		nodesList := &corev1.NodeList{}
		var workerNodeLabels map[string]string

		if instance.Spec.KataConfigPoolSelector != nil {
			workerNodeLabels = instance.Spec.KataConfigPoolSelector.MatchLabels
		} else {
			workerNodeLabels = map[string]string{"node-role.kubernetes.io/worker": ""}
		}

		listOpts := []client.ListOption{
			client.MatchingLabels(workerNodeLabels),
		}
		err = r.client.List(context.TODO(), nodesList, listOpts...)
		if err != nil {
			return reconcile.Result{}, err
		}
		instance.Status.TotalNodesCount = len(nodesList.Items)

		err = r.client.Status().Update(context.TODO(), instance)
		if err != nil {
			return reconcile.Result{}, err
		}

		// DS created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	if instance.Status.CompletedNodesCount == instance.Status.TotalNodesCount && instance.Status.TotalNodesCount != 0 {
		// Create runtime class object only after all the targetted nodes have completed kata installation including crio drop in config

		rc := newRuntimeClassForCR(instance)

		// Set Kataconfig instance as the owner and controller
		if err := controllerutil.SetControllerReference(instance, rc, r.scheme); err != nil {
			return reconcile.Result{}, err
		}

		foundRc := &nodeapi.RuntimeClass{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: rc.Name}, foundRc)
		if err != nil && errors.IsNotFound(err) {
			reqLogger.Info("Creating a new RuntimeClass", "rc.Name", rc.Name)
			err = r.client.Create(context.TODO(), rc)
			if err != nil {
				return reconcile.Result{}, err
			}

			// RuntimeClass created successfully - don't requeue
			return reconcile.Result{}, nil
		} else if err != nil {
			return reconcile.Result{}, err
		}

	}

	if *r.isOpenShift && instance.Status.TotalNodesCount != 0 && instance.Status.CompletedDaemons == instance.Status.TotalNodesCount {
		// Kata installation is complete on targetted nodes, now let's drop in crio config using MCO

		reqLogger.Info("Kata installation on the cluster is completed")

		mcp := newMCPforCR(instance)
		// Set Kataconfig instance as the owner and controller
		if err := controllerutil.SetControllerReference(instance, mcp, r.scheme); err != nil {
			return reconcile.Result{}, err
		}

		founcMcp := &mcfgv1.MachineConfigPool{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: mcp.Name}, founcMcp)
		if err != nil && errors.IsNotFound(err) {
			reqLogger.Info("Creating a new Machine Config Pool ", "mcp.Name", mcp.Name)
			err = r.client.Create(context.TODO(), mcp)
			if err != nil {
				return reconcile.Result{}, err
			}

			mc, err := newMCForCR(instance)
			if err != nil {
				return reconcile.Result{}, err
			}

			// Set Kataconfig instance as the owner and controller
			// TODO - this might be incorrect, maybe the owner should be mcp and not kataconfig.
			// if err := controllerutil.SetControllerReference(instance, mc, r.scheme); err != nil {
			// 	return reconcile.Result{}, err
			// }

			foundMc := &mcfgv1.MachineConfig{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, foundMc)
			if err != nil && errors.IsNotFound(err) {
				reqLogger.Info("Creating a new Machine Config ", "mc.Name", mc.Name)
				err = r.client.Create(context.TODO(), mc)
				if err != nil {
					return reconcile.Result{}, err
				}

				// mc created successfully - don't requeue
				return reconcile.Result{}, nil
			} else if err != nil {
				return reconcile.Result{}, err
			}
			// mcp created successfully - don't requeue
			return reconcile.Result{}, nil
		} else if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	reqLogger.Info("Skip reconcile: DS already exists", "DS.Namespace", foundDs.Namespace, "DS.Name", foundDs.Name)
	return reconcile.Result{}, nil
}

func processDaemonsetForCR(cr *kataconfigurationv1alpha1.KataConfig, operation string) *appsv1.DaemonSet {
	runPrivileged := true
	var runAsUser int64 = 0

	labels := map[string]string{
		"name": "kata-install-daemon",
	}

	var nodeSelector map[string]string
	if cr.Spec.KataConfigPoolSelector != nil {
		nodeSelector = cr.Spec.KataConfigPoolSelector.MatchLabels
	} else {
		nodeSelector = map[string]string{
			"node-role.kubernetes.io/worker": "",
		}
	}

	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kata-install-daemon",
			Namespace: "kata-operator",
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "kata-operator",
					NodeSelector:       nodeSelector,
					Containers: []corev1.Container{
						{
							Name:            "kata-install-pod",
							Image:           "quay.io/harpatil/kata-install-daemon:1.5",
							ImagePullPolicy: "Always",
							SecurityContext: &corev1.SecurityContext{
								Privileged: &runPrivileged,
								RunAsUser:  &runAsUser,
							},
							Command: []string{"/bin/sh", "-c", fmt.Sprintf("/daemon --resource %s --operation %s", cr.Name, operation)},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "hostroot",
									MountPath: "/host",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "hostroot", // Has to match VolumeMounts in containers
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/",
									//Type: &corev1.HostPathVolumeSource,
								},
							},
						},
					},
				},
			},
		},
	}
}

func newRuntimeClassForCR(cr *kataconfigurationv1alpha1.KataConfig) *nodeapi.RuntimeClass {
	rc := &nodeapi.RuntimeClass{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "node.k8s.io/v1beta1",
			Kind:       "RuntimeClass",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: cr.Status.RuntimeClass,
		},
		Handler: cr.Status.RuntimeClass,
	}

	if cr.Spec.KataConfigPoolSelector != nil {
		rc.Scheduling = &nodeapi.Scheduling{
			NodeSelector: cr.Spec.KataConfigPoolSelector.MatchLabels,
		}
	}

	return rc
}

func newMCPforCR(cr *kataconfigurationv1alpha1.KataConfig) *mcfgv1.MachineConfigPool {
	lsr := metav1.LabelSelectorRequirement{
		Key:      "machineconfiguration.openshift.io/role",
		Operator: metav1.LabelSelectorOpIn,
		Values:   []string{"kata-oc", "worker"},
	}

	var nodeSelector *metav1.LabelSelector

	if cr.Spec.KataConfigPoolSelector != nil {
		nodeSelector = cr.Spec.KataConfigPoolSelector
	} else {
		nodeSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"node-role.kubernetes.io/worker": "",
			},
		}
	}

	mcp := &mcfgv1.MachineConfigPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfigPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "kata-oc",
		},
		Spec: mcfgv1.MachineConfigPoolSpec{
			MachineConfigSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{lsr},
			},
			NodeSelector: nodeSelector,
		},
	}

	return mcp
}

func newMCForCR(cr *kataconfigurationv1alpha1.KataConfig) (*mcfgv1.MachineConfig, error) {

	mc := *&mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "50-kata-crio-dropin",
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": "kata-oc",
				"app":                                    cr.Name,
			},
			Namespace: "kata-operator",
		},
		Spec: mcfgv1.MachineConfigSpec{
			Config: ignTypes.Config{
				Ignition: ignTypes.Ignition{
					Version: "2.2.0",
				},
			},
		},
	}

	file := ignTypes.File{}
	c := ignTypes.FileContents{}

	dropinConf, err := generateDropinConfig(cr.Status.RuntimeClass)
	if err != nil {
		return nil, err
	}

	c.Source = "data:text/plain;charset=utf-8;base64," + dropinConf
	file.Contents = c
	file.Filesystem = "root"
	m := 420
	file.Mode = &m
	file.Path = "/opt/kata-1.conf"

	mc.Spec.Config.Storage.Files = []ignTypes.File{file}

	return &mc, nil
}

func generateDropinConfig(handlerName string) (string, error) {

	type RuntimeHandler struct {
		RuntimePath                  string `toml:"runtime_path"`
		RuntimeType                  string `toml:"runtime_type,omitempty"`
		RuntimeRoot                  string `toml:"runtime_root,omitempty"`
		PrivilegedWithoutHostDevices bool   `toml:"privileged_without_host_devices,omitempty"`
	}

	type Runtimes map[string]*RuntimeHandler

	kataHandler := &RuntimeHandler{
		RuntimePath: "/usr/bin/kata-runtime",
		RuntimeType: "vm",
	}

	runcHandler := &RuntimeHandler{
		RuntimePath: "/bin/runc",
		RuntimeType: "oci",
		RuntimeRoot: "/run/runc",
	}

	var r Runtimes

	r = Runtimes{
		"crio.runtime.runtimes.runc":           runcHandler,
		"crio.runtime.runtimes." + handlerName: kataHandler,
	}

	var err error
	buf := new(bytes.Buffer)
	if err = toml.NewEncoder(buf).Encode(r); err != nil {
		return "", err
	}

	sEnc := b64.StdEncoding.EncodeToString([]byte(buf.String()))
	return sEnc, err

}

func isOpenShift() (bool, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return false, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false, err
	}

	gv := metav1.GroupVersion{Group: "config.openshift.io", Version: "v1"}.String()

	apiGroup, err := discoveryClient.ServerResourcesForGroupVersion(gv)
	if err != nil {
		return false, err
	}

	for _, group := range apiGroup.APIResources {
		if group.Kind == "ClusterVersion" {
			return true, nil
		}
	}

	return false, nil
}
