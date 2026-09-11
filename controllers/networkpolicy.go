package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var hostNetworkAnnotation = map[string]string{
	"policy-group.network.openshift.io/host-network": "",
}

func (r *KataConfigOpenShiftReconciler) ensureNetworkPolicy(name string, annotations map[string]string, spec networkingv1.NetworkPolicySpec) error {
	ctx := context.TODO()
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: OperatorNamespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		np.Annotations = annotations
		np.Spec = spec
		return controllerutil.SetControllerReference(r.kataConfig, np, r.Scheme)
	})
	if err != nil {
		r.Log.Error(err, "failed to ensure NetworkPolicy", "name", name)
	}
	return err
}

func (r *KataConfigOpenShiftReconciler) deleteNetworkPolicy(name string) error {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: OperatorNamespace,
		},
	}
	err := r.Client.Delete(context.TODO(), np)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		r.Log.Error(err, "failed to delete NetworkPolicy", "name", name)
	}
	return err
}

func (r *KataConfigOpenShiftReconciler) deleteNetworkPolicies(names []string) error {
	for _, name := range names {
		if err := r.deleteNetworkPolicy(name); err != nil {
			return err
		}
	}
	return nil
}

// --- NP spec builders ---

func defaultDenySpec(podSelector map[string]string) networkingv1.NetworkPolicySpec {
	return networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{
			MatchLabels: podSelector,
		},
		PolicyTypes: []networkingv1.PolicyType{
			networkingv1.PolicyTypeIngress,
			networkingv1.PolicyTypeEgress,
		},
	}
}

func allowAllEgressSpec(podSelector map[string]string) networkingv1.NetworkPolicySpec {
	return networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{
			MatchLabels: podSelector,
		},
		PolicyTypes: []networkingv1.PolicyType{
			networkingv1.PolicyTypeEgress,
		},
		Egress: []networkingv1.NetworkPolicyEgressRule{{}},
	}
}

func allowDNSEgressSpec(podSelector map[string]string) networkingv1.NetworkPolicySpec {
	dnsPort := intstr.FromInt32(5353)
	tcp := corev1.ProtocolTCP
	udp := corev1.ProtocolUDP
	return networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{
			MatchLabels: podSelector,
		},
		PolicyTypes: []networkingv1.PolicyType{
			networkingv1.PolicyTypeEgress,
		},
		Egress: []networkingv1.NetworkPolicyEgressRule{
			{
				To: []networkingv1.NetworkPolicyPeer{
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "openshift-dns",
							},
						},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{Protocol: &tcp, Port: &dnsPort},
					{Protocol: &udp, Port: &dnsPort},
				},
			},
		},
	}
}

func allowIngressSpec(podSelector map[string]string, port int32) networkingv1.NetworkPolicySpec {
	p := intstr.FromInt32(port)
	tcp := corev1.ProtocolTCP
	return networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{
			MatchLabels: podSelector,
		},
		PolicyTypes: []networkingv1.PolicyType{
			networkingv1.PolicyTypeIngress,
		},
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{Protocol: &tcp, Port: &p},
				},
			},
		},
	}
}

// --- Per-operand network policy functions ---

// Kata Monitor: deny-all + metrics ingress (8443) + DNS egress
func (r *KataConfigOpenShiftReconciler) createKataMonitorNetworkPolicies() error {
	labels := map[string]string{"name": "openshift-sandboxed-containers-monitor"}

	if err := r.ensureNetworkPolicy("kata-monitor-deny-all", nil, defaultDenySpec(labels)); err != nil {
		return err
	}
	if err := r.ensureNetworkPolicy("kata-monitor-allow-metrics-ingress", hostNetworkAnnotation, allowIngressSpec(labels, 8443)); err != nil {
		return err
	}
	return r.ensureNetworkPolicy("kata-monitor-allow-dns-egress", nil, allowDNSEgressSpec(labels))
}

func (r *KataConfigOpenShiftReconciler) deleteKataMonitorNetworkPolicies() error {
	return r.deleteNetworkPolicies([]string{
		"kata-monitor-deny-all",
		"kata-monitor-allow-metrics-ingress",
		"kata-monitor-allow-dns-egress",
	})
}

