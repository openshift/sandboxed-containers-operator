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
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// https://github.com/kata-containers/kata-containers/blob/main/tools/packaging/kata-deploy/runtimeclasses/kata-remote.yaml#L7
	peerpodsRuntimeClassName        = "kata-remote"
	peerpodsRuntimeClassCpuOverhead = "0.25"
	peerpodsRuntimeClassMemOverhead = "120Mi"
	// cloud-api-adaptor (CAA) daemonset name
	caaDsName = "osc-caa-ds"
)

func MountProgagationRef(mode corev1.MountPropagationMode) *corev1.MountPropagationMode {
	return &mode
}

func (r *KataConfigOpenShiftReconciler) processDaemonsetForCAA(ds *appsv1.DaemonSet) *appsv1.DaemonSet {
	var (
		runPrivileged                = true
		runAsUser              int64 = 0
		defaultMode            int32 = 0600
		sshSecretOptional            = true
		authJsonSecretOptional       = true
		nodeSelector                 = r.getNodeSelectorAsMap()
	)

	dsLabelSelectors := map[string]string{
		"name": caaDsName,
	}

	imageString := os.Getenv("RELATED_IMAGE_CAA")
	if imageString == "" {
		r.Log.Info("RELATED_IMAGE_CAA env var is unset or empty, cloud-api-adaptor pods will not run")
	}

	ds.TypeMeta = metav1.TypeMeta{
		APIVersion: "apps/v1",
		Kind:       "DaemonSet",
	}

	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: dsLabelSelectors,
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
				Labels: dsLabelSelectors,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: "default",
				NodeSelector:       nodeSelector,
				HostNetwork:        true,
				Containers: []corev1.Container{
					{
						Name:            "caa-pod",
						Image:           imageString,
						ImagePullPolicy: "Always",
						SecurityContext: &corev1.SecurityContext{
							// TODO - do we really need to run as root?
							Privileged: &runPrivileged,
							RunAsUser:  &runAsUser,
						},
						Command: []string{"/usr/local/bin/entrypoint.sh"},
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
						EnvFrom: []corev1.EnvFromSource{
							{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "peer-pods-secret",
									},
								},
							},
							{
								ConfigMapRef: &corev1.ConfigMapEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "peer-pods-cm",
									},
								},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "auth-json-volume",
								MountPath: "/root/containers/",
								ReadOnly:  true,
							},
							{
								Name:      "ssh",
								MountPath: "/root/.ssh",
								ReadOnly:  true,
							},
							{
								MountPath: "/run/peerpod",
								Name:      "pods-dir",
							},
							{
								MountPath:        "/run/netns",
								MountPropagation: MountProgagationRef(corev1.MountPropagationHostToContainer),
								Name:             "netns",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "auth-json-volume",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  "auth-json-secret",
								DefaultMode: &defaultMode,
								Optional:    &authJsonSecretOptional,
							},
						},
					},
					{
						Name: "ssh",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  "ssh-key-secret",
								DefaultMode: &defaultMode,
								Optional:    &sshSecretOptional,
							},
						},
					},
					{
						Name: "pods-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/run/peerpod",
							},
						},
					},
					{
						Name: "netns",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/run/netns",
							},
						},
					},
				},
			},
		},
	}

	return ds
}

