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

// Package hsm provides PKCS#11 HSM integration for TLS private key operations.
//
// To enable HSM support, build with: go build -tags hsm
//
// Configuration example (nats-server.conf):
//
//	tls {
//	    cert_file: "/path/to/server-cert.pem"
//	    ca_file:   "/path/to/ca-cert.pem"
//	    hsm {
//	        provider:    "/usr/lib/softhsm/libsofthsm2.so"
//	        pin:         $HSM_PIN
//	        token_label: "my-token"
//	        key_label:   "server-key"
//	    }
//	}
package hsm

import (
	"crypto"
	"errors"
)

// Config holds HSM configuration for PKCS#11 private key access.
type Config struct {
	Provider   string // Path to PKCS#11 shared library (.so/.dylib/.dll)
	Pin        string // HSM token PIN (use unquoted $ENV_VAR in NATS config for env expansion)
	TokenLabel string // Token label to identify the slot/token
	KeyLabel   string // Label attribute (CKA_LABEL) to find the private key
	KeyID      string // Hex-encoded ID attribute (CKA_ID) to find the private key
}

// Signer extends crypto.Signer with a Close method for resource cleanup.
type Signer interface {
	crypto.Signer
	Close() error
}

var (
	ErrHSMNotAvailable  = errors.New("hsm: support not available, build with '-tags hsm'")
	ErrProviderRequired = errors.New("hsm: 'provider' (PKCS#11 library path) is required")
	ErrTokenRequired    = errors.New("hsm: 'token_label' is required")
	ErrKeyRequired      = errors.New("hsm: either 'key_label' or 'key_id' is required")
)

// Validate checks that the required HSM configuration fields are present.
func (c *Config) Validate() error {
	if c.Provider == "" {
		return ErrProviderRequired
	}
	if c.TokenLabel == "" {
		return ErrTokenRequired
	}
	if c.KeyLabel == "" && c.KeyID == "" {
		return ErrKeyRequired
	}
	return nil
}