// Peer Pods Webhook: deny-all + webhook ingress (9443) + metrics ingress (8443) + DNS egress + API server egress
func (r *KataConfigOpenShiftReconciler) createPeerPodsWebhookNetworkPolicies() error {
	labels := map[string]string{"app": "peer-pods-webhook"}

	if err := r.ensureNetworkPolicy("peer-pods-webhook-deny-all", nil, defaultDenySpec(labels)); err != nil {
		return err
	}
	if err := r.ensureNetworkPolicy("peer-pods-webhook-allow-webhook-ingress", hostNetworkAnnotation, allowIngressSpec(labels, 9443)); err != nil {
		return err
	}
	if err := r.ensureNetworkPolicy("peer-pods-webhook-allow-metrics-ingress", hostNetworkAnnotation, allowIngressSpec(labels, 8443)); err != nil {
		return err
	}
	if err := r.ensureNetworkPolicy("peer-pods-webhook-allow-dns-egress", nil, allowDNSEgressSpec(labels)); err != nil {
		return err
	}
	return r.ensureNetworkPolicy("peer-pods-webhook-allow-apiserver-egress", nil, allowAllEgressSpec(labels))
}

func (r *KataConfigOpenShiftReconciler) deletePeerPodsWebhookNetworkPolicies() error {
	return r.deleteNetworkPolicies([]string{
		"peer-pods-webhook-deny-all",
		"peer-pods-webhook-allow-webhook-ingress",
		"peer-pods-webhook-allow-metrics-ingress",
		"peer-pods-webhook-allow-dns-egress",
		"peer-pods-webhook-allow-apiserver-egress",
	})
}

// Kata Install DaemonSet: deny-all + allow-all egress
func (r *KataConfigOpenShiftReconciler) createKataInstallNetworkPolicies() error {
	labels := map[string]string{"name": "osc-rpm-install"}

	if err := r.ensureNetworkPolicy("kata-install-deny-all", nil, defaultDenySpec(labels)); err != nil {
		return err
	}
	return r.ensureNetworkPolicy("kata-install-allow-egress", nil, allowAllEgressSpec(labels))
}

func (r *KataConfigOpenShiftReconciler) deleteKataInstallNetworkPolicies() error {
	return r.deleteNetworkPolicies([]string{
		"kata-install-deny-all",
		"kata-install-allow-egress",
	})
}

// Kata Uninstall DaemonSet: deny-all + allow-all egress
func (r *KataConfigOpenShiftReconciler) createKataUninstallNetworkPolicies() error {
	labels := map[string]string{"name": "osc-rpm-uninstall"}

	if err := r.ensureNetworkPolicy("kata-uninstall-deny-all", nil, defaultDenySpec(labels)); err != nil {
		return err
	}
	return r.ensureNetworkPolicy("kata-uninstall-allow-egress", nil, allowAllEgressSpec(labels))
}

func (r *KataConfigOpenShiftReconciler) deleteKataUninstallNetworkPolicies() error {
	return r.deleteNetworkPolicies([]string{
		"kata-uninstall-deny-all",
		"kata-uninstall-allow-egress",
	})
}

// PodVM Image Creation Job: deny-all + allow-all egress
func (r *KataConfigOpenShiftReconciler) createPodVMImageCreationNetworkPolicies() error {
	labels := map[string]string{"job-name": "osc-podvm-image-creation"}

	if err := r.ensureNetworkPolicy("podvm-image-creation-deny-all", nil, defaultDenySpec(labels)); err != nil {
		return err
	}
	return r.ensureNetworkPolicy("podvm-image-creation-allow-egress", nil, allowAllEgressSpec(labels))
}

func (r *KataConfigOpenShiftReconciler) deletePodVMImageCreationNetworkPolicies() error {
	return r.deleteNetworkPolicies([]string{
		"podvm-image-creation-deny-all",
		"podvm-image-creation-allow-egress",
	})
}

// PodVM Image Deletion Job: deny-all + allow-all egress
func (r *KataConfigOpenShiftReconciler) createPodVMImageDeletionNetworkPolicies() error {
	labels := map[string]string{"job-name": "osc-podvm-image-deletion"}

	if err := r.ensureNetworkPolicy("podvm-image-deletion-deny-all", nil, defaultDenySpec(labels)); err != nil {
		return err
	}
	return r.ensureNetworkPolicy("podvm-image-deletion-allow-egress", nil, allowAllEgressSpec(labels))
}

func (r *KataConfigOpenShiftReconciler) deletePodVMImageDeletionNetworkPolicies() error {
	return r.deleteNetworkPolicies([]string{
		"podvm-image-deletion-deny-all",
		"podvm-image-deletion-allow-egress",
	})
}
