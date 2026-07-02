package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

const (
	DaemonSetConfigMapName = "osc-daemonset-config"
	DaemonSetImageKey      = "image"
)

// DaemonSetConfig holds configuration for DaemonSet deployment mode
type DaemonSetConfig struct {
	// Image override for the kata-install DaemonSet
	// If empty, uses the default hardcoded image
	Image string
}

// GetDaemonSetConfig retrieves DaemonSet-specific configuration from the
// osc-daemonset-config ConfigMap. Returns default values if ConfigMap doesn't exist.
func (r *KataConfigOpenShiftReconciler) GetDaemonSetConfig() (*DaemonSetConfig, error) {
	config := &DaemonSetConfig{
		Image: "", // Empty means use default
	}

	cfgMap := &corev1.ConfigMap{}
	err := r.Client.Get(context.TODO(),
		types.NamespacedName{Name: DaemonSetConfigMapName, Namespace: OperatorNamespace},
		cfgMap)

	if err == nil {
		if value, ok := cfgMap.Data[DaemonSetImageKey]; ok && value != "" {
			config.Image = value
			r.Log.Info("Using custom DaemonSet image from config", "image", value)
		}
	}

	if k8serrors.IsNotFound(err) {
		// ConfigMap doesn't exist - use defaults
		return config, nil
	}

	return config, err
}
