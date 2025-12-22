// Copyright 2015-2025 The NATS Authors
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
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"
)

const PING_CLIENT_PORT = 11228

var DefaultPingOptions = Options{
	Host:         "127.0.0.1",
	Port:         PING_CLIENT_PORT,
	NoLog:        true,
	NoSigs:       true,
	PingInterval: 50 * time.Millisecond,
}

func TestPing(t *testing.T) {
	o := DefaultPingOptions
	o.DisableShortFirstPing = true
	s := RunServer(&o)
	defer s.Shutdown()

	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", PING_CLIENT_PORT))
	if err != nil {
		t.Fatalf("Error connecting: %v", err)
	}
	defer c.Close()
	br := bufio.NewReader(c)
	// Wait for INFO
	br.ReadLine()
	// Send CONNECT
	c.Write([]byte("CONNECT {\"verbose\":false}\r\nPING\r\n"))
	// Wait for first PONG
	br.ReadLine()
	// Wait for PING
	start := time.Now()
	for i := 0; i < 3; i++ {
		l, _, err := br.ReadLine()
		if err != nil {
			t.Fatalf("Error: %v", err)
		}
		if string(l) != "PING" {
			t.Fatalf("Expected PING, got %q", l)
		}
		if dur := time.Since(start); dur < 25*time.Millisecond || dur > 75*time.Millisecond {
			t.Fatalf("Pings duration off: %v", dur)
		}
		c.Write([]byte(pongProto))
		start = time.Now()
	}
}

func TestClientCustomPingInterval(t *testing.T) {
	// Server configured with 100ms ping interval
	// MinClientPingInterval set to 10ms for testing (default is 5s)
	o := Options{
		Host:                  "127.0.0.1",
		Port:                  -1,
		NoLog:                 true,
		NoSigs:                true,
		PingInterval:          100 * time.Millisecond,
		MinClientPingInterval: 10 * time.Millisecond, // Low minimum for testing
		DisableShortFirstPing: true,
	}
	s := RunServer(&o)
	defer s.Shutdown()

	// Test 1: Client requests a shorter ping interval (50ms) than server's (100ms)
	// The client's interval is >= MinClientPingInterval (10ms) so it should be used.
	t.Run("ShorterInterval", func(t *testing.T) {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Addr().(*net.TCPAddr).Port))
		if err != nil {
			t.Fatalf("Error connecting: %v", err)
		}
		defer c.Close()
		br := bufio.NewReader(c)
		// Wait for INFO
		br.ReadLine()
		// Send CONNECT with custom ping_interval of 50ms (50000000 nanoseconds)
		c.Write([]byte("CONNECT {\"verbose\":false,\"ping_interval\":50000000}\r\nPING\r\n"))
		// Wait for first PONG
		br.ReadLine()
		// Wait for PING - should come in ~50ms, not 100ms
		start := time.Now()
		l, _, err := br.ReadLine()
		if err != nil {
			t.Fatalf("Error: %v", err)
		}
		if string(l) != "PING" {
			t.Fatalf("Expected PING, got %q", l)
		}
		dur := time.Since(start)
		// With client interval of 50ms, we expect the ping around that time
		// Allow some tolerance for timing
		if dur < 25*time.Millisecond || dur > 75*time.Millisecond {
			t.Fatalf("Expected ping around 50ms, got: %v", dur)
		}
	})

	// Test 2: Client requests a longer ping interval than server's
	// Server should use its own interval (shorter one takes precedence)
	t.Run("LongerInterval", func(t *testing.T) {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Addr().(*net.TCPAddr).Port))
		if err != nil {
			t.Fatalf("Error connecting: %v", err)
		}
		defer c.Close()
		br := bufio.NewReader(c)
		// Wait for INFO
		br.ReadLine()
		// Send CONNECT with custom ping_interval of 500ms (longer than server's 100ms)
		c.Write([]byte("CONNECT {\"verbose\":false,\"ping_interval\":500000000}\r\nPING\r\n"))
		// Wait for first PONG
		br.ReadLine()
		// Wait for PING - should come in ~100ms (server's interval), not 500ms
		start := time.Now()
		l, _, err := br.ReadLine()
		if err != nil {
			t.Fatalf("Error: %v", err)
		}
		if string(l) != "PING" {
			t.Fatalf("Expected PING, got %q", l)
		}
		dur := time.Since(start)
		// Server interval of 100ms should be used
		if dur < 50*time.Millisecond || dur > 150*time.Millisecond {
			t.Fatalf("Expected ping around 100ms (server interval), got: %v", dur)
		}
	})

	// Test 3: Client does not specify ping_interval, server's interval is used
	t.Run("NoInterval", func(t *testing.T) {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Addr().(*net.TCPAddr).Port))
		if err != nil {
			t.Fatalf("Error connecting: %v", err)
		}
		defer c.Close()
		br := bufio.NewReader(c)
		// Wait for INFO
		br.ReadLine()
		// Send CONNECT without ping_interval
		c.Write([]byte("CONNECT {\"verbose\":false}\r\nPING\r\n"))
		// Wait for first PONG
		br.ReadLine()
		// Wait for PING - should come in ~100ms (server's interval)
		start := time.Now()
		l, _, err := br.ReadLine()
		if err != nil {
			t.Fatalf("Error: %v", err)
		}
		if string(l) != "PING" {
			t.Fatalf("Expected PING, got %q", l)
		}
		dur := time.Since(start)
		if dur < 50*time.Millisecond || dur > 150*time.Millisecond {
			t.Fatalf("Expected ping around 100ms (server interval), got: %v", dur)
		}
	})
}

func TestClientPingIntervalMinimum(t *testing.T) {
	// Server configured with ping interval of 200ms
	// MinClientPingInterval set to 100ms - client requests below this should be bumped up
	o := Options{
		Host:                  "127.0.0.1",
		Port:                  -1,
		NoLog:                 true,
		NoSigs:                true,
		PingInterval:          200 * time.Millisecond,
		MinClientPingInterval: 100 * time.Millisecond, // Minimum allowed
		DisableShortFirstPing: true,
	}
	s := RunServer(&o)
	defer s.Shutdown()

	// Test that minimum interval is enforced
	// Client requests 10ms, but MinClientPingInterval (100ms) should be used
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatalf("Error connecting: %v", err)
	}
	defer c.Close()

	br := bufio.NewReader(c)
	// Wait for INFO
	br.ReadLine()
	// Send CONNECT with very small ping_interval (10ms = 10000000ns)
	c.Write([]byte("CONNECT {\"verbose\":false,\"ping_interval\":10000000}\r\nPING\r\n"))
	// Wait for first PONG
	br.ReadLine()

	// Wait for PING - should come in ~100ms (MinClientPingInterval), not 10ms
	start := time.Now()
	l, _, err := br.ReadLine()
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	if string(l) != "PING" {
		t.Fatalf("Expected PING, got %q", l)
	}
	dur := time.Since(start)
	// Should be around 100ms (minimum), not 10ms or 200ms
	if dur < 50*time.Millisecond || dur > 150*time.Millisecond {
		t.Fatalf("Expected ping around 100ms (minimum), got: %v", dur)
	}
}
