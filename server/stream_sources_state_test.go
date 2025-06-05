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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestStreamSourcesPersistentState(t *testing.T) {
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

	// Check that source state was saved
	mset.mu.RLock()
	storeDir := mset.store.(*fileStore).fcfg.StoreDir
	mset.mu.RUnlock()

	sourceStateFile := filepath.Join(storeDir, streamSourcesStateFile)

	// Force a save
	mset.mu.Lock()
	err = mset.saveSourcesState()
	mset.mu.Unlock()
	require_NoError(t, err)

	// Verify file exists
	_, err = os.Stat(sourceStateFile)
	require_NoError(t, err)

	// Read and log the file content for debugging
	data, err := os.ReadFile(sourceStateFile)
	require_NoError(t, err)
	t.Logf("Saved source state file content: %s", string(data))

	// Get the current source sequence
	mset.mu.RLock()
	var savedSeq uint64
	for _, si := range mset.sources {
		savedSeq = si.sseq
		break
	}
	mset.mu.RUnlock()

	// Simulate a restart by stopping source consumers and resetting state
	// This is what happens during leader election or server restart
	mset.mu.Lock()
	mset.stopSourceConsumers()
	// Clear the sequences to simulate losing in-memory state
	for _, si := range mset.sources {
		si.sseq = 0
		si.dseq = 0
	}
	mset.mu.Unlock()

	// Now simulate becoming leader again and setting up sources
	mset.mu.Lock()
	err = mset.setupSourceConsumers()
	mset.mu.Unlock()
	require_NoError(t, err)

	// Wait a moment for sources to be set up
	time.Sleep(100 * time.Millisecond)

	// Check if the file still exists
	data2, err := os.ReadFile(sourceStateFile)
	if err != nil {
		t.Logf("Source state file not found after recreate: %v", err)
	} else {
		t.Logf("Source state file after recreate: %s", string(data2))
	}

	// The source state should be loaded from disk
	mset.mu.RLock()
	var loadedSeq uint64
	var sourceCount int
	for iname, si := range mset.sources {
		loadedSeq = si.sseq
		sourceCount++
		t.Logf("Source %q has sequence %d", iname, si.sseq)
		break
	}
	mset.mu.RUnlock()

	if sourceCount == 0 {
		t.Fatal("No sources found after recreating stream")
	}

	if loadedSeq != savedSeq {
		t.Fatalf("Expected loaded sequence %d to match saved sequence %d", loadedSeq, savedSeq)
	}

	// Publish more messages to verify it continues from the right place
	for i := 100; i < 110; i++ {
		_, err := js.Publish(fmt.Sprintf("source.%d", i), []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// Wait for new messages
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		state := mset.state()
		if state.Msgs != 110 {
			return fmt.Errorf("expected 110 messages, got %d", state.Msgs)
		}
		return nil
	})
}

func TestStreamSourcesStateNoLinearScan(t *testing.T) {
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

	// Force a save
	mset.mu.Lock()
	err = mset.saveSourcesState()
	mset.mu.Unlock()
	require_NoError(t, err)

	// Simulate a restart by stopping source consumers and resetting state
	mset.mu.Lock()
	mset.stopSourceConsumers()
	// Clear the sequences to simulate losing in-memory state
	for _, si := range mset.sources {
		si.sseq = 0
		si.dseq = 0
	}
	mset.mu.Unlock()

	// Time the source setup - with persisted state it should be fast
	start := time.Now()
	mset.mu.Lock()
	err = mset.setupSourceConsumers()
	mset.mu.Unlock()
	require_NoError(t, err)
	elapsed := time.Since(start)

	// With persisted state, startup should be very fast even with 10k messages
	// Without it, scanning 10k messages would take much longer
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Stream startup took too long: %v (indicates linear scan was performed)", elapsed)
	}

	// Verify the state was loaded correctly by checking we can continue
	state := mset.state()
	if state.Msgs != 10000 {
		t.Fatalf("Expected 10000 messages after restart, got %d", state.Msgs)
	}
}
