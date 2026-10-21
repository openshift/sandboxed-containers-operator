package kata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	k8sversion "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = ginkgo.Describe("[sig-kata] ConsolePlugin", ginkgo.Serial, func() {
	defer ginkgo.GinkgoRecover()

	oc := NewOC("consoleplugin")

	consolePlugins := []struct {
		name string
	}{
		{"osc-openshift-console-plugin"},
		{"coco-openshift-console-plugin"},
	}

	for _, cp := range consolePlugins {
		cp := cp

		ginkgo.It(fmt.Sprintf("C00400-verify %s is deployed and available [Serial]", cp.name), func() {
			clusterVer, _, _, _ := getClusterVersion(oc)
			v, err := k8sversion.ParseGeneric(clusterVer)
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("cannot parse cluster version %q", clusterVer))
			// Compare major and minor together: 5.0 is newer than 4.22 even
			// though its minor (0) is smaller.
			if !v.AtLeast(k8sversion.MajorMinor(4, 22)) {
				ginkgo.Skip("Console plugins require OCP >= 4.22")
			}

			ginkgo.By("Checking ConsolePlugin CR exists")
			_, err = oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"consoleplugin", cp.name,
			).Output()
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("ConsolePlugin CR %s not found", cp.name))

			ginkgo.By("Checking Service exists")
			_, err = oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"service", cp.name, "-n", opNamespace,
			).Output()
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Service %s not found", cp.name))

			ginkgo.By("Checking Deployment exists and is available")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(_ context.Context) (bool, error) {
				available, getErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
					"deployment", cp.name, "-n", opNamespace,
					"-o=jsonpath={.status.conditions[?(@.type=='Available')].status}",
				).Output()
				if getErr != nil {
					Logf("Deployment %s not found yet: %v", cp.name, getErr)
					return false, nil
				}
				if strings.EqualFold(available, "True") {
					return true, nil
				}
				Logf("Deployment %s not yet available (Available=%s)", cp.name, available)
				return false, nil
			})
			o.Expect(err).NotTo(o.HaveOccurred(),
				fmt.Sprintf("Deployment %s did not become available in time", cp.name))

			ginkgo.By("Checking ConfigMap exists")
			_, err = oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"configmap", cp.name, "-n", opNamespace,
			).Output()
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("ConfigMap %s not found", cp.name))

			ginkgo.By("Checking NetworkPolicy exists")
			_, err = oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"networkpolicy", cp.name, "-n", opNamespace,
			).Output()
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("NetworkPolicy %s not found", cp.name))

			ginkgo.By(fmt.Sprintf("SUCCESS - console plugin %s is deployed and available", cp.name))
		})
	}
})
