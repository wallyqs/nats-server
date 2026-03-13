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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// ConfigError Type and Source() Method Tests (VAL-UNMARSHAL-025)
// ============================================================================

// TestUnmarshalErrorIsConfigError verifies that Unmarshal type mismatch errors
// are of type *ConfigError and can be extracted via errors.As.
func TestUnmarshalErrorIsConfigError(t *testing.T) {
	type Config struct {
		Port int
	}

	input := `port = "hello"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected error to be *ConfigError, got %T: %v", err, err)
	}

	// Verify structured position access.
	if cfgErr.Line == 0 {
		t.Fatal("expected Line to be non-zero")
	}
	if cfgErr.Reason == "" {
		t.Fatal("expected Reason to be non-empty")
	}
}

// TestUnmarshalConfigErrorSource verifies Source() returns correct format.
func TestUnmarshalConfigErrorSource(t *testing.T) {
	type Config struct {
		Port int
	}

	input := `port = "hello"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	// When parsing from string (no file), Source() returns "line:col".
	src := cfgErr.Source()
	if cfgErr.File != "" {
		t.Fatalf("expected File to be empty for string parse, got %q", cfgErr.File)
	}
	// Source should be "line:col" format.
	if !strings.Contains(src, ":") {
		t.Fatalf("expected Source() to contain ':', got %q", src)
	}
	// Should NOT have a file prefix.
	parts := strings.Split(src, ":")
	if len(parts) != 2 {
		t.Fatalf("expected Source() to be 'line:col' format, got %q", src)
	}
}

// TestUnmarshalConfigErrorFormat verifies Error() includes "line:col: reason".
func TestUnmarshalConfigErrorFormat(t *testing.T) {
	type Config struct {
		Port int
	}

	input := `port = "hello"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error")
	}

	errStr := err.Error()
	// Error format: "line:col: reason" (no file when parsing from string)
	if !strings.Contains(errStr, ":") {
		t.Fatalf("expected error to contain position info, got: %v", errStr)
	}
	// Should mention type mismatch details.
	if !strings.Contains(strings.ToLower(errStr), "port") {
		t.Fatalf("expected error to mention field name, got: %v", errStr)
	}
}

// ============================================================================
// Type Mismatch Errors with Position (VAL-UNMARSHAL-024)
// ============================================================================

// TestUnmarshalTypeMismatchErrorWithPosition verifies type mismatch errors
// include line and column from the config source.
func TestUnmarshalTypeMismatchErrorWithPosition(t *testing.T) {
	type Config struct {
		Port int
		Name string
	}

	tests := []struct {
		name         string
		input        string
		expectedLine int
	}{
		{
			"string to int on line 1",
			`port = "not-a-number"`,
			1,
		},
		{
			"string to int on line 2",
			"name = test\nport = \"not-a-number\"",
			2,
		},
		{
			"bool to string",
			`name = true`,
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			err := Unmarshal([]byte(tt.input), &cfg)
			if err == nil {
				t.Fatal("expected error")
			}

			var cfgErr *ConfigError
			if !errors.As(err, &cfgErr) {
				t.Fatalf("expected *ConfigError, got %T: %v", err, err)
			}

			if cfgErr.Line != tt.expectedLine {
				t.Fatalf("expected Line=%d, got %d (error: %v)", tt.expectedLine, cfgErr.Line, err)
			}
		})
	}
}

// TestUnmarshalOverflowErrorWithPosition verifies overflow errors include position.
func TestUnmarshalOverflowErrorWithPosition(t *testing.T) {
	type Config struct {
		Val int8
	}

	input := `val = 200`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected overflow error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	if cfgErr.Line != 1 {
		t.Fatalf("expected Line=1, got %d", cfgErr.Line)
	}
	if !strings.Contains(cfgErr.Reason, "overflow") {
		t.Fatalf("expected reason to contain 'overflow', got: %s", cfgErr.Reason)
	}
}

// ============================================================================
// Unknown Field Errors with Position (VAL-UNMARSHAL-029)
// ============================================================================

// TestUnmarshalStrictUnknownFieldErrorWithPosition verifies unknown field errors
// in strict mode include both the field name and position, matching the server's
// unknownConfigFieldErr pattern: "file:line:col: unknown field \"name\"".
func TestUnmarshalStrictUnknownFieldErrorWithPosition(t *testing.T) {
	type Config struct {
		Name string
	}

	input := "name = test\nbogus_field = 42"
	var cfg Config
	err := UnmarshalWith([]byte(input), &cfg, &UnmarshalOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error for unknown field in strict mode")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	// Should include the field name.
	errStr := err.Error()
	if !strings.Contains(errStr, "bogus_field") {
		t.Fatalf("expected error to contain field name 'bogus_field', got: %v", errStr)
	}

	// Should include "unknown field".
	if !strings.Contains(errStr, "unknown field") {
		t.Fatalf("expected error to contain 'unknown field', got: %v", errStr)
	}

	// Should be on line 2.
	if cfgErr.Line != 2 {
		t.Fatalf("expected Line=2 for bogus_field, got %d", cfgErr.Line)
	}

	// Error format should match: "line:col: unknown field \"bogus_field\""
	if !strings.Contains(errStr, fmt.Sprintf("unknown field %q", "bogus_field")) {
		t.Fatalf("expected error format with quoted field name, got: %v", errStr)
	}
}

// ============================================================================
// UnmarshalFile Errors Include Filename (VAL-UNMARSHAL-024)
// ============================================================================

// TestUnmarshalFileErrorIncludesFilename verifies UnmarshalFile errors
// include the filename in the error position.
func TestUnmarshalFileErrorIncludesFilename(t *testing.T) {
	type Config struct {
		Port int
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(fp, []byte("port = \"not-a-number\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	err := UnmarshalFile(fp, &cfg)
	if err == nil {
		t.Fatal("expected error for type mismatch in file")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	// File field should contain the file path.
	if cfgErr.File == "" {
		t.Fatal("expected File to be non-empty for file-based parsing")
	}

	// Source() should include filename: "file:line:col"
	src := cfgErr.Source()
	if !strings.Contains(src, "test.conf") {
		t.Fatalf("expected Source() to contain filename, got: %q", src)
	}

	// Error() should include filename.
	errStr := err.Error()
	if !strings.Contains(errStr, "test.conf") {
		t.Fatalf("expected error to contain filename, got: %v", errStr)
	}
}

// TestUnmarshalFileStrictUnknownFieldIncludesFilename verifies strict mode
// unknown field errors from UnmarshalFile include the filename.
func TestUnmarshalFileStrictUnknownFieldIncludesFilename(t *testing.T) {
	type Config struct {
		Name string
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "server.conf")
	if err := os.WriteFile(fp, []byte("name = test\nbogus = 42\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	err := UnmarshalFileWith(fp, &cfg, &UnmarshalOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	// Should include filename.
	if !strings.Contains(cfgErr.File, "server.conf") {
		t.Fatalf("expected File to contain 'server.conf', got %q", cfgErr.File)
	}

	// Error format: "file:line:col: unknown field \"bogus\""
	errStr := err.Error()
	if !strings.Contains(errStr, "server.conf") {
		t.Fatalf("expected error to contain filename, got: %v", errStr)
	}
	if !strings.Contains(errStr, "bogus") {
		t.Fatalf("expected error to contain field name, got: %v", errStr)
	}
}

// ============================================================================
// String-Based vs File-Based Error Format
// ============================================================================

// TestUnmarshalStringErrorOmitsFile verifies that when parsing from string
// (no file), the error Source() returns "line:col" without file prefix.
func TestUnmarshalStringErrorOmitsFile(t *testing.T) {
	type Config struct {
		Port int
	}

	input := `port = "bad"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	src := cfgErr.Source()
	// Should be "line:col" format without file.
	parts := strings.Split(src, ":")
	if len(parts) != 2 {
		t.Fatalf("expected Source() to be 'line:col' (2 parts), got %q (%d parts)", src, len(parts))
	}
}

