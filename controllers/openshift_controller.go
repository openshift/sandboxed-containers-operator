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

	"github.com/confidential-containers/cloud-api-adaptor/peerpodconfig-ctrl/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"

	"k8s.io/apimachinery/pkg/labels"

	ignTypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-logr/logr"
	secv1 "github.com/openshift/api/security/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	mcfgconsts "github.com/openshift/machine-config-operator/pkg/daemon/constants"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// blank assignment to verify that KataConfigOpenShiftReconciler implements reconcile.Reconciler
// var _ reconcile.Reconciler = &KataConfigOpenShiftReconciler{}

// KataConfigOpenShiftReconciler reconciles a KataConfig object
type KataConfigOpenShiftReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	kataConfig *kataconfigurationv1.KataConfig
}

const (
	OperatorNamespace                   = "openshift-sandboxed-containers-operator"
	dashboard_configmap_name            = "grafana-dashboard-sandboxed-containers"
	dashboard_configmap_namespace       = "openshift-config-managed"
	container_runtime_config_name       = "kata-crio-config"
	extension_mc_name                   = "50-enable-sandboxed-containers-extension"
	DEFAULT_PEER_PODS                   = "10"
	peerpodConfigCrdName                = "peerpodconfig-openshift"
	peerpodsMachineConfigPathLocation   = "/config/peerpods"
	peerpodsCrioMachineConfig           = "50-kata-remote"
	peerpodsCrioMachineConfigYaml       = "mc-50-crio-config.yaml"
	peerpodsKataRemoteMachineConfig     = "40-worker-kata-remote-config"
	peerpodsKataRemoteMachineConfigYaml = "mc-40-kata-remote-config.yaml"
	peerpodsRuntimeClassName            = "kata-remote"
	peerpodsRuntimeClassCpuOverhead     = "0.25"
	peerpodsRuntimeClassMemOverhead     = "350Mi"
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
// +kubebuilder:rbac:groups="",resources=nodes/status,verbs=patch
// +kubebuilder:rbac:groups=confidentialcontainers.org,resources=peerpodconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=confidentialcontainers.org,resources=peerpodconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=confidentialcontainers.org,resources=peerpodconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=confidentialcontainers.org,resources=peerpods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=confidentialcontainers.org,resources=peerpods/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=confidentialcontainers.org,resources=peerpods/finalizers,verbs=update
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch
// +kubebuilder:rbac:groups="batch",resources=jobs,verbs=create;get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get

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
		// indicated by the deletion timestamp being set.  However, don't let
		// uninstallation commence if another operation (installation, update)
		// is underway.
		if r.kataConfig.GetDeletionTimestamp() != nil && !r.isInstalling() && !r.isUpdating() {
			res, err := r.processKataConfigDeleteRequest()

			updateErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
			// The finalizer test is to get rid of the
			// "Operation cannot be fulfilled [...] Precondition failed"
			// error which happens when returning from a reconciliation that
			// deleted our KataConfig by removing its finalizer.  So if the
			// finalizer is missing the actual KataConfig object is probably
			// already gone from the cluster, hence the error.
			if updateErr != nil && controllerutil.ContainsFinalizer(r.kataConfig, kataConfigFinalizer) {
				r.Log.Info("Updating KataConfig failed", "err", updateErr)
				return ctrl.Result{}, updateErr
			}
			return res, err
		}

		res, err := r.processKataConfigInstallRequest()
		if err != nil {
			return res, err
		}
		updateErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
		if updateErr != nil {
			return ctrl.Result{}, updateErr
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

func (r *KataConfigOpenShiftReconciler) removeLogLevel() error {

	r.Log.Info("removing logLevel ContainerRuntimeConfig")

	ctrRuntimeCfg := &mcfgv1.ContainerRuntimeConfig{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: container_runtime_config_name}, ctrRuntimeCfg)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("no logLevel ContainerRuntimeConfig found, nothing to do")
			return nil
		} else {
			r.Log.Info("could not get ContainerRuntimeConfig", "err", err)
			return err
		}
	}

	err = r.Client.Delete(context.TODO(), ctrRuntimeCfg)
	if err != nil {
		r.Log.Info("error deleting ContainerRuntimeConfig", "err", err)
		return err
	}
	r.Log.Info("logLevel ContainerRuntimeConfig deleted successfully")
	return nil
}

func (r *KataConfigOpenShiftReconciler) processDaemonsetForMonitor() *appsv1.DaemonSet {
	var (
		runPrivileged = false
		runUserID     = int64(1001)
		runGroupID    = int64(1001)
	)

	kataMonitorImage := os.Getenv("RELATED_IMAGE_KATA_MONITOR")
	if len(kataMonitorImage) == 0 {
		// kata-monitor image URL is generally impossible to verify or sanitise,
		// with the empty value being pretty much the only exception where it's
		// fairly clear what good it is.  If we can only detect a single one
		// out of an infinite number of bad values, we choose not to return an
		// error here (giving an impression that we can actually detect errors)
		// but just log this incident and plow ahead.
		r.Log.Info("RELATED_IMAGE_KATA_MONITOR env var is unset or empty, kata-monitor pods will not run")
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
			Namespace: OperatorNamespace,
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
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
						},
					},
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
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: dashboard_configmap_name, Namespace: OperatorNamespace}, foundCm)
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

