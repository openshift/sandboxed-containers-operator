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
	"strconv"

	"github.com/go-logr/logr"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// Kata memory overhead annotation
	KataMemoryOverheadAnnotation = "io.katacontainers.config.hypervisor.memory_overhead"
	KataMemoryOverheadDefault    = 350
)

// +kubebuilder:webhook:path=/mutate-pods-v1,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=mpods.kb.io,admissionReviewVersions=v1

// PodMutator handles pod mutations to inject Kata-specific annotations
type PodMutator struct {
	Client  client.Client
	Log     logr.Logger
}

// Create a new PodMutator
func NewPodMutator(mgr ctrl.Manager) *PodMutator {
	return &PodMutator{
		Client:  mgr.GetClient(),
		Log:     log.Log.WithName("pod-mutator"),
	}
}

// Retrieve pod overhead from kata configuration
func (m *PodMutator) getMemoryOverheadFromKataConfig(ctx context.Context, pod *corev1.Pod) (int32, error) {

	// Check if this uses a Kata runtime class
	if pod.Spec.RuntimeClassName != nil {
		runtimeClassName := *pod.Spec.RuntimeClassName

		// List all KataConfigs
		kataConfigs := &kataconfigurationv1.KataConfigList{}
		err := m.Client.List(ctx, kataConfigs)
		if err != nil {
			return 0, fmt.Errorf("failed to list KataConfigs: %w", err)
		}

		// Use the first active KataConfig (normally there should be only one)
		for _, kataConfig := range kataConfigs.Items {
			overhead := kataConfig.Spec.MemoryOverheadMB
			for _, rtc := range kataConfig.Status.RuntimeClasses {
				if runtimeClassName == rtc {
					if overhead != nil {
						return *overhead, nil
					}
					// Fallback to default value
					return KataMemoryOverheadDefault, nil
				}
			}
		}
	}

	// Not a Kata runtime class
	return 0, nil
}

// injectMemoryOverheadAnnotation injects the memory overhead annotation into the pod
func (m *PodMutator) injectMemoryOverheadAnnotation(pod *corev1.Pod, memoryOverhead int32) error {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	// Set the memory overhead annotation
	pod.Annotations[KataMemoryOverheadAnnotation] = strconv.FormatInt(int64(memoryOverhead), 10)

	m.Log.Info("injected memory overhead annotation",
		"pod", pod.Name,
		"namespace", pod.Namespace,
		"memoryOverhead", memoryOverhead)

	return nil
}

// SetupWebhookWithManager sets up the webhook with the manager
func (m *PodMutator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&corev1.Pod{}).
		WithDefaulter(m).
		Complete()
}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (m *PodMutator) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("expected a Pod but got a %T", obj)
	}

	// Get memory overhead from KataConfig
	memoryOverhead, err := m.getMemoryOverheadFromKataConfig(ctx, pod)
	if err != nil {
		m.Log.Error(err, "failed to get memory overhead from KataConfig")
		return err
	}

	// If no overhead, just skip
	if memoryOverhead == 0 {
		return nil
	}

	// Inject memory overhead annotation
	return m.injectMemoryOverheadAnnotation(pod, memoryOverhead)
}
