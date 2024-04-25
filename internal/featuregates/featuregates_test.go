package featuregates

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFeatureGates_GetFeatureGateParams(t *testing.T) {
	fg := &FeatureGates{
		Client:    fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
		Namespace: "test-namespace",
	}

	ctx := context.TODO()

	t.Run("FeatureGate not found", func(t *testing.T) {
		feature := "non-existent-feature"

		params := fg.GetFeatureGateParams(ctx, feature)

		if len(params) != 0 {
			t.Errorf("Expected empty params, got: %v", params)
		}
	})

	t.Run("FeatureGate found", func(t *testing.T) {
		feature := "AdditionalRuntimeClasses"

		cfgMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      AdditionalRuntimeClassesConfig,
				Namespace: fg.Namespace,
			},
			Data: map[string]string{
				"runtimeClassConfig": "name1,name2",
			},
		}

		err := fg.Client.Create(ctx, cfgMap)
		if err != nil {
			t.Fatalf("Failed to create ConfigMap: %v", err)
		}

		params := fg.GetFeatureGateParams(ctx, feature)

		expectedParams := map[string]string{
			"runtimeClassConfig": "name1,name2",
		}

		if len(params) != len(expectedParams) {
			t.Errorf("Expected %d params, got: %d", len(expectedParams), len(params))
		}

		for key, value := range expectedParams {
			if params[key] != value {
				t.Errorf("Expected param %s to have value %s, got: %s", key, value, params[key])
			}
		}

		// Clean up the created ConfigMap
		err = fg.Client.Delete(ctx, cfgMap)
		if err != nil {
			t.Fatalf("Failed to delete ConfigMap: %v", err)
		}
	})

	t.Run("FeatureGate configmap not found", func(t *testing.T) {
		feature := "LayeredImageDeployment"

		params := fg.GetFeatureGateParams(ctx, feature)

		if len(params) != 0 {
			t.Errorf("Expected empty params, got: %v", params)
		}

	})

	t.Run("FeatureGate configmap not found", func(t *testing.T) {
		feature := "LayeredImageDeployment"

		params := fg.GetFeatureGateParams(ctx, feature)

		if len(params) != 0 {
			t.Errorf("Expected empty params, got: %v", params)
		}

		// Check existence of params["kernelArguments"] in the returned map
		if _, ok := params["kernelArguments"]; ok {
			t.Errorf("Expected empty params to not contain key 'kernelArguments'")
		}

	})
}
