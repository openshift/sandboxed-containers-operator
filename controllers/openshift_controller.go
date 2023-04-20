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
	"os"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	"k8s.io/apimachinery/pkg/labels"

	ignTypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-logr/logr"
	secv1 "github.com/openshift/api/security/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	labelsutil "k8s.io/kubernetes/pkg/util/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
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
	Os         OperatingSystem
}

const (
	dashboard_configmap_name      = "grafana-dashboard-sandboxed-containers"
	dashboard_configmap_namespace = "openshift-config-managed"
	container_runtime_config_name = "kata-crio-config"
	extension_mc_name             = "50-enable-sandboxed-containers-extension"
)

// +kubebuilder:rbac:groups=kataconfiguration.openshift.io,resources=kataconfigs;kataconfigs/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kataconfiguration.openshift.io,resources=kataconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;daemonsets;replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=daemonsets/finalizers,resourceNames=manager-role,verbs=update
// +kubebuilder:rbac:groups=node.k8s.io,resources=runtimeclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get
// +kubebuilder:rbac:groups="";machineconfiguration.openshift.io,resources=nodes;machineconfigs;machineconfigpools;containerruntimeconfigs;pods;services;services/finalizers;endpoints;persistentvolumeclaims;events;configmaps;secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=use;get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;update

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

		// k8s resource correctness checking on creation/modification
		// isn't fully reliable for matchExpressions.  Specifically,
		// it doesn't catch an invalid value of matchExpressions.operator.
		// With this work-around we check early if our kata node selector
		// is workable and bail out before making any changes to the
		// cluster if it turns out it isn't.
		_, err := r.getKataConfigNodeSelectorAsSelector()
		if err != nil {
			r.Log.Info("Invalid KataConfig.spec.kataConfigPoolSelector - please fix your KataConfig", "err", err)
			return ctrl.Result{}, nil
		}

		// Check if the KataConfig instance is marked to be deleted, which is
		// indicated by the deletion timestamp being set.
		if r.kataConfig.GetDeletionTimestamp() != nil {
			res, err := r.processKataConfigDeleteRequest()
			if err != nil {
				return res, err
			}
			updateErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
			if updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return res, nil
		}

		res, err := r.processKataConfigInstallRequest()
		if err != nil {
			return res, err
		}
		updateErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
		if updateErr != nil {
			return ctrl.Result{}, updateErr
		}

		ds := r.processDaemonsetForMonitor()
		// Set KataConfig instance as the owner and controller
		if err := controllerutil.SetControllerReference(r.kataConfig, ds, r.Scheme); err != nil {
			r.Log.Error(err, "failed to set controller reference on the monitor daemonset")
			return ctrl.Result{}, err
		}
		r.Log.Info("controller reference set for the monitor daemonset")

		foundDs := &appsv1.DaemonSet{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, foundDs)
		if err != nil {
			//The DaemonSet (DS) should be ideally created after the required SeLinux policy is installed on the
			//node. One of the ways to ensure this is to check for the existence of "kata" runtimeclass before
			//creating the DS
			//Alternatively we can create the DS post execution of createRuntimeClass()
			if k8serrors.IsNotFound(err) && r.kataConfig.Status.RuntimeClass == "kata" {
				r.Log.Info("Creating a new installation monitor daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
				err = r.Client.Create(context.TODO(), ds)
				if err != nil {
					r.Log.Error(err, "error when creating monitor daemonset")
					res = ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}
				}
			} else {
				r.Log.Error(err, "could not get monitor daemonset, try again")
				res = ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}
			}
		} else {
			r.Log.Info("Updating monitor daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
			err = r.Client.Update(context.TODO(), ds)
			if err != nil {
				r.Log.Error(err, "error when updating monitor daemonset")
				res = ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}
			}
		}
		cMap := r.processDashboardConfigMap()
		if cMap == nil {
			r.Log.Info("failed to generate config map for metrics dashboard")
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
		}
		foundCm := &corev1.ConfigMap{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: cMap.Name, Namespace: cMap.Namespace}, foundCm)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				r.Log.Info("Installing metrics dashboard")
				err = r.Client.Create(context.TODO(), cMap)
				if err != nil {
					r.Log.Error(err, "Error when creating the dashboard configmap")
					res = ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}
				}
			} else {
				r.Log.Error(err, "could not get dashboard info, try again")
				res = ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}
			}
		}

		err = r.processLogLevel(r.kataConfig.Spec.LogLevel)
		if err != nil {
			res = ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}
		}

		return res, err
	}()
}

func makeContainerRuntimeConfig(desiredLogLevel string, mcpSelector *metav1.LabelSelector) *mcfgv1.ContainerRuntimeConfig {
	return &mcfgv1.ContainerRuntimeConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "ContainerRuntimeConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: container_runtime_config_name,
		},
		Spec: mcfgv1.ContainerRuntimeConfigSpec{
			MachineConfigPoolSelector: mcpSelector,
			ContainerRuntimeConfig: &mcfgv1.ContainerRuntimeConfiguration{
				LogLevel: desiredLogLevel,
			},
		},
	}
}

