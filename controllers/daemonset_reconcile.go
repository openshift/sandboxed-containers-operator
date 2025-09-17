package controllers

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	peerPodsConfigInstallDaemonSetName = "osc-config-sync"
	peerPodsConfigDaemonSetLabel       = "kataconfiguration.openshift.io/osc-config-sync"

	kataInstallDaemonSetName       = "osc-rpm"
	kataInstallationDaemonSetLabel = "kataconfiguration.openshift.io/kata-ds-rpm-install"
)

const (
	rhelCoreOsExtensionsImageName = "rhel-coreos-extensions"
	cliImageName                  = "cli"

	daemonSetImage = "quay.io/openshift_sandboxed_containers/osc-daemonset:1.10.3"
)

// KataInstallationDaemonSetState defines the possible states of the Kata installation DaemonSet.
type KataInstallationDaemonSetState string

// TODO: Do we need to add Failed states?
const (
	KataWaitingToInstall   KataInstallationDaemonSetState = "waiting_to_install"
	KataInstalled          KataInstallationDaemonSetState = "installed"
	KataInstalling         KataInstallationDaemonSetState = "installing"
	KataWaitingForReboot   KataInstallationDaemonSetState = "waiting_for_reboot" // rpm-ostree changes applied after reboot
	KataWaitingToUninstall KataInstallationDaemonSetState = "waiting_to_uninstall"
	KataUninstalling       KataInstallationDaemonSetState = "uninstalling"
	KataUninstalled        KataInstallationDaemonSetState = "uninstalled"
)

type PeerPodsConfigDaemonSetState string

const (
	PeerPodsConfigRemoving PeerPodsConfigDaemonSetState = "removing"
	PeerPodsConfigRemoved  PeerPodsConfigDaemonSetState = "removed"
)

// KataDaemonSetAction defines the possible actions that can be performed by Kata installation DaemonSet.
type KataDaemonSetAction string

const (
	InstallKata   KataDaemonSetAction = "install"
	UninstallKata KataDaemonSetAction = "uninstall"
)

