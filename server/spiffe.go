// Copyright 2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"
)

const spiffeScheme = "spiffe"

// SPIFFEConfig holds the configuration for SPIFFE identity support
// within the auth callout flow.
type SPIFFEConfig struct {
	// TrustDomains is the list of allowed SPIFFE trust domains.
	// If empty, all trust domains are accepted.
	TrustDomains []string
}

// extractSPIFFEIDs extracts all SPIFFE IDs from a certificate's URI SANs.
// A SPIFFE ID is a URI with scheme "spiffe", e.g. spiffe://example.org/workload.
func extractSPIFFEIDs(cert *x509.Certificate) []string {
	if cert == nil {
		return nil
	}
	var ids []string
	for _, u := range cert.URIs {
		if isSPIFFEID(u) {
			ids = append(ids, u.String())
		}
	}
	return ids
}

// isSPIFFEID returns true if the URL is a valid SPIFFE ID.
// Per the SPIFFE specification, a SPIFFE ID must:
// - Use the "spiffe" scheme
// - Have a non-empty trust domain (host)
// - Have no query, fragment, port, or userinfo
func isSPIFFEID(u *url.URL) bool {
	if u == nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, spiffeScheme) {
		return false
	}
	if u.Host == _EMPTY_ {
		return false
	}
	if u.RawQuery != _EMPTY_ || u.Fragment != _EMPTY_ {
		return false
	}
	if u.User != nil {
		return false
	}
	if u.Port() != _EMPTY_ {
		return false
	}
	return true
}

// trustDomain extracts the trust domain from a SPIFFE ID string.
// For example, "spiffe://example.org/workload" returns "example.org".
func trustDomain(spiffeID string) string {
	u, err := url.Parse(spiffeID)
	if err != nil {
		return _EMPTY_
	}
	return u.Host
}

// validateSPIFFETrustDomain checks if the given SPIFFE ID belongs to
// one of the configured trust domains. If no trust domains are configured,
// all are accepted.
func validateSPIFFETrustDomain(spiffeID string, allowedDomains []string) error {
	if len(allowedDomains) == 0 {
		return nil
	}
	td := trustDomain(spiffeID)
	if td == _EMPTY_ {
		return fmt.Errorf("could not extract trust domain from SPIFFE ID %q", spiffeID)
	}
	for _, d := range allowedDomains {
		if strings.EqualFold(td, d) {
			return nil
		}
	}
	return fmt.Errorf("SPIFFE ID %q trust domain %q is not in the allowed list", spiffeID, td)
}

// extractSPIFFEIDsFromChains extracts SPIFFE IDs from TLS verified chains
// or peer certificates. It returns the SPIFFE IDs from the leaf certificate
// (first cert in the first chain, or first peer certificate).
func extractSPIFFEIDsFromChains(verifiedChains [][]*x509.Certificate, peerCerts []*x509.Certificate) []string {
	// Try verified chains first (leaf is first cert in first chain).
	if len(verifiedChains) > 0 && len(verifiedChains[0]) > 0 {
		return extractSPIFFEIDs(verifiedChains[0][0])
	}
	// Fall back to peer certificates.
	if len(peerCerts) > 0 {
		return extractSPIFFEIDs(peerCerts[0])
	}
	return nil
}
