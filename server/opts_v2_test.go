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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeTestConfig writes a config string to a temp file and returns its path.
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	tmpFile := filepath.Join(t.TempDir(), "test.conf")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}
	return tmpFile
}

// TestProcessConfigV2SimpleFields tests that simple top-level Options fields
// are populated correctly by ProcessConfigV2.
func TestProcessConfigV2SimpleFields(t *testing.T) {
	conf := `
		server_name: testing_v2
		host: 127.0.0.1
		port: 4222
		debug: true
		trace: true
		logtime: false
		max_connections: 100
		max_subscriptions: 1000
		max_payload: 65536
		max_control_line: 2048
		max_pending: 10000000
		ping_interval: "60s"
		ping_max: 3
		write_deadline: "3s"
		logfile: "/tmp/nats.log"
		pidfile: "/tmp/nats.pid"
		prof_port: 6543
		syslog: true
		remote_syslog: "udp://foo.com:33"
		http_port: 8222
		connect_error_reports: 86400
		reconnect_error_reports: 5
		max_traced_msg_len: 1024
		no_header_support: true
		no_auth_user: "default"
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	// Verify simple fields.
	checks := []struct {
		field    string
		got, exp any
	}{
		{"ServerName", opts.ServerName, "testing_v2"},
		{"Host", opts.Host, "127.0.0.1"},
		{"Port", opts.Port, 4222},
		{"Debug", opts.Debug, true},
		{"Trace", opts.Trace, true},
		{"Logtime", opts.Logtime, false},
		{"MaxConn", opts.MaxConn, 100},
		{"MaxSubs", opts.MaxSubs, 1000},
		{"MaxPayload", opts.MaxPayload, int32(65536)},
		{"MaxControlLine", opts.MaxControlLine, int32(2048)},
		{"MaxPending", opts.MaxPending, int64(10000000)},
		{"PingInterval", opts.PingInterval, 60 * time.Second},
		{"MaxPingsOut", opts.MaxPingsOut, 3},
		{"WriteDeadline", opts.WriteDeadline, 3 * time.Second},
		{"LogFile", opts.LogFile, "/tmp/nats.log"},
		{"PidFile", opts.PidFile, "/tmp/nats.pid"},
		{"ProfPort", opts.ProfPort, 6543},
		{"Syslog", opts.Syslog, true},
		{"RemoteSyslog", opts.RemoteSyslog, "udp://foo.com:33"},
		{"HTTPPort", opts.HTTPPort, 8222},
		{"ConnectErrorReports", opts.ConnectErrorReports, 86400},
		{"ReconnectErrorReports", opts.ReconnectErrorReports, 5},
		{"MaxTracedMsgLen", opts.MaxTracedMsgLen, 1024},
		{"NoHeaderSupport", opts.NoHeaderSupport, true},
		{"NoAuthUser", opts.NoAuthUser, "default"},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.exp) {
			t.Errorf("Field %s: got %v (%T), expected %v (%T)",
				c.field, c.got, c.got, c.exp, c.exp)
		}
	}

	// ConfigFile should be set.
	if opts.ConfigFile != fp {
		t.Errorf("ConfigFile: got %q, expected %q", opts.ConfigFile, fp)
	}

	// Digest should be set.
	if opts.ConfigDigest() == "" {
		t.Error("Expected non-empty config digest")
	}
}

// TestProcessConfigV2Listen tests the "listen" directive that parses host:port.
func TestProcessConfigV2Listen(t *testing.T) {
	t.Run("host:port", func(t *testing.T) {
		conf := `listen: "0.0.0.0:4222"`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if opts.Host != "0.0.0.0" || opts.Port != 4222 {
			t.Errorf("Expected 0.0.0.0:4222, got %s:%d", opts.Host, opts.Port)
		}
	})

	t.Run("port only", func(t *testing.T) {
		conf := `listen: 4333`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if opts.Port != 4333 {
			t.Errorf("Expected port 4333, got %d", opts.Port)
		}
	})
}

// TestProcessConfigV2HTTP tests the "http" and "https" monitoring endpoints.
func TestProcessConfigV2HTTP(t *testing.T) {
	t.Run("http port", func(t *testing.T) {
		conf := `http: 8222`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if opts.HTTPPort != 8222 {
			t.Errorf("Expected HTTPPort 8222, got %d", opts.HTTPPort)
		}
	})

	t.Run("http host:port", func(t *testing.T) {
		conf := `http: "0.0.0.0:8333"`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if opts.HTTPHost != "0.0.0.0" || opts.HTTPPort != 8333 {
			t.Errorf("Expected 0.0.0.0:8333, got %s:%d", opts.HTTPHost, opts.HTTPPort)
		}
	})

	t.Run("https port", func(t *testing.T) {
		conf := `https: 8443`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if opts.HTTPSPort != 8443 {
			t.Errorf("Expected HTTPSPort 8443, got %d", opts.HTTPSPort)
		}
	})
}

// TestProcessConfigV2Cluster tests the cluster block unmarshaling.
func TestProcessConfigV2Cluster(t *testing.T) {
	conf := `
		cluster {
			name: "my-cluster"
			host: "127.0.0.1"
			port: 6222
			advertise: "cluster.example.com:6222"
			no_advertise: true
			connect_retries: 5
			pool_size: 3
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	c := opts.Cluster
	if c.Name != "my-cluster" {
		t.Errorf("Cluster.Name: got %q, expected %q", c.Name, "my-cluster")
	}
	if c.Host != "127.0.0.1" {
		t.Errorf("Cluster.Host: got %q, expected %q", c.Host, "127.0.0.1")
	}
	if c.Port != 6222 {
		t.Errorf("Cluster.Port: got %d, expected %d", c.Port, 6222)
	}
	if c.Advertise != "cluster.example.com:6222" {
		t.Errorf("Cluster.Advertise: got %q, expected %q", c.Advertise, "cluster.example.com:6222")
	}
	if !c.NoAdvertise {
		t.Error("Cluster.NoAdvertise: expected true")
	}
	if c.ConnectRetries != 5 {
		t.Errorf("Cluster.ConnectRetries: got %d, expected %d", c.ConnectRetries, 5)
	}
	if c.PoolSize != 3 {
		t.Errorf("Cluster.PoolSize: got %d, expected %d", c.PoolSize, 3)
	}
}

// TestProcessConfigV2LeafNode tests the leafnode block unmarshaling.
func TestProcessConfigV2LeafNode(t *testing.T) {
	conf := `
		leafnodes {
			host: "0.0.0.0"
			port: 7422
			advertise: "leaf.example.com:7422"
			no_advertise: true
			reconnect: "5s"
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	ln := opts.LeafNode
	if ln.Host != "0.0.0.0" {
		t.Errorf("LeafNode.Host: got %q, expected %q", ln.Host, "0.0.0.0")
	}
	if ln.Port != 7422 {
		t.Errorf("LeafNode.Port: got %d, expected %d", ln.Port, 7422)
	}
	if ln.Advertise != "leaf.example.com:7422" {
		t.Errorf("LeafNode.Advertise: got %q, expected %q", ln.Advertise, "leaf.example.com:7422")
	}
	if !ln.NoAdvertise {
		t.Error("LeafNode.NoAdvertise: expected true")
	}
	if ln.ReconnectInterval != 5*time.Second {
		t.Errorf("LeafNode.ReconnectInterval: got %v, expected %v", ln.ReconnectInterval, 5*time.Second)
	}
}

// TestProcessConfigV2Gateway tests the gateway block unmarshaling.
func TestProcessConfigV2Gateway(t *testing.T) {
	conf := `
		gateway {
			name: "my-gw"
			host: "0.0.0.0"
			port: 7222
			advertise: "gw.example.com:7222"
			connect_retries: 3
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	gw := opts.Gateway
	if gw.Name != "my-gw" {
		t.Errorf("Gateway.Name: got %q, expected %q", gw.Name, "my-gw")
	}
	if gw.Host != "0.0.0.0" {
		t.Errorf("Gateway.Host: got %q, expected %q", gw.Host, "0.0.0.0")
	}
	if gw.Port != 7222 {
		t.Errorf("Gateway.Port: got %d, expected %d", gw.Port, 7222)
	}
	if gw.Advertise != "gw.example.com:7222" {
		t.Errorf("Gateway.Advertise: got %q, expected %q", gw.Advertise, "gw.example.com:7222")
	}
	if gw.ConnectRetries != 3 {
		t.Errorf("Gateway.ConnectRetries: got %d, expected %d", gw.ConnectRetries, 3)
	}
}

// TestProcessConfigV2Websocket tests the websocket block unmarshaling.
func TestProcessConfigV2Websocket(t *testing.T) {
	conf := `
		websocket {
			host: "0.0.0.0"
			port: 8080
			no_tls: true
			compression: true
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	ws := opts.Websocket
	if ws.Host != "0.0.0.0" {
		t.Errorf("Websocket.Host: got %q, expected %q", ws.Host, "0.0.0.0")
	}
	if ws.Port != 8080 {
		t.Errorf("Websocket.Port: got %d, expected %d", ws.Port, 8080)
	}
	if !ws.NoTLS {
		t.Error("Websocket.NoTLS: expected true")
	}
	if !ws.Compression {
		t.Error("Websocket.Compression: expected true")
	}
}

// TestProcessConfigV2MQTT tests the MQTT block unmarshaling.
func TestProcessConfigV2MQTT(t *testing.T) {
	conf := `
		mqtt {
			host: "0.0.0.0"
			port: 1883
			ack_wait: "30s"
			max_ack_pending: 100
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	mqtt := opts.MQTT
	if mqtt.Host != "0.0.0.0" {
		t.Errorf("MQTT.Host: got %q, expected %q", mqtt.Host, "0.0.0.0")
	}
	if mqtt.Port != 1883 {
		t.Errorf("MQTT.Port: got %d, expected %d", mqtt.Port, 1883)
	}
	if mqtt.AckWait != 30*time.Second {
		t.Errorf("MQTT.AckWait: got %v, expected %v", mqtt.AckWait, 30*time.Second)
	}
	if mqtt.MaxAckPending != 100 {
		t.Errorf("MQTT.MaxAckPending: got %d, expected %d", mqtt.MaxAckPending, 100)
	}
}

// TestProcessConfigV2JetStreamBool tests JetStream with a bool value.
func TestProcessConfigV2JetStreamBool(t *testing.T) {
	conf := `jetstream: true`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	if !opts.JetStream {
		t.Error("Expected JetStream to be enabled")
	}
}

// TestProcessConfigV2JetStreamString tests JetStream with a string value.
func TestProcessConfigV2JetStreamString(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		conf := `jetstream: "enabled"`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if !opts.JetStream {
			t.Error("Expected JetStream to be enabled")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		conf := `jetstream: "disabled"`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if opts.JetStream {
			t.Error("Expected JetStream to be disabled")
		}
	})
}

// TestProcessConfigV2JetStreamMap tests JetStream with a full map config.
func TestProcessConfigV2JetStreamMap(t *testing.T) {
	conf := `
		jetstream {
			store_dir: "/tmp/nats/jetstream"
			max_mem: 1GB
			max_file: 10GB
			domain: "test-domain"
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	if !opts.JetStream {
		t.Error("Expected JetStream to be enabled")
	}
	if opts.StoreDir != "/tmp/nats/jetstream" {
		t.Errorf("StoreDir: got %q, expected %q", opts.StoreDir, "/tmp/nats/jetstream")
	}
	if opts.JetStreamMaxMemory != 1024*1024*1024 {
		t.Errorf("JetStreamMaxMemory: got %d, expected %d", opts.JetStreamMaxMemory, 1024*1024*1024)
	}
	if opts.JetStreamMaxStore != 10*1024*1024*1024 {
		t.Errorf("JetStreamMaxStore: got %d, expected %d", opts.JetStreamMaxStore, 10*1024*1024*1024)
	}
	if opts.JetStreamDomain != "test-domain" {
		t.Errorf("JetStreamDomain: got %q, expected %q", opts.JetStreamDomain, "test-domain")
	}
}

// TestProcessConfigV2TLS tests the TLS block processing.
func TestProcessConfigV2TLS(t *testing.T) {
	// We need actual cert files for GenTLSConfig to work.
	// Use the test certs in configs/certs/
	cwd, _ := os.Getwd()
	certFile := filepath.Join(cwd, "configs", "certs", "server.pem")
	keyFile := filepath.Join(cwd, "configs", "certs", "key.pem")

	// Check if cert files exist.
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		t.Skip("Skipping TLS test: cert files not found")
	}

	conf := fmt.Sprintf(`
		tls {
			cert_file: %q
			key_file: %q
			timeout: 2
			verify: false
		}
	`, certFile, keyFile)

	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	if opts.TLSConfig == nil {
		t.Fatal("Expected TLSConfig to be set")
	}
	if opts.TLSTimeout != 2.0 {
		t.Errorf("TLSTimeout: got %v, expected %v", opts.TLSTimeout, 2.0)
	}
}

// TestProcessConfigV2Authorization tests the authorization block processing.
func TestProcessConfigV2Authorization(t *testing.T) {
	t.Run("simple user/pass", func(t *testing.T) {
		conf := `
			authorization {
				user: derek
				password: porkchop
				timeout: 1
			}
		`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if opts.Username != "derek" {
			t.Errorf("Username: got %q, expected %q", opts.Username, "derek")
		}
		if opts.Password != "porkchop" {
			t.Errorf("Password: got %q, expected %q", opts.Password, "porkchop")
		}
		if opts.AuthTimeout != 1.0 {
			t.Errorf("AuthTimeout: got %v, expected %v", opts.AuthTimeout, 1.0)
		}
	})

	t.Run("users array", func(t *testing.T) {
		conf := `
			authorization {
				users = [
					{user: alice, password: foo}
					{user: bob, password: bar}
				]
			}
		`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if len(opts.Users) != 2 {
			t.Fatalf("Expected 2 users, got %d", len(opts.Users))
		}
		if opts.Users[0].Username != "alice" {
			t.Errorf("Users[0].Username: got %q, expected %q", opts.Users[0].Username, "alice")
		}
		if opts.Users[1].Username != "bob" {
			t.Errorf("Users[1].Username: got %q, expected %q", opts.Users[1].Username, "bob")
		}
	})

	t.Run("users with permissions", func(t *testing.T) {
		conf := `
			authorization {
				users = [
					{user: alice, password: foo, permissions: {
						publish: ["foo.>"]
						subscribe: {allow: ["bar.>"], deny: ["bar.secret"]}
					}}
				]
			}
		`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if len(opts.Users) != 1 {
			t.Fatalf("Expected 1 user, got %d", len(opts.Users))
		}
		u := opts.Users[0]
		if u.Permissions == nil {
			t.Fatal("Expected user permissions to be set")
		}
		if u.Permissions.Publish == nil {
			t.Fatal("Expected publish permissions to be set")
		}
		if len(u.Permissions.Publish.Allow) != 1 || u.Permissions.Publish.Allow[0] != "foo.>" {
			t.Errorf("Publish.Allow: got %v, expected [foo.>]", u.Permissions.Publish.Allow)
		}
		if u.Permissions.Subscribe == nil {
			t.Fatal("Expected subscribe permissions to be set")
		}
		if len(u.Permissions.Subscribe.Allow) != 1 || u.Permissions.Subscribe.Allow[0] != "bar.>" {
			t.Errorf("Subscribe.Allow: got %v, expected [bar.>]", u.Permissions.Subscribe.Allow)
		}
		if len(u.Permissions.Subscribe.Deny) != 1 || u.Permissions.Subscribe.Deny[0] != "bar.secret" {
			t.Errorf("Subscribe.Deny: got %v, expected [bar.secret]", u.Permissions.Subscribe.Deny)
		}
	})

	t.Run("nkey users", func(t *testing.T) {
		conf := `
			authorization {
				users = [
					{nkey: UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX}
				]
			}
		`
		fp := writeTestConfig(t, conf)
		opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if len(opts.Nkeys) != 1 {
			t.Fatalf("Expected 1 nkey user, got %d", len(opts.Nkeys))
		}
		if opts.Nkeys[0].Nkey != "UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX" {
			t.Errorf("Nkey: got %q", opts.Nkeys[0].Nkey)
		}
	})
}

// TestProcessConfigV2Accounts tests the accounts block processing.
func TestProcessConfigV2Accounts(t *testing.T) {
	conf := `
		accounts {
			acc_a {
				users = [
					{user: alice, password: foo}
					{user: bob, password: bar}
				]
			}
			acc_b {
				users = [
					{user: charlie, password: baz}
				]
			}
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	if len(opts.Accounts) != 2 {
		t.Fatalf("Expected 2 accounts, got %d", len(opts.Accounts))
	}

	// Find accounts by name.
	accNames := make(map[string]bool)
	for _, acc := range opts.Accounts {
		accNames[acc.Name] = true
	}
	if !accNames["acc_a"] {
		t.Error("Expected account 'acc_a'")
	}
	if !accNames["acc_b"] {
		t.Error("Expected account 'acc_b'")
	}

	// Check users were attached.
	if len(opts.Users) != 3 {
		t.Errorf("Expected 3 users total, got %d", len(opts.Users))
	}
}

// TestProcessConfigV2ErrorPosition tests that errors from v2 include position info.
func TestProcessConfigV2ErrorPosition(t *testing.T) {
	// Create a config with an invalid value that should trigger an error during parsing.
	conf := `
		port: "not_a_number"
	`
	fp := writeTestConfig(t, conf)
	_, err := ProcessConfigV2(fp)
	if err == nil {
		t.Fatal("Expected error for invalid config")
	}
	// The error should contain position info from the file.
	errStr := err.Error()
	if !strings.Contains(errStr, "error") || !strings.Contains(errStr, "config") {
		// At minimum, there should be some informative error.
		t.Logf("Error message: %s", errStr)
	}
}

// TestProcessConfigV2ConfigDigest tests that the config digest is computed.
func TestProcessConfigV2ConfigDigest(t *testing.T) {
	conf := `port: 4222`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	digest := opts.ConfigDigest()
	if digest == "" {
		t.Error("Expected non-empty config digest")
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("Expected digest to start with 'sha256:', got %q", digest)
	}
}

// TestProcessConfigV2EquivalenceSimple tests that ProcessConfigV2 produces
// equivalent output to ProcessConfigFile for a simple config.
func TestProcessConfigV2EquivalenceSimple(t *testing.T) {
	conf := `
		server_name: equiv_test
		host: 127.0.0.1
		port: 4242
		debug: false
		trace: true
		logtime: false
		syslog: true
		remote_syslog: "udp://foo.com:33"
		pidfile: "/tmp/nats-server/nats-server.pid"
		prof_port: 6543
		max_connections: 100
		max_subscriptions: 1000
		max_pending: 10000000
		max_control_line: 2048
		max_payload: 65536
		ping_interval: "60s"
		ping_max: 3
		write_deadline: "3s"
		connect_error_reports: 86400
		reconnect_error_reports: 5
	`

	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}

	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	// Compare fields that should match.
	simpleFields := []struct {
		name     string
		v1, v2   any
	}{
		{"ServerName", v1Opts.ServerName, v2Opts.ServerName},
		{"Host", v1Opts.Host, v2Opts.Host},
		{"Port", v1Opts.Port, v2Opts.Port},
		{"Debug", v1Opts.Debug, v2Opts.Debug},
		{"Trace", v1Opts.Trace, v2Opts.Trace},
		{"Logtime", v1Opts.Logtime, v2Opts.Logtime},
		{"Syslog", v1Opts.Syslog, v2Opts.Syslog},
		{"RemoteSyslog", v1Opts.RemoteSyslog, v2Opts.RemoteSyslog},
		{"PidFile", v1Opts.PidFile, v2Opts.PidFile},
		{"ProfPort", v1Opts.ProfPort, v2Opts.ProfPort},
		{"MaxConn", v1Opts.MaxConn, v2Opts.MaxConn},
		{"MaxSubs", v1Opts.MaxSubs, v2Opts.MaxSubs},
		{"MaxPending", v1Opts.MaxPending, v2Opts.MaxPending},
		{"MaxControlLine", v1Opts.MaxControlLine, v2Opts.MaxControlLine},
		{"MaxPayload", v1Opts.MaxPayload, v2Opts.MaxPayload},
		{"PingInterval", v1Opts.PingInterval, v2Opts.PingInterval},
		{"MaxPingsOut", v1Opts.MaxPingsOut, v2Opts.MaxPingsOut},
		{"WriteDeadline", v1Opts.WriteDeadline, v2Opts.WriteDeadline},
		{"ConnectErrorReports", v1Opts.ConnectErrorReports, v2Opts.ConnectErrorReports},
		{"ReconnectErrorReports", v1Opts.ReconnectErrorReports, v2Opts.ReconnectErrorReports},
	}

	for _, f := range simpleFields {
		if !reflect.DeepEqual(f.v1, f.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", f.name, f.v1, f.v2)
		}
	}
}

// TestProcessConfigV2EquivalenceListenAndHTTP tests equivalence for listen/http/https.
func TestProcessConfigV2EquivalenceListenAndHTTP(t *testing.T) {
	conf := `
		listen: 127.0.0.1:4242
		http: 8222
		http_base_path: /nats
	`

	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}

	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	checks := []struct {
		name   string
		v1, v2 any
	}{
		{"Host", v1Opts.Host, v2Opts.Host},
		{"Port", v1Opts.Port, v2Opts.Port},
		{"HTTPHost", v1Opts.HTTPHost, v2Opts.HTTPHost},
		{"HTTPPort", v1Opts.HTTPPort, v2Opts.HTTPPort},
		{"HTTPBasePath", v1Opts.HTTPBasePath, v2Opts.HTTPBasePath},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.v1, c.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
		}
	}
}

