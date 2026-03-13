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

// Custom UnmarshalConfig implementations for complex/polymorphic server
// configuration types. These allow conf/v2.Unmarshal to handle types that
// accept multiple forms (bool, string, map) or require special parsing.

package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// unwrapConfigValue unwraps a value that may be wrapped in a pedantic
// mode token from conf/v2. The v2 token type satisfies the server's
// token interface (Value(), Line(), Position(), etc.), so we can use
// that interface to extract the underlying value.
func unwrapConfigValue(v any) any {
	if tk, ok := v.(token); ok {
		return tk.Value()
	}
	return v
}

// unwrapConfigMap deeply unwraps all values in a map[string]any that
// may contain token-wrapped values from pedantic mode parsing.
func unwrapConfigMap(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = unwrapConfigValue(v)
	}
	return result
}

// DurationValue is a wrapper type for time.Duration that implements the
// conf/v2 Unmarshaler interface. It handles both string durations
// (e.g., "5s", "2m30s") and int64-as-seconds for backwards compatibility.
type DurationValue struct {
	time.Duration
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for DurationValue.
// It accepts:
//   - string: parsed via time.ParseDuration (e.g., "5s", "100ms", "2h30m")
//   - int64: treated as seconds for backwards compatibility
func (d *DurationValue) UnmarshalConfig(v any) error {
	switch val := v.(type) {
	case string:
		dur, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("error parsing duration: %v", err)
		}
		d.Duration = dur
	case int64:
		d.Duration = time.Duration(val) * time.Second
	default:
		return fmt.Errorf("expected string or integer for duration, got %T", v)
	}
	return nil
}