func (r *KataConfigOpenShiftReconciler) processKataConfigDeleteRequestDaemonSet() (ctrl.Result, error) {
	r.Log.Info("KataConfig deletion in progress: ")

	res, err := r.checkDeletionEligibility()
	if err != nil {
		return res, err
	}

	r.setInProgressConditionToUninstalling()

	// Delete the kata install daemonset if it exists
	kataInstallDaemonSet, err := r.daemonSetForKataInstall(InstallKata)
	if err != nil {
		r.Log.Error(err, "Failed getting DaemonSet for Kata installation")
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}
	err = r.Client.Delete(context.TODO(), kataInstallDaemonSet)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("kata install daemonset was already deleted")
		} else {
			r.Log.Error(err, "error when deleting kata install Daemonset, try again")
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
		}
	}

	isUninstallDaemonSetJustCreated := false

	// Create kata uninstall daemonset if it doesn't exist
	// Save whether it was just created or not
	kataUninstallDaemonSet, err := r.daemonSetForKataInstall(UninstallKata)
	if err != nil {
		r.Log.Error(err, "Failed getting DaemonSet for Kata uninstallation")
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}
	if err := controllerutil.SetControllerReference(r.kataConfig, kataUninstallDaemonSet, r.Scheme); err != nil {
		r.Log.Error(err, "Failed setting ControllerReference for kata uninstallation DaemonSet")
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	foundKataDaemonSet := &appsv1.DaemonSet{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: kataUninstallDaemonSet.Name, Namespace: kataUninstallDaemonSet.Namespace}, foundKataDaemonSet)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating a new kata uninstallation daemonset", "kataUninstallDaemonSet.Namespace", kataUninstallDaemonSet.Namespace, "kataUninstallDaemonSet.Name", kataUninstallDaemonSet.Name)
			err = r.Client.Create(context.TODO(), kataUninstallDaemonSet)
			if err != nil {
				r.Log.Error(err, "Error when creating kata uninstallation daemonset")
				return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
			}
			isUninstallDaemonSetJustCreated = true

			kataSelector, err := r.getKataConfigNodeSelectorAsSelector()
			if err != nil {
				r.Log.Error(err, "Skipping node labeling due to missing Kata node selector", "label", KataWaitingToUninstall)
			} else {
				err = labelNodes(r.Client, kataSelector, map[string]string{kataInstallationDaemonSetLabel: string(KataWaitingToUninstall)})
				if err != nil {
					r.Log.Error(err, "Could not add label to kata nodes", "label", KataWaitingToUninstall, "selector", kataSelector)
				}
			}
		} else {
			r.Log.Error(err, "Could not get kata uninstallation daemonset, try again")
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	}

	err = r.updateStatusDaemonSet(UninstallKata)
	if err != nil {
		r.Log.Error(err, "Error updating KataConfig.status")
	}

	uninstallInProgress, err := r.isKataUninstallDaemonSetUninstalling()
	if err != nil {
		r.Log.Error(err, "Error getting uninstallation status")
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	// Wait for the Kata uninstall DaemonSet to uninstall the kata-containers rpm package and its dependencies
	// The daemonset will change the node label when the uninstallation is completed or reboot is required and will trigger reconciliation
	if isUninstallDaemonSetJustCreated || uninstallInProgress {
		r.Log.Info("Waiting for KataUninstall DaemonSet to finish")
		nodes, _ := r.isNodeRebootRequired()
		for _, node := range nodes {
			r.Log.Info("node must be rebooted", "node", node.Name)
		}
		return ctrl.Result{}, nil
	}

	r.resetInProgressCondition()

	err = r.deleteDaemonsetForMonitor()
	if err != nil {
		return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
	}

	if r.kataConfig.Spec.EnablePeerPods {
		// We want to make sure that the osc-config-sync daemonset removal successfully started
		// So we will try again in case of an error
		err = r.disablePeerPods()
		if err != nil {
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
		}

		// Wait for the osc-config-sync to delete the configuration files
		// The daemonset will change the node label when the removal is completed and will trigger reconciliation
		removalInProgress, err := r.isPeerPodsConfigRemovalInProgress()
		if err != nil {
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}

		if removalInProgress {
			r.Log.Info("Waiting for OSC Config sync DaemonSet to finish PeerPods Config removal")
			return ctrl.Result{}, nil
		}

		// Handle podvm image deletion
		res, err := r.deletePodVMImage()
		if res != nil {
			return *res, err
		}
		if err != nil {
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	}

	err = r.deleteScc()
	if err != nil {
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	kataNodeSelector, err := r.getKataConfigNodeSelectorAsSelector()
	if err != nil {
		r.Log.Info("Couldn't get node selector for unlabelling nodes", "err", err)
		return ctrl.Result{Requeue: true}, nil
	}
	err = r.unlabelNodesDaemonSet(kataNodeSelector)
	if err != nil {
		if k8serrors.IsConflict(err) {
			return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
		} else {
			return ctrl.Result{Requeue: true}, nil
		}
	}

	r.Log.Info("Uninstallation completed. Proceeding with the KataConfig deletion")
	if err = r.removeFinalizer(); err != nil {
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (r *KataConfigOpenShiftReconciler) isPeerPodsConfigRemovalInProgress() (bool, error) {
	removed, err := r.isKataNodePoolInState(peerPodsConfigDaemonSetLabel, string(PeerPodsConfigRemoved))
	if err != nil {
		return false, err
	}

	return !removed, nil
}

func (r *KataConfigOpenShiftReconciler) deletePeedPodsConfigDaemonSet() error {
	// Delete osc-config-sync-install daemonset
	copyPeerPodsConfigDaemonSet, err := r.daemonSetForPeerPodsConfig(InstallKata)
	if err != nil {
		r.Log.Error(err, "failed getting DaemonSet for peerpods config copy")
		return err
	}

	err = r.Client.Delete(context.TODO(), copyPeerPodsConfigDaemonSet)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("peerpods config copy daemonset was already deleted")
		} else {
			r.Log.Error(err, "error when deleting peerpods config copy Daemonset, try again")
			return err
		}
	}

	// Create osc-config-sync-uninstall daemonset
	removePeerPodsConfigDaemonSet, err := r.daemonSetForPeerPodsConfig(UninstallKata)
	if err != nil {
		r.Log.Error(err, "failed getting DaemonSet for peerpods config removal")
		return err
	}

	if err := controllerutil.SetControllerReference(r.kataConfig, removePeerPodsConfigDaemonSet, r.Scheme); err != nil {
		r.Log.Error(err, "Failed setting ControllerReference for peer-pods configuration removal DaemonSet")
		return err
	}

	foundPeerPodsConfigDaemonSet := &appsv1.DaemonSet{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: removePeerPodsConfigDaemonSet.Name, Namespace: removePeerPodsConfigDaemonSet.Namespace}, foundPeerPodsConfigDaemonSet)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating a new peer-pods configuration removal daemonset", "removePeerPodsConfigDaemonSet.Namespace", removePeerPodsConfigDaemonSet.Namespace, "removePeerPodsConfigDaemonSet.Name", removePeerPodsConfigDaemonSet.Name)
			err = r.Client.Create(context.TODO(), removePeerPodsConfigDaemonSet)
			if err != nil {
				r.Log.Error(err, "error when creating peer-pods configuration daemonset")
				return err
			}
		} else {
			r.Log.Error(err, "could not get peer-pods configuration daemonset, try again")
			return err
		}
	} else {
		r.Log.Info("Updating peer-pods configuration daemonset", "removePeerPodsConfigDaemonSet.Namespace", removePeerPodsConfigDaemonSet.Namespace, "removePeerPodsConfigDaemonSet.Name", removePeerPodsConfigDaemonSet.Name)
		err = r.Client.Update(context.TODO(), removePeerPodsConfigDaemonSet)
		if err != nil {
			r.Log.Error(err, "error when updating peer-pods configuration daemonset")
			return err
		}
	}
	return nil
}

