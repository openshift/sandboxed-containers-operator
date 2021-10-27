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
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	operatorv1 "github.com/openshift/api/operator/v1"
	cov1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"

	ignTypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-logr/logr"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// blank assignment to verify that KataConfigOpenShiftReconciler implements reconcile.Reconciler
// var _ reconcile.Reconciler = &KataConfigOpenShiftReconciler{}

// KataConfigOpenShiftReconciler reconciles a KataConfig object
type KataConfigOpenShiftReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	clientset  kubernetes.Interface
	kataConfig *kataconfigurationv1.KataConfig
}

// +kubebuilder:rbac:groups=kataconfiguration.openshift.io,resources=kataconfigs;kataconfigs/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kataconfiguration.openshift.io,resources=kataconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;daemonsets;replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=daemonsets/finalizers,resourceNames=manager-role,verbs=update
// +kubebuilder:rbac:groups=node.k8s.io,resources=runtimeclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get
// +kubebuilder:rbac:groups="";machineconfiguration.openshift.io,resources=nodes;machineconfigs;machineconfigpools;pods;services;services/finalizers;endpoints;persistentvolumeclaims;events;configmaps;secrets,verbs=get;list;watch;create;update;patch;delete

func (r *KataConfigOpenShiftReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("kataconfig", req.NamespacedName)
	r.Log.Info("Reconciling KataConfig in OpenShift Cluster")

	// Fetch the KataConfig instance
	r.kataConfig = &kataconfigurationv1.KataConfig{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, r.kataConfig)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after ctrl request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		r.Log.Error(err, "Cannot retrieve kataConfig")
		return ctrl.Result{}, err
	}

	return func() (ctrl.Result, error) {
		// Check if the KataConfig instance is marked to be deleted, which is
		// indicated by the deletion timestamp being set.
		if r.kataConfig.GetDeletionTimestamp() != nil {
			res, err := r.processKataConfigDeleteRequest()
			updateErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
			if updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return res, err
		}

		res, err := r.processKataConfigInstallRequest()
		updateErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
		if updateErr != nil {
			return ctrl.Result{}, updateErr
		}

		return res, err
	}()
}

func (r *KataConfigOpenShiftReconciler) newMCPforCR() *mcfgv1.MachineConfigPool {
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

func (r *KataConfigOpenShiftReconciler) newMCForCR(machinePool string) (*mcfgv1.MachineConfig, error) {
	r.Log.Info("Creating MachineConfig for Custom Resource")
	kataOC, err := r.kataOcExists()
	if err != nil {
		return nil, err
	}

	if kataOC {
		machinePool = "kata-oc"
	} else if _, ok := r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels["node-role.kubernetes.io/"+machinePool]; !ok {
		r.Log.Error(err, "no valid role for MachineConfig found")
	}

	ic := ignTypes.Config{
		Ignition: ignTypes.Ignition{
			Version: "3.2.0",
		},
	}

	icb, err := json.Marshal(ic)
	if err != nil {
		return nil, err
	}

	mc := mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "50-enable-sandboxed-containers-extension",
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": machinePool,
				"app":                                    r.kataConfig.Name,
			},
			Namespace: "openshift-sandboxed-containers-operator",
		},
		Spec: mcfgv1.MachineConfigSpec{
			Extensions: []string{"sandboxed-containers"},
			Config: runtime.RawExtension{
				Raw: icb,
			},
		},
	}

	return &mc, nil
}

func (r *KataConfigOpenShiftReconciler) addFinalizer() error {
	r.Log.Info("Adding Finalizer for the KataConfig")
	controllerutil.AddFinalizer(r.kataConfig, kataConfigFinalizer)

	// Update CR
	err := r.Client.Update(context.TODO(), r.kataConfig)
	if err != nil {
		r.Log.Error(err, "Failed to update KataConfig with finalizer")
		return err
	}
	return nil
}

func (r *KataConfigOpenShiftReconciler) listKataPods() error {
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(corev1.NamespaceAll),
	}
	if err := r.Client.List(context.TODO(), podList, listOpts...); err != nil {
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

func (r *KataConfigOpenShiftReconciler) kataOcExists() (bool, error) {
	kataOcMcp := &mcfgv1.MachineConfigPool{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: "kata-oc"}, kataOcMcp)
	if err != nil && k8serrors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		r.Log.Error(err, "Could not get the kata-oc MachineConfigPool")
		return false, err
	}

	return true, nil
}

