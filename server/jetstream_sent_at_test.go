// Copyright 2024 The NATS Authors
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
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestJetStreamSentAtHeaderProcessing(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create a stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "SENTAT_TEST",
		Subjects: []string{"sentat.*"},
	})
	require_NoError(t, err)

	// Test 1: Message without sent_at header (should work normally)
	t.Run("MessageWithoutSentAt", func(t *testing.T) {
		msg := nats.NewMsg("sentat.test1")
		msg.Data = []byte("test message without sent_at")

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)
	})

	// Test 2: Message with valid sent_at header
	t.Run("MessageWithValidSentAt", func(t *testing.T) {
		msg := nats.NewMsg("sentat.test2")
		msg.Data = []byte("test message with sent_at")

		// Set sent_at to current time in nanoseconds
		sentAtTime := time.Now().UnixNano()
		msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)
	})

	// Test 3: Message with sent_at in the past
	t.Run("MessageWithPastSentAt", func(t *testing.T) {
		msg := nats.NewMsg("sentat.test3")
		msg.Data = []byte("test message with past sent_at")

		// Set sent_at to 5 seconds ago
		pastTime := time.Now().Add(-5 * time.Second).UnixNano()
		msg.Header.Set("Nats-Sent-At", strconv.FormatInt(pastTime, 10))

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)
	})

	// Test 4: Message with invalid sent_at header (should be ignored)
	t.Run("MessageWithInvalidSentAt", func(t *testing.T) {
		msg := nats.NewMsg("sentat.test4")
		msg.Data = []byte("test message with invalid sent_at")

		msg.Header.Set("Nats-Sent-At", "invalid-timestamp")

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)
	})

	// Test 5: Message with zero sent_at header (should be ignored)
	t.Run("MessageWithZeroSentAt", func(t *testing.T) {
		msg := nats.NewMsg("sentat.test5")
		msg.Data = []byte("test message with zero sent_at")

		msg.Header.Set("Nats-Sent-At", "0")

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)
	})

	// Test 6: Message with negative sent_at header (should be ignored)
	t.Run("MessageWithNegativeSentAt", func(t *testing.T) {
		msg := nats.NewMsg("sentat.test6")
		msg.Data = []byte("test message with negative sent_at")

		msg.Header.Set("Nats-Sent-At", "-1234567890")

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)
	})

	// Verify all messages were stored
	si, err := js.StreamInfo("SENTAT_TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, uint64(6))
}

func TestGetSentAtFunction(t *testing.T) {
	// Test with valid timestamp
	validTimestamp := time.Now().UnixNano()
	validHeader := []byte("NATS/1.0\r\nNats-Sent-At: " + strconv.FormatInt(validTimestamp, 10) + "\r\n\r\n")

	result := getSentAt(validHeader)
	require_Equal(t, result, validTimestamp)

	// Test with no header
	noHeader := []byte("NATS/1.0\r\n\r\n")
	result = getSentAt(noHeader)
	require_Equal(t, result, int64(0))

	// Test with invalid timestamp
	invalidHeader := []byte("NATS/1.0\r\nNats-Sent-At: invalid\r\n\r\n")
	result = getSentAt(invalidHeader)
	require_Equal(t, result, int64(0))

	// Test with zero timestamp
	zeroHeader := []byte("NATS/1.0\r\nNats-Sent-At: 0\r\n\r\n")
	result = getSentAt(zeroHeader)
	require_Equal(t, result, int64(0))

	// Test with negative timestamp (treated as invalid)
	negativeHeader := []byte("NATS/1.0\r\nNats-Sent-At: -123\r\n\r\n")
	result = getSentAt(negativeHeader)
	require_Equal(t, result, int64(0))
}

func TestSentAtHeaderConstant(t *testing.T) {
	// Verify the header constant is correctly defined
	require_Equal(t, JSSentAt, "Nats-Sent-At")
}

func TestSentAtWithOtherHeaders(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create a stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "SENTAT_MULTI_HEADER_TEST",
		Subjects: []string{"multi.*"},
	})
	require_NoError(t, err)

	// Test message with multiple headers including sent_at
	msg := nats.NewMsg("multi.test")
	msg.Data = []byte("test message with multiple headers")

	// Add various headers
	msg.Header.Set("Nats-Msg-Id", "test-msg-123")
	msg.Header.Set("Nats-Sent-At", strconv.FormatInt(time.Now().UnixNano(), 10))
	msg.Header.Set("Custom-Header", "custom-value")

	_, err = js.PublishMsg(msg)
	require_NoError(t, err)

	// Verify message was stored
	si, err := js.StreamInfo("SENTAT_MULTI_HEADER_TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, uint64(1))
}

// Benchmark the getSentAt function
func BenchmarkGetSentAt(b *testing.B) {
	timestamp := time.Now().UnixNano()
	header := []byte("NATS/1.0\r\nNats-Sent-At: " + strconv.FormatInt(timestamp, 10) + "\r\n\r\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getSentAt(header)
	}
}

// Benchmark with no sent_at header
func BenchmarkGetSentAtEmpty(b *testing.B) {
	header := []byte("NATS/1.0\r\n\r\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getSentAt(header)
	}
}