func (r *KataConfigOpenShiftReconciler) processLogLevel(desiredLogLevel string) error {

	if desiredLogLevel == "" {
		r.Log.Info("desired logLevel value is empty, setting to default ('info')")
		desiredLogLevel = "info"
	}

	ctrRuntimeCfg := &mcfgv1.ContainerRuntimeConfig{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: container_runtime_config_name}, ctrRuntimeCfg)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			r.Log.Error(err, "could not get ContainerRuntimeConfig, try again")
			return err
		}

		r.Log.Info("no existing ContainerRuntimeConfig found")

		if desiredLogLevel == "info" {
			// if there's no ContainerRuntimeConfig - meaning that logLevel
			// wasn't set yet and thus is at the default value in the cluster -
			// *and* the desired value is the default one as well, there's
			// nothing to do
			r.Log.Info("current and desired logLevel values are both default, no action necessary")
			return nil
		}

		machineConfigPoolSelectorLabels := map[string]string{"pools.operator.machineconfiguration.openshift.io/kata-oc": ""}
		isConvergedCluster, err := r.checkConvergedCluster()
		if isConvergedCluster && err == nil {
			machineConfigPoolSelectorLabels = map[string]string{"pools.operator.machineconfiguration.openshift.io/master": ""}
		}

		machineConfigPoolSelector := &metav1.LabelSelector{
			MatchLabels: machineConfigPoolSelectorLabels,
		}

		ctrRuntimeCfg = makeContainerRuntimeConfig(desiredLogLevel, machineConfigPoolSelector)

		r.Log.Info("creating ContainerRuntimeConfig")
		err = r.Client.Create(context.TODO(), ctrRuntimeCfg)
		if err != nil {
			r.Log.Error(err, "error creating ContainerRuntimeConfig")
			return err
		}
		r.Log.Info("ContainerRuntimeConfig created successfully")
	} else {
		r.Log.Info("existing ContainerRuntimeConfig found")
		if ctrRuntimeCfg.Spec.ContainerRuntimeConfig.LogLevel == desiredLogLevel {
			r.Log.Info("existing ContainerRuntimeConfig is up-to-date, no action necessary")
			return nil
		}
		// We only update LogLevel and don't touch MachineConfigPoolSelector
		// as that shouldn't be necessary.  It selects an MCP based only on
		// whether the cluster is converged or not.  Assuming that being
		// converged is an immutable property of any given cluster, the initial
		// choice of MachineConfigPoolSelector value should always be valid.
		ctrRuntimeCfg.Spec.ContainerRuntimeConfig.LogLevel = desiredLogLevel

		r.Log.Info("updating ContainerRuntimeConfig")
		err = r.Client.Update(context.TODO(), ctrRuntimeCfg)
		if err != nil {
			r.Log.Error(err, "error updating ContainerRuntimeConfig")
			return err
		}
		r.Log.Info("ContainerRuntimeConfig updated successfully")
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) processDaemonsetForMonitor() *appsv1.DaemonSet {
	var (
		runPrivileged = false
		runUserID     = int64(1001)
		runGroupID    = int64(1001)
	)

	kataMonitorImage := os.Getenv("KATA_MONITOR_IMAGE")
	if len(kataMonitorImage) == 0 {
		// kata-monitor image URL is generally impossible to verify or sanitise,
		// with the empty value being pretty much the only exception where it's
		// fairly clear what good it is.  If we can only detect a single one
		// out of an infinite number of bad values, we choose not to return an
		// error here (giving an impression that we can actually detect errors)
		// but just log this incident and plow ahead.
		r.Log.Info("KATA_MONITOR_IMAGE env var is unset or empty, kata-monitor pods will not run")
	}

	r.Log.Info("Creating monitor DaemonSet with image file: \"" + kataMonitorImage + "\"")
	dsName := "openshift-sandboxed-containers-monitor"
	dsLabels := map[string]string{
		"name": dsName,
	}

	nodeSelector := r.getNodeSelectorAsMap()

	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      dsName,
			Namespace: "openshift-sandboxed-containers-operator",
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: dsLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: dsLabels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "monitor",
					NodeSelector:       nodeSelector,
					Containers: []corev1.Container{
						{
							Name:            "kata-monitor",
							Image:           kataMonitorImage,
							ImagePullPolicy: "Always",
							SecurityContext: &corev1.SecurityContext{
								Privileged: &runPrivileged,
								RunAsUser:  &runUserID,
								RunAsGroup: &runGroupID,
								SELinuxOptions: &corev1.SELinuxOptions{
									Type: "osc_monitor.process",
								},
							},
							Command: []string{"/usr/bin/kata-monitor", "--listen-address=:8090", "--log-level=debug", "--runtime-endpoint=/run/crio/crio.sock"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "crio-sock",
									MountPath: "/run/crio/",
								},
								{
									Name:      "sbs",
									MountPath: "/run/vc/sbs/",
								}},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "crio-sock",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/run/crio/",
								},
							},
						},
						{
							Name: "sbs",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/run/vc/sbs/",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *KataConfigOpenShiftReconciler) processDashboardConfigMap() *corev1.ConfigMap {

	r.Log.Info("Creating sandboxed containers dashboard in the OpenShift console")
	cmLabels := map[string]string{
		"console.openshift.io/dashboard": "true",
	}

	// retrieve content of the dashboard from our own namespace
	foundCm := &corev1.ConfigMap{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: dashboard_configmap_name, Namespace: "openshift-sandboxed-containers-operator"}, foundCm)
	if err != nil {
		r.Log.Error(err, "could not get dashboard data")
		return nil
	}

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      dashboard_configmap_name,
			Namespace: dashboard_configmap_namespace,
			Labels:    cmLabels,
		},
		Data: foundCm.Data,
	}
}

func (r *KataConfigOpenShiftReconciler) newMCPforCR() *mcfgv1.MachineConfigPool {
	lsr := metav1.LabelSelectorRequirement{
		Key:      "machineconfiguration.openshift.io/role",
		Operator: metav1.LabelSelectorOpIn,
		Values:   []string{"kata-oc", "worker"},
	}

	mcp := &mcfgv1.MachineConfigPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfigPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "kata-oc",
			Labels: map[string]string{
				// This label is added to make it possible to form a label
				// selector that selects this MCP.  One use case is the
				// ContainerRuntimeConfig resource which selects MCPs based
				// on labels and is used to implement KataConfig.spec.logLevel
				// handling.
				"pools.operator.machineconfiguration.openshift.io/kata-oc": "",
			},
		},

		Spec: mcfgv1.MachineConfigPoolSpec{
			MachineConfigSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{lsr},
			},
			NodeSelector: r.getNodeSelectorAsLabelSelector(),
		},
	}

	return mcp
}

