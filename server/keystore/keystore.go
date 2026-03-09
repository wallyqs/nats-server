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

// Package keystore provides pluggable private key backends for TLS.
//
// While cert_store provides both certificate and key from a platform store
// (e.g., Windows Certificate Store), key_store provides only a crypto.Signer
// for the private key. The certificate is still loaded from cert_file.
//
// Currently supported backends:
//   - PKCS11: Hardware Security Module access via PKCS#11 (build with -tags hsm)
//
// Configuration example (nats-server.conf):
//
//	tls {
//	    cert_file: "/path/to/server-cert.pem"
//	    ca_file:   "/path/to/ca-cert.pem"
//	    key_store: "PKCS11"
//	    key_match_by: "Label"
//	    key_match: "server-key"
//	    key_store_opts {
//	        provider:    "/usr/lib/softhsm/libsofthsm2.so"
//	        token_label: "my-token"
//	        pin:         $HSM_PIN
//	    }
//	}
package keystore

import (
	"crypto"
	"strings"
)

// StoreType identifies a key store backend.
type StoreType int

const STOREEMPTY StoreType = 0

const (
	PKCS11 StoreType = iota + 1
)

// StoreMap maps config strings to StoreType values.
var StoreMap = map[string]StoreType{
	"pkcs11": PKCS11,
}

// MatchByType identifies how to search for a key in the store.
type MatchByType int

const MATCHBYEMPTY MatchByType = 0

const (
	MatchByLabel MatchByType = iota + 1
	MatchByID
)

// MatchByMap maps config strings to MatchByType values.
var MatchByMap = map[string]MatchByType{
	"label": MatchByLabel,
	"id":    MatchByID,
}

// StoreOpts holds backend-specific configuration.
type StoreOpts struct {
	Provider   string // Path to PKCS#11 shared library (.so/.dylib)
	TokenLabel string // Token label to identify the HSM slot/token
	Pin        string // Token PIN (use unquoted $ENV_VAR for env expansion)
}

// Signer extends crypto.Signer with a Close method for resource cleanup.
type Signer interface {
	crypto.Signer
	Close() error
}

// ParseStoreType parses a key_store config string into a StoreType.
func ParseStoreType(s string) (StoreType, error) {
	st, ok := StoreMap[strings.ToLower(s)]
	if !ok {
		return 0, ErrBadKeyStore
	}
	return st, nil
}

// ParseMatchBy parses a key_match_by config string into a MatchByType.
func ParseMatchBy(s string) (MatchByType, error) {
	mt, ok := MatchByMap[strings.ToLower(s)]
	if !ok {
		return 0, ErrBadMatchByType
	}
	return mt, nil
}
