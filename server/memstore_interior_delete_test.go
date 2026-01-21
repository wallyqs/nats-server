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

package server

import (
	"testing"
	"time"
)

// TestMemStoreGetSeqFromTimeWithInteriorDeletes demonstrates the binary search
// behavior in memstore with interior deletes.
func TestMemStoreGetSeqFromTimeWithInteriorDeletes(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Name: "zzz", Subjects: []string{"foo"}, Storage: MemoryStorage})
	require_NoError(t, err)
	defer ms.Stop()

	var start int64
	// Store 6 messages
	for i := range 6 {
		_, ts, err := ms.StoreMsg("foo", nil, nil, 0)
		require_NoError(t, err)
		if i == 1 {
			// Record timestamp of message 2 (seq=2)
			start = ts
		}
	}

	// Create interior delete gaps (same pattern as PR's filestore test)
	// Delete messages 3, 4, and 6
	_, err = ms.RemoveMsg(3)
	require_NoError(t, err)
	_, err = ms.RemoveMsg(4)
	require_NoError(t, err)
	_, err = ms.RemoveMsg(6)
	require_NoError(t, err)

	// Now we have: seq 1, 2, [gap], [gap], 5, [gap]
	// Search for a time just before message 2's timestamp
	// Should return seq 2 (first message with ts >= start-1)
	ts := time.Unix(0, start-1).UTC()
	result := ms.GetSeqFromTime(ts)

	t.Logf("Searching for timestamp: %d (start-1)", start-1)
	t.Logf("Expected result: 2")
	t.Logf("Actual result: %d", result)

	require_Equal(t, result, uint64(2))
}

// TestMemStoreGetSeqFromTimeBinarySearchBug demonstrates that the memstore
// GetSeqFromTime has the same binary search bug as filestore (fixed in PR #7751).
// The bug: sort.Search is used with a function that returns false for deleted
// messages, violating the monotonicity requirement of binary search.
//
// This test verifies the fix for the memstore binary search bug.
func TestMemStoreGetSeqFromTimeBinarySearchBug(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Name: "zzz", Subjects: []string{"foo"}, Storage: MemoryStorage})
	require_NoError(t, err)
	defer ms.Stop()

	// Store 10 messages with controlled timestamps
	// We need enough messages that binary search will probe deleted indices
	timestamps := make([]int64, 10)
	for i := range 10 {
		_, ts, err := ms.StoreMsg("foo", nil, nil, 0)
		require_NoError(t, err)
		timestamps[i] = ts
	}

	// Delete messages to create gaps that binary search will hit
	// Sequences: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10
	// Delete: 4, 5, 6, 7 (middle gap)
	// Remaining: 1, 2, 3, [gap x4], 8, 9, 10
	for seq := uint64(4); seq <= 7; seq++ {
		_, err = ms.RemoveMsg(seq)
		require_NoError(t, err)
	}

	// Search for timestamp of message 3 (seq=3)
	// This should return seq 3
	target := timestamps[2] // message 3's timestamp (0-indexed)
	ts := time.Unix(0, target).UTC()
	result := ms.GetSeqFromTime(ts)

	t.Logf("Messages remaining: 1, 2, 3, 8, 9, 10 (deleted 4-7)")
	t.Logf("Searching for timestamp of seq 3: %d", target)
	t.Logf("Expected result: 3")
	t.Logf("Actual result: %d", result)

	// Note: memstore uses len(ms.msgs) which is 6 (actual count)
	// So sort.Search searches [0,6) with FirstSeq=1
	// Indices map to: 1, 2, 3, 4, 5, 6
	// But seqs 4-7 are deleted, so indices 3-6 may hit gaps

	require_Equal(t, result, uint64(3))
}