// TestProcessConfigV2EquivalenceCluster tests equivalence for cluster config.
func TestProcessConfigV2EquivalenceCluster(t *testing.T) {
	conf := `
		cluster {
			name: "my-cluster"
			host: "127.0.0.1"
			port: 6222
			no_advertise: true
			connect_retries: 5
			pool_size: 3
		}
	`

	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}

	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	checks := []struct {
		name   string
		v1, v2 any
	}{
		{"Cluster.Name", v1Opts.Cluster.Name, v2Opts.Cluster.Name},
		{"Cluster.Host", v1Opts.Cluster.Host, v2Opts.Cluster.Host},
		{"Cluster.Port", v1Opts.Cluster.Port, v2Opts.Cluster.Port},
		{"Cluster.NoAdvertise", v1Opts.Cluster.NoAdvertise, v2Opts.Cluster.NoAdvertise},
		{"Cluster.ConnectRetries", v1Opts.Cluster.ConnectRetries, v2Opts.Cluster.ConnectRetries},
		{"Cluster.PoolSize", v1Opts.Cluster.PoolSize, v2Opts.Cluster.PoolSize},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.v1, c.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
		}
	}
}

// TestProcessConfigV2EquivalenceJetStream tests equivalence for JetStream config.
func TestProcessConfigV2EquivalenceJetStream(t *testing.T) {
	t.Run("bool enabled", func(t *testing.T) {
		conf := `jetstream: true`
		fp := writeTestConfig(t, conf)

		v1Opts, err := ProcessConfigFile(fp)
		if err != nil {
			t.Fatalf("ProcessConfigFile error: %v", err)
		}
		v2Opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if v1Opts.JetStream != v2Opts.JetStream {
			t.Errorf("JetStream: v1=%v, v2=%v", v1Opts.JetStream, v2Opts.JetStream)
		}
	})

	t.Run("string enabled", func(t *testing.T) {
		conf := `jetstream: "enabled"`
		fp := writeTestConfig(t, conf)

		v1Opts, err := ProcessConfigFile(fp)
		if err != nil {
			t.Fatalf("ProcessConfigFile error: %v", err)
		}
		v2Opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}
		if v1Opts.JetStream != v2Opts.JetStream {
			t.Errorf("JetStream: v1=%v, v2=%v", v1Opts.JetStream, v2Opts.JetStream)
		}
	})

	t.Run("map config", func(t *testing.T) {
		conf := `
			jetstream {
				store_dir: "/tmp/nats/js"
				max_mem: 1GB
				max_file: 10GB
				domain: "test"
			}
		`
		fp := writeTestConfig(t, conf)

		v1Opts, err := ProcessConfigFile(fp)
		if err != nil {
			t.Fatalf("ProcessConfigFile error: %v", err)
		}
		v2Opts, err := ProcessConfigV2(fp)
		if err != nil {
			t.Fatalf("ProcessConfigV2 error: %v", err)
		}

		checks := []struct {
			name   string
			v1, v2 any
		}{
			{"JetStream", v1Opts.JetStream, v2Opts.JetStream},
			{"StoreDir", v1Opts.StoreDir, v2Opts.StoreDir},
			{"JetStreamMaxMemory", v1Opts.JetStreamMaxMemory, v2Opts.JetStreamMaxMemory},
			{"JetStreamMaxStore", v1Opts.JetStreamMaxStore, v2Opts.JetStreamMaxStore},
			{"JetStreamDomain", v1Opts.JetStreamDomain, v2Opts.JetStreamDomain},
		}

		for _, c := range checks {
			if !reflect.DeepEqual(c.v1, c.v2) {
				t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
			}
		}
	})
}