func getExtensionName() string {
	// RHCOS uses "sandboxed-containers" as thats resolved/translated in the machine-config-operator to "kata-containers"
	// FCOS however does not get any translation in the machine-config-operator so we need to
	// send in "kata-containers".
	// Both are later send to rpm-ostree for installation.
	//
	// As RHCOS is rather special variant, use "kata-containers" by default, which also applies to FCOS
	extension := os.Getenv("SANDBOXED_CONTAINERS_EXTENSION")
	if len(extension) == 0 {
		extension = "kata-containers"
	}
	return extension
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

	extension := getExtensionName()

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
			Namespace: OperatorNamespace,
		},
		Spec: mcfgv1.MachineConfigSpec{
			Extensions: []string{extension},
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

func (r *KataConfigOpenShiftReconciler) removeFinalizer() error {
	r.Log.Info("Removing finalizer from the KataConfig")
	controllerutil.RemoveFinalizer(r.kataConfig, kataConfigFinalizer)

	err := r.Client.Update(context.TODO(), r.kataConfig)
	if err != nil {
		r.Log.Error(err, "Unable to update KataConfig")
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
		return fmt.Errorf("failed to list kata pods: %v", err)
	}
	for _, pod := range podList.Items {
		if pod.Spec.RuntimeClassName != nil {
			if contains(r.kataConfig.Status.RuntimeClasses, *pod.Spec.RuntimeClassName) {
				return fmt.Errorf("existing pods using \"%v\" RuntimeClass found. Please delete the pods manually for KataConfig deletion to proceed", *pod.Spec.RuntimeClassName)
			}
		}
	}
	return nil
}

//lint:ignore U1000 This method is unused, but let's keep it for now
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

	if r.kataConfig.Spec.EnablePeerPods {
		r.Log.Info("enablePeerPods is true. Skipping since they are mutually exclusive.")
		return nil
	}
	nodes, err := r.getNodesWithLabels(map[string]string{"feature.node.kubernetes.io/runtime.kata": "true"})
	if err != nil {
		r.Log.Error(err, "Error in getting list of nodes with label: feature.node.kubernetes.io/runtime.kata")
		return err
	}
	if len(nodes.Items) == 0 {
		err = fmt.Errorf("no Nodes with required labels found. Is NFD running?")
		return err
	}

	return nil
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

func (r *KataConfigOpenShiftReconciler) createRuntimeClass(runtimeClassName string, cpuOverhead string, memoryOverhead string) error {

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
					corev1.ResourceCPU:    resource.MustParse(cpuOverhead),
					corev1.ResourceMemory: resource.MustParse(memoryOverhead),
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

	if !contains(r.kataConfig.Status.RuntimeClasses, runtimeClassName) {
		r.kataConfig.Status.RuntimeClasses = append(r.kataConfig.Status.RuntimeClasses, runtimeClassName)
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
		return &metav1.LabelSelector{MatchLabels: map[string]string{"node-role.kubernetes.io/master": ""}}
	}

	nodeSelector := &metav1.LabelSelector{}
	if r.kataConfig.Spec.KataConfigPoolSelector != nil {
		nodeSelector = r.kataConfig.Spec.KataConfigPoolSelector.DeepCopy()
	}

	if r.kataConfig.Spec.CheckNodeEligibility {
		nodeSelector = metav1.AddLabelToSelector(nodeSelector, "feature.node.kubernetes.io/runtime.kata", "true")
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
		return map[string]string{"node-role.kubernetes.io/master": ""}
	} else {
		return map[string]string{"node-role.kubernetes.io/kata-oc": ""}
	}
}

func (r *KataConfigOpenShiftReconciler) getNodeSelectorAsLabelSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: r.getNodeSelectorAsMap()}
}

func (r *KataConfigOpenShiftReconciler) isMcpUpdating(mcpName string) bool {
	mcp := &mcfgv1.MachineConfigPool{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mcpName}, mcp)
	if err != nil {
		r.Log.Info("Getting MachineConfigPool failed ", "machinePool", mcpName, "err", err)
		return false
	}
	return mcfgv1.IsMachineConfigPoolConditionTrue(mcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdating)
}

