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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
)

var _ = Describe("RuntimeClass label reconciliation", func() {
	It("validates configuration and repairs metadata through the owned-resource watch", func() {
		apiClient, err := client.New(cfg, client.Options{Scheme: k8sManager.GetScheme()})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			return k8sManager.GetWebhookServer().StartedChecker()(nil)
		}, 10*time.Second).Should(Succeed())

		invalid := &kataconfigurationv1.KataConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "invalid-label-config"},
			Spec: kataconfigurationv1.KataConfigSpec{
				RuntimeClassLabels: &kataconfigurationv1.RuntimeClassLabels{
					Defaults: map[string]string{"example.com/approved": "invalid value"},
				},
			},
		}
		Expect(apierrors.IsInvalid(apiClient.Create(ctx, invalid))).To(BeTrue())

		config := &kataconfigurationv1.KataConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "runtimeclass-label-test"},
			Spec: kataconfigurationv1.KataConfigSpec{
				CheckNodeEligibility: true,
				// Stop installation without a requeue. Label drift must be
				// repaired by events rather than installation retries.
				KataConfigPoolSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key: "test.example/stop-installation", Operator: "Invalid",
					}},
				},
				RuntimeClassLabels: &kataconfigurationv1.RuntimeClassLabels{
					Defaults: map[string]string{"example.com/approved": "true"},
				},
			},
		}
		Expect(apiClient.Create(ctx, config)).To(Succeed())
		DeferCleanup(func() {
			Eventually(func() error {
				if err := apiClient.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
					return client.IgnoreNotFound(err)
				}
				config.Finalizers = nil
				if err := apiClient.Update(ctx, config); err != nil {
					return err
				}
				return client.IgnoreNotFound(apiClient.Delete(ctx, config))
			}, 10*time.Second).Should(Succeed())
		})

		rc := &nodev1.RuntimeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "kata-label-test-future",
				Labels: map[string]string{"other.example/label": "keep"},
			},
			Handler: "kata-label-test-future",
		}
		Expect(controllerutil.SetControllerReference(config, rc, k8sManager.GetScheme())).To(Succeed())
		Expect(apiClient.Create(ctx, rc)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(apiClient.Delete(ctx, rc))).To(Succeed())
		})
		Eventually(func(g Gomega) {
			g.Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(rc), rc)).To(Succeed())
			g.Expect(rc.Labels).To(HaveKeyWithValue("example.com/approved", "true"))
		}, 15*time.Second).Should(Succeed())

		Eventually(func() error {
			if err := apiClient.Get(ctx, client.ObjectKeyFromObject(rc), rc); err != nil {
				return err
			}
			rc.Labels["example.com/approved"] = "drift"
			return apiClient.Update(ctx, rc)
		}, 10*time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(rc), rc)).To(Succeed())
			g.Expect(rc.Labels["example.com/approved"]).To(Equal("true"))
		}, 15*time.Second).Should(Succeed())

		Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(config), config)).To(Succeed())
		invalid = config.DeepCopy()
		invalid.Spec.RuntimeClassLabels.Overrides = map[string]map[string]string{"Bad_Name": {"key": "value"}}
		Expect(apierrors.IsInvalid(apiClient.Update(ctx, invalid))).To(BeTrue())

		Eventually(func() error {
			if err := apiClient.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
				return err
			}
			config.Spec.RuntimeClassLabels = nil
			return apiClient.Update(ctx, config)
		}, 10*time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(apiClient.Get(ctx, client.ObjectKeyFromObject(rc), rc)).To(Succeed())
			g.Expect(rc.Labels).To(Equal(map[string]string{"other.example/label": "keep"}))
			g.Expect(rc.Annotations).NotTo(HaveKey(managedRuntimeClassLabelsAnnotation))
		}, 15*time.Second).Should(Succeed())
	})
})
