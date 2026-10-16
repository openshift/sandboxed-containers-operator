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
	// Ginkgo runs a spec matching any focus string, so only default to sig-kata
	// when the caller gave none: appending it unconditionally would widen every
	// -ginkgo.focus back to the whole suite.
	if len(suiteConfig.FocusStrings) == 0 {
		suiteConfig.FocusStrings = append(suiteConfig.FocusStrings, "sig-kata")
	}
	// A focus that selects nothing otherwise reports success without running a
	// single spec.
	suiteConfig.FailOnEmpty = true
	ginkgo.RunSpecs(t, "OSC Kata E2E Suite", suiteConfig, reporterConfig)
}