// Handles provider specific parts of the CAA Ds
// Modifies the DaemonSet if needed
func (r *KataConfigOpenShiftReconciler) processProviderConfigCAA(ds *appsv1.DaemonSet) error {
	r.Log.Info("Getting cloud provider from infra")
	provider, err := getCloudProviderFromInfra(r.Client)
	if err != nil {
		return fmt.Errorf("failed to get cloud provider from infra: %w", err)
	}

	switch provider {
	case IBMCloudProvider:
		var expSecs int64 = 3600
		vaultTokenVolume := corev1.Volume{
			Name: "vault-token",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Path:              "vault-token",
								ExpirationSeconds: &expSecs,
								Audience:          "iam",
							},
						},
					},
				},
			},
		}

		vaultTokenVolumeMount := corev1.VolumeMount{
			MountPath: "/var/run/secrets/tokens",
			Name:      "vault-token",
		}

		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, vaultTokenVolume)
		for i := range ds.Spec.Template.Spec.Containers {
			container := &ds.Spec.Template.Spec.Containers[i]
			if container.Name == "caa-pod" {
				container.VolumeMounts = append(container.VolumeMounts, vaultTokenVolumeMount)
			}
		}

		return nil
	case AzureProvider:
		// Only add bound-sa-token volume for Azure federated identity if using STS flow
		// Check for all STS environment variables (set during OLM installation)
		hasAzureSTSCreds := os.Getenv("CLIENTID") != "" && os.Getenv("TENANTID") != "" && os.Getenv("SUBSCRIPTIONID") != ""
		if hasAzureSTSCreds {
			r.Log.Info("STS flow detected for Azure, adding bound-sa-token volume mount")
			boundSATokenVolume := corev1.Volume{
				Name: "bound-sa-token",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{
							{
								ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
									Path:     "token",
									Audience: "openshift",
								},
							},
						},
					},
				},
			}

			boundSATokenVolumeMount := corev1.VolumeMount{
				MountPath: "/var/run/secrets/openshift/serviceaccount",
				Name:      "bound-sa-token",
				ReadOnly:  true,
			}

			ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, boundSATokenVolume)
			for i := range ds.Spec.Template.Spec.Containers {
				container := &ds.Spec.Template.Spec.Containers[i]
				if container.Name == "caa-pod" {
					container.VolumeMounts = append(container.VolumeMounts, boundSATokenVolumeMount)
				}
			}
		}

		return nil
	default:
		return nil
	}
}

// Create the PeerPodConfig CRDs and misc configs required for peer-pods
func (r *KataConfigOpenShiftReconciler) enablePeerPodsMiscConfigs() error {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      caaDsName,
			Namespace: os.Getenv("PEERPODS_NAMESPACE"),
		},
	}

	// Create the CAA daemonset
	ds = r.processDaemonsetForCAA(ds)
	if err := r.processProviderConfigCAA(ds); err != nil {
		r.Log.Error(err, "Failed setting cloud provider specific configuration for cloud-api-adaptor DS")
		return err
	}
	r.Log.Info("Got CAA ds manifest", "ds", ds)

	if err := controllerutil.SetControllerReference(r.kataConfig, ds, r.Scheme); err != nil {
		r.Log.Error(err, "Failed setting ControllerReference for cloud-api-adaptor DS")
		return err
	}

	err := r.Client.Update(context.TODO(), ds)
	if err != nil && k8serrors.IsNotFound(err) {
		r.Log.Error(err, "cloud-api-adaptor daemonset doesn't exist. Creating")
		err = r.Client.Create(context.TODO(), ds)
		if err != nil {
			r.Log.Error(err, "failed to create cloud-api-adaptor daemonset")
			return err
		}
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
	err = r.createRuntimeClass(peerpodsRuntimeClassName, peerpodsRuntimeClassCpuOverhead, peerpodsRuntimeClassMemOverhead, "", peerpodsRuntimeClassName, nil)
	if err != nil {
		r.Log.Info("Error in creating kata remote runtimeclass", "err", err)
		return err
	}
	return nil
}

func (r *KataConfigOpenShiftReconciler) disablePeerPodsMiscConfigs() error {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      caaDsName,
			Namespace: os.Getenv("PEERPODS_NAMESPACE"),
		},
	}
	err := r.Client.Delete(context.TODO(), ds)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("cloud-api-adaptor daemonset was already deleted")
		} else {
			r.Log.Error(err, "error when deleting cloud-api-adaptor Daemonset, try again")
			return err
		}
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

