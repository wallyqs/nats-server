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

//go:build hsm

package keystore

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"sync"
	"unsafe"

	"github.com/miekg/pkcs11"
)

// pkcs11Signer implements the Signer interface backed by a PKCS#11 HSM
// using the miekg/pkcs11 library.
type pkcs11Signer struct {
	mu        sync.Mutex
	ctx       *pkcs11.Ctx
	session   pkcs11.SessionHandle
	keyHandle pkcs11.ObjectHandle
	publicKey crypto.PublicKey
}

// GetSigner creates a crypto.Signer backed by the configured key store.
// matchBy and match identify the key; opts provides backend-specific config.
// publicKey should come from the leaf certificate.
func GetSigner(store StoreType, matchBy MatchByType, match string, opts *StoreOpts, publicKey crypto.PublicKey) (Signer, error) {
	switch store {
	case PKCS11:
		return getPKCS11Signer(matchBy, match, opts, publicKey)
	default:
		return nil, ErrBadKeyStore
	}
}

func getPKCS11Signer(matchBy MatchByType, match string, opts *StoreOpts, publicKey crypto.PublicKey) (Signer, error) {
	if opts == nil {
		opts = &StoreOpts{}
	}
	if opts.Provider == "" {
		return nil, ErrProviderRequired
	}
	if opts.TokenLabel == "" {
		return nil, ErrTokenRequired
	}

	// Determine key_label and key_id from the matchBy/match fields.
	var keyLabel, keyID string
	switch matchBy {
	case MatchByLabel, MATCHBYEMPTY:
		keyLabel = match
	case MatchByID:
		keyID = match
	default:
		return nil, ErrBadMatchByType
	}

	// Load PKCS#11 module
	ctx := pkcs11.New(opts.Provider)
	if ctx == nil {
		return nil, fmt.Errorf("key_store: failed to load PKCS#11 provider %q (check path and library dependencies)", opts.Provider)
	}

	cleanup := func() {
		ctx.Finalize()
		ctx.Destroy()
	}

	if err := ctx.Initialize(); err != nil {
		ctx.Destroy() // Don't finalize since initialize failed
		return nil, fmt.Errorf("key_store: C_Initialize failed: %v", err)
	}

	// Find token slot by label
	slots, err := ctx.GetSlotList(true)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("key_store: C_GetSlotList failed: %v", err)
	}
	if len(slots) == 0 {
		cleanup()
		return nil, fmt.Errorf("key_store: no tokens found")
	}

	var slotID uint
	found := false
	for _, slot := range slots {
		ti, err := ctx.GetTokenInfo(slot)
		if err != nil {
			continue
		}
		if ti.Label == opts.TokenLabel {
			slotID = slot
			found = true
			break
		}
	}
	if !found {
		cleanup()
		return nil, fmt.Errorf("key_store: token with label %q not found", opts.TokenLabel)
	}

	// Open session
	session, err := ctx.OpenSession(slotID, pkcs11.CKF_SERIAL_SESSION)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("key_store: C_OpenSession failed: %v", err)
	}

	sessionCleanup := func() {
		ctx.CloseSession(session)
		cleanup()
	}

	// Login with PIN
	if opts.Pin != "" {
		if err := ctx.Login(session, pkcs11.CKU_USER, opts.Pin); err != nil {
			sessionCleanup()
			return nil, fmt.Errorf("key_store: C_Login failed: %v", err)
		}
	}

	// Build search template for private key
	template := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
	}
	if keyLabel != "" {
		template = append(template, pkcs11.NewAttribute(pkcs11.CKA_LABEL, keyLabel))
	}
	if keyID != "" {
		idBytes, err := hex.DecodeString(keyID)
		if err != nil {
			sessionCleanup()
			return nil, fmt.Errorf("key_store: invalid key_id hex encoding: %w", err)
		}
		template = append(template, pkcs11.NewAttribute(pkcs11.CKA_ID, idBytes))
	}

	// Find the private key object
	if err := ctx.FindObjectsInit(session, template); err != nil {
		sessionCleanup()
		return nil, fmt.Errorf("key_store: C_FindObjectsInit failed: %v", err)
	}
	objs, _, err := ctx.FindObjects(session, 1)
	if err2 := ctx.FindObjectsFinal(session); err == nil {
		err = err2
	}
	if err != nil {
		sessionCleanup()
		return nil, fmt.Errorf("key_store: key search failed: %v", err)
	}
	if len(objs) == 0 {
		sessionCleanup()
		return nil, fmt.Errorf("key_store: private key not found (label=%q, id=%q)", keyLabel, keyID)
	}
	keyHandle := objs[0]

	// Verify key type matches the certificate's public key type
	attrs, err := ctx.GetAttributeValue(session, keyHandle, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, nil),
	})
	if err != nil {
		sessionCleanup()
		return nil, fmt.Errorf("key_store: failed to get key type: %v", err)
	}
	if len(attrs) == 0 {
		sessionCleanup()
		return nil, fmt.Errorf("key_store: key type attribute not found")
	}
	keyType := readCKULong(attrs[0].Value)

	switch publicKey.(type) {
	case *rsa.PublicKey:
		if keyType != pkcs11.CKK_RSA {
			sessionCleanup()
			return nil, fmt.Errorf("key_store: certificate has RSA key but HSM key type is 0x%x", keyType)
		}
	case *ecdsa.PublicKey:
		if keyType != pkcs11.CKK_EC {
			sessionCleanup()
			return nil, fmt.Errorf("key_store: certificate has EC key but HSM key type is 0x%x", keyType)
		}
	default:
		sessionCleanup()
		return nil, fmt.Errorf("key_store: unsupported certificate key type %T", publicKey)
	}

	return &pkcs11Signer{
		ctx:       ctx,
		session:   session,
		keyHandle: keyHandle,
		publicKey: publicKey,
	}, nil
}

