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
	"strings"
	"testing"
)

// TestUnmarshalAliasPrimaryName verifies that the first (primary) alias
// is used when matching config keys to struct fields.
func TestUnmarshalAliasPrimaryName(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net"`
	}
	input := []byte(`host: 127.0.0.1`)
	var cfg Config
	if err := Unmarshal(input, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected Host=127.0.0.1, got %q", cfg.Host)
	}
}

// TestUnmarshalAliasSecondaryName verifies that a secondary alias
// name matches config keys to struct fields.
func TestUnmarshalAliasSecondaryName(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net"`
	}
	input := []byte(`net: 192.168.1.1`)
	var cfg Config
	if err := Unmarshal(input, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Host != "192.168.1.1" {
		t.Fatalf("expected Host=192.168.1.1, got %q", cfg.Host)
	}
}

// TestUnmarshalAliasCaseInsensitive verifies that alias matching is
// case-insensitive for all aliases.
func TestUnmarshalAliasCaseInsensitive(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net"`
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"uppercase HOST", "HOST: a.com", "a.com"},
		{"uppercase NET", "NET: b.com", "b.com"},
		{"mixed Host", "Host: c.com", "c.com"},
		{"mixed Net", "Net: d.com", "d.com"},
		{"lower host", "host: e.com", "e.com"},
		{"lower net", "net: f.com", "f.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := Unmarshal([]byte(tt.input), &cfg); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if cfg.Host != tt.want {
				t.Fatalf("expected Host=%q, got %q", tt.want, cfg.Host)
			}
		})
	}
}

// TestUnmarshalAliasWithOmitempty verifies that pipe-separated aliases
// work correctly with the omitempty option.
func TestUnmarshalAliasWithOmitempty(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net,omitempty"`
		Port int    `conf:"port,omitempty"`
	}

	// Primary alias with omitempty.
	input := []byte(`host: 127.0.0.1`)
	var cfg Config
	if err := Unmarshal(input, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected Host=127.0.0.1, got %q", cfg.Host)
	}

	// Secondary alias with omitempty.
	input = []byte(`net: 10.0.0.1`)
	cfg = Config{}
	if err := Unmarshal(input, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Host != "10.0.0.1" {
		t.Fatalf("expected Host=10.0.0.1, got %q", cfg.Host)
	}
}

// TestUnmarshalAliasRealNATSPatterns tests alias patterns commonly used
// in real NATS server configurations.
func TestUnmarshalAliasRealNATSPatterns(t *testing.T) {
	type ServerConfig struct {
		Host           string `conf:"host|net"`
		User           string `conf:"user|username"`
		Pass           string `conf:"pass|password"`
		StoreDir       string `conf:"store_dir|storedir"`
		MaxConnections int    `conf:"max_connections|max_conn"`
	}

	tests := []struct {
		name  string
		input string
		check func(t *testing.T, cfg *ServerConfig)
	}{
		{
			"host primary",
			`host: 0.0.0.0`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.Host != "0.0.0.0" {
					t.Fatalf("expected Host=0.0.0.0, got %q", cfg.Host)
				}
			},
		},
		{
			"net secondary",
			`net: 0.0.0.0`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.Host != "0.0.0.0" {
					t.Fatalf("expected Host=0.0.0.0, got %q", cfg.Host)
				}
			},
		},
		{
			"user primary",
			`user: admin`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.User != "admin" {
					t.Fatalf("expected User=admin, got %q", cfg.User)
				}
			},
		},
		{
			"username secondary",
			`username: admin`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.User != "admin" {
					t.Fatalf("expected User=admin, got %q", cfg.User)
				}
			},
		},
		{
			"pass primary",
			`pass: secret`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.Pass != "secret" {
					t.Fatalf("expected Pass=secret, got %q", cfg.Pass)
				}
			},
		},
		{
			"password secondary",
			`password: secret`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.Pass != "secret" {
					t.Fatalf("expected Pass=secret, got %q", cfg.Pass)
				}
			},
		},
		{
			"store_dir primary",
			`store_dir: /data/jetstream`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.StoreDir != "/data/jetstream" {
					t.Fatalf("expected StoreDir=/data/jetstream, got %q", cfg.StoreDir)
				}
			},
		},
		{
			"storedir secondary",
			`storedir: /data/jetstream`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.StoreDir != "/data/jetstream" {
					t.Fatalf("expected StoreDir=/data/jetstream, got %q", cfg.StoreDir)
				}
			},
		},
		{
			"max_connections primary",
			`max_connections: 1000`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.MaxConnections != 1000 {
					t.Fatalf("expected MaxConnections=1000, got %d", cfg.MaxConnections)
				}
			},
		},
		{
			"max_conn secondary",
			`max_conn: 1000`,
			func(t *testing.T, cfg *ServerConfig) {
				if cfg.MaxConnections != 1000 {
					t.Fatalf("expected MaxConnections=1000, got %d", cfg.MaxConnections)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg ServerConfig
			if err := Unmarshal([]byte(tt.input), &cfg); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			tt.check(t, &cfg)
		})
	}
}

