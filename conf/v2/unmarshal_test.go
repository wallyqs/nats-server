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

package v2

import (
	"math"
	"strings"
	"testing"
)

// TestUnmarshalNonPointerTarget verifies that passing a non-pointer or nil
// pointer to Unmarshal returns a descriptive error.
func TestUnmarshalNonPointerTarget(t *testing.T) {
	type Config struct{ Name string }

	// Non-pointer should return error.
	err := Unmarshal([]byte("name = test"), Config{})
	if err == nil {
		t.Fatal("expected error for non-pointer target")
	}
	if !strings.Contains(err.Error(), "non-nil pointer") {
		t.Fatalf("expected error about non-nil pointer, got: %v", err)
	}

	// Nil pointer should return error.
	var nilPtr *Config
	err = Unmarshal([]byte("name = test"), nilPtr)
	if err == nil {
		t.Fatal("expected error for nil pointer target")
	}
	if !strings.Contains(err.Error(), "non-nil pointer") {
		t.Fatalf("expected error about non-nil pointer, got: %v", err)
	}

	// Pointer to non-struct should return error.
	s := "hello"
	err = Unmarshal([]byte("name = test"), &s)
	if err == nil {
		t.Fatal("expected error for pointer to non-struct")
	}
	if !strings.Contains(err.Error(), "struct") {
		t.Fatalf("expected error about struct, got: %v", err)
	}
}

// TestUnmarshalStringField verifies basic string field unmarshaling.
func TestUnmarshalStringField(t *testing.T) {
	type Config struct {
		Name string
	}

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"quoted string", `name = "nats-server"`, "nats-server"},
		{"unquoted string", `name = nats-server`, "nats-server"},
		{"empty quoted string", `name = ""`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := Unmarshal([]byte(tt.input), &cfg); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Name != tt.expect {
				t.Fatalf("expected Name=%q, got %q", tt.expect, cfg.Name)
			}
		})
	}
}

// TestUnmarshalIntegerFields verifies integer field unmarshaling for all widths.
func TestUnmarshalIntegerFields(t *testing.T) {
	type Config struct {
		Port    int
		Port8   int8
		Port16  int16
		Port32  int32
		Port64  int64
		UPort   uint
		UPort8  uint8
		UPort16 uint16
		UPort32 uint32
		UPort64 uint64
	}

	input := `
port = 4222
port8 = 42
port16 = 4222
port32 = 4222
port64 = 4222
uport = 4222
uport8 = 42
uport16 = 4222
uport32 = 4222
uport64 = 4222
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.Port8 != 42 {
		t.Fatalf("expected Port8=42, got %d", cfg.Port8)
	}
	if cfg.Port16 != 4222 {
		t.Fatalf("expected Port16=4222, got %d", cfg.Port16)
	}
	if cfg.Port32 != 4222 {
		t.Fatalf("expected Port32=4222, got %d", cfg.Port32)
	}
	if cfg.Port64 != 4222 {
		t.Fatalf("expected Port64=4222, got %d", cfg.Port64)
	}
	if cfg.UPort != 4222 {
		t.Fatalf("expected UPort=4222, got %d", cfg.UPort)
	}
	if cfg.UPort8 != 42 {
		t.Fatalf("expected UPort8=42, got %d", cfg.UPort8)
	}
	if cfg.UPort16 != 4222 {
		t.Fatalf("expected UPort16=4222, got %d", cfg.UPort16)
	}
	if cfg.UPort32 != 4222 {
		t.Fatalf("expected UPort32=4222, got %d", cfg.UPort32)
	}
	if cfg.UPort64 != 4222 {
		t.Fatalf("expected UPort64=4222, got %d", cfg.UPort64)
	}
}

// TestUnmarshalIntegerOverflow verifies overflow checking for integer fields.
func TestUnmarshalIntegerOverflow(t *testing.T) {
	tests := []struct {
		name  string
		input string
		cfg   any
	}{
		{"int8 overflow", "val = 200", &struct{ Val int8 }{}},
		{"int8 underflow", "val = -200", &struct{ Val int8 }{}},
		{"int16 overflow", "val = 40000", &struct{ Val int16 }{}},
		{"int32 overflow", "val = 3000000000", &struct{ Val int32 }{}},
		{"uint8 overflow", "val = 300", &struct{ Val uint8 }{}},
		{"uint16 overflow", "val = 70000", &struct{ Val uint16 }{}},
		{"uint32 overflow", "val = 5000000000", &struct{ Val uint32 }{}},
		{"negative to uint", "val = -1", &struct{ Val uint }{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Unmarshal([]byte(tt.input), tt.cfg)
			if err == nil {
				t.Fatal("expected overflow error")
			}
			if !strings.Contains(err.Error(), "overflow") {
				t.Fatalf("expected overflow error, got: %v", err)
			}
		})
	}
}

// TestUnmarshalFloatFields verifies float field unmarshaling.
func TestUnmarshalFloatFields(t *testing.T) {
	type Config struct {
		Rate64 float64
		Rate32 float32
	}

	input := `
rate64 = 3.14
rate32 = 2.5
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(cfg.Rate64-3.14) > 1e-10 {
		t.Fatalf("expected Rate64=3.14, got %f", cfg.Rate64)
	}
	if math.Abs(float64(cfg.Rate32)-2.5) > 1e-6 {
		t.Fatalf("expected Rate32=2.5, got %f", cfg.Rate32)
	}
}

