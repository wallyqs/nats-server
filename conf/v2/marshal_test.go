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
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestMarshalBasicString tests marshaling of basic string fields.
func TestMarshalBasicString(t *testing.T) {
	type Config struct {
		Name string `conf:"name"`
	}
	cfg := Config{Name: "nats-server"}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Round-trip: parse and verify.
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["name"] != "nats-server" {
		t.Fatalf("expected name=nats-server, got %v", m["name"])
	}
}

// TestMarshalStringWithSpaces tests that strings with spaces are quoted.
func TestMarshalStringWithSpaces(t *testing.T) {
	type Config struct {
		Desc string `conf:"desc"`
	}
	cfg := Config{Desc: "hello world"}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Verify quoted in output.
	out := string(data)
	if !strings.Contains(out, `"hello world"`) {
		t.Fatalf("expected quoted string, got: %s", out)
	}

	// Round-trip.
	m, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["desc"] != "hello world" {
		t.Fatalf("expected desc='hello world', got %v", m["desc"])
	}
}

// TestMarshalStringSpecialChars tests strings with special characters.
func TestMarshalStringSpecialChars(t *testing.T) {
	type Config struct {
		Val string `conf:"val"`
	}
	specials := []string{
		"has#hash", "has//slash", "has{brace", "has}close",
		"has[bracket", "has]close", "has=equals", "has:colon",
		"has\ttab", "has\nnewline",
	}
	for _, s := range specials {
		cfg := Config{Val: s}
		data, err := Marshal(cfg)
		if err != nil {
			t.Fatalf("Marshal error for %q: %v", s, err)
		}
		m, err := Parse(string(data))
		if err != nil {
			t.Fatalf("Parse error for %q: %v\nOutput: %s", s, err, string(data))
		}
		if m["val"] != s {
			t.Fatalf("round-trip failed for %q: got %v", s, m["val"])
		}
	}
}

// TestMarshalInteger tests marshaling of integer fields.
func TestMarshalInteger(t *testing.T) {
	type Config struct {
		Port int    `conf:"port"`
		Max  int64  `conf:"max"`
		Min  int32  `conf:"min"`
		Size uint16 `conf:"size"`
	}
	cfg := Config{Port: 4222, Max: 1048576, Min: -100, Size: 8080}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v (%T)", m["port"], m["port"])
	}
	if m["max"] != int64(1048576) {
		t.Fatalf("expected max=1048576, got %v", m["max"])
	}
	if m["min"] != int64(-100) {
		t.Fatalf("expected min=-100, got %v", m["min"])
	}
	if m["size"] != int64(8080) {
		t.Fatalf("expected size=8080, got %v", m["size"])
	}
}

// TestMarshalFloat tests marshaling of float fields.
func TestMarshalFloat(t *testing.T) {
	type Config struct {
		Rate    float64 `conf:"rate"`
		Ratio   float32 `conf:"ratio"`
		Precise float64 `conf:"precise"`
	}
	cfg := Config{Rate: 3.14, Ratio: 2.5, Precise: 0.001}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["rate"] != float64(3.14) {
		t.Fatalf("expected rate=3.14, got %v", m["rate"])
	}
}

// TestMarshalBool tests marshaling of boolean fields.
func TestMarshalBool(t *testing.T) {
	type Config struct {
		Debug bool `conf:"debug"`
		Trace bool `conf:"trace"`
	}
	cfg := Config{Debug: true, Trace: false}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Verify output uses true/false literals.
	out := string(data)
	if !strings.Contains(out, "true") {
		t.Fatalf("expected 'true' in output, got: %s", out)
	}

	m, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["debug"] != true {
		t.Fatalf("expected debug=true, got %v", m["debug"])
	}
	if m["trace"] != false {
		t.Fatalf("expected trace=false, got %v", m["trace"])
	}
}