func (r *KataConfigOpenShiftReconciler) getMcpName() (string, error) {
	r.Log.Info("Getting MachineConfigPool Name")
	var mcpName string

	kataOC, err := r.kataOcExists()
	if kataOC && err == nil {
		r.Log.Info("kata-oc MachineConfigPool exists")
		return "kata-oc", nil
	}

	workerMcp := &mcfgv1.MachineConfigPool{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: "worker"}, workerMcp)
	if err != nil && k8serrors.IsNotFound(err) {
		r.Log.Error(err, "No worker MachineConfigPool found!")
		return "", err
	} else if err != nil {
		r.Log.Error(err, "Could not get the worker MachineConfigPool!")
		return "", err
	}

	if workerMcp.Status.MachineCount > 0 {
		mcpName = "worker"
	} else {
		mcpName = "master"
	}
	return mcpName, nil
}

func (r *KataConfigOpenShiftReconciler) setRuntimeClass() (ctrl.Result, error) {
	runtimeClassName := "kata"

	rc := func() *nodeapi.RuntimeClass {
		rc := &nodeapi.RuntimeClass{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "node.k8s.io/v1",
				Kind:       "RuntimeClass",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: runtimeClassName,
			},
			Handler: runtimeClassName,
			// Use same values for Pod Overhead as upstream kata-deploy using, see
			// https://github.com/kata-containers/packaging/blob/f17450317563b6e4d6b1a71f0559360b37783e19/kata-deploy/k8s-1.18/kata-runtimeClasses.yaml#L7
			Overhead: &nodeapi.Overhead{
				PodFixed: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("350Mi"),
				},
			},
		}

		if r.kataConfig.Spec.KataConfigPoolSelector != nil {
			r.Log.Info("KataConfigPoolSelector:", "r.kataConfig.Spec.KataConfigPoolSelector", r.kataConfig.Spec.KataConfigPoolSelector)
			nodeSelector, err := metav1.LabelSelectorAsMap(r.kataConfig.Spec.KataConfigPoolSelector)
			if err != nil {
				r.Log.Error(err, "Unable to get nodeSelector for runtimeClass")
			}
			rc.Scheduling = &nodeapi.Scheduling{
				NodeSelector: nodeSelector,
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
	if err != nil && k8serrors.IsNotFound(err) {
		r.Log.Info("Creating a new RuntimeClass", "rc.Name", rc.Name)
		err = r.Client.Create(context.TODO(), rc)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if r.kataConfig.Status.RuntimeClass == "" {
		r.kataConfig.Status.RuntimeClass = runtimeClassName
		err = r.Client.Status().Update(context.TODO(), r.kataConfig)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *KataConfigOpenShiftReconciler) setUpgradeableTrue() {
	cov1helpers.SetOperatorCondition(&r.kataConfig.Status.Conditions, operatorv1.OperatorCondition{
		Type:               "Upgradeable",
		Status:             operatorv1.ConditionStatus(metav1.ConditionTrue),
		LastTransitionTime: metav1.Time{},
		Reason:             "No operation ongoing",
		Message:            "Operator is upgradeable",
	})
}

func (r *KataConfigOpenShiftReconciler) setUpgradeableFalse(operation string) {
	cov1helpers.SetOperatorCondition(&r.kataConfig.Status.Conditions, operatorv1.OperatorCondition{
		Type:               "Upgradeable",
		Status:             operatorv1.ConditionStatus(metav1.ConditionFalse),
		LastTransitionTime: metav1.Time{},
		Reason:             "operation ongoing: " + operation,
		Message:            "Operator is not upgradeable",
	})
}

func (r *KataConfigOpenShiftReconciler) processKataConfigDeleteRequest() (ctrl.Result, error) {
	r.Log.Info("KataConfig deletion in progress: ")
	machinePool, err := r.getMcpName()
	if err != nil {
		return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	foundMcp := &mcfgv1.MachineConfigPool{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: machinePool}, foundMcp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
		// Get the list of pods that might be running using kata runtime
		err := r.listKataPods()
		if err != nil {
			r.kataConfig.Status.UnInstallationStatus.ErrorMessage = err.Error()
			updErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
			if updErr != nil {
				return ctrl.Result{}, updErr
			}
			r.Log.Info("Kata PODs are present. Requeue for reconciliation ")
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		} else {
			if r.kataConfig.Status.UnInstallationStatus.ErrorMessage != "" {
				r.kataConfig.Status.UnInstallationStatus.ErrorMessage = ""
				updErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
				if updErr != nil {
					return ctrl.Result{}, updErr
				}
			}
		}
	}

	if r.kataConfig.Spec.KataConfigPoolSelector == nil {
		r.kataConfig.Spec.KataConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"node-role.kubernetes.io/" + machinePool: ""},
		}
	}

	r.Log.Info("Making sure parent MCP is synced properly, SCNodeRole=" + machinePool)
	r.kataConfig.Status.UnInstallationStatus.InProgress.IsInProgress = corev1.ConditionTrue
	mc, err := r.newMCForCR(machinePool)
	if err != nil {
		return ctrl.Result{}, err
	}
	var isMcDeleted bool

	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, mc)
	if err != nil && k8serrors.IsNotFound(err) {
		isMcDeleted = true
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if !isMcDeleted {
		err = r.Client.Delete(context.TODO(), mc)
		if err != nil {
			// error during removing mc, don't block the uninstall. Just log the error and move on.
			r.Log.Error(err, "Error found deleting machine config. If the machine config exists after installation it can be safely deleted manually.",
				"mc", mc.Name)
		}
		// Sleep for MCP to reflect the changes
		r.Log.Info("Pausing for a minute to make sure worker mcp has started syncing up")
		time.Sleep(60 * time.Second)
	}

	workerMcp := &mcfgv1.MachineConfigPool{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: machinePool}, workerMcp)
	if err != nil {
		r.Log.Error(err, "Unable to get MachineConfigPool ", "machinePool", machinePool)
		return ctrl.Result{}, err
	}
	r.Log.Info("Monitoring worker mcp", "worker mcp name", workerMcp.Name, "ready machines", workerMcp.Status.ReadyMachineCount,
		"total machines", workerMcp.Status.MachineCount)
	r.kataConfig.Status.UnInstallationStatus.InProgress.IsInProgress = corev1.ConditionTrue
	r.clearUninstallStatus()
	_, result, err2, done := r.updateStatus(machinePool)
	if !done {
		return result, err2
	}

	if workerMcp.Status.ReadyMachineCount != workerMcp.Status.MachineCount {
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
	}

	r.setUpgradeableTrue()
	r.kataConfig.Status.UnInstallationStatus.InProgress.IsInProgress = corev1.ConditionFalse
	_, result, err2, done = r.updateStatus(machinePool)
	r.clearInstallStatus()
	if !done {
		r.setUpgradeableFalse("uninstallation")
		return result, err2
	}
	err = r.Client.Status().Update(context.TODO(), r.kataConfig)
	if err != nil {
		r.Log.Error(err, "Unable to update KataConfig status")
		r.setUpgradeableFalse("uninstallation")
		return ctrl.Result{}, err
	}

	r.Log.Info("Uninstallation completed. Proceeding with the KataConfig deletion")
	controllerutil.RemoveFinalizer(r.kataConfig, kataConfigFinalizer)

	err = r.Client.Update(context.TODO(), r.kataConfig)
	if err != nil {
		r.Log.Error(err, "Unable to update KataConfig")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *KataConfigOpenShiftReconciler) processKataConfigInstallRequest() (ctrl.Result, error) {
	r.Log.Info("Kata installation in progress")
	machinePool, err := r.getMcpName()
	if err != nil {
		return reconcile.Result{}, err
	}

	// Add finalizer for this CR
	if !contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
		if err := r.addFinalizer(); err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info("SCNodeRole is: " + machinePool)
	}

	if r.kataConfig.Spec.KataConfigPoolSelector == nil {
		r.kataConfig.Spec.KataConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"node-role.kubernetes.io/" + machinePool: ""},
		}
	}

	/* create custom Machine Config Pool if configured by user */
	if _, ok := r.kataConfig.Spec.KataConfigPoolSelector.MatchLabels["node-role.kubernetes.io/"+machinePool]; !ok {
		r.Log.Info("Creating new MachineConfigPool")
		mcp := r.newMCPforCR()

		foundMcp := &mcfgv1.MachineConfigPool{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mcp.Name}, foundMcp)
		if err != nil && k8serrors.IsNotFound(err) {
			r.Log.Info("Creating a new MachineConfigPool ", "mcp.Name", mcp.Name)
			err = r.Client.Create(context.TODO(), mcp)
			if err != nil {
				r.Log.Error(err, "Error in creating new MachineConfigPool ", "mcp.Name", mcp.Name)
				return ctrl.Result{}, err
			}
			// mcp created successfully - requeue to check the status later
			return ctrl.Result{Requeue: true, RequeueAfter: 20 * time.Second}, nil
		} else if err != nil {
			r.Log.Error(err, "Error in retreiving MachineConfigPool ", "mcp.Name", mcp.Name)
			return ctrl.Result{}, err
		}

		// Wait till MCP is ready
		if foundMcp.Status.MachineCount == 0 {
			r.Log.Info("Waiting till MachineConfigPool is initialized ", "mcp.Name", mcp.Name)
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
		}

	}

	doReconcile, err, isMcCreated := r.createExtensionMc(machinePool)
	if isMcCreated {
		return doReconcile, err
	}

	foundMcp, doReconcile, err, done := r.updateStatus(machinePool)
	if !done {
		return doReconcile, err
	}

	r.kataConfig.Status.TotalNodesCount = int(foundMcp.Status.MachineCount)

	if mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdating) &&
		r.kataConfig.Status.InstallationStatus.IsInProgress == "false" &&
		r.kataConfig.Status.RuntimeClass == "kata" {
		r.Log.Info("New node being added to existing cluster")
		r.kataConfig.Status.InstallationStatus.IsInProgress = corev1.ConditionTrue
		return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
	}

	if mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdated) &&
		foundMcp.Status.ObservedGeneration > r.kataConfig.Status.BaseMcpGeneration &&
		foundMcp.Status.UpdatedMachineCount == foundMcp.Status.MachineCount {
		r.Log.Info("set runtime class")
		r.kataConfig.Status.InstallationStatus.IsInProgress = "false"
		r.setUpgradeableTrue()
		return r.setRuntimeClass()
	} else {
		r.Log.Info("Waiting for MachineConfigPool to be fully updated")
		return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
	}
}

