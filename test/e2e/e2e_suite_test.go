package kata

import (
	"os"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func TestKataE2E(t *testing.T) {
	if os.Getenv("KUBECONFIG") == "" {
		t.Skip("KUBECONFIG not set, skipping kata e2e tests")
	}
	gomega.RegisterFailHandler(ginkgo.Fail)
	suiteConfig, reporterConfig := ginkgo.GinkgoConfiguration()
	suiteConfig.FocusStrings = append(suiteConfig.FocusStrings, "sig-kata")
	ginkgo.RunSpecs(t, "OSC Kata E2E Suite", suiteConfig, reporterConfig)
}