// TestMarshalAmbiguousStrings tests that ambiguous string values are quoted.
func TestMarshalAmbiguousStrings(t *testing.T) {
	type Config struct {
		Val string `conf:"val"`
	}
	cases := []struct {
		input    string
		desc     string
		wantType string // "string" - must round-trip as string, not other type
	}{
		{"true", "bool true", "string"},
		{"false", "bool false", "string"},
		{"yes", "bool yes", "string"},
		{"no", "bool no", "string"},
		{"on", "bool on", "string"},
		{"off", "bool off", "string"},
		{"TRUE", "uppercase bool", "string"},
		{"False", "mixed case bool", "string"},
		{"123", "integer string", "string"},
		{"-456", "negative integer string", "string"},
		{"3.14", "float string", "string"},
		{"$FOO", "variable ref", "string"},
		{"$2a$11$hash", "bcrypt", "string"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Config{Val: tc.input}
			data, err := Marshal(cfg)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			m, err := Parse(string(data))
			if err != nil {
				t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
			}
			got, ok := m["val"].(string)
			if !ok {
				t.Fatalf("expected string type, got %T (%v)\nOutput: %s", m["val"], m["val"], string(data))
			}
			if got != tc.input {
				t.Fatalf("expected %q, got %q", tc.input, got)
			}
		})
	}
}

