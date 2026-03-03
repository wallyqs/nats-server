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

package hsm

/*
#cgo LDFLAGS: -ldl

#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

// ============================================================
// Minimal PKCS#11 v2.20 Type Definitions (OASIS Standard)
// Only types needed for TLS signing operations are included.
// ============================================================

typedef unsigned long CK_ULONG;
typedef CK_ULONG CK_RV;
typedef CK_ULONG CK_SLOT_ID;
typedef CK_ULONG CK_SESSION_HANDLE;
typedef CK_ULONG CK_OBJECT_HANDLE;
typedef CK_ULONG CK_FLAGS;
typedef CK_ULONG CK_MECHANISM_TYPE;
typedef CK_ULONG CK_ATTRIBUTE_TYPE;
typedef CK_ULONG CK_USER_TYPE;
typedef CK_ULONG CK_OBJECT_CLASS;
typedef CK_ULONG CK_KEY_TYPE;
typedef unsigned char CK_BYTE;
typedef unsigned char CK_BBOOL;
typedef unsigned char CK_UTF8CHAR;
typedef void *CK_VOID_PTR;
typedef CK_BYTE *CK_BYTE_PTR;
typedef CK_ULONG *CK_ULONG_PTR;
typedef CK_SLOT_ID *CK_SLOT_ID_PTR;
typedef CK_OBJECT_HANDLE *CK_OBJECT_HANDLE_PTR;

// Return values
#define CKR_OK                  0x00000000UL

// Session flags
#define CKF_SERIAL_SESSION      0x00000004UL

// User types
#define CKU_USER                1UL

// Object classes
#define CKO_PRIVATE_KEY         3UL

// Attribute types
#define CKA_CLASS               0x00000000UL
#define CKA_LABEL               0x00000003UL
#define CKA_KEY_TYPE            0x00000100UL
#define CKA_ID                  0x00000102UL

// Key types
#define CKK_RSA                 0x00000000UL
#define CKK_EC                  0x00000003UL

// Mechanisms
#define CKM_RSA_PKCS            0x00000001UL
#define CKM_RSA_PKCS_PSS        0x0000000DUL
#define CKM_ECDSA               0x00001041UL

// Hash mechanisms (for PSS params)
#define CKM_SHA_1               0x00000220UL
#define CKM_SHA256              0x00000250UL
#define CKM_SHA384              0x00000260UL
#define CKM_SHA512              0x00000270UL

// MGF types (for PSS params)
#define CKG_MGF1_SHA1           0x00000001UL
#define CKG_MGF1_SHA256         0x00000002UL
#define CKG_MGF1_SHA384         0x00000003UL
#define CKG_MGF1_SHA512         0x00000004UL

// Custom error for key not found
#define CKR_KEY_NOT_FOUND       0x80000000UL

typedef struct {
    CK_BYTE major;
    CK_BYTE minor;
} CK_VERSION;

typedef struct {
    CK_UTF8CHAR      label[32];
    CK_UTF8CHAR      manufacturerID[32];
    CK_UTF8CHAR      model[16];
    CK_UTF8CHAR      serialNumber[16];
    CK_FLAGS         flags;
    CK_ULONG         ulMaxSessionCount;
    CK_ULONG         ulSessionCount;
    CK_ULONG         ulMaxRwSessionCount;
    CK_ULONG         ulRwSessionCount;
    CK_ULONG         ulMaxPinLen;
    CK_ULONG         ulMinPinLen;
    CK_ULONG         ulTotalPublicMemory;
    CK_ULONG         ulFreePublicMemory;
    CK_ULONG         ulTotalPrivateMemory;
    CK_ULONG         ulFreePrivateMemory;
    CK_VERSION       hardwareVersion;
    CK_VERSION       firmwareVersion;
    CK_UTF8CHAR      utcTime[16];
} CK_TOKEN_INFO;

typedef struct {
    CK_MECHANISM_TYPE mechanism;
    CK_VOID_PTR       pParameter;
    CK_ULONG          ulParameterLen;
} CK_MECHANISM;

typedef struct {
    CK_ATTRIBUTE_TYPE type;
    CK_VOID_PTR       pValue;
    CK_ULONG          ulValueLen;
} CK_ATTRIBUTE;

// RSA-PSS mechanism parameters
typedef struct {
    CK_MECHANISM_TYPE hashAlg;
    CK_ULONG          mgf;
    CK_ULONG          sLen;
} CK_RSA_PKCS_PSS_PARAMS;

// Generic function pointer for unused entries in the function list.
typedef CK_RV (*CK_FUNC_PTR)();

// Typed function pointers for functions we actually call.
typedef CK_RV (*fn_Initialize)(CK_VOID_PTR);
typedef CK_RV (*fn_Finalize)(CK_VOID_PTR);
typedef CK_RV (*fn_GetSlotList)(CK_BBOOL, CK_SLOT_ID_PTR, CK_ULONG_PTR);
typedef CK_RV (*fn_GetTokenInfo)(CK_SLOT_ID, CK_TOKEN_INFO*);
typedef CK_RV (*fn_OpenSession)(CK_SLOT_ID, CK_FLAGS, CK_VOID_PTR, CK_VOID_PTR, CK_SESSION_HANDLE*);
typedef CK_RV (*fn_CloseSession)(CK_SESSION_HANDLE);
typedef CK_RV (*fn_Login)(CK_SESSION_HANDLE, CK_USER_TYPE, CK_UTF8CHAR*, CK_ULONG);
typedef CK_RV (*fn_GetAttributeValue)(CK_SESSION_HANDLE, CK_OBJECT_HANDLE, CK_ATTRIBUTE*, CK_ULONG);
typedef CK_RV (*fn_FindObjectsInit)(CK_SESSION_HANDLE, CK_ATTRIBUTE*, CK_ULONG);
typedef CK_RV (*fn_FindObjects)(CK_SESSION_HANDLE, CK_OBJECT_HANDLE_PTR, CK_ULONG, CK_ULONG_PTR);
typedef CK_RV (*fn_FindObjectsFinal)(CK_SESSION_HANDLE);
typedef CK_RV (*fn_SignInit)(CK_SESSION_HANDLE, CK_MECHANISM*, CK_OBJECT_HANDLE);
typedef CK_RV (*fn_Sign)(CK_SESSION_HANDLE, CK_BYTE_PTR, CK_ULONG, CK_BYTE_PTR, CK_ULONG_PTR);

// CK_FUNCTION_LIST per PKCS#11 v2.20 specification.
// Functions we don't call are typed as generic CK_FUNC_PTR to preserve layout.
typedef struct {
    CK_VERSION          version;
    fn_Initialize       C_Initialize;          // 0
    fn_Finalize         C_Finalize;            // 1
    CK_FUNC_PTR         C_GetInfo;             // 2
    CK_FUNC_PTR         C_GetFunctionList;     // 3
    fn_GetSlotList      C_GetSlotList;         // 4
    CK_FUNC_PTR         C_GetSlotInfo;         // 5
    fn_GetTokenInfo     C_GetTokenInfo;        // 6
    CK_FUNC_PTR         C_GetMechanismList;    // 7
    CK_FUNC_PTR         C_GetMechanismInfo;    // 8
    CK_FUNC_PTR         C_InitToken;           // 9
    CK_FUNC_PTR         C_InitPIN;             // 10
    CK_FUNC_PTR         C_SetPIN;              // 11
    fn_OpenSession      C_OpenSession;         // 12
    fn_CloseSession     C_CloseSession;        // 13
    CK_FUNC_PTR         C_CloseAllSessions;    // 14
    CK_FUNC_PTR         C_GetSessionInfo;      // 15
    CK_FUNC_PTR         C_GetOperationState;   // 16
    CK_FUNC_PTR         C_SetOperationState;   // 17
    fn_Login            C_Login;               // 18
    CK_FUNC_PTR         C_Logout;              // 19
    CK_FUNC_PTR         C_CreateObject;        // 20
    CK_FUNC_PTR         C_CopyObject;          // 21
    CK_FUNC_PTR         C_DestroyObject;       // 22
    CK_FUNC_PTR         C_GetObjectSize;       // 23
    fn_GetAttributeValue C_GetAttributeValue;  // 24
    CK_FUNC_PTR         C_SetAttributeValue;   // 25
    fn_FindObjectsInit  C_FindObjectsInit;     // 26
    fn_FindObjects      C_FindObjects;         // 27
    fn_FindObjectsFinal C_FindObjectsFinal;    // 28
    CK_FUNC_PTR         C_EncryptInit;         // 29
    CK_FUNC_PTR         C_Encrypt;             // 30
    CK_FUNC_PTR         C_EncryptUpdate;       // 31
    CK_FUNC_PTR         C_EncryptFinal;        // 32
    CK_FUNC_PTR         C_DecryptInit;         // 33
    CK_FUNC_PTR         C_Decrypt;             // 34
    CK_FUNC_PTR         C_DecryptUpdate;       // 35
    CK_FUNC_PTR         C_DecryptFinal;        // 36
    CK_FUNC_PTR         C_DigestInit;          // 37
    CK_FUNC_PTR         C_Digest;              // 38
    CK_FUNC_PTR         C_DigestUpdate;        // 39
    CK_FUNC_PTR         C_DigestKey;           // 40
    CK_FUNC_PTR         C_DigestFinal;         // 41
    fn_SignInit         C_SignInit;             // 42
    fn_Sign             C_Sign;                // 43
    CK_FUNC_PTR         C_SignUpdate;          // 44
    CK_FUNC_PTR         C_SignFinal;           // 45
    CK_FUNC_PTR         C_SignRecoverInit;     // 46
    CK_FUNC_PTR         C_SignRecover;         // 47
    CK_FUNC_PTR         C_VerifyInit;          // 48
    CK_FUNC_PTR         C_Verify;              // 49
    CK_FUNC_PTR         C_VerifyUpdate;        // 50
    CK_FUNC_PTR         C_VerifyFinal;         // 51
    CK_FUNC_PTR         C_VerifyRecoverInit;   // 52
    CK_FUNC_PTR         C_VerifyRecover;       // 53
    CK_FUNC_PTR         C_DigestEncryptUpdate; // 54
    CK_FUNC_PTR         C_DecryptDigestUpdate; // 55
    CK_FUNC_PTR         C_SignEncryptUpdate;   // 56
    CK_FUNC_PTR         C_DecryptVerifyUpdate; // 57
    CK_FUNC_PTR         C_GenerateKey;         // 58
    CK_FUNC_PTR         C_GenerateKeyPair;     // 59
    CK_FUNC_PTR         C_WrapKey;             // 60
    CK_FUNC_PTR         C_UnwrapKey;           // 61
    CK_FUNC_PTR         C_DeriveKey;           // 62
    CK_FUNC_PTR         C_SeedRandom;          // 63
    CK_FUNC_PTR         C_GenerateRandom;      // 64
    CK_FUNC_PTR         C_GetFunctionStatus;   // 65
    CK_FUNC_PTR         C_CancelFunction;      // 66
    CK_FUNC_PTR         C_WaitForSlotEvent;    // 67
} CK_FUNCTION_LIST;

// ============================================================
// Module context and wrapper functions
// ============================================================

typedef CK_RV (*CK_C_GetFunctionList_fn)(CK_FUNCTION_LIST**);

typedef struct {
    void             *lib_handle;
    CK_FUNCTION_LIST *funcs;
} p11_ctx;

static p11_ctx* p11_open(const char *library_path) {
    void *handle = dlopen(library_path, RTLD_NOW);
    if (!handle) return NULL;

    CK_C_GetFunctionList_fn getFnList =
        (CK_C_GetFunctionList_fn)dlsym(handle, "C_GetFunctionList");
    if (!getFnList) {
        dlclose(handle);
        return NULL;
    }

    CK_FUNCTION_LIST *funcs = NULL;
    if (getFnList(&funcs) != CKR_OK || !funcs) {
        dlclose(handle);
        return NULL;
    }

    p11_ctx *ctx = (p11_ctx*)calloc(1, sizeof(p11_ctx));
    if (!ctx) {
        dlclose(handle);
        return NULL;
    }
    ctx->lib_handle = handle;
    ctx->funcs = funcs;
    return ctx;
}

static void p11_close(p11_ctx *ctx) {
    if (ctx) {
        if (ctx->lib_handle) dlclose(ctx->lib_handle);
        free(ctx);
    }
}

static CK_RV p11_initialize(p11_ctx *ctx) {
    return ctx->funcs->C_Initialize(NULL);
}

static CK_RV p11_finalize(p11_ctx *ctx) {
    return ctx->funcs->C_Finalize(NULL);
}

static CK_RV p11_get_slot_list(p11_ctx *ctx, CK_BBOOL tokenPresent,
    CK_SLOT_ID *pSlotList, CK_ULONG *pulCount) {
    return ctx->funcs->C_GetSlotList(tokenPresent, pSlotList, pulCount);
}

static CK_RV p11_get_token_info(p11_ctx *ctx, CK_SLOT_ID slotID,
    CK_TOKEN_INFO *pInfo) {
    return ctx->funcs->C_GetTokenInfo(slotID, pInfo);
}

static CK_RV p11_open_session(p11_ctx *ctx, CK_SLOT_ID slotID, CK_FLAGS flags,
    CK_SESSION_HANDLE *phSession) {
    return ctx->funcs->C_OpenSession(slotID, flags, NULL, NULL, phSession);
}

static CK_RV p11_close_session(p11_ctx *ctx, CK_SESSION_HANDLE hSession) {
    return ctx->funcs->C_CloseSession(hSession);
}

static CK_RV p11_login(p11_ctx *ctx, CK_SESSION_HANDLE hSession,
    CK_USER_TYPE userType, CK_UTF8CHAR *pPin, CK_ULONG pinLen) {
    return ctx->funcs->C_Login(hSession, userType, pPin, pinLen);
}

// p11_find_private_key searches for a private key by label and/or ID.
// Returns CKR_KEY_NOT_FOUND if no matching key is found.
static CK_RV p11_find_private_key(p11_ctx *ctx, CK_SESSION_HANDLE session,
    const char *label, int labelLen,
    const unsigned char *id, int idLen,
    CK_OBJECT_HANDLE *pKey) {

    CK_OBJECT_CLASS keyClass = CKO_PRIVATE_KEY;
    CK_ATTRIBUTE tmpl[3];
    int n = 0;

    tmpl[n].type = CKA_CLASS;
    tmpl[n].pValue = &keyClass;
    tmpl[n].ulValueLen = sizeof(keyClass);
    n++;

    if (label && labelLen > 0) {
        tmpl[n].type = CKA_LABEL;
        tmpl[n].pValue = (void*)label;
        tmpl[n].ulValueLen = (CK_ULONG)labelLen;
        n++;
    }
    if (id && idLen > 0) {
        tmpl[n].type = CKA_ID;
        tmpl[n].pValue = (void*)id;
        tmpl[n].ulValueLen = (CK_ULONG)idLen;
        n++;
    }

    CK_RV rv = ctx->funcs->C_FindObjectsInit(session, tmpl, (CK_ULONG)n);
    if (rv != CKR_OK) return rv;

    CK_ULONG found = 0;
    rv = ctx->funcs->C_FindObjects(session, pKey, 1, &found);
    ctx->funcs->C_FindObjectsFinal(session);

    if (rv != CKR_OK) return rv;
    if (found == 0) return CKR_KEY_NOT_FOUND;
    return CKR_OK;
}

// p11_get_key_type retrieves the CKA_KEY_TYPE attribute of a key object.
static CK_RV p11_get_key_type(p11_ctx *ctx, CK_SESSION_HANDLE session,
    CK_OBJECT_HANDLE key, CK_KEY_TYPE *pKeyType) {
    CK_ATTRIBUTE attr;
    attr.type = CKA_KEY_TYPE;
    attr.pValue = pKeyType;
    attr.ulValueLen = sizeof(CK_KEY_TYPE);
    return ctx->funcs->C_GetAttributeValue(session, key, &attr, 1);
}

// p11_sign_data performs SignInit + Sign in a single call.
static CK_RV p11_sign_data(p11_ctx *ctx, CK_SESSION_HANDLE session,
    CK_OBJECT_HANDLE key,
    CK_MECHANISM_TYPE mechType, void *mechParam, CK_ULONG mechParamLen,
    CK_BYTE *data, CK_ULONG dataLen,
    CK_BYTE *sig, CK_ULONG *sigLen) {

    CK_MECHANISM mech;
    mech.mechanism = mechType;
    mech.pParameter = mechParam;
    mech.ulParameterLen = mechParamLen;

    CK_RV rv = ctx->funcs->C_SignInit(session, &mech, key);
    if (rv != CKR_OK) return rv;
    return ctx->funcs->C_Sign(session, data, dataLen, sig, sigLen);
}
*/
import "C"

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"unsafe"
)

