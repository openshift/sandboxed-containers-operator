package kata

import (
	"os"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	exutil "github.com/openshift/origin/test/extended/util"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

func TestKataE2E(t *testing.T) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set, skipping kata e2e tests")
	}
	e2e.TestContext.KubeConfig = kubeconfig
	e2e.TestContext.Provider = "skeleton"
	provider, err := e2e.SetupProviderConfig(e2e.TestContext.Provider)
	if err != nil {
		t.Fatalf("Failed to setup provider: %v", err)
	}
	e2e.TestContext.CloudConfig.Provider = provider
	e2e.TestContext.DeleteNamespace = true

	gomega.RegisterFailHandler(ginkgo.Fail)
	suiteConfig, reporterConfig := ginkgo.GinkgoConfiguration()
	suiteConfig.FocusStrings = append(suiteConfig.FocusStrings, "sig-kata")
	exutil.WithCleanup(func() {
		ginkgo.RunSpecs(t, "OSC Kata E2E Suite", suiteConfig, reporterConfig)
	})
}
