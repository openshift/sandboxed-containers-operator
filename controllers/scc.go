package controllers

import (
	secv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetScc() *secv1.SecurityContextConstraints {

	trueVar := false
	sccName := "sandboxed-containers-operator-scc"

	return &secv1.SecurityContextConstraints{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "security.openshift.io/v1",
			Kind:       "SecurityContextConstraints",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: sccName,
		},
		AllowHostDirVolumePlugin: true,
		AllowHostIPC:             false,
		AllowHostNetwork:         false,
		AllowHostPID:             false,
		AllowHostPorts:           false,
		AllowPrivilegeEscalation: &trueVar,
		AllowPrivilegedContainer: false,
		RequiredDropCapabilities: []corev1.Capability{"MKNOD", "FSETID", "KILL", "FOWNER"},
		AllowedCapabilities:      []corev1.Capability{"DAC_READ_OVERRIDE"},
		RunAsUser: secv1.RunAsUserStrategyOptions{
			Type: secv1.RunAsUserStrategyMustRunAsNonRoot,
		},
		SELinuxContext: secv1.SELinuxContextStrategyOptions{
			Type: secv1.SELinuxStrategyMustRunAs,
			SELinuxOptions: &corev1.SELinuxOptions{
				Type: "osc_monitor.process",
			},
		},
		Volumes: []secv1.FSType{secv1.FSTypeAll},
		Users:   []string{"system:serviceaccount:openshift-sandboxed-containers-operator:monitor"},
	}
}
