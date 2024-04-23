package featuregates

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FeatureGates struct {
	Client        client.Client
	Namespace     string
	ConfigMapName string
}

// FeatureGate Status Struct map of string and bool
type FeatureGateStatus map[string]bool

var DefaultFeatureGates = map[string]bool{
	LayeredImageDeployment: false,
}

var fgLogger logr.Logger = ctrl.Log.WithName("featuregates")

func (fg *FeatureGates) IsEnabled(ctx context.Context, feature string) bool {
	if fg == nil {
		return false
	}
	cfgMap := &corev1.ConfigMap{}
	err := fg.Client.Get(ctx,
		client.ObjectKey{Name: fg.ConfigMapName, Namespace: fg.Namespace},
		cfgMap)

	if err != nil {
		fgLogger.Info("Error fetching feature gates", "err", err)
	} else {
		if value, exists := cfgMap.Data[feature]; exists {
			fgLogger.Info("Feature gate enabled", "feature", feature)
			return value == "true"
		}
	}

	defaultValue, exists := DefaultFeatureGates[feature]
	if exists {
		return defaultValue
	}
	return false
}

// Method to read the feature specific config parameters from the configmap
// The feature specific config params are stored in their own configmap like this
// data: |
//  key1=value1
//  key2=value2

func (fg *FeatureGates) GetFeatureGateParams(ctx context.Context, feature string) map[string]string {

	fgParams := make(map[string]string)

	if fg == nil {
		return fgParams
	}
	featureCmName := GetFeatureGateConfigMapName(feature)

	cfgMap := &corev1.ConfigMap{}
	err := fg.Client.Get(ctx,
		client.ObjectKey{Name: featureCmName, Namespace: fg.Namespace},
		cfgMap)

	if err != nil {
		fgLogger.Info("Error fetching config params for feature", "feature", feature, "err", err)
		return fgParams
	}
	fgParams = cfgMap.Data
	return fgParams
}

// Method to update the FeatureGate status struct with the status of all the feature gates
func (fg *FeatureGates) GetFeatureGateStatus(ctx context.Context) FeatureGateStatus {
	featureGateStatus := make(FeatureGateStatus)

	// Read the configmap to get the status of all the feature gates and populate the status struct
	// Ignore key with feature.params as these are the feature gate specific config params
	cfgMap := &corev1.ConfigMap{}
	err := fg.Client.Get(ctx,
		client.ObjectKey{Name: fg.ConfigMapName, Namespace: fg.Namespace},
		cfgMap)
	if err != nil {
		fgLogger.Info("Error fetching feature gates", "err", err)
		return nil
	} else {
		for key, value := range cfgMap.Data {
			featureGateStatus[key] = value == "true"
		}
	}

	// Populate the status struct with the default values for the feature gates which are not present in the configmap
	for key, value := range DefaultFeatureGates {
		if _, exists := featureGateStatus[key]; !exists {
			featureGateStatus[key] = value
		}
	}

	fgLogger.Info("Feature gate status", "status", featureGateStatus)

	return featureGateStatus

}

// Check if feature gate configmap is present in the namespace
func (fg *FeatureGates) IsFeatureConfigMapPresent(ctx context.Context, feature string) bool {
	featureCmName := GetFeatureGateConfigMapName(feature)

	cfgMap := &corev1.ConfigMap{}
	err := fg.Client.Get(ctx,
		client.ObjectKey{Name: featureCmName, Namespace: fg.Namespace},
		cfgMap)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			fgLogger.Info("Feature configmap not found. Please create it", "feature", feature)
		} else {
			fgLogger.Info("Error fetching feature configmap", "feature", feature, "err", err)
		}
		return false
	}
	return true
}