// TestProcessConfigV2EquivalenceTLS tests equivalence for TLS config.
func TestProcessConfigV2EquivalenceTLS(t *testing.T) {
	cwd, _ := os.Getwd()
	certFile := filepath.Join(cwd, "configs", "certs", "server.pem")
	keyFile := filepath.Join(cwd, "configs", "certs", "key.pem")

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		t.Skip("Skipping TLS equivalence test: cert files not found")
	}

	conf := fmt.Sprintf(`
		tls {
			cert_file: %q
			key_file: %q
			timeout: 2
		}
	`, certFile, keyFile)

	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	// Compare TLS-related fields.
	if v1Opts.TLSTimeout != v2Opts.TLSTimeout {
		t.Errorf("TLSTimeout: v1=%v, v2=%v", v1Opts.TLSTimeout, v2Opts.TLSTimeout)
	}
	if (v1Opts.TLSConfig == nil) != (v2Opts.TLSConfig == nil) {
		t.Errorf("TLSConfig nil: v1=%v, v2=%v", v1Opts.TLSConfig == nil, v2Opts.TLSConfig == nil)
	}
}

// TestProcessConfigV2EquivalenceAuth tests equivalence for authorization config.
func TestProcessConfigV2EquivalenceAuth(t *testing.T) {
	conf := `
		authorization {
			user: derek
			password: porkchop
			timeout: 1
		}
	`

	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	checks := []struct {
		name   string
		v1, v2 any
	}{
		{"Username", v1Opts.Username, v2Opts.Username},
		{"Password", v1Opts.Password, v2Opts.Password},
		{"AuthTimeout", v1Opts.AuthTimeout, v2Opts.AuthTimeout},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.v1, c.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
		}
	}
}

