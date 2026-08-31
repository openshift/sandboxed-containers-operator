package kata

import (
	"fmt"
	"strings"

	exutil "github.com/openshift/origin/test/extended/util"
	"github.com/tidwall/gjson"
)

const (
	ppConfigMapName = "peer-pods-cm"
	ppRuntimeClass  = "kata-remote"
)

// checkPodVMImageID verifies the cloud-specific podvm image ID field exists
// and is non-empty in the peer-pods-cm configmap.
func checkPodVMImageID(oc *exutil.CLI, cloudPlatform string) (string, error) {
	imageIDParam := map[string]string{
		"aws":   "PODVM_AMI_ID",
		"azure": "AZURE_IMAGE_ID",
		"gcp":   "PODVM_IMAGE_NAME",
	}

	param, ok := imageIDParam[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for image ID check", cloudPlatform)
	}

	cmData, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"configmap", ppConfigMapName, "-n", opNamespace, "-o=jsonpath={.data}",
	).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get %s: %w", ppConfigMapName, err)
	}

	if !gjson.Get(cmData, param).Exists() {
		return "", fmt.Errorf("%s does not have %s", ppConfigMapName, param)
	}

	imageID := gjson.Get(cmData, param).String()
	if imageID == "" {
		return "", fmt.Errorf("%s has empty value for %s", ppConfigMapName, param)
	}

	return imageID, nil
}

// getConfigmapParamValue reads a single field from the peer-pods-cm configmap.
func getConfigmapParamValue(oc *exutil.CLI, param string) (string, error) {
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
func checkPeerPodConfigMap(oc *exutil.CLI, cloudPlatform string) error {
	requiredFields := map[string][]string{
		"aws":    {"CLOUD_PROVIDER", "AWS_REGION", "AWS_SG_IDS", "AWS_SUBNET_ID", "AWS_VPC_ID", "VXLAN_PORT"},
		"azure":  {"CLOUD_PROVIDER", "AZURE_REGION", "AZURE_NSG_ID", "AZURE_SUBNET_ID", "AZURE_RESOURCE_GROUP", "VXLAN_PORT"},
		"gcp":    {"CLOUD_PROVIDER", "GCP_ZONE", "GCP_PROJECT_ID", "GCP_NETWORK", "VXLAN_PORT"},
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
// the pod to retrieve the instance type.
func getPeerPodMetadataInstanceType(oc *exutil.CLI, namespace, podName, cloudPlatform string) (string, error) {
	metadataCurl := map[string][]string{
		"aws":   {"http://169.254.169.254/latest/meta-data/instance-type"},
		"azure": {"-H", "Metadata:true", "\\*", "http://169.254.169.254/metadata/instance/compute/vmSize?api-version=2023-07-01&format=text"},
		// TODO: GCP metadata returns full resource path, needs parsing before comparison
		// "gcp":   {"-H", "Metadata-Flavor: Google", "http://metadata.google.internal/computeMetadata/v1/instance/machine-type"},
	}

	args, ok := metadataCurl[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for metadata query", cloudPlatform)
	}

	podCmd := []string{"-n", namespace, podName, "--", "curl", "-s"}
	msg, err := oc.WithoutNamespace().AsAdmin().Run("exec").Args(append(podCmd, args...)...).Output()
	return msg, err
}

// getPeerPodMetadataImageID queries the cloud metadata service from inside
// the pod to retrieve the podvm image ID.
func getPeerPodMetadataImageID(oc *exutil.CLI, namespace, podName, cloudPlatform string) (string, error) {
	metadataCurl := map[string][]string{
		"aws":   {"http://169.254.169.254/latest/meta-data/ami-id"},
		"azure": {"-H", "Metadata:true", "\\*", "http://169.254.169.254/metadata/instance/compute/storageProfile/imageReference/id?api-version=2023-07-01&format=text"},
	}

	args, ok := metadataCurl[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for image ID metadata query", cloudPlatform)
	}

	podCmd := []string{"-n", namespace, podName, "--", "curl", "-s"}
	msg, err := oc.WithoutNamespace().AsAdmin().Run("exec").Args(append(podCmd, args...)...).Output()
	return msg, err
}

// getPeerPodMetadataTags queries the cloud metadata service from inside
// the pod to retrieve instance tags.
func getPeerPodMetadataTags(oc *exutil.CLI, namespace, podName, cloudPlatform string) (string, error) {
	metadataCurl := map[string][]string{
		"aws":   {"http://169.254.169.254/latest/meta-data/tags/instance/key1"},
		"azure": {"-H", "Metadata:true", "\\*", "http://169.254.169.254/metadata/instance/compute/tags?api-version=2023-07-01&format=text"},
	}

	args, ok := metadataCurl[cloudPlatform]
	if !ok {
		return "", fmt.Errorf("unsupported cloud platform %q for tags metadata query", cloudPlatform)
	}

	podCmd := []string{"-n", namespace, podName, "--", "curl", "-s"}
	msg, err := oc.WithoutNamespace().AsAdmin().Run("exec").Args(append(podCmd, args...)...).Output()
	return msg, err
}