func (r *KataConfigOpenShiftReconciler) processKataConfigInstallRequestDaemonSet() (ctrl.Result, error) {
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

	// Label nodes with node-role.kubernetes.io/kata-oc
	// This will be used by all DaemonSets
	_, err := r.updateNodeLabels()
	if err != nil {
		if k8serrors.IsConflict(err) {
			return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
		} else {
			return ctrl.Result{Requeue: true}, nil
		}
	}

	//Get pull-secret from openshift-config ns and save it as auth-json-secret in our ns
	//This will be used by the podvm image provider to pull the pause image for embedding
	err = r.createAuthJsonSecret()
	if err != nil {
		r.Log.Info("Error in creating auth-json-secret", "err", err)
		return ctrl.Result{}, err
	}

	// If peerpods are enabled create the daemonset that will copy the related config files
	if r.kataConfig.Spec.EnablePeerPods {
		err := r.addPeerPodsConfigDaemonSet()
		if err != nil {
			r.Log.Error(err, "Adding peerpods configs daemonset failed")
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	}

	// Add finalizer for this CR
	if !contains(r.kataConfig.GetFinalizers(), kataConfigFinalizer) {
		if err := r.addFinalizer(); err != nil {
			return ctrl.Result{}, err
		}
	}

	// TODO: Logic that checks failed installation
	// TODO: Check the return value and restart daemonset if a new ConfigMap is created

	kataInstallDaemonSet, err := r.daemonSetForKataInstall(InstallKata)
	if err != nil {
		r.Log.Error(err, "Failed getting DaemonSet for Kata installation")
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}
	if err := controllerutil.SetControllerReference(r.kataConfig, kataInstallDaemonSet, r.Scheme); err != nil {
		r.Log.Error(err, "Failed setting ControllerReference for kata installation DaemonSet")
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	foundKataDaemonSet := &appsv1.DaemonSet{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: kataInstallDaemonSet.Name, Namespace: kataInstallDaemonSet.Namespace}, foundKataDaemonSet)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating a new kata installation daemonset", "kataInstallDaemonSet.Namespace", kataInstallDaemonSet.Namespace, "kataInstallDaemonSet.Name", kataInstallDaemonSet.Name)
			err = r.Client.Create(context.TODO(), kataInstallDaemonSet)
			if err != nil {
				r.Log.Error(err, "Error when creating kata installation daemonset")
				return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
			}
			// If the daemonset just got created we change the progress condition and label the nodes with the initial value
			r.setInProgressConditionToInstalling()

			kataSelector, err := r.getKataConfigNodeSelectorAsSelector()
			if err != nil {
				r.Log.Error(err, "Skipping node labeling due to missing Kata node selector", "label", KataWaitingToInstall)
			} else {
				err = labelNodes(r.Client, kataSelector, map[string]string{kataInstallationDaemonSetLabel: string(KataWaitingToInstall)})
				if err != nil {
					r.Log.Error(err, "Could not add label to kata nodes", "label", KataWaitingToInstall, "selector", kataSelector)
				}
			}
		} else {
			r.Log.Error(err, "Could not get kata installation daemonset, try again")
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	} else {
		r.Log.Info("Updating kata installation daemonset", "kataInstallDaemonSet.Namespace", kataInstallDaemonSet.Namespace, "kataInstallDaemonSet.Name", kataInstallDaemonSet.Name)
		err = r.Client.Update(context.TODO(), kataInstallDaemonSet)
		if err != nil {
			r.Log.Error(err, "error when updating kata installation daemonset")
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	}

	// Check whether the installation is still in progress or finished
	installInProgress, err := r.isKataInstallDaemonSetInstalling()
	if err != nil {
		r.Log.Error(err, "Error getting installation status")
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	if installInProgress && r.getInProgressConditionValue() == corev1.ConditionFalse {
		r.setInProgressConditionToUpdating()
	}

	err = r.updateStatusDaemonSet(InstallKata)
	if err != nil {
		r.Log.Error(err, "Error updating KataConfig.status")
	}

	// Basically the same as in processKataConfigInstallRequest
	if !installInProgress {
		res, err := r.postKataInstallation()
		if res != nil {
			return *res, err
		}
		if err != nil {
			// Give sometime for the error to go away before reconciling again
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	} else {
		// NodeEventHandler should trigger reconciliation on label changes
		// so no need for that.
		r.Log.Info("Waiting for kata installation DaemonSet to finish", "daemonSet", kataInstallDaemonSet.Name)
		nodes, _ := r.isNodeRebootRequired()
		for _, node := range nodes {
			r.Log.Info("node must be rebooted", "node", node.Name)
		}
	}

	return ctrl.Result{}, nil
}

func (r *KataConfigOpenShiftReconciler) addPeerPodsConfigDaemonSet() error {
	peerPodsConfigDaemonSet, err := r.daemonSetForPeerPodsConfig(InstallKata)
	if err != nil {
		return err
	}

	if err := controllerutil.SetControllerReference(r.kataConfig, peerPodsConfigDaemonSet, r.Scheme); err != nil {
		r.Log.Error(err, "Failed setting ControllerReference for peer-pods configuration DaemonSet")
		return err
	}

	foundPeerPodsConfigDaemonSet := &appsv1.DaemonSet{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: peerPodsConfigDaemonSet.Name, Namespace: peerPodsConfigDaemonSet.Namespace}, foundPeerPodsConfigDaemonSet)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating a new peer-pods configuration installation daemonset", "peerPodsConfigDaemonSet.Namespace", peerPodsConfigDaemonSet.Namespace, "peerPodsConfigDaemonSet.Name", peerPodsConfigDaemonSet.Name)
			err = r.Client.Create(context.TODO(), peerPodsConfigDaemonSet)
			if err != nil {
				r.Log.Error(err, "error when creating peer-pods configuration daemonset")
				return err
			}
		} else {
			r.Log.Error(err, "could not get peer-pods configuration daemonset, try again")
			return err
		}
	} else {
		r.Log.Info("Updating peer-pods configuration daemonset", "peerPodsConfigDaemonSet.Namespace", peerPodsConfigDaemonSet.Namespace, "peerPodsConfigDaemonSet.Name", peerPodsConfigDaemonSet.Name)
		err = r.Client.Update(context.TODO(), peerPodsConfigDaemonSet)
		if err != nil {
			r.Log.Error(err, "error when updating peer-pods configuration daemonset")
			return err
		}
	}

	// TODO: Check for errors

	return nil
}

