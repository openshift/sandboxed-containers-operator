package controllers

import (
	"context"
	"os"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	// import apps/v1 for Deployment
	appsv1 "k8s.io/api/apps/v1"
	// import metav1 for ObjectMeta
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// import admissionregistration/v1 for MutatingWebhookConfiguration
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

const (

	// Define webhook name
	webhookName = "mwebhook.peerpods.io"

	// Define webhook deployment name
	webhookDeploymentName = "peer-pods-webhook"

	// Define webhook service name
	webhookSvcName       = "peer-pods-webhook-svc"
	webhookSvcSecretName = "peer-pods-webhook-svc-secret"
	webhookDefaultImage  = "quay.io/confidential-containers/peer-pods-webhook:latest"

	// Define webhook mutating config name
	webhookConfigName = "mutating-webhook-configuration"
)

// Method to create the mutating webhook service
func (r *KataConfigOpenShiftReconciler) createMutatingWebhookService() error {

	// Define webhook service port
	webhookServicePort := int32(443)

	// Define webhook service target port
	webhookServiceTargetPort := intstr.FromInt(9443)

	// Define webhook service type
	webhookServiceType := corev1.ServiceTypeClusterIP

	// Define webhook service selector
	webhookServiceSelector := map[string]string{
		"app": "peer-pods-webhook",
	}

	webhookSvcNamespace := os.Getenv("PEERPODS_NAMESPACE")

	// Define Annotation for webhook service
	webhookServiceAnnotations := map[string]string{
		// annotation to the service to use the secret created by the operator
		// to serve the webhook service over TLS
		"service.beta.openshift.io/serving-cert-secret-name": webhookSvcSecretName,
	}

	// Create webhook service
	webhookService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookSvcName,
			Namespace: webhookSvcNamespace,
			// Add annotations to the service
			Annotations: webhookServiceAnnotations,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       webhookSvcName,
					Port:       webhookServicePort,
					TargetPort: webhookServiceTargetPort,
				},
			},
			Selector:  webhookServiceSelector,
			ClusterIP: "",
			Type:      webhookServiceType,
		},
	}

	// Create webhook service
	if err := r.Client.Create(context.Background(), webhookService); err != nil {
		// Check if the webhook service already exists
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}
	}
	r.Log.Info("created peerpods mutating webhook service")
	return nil

}

