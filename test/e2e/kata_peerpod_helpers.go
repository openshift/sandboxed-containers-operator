package kata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	exutil "github.com/openshift/origin/test/extended/util"
	"github.com/tidwall/gjson"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	ppConfigMapName = "peer-pods-cm"
	ppRuntimeClass  = "kata-remote"
)

// peerPodRequiredFields must exist and be non-empty on all cloud platforms.
var peerPodRequiredFields = []string{"CLOUD_PROVIDER", "VXLAN_PORT"}

// peerPodCloudConfig defines cloud-specific peer-pods-cm requirements.
// Empty value: field must exist and be non-empty (fail setup if missing).
// Non-empty value: field must have this value (patch configmap if missing or different).
var peerPodCloudConfig = map[string]map[string]string{
	"azure": {
		"AZURE_REGION":         "",
		"AZURE_NSG_ID":         "",
		"AZURE_SUBNET_ID":      "",
		"AZURE_RESOURCE_GROUP": "",
		"AZURE_IMAGE_ID":       "",
		"AZURE_INSTANCE_SIZES": "Standard_B2als_v2,Standard_B2as_v2,Standard_D2as_v5,Standard_B4als_v2,Standard_D4as_v5,Standard_D8as_v5",
		"TAGS":                 "key1=value1,key2=value2",
	},
	"aws": {
		"AWS_REGION":           "",
		"AWS_SG_IDS":           "",
		"AWS_SUBNET_ID":        "",
		"AWS_VPC_ID":           "",
		"PODVM_AMI_ID":         "",
		"PODVM_INSTANCE_TYPES": "t3.small,t3.medium,t3.xlarge",
	},
	"gcp": {
		"GCP_ZONE":         "",
		"GCP_PROJECT_ID":   "",
		"GCP_NETWORK":      "",
		"PODVM_IMAGE_NAME": "",
	},
	"libvirt": {},
}

// imageIDField maps cloud platform to the configmap field holding the podvm image ID.
var imageIDField = map[string]string{
	"aws":   "PODVM_AMI_ID",
	"azure": "AZURE_IMAGE_ID",
	"gcp":   "PODVM_IMAGE_NAME",
}

// ensurePeerPodConfig validates peer-pods-cm and patches missing values.
// Required fields (empty value in config) must exist — setup fails if missing.
// Default fields (non-empty value in config) are patched if missing or different,
// followed by a CAA daemonset restart.
func ensurePeerPodConfig(oc *exutil.CLI, cloudPlatform string) error {
	cmJSON, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"configmap", ppConfigMapName, "-n", opNamespace, "-o", "json",
	).Output()
	if err != nil {
		return fmt.Errorf("failed to get %s: %w", ppConfigMapName, err)
	}

	var missing []string

	for _, field := range peerPodRequiredFields {
		val := gjson.Get(cmJSON, "data."+field)
		if !val.Exists() || val.String() == "" {
			missing = append(missing, field)
		}
	}

	cloudFields, ok := peerPodCloudConfig[cloudPlatform]
	if !ok {
		return fmt.Errorf("unsupported cloud platform %q", cloudPlatform)
	}

	var patchFields []string
	patchData := make(map[string]string)

	for field, requiredValue := range cloudFields {
		val := gjson.Get(cmJSON, "data."+field)
		if requiredValue == "" {
			if !val.Exists() || val.String() == "" {
				missing = append(missing, field)
			}
			continue
		}
		merged := mergeCommaSeparated(val.String(), requiredValue)
		if merged != val.String() {
			patchData[field] = merged
			patchFields = append(patchFields, field)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%s is missing required fields: %v", ppConfigMapName, missing)
	}

	if len(patchData) > 0 {
		Logf("Patching %s fields: %v", ppConfigMapName, patchFields)

		dataJSON, err := json.Marshal(map[string]interface{}{"data": patchData})
		if err != nil {
			return fmt.Errorf("failed to marshal patch: %w", err)
		}

		_, err = oc.AsAdmin().WithoutNamespace().Run("patch").Args(
			"configmap", ppConfigMapName, "-n", opNamespace,
			"--type", "merge", "-p", string(dataJSON),
		).Output()
		if err != nil {
			return fmt.Errorf("failed to patch %s: %w", ppConfigMapName, err)
		}

		if err := rebootCaaDaemonset(oc); err != nil {
			return fmt.Errorf("failed to restart CAA after config patch: %w", err)
		}
	} else {
		Logf("%s validated, no changes needed", ppConfigMapName)
	}

	return nil
}