// daemonSetForPeerPodsConfig creates a DaemonSet for peer-pods configuration.
// The Daemonset will copy or remove the peer-pods configuration files based on the given action.
func (r *KataConfigOpenShiftReconciler) daemonSetForPeerPodsConfig(action KataDaemonSetAction) (*appsv1.DaemonSet, error) {
	cliImageString, err := GetImageForComponent(cliImageName, r.Client)
	if err != nil {
		r.Log.Info("couldn't get image", "err", err)
		return nil, err
	}

	var (
		runPrivileged       = true
		runAsUser     int64 = 0
		nodeSelector        = r.getNodeSelectorAsMap()
	)

	name := peerPodsConfigInstallDaemonSetName + "-" + string(action)
	daemonsetLabelSelectors := map[string]string{
		"name": name,
	}

	volumeMounts := r.volumeMountsForRegistries()
	volumes := r.volumesForRegistries()

	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: OperatorNamespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: daemonsetLabelSelectors,
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
					Labels: daemonsetLabelSelectors,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "default",
					NodeSelector:       nodeSelector,
					HostPID:            true,
					Containers: []corev1.Container{
						{
							Name:            "config-sync",
							Image:           daemonSetImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								Privileged: &runPrivileged,
								RunAsUser:  &runAsUser,
							},
							Command: []string{"/bin/bash", "/scripts/osc-configs-script.sh"},
							Args:    []string{string(action)},
							Env: []corev1.EnvVar{
								{
									Name: "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name:  "CLI_IMAGE",
									Value: cliImageString,
								},
							},
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}, nil
}