func (r *KataConfigOpenShiftReconciler) newMCForCR(machinePool string) (*mcfgv1.MachineConfig, error) {
	r.Log.Info("Creating MachineConfig for Custom Resource")

	ic := ignTypes.Config{
		Ignition: ignTypes.Ignition{
			Version: "3.2.0",
		},
	}

	icb, err := json.Marshal(ic)
	if err != nil {
		return nil, err
	}

	// RHCOS uses "sandboxed-containers" as thats resolved/translated in the machine-config-operator to "kata-containers"
	// FCOS however does not get any translation in the machine-config-operator so we need to
	// send in "kata-containers".
	// Both are later send to rpm-ostree for installation.
	//
	// As RHCOS is rather special variant, use "kata-containers" by default, which also applies to FCOS
	var extensions = []string{"kata-containers"}

	if r.Os.IsEL() {
		extensions = []string{"sandboxed-containers"}
	}

	mc := mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: extension_mc_name,
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": machinePool,
				"app":                                    r.kataConfig.Name,
			},
			Namespace: "openshift-sandboxed-containers-operator",
		},
		Spec: mcfgv1.MachineConfigSpec{
			Extensions: extensions,
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
		r.Log.Info("kata-oc MachineConfigPool not found")
		return false, nil
	} else if err != nil {
		r.Log.Error(err, "Could not get the kata-oc MachineConfigPool")
		return false, err
	}

	return true, nil
}

func (r *KataConfigOpenShiftReconciler) checkConvergedCluster() (bool, error) {
	//Check if only master and worker MCP exists
	//Worker machinecount should be 0
	listOpts := []client.ListOption{}
	mcpList := &mcfgv1.MachineConfigPoolList{}
	err := r.Client.List(context.TODO(), mcpList, listOpts...)
	if err != nil {
		r.Log.Error(err, "Unable to get the list of MCPs")
		return false, err
	}

	numMcp := len(mcpList.Items)
	r.Log.Info("Number of MCPs", "numMcp", numMcp)
	if numMcp == 2 {
		for _, mcp := range mcpList.Items {
			if mcp.Name == "worker" && mcp.Status.MachineCount == 0 {
				r.Log.Info("Converged Cluster")
				return true, nil
			}
		}
	}

	return false, nil

}

func (r *KataConfigOpenShiftReconciler) checkNodeEligibility() error {
	r.Log.Info("Check Node Eligibility to run Kata containers")
	// Check if node eligibility label exists
	err, nodes := r.getNodesWithLabels(map[string]string{"feature.node.kubernetes.io/runtime.kata": "true"})
	if err != nil {
		r.Log.Error(err, "Error in getting list of nodes with label: feature.node.kubernetes.io/runtime.kata")
		return err
	}
	if len(nodes.Items) == 0 {
		err = fmt.Errorf("No Nodes with required labels found. Is NFD running?")
		return err
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) getMcpNameIfMcpExists() (string, error) {
	r.Log.Info("Getting MachineConfigPool Name")

	kataOC, err := r.kataOcExists()
	if kataOC && err == nil {
		r.Log.Info("kata-oc MachineConfigPool exists")
		return "kata-oc", nil
	}
	isConvergedCluster, err := r.checkConvergedCluster()
	if err == nil && isConvergedCluster {
		r.Log.Info("Converged Cluster. Not creating kata-oc MCP")
		return "master", nil
	}
	r.Log.Info("No valid MCP found")
	return "", err
}

func (r *KataConfigOpenShiftReconciler) getMcpName() (string, error) {
	isConvergedCluster, err := r.checkConvergedCluster()
	if err != nil {
		r.Log.Info("Error trying to find out if cluster is converged", "err", err)
		return "", err
	}
	if isConvergedCluster {
		return "master", nil
	} else {
		return "kata-oc", nil
	}
}

func (r *KataConfigOpenShiftReconciler) createScc() error {

	scc := GetScc()
	// Set Kataconfig r.kataConfig as the owner and controller
	if err := controllerutil.SetControllerReference(r.kataConfig, scc, r.Scheme); err != nil {
		return err
	}

	foundScc := &secv1.SecurityContextConstraints{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: scc.Name}, foundScc)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		r.Log.Info("Creating a new Scc", "scc.Name", scc.Name)
		err = r.Client.Create(context.TODO(), scc)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) createRuntimeClass() error {
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

		nodeSelector := r.getNodeSelectorAsMap()

		rc.Scheduling = &nodeapi.Scheduling{
			NodeSelector: nodeSelector,
		}

		r.Log.Info("RuntimeClass NodeSelector:", "nodeSelector", nodeSelector)

		return rc
	}()

	// Set Kataconfig r.kataConfig as the owner and controller
	if err := controllerutil.SetControllerReference(r.kataConfig, rc, r.Scheme); err != nil {
		return err
	}

	foundRc := &nodeapi.RuntimeClass{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: rc.Name}, foundRc)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		r.Log.Info("Creating a new RuntimeClass", "rc.Name", rc.Name)
		err = r.Client.Create(context.TODO(), rc)
		if err != nil {
			return err
		}
	}

	if r.kataConfig.Status.RuntimeClass == "" {
		r.kataConfig.Status.RuntimeClass = runtimeClassName
		err = r.Client.Status().Update(context.TODO(), r.kataConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

// "KataConfigNodeSelector" in the names of the following couple of helper
// functions refers to the value of KataConfig.spec.kataConfigPoolSelector,
// i.e. the original selector supplied by the user of KataConfig.
func (r *KataConfigOpenShiftReconciler) getKataConfigNodeSelectorAsLabelSelector() *metav1.LabelSelector {

	isConvergedCluster, err := r.checkConvergedCluster()
	if err == nil && isConvergedCluster {
		// master MCP cannot be customized
		return &metav1.LabelSelector { MatchLabels: map[string]string{ "node-role.kubernetes.io/master": "" }}
	}

	nodeSelector := &metav1.LabelSelector{}
	if r.kataConfig.Spec.KataConfigPoolSelector != nil {
		nodeSelector = r.kataConfig.Spec.KataConfigPoolSelector.DeepCopy()
	}

	if r.kataConfig.Spec.CheckNodeEligibility {
		nodeSelector = labelsutil.AddLabelToSelector(nodeSelector, "feature.node.kubernetes.io/runtime.kata", "true")
	}
	r.Log.Info("getKataConfigNodeSelectorAsLabelSelector()", "selector", nodeSelector)
	return nodeSelector
}

func (r *KataConfigOpenShiftReconciler) getKataConfigNodeSelectorAsSelector() (labels.Selector, error) {
	selector, err := metav1.LabelSelectorAsSelector(r.getKataConfigNodeSelectorAsLabelSelector())
	r.Log.Info("getKataConfigNodeSelectorAsSelector()", "selector", selector, "err", err)
	return selector, err
}

// "NodeSelector" in the names of the following couple of helper
// functions refers to the selector we pass to resources we create that
// need to select kata-enabled nodes (currently the "kata-oc" MCP, the pod
// template in the monitor daemonset and the runtimeclass).  It's guaranteed
// to be a simple map[string]string (AKA MatchLabels) which is good because the
// pod template's and runtimeclass' node selectors don't support
// MatchExpressions and thus cannot hold the full value of
// KataConfig.spec.kataConfigPoolSelector.
func (r *KataConfigOpenShiftReconciler) getNodeSelectorAsMap() map[string]string {

	isConvergedCluster, err := r.checkConvergedCluster()
	if err == nil && isConvergedCluster {
		// master MCP cannot be customized
		return map[string]string{ "node-role.kubernetes.io/master": "" }
	} else {
		return map[string]string{"node-role.kubernetes.io/kata-oc": ""}
	}
}

func (r *KataConfigOpenShiftReconciler) getNodeSelectorAsLabelSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: r.getNodeSelectorAsMap()}
}

