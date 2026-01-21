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

// Extended test coverage for GetSeqFromTime with various delete patterns.
// These tests complement the basic test in PR #7751 and cover additional edge cases.

// TestStoreGetSeqFromTimeLargeGap tests when the gap is larger than
// the remaining messages on either side.
func TestStoreGetSeqFromTimeLargeGap(t *testing.T) {
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

			// Delete middle 6 messages (4-9), leaving only 1, 2, 3 and 10
			for seq := uint64(4); seq <= 9; seq++ {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search for timestamp of message 3 - should find seq 3
			ts := time.Unix(0, timestamps[2]).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(3))

			// Search for timestamp between 3 and 10 - should find seq 10
			midTs := timestamps[2] + (timestamps[9]-timestamps[2])/2
			ts = time.Unix(0, midTs).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(10))
		},
	)
}

// TestStoreGetSeqFromTimeSingleMessageRemaining tests when only one message
// remains after deletions.
func TestStoreGetSeqFromTimeSingleMessageRemaining(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			var middleTs int64
			for i := range 5 {
				_, ts, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
				if i == 2 {
					middleTs = ts
				}
			}

			// Delete all but message 3
			for seq := uint64(1); seq <= 5; seq++ {
				if seq != 3 {
					_, err := fs.RemoveMsg(seq)
					require_NoError(t, err)
				}
			}

			// Search before the remaining message
			ts := time.Unix(0, middleTs-1).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(3))

			// Search at exact timestamp
			ts = time.Unix(0, middleTs).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(3))

			// Search after - should return last+1
			ts = time.Unix(0, middleTs+1).UTC()
			result := fs.GetSeqFromTime(ts)
			// Result should be > 3 (either 4, 5, or 6 depending on implementation)
			require_True(t, result > 3)
		},
	)
}

// TestStoreGetSeqFromTimeConsecutiveBlocks tests delete patterns that span
// multiple message blocks in filestore.
func TestStoreGetSeqFromTimeConsecutiveBlocks(t *testing.T) {
	// Use small block size to force multiple blocks
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 256 // Small block size
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

		// Delete messages to create gaps across blocks
		// Delete 5-10 and 15-18
		for seq := uint64(5); seq <= 10; seq++ {
			_, err := fs.RemoveMsg(seq)
			require_NoError(t, err)
		}
		for seq := uint64(15); seq <= 18; seq++ {
			_, err := fs.RemoveMsg(seq)
			require_NoError(t, err)
		}

		// Remaining: 1-4, 11-14, 19-20

		// Search in first segment
		ts := time.Unix(0, timestamps[1]).UTC() // seq 2
		require_Equal(t, fs.GetSeqFromTime(ts), uint64(2))

		// Search in second segment
		ts = time.Unix(0, timestamps[11]).UTC() // seq 12
		require_Equal(t, fs.GetSeqFromTime(ts), uint64(12))

		// Search in gap - should find next available
		ts = time.Unix(0, timestamps[6]).UTC() // seq 7 (deleted)
		require_Equal(t, fs.GetSeqFromTime(ts), uint64(11))
	})
}

// TestStoreGetSeqFromTimeAllButFirstDeleted tests when all messages except
// the first are deleted.
func TestStoreGetSeqFromTimeAllButFirstDeleted(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			var firstTs int64
			for i := range 5 {
				_, ts, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
				if i == 0 {
					firstTs = ts
				}
			}

			// Delete all but first
			for seq := uint64(2); seq <= 5; seq++ {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search at first timestamp
			ts := time.Unix(0, firstTs).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(1))

			// Search before first
			ts = time.Unix(0, firstTs-1).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(1))
		},
	)
}

// TestStoreGetSeqFromTimeAllButLastDeleted tests when all messages except
// the last are deleted.
func TestStoreGetSeqFromTimeAllButLastDeleted(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			var lastTs int64
			for range 5 {
				_, ts, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
				lastTs = ts
			}

			// Delete all but last
			for seq := uint64(1); seq <= 4; seq++ {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search before last timestamp - should find seq 5
			ts := time.Unix(0, lastTs-1).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(5))

			// Search at last timestamp
			ts = time.Unix(0, lastTs).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(5))
		},
	)
}

// TestStoreGetSeqFromTimeAlternatingDeletes tests when every other message
// is deleted, creating maximum fragmentation.
func TestStoreGetSeqFromTimeAlternatingDeletes(t *testing.T) {
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

			// Delete even sequences: 2, 4, 6, 8, 10
			// Remaining: 1, 3, 5, 7, 9
			for seq := uint64(2); seq <= 10; seq += 2 {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search for timestamp of seq 5 (index 4)
			ts := time.Unix(0, timestamps[4]).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(5))

			// Search for timestamp of deleted seq 6 - should find seq 7
			ts = time.Unix(0, timestamps[5]).UTC()
			require_Equal(t, fs.GetSeqFromTime(ts), uint64(7))
		},
	)
}