// daemonSetForKataInstall creates a DaemonSet for installing or uninstalling Kata based on the specified action.
// It uses two container images: one for the Kata binaries (extensionImageString) and another for kubectl/cli tools (cliImageString).
// The DaemonSet's Pod contains three containers: one installs the binaries, one modifies the node labels, and one sets the runtime log level.
func (r *KataConfigOpenShiftReconciler) daemonSetForKataInstall(action KataDaemonSetAction) (*appsv1.DaemonSet, error) {
	// This image contains the binaries for kata-containers
	extensionImageString, err := GetImageForComponent(rhelCoreOsExtensionsImageName, r.Client)
	if err != nil {
		r.Log.Error(err, "couldn't get extension image")
		return nil, err
	}

	// This image contains kubectl and makes changing the node labels easier
	cliImageString, err := GetImageForComponent(cliImageName, r.Client)
	if err != nil {
		r.Log.Error(err, "couldn't get cli image")
		return nil, err
	}

	// Because we use chroot and modify the node we need root priviliges
	var (
		runPrivileged       = true
		runAsUser     int64 = 0
		nodeSelector        = r.getNodeSelectorAsMap()
	)

	name := kataInstallDaemonSetName + "-" + string(action)
	daemonsetLabelSelectors := map[string]string{
		"name": name,
	}

	logLevel := r.kataConfig.Spec.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}

	volumeMounts := r.volumeMountsForRegistries()
	volumes := r.volumesForRegistries()

	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: OperatorNamespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: daemonsetLabelSelectors,
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
					Labels: daemonsetLabelSelectors,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "default",
					NodeSelector:       nodeSelector,
					HostPID:            true,
					Containers: []corev1.Container{
						{
							Name:            "kata-install",
							Image:           daemonSetImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								Privileged: &runPrivileged,
								RunAsUser:  &runAsUser,
							},
							Command:      []string{"/bin/bash", "/scripts/osc-kata-install.sh"},
							Args:         []string{string(action)},
							VolumeMounts: volumeMounts,
							Env: []corev1.EnvVar{
								{
									Name: "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name:  "EXTENSION_IMAGE",
									Value: extensionImageString,
								},
								{
									Name:  "CLI_IMAGE",
									Value: cliImageString,
								},
								{
									Name:  "NODE_LABEL",
									Value: kataInstallationDaemonSetLabel,
								},
							},
						},
						{
							Name:            "set-log-level",
							Image:           daemonSetImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								Privileged: &runPrivileged,
								RunAsUser:  &runAsUser,
							},
							Command: []string{"/bin/bash", "/scripts/osc-log-level.sh"},
							Args:    []string{string(action), logLevel},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "host-root",
									MountPath: "/host",
								},
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}, nil
}

