// Copyright 2019-2025 The NATS Authors
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

//go:build !skip_js_tests

package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestStreamSourceInMemoryIndex(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	// Client for API requests.
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create source stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "SOURCE",
		Subjects: []string{"source.>"},
	})
	require_NoError(t, err)

	// Publish some messages to source
	for i := 0; i < 100; i++ {
		_, err := js.Publish(fmt.Sprintf("source.%d", i), []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// Create target stream with source
	cfg := &StreamConfig{
		Name: "TARGET",
		Sources: []*StreamSource{
			{
				Name: "SOURCE",
			},
		},
		Storage: FileStorage,
	}

	acc := s.GlobalAccount()
	mset, err := acc.addStream(cfg)
	require_NoError(t, err)

	// Wait for source to catch up
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		state := mset.state()
		if state.Msgs != 100 {
			return fmt.Errorf("expected 100 messages, got %d", state.Msgs)
		}
		return nil
	})

	// Check that the in-memory index was populated
	mset.mu.RLock()
	if mset.sourceSeqIndex == nil {
		mset.mu.RUnlock()
		t.Fatal("Expected sourceSeqIndex to be initialized")
	}

	indexSize := len(mset.sourceSeqIndex)
	var indexedSeq uint64
	for _, seq := range mset.sourceSeqIndex {
		indexedSeq = seq
		break
	}
	mset.mu.RUnlock()

	if indexSize != 1 {
		t.Fatalf("Expected 1 source in index, got %d", indexSize)
	}

	if indexedSeq != 100 {
		t.Fatalf("Expected indexed sequence to be 100, got %d", indexedSeq)
	}

	// Now simulate a restart by clearing the source sequences
	mset.mu.Lock()
	for _, si := range mset.sources {
		si.sseq = 0
		si.dseq = 0
	}
	mset.mu.Unlock()

	// Call startingSequenceForSources which should use the index
	mset.mu.Lock()
	mset.startingSequenceForSources()
	mset.mu.Unlock()

	// Verify the sequences were restored from the index
	mset.mu.RLock()
	var restoredSeq uint64
	for _, si := range mset.sources {
		restoredSeq = si.sseq
		break
	}
	mset.mu.RUnlock()

	if restoredSeq != 100 {
		t.Fatalf("Expected restored sequence to be 100, got %d", restoredSeq)
	}

	// Publish more messages to verify the index gets updated
	for i := 100; i < 110; i++ {
		_, err := js.Publish(fmt.Sprintf("source.%d", i), []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// Wait for new messages - may take longer since consumer needs to catch up
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		state := mset.state()
		if state.Msgs < 110 {
			return fmt.Errorf("expected at least 110 messages, got %d", state.Msgs)
		}
		return nil
	})

	// Check that the index was updated
	mset.mu.RLock()
	var updatedSeq uint64
	for _, seq := range mset.sourceSeqIndex {
		updatedSeq = seq
		break
	}
	mset.mu.RUnlock()

	if updatedSeq != 110 {
		t.Fatalf("Expected updated indexed sequence to be 110, got %d", updatedSeq)
	}
}

func TestStreamSourceIndexPerformance(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()

	// Client for API requests.
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Create source stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "SOURCE",
		Subjects: []string{"source.>"},
	})
	require_NoError(t, err)

	// Publish many messages to make linear scan expensive
	for i := 0; i < 10000; i++ {
		_, err := js.Publish(fmt.Sprintf("source.%d", i), []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// Create target stream with source
	cfg := &StreamConfig{
		Name: "TARGET",
		Sources: []*StreamSource{
			{
				Name: "SOURCE",
			},
		},
		Storage: FileStorage,
	}

	acc := s.GlobalAccount()
	mset, err := acc.addStream(cfg)
	require_NoError(t, err)

	// Wait for source to catch up
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		state := mset.state()
		if state.Msgs != 10000 {
			return fmt.Errorf("expected 10000 messages, got %d", state.Msgs)
		}
		return nil
	})

	// Verify index is populated
	mset.mu.RLock()
	hasIndex := mset.sourceSeqIndex != nil && len(mset.sourceSeqIndex) > 0
	mset.mu.RUnlock()

	if !hasIndex {
		t.Fatal("Expected sourceSeqIndex to be populated after processing messages")
	}

	// Clear source sequences to simulate restart
	mset.mu.Lock()
	for _, si := range mset.sources {
		si.sseq = 0
		si.dseq = 0
	}
	mset.mu.Unlock()

	// Time the restoration using index
	start := time.Now()
	mset.mu.Lock()
	mset.startingSequenceForSources()
	mset.mu.Unlock()
	elapsed := time.Since(start)

	// With the index, this should be very fast even with 10k messages
	if elapsed > 10*time.Millisecond {
		t.Fatalf("startingSequenceForSources took too long with index: %v", elapsed)
	}

	// Verify sequences were restored correctly
	mset.mu.RLock()
	var restoredSeq uint64
	for _, si := range mset.sources {
		restoredSeq = si.sseq
		break
	}
	mset.mu.RUnlock()

	if restoredSeq != 10000 {
		t.Fatalf("Expected restored sequence to be 10000, got %d", restoredSeq)
	}
}