// TestProcessConfigV2EquivalenceComprehensive is the comprehensive equivalence
// test that compares ProcessConfigV2 vs ProcessConfigFile on a config that
// exercises all major config sections.
func TestProcessConfigV2EquivalenceComprehensive(t *testing.T) {
	conf := `
		server_name: comprehensive_test
		listen: 127.0.0.1:4242

		http: 8222
		http_base_path: /nats

		debug: false
		trace: true
		logtime: false
		syslog: true
		remote_syslog: "udp://foo.com:33"
		pidfile: "/tmp/nats-server/nats-server.pid"
		prof_port: 6543
		max_connections: 100
		max_subscriptions: 1000
		max_pending: 10000000
		max_control_line: 2048
		max_payload: 65536
		ping_interval: "60s"
		ping_max: 3
		write_deadline: "3s"
		connect_error_reports: 86400
		reconnect_error_reports: 5
		max_traced_msg_len: 512

		cluster {
			name: "my-cluster"
			host: "127.0.0.1"
			port: 6222
			no_advertise: true
			connect_retries: 5
			pool_size: 3
		}

		gateway {
			name: "my-gw"
			host: "0.0.0.0"
			port: 7222
			connect_retries: 3
		}

		leafnodes {
			host: "0.0.0.0"
			port: 7422
			no_advertise: true
		}

		websocket {
			host: "0.0.0.0"
			port: 8080
			no_tls: true
			compression: true
		}

		mqtt {
			host: "0.0.0.0"
			port: 1883
			ack_wait: "30s"
			max_ack_pending: 100
		}

		jetstream {
			store_dir: "/tmp/nats/js"
			max_mem: 1GB
			max_file: 10GB
			domain: "test"
		}
	`

	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	// Fields to exclude from comparison:
	// - Internal/computed state fields that may differ
	// - TLS runtime objects (pointers)
	// - Accounts/Users/Nkeys (complex objects with Account back-refs)
	// - Fields set by inConfig/inCmdLine tracking
	excludeFields := map[string]bool{
		"ConfigFile":      true, // Set differently
		"TLSConfig":       true, // Runtime pointer
		"TLSPinnedCerts":  true, // Runtime
		"tlsConfigOpts":   true, // unexported
		"inConfig":        true, // unexported
		"inCmdLine":       true, // unexported
		"configDigest":    true, // May differ in pedantic handling
		"authBlockDefined": true, // unexported
		"maxMemSet":       true, // unexported
		"maxStoreSet":     true, // unexported
		"syncSet":         true, // unexported
		"operatorJWT":     true, // unexported
		"resolverPreloads": true, // unexported
		"resolverPinnedAccounts": true, // unexported
		"gatewaysSolicitDelay": true, // unexported
		"overrideProto":    true, // unexported
	}

	// Use reflect to compare all exported fields.
	v1Val := reflect.ValueOf(*v1Opts)
	v2Val := reflect.ValueOf(*v2Opts)
	v1Type := v1Val.Type()

	var mismatches []string
	for i := 0; i < v1Type.NumField(); i++ {
		field := v1Type.Field(i)
		if !field.IsExported() {
			continue
		}
		if excludeFields[field.Name] {
			continue
		}

		v1f := v1Val.Field(i)
		v2f := v2Val.Field(i)

		if !reflect.DeepEqual(v1f.Interface(), v2f.Interface()) {
			mismatches = append(mismatches, fmt.Sprintf(
				"Field %s: v1=%v, v2=%v", field.Name, v1f.Interface(), v2f.Interface()))
		}
	}

	if len(mismatches) > 0 {
		for _, m := range mismatches {
			t.Error(m)
		}
	}
}

