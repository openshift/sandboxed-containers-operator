package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kataconfigurationv1 "github.com/openshift/kata-operator/api/v1"
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
			Expect(k8sClient.Get(context.Background(), kataConfig2Key, kataconfig2)).Should(Succeed())

			By("Creating marking the second KataConfig CR correctly")
			Expect(kataconfig2.Status.InstallationStatus.Failed.FailedNodesCount).Should(Equal(-1))
		})
		It("Should return master in combined master/worker cluster", func() {
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

			key := types.NamespacedName{
				Name:      "example-kataconfig",
				Namespace: "kata-operator-system",
			}

			const timeout = time.Second * 30
			const interval = time.Second * 1

			By("Creating the KataConfig CR successfully")
			Expect(k8sClient.Create(context.Background(), kataconfig)).Should(Succeed())
			time.Sleep(time.Second * 5)

			exampleKataconfig := &kataconfigurationv1.KataConfig{}
			Eventually(func() bool {
				k8sClient.Get(context.Background(), key, exampleKataconfig)
				return exampleKataconfig.Status.TotalNodesCount == exampleKataconfig.Status.InstallationStatus.Completed.CompletedNodesCount
			}, timeout, interval).Should(BeTrue())
		})
	})
})