// ListenValue holds a parsed host:port combination. It implements the
// conf/v2 Unmarshaler interface for config fields like 'listen', 'http',
// and 'https' that accept either a plain port number or a "host:port" string.
type ListenValue struct {
	Host string
	Port int
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for ListenValue.
// It accepts:
//   - int64: treated as a plain port number
//   - string: parsed as "host:port" using net.SplitHostPort
func (l *ListenValue) UnmarshalConfig(v any) error {
	switch val := v.(type) {
	case int64:
		l.Port = int(val)
	case string:
		host, port, err := net.SplitHostPort(val)
		if err != nil {
			return fmt.Errorf("could not parse address string %q", val)
		}
		p, err := strconv.Atoi(port)
		if err != nil {
			return fmt.Errorf("could not parse port %q", port)
		}
		l.Host = host
		l.Port = p
	default:
		return fmt.Errorf("expected port or host:port, got %T", v)
	}
	return nil
}

// JetStreamValue holds JetStream configuration that can be specified as
// a bool, string ("enabled"/"disabled"), or a full map with detailed settings.
// It implements the conf/v2 Unmarshaler interface.
type JetStreamValue struct {
	// Enabled indicates whether JetStream is enabled.
	Enabled bool
	// StoreDir is the storage directory for JetStream data.
	StoreDir string
	// MaxMemory is the maximum memory storage limit.
	MaxMemory int64
	// MaxStore is the maximum file storage limit.
	MaxStore int64
	// Domain is the JetStream domain name.
	Domain string
	// Key is the encryption key.
	Key string
	// OldKey is the previous encryption key for rotation.
	OldKey string
	// Cipher is the encryption cipher name.
	Cipher string
	// UniqueTag for JetStream placement.
	UniqueTag string
	// ExtHint is the extension hint.
	ExtHint string
	// SyncInterval is the sync interval duration.
	SyncInterval string
	// SyncAlways forces synchronous writes.
	SyncAlways bool
	// MaxCatchup is the max outstanding catchup bytes.
	MaxCatchup int64
	// MaxBufferedSize is the max buffered size for streams.
	MaxBufferedSize int64
	// MaxBufferedMsgs is the max buffered messages for streams.
	MaxBufferedMsgs int64
	// RequestQueueLimit is the request queue limit.
	RequestQueueLimit int64
	// InfoQueueLimit is the info queue limit.
	InfoQueueLimit int64
	// MetaCompact threshold.
	MetaCompact int64
	// MetaCompactSize threshold.
	MetaCompactSize int64
	// MetaCompactSync enables sync on meta compact.
	MetaCompactSync bool
	// NoStrict disables strict mode.
	NoStrict bool
	// MaxMemSet indicates max_mem was explicitly set.
	MaxMemSet bool
	// MaxStoreSet indicates max_file was explicitly set.
	MaxStoreSet bool
	// SyncSet indicates sync was explicitly set.
	SyncSet bool
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for JetStreamValue.
// It accepts:
//   - bool: true enables, false disables JetStream
//   - string: "enabled"/"enable" or "disabled"/"disable"
//   - map[string]any: full configuration with store_dir, max_mem, max_file, etc.
func (js *JetStreamValue) UnmarshalConfig(v any) error {
	switch val := v.(type) {
	case bool:
		js.Enabled = val
	case string:
		switch strings.ToLower(val) {
		case "enabled", "enable":
			js.Enabled = true
		case "disabled", "disable":
			js.Enabled = false
		default:
			return fmt.Errorf("expected 'enabled' or 'disabled' for string value, got %q", val)
		}
	case map[string]any:
		doEnable := true
		for mk, rawMv := range val {
			mv := unwrapConfigValue(rawMv)
			switch strings.ToLower(mk) {
			case "strict":
				b, ok := mv.(bool)
				if !ok {
					return fmt.Errorf("expected boolean for 'strict', got %T", mv)
				}
				js.NoStrict = !b
			case "store", "store_dir", "storedir":
				s, ok := mv.(string)
				if !ok {
					return fmt.Errorf("expected string for 'store_dir', got %T", mv)
				}
				js.StoreDir = s
			case "sync", "sync_interval":
				if s, ok := mv.(string); ok && strings.EqualFold(s, "always") {
					js.SyncAlways = true
					js.SyncSet = true
				} else if s, ok := mv.(string); ok {
					js.SyncInterval = s
					js.SyncSet = true
				} else if n, ok := mv.(int64); ok {
					js.SyncInterval = time.Duration(n * int64(time.Second)).String()
					js.SyncSet = true
				} else {
					return fmt.Errorf("expected string or integer for 'sync', got %T", mv)
				}
			case "max_memory_store", "max_mem_store", "max_mem":
				s, err := getStorageSize(mv)
				if err != nil {
					return fmt.Errorf("max_mem_store %s", err)
				}
				js.MaxMemory = s
				js.MaxMemSet = true
			case "max_file_store", "max_file":
				s, err := getStorageSize(mv)
				if err != nil {
					return fmt.Errorf("max_file_store %s", err)
				}
				js.MaxStore = s
				js.MaxStoreSet = true
			case "domain":
				s, ok := mv.(string)
				if !ok {
					return fmt.Errorf("expected string for 'domain', got %T", mv)
				}
				js.Domain = s
			case "enable", "enabled":
				b, ok := mv.(bool)
				if !ok {
					return fmt.Errorf("expected boolean for 'enable', got %T", mv)
				}
				doEnable = b
			case "key", "ek", "encryption_key":
				s, ok := mv.(string)
				if !ok {
					return fmt.Errorf("expected string for 'key', got %T", mv)
				}
				js.Key = s
			case "prev_key", "prev_ek", "prev_encryption_key":
				s, ok := mv.(string)
				if !ok {
					return fmt.Errorf("expected string for 'prev_key', got %T", mv)
				}
				js.OldKey = s
			case "cipher":
				s, ok := mv.(string)
				if !ok {
					return fmt.Errorf("expected string for 'cipher', got %T", mv)
				}
				js.Cipher = s
			case "extension_hint":
				s, ok := mv.(string)
				if !ok {
					return fmt.Errorf("expected string for 'extension_hint', got %T", mv)
				}
				js.ExtHint = s
			case "unique_tag":
				s, ok := mv.(string)
				if !ok {
					return fmt.Errorf("expected string for 'unique_tag', got %T", mv)
				}
				js.UniqueTag = strings.ToLower(strings.TrimSpace(s))
			case "max_outstanding_catchup":
				s, err := getStorageSize(mv)
				if err != nil {
					return fmt.Errorf("%s %s", strings.ToLower(mk), err)
				}
				js.MaxCatchup = s
			case "max_buffered_size":
				s, err := getStorageSize(mv)
				if err != nil {
					return fmt.Errorf("%s %s", strings.ToLower(mk), err)
				}
				js.MaxBufferedSize = s
			case "max_buffered_msgs":
				mlen, ok := mv.(int64)
				if !ok {
					return fmt.Errorf("expected integer for %q, got %T", mk, mv)
				}
				js.MaxBufferedMsgs = mlen
			case "request_queue_limit":
				lim, ok := mv.(int64)
				if !ok {
					return fmt.Errorf("expected integer for %q, got %T", mk, mv)
				}
				js.RequestQueueLimit = lim
			case "info_queue_limit":
				lim, ok := mv.(int64)
				if !ok {
					return fmt.Errorf("expected integer for %q, got %T", mk, mv)
				}
				js.InfoQueueLimit = lim
			case "meta_compact":
				thres, ok := mv.(int64)
				if !ok || thres < 0 {
					return fmt.Errorf("expected positive integer for %q, got %v", mk, mv)
				}
				js.MetaCompact = thres
			case "meta_compact_size":
				s, err := getStorageSize(mv)
				if err != nil {
					return fmt.Errorf("%s %s", strings.ToLower(mk), err)
				}
				if s < 0 {
					return fmt.Errorf("expected positive size for %q, got %v", mk, mv)
				}
				js.MetaCompactSize = s
			case "meta_compact_sync":
				b, ok := mv.(bool)
				if !ok {
					return fmt.Errorf("expected boolean for 'meta_compact_sync', got %T", mv)
				}
				js.MetaCompactSync = b
			case "limits", "tpm":
				// These are handled by the nested struct unmarshal via conf tags.
				// Skip them here to avoid unknown field errors.
			default:
				// Ignore unknown fields for forward compatibility.
			}
		}
		js.Enabled = doEnable
	default:
		return fmt.Errorf("expected map, bool or string to define JetStream, got %T", v)
	}
	return nil
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for OCSPConfig.
// It accepts:
//   - bool: true sets OCSPModeAuto, false sets OCSPModeNever
//   - map[string]any: full config with "mode" and "urls"/"url" fields
func (o *OCSPConfig) UnmarshalConfig(v any) error {
	switch val := v.(type) {
	case bool:
		if val {
			o.Mode = OCSPModeAuto
		} else {
			o.Mode = OCSPModeNever
		}
	case map[string]any:
		o.Mode = OCSPModeAuto // Default
		for kk, rawKv := range val {
			kv := unwrapConfigValue(rawKv)
			switch strings.ToLower(kk) {
			case "mode":
				mode, ok := kv.(string)
				if !ok {
					return fmt.Errorf("error parsing ocsp config: expected string for 'mode', got %T", kv)
				}
				switch strings.ToLower(mode) {
				case "always":
					o.Mode = OCSPModeAlways
				case "must":
					o.Mode = OCSPModeMust
				case "never":
					o.Mode = OCSPModeNever
				case "auto":
					o.Mode = OCSPModeAuto
				default:
					return fmt.Errorf("error parsing ocsp config: unsupported ocsp mode %q", mode)
				}
			case "urls":
				switch urls := kv.(type) {
				case []any:
					for _, u := range urls {
						s, ok := unwrapConfigValue(u).(string)
						if !ok {
							return fmt.Errorf("error parsing ocsp config: expected string in 'urls' array, got %T", u)
						}
						o.OverrideURLs = append(o.OverrideURLs, s)
					}
				case []string:
					o.OverrideURLs = urls
				default:
					return fmt.Errorf("error parsing ocsp config: expected array for 'urls', got %T", kv)
				}
			case "url":
				url, ok := kv.(string)
				if !ok {
					return fmt.Errorf("error parsing ocsp config: expected string for 'url', got %T", kv)
				}
				o.OverrideURLs = []string{url}
			default:
				return fmt.Errorf("error parsing ocsp config: unsupported field %q", kk)
			}
		}
	default:
		return fmt.Errorf("error parsing ocsp config: unsupported type %T", v)
	}
	return nil
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for OCSPResponseCacheConfig.
// It accepts:
//   - bool: true sets LOCAL cache type, false sets NONE
//   - map[string]any: full config with type, local_store, preserve_revoked, save_interval
func (c *OCSPResponseCacheConfig) UnmarshalConfig(v any) error {
	switch val := v.(type) {
	case bool:
		if val {
			defaults := NewOCSPResponseCacheConfig()
			*c = *defaults
		} else {
			defaults := NewOCSPResponseCacheConfig()
			*c = *defaults
			c.Type = NONE
		}
	case map[string]any:
		// Start with defaults.
		defaults := NewOCSPResponseCacheConfig()
		*c = *defaults
		for mk, rawMv := range val {
			mv := unwrapConfigValue(rawMv)
			switch strings.ToLower(mk) {
			case "type":
				cache, ok := mv.(string)
				if !ok {
					return fmt.Errorf("error parsing ocsp cache config: expected string for 'type', got %T", mv)
				}
				cacheType, exists := OCSPResponseCacheTypeMap[strings.ToLower(cache)]
				if !exists {
					return fmt.Errorf("error parsing ocsp cache config: unknown cache type %q", cache)
				}
				c.Type = cacheType
			case "local_store":
				store, ok := mv.(string)
				if !ok {
					return fmt.Errorf("error parsing ocsp cache config: expected string for 'local_store', got %T", mv)
				}
				c.LocalStore = store
			case "preserve_revoked":
				preserve, ok := mv.(bool)
				if !ok {
					return fmt.Errorf("error parsing ocsp cache config: expected boolean for 'preserve_revoked', got %T", mv)
				}
				c.PreserveRevoked = preserve
			case "save_interval":
				at := float64(0)
				switch sv := mv.(type) {
				case int64:
					at = float64(sv)
				case float64:
					at = sv
				case string:
					d, err := time.ParseDuration(sv)
					if err != nil {
						return fmt.Errorf("error parsing ocsp cache config, 'save_interval' %v", err)
					}
					at = d.Seconds()
				default:
					return fmt.Errorf("error parsing ocsp cache config: unexpected type %T for 'save_interval'", mv)
				}
				si := time.Duration(at) * time.Second
				if si < OCSPResponseCacheMinimumSaveInterval {
					si = OCSPResponseCacheMinimumSaveInterval
				}
				c.SaveInterval = si.Seconds()
			default:
				return fmt.Errorf("error parsing ocsp cache config: unknown field %q", mk)
			}
		}
	default:
		return fmt.Errorf("error parsing ocsp cache config: unsupported type %T", v)
	}
	return nil
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for TLSConfigOpts.
// It accepts a map[string]any with TLS configuration fields: cert_file, key_file,
// ca_file, verify, timeout, cipher_suites, curve_preferences, etc.
func (tc *TLSConfigOpts) UnmarshalConfig(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("expected map to define TLS config, got %T", v)
	}

	for mk, rawMv := range m {
		mv := unwrapConfigValue(rawMv)
		switch strings.ToLower(mk) {
		case "cert_file":
			s, ok := mv.(string)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'cert_file' to be filename")
			}
			tc.CertFile = s
		case "key_file":
			s, ok := mv.(string)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'key_file' to be filename")
			}
			tc.KeyFile = s
		case "ca_file":
			s, ok := mv.(string)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'ca_file' to be filename")
			}
			tc.CaFile = s
		case "insecure":
			b, ok := mv.(bool)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'insecure' to be a boolean")
			}
			tc.Insecure = b
		case "verify":
			b, ok := mv.(bool)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'verify' to be a boolean")
			}
			tc.Verify = b
		case "verify_and_map":
			b, ok := mv.(bool)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'verify_and_map' to be a boolean")
			}
			if b {
				tc.Verify = b
			}
			tc.Map = b
		case "verify_cert_and_check_known_urls":
			b, ok := mv.(bool)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'verify_cert_and_check_known_urls' to be a boolean")
			}
			if b {
				tc.Verify = b
			}
			tc.TLSCheckKnownURLs = b
		case "allow_insecure_cipher_suites":
			b, ok := mv.(bool)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'allow_insecure_cipher_suites' to be a boolean")
			}
			tc.AllowInsecureCiphers = b
		case "cipher_suites":
			ra, ok := mv.([]any)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'cipher_suites' to be an array")
			}
			if len(ra) == 0 {
				return fmt.Errorf("error parsing tls config, 'cipher_suites' cannot be empty")
			}
			tc.Ciphers = make([]uint16, 0, len(ra))
			for _, r := range ra {
				name, ok := unwrapConfigValue(r).(string)
				if !ok {
					return fmt.Errorf("error parsing tls config, expected string in 'cipher_suites', got %T", r)
				}
				cipher, err := parseCipher(name)
				if err != nil {
					return fmt.Errorf("error parsing tls config: %v", err)
				}
				tc.Ciphers = append(tc.Ciphers, cipher.ID)
			}
		case "curve_preferences":
			ra, ok := mv.([]any)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'curve_preferences' to be an array")
			}
			if len(ra) == 0 {
				return fmt.Errorf("error parsing tls config, 'curve_preferences' cannot be empty")
			}
			tc.CurvePreferences = make([]tls.CurveID, 0, len(ra))
			for _, r := range ra {
				name, ok := unwrapConfigValue(r).(string)
				if !ok {
					return fmt.Errorf("error parsing tls config, expected string in 'curve_preferences', got %T", r)
				}
				cps, err := parseCurvePreferences(name)
				if err != nil {
					return fmt.Errorf("error parsing tls config: %v", err)
				}
				tc.CurvePreferences = append(tc.CurvePreferences, cps)
			}
		case "timeout":
			at := float64(0)
			switch tv := mv.(type) {
			case int64:
				at = float64(tv)
			case float64:
				at = tv
			case string:
				d, err := time.ParseDuration(tv)
				if err != nil {
					return fmt.Errorf("error parsing tls config, 'timeout' %v", err)
				}
				at = d.Seconds()
			default:
				return fmt.Errorf("error parsing tls config, 'timeout' wrong type")
			}
			tc.Timeout = at
		case "connection_rate_limit":
			n, ok := mv.(int64)
			if !ok {
				return fmt.Errorf("error parsing tls config, 'connection_rate_limit' wrong type")
			}
			tc.RateLimit = n
		case "pinned_certs":
			ra, ok := mv.([]any)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'pinned_certs' to be an array")
			}
			if len(ra) != 0 {
				wl := PinnedCertSet{}
				for _, r := range ra {
					entry, ok := unwrapConfigValue(r).(string)
					if !ok {
						return fmt.Errorf("error parsing tls config, expected string in 'pinned_certs', got %T", r)
					}
					wl[strings.ToLower(entry)] = struct{}{}
				}
				tc.PinnedCerts = wl
			}
		case "handshake_first", "first", "immediate":
			switch hv := mv.(type) {
			case bool:
				tc.HandshakeFirst = hv
			case string:
				switch strings.ToLower(hv) {
				case "true", "on":
					tc.HandshakeFirst = true
				case "false", "off":
					tc.HandshakeFirst = false
				case "auto", "auto_fallback":
					tc.HandshakeFirst = true
					tc.FallbackDelay = DEFAULT_TLS_HANDSHAKE_FIRST_FALLBACK_DELAY
				default:
					if dur, err := time.ParseDuration(hv); err == nil {
						tc.HandshakeFirst = true
						tc.FallbackDelay = dur
					} else {
						return fmt.Errorf("field %q value %q is invalid", mk, hv)
					}
				}
			default:
				return fmt.Errorf("field %q should be a boolean or string, got %T", mk, mv)
			}
		case "cert_match_skip_invalid":
			b, ok := mv.(bool)
			if !ok {
				return fmt.Errorf("error parsing tls config, expected 'cert_match_skip_invalid' to be a boolean")
			}
			tc.CertMatchSkipInvalid = b
		case "cert_store":
			// handled by struct tag unmarshal
		case "cert_match_by":
			// handled by struct tag unmarshal
		case "cert_match":
			// handled by struct tag unmarshal
		case "ca_certs_match":
			rv := []string{}
			switch cv := mv.(type) {
			case string:
				rv = append(rv, cv)
			case []string:
				rv = append(rv, cv...)
			case []any:
				for _, t := range cv {
					if ts, ok := unwrapConfigValue(t).(string); ok {
						rv = append(rv, ts)
					} else {
						return fmt.Errorf("error parsing ca_certs_match: expected string, got %T", t)
					}
				}
			default:
				return fmt.Errorf("error parsing ca_certs_match: unsupported type %T", mv)
			}
			tc.CaCertsMatch = rv
		case "certs", "certificates":
			certs, ok := mv.([]any)
			if !ok {
				return fmt.Errorf("error parsing certificates config: unsupported type %T", mv)
			}
			tc.Certificates = make([]*TLSCertPairOpt, len(certs))
			for i, cv := range certs {
				pair, ok := unwrapConfigValue(cv).(map[string]any)
				if !ok {
					return fmt.Errorf("error parsing certificates config: expected map, got %T", cv)
				}
				certPair := &TLSCertPairOpt{}
				for k, rawFv := range pair {
					file, ok := unwrapConfigValue(rawFv).(string)
					if !ok {
						return fmt.Errorf("error parsing certificates config: expected string, got %T", rawFv)
					}
					switch strings.ToLower(k) {
					case "cert_file":
						certPair.CertFile = file
					case "key_file":
						certPair.KeyFile = file
					default:
						return fmt.Errorf("error parsing tls certs config, unknown field %q", k)
					}
				}
				if certPair.CertFile == _EMPTY_ || certPair.KeyFile == _EMPTY_ {
					return fmt.Errorf("error parsing certificates config: both 'cert_file' and 'key_file' options are required")
				}
				tc.Certificates[i] = certPair
			}
		case "min_version":
			minVersion, err := parseTLSVersion(mv)
			if err != nil {
				return fmt.Errorf("error parsing tls config: %v", err)
			}
			tc.MinVersion = minVersion
		case "ocsp_peer":
			// OCSP peer config is complex; skip here, handled externally.
		default:
			return fmt.Errorf("error parsing tls config, unknown field %q", mk)
		}
	}

	// Apply default cipher suites if none specified.
	if tc.Ciphers == nil {
		tc.Ciphers = defaultCipherSuites()
	}
	// Apply default curve preferences if none specified.
	if tc.CurvePreferences == nil {
		tc.CurvePreferences = defaultCurvePreferences()
	}

	// Validate: cannot combine cert_file with certs.
	if len(tc.Certificates) > 0 && tc.CertFile != _EMPTY_ {
		return fmt.Errorf("error parsing tls config, cannot combine 'cert_file' option with 'certs' option")
	}

	return nil
}

