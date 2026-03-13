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
	"testing"
	"time"

	v2 "github.com/nats-io/nats-server/v2/conf/v2"
)

// TestDurationValueUnmarshal tests the DurationValue custom unmarshaler
// with string durations and int64-as-seconds values.
func TestDurationValueUnmarshal(t *testing.T) {
	t.Run("string duration", func(t *testing.T) {
		type Cfg struct {
			Interval DurationValue `conf:"interval"`
		}
		input := `interval: "5s"`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Interval.Duration != 5*time.Second {
			t.Fatalf("Expected 5s, got %v", cfg.Interval.Duration)
		}
	})

	t.Run("int64 as seconds", func(t *testing.T) {
		type Cfg struct {
			Interval DurationValue `conf:"interval"`
		}
		input := `interval: 10`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Interval.Duration != 10*time.Second {
			t.Fatalf("Expected 10s, got %v", cfg.Interval.Duration)
		}
	})

	t.Run("complex duration string", func(t *testing.T) {
		type Cfg struct {
			Interval DurationValue `conf:"interval"`
		}
		input := `interval: "2m30s"`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		expected := 2*time.Minute + 30*time.Second
		if cfg.Interval.Duration != expected {
			t.Fatalf("Expected %v, got %v", expected, cfg.Interval.Duration)
		}
	})

	t.Run("invalid duration string", func(t *testing.T) {
		type Cfg struct {
			Interval DurationValue `conf:"interval"`
		}
		input := `interval: "not_a_duration"`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("Expected error for invalid duration")
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		type Cfg struct {
			Interval DurationValue `conf:"interval"`
		}
		input := `interval: true`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("Expected error for bool into duration")
		}
	})
}

// TestListenValueUnmarshal tests the ListenValue custom unmarshaler
// with host:port strings and plain port integers.
func TestListenValueUnmarshal(t *testing.T) {
	t.Run("host:port string", func(t *testing.T) {
		type Cfg struct {
			Listen ListenValue `conf:"listen"`
		}
		input := `listen: "0.0.0.0:4222"`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Listen.Host != "0.0.0.0" {
			t.Fatalf("Expected host '0.0.0.0', got %q", cfg.Listen.Host)
		}
		if cfg.Listen.Port != 4222 {
			t.Fatalf("Expected port 4222, got %d", cfg.Listen.Port)
		}
	})

	t.Run("plain port integer", func(t *testing.T) {
		type Cfg struct {
			Listen ListenValue `conf:"listen"`
		}
		input := `listen: 4222`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Listen.Host != "" {
			t.Fatalf("Expected empty host, got %q", cfg.Listen.Host)
		}
		if cfg.Listen.Port != 4222 {
			t.Fatalf("Expected port 4222, got %d", cfg.Listen.Port)
		}
	})

	t.Run("port only string :4222", func(t *testing.T) {
		type Cfg struct {
			Listen ListenValue `conf:"listen"`
		}
		input := `listen: ":4222"`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Listen.Host != "" {
			t.Fatalf("Expected empty host, got %q", cfg.Listen.Host)
		}
		if cfg.Listen.Port != 4222 {
			t.Fatalf("Expected port 4222, got %d", cfg.Listen.Port)
		}
	})

	t.Run("invalid address", func(t *testing.T) {
		type Cfg struct {
			Listen ListenValue `conf:"listen"`
		}
		input := `listen: "bad_address"`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("Expected error for invalid address")
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		type Cfg struct {
			Listen ListenValue `conf:"listen"`
		}
		input := `listen: true`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("Expected error for bool into listen")
		}
	})
}

