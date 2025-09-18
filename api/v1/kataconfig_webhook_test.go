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
			name: "Valid KataConfig with MemoryOverheadMB within range",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(512),
				},
			},
			expectError: false,
		},
		{
			name: "Valid KataConfig with MemoryOverheadMB at minimum value",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(60),
				},
			},
			expectError: false,
		},
		{
			name: "Valid KataConfig with MemoryOverheadMB at maximum value",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(4096 * 1024), // 4GB
				},
			},
			expectError: false,
		},
		{
			name: "Valid KataConfig without MemoryOverheadMB",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					// No MemoryOverheadMB specified
				},
			},
			expectError: false,
		},
		{
			name: "Invalid KataConfig with MemoryOverheadMB below minimum",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(0),
				},
			},
			expectError: true,
			errorMsg:    "memoryOverheadMB must be at least 60MB",
		},
		{
			name: "Invalid KataConfig with MemoryOverheadMB above maximum",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(4096*1024 + 1), // Just over 4GB
				},
			},
			expectError: true,
			errorMsg:    "memoryOverheadMB must be at most 4GB",
		},
		{
			name: "Invalid KataConfig with negative MemoryOverheadMB",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(-1),
				},
			},
			expectError: true,
			errorMsg:    "memoryOverheadMB must be at least 60MB",
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
					MemoryOverheadMB:     int32Ptr(1024),
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
			name: "Valid update with MemoryOverheadMB within range",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(256),
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(512),
				},
			},
			expectError: false,
		},
		{
			name: "Valid update removing MemoryOverheadMB",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(512),
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					// No MemoryOverheadMB specified
				},
			},
			expectError: false,
		},
		{
			name: "Valid update adding MemoryOverheadMB",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					// No MemoryOverheadMB specified
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(1024),
				},
			},
			expectError: false,
		},
		{
			name: "Invalid update with MemoryOverheadMB below minimum",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(512),
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(0),
				},
			},
			expectError: true,
			errorMsg:    "memoryOverheadMB must be at least 60MB",
		},
		{
			name: "Invalid update with MemoryOverheadMB above maximum",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(512),
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(4096*1024 + 1), // Just over 4GB
				},
			},
			expectError: true,
			errorMsg:    "memoryOverheadMB must be at most 4GB",
		},
		{
			name: "Valid update with other fields unchanged",
			oldConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
					LogLevel:             "info",
					EnablePeerPods:       false,
					MemoryOverheadMB:     int32Ptr(256),
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					CheckNodeEligibility: true,
					LogLevel:             "debug", // Changed
					EnablePeerPods:       false,
					MemoryOverheadMB:     int32Ptr(512), // Changed
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
			name: "Valid delete with MemoryOverheadMB",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(512),
				},
			},
			expectError: false,
		},
		{
			name: "Valid delete without MemoryOverheadMB",
			kataConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					// No MemoryOverheadMB specified
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

// Test edge cases and boundary conditions
func TestKataConfig_MemoryOverheadMB_EdgeCases(t *testing.T) {
	tests := []struct {
		name             string
		memoryOverheadMB *int32
		expectError      bool
		errorMsg         string
	}{
		{
			name:             "nil MemoryOverheadMB should be valid",
			memoryOverheadMB: nil,
			expectError:      false,
		},
		{
			name:             "MemoryOverheadMB = 60 should be valid",
			memoryOverheadMB: int32Ptr(60),
			expectError:      false,
		},
		{
			name:             "MemoryOverheadMB = 4096*1024 should be valid",
			memoryOverheadMB: int32Ptr(4096 * 1024),
			expectError:      false,
		},
		{
			name:             "MemoryOverheadMB = 0 should be invalid",
			memoryOverheadMB: int32Ptr(0),
			expectError:      true,
			errorMsg:         "memoryOverheadMB must be at least 60MB",
		},
		{
			name:             "MemoryOverheadMB = -1 should be invalid",
			memoryOverheadMB: int32Ptr(-1),
			expectError:      true,
			errorMsg:         "memoryOverheadMB must be at least 60MB",
		},
		{
			name:             "MemoryOverheadMB = 4096*1024+1 should be invalid",
			memoryOverheadMB: int32Ptr(4096*1024 + 1),
			expectError:      true,
			errorMsg:         "memoryOverheadMB must be at most 4GB",
		},
		{
			name:             "MemoryOverheadMB = 10000 should be valid",
			memoryOverheadMB: int32Ptr(10000),
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kataConfig := KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: tt.memoryOverheadMB,
				},
			}

			warnings, err := kataConfig.ValidateCreate(context.Background(), &kataConfig)

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
					MemoryOverheadMB: int32Ptr(256),
				},
			},
			newConfig: KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kataconfig",
				},
				Spec: KataConfigSpec{
					MemoryOverheadMB: int32Ptr(512),
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
					MemoryOverheadMB: int32Ptr(512),
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
			MemoryOverheadMB: int32Ptr(512),
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
			MemoryOverheadMB: int32Ptr(256),
		},
	}

	newConfig := KataConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-kataconfig",
		},
		Spec: KataConfigSpec{
			MemoryOverheadMB: int32Ptr(512),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newConfig.ValidateUpdate(context.Background(), &oldConfig, &newConfig)
	}
}

// Helper function
func int32Ptr(i int32) *int32 {
	return &i
}