// readCKULong reads a CK_ULONG value from a byte slice.
// CK_ULONG is platform-dependent (typically 4 or 8 bytes) and native-endian.
func readCKULong(b []byte) uint {
	switch len(b) {
	case 4:
		return uint(nativeEndian.Uint32(b))
	case 8:
		return uint(nativeEndian.Uint64(b))
	default:
		// Fallback: read as many bytes as available using native endianness.
		var val uint
		for i := len(b) - 1; i >= 0; i-- {
			val = val<<8 | uint(b[i])
		}
		return val
	}
}

// Public returns the public key from the associated certificate.
func (s *pkcs11Signer) Public() crypto.PublicKey {
	return s.publicKey
}

// Sign signs digest using the PKCS#11 HSM.
// For RSA keys, it supports both PKCS#1 v1.5 and PSS padding.
// For ECDSA keys, it converts the PKCS#11 raw r||s output to ASN.1 DER.
func (s *pkcs11Signer) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx == nil {
		return nil, fmt.Errorf("key_store: signer is closed")
	}

	mech, signData, err := s.prepareSigning(digest, opts)
	if err != nil {
		return nil, err
	}

	if err := s.ctx.SignInit(s.session, []*pkcs11.Mechanism{mech}, s.keyHandle); err != nil {
		return nil, fmt.Errorf("key_store: C_SignInit failed: %v", err)
	}
	sig, err := s.ctx.Sign(s.session, signData)
	if err != nil {
		return nil, fmt.Errorf("key_store: C_Sign failed: %v", err)
	}

	// ECDSA: PKCS#11 returns raw r||s, convert to ASN.1 DER for Go TLS
	if _, ok := s.publicKey.(*ecdsa.PublicKey); ok {
		return ecdsaRawToASN1(sig)
	}

	return sig, nil
}