func (r *KataConfigOpenShiftReconciler) processKataConfigDeleteRequest() (ctrl.Result, error) {
	r.Log.Info("KataConfig deletion in progress: ")
	machinePool, err := r.getMcpNameIfMcpExists()
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

	kataNodeSelector, err := r.getKataConfigNodeSelectorAsSelector()
	if err != nil {
		r.Log.Info("Couldn't get node selector for unlabelling nodes", "err", err)
		return ctrl.Result{Requeue: true}, nil
	}
	err = r.unlabelNodes(kataNodeSelector)

	if err != nil {
		if k8serrors.IsConflict(err) {
			return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
		} else {
			return ctrl.Result{Requeue: true}, nil
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
		r.Log.Info("Pausing for a minute to make sure mcp has started syncing up")
		time.Sleep(60 * time.Second)
	}

	mcp := &mcfgv1.MachineConfigPool{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: machinePool}, mcp)
	if err != nil {
		r.Log.Error(err, "Unable to get MachineConfigPool ", "machinePool", machinePool)
		return ctrl.Result{}, err
	}
	r.Log.Info("Monitoring mcp", "mcp name", mcp.Name, "ready machines", mcp.Status.ReadyMachineCount,
		"total machines", mcp.Status.MachineCount)
	r.kataConfig.Status.UnInstallationStatus.InProgress.IsInProgress = corev1.ConditionTrue
	r.clearUninstallStatus()
	_, result, err2, done := r.updateStatus(machinePool)
	if !done {
		return result, err2
	}

	if mcp.Status.ReadyMachineCount != mcp.Status.MachineCount {
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
	}

	// Sleep for MCP to reflect the changes
	r.Log.Info("Pausing for a minute to make sure mcp has started syncing up")
	time.Sleep(60 * time.Second)

	//This is not applicable for converged cluster
	isConvergedCluster, _ := r.checkConvergedCluster()
	if !isConvergedCluster {
		//Get "worker" MCP
		wMcp := &mcfgv1.MachineConfigPool{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: "worker"}, wMcp)
		if err != nil {
			r.Log.Error(err, "Unable to get MachineConfigPool - worker")
			return ctrl.Result{}, err
		}

		// At this time the kata-oc MCP is updated. However the worker MCP might still be in Updating state
		// We'll need to wait for the worker MCP to complete Updating before deletion
		r.Log.Info("Wait till worker MCP has updated")
		if (wMcp.Status.ReadyMachineCount != wMcp.Status.MachineCount) &&
			mcfgv1.IsMachineConfigPoolConditionTrue(wMcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdating) {
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
		}

		err = r.Client.Delete(context.TODO(), mcp)
		if err != nil {
			r.Log.Error(err, "Unable to delete kata-oc MachineConfigPool")
			return ctrl.Result{}, err
		}
	}

	r.kataConfig.Status.UnInstallationStatus.InProgress.IsInProgress = corev1.ConditionFalse
	_, result, err2, done = r.updateStatus(machinePool)
	r.clearInstallStatus()
	if !done {
		return result, err2
	}
	err = r.Client.Status().Update(context.TODO(), r.kataConfig)
	if err != nil {
		r.Log.Error(err, "Unable to update KataConfig status")
		return ctrl.Result{}, err
	}

	ds := r.processDaemonsetForMonitor()
	err = r.Client.Delete(context.TODO(), ds)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("monitor daemonset was already deleted")
		} else {
			r.Log.Error(err, "error when deleting monitor Daemonset, try again")
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
		}
	}

	scc := GetScc()
	err = r.Client.Delete(context.TODO(), scc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("SCC was already deleted")
		} else {
			r.Log.Error(err, "error when deleting SCC, retrying")
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
		}
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

	// Check Node Eligibility
	if r.kataConfig.Spec.CheckNodeEligibility {
		err := r.checkNodeEligibility()
		if err != nil {
			// If no nodes are found, requeue to check again for eligible nodes
			r.Log.Error(err, "Failed to check Node eligibility for running Kata containers")
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 20}, err
		}
	}

	// If converged cluster, then MCP == master, otherwise "kata-oc" if it exists
	machinePool, err := r.getMcpName()
	if err != nil {
		r.Log.Error(err, "Failed to get the MachineConfigPool name")
		return ctrl.Result{}, err
	}

	isConvergedCluster := machinePool == "master"

	// Add finalizer for this CR
	if !contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
		if err := r.addFinalizer(); err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info("SCNodeRole is: " + machinePool)
	}

	wasMcJustCreated, err := r.createExtensionMc(machinePool)
	if err != nil {
		return ctrl.Result{Requeue: true}, nil
	}

	// Create kata-oc MCP only if it's not a converged cluster
	if !isConvergedCluster {
		labelingChanged, err := r.updateNodeLabels()
		if err != nil {
			if k8serrors.IsConflict(err) {
				return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
			} else {
				return ctrl.Result{Requeue: true}, nil
			}
		}
		if labelingChanged {
			r.Log.Info("node labels updated")
			// IsInProgress == ConditionFalse would be more direct and clear but it doesn't handle
			// the possibility that IsInProgress == "" which actually comes up right after
			// KataConfig is created.
			if r.kataConfig.Status.InstallationStatus.IsInProgress != corev1.ConditionTrue {
				r.Log.Info("Starting to wait for MCO to start")
				r.kataConfig.Status.WaitingForMcoToStart = true
			} else {
				r.Log.Info("installation already in progress", "IsInProgress", r.kataConfig.Status.InstallationStatus.IsInProgress)
			}
		}

		// Create kata-oc only if it doesn't exist
		mcp := &mcfgv1.MachineConfigPool{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: machinePool}, mcp)
		if err != nil && k8serrors.IsNotFound(err) {
			r.Log.Info("Creating a new MachineConfigPool ", "machinePool", machinePool)
			mcp = r.newMCPforCR()
			err = r.Client.Create(context.TODO(), mcp)
			if err != nil {
				r.Log.Error(err, "Error in creating new MachineConfigPool ", "machinePool", machinePool)
				return ctrl.Result{}, err
			}
			// Don't requeue - the MCP creation will send us
			// a reconcile request via our MCP watching so we should be
			// guaranteed to run again at due time even without requeueing.
		} else if err != nil {
			r.Log.Error(err, "Error in retreiving MachineConfigPool ", "machinePool", machinePool)
			return ctrl.Result{}, err
		}
	} else {
		if wasMcJustCreated {
			r.kataConfig.Status.WaitingForMcoToStart = true
		}
	}

	isMcpUpdating := func(mcpName string) bool {
		mcp := &mcfgv1.MachineConfigPool{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: mcpName}, mcp)
		if err != nil {
			r.Log.Info("Getting MachineConfigPool failed ", "machinePool", mcpName, "err", err)
			return false
		}
		return mcfgv1.IsMachineConfigPoolConditionTrue(mcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdating)
	}

	isKataMcpUpdating := isMcpUpdating(machinePool)
	r.Log.Info("MCP updating state", "MCP name", machinePool, "is updating", isKataMcpUpdating)
	if isKataMcpUpdating {
		r.kataConfig.Status.InstallationStatus.IsInProgress = corev1.ConditionTrue
	}
	isMcoUpdating := isKataMcpUpdating
	if !isConvergedCluster {
		isWorkerUpdating := isMcpUpdating("worker")
		r.Log.Info("MCP updating state", "MCP name", "worker", "is updating", isWorkerUpdating)
		if isWorkerUpdating {
			r.kataConfig.Status.InstallationStatus.IsInProgress = corev1.ConditionTrue
		}
		isMcoUpdating = isKataMcpUpdating || isWorkerUpdating
	}

	// This condition might look tricky so here's a quick rundown of
	// what each possible state means:
	// - isMcoUpdating && WaitingForMcoToStart:
	//     We've just finished waiting for the MCO to start updating.
	//     The MCO is updating already but "Waiting" is still 'true'
	//     (it will be set to 'false' shortly).
	// - !isMcoUpdating && WaitingForMcoToStart:
	//     We're waiting for the MCO to pick up our recent changes and
	//     start updating.
	// - isMcoUpdating && !WaitingForMcoToStart:
	//     The MCO is updating (it hasn't yet finished processing our
	//     recent changes).
	// - !isMcoUpdating && !WaitingForMcoToStart:
	//     The MCO isn't updating nor do we think it should be.  This is
	//     the case e.g. when we're reconciliating a KataConfig change
	//     that doesn't affect kata installation on cluster.
	if !isMcoUpdating && r.kataConfig.Status.WaitingForMcoToStart == true {
		r.Log.Info("Waiting for MCO to start updating.")
		// We don't requeue, an MCP going Updated->Updating will
		// trigger reconciliation by itself thanks to our watching MCPs.
		return reconcile.Result{}, nil
	} else {
		r.Log.Info("No need to wait for MCO to start updating.", "isMcoUpdating", isMcoUpdating, "Status.WaitingForMcoToStart", r.kataConfig.Status.WaitingForMcoToStart)
		r.kataConfig.Status.WaitingForMcoToStart = false
	}

	foundMcp, doReconcile, err, done := r.updateStatus(machinePool)
	if !done {
		return doReconcile, err
	}

	r.kataConfig.Status.TotalNodesCount = int(foundMcp.Status.MachineCount)

	if !isMcoUpdating {
		r.Log.Info("create runtime class")
		r.kataConfig.Status.InstallationStatus.IsInProgress = corev1.ConditionFalse
		err := r.createRuntimeClass()
		if err != nil {
			// Give sometime for the error to go away before reconciling again
			return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
		r.Log.Info("create Scc")
		err = r.createScc()
		if err != nil {
			// Give sometime for the error to go away before reconciling again
			return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}

	} else {
		// We don't requeue - we're waiting for an MCP to go
		// Updating->Updated which will trigger reconciliation
		// by itself thanks to our watching MCPs.
		r.Log.Info("Waiting for MachineConfigPool to be fully updated", "machinePool", machinePool)
	}
	return ctrl.Result{}, nil
}