func (r *KataConfigOpenShiftReconciler) createExtensionMc(machinePool string) (ctrl.Result, error, bool) {
	r.Log.Info("creating RHCOS extension MachineConfig")
	mc, err := r.newMCForCR(machinePool)
	if err != nil {
		return ctrl.Result{}, err, true
	}

	foundMcp := &mcfgv1.MachineConfigPool{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: machinePool}, foundMcp)
	if err != nil && k8serrors.IsNotFound(err) {
		r.Log.Info("MachineConfigPool not found")
		return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil, false
	}

	/* Create Machine Config object to enable sandboxed containers RHCOS extension */
	foundMc := &mcfgv1.MachineConfig{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: mc.Name}, foundMc)
	if err != nil && (k8serrors.IsNotFound(err) || k8serrors.IsGone(err)) {
		err = r.Client.Create(context.TODO(), mc)
		if err != nil {
			r.Log.Error(err, "Failed to create a new MachineConfig ", "mc.Name", mc.Name)
			return ctrl.Result{}, err, true
		}
		/* mc created successfully - it will take a moment to finalize, requeue to create runtimeclass */
		r.kataConfig.Status.InstallationStatus.IsInProgress = corev1.ConditionTrue
		r.kataConfig.Status.BaseMcpGeneration = foundMcp.Status.ObservedGeneration
		return ctrl.Result{Requeue: true}, nil, true
	}
	return ctrl.Result{}, nil, false
}