// TestUnmarshalBoolField verifies bool field unmarshaling for all variants.
func TestUnmarshalBoolField(t *testing.T) {
	type Config struct {
		Debug bool
		Trace bool
	}

	tests := []struct {
		name        string
		input       string
		expectDebug bool
		expectTrace bool
	}{
		{"true/false", "debug = true\ntrace = false", true, false},
		{"yes/no", "debug = yes\ntrace = no", true, false},
		{"on/off", "debug = on\ntrace = off", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := Unmarshal([]byte(tt.input), &cfg); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Debug != tt.expectDebug {
				t.Fatalf("expected Debug=%v, got %v", tt.expectDebug, cfg.Debug)
			}
			if cfg.Trace != tt.expectTrace {
				t.Fatalf("expected Trace=%v, got %v", tt.expectTrace, cfg.Trace)
			}
		})
	}
}

// TestUnmarshalInt64ToFloat64Coercion verifies int64 values coerce to float64.
func TestUnmarshalInt64ToFloat64Coercion(t *testing.T) {
	type Config struct {
		Timeout float64
	}

	input := `timeout = 5`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 5.0 {
		t.Fatalf("expected Timeout=5.0, got %f", cfg.Timeout)
	}
}

// TestUnmarshalStructTagFieldNaming verifies struct tag controls field matching.
func TestUnmarshalStructTagFieldNaming(t *testing.T) {
	type Config struct {
		Host string `conf:"addr"`
		Port int    `conf:"listen_port"`
	}

	input := `
addr = 127.0.0.1
listen_port = 4222
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected Host=127.0.0.1, got %q", cfg.Host)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
}

// TestUnmarshalStructTagSkip verifies conf:"-" fields are skipped.
func TestUnmarshalStructTagSkip(t *testing.T) {
	type Config struct {
		Name   string
		Secret string `conf:"-"`
	}

	input := `
name = test
secret = sensitive
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
	if cfg.Secret != "" {
		t.Fatalf("expected Secret to be empty (skipped), got %q", cfg.Secret)
	}
}

