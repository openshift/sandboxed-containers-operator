package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	machineTypeAnnotation       = "io.katacontainers.config.hypervisor.machine_type"
	initdataAnnotation          = "io.katacontainers.config.hypervisor.cc_init_data"
	initdataWebhookName         = "peerpod-initdata-webhook"
	initdataWebhookCertSecret   = "controller-manager-service-cert"
	initdataWebhookServiceName  = "controller-manager-service"
	initdataWebhookPath         = "/mutate-peerpod-initdata"
)

type PeerPodInitdataInjector struct {
	decoder admission.Decoder
	client  client.Reader
}

func SetupInitdataWebhook(mgr ctrl.Manager) error {
	mgr.GetWebhookServer().Register(initdataWebhookPath, &webhook.Admission{
		Handler: &PeerPodInitdataInjector{
			decoder: admission.NewDecoder(mgr.GetScheme()),
			client:  mgr.GetAPIReader(),
		},
	})

	go func() {
		<-mgr.Elected()
		ctx := context.Background()
		log := ctrl.Log.WithName("initdata-webhook")

		c := mgr.GetClient()

		caBundle, err := getCABundle(ctx, mgr.GetAPIReader())
		if err != nil {
			log.Error(err, "failed to get CA bundle, skipping webhook configuration creation")
			return
		}

		if err := ensureWebhookConfiguration(ctx, c, caBundle); err != nil {
			log.Error(err, "failed to create MutatingWebhookConfiguration")
			return
		}
		log.Info("MutatingWebhookConfiguration created for peerpod initdata injection")
	}()

	return nil
}

func getCABundle(ctx context.Context, reader client.Reader) ([]byte, error) {
	secret := &corev1.Secret{}
	err := reader.Get(ctx, types.NamespacedName{
		Name:      initdataWebhookCertSecret,
		Namespace: OperatorNamespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("getting cert secret: %w", err)
	}

	ca, ok := secret.Data["olmCAKey"]
	if !ok {
		ca, ok = secret.Data["ca.crt"]
		if !ok {
			return nil, fmt.Errorf("no CA bundle found in secret %s", initdataWebhookCertSecret)
		}
	}
	return ca, nil
}

func ensureWebhookConfiguration(ctx context.Context, c client.Client, caBundle []byte) error {
	failurePolicy := admissionregistrationv1.Ignore
	sideEffects := admissionregistrationv1.SideEffectClassNone
	path := initdataWebhookPath
	port := int32(443)

	desired := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: initdataWebhookName,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name:                    "mpeerpod-initdata.kb.io",
				AdmissionReviewVersions: []string{"v1"},
				FailurePolicy:           &failurePolicy,
				SideEffects:             &sideEffects,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: caBundle,
					Service: &admissionregistrationv1.ServiceReference{
						Name:      initdataWebhookServiceName,
						Namespace: OperatorNamespace,
						Path:      &path,
						Port:      &port,
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					},
				},
			},
		},
	}

	existing := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err := c.Get(ctx, types.NamespacedName{Name: initdataWebhookName}, existing)
	if apierrors.IsNotFound(err) {
		return c.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Webhooks = desired.Webhooks
	return c.Update(ctx, existing)
}

func (h *PeerPodInitdataInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := h.decoder.Decode(req, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != peerpodsRuntimeClassName {
		return admission.Allowed("")
	}

	annotations := pod.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if _, ok := annotations[initdataAnnotation]; ok {
		return admission.Allowed("")
	}

	machineType := annotations[machineTypeAnnotation]

	if machineType == "" {
		cm, err := h.getPeerPodsCM(ctx)
		if err != nil {
			return admission.Allowed("")
		}
		switch cm.Data["CLOUD_PROVIDER"] {
		case "azure":
			machineType = strings.TrimSpace(cm.Data["AZURE_INSTANCE_SIZE"])
			if machineType == "" || isAzureConfidentialVMSize(machineType) {
				return admission.Allowed("")
			}
		// case "aws":
		default:
			return admission.Allowed("")
		}
	} else if isAzureConfidentialVMSize(machineType) {
		return admission.Allowed("")
	}

	annotations[initdataAnnotation] = defaultNonCCInitdata
	pod.SetAnnotations(annotations)

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (h *PeerPodInitdataInjector) getPeerPodsCM(ctx context.Context) (*corev1.ConfigMap, error) {
	ns := os.Getenv("PEERPODS_NAMESPACE")
	if ns == "" {
		ns = OperatorNamespace
	}

	cm := &corev1.ConfigMap{}
	err := h.client.Get(ctx, types.NamespacedName{Name: peerpodsCMName, Namespace: ns}, cm)
	return cm, err
}

func isAzureConfidentialVMSize(size string) bool {
	s := strings.ToLower(strings.TrimPrefix(strings.ToLower(size), "standard_"))
	return strings.HasPrefix(s, "dc") || strings.HasPrefix(s, "ec") || strings.HasPrefix(s, "ncc")
}

