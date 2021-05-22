package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ignTypes "github.com/coreos/ignition/v2/config/v3_2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	Context("Kata RuntimeClass Create", func() {
		It("Should be created after successful CR creation", func() {

			const (
				name = "example-kataconfig"
				timeout  = time.Second * 10
				interval = time.Second * 2
			)

			// Create the Namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-sandboxed-containers",
				},
			}

			By("Creating the namespace successfully")
			Expect(k8sClient.Create(context.Background(), ns)).Should(Succeed())

			// Create Node
			node := &corev1.Node{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Node",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "worker0",
					Labels: map[string]string{"machineconfiguration.openshift.io/role": "worker",
						"node-role.kubernetes.io/worker": ""},
					Annotations: map[string]string{"machineconfiguration.openshift.io/state": "",
						"machineconfiguration.openshift.io/currentConfig": "rendered-worker-00"},
				},
			}

			By("Creating node worker0 successfully")
			Expect(k8sClient.Create(context.Background(), node)).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] node: %v\n", node)

			//Create MachineConfig
			ic := ignTypes.Config{
				Ignition: ignTypes.Ignition{
					Version: "3.2.0",
				},
			}

			icb, _ := json.Marshal(ic)

			mc := &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "machineconfiguration.openshift.io/v1",
					Kind:       "MachineConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "00-worker",
					Namespace: "openshift-sandboxed-containers",
					Labels:    map[string]string{"machineconfiguration.openshift.io/role": "worker"},
				},
				Spec: mcfgv1.MachineConfigSpec{
					Extensions: []string{"sandboxed-containers"},
					Config: runtime.RawExtension{
						Raw: icb,
					},
				},
			}

			By("Creating the MachineConfig successfully")
			Expect(k8sClient.Create(context.Background(), mc)).Should(Succeed())

			mcp := &mcfgv1.MachineConfigPool{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "machineconfiguration.openshift.io/v1",
					Kind:       "MachineConfigPool",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "worker",
					Namespace: "openshift-sandboxed-containers", //This is needed otherwise MCP creation will fail
					Labels:    map[string]string{"pools.operator.machineconfiguration.openshift.io/worker": ""},
				},
				Spec: mcfgv1.MachineConfigPoolSpec{
					MachineConfigSelector: metav1.AddLabelToSelector(&metav1.LabelSelector{}, "machineconfiguration.openshift.io/role", "worker"),
					NodeSelector:          metav1.AddLabelToSelector(&metav1.LabelSelector{}, "node-role.kubernetes.io/worker", ""),
					Configuration: mcfgv1.MachineConfigPoolStatusConfiguration{
						ObjectReference: corev1.ObjectReference{
							Name: "rendered-worker-00",
						},
					},
				},
			}

			By("Creating the MachineConfigPool successfully")
			Expect(k8sClient.Create(context.Background(), mcp)).Should(Succeed())

			By("Getting the worker MachineConfigPool successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker"}, mcp)
			}, timeout, interval).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] MachineConfigPool: %+v\n", mcp)

			// Update MCP status
			mcp.Status.MachineCount = 1
			mcp.Status.ReadyMachineCount = 1
			mcp.Status.UpdatedMachineCount = 1
			mcp.Status.UnavailableMachineCount = 0
			mcp.Status.DegradedMachineCount = 0
			mcp.Status.ObservedGeneration = 2
			mcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:    mcfgv1.MachineConfigPoolUpdated,
					Status:  corev1.ConditionTrue,
					Reason:  "",
					Message: "",
				},
			}

			By("Updating MachineConfigPool status")
			Expect(k8sClient.Status().Update(context.Background(), mcp)).Should(Succeed())

			// Create KataConfig CR
			kataConfig := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "openshift-sandboxed-containers",
				},
			}

			By("Creating the KataConfig CR successfully")
			Expect(k8sClient.Create(context.Background(), kataConfig)).Should(Succeed())

			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)
			}, timeout, interval).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] kataConfig: %+v\n", kataConfig)

			// Change node state to indicate Install in progress
			By("Updating Node status")
			nodeRet := &corev1.Node{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker0"}, nodeRet)).Should(Succeed())

			// Set Node annotations
			nodeRet.Annotations["machineconfiguration.openshift.io/state"] = "Working"
			node.Annotations["machineconfiguration.openshift.io/currentConfig"] = "rendered-worker-00"
			Expect(k8sClient.Update(context.Background(), nodeRet)).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] Node: %v\n", nodeRet)

			// Change MCP state to indicate Install in progress
			mcp.Status.ObservedGeneration = 3
			mcp.Status.UnavailableMachineCount = 1
			mcp.Status.MachineCount = 1
			mcp.Status.ReadyMachineCount = 0
			mcp.Status.UpdatedMachineCount = 0
			mcp.Status.DegradedMachineCount = 0

			mcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdating,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			By("Updating MCP status to Updating")
			Expect(k8sClient.Status().Update(context.Background(), mcp)).Should(Succeed())

			time.Sleep(interval)

			By("Checking the KataConfig CR InstallationStatus")
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)).Should(Succeed())

			//TBD InProgressNodesCount is not updated
			Expect(kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList).Should(ContainElement("worker0"))

			// Change node state to indicate Install complete
			nodeRet = &corev1.Node{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker0"}, nodeRet)).Should(Succeed())

			nodeRet.Annotations["machineconfiguration.openshift.io/state"] = "Done"
			nodeRet.Annotations["machineconfiguration.openshift.io/currentConfig"] = "rendered-worker-00"
			Expect(k8sClient.Update(context.Background(), nodeRet)).Should(Succeed())

			// Change MCP state to indicate Install complete
			mcp.Status.ObservedGeneration = 3
			mcp.Status.UpdatedMachineCount = 1
			mcp.Status.ReadyMachineCount = 1
			mcp.Status.MachineCount = 1
			mcp.Status.UnavailableMachineCount = 0

			mcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdated,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			By("Updating MCP status to Updated")
			Expect(k8sClient.Status().Update(context.Background(), mcp)).Should(Succeed())

			time.Sleep(10 * time.Second)

			By("Checking KataConfig Completed status")
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)).Should(Succeed())

			Expect(kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount).Should(Equal(1))
			Expect(kataConfig.Status.InstallationStatus.Completed.CompletedNodesList).Should(ContainElement("worker0"))

			By("Creating the RuntimeClass successfully")
			rc := &nodeapi.RuntimeClass{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata"}, rc)
			}, timeout, interval).Should(Succeed())

		})
	})
})