// TestUnmarshalAliasMultipleAliases verifies that more than two aliases
// work (e.g., pub|publish|import with three aliases).
func TestUnmarshalAliasMultipleAliases(t *testing.T) {
	type Config struct {
		Pub  string `conf:"pub|publish|import"`
		Leaf string `conf:"leaf|leafnodes"`
	}

	tests := []struct {
		name  string
		input string
		field string
		want  string
	}{
		{"pub primary", "pub: allow", "Pub", "allow"},
		{"publish secondary", "publish: deny", "Pub", "deny"},
		{"import tertiary", "import: allow", "Pub", "allow"},
		{"leaf primary", "leaf: enabled", "Leaf", "enabled"},
		{"leafnodes secondary", "leafnodes: disabled", "Leaf", "disabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := Unmarshal([]byte(tt.input), &cfg); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			var got string
			switch tt.field {
			case "Pub":
				got = cfg.Pub
			case "Leaf":
				got = cfg.Leaf
			}
			if got != tt.want {
				t.Fatalf("expected %s=%q, got %q", tt.field, tt.want, got)
			}
		})
	}
}

// TestUnmarshalAliasComplexConfig tests a complete config with multiple
// alias patterns used together.
func TestUnmarshalAliasComplexConfig(t *testing.T) {
	type Config struct {
		Host     string `conf:"host|net"`
		Port     int    `conf:"port"`
		User     string `conf:"user|username"`
		Pass     string `conf:"pass|password"`
		StoreDir string `conf:"store_dir|storedir"`
	}

	input := []byte(`
net: 0.0.0.0
port: 4222
username: admin
password: secret
storedir: /data
`)

	var cfg Config
	if err := Unmarshal(input, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("expected Host=0.0.0.0, got %q", cfg.Host)
	}
	if cfg.Port != 4222 {
		t.Fatalf("expected Port=4222, got %d", cfg.Port)
	}
	if cfg.User != "admin" {
		t.Fatalf("expected User=admin, got %q", cfg.User)
	}
	if cfg.Pass != "secret" {
		t.Fatalf("expected Pass=secret, got %q", cfg.Pass)
	}
	if cfg.StoreDir != "/data" {
		t.Fatalf("expected StoreDir=/data, got %q", cfg.StoreDir)
	}
}

// TestUnmarshalAliasStrictModeUnknownField verifies that strict mode
// still reports unknown fields correctly when aliases are in use.
func TestUnmarshalAliasStrictModeUnknownField(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net"`
	}

	input := []byte(`
host: 127.0.0.1
unknown_field: oops
`)

	var cfg Config
	err := UnmarshalWith(input, &cfg, &UnmarshalOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error for unknown field in strict mode")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("expected error to mention unknown_field, got: %v", err)
	}
}