// If the first return value is 'true' it means that the MC was just created
// by this call, 'false' means that it's already existed.  As usual, the first
// return value is only valid if the second one is nil.
func (r *KataConfigOpenShiftReconciler) createExtensionMc(machinePool string) (bool, error) {

	// In case we're returning an error we want to make it explicit that
	// the first return value is "not care".  Unfortunately golang seems
	// to lack syntax for creating an expression with default bool value
	// hence this work-around.
	var dummy bool

	/* Create Machine Config object to enable sandboxed containers RHCOS extension */
	mc := &mcfgv1.MachineConfig{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: extension_mc_name}, mc)
	if err != nil && (k8serrors.IsNotFound(err) || k8serrors.IsGone(err)) {

		r.Log.Info("creating RHCOS extension MachineConfig")
		mc, err = r.newMCForCR(machinePool)
		if err != nil {
			return dummy, err
		}

		err = r.Client.Create(context.TODO(), mc)
		if err != nil {
			r.Log.Error(err, "Failed to create a new MachineConfig ", "mc.Name", mc.Name)
			return dummy, err
		}
		r.Log.Info("MachineConfig successfully created", "mc.Name", mc.Name)
		return true, nil
	} else if err != nil {
		r.Log.Info("failed to retrieve extension MachineConfig", "err", err)
		return dummy, err
	} else {
		r.Log.Info("extension MachineConfig already exists")
		return false, nil
	}
}

