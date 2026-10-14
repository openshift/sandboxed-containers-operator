package controllers

import (
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
)

func TestCcoDataMappingGCP(t *testing.T) {
	t.Parallel()

	r := &SecretReconciler{Log: logr.Discard()}

	wifJSON := []byte(`{"type":"external_account","audience":"//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/prov"}`)
	staticJSON := []byte(`{"type":"service_account","project_id":"my-project","private_key":"xxx"}`)

	t.Run("GCP WIF external_account maps to GCP_WIF_CREDENTIALS", func(t *testing.T) {
		t.Parallel()
		out := r.ccoDataMapping(map[string][]byte{"service_account.json": wifJSON})
		if got, ok := out["GCP_WIF_CREDENTIALS"]; !ok || string(got) != string(wifJSON) {
			t.Fatalf("expected GCP_WIF_CREDENTIALS to equal input, got %q (present=%v)", got, ok)
		}
		if _, ok := out["GCP_CREDENTIALS"]; ok {
			t.Fatalf("did not expect GCP_CREDENTIALS for external_account input")
		}
	})

	t.Run("GCP static key maps to GCP_CREDENTIALS", func(t *testing.T) {
		t.Parallel()
		out := r.ccoDataMapping(map[string][]byte{"service_account.json": staticJSON})
		if got, ok := out["GCP_CREDENTIALS"]; !ok || string(got) != string(staticJSON) {
			t.Fatalf("expected GCP_CREDENTIALS to equal input, got %q (present=%v)", got, ok)
		}
		if _, ok := out["GCP_WIF_CREDENTIALS"]; ok {
			t.Fatalf("did not expect GCP_WIF_CREDENTIALS for static key input")
		}
	})

	t.Run("existing AWS/Azure mappings are preserved", func(t *testing.T) {
		t.Parallel()
		out := r.ccoDataMapping(map[string][]byte{
			"aws_access_key_id": []byte("AKIA"),
			"azure_client_id":   []byte("client"),
		})
		if string(out["AWS_ACCESS_KEY_ID"]) != "AKIA" {
			t.Fatalf("expected AWS_ACCESS_KEY_ID to be preserved, got %q", out["AWS_ACCESS_KEY_ID"])
		}
		if string(out["AZURE_CLIENT_ID"]) != "client" {
			t.Fatalf("expected AZURE_CLIENT_ID to be preserved, got %q", out["AZURE_CLIENT_ID"])
		}
	})
}

func TestBuildGCPWIFCredentials(t *testing.T) {
	t.Parallel()

	audience := "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/prov"
	sa := "caa@my-project.iam.gserviceaccount.com"
	tokenPath := "/var/run/secrets/openshift/serviceaccount/token"

	raw, err := buildGCPWIFCredentials(audience, sa, tokenPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var creds struct {
		Type                           string `json:"type"`
		Audience                       string `json:"audience"`
		ServiceAccountImpersonationURL string `json:"service_account_impersonation_url"`
		CredentialSource               struct {
			File string `json:"file"`
		} `json:"credential_source"`
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if creds.Type != "external_account" {
		t.Errorf("expected type external_account, got %q", creds.Type)
	}
	if creds.Audience != audience {
		t.Errorf("expected audience %q, got %q", audience, creds.Audience)
	}
	if creds.CredentialSource.File != tokenPath {
		t.Errorf("expected credential_source.file %q, got %q", tokenPath, creds.CredentialSource.File)
	}
	want := "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + sa + ":generateAccessToken"
	if creds.ServiceAccountImpersonationURL != want {
		t.Errorf("expected impersonation URL %q, got %q", want, creds.ServiceAccountImpersonationURL)
	}
}
