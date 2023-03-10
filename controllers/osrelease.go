package controllers

import (
	"github.com/ashcrow/osrelease"
	"strings"
)

// OS Release Paths
const (
	EtcOSReleasePath string = "/etc/os-release"
	LibOSReleasePath string = "/usr/lib/os-release"
)

// OS IDs
const (
	coreos string = "coreos"
	fedora string = "fedora"
	rhcos  string = "rhcos"
	scos   string = "scos"
)

// OperatingSystem is a wrapper around a subset of the os-release fields
// and also tracks whether ostree is in use.
type OperatingSystem struct {
	// id is the ID field from the os-release
	id string
	// variantID is the VARIANT_ID field from the os-release
	variantID string
	// version is the VERSION, RHEL_VERSION, or VERSION_ID field from the os-release
	version string
	// osrelease is the underlying struct from github.com/ashcrow/osrelease
	osrelease osrelease.OSRelease
}

func NewOperatingSystem(etcPath, libPath string) (OperatingSystem, error) {
	ret := OperatingSystem{}

	or, err := osrelease.NewWithOverrides(etcPath, libPath)
	if err != nil {
		return ret, err
	}

	ret.id = or.ID
	ret.variantID = or.VARIANT_ID
	ret.version = getOSVersion(or)
	ret.osrelease = or

	return ret, nil
}

// IsEL is true if the OS is an Enterprise Linux variant,
// i.e. RHEL CoreOS (RHCOS) or CentOS Stream CoreOS (SCOS)
func (os OperatingSystem) IsEL() bool {
	return os.id == rhcos || os.id == scos
}

// IsFCOS is true if the OS is Fedora CoreOS
func (os OperatingSystem) IsFCOS() bool {
	return os.id == fedora && os.variantID == coreos
}

// Determines the OS version based upon the contents of the RHEL_VERSION, VERSION or VERSION_ID fields.
func getOSVersion(or osrelease.OSRelease) string {
	// If we have the RHEL_VERSION field, we should use that value instead.
	if rhelVersion, ok := or.ADDITIONAL_FIELDS["RHEL_VERSION"]; ok {
		return rhelVersion
	}

	// If we have the OPENSHIFT_VERSION field, we can compute the OS version.
	if openshiftVersion, ok := or.ADDITIONAL_FIELDS["OPENSHIFT_VERSION"]; ok {
		// Move the "." from the middle of the OpenShift version to the end; e.g., 4.12 becomes 412.
		openshiftVersion := strings.ReplaceAll(openshiftVersion, ".", "") + "."
		if strings.HasPrefix(or.VERSION, openshiftVersion) {
			// Strip the OpenShift Version prefix from the VERSION field, if it is found.
			return strings.ReplaceAll(or.VERSION, openshiftVersion, "")
		}
	}

	// Fallback to the VERSION_ID field
	return or.VERSION_ID
}
