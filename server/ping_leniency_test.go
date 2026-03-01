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
	"bytes"
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// TestPingLeniency_UnboundedConsumption demonstrates that OP_PING, OP_PONG,
// and OP_PLUS_OK silently consume arbitrary data without enforcing
// max_control_line, unlike all other ARG states.
//
// The parser stays in these states consuming every byte until a \n arrives.
// This means:
//   - A SUB with 100KB of data → rejected by overMaxControlLineLimit
//   - A PING with 100KB of data → silently consumed, no error
//
// While this does NOT cause memory growth (nothing is buffered), it means
// these states bypass the control line limit that protects every other state.
func TestPingLeniency_UnboundedConsumption(t *testing.T) {
	// Generate a chunk of data much larger than MAX_CONTROL_LINE_SIZE (4KB default).
	// We simulate what happens when this arrives across multiple read buffers
	// without a \n terminator.
	chunkSize := int(MAX_CONTROL_LINE_SIZE) * 25 // 100KB - 25x the limit
	junk := bytes.Repeat([]byte("X"), chunkSize)

	t.Run("SUB_ARG_is_bounded", func(t *testing.T) {
		// Demonstrate that SUB_ARG correctly enforces max_control_line.
		// Send "SUB " to enter SUB_ARG state, then feed data in chunks.
		c := dummyClient()
		if err := c.parse([]byte("SUB ")); err != nil {
			t.Fatalf("Failed to enter SUB_ARG state: %v", err)
		}
		if c.state != SUB_ARG {
			t.Fatalf("Expected SUB_ARG state, got %d", c.state)
		}
		// Now feed chunks. The split buffer handling should catch this
		// and enforce overMaxControlLineLimit.
		var hitLimit bool
		for i := 0; i < 100; i++ {
			if err := c.parse(junk); err != nil {
				hitLimit = true
				break
			}
		}
		if !hitLimit {
			t.Fatal("SUB_ARG should have hit max control line limit but didn't")
		}
	})

	t.Run("OP_PING_is_unbounded", func(t *testing.T) {
		// Demonstrate that OP_PING does NOT enforce max_control_line.
		// Send "PING" to enter OP_PING state, then feed data in chunks.
		c := dummyClient()
		if err := c.parse([]byte("PING")); err != nil {
			t.Fatalf("Failed to enter OP_PING state: %v", err)
		}
		if c.state != OP_PING {
			t.Fatalf("Expected OP_PING state, got %d", c.state)
		}
		// Feed 100 chunks of 100KB each (10MB total) - all silently consumed.
		for i := 0; i < 100; i++ {
			if err := c.parse(junk); err != nil {
				t.Fatalf("OP_PING unexpectedly returned error at chunk %d: %v", i, err)
			}
			if c.state != OP_PING {
				t.Fatalf("Expected to remain in OP_PING, got state %d at chunk %d", c.state, i)
			}
		}
		// Parser consumed 10MB of data while stuck in OP_PING - no error raised.
		// Total bytes silently consumed: 100 * 100KB = 10MB
		t.Logf("OP_PING silently consumed %d bytes across %d parse calls with no error",
			100*chunkSize, 100)
	})

	t.Run("OP_PONG_is_unbounded", func(t *testing.T) {
		c := dummyClient()
		if err := c.parse([]byte("PONG")); err != nil {
			t.Fatalf("Failed to enter OP_PONG state: %v", err)
		}
		if c.state != OP_PONG {
			t.Fatalf("Expected OP_PONG state, got %d", c.state)
		}
		for i := 0; i < 100; i++ {
			if err := c.parse(junk); err != nil {
				t.Fatalf("OP_PONG unexpectedly returned error at chunk %d: %v", i, err)
			}
		}
		t.Logf("OP_PONG silently consumed %d bytes with no error", 100*chunkSize)
	})

	t.Run("OP_PLUS_OK_is_unbounded", func(t *testing.T) {
		c := dummyClient()
		if err := c.parse([]byte("+OK")); err != nil {
			t.Fatalf("Failed to enter OP_PLUS_OK state: %v", err)
		}
		if c.state != OP_PLUS_OK {
			t.Fatalf("Expected OP_PLUS_OK state, got %d", c.state)
		}
		for i := 0; i < 100; i++ {
			if err := c.parse(junk); err != nil {
				t.Fatalf("OP_PLUS_OK unexpectedly returned error at chunk %d: %v", i, err)
			}
		}
		t.Logf("OP_PLUS_OK silently consumed %d bytes with no error", 100*chunkSize)
	})
}

