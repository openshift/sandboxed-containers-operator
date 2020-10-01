package kataconfig

import (
	"bytes"
	"context"
	b64 "encoding/base64"
	"fmt"
	"text/template"
	"time"

	ignTypes "github.com/coreos/ignition/config/v2_2/types"
	"github.com/go-logr/logr"
	kataconfigurationv1alpha1 "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// blank assignment to verify that ReconcileKataConfigOpenShift implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileKataConfigOpenShift{}

// ReconcileKataConfigOpenShift reconciles a KataConfig object
type ReconcileKataConfigOpenShift struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme

	clientset  kubernetes.Interface
	kataConfig *kataconfigurationv1alpha1.KataConfig
	kataLogger logr.Logger
}

// Reconcile reads that state of the cluster for a KataConfig object and makes changes based on the state read
// and what is in the KataConfig.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileKataConfigOpenShift) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	r.kataLogger = log.WithValues("Request.Name", request.Name)
	r.kataLogger.Info("Reconciling KataConfig in OpenShift Cluster")

	// Fetch the KataConfig instance
	r.kataConfig = &kataconfigurationv1alpha1.KataConfig{}
	err := r.client.Get(context.TODO(), request.NamespacedName, r.kataConfig)
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

	return func() (reconcile.Result, error) {
		// Check if the KataConfig instance is marked to be deleted, which is
		// indicated by the deletion timestamp being set.
		if r.kataConfig.GetDeletionTimestamp() != nil {
			return r.processKataConfigDeleteRequest()
		}

		// if we are using openshift then make sure that MCO related things are
		// handled only after kata binaries are installed on the nodes
		if r.kataConfig.Status.TotalNodesCount > 0 &&
			len(r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList) == r.kataConfig.Status.TotalNodesCount {
			return r.monitorKataConfigInstallation()
		}

		// Once all the nodes have installed kata binaries and configured the CRI runtime create the runtime class
		if r.kataConfig.Status.TotalNodesCount > 0 &&
			r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount == r.kataConfig.Status.TotalNodesCount &&
			r.kataConfig.Status.RuntimeClass == "" {

			err := r.deleteKataDaemonset(InstallOperation)
			if err != nil {
				return reconcile.Result{}, err
			}

			return r.setRuntimeClass()
		}
		// Intiate the installation of kata runtime on the nodes if it doesn't exist already
		return r.processKataConfigInstallRequest()
	}()
}

func (r *ReconcileKataConfigOpenShift) processDaemonsetForCR(operation DaemonOperation) *appsv1.DaemonSet {
	runPrivileged := true
	var runAsUser int64 = 0

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
							Image:           "quay.io/isolatedcontainers/kata-operator-daemon:4.6",
							ImagePullPolicy: "Always",
							SecurityContext: &corev1.SecurityContext{
								Privileged: &runPrivileged,
								RunAsUser:  &runAsUser,
							},
							Lifecycle: &corev1.Lifecycle{
								PreStop: &corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{"rm", "-rf", "/opt/kata-install", "/usr/local/kata/"},
									},
								},
							},
							Command: []string{"/bin/sh", "-c", fmt.Sprintf("/daemon --resource %s --operation %s", r.kataConfig.Name, operation)},
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
					HostNetwork: true,
					HostPID:     true,
				},
			},
		},
	}
}

func (r *ReconcileKataConfigOpenShift) newMCPforCR() *mcfgv1.MachineConfigPool {
	lsr := metav1.LabelSelectorRequirement{
		Key:      "machineconfiguration.openshift.io/role",
		Operator: metav1.LabelSelectorOpIn,
		Values:   []string{"kata-oc", "worker"},
	}

	var nodeSelector *metav1.LabelSelector

	if r.kataConfig.Spec.KataConfigPoolSelector != nil {
		nodeSelector = r.kataConfig.Spec.KataConfigPoolSelector
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

func (r *ReconcileKataConfigOpenShift) newMCForCR() (*mcfgv1.MachineConfig, error) {
	isenabled := true
	name := "kata-osbuilder-generate.service"
	content := `
[Unit]
Description=Hacky service to enable kata-osbuilder-generate.service
ConditionPathExists=/usr/lib/systemd/system/kata-osbuilder-generate.service
[Service]
Type=oneshot
ExecStart=/usr/libexec/kata-containers/osbuilder/kata-osbuilder.sh
ExecRestart=/usr/libexec/kata-containers/osbuilder/kata-osbuilder.sh
[Install]
WantedBy=multi-user.target
`

	var workerRole string

	if _, ok := r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels["node-role.kubernetes.io/worker"]; ok {
		workerRole = "worker"
	} else {
		workerRole = "kata-oc"
	}

	mc := *&mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "50-kata-crio-dropin",
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": workerRole,
				"app":                                    r.kataConfig.Name,
			},
			Namespace: "kata-operator",
		},
		Spec: mcfgv1.MachineConfigSpec{
			Config: ignTypes.Config{
				Ignition: ignTypes.Ignition{
					Version: "2.2.0",
				},
				Systemd: ignTypes.Systemd{
					Units: []ignTypes.Unit{
						{Name: name, Enabled: &isenabled, Contents: content},
					},
				},
			},
		},
	}

	file := ignTypes.File{}
	c := ignTypes.FileContents{}

	dropinConf, err := generateDropinConfig(r.kataConfig.Status.RuntimeClass)
	if err != nil {
		return nil, err
	}

	c.Source = "data:text/plain;charset=utf-8;base64," + dropinConf
	file.Contents = c
	file.Filesystem = "root"
	m := 420
	file.Mode = &m
	file.Path = "/etc/crio/crio.conf.d/50-kata.conf"

	mc.Spec.Config.Storage.Files = []ignTypes.File{file}

	return &mc, nil
}