// AuthorizationConfig holds the parsed authorization block configuration.
// It mirrors the unexported authorization struct but is exported for use
// by ProcessConfigV2 and conf/v2.Unmarshal.
type AuthorizationConfig struct {
	// User is the single authorization username.
	User string
	// Pass is the single authorization password.
	Pass string
	// Token is the single authorization token.
	Token string
	// Timeout is the auth timeout in seconds.
	Timeout float64
	// Users is the list of authorized users.
	Users []*User
	// Nkeys is the list of authorized nkey users.
	Nkeys []*NkeyUser
	// DefaultPermissions are the default permissions for users.
	DefaultPermissions *Permissions
	// Callout is the auth callout configuration.
	Callout *AuthCallout
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for AuthorizationConfig.
// It accepts a map[string]any with authorization fields: user, pass, token, timeout,
// users array, default_permissions, auth_callout.
func (a *AuthorizationConfig) UnmarshalConfig(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("expected map to define authorization, got %T", v)
	}

	for mk, rawMv := range m {
		mv := unwrapConfigValue(rawMv)
		switch strings.ToLower(mk) {
		case "user", "username":
			s, ok := mv.(string)
			if !ok {
				return fmt.Errorf("expected string for 'user', got %T", mv)
			}
			a.User = s
		case "pass", "password":
			s, ok := mv.(string)
			if !ok {
				return fmt.Errorf("expected string for 'pass', got %T", mv)
			}
			a.Pass = s
		case "token":
			s, ok := mv.(string)
			if !ok {
				return fmt.Errorf("expected string for 'token', got %T", mv)
			}
			a.Token = s
		case "timeout":
			at := float64(0)
			switch tv := mv.(type) {
			case int64:
				at = float64(tv)
			case float64:
				at = tv
			case string:
				d, err := time.ParseDuration(tv)
				if err != nil {
					return fmt.Errorf("error parsing authorization timeout: %v", err)
				}
				at = d.Seconds()
			default:
				return fmt.Errorf("error parsing authorization timeout: wrong type %T", mv)
			}
			a.Timeout = at
		case "users":
			users, nkeys, err := unmarshalUsers(mv)
			if err != nil {
				return fmt.Errorf("error parsing authorization users: %v", err)
			}
			a.Users = users
			a.Nkeys = nkeys
		case "default_permission", "default_permissions", "permissions":
			perms, err := unmarshalPermissions(mv)
			if err != nil {
				return fmt.Errorf("error parsing authorization permissions: %v", err)
			}
			a.DefaultPermissions = perms
		case "auth_callout", "auth_hook":
			ac, err := unmarshalAuthCallout(mv)
			if err != nil {
				return fmt.Errorf("error parsing auth_callout: %v", err)
			}
			a.Callout = ac
		default:
			// Ignore unknown fields for forward compatibility.
		}
	}

	return nil
}

