package kata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	ppConfigMapName = "peer-pods-cm"
	ppRuntimeClass  = "kata-remote"
	// caaDsName is the cloud-api-adaptor daemonset deployed by the operator.
	caaDsName = "osc-caa-ds"
	// caaRolloutTimeout bounds a CAA daemonset rollout. The operator may have
	// to restart before it reconciles, and the pods are then replaced one node
	// at a time.
	caaRolloutTimeout = 5 * time.Minute
	// podvmImageJobName is the job the operator runs to build the podvm image.
	podvmImageJobName = "osc-podvm-image-creation"
	// podvmImageJobTimeout bounds how long the operator takes to tear down an
	// image job once the image ID is back in peer-pods-cm.
	podvmImageJobTimeout = 5 * time.Minute
)

// waitForPodvmImageJobIdle blocks until no podvm image creation job is
// running.
//
// The operator starts that job whenever the image ID is missing from
// peer-pods-cm, and on completion the job writes the newly built ID back into
// the configmap. That write rolls the CAA daemonset and repoints peer pods at
// a different image, so a test that clears the image ID has to leave the job
// finished or removed rather than running on into whatever uses the cluster
// next.
func waitForPodvmImageJobIdle(oc *CLI) error {
	ctx, cancel := context.WithTimeout(context.Background(), podvmImageJobTimeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 15*time.Second, podvmImageJobTimeout, true,
		func(_ context.Context) (bool, error) {
			active, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"job", podvmImageJobName, "-n", opNamespace,
				"--ignore-not-found", "-o=jsonpath={.status.active}",
			).Output()
			if err != nil {
				Logf("Waiting for the %s job to be queryable: %v", podvmImageJobName, err)
				return false, nil
			}

			// Empty output means the job is gone or has no running pod.
			if active == "" || active == "0" {
				Logf("No %s job is running", podvmImageJobName)
				return true, nil
			}

			Logf("Waiting for the %s job to finish or be removed (%s active pod(s))", podvmImageJobName, active)
			return false, nil
		})
}

// getCaaDaemonSetGeneration returns the CAA daemonset's metadata.generation.
// Take it as a baseline before a change the operator reacts to, so that
// waitForCaaDaemonSetRollout can tell "not reconciled yet" from "done".
func getCaaDaemonSetGeneration(oc *CLI) (string, error) {
	generation, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"daemonset", caaDsName, "-n", opNamespace, "-o=jsonpath={.metadata.generation}",
	).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get %s generation: %w", caaDsName, err)
	}
	return generation, nil
}

// waitForCaaDaemonSetRollout blocks until the operator has rewritten the CAA
// daemonset past baselineGeneration and every node runs a pod of the new
// revision.
//
// The operator builds the daemonset from peer-pods-cm, from the trusted-ca
// configmap and from its own environment, and reconciles asynchronously: a
// change to any of those rolls the daemonset some time after the change lands.
// Waiting only for the generation to move returns while pods of the previous
// revision are still being replaced, which hands a half-rolled daemonset to
// whatever runs next.
func waitForCaaDaemonSetRollout(oc *CLI, baselineGeneration string) error {
	ctx, cancel := context.WithTimeout(context.Background(), caaRolloutTimeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 10*time.Second, caaRolloutTimeout, true,
		func(_ context.Context) (bool, error) {
			dsJSON, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"daemonset", caaDsName, "-n", opNamespace, "-o=json",
			).Output()
			if err != nil {
				Logf("Waiting for the %s daemonset to be readable...", caaDsName)
				return false, nil
			}

			generation := gjson.Get(dsJSON, "metadata.generation").String()
			if generation == baselineGeneration {
				Logf("Waiting for the operator to rewrite %s (generation still %s)", caaDsName, generation)
				return false, nil
			}

			status := gjson.Get(dsJSON, "status")
			observed := status.Get("observedGeneration").String()
			desired := status.Get("desiredNumberScheduled").Int()
			updated := status.Get("updatedNumberScheduled").Int()
			available := status.Get("numberAvailable").Int()

			if observed != generation || updated != desired || available != desired {
				Logf("Waiting for %s rollout: generation=%s observed=%s updated=%d available=%d desired=%d",
					caaDsName, generation, observed, updated, available, desired)
				return false, nil
			}

			Logf("%s rolled out (generation=%s, %d pods available)", caaDsName, generation, available)
			return true, nil
		})
}