func generateDropinConfig(handlerName string) (string, error) {
	var err error
	buf := new(bytes.Buffer)
	type RuntimeConfig struct {
		RuntimeName string
	}
	const b = `
[crio.runtime]
  manage_ns_lifecycle = true

[crio.runtime.runtimes.{{.RuntimeName}}]
  runtime_path = "/usr/bin/containerd-shim-kata-v2"
  runtime_type = "vm"
  runtime_root = "/run/vc"
  
[crio.runtime.runtimes.runc]
  runtime_path = ""
  runtime_type = "oci"
  runtime_root = "/run/runc"
`
	c := RuntimeConfig{RuntimeName: "kata"}
	t := template.Must(template.New("test").Parse(b))
	err = t.Execute(buf, c)
	if err != nil {
		return "", err
	}
	sEnc := b64.StdEncoding.EncodeToString([]byte(buf.String()))
	return sEnc, err
}

func (r *ReconcileKataConfigOpenShift) addFinalizer() error {
	r.kataLogger.Info("Adding Finalizer for the KataConfig")
	controllerutil.AddFinalizer(r.kataConfig, kataConfigFinalizer)

	// Update CR
	err := r.client.Update(context.TODO(), r.kataConfig)
	if err != nil {
		r.kataLogger.Error(err, "Failed to update KataConfig with finalizer")
		return err
	}
	return nil
}

func (r *ReconcileKataConfigOpenShift) listKataPods() error {
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(corev1.NamespaceAll),
	}
	if err := r.client.List(context.TODO(), podList, listOpts...); err != nil {
		return fmt.Errorf("Failed to list kata pods: %v", err)
	}
	for _, pod := range podList.Items {
		if pod.Spec.RuntimeClassName != nil {
			if *pod.Spec.RuntimeClassName == r.kataConfig.Status.RuntimeClass {
				return fmt.Errorf("Existing pods using Kata Runtime found. Please delete the pods manually for KataConfig deletion to proceed")
			}
		}
	}
	return nil
}

