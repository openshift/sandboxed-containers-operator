package controllers

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOSRelease(t *testing.T) {
	rhcos90OSReleaseContents := `NAME="Red Hat Enterprise Linux CoreOS"
ID="rhcos"
ID_LIKE="rhel fedora"
VERSION="413.90.202212151724-0"
VERSION_ID="4.13"
VARIANT="CoreOS"
VARIANT_ID=coreos
PLATFORM_ID="platform:el9"
PRETTY_NAME="Red Hat Enterprise Linux CoreOS 413.90.202212151724-0 (Plow)"
ANSI_COLOR="0;31"
CPE_NAME="cpe:/o:redhat:enterprise_linux:9::coreos"
HOME_URL="https://www.redhat.com/"
DOCUMENTATION_URL="https://docs.openshift.com/container-platform/4.13/"
BUG_REPORT_URL="https://bugzilla.redhat.com/"
REDHAT_BUGZILLA_PRODUCT="OpenShift Container Platform"
REDHAT_BUGZILLA_PRODUCT_VERSION="4.13"
REDHAT_SUPPORT_PRODUCT="OpenShift Container Platform"
REDHAT_SUPPORT_PRODUCT_VERSION="4.13"
OPENSHIFT_VERSION="4.13"
RHEL_VERSION="9.0"
OSTREE_VERSION="413.90.202212151724-0"`

	fcosOSReleaseContents := `NAME="Fedora Linux"
VERSION="37.20230110.3.1 (CoreOS)"
ID=fedora
VERSION_ID=37
VERSION_CODENAME=""
PLATFORM_ID="platform:f37"
PRETTY_NAME="Fedora CoreOS 37.20230110.3.1"
ANSI_COLOR="0;38;2;60;110;180"
LOGO=fedora-logo-icon
CPE_NAME="cpe:/o:fedoraproject:fedora:37"
HOME_URL="https://getfedora.org/coreos/"
DOCUMENTATION_URL="https://docs.fedoraproject.org/en-US/fedora-coreos/"
SUPPORT_URL="https://github.com/coreos/fedora-coreos-tracker/"
BUG_REPORT_URL="https://github.com/coreos/fedora-coreos-tracker/"
REDHAT_BUGZILLA_PRODUCT="Fedora"
REDHAT_BUGZILLA_PRODUCT_VERSION=37
REDHAT_SUPPORT_PRODUCT="Fedora"
REDHAT_SUPPORT_PRODUCT_VERSION=37
SUPPORT_END=2023-11-14
VARIANT="CoreOS"
VARIANT_ID=coreos
OSTREE_VERSION='37.20230110.3.1'`

	testCases := []struct {
		Name              string
		OSReleaseContents string
		IsEL              bool
		IsFCOS            bool
	}{
		{
			Name:              "FCOS",
			OSReleaseContents: fcosOSReleaseContents,
			IsFCOS:            true,
		},
		{
			Name:              "RHCOS 9.0",
			OSReleaseContents: rhcos90OSReleaseContents,
			IsEL:              true,
			IsFCOS:            false,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			It(fmt.Sprintf("Should handle OS %s", testCase.Name), func() {

				os, err := LoadOSRelease(testCase.OSReleaseContents, testCase.OSReleaseContents)
				if err != nil {
					t.Errorf("Failed to load OS data for test case {%s}: {%s}", testCase.Name, err.Error())
				}

				Expect(os.IsEL()).Should(Equal(testCase.IsEL))
				Expect(os.IsFCOS()).Should(Equal(testCase.IsFCOS))
			})
		})
	}
}

// Generates the OperatingSystem data from strings which contain the desired
// content. Mostly useful for testing purposes.
func LoadOSRelease(etcOSReleaseContent, libOSReleaseContent string) (OperatingSystem, error) {
	tempDir, err := os.MkdirTemp("", "")
	if err != nil {
		return OperatingSystem{}, err
	}

	defer os.RemoveAll(tempDir)

	etcOSReleasePath := filepath.Join(tempDir, "etc-os-release")
	libOSReleasePath := filepath.Join(tempDir, "lib-os-release")

	if err := os.WriteFile(etcOSReleasePath, []byte(etcOSReleaseContent), 0o644); err != nil {
		return OperatingSystem{}, err
	}

	if err := os.WriteFile(libOSReleasePath, []byte(libOSReleaseContent), 0o644); err != nil {
		return OperatingSystem{}, err
	}

	return NewOperatingSystem(etcOSReleasePath, libOSReleasePath)
}