func (r *KataConfigOpenShiftReconciler) processKataConfigDeleteRequest() (ctrl.Result, error) {
	r.Log.Info("KataConfig deletion in progress: ")
	machinePool, err := r.getMcpName()
	if err != nil {
		return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	if contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
		// Get the list of pods that might be running using kata runtime
		err := r.listKataPods()
		if err != nil {
			r.setInProgressConditionToBlockedByExistingKataPods(err.Error())
			updErr := r.Client.Status().Update(context.TODO(), r.kataConfig)
			if updErr != nil {
				return ctrl.Result{}, updErr
			}
			r.Log.Info("Kata PODs are present. Requeue for reconciliation ")
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	}

	kataNodeSelector, err := r.getKataConfigNodeSelectorAsSelector()
	if err != nil {
		r.Log.Info("Couldn't get node selector for unlabelling nodes", "err", err)
		return ctrl.Result{Requeue: true}, nil
	}
	labelingChanged, err := r.unlabelNodes(kataNodeSelector)

	if err != nil {
		if k8serrors.IsConflict(err) {
			return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
		} else {
			return ctrl.Result{Requeue: true}, nil
		}
	}

	r.Log.Info("Making sure parent MCP is synced properly, SCNodeRole=" + machinePool)
	r.setInProgressConditionToUninstalling()

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
	}

	isConvergedCluster, _ := r.checkConvergedCluster()

	// Conditions to detect whether we need to wait for the MCO to start
	// reconciliation differ based on whether the cluster is converged.
	// If so then it's the fact we've just deleted the extension MC, if not
	// then it's the node-role labeling change (if there's none it means
	// we're deleting a KataConfig on a cluster where no nodes matched the
	// kataConfigPoolSelector and thus there will be no change for the MCO
	// to reconciliate).
	if (isConvergedCluster && !isMcDeleted) || (!isConvergedCluster && labelingChanged) {
		r.Log.Info("Starting to wait for MCO to start reconciliation")
		r.kataConfig.Status.WaitingForMcoToStart = true
	}

	// When nodes migrate from a source pool to a target pool the source
	// pool is drained immediately and the nodes then slowly join the target
	// pool.  Thus the operation duration is dominated by the target pool
	// part and the target pool is what we need to watch to find out when
	// the operation is finished.  When uninstalling kata on a regular
	// cluster nodes leave "kata-oc" to rejoin "worker" so "worker" is our
	// target pool.  On a converged cluster, nodes leave "master" to rejoin
	// it so "master" is both source and target in this case.
	targetPool := "worker"
	if isConvergedCluster {
		targetPool = "master"
	}
	isMcoUpdating := r.isMcpUpdating(targetPool)

	if !isMcoUpdating && r.kataConfig.Status.WaitingForMcoToStart {
		r.Log.Info("Waiting for MCO to start updating.")
		// We don't requeue, an MCP going Updated->Updating will
		// trigger reconciliation by itself thanks to our watching MCPs.
		return reconcile.Result{}, nil
	} else {
		r.Log.Info("No need to wait for MCO to start updating.", "isMcoUpdating", isMcoUpdating, "Status.WaitingForMcoToStart", r.kataConfig.Status.WaitingForMcoToStart)
		r.kataConfig.Status.WaitingForMcoToStart = false
	}

	err = r.updateStatus()
	if err != nil {
		r.Log.Info("Error updating KataConfig.status", "err", err)
	}

	if isMcoUpdating {
		r.Log.Info("Waiting for MachineConfigPool to be fully updated", "machinePool", targetPool)
		return reconcile.Result{}, nil
	}

	r.resetInProgressCondition()

	if !isConvergedCluster {
		r.Log.Info("Get()'ing MachineConfigPool to delete it", "machinePool", "kata-oc")
		kataOcMcp := &mcfgv1.MachineConfigPool{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: "kata-oc"}, kataOcMcp)
		if err == nil {
			r.Log.Info("Deleting MachineConfigPool ", "machinePool", "kata-oc")
			err = r.Client.Delete(context.TODO(), kataOcMcp)
			if err != nil {
				r.Log.Error(err, "Unable to delete kata-oc MachineConfigPool")
				return ctrl.Result{}, err
			}
		} else if k8serrors.IsNotFound(err) {
			r.Log.Info("MachineConfigPool not found", "machinePool", "kata-oc")
		} else {
			r.Log.Error(err, "Unable to get MachineConfigPool ", "machinePool", "kata-oc")
			return ctrl.Result{}, err
		}
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

	if r.kataConfig.Spec.EnablePeerPods {
		// We are explicitly ignoring any errors in peerpodconfig and related machineconfigs removal as
		// these can be removed manually if needed and this is not in the critical path
		// of operator functionality
		_ = r.disablePeerPods()

		// Handle podvm image deletion
		status, err := ImageDelete(r.Client)
		if status == RequeueNeeded && err == nil {
			// Set the KataConfig status to PodVM Image Deleting
			r.setInProgressConditionToPodVMImageDeleting()
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
		} else if status == ImageDeletionFailed {
			// Set the KataConfig status to PodVM Image Deletion Failed
			r.setInProgressConditionToPodVMImageDeletionFailed()
			return reconcile.Result{}, err
		} else if status == ImageDeletionStatusUnknown {
			// Set the KataConfig status to PodVM Image Deletion Status Unknown
			r.setInProgressConditionToPodVMImageDeletionUnknown()

			// Reconcile with error
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err

		} else if err != nil {
			// Reconcile with error
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
		}

		// Set the KataConfig status to PodVM Image Deleted
		r.setInProgressConditionToPodVMImageDeleted()

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

	err = r.removeLogLevel()
	if err != nil {
		return ctrl.Result{Requeue: true}, nil
	}

	r.Log.Info("Uninstallation completed. Proceeding with the KataConfig deletion")
	if err = r.removeFinalizer(); err != nil {
		return ctrl.Result{Requeue: true}, nil
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

	// peer pod enablement
	if r.kataConfig.Spec.EnablePeerPods {
		err := r.enablePeerPodsMc()
		if err != nil {
			r.Log.Info("Enabling peerpods machineconfigs failed", "err", err)
			return ctrl.Result{}, err
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

	if wasMcJustCreated {
		r.setInProgressConditionToInstalling()
	}

	isInstallationInProgress := r.isMcpUpdating(machinePool) || (!isConvergedCluster && r.isMcpUpdating("worker"))

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

			if !isInstallationInProgress {
				r.Log.Info("Starting to wait for MCO to start")
				r.kataConfig.Status.WaitingForMcoToStart = true
			} else {
				r.Log.Info("installation already in progress")
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

	isKataMcpUpdating := r.isMcpUpdating(machinePool)
	r.Log.Info("MCP updating state", "MCP name", machinePool, "is updating", isKataMcpUpdating)

	isMcoUpdating := isKataMcpUpdating
	if !isConvergedCluster {
		isWorkerUpdating := r.isMcpUpdating("worker")
		r.Log.Info("MCP updating state", "MCP name", "worker", "is updating", isWorkerUpdating)
		isMcoUpdating = isKataMcpUpdating || isWorkerUpdating
	}

	if isMcoUpdating && r.getInProgressConditionValue() == corev1.ConditionFalse {
		r.setInProgressConditionToUpdating()
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

	err = r.updateStatus()
	if err != nil {
		r.Log.Info("Error updating KataConfig.status", "err", err)
	}

	if !isMcoUpdating {
		r.Log.Info("create runtime class")
		r.resetInProgressCondition()
		err := r.createRuntimeClass("kata", "0.25", "350Mi")
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

		ds := r.processDaemonsetForMonitor()
		// Set KataConfig instance as the owner and controller
		if err = controllerutil.SetControllerReference(r.kataConfig, ds, r.Scheme); err != nil {
			r.Log.Error(err, "failed to set controller reference on the monitor daemonset")
			return ctrl.Result{}, err
		}
		r.Log.Info("controller reference set for the monitor daemonset")

		foundDs := &appsv1.DaemonSet{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, foundDs)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				r.Log.Info("Creating a new installation monitor daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
				err = r.Client.Create(context.TODO(), ds)
				if err != nil {
					r.Log.Error(err, "error when creating monitor daemonset")
					return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
				}
			} else {
				r.Log.Error(err, "could not get monitor daemonset, try again")
				return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
			}
		} else {
			r.Log.Info("Updating monitor daemonset", "ds.Namespace", ds.Namespace, "ds.Name", ds.Name)
			err = r.Client.Update(context.TODO(), ds)
			if err != nil {
				r.Log.Error(err, "error when updating monitor daemonset")
				return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
			}
		}

		// create Pod VM image PeerPodConfig CRD and runtimeclass for peerpods
		if r.kataConfig.Spec.EnablePeerPods {
			// Create the podvm image
			status, err := ImageCreate(r.Client)
			if status == RequeueNeeded && err == nil {
				// Set the KataConfig status to PodVM Image Creating
				r.setInProgressConditionToPodVMImageCreating()
				return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
			} else if status == ImageCreationFailed {
				// Set the KataConfig status to PodVM Image Creation Failed
				r.setInProgressConditionToPodVMImageCreationFailed()
				return ctrl.Result{}, err
			} else if status == ImageCreationStatusUnknown {
				// Set the KataConfig status to PodVM Image Creation Status Unknown
				r.setInProgressConditionToPodVMImageCreationUnknown()

				// Reconcile with error
				return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
			} else if err != nil {
				// Reconcile with error
				return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
			}

			// Set the KataConfig status to PodVM Image Created
			r.setInProgressConditionToPodVMImageCreated()

			err = r.enablePeerPodsMiscConfigs()
			if err != nil {
				r.Log.Info("Enabling peerpodconfig CR, runtimeclass etc", "err", err)
				// Give sometime for the error to go away before reconciling again
				return reconcile.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err

			}

			// Reset the in progress condition
			r.resetInProgressCondition()
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

func (eh *McpEventHandler) Create(ctx context.Context, event event.CreateEvent, queue workqueue.RateLimitingInterface) {
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

func (eh *McpEventHandler) Update(ctx context.Context, event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
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

func (eh *McpEventHandler) Delete(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	mcp := event.Object

	if !isMcpRelevant(mcp) {
		return
	}

	// Don't reconcile on MCP deletion since "worker" should never be deleted and
	// "kata-oc" should be only deleted by this controller.  Log the event anyway.
	log := eh.reconciler.Log.WithName("McpDelete").WithValues("MCP name", mcp.GetName())
	log.Info("MCP deleted")
}

func (eh *McpEventHandler) Generic(ctx context.Context, event event.GenericEvent, queue workqueue.RateLimitingInterface) {
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

func (eh *NodeEventHandler) Create(ctx context.Context, event event.CreateEvent, queue workqueue.RateLimitingInterface) {
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

func (eh *NodeEventHandler) Update(ctx context.Context, event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
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

func (eh *NodeEventHandler) Delete(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
}

func (eh *NodeEventHandler) Generic(ctx context.Context, event event.GenericEvent, queue workqueue.RateLimitingInterface) {
}

func (r *KataConfigOpenShiftReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kataconfigurationv1.KataConfig{}).
		Watches(
			&mcfgv1.MachineConfigPool{},
			&McpEventHandler{r}).
		Watches(
			&corev1.Node{},
			&NodeEventHandler{r}).
		Complete(r)
}

func (r *KataConfigOpenShiftReconciler) getNodes() (*corev1.NodeList, error) {
	nodes := &corev1.NodeList{}
	labelSelector := labels.SelectorFromSet(map[string]string{"node-role.kubernetes.io/worker": ""})
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: labelSelector},
	}

	if err := r.Client.List(context.TODO(), nodes, listOpts...); err != nil {
		r.Log.Error(err, "Getting list of nodes failed")
		return &corev1.NodeList{}, err
	}
	return nodes, nil
}

func (r *KataConfigOpenShiftReconciler) getNodesWithLabels(nodeLabels map[string]string) (*corev1.NodeList, error) {
	nodes := &corev1.NodeList{}
	labelSelector := labels.SelectorFromSet(nodeLabels)
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: labelSelector},
	}

	if err := r.Client.List(context.TODO(), nodes, listOpts...); err != nil {
		r.Log.Error(err, "Getting list of nodes having specified labels failed")
		return &corev1.NodeList{}, err
	}
	return nodes, nil
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

func (r *KataConfigOpenShiftReconciler) unlabelNodes(nodeSelector labels.Selector) (labelingChanged bool, err error) {
	nodeList := &corev1.NodeList{}
	listOpts := []client.ListOption{
		client.MatchingLabelsSelector{Selector: nodeSelector},
	}

	if err := r.Client.List(context.TODO(), nodeList, listOpts...); err != nil {
		r.Log.Error(err, "Getting list of nodes failed")
		return false, err
	}

	for _, node := range nodeList.Items {
		if _, ok := node.Labels["node-role.kubernetes.io/kata-oc"]; ok {
			delete(node.Labels, "node-role.kubernetes.io/kata-oc")
			err = r.Client.Update(context.TODO(), &node)
			if err != nil {
				r.Log.Error(err, "Error when removing labels from node", "node", node)
				return labelingChanged, err
			}
			labelingChanged = true
		}
	}
	return labelingChanged, nil
}

//lint:ignore U1000 This method is unused, but let's keep it for now
func (r *KataConfigOpenShiftReconciler) getConditionReason(conditions []mcfgv1.MachineConfigPoolCondition, conditionType mcfgv1.MachineConfigPoolConditionType) string {
	for _, c := range conditions {
		if c.Type == conditionType {
			return c.Message
		}
	}

	return ""
}

func (r *KataConfigOpenShiftReconciler) getMcpByName(mcpName string) (*mcfgv1.MachineConfigPool, error) {

	mcp := &mcfgv1.MachineConfigPool{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: mcpName}, mcp)
	if err != nil {
		r.Log.Info("Getting MachineConfigPool failed ", "machinePool", mcp, "err", err)
		return nil, err
	}

	return mcp, nil
}

const (
	// "Working"
	NodeWorking = mcfgconsts.MachineConfigDaemonStateWorking
	// "Done"
	NodeDone = mcfgconsts.MachineConfigDaemonStateDone
	// "Degraded"
	NodeDegraded = mcfgconsts.MachineConfigDaemonStateDegraded
)

// If multiple errors occur during execution of this function the last one
// will be returned.
func (r *KataConfigOpenShiftReconciler) updateStatus() error {

	nodeList, err := r.getNodes()
	if err != nil {
		return err
	}

	r.clearNodeStatusLists()

	r.kataConfig.Status.KataNodes.NodeCount = func() int {
		nodes, err := r.getNodesWithLabels(r.getNodeSelectorAsMap())
		if err != nil {
			r.Log.Info("Error retrieving kata-oc labelled Nodes to count them", "err", err)
			return 0
		}
		return len(nodes.Items)
	}()

	for _, node := range nodeList.Items {
		e := r.putNodeOnStatusList(&node)
		if e != nil {
			err = e
		}
	}

	r.kataConfig.Status.KataNodes.ReadyNodeCount = len(r.kataConfig.Status.KataNodes.Installed)

	return err
}

// A set of mutually exclusive predicate functions that figure out kata
// installation status on a given Node from four pieces of data:
// - Node's MCO state
// - MachineConfig the Node is currently at
// - MachineConfig the Node is supposed to be at
// - and whether kata is enabled for the Node
func isNodeInstalled(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeDone && nodeCurrMc == nodeTargetMc && isKataEnabledOnNode
}

func isNodeNotInstalled(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeDone && nodeCurrMc == nodeTargetMc && !isKataEnabledOnNode
}

func isNodeInstalling(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeWorking && isKataEnabledOnNode
}

func isNodeUninstalling(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeWorking && !isKataEnabledOnNode
}

func isNodeWaitingToInstall(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeDone && nodeCurrMc != nodeTargetMc && isKataEnabledOnNode
}

func isNodeWaitingToUninstall(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeDone && nodeCurrMc != nodeTargetMc && !isKataEnabledOnNode
}

func isNodeFailedToInstall(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeDegraded && isKataEnabledOnNode
}

func isNodeFailedToUninstall(nodeMcoState string, nodeCurrMc string, nodeTargetMc string, isKataEnabledOnNode bool) bool {
	return nodeMcoState == NodeDegraded && !isKataEnabledOnNode
}

func (r *KataConfigOpenShiftReconciler) putNodeOnStatusList(node *corev1.Node) error {

	isConvergedCluster, err := r.checkConvergedCluster()
	if err != nil {
		return err
	}

	targetMcpName := func() string {
		if isConvergedCluster {
			return "master"
		}
		_, nodeLabeledForKata := node.Labels["node-role.kubernetes.io/kata-oc"]
		if nodeLabeledForKata {
			return "kata-oc"
		} else {
			return "worker"
		}
	}()

	targetMcp, err := r.getMcpByName(targetMcpName)
	if err != nil {
		return err
	}

	nodeMcoState, ok := node.Annotations["machineconfiguration.openshift.io/state"]
	if !ok {
		return fmt.Errorf("missing machineconfiguration.openshift.io/state on node %v", node.GetName())
	}

	nodeCurrMc, ok := node.Annotations["machineconfiguration.openshift.io/currentConfig"]
	if !ok {
		return fmt.Errorf("missing machineconfiguration.openshift.io/currentConfig on node %v", node.GetName())
	}

	// Note that to figure out the MachineConfig our Node should be at we
	// unfortunately cannot use
	// machineconfiguration.openshift.io/desiredConfig as would seem
	// logical and easy.  The reason is that the MCO only sets
	// `desiredConfig` to the actual desired config right before it starts
	// updating the Node.  So to get the correct target MachineConfig we
	// need to look at the MachineConfigPool that our Node belongs to or
	// will belong to shortly.
	nodeTargetMc := targetMcp.Spec.Configuration.Name

	// `isKataEnabledOnNode` is a per Node condition on regular clusters
	// but cluster-wide on converged ones.
	// On regular clusters, this is ultimately determined by
	// KataConfig.spec.kataConfigPoolSelector (we use the
	// node-role.kubernetes.io/kata-oc to find this above in this function,
	// and the node-role is in turn assigned to Nodes based on the pool
	// selector).
	// On converged clusters, basically only two operations are possible:
	// installing kata on all masters and uninstalling kata from all
	// masters, no per-Node options can be supported.  We find if kata is
	// supposed to be installed on the cluster by examining the "master"
	// MCP's MachineConfig to see if it installs the kata containers
	// extension.
	var isKataEnabledOnNode bool
	if isConvergedCluster {
		targetMc := &mcfgv1.MachineConfig{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: targetMcp.Spec.Configuration.Name}, targetMc)
		if err != nil {
			r.Log.Info("Failed to retrieve MachineConfig", "MC name", targetMcp.Spec.Configuration.Name, targetMc, "MCP name", targetMcpName)
			return err
		}

		isKataEnabledOnNode = func() bool {
			extensionName := getExtensionName()
			for _, extName := range targetMc.Spec.Extensions {
				if extName == extensionName {
					return true
				}
			}
			return false
		}()
	} else {
		isKataEnabledOnNode = targetMcpName == "kata-oc"
	}

	if isNodeInstalled(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is Installed", "node", node.GetName())
		r.kataConfig.Status.KataNodes.Installed = append(r.kataConfig.Status.KataNodes.Installed, node.GetName())
	} else if isNodeNotInstalled(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is NotInstalled", "node", node.GetName())
	} else if isNodeInstalling(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is Installing", "node", node.GetName())
		r.kataConfig.Status.KataNodes.Installing = append(r.kataConfig.Status.KataNodes.Installing, node.GetName())
	} else if isNodeUninstalling(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is Uninstalling", "node", node.GetName())
		r.kataConfig.Status.KataNodes.Uninstalling = append(r.kataConfig.Status.KataNodes.Uninstalling, node.GetName())
	} else if isNodeWaitingToInstall(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is WaitingToInstall", "node", node.GetName())
		r.kataConfig.Status.KataNodes.WaitingToInstall = append(r.kataConfig.Status.KataNodes.WaitingToInstall, node.GetName())
	} else if isNodeWaitingToUninstall(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is WaitingToUninstall", "node", node.GetName())
		r.kataConfig.Status.KataNodes.WaitingToUninstall = append(r.kataConfig.Status.KataNodes.WaitingToUninstall, node.GetName())
	} else if isNodeFailedToInstall(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is FailedToInstall", "node", node.GetName())
		r.kataConfig.Status.KataNodes.FailedToInstall = append(r.kataConfig.Status.KataNodes.FailedToInstall, node.GetName())
		r.setInProgressConditionToFailed(node)
	} else if isNodeFailedToUninstall(nodeMcoState, nodeCurrMc, nodeTargetMc, isKataEnabledOnNode) {
		r.Log.Info("node is FailedToUninstall", "node", node.GetName())
		r.kataConfig.Status.KataNodes.FailedToUninstall = append(r.kataConfig.Status.KataNodes.FailedToUninstall, node.GetName())
		r.setInProgressConditionToFailed(node)
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) clearNodeStatusLists() {
	r.kataConfig.Status.KataNodes.Installed = nil
	r.kataConfig.Status.KataNodes.Installing = nil
	r.kataConfig.Status.KataNodes.WaitingToInstall = nil
	r.kataConfig.Status.KataNodes.FailedToInstall = nil

	r.kataConfig.Status.KataNodes.Uninstalling = nil
	r.kataConfig.Status.KataNodes.WaitingToUninstall = nil
	r.kataConfig.Status.KataNodes.FailedToUninstall = nil
}

func (r *KataConfigOpenShiftReconciler) findInProgressCondition() *kataconfigurationv1.KataConfigCondition {
	for i := 0; i < len(r.kataConfig.Status.Conditions); i++ {
		if r.kataConfig.Status.Conditions[i].Type == kataconfigurationv1.KataConfigInProgress {
			return &r.kataConfig.Status.Conditions[i]
		}
	}
	return nil
}

func (r *KataConfigOpenShiftReconciler) getInProgressConditionValue() corev1.ConditionStatus {
	cond := r.findInProgressCondition()
	if cond == nil {
		return corev1.ConditionUnknown
	}
	return cond.Status
}

func (r *KataConfigOpenShiftReconciler) addInProgressCondition() *kataconfigurationv1.KataConfigCondition {
	r.kataConfig.Status.Conditions = append(r.kataConfig.Status.Conditions, kataconfigurationv1.KataConfigCondition{Type: kataconfigurationv1.KataConfigInProgress})

	r.Log.Info("InProgress Condition added")

	return &r.kataConfig.Status.Conditions[len(r.kataConfig.Status.Conditions)-1]
}

// This is just a technical helper to all InProgress Condition mutators,
// factoring their common preamble out into an own function.
func (r *KataConfigOpenShiftReconciler) retrieveInProgressConditionForChange() *kataconfigurationv1.KataConfigCondition {
	cond := r.findInProgressCondition()
	if cond == nil {
		cond = r.addInProgressCondition()
	}

	cond.LastTransitionTime = metav1.Now()

	return cond
}

func (r *KataConfigOpenShiftReconciler) setInProgressConditionToInstalling() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = "Installing"
	cond.Message = "Performing initial installation of kata on cluster"

	r.Log.Info("InProgress Condition set to Installing")
}

func (r *KataConfigOpenShiftReconciler) setInProgressConditionToUninstalling() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = "Uninstalling"
	cond.Message = "Removing kata from cluster"

	r.Log.Info("InProgress Condition set to Uninstalling")
}

func (r *KataConfigOpenShiftReconciler) setInProgressConditionToUpdating() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = "Updating"
	cond.Message = "Adding and/or removing kata-enabled nodes"

	r.Log.Info("InProgress Condition set to Updating")
}

func (r *KataConfigOpenShiftReconciler) setInProgressConditionToFailed(failingNode *corev1.Node) {
	reasonForDegraded, ok := failingNode.Annotations["machineconfiguration.openshift.io/reason"]
	if !ok {
		r.Log.Info("Missing machineconfiguration.openshift.io/reason on Degraded node", "node", failingNode.GetName())
	}

	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = "Failed"
	cond.Message = "Node " + failingNode.GetName() + " Degraded: " + reasonForDegraded

	r.Log.Info("InProgress Condition set to Failed")
}

func (r *KataConfigOpenShiftReconciler) setInProgressConditionToBlockedByExistingKataPods(message string) {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionFalse
	cond.Reason = "BlockedByExistingKataPods"
	cond.Message = message

	r.Log.Info("InProgress Condition set to BlockedByExistingKataPods")
}

func (r *KataConfigOpenShiftReconciler) resetInProgressCondition() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionFalse
	cond.Reason = ""
	cond.Message = ""

	r.Log.Info("InProgress Condition reset")
}

// Method to set the InProgress condition to indicate that the Pod VM Image is being created
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageCreating() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = PodVMImageJobRunning
	cond.Message = "Creating Pod VM Image"

	r.Log.Info("InProgress Condition set to PodVMImageJobRunning")
}

// Method to set the InProgress condition to indicate that the Pod VM Image has been created
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageCreated() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = PodVMImageJobCompleted
	cond.Message = "Created Pod VM Image"

	r.Log.Info("InProgress Condition set to PodVMImageJobCompleted")
}

// Method to set the InProgress condition to indicate that the Pod VM Image creation has failed
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageCreationFailed() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = PodVMImageJobFailed
	cond.Message = "Failed to create Pod VM Image"

	r.Log.Info("InProgress Condition set to PodVMImageJobFailed")
}

// Method to set the InProgress condition to indicate that the Pod VM Image creation status is unknown
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageCreationUnknown() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionUnknown
	cond.Reason = PodVMImageJobStatusUnknown
	cond.Message = "Pod VM Image creation status is unknown"

	r.Log.Info("InProgress Condition set to PodVMImageJobStatusUnknown")
}