func (r *ReconcileKataConfigOpenShift) processKataConfigInstallRequest() (reconcile.Result, error) {
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

		err := r.client.List(context.TODO(), nodesList, listOpts...)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.kataConfig.Status.TotalNodesCount = len(nodesList.Items)

		if r.kataConfig.Status.TotalNodesCount == 0 {
			return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second},
				fmt.Errorf("No suitable worker nodes found for kata installation. Please make sure to label the nodes with labels specified in KataConfigPoolSelector")
		}

		err = r.client.Status().Update(context.TODO(), r.kataConfig)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	if r.kataConfig.Status.KataImage == "" {
		// TODO - placeholder. This will change in future.
		r.kataConfig.Status.KataImage = "quay.io/kata-operator/kata-artifacts:1.0"
	}

	// Don't create the daemonset if kata is already installed on the cluster nodes
	if r.kataConfig.Status.TotalNodesCount > 0 &&
		r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount != r.kataConfig.Status.TotalNodesCount {
		ds := r.processDaemonsetForCR(InstallOperation)
		// Set KataConfig instance as the owner and controller
		if err := controllerutil.SetControllerReference(r.kataConfig, ds, r.scheme); err != nil {
			return reconcile.Result{}, err
		}
		foundDs := &appsv1.DaemonSet{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, foundDs)
		if err != nil && errors.IsNotFound(err) {
			r.kataLogger.Info("Creating a new installation Daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
			err = r.client.Create(context.TODO(), ds)
			if err != nil {
				return reconcile.Result{}, err
			}
		} else if err != nil {
			return reconcile.Result{}, err
		}
	}

	// Add finalizer for this CR
	if !contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
		if err := r.addFinalizer(); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileKataConfigOpenShift) setRuntimeClass() (reconcile.Result, error) {
	runtimeClassName := "kata"

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
	if err := controllerutil.SetControllerReference(r.kataConfig, rc, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	foundRc := &nodeapi.RuntimeClass{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: rc.Name}, foundRc)
	if err != nil && errors.IsNotFound(err) {
		r.kataLogger.Info("Creating a new RuntimeClass", "rc.Name", rc.Name)
		err = r.client.Create(context.TODO(), rc)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	if r.kataConfig.Status.RuntimeClass == "" {
		r.kataConfig.Status.RuntimeClass = runtimeClassName
		err = r.client.Status().Update(context.TODO(), r.kataConfig)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileKataConfigOpenShift) processKataConfigDeleteRequest() (reconcile.Result, error) {
	r.kataLogger.Info("KataConfig deletion in progress: ")
	if contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
		// Get the list of pods that might be running using kata runtime
		err := r.listKataPods()
		if err != nil {
			return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}

		ds := r.processDaemonsetForCR(UninstallOperation)

		foundDs := &appsv1.DaemonSet{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, foundDs)
		if err != nil && errors.IsNotFound(err) {
			r.kataLogger.Info("Creating a new uninstallation Daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
			err = r.client.Create(context.TODO(), ds)
			if err != nil {
				return reconcile.Result{}, err
			}
		} else if err != nil {
			return reconcile.Result{}, err
		}

		if r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesCount != r.kataConfig.Status.TotalNodesCount {
			r.kataLogger.Info("KataConfig uninstallation: ", "Number of nodes completed uninstallation ",
				r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesCount,
				"Total number of kata installed nodes ", r.kataConfig.Status.TotalNodesCount)
			// TODO - we don't need this nil check if we know that pool is always initialized
			if r.kataConfig.Spec.KataConfigPoolSelector != nil &&
				r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels != nil && len(r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels) > 0 {
				if r.clientset == nil {
					r.clientset, err = getClientSet()
					if err != nil {
						return reconcile.Result{}, err
					}
				}

				for _, nodeName := range r.kataConfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList {
					if contains(r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesList, nodeName) {
						continue
					}

					if _, ok := r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels["node-role.kubernetes.io/worker"]; !ok {
						r.kataLogger.Info("Removing the kata pool selector label from the node", "node name ", nodeName)
						node, err := r.clientset.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
						if err != nil {
							return reconcile.Result{}, err
						}

						nodeLabels := node.GetLabels()

						for k := range r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels {
							delete(nodeLabels, k)
						}

						node.SetLabels(nodeLabels)
						_, err = r.clientset.CoreV1().Nodes().Update(node)

						if err != nil {
							return reconcile.Result{}, err
						}
					}
				}
			}
		}

		r.kataLogger.Info("Making sure parent MCP is synced properly")
		if _, ok := r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels["node-role.kubernetes.io/worker"]; ok {
			mc, err := r.newMCForCR()
			var isMcDeleted bool

			err = r.client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, mc)
			if err != nil && errors.IsNotFound(err) {
				isMcDeleted = true
			} else if err != nil {
				return reconcile.Result{}, err
			}

			if !isMcDeleted {
				err = r.client.Delete(context.TODO(), mc)
				if err != nil {
					// error during removing mc, don't block the uninstall. Just log the error and move on.
					r.kataLogger.Info("Error found deleting machine config. If the machine config exists after installation it can be safely deleted manually.",
						"mc", mc.Name, "error", err)
				}
				// Sleep for MCP to reflect the changes
				r.kataLogger.Info("Pausing for a minute to make sure worker mcp has started syncing up")
				time.Sleep(60 * time.Second)
			}

			workreMcp := &mcfgv1.MachineConfigPool{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: "worker"}, workreMcp)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.kataLogger.Info("Monitoring worker mcp", "worker mcp name", workreMcp.Name, "ready machines", workreMcp.Status.ReadyMachineCount,
				"total machines", workreMcp.Status.MachineCount)
			if workreMcp.Status.ReadyMachineCount != workreMcp.Status.MachineCount {
				return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
			}
		} else {
			// Sleep for MCP to reflect the changes
			if len(r.kataConfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList) > 0 {
				r.kataLogger.Info("Pausing for a minute to make sure parent mcp has started syncing up")
				time.Sleep(60 * time.Second)

				parentMcp := &mcfgv1.MachineConfigPool{}

				err := r.client.Get(context.TODO(), types.NamespacedName{Name: "worker"}, parentMcp)
				if err != nil && errors.IsNotFound(err) {
					return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, fmt.Errorf("Not able to find parent pool %s", parentMcp.GetName())
				} else if err != nil {
					return reconcile.Result{}, err
				}

				r.kataLogger.Info("Monitoring parent mcp", "parent mcp name", parentMcp.Name, "ready machines", parentMcp.Status.ReadyMachineCount,
					"total machines", parentMcp.Status.MachineCount)
				if parentMcp.Status.ReadyMachineCount != parentMcp.Status.MachineCount {
					return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
				}

				mcp := r.newMCPforCR()
				err = r.client.Delete(context.TODO(), mcp)
				if err != nil {
					// error during removing mcp, don't block the uninstall. Just log the error and move on.
					r.kataLogger.Info("Error found deleting mcp. If the mcp exists after installation it can be safely deleted manually.",
						"mcp", mcp.Name, "error", err)
				}

				mc, err := r.newMCForCR()
				err = r.client.Delete(context.TODO(), mc)
				if err != nil {
					// error during removing mc, don't block the uninstall. Just log the error and move on.
					r.kataLogger.Info("Error found deleting machine config. If the machine config exists after installation it can be safely deleted manually.",
						"mc", mc.Name, "error", err)
				}
			} else {
				return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
			}
		}

		for _, nodeName := range r.kataConfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList {
			if contains(r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesList, nodeName) {
				continue
			}

			r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesCount++
			r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesList = append(r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesList, nodeName)
			if r.kataConfig.Status.UnInstallationStatus.InProgress.InProgressNodesCount > 0 {
				r.kataConfig.Status.UnInstallationStatus.InProgress.InProgressNodesCount--
			}
		}

		err = r.client.Status().Update(context.TODO(), r.kataConfig)
		if err != nil {
			return reconcile.Result{}, err
		}

		r.kataLogger.Info("Deleting uninstall daemonset")
		err = r.deleteKataDaemonset(UninstallOperation)
		if err != nil {
			return reconcile.Result{}, err
		}

		r.kataLogger.Info("Uninstallation completed on all nodes. Proceeding with the KataConfig deletion")
		controllerutil.RemoveFinalizer(r.kataConfig, kataConfigFinalizer)
		err = r.client.Update(context.TODO(), r.kataConfig)
		if err != nil {
			return reconcile.Result{}, err
		}
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileKataConfigOpenShift) deleteKataDaemonset(operation DaemonOperation) error {

	ds := r.processDaemonsetForCR(operation)
	foundDs := &appsv1.DaemonSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, foundDs)
	if err != nil && errors.IsNotFound(err) {
		// DaemonSet not found, nothing to delete, ignore the request.
		return nil
	} else if err != nil {
		return err
	}

	err = r.client.Delete(context.TODO(), foundDs)
	if err != nil {
		return err
	}

	return nil
}

