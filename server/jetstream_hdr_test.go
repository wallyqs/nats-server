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
	"bufio"
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// rawHPubConn creates a raw TCP connection to the server with headers enabled.
func rawHPubConn(t *testing.T, s *Server) (net.Conn, *bufio.Reader) {
	t.Helper()
	addr := strings.TrimPrefix(s.ClientURL(), "nats://")
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("Error connecting: %v", err)
	}
	cr := bufio.NewReaderSize(c, maxBufSize)
	line, _, err := cr.ReadLine()
	if err != nil {
		t.Fatalf("Error reading INFO: %v", err)
	}
	if !strings.HasPrefix(string(line), "INFO ") {
		t.Fatalf("Expected INFO, got: %s", line)
	}
	_, err = c.Write([]byte("CONNECT {\"verbose\":false,\"protocol\":1,\"headers\":true,\"no_responders\":true}\r\n"))
	if err != nil {
		t.Fatalf("Error sending CONNECT: %v", err)
	}
	_, err = c.Write([]byte("PING\r\n"))
	if err != nil {
		t.Fatalf("Error sending PING: %v", err)
	}
	// Read PONG, skipping any INFO messages the server sends after CONNECT.
	for {
		line, _, err = cr.ReadLine()
		if err != nil {
			t.Fatalf("Error reading PONG: %v", err)
		}
		if strings.HasPrefix(string(line), "INFO ") {
			continue
		}
		if string(line) != "PONG" {
			t.Fatalf("Expected PONG, got: %s", line)
		}
		break
	}
	return c, cr
}

// sendRawHPUB sends a raw HPUB command with the given header and payload bytes.
func sendRawHPUB(t *testing.T, c net.Conn, subject string, hdr, payload []byte) {
	t.Helper()
	hdrLen := len(hdr)
	totalLen := hdrLen + len(payload)
	cmd := fmt.Sprintf("HPUB %s %d %d\r\n", subject, hdrLen, totalLen)
	var buf bytes.Buffer
	buf.WriteString(cmd)
	buf.Write(hdr)
	buf.Write(payload)
	buf.WriteString("\r\n")
	_, err := c.Write(buf.Bytes())
	if err != nil {
		t.Fatalf("Error sending HPUB: %v", err)
	}
}

