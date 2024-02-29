package featuregates

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	"log"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FeatureGates struct {
	Client        client.Client
	Namespace     string
	ConfigMapName string
}

var DefaultFeatureGates = map[string]bool{
	"timeTravel":              false,
	"quantumEntanglementSync": false,
	"autoHealingWithAI":       true,
}

func (fg *FeatureGates) IsEnabled(ctx context.Context, feature string) bool {
	if fg == nil {
		return false
	}
	cfgMap := &corev1.ConfigMap{}
	err := fg.Client.Get(ctx,
		client.ObjectKey{Name: fg.ConfigMapName, Namespace: fg.Namespace},
		cfgMap)

	if err != nil {
		log.Printf("Error fetching feature gates: %v", err)
	} else {
		if value, exists := cfgMap.Data[feature]; exists {
			return value == "true"
		}
	}

	defaultValue, exists := DefaultFeatureGates[feature]
	if exists {
		return defaultValue
	}
	return false
}
