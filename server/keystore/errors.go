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

package keystore

import "errors"

var (
	// ErrBadKeyStore represents an unknown key_store type.
	ErrBadKeyStore = errors.New("key store type not implemented")

	// ErrBadMatchByType represents an unknown key_match_by type.
	ErrBadMatchByType = errors.New("key match by type not implemented")

	// ErrBadKeyStoreField represents a malformed key_store option.
	ErrBadKeyStoreField = errors.New("expected 'key_store' to be a valid non-empty string")

	// ErrBadKeyMatchByField represents a malformed key_match_by option.
	ErrBadKeyMatchByField = errors.New("expected 'key_match_by' to be a valid non-empty string")

	// ErrBadKeyMatchField represents a malformed key_match option.
	ErrBadKeyMatchField = errors.New("expected 'key_match' to be a valid non-empty string")

	// ErrConflictKeyFileAndStore represents ambiguous configuration.
	ErrConflictKeyFileAndStore = errors.New("'key_file' and 'key_store' may not both be configured")

	// ErrKeyStoreRequiresCertFile indicates that key_store needs cert_file.
	ErrKeyStoreRequiresCertFile = errors.New("'cert_file' is required when using 'key_store'")

	// ErrNotAvailable indicates the key store backend was not compiled in.
	ErrNotAvailable = errors.New("key store backend not available (build with required tags)")

	// ErrProviderRequired indicates a missing provider library path.
	ErrProviderRequired = errors.New("key_store_opts: 'provider' is required for PKCS11")

	// ErrTokenRequired indicates a missing token_label.
	ErrTokenRequired = errors.New("key_store_opts: 'token_label' is required for PKCS11")
)
