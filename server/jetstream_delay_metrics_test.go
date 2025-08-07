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

func TestMessageDelayMetrics(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create a stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "DELAY_TEST",
		Subjects: []string{"delay.*"},
	})
	require_NoError(t, err)

	// Get JetStream instance for metrics
	jsInstance := s.getJetStream()
	require_True(t, jsInstance != nil)
	require_True(t, jsInstance.delayMetrics != nil)

	// Test 1: Message with 150ms delay (should increment >100ms counter)
	t.Run("Delay150ms", func(t *testing.T) {
		msg := nats.NewMsg("delay.test1")
		msg.Data = []byte("150ms delay test")

		// Set sent_at to 150ms ago
		sentAtTime := time.Now().Add(-150 * time.Millisecond).UnixNano()
		msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)

		// Check metrics
		stats := jsInstance.getDelayStats()
		require_True(t, stats.SampleCount >= 1)
		require_True(t, stats.DelayOver100ms >= 1)
		require_True(t, stats.AverageDelayMs >= 100) // Should be around 150ms
	})

	// Test 2: Message with 300ms delay (should increment >100ms and >250ms counters)
	t.Run("Delay300ms", func(t *testing.T) {
		msg := nats.NewMsg("delay.test2")
		msg.Data = []byte("300ms delay test")

		// Set sent_at to 300ms ago
		sentAtTime := time.Now().Add(-300 * time.Millisecond).UnixNano()
		msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)

		// Check metrics
		stats := jsInstance.getDelayStats()
		require_True(t, stats.SampleCount >= 2)
		require_True(t, stats.DelayOver100ms >= 2)
		require_True(t, stats.DelayOver250ms >= 1)
	})

	// Test 3: Message with 600ms delay (should increment >100ms, >250ms, and >500ms counters)
	t.Run("Delay600ms", func(t *testing.T) {
		msg := nats.NewMsg("delay.test3")
		msg.Data = []byte("600ms delay test")

		// Set sent_at to 600ms ago
		sentAtTime := time.Now().Add(-600 * time.Millisecond).UnixNano()
		msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)

		// Check metrics
		stats := jsInstance.getDelayStats()
		require_True(t, stats.SampleCount >= 3)
		require_True(t, stats.DelayOver100ms >= 3)
		require_True(t, stats.DelayOver250ms >= 2)
		require_True(t, stats.DelayOver500ms >= 1)
	})

	// Test 4: Message with 1.5s delay (should increment all counters)
	t.Run("Delay1500ms", func(t *testing.T) {
		msg := nats.NewMsg("delay.test4")
		msg.Data = []byte("1500ms delay test")

		// Set sent_at to 1.5s ago
		sentAtTime := time.Now().Add(-1500 * time.Millisecond).UnixNano()
		msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)

		// Check metrics
		stats := jsInstance.getDelayStats()
		require_True(t, stats.SampleCount >= 4)
		require_True(t, stats.DelayOver100ms >= 4)
		require_True(t, stats.DelayOver250ms >= 3)
		require_True(t, stats.DelayOver500ms >= 2)
		require_True(t, stats.DelayOver1s >= 1)
	})

	// Test 5: Message with small delay (should not increment distribution counters)
	t.Run("Delay50ms", func(t *testing.T) {
		msg := nats.NewMsg("delay.test5")
		msg.Data = []byte("50ms delay test")

		// Set sent_at to 50ms ago
		sentAtTime := time.Now().Add(-50 * time.Millisecond).UnixNano()
		msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

		initialStats := jsInstance.getDelayStats()

		_, err := js.PublishMsg(msg)
		require_NoError(t, err)

		// Check metrics - counters should not change except sample count
		stats := jsInstance.getDelayStats()
		require_True(t, stats.SampleCount == initialStats.SampleCount+1)
		require_True(t, stats.DelayOver100ms == initialStats.DelayOver100ms) // Should not increment
	})

	// Final verification
	finalStats := jsInstance.getDelayStats()
	t.Logf("Final stats: Samples=%d, Avg=%.1fms, >100ms=%d, >250ms=%d, >500ms=%d, >1s=%d",
		finalStats.SampleCount, finalStats.AverageDelayMs,
		finalStats.DelayOver100ms, finalStats.DelayOver250ms,
		finalStats.DelayOver500ms, finalStats.DelayOver1s)
}