func (r *KataConfigOpenShiftReconciler) makeReconcileRequest() reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: r.kataConfig.Name,
		},
	}
}

func isMcpRelevant(mcp client.Object) bool {
	mcpName := mcp.GetName()
	// TODO Try to find a way to include "master" only if cluster is
	// converged.  It doesn't seem to hurt to watch it even on regular
	// clusters as it doesn't really seem to change much there but it
	// would be cleaner to watch it only when it's actually needed.
	if mcpName == "kata-oc" || mcpName == "worker" || mcpName == "master" {
		return true
	}
	return false
}

const missingMcpStatusConditionStr = "<missing>"

func logMcpChange(log logr.Logger, statusOld, statusNew mcfgv1.MachineConfigPoolStatus) {

	log.Info("MCP updated")

	if statusOld.MachineCount != statusNew.MachineCount {
		log.Info("MachineCount changed", "old", statusOld.MachineCount, "new", statusNew.MachineCount)
	} else {
		log.Info("MachineCount", "#", statusNew.MachineCount)
	}
	if statusOld.ReadyMachineCount != statusNew.ReadyMachineCount {
		log.Info("ReadyMachineCount changed", "old", statusOld.ReadyMachineCount, "new", statusNew.ReadyMachineCount)
	} else {
		log.Info("ReadyMachineCount", "#", statusNew.ReadyMachineCount)
	}
	if statusOld.UpdatedMachineCount != statusNew.UpdatedMachineCount {
		log.Info("UpdatedMachineCount changed", "old", statusOld.UpdatedMachineCount, "new", statusNew.UpdatedMachineCount)
	} else {
		log.Info("UpdatedMachineCount", "#", statusNew.UpdatedMachineCount)
	}
	if statusOld.DegradedMachineCount != statusNew.DegradedMachineCount {
		log.Info("DegradedMachineCount changed", "old", statusOld.DegradedMachineCount, "new", statusNew.DegradedMachineCount)
	} else {
		log.Info("DegradedMachineCount", "#", statusNew.DegradedMachineCount)
	}
	if statusOld.ObservedGeneration != statusNew.ObservedGeneration {
		log.Info("ObservedGeneration changed", "old", statusOld.ObservedGeneration, "new", statusNew.ObservedGeneration)
	}

	if !reflect.DeepEqual(statusOld.Conditions, statusNew.Conditions) {

		for _, condType := range []mcfgv1.MachineConfigPoolConditionType{"Updating", "Updated"} {
			condOld := mcfgv1.GetMachineConfigPoolCondition(statusOld, condType)
			condNew := mcfgv1.GetMachineConfigPoolCondition(statusNew, condType)
			condStatusOld := missingMcpStatusConditionStr
			if condOld != nil {
				condStatusOld = string(condOld.Status)
			}
			condStatusNew := missingMcpStatusConditionStr
			if condNew != nil {
				condStatusNew = string(condNew.Status)
			}

			if condStatusOld != condStatusNew {
				log.Info("mcp.status.conditions[] changed", "type", condType, "old", condStatusOld, "new", condStatusNew)
			}
		}
	}
}