// TestUnmarshalStructTagOmitempty verifies omitempty doesn't affect unmarshaling.
func TestUnmarshalStructTagOmitempty(t *testing.T) {
	type Config struct {
		Port int    `conf:"port,omitempty"`
		Name string `conf:"name,omitempty"`
	}

	input := `
port = 4222
name = test
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
}

// TestUnmarshalCaseInsensitiveKeyMatching verifies case-insensitive matching.
func TestUnmarshalCaseInsensitiveKeyMatching(t *testing.T) {
	type Config struct {
		MaxPayload int64
	}

	tests := []struct {
		name  string
		input string
	}{
		{"lowercase", "maxpayload = 1024"},
		{"mixed case", "MaxPayload = 1024"},
		{"uppercase", "MAXPAYLOAD = 1024"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := Unmarshal([]byte(tt.input), &cfg); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.MaxPayload != 1024 {
				t.Fatalf("expected MaxPayload=1024, got %d", cfg.MaxPayload)
			}
		})
	}
}

// TestUnmarshalPointerFields verifies pointer field handling.
func TestUnmarshalPointerFields(t *testing.T) {
	type Config struct {
		Name    *string
		Port    *int64
		Debug   *bool
		Missing *string
	}

	input := `
name = "nats"
port = 4222
debug = true
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name == nil || *cfg.Name != "nats" {
		t.Fatalf("expected Name=nats, got %v", cfg.Name)
	}
	if cfg.Port == nil || *cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %v", cfg.Port)
	}
	if cfg.Debug == nil || *cfg.Debug != true {
		t.Fatalf("expected Debug=true, got %v", cfg.Debug)
	}
	if cfg.Missing != nil {
		t.Fatalf("expected Missing=nil, got %v", cfg.Missing)
	}
}

// TestUnmarshalPointerToNestedStruct verifies pointer to struct fields.
func TestUnmarshalPointerToNestedStruct(t *testing.T) {
	type Inner struct {
		Name string
		Port int
	}
	type Config struct {
		Cluster *Inner
		Missing *Inner
	}

	input := `
cluster {
  name = "my-cluster"
  port = 6222
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Cluster == nil {
		t.Fatal("expected Cluster to be non-nil")
	}
	if cfg.Cluster.Name != "my-cluster" {
		t.Fatalf("expected Cluster.Name=my-cluster, got %q", cfg.Cluster.Name)
	}
	if cfg.Cluster.Port != 6222 {
		t.Fatalf("expected Cluster.Port=6222, got %d", cfg.Cluster.Port)
	}
	if cfg.Missing != nil {
		t.Fatal("expected Missing to be nil")
	}
}

// TestUnmarshalUnexportedFields verifies unexported fields are silently skipped.
func TestUnmarshalUnexportedFields(t *testing.T) {
	type Config struct {
		Name     string
		internal string //nolint:unused
		Port     int
	}

	input := `
name = test
internal = should-be-skipped
port = 4222
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
}