// Method to set the InProgress condition to indicate that the Pod VM Image is being deleted
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageDeleting() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = PodVMImageJobRunning
	cond.Message = "Deleting Pod VM Image"

	r.Log.Info("InProgress Condition set to PodVMImageJobRunning")
}

// Method to set the InProgress condition to indicate that the Pod VM Image has been deleted
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageDeleted() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = PodVMImageJobCompleted
	cond.Message = "Deleted Pod VM Image"

	r.Log.Info("InProgress Condition set to PodVMImageJobCompleted")
}

// Method to set the InProgress condition to indicate that the Pod VM Image deletion has failed
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageDeletionFailed() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionTrue
	cond.Reason = PodVMImageJobFailed
	cond.Message = "Failed to delete Pod VM Image"

	r.Log.Info("InProgress Condition set to PodVMImageJobFailed")
}

// Method to set the InProgress condition to indicate that the Pod VM Image deletion status is unknown
func (r *KataConfigOpenShiftReconciler) setInProgressConditionToPodVMImageDeletionUnknown() {
	cond := r.retrieveInProgressConditionForChange()
	cond.Status = corev1.ConditionUnknown
	cond.Reason = PodVMImageJobStatusUnknown
	cond.Message = "Pod VM Image deletion status is unknown"

	r.Log.Info("InProgress Condition set to PodVMImageJobStatusUnknown")
}