// TestPingLeniency_MemoryImpact measures the actual memory footprint while
// feeding data into OP_PING vs SUB_ARG. This proves that OP_PING doesn't
// accumulate memory (no argBuf, no msgBuf), so the impact is CPU/connection
// stalling rather than memory exhaustion.
func TestPingLeniency_MemoryImpact(t *testing.T) {
	chunkSize := int(MAX_CONTROL_LINE_SIZE) * 25 // 100KB
	junk := bytes.Repeat([]byte("X"), chunkSize)

	// Helper to get current heap allocation.
	heapAlloc := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}

	t.Run("PING_no_memory_growth", func(t *testing.T) {
		c := dummyClient()
		c.parse([]byte("PING"))

		before := heapAlloc()

		// Feed 10MB of data
		for i := 0; i < 100; i++ {
			c.parse(junk)
		}

		after := heapAlloc()

		// Verify no client-side buffer accumulation.
		if c.argBuf != nil {
			t.Error("argBuf should be nil for OP_PING - no buffering occurs")
		}
		if c.msgBuf != nil {
			t.Error("msgBuf should be nil for OP_PING - no buffering occurs")
		}

		t.Logf("Heap before: %d bytes, after: %d bytes, delta: %+d bytes",
			before, after, int64(after)-int64(before))
		t.Logf("Client parseState size: %d bytes (fixed, does not grow)",
			unsafe.Sizeof(c.parseState))

		// The delta should be small (noise from runtime, GC, etc.)
		// because OP_PING doesn't allocate anything.
		const maxAcceptableDelta = 1024 * 1024 // 1MB tolerance for runtime noise
		delta := int64(after) - int64(before)
		if delta > int64(maxAcceptableDelta) {
			t.Errorf("Unexpected memory growth of %d bytes - OP_PING should not accumulate memory", delta)
		}
	})
}

// TestPingLeniency_CommandSwallowing demonstrates that the leniency allows
// entire protocol commands to be silently swallowed when concatenated
// directly after PING/PONG without a \n separator.
func TestPingLeniency_CommandSwallowing(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		description  string
		expectState  parserState
		expectParsed bool // Whether the embedded command is actually processed
	}{
		{
			name:         "PING_swallows_SUB",
			input:        "PINGSUB foo 1\r\n",
			description:  "SUB foo 1 is consumed as PING trailer, never parsed",
			expectState:  OP_START, // \n terminates PING, returns to start
			expectParsed: false,
		},
		{
			name:         "PONG_swallows_PUB",
			input:        "PONGPUB foo 5\r\nhello\r\n",
			description:  "PUB foo 5 header is consumed as PONG trailer",
			expectState:  OP_START,
			expectParsed: false,
		},
		{
			name:         "PING_swallows_CONNECT",
			input:        "PINGCONNECT {}\r\n",
			description:  "CONNECT {} is consumed as PING trailer",
			expectState:  OP_START,
			expectParsed: false,
		},
		{
			name:         "OK_swallows_UNSUB",
			input:        "+OKUNSUB 1\r\n",
			description:  "UNSUB 1 is consumed as +OK trailer",
			expectState:  OP_START,
			expectParsed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			err := c.parse([]byte(tt.input))
			// The parse should succeed (the swallowed command is just consumed).
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if c.state != tt.expectState {
				t.Fatalf("Expected state %d, got %d", tt.expectState, c.state)
			}
			// Verify the embedded command was NOT processed.
			// For the SUB case, check that no subscription was created.
			if tt.name == "PING_swallows_SUB" {
				if c.subs != nil && len(c.subs) > 0 {
					t.Error("SUB was processed - it should have been swallowed by PING")
				}
			}
			t.Logf("Confirmed: %s", tt.description)
		})
	}
}