// TestUnmarshalEmbeddedStruct verifies embedded struct field promotion.
func TestUnmarshalEmbeddedStruct(t *testing.T) {
	type Base struct {
		Host string
		Port int
	}
	type Config struct {
		Base
		Name string
	}

	input := `
host = 127.0.0.1
port = 4222
name = test
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected Host=127.0.0.1, got %q", cfg.Host)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
}

// TestUnmarshalEmbeddedPointerStruct verifies embedded pointer struct promotion.
func TestUnmarshalEmbeddedPointerStruct(t *testing.T) {
	type Base struct {
		Host string
		Port int
	}
	type Config struct {
		*Base
		Name string
	}

	input := `
host = 127.0.0.1
port = 4222
name = test
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Base == nil {
		t.Fatal("expected embedded Base to be non-nil")
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected Host=127.0.0.1, got %q", cfg.Host)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
}

// TestUnmarshalMissingKeys verifies fields retain zero/pre-initialized values.
func TestUnmarshalMissingKeys(t *testing.T) {
	type Config struct {
		Name  string
		Port  int
		Debug bool
	}

	// Only provide name, leave port and debug missing.
	input := `name = test`
	cfg := Config{Port: 9999, Debug: true}
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
	// Pre-initialized values should be preserved.
	if cfg.Port != 9999 {
		t.Fatalf("expected Port=9999 (pre-initialized), got %d", cfg.Port)
	}
	if cfg.Debug != true {
		t.Fatalf("expected Debug=true (pre-initialized), got %v", cfg.Debug)
	}
}

// TestUnmarshalTypeMismatchError verifies descriptive type mismatch errors.
func TestUnmarshalTypeMismatchError(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		target any
		errMsg string
	}{
		{
			"string to int",
			`port = "not-a-number"`,
			&struct{ Port int }{},
			"port",
		},
		{
			"bool to string (no error - bool has string representation)",
			// Actually this is a type mismatch: config has bool, field wants string
			`name = true`,
			&struct{ Name string }{},
			"name",
		},
		{
			"int to bool",
			`debug = 42`,
			&struct{ Debug bool }{},
			"debug",
		},
		{
			"map to string",
			"name { key = val }",
			&struct{ Name string }{},
			"name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Unmarshal([]byte(tt.input), tt.target)
			if err == nil {
				t.Fatal("expected type mismatch error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.errMsg) {
				t.Fatalf("expected error to mention %q, got: %v", tt.errMsg, err)
			}
		})
	}
}

// TestUnmarshalNestedStruct verifies nested struct unmarshaling.
func TestUnmarshalNestedStruct(t *testing.T) {
	type Cluster struct {
		Name string
		Port int
	}
	type Config struct {
		Name    string
		Cluster Cluster
	}

	input := `
name = test
cluster {
  name = my-cluster
  port = 6222
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
	if cfg.Cluster.Name != "my-cluster" {
		t.Fatalf("expected Cluster.Name=my-cluster, got %q", cfg.Cluster.Name)
	}
	if cfg.Cluster.Port != 6222 {
		t.Fatalf("expected Cluster.Port=6222, got %d", cfg.Cluster.Port)
	}
}

// TestUnmarshalInt64ToFloat32Coercion verifies int64 values coerce to float32.
func TestUnmarshalInt64ToFloat32Coercion(t *testing.T) {
	type Config struct {
		Rate float32
	}

	input := `rate = 10`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rate != 10.0 {
		t.Fatalf("expected Rate=10.0, got %f", cfg.Rate)
	}
}

// TestUnmarshalFloat64ToFloat32 verifies float64 config values assigned to float32.
func TestUnmarshalFloat64ToFloat32(t *testing.T) {
	type Config struct {
		Rate float32
	}

	input := `rate = 3.14`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(float64(cfg.Rate)-3.14) > 1e-5 {
		t.Fatalf("expected Rate≈3.14, got %f", cfg.Rate)
	}
}

// TestUnmarshalEmptyInput verifies empty config doesn't error and fields retain zero.
func TestUnmarshalEmptyInput(t *testing.T) {
	type Config struct {
		Name string
		Port int
	}

	var cfg Config
	if err := Unmarshal([]byte(""), &cfg); err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if cfg.Name != "" {
		t.Fatalf("expected Name to be empty, got %q", cfg.Name)
	}
	if cfg.Port != 0 {
		t.Fatalf("expected Port=0, got %d", cfg.Port)
	}
}

// TestUnmarshalUntaggedFieldLowercaseMatch verifies untagged fields match
// by lowercased Go field name.
func TestUnmarshalUntaggedFieldLowercaseMatch(t *testing.T) {
	type Config struct {
		MaxPayload int64
		ServerName string
	}

	input := `
maxpayload = 1048576
servername = my-server
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxPayload != 1048576 {
		t.Fatalf("expected MaxPayload=1048576, got %d", cfg.MaxPayload)
	}
	if cfg.ServerName != "my-server" {
		t.Fatalf("expected ServerName=my-server, got %q", cfg.ServerName)
	}
}