// flushRawConn sends a PING and waits for PONG to ensure previous messages are processed.
func flushRawConn(t *testing.T, c net.Conn, cr *bufio.Reader) {
	t.Helper()
	_, err := c.Write([]byte("PING\r\n"))
	if err != nil {
		t.Fatalf("Error sending PING: %v", err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	// Skip any INFO messages the server may send after CONNECT.
	for {
		line, _, err := cr.ReadLine()
		if err != nil {
			t.Fatalf("Error reading PONG: %v", err)
		}
		if strings.HasPrefix(string(line), "INFO ") {
			continue
		}
		if string(line) != "PONG" {
			t.Fatalf("Expected PONG, got: %s", line)
		}
		break
	}
	c.SetReadDeadline(time.Time{})
}

// waitForMsgs waits for the stream to have the expected number of messages.
func waitForMsgs(t *testing.T, mset *stream, expected uint64) {
	t.Helper()
	checkFor(t, 2*time.Second, 50*time.Millisecond, func() error {
		if msgs := mset.state().Msgs; msgs != expected {
			return fmt.Errorf("expected %d msgs, got %d", expected, msgs)
		}
		return nil
	})
}

// TestJetStreamMalformedHeaderVersionLine verifies that a header with a trailing
// space in the NATS/1.0 version line is accepted and stored by the server.
// The header "NATS/1.0 \r\n\r\n" (13 bytes) is structurally close to valid but
// causes the nats.go client to panic in DecodeHeadersMsg, since it interprets the
// trailing space as beginning a status code that isn't there.
func TestJetStreamMalformedHeaderVersionLine(t *testing.T) {
	for _, st := range []StorageType{MemoryStorage, FileStorage} {
		t.Run(st.String(), func(t *testing.T) {
			s := RunBasicJetStreamServer(t)
			defer s.Shutdown()

			nc, _ := jsClientConnect(t, s)
			defer nc.Close()

			addStream(t, nc, &StreamConfig{
				Name:     "TEST",
				Subjects: []string{"foo"},
				Storage:  st,
			})

			rawConn, rawCR := rawHPubConn(t, s)
			defer rawConn.Close()

			// "NATS/1.0 \r\n\r\n" = 13 bytes: version line with trailing space + terminator.
			hdr := []byte("NATS/1.0 \r\n\r\n")
			payload := []byte("hi")
			sendRawHPUB(t, rawConn, "foo", hdr, payload)
			flushRawConn(t, rawConn, rawCR)

			acc := s.GlobalAccount()
			mset, err := acc.lookupStream("TEST")
			require_NoError(t, err)

			// Wait for async JetStream processing.
			waitForMsgs(t, mset, 1)

			// Retrieve via internal getMsg and check raw bytes are preserved.
			sm, err := mset.getMsg(1)
			require_NoError(t, err)
			require_True(t, bytes.Equal(sm.Header, hdr))
			require_True(t, bytes.Equal(sm.Data, payload))
		})
	}
}

// TestJetStreamMalformedHeaderMissingTerminator verifies behavior when
// the header bytes are missing the required \r\n\r\n terminator.
func TestJetStreamMalformedHeaderMissingTerminator(t *testing.T) {
	for _, st := range []StorageType{MemoryStorage, FileStorage} {
		t.Run(st.String(), func(t *testing.T) {
			s := RunBasicJetStreamServer(t)
			defer s.Shutdown()

			nc, _ := jsClientConnect(t, s)
			defer nc.Close()

			addStream(t, nc, &StreamConfig{
				Name:     "TEST",
				Subjects: []string{"foo"},
				Storage:  st,
			})

			rawConn, rawCR := rawHPubConn(t, s)
			defer rawConn.Close()

			// "NATS/1.0\r\n" = 10 bytes: version line only, no \r\n\r\n terminator.
			hdr := []byte("NATS/1.0\r\n")
			payload := []byte("hello")
			sendRawHPUB(t, rawConn, "foo", hdr, payload)
			flushRawConn(t, rawConn, rawCR)

			acc := s.GlobalAccount()
			mset, err := acc.lookupStream("TEST")
			require_NoError(t, err)
			waitForMsgs(t, mset, 1)

			sm, err := mset.getMsg(1)
			require_NoError(t, err)
			require_True(t, bytes.Equal(sm.Header, hdr))
			require_True(t, bytes.Equal(sm.Data, payload))
		})
	}
}

// TestJetStreamMalformedHeaderMissingVersionPrefix verifies behavior when
// the header bytes don't start with "NATS/1.0\r\n" at all.
func TestJetStreamMalformedHeaderMissingVersionPrefix(t *testing.T) {
	for _, st := range []StorageType{MemoryStorage, FileStorage} {
		t.Run(st.String(), func(t *testing.T) {
			s := RunBasicJetStreamServer(t)
			defer s.Shutdown()

			nc, _ := jsClientConnect(t, s)
			defer nc.Close()

			addStream(t, nc, &StreamConfig{
				Name:     "TEST",
				Subjects: []string{"foo"},
				Storage:  st,
			})

			rawConn, rawCR := rawHPubConn(t, s)
			defer rawConn.Close()

			// No NATS/1.0 prefix — just a key-value pair and terminator.
			hdr := []byte("Foo:Bar\r\n\r\n")
			payload := []byte("data")
			sendRawHPUB(t, rawConn, "foo", hdr, payload)
			flushRawConn(t, rawConn, rawCR)

			acc := s.GlobalAccount()
			mset, err := acc.lookupStream("TEST")
			require_NoError(t, err)
			waitForMsgs(t, mset, 1)

			sm, err := mset.getMsg(1)
			require_NoError(t, err)
			require_True(t, bytes.Equal(sm.Header, hdr))

			// getHeaderKeyIndex should NOT find "Foo" because it requires
			// the key to be preceded by \r\n (not at position 0).
			val := bytesToString(sliceHeader("Foo", sm.Header))
			require_Equal(t, val, _EMPTY_)
		})
	}
}

// TestJetStreamMalformedHeaderKeyInVersionLine verifies behavior when
// a header key-value pair is concatenated directly with the version string.
func TestJetStreamMalformedHeaderKeyInVersionLine(t *testing.T) {
	for _, st := range []StorageType{MemoryStorage, FileStorage} {
		t.Run(st.String(), func(t *testing.T) {
			s := RunBasicJetStreamServer(t)
			defer s.Shutdown()

			nc, _ := jsClientConnect(t, s)
			defer nc.Close()

			addStream(t, nc, &StreamConfig{
				Name:     "TEST",
				Subjects: []string{"foo"},
				Storage:  st,
			})

			rawConn, rawCR := rawHPubConn(t, s)
			defer rawConn.Close()

			// Version line runs into the header key without a CRLF.
			hdr := []byte("NATS/1.0Foo: Bar\r\n\r\n")
			payload := []byte("hi")
			sendRawHPUB(t, rawConn, "foo", hdr, payload)
			flushRawConn(t, rawConn, rawCR)

			acc := s.GlobalAccount()
			mset, err := acc.lookupStream("TEST")
			require_NoError(t, err)
			waitForMsgs(t, mset, 1)

			sm, err := mset.getMsg(1)
			require_NoError(t, err)

			// "Foo" should NOT be found: getHeaderKeyIndex requires \r\n before the key.
			val := bytesToString(sliceHeader("Foo", sm.Header))
			require_Equal(t, val, _EMPTY_)
		})
	}
}

// TestJetStreamMalformedHeaderGenHeader verifies the behavior of genHeader
// (used by JetStream consumers during delivery) when applied to malformed headers.
// genHeader strips the last \r\n from existing headers and appends new key-value
// pairs. With malformed headers this produces subtly broken output.
func TestJetStreamMalformedHeaderGenHeader(t *testing.T) {
	tests := []struct {
		name     string
		hdr      []byte
		key      string
		value    string
		expected []byte
	}{
		{
			// Valid header: genHeader strips trailing \r\n, appends key: value\r\n\r\n.
			name:     "valid header",
			hdr:      []byte("NATS/1.0\r\nKey: val\r\n\r\n"),
			key:      "New",
			value:    "data",
			expected: []byte("NATS/1.0\r\nKey: val\r\nNew: data\r\n\r\n"),
		},
		{
			// Trailing space in version line: genHeader preserves the trailing space.
			// The result still has the trailing space in the version line.
			name:     "trailing space in version line",
			hdr:      []byte("NATS/1.0 \r\nKey: val\r\n\r\n"),
			key:      "New",
			value:    "data",
			expected: []byte("NATS/1.0 \r\nKey: val\r\nNew: data\r\n\r\n"),
		},
		{
			// Missing terminator: "NATS/1.0\r\n" (10 bytes).
			// genHeader strips last 2 bytes (\r\n), leaving "NATS/1.0",
			// then appends the new key-value. The result has the version string
			// running directly into the key name: "NATS/1.0New: data\r\n\r\n".
			// This corrupts the header structure completely.
			name:     "missing terminator",
			hdr:      []byte("NATS/1.0\r\n"),
			key:      "New",
			value:    "data",
			expected: []byte("NATS/1.0New: data\r\n\r\n"),
		},
		{
			// No version prefix: "Foo:Bar\r\n\r\n".
			// genHeader strips trailing \r\n and appends new header.
			// The result has no NATS/1.0 line at all.
			name:     "no version prefix",
			hdr:      []byte("Foo:Bar\r\n\r\n"),
			key:      "New",
			value:    "data",
			expected: []byte("Foo:Bar\r\nNew: data\r\n\r\n"),
		},
		{
			// Header too short (< LEN_CR_LF): genHeader falls back to inserting
			// a fresh "NATS/1.0\r\n" prefix.
			name:     "header too short fallback",
			hdr:      []byte("X"),
			key:      "New",
			value:    "data",
			expected: []byte("NATS/1.0\r\nNew: data\r\n\r\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := genHeader(tt.hdr, tt.key, tt.value)
			if !bytes.Equal(result, tt.expected) {
				t.Fatalf("genHeader(%q, %q, %q):\n  got:  %q\n  want: %q",
					tt.hdr, tt.key, tt.value, result, tt.expected)
			}
		})
	}
}

// TestJetStreamMalformedHeaderDirectGetDedup verifies dedup behavior when
// the version line has a trailing space: dedup still works because
// getHeaderKeyIndex finds "Nats-Msg-Id" preceded by \r\n.
func TestJetStreamMalformedHeaderDirectGetDedup(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, _ := jsClientConnect(t, s)
	defer nc.Close()

	addStream(t, nc, &StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Storage:  MemoryStorage,
	})

	rawConn, rawCR := rawHPubConn(t, s)
	defer rawConn.Close()

	// Publish with valid headers containing a Nats-Msg-Id for dedup.
	validHdr := []byte("NATS/1.0\r\nNats-Msg-Id: msg1\r\n\r\n")
	sendRawHPUB(t, rawConn, "foo", validHdr, []byte("first"))
	flushRawConn(t, rawConn, rawCR)

	acc := s.GlobalAccount()
	mset, err := acc.lookupStream("TEST")
	require_NoError(t, err)
	waitForMsgs(t, mset, 1)

	// Publish another with malformed header (trailing space in version line)
	// but the same Nats-Msg-Id. Dedup should still work because
	// getHeaderKeyIndex finds "Nats-Msg-Id" preceded by \r\n.
	malformedHdr := []byte("NATS/1.0 \r\nNats-Msg-Id: msg1\r\n\r\n")
	sendRawHPUB(t, rawConn, "foo", malformedHdr, []byte("dupe"))
	flushRawConn(t, rawConn, rawCR)

	// Give time for potential second message to arrive (it shouldn't).
	time.Sleep(100 * time.Millisecond)

	// Should still be 1 message if dedup worked.
	require_Equal(t, mset.state().Msgs, uint64(1))

	// Verify the original message is preserved.
	sm, err := mset.getMsg(1)
	require_NoError(t, err)
	require_Equal(t, string(sm.Data), "first")
}

// TestJetStreamMalformedHeaderDedupBrokenByMissingVersionLine tests that
// dedup breaks when the NATS/1.0 version line is missing entirely.
// The Nats-Msg-Id at position 0 is not preceded by \r\n, so
// getHeaderKeyIndex returns -1 and dedup does not work.
func TestJetStreamMalformedHeaderDedupBrokenByMissingVersionLine(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, _ := jsClientConnect(t, s)
	defer nc.Close()

	addStream(t, nc, &StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Storage:  MemoryStorage,
	})

	rawConn, rawCR := rawHPubConn(t, s)
	defer rawConn.Close()

	// No NATS/1.0 prefix: "Nats-Msg-Id: msg1\r\n\r\n"
	// Key starts at offset 0, not after \r\n.
	malformedHdr := []byte("Nats-Msg-Id: msg1\r\n\r\n")
	sendRawHPUB(t, rawConn, "foo", malformedHdr, []byte("first"))
	flushRawConn(t, rawConn, rawCR)

	acc := s.GlobalAccount()
	mset, err := acc.lookupStream("TEST")
	require_NoError(t, err)
	waitForMsgs(t, mset, 1)

	// Publish again with the same msg-id.
	sendRawHPUB(t, rawConn, "foo", malformedHdr, []byte("second"))
	flushRawConn(t, rawConn, rawCR)

	// Dedup does NOT work: both messages get stored.
	waitForMsgs(t, mset, 2)
}