type McpEventHandler struct {
	reconciler *KataConfigOpenShiftReconciler
}

func (eh *McpEventHandler) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	mcp := event.Object

	if !isMcpRelevant(mcp) {
		return
	}

	// Don't reconcile on MCP creation since we're unlikely to witness "worker"
	// creation and "kata-oc" should be only created by this controller.
	// Log the event anyway.
	log := eh.reconciler.Log.WithName("McpCreate").WithValues("MCP name", mcp.GetName())
	log.Info("MCP created")
}

func (eh *McpEventHandler) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	mcpOld := event.ObjectOld
	mcpNew := event.ObjectNew

	if !isMcpRelevant(mcpNew) {
		return
	}

	statusOld := mcpOld.(*mcfgv1.MachineConfigPool).Status
	statusNew := mcpNew.(*mcfgv1.MachineConfigPool).Status
	if reflect.DeepEqual(statusOld, statusNew) {
		return
	}

	foundRelevantChange := false

	if statusOld.MachineCount != statusNew.MachineCount {
		foundRelevantChange = true
	} else if statusOld.ReadyMachineCount != statusNew.ReadyMachineCount {
		foundRelevantChange = true
	} else if statusOld.UpdatedMachineCount != statusNew.UpdatedMachineCount {
		foundRelevantChange = true
	} else if statusOld.DegradedMachineCount != statusNew.DegradedMachineCount {
		foundRelevantChange = true
	}

	if !reflect.DeepEqual(statusOld.Conditions, statusNew.Conditions) {

		for _, condType := range []mcfgv1.MachineConfigPoolConditionType{"Updating", "Updated"} {
			condOld := mcfgv1.GetMachineConfigPoolCondition(statusOld, condType)
			condNew := mcfgv1.GetMachineConfigPoolCondition(statusNew, condType)
			condStatusOld := missingMcpStatusConditionStr
			if condOld != nil {
				condStatusOld = string(condOld.Status)
			}
			condStatusNew := missingMcpStatusConditionStr
			if condNew != nil {
				condStatusNew = string(condNew.Status)
			}

			if condStatusOld != condStatusNew {
				foundRelevantChange = true
			}
		}
	}

	if eh.reconciler.kataConfig != nil && foundRelevantChange {

		log := eh.reconciler.Log.WithName("McpUpdate").WithValues("MCP name", mcpOld.GetName())
		logMcpChange(log, statusOld, statusNew)

		queue.Add(eh.reconciler.makeReconcileRequest())
	}
}

func (eh *McpEventHandler) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	mcp := event.Object

	if !isMcpRelevant(mcp) {
		return
	}

	// Don't reconcile on MCP deletion since "worker" should never be deleted and
	// "kata-oc" should be only deleted by this controller.  Log the event anyway.
	log := eh.reconciler.Log.WithName("McpDelete").WithValues("MCP name", mcp.GetName())
	log.Info("MCP deleted")
}

func (eh *McpEventHandler) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
	mcp := event.Object

	if !isMcpRelevant(mcp) {
		return
	}

	// Don't reconcile on MCP generic event since it's not quite clear ATM
	// what it even means (we might revisit this later).  Log the event anyway.
	log := eh.reconciler.Log.WithName("McpGenericEvt").WithValues("MCP name", mcp.GetName())
	log.Info("MCP generic event")
}


func (r *KataConfigOpenShiftReconciler) nodeMatchesKataSelector(nodeLabels map[string]string) bool {
	nodeSelector, err := r.getKataConfigNodeSelectorAsSelector()

	if err != nil {
		r.Log.Info("couldn't get kata node selector", "err", err)
		// If we cannot find out whether a Node matches assuming that it
		// doesn't seems to be the safer assumption.  This also seems
		// consistent with error-handling semantics of earlier similar code.
		return false
	}

	return nodeSelector.Matches(labels.Set(nodeLabels))
}

func isWorkerNode(node client.Object) bool {
	if _, ok := node.GetLabels()["node-role.kubernetes.io/worker"]; ok {
		return true
	}
	return false
}

func getStringMapDiff(oldMap, newMap map[string]string) (added, modified, removed map[string]string) {
	added = map[string]string{}
	modified = map[string]string{}
	removed = map[string]string{}

	if oldMap == nil {
		added = newMap
		return
	}

	if newMap == nil {
		removed = oldMap
		return
	}

	for newKey, newVal := range newMap {
		if _, ok := oldMap[newKey]; !ok {
			added[newKey] = newVal
		}
	}
	for oldKey, oldVal := range oldMap {
		if _, ok := newMap[oldKey]; !ok {
			removed[oldKey] = oldVal
		} else {
			if oldMap[oldKey] != newMap[oldKey] {
				modified[oldKey] = newMap[oldKey]
			}
		}
	}
	return added, modified, removed
}


type NodeEventHandler struct {
	reconciler *KataConfigOpenShiftReconciler
}

func (eh *NodeEventHandler) Create(event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	node := event.Object

	log := eh.reconciler.Log.WithName("NodeCreate").WithValues("node name", node.GetName())
	log.Info("node created")

	if !isWorkerNode(node) {
		return
	}

	if eh.reconciler.kataConfig == nil {
		return
	}

	if !eh.reconciler.nodeMatchesKataSelector(node.GetLabels()) {
		return
	}
	log.Info("node matches kata node selector", "node labels", node.GetLabels())

	queue.Add(eh.reconciler.makeReconcileRequest())
}