func TestMessageDelayMetricsInJetStreamStats(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create a stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "STATS_DELAY_TEST",
		Subjects: []string{"stats.*"},
	})
	require_NoError(t, err)

	// Send a message with delay
	msg := nats.NewMsg("stats.test")
	msg.Data = []byte("test message")
	sentAtTime := time.Now().Add(-200 * time.Millisecond).UnixNano()
	msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

	_, err = js.PublishMsg(msg)
	require_NoError(t, err)

	// Get JetStream stats from the server
	jsInstance := s.getJetStream()
	stats := jsInstance.usageStats()

	// Verify delay metrics are included in stats
	require_True(t, stats.DelayMetrics.SampleCount >= 1)
	require_True(t, stats.DelayMetrics.DelayOver100ms >= 1)
	require_True(t, stats.DelayMetrics.AverageDelayMs > 0)

	t.Logf("JetStream stats delay metrics: %+v", stats.DelayMetrics)
}

func TestDelayMetricsDataStructure(t *testing.T) {
	// Test the delay metrics data structure directly
	dm := newMessageDelayMetrics(3) // Small buffer for testing
	require_True(t, dm != nil)
	require_Equal(t, len(dm.samples), 3)

	// Test recording delays
	delays := []int64{
		50 * 1_000_000,   // 50ms
		150 * 1_000_000,  // 150ms
		300 * 1_000_000,  // 300ms
		600 * 1_000_000,  // 600ms
		1200 * 1_000_000, // 1200ms (1.2s)
	}

	for _, delay := range delays {
		dm.recordDelay(delay)
	}

	stats := dm.getStats()

	// Check distribution counters
	require_Equal(t, stats.DelayOver100ms, uint64(4)) // 150ms, 300ms, 600ms, 1200ms
	require_Equal(t, stats.DelayOver250ms, uint64(3)) // 300ms, 600ms, 1200ms
	require_Equal(t, stats.DelayOver500ms, uint64(2)) // 600ms, 1200ms
	require_Equal(t, stats.DelayOver1s, uint64(1))    // 1200ms

	// With buffer size 3, we should only have 3 samples for rolling average
	require_Equal(t, stats.SampleCount, uint64(3))

	t.Logf("Delay stats: %+v", stats)
}

func TestDelayMetricsRollingBuffer(t *testing.T) {
	dm := newMessageDelayMetrics(2) // Buffer size of 2

	// Add 3 samples - should evict the first one
	dm.recordDelay(100 * 1_000_000) // 100ms
	dm.recordDelay(200 * 1_000_000) // 200ms
	dm.recordDelay(300 * 1_000_000) // 300ms (should evict 100ms)

	stats := dm.getStats()

	// Should only have 2 samples in rolling average (200ms and 300ms)
	require_Equal(t, stats.SampleCount, uint64(2))

	// Average should be around 250ms
	require_True(t, stats.AverageDelayMs >= 240 && stats.AverageDelayMs <= 260)
}

func TestDelayMetricsConcurrency(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create a stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "CONCURRENT_DELAY_TEST",
		Subjects: []string{"concurrent.*"},
	})
	require_NoError(t, err)

	// Send multiple messages concurrently
	const numMessages = 20
	done := make(chan bool, numMessages)

	for i := 0; i < numMessages; i++ {
		go func(id int) {
			defer func() { done <- true }()

			msg := nats.NewMsg("concurrent.test")
			msg.Data = []byte("concurrent test message")

			// Varying delays
			delay := time.Duration((id%5+1)*100) * time.Millisecond
			sentAtTime := time.Now().Add(-delay).UnixNano()
			msg.Header.Set("Nats-Sent-At", strconv.FormatInt(sentAtTime, 10))

			_, err := js.PublishMsg(msg)
			require_NoError(t, err)
		}(i)
	}

	// Wait for all messages to be processed
	for i := 0; i < numMessages; i++ {
		<-done
	}

	// Give a moment for processing
	time.Sleep(100 * time.Millisecond)

	// Check final metrics
	jsInstance := s.getJetStream()
	stats := jsInstance.getDelayStats()

	require_True(t, stats.SampleCount >= uint64(numMessages))
	require_True(t, stats.AverageDelayMs > 0)

	t.Logf("Concurrent test final stats: %+v", stats)
}

// Benchmark the delay metrics recording
func BenchmarkDelayMetricsRecording(b *testing.B) {
	dm := newMessageDelayMetrics(1000)
	delay := int64(150 * 1_000_000) // 150ms in nanoseconds

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dm.recordDelay(delay)
	}
}

// Benchmark getting delay stats
func BenchmarkDelayMetricsGetStats(b *testing.B) {
	dm := newMessageDelayMetrics(1000)

	// Populate with some data
	for i := 0; i < 100; i++ {
		dm.recordDelay(int64(i * 1_000_000))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dm.getStats()
	}
}