// prepareSigning returns the PKCS#11 mechanism and data for a signing operation.
func (s *pkcs11Signer) prepareSigning(digest []byte, opts crypto.SignerOpts) (
	mech *pkcs11.Mechanism,
	signData []byte,
	err error,
) {
	switch s.publicKey.(type) {
	case *rsa.PublicKey:
		if pssOpts, ok := opts.(*rsa.PSSOptions); ok {
			// RSA-PSS (used by TLS 1.3)
			pssParams := pkcs11.NewPSSParams(
				hashToCKM(pssOpts.HashFunc()),
				hashToMGF(pssOpts.HashFunc()),
				uint(pssOpts.SaltLength),
			)
			return pkcs11.NewMechanism(pkcs11.CKM_RSA_PKCS_PSS, pssParams),
				digest,
				nil
		}
		// RSA PKCS#1 v1.5: prepend DigestInfo structure
		prefixed, err := prependDigestInfo(opts.HashFunc(), digest)
		if err != nil {
			return nil, nil, err
		}
		return pkcs11.NewMechanism(pkcs11.CKM_RSA_PKCS, nil), prefixed, nil

	case *ecdsa.PublicKey:
		return pkcs11.NewMechanism(pkcs11.CKM_ECDSA, nil), digest, nil

	default:
		return nil, nil, fmt.Errorf("key_store: unsupported key type %T", s.publicKey)
	}
}

// Close releases the PKCS#11 session and module resources.
func (s *pkcs11Signer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx != nil {
		s.ctx.CloseSession(s.session)
		s.ctx.Finalize()
		s.ctx.Destroy()
		s.ctx = nil
	}
	return nil
}

// hashToCKM maps Go crypto.Hash to PKCS#11 hash mechanism for PSS params.
func hashToCKM(h crypto.Hash) uint {
	switch h {
	case crypto.SHA256:
		return pkcs11.CKM_SHA256
	case crypto.SHA384:
		return pkcs11.CKM_SHA384
	case crypto.SHA512:
		return pkcs11.CKM_SHA512
	case crypto.SHA1:
		return pkcs11.CKM_SHA_1
	default:
		return pkcs11.CKM_SHA256
	}
}

// hashToMGF maps Go crypto.Hash to PKCS#11 MGF type for PSS params.
func hashToMGF(h crypto.Hash) uint {
	switch h {
	case crypto.SHA256:
		return pkcs11.CKG_MGF1_SHA256
	case crypto.SHA384:
		return pkcs11.CKG_MGF1_SHA384
	case crypto.SHA512:
		return pkcs11.CKG_MGF1_SHA512
	case crypto.SHA1:
		return pkcs11.CKG_MGF1_SHA1
	default:
		return pkcs11.CKG_MGF1_SHA256
	}
}

// DigestInfo prefixes per PKCS#1 v2.1 Section 9.2, Note 1.
var digestInfoPrefixes = map[crypto.Hash][]byte{
	crypto.SHA1:   {0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2b, 0x0e, 0x03, 0x02, 0x1a, 0x05, 0x00, 0x04, 0x14},
	crypto.SHA256: {0x30, 0x31, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05, 0x00, 0x04, 0x20},
	crypto.SHA384: {0x30, 0x41, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02, 0x05, 0x00, 0x04, 0x30},
	crypto.SHA512: {0x30, 0x51, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03, 0x05, 0x00, 0x04, 0x40},
}

func prependDigestInfo(h crypto.Hash, digest []byte) ([]byte, error) {
	prefix, ok := digestInfoPrefixes[h]
	if !ok {
		return nil, fmt.Errorf("key_store: unsupported hash %v for PKCS#1 v1.5 DigestInfo", h)
	}
	result := make([]byte, len(prefix)+len(digest))
	copy(result, prefix)
	copy(result[len(prefix):], digest)
	return result, nil
}

type ecdsaSig struct {
	R, S *big.Int
}

// ecdsaRawToASN1 converts a PKCS#11 ECDSA signature (raw r||s) to ASN.1 DER.
func ecdsaRawToASN1(raw []byte) ([]byte, error) {
	if len(raw)%2 != 0 {
		return nil, fmt.Errorf("key_store: invalid ECDSA signature length %d (must be even)", len(raw))
	}
	half := len(raw) / 2
	r := new(big.Int).SetBytes(raw[:half])
	s := new(big.Int).SetBytes(raw[half:])
	return asn1.Marshal(ecdsaSig{R: r, S: s})
}

// nativeEndian detects the platform's byte order at init time.
var nativeEndian binary.ByteOrder

func init() {
	// Detect endianness by examining the byte layout of a known value.
	var x uint32 = 0x01020304
	b := (*[4]byte)(unsafe.Pointer(&x))
	if b[0] == 0x01 {
		nativeEndian = binary.BigEndian
	} else {
		nativeEndian = binary.LittleEndian
	}
}