// hsmSigner implements the Signer interface backed by a PKCS#11 HSM.
type hsmSigner struct {
	mu        sync.Mutex
	ctx       *C.p11_ctx
	session   C.CK_SESSION_HANDLE
	keyHandle C.CK_OBJECT_HANDLE
	publicKey crypto.PublicKey
}

// GetSigner creates a new crypto.Signer backed by a PKCS#11 HSM.
// The publicKey should come from the leaf certificate that corresponds
// to the HSM-held private key.
func GetSigner(cfg *Config, publicKey crypto.PublicKey) (Signer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Load PKCS#11 module
	cPath := C.CString(cfg.Provider)
	defer C.free(unsafe.Pointer(cPath))

	ctx := C.p11_open(cPath)
	if ctx == nil {
		return nil, fmt.Errorf("hsm: failed to load PKCS#11 provider %q (check path and library dependencies)", cfg.Provider)
	}

	cleanup := func() {
		C.p11_finalize(ctx)
		C.p11_close(ctx)
	}

	rv := C.p11_initialize(ctx)
	if rv != C.CKR_OK {
		C.p11_close(ctx) // Don't finalize since initialize failed
		return nil, fmt.Errorf("hsm: C_Initialize failed (CKR=0x%08x)", uint(rv))
	}

	// Find token slot by label
	var slotCount C.CK_ULONG
	rv = C.p11_get_slot_list(ctx, 1, nil, &slotCount)
	if rv != C.CKR_OK {
		cleanup()
		return nil, fmt.Errorf("hsm: C_GetSlotList(count) failed (CKR=0x%08x)", uint(rv))
	}
	if slotCount == 0 {
		cleanup()
		return nil, fmt.Errorf("hsm: no tokens found")
	}

	slots := make([]C.CK_SLOT_ID, slotCount)
	rv = C.p11_get_slot_list(ctx, 1, &slots[0], &slotCount)
	if rv != C.CKR_OK {
		cleanup()
		return nil, fmt.Errorf("hsm: C_GetSlotList failed (CKR=0x%08x)", uint(rv))
	}

	var slotID C.CK_SLOT_ID
	found := false
	for _, slot := range slots[:slotCount] {
		var ti C.CK_TOKEN_INFO
		if C.p11_get_token_info(ctx, slot, &ti) != C.CKR_OK {
			continue
		}
		// Token labels are padded to 32 chars with spaces per PKCS#11 spec
		label := strings.TrimRight(
			C.GoStringN((*C.char)(unsafe.Pointer(&ti.label[0])), 32), " ")
		if label == cfg.TokenLabel {
			slotID = slot
			found = true
			break
		}
	}
	if !found {
		cleanup()
		return nil, fmt.Errorf("hsm: token with label %q not found", cfg.TokenLabel)
	}

	// Open session
	var session C.CK_SESSION_HANDLE
	rv = C.p11_open_session(ctx, slotID, C.CKF_SERIAL_SESSION, &session)
	if rv != C.CKR_OK {
		cleanup()
		return nil, fmt.Errorf("hsm: C_OpenSession failed (CKR=0x%08x)", uint(rv))
	}

	sessionCleanup := func() {
		C.p11_close_session(ctx, session)
		cleanup()
	}

	// Login with PIN
	if cfg.Pin != "" {
		cPin := C.CString(cfg.Pin)
		rv = C.p11_login(ctx, session, C.CKU_USER,
			(*C.CK_UTF8CHAR)(unsafe.Pointer(cPin)), C.CK_ULONG(len(cfg.Pin)))
		C.free(unsafe.Pointer(cPin))
		if rv != C.CKR_OK {
			sessionCleanup()
			return nil, fmt.Errorf("hsm: C_Login failed (CKR=0x%08x)", uint(rv))
		}
	}

	// Find private key by label and/or ID
	var keyHandle C.CK_OBJECT_HANDLE
	var cLabel *C.char
	var labelLen C.int
	var cID *C.uchar
	var idLen C.int

	if cfg.KeyLabel != "" {
		cLabel = C.CString(cfg.KeyLabel)
		defer C.free(unsafe.Pointer(cLabel))
		labelLen = C.int(len(cfg.KeyLabel))
	}

	if cfg.KeyID != "" {
		idBytes, err := hex.DecodeString(cfg.KeyID)
		if err != nil {
			sessionCleanup()
			return nil, fmt.Errorf("hsm: invalid key_id hex encoding: %w", err)
		}
		cID = (*C.uchar)(C.CBytes(idBytes))
		defer C.free(unsafe.Pointer(cID))
		idLen = C.int(len(idBytes))
	}

	rv = C.p11_find_private_key(ctx, session, cLabel, labelLen, cID, idLen, &keyHandle)
	if rv != C.CKR_OK {
		sessionCleanup()
		if rv == C.CKR_KEY_NOT_FOUND {
			return nil, fmt.Errorf("hsm: private key not found (label=%q, id=%q)", cfg.KeyLabel, cfg.KeyID)
		}
		return nil, fmt.Errorf("hsm: key search failed (CKR=0x%08x)", uint(rv))
	}

	// Verify key type matches the certificate's public key type
	var keyType C.CK_KEY_TYPE
	rv = C.p11_get_key_type(ctx, session, keyHandle, &keyType)
	if rv != C.CKR_OK {
		sessionCleanup()
		return nil, fmt.Errorf("hsm: failed to get key type (CKR=0x%08x)", uint(rv))
	}

	switch publicKey.(type) {
	case *rsa.PublicKey:
		if keyType != C.CKK_RSA {
			sessionCleanup()
			return nil, fmt.Errorf("hsm: certificate has RSA key but HSM key type is 0x%x", uint(keyType))
		}
	case *ecdsa.PublicKey:
		if keyType != C.CKK_EC {
			sessionCleanup()
			return nil, fmt.Errorf("hsm: certificate has EC key but HSM key type is 0x%x", uint(keyType))
		}
	default:
		sessionCleanup()
		return nil, fmt.Errorf("hsm: unsupported certificate key type %T", publicKey)
	}

	return &hsmSigner{
		ctx:       ctx,
		session:   session,
		keyHandle: keyHandle,
		publicKey: publicKey,
	}, nil
}