func (r *KataConfigOpenShiftReconciler) volumesForRegistries() []corev1.Volume {
	var defaultMode int32 = 0600

	return []corev1.Volume{
		{
			Name: "host-root",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/",
				},
			},
		},
		{
			Name: "auth-json-volume",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "auth-json-secret",
					DefaultMode: &defaultMode,
				},
			},
		},
		{
			Name: "registries-conf",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/etc/containers/registries.conf",
					Type: HostPathTypePtr(corev1.HostPathFileOrCreate),
				},
			},
		},
		{
			Name: "registries-conf-d",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/etc/containers/registries.conf.d",
					Type: HostPathTypePtr(corev1.HostPathDirectoryOrCreate),
				},
			},
		},
		{
			Name: "containers-auth",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/var/lib/kubelet/config.json",
					Type: HostPathTypePtr(corev1.HostPathFile),
				},
			},
		},
		{
			Name: "containers-policy",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/etc/containers/policy.json",
					Type: HostPathTypePtr(corev1.HostPathFileOrCreate),
				},
			},
		},
	}
}

func (r *KataConfigOpenShiftReconciler) volumeMountsForRegistries() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "host-root",
			MountPath: "/host",
		},
		{
			Name:      "auth-json-volume",
			MountPath: "/tmp/regauth",
			ReadOnly:  true,
		},
		{
			Name:      "registries-conf",
			MountPath: "/etc/containers/registries.conf",
			ReadOnly:  true,
		},
		{
			Name:      "registries-conf-d",
			MountPath: "/etc/containers/registries.conf.d",
			ReadOnly:  true,
		},
		{
			Name:      "containers-auth",
			MountPath: "/root/.config/containers/auth.json",
			ReadOnly:  true,
		},
		{
			Name:      "containers-policy",
			MountPath: "/etc/containers/policy.json",
			ReadOnly:  true,
		},
	}
}

// kataInstallationDaemonSetAllNodeInState checks if all nodes match the given Kata DaemonSet installation state.
func (r *KataConfigOpenShiftReconciler) kataInstallationDaemonSetAllNodeInState(matchingState KataInstallationDaemonSetState) (bool, error) {
	return r.isKataNodePoolInState(kataInstallationDaemonSetLabel, string(matchingState))
}

// isKataNodePoolInState checks whether all nodes in the Kata node pool are in the specified state.
//
// It takes a label name and a target state value as input.
// The function retrieves nodes matching the Kata node selector,
// then verifies that each node has the specified label set to the target state.
// Returns true if all nodes match the state, false otherwise.
func (r *KataConfigOpenShiftReconciler) isKataNodePoolInState(labelName string, matchingState string) (bool, error) {
	nodes, err := r.getNodesWithLabels(r.getNodeSelectorAsMap())
	if err != nil {
		r.Log.Error(err, "Getting Node List failed")
		return false, err
	}

	matchedNodeCount := 0

	for _, node := range nodes.Items {
		currentState := node.Labels[labelName]
		if currentState == matchingState {
			matchedNodeCount++
		}
	}

	return matchedNodeCount == len(nodes.Items), nil
}

func (r *KataConfigOpenShiftReconciler) isKataUninstallDaemonSetUninstalling() (bool, error) {
	allNodeUninstalled, err := r.kataInstallationDaemonSetAllNodeInState(KataUninstalled)
	if err != nil {
		return false, err
	}

	return !allNodeUninstalled, nil
}

func (r *KataConfigOpenShiftReconciler) isKataInstallDaemonSetInstalling() (bool, error) {
	allNodeInstalled, err := r.kataInstallationDaemonSetAllNodeInState(KataInstalled)
	if err != nil {
		return false, err
	}

	return !allNodeInstalled, nil
}

// isNodeRebootRequired checks if any nodes require a reboot based on their labels.
// It returns a list of nodes that need a reboot and an error if any occurs.
func (r *KataConfigOpenShiftReconciler) isNodeRebootRequired() ([]corev1.Node, error) {
	nodes, err := r.getNodesWithLabels(r.getNodeSelectorAsMap())
	if err != nil {
		r.Log.Error(err, "Getting Node List failed")
		return nil, err
	}

	result := make([]corev1.Node, 0)

	for _, node := range nodes.Items {
		currentState, ok := node.Labels[kataInstallationDaemonSetLabel]
		if ok {
			if currentState == string(KataWaitingForReboot) {
				result = append(result, node)
			}
		}
	}

	return result, nil
}