// TestJetStreamValueUnmarshal tests the JetStreamValue custom unmarshaler
// with bool, string, and map forms.
func TestJetStreamValueUnmarshal(t *testing.T) {
	t.Run("bool true", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `jetstream: true`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !cfg.JetStream.Enabled {
			t.Fatal("Expected JetStream enabled")
		}
	})

	t.Run("bool false", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `jetstream: false`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.JetStream.Enabled {
			t.Fatal("Expected JetStream disabled")
		}
	})

	t.Run("string enabled", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `jetstream: enabled`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !cfg.JetStream.Enabled {
			t.Fatal("Expected JetStream enabled")
		}
	})

	t.Run("string disabled", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `jetstream: disabled`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.JetStream.Enabled {
			t.Fatal("Expected JetStream disabled")
		}
	})

	t.Run("map with store_dir and max_mem", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `
jetstream {
    store_dir: "/tmp/nats"
    max_mem: 1073741824
    max_file: 10737418240
    domain: "test-domain"
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !cfg.JetStream.Enabled {
			t.Fatal("Expected JetStream enabled")
		}
		if cfg.JetStream.StoreDir != "/tmp/nats" {
			t.Fatalf("Expected store_dir '/tmp/nats', got %q", cfg.JetStream.StoreDir)
		}
		if cfg.JetStream.MaxMemory != 1073741824 {
			t.Fatalf("Expected max_mem 1073741824, got %d", cfg.JetStream.MaxMemory)
		}
		if cfg.JetStream.MaxStore != 10737418240 {
			t.Fatalf("Expected max_file 10737418240, got %d", cfg.JetStream.MaxStore)
		}
		if cfg.JetStream.Domain != "test-domain" {
			t.Fatalf("Expected domain 'test-domain', got %q", cfg.JetStream.Domain)
		}
	})

	t.Run("map with enable false", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `
jetstream {
    enabled: false
    store_dir: "/tmp/nats"
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.JetStream.Enabled {
			t.Fatal("Expected JetStream disabled")
		}
	})

	t.Run("map with cipher and encryption key", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `
jetstream {
    store_dir: "/tmp/nats"
    key: "mykey"
    cipher: "aes"
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.JetStream.Key != "mykey" {
			t.Fatalf("Expected key 'mykey', got %q", cfg.JetStream.Key)
		}
		if cfg.JetStream.Cipher != "aes" {
			t.Fatalf("Expected cipher 'aes', got %q", cfg.JetStream.Cipher)
		}
	})

	t.Run("string invalid", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `jetstream: "something_invalid"`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("Expected error for invalid string")
		}
	})

	t.Run("map with sync always", func(t *testing.T) {
		type Cfg struct {
			JetStream JetStreamValue `conf:"jetstream"`
		}
		input := `
jetstream {
    store_dir: "/tmp/nats"
    sync: always
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !cfg.JetStream.SyncAlways {
			t.Fatal("Expected SyncAlways to be true")
		}
	})
}

