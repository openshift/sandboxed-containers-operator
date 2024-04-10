package controllers

import (
	"context"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	credv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	kataConfigName = "some-kataconfig"
	ccoNamespace   = "openshift-cloud-credential-operator"
	awsInfra       = configv1.AWSPlatformType
	azureInfra     = configv1.AzurePlatformType
	libvirtInfra   = configv1.NonePlatformType
)

var _ = Describe("Openshift Sandboxed Containers Credentials Controller", Ordered, func() {
	BeforeAll(func() {
		// Create the OSC Operator Namespace
		By("Creating the OSC perator Namespace")
		Expect(k8sClient.Create(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: oscNamespace,
			},
		})).Should(Succeed())

		// Create the CCO Operator Namespace
		By("Creating the CCO operator Namespace")
		Expect(k8sClient.Create(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ccoNamespace,
			},
		})).Should(Succeed())
	})
	AfterAll(func() {
		// delete namespaces
		Expect(k8sClient.Delete(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: oscNamespace,
			},
		})).Should(Succeed())

		Expect(k8sClient.Delete(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ccoNamespace,
			},
		})).Should(Succeed())
	})
	Context("KataConfig events against Credetials Controller", Ordered, func() {
		Context("AWS Enviroment", Ordered, func() {
			BeforeAll(func() {
				// mock AWS Infrastructure CR
				Expect(mockInfrastructure(awsInfra)).Should(Succeed())
			})
			AfterAll(func() {
				// delete Infrastructure CR
				Expect(k8sClient.Delete(context.Background(), &configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}})).Should(Succeed())
			})
			It("Should Create AWS Credentials Request", func() {
				kataconfig := makeKataConfig(true)

				By("Creating the KataConfig CR")
				Expect(k8sClient.Create(context.Background(), kataconfig)).Should(Succeed())
				time.Sleep(time.Second * 10)

				By("Checking Credentails Request created successfully")
				Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "openshift-sandboxed-containers-aws", Namespace: ccoNamespace}, &credv1.CredentialsRequest{})).Should(Succeed())

				// clean
				Eventually(deleteKataConfig, timeout, interval).Should(Succeed())
			})
			// kataconfig with disabled peerpods is hanging on deletion ATM, once resolved, we should test the opposite case
		})
		Context("Azure Enviroment", Ordered, func() {
			BeforeAll(func() {
				// mock Azure Infrastructure CR
				Expect(mockInfrastructure(azureInfra)).Should(Succeed())
			})
			AfterAll(func() {
				// delete Infrastructure CR
				Expect(k8sClient.Delete(context.Background(), &configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}})).Should(Succeed())
			})
			It("Should Create Azure Credentials Request", func() {
				By("Creating the KataConfig CR")
				kataconfig := makeKataConfig(true)
				Expect(k8sClient.Create(context.Background(), kataconfig)).Should(Succeed())
				time.Sleep(time.Second * 10)

				By("Checking Credentails Request created successfully")
				Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "openshift-sandboxed-containers-azure", Namespace: ccoNamespace}, &credv1.CredentialsRequest{})).Should(Succeed())

				// clean
				Eventually(deleteKataConfig, timeout, interval).Should(Succeed())
			})
		})
	})
	Context("Secret events against Credetials Controller", Ordered, func() {
		Context("AWS Enviroment", Ordered, func() {
			It("Should Create valid Peer-Pods Secret", func() {
				// prepare
				ccoSecret := makeCCOSecret(awsInfra)
				Expect(k8sClient.Create(context.Background(), ccoSecret)).Should(Succeed())
				time.Sleep(time.Second * 10)

				// test
				By("Checking peer-pods Secret created successfully")
				ppSecret := &corev1.Secret{}
				Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "peer-pods-secret", Namespace: oscNamespace}, ppSecret)).Should(Succeed())

				By("Validating the peer-pods Secret keys")
				Expect(ppSecret.Data).To(HaveKey("AWS_ACCESS_KEY_ID"))
				Expect(ppSecret.Data).To(HaveKey("AWS_SECRET_ACCESS_KEY"))

				// no built-in controllers are running in the test context, not GC, hence, check only for the OwnerReferences
				By("Validating the peer-pods Secret OwnerReferences")
				Expect(ppSecret.ObjectMeta.OwnerReferences[0].UID).To(Equal(ccoSecret.UID))
				Expect(ppSecret.ObjectMeta.OwnerReferences).To(ContainElement(HaveField("UID", ccoSecret.UID)))

				// clean
				Expect(k8sClient.Delete(context.Background(), ccoSecret)).Should(Succeed())
				Expect(k8sClient.Delete(context.Background(), ppSecret)).Should(Succeed())
			})
			Context("Azure Enviroment", Ordered, func() {
				It("Should Create valid Peer-Pods Secret", func() {
					// prepare
					ccoSecret := makeCCOSecret(azureInfra)
					Expect(k8sClient.Create(context.Background(), ccoSecret)).Should(Succeed())
					time.Sleep(time.Second * 10)

					// test
					By("Checking peer-pods Secret created successfully")
					ppSecret := &corev1.Secret{}
					Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "peer-pods-secret", Namespace: oscNamespace}, ppSecret)).Should(Succeed())

					By("Validating the peer-pods Secret keys")
					Expect(ppSecret.Data).To(HaveKey("AZURE_CLIENT_ID"))
					Expect(ppSecret.Data).To(HaveKey("AZURE_CLIENT_SECRET"))
					Expect(ppSecret.Data).To(HaveKey("AZURE_TENANT_ID"))
					Expect(ppSecret.Data).To(HaveKey("AZURE_SUBSCRIPTION_ID"))

					// no built-in controllers are running in the test context, not GC, hence, check only for the OwnerReferences
					By("Validating the peer-pods Secret OwnerReferences")
					Expect(ppSecret.ObjectMeta.OwnerReferences[0].UID).To(Equal(ccoSecret.UID))
					Expect(ppSecret.ObjectMeta.OwnerReferences).To(ContainElement(HaveField("UID", ccoSecret.UID)))

					// clean
					Expect(k8sClient.Delete(context.Background(), ccoSecret)).Should(Succeed())
					Expect(k8sClient.Delete(context.Background(), ppSecret)).Should(Succeed())
				})
			})
		})
	})

})

