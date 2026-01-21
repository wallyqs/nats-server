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

// TestGetSeqFromTimeEmptyStore tests behavior on an empty store.
func TestGetSeqFromTimeEmptyStore(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			// Don't store any messages - search on empty store
			ts := time.Now().UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Empty store, searching for current time")
			t.Logf("Result: %d", result)

			// Should return LastSeq+1 which is 1 for empty store
			require_Equal(t, result, uint64(1))
		},
	)
}

// TestGetSeqFromTimeAllMessagesDeleted tests when all messages have been deleted.
func TestGetSeqFromTimeAllMessagesDeleted(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			// Store and then delete all messages
			for i := range 5 {
				_, _, err := fs.StoreMsg("foo", nil, []byte{byte(i)}, 0)
				require_NoError(t, err)
			}

			// Delete all messages
			for seq := uint64(1); seq <= 5; seq++ {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			ts := time.Now().UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("All messages deleted, searching for current time")
			t.Logf("Result: %d", result)

			// Should return LastSeq+1 = 6
			require_Equal(t, result, uint64(6))
		},
	)
}

// TestGetSeqFromTimeVeryOldTimestamp tests searching for a timestamp
// way before any messages.
func TestGetSeqFromTimeVeryOldTimestamp(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			for range 5 {
				_, _, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
			}

			// Search for timestamp from year 2000
			ts := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			result := fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp from year 2000")
			t.Logf("Result: %d", result)

			// Should return FirstSeq = 1
			require_Equal(t, result, uint64(1))
		},
	)
}

// TestGetSeqFromTimeFarFutureTimestamp tests searching for a timestamp
// way after any messages.
func TestGetSeqFromTimeFarFutureTimestamp(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			for range 5 {
				_, _, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
			}

			// Search for timestamp from year 2100
			ts := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
			result := fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp from year 2100")
			t.Logf("Result: %d", result)

			// Should return LastSeq+1 = 6
			require_Equal(t, result, uint64(6))
		},
	)
}

// TestGetSeqFromTimeConsecutiveDeletes tests deleting messages one by one
// from the beginning while searching.
func TestGetSeqFromTimeConsecutiveDeletesFromStart(t *testing.T) {
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

			// Delete messages 1-7 from the start
			for seq := uint64(1); seq <= 7; seq++ {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search for timestamp of message 5 (deleted)
			// Should return message 8 (first available)
			ts := time.Unix(0, timestamps[4]).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Messages 1-7 deleted, searching for timestamp of seq 5")
			t.Logf("Expected: 8 (first remaining)")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(8))
		},
	)
}

// TestGetSeqFromTimeConsecutiveDeletesFromEnd tests deleting messages
// from the end while searching.
func TestGetSeqFromTimeConsecutiveDeletesFromEnd(t *testing.T) {
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

			// Delete messages 4-10 from the end
			for seq := uint64(4); seq <= 10; seq++ {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search for timestamp of message 3
			ts := time.Unix(0, timestamps[2]).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Messages 4-10 deleted, searching for timestamp of seq 3")
			t.Logf("Expected: 3")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(3))

			// Search for timestamp of message 5 (deleted)
			// Should return LastSeq+1 since no message with ts >= target exists
			ts = time.Unix(0, timestamps[4]).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp of deleted seq 5")
			t.Logf("Expected: 11 (LastSeq+1)")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(11))
		},
	)
}

// TestGetSeqFromTimeSparseDeletes tests a sparse deletion pattern
// (delete every 3rd message).
func TestGetSeqFromTimeSparseDeletes(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			timestamps := make([]int64, 15)
			for i := range 15 {
				_, ts, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
				timestamps[i] = ts
			}

			// Delete every 3rd message: 3, 6, 9, 12, 15
			// Remaining: 1, 2, 4, 5, 7, 8, 10, 11, 13, 14
			for seq := uint64(3); seq <= 15; seq += 3 {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search for timestamp of message 6 (deleted)
			// Should return message 7 (next available)
			ts := time.Unix(0, timestamps[5]).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Every 3rd message deleted, searching for timestamp of deleted seq 6")
			t.Logf("Expected: 7")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(7))

			// Search for timestamp of message 10
			ts = time.Unix(0, timestamps[9]).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp of seq 10")
			t.Logf("Expected: 10")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(10))
		},
	)
}

