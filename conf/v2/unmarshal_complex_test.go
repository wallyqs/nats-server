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
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Slice Unmarshaling Tests
// ============================================================================

// TestUnmarshalStringSlice verifies []string unmarshaling from config arrays.
func TestUnmarshalStringSlice(t *testing.T) {
	type Config struct {
		Routes []string
	}

	input := `routes = ["nats://host1:4222", "nats://host2:4222", "nats://host3:4222"]`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"nats://host1:4222", "nats://host2:4222", "nats://host3:4222"}
	if !reflect.DeepEqual(cfg.Routes, expected) {
		t.Fatalf("expected Routes=%v, got %v", expected, cfg.Routes)
	}
}

// TestUnmarshalInt64Slice verifies []int64 unmarshaling from config arrays.
func TestUnmarshalInt64Slice(t *testing.T) {
	type Config struct {
		Ports []int64
	}

	input := "ports = [\n  4222\n  4223\n  4224\n]\n"
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []int64{4222, 4223, 4224}
	if !reflect.DeepEqual(cfg.Ports, expected) {
		t.Fatalf("expected Ports=%v, got %v", expected, cfg.Ports)
	}
}

// TestUnmarshalFloat64Slice verifies []float64 unmarshaling from config arrays.
func TestUnmarshalFloat64Slice(t *testing.T) {
	type Config struct {
		Rates []float64
	}

	input := `rates = [1.5, 2.5, 3.5]`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []float64{1.5, 2.5, 3.5}
	if !reflect.DeepEqual(cfg.Rates, expected) {
		t.Fatalf("expected Rates=%v, got %v", expected, cfg.Rates)
	}
}

// TestUnmarshalBoolSlice verifies []bool unmarshaling from config arrays.
func TestUnmarshalBoolSlice(t *testing.T) {
	type Config struct {
		Flags []bool
	}

	input := `flags = [true, false, true]`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []bool{true, false, true}
	if !reflect.DeepEqual(cfg.Flags, expected) {
		t.Fatalf("expected Flags=%v, got %v", expected, cfg.Flags)
	}
}

// TestUnmarshalEmptySlice verifies empty arrays produce non-nil zero-length slices.
func TestUnmarshalEmptySlice(t *testing.T) {
	type Config struct {
		Routes []string
		Ports  []int64
	}

	input := `
routes = []
ports = []
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Routes == nil {
		t.Fatal("expected Routes to be non-nil")
	}
	if len(cfg.Routes) != 0 {
		t.Fatalf("expected Routes to be empty, got %v", cfg.Routes)
	}
	if cfg.Ports == nil {
		t.Fatal("expected Ports to be non-nil")
	}
	if len(cfg.Ports) != 0 {
		t.Fatalf("expected Ports to be empty, got %v", cfg.Ports)
	}
}

// TestUnmarshalSliceTypeMismatch verifies type mismatches in arrays produce errors.
func TestUnmarshalSliceTypeMismatch(t *testing.T) {
	type Config struct {
		Ports []int64
	}

	input := "ports = [\n  \"not-a-number\"\n  4222\n]\n"
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for type mismatch in slice")
	}
	if !strings.Contains(err.Error(), "ports[0]") {
		t.Fatalf("expected error to mention element index, got: %v", err)
	}
}

// TestUnmarshalIntSlice verifies []int unmarshaling (narrower int).
func TestUnmarshalIntSlice(t *testing.T) {
	type Config struct {
		Values []int
	}

	input := "values = [\n  1\n  2\n  3\n]\n"
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []int{1, 2, 3}
	if !reflect.DeepEqual(cfg.Values, expected) {
		t.Fatalf("expected Values=%v, got %v", expected, cfg.Values)
	}
}

// ============================================================================
// Slice of Structs Tests
// ============================================================================

// TestUnmarshalSliceOfStructs verifies []struct unmarshaling from arrays of maps.
func TestUnmarshalSliceOfStructs(t *testing.T) {
	type User struct {
		Name string
		Age  int
	}
	type Config struct {
		Users []User
	}

	input := `
users = [
  {name: "alice", age: 30},
  {name: "bob", age: 25}
]
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(cfg.Users))
	}
	if cfg.Users[0].Name != "alice" || cfg.Users[0].Age != 30 {
		t.Fatalf("expected first user {alice, 30}, got %+v", cfg.Users[0])
	}
	if cfg.Users[1].Name != "bob" || cfg.Users[1].Age != 25 {
		t.Fatalf("expected second user {bob, 25}, got %+v", cfg.Users[1])
	}
}

