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

// TestFileStoreGetSeqFromTimeAllBlockDeleted tests what happens when
// selectMsgBlockForStart returns a block where ALL messages have been deleted.
// The search should ideally find the next valid message in a subsequent block.
func TestFileStoreGetSeqFromTimeAllBlockDeleted(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 256 // Small block size to force multiple blocks
		cfg := StreamConfig{Name: "zzz", Subjects: []string{"foo"}, Storage: FileStorage}
		fs, err := newFileStoreWithCreated(fcfg, cfg, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Store messages to create multiple blocks
		var block1LastTs, block2FirstTs int64
		for i := range 20 {
			_, ts, err := fs.StoreMsg("foo", nil, []byte("data"), 0)
			require_NoError(t, err)
			// Track timestamps around block boundary
			if i == 4 {
				block1LastTs = ts
			}
			if i == 5 {
				block2FirstTs = ts
			}
		}

		// Verify we have multiple blocks
		fs.mu.RLock()
		numBlocks := len(fs.blks)
		fs.mu.RUnlock()
		if numBlocks < 2 {
			t.Skip("Need multiple blocks for this test")
		}

		// Delete ALL messages in the first block
		// First, figure out which sequences are in block 1
		fs.mu.RLock()
		blk1FirstSeq := fs.blks[0].first.seq
		blk1LastSeq := fs.blks[0].last.seq
		fs.mu.RUnlock()

		t.Logf("Block 1: seq %d-%d", blk1FirstSeq, blk1LastSeq)
		t.Logf("Block 1 last ts: %d, Block 2 first ts: %d", block1LastTs, block2FirstTs)

		for seq := blk1FirstSeq; seq <= blk1LastSeq; seq++ {
			_, err := fs.RemoveMsg(seq)
			require_NoError(t, err)
		}

		// Now search for a timestamp that would have been in block 1
		// Since all block 1 messages are deleted, we should get the first
		// message from block 2
		ts := time.Unix(0, block1LastTs).UTC()
		result := fs.GetSeqFromTime(ts)

		t.Logf("Searching for timestamp in deleted block 1")
		t.Logf("Result: %d", result)

		// The result should be the first message of block 2 (blk1LastSeq + 1)
		// or at least a valid sequence > blk1LastSeq
		if result == 0 {
			t.Errorf("GetSeqFromTime returned 0, expected first message of block 2")
		}
		if result <= blk1LastSeq {
			t.Errorf("GetSeqFromTime returned %d which is in deleted block 1 range", result)
		}
	})
}

// TestFileStoreGetSeqFromTimeBlockBoundary tests searching for a timestamp
// exactly at a block boundary.
func TestFileStoreGetSeqFromTimeBlockBoundary(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 256
		cfg := StreamConfig{Name: "zzz", Subjects: []string{"foo"}, Storage: FileStorage}
		fs, err := newFileStoreWithCreated(fcfg, cfg, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		timestamps := make([]int64, 20)
		for i := range 20 {
			_, ts, err := fs.StoreMsg("foo", nil, []byte("data"), 0)
			require_NoError(t, err)
			timestamps[i] = ts
		}

		// Verify multiple blocks
		fs.mu.RLock()
		numBlocks := len(fs.blks)
		var blk1LastSeq uint64
		if numBlocks >= 2 {
			blk1LastSeq = fs.blks[0].last.seq
		}
		fs.mu.RUnlock()

		if numBlocks < 2 {
			t.Skip("Need multiple blocks for this test")
		}

		// Search for exact timestamp of last message in block 1
		ts := time.Unix(0, timestamps[blk1LastSeq-1]).UTC()
		result := fs.GetSeqFromTime(ts)

		t.Logf("Block 1 last seq: %d", blk1LastSeq)
		t.Logf("Searching for timestamp of seq %d", blk1LastSeq)
		t.Logf("Result: %d", result)

		require_Equal(t, result, blk1LastSeq)
	})
}

// TestGetSeqFromTimeSearchForDeletedTimestamp tests searching for a timestamp
// that belonged to a deleted message.
func TestGetSeqFromTimeSearchForDeletedTimestamp(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			timestamps := make([]int64, 10)
			for i := range 10 {
				_, ts, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
				timestamps[i] = ts
			}

			// Delete message 5
			_, err := fs.RemoveMsg(5)
			require_NoError(t, err)

			// Search for the exact timestamp of deleted message 5
			// Should return message 6 (first message with ts >= target)
			ts := time.Unix(0, timestamps[4]).UTC() // timestamps[4] is seq 5
			result := fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp of deleted seq 5: %d", timestamps[4])
			t.Logf("Expected: 6 (next valid message)")
			t.Logf("Actual: %d", result)

			// Since message 5 is deleted, and message 6 has ts > message 5's ts,
			// we should get message 6
			require_Equal(t, result, uint64(6))
		},
	)
}

// TestGetSeqFromTimeFirstMessageDeleted tests when the first message is deleted
// and we search for a timestamp before the new first message.
func TestGetSeqFromTimeFirstMessageDeleted(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			timestamps := make([]int64, 5)
			for i := range 5 {
				_, ts, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
				timestamps[i] = ts
			}

			// Delete the first message
			_, err := fs.RemoveMsg(1)
			require_NoError(t, err)

			// Search for timestamp BEFORE the original first message
			// Should return the new first message (seq 2)
			ts := time.Unix(0, timestamps[0]-1000).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("First message deleted, searching for timestamp before original first")
			t.Logf("Expected: 2 (new first message)")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(2))
		},
	)
}

// TestGetSeqFromTimeAllButOneDeleted tests when all messages except one in the
// middle are deleted.
func TestGetSeqFromTimeAllButOneDeleted(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			timestamps := make([]int64, 10)
			for i := range 10 {
				_, ts, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
				timestamps[i] = ts
			}

			// Delete all except message 5
			for seq := uint64(1); seq <= 10; seq++ {
				if seq != 5 {
					_, err := fs.RemoveMsg(seq)
					require_NoError(t, err)
				}
			}

			// Search for timestamp before message 5
			ts := time.Unix(0, timestamps[4]-1).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Only message 5 remains, searching for earlier timestamp")
			t.Logf("Expected: 5")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(5))

			// Search for exact timestamp of message 5
			ts = time.Unix(0, timestamps[4]).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for exact timestamp of message 5")
			t.Logf("Expected: 5")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(5))

			// Search for timestamp after message 5
			ts = time.Unix(0, timestamps[4]+1).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp after message 5")
			t.Logf("Expected: > 5 (LastSeq+1 = 11)")
			t.Logf("Actual: %d", result)

			// Should return LastSeq+1 since no message with ts >= target exists
			if result <= 5 {
				t.Errorf("Expected result > 5, got %d", result)
			}
		},
	)
}
