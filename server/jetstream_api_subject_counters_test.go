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
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestJetStreamAPISubjectCounters(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Get initial stats - should be empty
	jsInstance := s.getJetStream()
	require_True(t, jsInstance != nil)

	initialStats := jsInstance.ApiSubjectStats()
	require_Equal(t, len(initialStats), 0)

	// Create a stream (triggers $JS.API.STREAM.CREATE.* counter)
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST_STREAM",
		Subjects: []string{"test.*"},
	})
	require_NoError(t, err)

	// Get stream info (triggers $JS.API.STREAM.INFO.* counter)
	_, err = js.StreamInfo("TEST_STREAM")
	require_NoError(t, err)

	// List streams (triggers $JS.API.STREAM.NAMES counter)
	for range js.StreamNames() {
		// Just consume the iterator
	}

	// Create a consumer (triggers $JS.API.CONSUMER.CREATE.* counter)
	_, err = js.AddConsumer("TEST_STREAM", &nats.ConsumerConfig{
		Durable: "TEST_CONSUMER",
	})
	require_NoError(t, err)

	// Get consumer info (triggers $JS.API.CONSUMER.INFO.* counter)
	_, err = js.ConsumerInfo("TEST_STREAM", "TEST_CONSUMER")
	require_NoError(t, err)

	// Check that counters have been incremented
	stats := jsInstance.ApiSubjectStats()

	// Should have at least these counters with non-zero values
	expectedCounters := []string{
		JSApiStreamCreate,
		JSApiStreamInfo,
		JSApiStreams,
		JSApiConsumerCreate,
		JSApiConsumerInfo,
	}

	for _, counter := range expectedCounters {
		count, exists := stats[counter]
		if !exists {
			t.Errorf("Expected counter %s to exist in stats", counter)
		}
		if count == 0 {
			t.Errorf("Expected counter %s to be > 0, got %d", counter, count)
		}
	}

	// Verify specific expected counts
	require_True(t, stats[JSApiStreamCreate] >= 1)
	require_True(t, stats[JSApiStreamInfo] >= 1)
	require_True(t, stats[JSApiStreams] >= 1)
	require_True(t, stats[JSApiConsumerCreate] >= 1)
	require_True(t, stats[JSApiConsumerInfo] >= 1)
}

func TestJetStreamAPISubjectCountersConcurrency(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	// Test concurrent access to counters
	jsInstance := s.getJetStream()
	require_True(t, jsInstance != nil)

	const numGoroutines = 10
	const incrementsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Concurrently increment different patterns
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			pattern := JSApiStreamCreate
			if id%2 == 0 {
				pattern = JSApiConsumerCreate
			}

			for j := 0; j < incrementsPerGoroutine; j++ {
				jsInstance.incrementAPISubjectCounter(pattern)
			}
		}(i)
	}

	// Also concurrently read stats
	var readWg sync.WaitGroup
	readWg.Add(5)
	for i := 0; i < 5; i++ {
		go func() {
			defer readWg.Done()
			for j := 0; j < 50; j++ {
				_ = jsInstance.ApiSubjectStats()
			}
		}()
	}

	wg.Wait()
	readWg.Wait()

	// Verify final counts
	stats := jsInstance.ApiSubjectStats()

	streamCreateCount := stats[JSApiStreamCreate]
	consumerCreateCount := stats[JSApiConsumerCreate]

	// Half of goroutines incremented stream create, half consumer create
	expectedStreamCreate := uint64((numGoroutines / 2) * incrementsPerGoroutine)
	expectedConsumerCreate := uint64((numGoroutines / 2) * incrementsPerGoroutine)

	require_Equal(t, streamCreateCount, expectedStreamCreate)
	require_Equal(t, consumerCreateCount, expectedConsumerCreate)
}