// TestJetStreamMalformedHeaderClusterPropagation tests that malformed headers
// are preserved when messages are replicated across a JetStream cluster.
func TestJetStreamMalformedHeaderClusterPropagation(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3", 3)
	defer c.shutdown()

	nc, _ := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	addStream(t, nc, &StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
		Storage:  MemoryStorage,
	})

	leader := c.streamLeader(globalAccountName, "TEST")
	rawConn, rawCR := rawHPubConn(t, leader)
	defer rawConn.Close()

	// Publish with trailing space in version line.
	hdr := []byte("NATS/1.0 \r\nMyKey: MyVal\r\n\r\n")
	payload := []byte("cluster-data")
	sendRawHPUB(t, rawConn, "foo", hdr, payload)
	flushRawConn(t, rawConn, rawCR)

	// Wait for the leader to report the message.
	mset, err := leader.GlobalAccount().lookupStream("TEST")
	require_NoError(t, err)
	waitForMsgs(t, mset, 1)

	// Wait for replication.
	c.waitOnStreamCurrent(leader, globalAccountName, "TEST")

	// Verify the raw header bytes are preserved on each replica.
	for _, srv := range c.servers {
		mset, err := srv.GlobalAccount().lookupStream("TEST")
		require_NoError(t, err)
		sm, err := mset.getMsg(1)
		require_NoError(t, err)
		require_True(t, bytes.Equal(sm.Header, hdr))
		require_True(t, bytes.Equal(sm.Data, payload))

		// Server's sliceHeader still finds "MyKey" because it's preceded by \r\n.
		val := bytesToString(sliceHeader("MyKey", sm.Header))
		require_Equal(t, val, "MyVal")
	}
}

