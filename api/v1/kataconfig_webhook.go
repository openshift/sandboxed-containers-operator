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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	// log is for logging in this package.
	kataconfiglog = logf.Log.WithName("kataconfig-resource")
	clientInst    client.Client
)

func (r *KataConfig) SetupWebhookWithManager(mgr ctrl.Manager) error {
	clientInst = mgr.GetClient()

	kataconfiglog.Info("SetupWebhookWithManager")
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:verbs=create,path=/validate-kataconfiguration-openshift-io-v1-kataconfig,mutating=false,failurePolicy=fail,groups=kataconfiguration.openshift.io,resources=kataconfigs,versions=v1,name=vkataconfig.kb.io,sideEffects=none,admissionReviewVersions={v1}

var _ webhook.CustomValidator = &KataConfig{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *KataConfig) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	kataconfig, ok := obj.(*KataConfig)
	if !ok {
		return nil, fmt.Errorf("expected a KataConfig object but got %T", obj)
	}

	kataconfiglog.Info("validate create", "name", kataconfig.Name)

	kataConfigList := &KataConfigList{}
	listOpts := []client.ListOption{
		client.InNamespace(corev1.NamespaceAll),
	}
	if err := clientInst.List(ctx, kataConfigList, listOpts...); err != nil {
		return nil, fmt.Errorf("Failed to list KataConfig custom resources: %v", err)
	}

	if len(kataConfigList.Items) == 1 {
		return nil, fmt.Errorf("A KataConfig instance already exists, refusing to create a duplicate")
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *KataConfig) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	kataconfig, ok := newObj.(*KataConfig)
	if !ok {
		return nil, fmt.Errorf("expected a KataConfig object but got %T", newObj)
	}

	kataconfiglog.Info("validate update", "name", kataconfig.Name)

	// TODO(user): fill in your validation logic upon object update.
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *KataConfig) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	kataconfig, ok := obj.(*KataConfig)
	if !ok {
		return nil, fmt.Errorf("expected a KataConfig object but got %T", obj)
	}

	kataconfiglog.Info("validate delete", "name", kataconfig.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}
