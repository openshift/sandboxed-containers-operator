package kata

import (
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// AssertWaitPollNoErr fails the test if err is non-nil. When err is a timeout,
// the error message is replaced with msg for clearer CI output.
func AssertWaitPollNoErr(e error, msg string) {
	if e == nil {
		return
	}
	var err error
	if e.Error() == "timed out waiting for the condition" || e.Error() == "context deadline exceeded" {
		err = fmt.Errorf("case: %v\nerror: %s", ginkgo.CurrentSpecReport().FullText(), msg)
	} else {
		err = fmt.Errorf("case: %v\nerror: %s", ginkgo.CurrentSpecReport().FullText(), e.Error())
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

// GetPodNodeName returns the node name where the given pod is scheduled.
func GetPodNodeName(oc *CLI, namespace, podName string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"pod", podName, "-n", namespace, "-o=jsonpath={.spec.nodeName}",
	).Output()
}

// GetNodeListByLabel returns node names matching the given label selector.
func GetNodeListByLabel(oc *CLI, labelKey string) []string {
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"node", "-l", labelKey, "-o=jsonpath={.items[*].metadata.name}",
	).Output()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"failed to get nodes with label %v: %v", labelKey, err)
	return strings.Fields(output)
}

// DebugNodeWithOptionsAndChroot runs oc debug node/<node> with chroot /host.
func DebugNodeWithOptionsAndChroot(oc *CLI, nodeName string, options []string, cmd ...string) (string, error) {
	debugNs := oc.Namespace()
	if debugNs == "" {
		debugNs = "default"
	}

	needsRecover := false
	isPriv, err := isNamespacePrivileged(oc, debugNs)
	if err != nil {
		return "", fmt.Errorf("failed to check namespace privileges: %w", err)
	}
	if !isPriv {
		if labelErr := setNamespacePrivileged(oc, debugNs); labelErr != nil {
			return "", fmt.Errorf("failed to set namespace privileged: %w", labelErr)
		}
		needsRecover = true
		defer func() {
			if needsRecover {
				if err := recoverNamespaceRestricted(oc, debugNs); err != nil {
					Logf("warning: failed to restore namespace %s labels: %v", debugNs, err)
				}
			}
		}()
	}

	cargs := []string{"node/" + nodeName}
	cargs = append(cargs, options...)
	cargs = append(cargs, "--to-namespace="+debugNs)
	cargs = append(cargs, "--", "chroot", "/host")
	cargs = append(cargs, cmd...)

	return oc.AsAdmin().WithoutNamespace().Run("debug").Args(cargs...).Output()
}

func isNamespacePrivileged(oc *CLI, namespace string) (bool, error) {
	out, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"namespace", namespace,
		`-o=jsonpath={.metadata.labels.pod-security\.kubernetes\.io/enforce}`,
	).Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "privileged", nil
}

func setNamespacePrivileged(oc *CLI, namespace string) error {
	return oc.AsAdmin().WithoutNamespace().Run("label").Args(
		"namespace", namespace,
		"pod-security.kubernetes.io/enforce=privileged",
		"pod-security.kubernetes.io/audit=privileged",
		"pod-security.kubernetes.io/warn=privileged",
		"security.openshift.io/scc.podSecurityLabelSync=false",
		"--overwrite",
	).Execute()
}

func recoverNamespaceRestricted(oc *CLI, namespace string) error {
	return oc.AsAdmin().WithoutNamespace().Run("label").Args(
		"namespace", namespace,
		"pod-security.kubernetes.io/enforce-",
		"pod-security.kubernetes.io/audit-",
		"pod-security.kubernetes.io/warn-",
		"security.openshift.io/scc.podSecurityLabelSync-",
	).Execute()
}
