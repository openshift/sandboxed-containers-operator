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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// Kata memory overhead annotation
	KataMemoryOverheadAnnotation = "io.katacontainers.config.hypervisor.memory_overhead"
)

// +kubebuilder:webhook:path=/mutate-pods-v1,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=mpods.kb.io,admissionReviewVersions=v1

// PodMutator handles pod mutations to inject Kata-specific annotations
type PodMutator struct {
	Client client.Client
	Log    logr.Logger
}

// Create a new PodMutator
func NewPodMutator(mgr ctrl.Manager) *PodMutator {
	return &PodMutator{
		Client: mgr.GetClient(),
		Log:    log.Log.WithName("pod-mutator"),
	}
}

// Retrieve pod overhead from osc-feature-gates ConfigMap
func (m *PodMutator) getMemoryOverheadForKataPod(ctx context.Context, pod *corev1.Pod) (int32, error) {

	// Check if this uses a Kata runtime class
	if pod.Spec.RuntimeClassName == nil {
		return 0, nil
	}

	runtimeClassName := *pod.Spec.RuntimeClassName

	// List all KataConfigs to check if this runtime class is a Kata runtime
	kataConfigs := &kataconfigurationv1.KataConfigList{}
	err := m.Client.List(ctx, kataConfigs)
	if err != nil {
		return 0, fmt.Errorf("failed to list KataConfigs: %w", err)
	}

	// Check if the runtime class is a Kata runtime class
	isKataRuntime := false
	for _, kataConfig := range kataConfigs.Items {
		for _, rtc := range kataConfig.Status.RuntimeClasses {
			if runtimeClassName == rtc {
				isKataRuntime = true
				break
			}
		}
		if isKataRuntime {
			break
		}
	}

	if !isKataRuntime {
		return 0, nil
	}

	// Get memory overhead from the osc-feature-gates ConfigMap
	return m.getMemoryOverheadFromConfigMap(ctx)
}

// getMemoryOverheadFromConfigMap reads the memoryOverheadMB value from the osc-feature-gates ConfigMap
func (m *PodMutator) getMemoryOverheadFromConfigMap(ctx context.Context) (int32, error) {
	cfgMap := &corev1.ConfigMap{}
	err := m.Client.Get(ctx, types.NamespacedName{
		Name:      FgConfigMapName,
		Namespace: OperatorNamespace,
	}, cfgMap)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			// ConfigMap not found, use default value
			return MemoryOverheadMBDefault, nil
		}
		return 0, fmt.Errorf("failed to get ConfigMap %s: %w", FgConfigMapName, err)
	}

	if value, ok := cfgMap.Data[MemoryOverheadMBConfig]; ok {
		memoryOverhead, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			m.Log.Info("Couldn't parse memoryOverheadMB from ConfigMap, using default value",
				"default", MemoryOverheadMBDefault, "error", err)
			return MemoryOverheadMBDefault, nil
		}
		return int32(memoryOverhead), nil
	}

	// Key not found in ConfigMap, use default value
	return MemoryOverheadMBDefault, nil
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
	// Register the webhook at the path specified in the kubebuilder marker
	// This must match the path in config/webhook/manifests.yaml
	mgr.GetWebhookServer().Register("/mutate-pods-v1", &admission.Webhook{
		Handler: admission.WithCustomDefaulter(mgr.GetScheme(), &corev1.Pod{}, m),
	})
	return nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (m *PodMutator) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("expected a Pod but got a %T", obj)
	}

	// Get memory overhead from osc-feature-gates ConfigMap
	memoryOverhead, err := m.getMemoryOverheadForKataPod(ctx, pod)
	if err != nil {
		m.Log.Error(err, "failed to get memory overhead from ConfigMap")
		return err
	}

	// If no overhead, just skip
	if memoryOverhead == 0 {
		return nil
	}

	// Inject memory overhead annotation
	return m.injectMemoryOverheadAnnotation(pod, memoryOverhead)
}