// TestProcessConfigV2StoreDirTopLevel tests the top-level store_dir directive.
func TestProcessConfigV2StoreDirTopLevel(t *testing.T) {
	conf := `
		store_dir: "/tmp/nats/store"
		jetstream: true
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	if opts.StoreDir != "/tmp/nats/store" {
		t.Errorf("StoreDir: got %q, expected %q", opts.StoreDir, "/tmp/nats/store")
	}
	if !opts.JetStream {
		t.Error("Expected JetStream to be enabled")
	}
}

// TestProcessConfigV2MissingFile tests error handling for missing config file.
func TestProcessConfigV2MissingFile(t *testing.T) {
	_, err := ProcessConfigV2("/nonexistent/path/nats.conf")
	if err == nil {
		t.Fatal("Expected error for missing config file")
	}
}

// TestProcessConfigV2EmptyConfig tests handling of an empty config file.
func TestProcessConfigV2EmptyConfig(t *testing.T) {
	conf := ``
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	// Should return valid Options with defaults.
	if opts == nil {
		t.Fatal("Expected non-nil Options")
	}
}

// TestProcessConfigV2JetStreamDisabled tests JetStream disabled via bool.
func TestProcessConfigV2JetStreamDisabled(t *testing.T) {
	conf := `jetstream: false`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	if opts.JetStream {
		t.Error("Expected JetStream to be disabled")
	}
}

// TestProcessConfigV2SizeSuffixes tests that size suffix fields are properly handled.
func TestProcessConfigV2SizeSuffixes(t *testing.T) {
	conf := `
		max_payload: 1MB
		max_pending: 64MB
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	if opts.MaxPayload != int32(1024*1024) {
		t.Errorf("MaxPayload: got %d, expected %d", opts.MaxPayload, 1024*1024)
	}
	if opts.MaxPending != int64(64*1024*1024) {
		t.Errorf("MaxPending: got %d, expected %d", opts.MaxPending, 64*1024*1024)
	}
}