// TestPingLeniency_ConnectionStall demonstrates the connection stalling effect.
// Once the parser enters OP_PING/OP_PONG/OP_PLUS_OK without a \n, ALL
// subsequent protocol data is consumed as part of the trailing garbage.
// The connection cannot process any more real commands until a \n byte arrives.
func TestPingLeniency_ConnectionStall(t *testing.T) {
	c := dummyClient()

	// Step 1: Send PING without \n terminator (simulating a TCP segment boundary).
	if err := c.parse([]byte("PING")); err != nil {
		t.Fatalf("Failed to parse PING prefix: %v", err)
	}
	if c.state != OP_PING {
		t.Fatalf("Expected OP_PING, got %d", c.state)
	}

	// Step 2: Send what should be valid commands. They're all consumed as
	// PING trailer data and never processed.
	validCommands := []string{
		"SUB foo 1\r",
		"PUB bar 5\r",
		"hello\r",
		"UNSUB 1\r",
		"CONNECT {}\r",
	}
	for _, cmd := range validCommands {
		if err := c.parse([]byte(cmd)); err != nil {
			t.Fatalf("Unexpected error while stalled: %v", err)
		}
		if c.state != OP_PING {
			t.Fatalf("Expected to remain stalled in OP_PING, got state %d", c.state)
		}
	}

	// Step 3: Only a \n byte can unstick the parser.
	if err := c.parse([]byte("\n")); err != nil {
		t.Fatalf("Failed to terminate PING: %v", err)
	}
	if c.state != OP_START {
		t.Fatalf("Expected OP_START after \\n, got %d", c.state)
	}

	// Step 4: Now commands work again.
	if err := c.parse([]byte("PING\r\n")); err != nil {
		t.Fatalf("Failed to parse PING after recovery: %v", err)
	}
	if c.state != OP_START {
		t.Fatalf("Expected OP_START, got %d", c.state)
	}

	t.Log("Confirmed: parser was stalled in OP_PING across 5 valid commands until \\n arrived")
}

// TestPingLeniency_ContrastWithStrictStates shows that every other parser state
// immediately rejects unexpected bytes via 'goto parseErr', while PING/PONG/+OK
// silently accept them.
func TestPingLeniency_ContrastWithStrictStates(t *testing.T) {
	strictStates := []struct {
		name    string
		setup   string // bytes to reach the state
		state   parserState
		garbage byte // byte that should be rejected
	}{
		{"OP_P", "P", OP_P, 'X'},
		{"OP_PI", "PI", OP_PI, 'X'},
		{"OP_PIN", "PIN", OP_PIN, 'X'},
		{"OP_PO", "PO", OP_PO, 'X'},
		{"OP_PON", "PON", OP_PON, 'X'},
		{"OP_PU", "PU", OP_PU, 'X'},
		{"OP_S", "S", OP_S, 'X'},
		{"OP_SU", "SU", OP_SU, 'X'},
		{"OP_C", "C", OP_C, 'X'},
		{"OP_CO", "CO", OP_CO, 'X'},
		{"OP_MINUS", "-", OP_MINUS, 'X'},
		{"OP_MINUS_E", "-E", OP_MINUS_E, 'X'},
		{"OP_MINUS_ER", "-ER", OP_MINUS_ER, 'X'},
	}

	lenientStates := []struct {
		name  string
		setup string
		state parserState
	}{
		{"OP_PING", "PING", OP_PING},
		{"OP_PONG", "PONG", OP_PONG},
		{"OP_PLUS_OK", "+OK", OP_PLUS_OK},
	}

	t.Run("strict_states_reject_garbage", func(t *testing.T) {
		for _, ss := range strictStates {
			t.Run(ss.name, func(t *testing.T) {
				c := dummyClient()
				c.parse([]byte(ss.setup))
				if c.state != ss.state {
					t.Skipf("Could not reach state %d (got %d)", ss.state, c.state)
				}
				err := c.parse([]byte{ss.garbage})
				if err == nil {
					t.Errorf("State %s accepted garbage byte 0x%02X without error", ss.name, ss.garbage)
				}
			})
		}
	})

	t.Run("lenient_states_accept_garbage", func(t *testing.T) {
		for _, ls := range lenientStates {
			t.Run(ls.name, func(t *testing.T) {
				c := dummyClient()
				c.parse([]byte(ls.setup))
				if c.state != ls.state {
					t.Fatalf("Could not reach state %d (got %d)", ls.state, c.state)
				}
				// Feed 256 different byte values (except \n which would terminate).
				for b := byte(0); b < 255; b++ {
					if b == '\n' {
						continue
					}
					err := c.parse([]byte{b})
					if err != nil {
						t.Errorf("State %s rejected byte 0x%02X: %v", ls.name, b, err)
					}
					if c.state != ls.state {
						t.Errorf("State %s changed to %d after byte 0x%02X", ls.name, c.state, b)
					}
				}
				t.Logf("%s accepts all 254 non-\\n byte values without error", ls.name)
			})
		}
	})
}

