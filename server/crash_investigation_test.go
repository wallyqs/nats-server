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

// TestCrashInvestigation_PUB_MalformedArgs tests that the server does not crash
// when a client sends PUB commands with various malformed arguments.
func TestCrashInvestigation_PUB_MalformedArgs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// No arguments
		{"no_args", "PUB \r\n"},
		// Single argument (missing size)
		{"missing_size", "PUB foo\r\n"},
		// Too many arguments
		{"too_many_args", "PUB foo bar baz 5\r\n"},
		// Negative size
		{"negative_size", "PUB foo -1\r\n"},
		// Zero size
		{"zero_size", "PUB foo 0\r\n\r\n"},
		// Non-numeric size
		{"non_numeric_size", "PUB foo abc\r\n"},
		// Very large size (9 digits, max parseSize allows)
		{"very_large_size", "PUB foo 999999999\r\n"},
		// Size overflow (10+ digits, exceeds parseSize limit)
		{"size_overflow_10_digits", "PUB foo 9999999999\r\n"},
		// Size overflow (very large)
		{"size_overflow_huge", "PUB foo 99999999999999999999\r\n"},
		// Empty subject with size
		{"only_spaces", "PUB   \r\n"},
		// Size with leading zeros
		{"leading_zeros", "PUB foo 0005\r\n"},
		// Tabs only
		{"tabs_only", "PUB\t\r\n"},
		// Mixed delimiters
		{"mixed_delims", "PUB \t foo \t 5\r\n"},
		// Just newline without CR
		{"no_cr", "PUB foo 5\nhello\r\n"},
		// Only CR
		{"only_cr", "PUB foo 5\r"},
		// Subject with dots
		{"dotted_subject", "PUB foo.bar.baz 5\r\nhello\r\n"},
		// Subject with wildcards (normally invalid for PUB in pedantic mode)
		{"wildcard_subject", "PUB foo.*.bar 5\r\nhello\r\n"},
		{"fwc_subject", "PUB foo.> 5\r\nhello\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			// Should not panic - errors are acceptable
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_HPUB_MalformedArgs tests that the server does not crash
// when a client sends HPUB commands with various malformed arguments.
func TestCrashInvestigation_HPUB_MalformedArgs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// No arguments
		{"no_args", "HPUB \r\n"},
		// Missing header/total sizes
		{"missing_sizes", "HPUB foo\r\n"},
		{"missing_total_size", "HPUB foo 5\r\n"},
		// Too many arguments
		{"too_many_args", "HPUB foo bar baz 5 10\r\n"},
		// Header size > total size
		{"hdr_gt_total", "HPUB foo 10 5\r\n"},
		// Negative header size
		{"negative_hdr", "HPUB foo -1 5\r\n"},
		// Negative total size
		{"negative_total", "HPUB foo 5 -1\r\n"},
		// Both zero
		{"both_zero", "HPUB foo 0 0\r\n\r\n"},
		// Header zero, size nonzero
		{"hdr_zero_size_nonzero", "HPUB foo 0 5\r\nhello\r\n"},
		// Non-numeric sizes
		{"non_numeric_hdr", "HPUB foo abc 5\r\n"},
		{"non_numeric_total", "HPUB foo 5 abc\r\n"},
		// Very large sizes
		{"very_large_hdr", "HPUB foo 999999999 999999999\r\n"},
		// Size overflow
		{"size_overflow", "HPUB foo 9999999999 9999999999\r\n"},
		// Without headers support
		{"no_headers_support", "HPUB foo 5 10\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			// Enable headers for most tests
			if tt.name != "no_headers_support" {
				c.headers = true
			}
			// Should not panic - errors are acceptable
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_SUB_MalformedArgs tests that the server does not crash
// when a client sends SUB commands with various malformed arguments.
func TestCrashInvestigation_SUB_MalformedArgs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// No arguments
		{"no_args", "SUB \r\n"},
		// Single argument (missing SID)
		{"missing_sid", "SUB foo\r\n"},
		// Too many arguments
		{"too_many_args", "SUB foo bar baz extra\r\n"},
		// Valid 2-arg
		{"valid_2_arg", "SUB foo 1\r\n"},
		// Valid 3-arg (with queue)
		{"valid_3_arg", "SUB foo bar 1\r\n"},
		// Very long subject
		{"long_subject", "SUB " + strings.Repeat("a", 1000) + " 1\r\n"},
		// Subject with special characters
		{"special_chars_subject", "SUB foo.*.> 1\r\n"},
		// Empty-looking args (just spaces)
		{"only_spaces", "SUB   \r\n"},
		// Tabs
		{"tabs", "SUB\tfoo\t1\r\n"},
		// Just newline
		{"just_newline", "SUB foo 1\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			// Should not panic - errors are acceptable
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_UNSUB_MalformedArgs tests UNSUB with malformed arguments.
func TestCrashInvestigation_UNSUB_MalformedArgs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// No arguments
		{"no_args", "UNSUB \r\n"},
		// Nonexistent SID
		{"nonexistent_sid", "UNSUB 999\r\n"},
		// With max
		{"with_max", "UNSUB 1 10\r\n"},
		// Negative max
		{"negative_max", "UNSUB 1 -1\r\n"},
		// Too many arguments
		{"too_many_args", "UNSUB 1 10 extra\r\n"},
		// Non-numeric SID
		{"non_numeric_sid", "UNSUB abc\r\n"},
		// Very long SID
		{"long_sid", "UNSUB " + strings.Repeat("x", 500) + "\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			// Should not panic - errors are acceptable
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_PING_Variations tests PING with various formats.
func TestCrashInvestigation_PING_Variations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"normal", "PING\r\n"},
		{"no_cr", "PING\n"},
		{"extra_data", "PING extra data\r\n"},
		{"extra_data_no_cr", "PING extra data\n"},
		{"lowercase", "ping\r\n"},
		{"mixed_case", "PiNg\r\n"},
		{"trailing_spaces", "PING   \r\n"},
		{"many_pings", "PING\r\nPING\r\nPING\r\nPING\r\nPING\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_PONG_Variations tests PONG with various formats.
func TestCrashInvestigation_PONG_Variations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"normal", "PONG\r\n"},
		{"no_cr", "PONG\n"},
		{"extra_data", "PONG extra\r\n"},
		{"lowercase", "pong\r\n"},
		{"trailing_spaces", "PONG   \r\n"},
		{"many_pongs", "PONG\r\nPONG\r\nPONG\r\nPONG\r\nPONG\r\n"},
		// PONG without prior PING - should not crash
		{"unsolicited_pong", "PONG\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			c.ping.out = 5 // simulate outstanding pings
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_ERR_FromClient tests -ERR sent from a client to the server.
// Normally -ERR is server->client, but a malicious client could send it.
func TestCrashInvestigation_ERR_FromClient(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"normal", "-ERR 'some error'\r\n"},
		{"no_quotes", "-ERR some error\r\n"},
		{"empty_error", "-ERR \r\n"},
		{"very_long_error", "-ERR " + strings.Repeat("x", 2000) + "\r\n"},
		{"special_chars", "-ERR <script>alert(1)</script>\r\n"},
		{"format_string", "-ERR %s%s%s%n%n%n\r\n"},
		{"null_bytes", "-ERR hello\x00world\r\n"},
		{"newlines_in_error", "-ERR line1\r\n"},
		{"just_err", "-ERR x\r\n"},
		{"lowercase", "-err test\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			// Should not panic
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_InvalidProtocolOps tests completely invalid protocol operations.
func TestCrashInvestigation_InvalidProtocolOps(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"single_byte", "X"},
		{"null_byte", "\x00"},
		{"binary_data", "\x00\x01\x02\x03\x04\x05"},
		{"random_ascii", "XYZZY\r\n"},
		{"partial_pub", "PU"},
		{"partial_sub", "SU"},
		{"partial_ping", "PIN"},
		{"partial_pong", "PON"},
		{"partial_connect", "CONN"},
		{"partial_hpub", "HPU"},
		{"partial_err", "-ER"},
		{"just_cr", "\r"},
		{"just_lf", "\n"},
		{"just_crlf", "\r\n"},
		{"many_crlf", "\r\n\r\n\r\n\r\n"},
		// MSG and HMSG are route/leaf only, should fail for CLIENT
		{"msg_as_client", "MSG foo 1 5\r\nhello\r\n"},
		{"hmsg_as_client", "HMSG foo 1 5 10\r\n"},
		// Route-only operations sent from CLIENT
		{"rsub_as_client", "RS+ foo 1\r\n"},
		{"runsub_as_client", "RS- foo 1\r\n"},
		{"asub_as_client", "A+ foo 1\r\n"},
		{"aunsub_as_client", "A- foo 1\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			// Should not panic - parse errors are acceptable
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_MixedProtocolSequences tests sequences of different
// protocol messages that could reveal state machine issues.
func TestCrashInvestigation_MixedProtocolSequences(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"pub_then_ping", "PUB foo 5\r\nhello\r\nPING\r\n"},
		{"ping_then_pub", "PING\r\nPUB foo 5\r\nhello\r\n"},
		{"sub_then_pub", "SUB foo 1\r\nPUB foo 5\r\nhello\r\n"},
		{"pub_then_sub", "PUB foo 5\r\nhello\r\nSUB foo 1\r\n"},
		{"many_ops", "PING\r\nPONG\r\nSUB foo 1\r\nPUB foo 5\r\nhello\r\nUNSUB 1\r\nPING\r\n"},
		{"ping_pong_ping", "PING\r\nPONG\r\nPING\r\n"},
		{"err_then_pub", "-ERR test\r\nPUB foo 5\r\nhello\r\n"},
		{"pub_zero_then_sub", "PUB foo 0\r\n\r\nSUB bar 1\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_SplitBuffer tests protocol messages split across
// multiple parse calls, which simulates partial network reads.
func TestCrashInvestigation_SplitBuffer(t *testing.T) {
	messages := []string{
		"PUB foo 5\r\nhello\r\n",
		"HPUB foo 12 17\r\nname:derek\r\nHELLO\r\n",
		"SUB foo 1\r\n",
		"UNSUB 1\r\n",
		"PING\r\n",
		"PONG\r\n",
		"-ERR test\r\n",
	}

	for _, msg := range messages {
		data := []byte(msg)
		// Test splitting at every possible position
		for i := 1; i < len(data); i++ {
			t.Run(fmt.Sprintf("%s_split_at_%d", msg[:4], i), func(t *testing.T) {
				c := dummyClient()
				c.headers = true
				// First half
				if err := c.parse(data[:i]); err != nil {
					return // error is ok, just don't panic
				}
				// Second half
				c.parse(data[i:])
			})
		}
	}
}

// TestCrashInvestigation_PUB_PayloadBoundaries tests PUB with payloads
// at various boundary sizes.
func TestCrashInvestigation_PUB_PayloadBoundaries(t *testing.T) {
	sizes := []int{0, 1, 2, 3, 5, 10, 100, 1000, 4094, 4095, 4096, 4097}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			c := dummyClient()
			payload := strings.Repeat("x", size)
			msg := fmt.Sprintf("PUB foo %d\r\n%s\r\n", size, payload)
			c.parse([]byte(msg))
		})
	}
}

// TestCrashInvestigation_PUB_DeclaredSizeMismatch tests PUB where the
// declared size doesn't match the actual payload.
func TestCrashInvestigation_PUB_DeclaredSizeMismatch(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// Declared size larger than actual payload
		{"larger_declared", "PUB foo 100\r\nhello\r\n"},
		// Declared size smaller than actual payload
		{"smaller_declared", "PUB foo 2\r\nhello\r\n"},
		// Declared size 0 but has payload
		{"zero_declared_with_payload", "PUB foo 0\r\nhello\r\n"},
		// Declared size matches
		{"exact_match", "PUB foo 5\r\nhello\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			// Should not panic
			c.parse([]byte(tt.input))
		})
	}
}

// TestCrashInvestigation_RapidPingPong tests many rapid PING/PONG messages
// to check for state corruption.
func TestCrashInvestigation_RapidPingPong(t *testing.T) {
	c := dummyClient()
	// Send 1000 PINGs
	ping := []byte("PING\r\n")
	for i := 0; i < 1000; i++ {
		if err := c.parse(ping); err != nil {
			return
		}
	}

	// Send 1000 PONGs
	pong := []byte("PONG\r\n")
	for i := 0; i < 1000; i++ {
		if err := c.parse(pong); err != nil {
			return
		}
	}

	// Interleaved
	for i := 0; i < 1000; i++ {
		if err := c.parse(ping); err != nil {
			return
		}
		if err := c.parse(pong); err != nil {
			return
		}
	}
}

// TestCrashInvestigation_MaxControlLine tests that the max control line
// limit is enforced and doesn't cause crashes.
func TestCrashInvestigation_MaxControlLine(t *testing.T) {
	c := dummyClient()

	// PUB with subject exceeding max control line
	longSubject := strings.Repeat("a", int(MAX_CONTROL_LINE_SIZE)+100)
	msg := fmt.Sprintf("PUB %s 5\r\nhello\r\n", longSubject)
	c.parse([]byte(msg))

	// SUB with subject exceeding max control line
	c.state = OP_START
	msg = fmt.Sprintf("SUB %s 1\r\n", longSubject)
	c.parse([]byte(msg))

	// -ERR with message exceeding max control line
	c.state = OP_START
	longErr := strings.Repeat("e", int(MAX_CONTROL_LINE_SIZE)+100)
	msg = fmt.Sprintf("-ERR %s\r\n", longErr)
	c.parse([]byte(msg))
}

// TestCrashInvestigation_HPUB_HeaderSizeBoundaries tests HPUB with
// various header size and total size combinations.
func TestCrashInvestigation_HPUB_HeaderSizeBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		hdrSize int
		total   int
		payload string
	}{
		{"hdr_eq_total", 5, 5, "hello"},
		{"hdr_1_total_1", 1, 1, "x"},
		{"hdr_0_total_5", 0, 5, "hello"},
		{"hdr_0_total_0", 0, 0, ""},
		{"hdr_3_total_8", 3, 8, "XXXhello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := dummyClient()
			c.headers = true
			msg := fmt.Sprintf("HPUB foo %d %d\r\n%s\r\n", tt.hdrSize, tt.total, tt.payload)
			c.parse([]byte(msg))
		})
	}
}