func (r *KataConfigOpenShiftReconciler) mapKataConfigToRequests(kataConfigObj client.Object) []reconcile.Request {

	kataConfigList := &kataconfigurationv1.KataConfigList{}

	err := r.Client.List(context.TODO(), kataConfigList)
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
}

func (r *KataConfigOpenShiftReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kataconfigurationv1.KataConfig{}).
		Watches(
			&source.Kind{Type: &mcfgv1.MachineConfigPool{}},
			handler.EnqueueRequestsFromMapFunc(r.mapKataConfigToRequests)).
		Complete(r)
}

func (r *KataConfigOpenShiftReconciler) getMcp() (*mcfgv1.MachineConfigPool, error) {
	machinePool, err := r.getMcpName()
	if err != nil {
		return nil, err
	}

	foundMcp := &mcfgv1.MachineConfigPool{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: machinePool}, foundMcp)
	if err != nil {
		r.Log.Error(err, "Getting MachineConfigPool failed ", "machinePool", machinePool)
		return nil, err
	}

	return foundMcp, nil
}

func (r *KataConfigOpenShiftReconciler) getNodes() (error, *corev1.NodeList) {
	nodes := &corev1.NodeList{}
	labelSelector := labels.SelectorFromSet(map[string]string{"node-role.kubernetes.io/worker": ""})
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: labelSelector},
	}

	if err := r.Client.List(context.TODO(), nodes, listOpts...); err != nil {
		r.Log.Error(err, "Getting list of nodes failed")
		return err, &corev1.NodeList{}
	}
	return nil, nodes
}