// TestUnmarshalFileErrorHasThreePartSource verifies file-based errors have
// "file:line:col" format in Source().
func TestUnmarshalFileErrorHasThreePartSource(t *testing.T) {
	type Config struct {
		Port int
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(fp, []byte("port = \"bad\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	err := UnmarshalFile(fp, &cfg)
	if err == nil {
		t.Fatal("expected error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	src := cfgErr.Source()
	// Should be "file:line:col" format.
	if !strings.Contains(src, fp) {
		t.Fatalf("expected Source() to contain file path %q, got %q", fp, src)
	}
}

// ============================================================================
// All Existing Tests Still Pass (regression)
// ============================================================================

// TestUnmarshalExistingBehaviorPreserved verifies that basic Unmarshal still
// works correctly for valid configs (no errors should be returned).
func TestUnmarshalExistingBehaviorPreserved(t *testing.T) {
	type Config struct {
		Name       string
		Port       int
		Debug      bool
		Rate       float64
		MaxPayload int64 `conf:"max_payload"`
	}

	input := `
name = "nats-server"
port = 4222
debug = true
rate = 3.14
max_payload = 1MB
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "nats-server" {
		t.Fatalf("expected Name=nats-server, got %q", cfg.Name)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if !cfg.Debug {
		t.Fatal("expected Debug=true")
	}
	if cfg.MaxPayload != 1048576 {
		t.Fatalf("expected MaxPayload=1048576, got %d", cfg.MaxPayload)
	}
}

// TestUnmarshalNestedStructErrorPosition verifies errors in nested structs
// include correct position info.
func TestUnmarshalNestedStructErrorPosition(t *testing.T) {
	type Inner struct {
		Port int
	}
	type Config struct {
		Cluster Inner
	}

	input := `
cluster {
  port = "bad"
}
`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}

	// The error should be on line 3 (where port = "bad" is).
	if cfgErr.Line != 3 {
		t.Fatalf("expected Line=3, got %d (error: %v)", cfgErr.Line, err)
	}
}

// TestUnmarshalMultipleErrorsFirstReported verifies the first error is reported.
func TestUnmarshalMultipleErrorsFirstReported(t *testing.T) {
	type Config struct {
		Port  int
		Debug int
	}

	// Both are type mismatches, but first error stops processing.
	input := "port = \"bad\"\ndebug = \"also-bad\""
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}
	// We can't predict which one comes first due to map iteration order,
	// but the error should have valid position info.
	if cfgErr.Line == 0 {
		t.Fatal("expected non-zero line")
	}
}
