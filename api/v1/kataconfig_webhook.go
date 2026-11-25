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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metavalidation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

//+kubebuilder:webhook:verbs=create;update,path=/validate-kataconfiguration-openshift-io-v1-kataconfig,mutating=false,failurePolicy=fail,groups=kataconfiguration.openshift.io,resources=kataconfigs,versions=v1,name=vkataconfig.kb.io,sideEffects=none,admissionReviewVersions={v1}

var _ admission.Validator[*KataConfig] = &KataConfig{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *KataConfig) ValidateCreate(ctx context.Context, obj *KataConfig) (admission.Warnings, error) {
	kataconfiglog.Info("validate create", "name", obj.Name)
	if err := obj.validateRuntimeClassLabels(); err != nil {
		return nil, err
	}

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
func (r *KataConfig) ValidateUpdate(ctx context.Context, oldObj, newObj *KataConfig) (admission.Warnings, error) {
	kataconfiglog.Info("validate update", "name", newObj.Name)

	return nil, newObj.validateRuntimeClassLabels()
}

func (r *KataConfig) validateRuntimeClassLabels() error {
	config := r.Spec.RuntimeClassLabels
	if config == nil {
		return nil
	}
	path := field.NewPath("spec", "runtimeClassLabels")
	errs := metavalidation.ValidateLabels(config.Defaults, path.Child("defaults"))
	for name, labels := range config.Overrides {
		entryPath := path.Child("overrides").Key(name)
		for _, message := range validation.IsDNS1123Subdomain(name) {
			errs = append(errs, field.Invalid(entryPath, name, message))
		}
		errs = append(errs, metavalidation.ValidateLabels(labels, entryPath)...)
	}
	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "KataConfig"}, r.Name, errs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *KataConfig) ValidateDelete(ctx context.Context, obj *KataConfig) (admission.Warnings, error) {
	kataconfiglog.Info("validate delete", "name", obj.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}
