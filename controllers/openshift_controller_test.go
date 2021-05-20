package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("OpenShift KataConfig Controller", func() {
	Context("KataConfig create", func() {
		It("Should not support multiple KataConfig CRs", func() {

			const (
				name = "example-kataconfig"
			)

			kataconfig := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
			}

			By("Creating the first KataConfig CR successfully")
			Expect(k8sClient.Create(context.Background(), kataconfig)).Should(Succeed())
			time.Sleep(time.Second * 5)

			kataconfig2 := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name + "2",
				},
			}

			Expect(k8sClient.Create(context.Background(), kataconfig2)).Should(Succeed())
			time.Sleep(time.Second * 5)

			kataConfig2Key := types.NamespacedName{Name: kataconfig2.Name}
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), kataConfig2Key, kataconfig2)
				if err != nil {
					return false
				}
				return true
			}, 5, time.Second).Should(BeTrue())

			By("Creating and marking the second KataConfig CR correctly")
			Expect(kataconfig2.Status.InstallationStatus.Failed.FailedNodesCount).Should(Equal(-1))

			// Delete
			By("Deleting KataConfig CR successfully")
			kataConfigKey := types.NamespacedName{Name: kataconfig.Name}
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
				return k8sClient.Delete(context.Background(), kataconfig)
			}, 5, time.Second).Should(Succeed())

			By("Expecting to delete KataConfig CR successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
			}, 5, time.Second).ShouldNot(Succeed())

			// Delete
			By("Deleting KataConfig2 CR successfully")
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfig2Key, kataconfig2)
				return k8sClient.Delete(context.Background(), kataconfig2)
			}, 5, time.Second).Should(Succeed())

			By("Expecting to delete KataConfig2 CR successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), kataConfig2Key, kataconfig2)
			}, 5, time.Second).ShouldNot(Succeed())
		})
	})
	Context("Custom KataConfig create", func() {
		It("Should support KataConfig with custom node selector label", func() {

			const (
				name = "example-kataconfig"
			)

			kataconfig := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: kataconfigurationv1.KataConfigSpec{
					KataConfigPoolSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kata": "true"},
					},
				},
			}

			By("Creating the KataConfig CR with custom node selector label successfully")
			Expect(k8sClient.Create(context.Background(), kataconfig)).Should(Succeed())
			time.Sleep(time.Second * 5)

			kataconfig2 := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name + "2",
				},
				Spec: kataconfigurationv1.KataConfigSpec{
					KataConfigPoolSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kata": "true"},
					},
				},
			}

			Expect(k8sClient.Create(context.Background(), kataconfig2)).Should(Succeed())
			time.Sleep(time.Second * 5)

			kataConfig2Key := types.NamespacedName{Name: kataconfig2.Name}
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), kataConfig2Key, kataconfig2)
				if err != nil {
					return false
				}
				return true
			}, 5, time.Second).Should(BeTrue())

			By("Creating and marking the second KataConfig CR with same custom node selector label correctly")
			Expect(kataconfig2.Status.InstallationStatus.Failed.FailedNodesCount).Should(Equal(-1))

			//Delete
			By("Deleting KataConfig CR successfully")
			kataConfigKey := types.NamespacedName{Name: kataconfig.Name}
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
				return k8sClient.Delete(context.Background(), kataconfig)
			}, 5, time.Second).Should(Succeed())

			By("Deleting KataConfig2 CR successfully")
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfig2Key, kataconfig2)
				return k8sClient.Delete(context.Background(), kataconfig2)
			}, 5, time.Second).Should(Succeed())

		})
	})

})