// unmarshalUsers parses a users array from the config into User and NkeyUser slices.
func unmarshalUsers(v any) ([]*User, []*NkeyUser, error) {
	uv, ok := v.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("expected users to be an array, got %T", v)
	}

	var (
		users []*User
		nkeys []*NkeyUser
	)

	for _, rawU := range uv {
		u := unwrapConfigValue(rawU)
		um, ok := u.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("expected user entry to be a map, got %T", u)
		}

		user := &User{}
		nkey := &NkeyUser{}

		for k, rawV := range um {
			fv := unwrapConfigValue(rawV)
			switch strings.ToLower(k) {
			case "nkey":
				nkey.Nkey, ok = fv.(string)
				if !ok {
					return nil, nil, fmt.Errorf("expected string for 'nkey', got %T", fv)
				}
			case "user", "username":
				user.Username, ok = fv.(string)
				if !ok {
					return nil, nil, fmt.Errorf("expected string for 'user', got %T", fv)
				}
			case "pass", "password":
				user.Password, ok = fv.(string)
				if !ok {
					return nil, nil, fmt.Errorf("expected string for 'pass', got %T", fv)
				}
			case "permission", "permissions", "authorization":
				perms, err := unmarshalPermissions(fv)
				if err != nil {
					return nil, nil, err
				}
				user.Permissions = perms
				nkey.Permissions = perms
			case "allowed_connection_types", "connection_types", "clients":
				cts, err := unmarshalStringArray(fv)
				if err != nil {
					return nil, nil, fmt.Errorf("error parsing connection types: %v", err)
				}
				m := make(map[string]struct{})
				for _, ct := range cts {
					m[strings.ToUpper(ct)] = struct{}{}
				}
				user.AllowedConnectionTypes = m
				nkey.AllowedConnectionTypes = m
			default:
				// Ignore unknown fields.
			}
		}

		if nkey.Nkey != _EMPTY_ {
			nkeys = append(nkeys, nkey)
		} else if user.Username != _EMPTY_ {
			users = append(users, user)
		}
	}

	return users, nkeys, nil
}