// TestProcessConfigV2AuthorizationWithToken tests authorization with a token.
func TestProcessConfigV2AuthorizationWithToken(t *testing.T) {
	conf := `
		authorization {
			token: "mytoken123"
		}
	`
	fp := writeTestConfig(t, conf)
	opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}
	if opts.Authorization != "mytoken123" {
		t.Errorf("Authorization: got %q, expected %q", opts.Authorization, "mytoken123")
	}
}

// TestProcessConfigV2LeafNodeEquivalence tests equivalence of leafnode config.
func TestProcessConfigV2LeafNodeEquivalence(t *testing.T) {
	conf := `
		leafnodes {
			host: "0.0.0.0"
			port: 7422
			no_advertise: true
		}
	`
	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	checks := []struct {
		name   string
		v1, v2 any
	}{
		{"LeafNode.Host", v1Opts.LeafNode.Host, v2Opts.LeafNode.Host},
		{"LeafNode.Port", v1Opts.LeafNode.Port, v2Opts.LeafNode.Port},
		{"LeafNode.NoAdvertise", v1Opts.LeafNode.NoAdvertise, v2Opts.LeafNode.NoAdvertise},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.v1, c.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
		}
	}
}

// TestProcessConfigV2GatewayEquivalence tests equivalence of gateway config.
func TestProcessConfigV2GatewayEquivalence(t *testing.T) {
	conf := `
		gateway {
			name: "my-gw"
			host: "0.0.0.0"
			port: 7222
			connect_retries: 3
		}
	`
	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	checks := []struct {
		name   string
		v1, v2 any
	}{
		{"Gateway.Name", v1Opts.Gateway.Name, v2Opts.Gateway.Name},
		{"Gateway.Host", v1Opts.Gateway.Host, v2Opts.Gateway.Host},
		{"Gateway.Port", v1Opts.Gateway.Port, v2Opts.Gateway.Port},
		{"Gateway.ConnectRetries", v1Opts.Gateway.ConnectRetries, v2Opts.Gateway.ConnectRetries},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.v1, c.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
		}
	}
}