// Public returns the public key from the associated certificate.
func (s *hsmSigner) Public() crypto.PublicKey {
	return s.publicKey
}

// Sign signs digest using the PKCS#11 HSM.
// For RSA keys, it supports both PKCS#1 v1.5 and PSS padding.
// For ECDSA keys, it converts the PKCS#11 raw r||s output to ASN.1 DER.
func (s *hsmSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx == nil {
		return nil, fmt.Errorf("hsm: signer is closed")
	}

	mechType, mechParam, mechParamLen, signData, err := s.prepareSigning(digest, opts)
	if err != nil {
		return nil, err
	}

	// Allocate signature buffer large enough for RSA-4096 (512 bytes)
	sigBuf := make([]byte, 512)
	sigLen := C.CK_ULONG(len(sigBuf))

	rv := C.p11_sign_data(s.ctx, s.session, s.keyHandle,
		mechType, mechParam, mechParamLen,
		(*C.CK_BYTE)(unsafe.Pointer(&signData[0])), C.CK_ULONG(len(signData)),
		(*C.CK_BYTE)(unsafe.Pointer(&sigBuf[0])), &sigLen)
	if rv != C.CKR_OK {
		return nil, fmt.Errorf("hsm: C_Sign failed (CKR=0x%08x)", uint(rv))
	}

	sig := make([]byte, sigLen)
	copy(sig, sigBuf[:sigLen])

	// ECDSA: PKCS#11 returns raw r||s, convert to ASN.1 DER for Go TLS
	if _, ok := s.publicKey.(*ecdsa.PublicKey); ok {
		return ecdsaRawToASN1(sig)
	}

	return sig, nil
}