// unmarshalPermissions parses a permissions map from the config.
func unmarshalPermissions(v any) (*Permissions, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map for permissions, got %T", v)
	}

	perms := &Permissions{}
	for k, rawV := range m {
		fv := unwrapConfigValue(rawV)
		switch strings.ToLower(k) {
		case "pub", "publish":
			sp, err := unmarshalSubjectPermission(fv)
			if err != nil {
				return nil, fmt.Errorf("error parsing publish permissions: %v", err)
			}
			perms.Publish = sp
		case "sub", "subscribe":
			sp, err := unmarshalSubjectPermission(fv)
			if err != nil {
				return nil, fmt.Errorf("error parsing subscribe permissions: %v", err)
			}
			perms.Subscribe = sp
		case "resp", "response", "responses":
			rp, err := unmarshalResponsePermission(fv)
			if err != nil {
				return nil, fmt.Errorf("error parsing response permissions: %v", err)
			}
			perms.Response = rp
		default:
			// Ignore unknown fields.
		}
	}

	return perms, nil
}

// unmarshalSubjectPermission parses a subject permission from the config.
// It accepts either a string array (shorthand for allow-only) or a map
// with "allow" and "deny" arrays.
func unmarshalSubjectPermission(v any) (*SubjectPermission, error) {
	sp := &SubjectPermission{}

	switch val := v.(type) {
	case []any:
		// Shorthand: array of strings = allow list
		for _, item := range val {
			s, ok := unwrapConfigValue(item).(string)
			if !ok {
				return nil, fmt.Errorf("expected string in subject permission array, got %T", item)
			}
			sp.Allow = append(sp.Allow, s)
		}
	case string:
		// Single string = allow list with one entry
		sp.Allow = []string{val}
	case map[string]any:
		for k, rawV := range val {
			fv := unwrapConfigValue(rawV)
			switch strings.ToLower(k) {
			case "allow":
				arr, err := unmarshalStringArray(fv)
				if err != nil {
					return nil, fmt.Errorf("error parsing allow: %v", err)
				}
				sp.Allow = arr
			case "deny":
				arr, err := unmarshalStringArray(fv)
				if err != nil {
					return nil, fmt.Errorf("error parsing deny: %v", err)
				}
				sp.Deny = arr
			}
		}
	default:
		return nil, fmt.Errorf("expected array or map for subject permission, got %T", v)
	}

	return sp, nil
}

