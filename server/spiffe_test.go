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
	"net/url"
	"testing"
)

func TestIsSPIFFEID(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		expected bool
	}{
		{"valid spiffe ID", "spiffe://example.org/workload", true},
		{"valid spiffe ID with path", "spiffe://example.org/ns/prod/sa/payments", true},
		{"valid spiffe ID root path", "spiffe://example.org/", true},
		{"valid spiffe ID no path", "spiffe://example.org", true},
		{"https not spiffe", "https://example.org/workload", false},
		{"spiffe with query", "spiffe://example.org/workload?foo=bar", false},
		{"spiffe with fragment", "spiffe://example.org/workload#section", false},
		{"spiffe with port", "spiffe://example.org:8080/workload", false},
		{"spiffe with userinfo", "spiffe://user@example.org/workload", false},
		{"empty host", "spiffe:///workload", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.uri)
			require_NoError(t, err)
			result := isSPIFFEID(u)
			if result != tt.expected {
				t.Fatalf("isSPIFFEID(%q) = %v, want %v", tt.uri, result, tt.expected)
			}
		})
	}

	t.Run("nil url", func(t *testing.T) {
		require_True(t, !isSPIFFEID(nil))
	})
}

func TestExtractSPIFFEIDs(t *testing.T) {
	t.Run("cert with spiffe URI", func(t *testing.T) {
		spiffeURL, _ := url.Parse("spiffe://example.org/workload")
		httpURL, _ := url.Parse("https://example.org/other")
		cert := &x509.Certificate{
			URIs: []*url.URL{httpURL, spiffeURL},
		}
		ids := extractSPIFFEIDs(cert)
		require_Len(t, len(ids), 1)
		require_Equal(t, ids[0], "spiffe://example.org/workload")
	})

	t.Run("cert without spiffe URI", func(t *testing.T) {
		httpURL, _ := url.Parse("https://example.org/other")
		cert := &x509.Certificate{
			URIs: []*url.URL{httpURL},
		}
		ids := extractSPIFFEIDs(cert)
		require_Len(t, len(ids), 0)
	})

	t.Run("nil cert", func(t *testing.T) {
		ids := extractSPIFFEIDs(nil)
		require_Len(t, len(ids), 0)
	})

	t.Run("cert with multiple spiffe URIs", func(t *testing.T) {
		u1, _ := url.Parse("spiffe://example.org/workload-a")
		u2, _ := url.Parse("spiffe://example.org/workload-b")
		cert := &x509.Certificate{
			URIs: []*url.URL{u1, u2},
		}
		ids := extractSPIFFEIDs(cert)
		require_Len(t, len(ids), 2)
	})
}

func TestTrustDomain(t *testing.T) {
	tests := []struct {
		spiffeID string
		expected string
	}{
		{"spiffe://example.org/workload", "example.org"},
		{"spiffe://localhost/my-service", "localhost"},
		{"spiffe://prod.example.com/ns/default/sa/web", "prod.example.com"},
		{"not-a-url", ""},
	}

	for _, tt := range tests {
		t.Run(tt.spiffeID, func(t *testing.T) {
			td := trustDomain(tt.spiffeID)
			require_Equal(t, td, tt.expected)
		})
	}
}

func TestValidateSPIFFETrustDomain(t *testing.T) {
	t.Run("allowed domain", func(t *testing.T) {
		err := validateSPIFFETrustDomain("spiffe://example.org/workload", []string{"example.org"})
		require_NoError(t, err)
	})

	t.Run("allowed domain case insensitive", func(t *testing.T) {
		err := validateSPIFFETrustDomain("spiffe://Example.Org/workload", []string{"example.org"})
		require_NoError(t, err)
	})

	t.Run("disallowed domain", func(t *testing.T) {
		err := validateSPIFFETrustDomain("spiffe://evil.org/workload", []string{"example.org"})
		require_Error(t, err)
	})

	t.Run("multiple allowed domains", func(t *testing.T) {
		err := validateSPIFFETrustDomain("spiffe://prod.example.com/workload", []string{"example.org", "prod.example.com"})
		require_NoError(t, err)
	})

	t.Run("empty allowed list accepts all", func(t *testing.T) {
		err := validateSPIFFETrustDomain("spiffe://anything.org/workload", nil)
		require_NoError(t, err)
	})
}

func TestExtractSPIFFEIDsFromChains(t *testing.T) {
	spiffeURL, _ := url.Parse("spiffe://example.org/workload")
	leafCert := &x509.Certificate{
		URIs: []*url.URL{spiffeURL},
	}
	caCert := &x509.Certificate{}

	t.Run("from verified chains", func(t *testing.T) {
		chains := [][]*x509.Certificate{{leafCert, caCert}}
		ids := extractSPIFFEIDsFromChains(chains, nil)
		require_Len(t, len(ids), 1)
		require_Equal(t, ids[0], "spiffe://example.org/workload")
	})

	t.Run("from peer certs when no chains", func(t *testing.T) {
		ids := extractSPIFFEIDsFromChains(nil, []*x509.Certificate{leafCert})
		require_Len(t, len(ids), 1)
		require_Equal(t, ids[0], "spiffe://example.org/workload")
	})

	t.Run("empty when no certs", func(t *testing.T) {
		ids := extractSPIFFEIDsFromChains(nil, nil)
		require_Len(t, len(ids), 0)
	})
}