// TestGetSeqFromTimeTwoRemainingAtEnds tests when only first and last
// messages remain.
func TestGetSeqFromTimeTwoRemainingAtEnds(t *testing.T) {
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

			// Delete all except first and last: delete 2-9
			// Remaining: 1, 10
			for seq := uint64(2); seq <= 9; seq++ {
				_, err := fs.RemoveMsg(seq)
				require_NoError(t, err)
			}

			// Search for timestamp before first message
			ts := time.Unix(0, timestamps[0]-1).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Only seq 1 and 10 remain")
			t.Logf("Searching for timestamp before seq 1")
			t.Logf("Expected: 1")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(1))

			// Search for timestamp of first message
			ts = time.Unix(0, timestamps[0]).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp of seq 1")
			t.Logf("Expected: 1")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(1))

			// Search for timestamp between first and last (in the gap)
			midTs := timestamps[0] + (timestamps[9]-timestamps[0])/2
			ts = time.Unix(0, midTs).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp in the middle of gap")
			t.Logf("Expected: 10 (next available after gap)")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(10))

			// Search for timestamp of last message
			ts = time.Unix(0, timestamps[9]).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp of seq 10")
			t.Logf("Expected: 10")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(10))
		},
	)
}

// TestGetSeqFromTimeSingleMessage tests with only one message in the store.
func TestGetSeqFromTimeSingleMessage(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			_, msgTs, err := fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)

			// Search before the message
			ts := time.Unix(0, msgTs-1000).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Single message store")
			t.Logf("Searching before the message")
			t.Logf("Expected: 1")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(1))

			// Search at exact timestamp
			ts = time.Unix(0, msgTs).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching at exact timestamp")
			t.Logf("Expected: 1")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(1))

			// Search after the message
			ts = time.Unix(0, msgTs+1000).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching after the message")
			t.Logf("Expected: 2 (LastSeq+1)")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(2))
		},
	)
}

// TestGetSeqFromTimeTwoMessages tests with exactly two messages.
func TestGetSeqFromTimeTwoMessages(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			_, ts1, err := fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)
			_, ts2, err := fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)

			// Search between the two messages
			midTs := ts1 + (ts2-ts1)/2
			ts := time.Unix(0, midTs).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Two message store, searching between them")
			t.Logf("Expected: 2")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(2))
		},
	)
}

// TestGetSeqFromTimeTwoMessagesFirstDeleted tests with two messages
// where the first is deleted.
func TestGetSeqFromTimeTwoMessagesFirstDeleted(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			_, ts1, err := fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)
			_, ts2, err := fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)

			// Delete first message
			_, err = fs.RemoveMsg(1)
			require_NoError(t, err)

			// Search for timestamp of deleted first message
			ts := time.Unix(0, ts1).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Two messages, first deleted")
			t.Logf("Searching for timestamp of deleted seq 1")
			t.Logf("Expected: 2")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(2))

			// Search for timestamp of second message
			ts = time.Unix(0, ts2).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp of seq 2")
			t.Logf("Expected: 2")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(2))
		},
	)
}

// TestGetSeqFromTimeTwoMessagesSecondDeleted tests with two messages
// where the second is deleted.
func TestGetSeqFromTimeTwoMessagesSecondDeleted(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			_, ts1, err := fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)
			_, ts2, err := fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)

			// Delete second message
			_, err = fs.RemoveMsg(2)
			require_NoError(t, err)

			// Search for timestamp of first message
			ts := time.Unix(0, ts1).UTC()
			result := fs.GetSeqFromTime(ts)

			t.Logf("Two messages, second deleted")
			t.Logf("Searching for timestamp of seq 1")
			t.Logf("Expected: 1")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(1))

			// Search for timestamp of deleted second message
			// Should return LastSeq+1 = 3 since no message with ts >= ts2 exists
			ts = time.Unix(0, ts2).UTC()
			result = fs.GetSeqFromTime(ts)

			t.Logf("Searching for timestamp of deleted seq 2")
			t.Logf("Expected: 3 (LastSeq+1)")
			t.Logf("Actual: %d", result)

			require_Equal(t, result, uint64(3))
		},
	)
}