func (r *KataConfigOpenShiftReconciler) getConditionReason(conditions []mcfgv1.MachineConfigPoolCondition, conditionType mcfgv1.MachineConfigPoolConditionType) string {
	for _, c := range conditions {
		if c.Type == conditionType {
			return c.Message
		}
	}

	return ""
}

func (r *KataConfigOpenShiftReconciler) updateStatus(machinePool string) (*mcfgv1.MachineConfigPool, ctrl.Result, error, bool) {
	/* update KataConfig according to occurred error
	 * We need to pull the status information from the machine config pool object
	 */
	foundMcp := &mcfgv1.MachineConfigPool{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: machinePool}, foundMcp)
	if err != nil && k8serrors.IsNotFound(err) {
		r.Log.Error(err, "Unable to get MachineConfigPool ", "machinePool", machinePool)
		return nil, reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil, true
	}

	/* installation status */
	if corev1.ConditionTrue == r.kataConfig.Status.InstallationStatus.IsInProgress {
		r.setUpgradeableFalse("installation")
		err, _ := r.updateInstallStatus()
		if err != nil {
			return foundMcp, reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err, false
		}
		if foundMcp.Status.DegradedMachineCount > 0 || mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions,
			mcfgv1.MachineConfigPoolDegraded) {
			err, r.kataConfig.Status.InstallationStatus.Failed = r.updateFailedStatus(r.kataConfig.Status.InstallationStatus.Failed)
			if err != nil {
				return foundMcp, reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err, false
			}
		}
	}

	/* uninstallation status */
	if corev1.ConditionTrue == r.kataConfig.Status.UnInstallationStatus.InProgress.IsInProgress {
		r.setUpgradeableFalse("uninstallation")
		err, _ := r.updateUninstallStatus()
		if err != nil {
			return foundMcp, reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err, false
		}
		if foundMcp.Status.DegradedMachineCount > 0 || mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions,
			mcfgv1.MachineConfigPoolDegraded) {
			err, r.kataConfig.Status.UnInstallationStatus.Failed = r.updateFailedStatus(r.kataConfig.Status.UnInstallationStatus.Failed)
			if err != nil {
				return foundMcp, reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err, false
			}
		}
	}

	return foundMcp, reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil, true
}

func (r *KataConfigOpenShiftReconciler) updateUninstallStatus() (error, bool) {
	var err error
	err, nodeList := r.getNodes()
	if err != nil {
		return err, false
	}

	r.clearUninstallStatus()

	for _, node := range nodeList.Items {
		if annotation, ok := node.Annotations["machineconfiguration.openshift.io/state"]; ok {
			switch annotation {
			case "Done":
				err, r.kataConfig.Status.UnInstallationStatus.Completed =
					r.updateCompletedNodes(&node, r.kataConfig.Status.UnInstallationStatus.Completed)
			case "Degraded":
				err, r.kataConfig.Status.UnInstallationStatus.Failed.FailedNodesList =
					r.updateFailedNodes(&node, r.kataConfig.Status.UnInstallationStatus.Failed.FailedNodesList)
			case "Working":
				err, r.kataConfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList =
					r.updateInProgressNodes(&node, r.kataConfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList)
			default:
				err = fmt.Errorf("Invalid machineconfig state: %v ", annotation)
				r.Log.Error(err, "Error updating Uninstall status")
			}
		}
	}
	return err, true
}

func (r *KataConfigOpenShiftReconciler) updateInProgressNodes(node *corev1.Node, inProgressList []string) (error, []string) {
	foundMcp, err := r.getMcp()
	if err != nil {
		return err, inProgressList
	}
	if mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdating) &&
		r.kataConfig.Status.BaseMcpGeneration < foundMcp.Status.ObservedGeneration {
		inProgressList = append(inProgressList, node.GetName())
	}

	return nil, inProgressList
}