// prepareSigning returns the PKCS#11 mechanism and data for a signing operation.
func (s *hsmSigner) prepareSigning(digest []byte, opts crypto.SignerOpts) (
	mechType C.CK_MECHANISM_TYPE,
	mechParam unsafe.Pointer,
	mechParamLen C.CK_ULONG,
	signData []byte,
	err error,
) {
	switch s.publicKey.(type) {
	case *rsa.PublicKey:
		if pssOpts, ok := opts.(*rsa.PSSOptions); ok {
			// RSA-PSS (used by TLS 1.3)
			pssParams := C.CK_RSA_PKCS_PSS_PARAMS{
				hashAlg: hashToCKM(pssOpts.HashFunc()),
				mgf:     hashToMGF(pssOpts.HashFunc()),
				sLen:    C.CK_ULONG(pssOpts.SaltLength),
			}
			return C.CKM_RSA_PKCS_PSS,
				unsafe.Pointer(&pssParams),
				C.CK_ULONG(C.sizeof_CK_RSA_PKCS_PSS_PARAMS),
				digest,
				nil
		}
		// RSA PKCS#1 v1.5: prepend DigestInfo structure
		prefixed, err := prependDigestInfo(opts.HashFunc(), digest)
		if err != nil {
			return 0, nil, 0, nil, err
		}
		return C.CKM_RSA_PKCS, nil, 0, prefixed, nil

	case *ecdsa.PublicKey:
		return C.CKM_ECDSA, nil, 0, digest, nil

	default:
		return 0, nil, 0, nil, fmt.Errorf("hsm: unsupported key type %T", s.publicKey)
	}
}

