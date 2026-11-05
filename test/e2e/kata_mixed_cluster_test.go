package kata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = ginkgo.Describe("[sig-kata] Mixed Cluster", ginkgo.Serial, func() {
	defer ginkgo.GinkgoRecover()

	var (
		oc = NewOC("kata-mixed")
	)

	testrun := TestRunDescription{
		checked:        false,
		workloadImage:  "ghcr.io/nginxinc/nginx-unprivileged:alpine@sha256:e234d4f7e346c098f7b22245690372382aded5be988611eb2c53ae62231eece7",
		workloadToTest: "kata",
	}

	isMixedClusterEnabled := func() bool {
		val, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"kataconfig", "example-kataconfig",
			"-o=jsonpath={.spec.enableMixedCluster}",
		).Output()
		if err != nil {
			ginkgo.Skip(fmt.Sprintf("cannot read KataConfig: %v", err))
		}
		return val == "true"
	}

	hasMixedHardware := func() bool {
		bmNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", nodeTypeLabelKey+"=bare-metal", "--no-headers",
		).Output()
		if err != nil {
			ginkgo.Skip(fmt.Sprintf("cannot determine cluster type: %v", err))
		}
		vmNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", nodeTypeLabelKey+"=virtual", "--no-headers",
		).Output()
		if err != nil {
			ginkgo.Skip(fmt.Sprintf("cannot determine cluster type: %v", err))
		}
		return len(bmNodes) > 0 && len(vmNodes) > 0
	}

	ginkgo.BeforeEach(func() {
		if !isMixedClusterEnabled() {
			ginkgo.Skip("Mixed cluster tests require enableMixedCluster=true in KataConfig")
		}

		if !testrun.checked {
			configmapExists, err := getTestRunConfigmap(oc, &testrun, testrunConfigmapNs, testrunConfigmapName)
			if configmapExists {
				o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("osc-config validation failed: %v", err))
			}
			testrun.checked = true
		}
	})

	// --- Node Label Tests ---

	ginkgo.It("MC01-verify BM nodes labeled with node-type and kata-capable [Serial]", func() {
		ginkgo.By("Checking BM nodes have node-type=bare-metal")
		bmNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", nodeTypeLabelKey+"=bare-metal", "-o=jsonpath={.items[*].metadata.name}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list BM nodes")
		if bmNodes == "" {
			ginkgo.Skip("No bare-metal nodes found in cluster")
		}
		Logf("BM nodes: %v", bmNodes)

		ginkgo.By("Checking BM nodes have kata-capable=true")
		capableNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", kataCapableLabelKey+"=true,"+nodeTypeLabelKey+"=bare-metal",
			"-o=jsonpath={.items[*].metadata.name}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list kata-capable BM nodes")
		o.Expect(capableNodes).To(o.Equal(bmNodes), "not all BM nodes are labeled kata-capable")

		ginkgo.By("SUCCESS - all BM nodes labeled correctly")
	})

	ginkgo.It("MC02-verify nested-virt VM nodes labeled with kata-capable and nested-virt-capable [Serial]", func() {
		if !hasMixedHardware() {
			ginkgo.Skip("Requires both BM and VM nodes")
		}
		nestedNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", nestedVirtLabelKey+"=true", "-o=jsonpath={.items[*].metadata.name}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list nested-virt nodes")

		if nestedNodes == "" {
			ginkgo.Skip("No nested-virt-capable nodes found in cluster")
		}

		Logf("Nested-virt nodes: %v", nestedNodes)

		ginkgo.By("Checking nested-virt nodes also have kata-capable=true")
		capableNested, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", nestedVirtLabelKey+"=true,"+kataCapableLabelKey+"=true",
			"-o=jsonpath={.items[*].metadata.name}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list kata-capable nested-virt nodes")
		o.Expect(capableNested).To(o.Equal(nestedNodes),
			"not all nested-virt nodes are labeled kata-capable")

		ginkgo.By("Checking nested-virt nodes also have node-type=virtual")
		virtualNested, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", nestedVirtLabelKey+"=true,"+nodeTypeLabelKey+"=virtual",
			"-o=jsonpath={.items[*].metadata.name}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list virtual nested-virt nodes")
		o.Expect(virtualNested).To(o.Equal(nestedNodes),
			"not all nested-virt nodes are labeled node-type=virtual")

		ginkgo.By("SUCCESS - nested-virt nodes labeled correctly")
	})

	// --- RuntimeClass nodeSelector Tests ---

	ginkgo.It("MC03-verify kata RuntimeClass has kata-capable nodeSelector [Serial]", func() {
		ginkgo.By("Checking kata RuntimeClass nodeSelector")
		sel, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata", "-o=jsonpath={.scheduling.nodeSelector}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kata RuntimeClass")
		o.Expect(sel).To(o.ContainSubstring(kataCapableLabelKey),
			fmt.Sprintf("kata RuntimeClass missing %v in nodeSelector: %v", kataCapableLabelKey, sel))

		ginkgo.By("SUCCESS - kata RuntimeClass has kata-capable nodeSelector")
	})

	ginkgo.It("MC04-verify kata-remote RuntimeClass has node-type=virtual nodeSelector [Serial]", func() {
		if !hasMixedHardware() {
			ginkgo.Skip("Requires both BM and VM nodes")
		}
		ginkgo.By("Checking if kata-remote RuntimeClass exists")
		_, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-remote",
		).Output()
		if err != nil {
			ginkgo.Skip("kata-remote RuntimeClass not present")
		}

		sel, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-remote", "-o=jsonpath={.scheduling.nodeSelector}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kata-remote RuntimeClass nodeSelector")
		o.Expect(sel).To(o.ContainSubstring("virtual"),
			fmt.Sprintf("kata-remote RuntimeClass missing node-type=virtual in nodeSelector: %v", sel))

		ginkgo.By("SUCCESS - kata-remote RuntimeClass has node-type=virtual nodeSelector")
	})

	ginkgo.It("MC05-verify kata-nvidia-gpu RuntimeClass has node-type=bare-metal nodeSelector [Serial]", func() {
		ginkgo.By("Checking if kata-nvidia-gpu RuntimeClass exists")
		_, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-nvidia-gpu",
		).Output()
		if err != nil {
			ginkgo.Skip("kata-nvidia-gpu RuntimeClass not present")
		}

		sel, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-nvidia-gpu", "-o=jsonpath={.scheduling.nodeSelector}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kata-nvidia-gpu RuntimeClass nodeSelector")
		o.Expect(sel).To(o.ContainSubstring("bare-metal"),
			fmt.Sprintf("kata-nvidia-gpu missing node-type=bare-metal in nodeSelector: %v", sel))

		ginkgo.By("SUCCESS - kata-nvidia-gpu RuntimeClass has bare-metal nodeSelector")
	})

	ginkgo.It("MC06-verify kata-cc RuntimeClass has node-type=bare-metal nodeSelector [Serial]", func() {
		ginkgo.By("Checking if kata-cc RuntimeClass exists")
		_, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-cc",
		).Output()
		if err != nil {
			ginkgo.Skip("kata-cc RuntimeClass not present")
		}

		sel, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-cc", "-o=jsonpath={.scheduling.nodeSelector}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kata-cc RuntimeClass nodeSelector")
		o.Expect(sel).To(o.ContainSubstring("bare-metal"),
			fmt.Sprintf("kata-cc missing node-type=bare-metal in nodeSelector: %v", sel))

		ginkgo.By("SUCCESS - kata-cc RuntimeClass has bare-metal nodeSelector")
	})

	ginkgo.It("MC07-verify kata-cc-nvidia-gpu RuntimeClass has node-type=bare-metal nodeSelector [Serial]", func() {
		ginkgo.By("Checking if kata-cc-nvidia-gpu RuntimeClass exists")
		_, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-cc-nvidia-gpu",
		).Output()
		if err != nil {
			ginkgo.Skip("kata-cc-nvidia-gpu RuntimeClass not present")
		}

		sel, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-cc-nvidia-gpu", "-o=jsonpath={.scheduling.nodeSelector}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kata-cc-nvidia-gpu RuntimeClass nodeSelector")
		o.Expect(sel).To(o.ContainSubstring("bare-metal"),
			fmt.Sprintf("kata-cc-nvidia-gpu missing node-type=bare-metal in nodeSelector: %v", sel))

		ginkgo.By("SUCCESS - kata-cc-nvidia-gpu RuntimeClass has bare-metal nodeSelector")
	})

	// --- Pod Scheduling Tests ---

	ginkgo.It("MC08-deploy kata pod on kata-capable node [Serial]", func() {
		if !hasMixedHardware() {
			ginkgo.Skip("Requires both BM and VM nodes")
		}
		ginkgo.By("Deploying kata pod")
		testrun.runtimeClassName = "kata"
		pod := NewPodDescription(&testrun, "mixed-kata")

		err := createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create kata pod")

		ginkgo.By("Verifying pod landed on kata-capable node")
		nodeName, err := GetPodNodeName(oc, pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get pod node name")

		nodeLabels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"node", nodeName, "-o=jsonpath={.metadata.labels}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get node labels")
		o.Expect(nodeLabels).To(o.ContainSubstring(kataCapableLabelKey),
			fmt.Sprintf("kata pod landed on node %v without kata-capable label", nodeName))

		ginkgo.By("SUCCESS - kata pod scheduled on kata-capable node")
	})

	ginkgo.It("MC09-deploy kata-remote pod on virtual node [Serial]", func() {
		if !hasMixedHardware() {
			ginkgo.Skip("Requires both BM and VM nodes")
		}
		_, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", "kata-remote",
		).Output()
		if err != nil {
			ginkgo.Skip("kata-remote RuntimeClass not present")
		}
		ginkgo.By("Deploying kata-remote pod")
		testrun.runtimeClassName = "kata-remote"
		pod := NewPodDescription(&testrun, "mixed-remote")

		err = createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create kata-remote pod")

		ginkgo.By("Verifying pod landed on virtual node")
		nodeName, err := GetPodNodeName(oc, pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get pod node name")

		nodeType, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"node", nodeName, fmt.Sprintf(`-o=jsonpath={.metadata.labels.%s}`, strings.ReplaceAll(nodeTypeLabelKey, ".", `\.`)),
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get node-type label")
		o.Expect(nodeType).To(o.Equal("virtual"),
			fmt.Sprintf("kata-remote pod landed on node %v with node-type=%v, expected virtual", nodeName, nodeType))

		ginkgo.By("SUCCESS - kata-remote pod scheduled on virtual node")
	})

	ginkgo.It("MC10-verify kata pod does not land on non-kata-capable node [Serial]", func() {
		if !hasMixedHardware() {
			ginkgo.Skip("Requires both BM and VM nodes")
		}
		ginkgo.By("Checking that no kata-oc node has kata pod without kata-capable label")
		pods, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"pods", "--all-namespaces", "--field-selector=status.phase=Running",
			"-o=jsonpath={range .items[?(@.spec.runtimeClassName==\"kata\")]}{.spec.nodeName}{\"\\n\"}{end}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list running kata pods")

		if pods == "" {
			ginkgo.Skip("No running kata pods found to verify")
		}

		for _, nodeName := range strings.Split(strings.TrimSpace(pods), "\n") {
			if nodeName == "" {
				continue
			}
			capable, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"node", nodeName,
				fmt.Sprintf(`-o=jsonpath={.metadata.labels.%s}`, strings.ReplaceAll(kataCapableLabelKey, ".", `\.`)),
			).Output()
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to check labels on node %v", nodeName))
			o.Expect(capable).To(o.Equal("true"),
				fmt.Sprintf("kata pod running on node %v without kata-capable=true", nodeName))
		}

		ginkgo.By("SUCCESS - no kata pods on non-kata-capable nodes")
	})

	// --- Day-2 Tests ---

	ginkgo.It("MC11-verify EnableMixedCluster toggle off and on restores labels and nodeSelectors [Serial]", func() {
		ginkgo.By("Recording current node labels before toggle")
		bmBefore, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"nodes", "-l", nodeTypeLabelKey+"=bare-metal", "-o=jsonpath={.items[*].metadata.name}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list BM nodes before toggle")
		if bmBefore == "" {
			ginkgo.Skip("No bare-metal nodes found in cluster")
		}

		ginkgo.By("Toggling EnableMixedCluster to false")
		_, err = oc.AsAdmin().WithoutNamespace().Run("patch").Args(
			"kataconfig", "example-kataconfig", "--type=merge",
			"-p", `{"spec":{"enableMixedCluster":false}}`,
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to patch kataconfig to disable mixed cluster")

		ginkgo.DeferCleanup(func() {
			ginkgo.By("Restoring EnableMixedCluster to true")
			_, restoreErr := oc.AsAdmin().WithoutNamespace().Run("patch").Args(
				"kataconfig", "example-kataconfig", "--type=merge",
				"-p", `{"spec":{"enableMixedCluster":true}}`,
			).Output()
			o.Expect(restoreErr).NotTo(o.HaveOccurred(), "failed to restore enableMixedCluster")
		})

		ginkgo.By("Waiting for node-type labels to be removed")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(_ context.Context) (bool, error) {
			bmNodes, pollErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"nodes", "-l", nodeTypeLabelKey+"=bare-metal", "--no-headers",
			).Output()
			if pollErr != nil {
				Logf("Error checking node labels: %v", pollErr)
				return false, nil
			}
			if bmNodes == "" {
				return true, nil
			}
			Logf("Still waiting for node-type labels to be removed...")
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "node-type labels not removed after disabling mixed cluster")

		ginkgo.By("Waiting for kata RuntimeClass to remove kata-capable nodeSelector")
		ctxRC, cancelRC := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancelRC()
		err = wait.PollUntilContextTimeout(ctxRC, 5*time.Second, 1*time.Minute, true, func(_ context.Context) (bool, error) {
			sel, pollErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"runtimeclass", "kata", "-o=jsonpath={.scheduling.nodeSelector}",
			).Output()
			if pollErr != nil {
				Logf("Error checking kata RC nodeSelector: %v", pollErr)
				return false, nil
			}
			if !strings.Contains(sel, kataCapableLabelKey) {
				return true, nil
			}
			Logf("Still waiting for kata RC nodeSelector to be updated...")
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "kata RuntimeClass still has kata-capable after toggle off")

		ginkgo.By("Toggling EnableMixedCluster back to true")
		_, err = oc.AsAdmin().WithoutNamespace().Run("patch").Args(
			"kataconfig", "example-kataconfig", "--type=merge",
			"-p", `{"spec":{"enableMixedCluster":true}}`,
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to patch kataconfig to re-enable mixed cluster")

		ginkgo.By("Waiting for node-type labels to be reapplied")
		ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel2()
		err = wait.PollUntilContextTimeout(ctx2, 10*time.Second, 3*time.Minute, true, func(_ context.Context) (bool, error) {
			bmNodes, pollErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"nodes", "-l", nodeTypeLabelKey+"=bare-metal", "--no-headers",
			).Output()
			if pollErr != nil {
				Logf("Error checking node labels: %v", pollErr)
				return false, nil
			}
			if bmNodes != "" {
				return true, nil
			}
			Logf("Still waiting for node-type labels to be reapplied...")
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "node-type labels not reapplied after re-enabling mixed cluster")

		ginkgo.By("Waiting for kata RuntimeClass to restore kata-capable nodeSelector")
		ctxRC2, cancelRC2 := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancelRC2()
		err = wait.PollUntilContextTimeout(ctxRC2, 5*time.Second, 1*time.Minute, true, func(_ context.Context) (bool, error) {
			sel, pollErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"runtimeclass", "kata", "-o=jsonpath={.scheduling.nodeSelector}",
			).Output()
			if pollErr != nil {
				Logf("Error checking kata RC nodeSelector: %v", pollErr)
				return false, nil
			}
			if strings.Contains(sel, kataCapableLabelKey) {
				return true, nil
			}
			Logf("Still waiting for kata RC nodeSelector to be restored...")
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "kata RuntimeClass missing kata-capable after toggle on")

		ginkgo.By("SUCCESS - EnableMixedCluster toggle off/on restores labels and nodeSelectors")
	})
})