// unmarshalResponsePermission parses a response permission from the config.
func unmarshalResponsePermission(v any) (*ResponsePermission, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map for response permission, got %T", v)
	}

	rp := &ResponsePermission{}
	for k, rawV := range m {
		fv := unwrapConfigValue(rawV)
		switch strings.ToLower(k) {
		case "max", "max_msgs":
			n, ok := fv.(int64)
			if !ok {
				return nil, fmt.Errorf("expected integer for 'max', got %T", fv)
			}
			rp.MaxMsgs = int(n)
		case "ttl", "expires":
			switch tv := fv.(type) {
			case string:
				d, err := time.ParseDuration(tv)
				if err != nil {
					return nil, fmt.Errorf("error parsing response ttl: %v", err)
				}
				rp.Expires = d
			case int64:
				rp.Expires = time.Duration(tv) * time.Second
			default:
				return nil, fmt.Errorf("expected string or integer for 'ttl', got %T", fv)
			}
		}
	}

	return rp, nil
}

// unmarshalAuthCallout parses an auth_callout block from the config.
func unmarshalAuthCallout(v any) (*AuthCallout, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map for auth_callout, got %T", v)
	}

	ac := &AuthCallout{}
	for k, rawV := range m {
		fv := unwrapConfigValue(rawV)
		switch strings.ToLower(k) {
		case "issuer":
			s, ok := fv.(string)
			if !ok {
				return nil, fmt.Errorf("expected string for 'issuer', got %T", fv)
			}
			ac.Issuer = s
		case "account", "acc":
			s, ok := fv.(string)
			if !ok {
				return nil, fmt.Errorf("expected string for 'account', got %T", fv)
			}
			ac.Account = s
		case "auth_users", "users":
			arr, err := unmarshalStringArray(fv)
			if err != nil {
				return nil, fmt.Errorf("error parsing auth_users: %v", err)
			}
			ac.AuthUsers = arr
		case "xkey", "key":
			s, ok := fv.(string)
			if !ok {
				return nil, fmt.Errorf("expected string for 'xkey', got %T", fv)
			}
			ac.XKey = s
		default:
			// Ignore unknown fields.
		}
	}

	return ac, nil
}

