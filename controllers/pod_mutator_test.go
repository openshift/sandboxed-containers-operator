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

package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPodMutator_getMemoryOverheadFromKataConfig(t *testing.T) {
	tests := []struct {
		name          string
		pod           *corev1.Pod
		kataConfigs   []kataconfigurationv1.KataConfig
		expectedValue int32
		expectError   bool
	}{
		{
			name: "Pod with Kata runtime class and KataConfig with MemoryOverheadMB",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata"),
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(512),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata", "kata-remote"},
					},
				},
			},
			expectedValue: 512,
			expectError:   false,
		},
		{
			name: "Pod with kata-remote runtime class",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata-remote"),
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(256),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata", "kata-remote"},
					},
				},
			},
			expectedValue: 256,
			expectError:   false,
		},
		{
			name: "Pod with Kata runtime class and KataConfig without MemoryOverheadMB should use default",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata"),
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						// No MemoryOverheadMB specified
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
			},
			expectedValue: KataMemoryOverheadDefault,
			expectError:   false,
		},
		{
			name: "Pod without runtime class should return 0",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(512),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
			},
			expectedValue: 0,
			expectError:   false,
		},
		{
			name: "Pod with non-Kata runtime class should return 0",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("runc"),
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(512),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
			},
			expectedValue: 0,
			expectError:   false,
		},
		{
			name: "Pod with Kata runtime class but no KataConfigs should return 0",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata"),
				},
			},
			kataConfigs:   []kataconfigurationv1.KataConfig{},
			expectedValue: 0,
			expectError:   false,
		},
		{
			name: "Pod with Kata runtime class but KataConfig has empty RuntimeClasses should return 0",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata"),
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(512),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{}, // Empty - not yet populated
					},
				},
			},
			expectedValue: 0,
			expectError:   false,
		},
		{
			name: "Multiple KataConfigs - first matching one wins",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata"),
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kataconfig-1",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(256),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kataconfig-2",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(1024),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
			},
			expectedValue: 256, // First one found
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with KataConfigs
			scheme := scheme.Scheme
			kataconfigurationv1.AddToScheme(scheme)

			objects := make([]client.Object, 0)
			for i := range tt.kataConfigs {
				objects = append(objects, &tt.kataConfigs[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			mutator := &PodMutator{
				Client: fakeClient,
				Log:    logr.Discard(),
			}

			result, err := mutator.getMemoryOverheadFromKataConfig(context.Background(), tt.pod)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if !tt.expectError && result != tt.expectedValue {
				t.Errorf("Expected %d, got %d", tt.expectedValue, result)
			}
		})
	}
}

func TestPodMutator_injectMemoryOverheadAnnotation(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		memoryOverhead int32
		expectedValue  string
		expectError    bool
	}{
		{
			name: "Pod without annotations should get annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			memoryOverhead: 512,
			expectedValue:  "512",
			expectError:    false,
		},
		{
			name: "Pod with existing annotations should get additional annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"existing-annotation": "existing-value",
					},
				},
			},
			memoryOverhead: 256,
			expectedValue:  "256",
			expectError:    false,
		},
		{
			name: "Pod with existing memory overhead annotation should be updated",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						KataMemoryOverheadAnnotation: "128",
					},
				},
			},
			memoryOverhead: 1024,
			expectedValue:  "1024",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutator := &PodMutator{
				Log: logr.Discard(),
			}

			err := mutator.injectMemoryOverheadAnnotation(tt.pod, tt.memoryOverhead)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if !tt.expectError {
				if tt.pod.Annotations == nil {
					t.Errorf("Expected annotations to be set")
					return
				}

				annotationValue, exists := tt.pod.Annotations[KataMemoryOverheadAnnotation]
				if !exists {
					t.Errorf("Expected memory overhead annotation to be present")
					return
				}

				if annotationValue != tt.expectedValue {
					t.Errorf("Expected annotation value %s, got %s", tt.expectedValue, annotationValue)
				}
			}
		})
	}
}

func TestPodMutator_Default(t *testing.T) {
	tests := []struct {
		name               string
		obj                runtime.Object
		kataConfigs        []kataconfigurationv1.KataConfig
		expectAnnotation   bool
		expectedAnnotation string
		expectError        bool
	}{
		{
			name: "Pod with Kata runtime class should get annotation",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata"),
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx"},
					},
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(512),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata", "kata-remote"},
					},
				},
			},
			expectAnnotation:   true,
			expectedAnnotation: "512",
			expectError:        false,
		},
		{
			name: "Pod with Kata runtime class and no MemoryOverheadMB should use default",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("kata"),
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx"},
					},
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						// No MemoryOverheadMB
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
			},
			expectAnnotation:   true,
			expectedAnnotation: fmt.Sprintf("%d", KataMemoryOverheadDefault),
			expectError:        false,
		},
		{
			name: "Non-pod object should return error",
			obj: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: "default",
				},
			},
			kataConfigs:      []kataconfigurationv1.KataConfig{},
			expectAnnotation: false,
			expectError:      true,
		},
		{
			name: "Pod without Kata runtime class should not get annotation",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: stringPtr("runc"),
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx"},
					},
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(512),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
			},
			expectAnnotation: false,
			expectError:      false,
		},
		{
			name: "Pod without runtime class should not get annotation",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx"},
					},
				},
			},
			kataConfigs: []kataconfigurationv1.KataConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-kataconfig",
					},
					Spec: kataconfigurationv1.KataConfigSpec{
						MemoryOverheadMB: int32Ptr(512),
					},
					Status: kataconfigurationv1.KataConfigStatus{
						RuntimeClasses: []string{"kata"},
					},
				},
			},
			expectAnnotation: false,
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with KataConfigs
			scheme := scheme.Scheme
			kataconfigurationv1.AddToScheme(scheme)

			objects := make([]client.Object, 0)
			for i := range tt.kataConfigs {
				objects = append(objects, &tt.kataConfigs[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			mutator := &PodMutator{
				Client: fakeClient,
				Log:    logr.Discard(),
			}

			err := mutator.Default(context.Background(), tt.obj)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Check annotation on pod
			if pod, ok := tt.obj.(*corev1.Pod); ok && !tt.expectError {
				_, exists := pod.Annotations[KataMemoryOverheadAnnotation]
				if tt.expectAnnotation && !exists {
					t.Errorf("Expected annotation to be present but it wasn't")
				}
				if !tt.expectAnnotation && exists {
					t.Errorf("Expected no annotation but found one")
				}
				if tt.expectAnnotation && exists {
					if pod.Annotations[KataMemoryOverheadAnnotation] != tt.expectedAnnotation {
						t.Errorf("Expected annotation value %s, got %s",
							tt.expectedAnnotation, pod.Annotations[KataMemoryOverheadAnnotation])
					}
				}
			}
		})
	}
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}

// Benchmark test for Default method
func BenchmarkPodMutator_Default(b *testing.B) {
	// Setup
	scheme := scheme.Scheme
	kataconfigurationv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&kataconfigurationv1.KataConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-kataconfig",
			},
			Spec: kataconfigurationv1.KataConfigSpec{
				MemoryOverheadMB: int32Ptr(512),
			},
			Status: kataconfigurationv1.KataConfigStatus{
				RuntimeClasses: []string{"kata", "kata-remote"},
			},
		}).
		Build()

	mutator := &PodMutator{
		Client: fakeClient,
		Log:    logr.Discard(),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: stringPtr("kata"),
			Containers: []corev1.Container{
				{Name: "test", Image: "nginx"},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset annotations for each iteration
		pod.Annotations = nil
		mutator.Default(context.Background(), pod)
	}
}
