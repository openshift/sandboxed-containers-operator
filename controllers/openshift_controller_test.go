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
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), kataConfig2Key, kataconfig2)
				if err != nil {
					return false
				}
				return true
			}, 5, time.Second).Should(BeTrue())

			By("Creating and marking the second KataConfig CR correctly")
			Expect(kataconfig2.Status.InstallationStatus.Failed.FailedNodesCount).Should(Equal(-1))
		})
	})

})
