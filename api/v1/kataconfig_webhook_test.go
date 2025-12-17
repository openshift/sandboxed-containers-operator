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

package v1

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestKataConfig_ValidateCreate(t *testing.T) {
	tests := []struct {
		name        string
		kataConfig  KataConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid KataConfig with default values",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{},
			},
			expectError: false,
		},
		{
			name: "Valid KataConfig with all fields",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
					LogLevel:             "debug",
					EnablePeerPods:       true,
				},
			},
			expectError: false,
		},
		{
			name: "Valid KataConfig with CheckNodeEligibility",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
				},
			},
			expectError: false,
		},
		{
			name: "Valid KataConfig with EnablePeerPods",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					EnablePeerPods: true,
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := tt.kataConfig.ValidateCreate(context.Background(), &tt.kataConfig)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorMsg != "" && err.Error() != tt.errorMsg {
					t.Errorf("Expected error message '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}

			// Check warnings if any
			if warnings != nil {
				t.Logf("Warnings: %v", warnings)
			}
		})
	}
}

func TestKataConfig_ValidateUpdate(t *testing.T) {
	tests := []struct {
		name        string
		oldConfig   KataConfig
		newConfig   KataConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid update changing LogLevel",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					LogLevel: "info",
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					LogLevel: "debug",
				},
			},
			expectError: false,
		},
		{
			name: "Valid update enabling CheckNodeEligibility",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: false,
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
				},
			},
			expectError: false,
		},
		{
			name: "Valid update with multiple field changes",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
					LogLevel:             "info",
					EnablePeerPods:       false,
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
					LogLevel:             "debug",
					EnablePeerPods:       true,
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := tt.newConfig.ValidateUpdate(context.Background(), &tt.oldConfig, &tt.newConfig)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorMsg != "" && err.Error() != tt.errorMsg {
					t.Errorf("Expected error message '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}

			// Check warnings if any
			if warnings != nil {
				t.Logf("Warnings: %v", warnings)
			}
		})
	}
}

func TestKataConfig_ValidateDelete(t *testing.T) {
	tests := []struct {
		name        string
		kataConfig  KataConfig
		expectError bool
	}{
		{
			name: "Valid delete with default KataConfig",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{},
			},
			expectError: false,
		},
		{
			name: "Valid delete with fully configured KataConfig",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
					LogLevel:             "debug",
					EnablePeerPods:       true,
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := tt.kataConfig.ValidateDelete(context.Background(), &tt.kataConfig)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}

			// Check warnings if any
			if warnings != nil {
				t.Logf("Warnings: %v", warnings)
			}
		})
	}
}

// Test validation with different object types
func TestKataConfig_ValidateUpdate_DifferentObjectTypes(t *testing.T) {
	tests := []struct {
		name        string
		oldObject   runtime.Object
		newConfig   KataConfig
		expectError bool
	}{
		{
			name: "Valid update with KataConfig object",
			oldObject: &KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					LogLevel: "info",
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					LogLevel: "debug",
				},
			},
			expectError: false,
		},
		{
			name: "Valid update with different old object type",
			oldObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					LogLevel: "debug",
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := tt.newConfig.ValidateUpdate(context.Background(), tt.oldObject, &tt.newConfig)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}

			// Check warnings if any
			if warnings != nil {
				t.Logf("Warnings: %v", warnings)
			}
		})
	}
}

// Benchmark tests
func BenchmarkKataConfig_ValidateCreate(b *testing.B) {
	kataConfig := KataConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-kataconfig",
		},
		Spec: KataConfigSpec{
			CheckNodeEligibility: true,
			LogLevel:             "debug",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kataConfig.ValidateCreate(context.Background(), &kataConfig)
	}
}

func BenchmarkKataConfig_ValidateUpdate(b *testing.B) {
	oldConfig := KataConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-kataconfig",
		},
		Spec: KataConfigSpec{
			LogLevel: "info",
		},
	}

	newConfig := KataConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-kataconfig",
		},
		Spec: KataConfigSpec{
			LogLevel: "debug",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newConfig.ValidateUpdate(context.Background(), &oldConfig, &newConfig)
	}
}