// unmarshalStringArray parses a string array from either []any or []string.
func unmarshalStringArray(v any) ([]string, error) {
	switch val := v.(type) {
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			s, ok := unwrapConfigValue(item).(string)
			if !ok {
				return nil, fmt.Errorf("expected string in array, got %T", item)
			}
			result = append(result, s)
		}
		return result, nil
	case []string:
		return val, nil
	case string:
		return []string{val}, nil
	default:
		return nil, fmt.Errorf("expected array or string, got %T", v)
	}
}

// AccountsConfig holds the parsed accounts block configuration.
// It implements the conf/v2 Unmarshaler interface for the accounts
// configuration block which is very complex, handling account maps with
// users, nkeys, exports, imports, mappings, and jetstream limits.
// For the most complex sub-blocks (exports, imports, mappings), this
// implementation delegates to the existing parse* helper functions
// by re-wrapping values into the pedantic token format they expect.
type AccountsConfig struct {
	// Accounts is the list of accounts parsed from the config block.
	// Each account may contain users, nkeys, and other configuration.
	Accounts []*Account
	// Users accumulated from all accounts.
	Users []*User
	// Nkeys accumulated from all accounts.
	Nkeys []*NkeyUser
}

// UnmarshalConfig implements the conf/v2 Unmarshaler interface for AccountsConfig.
// The accounts block can be either a simple array of account names or a map
// of account names to their configuration.
func (ac *AccountsConfig) UnmarshalConfig(v any) error {
	switch val := v.(type) {
	case []any:
		// Simple array of account names.
		for _, item := range val {
			name, ok := unwrapConfigValue(item).(string)
			if !ok {
				return fmt.Errorf("expected string for account name, got %T", item)
			}
			ac.Accounts = append(ac.Accounts, NewAccount(name))
		}
	case map[string]any:
		for aname, rawAmv := range val {
			amv := unwrapConfigValue(rawAmv)
			mv, ok := amv.(map[string]any)
			if !ok {
				// Skip non-map entries (e.g., used variables).
				continue
			}

			acc := NewAccount(aname)
			ac.Accounts = append(ac.Accounts, acc)

			for k, rawV := range mv {
				fv := unwrapConfigValue(rawV)
				switch strings.ToLower(k) {
				case "users":
					users, nkeys, err := unmarshalUsers(fv)
					if err != nil {
						return fmt.Errorf("account %q: %v", aname, err)
					}
					for _, u := range users {
						u.Account = acc
					}
					for _, nk := range nkeys {
						nk.Account = acc
					}
					ac.Users = append(ac.Users, users...)
					ac.Nkeys = append(ac.Nkeys, nkeys...)
				case "exports":
					// Exports are complex; parsing is deferred to ProcessConfigV2
					// which can use the existing parseAccountExports function.
				case "imports":
					// Imports are complex; parsing is deferred to ProcessConfigV2
					// which can use the existing parseAccountImports function.
				case "jetstream":
					// JetStream per-account is complex; deferred to ProcessConfigV2.
				case "default_permissions":
					perms, err := unmarshalPermissions(fv)
					if err != nil {
						return fmt.Errorf("account %q: error parsing default_permissions: %v", aname, err)
					}
					acc.defaultPerms = perms
				case "mappings", "maps":
					// Mappings are complex; deferred to ProcessConfigV2.
				case "nkey":
					nk, ok := fv.(string)
					if !ok {
						return fmt.Errorf("account %q: expected string for 'nkey', got %T", aname, fv)
					}
					acc.Nkey = nk
				case "limits":
					// Account limits are complex; deferred to ProcessConfigV2.
				default:
					// Ignore unknown fields.
				}
			}
		}
	default:
		return fmt.Errorf("expected array or map for accounts, got %T", v)
	}

	return nil
}