// checkPodVMImageID verifies the cloud-specific podvm image ID field exists
// and is non-empty in the peer-pods-cm configmap.
func checkPodVMImageID(oc *CLI, cloudPlatform string) (string, error) {
	imageIDParam := map[string]string{
		"aws":   "PODVM_AMI_ID",
		"azure": "AZURE_IMAGE_ID",
		"gcp":   "PODVM_IMAGE_NAME",
	}

	param, ok := imageIDParam[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for image ID check", cloudPlatform)
	}

	imageID, err := getConfigmapParamValue(oc, param)
	if err != nil {
		return "", err
	}
	if imageID == "" {
		return "", fmt.Errorf("%s has empty value for %s", ppConfigMapName, param)
	}
	return imageID, nil
}

// getConfigmapParamValue reads a single field from the peer-pods-cm configmap.
// Note: jsonpath={.data} works with gjson because oc outputs JSON-encoded data
// for configmaps with simple key=value pairs. If a future configmap value
// contains special characters, switch to -o=json and query data.<param>.
func getConfigmapParamValue(oc *CLI, param string) (string, error) {
	cmData, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"configmap", ppConfigMapName, "-n", opNamespace, "-o=jsonpath={.data}",
	).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get %s: %w", ppConfigMapName, err)
	}

	if !gjson.Get(cmData, param).Exists() {
		return "", fmt.Errorf("%s does not have field %s", ppConfigMapName, param)
	}

	return gjson.Get(cmData, param).String(), nil
}

// checkPeerPodConfigMap verifies that the peer-pods-cm configmap contains all
// required cloud-specific fields for the given provider.
func checkPeerPodConfigMap(oc *CLI, cloudPlatform string) error {
	requiredFields := map[string][]string{
		"aws":     {"CLOUD_PROVIDER", "AWS_REGION", "AWS_SG_IDS", "AWS_SUBNET_ID", "AWS_VPC_ID", "VXLAN_PORT"},
		"azure":   {"CLOUD_PROVIDER", "AZURE_REGION", "AZURE_NSG_ID", "AZURE_SUBNET_ID", "AZURE_RESOURCE_GROUP", "VXLAN_PORT"},
		"gcp":     {"CLOUD_PROVIDER", "GCP_ZONE", "GCP_PROJECT_ID", "GCP_NETWORK", "VXLAN_PORT"},
		"libvirt": {"CLOUD_PROVIDER"},
	}

	fields, ok := requiredFields[cloudPlatform]
	if !ok {
		return fmt.Errorf("unsupported cloud platform %q", cloudPlatform)
	}

	cmData, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"configmap", ppConfigMapName, "-n", opNamespace, "-o=jsonpath={.data}",
	).Output()
	if err != nil {
		return fmt.Errorf("failed to get %s: %w", ppConfigMapName, err)
	}

	var missing []string
	for _, f := range fields {
		if !gjson.Get(cmData, f).Exists() || gjson.Get(cmData, f).String() == "" {
			missing = append(missing, f)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%s is missing required fields: %v", ppConfigMapName, missing)
	}

	return nil
}

// checkKataconfigPeerPods verifies that the kataconfig has enablePeerPods=true.
func checkKataconfigPeerPods(oc *CLI, kcName string) error {
	msg, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"kataconfig", kcName, "-o=jsonpath={.spec.enablePeerPods}",
	).Output()
	if err != nil {
		return fmt.Errorf("failed to query kataconfig %s: %w", kcName, err)
	}
	if msg != "true" {
		return fmt.Errorf("kataconfig %s has enablePeerPods=%s, expected true", kcName, msg)
	}
	return nil
}