func TestJetStreamAPISubjectPatternMapping(t *testing.T) {
	jsInstance := &jetStream{}

	// Test known patterns
	testCases := []struct {
		subject  string
		expected string
	}{
		{"$JS.API.INFO", JSApiAccountInfo},
		{"$JS.API.STREAM.CREATE.mystream", JSApiStreamCreate},
		{"$JS.API.STREAM.UPDATE.mystream", JSApiStreamUpdate},
		{"$JS.API.STREAM.INFO.mystream", JSApiStreamInfo},
		{"$JS.API.STREAM.DELETE.mystream", JSApiStreamDelete},
		{"$JS.API.STREAM.NAMES", JSApiStreams},
		{"$JS.API.STREAM.LIST", JSApiStreamList},
		{"$JS.API.STREAM.PURGE.mystream", JSApiStreamPurge},
		{"$JS.API.STREAM.MSG.GET.mystream", JSApiMsgGet},
		{"$JS.API.STREAM.MSG.DELETE.mystream", JSApiMsgDelete},
		{"$JS.API.CONSUMER.CREATE.mystream", JSApiConsumerCreate},
		{"$JS.API.CONSUMER.CREATE.mystream.myconsumer.filter", JSApiConsumerCreate},
		{"$JS.API.CONSUMER.DURABLE.CREATE.mystream.myconsumer", JSApiDurableCreate},
		{"$JS.API.CONSUMER.INFO.mystream.myconsumer", JSApiConsumerInfo},
		{"$JS.API.CONSUMER.DELETE.mystream.myconsumer", JSApiConsumerDelete},
		{"$JS.API.CONSUMER.NAMES.mystream", JSApiConsumers},
		{"$JS.API.CONSUMER.LIST.mystream", JSApiConsumerList},
		{"$JS.API.CONSUMER.PAUSE.mystream.myconsumer", JSApiConsumerPause},
		{"$JS.API.DIRECT.GET.mystream", JSDirectMsgGet},
		{"$JS.API.STREAM.TEMPLATE.CREATE.mytemplate", JSApiTemplateCreate},
		{"$JS.API.STREAM.TEMPLATE.NAMES", JSApiTemplates},
		{"$JS.API.STREAM.TEMPLATE.INFO.mytemplate", JSApiTemplateInfo},
		{"$JS.API.STREAM.TEMPLATE.DELETE.mytemplate", JSApiTemplateDelete},
		{"$JS.API.STREAM.PEER.REMOVE.mystream", JSApiStreamRemovePeer},
		{"$JS.API.STREAM.LEADER.STEPDOWN.mystream", JSApiStreamLeaderStepDown},
		{"$JS.API.CONSUMER.LEADER.STEPDOWN.mystream.myconsumer", JSApiConsumerLeaderStepDown},
		{"$JS.API.CONSUMER.MSG.NEXT.mystream.myconsumer", JSApiRequestNext},
		{"$JS.API.UNKNOWN.PATTERN", "unknown"},
	}

	for _, tc := range testCases {
		result := jsInstance.getAPISubjectPattern(tc.subject)
		if result != tc.expected {
			t.Errorf("Subject %s: expected pattern %s, got %s", tc.subject, tc.expected, result)
		}
	}
}

func TestJetStreamAPISubjectCountersInAPIStats(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create a stream to trigger API calls
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "STATS_TEST",
		Subjects: []string{"stats.*"},
	})
	require_NoError(t, err)

	// Get JetStream server stats
	jsInstance := s.getJetStream()
	serverStats := jsInstance.usageStats()
	require_True(t, serverStats != nil)
	require_True(t, len(serverStats.API.Subjects) > 0)

	// Should have stream create counter
	streamCreateCount, exists := serverStats.API.Subjects[JSApiStreamCreate]
	require_True(t, exists)
	require_True(t, streamCreateCount > 0)

	// Test account-level stats as well
	acc, err := s.LookupAccount("$G")
	require_NoError(t, err)
	accountStats := acc.JetStreamUsage()
	require_True(t, len(accountStats.API.Subjects) > 0)

	// Should have the same stream create counter
	accountStreamCreateCount, exists := accountStats.API.Subjects[JSApiStreamCreate]
	require_True(t, exists)
	require_True(t, accountStreamCreateCount > 0)
}

func TestJetStreamMsgNextAPISubjectCounters(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create a stream and consumer for MSG.NEXT testing
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "MSG_NEXT_COUNTER_TEST",
		Subjects: []string{"msgnext.counter.*"},
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("MSG_NEXT_COUNTER_TEST", &nats.ConsumerConfig{
		Durable:   "test-consumer",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// Publish some messages
	for i := 0; i < 3; i++ {
		_, err := js.Publish("msgnext.counter.test", []byte("test message"))
		require_NoError(t, err)
	}

	// Get JetStream instance to check initial counter state
	jsInstance := s.getJetStream()
	require_True(t, jsInstance != nil)

	// Get initial MSG.NEXT counter (should be 0)
	initialStats := jsInstance.ApiSubjectStats()
	initialMsgNextCount := initialStats[JSApiRequestNext]

	// Make 2 MSG.NEXT requests directly
	subject := "$JS.API.CONSUMER.MSG.NEXT.MSG_NEXT_COUNTER_TEST.test-consumer"
	for i := 0; i < 2; i++ {
		_, err := nc.Request(subject, []byte(`{"batch": 1}`), time.Second)
		require_NoError(t, err)
	}

	// Wait a moment for counter updates
	time.Sleep(50 * time.Millisecond)

	// Check that MSG.NEXT counter increased by 2
	finalStats := jsInstance.ApiSubjectStats()
	finalMsgNextCount := finalStats[JSApiRequestNext]

	t.Logf("MSG.NEXT counter: initial=%d, final=%d", initialMsgNextCount, finalMsgNextCount)
	require_True(t, finalMsgNextCount == initialMsgNextCount+2)
}