// TestJetStreamMalformedHeaderSuperCluster tests that malformed headers
// propagate through a supercluster via sources.
func TestJetStreamMalformedHeaderSuperCluster(t *testing.T) {
	sc := createJetStreamSuperCluster(t, 1, 2)
	defer sc.shutdown()

	s1 := sc.clusterForName("C1").randomServer()
	nc1, _ := jsClientConnect(t, s1)
	defer nc1.Close()

	addStream(t, nc1, &StreamConfig{
		Name:     "ORIGIN",
		Subjects: []string{"foo"},
		Storage:  MemoryStorage,
	})

	s2 := sc.clusterForName("C2").randomServer()
	nc2, js2 := jsClientConnect(t, s2)
	defer nc2.Close()

	_, err := js2.AddStream(&nats.StreamConfig{
		Name:    "MIRROR",
		Sources: []*nats.StreamSource{{Name: "ORIGIN"}},
	})
	require_NoError(t, err)

	// Publish malformed header via raw HPUB to C1.
	rawConn, rawCR := rawHPubConn(t, s1)
	defer rawConn.Close()

	hdr := []byte("NATS/1.0 \r\nX-Custom: test-value\r\n\r\n")
	payload := []byte("super-data")
	sendRawHPUB(t, rawConn, "foo", hdr, payload)
	flushRawConn(t, rawConn, rawCR)

	// Wait for the message to be sourced into MIRROR on C2.
	checkFor(t, 5*time.Second, 250*time.Millisecond, func() error {
		si, err := js2.StreamInfo("MIRROR")
		if err != nil {
			return err
		}
		if si.State.Msgs != 1 {
			return fmt.Errorf("expected 1 msg in MIRROR, got %d", si.State.Msgs)
		}
		return nil
	})

	// Verify the raw header bytes are preserved in the MIRROR stream.
	mset, err := s2.GlobalAccount().lookupStream("MIRROR")
	require_NoError(t, err)
	sm, err := mset.getMsg(1)
	require_NoError(t, err)
	require_True(t, bytes.Equal(sm.Data, payload))

	// Server's sliceHeader should still find "X-Custom" after \r\n.
	val := bytesToString(sliceHeader("X-Custom", sm.Header))
	require_Equal(t, val, "test-value")
}

