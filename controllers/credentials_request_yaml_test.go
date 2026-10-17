package controllers

import (
	"os"
	"path/filepath"
	"testing"
)

// credentialsRequestsDir is the on-disk location of the committed
// CredentialsRequest YAMLs, relative to this test file (controllers/).
// At runtime the operator reads them from peerpodsCredentialsRequestsPathLocation
// ("/config/peerpods/credentials-requests") inside the image.
const credentialsRequestsDir = "../config/peerpods/credentials-requests"

// TestCredentialsRequestYAMLsAreValid parses every committed
// credentials_request_<provider>.yaml through the exact same code path the
// credentials controller uses (parseCredentialsRequestYAML). This reproduces
// runtime failures such as:
//
//	error converting YAML to JSON: yaml: line 46: did not find expected key
//
// which otherwise only surface when the controller reconciles on a live
// cluster. A malformed file (e.g. inconsistent list indentation under
// statementEntries) fails here instead.
func TestCredentialsRequestYAMLsAreValid(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(credentialsRequestsDir, "credentials_request_*.yaml"))
	if err != nil {
		t.Fatalf("failed to glob CredentialsRequest YAMLs: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no CredentialsRequest YAMLs found in %s", credentialsRequestsDir)
	}

	for _, file := range matches {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			yamlData, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("failed to read %s: %v", file, err)
			}

			cr, err := parseCredentialsRequestYAML(yamlData)
			if err != nil {
				t.Fatalf("parseCredentialsRequestYAML(%s) returned error: %v", file, err)
			}

			if cr.Kind != "CredentialsRequest" {
				t.Errorf("%s: expected Kind=CredentialsRequest, got %q", file, cr.Kind)
			}
			if cr.Name == "" {
				t.Errorf("%s: expected a non-empty metadata.name", file)
			}
			if cr.Spec.ProviderSpec == nil {
				t.Errorf("%s: expected a non-nil spec.providerSpec", file)
			}
		})
	}
}