// Close releases the PKCS#11 session and module resources.
func (s *hsmSigner) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx != nil {
		C.p11_close_session(s.ctx, s.session)
		C.p11_finalize(s.ctx)
		C.p11_close(s.ctx)
		s.ctx = nil
	}
	return nil
}

// hashToCKM maps Go crypto.Hash to PKCS#11 hash mechanism for PSS params.
func hashToCKM(h crypto.Hash) C.CK_MECHANISM_TYPE {
	switch h {
	case crypto.SHA256:
		return C.CKM_SHA256
	case crypto.SHA384:
		return C.CKM_SHA384
	case crypto.SHA512:
		return C.CKM_SHA512
	case crypto.SHA1:
		return C.CKM_SHA_1
	default:
		return C.CKM_SHA256
	}
}

// hashToMGF maps Go crypto.Hash to PKCS#11 MGF type for PSS params.
func hashToMGF(h crypto.Hash) C.CK_ULONG {
	switch h {
	case crypto.SHA256:
		return C.CKG_MGF1_SHA256
	case crypto.SHA384:
		return C.CKG_MGF1_SHA384
	case crypto.SHA512:
		return C.CKG_MGF1_SHA512
	case crypto.SHA1:
		return C.CKG_MGF1_SHA1
	default:
		return C.CKG_MGF1_SHA256
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
		return nil, fmt.Errorf("hsm: unsupported hash %v for PKCS#1 v1.5 DigestInfo", h)
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
		return nil, fmt.Errorf("hsm: invalid ECDSA signature length %d (must be even)", len(raw))
	}
	half := len(raw) / 2
	r := new(big.Int).SetBytes(raw[:half])
	s := new(big.Int).SetBytes(raw[half:])
	return asn1.Marshal(ecdsaSig{R: r, S: s})
}