// TestUnmarshalSliceOfStructsWithNested verifies nested structs within slices.
func TestUnmarshalSliceOfStructsWithNested(t *testing.T) {
	type Address struct {
		City string
	}
	type User struct {
		Name    string
		Address Address
	}
	type Config struct {
		Users []User
	}

	input := `
users = [
  {name: "alice", address: {city: "NYC"}},
  {name: "bob", address: {city: "LA"}}
]
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(cfg.Users))
	}
	if cfg.Users[0].Address.City != "NYC" {
		t.Fatalf("expected first user city=NYC, got %q", cfg.Users[0].Address.City)
	}
	if cfg.Users[1].Address.City != "LA" {
		t.Fatalf("expected second user city=LA, got %q", cfg.Users[1].Address.City)
	}
}

// TestUnmarshalSliceOfStructsInvalidElement verifies error for non-map elements.
func TestUnmarshalSliceOfStructsInvalidElement(t *testing.T) {
	type User struct {
		Name string
	}
	type Config struct {
		Users []User
	}

	input := `users = ["not-a-map"]`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for non-map element in struct slice")
	}
}

// ============================================================================
// Scalar-to-Single-Element-Slice Coercion Tests
// ============================================================================

// TestUnmarshalScalarToStringSlice verifies scalar string coerces to []string.
func TestUnmarshalScalarToStringSlice(t *testing.T) {
	type Config struct {
		Routes []string
	}

	input := `routes = "nats://host:4222"`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"nats://host:4222"}
	if !reflect.DeepEqual(cfg.Routes, expected) {
		t.Fatalf("expected Routes=%v, got %v", expected, cfg.Routes)
	}
}

// TestUnmarshalScalarToIntSlice verifies scalar int64 coerces to []int64.
func TestUnmarshalScalarToIntSlice(t *testing.T) {
	type Config struct {
		Ports []int64
	}

	input := `ports = 4222`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []int64{4222}
	if !reflect.DeepEqual(cfg.Ports, expected) {
		t.Fatalf("expected Ports=%v, got %v", expected, cfg.Ports)
	}
}

// TestUnmarshalScalarToBoolSlice verifies scalar bool coerces to []bool.
func TestUnmarshalScalarToBoolSlice(t *testing.T) {
	type Config struct {
		Flags []bool
	}

	input := `flags = true`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []bool{true}
	if !reflect.DeepEqual(cfg.Flags, expected) {
		t.Fatalf("expected Flags=%v, got %v", expected, cfg.Flags)
	}
}

// TestUnmarshalScalarToFloat64Slice verifies scalar float64 coerces to []float64.
func TestUnmarshalScalarToFloat64Slice(t *testing.T) {
	type Config struct {
		Rates []float64
	}

	input := `rates = 3.14`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Rates) != 1 {
		t.Fatalf("expected 1 rate, got %d", len(cfg.Rates))
	}
	if math.Abs(cfg.Rates[0]-3.14) > 1e-10 {
		t.Fatalf("expected Rates[0]=3.14, got %f", cfg.Rates[0])
	}
}

// ============================================================================
// Map Unmarshaling Tests
// ============================================================================

// TestUnmarshalMapStringString verifies map[string]string unmarshaling.
func TestUnmarshalMapStringString(t *testing.T) {
	type Config struct {
		Metadata map[string]string
	}

	input := `
metadata {
  region: "us-east"
  env: "production"
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Metadata["region"] != "us-east" {
		t.Fatalf("expected region=us-east, got %q", cfg.Metadata["region"])
	}
	if cfg.Metadata["env"] != "production" {
		t.Fatalf("expected env=production, got %q", cfg.Metadata["env"])
	}
}

// TestUnmarshalMapStringInt64 verifies map[string]int64 unmarshaling.
func TestUnmarshalMapStringInt64(t *testing.T) {
	type Config struct {
		Ports map[string]int64
	}

	input := `
ports {
  http: 8080
  grpc: 9090
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Ports["http"] != 8080 {
		t.Fatalf("expected http=8080, got %d", cfg.Ports["http"])
	}
	if cfg.Ports["grpc"] != 9090 {
		t.Fatalf("expected grpc=9090, got %d", cfg.Ports["grpc"])
	}
}

// TestUnmarshalMapStringFloat64 verifies map[string]float64 unmarshaling.
func TestUnmarshalMapStringFloat64(t *testing.T) {
	type Config struct {
		Rates map[string]float64
	}

	input := `
rates {
  read: 1.5
  write: 2.5
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rates["read"] != 1.5 {
		t.Fatalf("expected read=1.5, got %f", cfg.Rates["read"])
	}
	if cfg.Rates["write"] != 2.5 {
		t.Fatalf("expected write=2.5, got %f", cfg.Rates["write"])
	}
}

// TestUnmarshalMapStringBool verifies map[string]bool unmarshaling.
func TestUnmarshalMapStringBool(t *testing.T) {
	type Config struct {
		Features map[string]bool
	}

	input := `
features {
  auth: true
  tls: false
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Features["auth"] != true {
		t.Fatalf("expected auth=true, got %v", cfg.Features["auth"])
	}
	if cfg.Features["tls"] != false {
		t.Fatalf("expected tls=false, got %v", cfg.Features["tls"])
	}
}

// TestUnmarshalMapStringAny verifies map[string]any unmarshaling.
func TestUnmarshalMapStringAny(t *testing.T) {
	type Config struct {
		Metadata map[string]any
	}

	input := `
metadata {
  name: "test"
  port: 4222
  rate: 3.14
  debug: true
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Metadata["name"] != "test" {
		t.Fatalf("expected name=test, got %v", cfg.Metadata["name"])
	}
	if cfg.Metadata["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", cfg.Metadata["port"])
	}
	if cfg.Metadata["rate"] != 3.14 {
		t.Fatalf("expected rate=3.14, got %v", cfg.Metadata["rate"])
	}
	if cfg.Metadata["debug"] != true {
		t.Fatalf("expected debug=true, got %v", cfg.Metadata["debug"])
	}
}

// TestUnmarshalEmptyMap verifies empty map blocks produce non-nil empty maps.
func TestUnmarshalEmptyMap(t *testing.T) {
	type Config struct {
		Metadata map[string]string
	}

	input := `metadata {}`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Metadata == nil {
		t.Fatal("expected Metadata to be non-nil")
	}
	if len(cfg.Metadata) != 0 {
		t.Fatalf("expected Metadata to be empty, got %v", cfg.Metadata)
	}
}

// TestUnmarshalMapTypeMismatch verifies type mismatch errors in maps.
func TestUnmarshalMapTypeMismatch(t *testing.T) {
	type Config struct {
		Ports map[string]int64
	}

	input := `
ports {
  http: "not-a-number"
}
`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for type mismatch in map")
	}
}

// ============================================================================
// Nested Struct Tests (deep nesting)
// ============================================================================

// TestUnmarshalDeeplyNestedStructThreeLevels verifies 3+ levels of struct nesting.
func TestUnmarshalDeeplyNestedStructThreeLevels(t *testing.T) {
	type L3 struct {
		Value string
		Count int
	}
	type L2 struct {
		Inner L3
	}
	type L1 struct {
		Middle L2
	}
	type Config struct {
		Top L1
	}

	input := `
top {
  middle {
    inner {
      value = "deep-value"
      count = 42
    }
  }
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Top.Middle.Inner.Value != "deep-value" {
		t.Fatalf("expected Value=deep-value, got %q", cfg.Top.Middle.Inner.Value)
	}
	if cfg.Top.Middle.Inner.Count != 42 {
		t.Fatalf("expected Count=42, got %d", cfg.Top.Middle.Inner.Count)
	}
}

// TestUnmarshalFourLevelNesting verifies 4 levels of nesting.
func TestUnmarshalFourLevelNesting(t *testing.T) {
	type L4 struct {
		ID string
	}
	type L3 struct {
		Deep L4
	}
	type L2 struct {
		Inner L3
	}
	type L1 struct {
		Middle L2
	}
	type Config struct {
		Top L1
	}

	input := `
top {
  middle {
    inner {
      deep {
        id = "four-deep"
      }
    }
  }
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Top.Middle.Inner.Deep.ID != "four-deep" {
		t.Fatalf("expected ID=four-deep, got %q", cfg.Top.Middle.Inner.Deep.ID)
	}
}

// ============================================================================
// time.Duration Tests
// ============================================================================

// TestUnmarshalDurationFromString verifies duration parsing from Go duration strings.
func TestUnmarshalDurationFromString(t *testing.T) {
	type Config struct {
		Ping    time.Duration `conf:"ping_interval"`
		Timeout time.Duration
		Short   time.Duration
	}

	input := `
ping_interval = "2s"
timeout = "100ms"
short = "2h30m"
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Ping != 2*time.Second {
		t.Fatalf("expected Ping=2s, got %v", cfg.Ping)
	}
	if cfg.Timeout != 100*time.Millisecond {
		t.Fatalf("expected Timeout=100ms, got %v", cfg.Timeout)
	}
	if cfg.Short != 2*time.Hour+30*time.Minute {
		t.Fatalf("expected Short=2h30m, got %v", cfg.Short)
	}
}

// TestUnmarshalDurationFromInt64 verifies int64 values are treated as seconds.
func TestUnmarshalDurationFromInt64(t *testing.T) {
	type Config struct {
		Ping time.Duration `conf:"ping_interval"`
	}

	input := `ping_interval = 5`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Ping != 5*time.Second {
		t.Fatalf("expected Ping=5s, got %v", cfg.Ping)
	}
}

// TestUnmarshalDurationInvalid verifies invalid duration returns error.
func TestUnmarshalDurationInvalid(t *testing.T) {
	type Config struct {
		Ping time.Duration `conf:"ping_interval"`
	}

	input := `ping_interval = "xyz"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Fatalf("expected error about invalid duration, got: %v", err)
	}
}

// TestUnmarshalDurationUnsupportedType verifies error for non-string, non-int types.
func TestUnmarshalDurationUnsupportedType(t *testing.T) {
	type Config struct {
		Ping time.Duration `conf:"ping_interval"`
	}

	input := `ping_interval = true`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for bool into duration")
	}
}