// TestCrashInvestigation_ProtoSnippet tests the protoSnippet function
// with edge cases.
func TestCrashInvestigation_ProtoSnippet(t *testing.T) {
	tests := []struct {
		name  string
		start int
		max   int
		buf   []byte
	}{
		{"empty_buf", 0, 10, []byte{}},
		{"start_eq_len", 5, 10, []byte("hello")},
		{"start_gt_len", 10, 10, []byte("hello")},
		{"normal", 0, 3, []byte("hello")},
		{"single_byte", 0, 1, []byte("x")},
		{"max_zero", 0, 0, []byte("hello")},
		{"start_at_end", 4, 10, []byte("hello")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			protoSnippet(tt.start, tt.max, tt.buf)
		})
	}
}

// TestCrashInvestigation_ParseSize tests the parseSize function with edge cases.
func TestCrashInvestigation_ParseSize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"empty", "", -1},
		{"zero", "0", 0},
		{"one", "1", 1},
		{"max_9_digits", "999999999", 999999999},
		{"ten_digits", "1234567890", -1}, // exceeds maxParseSizeLen
		{"negative", "-1", -1},           // '-' is not a digit
		{"alpha", "abc", -1},
		{"mixed", "12a", -1},
		{"leading_zero", "007", 7},
		{"spaces", " 5", -1}, // space is not a digit
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseSize([]byte(tt.input))
			if result != tt.expected {
				t.Fatalf("parseSize(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}
