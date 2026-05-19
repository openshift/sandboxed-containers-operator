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
	"crypto/tls"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
)

// ianaByID maps TLS cipher suite IDs to their IANA names. Built once from the
// static lists returned by tls.CipherSuites and tls.InsecureCipherSuites; TLS
// 1.3 suite IDs are absent by design (Go does not expose them there).
var ianaByID = func() map[uint16]string {
	all := append(tls.CipherSuites(), tls.InsecureCipherSuites()...)
	m := make(map[uint16]string, len(all))
	for _, cs := range all {
		m[cs.ID] = cs.Name
	}
	return m
}()

// tlsCipherSuitesEnvValue returns a comma-separated list of IANA cipher names
// suitable for the TLS_CIPHER_SUITES env var consumed by CAA and the peer-pods
// webhook.
//
// Three problems are handled here:
//  1. configv1.TLSProfileSpec.Ciphers mixes IANA and OpenSSL names; CAA's
//     parser (k8s.io/component-base cliflag) only accepts IANA names.
//  2. When MinTLSVersion is VersionTLS13, Go's crypto/tls does not allow
//     configuring cipher suites and CAA rejects any non-empty list, so we
//     return "" for that case.
//  3. TLS 1.3 suite names (TLS_AES_*, TLS_CHACHA20_*) appear in the
//     Intermediate profile's cipher list but cliflag returns a hard error for
//     them — they are not in tls.CipherSuites() / tls.InsecureCipherSuites().
//
// We resolve all three by applying NewTLSConfigFromProfile to a temporary
// tls.Config (which handles 1 and 2), then mapping the resulting uint16 IDs
// back to names via tls.CipherSuites()+tls.InsecureCipherSuites() (which
// naturally drops TLS 1.3 IDs, handling 3) — producing exactly the set that
// cliflag accepts.
func (r *KataConfigOpenShiftReconciler) tlsCipherSuitesEnvValue(profile configv1.TLSProfileSpec) string {
	applyOpts, _ := openshifttls.NewTLSConfigFromProfile(profile)
	cfg := &tls.Config{}
	applyOpts(cfg)

	if len(cfg.CipherSuites) == 0 {
		return ""
	}

	names := make([]string, 0, len(cfg.CipherSuites))
	for _, id := range cfg.CipherSuites {
		if name, ok := ianaByID[id]; ok {
			names = append(names, name)
		} else {
			r.Log.V(1).Info("dropping cipher suite not in IANA list (TLS 1.3 suite)", "id", id)
		}
	}
	return strings.Join(names, ",")
}