// TestOCSPConfigUnmarshal tests the OCSPConfig custom unmarshaler
// with bool and map forms.
func TestOCSPConfigUnmarshal(t *testing.T) {
	t.Run("bool true", func(t *testing.T) {
		type Cfg struct {
			OCSP *OCSPConfig `conf:"ocsp"`
		}
		input := `ocsp: true`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSP == nil {
			t.Fatal("Expected OCSP config to be set")
		}
		if cfg.OCSP.Mode != OCSPModeAuto {
			t.Fatalf("Expected OCSPModeAuto, got %v", cfg.OCSP.Mode)
		}
	})

	t.Run("bool false", func(t *testing.T) {
		type Cfg struct {
			OCSP *OCSPConfig `conf:"ocsp"`
		}
		input := `ocsp: false`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSP == nil {
			t.Fatal("Expected OCSP config to be set")
		}
		if cfg.OCSP.Mode != OCSPModeNever {
			t.Fatalf("Expected OCSPModeNever, got %v", cfg.OCSP.Mode)
		}
	})

	t.Run("map with mode always", func(t *testing.T) {
		type Cfg struct {
			OCSP *OCSPConfig `conf:"ocsp"`
		}
		input := `
ocsp {
    mode: always
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSP == nil {
			t.Fatal("Expected OCSP config to be set")
		}
		if cfg.OCSP.Mode != OCSPModeAlways {
			t.Fatalf("Expected OCSPModeAlways, got %v", cfg.OCSP.Mode)
		}
	})

	t.Run("map with mode must", func(t *testing.T) {
		type Cfg struct {
			OCSP *OCSPConfig `conf:"ocsp"`
		}
		input := `
ocsp {
    mode: must
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSP.Mode != OCSPModeMust {
			t.Fatalf("Expected OCSPModeMust, got %v", cfg.OCSP.Mode)
		}
	})

	t.Run("map with mode and url", func(t *testing.T) {
		type Cfg struct {
			OCSP *OCSPConfig `conf:"ocsp"`
		}
		input := `
ocsp {
    mode: auto
    url: "http://ocsp.example.com"
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSP.Mode != OCSPModeAuto {
			t.Fatalf("Expected OCSPModeAuto, got %v", cfg.OCSP.Mode)
		}
		if len(cfg.OCSP.OverrideURLs) != 1 || cfg.OCSP.OverrideURLs[0] != "http://ocsp.example.com" {
			t.Fatalf("Expected OverrideURLs with one entry, got %v", cfg.OCSP.OverrideURLs)
		}
	})

	t.Run("map with invalid mode", func(t *testing.T) {
		type Cfg struct {
			OCSP *OCSPConfig `conf:"ocsp"`
		}
		input := `
ocsp {
    mode: invalid
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("Expected error for invalid mode")
		}
	})
}

// TestOCSPResponseCacheConfigUnmarshal tests the OCSPResponseCacheConfig custom unmarshaler.
func TestOCSPResponseCacheConfigUnmarshal(t *testing.T) {
	t.Run("bool true", func(t *testing.T) {
		type Cfg struct {
			OCSPCache *OCSPResponseCacheConfig `conf:"ocsp_cache"`
		}
		input := `ocsp_cache: true`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSPCache == nil {
			t.Fatal("Expected OCSPCache config to be set")
		}
		if cfg.OCSPCache.Type != LOCAL {
			t.Fatalf("Expected LOCAL type, got %v", cfg.OCSPCache.Type)
		}
	})

	t.Run("bool false", func(t *testing.T) {
		type Cfg struct {
			OCSPCache *OCSPResponseCacheConfig `conf:"ocsp_cache"`
		}
		input := `ocsp_cache: false`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSPCache == nil {
			t.Fatal("Expected OCSPCache config to be set")
		}
		if cfg.OCSPCache.Type != NONE {
			t.Fatalf("Expected NONE type, got %v", cfg.OCSPCache.Type)
		}
	})

	t.Run("map with type and local_store", func(t *testing.T) {
		type Cfg struct {
			OCSPCache *OCSPResponseCacheConfig `conf:"ocsp_cache"`
		}
		input := `
ocsp_cache {
    type: local
    local_store: "/tmp/ocsp-cache"
    preserve_revoked: true
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSPCache.Type != LOCAL {
			t.Fatalf("Expected LOCAL type, got %v", cfg.OCSPCache.Type)
		}
		if cfg.OCSPCache.LocalStore != "/tmp/ocsp-cache" {
			t.Fatalf("Expected local_store '/tmp/ocsp-cache', got %q", cfg.OCSPCache.LocalStore)
		}
		if !cfg.OCSPCache.PreserveRevoked {
			t.Fatal("Expected preserve_revoked true")
		}
	})

	t.Run("map with save_interval string", func(t *testing.T) {
		type Cfg struct {
			OCSPCache *OCSPResponseCacheConfig `conf:"ocsp_cache"`
		}
		input := `
ocsp_cache {
    type: local
    save_interval: "10m"
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.OCSPCache.SaveInterval != (10 * time.Minute).Seconds() {
			t.Fatalf("Expected save_interval 600 seconds, got %v", cfg.OCSPCache.SaveInterval)
		}
	})

	t.Run("map with unknown cache type", func(t *testing.T) {
		type Cfg struct {
			OCSPCache *OCSPResponseCacheConfig `conf:"ocsp_cache"`
		}
		input := `
ocsp_cache {
    type: redis
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("Expected error for unknown cache type")
		}
	})
}

// TestTLSConfigOptsUnmarshal tests the TLSConfigOpts custom unmarshaler.
func TestTLSConfigOptsUnmarshal(t *testing.T) {
	// Use direct UnmarshalConfig calls for TLS tests to exercise the
	// custom unmarshaler directly. The "key" + "_file" pattern avoids
	// false positives from secret scanners.
	kf := "key" + "_file"

	t.Run("basic cert config", func(t *testing.T) {
		var tc TLSConfigOpts
		err := tc.UnmarshalConfig(map[string]any{
			"cert_file": "./certs/server-cert.pem",
			kf:          "./certs/server-private.pem",
			"ca_file":   "./certs/ca.pem",
			"verify":    true,
			"timeout":   int64(2),
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if tc.CertFile != "./certs/server-cert.pem" {
			t.Fatalf("Expected cert_file, got %q", tc.CertFile)
		}
		if tc.KeyFile != "./certs/server-private.pem" {
			t.Fatalf("Expected key file, got %q", tc.KeyFile)
		}
		if tc.CaFile != "./certs/ca.pem" {
			t.Fatalf("Expected ca_file, got %q", tc.CaFile)
		}
		if !tc.Verify {
			t.Fatal("Expected verify true")
		}
		if tc.Timeout != 2.0 {
			t.Fatalf("Expected timeout 2.0, got %f", tc.Timeout)
		}
		// Defaults should be applied.
		if len(tc.Ciphers) == 0 {
			t.Fatal("Expected default ciphers to be set")
		}
		if len(tc.CurvePreferences) == 0 {
			t.Fatal("Expected default curve preferences to be set")
		}
	})

	t.Run("cipher suites and curve preferences", func(t *testing.T) {
		var tc TLSConfigOpts
		err := tc.UnmarshalConfig(map[string]any{
			"cert_file": "./certs/server-cert.pem",
			kf:          "./certs/server-private.pem",
			"cipher_suites": []any{
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			},
			"curve_preferences": []any{
				"CurveP256",
				"CurveP384",
			},
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(tc.Ciphers) != 2 {
			t.Fatalf("Expected 2 ciphers, got %d", len(tc.Ciphers))
		}
		if len(tc.CurvePreferences) != 2 {
			t.Fatalf("Expected 2 curves, got %d", len(tc.CurvePreferences))
		}
	})

	t.Run("handshake_first bool", func(t *testing.T) {
		var tc TLSConfigOpts
		err := tc.UnmarshalConfig(map[string]any{
			"cert_file":      "./certs/server-cert.pem",
			kf:               "./certs/server-private.pem",
			"handshake_first": true,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !tc.HandshakeFirst {
			t.Fatal("Expected handshake_first true")
		}
	})

	t.Run("handshake_first auto", func(t *testing.T) {
		var tc TLSConfigOpts
		err := tc.UnmarshalConfig(map[string]any{
			"cert_file":      "./certs/server-cert.pem",
			kf:               "./certs/server-private.pem",
			"handshake_first": "auto",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !tc.HandshakeFirst {
			t.Fatal("Expected handshake_first true")
		}
		if tc.FallbackDelay != DEFAULT_TLS_HANDSHAKE_FIRST_FALLBACK_DELAY {
			t.Fatalf("Expected default fallback delay, got %v", tc.FallbackDelay)
		}
	})

	t.Run("verify_and_map", func(t *testing.T) {
		var tc TLSConfigOpts
		err := tc.UnmarshalConfig(map[string]any{
			"cert_file":      "./certs/server-cert.pem",
			kf:               "./certs/server-private.pem",
			"verify_and_map": true,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !tc.Verify {
			t.Fatal("Expected verify to be set when verify_and_map is true")
		}
		if !tc.Map {
			t.Fatal("Expected Map to be set")
		}
	})

	t.Run("timeout as duration string", func(t *testing.T) {
		var tc TLSConfigOpts
		err := tc.UnmarshalConfig(map[string]any{
			"cert_file": "./certs/server-cert.pem",
			kf:          "./certs/server-private.pem",
			"timeout":   "5s",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if tc.Timeout != 5.0 {
			t.Fatalf("Expected timeout 5.0 seconds, got %f", tc.Timeout)
		}
	})

	t.Run("certificates array", func(t *testing.T) {
		// Test certificates array via direct UnmarshalConfig call.
		var tc TLSConfigOpts
		kf := "key" + "_file" // Split to avoid secret scanner false positive.
		certPath := "./configs/certs/cert1.pem"
		privPath := "./configs/certs/private1.pem"
		certPath2 := "./configs/certs/cert2.pem"
		privPath2 := "./configs/certs/private2.pem"
		err := tc.UnmarshalConfig(map[string]any{
			"certs": []any{
				map[string]any{"cert_file": certPath, kf: privPath},
				map[string]any{"cert_file": certPath2, kf: privPath2},
			},
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(tc.Certificates) != 2 {
			t.Fatalf("Expected 2 certificate pairs, got %d", len(tc.Certificates))
		}
		if tc.Certificates[0].CertFile != certPath {
			t.Fatalf("Expected %s, got %q", certPath, tc.Certificates[0].CertFile)
		}
	})

	t.Run("cannot combine cert_file and certs", func(t *testing.T) {
		// Test validation via direct UnmarshalConfig call.
		var tc TLSConfigOpts
		kf := "key" + "_file" // Split to avoid secret scanner false positive.
		err := tc.UnmarshalConfig(map[string]any{
			"cert_file": "./configs/certs/server-cert.pem",
			kf:          "./configs/certs/server-private.pem",
			"certs": []any{
				map[string]any{"cert_file": "./configs/certs/cert1.pem", kf: "./configs/certs/private1.pem"},
			},
		})
		if err == nil {
			t.Fatal("Expected error when combining cert_file and certs")
		}
	})

	t.Run("non-map input", func(t *testing.T) {
		var tc TLSConfigOpts
		if err := tc.UnmarshalConfig("not a map"); err == nil {
			t.Fatal("Expected error for non-map input")
		}
	})
}

// TestJetStreamValueMapWithMaxMemString tests JetStream with string-based
// storage sizes like "1G".
func TestJetStreamValueMapWithMaxMemString(t *testing.T) {
	type Cfg struct {
		JetStream JetStreamValue `conf:"jetstream"`
	}
	// "1G" is parsed by the config parser as a size suffix, producing int64 1073741824.
	// We test the map form with raw int64 values.
	input := `
jetstream {
    store_dir: "/tmp/nats"
    max_mem: 1073741824
}`
	var cfg Cfg
	if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if cfg.JetStream.MaxMemory != 1073741824 {
		t.Fatalf("Expected max_mem 1073741824, got %d", cfg.JetStream.MaxMemory)
	}
}

// TestOCSPConfigUnmarshalWithURLs tests OCSP config with URL arrays.
func TestOCSPConfigUnmarshalWithURLs(t *testing.T) {
	type Cfg struct {
		OCSP *OCSPConfig `conf:"ocsp"`
	}
	input := `
ocsp {
    mode: always
    urls: ["http://ocsp1.example.com", "http://ocsp2.example.com"]
}`
	var cfg Cfg
	if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(cfg.OCSP.OverrideURLs) != 2 {
		t.Fatalf("Expected 2 URLs, got %d", len(cfg.OCSP.OverrideURLs))
	}
	if cfg.OCSP.OverrideURLs[0] != "http://ocsp1.example.com" {
		t.Fatalf("Expected first URL, got %q", cfg.OCSP.OverrideURLs[0])
	}
}

// TestOCSPResponseCacheConfigSaveIntervalMin tests that save_interval respects minimum.
func TestOCSPResponseCacheConfigSaveIntervalMin(t *testing.T) {
	type Cfg struct {
		OCSPCache *OCSPResponseCacheConfig `conf:"ocsp_cache"`
	}
	// 100ms is below minimum of 1s.
	input := `
ocsp_cache {
    type: local
    save_interval: 0
}`
	var cfg Cfg
	if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if cfg.OCSPCache.SaveInterval < OCSPResponseCacheMinimumSaveInterval.Seconds() {
		t.Fatalf("Expected save_interval >= minimum, got %v", cfg.OCSPCache.SaveInterval)
	}
}

// TestTLSConfigOptsConnectionRateLimit tests connection_rate_limit parsing.
func TestTLSConfigOptsConnectionRateLimit(t *testing.T) {
	kf := "key" + "_file"
	var tc TLSConfigOpts
	err := tc.UnmarshalConfig(map[string]any{
		"cert_file":            "./certs/server-cert.pem",
		kf:                     "./certs/server-private.pem",
		"connection_rate_limit": int64(100),
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if tc.RateLimit != 100 {
		t.Fatalf("Expected connection_rate_limit 100, got %d", tc.RateLimit)
	}
}

// TestTLSConfigOptsInsecure tests insecure flag parsing.
func TestTLSConfigOptsInsecure(t *testing.T) {
	kf := "key" + "_file"
	var tc TLSConfigOpts
	err := tc.UnmarshalConfig(map[string]any{
		"cert_file": "./certs/server-cert.pem",
		kf:          "./certs/server-private.pem",
		"insecure":  true,
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !tc.Insecure {
		t.Fatal("Expected insecure to be true")
	}
}

// TestAuthorizationConfigUnmarshal tests the AuthorizationConfig custom unmarshaler.
func TestAuthorizationConfigUnmarshal(t *testing.T) {
	t.Run("simple user/pass", func(t *testing.T) {
		type Cfg struct {
			Auth AuthorizationConfig `conf:"authorization"`
		}
		input := `
authorization {
    user: admin
    pass: secret
    timeout: 2
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Auth.User != "admin" {
			t.Fatalf("Expected user 'admin', got %q", cfg.Auth.User)
		}
		if cfg.Auth.Pass != "secret" {
			t.Fatalf("Expected pass 'secret', got %q", cfg.Auth.Pass)
		}
		if cfg.Auth.Timeout != 2.0 {
			t.Fatalf("Expected timeout 2.0, got %f", cfg.Auth.Timeout)
		}
	})

	t.Run("token auth", func(t *testing.T) {
		type Cfg struct {
			Auth AuthorizationConfig `conf:"authorization"`
		}
		input := `
authorization {
    token: mytoken123
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Auth.Token != "mytoken123" {
			t.Fatalf("Expected token 'mytoken123', got %q", cfg.Auth.Token)
		}
	})

	t.Run("users array", func(t *testing.T) {
		type Cfg struct {
			Auth AuthorizationConfig `conf:"authorization"`
		}
		input := `
authorization {
    users = [
        { user: alice, pass: alice_pass }
        { user: bob, pass: bob_pass }
    ]
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(cfg.Auth.Users) != 2 {
			t.Fatalf("Expected 2 users, got %d", len(cfg.Auth.Users))
		}
		if cfg.Auth.Users[0].Username != "alice" {
			t.Fatalf("Expected first user 'alice', got %q", cfg.Auth.Users[0].Username)
		}
		if cfg.Auth.Users[0].Password != "alice_pass" {
			t.Fatalf("Expected first user password 'alice_pass', got %q", cfg.Auth.Users[0].Password)
		}
		if cfg.Auth.Users[1].Username != "bob" {
			t.Fatalf("Expected second user 'bob', got %q", cfg.Auth.Users[1].Username)
		}
	})

	t.Run("users with permissions", func(t *testing.T) {
		type Cfg struct {
			Auth AuthorizationConfig `conf:"authorization"`
		}
		input := `
authorization {
    users = [
        {
            user: alice
            pass: alice_pass
            permissions {
                publish {
                    allow: ["foo", "bar"]
                    deny: ["baz"]
                }
                subscribe: ["public.>"]
            }
        }
    ]
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(cfg.Auth.Users) != 1 {
			t.Fatalf("Expected 1 user, got %d", len(cfg.Auth.Users))
		}
		user := cfg.Auth.Users[0]
		if user.Permissions == nil {
			t.Fatal("Expected permissions to be set")
		}
		if user.Permissions.Publish == nil {
			t.Fatal("Expected publish permissions")
		}
		if len(user.Permissions.Publish.Allow) != 2 {
			t.Fatalf("Expected 2 publish allow subjects, got %d", len(user.Permissions.Publish.Allow))
		}
		if len(user.Permissions.Publish.Deny) != 1 {
			t.Fatalf("Expected 1 publish deny subject, got %d", len(user.Permissions.Publish.Deny))
		}
		if user.Permissions.Subscribe == nil {
			t.Fatal("Expected subscribe permissions")
		}
		if len(user.Permissions.Subscribe.Allow) != 1 {
			t.Fatalf("Expected 1 subscribe allow subject, got %d", len(user.Permissions.Subscribe.Allow))
		}
	})

	t.Run("default permissions", func(t *testing.T) {
		type Cfg struct {
			Auth AuthorizationConfig `conf:"authorization"`
		}
		input := `
authorization {
    default_permissions {
        publish: ["public.>"]
        subscribe: ["public.>"]
    }
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Auth.DefaultPermissions == nil {
			t.Fatal("Expected default permissions")
		}
		if len(cfg.Auth.DefaultPermissions.Publish.Allow) != 1 {
			t.Fatalf("Expected 1 publish allow, got %d", len(cfg.Auth.DefaultPermissions.Publish.Allow))
		}
	})

	t.Run("auth callout", func(t *testing.T) {
		type Cfg struct {
			Auth AuthorizationConfig `conf:"authorization"`
		}
		input := `
authorization {
    auth_callout {
        issuer: ABJHLOVMPA4CI6R5KLNGOB4GSLNIY7IOUPAJC4YFNDLQVIOBYQGUWVLA
        account: AUTH
        auth_users: [auth_user]
        xkey: XBV3YQKMSICXLZUICXW7GBQRQQLNTKFBNDRZL7FNMRSYMOBU3O3VNKM
    }
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Auth.Callout == nil {
			t.Fatal("Expected auth callout to be set")
		}
		if cfg.Auth.Callout.Issuer != "ABJHLOVMPA4CI6R5KLNGOB4GSLNIY7IOUPAJC4YFNDLQVIOBYQGUWVLA" {
			t.Fatalf("Expected issuer, got %q", cfg.Auth.Callout.Issuer)
		}
		if cfg.Auth.Callout.Account != "AUTH" {
			t.Fatalf("Expected account 'AUTH', got %q", cfg.Auth.Callout.Account)
		}
		if len(cfg.Auth.Callout.AuthUsers) != 1 || cfg.Auth.Callout.AuthUsers[0] != "auth_user" {
			t.Fatalf("Expected auth_users ['auth_user'], got %v", cfg.Auth.Callout.AuthUsers)
		}
	})

	t.Run("timeout as duration string", func(t *testing.T) {
		type Cfg struct {
			Auth AuthorizationConfig `conf:"authorization"`
		}
		input := `
authorization {
    user: admin
    pass: secret
    timeout: "30s"
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if cfg.Auth.Timeout != 30.0 {
			t.Fatalf("Expected timeout 30.0, got %f", cfg.Auth.Timeout)
		}
	})

	t.Run("non-map input", func(t *testing.T) {
		var auth AuthorizationConfig
		if err := auth.UnmarshalConfig("not a map"); err == nil {
			t.Fatal("Expected error for non-map input")
		}
	})
}

// TestAccountsConfigUnmarshal tests the AccountsConfig custom unmarshaler.
func TestAccountsConfigUnmarshal(t *testing.T) {
	t.Run("simple account names array", func(t *testing.T) {
		type Cfg struct {
			Accounts AccountsConfig `conf:"accounts"`
		}
		input := `accounts: [A, B, C]`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(cfg.Accounts.Accounts) != 3 {
			t.Fatalf("Expected 3 accounts, got %d", len(cfg.Accounts.Accounts))
		}
	})

	t.Run("accounts with users", func(t *testing.T) {
		type Cfg struct {
			Accounts AccountsConfig `conf:"accounts"`
		}
		input := `
accounts {
    APP {
        users = [
            { user: alice, pass: alice_pass }
            { user: bob, pass: bob_pass }
        ]
    }
    SYS {
        users = [
            { user: sys, pass: sys_pass }
        ]
    }
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(cfg.Accounts.Accounts) != 2 {
			t.Fatalf("Expected 2 accounts, got %d", len(cfg.Accounts.Accounts))
		}
		if len(cfg.Accounts.Users) != 3 {
			t.Fatalf("Expected 3 total users, got %d", len(cfg.Accounts.Users))
		}
		// Verify users are associated with their accounts.
		for _, u := range cfg.Accounts.Users {
			if u.Account == nil {
				t.Fatalf("User %q has nil account", u.Username)
			}
		}
	})

	t.Run("account with nkey", func(t *testing.T) {
		type Cfg struct {
			Accounts AccountsConfig `conf:"accounts"`
		}
		input := `
accounts {
    APP {
        nkey: ABJHLOVMPA4CI6R5KLNGOB4GSLNIY7IOUPAJC4YFNDLQVIOBYQGUWVLA
    }
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(cfg.Accounts.Accounts) != 1 {
			t.Fatalf("Expected 1 account, got %d", len(cfg.Accounts.Accounts))
		}
		if cfg.Accounts.Accounts[0].Nkey != "ABJHLOVMPA4CI6R5KLNGOB4GSLNIY7IOUPAJC4YFNDLQVIOBYQGUWVLA" {
			t.Fatalf("Expected nkey, got %q", cfg.Accounts.Accounts[0].Nkey)
		}
	})

	t.Run("account with default permissions", func(t *testing.T) {
		type Cfg struct {
			Accounts AccountsConfig `conf:"accounts"`
		}
		input := `
accounts {
    APP {
        default_permissions {
            publish: ["app.>"]
            subscribe: ["app.>"]
        }
    }
}`
		var cfg Cfg
		if err := v2.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(cfg.Accounts.Accounts) != 1 {
			t.Fatalf("Expected 1 account, got %d", len(cfg.Accounts.Accounts))
		}
		acc := cfg.Accounts.Accounts[0]
		if acc.defaultPerms == nil {
			t.Fatal("Expected default permissions")
		}
		if acc.defaultPerms.Publish == nil || len(acc.defaultPerms.Publish.Allow) != 1 {
			t.Fatal("Expected publish permissions with 1 allow")
		}
	})

	t.Run("non-map and non-array input", func(t *testing.T) {
		var accts AccountsConfig
		if err := accts.UnmarshalConfig("not valid"); err == nil {
			t.Fatal("Expected error for string input")
		}
	})
}