// TestUnmarshalDurationPointer verifies *time.Duration pointer field.
func TestUnmarshalDurationPointer(t *testing.T) {
	type Config struct {
		Ping    *time.Duration `conf:"ping_interval"`
		Missing *time.Duration
	}

	input := `ping_interval = "3s"`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Ping == nil || *cfg.Ping != 3*time.Second {
		t.Fatalf("expected Ping=3s, got %v", cfg.Ping)
	}
	if cfg.Missing != nil {
		t.Fatal("expected Missing to be nil")
	}
}

// ============================================================================
// time.Time Tests
// ============================================================================

// TestUnmarshalTimeFromDatetime verifies time.Time from ISO8601 datetime.
func TestUnmarshalTimeFromDatetime(t *testing.T) {
	type Config struct {
		Created time.Time
	}

	input := `created = 2023-11-01T00:00:00Z`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC)
	if !cfg.Created.Equal(expected) {
		t.Fatalf("expected Created=%v, got %v", expected, cfg.Created)
	}
}

// TestUnmarshalTimeFromString verifies time.Time from ISO8601 string.
func TestUnmarshalTimeFromString(t *testing.T) {
	type Config struct {
		Created time.Time
	}

	input := `created = "2024-06-15T10:30:00Z"`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	if !cfg.Created.Equal(expected) {
		t.Fatalf("expected Created=%v, got %v", expected, cfg.Created)
	}
}

