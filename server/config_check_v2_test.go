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
	"fmt"
	"strings"
	"testing"
)

func TestConfigCheckV2(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		err         error
		warningErr  error
		errContains string

		// errorLine is the 1-based line number of the error in the config.
		errorLine int

		// errorPos is the 0-based character position within the line.
		errorPos int
	}{
		// =================================================================
		// Unknown field detection (strict mode)
		// =================================================================
		{
			name: "unknown field at top level",
			config: `
				monitor = "127.0.0.1:4442"
			`,
			errContains: `unknown field "monitor"`,
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "unknown field default_permissions at top level",
			config: `
				"default_permissions" {
				  publish = ["_SANDBOX.>"]
				  subscribe = ["_SANDBOX.>"]
				}
			`,
			errContains: `unknown field "default_permissions"`,
			errorLine:   2,
			errorPos:    6,
		},
		{
			name: "multiple unknown top level fields",
			config: `
				port = 4222
				foo_bar = "hello"
			`,
			errContains: `unknown field "foo_bar"`,
			errorLine:   3,
			errorPos:    5,
		},
		{
			name: "valid top level fields accepted",
			config: `
				port = 4222
				host = "127.0.0.1"
				debug = true
			`,
			err: nil,
		},
		{
			name: "server_name with spaces valid parse",
			config: `
				server_name = "my server"
				port = 4222
			`,
			err: nil,
		},
		{
			name: "valid logging fields",
			config: `
				logfile = "/var/log/nats.log"
				logtime = true
				logtime_utc = true
			`,
			err: nil,
		},

		// =================================================================
		// TLS unknown fields (handled by TLSConfigOpts custom Unmarshaler)
		// =================================================================
		{
			name: "tls unknown field",
			config: `
				tls = {
				  hello = "world"
				}
			`,
			errContains: `unknown field "hello"`,
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "tls cipher suites with unknown field",
			config: `
				tls = {
				  cipher_suites: [
				    "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				    "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
				  ]
				  preferences = []
				}
			`,
			errContains: `unknown field "preferences"`,
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "tls curve preferences with unknown field",
			config: `
				tls = {
				  curve_preferences: [
				    "CurveP256",
				    "CurveP384",
				    "CurveP521"
				  ]
				  suites = []
				}
			`,
			errContains: `unknown field "suites"`,
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "tls invalid curve preference",
			config: `
				tls = {
				  curve_preferences: [
				    "CurveP5210000"
				  ]
				}
			`,
			errContains: `unrecognized curve preference`,
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "missing key_file in TLS",
			config: `
				tls = {
				  cert_file: "configs/certs/server.pem"
				}
			`,
			errContains: "missing 'key_file' in TLS configuration",
			// No errorLine/errorPos: TLS validation error from GenTLSConfig
			// in post-processing, not from parser token positions.
		},

		// =================================================================
		// Cluster unknown fields (struct-level strict mode)
		// =================================================================
		{
			name: "cluster unknown field foo",
			config: `
				cluster = {
				  port = 6222
				  foo = "bar"
				}
			`,
			errContains: `unknown field "foo"`,
			errorLine:   4,
			errorPos:    7,
		},
		{
			name: "cluster with port and name valid",
			config: `
				cluster {
				  port = 6222
				  name = "my-cluster"
				}
			`,
			err: nil,
		},

		// =================================================================
		// Conflicting authorization options (value validation)
		// =================================================================
		{
			name: "user/pass and token conflict",
			config: `
				authorization = {
				  user = "foo"
				  pass = "bar"
				  token = "baz"
				}
			`,
			errContains: "Cannot have a user/pass and token",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "token and users array conflict",
			config: `
				authorization = {
				  token = "s3cr3t"
				  users = [
				    {
				      user = "foo"
				      pass = "bar"
				    }
				  ]
				}
			`,
			errContains: "Can not have a token and a users array",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "user/pass and users array conflict",
			config: `
				authorization = {
				  user = "user1"
				  pass = "pwd1"
				  users = [
				    {
				      user = "user2"
				      pass = "pwd2"
				    }
				  ]
				}
			`,
			errContains: "Can not have a single user/pass and a users array",
			errorLine:   2,
			errorPos:    5,
		},

		// =================================================================
		// Duplicate users and nkeys (value validation)
		// =================================================================
		{
			name: "duplicate users in authorization",
			config: `
				authorization = {
				  users = [
				    {user: "user1", pass: "pwd"}
				    {user: "user2", pass: "pwd"}
				    {user: "user1", pass: "pwd"}
				  ]
				}
			`,
			errContains: `Duplicate user "user1" detected`,
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "duplicate nkeys in authorization",
			config: `
				authorization = {
				  users = [
				    {nkey: UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX }
				    {nkey: UBAAQWTW6CG2G6ANGNKB5U2B7HRWHSGMZEZX3AQSAJOQDAUGJD46LD2E }
				    {nkey: UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX }
				  ]
				}
			`,
			errContains: `Duplicate nkey "UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX" detected`,
			errorLine:   2,
			errorPos:    5,
		},

		// =================================================================
		// Invalid nkeys (value validation)
		// =================================================================
		{
			name: "invalid nkey for user in authorization",
			config: `
				authorization = {
				  users = [
				    {nkey: "SCARKS2E3KVB7YORL2DG34XLT7PUCOL2SVM7YXV6ETHLW6Z46UUJ2VZ3" }
				  ]
				}
			`,
			errContains: "Not a valid public nkey for a user",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "valid nkey for user",
			config: `
				authorization = {
				  users = [
				    {nkey: "UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX" }
				  ]
				}
			`,
			err: nil,
		},

		// =================================================================
		// Account nkey validation
		// =================================================================
		{
			name: "accounts block correctly configured",
			config: `
				http_port = 8222
				accounts {
				  synadia {
				    nkey = "AC5GRL36RQV7MJ2GT6WQSCKDKJKYTK4T2LGLWJ2SEJKRDHFOQQWGGFQL"
				    users [
				      {
				        nkey = "UCARKS2E3KVB7YORL2DG34XLT7PUCOL2SVM7YXV6ETHLW6Z46UUJ2VZ3"
				      }
				    ]
				    exports = [
				      { service: "synadia.requests", accounts: [nats] }
				    ]
				  }
				  nats {
				    nkey = "ADRZ42QBM7SXQDXXTSVWT2WLLFYOQGAFC4TO6WOAXHEKQHIXR4HFYJDS"
				    users [
				      {
				        nkey = "UD6AYQSOIN2IN5OGC6VQZCR4H3UFMIOXSW6NNS6N53CLJA4PB56CEJJI"
				      }
				    ]
				    imports = [
				      { service: { account: "synadia", subject: "synadia.requests" }, to: "nats.requests" }
				    ]
				  }
				}
			`,
			err: nil,
		},
		{
			name: "invalid nkey in accounts block for user",
			config: `
				accounts {
				  synadia {
				    nkey = "AC5GRL36RQV7MJ2GT6WQSCKDKJKYTK4T2LGLWJ2SEJKRDHFOQQWGGFQL"
				    users [
				      {
				        nkey = "SCARKS2E3KVB7YORL2DG34XLT7PUCOL2SVM7YXV6ETHLW6Z46UUJ2VZ3"
				      }
				    ]
				  }
				}
			`,
			errContains: "Not a valid public nkey for a user",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "invalid nkey in accounts block for account",
			config: `
				accounts {
				  synadia = {
				    nkey = "invalid"
				  }
				}
			`,
			errContains: `Not a valid public nkey for an account: "invalid"`,
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "accounts block includes duplicate user",
			config: `
				port = 4222
				accounts = {
				  nats {
				    users = [
				      { user: "foo",   pass: "bar" },
				      { user: "hello", pass: "world" },
				      { user: "foo",   pass: "bar" }
				    ]
				  }
				}
				http_port = 8222
			`,
			errContains: `Duplicate user "foo" detected`,
			errorLine:   3,
			errorPos:    5,
		},
		{
			name: "accounts block with referenced config variable within same block",
			config: `
				accounts {
				  PERMISSIONS = {
				    publish = {
				      allow = ["foo","bar"]
				      deny = ["quux"]
				    }
				  }
				  synadia {
				    nkey = "AC5GRL36RQV7MJ2GT6WQSCKDKJKYTK4T2LGLWJ2SEJKRDHFOQQWGGFQL"
				    users [
				      {
				        nkey = "UCARKS2E3KVB7YORL2DG34XLT7PUCOL2SVM7YXV6ETHLW6Z46UUJ2VZ3"
				        permissions = $PERMISSIONS
				      }
				    ]
				    exports = [
				      { stream: "synadia.>" }
				    ]
				  }
				}
			`,
			err: nil,
		},

		// =================================================================
		// JetStream validation
		// =================================================================
		{
			name: "ambiguous store dir",
			config: `
				store_dir: "foo"
				jetstream {
				  store_dir: "bar"
				}
			`,
			errContains: "duplicate",
		},
		{
			name: "jetstream boolean true is valid",
			config: `
				jetstream: true
			`,
			err: nil,
		},
		{
			name: "jetstream with store_dir is valid",
			config: `
				jetstream {
				  store_dir: "/tmp/js"
				  max_mem: 1GB
				  max_file: 10GB
				}
			`,
			err: nil,
		},
		{
			name: "jetstream string enabled valid",
			config: `
				jetstream: "enabled"
			`,
			err: nil,
		},

		// =================================================================
		// Leafnode validation
		// =================================================================
		{
			name: "leafnode min_version has parsing error",
			config: `
				leafnodes {
				  port: -1
				  min_version = bad.version
				}
			`,
			errContains: "invalid leafnode's minimum version",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "leafnode min_version is too low",
			config: `
				leafnodes {
				  port: -1
				  min_version = 2.7.9
				}
			`,
			errContains: "the minimum version should be at least 2.8.0",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "valid leafnodes with port",
			config: `
				leafnodes {
				  port = 7422
				}
			`,
			err: nil,
		},

		// =================================================================
		// Duration validation
		// =================================================================
		{
			name: "invalid lame_duck_duration type",
			config: `
				lame_duck_duration: abc
			`,
			errContains: "invalid duration",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "lame_duck_duration too small",
			config: `
				lame_duck_duration: "5s"
			`,
			errContains: "invalid lame_duck_duration",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "valid lame_duck_duration",
			config: `
				lame_duck_duration: "60s"
			`,
			err: nil,
		},
		{
			name: "invalid lame_duck_grace_period type",
			config: `
				lame_duck_grace_period: abc
			`,
			errContains: "invalid duration",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "lame_duck_grace_period should be positive",
			config: `
				lame_duck_grace_period: "-5s"
			`,
			errContains: "invalid lame_duck_grace_period",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "valid lame_duck_grace_period",
			config: `
				lame_duck_grace_period: "10s"
			`,
			err: nil,
		},
		{
			name: "valid duration fields",
			config: `
				lame_duck_duration: "60s"
				lame_duck_grace_period: "10s"
			`,
			err: nil,
		},

		// =================================================================
		// Cluster validation
		// =================================================================
		{
			name: "cluster ping_interval too high warns",
			config: `
				cluster {
				  port: -1
				  ping_interval: '2m'
				  ping_max: 6
				}
			`,
			warningErr: fmt.Errorf("Cluster 'ping_interval' will reset to"),
			errorLine:  2,
			errorPos:   5,
		},

		// =================================================================
		// Warning: empty config
		// =================================================================
		{
			name: "empty config warns",
			config: ``,
			warningErr: fmt.Errorf("config has no values or is empty"),
		},
		{
			name: "comment-only config warns",
			config: `# Valid file but has no usable values.
			`,
			warningErr: fmt.Errorf("config has no values or is empty"),
		},

		// =================================================================
		// Valid mixed configs
		// =================================================================
		{
			name: "simple valid config",
			config: `
				port = 4222
				server_name = "test"
				debug = false
			`,
			err: nil,
		},
		{
			name: "multiple simple top-level fields all valid",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "test-server"
				max_payload = 1MB
				max_connections = 100
				ping_interval = "2s"
				debug = true
				trace = false
			`,
			err: nil,
		},
		{
			name: "valid authorization with users",
			config: `
				authorization {
				  users = [
				    {user: "alice", pass: "secret1"}
				    {user: "bob", pass: "secret2"}
				  ]
				}
			`,
			err: nil,
		},
		{
			name: "valid gateway config",
			config: `
				gateway {
				  port = 7222
				  name = "A"
				}
			`,
			err: nil,
		},
		{
			name: "valid websocket config",
			config: `
				websocket {
				  port = 8080
				  no_tls = true
				}
			`,
			err: nil,
		},
		{
			name: "valid mqtt config",
			config: `
				mqtt {
				  port = 1883
				}
			`,
			err: nil,
		},
		{
			name: "valid authorization with single user",
			config: `
				authorization {
				  user = "admin"
				  pass = "secret"
				  timeout = 2.0
				}
			`,
			err: nil,
		},
		{
			name: "valid authorization with token",
			config: `
				authorization {
				  token = "my_secret_token"
				}
			`,
			err: nil,
		},

		// =================================================================
		// Type mismatch errors from Unmarshal
		// =================================================================
		{
			name: "mqtt wrong value type for port",
			config: `
				mqtt {
				  port: "abc"
				}
			`,
			errContains: "cannot unmarshal",
			errorLine:   3,
			errorPos:    7,
		},
		{
			name: "port wrong type - string instead of int",
			config: `
				port = "abc"
			`,
			errContains: "cannot unmarshal",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "debug wrong type - string instead of bool",
			config: `
				debug = "abc"
			`,
			errContains: "cannot unmarshal",
			errorLine:   2,
			errorPos:    5,
		},
		{
			name: "max_payload wrong type - bool instead of int",
			config: `
				max_payload = true
			`,
			errContains: "cannot unmarshal",
			errorLine:   2,
			errorPos:    5,
		},

		// =================================================================
		// Size suffix fields
		// =================================================================
		{
			name: "max_payload with size suffix valid",
			config: `
				max_payload = 4MB
			`,
			err: nil,
		},
		{
			name: "max_connections integer valid",
			config: `
				max_connections = 1000
			`,
			err: nil,
		},

		// =================================================================
		// Latency tracking with system account valid
		// =================================================================
		{
			name: "latency tracking with system account is valid",
			config: `
				system_account: sys
				accounts {
				  sys { users = [ {user: sys, pass: "" } ] }
				  nats.io: {
				    users = [ { user : bar, pass: "" } ]
				    exports = [
				      { service: "nats.add"
				        response: singleton
				        latency: {
				          sampling: 100%
				          subject: "latency.tracking.add"
				        }
				      }
				    ]
				  }
				}
			`,
			err: nil,
		},

		// =================================================================
		// Gateway unknown fields
		// =================================================================
		{
			name: "gateway unknown field",
			config: `
				gateway {
				  port = 7222
				  name = "A"
				  foo = "bar"
				}
			`,
			errContains: `unknown field "foo"`,
			errorLine:   5,
			errorPos:    7,
		},

		// =================================================================
		// Websocket unknown fields
		// =================================================================
		{
			name: "websocket unknown field",
			config: `
				websocket {
				  port = 8080
				  no_tls = true
				  foo_bar = "baz"
				}
			`,
			errContains: `unknown field "foo_bar"`,
			errorLine:   5,
			errorPos:    7,
		},

		// =================================================================
		// MQTT unknown fields
		// =================================================================
		{
			name: "mqtt unknown field",
			config: `
				mqtt {
				  port = 1883
				  baz = "quux"
				}
			`,
			errContains: `unknown field "baz"`,
			errorLine:   4,
			errorPos:    7,
		},

		// =================================================================
		// Leafnode unknown fields
		// =================================================================
		{
			name: "leafnode unknown field",
			config: `
				leafnodes {
				  port = 7422
				  foo_field = "bar"
				}
			`,
			errContains: `unknown field "foo_field"`,
			errorLine:   4,
			errorPos:    7,
		},

		// =================================================================
		// Multiple valid nkeys in accounts
		// =================================================================
		{
			name: "multiple valid nkeys in accounts",
			config: `
				accounts {
				  synadia {
				    nkey = "AC5GRL36RQV7MJ2GT6WQSCKDKJKYTK4T2LGLWJ2SEJKRDHFOQQWGGFQL"
				    users [
				      { nkey = "UCARKS2E3KVB7YORL2DG34XLT7PUCOL2SVM7YXV6ETHLW6Z46UUJ2VZ3" }
				    ]
				  }
				  nats {
				    nkey = "ADRZ42QBM7SXQDXXTSVWT2WLLFYOQGAFC4TO6WOAXHEKQHIXR4HFYJDS"
				    users [
				      { nkey = "UD6AYQSOIN2IN5OGC6VQZCR4H3UFMIOXSW6NNS6N53CLJA4PB56CEJJI" }
				    ]
				  }
				}
			`,
			err: nil,
		},

		// =================================================================
		// Cluster with valid fields
		// =================================================================
		{
			name: "cluster with pool_size and accounts valid",
			config: `
				cluster {
				  port = 6222
				  name = "test"
				  pool_size = 3
				  accounts = ["A", "B"]
				}
			`,
			err: nil,
		},
		{
			name: "cluster compression bool to struct type error",
			config: `
				cluster {
				  port = 6222
				  compression = true
				}
			`,
			// Note: In v2, CompressionOpts is a struct without a custom
			// Unmarshaler, so bool cannot be assigned to it directly.
			// This differs from v1 which handles it in custom parsing.
			errContains: "cannot unmarshal",
			errorLine:   4,
			errorPos:    7,
		},

		// =================================================================
		// Position accuracy: errors deep in nested blocks
		// =================================================================
		{
			name: "unknown field deep in cluster after many valid lines",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "test-server"
				debug = false
				trace = false
				max_payload = 1MB
				max_connections = 100
				cluster {
				  port = 6222
				  name = "my-cluster"
				  bogus_field = true
				}
			`,
			errContains: `unknown field "bogus_field"`,
			errorLine:   12,
			errorPos:    7,
		},
		{
			name: "unknown field deep in gateway after many valid lines",
			config: `
				port = 4222
				host = "127.0.0.1"
				debug = true
				trace = false
				gateway {
				  port = 7222
				  name = "A"
				  unknown_gw = true
				}
			`,
			errContains: `unknown field "unknown_gw"`,
			errorLine:   9,
			errorPos:    7,
		},
		{
			name: "type mismatch in nested mqtt block after leading config",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "test"
				mqtt {
				  port: "not_a_number"
				}
			`,
			errContains: "cannot unmarshal",
			errorLine:   6,
			errorPos:    7,
		},
		{
			name: "auth conflict after many config lines",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "test-server"
				debug = false
				trace = false
				max_payload = 1MB
				authorization = {
				  user = "admin"
				  pass = "secret"
				  token = "my_token"
				}
			`,
			errContains: "Cannot have a user/pass and token",
			errorLine:   8,
			errorPos:    5,
		},
		{
			name: "invalid nkey in accounts after many valid fields",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "test-server"
				debug = false
				accounts {
				  synadia = {
				    nkey = "invalid_key"
				  }
				}
			`,
			errContains: `Not a valid public nkey for an account: "invalid_key"`,
			errorLine:   6,
			errorPos:    5,
		},
		{
			name: "lame_duck_duration after many config lines",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "test-server"
				debug = false
				trace = false
				max_payload = 1MB
				max_connections = 100
				lame_duck_duration: "5s"
			`,
			errContains: "invalid lame_duck_duration",
			errorLine:   9,
			errorPos:    5,
		},
		{
			name: "leafnode min_version error after leading config",
			config: `
				port = 4222
				host = "0.0.0.0"
				leafnodes {
				  port: 7422
				  min_version = 2.7.9
				}
			`,
			errContains: "the minimum version should be at least 2.8.0",
			errorLine:   4,
			errorPos:    5,
		},
		{
			name: "duplicate user in accounts deep in config",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "test"
				accounts = {
				  nats {
				    users = [
				      { user: "alice", pass: "secret1" }
				      { user: "bob",   pass: "secret2" }
				      { user: "alice", pass: "secret3" }
				    ]
				  }
				}
			`,
			errContains: `Duplicate user "alice" detected`,
			errorLine:   5,
			errorPos:    5,
		},
		{
			name: "websocket unknown field deep in nested block",
			config: `
				port = 4222
				host = "0.0.0.0"
				server_name = "my-server"
				debug = false
				websocket {
				  port = 8080
				  no_tls = true
				  invalid_ws_field = "test"
				}
			`,
			errContains: `unknown field "invalid_ws_field"`,
			errorLine:   9,
			errorPos:    7,
		},
	}

	checkConfigV2 := func(config string) error {
		opts := &Options{
			CheckConfig: true,
		}
		return opts.ProcessConfigFileV2(config)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := createConfFile(t, []byte(test.config))
			err := checkConfigV2(conf)

			var expectedErr error
			if test.err != nil {
				expectedErr = test.err
			} else if test.warningErr != nil {
				expectedErr = test.warningErr
			} else if test.errContains != "" {
				expectedErr = fmt.Errorf("%s", test.errContains)
			}

			if expectedErr != nil {
				if err == nil {
					t.Fatalf("Expected error containing %q but got none", expectedErr)
				}

				// When errorLine > 0, verify the error contains the expected
				// file:line:pos prefix, matching v1's TestConfigCheck pattern.
				if test.errorLine > 0 {
					prefix := fmt.Sprintf("%s:%d:%d:", conf, test.errorLine, test.errorPos)
					if !strings.Contains(err.Error(), prefix) {
						t.Errorf("Expected error to contain position prefix:\n  %q\ngot:\n  %q", prefix, err.Error())
					}
				}

				matchStr := ""
				if test.errContains != "" {
					matchStr = test.errContains
				} else if test.warningErr != nil {
					matchStr = test.warningErr.Error()
				} else {
					matchStr = test.err.Error()
				}

				if !strings.Contains(err.Error(), matchStr) {
					t.Errorf("Expected error to contain:\n  %q\ngot:\n  %q", matchStr, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %s", err)
				}
			}
		})
	}
}

// TestConfigCheckV2ProcessConfigV2Err verifies the processConfigV2Err type.
func TestConfigCheckV2ProcessConfigV2Err(t *testing.T) {
	t.Run("errors and warnings", func(t *testing.T) {
		e := &processConfigV2Err{
			errors:   []error{fmt.Errorf("error1"), fmt.Errorf("error2")},
			warnings: []error{fmt.Errorf("warning1")},
		}
		if len(e.Errors()) != 2 {
			t.Fatalf("expected 2 errors, got %d", len(e.Errors()))
		}
		if len(e.Warnings()) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(e.Warnings()))
		}
		msg := e.Error()
		if !strings.Contains(msg, "warning1") {
			t.Errorf("expected Error() to contain 'warning1', got: %s", msg)
		}
		if !strings.Contains(msg, "error1") {
			t.Errorf("expected Error() to contain 'error1', got: %s", msg)
		}
		if !strings.Contains(msg, "error2") {
			t.Errorf("expected Error() to contain 'error2', got: %s", msg)
		}
		// Warnings should appear before errors in the output.
		wIdx := strings.Index(msg, "warning1")
		eIdx := strings.Index(msg, "error1")
		if wIdx > eIdx {
			t.Errorf("expected warnings before errors in output")
		}
	})

	t.Run("empty errors and warnings", func(t *testing.T) {
		e := &processConfigV2Err{}
		if len(e.Errors()) != 0 {
			t.Fatalf("expected 0 errors, got %d", len(e.Errors()))
		}
		if len(e.Warnings()) != 0 {
			t.Fatalf("expected 0 warnings, got %d", len(e.Warnings()))
		}
		if e.Error() != "" {
			t.Errorf("expected empty Error(), got: %q", e.Error())
		}
	})

	t.Run("only warnings", func(t *testing.T) {
		e := &processConfigV2Err{
			warnings: []error{fmt.Errorf("just a warning")},
		}
		if len(e.Errors()) != 0 {
			t.Fatalf("expected 0 errors, got %d", len(e.Errors()))
		}
		if len(e.Warnings()) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(e.Warnings()))
		}
	})

	t.Run("only errors", func(t *testing.T) {
		e := &processConfigV2Err{
			errors: []error{fmt.Errorf("an error")},
		}
		if len(e.Errors()) != 1 {
			t.Fatalf("expected 1 error, got %d", len(e.Errors()))
		}
		if len(e.Warnings()) != 0 {
			t.Fatalf("expected 0 warnings, got %d", len(e.Warnings()))
		}
		if !strings.Contains(e.Error(), "an error") {
			t.Errorf("expected Error() to contain 'an error', got: %q", e.Error())
		}
	})
}

// TestConfigCheckV2StrictModeDetectsUnknownFields verifies that strict mode
// detects unknown config fields and reports them with position info.
func TestConfigCheckV2StrictModeDetectsUnknownFields(t *testing.T) {
	config := `
		port = 4222
		bogus_field = "hello"
	`
	conf := createConfFile(t, []byte(config))
	opts := &Options{CheckConfig: true}
	err := opts.ProcessConfigFileV2(conf)
	if err == nil {
		t.Fatal("Expected error for unknown field but got none")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, `unknown field "bogus_field"`) {
		t.Errorf("Expected error to mention bogus_field, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, conf) {
		t.Errorf("Expected error to contain file path %q, got: %s", conf, errMsg)
	}
}

// TestConfigCheckV2PermissiveByDefault verifies that without CheckConfig,
// unknown fields are silently ignored.
func TestConfigCheckV2PermissiveByDefault(t *testing.T) {
	config := `
		port = 4222
		bogus_field = "hello"
	`
	conf := createConfFile(t, []byte(config))
	opts, err := ProcessConfigV2(conf)
	if err != nil {
		t.Fatalf("Unexpected error in permissive mode: %v", err)
	}
	if opts.Port != 4222 {
		t.Fatalf("Expected port 4222, got %d", opts.Port)
	}
}

// TestConfigCheckV2MultipleValidationErrors verifies that multiple
// validation errors are collected and reported together.
func TestConfigCheckV2MultipleValidationErrors(t *testing.T) {
	config := `
		authorization = {
		  user = "admin"
		  pass = "secret"
		  token = "my_token"
		}
	`
	conf := createConfFile(t, []byte(config))
	opts := &Options{CheckConfig: true}
	err := opts.ProcessConfigFileV2(conf)
	if err == nil {
		t.Fatal("Expected error for conflicting auth")
	}
	if !strings.Contains(err.Error(), "Cannot have a user/pass and token") {
		t.Errorf("Expected user/pass and token conflict error, got: %s", err.Error())
	}
}

// TestConfigCheckV2WarningsOnlyStructType verifies that processConfigV2Err
// with only warnings has the correct type and methods.
func TestConfigCheckV2WarningsOnlyStructType(t *testing.T) {
	config := `
		cluster {
		  port: -1
		  ping_interval: '2m'
		  ping_max: 6
		}
	`
	conf := createConfFile(t, []byte(config))
	opts := &Options{CheckConfig: true}
	err := opts.ProcessConfigFileV2(conf)
	if err == nil {
		t.Fatal("Expected a warning to be returned")
	}
	cerr, ok := err.(*processConfigV2Err)
	if !ok {
		t.Fatalf("Expected processConfigV2Err type, got %T", err)
	}
	if len(cerr.Errors()) != 0 {
		t.Fatalf("Expected no hard errors, got %d: %v", len(cerr.Errors()), cerr.Errors())
	}
	if len(cerr.Warnings()) == 0 {
		t.Fatal("Expected at least one warning")
	}
}

// TestConfigCheckV2ErrorWithPositionInfo verifies that errors include
// file:line:col position information.
func TestConfigCheckV2ErrorWithPositionInfo(t *testing.T) {
	config := `port = 4222
unknown_top = true`
	conf := createConfFile(t, []byte(config))
	opts := &Options{CheckConfig: true}
	err := opts.ProcessConfigFileV2(conf)
	if err == nil {
		t.Fatal("Expected error for unknown field")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, conf) {
		t.Errorf("Expected error to contain file path, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, ":2:") {
		t.Errorf("Expected error to contain line 2 position info, got: %s", errMsg)
	}
}

// TestConfigCheckV2ProcessConfigV2PublicAPI verifies the public ProcessConfigV2
// function returns options for valid configs.
func TestConfigCheckV2ProcessConfigV2PublicAPI(t *testing.T) {
	config := `
		port = 4222
		debug = true
	`
	conf := createConfFile(t, []byte(config))
	opts, err := ProcessConfigV2(conf)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if opts.Port != 4222 {
		t.Fatalf("Expected port 4222, got %d", opts.Port)
	}
	if !opts.Debug {
		t.Fatal("Expected debug to be true")
	}
}

// TestConfigCheckV2DurationFieldsValid verifies that valid duration fields
// are accepted without errors.
func TestConfigCheckV2DurationFieldsValid(t *testing.T) {
	config := `
		lame_duck_duration: "60s"
		lame_duck_grace_period: "10s"
	`
	conf := createConfFile(t, []byte(config))
	opts := &Options{CheckConfig: true}
	err := opts.ProcessConfigFileV2(conf)
	if err != nil {
		t.Fatalf("Unexpected error for valid durations: %v", err)
	}
}

// TestConfigCheckV2NkeyValidation verifies nkey validation for users
// across both authorization and accounts blocks.
func TestConfigCheckV2NkeyValidation(t *testing.T) {
	// Valid user nkey.
	config := `
		authorization {
		  users = [
		    {nkey: "UCARKS2E3KVB7YORL2DG34XLT7PUCOL2SVM7YXV6ETHLW6Z46UUJ2VZ3"}
		  ]
		}
	`
	conf := createConfFile(t, []byte(config))
	opts := &Options{CheckConfig: true}
	err := opts.ProcessConfigFileV2(conf)
	if err != nil {
		t.Fatalf("Unexpected error for valid nkey: %v", err)
	}

	// Invalid user nkey (S prefix = seed, not user).
	config = `
		authorization {
		  users = [
		    {nkey: "SCARKS2E3KVB7YORL2DG34XLT7PUCOL2SVM7YXV6ETHLW6Z46UUJ2VZ3"}
		  ]
		}
	`
	conf = createConfFile(t, []byte(config))
	opts = &Options{CheckConfig: true}
	err = opts.ProcessConfigFileV2(conf)
	if err == nil {
		t.Fatal("Expected error for invalid nkey")
	}
	if !strings.Contains(err.Error(), "Not a valid public nkey for a user") {
		t.Errorf("Expected nkey validation error, got: %v", err)
	}
}

// TestConfigCheckV2ProcessConfigFileV2Method verifies the receiver method.
func TestConfigCheckV2ProcessConfigFileV2Method(t *testing.T) {
	config := `
		port = 4222
		debug = true
		trace = false
	`
	conf := createConfFile(t, []byte(config))
	opts := &Options{}
	err := opts.ProcessConfigFileV2(conf)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if opts.Port != 4222 {
		t.Fatalf("Expected port 4222, got %d", opts.Port)
	}
	if !opts.Debug {
		t.Fatal("Expected debug to be true")
	}
}