func (r *KataConfigOpenShiftReconciler) isInstalling() bool {
	cond := r.findInProgressCondition()
	if cond == nil {
		return false
	}
	return cond.Status == corev1.ConditionTrue && cond.Reason == "Installing"
}

func (r *KataConfigOpenShiftReconciler) isUpdating() bool {
	cond := r.findInProgressCondition()
	if cond == nil {
		return false
	}
	return cond.Status == corev1.ConditionTrue && cond.Reason == "Updating"
}

func (r *KataConfigOpenShiftReconciler) createAuthJsonSecret() error {
	var err error = nil

	pullSecret := &corev1.Secret{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: "pull-secret", Namespace: "openshift-config"}, pullSecret)
	if err != nil {
		r.Log.Info("Error fetching pull-secret", "err", err)
		return err
	}

	authJsonSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-json-secret",
			Namespace: OperatorNamespace,
		},
		Data: map[string][]byte{
			"auth.json": pullSecret.Data[".dockerconfigjson"],
		},
		Type: corev1.SecretTypeOpaque,
	}

	err = r.Client.Create(context.TODO(), &authJsonSecret)
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			err = r.Client.Update(context.TODO(), &authJsonSecret)
			if err != nil {
				r.Log.Info("Error updating auth-json-secret", "err", err)
				return err
			}
		} else {
			r.Log.Info("Error creating auth-json-secret", "err", err)
			return err
		}
	}

	return err
}