// Method to create the mutating webhook deployment
func (r *KataConfigOpenShiftReconciler) createMutatingWebhookDeployment() error {

	// Define webhook deployment namespace
	webhookDeploymentNamespace := os.Getenv("PEERPODS_NAMESPACE")

	// Define webhook deployment labels
	webhookDeploymentLabels := map[string]string{
		"app": "peer-pods-webhook",
	}

	// Define webhook deployment replicas
	webhookDeploymentReplicas := int32(2)

	// Define webhook deployment strategy
	webhookDeploymentStrategy := appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
	}

	// Define webhook deployment pod template labels
	webhookDeploymentPodTemplateLabels := map[string]string{
		"app": "peer-pods-webhook",
	}

	// Disable privilege escalation
	allowPrivilegeEscalation := false

	// Run as non-root user
	runAsNonRoot := true

	// Get the webhook image from environment variable. If environment variable is not set, use the default image
	webhookImage := os.Getenv("RELATED_IMAGE_PEERPODS_WEBHOOK")
	if webhookImage == "" {
		webhookImage = webhookDefaultImage
	}

	// Add volume default mode
	defaultMode := int32(420)

	// Define webhook deployment pod template spec
	webhookDeploymentPodTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: webhookDeploymentPodTemplateLabels,
		},
		Spec: corev1.PodSpec{
			// Add TLS secret volumes
			Volumes: []corev1.Volume{
				{
					Name: "webhook-cert",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  webhookSvcSecretName,
							DefaultMode: &defaultMode,
							// Add items
							Items: []corev1.KeyToPath{
								{
									Key:  "tls.crt",
									Path: "tls.crt",
								},
								{
									Key:  "tls.key",
									Path: "tls.key",
								},
							},
						},
					},
				},
			},

			// Define security context
			SecurityContext: &corev1.PodSecurityContext{
				// Run as nonroot user
				RunAsNonRoot: &runAsNonRoot,
			},
			Containers: []corev1.Container{
				{
					Name: webhookDeploymentName,
					// Define security context
					SecurityContext: &corev1.SecurityContext{
						// Disable privilege escalation
						AllowPrivilegeEscalation: &allowPrivilegeEscalation,
					},
					Image:           webhookImage,
					ImagePullPolicy: corev1.PullAlways,
					// Define Command
					Command: []string{
						"/manager",
					},
					// Define args
					Args: []string{
						" --leader-elect",
					},
					// Define 3 env variables
					Env: []corev1.EnvVar{
						{
							Name:  "PEERPODS_NAMESPACE",
							Value: webhookDeploymentNamespace,
						},
						{
							Name:  "TARGET_RUNTIMECLASS",
							Value: peerpodsRuntimeClassName,
						},
						{
							Name:  "POD_VM_EXTENDED_RESOURCE",
							Value: "kata.peerpods.io/vm",
						},
					},
					// Define resources
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"cpu":    resource.MustParse("10m"),
							"memory": resource.MustParse("64Mi"),
						},
						Limits: corev1.ResourceList{
							"cpu":    resource.MustParse("500m"),
							"memory": resource.MustParse("128Mi"),
						},
					},
					// Add volume mounts
					VolumeMounts: []corev1.VolumeMount{
						{
							Name: "webhook-cert",
							// This is the default path for webhook servers created using
							// controller-runtime
							MountPath: "/tmp/k8s-webhook-server/serving-certs",
							ReadOnly:  true,
						},
					},
				},
			},
		},
	}

	// Define webhook deployment
	webhookDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookDeploymentName,
			Namespace: webhookDeploymentNamespace,
			Labels:    webhookDeploymentLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &webhookDeploymentReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: webhookDeploymentLabels,
			},
			Strategy: webhookDeploymentStrategy,
			Template: webhookDeploymentPodTemplateSpec,
		},
	}

	// Create webhook deployment
	if err := r.Client.Create(context.Background(), webhookDeployment); err != nil {
		// Check if the webhook deployment already exists
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}
	}
	r.Log.Info("created peerpods mutating webhook deployment")
	return nil
}

