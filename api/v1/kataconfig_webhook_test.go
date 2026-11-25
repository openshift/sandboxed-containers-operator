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
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRuntimeClassLabelsValidation(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	previous := clientInst
	clientInst = fake.NewClientBuilder().WithScheme(s).Build()
	t.Cleanup(func() { clientInst = previous })
	for _, tc := range []struct {
		name    string
		config  *RuntimeClassLabels
		invalid bool
	}{
		{name: "absent"},
		{name: "empty", config: &RuntimeClassLabels{}},
		{name: "valid", config: &RuntimeClassLabels{
			Defaults:  map[string]string{"example.com/approved": "true", "empty": ""},
			Overrides: map[string]map[string]string{"kata.future": {"example.com/device": "gpu"}},
		}},
		{name: "invalid default key", config: &RuntimeClassLabels{Defaults: map[string]string{"bad key": "true"}}, invalid: true},
		{name: "invalid default value", config: &RuntimeClassLabels{Defaults: map[string]string{"key": "bad value"}}, invalid: true},
		{name: "long value", config: &RuntimeClassLabels{Defaults: map[string]string{"key": strings.Repeat("a", 64)}}, invalid: true},
		{name: "invalid class name", config: &RuntimeClassLabels{Overrides: map[string]map[string]string{"Bad_Name": {}}}, invalid: true},
		{name: "empty class name", config: &RuntimeClassLabels{Overrides: map[string]map[string]string{"": {}}}, invalid: true},
		{name: "invalid override key", config: &RuntimeClassLabels{Overrides: map[string]map[string]string{"kata": {"bad/key/key": "true"}}}, invalid: true},
		{name: "invalid override value", config: &RuntimeClassLabels{Overrides: map[string]map[string]string{"kata": {"key": "bad value"}}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &KataConfig{ObjectMeta: metav1.ObjectMeta{Name: "example"}, Spec: KataConfigSpec{RuntimeClassLabels: tc.config}}
			_, createErr := obj.ValidateCreate(context.Background(), obj)
			_, updateErr := obj.ValidateUpdate(context.Background(), &KataConfig{}, obj)
			for operation, err := range map[string]error{"create": createErr, "update": updateErr} {
				if tc.invalid && !apierrors.IsInvalid(err) {
					t.Errorf("%s: want Invalid, got %v", operation, err)
				} else if !tc.invalid && err != nil {
					t.Errorf("%s: %v", operation, err)
				}
			}
		})
	}
}