func deleteKataConfig() error {
	kataConfigKey := types.NamespacedName{Name: kataConfigName}
	kataconfig := &kataconfigurationv1.KataConfig{}
	err := k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	k8sClient.Patch(context.Background(), kataconfig, client.RawPatch(types.StrategicMergePatchType, []byte(`{"metadata":{"finalizers":[]}}`)))
	k8sClient.Delete(context.Background(), kataconfig) // not blocking due to finalizer?
	return errors.New("Unconfirmed KataConfig CR deletion")
}

func mockInfrastructure(provider configv1.PlatformType) error {
	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.InfrastructureSpec{
			PlatformSpec: configv1.PlatformSpec{
				Type: provider,
			},
		},
		Status: configv1.InfrastructureStatus{
			Platform:               provider,
			InfrastructureName:     "infra",
			InfrastructureTopology: configv1.SingleReplicaTopologyMode,
			ControlPlaneTopology:   configv1.SingleReplicaTopologyMode,
			PlatformStatus: &configv1.PlatformStatus{
				Type: provider,
			},
		},
	}
	if err := k8sClient.Create(context.Background(), infra); err != nil {
		return err
	}
	// update status
	infra.Status = configv1.InfrastructureStatus{
		Platform:               infra.Spec.PlatformSpec.Type,
		InfrastructureName:     "infra",
		InfrastructureTopology: configv1.SingleReplicaTopologyMode,
		ControlPlaneTopology:   configv1.SingleReplicaTopologyMode,
		PlatformStatus: &configv1.PlatformStatus{
			Type: infra.Spec.PlatformSpec.Type,
		},
	}
	return k8sClient.Status().Update(context.Background(), infra)

}

func makeKataConfig(peerpodsEnabled bool) *kataconfigurationv1.KataConfig {
	return &kataconfigurationv1.KataConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: kataConfigName,
		},
		Spec: kataconfigurationv1.KataConfigSpec{EnablePeerPods: peerpodsEnabled},
	}
}

func makeCCOSecret(provider configv1.PlatformType) *corev1.Secret {
	var data map[string][]byte
	switch strings.ToLower(string(provider)) {
	case "aws":
		data = map[string][]byte{
			"aws_access_key_id":     []byte("access"),
			"aws_secret_access_key": []byte("1234567890"),
		}
	case "azure":
		data = map[string][]byte{
			"azure_client_id":       []byte("client"),
			"azure_client_secret":   []byte("1234567890"),
			"azure_tenant_id":       []byte("tenant"),
			"azure_subscription_id": []byte("subscription"),
		}
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cco-secret",
			Namespace: oscNamespace,
		},
		Data: data,
	}
}
