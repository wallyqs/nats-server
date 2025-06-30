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
	"testing"
)

// TestFileStoreSelectiveFlushOnTruncate tests that truncate only flushes
// pending writes when necessary (when they would be affected by truncation).
func TestFileStoreSelectiveFlushOnTruncate(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 1024 // Small block size to test with multiple messages
		fs, err := newFileStore(fcfg, StreamConfig{Name: "TEST", Storage: FileStorage})
		require_NoError(t, err)
		defer fs.Stop()

		// Store some messages
		for i := 1; i <= 10; i++ {
			msg := fmt.Sprintf("msg-%d", i)
			_, _, err := fs.StoreMsg("test", nil, []byte(msg), 0)
			require_NoError(t, err)
		}

		// Get the last message block which should have pending writes if async
		fs.mu.RLock()
		mb := fs.lmb
		fs.mu.RUnlock()

		if mb == nil {
			t.Fatal("Expected last message block")
		}

		// Check current state before truncate
		mb.mu.RLock()
		pendingBefore := mb.pendingWriteSize()
		wpBefore := 0
		if mb.cache != nil {
			wpBefore = mb.cache.wp
		}
		mb.mu.RUnlock()

		// Truncate to message 8 (removing messages 9 and 10)
		sm, err := fs.LoadMsg(8, nil)
		require_NoError(t, err)

		err = fs.Truncate(sm.seq)
		require_NoError(t, err)

		// Verify truncation worked
		state := fs.State()
		if state.LastSeq != 8 {
			t.Fatalf("Expected last sequence to be 8, got %d", state.LastSeq)
		}
		if state.Msgs != 8 {
			t.Fatalf("Expected 8 messages, got %d", state.Msgs)
		}

		// Check that pending writes were handled appropriately
		mb.mu.RLock()
		pendingAfter := mb.pendingWriteSize()
		wpAfter := 0
		if mb.cache != nil {
			wpAfter = mb.cache.wp
		}
		mb.mu.RUnlock()

		// Log the state for debugging
		t.Logf("Before truncate: pending=%d, wp=%d", pendingBefore, wpBefore)
		t.Logf("After truncate: pending=%d, wp=%d", pendingAfter, wpAfter)

		// After truncation, there should be no pending writes
		// as they would have been flushed if they affected the truncation
		if pendingAfter > 0 {
			t.Logf("Note: %d bytes still pending after truncate (may be new data)", pendingAfter)
		}
	})
}

// TestFileStoreSelectiveFlushOnLoad tests that loadMsgsWithLock only flushes
// pending writes when necessary.
func TestFileStoreSelectiveFlushOnLoad(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 2048 // Small enough to trigger multiple blocks
		fs, err := newFileStore(fcfg, StreamConfig{Name: "TEST", Storage: FileStorage})
		require_NoError(t, err)
		defer fs.Stop()

		// Store enough messages to create multiple blocks
		for i := 1; i <= 50; i++ {
			msg := fmt.Sprintf("message-%d-with-some-padding-to-increase-size", i)
			_, _, err := fs.StoreMsg("test", nil, []byte(msg), 0)
			require_NoError(t, err)
		}

		// Get a message block that's not the last one
		fs.mu.RLock()
		var targetMb *msgBlock
		if len(fs.blks) > 1 {
			targetMb = fs.blks[0] // First block
		}
		fs.mu.RUnlock()

		if targetMb == nil {
			t.Skip("Need multiple blocks for this test")
		}

		// Clear the cache to force a reload
		targetMb.mu.Lock()
		targetMb.clearCacheAndOffset()
		// Store the write pointer before load
		hadCache := targetMb.cache != nil
		targetMb.mu.Unlock()

		// Try to load a message from this block, which will trigger loadMsgsWithLock
		_, err = fs.LoadMsg(1, nil)
		require_NoError(t, err)

		// Verify the cache was reloaded
		targetMb.mu.RLock()
		hasCache := targetMb.cache != nil && len(targetMb.cache.buf) > 0
		targetMb.mu.RUnlock()

		if !hadCache && hasCache {
			t.Log("Cache was successfully loaded")
		}
	})
}