// TestJetStreamMalformedHeaderMsgGetAPI verifies that the JetStream storage
// preserves raw header bytes for malformed headers.
func TestJetStreamMalformedHeaderMsgGetAPI(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, _ := jsClientConnect(t, s)
	defer nc.Close()

	addStream(t, nc, &StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Storage:  MemoryStorage,
	})

	rawConn, rawCR := rawHPubConn(t, s)
	defer rawConn.Close()

	tests := []struct {
		name    string
		hdr     []byte
		payload []byte
	}{
		{
			name:    "trailing space in version",
			hdr:     []byte("NATS/1.0 \r\nKey: val\r\n\r\n"),
			payload: []byte("msg1"),
		},
		{
			name:    "missing terminator",
			hdr:     []byte("NATS/1.0\r\n"),
			payload: []byte("msg2"),
		},
		{
			name:    "valid headers",
			hdr:     []byte("NATS/1.0\r\nKey: val\r\n\r\n"),
			payload: []byte("msg3"),
		},
	}

	for _, tt := range tests {
		sendRawHPUB(t, rawConn, "foo", tt.hdr, tt.payload)
		flushRawConn(t, rawConn, rawCR)
	}

	acc := s.GlobalAccount()
	mset, err := acc.lookupStream("TEST")
	require_NoError(t, err)
	waitForMsgs(t, mset, uint64(len(tests)))

	// Verify each message via internal getMsg.
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, err := mset.getMsg(uint64(i + 1))
			require_NoError(t, err)
			require_True(t, bytes.Equal(sm.Data, tt.payload))
			require_True(t, bytes.Equal(sm.Header, tt.hdr))
		})
	}
}