func (eh *NodeEventHandler) Update(event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	// This function assumes that a node cannot change its role from master to
	// worker or vice-versa.
	nodeOld := event.ObjectOld
	nodeNew := event.ObjectNew

	log := eh.reconciler.Log.WithName("NodeUpdate").WithValues("node name", nodeNew.GetName())

	if !isWorkerNode(nodeNew) {
		return
	}

	if eh.reconciler.kataConfig == nil {
		return
	}

	foundRelevantChange := false

	// no need to check the second return value of the indexing operation
	// as "" is not a valid machineconfiguration.openshift.io/state value
	stateOld := nodeOld.GetAnnotations()["machineconfiguration.openshift.io/state"]
	stateNew := nodeNew.GetAnnotations()["machineconfiguration.openshift.io/state"]
	if stateOld != stateNew {
		foundRelevantChange = true
		log.Info("machineconfiguration.openshift.io/state changed", "old", stateOld, "new", stateNew)
	}

	labelsOld := nodeOld.GetLabels()
	labelsNew := nodeNew.GetLabels()

	if !reflect.DeepEqual(labelsOld, labelsNew) {

		log.Info("labels changed", "old", labelsOld, "new", labelsNew)
		added, modified, removed := getStringMapDiff(labelsOld, labelsNew)
		log.Info("labels diff", "added", added, "modified", modified, "removed", removed)

		matchOld := eh.reconciler.nodeMatchesKataSelector(labelsOld)
		matchNew := eh.reconciler.nodeMatchesKataSelector(labelsNew)

		log.Info("labels matching kata node selector", "old", matchOld, "new", matchNew)
		if matchOld != matchNew {
			foundRelevantChange = true
		}
	}

	if foundRelevantChange {
		queue.Add(eh.reconciler.makeReconcileRequest())
	}
}

func (eh *NodeEventHandler) Delete(event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
}

func (eh *NodeEventHandler) Generic(event event.GenericEvent, queue workqueue.RateLimitingInterface) {
}

func (r *KataConfigOpenShiftReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kataconfigurationv1.KataConfig{}).
		Watches(
			&source.Kind{Type: &mcfgv1.MachineConfigPool{}},
			&McpEventHandler{r}).
		Watches(
			&source.Kind{Type: &corev1.Node{}},
			&NodeEventHandler{r}).
		Complete(r)
}

func (r *KataConfigOpenShiftReconciler) getMcp() (*mcfgv1.MachineConfigPool, error) {
	machinePool, err := r.getMcpNameIfMcpExists()
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

func (r *KataConfigOpenShiftReconciler) getNodesWithLabels(nodeLabels map[string]string) (error, *corev1.NodeList) {
	nodes := &corev1.NodeList{}
	labelSelector := labels.SelectorFromSet(nodeLabels)
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: labelSelector},
	}

	if err := r.Client.List(context.TODO(), nodes, listOpts...); err != nil {
		r.Log.Error(err, "Getting list of nodes having specified labels failed")
		return err, &corev1.NodeList{}
	}
	return nil, nodes
}

func (r *KataConfigOpenShiftReconciler) updateNodeLabels() (labelingChanged bool, err error) {
	workerNodeList := &corev1.NodeList{}
	workerSelector := labels.SelectorFromSet(map[string]string{"node-role.kubernetes.io/worker": ""})
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: workerSelector},
	}

	if err := r.Client.List(context.TODO(), workerNodeList, listOpts...); err != nil {
		r.Log.Error(err, "Getting list of nodes failed")
		return false, err
	}

	kataNodeSelector, err := r.getKataConfigNodeSelectorAsSelector()
	if err != nil {
		r.Log.Info("Couldn't getKataConfigNodeSelectorAsSelector()", "err", err)
		return false, err
	}

	for _, worker := range workerNodeList.Items {
		workerMatchesKata := kataNodeSelector.Matches(labels.Set(worker.Labels))
		_, workerLabeledForKata := worker.Labels["node-role.kubernetes.io/kata-oc"]

		isLabelUpToDate := (workerMatchesKata && workerLabeledForKata) || (!workerMatchesKata && !workerLabeledForKata)

		if isLabelUpToDate {
			continue
		}

		if workerMatchesKata && !workerLabeledForKata {
			r.Log.Info("worker labeled", "node", worker.GetName())
			worker.Labels["node-role.kubernetes.io/kata-oc"] = ""
		} else if !workerMatchesKata && workerLabeledForKata {
			r.Log.Info("worker unlabeled", "node", worker.GetName())
			delete(worker.Labels, "node-role.kubernetes.io/kata-oc")
		}

		err = r.Client.Update(context.TODO(), &worker)
		if err != nil {
			r.Log.Error(err, "Error when adding labels to node", "node", worker)
			return labelingChanged, err
		}

		labelingChanged = true
	}

	return labelingChanged, nil
}

func (r *KataConfigOpenShiftReconciler) unlabelNodes(nodeSelector labels.Selector) (err error) {
	nodeList := &corev1.NodeList{}
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: nodeSelector},
	}

	if err := r.Client.List(context.TODO(), nodeList, listOpts...); err != nil {
		r.Log.Error(err, "Getting list of nodes failed")
		return err
	}

	for _, node := range nodeList.Items {
		if _, ok := node.Labels["node-role.kubernetes.io/kata-oc"]; ok {
			delete(node.Labels, "node-role.kubernetes.io/kata-oc")
			err = r.Client.Update(context.TODO(), &node)
			if err != nil {
				r.Log.Error(err, "Error when removing labels from node", "node", node)
				return err
			}
		}
	}
	return nil
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
	if mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdating) {
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
		mcfgv1.IsMachineConfigPoolConditionTrue(foundMcp.Status.Conditions, mcfgv1.MachineConfigPoolDegraded)) {
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
