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
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	kataconfigurationv1 "github.com/openshift/kata-operator/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// var _ reconcile.Reconciler = &KataConfigKubernetesReconciler{}

// KataConfigKubernetesReconciler reconciles a KataConfig object in Kubernetes cluster
type KataConfigKubernetesReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	clientset  kubernetes.Interface
	kataConfig *kataconfigurationv1.KataConfig
}

func (r *KataConfigKubernetesReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("kataconfig", req.NamespacedName)
	r.Log.Info("Reconciling KataConfig in Kubernetes Cluster")

	// Fetch the KataConfig instance
	r.kataConfig = &kataconfigurationv1.KataConfig{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, r.kataConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Check if the KataConfig instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	if r.kataConfig.GetDeletionTimestamp() != nil {
		return r.processKataConfigDeleteRequest()
	}

	return r.processKataConfigInstallRequest()
}

func (r *KataConfigKubernetesReconciler) processKataConfigDeleteRequest() (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *KataConfigKubernetesReconciler) processKataConfigInstallRequest() (ctrl.Result, error) {
	if r.kataConfig.Status.TotalNodesCount == 0 {

		nodesList := &corev1.NodeList{}

		if r.kataConfig.Spec.KataConfigPoolSelector == nil {
			r.kataConfig.Spec.KataConfigPoolSelector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"node-role.kubernetes.io/worker": ""},
			}
		}

		listOpts := []client.ListOption{
			client.MatchingLabels(r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels),
		}

		err := r.Client.List(context.TODO(), nodesList, listOpts...)
		if err != nil {
			return ctrl.Result{}, err
		}
		r.kataConfig.Status.TotalNodesCount = len(nodesList.Items)

		if r.kataConfig.Status.TotalNodesCount == 0 {
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second},
				fmt.Errorf("No suitable worker nodes found for kata installation. Please make sure to label the nodes with labels specified in KataConfigPoolSelector")
		}

		if r.kataConfig.Spec.Config.SourceImage == "" {
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second},
				fmt.Errorf("SourceImage must be specified to download the kata binaries")
		}

		if r.kataConfig.Status.KataImage == "" {
			// TODO - placeholder. This will change in future.
			r.kataConfig.Status.KataImage = r.kataConfig.Spec.Config.SourceImage
		}

		err = r.Client.Status().Update(context.TODO(), r.kataConfig)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Don't create the daemonset if kata is already installed on the cluster nodes
	if r.kataConfig.Status.TotalNodesCount > 0 &&
		r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount != r.kataConfig.Status.TotalNodesCount {
		ds := r.processDaemonset(InstallOperation)
		// Set KataConfig instance as the owner and controller
		if err := controllerutil.SetControllerReference(r.kataConfig, ds, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		foundDs := &appsv1.DaemonSet{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, foundDs)
		if err != nil && errors.IsNotFound(err) {
			r.Log.Info("Creating a new installation Daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
			err = r.Client.Create(context.TODO(), ds)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else if err != nil {
			return ctrl.Result{}, err
		}

		return r.monitorKataConfigInstallation()
	}

	// Add finalizer for this CR
	// if !contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
	// 	if err := r.addFinalizer(); err != nil {
	// 		return ctrl.Result{}, err
	// 	}
	// }

	return ctrl.Result{}, nil
}

func (r *KataConfigKubernetesReconciler) monitorKataConfigInstallation() (ctrl.Result, error) {
	// If the installation of the binaries is successful on all nodes, proceed with creating the runtime classes
	if r.kataConfig.Status.TotalNodesCount > 0 && r.kataConfig.Status.InstallationStatus.InProgress.InProgressNodesCount == r.kataConfig.Status.TotalNodesCount {
		rs, err := r.setRuntimeClass()
		if err != nil {
			return rs, err
		}

		r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesList = r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList
		r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount = len(r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesList)
		r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList = []string{}
		r.kataConfig.Status.InstallationStatus.InProgress.InProgressNodesCount = 0

		err = r.Client.Status().Update(context.TODO(), r.kataConfig)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	nodesList := &corev1.NodeList{}

	if r.kataConfig.Spec.KataConfigPoolSelector == nil {
		r.kataConfig.Spec.KataConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"node-role.kubernetes.io/worker": ""},
		}
	}

	listOpts := []client.ListOption{
		client.MatchingLabels(r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels),
	}

	err := r.Client.List(context.TODO(), nodesList, listOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, node := range nodesList.Items {
		if !contains(r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList, node.Name) {
			for k, v := range node.GetLabels() {
				if k == "katacontainers.io/kata-runtime" && v == "true" {
					r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList = append(r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList, node.Name)
					r.kataConfig.Status.InstallationStatus.InProgress.InProgressNodesCount++

					err = r.Client.Status().Update(context.TODO(), r.kataConfig)
					if err != nil {
						return ctrl.Result{}, err
					}
				}
			}
		}
		if r.kataConfig.Status.InstallationStatus.InProgress.InProgressNodesCount == r.kataConfig.Status.TotalNodesCount {
			return ctrl.Result{Requeue: true}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *KataConfigKubernetesReconciler) setRuntimeClass() (ctrl.Result, error) {
	runtimeClassNames := []string{"kata-qemu-virtiofs", "kata-qemu", "kata-clh", "kata-fc", "kata"}

	for _, runtimeClassName := range runtimeClassNames {
		rc := func() *nodeapi.RuntimeClass {
			rc := &nodeapi.RuntimeClass{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "node.k8s.io/v1beta1",
					Kind:       "RuntimeClass",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: runtimeClassName,
				},
				Handler: runtimeClassName,
			}

			if r.kataConfig.Spec.KataConfigPoolSelector != nil {
				rc.Scheduling = &nodeapi.Scheduling{
					NodeSelector: r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels,
				}
			}
			return rc
		}()

		// Set Kataconfig r.kataConfig as the owner and controller
		if err := controllerutil.SetControllerReference(r.kataConfig, rc, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		foundRc := &nodeapi.RuntimeClass{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: rc.Name}, foundRc)
		if err != nil && errors.IsNotFound(err) {
			r.Log.Info("Creating a new RuntimeClass", "rc.Name", rc.Name)
			err = r.Client.Create(context.TODO(), rc)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

	}

	r.kataConfig.Status.RuntimeClass = strings.Join(runtimeClassNames, ",")
	err := r.Client.Status().Update(context.TODO(), r.kataConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *KataConfigKubernetesReconciler) processDaemonset(operation DaemonOperation) *appsv1.DaemonSet {
	runPrivileged := true
	var runAsUser int64 = 0
	hostPt := corev1.HostPathType("DirectoryOrCreate")

	dsName := "kata-operator-daemon-" + string(operation)
	labels := map[string]string{
		"name": dsName,
	}

	var nodeSelector map[string]string
	if r.kataConfig.Spec.KataConfigPoolSelector != nil {
		nodeSelector = r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels
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
			Name:      dsName,
			Namespace: "kata-operator",
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: "RollingUpdate",
				RollingUpdate: &appsv1.RollingUpdateDaemonSet{
					MaxUnavailable: &intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 1,
					},
				},
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
							Image:           r.kataConfig.Status.KataImage,
							ImagePullPolicy: "Always",
							Lifecycle: &corev1.Lifecycle{
								PreStop: &corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{"bash", "-c", "/opt/kata-artifacts/scripts/kata-deploy.sh cleanup"},
									},
								},
							},
							SecurityContext: &corev1.SecurityContext{
								// TODO - do we really need to run as root?
								Privileged: &runPrivileged,
								RunAsUser:  &runAsUser,
							},
							Command: []string{"bash", "-c", "/opt/kata-artifacts/scripts/kata-deploy.sh install"},
							Env: []corev1.EnvVar{
								{
									Name: "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "crio-conf",
									MountPath: "/etc/crio/",
								},
								{
									Name:      "containerd-conf",
									MountPath: "/etc/containerd/",
								},
								{
									Name:      "kata-artifacts",
									MountPath: "/opt/kata/",
								},
								{
									Name:      "dbus",
									MountPath: "/var/run/dbus",
								},
								{
									Name:      "systemd",
									MountPath: "/run/systemd",
								},
								{
									Name:      "local-bin",
									MountPath: "/usr/local/bin/",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "crio-conf",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/etc/crio/",
								},
							},
						},
						{
							Name: "containerd-conf",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/etc/containerd/",
								},
							},
						},
						{
							Name: "kata-artifacts",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/opt/kata/",
									Type: &hostPt,
								},
							},
						},
						{
							Name: "dbus",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/run/dbus",
								},
							},
						},
						{
							Name: "systemd",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/run/systemd",
								},
							},
						},
						{
							Name: "local-bin",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/usr/local/bin/",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *KataConfigKubernetesReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kataconfigurationv1.KataConfig{}).
		Owns(&appsv1.DaemonSet{}).
		Watches(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestsFromMapFunc{
			ToRequests: handler.ToRequestsFunc(func(kataConfigObj handler.MapObject) []reconcile.Request {
				kataConfigList := &kataconfigurationv1.KataConfigList{}
				client := mgr.GetClient()

				err := client.List(context.TODO(), kataConfigList)
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
		}).
		Complete(r)
}