// TestUnmarshalTimeInvalid verifies invalid datetime returns error.
func TestUnmarshalTimeInvalid(t *testing.T) {
	type Config struct {
		Created time.Time
	}

	input := `created = "not-a-date"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for invalid datetime")
	}
	if !strings.Contains(err.Error(), "invalid datetime") {
		t.Fatalf("expected error about invalid datetime, got: %v", err)
	}
}

// TestUnmarshalTimeUnsupportedType verifies error for unsupported types into time.Time.
func TestUnmarshalTimeUnsupportedType(t *testing.T) {
	type Config struct {
		Created time.Time
	}

	input := `created = 12345`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for int into time.Time")
	}
}

// TestUnmarshalTimePointer verifies *time.Time pointer field.
func TestUnmarshalTimePointer(t *testing.T) {
	type Config struct {
		Created *time.Time
		Missing *time.Time
	}

	input := `created = 2023-11-01T00:00:00Z`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Created == nil {
		t.Fatal("expected Created to be non-nil")
	}
	expected := time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC)
	if !cfg.Created.Equal(expected) {
		t.Fatalf("expected Created=%v, got %v", expected, *cfg.Created)
	}
	if cfg.Missing != nil {
		t.Fatal("expected Missing to be nil")
	}
}

// ============================================================================
// Size Suffix Tests (parser already expands to int64)
// ============================================================================

// TestUnmarshalSizeSuffix verifies size suffixes are expanded to int64 by parser.
func TestUnmarshalSizeSuffix(t *testing.T) {
	type Config struct {
		MaxPayload int64 `conf:"max_payload"`
		Buffer     int64
	}

	input := `
max_payload = 1MB
buffer = 64KB
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1MB = 1024 * 1024 = 1048576
	if cfg.MaxPayload != 1048576 {
		t.Fatalf("expected MaxPayload=1048576, got %d", cfg.MaxPayload)
	}
	// 64KB = 64 * 1024 = 65536
	if cfg.Buffer != 65536 {
		t.Fatalf("expected Buffer=65536, got %d", cfg.Buffer)
	}
}