// TestUnmarshalAliasNestedStruct verifies that aliases work in nested structs.
func TestUnmarshalAliasNestedStruct(t *testing.T) {
	type Cluster struct {
		Host string `conf:"host|net"`
		Port int    `conf:"port"`
	}
	type Config struct {
		Cluster Cluster `conf:"cluster"`
	}

	input := []byte(`
cluster {
  net: 0.0.0.0
  port: 6222
}
`)
	var cfg Config
	if err := Unmarshal(input, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Cluster.Host != "0.0.0.0" {
		t.Fatalf("expected Cluster.Host=0.0.0.0, got %q", cfg.Cluster.Host)
	}
	if cfg.Cluster.Port != 6222 {
		t.Fatalf("expected Cluster.Port=6222, got %d", cfg.Cluster.Port)
	}
}

// TestMarshalAliasPrimaryName verifies that Marshal uses the first (primary)
// alias name as the output key.
func TestMarshalAliasPrimaryName(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net"`
		User string `conf:"user|username"`
		Pass string `conf:"pass|password"`
	}

	cfg := Config{
		Host: "0.0.0.0",
		User: "admin",
		Pass: "secret",
	}

	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	output := string(data)

	// Should use primary alias names.
	if !strings.Contains(output, "host: ") {
		t.Fatalf("expected output to contain 'host: ', got:\n%s", output)
	}
	if !strings.Contains(output, "user: ") {
		t.Fatalf("expected output to contain 'user: ', got:\n%s", output)
	}
	if !strings.Contains(output, "pass: ") {
		t.Fatalf("expected output to contain 'pass: ', got:\n%s", output)
	}

	// Should NOT contain secondary alias names.
	if strings.Contains(output, "net: ") {
		t.Fatalf("expected output to NOT contain 'net: ', got:\n%s", output)
	}
	if strings.Contains(output, "username: ") {
		t.Fatalf("expected output to NOT contain 'username: ', got:\n%s", output)
	}
	if strings.Contains(output, "password: ") {
		t.Fatalf("expected output to NOT contain 'password: ', got:\n%s", output)
	}
}

// TestMarshalAliasWithOmitempty verifies that Marshal correctly handles
// omitempty with aliased fields.
func TestMarshalAliasWithOmitempty(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net,omitempty"`
		Port int    `conf:"port"`
	}

	// Host is zero value (empty string), should be omitted.
	cfg := Config{Port: 4222}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(data)
	if strings.Contains(output, "host") {
		t.Fatalf("expected host to be omitted, got:\n%s", output)
	}

	// Host is non-zero, should be present with primary name.
	cfg.Host = "0.0.0.0"
	data, err = Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output = string(data)
	if !strings.Contains(output, "host: 0.0.0.0") {
		t.Fatalf("expected 'host: 0.0.0.0', got:\n%s", output)
	}
}

// TestMarshalAliasMultipleAliases verifies that Marshal uses the first
// alias even when there are more than two.
func TestMarshalAliasMultipleAliases(t *testing.T) {
	type Config struct {
		Pub string `conf:"pub|publish|import"`
	}

	cfg := Config{Pub: "allow"}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(data)
	if !strings.Contains(output, "pub: ") {
		t.Fatalf("expected 'pub: ', got:\n%s", output)
	}
	if strings.Contains(output, "publish: ") || strings.Contains(output, "import: ") {
		t.Fatalf("expected only primary alias, got:\n%s", output)
	}
}

// TestUnmarshalMarshalRoundTripAlias verifies that an alias-tagged struct
// can be unmarshaled and then marshaled round-trip, using the primary name.
func TestUnmarshalMarshalRoundTripAlias(t *testing.T) {
	type Config struct {
		Host string `conf:"host|net"`
		Port int    `conf:"port"`
	}

	// Unmarshal using secondary alias.
	input := []byte(`
net: 0.0.0.0
port: 4222
`)
	var cfg Config
	if err := Unmarshal(input, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("expected Host=0.0.0.0, got %q", cfg.Host)
	}

	// Marshal should use primary alias.
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(data)
	if !strings.Contains(output, "host: ") {
		t.Fatalf("expected 'host: ' in marshaled output, got:\n%s", output)
	}

	// Re-unmarshal marshaled output.
	var cfg2 Config
	if err := Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	if cfg2.Host != cfg.Host || cfg2.Port != cfg.Port {
		t.Fatalf("round-trip mismatch: got Host=%q Port=%d, want Host=%q Port=%d",
			cfg2.Host, cfg2.Port, cfg.Host, cfg.Port)
	}
}

// TestMarshalIndentAlias verifies MarshalIndent uses the primary alias name.
func TestMarshalIndentAlias(t *testing.T) {
	type Cluster struct {
		Host string `conf:"host|net"`
		Port int    `conf:"port"`
	}
	type Config struct {
		Cluster Cluster `conf:"cluster"`
	}

	cfg := Config{Cluster: Cluster{Host: "0.0.0.0", Port: 6222}}
	data, err := MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	output := string(data)
	if !strings.Contains(output, "host: ") {
		t.Fatalf("expected 'host: ' in output, got:\n%s", output)
	}
	if strings.Contains(output, "net: ") {
		t.Fatalf("expected NO 'net: ' in output, got:\n%s", output)
	}
}