func (r *KataConfigOpenShiftReconciler) deletePodVMImage() (*ctrl.Result, error) {
	// Handle podvm image deletion
	// Since we want to declaratively reach the final state, we need to reconcile when there are errors
	// as we want the system to give a chance of fixing the error.
	// For cases we don't want to reconcile, ie for ImageDeletedSuccessfully and UnsupportedPodVMImageProvider
	// we should just log the message and let the code continue without explicitly returning from the method

	// Following are returned statuses:
	// ImageDeletedSuccessfully
	// UnsupportedPodVMImageProvider
	// ImageDeletionFailed
	// RequeueNeeded
	// ImageDeletionStatusUnknown
	status, err := ImageDelete(r.Client)
	switch status {
	case ImageDeletedSuccessfully:
		r.setInProgressConditionToPodVMImageDeleted()
		r.Log.Info("PodVM Image deleted successfully")

	case UnsupportedPodVMImageProvider:
		r.setInProgressConditionToPodVMImageUnsupportedProvider()
		r.Log.Info("unsupported cloud provider, skipping image deletion")

	case RequeueNeeded:
		r.setInProgressConditionToPodVMImageDeleting()
		return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err

	case ImageDeletionFailed:
		r.setInProgressConditionToPodVMImageDeletionFailed()
		if err != nil {
			// We requeue only if there is an error.
			return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
		// If there's no error, log and continue
		r.Log.Info("Image deletion failed. Check logs for more details")

	case ImageDeletionStatusUnknown:
		r.setInProgressConditionToPodVMImageDeletionUnknown()
		return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err

	default:
		// For all other statuses, just log and continue
		r.Log.Info("PodVM Image deletion status and error", "status", status, "error", err)
	}
	return nil, nil
}

func (r *KataConfigOpenShiftReconciler) enablePeerPods() (*ctrl.Result, error) {
	//Get pull-secret from openshift-config ns and save it as auth-json-secret in our ns
	//This will be used by the podvm image provider to pull the pause image for embedding
	err := r.createAuthJsonSecret()
	if err != nil {
		r.Log.Info("Error in creating auth-json-secret", "err", err)
		return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	// Create the podvm image
	// Since we want to declaratively reach the final state, we need to reconcile when there are errors
	// as we want the system to give a chance of fixing the error.
	// For cases we don't want to reconcile, ie for ImageCreatedSuccessfully and UnsupportedPodVMImageProvider
	// we should just log the message and let the code continue without explicitly returning from the method

	// Following are the returned statuses:
	// ImageCreatedSuccessfully
	// UnsupportedPodVMImageProvider
	// ImageCreationFailed
	// RequeueNeeded
	// ImageCreationStatusUnknown

	status, err := ImageCreate(r.Client, r.kataConfig)
	switch status {
	case ImageCreatedSuccessfully:
		r.setInProgressConditionToPodVMImageCreated()
		r.Log.Info("PodVM Image created successfully")

	case UnsupportedPodVMImageProvider:
		r.setInProgressConditionToPodVMImageUnsupportedProvider()
		r.Log.Info("unsupported cloud provider, skipping image creation")

	case RequeueNeeded:
		r.setInProgressConditionToPodVMImageCreating()
		return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err

	case ImageCreationFailed:
		r.setInProgressConditionToPodVMImageCreationFailed()
		if err != nil {
			// We requeue only if there is an error.
			return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
		// If there's no error, log and continue
		r.Log.Info("Image creation failed. Check logs for more details")

	case ImageCreationStatusUnknown:
		r.setInProgressConditionToPodVMImageCreationUnknown()

		// Reconcile with error
		return &ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err

	default:
		// For all other statuses, just log and continue
		r.Log.Info("PodVM Image creation status and error", "status", status, "error", err)
	}

	err = r.enablePeerPodsMiscConfigs()
	if err != nil {
		r.Log.Info("Enabling peerpodconfig CR, runtimeclass etc", "err", err)
		// Give sometime for the error to go away before reconciling again
		return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err

	}

	// Reset the in progress condition
	r.resetInProgressCondition()

	return nil, nil
}

func (r *KataConfigOpenShiftReconciler) disablePeerPods() (*ctrl.Result, error) {
	// We are explicitly ignoring any errors as the various involved resources
	// can be removed manually if needed and this is not in the critical path
	// of operator functionality
	_ = r.disablePeerPodsMiscConfigs()

	// Handle podvm image deletion
	res, err := r.deletePodVMImage()
	if res != nil {
		return res, err
	}
	// FIXME : dead code, revisit deletePodVMImage() return paths
	if err != nil {
		return &ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	return nil, nil
}