// Create the MachineConfigs for PeerPod
// We do it before kata-oc creation to optimise the reboots required for MC creation
func (r *KataConfigOpenShiftReconciler) enablePeerPodsMc() error {

	//Create MachineConfig for kata-remote hyp CRIO config
	err := r.createMcFromFile(peerpodsCrioMachineConfigYaml)
	if err != nil {
		r.Log.Info("Error in creating CRIO MachineConfig", "err", err)
		return err
	}

	//Create MachineConfig for kata-remote hyp config toml
	err = r.createMcFromFile(peerpodsKataRemoteMachineConfigYaml)
	if err != nil {
		r.Log.Info("Error in creating kata remote configuration.toml MachineConfig", "err", err)
		return err
	}

	return nil
}

// Create the PeerPodConfig CRDs and misc configs required for peer-pods
func (r *KataConfigOpenShiftReconciler) enablePeerPodsMiscConfigs() error {
	peerPodConfig := v1alpha1.PeerPodConfig{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      peerpodConfigCrdName,
			Namespace: OperatorNamespace,
		},
		Spec: v1alpha1.PeerPodConfigSpec{
			CloudSecretName: "peer-pods-secret",
			ConfigMapName:   "peer-pods-cm",
			Limit:           DEFAULT_PEER_PODS,
			NodeSelector:    r.getNodeSelectorAsMap(),
		},
	}

	err := r.Client.Create(context.TODO(), &peerPodConfig)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		r.Log.Info("Error in creating peerpodconfig", "err", err)
		return err
	}

	//Get pull-secret from openshift-config ns and save it as auth-json-secret in our ns
	err = r.createAuthJsonSecret()
	if err != nil {
		r.Log.Info("Error in creating auth-json-secret", "err", err)
		return err
	}

	// Create the mutating webhook deployment
	err = r.createMutatingWebhookDeployment()
	if err != nil {
		r.Log.Info("Error in creating mutating webhook deployment for peerpods", "err", err)
		return err
	}

	// Create the mutating webhook service
	err = r.createMutatingWebhookService()
	if err != nil {
		r.Log.Info("Error in creating mutating webhook service for peerpods", "err", err)
		return err
	}

	// Create the mutating webhook
	err = r.createMutatingWebhookConfig()
	if err != nil {
		r.Log.Info("Error in creating mutating webhook for peerpods", "err", err)
		return err
	}

	// Create runtimeClass config for peer-pods
	err = r.createRuntimeClass(peerpodsRuntimeClassName, peerpodsRuntimeClassCpuOverhead, peerpodsRuntimeClassMemOverhead)
	if err != nil {
		r.Log.Info("Error in creating kata remote runtimeclass", "err", err)
		return err
	}
	return nil
}