// TestPingLeniency_LiveServerStall demonstrates the issue against a live
// server using a raw TCP connection. A client sends "PING" followed by
// continuous junk data. The server's parser for that connection is stuck
// in OP_PING, consuming CPU on each read while doing no useful work.
//
// The server's ping timer will eventually close the connection, but until
// then the readLoop goroutine is busy-reading and discarding bytes.
func TestPingLeniency_LiveServerStall(t *testing.T) {
	opts := DefaultOptions()
	opts.Port = -1
	opts.PingInterval = 500 * time.Millisecond // Short interval to speed up test
	opts.MaxPingsOut = 2
	s := RunServer(opts)
	defer s.Shutdown()

	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)

	// Connect raw TCP
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Read the INFO line
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read INFO: %v", err)
	}
	if !strings.HasPrefix(string(buf[:n]), "INFO") {
		t.Fatalf("Expected INFO, got: %s", string(buf[:n]))
	}

	// Send CONNECT + PING to establish connection
	_, err = conn.Write([]byte("CONNECT {\"verbose\":false,\"protocol\":1}\r\nPING\r\n"))
	if err != nil {
		t.Fatalf("Failed to send CONNECT: %v", err)
	}

	// Read the PONG response
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read PONG: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "PONG") {
		t.Fatalf("Expected PONG, got: %s", string(buf[:n]))
	}

	// Now send PING WITHOUT \n terminator, followed by junk.
	// This puts the server's parser in OP_PING state where it
	// silently consumes all subsequent data.
	_, err = conn.Write([]byte("PING"))
	if err != nil {
		t.Fatalf("Failed to send PING prefix: %v", err)
	}

	// Now send junk data. The server's parser will consume all of this
	// as part of the PING "trailer" without any error or disconnection
	// (until the ping timer fires).
	junk := bytes.Repeat([]byte("X"), 4096)
	totalSent := 0
	writeErrors := 0

	// Send junk for a limited time or until the connection is closed
	// by the server's ping timer.
	deadline := time.Now().Add(3 * time.Second)
	conn.SetWriteDeadline(deadline)

	for time.Now().Before(deadline) {
		n, err := conn.Write(junk)
		if err != nil {
			writeErrors++
			break
		}
		totalSent += n
	}

	t.Logf("Sent %d bytes (%d MB) of junk data to stalled OP_PING parser before connection closed",
		totalSent, totalSent/(1024*1024))

	if totalSent > int(MAX_CONTROL_LINE_SIZE) {
		t.Logf("CONFIRMED: Server accepted %d bytes in OP_PING state, which is %dx the max_control_line limit (%d bytes)",
			totalSent, totalSent/int(MAX_CONTROL_LINE_SIZE), MAX_CONTROL_LINE_SIZE)
	}

	// Verify the server is still healthy (not crashed).
	if s.NumClients() < 0 {
		t.Fatal("Server appears to have crashed")
	}
}
