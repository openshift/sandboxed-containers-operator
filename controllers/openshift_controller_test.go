package controllers

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	name = "example-kataconfig"
	// Have a higher timeout to account for waits in the operator reconciliation logic
	// There are two 60s sleep in operator reconciliation logic during uninstall in addition to
	// wait time before triggering reconciliation logic
	timeout  = time.Second * 160
	interval = time.Second * 2
)

var _ = Describe("OpenShift KataConfig Controller", func() {
	Context("KataConfig create", func() {
		It("Should not support multiple KataConfig CRs", func() {

			// Create the Namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-sandboxed-containers-operator",
				},
			}

			By("Creating the namespace successfully")
			Expect(k8sClient.Create(context.Background(), ns)).Should(Succeed())

			masterMcp := &mcfgv1.MachineConfigPool{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "machineconfiguration.openshift.io/v1",
					Kind:       "MachineConfigPool",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "master",
					Namespace: "openshift-sandboxed-containers-operator", //This is needed otherwise MCP creation will fail
					Labels:    map[string]string{"pools.operator.machineconfiguration.openshift.io/master": ""},
				},
				Spec: mcfgv1.MachineConfigPoolSpec{
					MachineConfigSelector: metav1.AddLabelToSelector(&metav1.LabelSelector{}, "machineconfiguration.openshift.io/role", "master"),
					NodeSelector:          metav1.AddLabelToSelector(&metav1.LabelSelector{}, "node-role.kubernetes.io/master", ""),
					Configuration: mcfgv1.MachineConfigPoolStatusConfiguration{
						ObjectReference: corev1.ObjectReference{
							Name: "rendered-master-00",
						},
					},
				},
			}

			By("Creating the master MachineConfigPool successfully")
			Expect(k8sClient.Create(context.Background(), masterMcp)).Should(Succeed())

			By("Getting the master MachineConfigPool successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "master"}, masterMcp)
			}, 10, time.Second).Should(Succeed())

			// Update MCP status
			masterMcp.Status.MachineCount = 3
			masterMcp.Status.ReadyMachineCount = 3
			masterMcp.Status.UpdatedMachineCount = 3
			masterMcp.Status.ObservedGeneration = 1
			masterMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:    mcfgv1.MachineConfigPoolUpdated,
					Status:  corev1.ConditionTrue,
					Reason:  "",
					Message: "",
				},
			}

			By("Updating master MachineConfigPool status")
			Expect(k8sClient.Status().Update(context.Background(), masterMcp)).Should(Succeed())

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
					Annotations: map[string]string{"machineconfiguration.openshift.io/state": "Done",
						"machineconfiguration.openshift.io/currentConfig": "rendered-worker-00"},
				},
			}

			By("Creating node worker0 successfully")
			Expect(k8sClient.Create(context.Background(), node)).Should(Succeed())

			mcp := &mcfgv1.MachineConfigPool{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "machineconfiguration.openshift.io/v1",
					Kind:       "MachineConfigPool",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "worker",
					Namespace: "openshift-sandboxed-containers-operator", //This is needed otherwise MCP creation will fail
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
			}, 10, time.Second).Should(Succeed())

			// Update MCP status
			mcp.Status.MachineCount = 1
			mcp.Status.ReadyMachineCount = 1
			mcp.Status.UpdatedMachineCount = 1
			mcp.Status.UnavailableMachineCount = 0
			mcp.Status.DegradedMachineCount = 0
			mcp.Status.ObservedGeneration = 1
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

			fmt.Fprintf(GinkgoWriter, "[DEBUG] MachineConfigPool: %+v\n", mcp)

			// Both master and worker MCP exists.
			// New KataConfig should create kata-oc MCP
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

			Expect(k8sClient.Create(context.Background(), kataconfig2)).ShouldNot(Succeed())
			time.Sleep(time.Second * 5)

			// Delete
			By("Deleting KataConfig CR successfully")
			kataConfigKey := types.NamespacedName{Name: kataconfig.Name}
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
				return k8sClient.Delete(context.Background(), kataconfig)
			}, timeout, interval).Should(Succeed())

			By("Ensuring kata-oc MCP is successfully deleted")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata-oc"}, mcp)
			}, timeout, interval).ShouldNot(Succeed())

			By("Expecting to delete KataConfig CR successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
			}, timeout, interval).ShouldNot(Succeed())

		})
	})
	Context("Custom KataConfig create", func() {
		It("Should support KataConfig with custom node selector label", func() {

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

			Expect(k8sClient.Create(context.Background(), kataconfig2)).ShouldNot(Succeed())
			time.Sleep(time.Second * 5)

			//Delete
			By("Deleting KataConfig CR successfully")
			kataConfigKey := types.NamespacedName{Name: kataconfig.Name}
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
				return k8sClient.Delete(context.Background(), kataconfig)
			}, timeout, interval).Should(Succeed())

			By("Ensuring kata-oc MCP is successfully deleted")

			mcp := &mcfgv1.MachineConfigPool{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata-oc"}, mcp)
			}, timeout, interval).ShouldNot(Succeed())

			By("Expecting to delete KataConfig CR successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), kataConfigKey, kataconfig)
			}, timeout, interval).ShouldNot(Succeed())

		})

	})
	Context("Kata RuntimeClass Create", func() {
		It("Should be created after successful CR creation", func() {
			// Create KataConfig CR
			kataConfig := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "openshift-sandboxed-containers-operator",
				},
			}

			By("Creating the KataConfig CR successfully")
			Expect(k8sClient.Create(context.Background(), kataConfig)).Should(Succeed())

			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)
			}, timeout, interval).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] kataConfig: %+v\n", kataConfig)

			By("Getting the kata-oc MachineConfigPool successfully")
			kataMcp := &mcfgv1.MachineConfigPool{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata-oc"}, kataMcp)
			}, timeout, interval).Should(Succeed())

			// Change MCP state to mark it ready
			kataMcp.Status.ObservedGeneration = 1
			kataMcp.Status.UnavailableMachineCount = 0
			kataMcp.Status.MachineCount = 1
			kataMcp.Status.ReadyMachineCount = 0
			kataMcp.Status.UpdatedMachineCount = 0
			kataMcp.Status.DegradedMachineCount = 0

			kataMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdated,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			By("Updating kata-oc MCP status to Updated")
			Expect(k8sClient.Status().Update(context.Background(), kataMcp)).Should(Succeed())

			// Change node state to indicate Install in progress
			By("Updating Node status")
			nodeRet := &corev1.Node{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker0"}, nodeRet)).Should(Succeed())

			// Set Node annotations
			nodeRet.Annotations["machineconfiguration.openshift.io/state"] = "Working"
			nodeRet.Annotations["machineconfiguration.openshift.io/currentConfig"] = "rendered-worker-00"
			Expect(k8sClient.Update(context.Background(), nodeRet)).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] Node: %v\n", nodeRet)

			// Change MCP state to indicate Install in progress
			kataMcp.Status.ObservedGeneration = 2
			kataMcp.Status.UnavailableMachineCount = 0
			kataMcp.Status.MachineCount = 1
			kataMcp.Status.ReadyMachineCount = 0
			kataMcp.Status.UpdatedMachineCount = 0
			kataMcp.Status.DegradedMachineCount = 0

			kataMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdating,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			By("Updating kata-oc MCP status to Updating")
			Expect(k8sClient.Status().Update(context.Background(), kataMcp)).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] kata-oc MachineConfigPool: %+v\n", kataMcp)

			By("Checking the KataConfig CR InstallationStatus")

			Eventually(func() corev1.ConditionStatus {
				k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)
				return kataConfig.Status.InstallationStatus.IsInProgress
			}, timeout, interval).Should(Equal(corev1.ConditionTrue))

			fmt.Fprintf(GinkgoWriter, "[DEBUG] kataConfig: %v\n", kataConfig)

			// Update Spec
			By("Updating kata-oc Configuration Name")
			kataMcp.Spec.Configuration.Name = "rendered-worker-00"
			Expect(k8sClient.Update(context.Background(), kataMcp)).Should(Succeed())

			kataMcp = &mcfgv1.MachineConfigPool{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata-oc"}, kataMcp)
				return kataMcp.Spec.Configuration.Name
			}, timeout, interval).Should(Equal("rendered-worker-00"))

			fmt.Fprintf(GinkgoWriter, "[DEBUG] MachineConfigPool: %+v\n", kataMcp)

			// Change node state to indicate Install complete
			nodeRet = &corev1.Node{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker0"}, nodeRet)).Should(Succeed())

			nodeRet.Annotations["machineconfiguration.openshift.io/state"] = "Done"
			nodeRet.Annotations["machineconfiguration.openshift.io/currentConfig"] = "rendered-worker-00"
			Expect(k8sClient.Update(context.Background(), nodeRet)).Should(Succeed())

			// Change MCP state to indicate Install complete
			kataMcp.Status.ObservedGeneration = 3
			kataMcp.Status.UpdatedMachineCount = 1
			kataMcp.Status.ReadyMachineCount = 1
			kataMcp.Status.MachineCount = 1
			kataMcp.Status.UnavailableMachineCount = 0

			kataMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdated,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			By("Updating kata-oc MCP status to Updated")
			Expect(k8sClient.Status().Update(context.Background(), kataMcp)).Should(Succeed())

			By("Checking KataConfig Completed status")
			Eventually(func() int {
				k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)
				return kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount
			}, timeout, interval).Should(Equal(1))

			Expect(kataConfig.Status.InstallationStatus.Completed.CompletedNodesList).Should(ContainElement("worker0"))

			By("Creating the RuntimeClass successfully")
			rc := &nodeapi.RuntimeClass{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata"}, rc)
			}, timeout, interval).Should(Succeed())

			time.Sleep(10 * time.Second)

			By("Creating the monitor DS successfully")
			ds := &appsv1.DaemonSet{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(),
					types.NamespacedName{Name: "openshift-sandboxed-containers-monitor",
						Namespace: "openshift-sandboxed-containers-operator"}, ds)
			}, timeout, interval).Should(Succeed())
		})
	})
	Context("Adding a new worker node", func() {
		It("Should be part of Kata runtime pool", func() {

			// Create New Node
			node := &corev1.Node{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Node",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "worker1",
					Labels: map[string]string{"machineconfiguration.openshift.io/role": "worker",
						"node-role.kubernetes.io/worker": "", "node-role.kubernetes.io/kata-oc": ""},
					Annotations: map[string]string{"machineconfiguration.openshift.io/state": "",
						"machineconfiguration.openshift.io/currentConfig": "rendered-worker-00"},
				},
			}

			By("Creating node worker1 successfully")
			Expect(k8sClient.Create(context.Background(), node)).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] node: %v\n", node)

			By("Getting the kata-oc MachineConfigPool successfully")
			kataMcp := &mcfgv1.MachineConfigPool{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata-oc"}, kataMcp)
			}, timeout, interval).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] MachineConfigPool: %+v\n", kataMcp)

			By("Getting the kataConfig CR successfully")
			kataConfig := &kataconfigurationv1.KataConfig{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "example-kataconfig"}, kataConfig)
			}, timeout, interval).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] kataConfig: %+v\n", kataConfig)

			// Change node state to indicate Install in progress
			By("Updating Node status")
			nodeRet := &corev1.Node{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker1"}, nodeRet)).Should(Succeed())

			// Set Node annotations
			nodeRet.Annotations["machineconfiguration.openshift.io/state"] = "Working"
			node.Annotations["machineconfiguration.openshift.io/currentConfig"] = "rendered-worker-00"
			Expect(k8sClient.Update(context.Background(), nodeRet)).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] Node: %v\n", nodeRet)

			// Change MCP state to indicate Install in progress
			kataMcp.Status.UnavailableMachineCount = 0
			kataMcp.Status.MachineCount = 2
			kataMcp.Status.ReadyMachineCount = 1
			kataMcp.Status.UpdatedMachineCount = 1
			kataMcp.Status.DegradedMachineCount = 0

			kataMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdating,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			By("Updating kata-oc MCP status to Updating")
			Expect(k8sClient.Status().Update(context.Background(), kataMcp)).Should(Succeed())

			By("Checking the KataConfig CR InstallationStatus")
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)).Should(Succeed())

			Eventually(func() corev1.ConditionStatus {
				k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)
				return kataConfig.Status.InstallationStatus.IsInProgress
			}, timeout, interval).Should(Equal(corev1.ConditionTrue))

			fmt.Fprintf(GinkgoWriter, "[DEBUG] kataConfig: %v\n", kataConfig)

			//TBD InProgressNodesCount is not updated
			Expect(kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList).Should(ContainElement("worker1"))
			Expect(kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList).ShouldNot(ContainElement("worker0"))

			// Change node state to indicate Install complete
			nodeRet = &corev1.Node{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker1"}, nodeRet)).Should(Succeed())

			nodeRet.Annotations["machineconfiguration.openshift.io/state"] = "Done"
			nodeRet.Annotations["machineconfiguration.openshift.io/currentConfig"] = "rendered-worker-00"
			Expect(k8sClient.Update(context.Background(), nodeRet)).Should(Succeed())

			// Change MCP state to indicate Install complete
			kataMcp.Status.UpdatedMachineCount = 2
			kataMcp.Status.ReadyMachineCount = 2
			kataMcp.Status.MachineCount = 2
			kataMcp.Status.UnavailableMachineCount = 0
			kataMcp.Status.ObservedGeneration = kataMcp.Status.ObservedGeneration + 1

			kataMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdated,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			By("Updating MCP status to Updated")
			Expect(k8sClient.Status().Update(context.Background(), kataMcp)).Should(Succeed())

			By("Checking KataConfig Completed status")
			Eventually(func() int {
				k8sClient.Get(context.Background(), types.NamespacedName{Name: kataConfig.Name}, kataConfig)
				return kataConfig.Status.InstallationStatus.Completed.CompletedNodesCount
			}, timeout, interval).Should(Equal(2))

			Expect(kataConfig.Status.InstallationStatus.Completed.CompletedNodesList).Should(ContainElements("worker0", "worker1"))

			//Delete
			By("Deleting KataConfig CR successfully")
			kataConfigKey := types.NamespacedName{Name: kataConfig.Name}
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfigKey, kataConfig)
				return k8sClient.Delete(context.Background(), kataConfig)
			}, timeout, time.Second).Should(Succeed())

			By("Updating kata-oc MCP successfully")
			kataMcp.Status.UpdatedMachineCount = 0
			kataMcp.Status.ReadyMachineCount = 0
			kataMcp.Status.MachineCount = 0
			kataMcp.Status.UnavailableMachineCount = 0
			kataMcp.Status.ObservedGeneration = kataMcp.Status.ObservedGeneration + 1

			kataMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdated,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			Expect(k8sClient.Status().Update(context.Background(), kataMcp)).Should(Succeed())

			By("Getting the worker MachineConfigPool successfully")
			workerMcp := &mcfgv1.MachineConfigPool{}
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "worker"}, workerMcp)
			}, timeout, interval).Should(Succeed())

			By("Updating worker MCP successfully")
			workerMcp.Status.UpdatedMachineCount = 0
			workerMcp.Status.ReadyMachineCount = 2
			workerMcp.Status.MachineCount = 2
			workerMcp.Status.UnavailableMachineCount = 0
			workerMcp.Status.ObservedGeneration = workerMcp.Status.ObservedGeneration + 1

			workerMcp.Status.Conditions = []mcfgv1.MachineConfigPoolCondition{
				{
					Type:               mcfgv1.MachineConfigPoolUpdated,
					Status:             corev1.ConditionTrue,
					Reason:             "",
					Message:            "",
					LastTransitionTime: metav1.Time{Time: time.Now()},
				},
			}

			Expect(k8sClient.Status().Update(context.Background(), workerMcp)).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "[DEBUG] MachineConfigPool: %+v\n", workerMcp)

			By("Ensuring kata-oc MCP is successfully deleted")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "kata-oc"}, kataMcp)
			}, timeout, interval).ShouldNot(Succeed())

			By("Expecting to delete KataConfig CR successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), kataConfigKey, kataConfig)
			}, timeout, time.Second).ShouldNot(Succeed())

		})
	})
	Context("Custom KataConfig with CheckNodeEligibility create", func() {
		It("Should support KataConfig with CheckNodeEligibility set", func() {

			kataConfig := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: kataconfigurationv1.KataConfigSpec{
					CheckNodeEligibility: true,
				},
			}

			By("Creating the KataConfig CR successfully")
			Expect(k8sClient.Create(context.Background(), kataConfig)).Should(Succeed())

			// Delete
			By("Deleting KataConfig CR successfully")
			kataConfigKey := types.NamespacedName{Name: kataConfig.Name}
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfigKey, kataConfig)
				return k8sClient.Delete(context.Background(), kataConfig)
			}, timeout, interval).Should(Succeed())

			By("Expecting to delete KataConfig CR successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), kataConfigKey, kataConfig)
			}, timeout, interval).ShouldNot(Succeed())

		})
	})
	Context("Custom KataConfig with CheckNodeEligibility and PeerPods enabled", func() {
		It("Should ignore CheckNodeEligibility when PeerPods is enabled", func() {

			kataConfig := &kataconfigurationv1.KataConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kataconfiguration.openshift.io/v1",
					Kind:       "KataConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name + "-with-peerpods",
				},
				Spec: kataconfigurationv1.KataConfigSpec{
					CheckNodeEligibility: true,
					EnablePeerPods:       true,
				},
			}

			By("Creating the KataConfig CR successfully")
			Expect(k8sClient.Create(context.Background(), kataConfig)).Should(Succeed())

			// Delete
			By("Deleting KataConfig CR successfully")
			kataConfigKey := types.NamespacedName{Name: kataConfig.Name}
			Eventually(func() error {
				k8sClient.Get(context.Background(), kataConfigKey, kataConfig)
				return k8sClient.Delete(context.Background(), kataConfig)
			}, timeout, interval).Should(Succeed())

			By("Expecting to delete KataConfig CR successfully")
			Eventually(func() error {
				return k8sClient.Get(context.Background(), kataConfigKey, kataConfig)
			}, timeout, interval).ShouldNot(Succeed())

		})
	})
})