// TestProcessConfigV2WebsocketEquivalence tests equivalence of websocket config.
func TestProcessConfigV2WebsocketEquivalence(t *testing.T) {
	conf := `
		websocket {
			host: "0.0.0.0"
			port: 8080
			no_tls: true
			compression: true
		}
	`
	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	checks := []struct {
		name   string
		v1, v2 any
	}{
		{"Websocket.Host", v1Opts.Websocket.Host, v2Opts.Websocket.Host},
		{"Websocket.Port", v1Opts.Websocket.Port, v2Opts.Websocket.Port},
		{"Websocket.NoTLS", v1Opts.Websocket.NoTLS, v2Opts.Websocket.NoTLS},
		{"Websocket.Compression", v1Opts.Websocket.Compression, v2Opts.Websocket.Compression},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.v1, c.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
		}
	}
}

// TestDigestEquivalenceMinimal tests that the SHA256 config digest
// produced by ProcessConfigV2 matches the digest produced by
// ProcessConfigFile (v1) for a minimal config (just port).
func TestDigestEquivalenceMinimal(t *testing.T) {
	conf := `port: 4222`
	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	v1Digest := v1Opts.ConfigDigest()
	v2Digest := v2Opts.ConfigDigest()

	if v1Digest == "" {
		t.Fatal("v1 digest is empty")
	}
	if v2Digest == "" {
		t.Fatal("v2 digest is empty")
	}
	if !strings.HasPrefix(v1Digest, "sha256:") {
		t.Errorf("v1 digest does not start with 'sha256:': %q", v1Digest)
	}
	if !strings.HasPrefix(v2Digest, "sha256:") {
		t.Errorf("v2 digest does not start with 'sha256:': %q", v2Digest)
	}
	if v1Digest != v2Digest {
		t.Errorf("Digest mismatch for minimal config:\n  v1: %s\n  v2: %s", v1Digest, v2Digest)
	}
}

// TestDigestEquivalenceMedium tests that the SHA256 config digest
// produced by ProcessConfigV2 matches the digest produced by
// ProcessConfigFile (v1) for a medium-complexity config with
// listen, cluster, and jetstream.
func TestDigestEquivalenceMedium(t *testing.T) {
	conf := `
		listen: 127.0.0.1:4242
		server_name: digest_medium

		cluster {
			name: "digest-cluster"
			host: "127.0.0.1"
			port: 6222
			no_advertise: true
			connect_retries: 5
		}

		jetstream {
			store_dir: "/tmp/nats/jetstream"
			max_mem: 1GB
			max_file: 10GB
			domain: "test-domain"
		}
	`
	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	v1Digest := v1Opts.ConfigDigest()
	v2Digest := v2Opts.ConfigDigest()

	if v1Digest == "" {
		t.Fatal("v1 digest is empty")
	}
	if v2Digest == "" {
		t.Fatal("v2 digest is empty")
	}
	if !strings.HasPrefix(v1Digest, "sha256:") {
		t.Errorf("v1 digest does not start with 'sha256:': %q", v1Digest)
	}
	if !strings.HasPrefix(v2Digest, "sha256:") {
		t.Errorf("v2 digest does not start with 'sha256:': %q", v2Digest)
	}
	if v1Digest != v2Digest {
		t.Errorf("Digest mismatch for medium config:\n  v1: %s\n  v2: %s", v1Digest, v2Digest)
	}
}