// TestMarshalSlice tests marshaling of slice/array fields.
func TestMarshalSlice(t *testing.T) {
	type Config struct {
		Routes []string `conf:"routes"`
		Ports  []int    `conf:"ports"`
	}
	cfg := Config{
		Routes: []string{"nats://host1:4222", "nats://host2:4222"},
		Ports:  []int{4222, 6222, 8222},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	routes, ok := m["routes"].([]any)
	if !ok {
		t.Fatalf("expected []any for routes, got %T", m["routes"])
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[0] != "nats://host1:4222" {
		t.Fatalf("expected first route nats://host1:4222, got %v", routes[0])
	}

	ports, ok := m["ports"].([]any)
	if !ok {
		t.Fatalf("expected []any for ports, got %T", m["ports"])
	}
	if len(ports) != 3 {
		t.Fatalf("expected 3 ports, got %d", len(ports))
	}
}

// TestMarshalMap tests marshaling of map fields.
func TestMarshalMap(t *testing.T) {
	type Config struct {
		Metadata map[string]string `conf:"metadata"`
	}
	cfg := Config{
		Metadata: map[string]string{
			"region": "us-east",
			"env":    "production",
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	md, ok := m["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for metadata, got %T", m["metadata"])
	}
	if md["region"] != "us-east" {
		t.Fatalf("expected region=us-east, got %v", md["region"])
	}
}

// TestMarshalNestedStruct tests marshaling of nested structs.
func TestMarshalNestedStruct(t *testing.T) {
	type ClusterOpts struct {
		Name string `conf:"name"`
		Port int    `conf:"port"`
	}
	type Config struct {
		Host    string      `conf:"host"`
		Cluster ClusterOpts `conf:"cluster"`
	}
	cfg := Config{
		Host: "0.0.0.0",
		Cluster: ClusterOpts{
			Name: "my-cluster",
			Port: 6222,
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	cluster, ok := m["cluster"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for cluster, got %T", m["cluster"])
	}
	if cluster["name"] != "my-cluster" {
		t.Fatalf("expected cluster name=my-cluster, got %v", cluster["name"])
	}
	if cluster["port"] != int64(6222) {
		t.Fatalf("expected cluster port=6222, got %v", cluster["port"])
	}
}

// TestMarshalDeeplyNestedStruct tests 3+ level nesting.
func TestMarshalDeeplyNestedStruct(t *testing.T) {
	type Inner struct {
		Val string `conf:"val"`
	}
	type Middle struct {
		Inner Inner `conf:"inner"`
	}
	type Outer struct {
		Middle Middle `conf:"middle"`
	}
	cfg := Outer{
		Middle: Middle{
			Inner: Inner{Val: "deep"},
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	middle, ok := m["middle"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for middle, got %T", m["middle"])
	}
	inner, ok := middle["inner"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for inner, got %T", middle["inner"])
	}
	if inner["val"] != "deep" {
		t.Fatalf("expected val=deep, got %v", inner["val"])
	}
}

// TestMarshalArrayOfMaps tests marshaling []map[string]any.
func TestMarshalArrayOfMaps(t *testing.T) {
	type Config struct {
		Items []map[string]string `conf:"items"`
	}
	cfg := Config{
		Items: []map[string]string{
			{"name": "alice", "role": "admin"},
			{"name": "bob", "role": "user"},
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("expected []any for items, got %T", m["items"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

// TestMarshalArrayOfStructs tests marshaling []Struct.
func TestMarshalArrayOfStructs(t *testing.T) {
	type User struct {
		Name string `conf:"name"`
		Age  int    `conf:"age"`
	}
	type Config struct {
		Users []User `conf:"users"`
	}
	cfg := Config{
		Users: []User{
			{Name: "alice", Age: 30},
			{Name: "bob", Age: 25},
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	users, ok := m["users"].([]any)
	if !ok {
		t.Fatalf("expected []any for users, got %T", m["users"])
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	u0, ok := users[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map for user[0], got %T", users[0])
	}
	if u0["name"] != "alice" {
		t.Fatalf("expected user[0].name=alice, got %v", u0["name"])
	}
}

// TestMarshalStructTagCustomName tests conf:"custom_name" tag.
func TestMarshalStructTagCustomName(t *testing.T) {
	type Config struct {
		Host string `conf:"listen_addr"`
		Port int    `conf:"listen_port"`
	}
	cfg := Config{Host: "0.0.0.0", Port: 4222}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "listen_addr") {
		t.Fatalf("expected listen_addr key in output, got: %s", out)
	}
	if !strings.Contains(out, "listen_port") {
		t.Fatalf("expected listen_port key in output, got: %s", out)
	}

	m, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["listen_addr"] != "0.0.0.0" {
		t.Fatalf("expected listen_addr=0.0.0.0, got %v", m["listen_addr"])
	}
}

// TestMarshalStructTagSkip tests conf:"-" skipping a field.
func TestMarshalStructTagSkip(t *testing.T) {
	type Config struct {
		Name   string `conf:"name"`
		Secret string `conf:"-"`
	}
	cfg := Config{Name: "test", Secret: "password123"}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if strings.Contains(out, "password123") {
		t.Fatalf("expected Secret to be skipped, but found in output: %s", out)
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("expected 'secret' key to be absent, got: %s", out)
	}
}

// TestMarshalOmitempty tests omitempty skips zero-value fields.
func TestMarshalOmitempty(t *testing.T) {
	type Config struct {
		Name    string   `conf:"name,omitempty"`
		Port    int      `conf:"port,omitempty"`
		Debug   bool     `conf:"debug,omitempty"`
		Rate    float64  `conf:"rate,omitempty"`
		Routes  []string `conf:"routes,omitempty"`
		Present string   `conf:"present,omitempty"`
	}
	cfg := Config{
		// All zero except Present.
		Present: "yes-here",
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	// Zero-value fields should be absent.
	if strings.Contains(out, "name") {
		t.Fatalf("expected name to be omitted, got: %s", out)
	}
	if strings.Contains(out, "port") {
		t.Fatalf("expected port to be omitted, got: %s", out)
	}
	if strings.Contains(out, "debug") {
		t.Fatalf("expected debug to be omitted, got: %s", out)
	}
	if strings.Contains(out, "rate") {
		t.Fatalf("expected rate to be omitted, got: %s", out)
	}
	if strings.Contains(out, "routes") {
		t.Fatalf("expected routes to be omitted, got: %s", out)
	}
	// Present should be present.
	if !strings.Contains(out, "present") {
		t.Fatalf("expected present key in output, got: %s", out)
	}
}

// TestMarshalOmitemptyNilPointerAndMap tests omitempty for nil pointers and maps.
func TestMarshalOmitemptyNilPointerAndMap(t *testing.T) {
	type Config struct {
		Ptr  *string           `conf:"ptr,omitempty"`
		Meta map[string]string `conf:"meta,omitempty"`
	}
	cfg := Config{} // both nil
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if strings.Contains(out, "ptr") {
		t.Fatalf("expected ptr to be omitted for nil pointer, got: %s", out)
	}
	if strings.Contains(out, "meta") {
		t.Fatalf("expected meta to be omitted for nil map, got: %s", out)
	}
}

// TestMarshalTimeDuration tests marshaling of time.Duration fields.
func TestMarshalTimeDuration(t *testing.T) {
	type Config struct {
		PingInterval time.Duration `conf:"ping_interval"`
		Timeout      time.Duration `conf:"timeout"`
	}
	cfg := Config{
		PingInterval: 5 * time.Second,
		Timeout:      2*time.Minute + 30*time.Second,
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	// Should contain duration strings like "5s" and "2m30s".
	if !strings.Contains(out, "5s") {
		t.Fatalf("expected '5s' in output, got: %s", out)
	}
	if !strings.Contains(out, "2m30s") {
		t.Fatalf("expected '2m30s' in output, got: %s", out)
	}

	// Round-trip via Unmarshal.
	var got Config
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if got.PingInterval != 5*time.Second {
		t.Fatalf("expected PingInterval=5s, got %v", got.PingInterval)
	}
	if got.Timeout != 2*time.Minute+30*time.Second {
		t.Fatalf("expected Timeout=2m30s, got %v", got.Timeout)
	}
}

// TestMarshalTimeTime tests marshaling of time.Time fields.
func TestMarshalTimeTime(t *testing.T) {
	type Config struct {
		Created time.Time `conf:"created"`
	}
	tm := time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC)
	cfg := Config{Created: tm}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "2023-11-01T00:00:00Z") {
		t.Fatalf("expected ISO8601 Zulu format, got: %s", out)
	}

	// Round-trip via Unmarshal.
	var got Config
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !got.Created.Equal(tm) {
		t.Fatalf("expected Created=%v, got %v", tm, got.Created)
	}
}

// customMarshalType implements the MarshalConfig interface.
type customMarshalType struct {
	Data string
}

func (c customMarshalType) MarshalConfig() ([]byte, error) {
	return []byte(c.Data), nil
}

// TestMarshalCustomMarshaler tests MarshalConfig() interface.
func TestMarshalCustomMarshaler(t *testing.T) {
	type Config struct {
		Custom customMarshalType `conf:"custom"`
	}
	cfg := Config{
		Custom: customMarshalType{Data: "custom-output"},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "custom-output") {
		t.Fatalf("expected custom-output in output, got: %s", out)
	}
}

// TestMarshalIndent tests MarshalIndent with controlled indentation.
func TestMarshalIndent(t *testing.T) {
	type Inner struct {
		Val string `conf:"val"`
	}
	type Config struct {
		Host  string `conf:"host"`
		Inner Inner  `conf:"inner"`
	}
	cfg := Config{
		Host:  "0.0.0.0",
		Inner: Inner{Val: "test"},
	}

	// 2-space indent.
	data, err := MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent error: %v", err)
	}
	out := string(data)
	// The nested val should be indented.
	if !strings.Contains(out, "  val") {
		t.Fatalf("expected indented 'val', got: %s", out)
	}

	// Verify it re-parses.
	m, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	inner, ok := m["inner"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for inner, got %T", m["inner"])
	}
	if inner["val"] != "test" {
		t.Fatalf("expected val=test, got %v", inner["val"])
	}
}

// TestMarshalIndentTabs tests MarshalIndent with tab indentation.
func TestMarshalIndentTabs(t *testing.T) {
	type Inner struct {
		Val string `conf:"val"`
	}
	type Config struct {
		Inner Inner `conf:"inner"`
	}
	cfg := Config{Inner: Inner{Val: "test"}}
	data, err := MarshalIndent(cfg, "", "\t")
	if err != nil {
		t.Fatalf("MarshalIndent error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "\tval") {
		t.Fatalf("expected tab-indented 'val', got: %s", out)
	}

	// Must re-parse.
	_, err = Parse(out)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
}

// TestMarshalIndentDeep tests 3-level nesting indentation.
func TestMarshalIndentDeep(t *testing.T) {
	type Deep struct {
		Val string `conf:"val"`
	}
	type Middle struct {
		Deep Deep `conf:"deep"`
	}
	type Config struct {
		Middle Middle `conf:"middle"`
	}
	cfg := Config{Middle: Middle{Deep: Deep{Val: "nested"}}}
	data, err := MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent error: %v", err)
	}
	out := string(data)
	// Deep val should have 4 spaces (2 levels of indent).
	if !strings.Contains(out, "    val") {
		t.Fatalf("expected 4-space indented val, got: %s", out)
	}
	// Verify round-trip.
	m, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	middle := m["middle"].(map[string]any)
	deep := middle["deep"].(map[string]any)
	if deep["val"] != "nested" {
		t.Fatalf("expected val=nested, got %v", deep["val"])
	}
}

// TestMarshalKeySeparatorConsistency tests all keys use ": " separator.
func TestMarshalKeySeparatorConsistency(t *testing.T) {
	type Config struct {
		Name  string `conf:"name"`
		Port  int    `conf:"port"`
		Debug bool   `conf:"debug"`
	}
	cfg := Config{Name: "test", Port: 4222, Debug: true}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "}") {
			continue
		}
		if !strings.Contains(line, ": ") {
			t.Fatalf("expected ': ' separator in line: %q", line)
		}
	}
}

// TestMarshalUnsupportedTypeChan tests error for chan type.
func TestMarshalUnsupportedTypeChan(t *testing.T) {
	type Config struct {
		Ch chan int `conf:"ch"`
	}
	cfg := Config{Ch: make(chan int)}
	_, err := Marshal(cfg)
	if err == nil {
		t.Fatal("expected error for chan type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected 'unsupported' in error, got: %v", err)
	}
}

// TestMarshalUnsupportedTypeFunc tests error for func type.
func TestMarshalUnsupportedTypeFunc(t *testing.T) {
	type Config struct {
		Fn func() `conf:"fn"`
	}
	cfg := Config{Fn: func() {}}
	_, err := Marshal(cfg)
	if err == nil {
		t.Fatal("expected error for func type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected 'unsupported' in error, got: %v", err)
	}
}

// TestMarshalUnsupportedTypeComplex tests error for complex type.
func TestMarshalUnsupportedTypeComplex(t *testing.T) {
	type Config struct {
		C complex128 `conf:"c"`
	}
	cfg := Config{C: 1 + 2i}
	_, err := Marshal(cfg)
	if err == nil {
		t.Fatal("expected error for complex type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected 'unsupported' in error, got: %v", err)
	}
}

// TestMarshalNilInput tests that nil input returns an error.
func TestMarshalNilInput(t *testing.T) {
	_, err := Marshal(nil)
	if err == nil {
		t.Fatal("expected error for nil input, got nil")
	}
}

// TestMarshalNilPointer tests that nil pointer returns an error.
func TestMarshalNilPointer(t *testing.T) {
	type Config struct {
		Name string `conf:"name"`
	}
	var cfg *Config
	_, err := Marshal(cfg)
	if err == nil {
		t.Fatal("expected error for nil pointer, got nil")
	}
}

// TestMarshalEmptyStruct tests that empty struct returns valid empty config.
func TestMarshalEmptyStruct(t *testing.T) {
	type Config struct{}
	cfg := Config{}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	// Empty config should be valid (parse should succeed even if empty).
	out := strings.TrimSpace(string(data))
	if out != "" {
		// If non-empty, it should still parse.
		_, err = Parse(string(data))
		if err != nil {
			t.Fatalf("Parse error on empty struct output: %v", err)
		}
	}
}

// TestMarshalPointerFields tests pointer field handling.
func TestMarshalPointerFields(t *testing.T) {
	type Config struct {
		Name *string `conf:"name"`
		Port *int    `conf:"port"`
		Skip *string `conf:"skip"` // nil pointer should be skipped
	}
	name := "test"
	port := 4222
	cfg := Config{Name: &name, Port: &port} // Skip is nil
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)

	// Nil pointer field should be absent.
	m, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["name"] != "test" {
		t.Fatalf("expected name=test, got %v", m["name"])
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m["port"])
	}
	if _, ok := m["skip"]; ok {
		t.Fatalf("expected skip to be absent (nil pointer), but found in output")
	}
}

// TestMarshalUnexportedFields tests that unexported fields are skipped.
func TestMarshalUnexportedFields(t *testing.T) {
	type Config struct {
		Name   string `conf:"name"`
		secret string //nolint:unused
	}
	cfg := Config{Name: "test"}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "name") {
		t.Fatalf("expected name in output, got: %s", out)
	}
}

// TestMarshalEmbeddedStruct tests embedded struct fields are promoted.
func TestMarshalEmbeddedStruct(t *testing.T) {
	type Base struct {
		Host string `conf:"host"`
	}
	type Config struct {
		Base
		Port int `conf:"port"`
	}
	cfg := Config{
		Base: Base{Host: "0.0.0.0"},
		Port: 4222,
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// Host should be at top level, not nested under "base".
	if m["host"] != "0.0.0.0" {
		t.Fatalf("expected host=0.0.0.0 at top level, got %v", m["host"])
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m["port"])
	}
}

// TestMarshalMapStringAny tests marshaling map[string]any.
func TestMarshalMapStringAny(t *testing.T) {
	type Config struct {
		Extra map[string]any `conf:"extra"`
	}
	cfg := Config{
		Extra: map[string]any{
			"key1": "value1",
			"key2": int64(42),
			"key3": true,
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	extra, ok := m["extra"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for extra, got %T", m["extra"])
	}
	if extra["key1"] != "value1" {
		t.Fatalf("expected key1=value1, got %v", extra["key1"])
	}
}

// TestMarshalRoundTrip tests full Unmarshal->Marshal->Unmarshal round-trip.
func TestMarshalRoundTrip(t *testing.T) {
	type ClusterOpts struct {
		Name   string   `conf:"name"`
		Port   int      `conf:"port"`
		Routes []string `conf:"routes"`
	}
	type Config struct {
		Host    string        `conf:"host"`
		Port    int           `conf:"port"`
		Debug   bool          `conf:"debug"`
		Cluster ClusterOpts   `conf:"cluster"`
		Timeout time.Duration `conf:"timeout"`
	}

	original := Config{
		Host:  "0.0.0.0",
		Port:  4222,
		Debug: true,
		Cluster: ClusterOpts{
			Name:   "test-cluster",
			Port:   6222,
			Routes: []string{"nats://host1:6222", "nats://host2:6222"},
		},
		Timeout: 30 * time.Second,
	}

	// Marshal.
	data, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Unmarshal back.
	var got Config
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v\nOutput: %s", err, string(data))
	}

	// Compare.
	if !reflect.DeepEqual(original, got) {
		t.Fatalf("round-trip mismatch:\noriginal: %+v\ngot:      %+v\noutput: %s", original, got, string(data))
	}
}

// TestMarshalNonStructInput tests that non-struct input returns error.
func TestMarshalNonStructInput(t *testing.T) {
	_, err := Marshal("not a struct")
	if err == nil {
		t.Fatal("expected error for non-struct input, got nil")
	}

	_, err = Marshal(42)
	if err == nil {
		t.Fatal("expected error for non-struct input, got nil")
	}
}

// TestMarshalIndentPrefix tests MarshalIndent with prefix.
func TestMarshalIndentPrefix(t *testing.T) {
	type Config struct {
		Name string `conf:"name"`
	}
	cfg := Config{Name: "test"}
	data, err := MarshalIndent(cfg, "// ", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent error: %v", err)
	}
	out := string(data)
	// Each line should start with the prefix.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "// ") {
			t.Fatalf("expected prefix '// ' on line: %q", line)
		}
	}
}

// TestMarshalEmptyStringNotOmitted tests that empty string without
// omitempty is NOT omitted.
func TestMarshalEmptyStringNotOmitted(t *testing.T) {
	type Config struct {
		Name string `conf:"name"`
	}
	cfg := Config{Name: ""}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "name") {
		t.Fatalf("expected name key in output (no omitempty), got: %s", out)
	}
}

// TestMarshalCustomMarshalerPointer tests MarshalConfig on pointer receiver.
type customMarshalPtrType struct {
	Data string
}

func (c *customMarshalPtrType) MarshalConfig() ([]byte, error) {
	return []byte("ptr-custom:" + c.Data), nil
}

func TestMarshalCustomMarshalerPointer(t *testing.T) {
	type Config struct {
		Custom *customMarshalPtrType `conf:"custom"`
	}
	cfg := Config{
		Custom: &customMarshalPtrType{Data: "hello"},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "ptr-custom:hello") {
		t.Fatalf("expected custom marshaler output, got: %s", out)
	}
}

// TestMarshalEmptySliceNotOmitted tests empty (non-nil) slice without omitempty.
func TestMarshalEmptySliceNotOmitted(t *testing.T) {
	type Config struct {
		Items []string `conf:"items"`
	}
	cfg := Config{Items: []string{}}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "items") {
		t.Fatalf("expected items key in output (non-nil empty slice), got: %s", out)
	}
}

// TestMarshalInt64Types tests various integer types round-trip.
func TestMarshalInt64Types(t *testing.T) {
	type Config struct {
		A int8   `conf:"a"`
		B int16  `conf:"b"`
		C int32  `conf:"c"`
		D int64  `conf:"d"`
		E uint8  `conf:"e"`
		F uint16 `conf:"f"`
		G uint32 `conf:"g"`
		H uint64 `conf:"h"`
	}
	cfg := Config{A: 1, B: 2, C: 3, D: 4, E: 5, F: 6, G: 7, H: 8}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var got Config
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !reflect.DeepEqual(cfg, got) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", cfg, got)
	}
}

// TestMarshalNegativeFloat tests negative float marshaling.
func TestMarshalNegativeFloat(t *testing.T) {
	type Config struct {
		Val float64 `conf:"val"`
	}
	cfg := Config{Val: -22.5}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if m["val"] != float64(-22.5) {
		t.Fatalf("expected val=-22.5, got %v", m["val"])
	}
}

// TestMarshalDurationEdgeCases tests duration edge cases.
func TestMarshalDurationEdgeCases(t *testing.T) {
	type Config struct {
		Short time.Duration `conf:"short"`
		Long  time.Duration `conf:"long"`
	}
	cfg := Config{
		Short: 100 * time.Millisecond,
		Long:  24 * time.Hour,
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var got Config
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if got.Short != cfg.Short {
		t.Fatalf("expected Short=%v, got %v", cfg.Short, got.Short)
	}
	if got.Long != cfg.Long {
		t.Fatalf("expected Long=%v, got %v", cfg.Long, got.Long)
	}
}

// TestMarshalPointerToStruct tests pointer to nested struct.
func TestMarshalPointerToStruct(t *testing.T) {
	type Inner struct {
		Val string `conf:"val"`
	}
	type Config struct {
		Inner *Inner `conf:"inner"`
	}
	cfg := Config{Inner: &Inner{Val: "test"}}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	inner, ok := m["inner"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for inner, got %T", m["inner"])
	}
	if inner["val"] != "test" {
		t.Fatalf("expected val=test, got %v", inner["val"])
	}
}

// TestMarshalMapAnyValues tests map[string]any with mixed value types.
func TestMarshalMapAnyValues(t *testing.T) {
	type Config struct {
		Data map[string]any `conf:"data"`
	}
	cfg := Config{
		Data: map[string]any{
			"str":    "hello",
			"num":    int64(42),
			"flag":   true,
			"nested": map[string]any{"inner": "val"},
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	dm, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for data, got %T", m["data"])
	}
	if dm["str"] != "hello" {
		t.Fatalf("expected str=hello, got %v", dm["str"])
	}
	if dm["num"] != int64(42) {
		t.Fatalf("expected num=42, got %v (%T)", dm["num"], dm["num"])
	}
	if dm["flag"] != true {
		t.Fatalf("expected flag=true, got %v", dm["flag"])
	}
}

// TestMarshalStringDoubleQuoteEscape tests strings containing double quotes.
func TestMarshalStringDoubleQuoteEscape(t *testing.T) {
	type Config struct {
		Val string `conf:"val"`
	}
	cfg := Config{Val: `has "quotes" inside`}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	if m["val"] != `has "quotes" inside` {
		t.Fatalf("expected string with quotes, got %v", m["val"])
	}
}

// TestMarshalEmptyMap tests marshaling an empty map.
func TestMarshalEmptyMap(t *testing.T) {
	type Config struct {
		Data map[string]string `conf:"data"`
	}
	cfg := Config{Data: map[string]string{}}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	m, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse error: %v\nOutput: %s", err, string(data))
	}
	dm, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for data, got %T", m["data"])
	}
	if len(dm) != 0 {
		t.Fatalf("expected empty map, got %v", dm)
	}
}

// TestMarshalDurationSlice tests that []time.Duration fields marshal
// duration elements as quoted Go duration strings (e.g. "5s") instead
// of raw nanosecond integers.
func TestMarshalDurationSlice(t *testing.T) {
	type Config struct {
		Timeouts []time.Duration `conf:"timeouts"`
	}
	cfg := Config{
		Timeouts: []time.Duration{
			5 * time.Second,
			100 * time.Millisecond,
			2*time.Minute + 30*time.Second,
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	out := string(data)

	// Verify human-readable duration strings appear, not nanosecond integers.
	if !strings.Contains(out, "5s") {
		t.Fatalf("expected '5s' in output, got: %s", out)
	}
	if !strings.Contains(out, "100ms") {
		t.Fatalf("expected '100ms' in output, got: %s", out)
	}
	if !strings.Contains(out, "2m30s") {
		t.Fatalf("expected '2m30s' in output, got: %s", out)
	}

	// Verify nanosecond values do NOT appear.
	if strings.Contains(out, "5000000000") {
		t.Fatalf("found raw nanoseconds in output, expected duration string: %s", out)
	}
	if strings.Contains(out, "100000000") {
		t.Fatalf("found raw nanoseconds in output, expected duration string: %s", out)
	}

	// Round-trip via Unmarshal.
	var got Config
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v\nOutput: %s", err, string(data))
	}
	if len(got.Timeouts) != 3 {
		t.Fatalf("expected 3 timeouts, got %d", len(got.Timeouts))
	}
	if got.Timeouts[0] != 5*time.Second {
		t.Fatalf("expected Timeouts[0]=5s, got %v", got.Timeouts[0])
	}
	if got.Timeouts[1] != 100*time.Millisecond {
		t.Fatalf("expected Timeouts[1]=100ms, got %v", got.Timeouts[1])
	}
	if got.Timeouts[2] != 2*time.Minute+30*time.Second {
		t.Fatalf("expected Timeouts[2]=2m30s, got %v", got.Timeouts[2])
	}
}

// TestMarshalMapKeysSorted tests that marshalMapToConfig in cross_test
// produces deterministic output by sorting map keys.
func TestMarshalMapKeysSorted(t *testing.T) {
	type Config struct {
		Data map[string]string `conf:"data"`
	}
	cfg := Config{
		Data: map[string]string{
			"zebra":    "z",
			"apple":    "a",
			"mango":    "m",
			"banana":   "b",
			"cherry":   "c",
			"date":     "d",
			"eggplant": "e",
		},
	}
	// Marshal multiple times and verify deterministic output.
	var first string
	for i := 0; i < 10; i++ {
		data, err := Marshal(cfg)
		if err != nil {
			t.Fatalf("Marshal error: %v", err)
		}
		out := string(data)
		if i == 0 {
			first = out
		} else if out != first {
			t.Fatalf("non-deterministic output on iteration %d:\nfirst:\n%s\ngot:\n%s", i, first, out)
		}
	}

	// Verify the keys appear in sorted order within the data block.
	lines := strings.Split(first, "\n")
	var dataKeys []string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			inBlock = true
			continue
		}
		if inBlock && trimmed == "}" {
			break
		}
		if inBlock && strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			dataKeys = append(dataKeys, strings.TrimSpace(parts[0]))
		}
	}
	for i := 1; i < len(dataKeys); i++ {
		if dataKeys[i-1] >= dataKeys[i] {
			t.Fatalf("keys not in sorted order: %v", dataKeys)
		}
	}
}