// Method to create the mutating webhook config
func (r *KataConfigOpenShiftReconciler) createMutatingWebhookConfig() error {

	// Define webhook path
	webhookPath := "/mutate-v1-pod"

	// Add failure policy
	failurePolicy := admissionregistrationv1.Fail

	// Add side effect
	sideEffect := admissionregistrationv1.SideEffectClassNone

	// Add list of namespaces to exclude
	namespacesToExclude := []string{
		"peer-pods-webhook-system",
		"openshift-sandboxed-containers-operator",
		"openshift",
		"openshift-apiserver",
		"openshift-apiserver-operator",
		"openshift-authentication",
		"openshift-authentication-operator",
		"openshift-cloud-controller-manager",
		"openshift-cloud-controller-manager-operator",
		"openshift-cloud-credential-operator",
		"openshift-cloud-network-config-controller",
		"openshift-cluster-csi-drivers",
		"openshift-cluster-machine-approver",
		"openshift-cluster-node-tuning-operator",
		"openshift-cluster-samples-operator",
		"openshift-cluster-storage-operator",
		"openshift-cluster-version",
		"openshift-config",
		"openshift-config-managed",
		"openshift-config-operator",
		"openshift-console",
		"openshift-console-operator",
		"openshift-console-user-settings",
		"openshift-controller-manager",
		"openshift-controller-manager-operator",
		"openshift-dns",
		"openshift-dns-operator",
		"openshift-etcd",
		"openshift-etcd-operator",
		"openshift-host-network",
		"openshift-image-registry",
		"openshift-infra",
		"openshift-ingress",
		"openshift-ingress-canary",
		"openshift-ingress-operator",
		"openshift-insights",
		"openshift-kni-infra",
		"openshift-kube-apiserver",
		"openshift-kube-apiserver-operator",
		"openshift-kube-controller-manager",
		"openshift-kube-scheduler",
		"openshift-kube-scheduler-operator",
		"openshift-kube-storage-version-migrator",
		"openshift-kube-storage-version-migrator-operator",
		"openshift-machine-api",
		"openshift-machine-config-operator",
		"openshift-marketplace",
		"openshift-monitoring",
		"openshift-multus",
		"openshift-network-diagnostics",
		"openshift-network-operator",
		"openshift-node",
		"openshift-nutanix-infra",
		"openshift-oauth-apiserver",
		"openshift-openstack-infra",
		"openshift-operator-lifecycle-manager",
		"openshift-operators",
		"openshift-ovirt-infra",
		"openshift-ovn-kubernetes",
		"openshift-route-controller-manager",
		"openshift-service-ca",
		"openshift-service-ca-operator",
		"openshift-user-workload-monitoring",
		"openshift-vsphere-infra",
		"kube-system",
		"kube-node-lease",
	}

	webhookSvcNamespace := os.Getenv("PEERPODS_NAMESPACE")

	// Add annotations to inject ca bundle into the webhook config
	annotations := map[string]string{
		"service.beta.openshift.io/inject-cabundle": "true",
	}

	// Add mutating webhook configuration
	mutatingWebhookConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: webhookConfigName,
			// Add annotations
			Annotations: annotations,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: webhookName,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Name:      webhookSvcName,
						Namespace: webhookSvcNamespace,
						Path:      &webhookPath,
					},
				},
				// Add rules
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					},
				},
				// Add failure policy
				FailurePolicy: &failurePolicy,
				// Add side effects
				SideEffects: &sideEffect,
				// Add admission review versions
				AdmissionReviewVersions: []string{"v1"},
				// Add namespace selector using matchExpressions
				NamespaceSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "kubernetes.io/metadata.name",
							Operator: metav1.LabelSelectorOpNotIn,
							// Take values from a predefined list
							Values: namespacesToExclude,
						},
					},
				},
			},
		},
	}

	// Create MutatingWebhookConfiguration object
	if err := r.Client.Create(context.Background(), mutatingWebhookConfig); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}
	}
	r.Log.Info("created peerpods mutating webhook configuration")
	return nil
}

// Method to delete the Mutating Webhook Deployment
func (r *KataConfigOpenShiftReconciler) deleteMutatingWebhookDeployment() error {

	webhookDeploymentNamespace := os.Getenv("PEERPODS_NAMESPACE")

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookDeploymentName,
			Namespace: webhookDeploymentNamespace,
		},
	}
	// Delete the deployment
	err := r.Client.Delete(context.Background(), deployment)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	}
	r.Log.Info("deleted peerpods mutating webhook deployment")
	return nil
}

// Method to delete the Mutating Webhook Service
func (r *KataConfigOpenShiftReconciler) deleteMutatingWebhookService() error {

	webhookSvcNamespace := os.Getenv("PEERPODS_NAMESPACE")

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookSvcName,
			Namespace: webhookSvcNamespace,
		},
	}
	// Delete the service
	err := r.Client.Delete(context.Background(), service)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	}
	r.Log.Info("deleted peerpods mutating webhook service")
	return nil
}

// Method to delete the Mutating Webhook Configuration
func (r *KataConfigOpenShiftReconciler) deleteMutatingWebhookConfig() error {
	mutatingWebhookConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: webhookConfigName,
		},
	}
	// Delete the mutating webhook configuration
	err := r.Client.Delete(context.Background(), mutatingWebhookConfig)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	}
	r.Log.Info("deleted peerpods mutating webhook configuration")
	return nil
}