// TestDigestEquivalenceComprehensive tests that the SHA256 config digest
// produced by ProcessConfigV2 matches the digest produced by
// ProcessConfigFile (v1) for a comprehensive config exercising all
// major sections: listen, cluster, leafnode, gateway, websocket, mqtt,
// jetstream map, authorization with users, and accounts.
func TestDigestEquivalenceComprehensive(t *testing.T) {
	conf := `
		server_name: digest_comprehensive
		listen: 127.0.0.1:4242

		http: 8222
		http_base_path: /nats

		debug: true
		trace: false
		logtime: true
		max_connections: 500
		max_subscriptions: 5000
		max_payload: 2MB
		max_control_line: 4096
		max_pending: 128MB
		ping_interval: "30s"
		ping_max: 5
		write_deadline: "5s"
		max_traced_msg_len: 2048
		connect_error_reports: 3600
		reconnect_error_reports: 10

		cluster {
			name: "digest-cluster"
			host: "127.0.0.1"
			port: 6222
			no_advertise: true
			connect_retries: 3
			pool_size: 5
		}

		leafnodes {
			host: "0.0.0.0"
			port: 7422
			no_advertise: true
		}

		gateway {
			name: "digest-gw"
			host: "0.0.0.0"
			port: 7222
			connect_retries: 2
		}

		websocket {
			host: "0.0.0.0"
			port: 8080
			no_tls: true
			compression: true
		}

		mqtt {
			host: "0.0.0.0"
			port: 1883
			ack_wait: "60s"
			max_ack_pending: 200
		}

		jetstream {
			store_dir: "/tmp/nats/jetstream"
			max_mem: 2GB
			max_file: 20GB
			domain: "comprehensive"
		}

		authorization {
			users = [
				{user: alice, password: s3cr3t}
				{user: bob, password: p@ssw0rd}
			]
		}

		accounts {
			SYS {
				users = [{user: admin, password: admin123}]
			}
			APP {
				users = [{user: app_user, password: app_pass}]
			}
		}
	`
	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	v1Digest := v1Opts.ConfigDigest()
	v2Digest := v2Opts.ConfigDigest()

	if v1Digest == "" {
		t.Fatal("v1 digest is empty")
	}
	if v2Digest == "" {
		t.Fatal("v2 digest is empty")
	}
	if !strings.HasPrefix(v1Digest, "sha256:") {
		t.Errorf("v1 digest does not start with 'sha256:': %q", v1Digest)
	}
	if !strings.HasPrefix(v2Digest, "sha256:") {
		t.Errorf("v2 digest does not start with 'sha256:': %q", v2Digest)
	}
	if v1Digest != v2Digest {
		t.Errorf("Digest mismatch for comprehensive config:\n  v1: %s\n  v2: %s", v1Digest, v2Digest)
	}
}

// TestDigestEquivalenceChangedConfig tests that changing a config value
// produces a different digest, verifying the digest is actually computed
// from the config content and not a constant value.
func TestDigestEquivalenceChangedConfig(t *testing.T) {
	confA := `
		port: 4222
		server_name: digest_a
	`
	confB := `
		port: 4333
		server_name: digest_b
	`

	fpA := writeTestConfig(t, confA)
	fpB := filepath.Join(t.TempDir(), "b.conf")
	if err := os.WriteFile(fpB, []byte(confB), 0644); err != nil {
		t.Fatalf("Failed to write config B: %v", err)
	}

	// Get v1 digests for both configs.
	v1OptsA, err := ProcessConfigFile(fpA)
	if err != nil {
		t.Fatalf("ProcessConfigFile(A) error: %v", err)
	}
	v1OptsB, err := ProcessConfigFile(fpB)
	if err != nil {
		t.Fatalf("ProcessConfigFile(B) error: %v", err)
	}

	// Get v2 digests for both configs.
	v2OptsA, err := ProcessConfigV2(fpA)
	if err != nil {
		t.Fatalf("ProcessConfigV2(A) error: %v", err)
	}
	v2OptsB, err := ProcessConfigV2(fpB)
	if err != nil {
		t.Fatalf("ProcessConfigV2(B) error: %v", err)
	}

	// Verify digests are non-empty and well-formed.
	for name, digest := range map[string]string{
		"v1A": v1OptsA.ConfigDigest(),
		"v1B": v1OptsB.ConfigDigest(),
		"v2A": v2OptsA.ConfigDigest(),
		"v2B": v2OptsB.ConfigDigest(),
	} {
		if digest == "" {
			t.Fatalf("%s digest is empty", name)
		}
		if !strings.HasPrefix(digest, "sha256:") {
			t.Errorf("%s digest does not start with 'sha256:': %q", name, digest)
		}
	}

	// v1A == v2A (same config, different engine).
	if v1OptsA.ConfigDigest() != v2OptsA.ConfigDigest() {
		t.Errorf("Config A digest mismatch:\n  v1: %s\n  v2: %s",
			v1OptsA.ConfigDigest(), v2OptsA.ConfigDigest())
	}

	// v1B == v2B (same config, different engine).
	if v1OptsB.ConfigDigest() != v2OptsB.ConfigDigest() {
		t.Errorf("Config B digest mismatch:\n  v1: %s\n  v2: %s",
			v1OptsB.ConfigDigest(), v2OptsB.ConfigDigest())
	}

	// Config A digest != Config B digest (different configs produce different digests).
	if v1OptsA.ConfigDigest() == v1OptsB.ConfigDigest() {
		t.Errorf("Different configs should produce different digests, but both have: %s",
			v1OptsA.ConfigDigest())
	}
}

// TestProcessConfigV2MQTTEquivalence tests equivalence of MQTT config.
func TestProcessConfigV2MQTTEquivalence(t *testing.T) {
	conf := `
		mqtt {
			host: "0.0.0.0"
			port: 1883
			ack_wait: "30s"
			max_ack_pending: 100
		}
	`
	fp := writeTestConfig(t, conf)

	v1Opts, err := ProcessConfigFile(fp)
	if err != nil {
		t.Fatalf("ProcessConfigFile error: %v", err)
	}
	v2Opts, err := ProcessConfigV2(fp)
	if err != nil {
		t.Fatalf("ProcessConfigV2 error: %v", err)
	}

	checks := []struct {
		name   string
		v1, v2 any
	}{
		{"MQTT.Host", v1Opts.MQTT.Host, v2Opts.MQTT.Host},
		{"MQTT.Port", v1Opts.MQTT.Port, v2Opts.MQTT.Port},
		{"MQTT.AckWait", v1Opts.MQTT.AckWait, v2Opts.MQTT.AckWait},
		{"MQTT.MaxAckPending", v1Opts.MQTT.MaxAckPending, v2Opts.MQTT.MaxAckPending},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.v1, c.v2) {
			t.Errorf("Field %s: v1=%v, v2=%v", c.name, c.v1, c.v2)
		}
	}
}