func (r *ReconcileKataConfigOpenShift) monitorKataConfigInstallation() (reconcile.Result, error) {
	r.kataLogger.Info("installation is complete on targetted nodes, now dropping in crio config using MCO")

	if _, ok := r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels["node-role.kubernetes.io/worker"]; !ok {
		mcp := r.newMCPforCR()

		founcMcp := &mcfgv1.MachineConfigPool{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: mcp.Name}, founcMcp)
		if err != nil && errors.IsNotFound(err) {
			r.kataLogger.Info("Creating a new Machine Config Pool ", "mcp.Name", mcp.Name)
			err = r.client.Create(context.TODO(), mcp)
			if err != nil {
				return reconcile.Result{}, err
			}
			// mcp created successfully - requeue to check the status later
			return reconcile.Result{Requeue: true, RequeueAfter: 20 * time.Second}, nil
		} else if err != nil {
			return reconcile.Result{}, err
		}

		// Wait till MCP is ready
		if founcMcp.Status.MachineCount == 0 {
			r.kataLogger.Info("Waiting till Machine Config Pool is initialized ", "mcp.Name", mcp.Name)
			return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
		}
		if founcMcp.Status.MachineCount != founcMcp.Status.ReadyMachineCount {
			r.kataLogger.Info("Waiting till Machine Config Pool is ready ", "mcp.Name", mcp.Name)
			return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
		}
	}

	mc, err := r.newMCForCR()
	if err != nil {
		return reconcile.Result{}, err
	}

	foundMc := &mcfgv1.MachineConfig{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, foundMc)
	if err != nil && errors.IsNotFound(err) {
		r.kataLogger.Info("Creating a new Machine Config ", "mc.Name", mc.Name)
		err = r.client.Create(context.TODO(), mc)
		if err != nil {
			return reconcile.Result{}, err
		}
		// mc created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