// ============================================================================
// Custom Unmarshaler Interface Tests
// ============================================================================

// customType implements Unmarshaler for testing.
type customType struct {
	raw  any
	data string
}

func (c *customType) UnmarshalConfig(v any) error {
	c.raw = v
	switch val := v.(type) {
	case string:
		c.data = strings.ToUpper(val)
	case int64:
		c.data = "INT"
	default:
		c.data = "OTHER"
	}
	return nil
}

// TestUnmarshalCustomUnmarshaler verifies custom Unmarshaler interface is called.
func TestUnmarshalCustomUnmarshaler(t *testing.T) {
	type Config struct {
		Custom customType
	}

	input := `custom = "hello"`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Custom.data != "HELLO" {
		t.Fatalf("expected custom.data=HELLO, got %q", cfg.Custom.data)
	}
	if cfg.Custom.raw != "hello" {
		t.Fatalf("expected custom.raw=hello, got %v", cfg.Custom.raw)
	}
}

// TestUnmarshalCustomUnmarshalerWithInt verifies Unmarshaler receives int64.
func TestUnmarshalCustomUnmarshalerWithInt(t *testing.T) {
	type Config struct {
		Custom customType
	}

	input := `custom = 42`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Custom.data != "INT" {
		t.Fatalf("expected custom.data=INT, got %q", cfg.Custom.data)
	}
}

// errorUnmarshaler always returns an error.
type errorUnmarshaler struct{}

func (e *errorUnmarshaler) UnmarshalConfig(v any) error {
	return errors.New("custom unmarshal error")
}

// TestUnmarshalCustomUnmarshalerError verifies errors from Unmarshaler propagate.
func TestUnmarshalCustomUnmarshalerError(t *testing.T) {
	type Config struct {
		Custom errorUnmarshaler
	}

	input := `custom = "test"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error from custom Unmarshaler")
	}
	if !strings.Contains(err.Error(), "custom unmarshal error") {
		t.Fatalf("expected custom unmarshal error, got: %v", err)
	}
}

// TestUnmarshalCustomUnmarshalerPointer verifies pointer receiver Unmarshaler.
func TestUnmarshalCustomUnmarshalerPointer(t *testing.T) {
	type Config struct {
		Custom *customType
	}

	input := `custom = "world"`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Custom == nil {
		t.Fatal("expected Custom to be non-nil")
	}
	if cfg.Custom.data != "WORLD" {
		t.Fatalf("expected custom.data=WORLD, got %q", cfg.Custom.data)
	}
}

// TestUnmarshalCustomUnmarshalerWithMap verifies Unmarshaler receives map value.
func TestUnmarshalCustomUnmarshalerWithMap(t *testing.T) {
	type Config struct {
		Custom customType
	}

	input := `custom { key = "value" }`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The raw value should be a map[string]any
	if _, ok := cfg.Custom.raw.(map[string]any); !ok {
		t.Fatalf("expected raw to be map, got %T", cfg.Custom.raw)
	}
}

// ============================================================================
// Strict vs Permissive Mode Tests
// ============================================================================

// TestUnmarshalPermissiveMode verifies unknown keys are ignored by default.
func TestUnmarshalPermissiveMode(t *testing.T) {
	type Config struct {
		Name string
	}

	input := `
name = "test"
unknown_key = "ignored"
extra_field = 42
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error in permissive mode: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
}

// TestUnmarshalStrictModeRejectsUnknown verifies strict mode errors on unknown keys.
func TestUnmarshalStrictModeRejectsUnknown(t *testing.T) {
	type Config struct {
		Name string
	}

	input := `
name = "test"
unknown_key = "rejected"
`
	var cfg Config
	err := UnmarshalWith([]byte(input), &cfg, &UnmarshalOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error in strict mode for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "unknown_key") {
		t.Fatalf("expected error to mention the key name, got: %v", err)
	}
}

