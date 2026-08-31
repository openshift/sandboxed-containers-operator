package kata

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	compat_otp "github.com/openshift/origin/test/extended/util/compat_otp"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = ginkgo.Describe("[sig-kata] Kata", ginkgo.Serial, func() {
	defer ginkgo.GinkgoRecover()

	var (
		oc            = compat_otp.NewCLI("kata", compat_otp.KubeConfigPath())
		cloudPlatform string
	)

	kataconfig := KataconfigDescription{
		name:             "example-kataconfig",
		runtimeClassName: "kata",
		enablePeerPods:   false,
	}

	testrun := TestRunDescription{
		checked:          false,
		runtimeClassName: kataconfig.runtimeClassName,
		enablePeerPods:   kataconfig.enablePeerPods,
		workloadImage:    "quay.io/openshift/origin-hello-openshift",
		workloadToTest:   "kata",
	}

	ginkgo.BeforeEach(func() {
		if testrun.checked {
			return
		}

		cloudPlatform = getCloudProvider(oc)
		Logf("Cloud platform: %v", cloudPlatform)

		clusterVer, _, _, minorVer := getClusterVersion(oc)
		testrun.ocpMinorVerInt = minorVer
		Logf("Cluster version: %v (minor: %v)", clusterVer, minorVer)

		configmapExists, err := getTestRunConfigmap(oc, &testrun, testrunConfigmapNs, testrunConfigmapName)
		if configmapExists {
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("osc-config validation failed: %v", err))
			kataconfig.runtimeClassName = testrun.runtimeClassName
			kataconfig.enablePeerPods = testrun.enablePeerPods
		} else {
			Logf("No osc-config configmap found, using defaults")
		}

		err = checkKataconfigIsCreated(oc, kataconfig.name)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Precondition failed: %v", err))

		if testrun.enablePeerPods {
			err = validatePeerPodsSetup(oc, kataconfig.name, cloudPlatform)
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Peer-pods setup validation failed: %v", err))
		}

		testrun.checked = true
		Logf("Suite setup complete: runtime=%v, peerpods=%v, workload=%v",
			testrun.runtimeClassName, testrun.enablePeerPods, testrun.workloadToTest)
	})

	// --- Pod Tests ---

	ginkgo.It("C00367-deploy a pod with initContainer using kata runtime [Serial]", func() {
		if testrun.workloadToTest == "coco" {
			ginkgo.Skip("Test not supported with coco")
		}

		ginkgo.By("Deploying pod with initContainer using kata runtime")
		pod := NewPodDescription(&testrun, "initcontainer")

		pod.attributes["initContainers"] = []map[string]interface{}{
			{
				"name":    "init-test",
				"image":   testrun.workloadImage,
				"command": []string{"/bin/sh", "-ec", "echo init-success >> /mnt/data/test"},
				"volumeMounts": []map[string]string{
					{"name": "shared-data", "mountPath": "/mnt/data"},
				},
			},
		}

		pod.volumes = []VolumeConfig{
			{
				name:       "shared-data",
				volumeType: "emptyDir",
				mountPath:  "/mnt/data",
			},
		}

		err := createKataPodFromDescription(oc, pod)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create pod with initContainer")
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)

		ginkgo.By("Verify initContainer completed successfully")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 2*time.Minute, true, func(_ context.Context) (bool, error) {
			initConStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"pod", pod.name, "-n", pod.namespace,
				"-o=jsonpath={.status.initContainerStatuses[0].state.terminated.reason}",
			).Output()
			if err != nil {
				return false, nil
			}
			Logf("InitContainer status: %v", initConStatus)
			if strings.Contains(initConStatus, "Completed") {
				return true, nil
			}
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "initContainer did not complete in time")

		ginkgo.By("Verify main container can read initContainer output from shared volume")
		fileContent, err := oc.AsAdmin().WithoutNamespace().Run("exec").Args(
			pod.name, "-n", pod.namespace, "--", "cat", "/mnt/data/test",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to read initContainer output from shared volume")
		o.Expect(fileContent).To(o.ContainSubstring("init-success"))
	})

	ginkgo.It("C00091-deploy kata with cpu and memory annotation [Serial]", func() {
		if testrun.workloadToTest == "coco" {
			ginkgo.Skip("Test not supported with coco")
		}

		var (
			memory             = "1234"
			cpu                = "2"
			supportedProviders = []string{"azure", "gcp", "none"}
			memoryOptions      = fmt.Sprintf("-m %vM", memory)
		)

		if kataconfig.enablePeerPods || !slices.Contains(supportedProviders, cloudPlatform) {
			ginkgo.Skip("C00091 supported only for kata runtime on platforms with nested virtualization")
		}

		ginkgo.By("Deploying pod with kata runtime and verify it")

		pod := NewPodDescription(&testrun, "example-91")
		pod.annotations = map[string]string{
			"default_memory": memory,
			"default_vcpus":  cpu,
		}

		err := createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create pod with cpu/memory annotations")

		podAnnotations, annErr := oc.WithoutNamespace().Run("get").Args("pods", pod.name, "-o=jsonpath={.metadata.annotations}", "-n", pod.namespace).Output()
		if annErr != nil {
			Logf("failed to get pod annotations: %v", annErr)
		}
		podCmd := []string{"-n", pod.namespace, pod.name, "--", "nproc"}
		actualCPU, err := oc.WithoutNamespace().AsAdmin().Run("exec").Args(podCmd...).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("'oc exec %v' Failed", podCmd))
		o.Expect(actualCPU).To(o.Equal(cpu),
			fmt.Sprintf("Actual CPU count %v isn't matching expected %v\nannotations:\n%v", actualCPU, cpu, podAnnotations))

		nodeName, nodeErr := compat_otp.GetPodNodeName(oc, pod.namespace, pod.name)
		o.Expect(nodeErr).NotTo(o.HaveOccurred(), "failed to get pod node name")
		cmd := "ps -ef | grep uuid | grep -v grep"
		vmFlags, err := compat_otp.DebugNodeWithOptionsAndChroot(oc, nodeName, []string{"-q"}, "bin/sh", "-c", cmd)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed debug node to get qemu instance options")
		o.Expect(vmFlags).To(o.ContainSubstring(memoryOptions),
			fmt.Sprintf("VM flags don't contain expected %v\nannotations:\n%v", memoryOptions, podAnnotations))

		ginkgo.By("SUCCESS - KATA pod with required VM instance size was launched")
	})

	ginkgo.It("C00350-deploy kata with resources limits cpu hot-plug [Serial]", func() {
		if testrun.workloadToTest == "coco" {
			ginkgo.Skip("Test not supported with coco")
		}

		if testrun.enablePeerPods {
			ginkgo.Skip("Test supported only with kata")
		}

		var (
			cpuRequest  = "500m"
			memRequest  = "256Mi"
			expectedCpu = "2"
			actualCPU   = "1"
		)

		ginkgo.By("Deploying pod with kata runtime and verify it")

		pod := NewPodDescription(&testrun, "example-00350")
		pod.attributes = map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]string{
					"cpu":    cpuRequest,
					"memory": memRequest,
				},
				"limits": map[string]string{
					"cpu":    cpuRequest,
					"memory": memRequest,
				},
			},
		}

		err := createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create pod for cpu hot-plug test")

		podCmd := []string{"-n", pod.namespace, pod.name, "--", "nproc", "--all"}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(_ context.Context) (bool, error) {
			actualCPU, err = oc.WithoutNamespace().AsAdmin().Run("exec").Args(podCmd...).Output()
			if err != nil {
				return false, nil
			}
			Logf("actualCPU at the moment is: %v", actualCPU)
			if strings.Contains(actualCPU, expectedCpu) {
				return true, nil
			}
			return false, nil
		})
		o.Expect(actualCPU).To(o.Equal(expectedCpu),
			fmt.Sprintf("Actual CPU count =%v isn't matching expected %v after polling for 3min", actualCPU, expectedCpu))

		ginkgo.By("SUCCESS - kata pod with required resources and hot-plugged CPU was launched")
	})

	// --- Deployment Tests ---

	ginkgo.It("C00100-expose-service deployment [Serial]", func() {
		if testrun.workloadToTest == "coco" {
			ginkgo.Skip("Test not supported with coco")
		}

		var (
			statusCode   = 200
			testPageBody = "Hello OpenShift!"
		)

		ginkgo.By("Create deployment with kata runtime")
		deploy := NewDeploymentDescription(&testrun, "dep-100-"+getRandomString(), 3)
		err := createKataDeploymentFromDescription(oc, deploy)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create expose-service deployment")
		defer deleteKataResource(oc, "deploy", deploy.namespace, deploy.name)

		ginkgo.By("Expose deployment and its service")
		defer deleteRouteAndService(oc, deploy.name, deploy.namespace)
		host, err := createServiceAndRoute(oc, deploy.name, deploy.namespace)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create service and route")
		Logf("route host=%v", host)

		ginkgo.By("Send request via the route")
		strURL := "http://" + host
		resp, err := getHttpResponse(strURL, statusCode)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("send request via the route %v failed: %v", strURL, err))
		o.Expect(resp).To(o.ContainSubstring(testPageBody), "Response doesn't match")

		ginkgo.By("SUCCESS - deployment expose service finished successfully")
	})

	ginkgo.It("C00122-Scale-up deployment [Serial]", func() {
		if testrun.workloadToTest == "coco" {
			ginkgo.Skip("Test not supported with coco")
		}

		var (
			initReplicas = 3
			maxReplicas  = 6
			baselineVMs  int
			numOfVMs     int
		)

		kataNodes := compat_otp.GetNodeListByLabel(oc, kataocLabel)
		o.Expect(len(kataNodes) > 0).To(o.BeTrue(), fmt.Sprintf("kata nodes list is empty %v", kataNodes))

		if !kataconfig.enablePeerPods {
			ginkgo.By("Record baseline VM count before the test")
			baselineVMs = getTotalInstancesOnNodes(oc, opNamespace, kataNodes)
			Logf("Baseline VM count: %v", baselineVMs)
		}

		ginkgo.By("Create deployment with kata runtime")
		deploy := NewDeploymentDescription(&testrun, "dep-122-"+getRandomString(), initReplicas)
		err := createKataDeploymentFromDescription(oc, deploy)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create scale-up deployment")
		defer deleteKataResource(oc, "deploy", deploy.namespace, deploy.name)

		if !kataconfig.enablePeerPods {
			ginkgo.By("Verifying actual number of VM instances")
			numOfVMs = getTotalInstancesOnNodes(oc, opNamespace, kataNodes)
			o.Expect(numOfVMs).To(o.Equal(baselineVMs+initReplicas), "actual number of VM instances doesn't match")
		}

		ginkgo.By(fmt.Sprintf("Scaling deployment from %v to %v", initReplicas, maxReplicas))
		err = scaleDeployment(oc, deploy, maxReplicas)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to scale up deployment")

		if !kataconfig.enablePeerPods {
			numOfVMs = getTotalInstancesOnNodes(oc, opNamespace, kataNodes)
			o.Expect(numOfVMs).To(o.Equal(baselineVMs+maxReplicas), "actual number of VM instances doesn't match")
		}
		ginkgo.By("SUCCESS - deployment scale-up finished successfully")
	})

	ginkgo.It("C00123-Scale-down deployment [Serial]", func() {
		if testrun.workloadToTest == "coco" {
			ginkgo.Skip("Test not supported with coco")
		}

		var (
			initReplicas = 6
			updReplicas  = 3
			baselineVMs  int
			numOfVMs     int
		)

		kataNodes := compat_otp.GetNodeListByLabel(oc, kataocLabel)
		o.Expect(len(kataNodes) > 0).To(o.BeTrue(), fmt.Sprintf("kata nodes list is empty %v", kataNodes))

		if !kataconfig.enablePeerPods {
			ginkgo.By("Record baseline VM count before the test")
			baselineVMs = getTotalInstancesOnNodes(oc, opNamespace, kataNodes)
			Logf("Baseline VM count: %v", baselineVMs)
		}

		ginkgo.By("Create deployment with kata runtime")
		deploy := NewDeploymentDescription(&testrun, "dep-123-"+getRandomString(), initReplicas)
		err := createKataDeploymentFromDescription(oc, deploy)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create scale-down deployment")
		defer deleteKataResource(oc, "deploy", deploy.namespace, deploy.name)

		if !kataconfig.enablePeerPods {
			ginkgo.By("Verifying actual number of VM instances")
			numOfVMs = getTotalInstancesOnNodes(oc, opNamespace, kataNodes)
			o.Expect(numOfVMs).To(o.Equal(baselineVMs+initReplicas), "actual number of VM instances doesn't match")
		}

		ginkgo.By(fmt.Sprintf("Scaling deployment from %v to %v", initReplicas, updReplicas))
		err = scaleDeployment(oc, deploy, updReplicas)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to scale down deployment")

		if !kataconfig.enablePeerPods {
			numOfVMs = getTotalInstancesOnNodes(oc, opNamespace, kataNodes)
			o.Expect(numOfVMs).To(o.Equal(baselineVMs+updReplicas), "actual number of VM instances doesn't match")
		}
		ginkgo.By("SUCCESS - deployment scale-down finished successfully")
	})

	ginkgo.It("C00192-Deployment with sidecar container [Serial]", func() {
		if testrun.workloadToTest == "coco" {
			ginkgo.Skip("Test not supported with coco")
		}

		ginkgo.By("Creating deployment with main container and sidecar")
		deploy := NewDeploymentDescription(&testrun, "sidecar-test-"+getRandomString(), 1)

		deploy.attributes["volumes"] = []map[string]interface{}{
			{"name": "shared-logs", "type": "emptyDir"},
		}

		deploy.attributes["command"] = []string{"sh", "-c", "while true; do echo kata logging >> /opt/logs.txt; sleep 2; done"}
		deploy.attributes["volumeMounts"] = []map[string]interface{}{
			{"name": "shared-logs", "mountPath": "/opt"},
		}

		deploy.attributes["initContainers"] = []map[string]interface{}{
			{
				"name":          "logshipper",
				"image":         testrun.workloadImage,
				"restartPolicy": "Always",
				"command":       []string{"sh", "-c", "tail -F /opt/logs.txt"},
				"volumeMounts": []map[string]interface{}{
					{"name": "shared-logs", "mountPath": "/opt"},
				},
			},
		}

		err := createKataDeploymentFromDescription(oc, deploy)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("%v", err))
		defer deleteKataResource(oc, "deploy", deploy.namespace, deploy.name)

		ginkgo.By("Getting pod name from deployment")
		podName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"pods", "-n", deploy.namespace,
			"-l", "app="+deploy.name,
			"-o=jsonpath={.items[0].metadata.name}",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("%v", err))
		o.Expect(podName).NotTo(o.BeEmpty(), fmt.Sprintf("pod name is:%v", podName))
		Logf("Pod name: %v", podName)

		ginkgo.By("Verify pod is running")
		msg, err := checkControlPod(oc, podName, deploy.namespace, podRunState)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("getting pod status.phase failed with: %v", msg))

		ginkgo.By("Verifying both containers are running")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(_ context.Context) (bool, error) {
			mainStatus, mainErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"pod", podName, "-n", deploy.namespace,
				"-o=jsonpath={.status.containerStatuses[0].state.running}",
			).Output()
			if mainErr != nil {
				return false, nil
			}

			sidecarStatus, sideErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"pod", podName, "-n", deploy.namespace,
				"-o=jsonpath={.status.initContainerStatuses[0].state.running}",
			).Output()
			if sideErr != nil {
				return false, nil
			}

			if mainStatus != "" && sidecarStatus != "" {
				Logf("Both containers running - main: %v, sidecar: %v", mainStatus, sidecarStatus)
				return true, nil
			}
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to verify both containers running: %v", err))

		ginkgo.By("SUCCESS - deployment with sidecar container works correctly")
	})

	// --- Peer-Pod Tests ---

	ginkgo.It("C00099-deploy peerpod with type annotation [Serial]", func() {
		if testrun.workloadToTest != "peer-pods" {
			ginkgo.Skip("Test supported only with peer-pods")
		}

		instanceSize := map[string]string{
			"aws":   "t3.xlarge",
			"azure": "Standard_D4as_v5",
			// TODO: GCP metadata returns full resource path, needs parsing
		}

		expected, ok := instanceSize[cloudPlatform]
		if !ok {
			ginkgo.Skip(fmt.Sprintf("C00099 not supported on platform %s", cloudPlatform))
		}

		ginkgo.By("Deploying peerpod with machine_type annotation")
		pod := NewPodDescription(&testrun, "example-99")
		pod.annotations = map[string]string{
			"machine_type": expected,
		}

		err := createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to create pod with machine_type annotation %s", expected))

		actual, err := getPeerPodMetadataInstanceType(oc, pod.namespace, pod.name, cloudPlatform)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to query instance type metadata from pod %v", pod.name))
		o.Expect(actual).To(o.Equal(expected),
			fmt.Sprintf("instance type %v doesn't match annotation %v", actual, expected))

		ginkgo.By("SUCCESS - peerpod with required instance type was launched")
	})

	ginkgo.It("C00131-deploy peerpod with vcpu and memory annotation [Serial]", func() {
		if testrun.workloadToTest != "peer-pods" {
			ginkgo.Skip("Test supported only with peer-pods")
		}

		instanceSize := map[string]string{
			"aws":   "t3.xlarge",
			"azure": "Standard_D4as_v5",
			// TODO: GCP metadata returns full resource path, needs parsing
		}

		expected, ok := instanceSize[cloudPlatform]
		if !ok {
			ginkgo.Skip(fmt.Sprintf("C00131 not supported on platform %s", cloudPlatform))
		}

		ginkgo.By("Deploying peerpod with vcpu and memory annotations")
		pod := NewPodDescription(&testrun, "example-131")
		pod.annotations = map[string]string{
			"default_memory": "16000",
			"default_vcpus":  "4",
		}

		err := createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create pod with vcpu/memory annotations")

		actual, err := getPeerPodMetadataInstanceType(oc, pod.namespace, pod.name, cloudPlatform)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to query instance type metadata from pod %v", pod.name))
		o.Expect(actual).To(o.Equal(expected),
			fmt.Sprintf("instance type %v doesn't match expected %v for vcpu/memory annotations", actual, expected))

		ginkgo.By("SUCCESS - peerpod with required vcpu/memory was launched")
	})

	ginkgo.It("C00320-deploy peerpod with custom tags [Serial]", func() {
		if testrun.workloadToTest != "peer-pods" {
			ginkgo.Skip("Test supported only with peer-pods")
		}

		if cloudPlatform != "azure" {
			ginkgo.Skip("C00320 custom tags supported only on Azure")
		}

		configuredTags, err := getConfigmapParamValue(oc, "TAGS")
		if err != nil || configuredTags == "" {
			ginkgo.Skip("TAGS not configured in peer-pods-cm, skipping custom tags test")
		}
		Logf("TAGS configured in peer-pods-cm: %v", configuredTags)

		ginkgo.By("Deploying peerpod and verifying custom tags from metadata")
		pod := NewPodDescription(&testrun, "example-320")

		err = createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create pod for custom tags test")

		actual, err := getPeerPodMetadataTags(oc, pod.namespace, pod.name, cloudPlatform)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to query tags metadata from pod %v", pod.name))
		o.Expect(actual).NotTo(o.BeEmpty(), "tags metadata is empty")

		ginkgo.By("SUCCESS - peerpod with custom tags verified")
	})

	ginkgo.It("C00347-deploy peerpod with existing image annotation [Serial]", func() {
		if testrun.workloadToTest != "peer-pods" {
			ginkgo.Skip("Test supported only with peer-pods")
		}

		if cloudPlatform != "aws" && cloudPlatform != "azure" {
			ginkgo.Skip(fmt.Sprintf("C00347 image metadata verification not supported on %s", cloudPlatform))
		}

		imageID, err := checkPodVMImageID(oc, cloudPlatform)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get image ID from peer-pods-cm")
		Logf("Image ID from configmap: %v", imageID)

		ginkgo.By("Deploying peerpod with image annotation")
		pod := NewPodDescription(&testrun, "example-347")
		pod.annotations = map[string]string{
			"image": imageID,
		}

		err = createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create pod with image annotation")

		actual, err := getPeerPodMetadataImageID(oc, pod.namespace, pod.name, cloudPlatform)
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to query image ID metadata from pod %v", pod.name))
		o.Expect(actual).To(o.Equal(imageID),
			fmt.Sprintf("image ID %v doesn't match annotation %v", actual, imageID))

		ginkgo.By("SUCCESS - peerpod with specified image was launched")
	})

	ginkgo.It("C00366-run [peerpodGPU] cuda-vectoradd GPUS annotated [Serial]", func() {
		if !(testrun.workloadToTest == "peer-pods" && testrun.enableGPU && cloudPlatform == "aws") {
			ginkgo.Skip("C00366 supported only on AWS with peer-pods and GPU enabled")
		}

		var (
			cudaImage            = "nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0"
			expectedInstanceType = "g5.2xlarge"
			logPassed            = "Test PASSED"
		)

		instancesParam, err := getConfigmapParamValue(oc, "PODVM_INSTANCE_TYPES")
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get PODVM_INSTANCE_TYPES from peer-pods-cm")
		o.Expect(instancesParam).To(o.ContainSubstring(expectedInstanceType),
			"expected GPU instance type missing in peer-pods-cm")

		ginkgo.By("Deploying peerpod with GPU annotation")
		pod := NewPodDescription(&testrun, "example-366")
		pod.image = cudaImage
		pod.phase = "Succeeded"
		pod.annotations = map[string]string{
			"default_gpus": "1",
		}

		err = createKataPodFromDescription(oc, pod)
		defer deleteKataResource(oc, "pod", pod.namespace, pod.name)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create pod with GPU annotation")

		ginkgo.By("Verifying cuda-vectoradd output")
		log, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args(
			pod.name, "-n", pod.namespace,
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to get logs from pod %v", pod.name))
		o.Expect(log).To(o.ContainSubstring(logPassed),
			fmt.Sprintf("cuda-vectoradd did not pass, log: %v", log))

		ginkgo.By("SUCCESS - peerpod with GPU annotation translated to instance type")
	})
})