func (r *KataConfigOpenShiftReconciler) disablePeerPods() error {
	peerPodConfig := v1alpha1.PeerPodConfig{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      peerpodConfigCrdName,
			Namespace: OperatorNamespace,
		},
	}
	err := r.Client.Delete(context.TODO(), &peerPodConfig)
	if err != nil {
		// error during removing peerpodconfig. Just log the error and move on.
		r.Log.Info("Error found deleting PeerPodConfig. If the PeerPodConfig object exists after uninstallation it can be safely deleted manually", "err", err)
	}

	mc := mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: peerpodsKataRemoteMachineConfig,
		},
	}

	err = r.Client.Delete(context.TODO(), &mc)
	if err != nil {
		// error during removing mc. Just log the error and move on.
		r.Log.Info("Error found deleting mc. If the MachineConfig object exists after uninstallation it can be safely deleted manually",
			"mc", mc.Name, "err", err)
	}

	mc = mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: peerpodsCrioMachineConfig,
		},
	}

	err = r.Client.Delete(context.TODO(), &mc)
	if err != nil {
		// error during removing mc. Just log the error and move on.
		r.Log.Info("Error found deleting mc. If the MachineConfig object exists after uninstallation it can be safely deleted manually",
			"mc", mc.Name, "err", err)
	}

	// Delete mutating webhook deployment
	err = r.deleteMutatingWebhookDeployment()
	if err != nil {
		r.Log.Info("Error in deleting mutating webhook deployment for peerpods", "err", err)
		return err
	}

	// Delete mutating webhook service
	err = r.deleteMutatingWebhookService()
	if err != nil {
		r.Log.Info("Error in deleting mutating webhook service for peerpods", "err", err)
		return err
	}

	// Delete the mutating webhook
	err = r.deleteMutatingWebhookConfig()
	if err != nil {
		r.Log.Info("Error in deleting mutating webhook for peerpods", "err", err)
		return err
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) createMcFromFile(mcFileName string) error {
	yamlData, err := readMachineConfigYAML(mcFileName)
	if err != nil {
		r.Log.Info("Error in reading MachineConfigYaml", "mcFileName", mcFileName, "err", err)
		return err
	}

	r.Log.Info("machineConfig yaml dump ", "yamlData", yamlData)

	machineConfig, err := parseMachineConfigYAML(yamlData)
	if err != nil {
		r.Log.Info("Error in parsing MachineConfigYaml", "mcFileName", mcFileName, "err", err)
		return err
	}

	// Default MCP is kata-oc, however for converged cluster it should be "master"
	isConvergedCluster, err := r.checkConvergedCluster()
	if isConvergedCluster && err == nil {
		machineConfig.Labels["machineconfiguration.openshift.io/role"] = "master"
	}

	r.Log.Info("machineConfig dump ", "machineConfig", machineConfig)

	if err := r.Client.Create(context.TODO(), machineConfig); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			r.Log.Info("machineConfig already exists")
			return nil
		} else {
			return err
		}
	}
	return nil
}