// TestUnmarshalStrictModeAllowsKnown verifies strict mode works with valid keys.
func TestUnmarshalStrictModeAllowsKnown(t *testing.T) {
	type Config struct {
		Name string
		Port int
	}

	input := `
name = "test"
port = 4222
`
	var cfg Config
	if err := UnmarshalWith([]byte(input), &cfg, &UnmarshalOptions{Strict: true}); err != nil {
		t.Fatalf("unexpected error in strict mode with known keys: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
}

// TestUnmarshalStrictModeNilOptions verifies nil options uses permissive mode.
func TestUnmarshalStrictModeNilOptions(t *testing.T) {
	type Config struct {
		Name string
	}

	input := `
name = "test"
unknown_key = "ignored"
`
	var cfg Config
	if err := UnmarshalWith([]byte(input), &cfg, nil); err != nil {
		t.Fatalf("unexpected error with nil options: %v", err)
	}
}

// ============================================================================
// UnmarshalFile Tests
// ============================================================================

// TestUnmarshalFileValid verifies UnmarshalFile with a valid config file.
func TestUnmarshalFileValid(t *testing.T) {
	type Config struct {
		Name string
		Port int
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(fp, []byte("name = test\nport = 4222\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := UnmarshalFile(fp, &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
}

// TestUnmarshalFileMissing verifies UnmarshalFile with missing file returns PathError.
func TestUnmarshalFileMissing(t *testing.T) {
	type Config struct {
		Name string
	}

	var cfg Config
	err := UnmarshalFile("/nonexistent/path/config.conf", &cfg)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *os.PathError, got %T: %v", err, err)
	}
}

// TestUnmarshalFileInvalidSyntax verifies UnmarshalFile with invalid syntax.
func TestUnmarshalFileInvalidSyntax(t *testing.T) {
	type Config struct {
		Name string
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "invalid.conf")
	if err := os.WriteFile(fp, []byte("name {\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	err := UnmarshalFile(fp, &cfg)
	if err == nil {
		t.Fatal("expected error for invalid syntax")
	}
}

// TestUnmarshalFileWithIncludes verifies includes resolved relative to file path.
func TestUnmarshalFileWithIncludes(t *testing.T) {
	type Config struct {
		Name string
		Port int
	}

	dir := t.TempDir()

	// Create included file.
	includedFp := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includedFp, []byte("port = 6222\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create main file that includes the other.
	mainFp := filepath.Join(dir, "main.conf")
	mainContent := fmt.Sprintf("name = test\ninclude '%s'\n", includedFp)
	if err := os.WriteFile(mainFp, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := UnmarshalFile(mainFp, &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "test" {
		t.Fatalf("expected Name=test, got %q", cfg.Name)
	}
	if cfg.Port != 6222 {
		t.Fatalf("expected Port=6222 (from include), got %d", cfg.Port)
	}
}

// TestUnmarshalFileStrict verifies UnmarshalFileWith with strict mode.
func TestUnmarshalFileStrict(t *testing.T) {
	type Config struct {
		Name string
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(fp, []byte("name = test\nextra = bad\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	err := UnmarshalFileWith(fp, &cfg, &UnmarshalOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error for unknown key in strict mode")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got: %v", err)
	}
}

// TestUnmarshalFileNonPointer verifies error for non-pointer target.
func TestUnmarshalFileNonPointer(t *testing.T) {
	type Config struct {
		Name string
	}

	err := UnmarshalFile("/some/path", Config{})
	if err == nil {
		t.Fatal("expected error for non-pointer target")
	}
}

// ============================================================================
// Mixed / Integration Tests
// ============================================================================

// TestUnmarshalComplexConfig verifies a realistic complex configuration.
func TestUnmarshalComplexConfig(t *testing.T) {
	type TLS struct {
		CertFile string `conf:"cert_file"`
		KeyFile  string `conf:"key_file"`
	}
	type Cluster struct {
		Name   string
		Port   int
		Routes []string
		TLS    TLS
	}
	type Config struct {
		Port          int
		ServerName    string        `conf:"server_name"`
		MaxPayload    int64         `conf:"max_payload"`
		PingInterval  time.Duration `conf:"ping_interval"`
		WriteDeadline time.Duration `conf:"write_deadline"`
		Debug         bool
		Cluster       Cluster
		Tags          map[string]string
	}

	input := `
port = 4222
server_name = "my-server"
max_payload = 1MB
ping_interval = "2s"
write_deadline = 10
debug = true

cluster {
  name = "my-cluster"
  port = 6222
  routes = [
    "nats://host1:6222"
    "nats://host2:6222"
  ]
  tls {
    cert_file = "/path/to/cert.pem"
    key_file = "/path/to/key.pem"
  }
}

tags {
  region: "us-east"
  env: "production"
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.ServerName != "my-server" {
		t.Fatalf("expected ServerName=my-server, got %q", cfg.ServerName)
	}
	if cfg.MaxPayload != 1048576 {
		t.Fatalf("expected MaxPayload=1048576, got %d", cfg.MaxPayload)
	}
	if cfg.PingInterval != 2*time.Second {
		t.Fatalf("expected PingInterval=2s, got %v", cfg.PingInterval)
	}
	if cfg.WriteDeadline != 10*time.Second {
		t.Fatalf("expected WriteDeadline=10s, got %v", cfg.WriteDeadline)
	}
	if !cfg.Debug {
		t.Fatal("expected Debug=true")
	}

	// Cluster
	if cfg.Cluster.Name != "my-cluster" {
		t.Fatalf("expected Cluster.Name=my-cluster, got %q", cfg.Cluster.Name)
	}
	if cfg.Cluster.Port != 6222 {
		t.Fatalf("expected Cluster.Port=6222, got %d", cfg.Cluster.Port)
	}
	if len(cfg.Cluster.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(cfg.Cluster.Routes))
	}
	if cfg.Cluster.Routes[0] != "nats://host1:6222" {
		t.Fatalf("expected route[0]=nats://host1:6222, got %q", cfg.Cluster.Routes[0])
	}
	if cfg.Cluster.TLS.CertFile != "/path/to/cert.pem" {
		t.Fatalf("expected CertFile=/path/to/cert.pem, got %q", cfg.Cluster.TLS.CertFile)
	}

	// Tags map
	if cfg.Tags["region"] != "us-east" {
		t.Fatalf("expected region=us-east, got %q", cfg.Tags["region"])
	}
	if cfg.Tags["env"] != "production" {
		t.Fatalf("expected env=production, got %q", cfg.Tags["env"])
	}
}

// TestUnmarshalSliceWithStructTags verifies slice fields with struct tags.
func TestUnmarshalSliceWithStructTags(t *testing.T) {
	type Config struct {
		Hosts []string `conf:"cluster_routes"`
	}

	input := `cluster_routes = ["host1", "host2"]`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(cfg.Hosts))
	}
}

// TestUnmarshalMapInNestedStruct verifies map fields inside nested structs.
func TestUnmarshalMapInNestedStruct(t *testing.T) {
	type Inner struct {
		Tags map[string]string
	}
	type Config struct {
		Inner Inner
	}

	input := `
inner {
  tags {
    key1: "value1"
    key2: "value2"
  }
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Inner.Tags["key1"] != "value1" {
		t.Fatalf("expected key1=value1, got %q", cfg.Inner.Tags["key1"])
	}
}

// TestUnmarshalSliceOfAny verifies []any (interface slice).
func TestUnmarshalSliceOfAny(t *testing.T) {
	type Config struct {
		Mixed []any
	}

	input := `mixed = ["hello", 42, true, 3.14]`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Mixed) != 4 {
		t.Fatalf("expected 4 elements, got %d", len(cfg.Mixed))
	}
	if cfg.Mixed[0] != "hello" {
		t.Fatalf("expected [0]=hello, got %v", cfg.Mixed[0])
	}
	if cfg.Mixed[1] != int64(42) {
		t.Fatalf("expected [1]=42, got %v", cfg.Mixed[1])
	}
	if cfg.Mixed[2] != true {
		t.Fatalf("expected [2]=true, got %v", cfg.Mixed[2])
	}
	if cfg.Mixed[3] != 3.14 {
		t.Fatalf("expected [3]=3.14, got %v", cfg.Mixed[3])
	}
}

// TestUnmarshalDurationZero verifies 0 duration value.
func TestUnmarshalDurationZero(t *testing.T) {
	type Config struct {
		Timeout time.Duration
	}

	input := `timeout = 0`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 0 {
		t.Fatalf("expected Timeout=0, got %v", cfg.Timeout)
	}
}

// TestUnmarshalSliceFloat64FromInt verifies int64 elements coerce to float64 in slices.
func TestUnmarshalSliceFloat64FromInt(t *testing.T) {
	type Config struct {
		Rates []float64
	}

	input := "rates = [\n  1\n  2\n  3\n]\n"
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []float64{1.0, 2.0, 3.0}
	if !reflect.DeepEqual(cfg.Rates, expected) {
		t.Fatalf("expected Rates=%v, got %v", expected, cfg.Rates)
	}
}

// TestUnmarshalMapFloat64FromInt verifies int64 values coerce to float64 in maps.
func TestUnmarshalMapFloat64FromInt(t *testing.T) {
	type Config struct {
		Rates map[string]float64
	}

	input := `
rates {
  read: 5
  write: 10
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rates["read"] != 5.0 {
		t.Fatalf("expected read=5.0, got %f", cfg.Rates["read"])
	}
	if cfg.Rates["write"] != 10.0 {
		t.Fatalf("expected write=10.0, got %f", cfg.Rates["write"])
	}
}

// TestUnmarshalNestedStructWithSliceAndMap combines nested structs, slices, and maps.
func TestUnmarshalNestedStructWithSliceAndMap(t *testing.T) {
	type Endpoint struct {
		URL  string
		Port int
	}
	type Config struct {
		Name      string
		Endpoints []Endpoint
		Labels    map[string]string
	}

	input := `
name = "service"
endpoints = [
  {url: "http://host1", port: 8080}
  {url: "http://host2", port: 9090}
]
labels {
  app: "myapp"
  version: "1.0"
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "service" {
		t.Fatalf("expected Name=service, got %q", cfg.Name)
	}
	if len(cfg.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(cfg.Endpoints))
	}
	if cfg.Endpoints[0].URL != "http://host1" || cfg.Endpoints[0].Port != 8080 {
		t.Fatalf("unexpected endpoint[0]: %+v", cfg.Endpoints[0])
	}
	if cfg.Endpoints[1].URL != "http://host2" || cfg.Endpoints[1].Port != 9090 {
		t.Fatalf("unexpected endpoint[1]: %+v", cfg.Endpoints[1])
	}
	if cfg.Labels["app"] != "myapp" {
		t.Fatalf("expected app=myapp, got %q", cfg.Labels["app"])
	}
}

// TestUnmarshalScalarToSliceWithCoercionInNestedStruct tests coercion in nested context.
func TestUnmarshalScalarToSliceWithCoercionInNestedStruct(t *testing.T) {
	type Cluster struct {
		Routes []string
	}
	type Config struct {
		Cluster Cluster
	}

	input := `
cluster {
  routes = "nats://single-host:4222"
}
`
	var cfg Config
	if err := Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Cluster.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Cluster.Routes))
	}
	if cfg.Cluster.Routes[0] != "nats://single-host:4222" {
		t.Fatalf("expected route=nats://single-host:4222, got %q", cfg.Cluster.Routes[0])
	}
}

// TestUnmarshalInterfaceField verifies any/interface{} field assignment.
func TestUnmarshalInterfaceField(t *testing.T) {
	type Config struct {
		Value any
	}

	tests := []struct {
		name   string
		input  string
		expect any
	}{
		{"string", `value = "hello"`, "hello"},
		{"int", `value = 42`, int64(42)},
		{"bool", `value = true`, true},
		{"float", `value = 3.14`, 3.14},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := Unmarshal([]byte(tt.input), &cfg); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(cfg.Value, tt.expect) {
				t.Fatalf("expected Value=%v (%T), got %v (%T)", tt.expect, tt.expect, cfg.Value, cfg.Value)
			}
		})
	}
}

// TestUnmarshalMapNonStringScalarValue verifies map with non-scalar errors properly.
func TestUnmarshalMapNonStringKey(t *testing.T) {
	type Config struct {
		Data map[int]string
	}

	input := `data { key = "value" }`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for non-string map key type")
	}
	if !strings.Contains(err.Error(), "only string keys") {
		t.Fatalf("expected error about string keys, got: %v", err)
	}
}

// TestUnmarshalWithRelativeInclude verifies UnmarshalFile resolves includes.
func TestUnmarshalFileWithRelativeInclude(t *testing.T) {
	type Config struct {
		Name string
		Port int
	}

	dir := t.TempDir()
	subdir := filepath.Join(dir, "includes")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create included file in subdirectory.
	includedFp := filepath.Join(subdir, "ports.conf")
	if err := os.WriteFile(includedFp, []byte("port = 9222\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Main file includes the subdirectory file using relative path.
	mainFp := filepath.Join(dir, "main.conf")
	mainContent := "name = relative-test\ninclude './includes/ports.conf'\n"
	if err := os.WriteFile(mainFp, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := UnmarshalFile(mainFp, &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "relative-test" {
		t.Fatalf("expected Name=relative-test, got %q", cfg.Name)
	}
	if cfg.Port != 9222 {
		t.Fatalf("expected Port=9222 (from include), got %d", cfg.Port)
	}
}

// TestUnmarshalMapStringValueTypeMismatch verifies error when map value can't be coerced.
func TestUnmarshalMapStringValueTypeMismatch(t *testing.T) {
	type Config struct {
		Labels map[string]string
	}

	input := `
labels {
  count: 42
}
`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for int64 into string map value")
	}
}

// TestUnmarshalMapNonMapValue verifies error when non-map value assigned to map field.
func TestUnmarshalMapNonMapValue(t *testing.T) {
	type Config struct {
		Labels map[string]string
	}

	input := `labels = "not-a-map"`
	var cfg Config
	err := Unmarshal([]byte(input), &cfg)
	if err == nil {
		t.Fatal("expected error for string into map field")
	}
}
