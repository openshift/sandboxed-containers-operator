/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"os"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
)

var _ = Describe("validateOCPVersion", func() {
	var (
		reconciler *KataConfigOpenShiftReconciler
		origEnv    string
	)

	BeforeEach(func() {
		// Save original environment variable
		origEnv = os.Getenv("BM_COCO_OVERRIDE_OCP_VERSION")

		// Create a minimal reconciler for testing
		reconciler = &KataConfigOpenShiftReconciler{
			Log:        logr.Discard(), // Use discard logger for tests
			kataConfig: &kataconfigurationv1.KataConfig{},
		}
	})

	AfterEach(func() {
		// Restore original environment variable
		if origEnv != "" {
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", origEnv)).To(Succeed())
		} else {
			Expect(os.Unsetenv("BM_COCO_OVERRIDE_OCP_VERSION")).To(Succeed())
		}
	})

	Context("when testing version validation logic", func() {
		// Test cases for versions below minimum requirements
		DescribeTable("should return false for versions below minimum",
			func(version string) {
				Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", version)).To(Succeed())
				valid, err := reconciler.validateOCPVersion()
				Expect(err).ToNot(HaveOccurred())
				Expect(valid).To(BeFalse())
			},
			Entry("version 4.18.0 (major.minor too low)", "4.18.0"),
			Entry("version 4.19.0 (patch too low)", "4.19.0"),
			Entry("version 4.19.27 (patch one below minimum)", "4.19.27"),
			Entry("version 4.20.0 (patch too low)", "4.20.0"),
			Entry("version 4.20.17 (patch one below minimum)", "4.20.17"),
			Entry("version 4.21.0 (patch too low)", "4.21.0"),
			Entry("version 4.21.8 (patch one below minimum)", "4.21.8"),
		)

		// Test cases for versions at or above minimum requirements
		DescribeTable("should return true for versions at or above minimum",
			func(version string) {
				Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", version)).To(Succeed())
				valid, err := reconciler.validateOCPVersion()
				Expect(err).ToNot(HaveOccurred())
				Expect(valid).To(BeTrue())
			},
			Entry("version 4.19.28 (exact minimum for 4.19)", "4.19.28"),
			Entry("version 4.19.29 (above minimum for 4.19)", "4.19.29"),
			Entry("version 4.19.100 (well above minimum for 4.19)", "4.19.100"),
			Entry("version 4.20.18 (exact minimum for 4.20)", "4.20.18"),
			Entry("version 4.20.19 (above minimum for 4.20)", "4.20.19"),
			Entry("version 4.20.100 (well above minimum for 4.20)", "4.20.100"),
			Entry("version 4.21.9 (exact minimum for 4.21)", "4.21.9"),
			Entry("version 4.21.10 (above minimum for 4.21)", "4.21.10"),
			Entry("version 4.21.100 (well above minimum for 4.21)", "4.21.100"),
			Entry("version 4.22.0 (higher minor, assumed supported)", "4.22.0"),
			Entry("version 4.25.1 (much higher minor, assumed supported)", "4.25.1"),
			Entry("version 5.0.0 (higher major, assumed supported)", "5.0.0"),
		)

		// Test cases for invalid version formats
		It("should return error for invalid version format", func() {
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", "invalid-version")).To(Succeed())
			valid, err := reconciler.validateOCPVersion()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
			Expect(err.Error()).To(ContainSubstring("unable to parse current OCP version"))
		})

		It("should return error for malformed version", func() {
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", "not-a-version")).To(Succeed())
			valid, err := reconciler.validateOCPVersion()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
		})

		It("should handle version with pre-release suffix", func() {
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", "4.21.9-rc1")).To(Succeed())
			valid, err := reconciler.validateOCPVersion()
			Expect(err).ToNot(HaveOccurred())
			// Pre-release versions (4.21.9-rc1) are considered LESS than their base (4.21.9) by semver
			// So this should return false since it's technically below the minimum
			Expect(valid).To(BeFalse())
		})
	})

	Context("when BM_COCO_OVERRIDE_OCP_VERSION is not set", func() {
		It("should attempt to fetch from cluster", func() {
			Expect(os.Unsetenv("BM_COCO_OVERRIDE_OCP_VERSION")).To(Succeed())

			// In unit test environment, this will use the test k8s client from suite_test.go
			// The cluster won't have the version CRD populated, so it should return an error
			reconciler.Client = k8sClient
			_, err := reconciler.validateOCPVersion()
			Expect(err).To(HaveOccurred())
		})
	})

	Context("edge cases", func() {
		It("should handle version 4.19.27 correctly (just below threshold)", func() {
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", "4.19.27")).To(Succeed())
			valid, err := reconciler.validateOCPVersion()
			Expect(err).ToNot(HaveOccurred())
			Expect(valid).To(BeFalse())
		})

		It("should handle version 4.19.28 correctly (exactly at threshold)", func() {
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", "4.19.28")).To(Succeed())
			valid, err := reconciler.validateOCPVersion()
			Expect(err).ToNot(HaveOccurred())
			Expect(valid).To(BeTrue())
		})

		It("should handle versions between supported minors", func() {
			// 4.19.28+ is supported, 4.20.18+ is supported
			// But 4.20.0 to 4.20.17 should not be supported
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", "4.20.10")).To(Succeed())
			valid, err := reconciler.validateOCPVersion()
			Expect(err).ToNot(HaveOccurred())
			Expect(valid).To(BeFalse())
		})
	})
})

var _ = Describe("validateOCPVersion integration", func() {
	var reconciler *KataConfigOpenShiftReconciler

	BeforeEach(func() {
		reconciler = &KataConfigOpenShiftReconciler{
			Client:     k8sClient,
			Log:        ctrl.Log.WithName("test").WithName("validateOCPVersion"),
			Scheme:     k8sManager.GetScheme(),
			kataConfig: &kataconfigurationv1.KataConfig{},
		}
	})

	Context("with environment override", func() {
		AfterEach(func() {
			Expect(os.Unsetenv("BM_COCO_OVERRIDE_OCP_VERSION")).To(Succeed())
		})

		It("should use environment variable when set", func() {
			Expect(os.Setenv("BM_COCO_OVERRIDE_OCP_VERSION", "4.21.9")).To(Succeed())
			valid, err := reconciler.validateOCPVersion()
			Expect(err).ToNot(HaveOccurred())
			Expect(valid).To(BeTrue())
		})
	})
})