// checkRuntimeClass verifies that the kata-remote runtimeclass exists.
func checkRuntimeClass(oc *CLI) error {
	msg, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"runtimeclass", ppRuntimeClass, "--no-headers",
	).Output()
	if err != nil || !strings.Contains(msg, ppRuntimeClass) {
		return fmt.Errorf("runtimeclass %s not found: %s %v", ppRuntimeClass, msg, err)
	}
	return nil
}

// validatePeerPodsSetup runs all peer-pods precondition checks and returns
// a combined error if any fail.
func validatePeerPodsSetup(oc *CLI, kcName, cloudPlatform string) error {
	var failures []string

	if err := checkPeerPodConfigMap(oc, cloudPlatform); err != nil {
		failures = append(failures, err.Error())
	}

	if _, err := checkPodVMImageID(oc, cloudPlatform); err != nil {
		failures = append(failures, err.Error())
	}

	if err := checkKataconfigPeerPods(oc, kcName); err != nil {
		failures = append(failures, err.Error())
	}

	if err := checkRuntimeClass(oc); err != nil {
		failures = append(failures, err.Error())
	}

	if len(failures) > 0 {
		return fmt.Errorf("peer-pods setup validation failed:\n  %s", strings.Join(failures, "\n  "))
	}

	Logf("Peer-pods setup validation passed")
	return nil
}

// getPeerPodMetadataInstanceType queries the cloud metadata service from inside
// the pod to retrieve the instance type. Requires the pod to have network
// access to the cloud metadata endpoint (169.254.169.254). Peer-pod VMs always
// have this access — it is how cloud-api-adaptor itself discovers instance
// identity.
func getPeerPodMetadataInstanceType(oc *CLI, namespace, podName, cloudPlatform string) (string, error) {
	metadataCurl := map[string][]string{
		"aws":   {"http://169.254.169.254/latest/meta-data/instance-type"},
		"azure": {"-H", "Metadata:true", "http://169.254.169.254/metadata/instance/compute/vmSize?api-version=2023-07-01&format=text"},
		// TODO: GCP metadata returns full resource path, needs parsing before comparison
		// "gcp":   {"-H", "Metadata-Flavor: Google", "http://metadata.google.internal/computeMetadata/v1/instance/machine-type"},
	}

	args, ok := metadataCurl[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for metadata query", cloudPlatform)
	}

	podCmd := []string{"-n", namespace, podName, "--", "curl", "-s", "--max-time", "30"}
	msg, err := oc.WithoutNamespace().AsAdmin().Run("exec").Args(append(podCmd, args...)...).Output()
	return msg, err
}

// getPeerPodMetadataImageID queries the cloud metadata service from inside
// the pod to retrieve the podvm image ID.
func getPeerPodMetadataImageID(oc *CLI, namespace, podName, cloudPlatform string) (string, error) {
	metadataCurl := map[string][]string{
		"aws":   {"http://169.254.169.254/latest/meta-data/ami-id"},
		"azure": {"-H", "Metadata:true", "http://169.254.169.254/metadata/instance/compute/storageProfile/imageReference/id?api-version=2023-07-01&format=text"},
	}

	args, ok := metadataCurl[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for image ID metadata query", cloudPlatform)
	}

	podCmd := []string{"-n", namespace, podName, "--", "curl", "-s", "--max-time", "30"}
	msg, err := oc.WithoutNamespace().AsAdmin().Run("exec").Args(append(podCmd, args...)...).Output()
	return msg, err
}

// getPeerPodMetadataTags queries the cloud metadata service from inside
// the pod to retrieve instance tags.
func getPeerPodMetadataTags(oc *CLI, namespace, podName, cloudPlatform string) (string, error) {
	metadataCurl := map[string][]string{
		"aws":   {"http://169.254.169.254/latest/meta-data/tags/instance/key1"},
		"azure": {"-H", "Metadata:true", "http://169.254.169.254/metadata/instance/compute/tags?api-version=2023-07-01&format=text"},
	}

	args, ok := metadataCurl[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for tags metadata query", cloudPlatform)
	}

	podCmd := []string{"-n", namespace, podName, "--", "curl", "-s", "--max-time", "30"}
	msg, err := oc.WithoutNamespace().AsAdmin().Run("exec").Args(append(podCmd, args...)...).Output()
	return msg, err
}