// TestJetStreamMalformedHeaderCoreNATSRawDelivery verifies that a malformed header
// published via HPUB is delivered to a raw TCP subscriber as an HMSG.
func TestJetStreamMalformedHeaderCoreNATSRawDelivery(t *testing.T) {
	opts := DefaultTestOptions
	opts.Port = -1
	s := RunServer(&opts)
	defer s.Shutdown()

	// Use a raw subscriber to avoid the nats.go client panic.
	rawSub, rawSubCR := rawHPubConn(t, s)
	defer rawSub.Close()

	_, err := rawSub.Write([]byte("SUB foo 1\r\nPING\r\n"))
	require_NoError(t, err)
	rawSub.SetReadDeadline(time.Now().Add(2 * time.Second))
	// Skip any INFO messages the server sends after CONNECT.
	for {
		line, _, err := rawSubCR.ReadLine()
		require_NoError(t, err)
		if strings.HasPrefix(string(line), "INFO ") {
			continue
		}
		require_Equal(t, string(line), "PONG")
		break
	}
	rawSub.SetReadDeadline(time.Time{})

	rawPub, rawPubCR := rawHPubConn(t, s)
	defer rawPub.Close()

	// Valid header.
	hdr := []byte("NATS/1.0\r\nX-Test: hello\r\n\r\n")
	sendRawHPUB(t, rawPub, "foo", hdr, []byte("good"))
	flushRawConn(t, rawPub, rawPubCR)

	// Read HMSG for first (valid) message, skipping any INFO lines.
	rawSub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, _, err := rawSubCR.ReadLine()
		require_NoError(t, err)
		if strings.HasPrefix(string(line), "INFO ") {
			continue
		}
		require_True(t, strings.HasPrefix(string(line), "HMSG foo 1 "))
		break
	}
	rawSub.SetReadDeadline(time.Time{})

	// Malformed: no version prefix.
	hdr2 := []byte("BadKey: val\r\n\r\n")
	sendRawHPUB(t, rawPub, "foo", hdr2, []byte("payload"))
	flushRawConn(t, rawPub, rawPubCR)

	// Drain remaining bytes from first message, then read next HMSG.
	rawSub.SetReadDeadline(time.Now().Add(2 * time.Second))
	found := false
	for i := 0; i < 50; i++ {
		lineB, _, err := rawSubCR.ReadLine()
		if err != nil {
			break
		}
		if strings.HasPrefix(string(lineB), "HMSG foo 1 ") {
			found = true
			break
		}
	}
	rawSub.SetReadDeadline(time.Time{})
	require_True(t, found)
}

// TestJetStreamMalformedHeaderClusterDirectGetAllNodes verifies that after
// a message with malformed headers is published to a clustered stream,
// the raw header bytes are consistent across all replicas.
func TestJetStreamMalformedHeaderClusterDirectGetAllNodes(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3", 3)
	defer c.shutdown()

	nc, _ := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	addStream(t, nc, &StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
		Storage:  MemoryStorage,
	})

	leader := c.streamLeader(globalAccountName, "TEST")
	rawConn, rawCR := rawHPubConn(t, leader)
	defer rawConn.Close()

	cases := []struct {
		name    string
		hdr     []byte
		payload []byte
	}{
		{
			name:    "missing terminator",
			hdr:     []byte("NATS/1.0\r\n"),
			payload: []byte("no-term"),
		},
		{
			name:    "version with trailing space",
			hdr:     []byte("NATS/1.0 \r\n\r\n"),
			payload: []byte("trail-space"),
		},
		{
			name:    "key concatenated with version",
			hdr:     []byte("NATS/1.0Key:Val\r\n\r\n"),
			payload: []byte("concat"),
		},
	}

	for _, tt := range cases {
		sendRawHPUB(t, rawConn, "foo", tt.hdr, tt.payload)
		flushRawConn(t, rawConn, rawCR)
	}

	// Wait for storage.
	mset, err := leader.GlobalAccount().lookupStream("TEST")
	require_NoError(t, err)
	waitForMsgs(t, mset, uint64(len(cases)))

	// Wait for replication.
	c.waitOnStreamCurrent(leader, globalAccountName, "TEST")

	// Check each message is consistent across all nodes via internal getMsg.
	for i, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			for _, srv := range c.servers {
				mset, err := srv.GlobalAccount().lookupStream("TEST")
				require_NoError(t, err)
				sm, err := mset.getMsg(uint64(i + 1))
				require_NoError(t, err)
				require_True(t, bytes.Equal(sm.Header, tt.hdr))
				require_True(t, bytes.Equal(sm.Data, tt.payload))
			}
		})
	}
}