func (r *KataConfigOpenShiftReconciler) updateCompletedNodes(node *corev1.Node, completedStatus kataconfigurationv1.KataConfigCompletedStatus) (error, kataconfigurationv1.KataConfigCompletedStatus) {
	foundMcp, err := r.getMcp()
	if err != nil {
		return err, completedStatus
	}
	currentNodeConfig, ok := node.Annotations["machineconfiguration.openshift.io/currentConfig"]
	if ok && foundMcp.Spec.Configuration.Name == currentNodeConfig &&
		r.kataConfig.Status.BaseMcpGeneration < foundMcp.Status.ObservedGeneration &&
		(r.kataConfig.Status.InstallationStatus.IsInProgress == corev1.ConditionTrue ||
			r.kataConfig.Status.UnInstallationStatus.InProgress.IsInProgress == corev1.ConditionTrue) {

		completedStatus.CompletedNodesList = append(completedStatus.CompletedNodesList, node.GetName())
		completedStatus.CompletedNodesCount = int(foundMcp.Status.UpdatedMachineCount)
	}

	return nil, completedStatus
}

func (r *KataConfigOpenShiftReconciler) updateFailedNodes(node *corev1.Node,
	failedList []kataconfigurationv1.FailedNodeStatus) (error, []kataconfigurationv1.FailedNodeStatus) {

	foundMcp, err := r.getMcp()
	if err != nil {
		return err, failedList
	}
	if (mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolNodeDegraded) ||
		mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolDegraded)) &&
		r.kataConfig.Status.BaseMcpGeneration < foundMcp.Status.ObservedGeneration {
		failedList =
			append(r.kataConfig.Status.InstallationStatus.Failed.FailedNodesList,
				kataconfigurationv1.FailedNodeStatus{Name: node.GetName(),
					Error: node.Annotations["machineconfiguration.openshift.io/reason"]})
	}

	return nil, failedList
}

func (r *KataConfigOpenShiftReconciler) updateInstallStatus() (error, bool) {
	var err error
	err, nodeList := r.getNodes()
	if err != nil {
		return err, false
	}

	r.clearInstallStatus()

	for _, node := range nodeList.Items {
		if annotation, ok := node.Annotations["machineconfiguration.openshift.io/state"]; ok {
			switch annotation {
			case "Done":
				err, r.kataConfig.Status.InstallationStatus.Completed =
					r.updateCompletedNodes(&node, r.kataConfig.Status.InstallationStatus.Completed)
			case "Degraded":
				err, r.kataConfig.Status.InstallationStatus.Failed.FailedNodesList =
					r.updateFailedNodes(&node, r.kataConfig.Status.InstallationStatus.Failed.FailedNodesList)
			case "Working":
				err, r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList =
					r.updateInProgressNodes(&node, r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList)
			default:
				err = fmt.Errorf("Invalid machineconfig state: %v ", annotation)
				r.Log.Error(err, "Error updating Install status")
			}
		}
	}
	return err, true
}

func (r *KataConfigOpenShiftReconciler) updateFailedStatus(status kataconfigurationv1.KataFailedNodeStatus) (error, kataconfigurationv1.KataFailedNodeStatus) {
	foundMcp, err := r.getMcp()
	if err != nil {
		r.Log.Error(err, "couldn't get MachineConfigPool information")
		return err, status
	}

	status = r.clearFailedStatus(status)

	if foundMcp.Status.DegradedMachineCount > 0 {
		status.FailedReason = r.getConditionReason(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolNodeDegraded)
		return nil, status
	} else if mcfgv1.IsMachineConfigPoolConditionPresentAndEqual(foundMcp.Status.Conditions,
		mcfgv1.MachineConfigPoolDegraded, corev1.ConditionTrue) {
		status.FailedReason = r.getConditionReason(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolDegraded)
		return nil, status
	}

	return err, status
}

func (r *KataConfigOpenShiftReconciler) clearInstallStatus() {
	r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesList = nil
	r.kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount = 0
	r.kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList = nil
	r.kataConfig.Status.InstallationStatus.Failed.FailedNodesList = nil
	r.kataConfig.Status.InstallationStatus.Failed.FailedReason = ""
	r.kataConfig.Status.InstallationStatus.Failed.FailedNodesCount = 0
}

func (r *KataConfigOpenShiftReconciler) clearUninstallStatus() {
	r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesList = nil
	r.kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesCount = 0
	r.kataConfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList = nil
	r.kataConfig.Status.UnInstallationStatus.Failed.FailedNodesList = nil
	r.kataConfig.Status.UnInstallationStatus.Failed.FailedReason = ""
	r.kataConfig.Status.UnInstallationStatus.Failed.FailedNodesCount = 0
}

func (r *KataConfigOpenShiftReconciler) clearFailedStatus(status kataconfigurationv1.KataFailedNodeStatus) kataconfigurationv1.KataFailedNodeStatus {
	status.FailedNodesList = nil
	status.FailedReason = ""
	status.FailedNodesCount = 0

	return status
}