// updateStatusDaemonSet updates the status of DaemonSet based on the given action.
// It fetches nodes with appropriate labels, clears existing node status lists,
// updates node count, and move nodes in the status list based on the action.
func (r *KataConfigOpenShiftReconciler) updateStatusDaemonSet(action KataDaemonSetAction) error {

	nodes, err := r.getNodesWithLabels(r.getNodeSelectorAsMap())
	if err != nil {
		return err
	}

	r.clearNodeStatusLists()

	r.kataConfig.Status.KataNodes.NodeCount = len(nodes.Items)

	var putNodeOnStatusList func(*corev1.Node) error

	if action == InstallKata {
		putNodeOnStatusList = r.putNodeOnStatusListDaemonSetInstall
	} else {
		// Assume that the other action is UninstallKata
		putNodeOnStatusList = r.putNodeOnStatusListDaemonSetUninstall
	}

	for _, node := range nodes.Items {
		e := putNodeOnStatusList(&node)
		if e != nil {
			err = e
		}
	}

	r.kataConfig.Status.KataNodes.ReadyNodeCount = len(r.kataConfig.Status.KataNodes.Installed)

	return err
}

func (r *KataConfigOpenShiftReconciler) putNodeOnStatusListDaemonSetInstall(node *corev1.Node) error {
	// TODO: Add FailedToInstall nodes
	kataInstallationState := node.Labels[kataInstallationDaemonSetLabel]

	switch kataInstallationState {
	case string(KataWaitingToInstall):
		r.kataConfig.Status.KataNodes.WaitingToInstall = append(r.kataConfig.Status.KataNodes.WaitingToInstall, node.GetName())
	case string(KataInstalling):
		r.kataConfig.Status.KataNodes.Installing = append(r.kataConfig.Status.KataNodes.Installing, node.GetName())
		// rpm-ostree changes applied after reboot
	case string(KataWaitingForReboot):
		r.kataConfig.Status.KataNodes.Installing = append(r.kataConfig.Status.KataNodes.Installing, node.GetName())
	case string(KataInstalled):
		r.kataConfig.Status.KataNodes.Installed = append(r.kataConfig.Status.KataNodes.Installed, node.GetName())
	default:
		r.kataConfig.Status.KataNodes.WaitingToInstall = append(r.kataConfig.Status.KataNodes.WaitingToInstall, node.GetName())
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) putNodeOnStatusListDaemonSetUninstall(node *corev1.Node) error {
	kataInstallationState := node.Labels[kataInstallationDaemonSetLabel]

	switch kataInstallationState {
	case string(KataWaitingToUninstall):
		r.kataConfig.Status.KataNodes.WaitingToUninstall = append(r.kataConfig.Status.KataNodes.WaitingToUninstall, node.GetName())
	case string(KataUninstalling):
		r.kataConfig.Status.KataNodes.Uninstalling = append(r.kataConfig.Status.KataNodes.Uninstalling, node.GetName())
		// rpm-ostree changes applied after reboot
	case string(KataWaitingForReboot):
		r.kataConfig.Status.KataNodes.Uninstalling = append(r.kataConfig.Status.KataNodes.Uninstalling, node.GetName())
	case string(KataUninstalled):
	default:
		r.kataConfig.Status.KataNodes.WaitingToUninstall = append(r.kataConfig.Status.KataNodes.WaitingToUninstall, node.GetName())
	}

	return nil
}

func (r *KataConfigOpenShiftReconciler) unlabelNodesDaemonSet(nodeSelector labels.Selector) error {
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
			delete(node.Labels, kataInstallationDaemonSetLabel)
			delete(node.Labels, peerPodsConfigDaemonSetLabel)
			err := r.Client.Update(context.TODO(), &node)
			if err != nil {
				r.Log.Error(err, "Error when removing labels from node", "node", node)
				return err
			}
		}
	}
	return nil
}
