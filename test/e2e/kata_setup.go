package kata

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	exutil "github.com/openshift/origin/test/extended/util"
	compat_otp "github.com/openshift/origin/test/extended/util/compat_otp"
	"github.com/tidwall/gjson"
	"k8s.io/apimachinery/pkg/util/wait"
)

// TestRunDescription holds test configuration read from the osc-config configmap.
type TestRunDescription struct {
	checked          bool
	runtimeClassName string
	enablePeerPods   bool
	workloadToTest   string
	workloadImage    string
	ocpMinorVerInt   int
}

// KataconfigDescription holds the expected kataconfig resource properties.
type KataconfigDescription struct {
	name             string
	runtimeClassName string
	enablePeerPods   bool
}

func getCloudProvider(oc *exutil.CLI) string {
	var cloudprovider string
	err := wait.PollImmediate(5*time.Second, 30*time.Second, func() (bool, error) {
		output, errMsg := oc.WithoutNamespace().AsAdmin().Run("get").Args(
			"infrastructure", "cluster", "-o=jsonpath={.status.platformStatus.type}",
		).Output()
		if errMsg != nil {
			Logf("Get cloudProvider failed with: %v, retrying...", errMsg)
			return false, nil
		}
		cloudprovider = strings.ToLower(output)
		if cloudprovider == "none" {
			cloudprovider = "libvirt"
		}
		Logf("Cluster cloud provider: %s", cloudprovider)
		return true, nil
	})
	compat_otp.AssertWaitPollNoErr(err, "Waiting for get cloudProvider timeout")
	return cloudprovider
}

func getClusterVersion(oc *exutil.CLI) (clusterVersion, ocpMajorVer, ocpMinorVer string, minorVer int) {
	jsonVersion, err := oc.AsAdmin().WithoutNamespace().Run("version").Args("-o", "json").Output()
	if err != nil || jsonVersion == "" || !gjson.Get(jsonVersion, "openshiftVersion").Exists() {
		Logf("Error: could not get oc version: %v %v", jsonVersion, err)
	}
	clusterVersion = gjson.Get(jsonVersion, "openshiftVersion").String()
	sa := strings.Split(clusterVersion, ".")
	ocpMajorVer = sa[0]
	ocpMinorVer = sa[1]
	minorVer, _ = strconv.Atoi(ocpMinorVer)
	return clusterVersion, ocpMajorVer, ocpMinorVer, minorVer
}

func getTestRunConfigmap(oc *exutil.CLI, testrun *TestRunDescription, ns, name string) (bool, error) {
	if testrun.checked {
		return true, nil
	}

	configmapData, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"configmap", "-n", ns, name, "-o", "jsonpath={.data}",
	).Output()
	if err != nil {
		Logf("osc-config configmap not found: %v", err)
		testrun.checked = true
		return false, nil
	}
	Logf("osc-config configmap found, parsing fields")

	var errorMessage string

	if gjson.Get(configmapData, "runtimeClassName").Exists() {
		testrun.runtimeClassName = gjson.Get(configmapData, "runtimeClassName").String()
		if !slices.Contains(allowedRuntimeClasses[:], testrun.runtimeClassName) {
			errorMessage += fmt.Sprintf("runtimeClassName (%v) is not allowed (%v)\n", testrun.runtimeClassName, allowedRuntimeClasses)
		}
	} else {
		errorMessage += "runtimeClassName is missing from data\n"
	}

	if gjson.Get(configmapData, "enablePeerPods").Exists() {
		testrun.enablePeerPods = gjson.Get(configmapData, "enablePeerPods").Bool()
	} else {
		errorMessage += "enablePeerPods is missing from data\n"
	}

	if gjson.Get(configmapData, "workloadImage").Exists() {
		testrun.workloadImage = gjson.Get(configmapData, "workloadImage").String()
	} else {
		errorMessage += "workloadImage is missing from data\n"
	}

	if gjson.Get(configmapData, "workloadToTest").Exists() {
		testrun.workloadToTest = gjson.Get(configmapData, "workloadToTest").String()
		if !slices.Contains(allowedWorkloadTypes[:], testrun.workloadToTest) {
			errorMessage += fmt.Sprintf("workloadToTest (%v) is not allowed (%v)\n", testrun.workloadToTest, allowedWorkloadTypes)
		}
	} else {
		errorMessage += "workloadToTest is missing from data\n"
	}

	if errorMessage != "" {
		return true, fmt.Errorf("%v", errorMessage)
	}

	testrun.checked = true
	return true, nil
}

func checkKataconfigIsCreated(oc *exutil.CLI, kcName string) error {
	msg, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"kataconfig", kcName, "-o=jsonpath={.status.conditions[?(@.type=='InProgress')].status}",
	).Output()
	if err != nil {
		return fmt.Errorf("kataconfig %v not found: %v", kcName, err)
	}
	if strings.ToLower(msg) != "false" {
		return fmt.Errorf("kataconfig %v is still in progress (status: %v)", kcName, msg)
	}
	return nil
}

func getInstancesOnNode(oc *exutil.CLI, opNamespace, node string) (int, error) {
	cmd := "ps -ef | grep uuid | grep -v grep | wc -l"
	msg, err := compat_otp.DebugNodeWithOptionsAndChroot(oc, node, []string{"-q"}, "bin/sh", "-c", cmd)
	if err != nil {
		return 0, err
	}
	instances, err := strconv.Atoi(strings.TrimSpace(msg))
	if err != nil {
		return 0, nil
	}
	return instances, nil
}

func getTotalInstancesOnNodes(oc *exutil.CLI, opNamespace string, nodeList []string) int {
	total := 0
	for i, node := range nodeList {
		count, err := getInstancesOnNode(oc, opNamespace, node)
		if err != nil {
			Logf("failed to get VM count on node %v/%v: %v", i+1, len(nodeList), err)
		}
		Logf("found %v VMs on node %v/%v", count, i+1, len(nodeList))
		total += count
	}
	Logf("Total %v VMs on all nodes", total)
	return total
}