// mergeCommaSeparated returns the current value extended with any entries
// from required that are not already present. If current is empty, returns
// required as-is. Preserves all existing entries.
func mergeCommaSeparated(current, required string) string {
	if current == "" {
		return required
	}
	existing := make(map[string]bool)
	for _, v := range strings.Split(current, ",") {
		existing[strings.TrimSpace(v)] = true
	}
	result := current
	for _, v := range strings.Split(required, ",") {
		v = strings.TrimSpace(v)
		if v != "" && !existing[v] {
			result += "," + v
		}
	}
	return result
}

// rebootCaaDaemonset triggers a rolling restart of the CAA daemonset by
// injecting a REBOOT env var, then waits for all pods to become ready.
func rebootCaaDaemonset(oc *exutil.CLI) error {
	Logf("Restarting %s daemonset", caaDaemonsetName)
	_, err := oc.AsAdmin().WithoutNamespace().Run("set").Args(
		"env", "-n", opNamespace, "ds", caaDaemonsetName,
		"REBOOT="+getRandomString(),
	).Output()
	if err != nil {
		return fmt.Errorf("failed to trigger restart of %s: %w", caaDaemonsetName, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(_ context.Context) (bool, error) {
		status, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"ds", caaDaemonsetName, "-n", opNamespace,
			"-o=jsonpath={.status.desiredNumberScheduled},{.status.numberReady}",
		).Output()
		if err != nil {
			return false, nil
		}
		parts := strings.SplitN(status, ",", 2)
		if len(parts) == 2 && parts[0] != "" && parts[0] == parts[1] {
			Logf("%s ready: %s/%s pods", caaDaemonsetName, parts[1], parts[0])
			return true, nil
		}
		return false, nil
	})
}

// getConfigmapParamValue reads a single field from the peer-pods-cm configmap.
func getConfigmapParamValue(oc *exutil.CLI, param string) (string, error) {
	cmJSON, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"configmap", ppConfigMapName, "-n", opNamespace, "-o", "json",
	).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get %s: %w", ppConfigMapName, err)
	}

	val := gjson.Get(cmJSON, "data."+param)
	if !val.Exists() {
		return "", fmt.Errorf("%s does not have field %s", ppConfigMapName, param)
	}

	return val.String(), nil
}

// checkKataconfigPeerPods verifies that the kataconfig has enablePeerPods=true.
func checkKataconfigPeerPods(oc *exutil.CLI, kcName string) error {
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
func checkRuntimeClass(oc *exutil.CLI) error {
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
func validatePeerPodsSetup(oc *exutil.CLI, kcName, cloudPlatform string) error {
	var failures []string

	if err := ensurePeerPodConfig(oc, cloudPlatform); err != nil {
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
// the pod to retrieve the instance type.
func getPeerPodMetadataInstanceType(oc *exutil.CLI, namespace, podName, cloudPlatform string) (string, error) {
	metadataCurl := map[string][]string{
		"aws":   {"http://169.254.169.254/latest/meta-data/instance-type"},
		"azure": {"-H", "Metadata:true", "http://169.254.169.254/metadata/instance/compute/vmSize?api-version=2023-07-01&format=text"},
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
func getPeerPodMetadataImageID(oc *exutil.CLI, namespace, podName, cloudPlatform string) (string, error) {
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
func getPeerPodMetadataTags(oc *exutil.CLI, namespace, podName, cloudPlatform string) (string, error) {
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