// TestUnmarshalMultipleEmbeddedStructs verifies multiple embedded structs.
func TestUnmarshalMultipleEmbeddedStructs(t *testing.T) {
	type Network struct {
		Host string
		Port int
	}
	type Auth struct {
		Username string
		Password string
	}
	type Config struct {
		Network
		Auth
		Name string
	}

	input := `
host = 127.0.0.1
port = 4222
username = admin
password = secret
name = test
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected Host=127.0.0.1, got %q", cfg.Host)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.Username != "admin" {
		t.Fatalf("expected Username=admin, got %q", cfg.Username)
	}
	if cfg.Password != "secret" {
		t.Fatalf("expected Password=secret, got %q", cfg.Password)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
}

// TestUnmarshalPointerToBasicTypes verifies pointer fields for basic types.
func TestUnmarshalPointerToBasicTypes(t *testing.T) {
	type Config struct {
		Name    *string
		Port    *int
		Rate    *float64
		Debug   *bool
		Missing *string
	}

	input := `
name = test
port = 4222
rate = 3.14
debug = true
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name == nil || *cfg.Name != "test" {
		t.Fatalf("expected Name=test")
	}
	if cfg.Port == nil || *cfg.Port != 4222 {
		t.Fatalf("expected Port=4222")
	}
	if cfg.Rate == nil || math.Abs(*cfg.Rate-3.14) > 1e-10 {
		t.Fatalf("expected Rate=3.14")
	}
	if cfg.Debug == nil || *cfg.Debug != true {
		t.Fatalf("expected Debug=true")
	}
	if cfg.Missing != nil {
		t.Fatalf("expected Missing=nil")
	}
}

// TestUnmarshalDeeplyNestedStruct verifies deeply nested struct unmarshaling.
func TestUnmarshalDeeplyNestedStruct(t *testing.T) {
	type Level3 struct {
		Value string
	}
	type Level2 struct {
		Inner Level3
	}
	type Level1 struct {
		Middle Level2
	}
	type Config struct {
		Top Level1
	}

	input := `
top {
  middle {
    inner {
      value = deep
    }
  }
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Top.Middle.Inner.Value != "deep" {
		t.Fatalf("expected deeply nested value=deep, got %q", cfg.Top.Middle.Inner.Value)
	}
}

// TestUnmarshalSkipsUsedVariables verifies that variable definitions
// referenced via $var are silently skipped during unmarshal, even in
// strict mode. This matches v1 behavior where server/opts.go checks
// tk.IsUsedVariable() before reporting unknown fields.
func TestUnmarshalSkipsUsedVariables(t *testing.T) {
	type Config struct {
		Port     int    `conf:"port"`
		HttpPort int    `conf:"http_port"`
		Host     string `conf:"host"`
	}

	// Config where monitoring_port is a variable definition consumed
	// by $monitoring_port in http_port. The unmarshal should NOT reject
	// monitoring_port as an unknown field.
	input := `
monitoring_port = 8222
port = 4222
http_port = $monitoring_port
host = 127.0.0.1
`

	// Test in strict mode: variable definitions must be skipped.
	var cfg Config
	err := UnmarshalWith([]byte(input), &cfg, &UnmarshalOptions{Strict: true})
	if err != nil {
		t.Fatalf("unexpected error in strict mode: %v", err)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.HttpPort != 8222 {
		t.Fatalf("expected HttpPort=8222 (resolved from $monitoring_port), got %d", cfg.HttpPort)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected Host=127.0.0.1, got %q", cfg.Host)
	}

	// Test in permissive mode as well — should also work.
	var cfg2 Config
	err = Unmarshal([]byte(input), &cfg2)
	if err != nil {
		t.Fatalf("unexpected error in permissive mode: %v", err)
	}
	if cfg2.HttpPort != 8222 {
		t.Fatalf("expected HttpPort=8222 in permissive mode, got %d", cfg2.HttpPort)
	}
}
