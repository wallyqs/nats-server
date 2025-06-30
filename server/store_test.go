// Copyright 2012-2025 The NATS Authors
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

//go:build !skip_store_tests

package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server/gsl"
)

func testAllStoreAllPermutations(t *testing.T, compressionAndEncryption bool, cfg StreamConfig, fn func(t *testing.T, fs StreamStore)) {
	t.Run("Memory", func(t *testing.T) {
		cfg.Storage = MemoryStorage
		fs, err := newMemStore(&cfg)
		require_NoError(t, err)
		defer fs.Stop()
		fn(t, fs)
	})
	t.Run("File", func(t *testing.T) {
		cfg.Storage = FileStorage
		if compressionAndEncryption {
			testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
				fs, err := newFileStore(fcfg, cfg)
				require_NoError(t, err)
				defer fs.Stop()
				fn(t, fs)
			})
		} else {
			fs, err := newFileStore(FileStoreConfig{
				StoreDir: t.TempDir(),
			}, cfg)
			require_NoError(t, err)
			defer fs.Stop()
			fn(t, fs)
		}
	})
}

// Helper function to compare results between SimpleSublist and old Sublist
func compareWithOldSublist(t *testing.T, fs StreamStore, subjects []string, startSeq uint64) {
	t.Helper()
	
	// Create new SimpleSublist
	sl := gsl.NewSublist[struct{}]()
	for _, subj := range subjects {
		sl.Insert(subj, struct{}{})
	}
	
	// Create old Sublist
	oldSl := NewSublistNoCache()
	for _, subj := range subjects {
		oldSl.Insert(&subscription{subject: []byte(subj)})
	}
	
	// For comparison, we'll collect all matching subjects from the old sublist
	var oldMatches []string
	for seq := startSeq; seq <= fs.State().LastSeq; seq++ {
		var smv StoreMsg
		sm, err := fs.LoadMsg(seq, &smv)
		if err != nil {
			continue
		}
		// Check if subject matches any in the old sublist
		if r := oldSl.Match(sm.subj); len(r.psubs) > 0 || len(r.qsubs) > 0 {
			oldMatches = append(oldMatches, sm.subj)
		}
	}
	
	// Collect all matches using the new SimpleSublist
	var newMatches []string
	var smv StoreMsg
	for seq := startSeq; ; {
		sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
		if err != nil {
			break
		}
		newMatches = append(newMatches, sm.subj)
		seq = nseq + 1
	}
	
	// Compare results
	require_Equal(t, len(newMatches), len(oldMatches))
	for i := range newMatches {
		require_Equal(t, newMatches[i], oldMatches[i])
	}
}

// Helper function to compare NumPendingMulti results between SimpleSublist and old Sublist
func compareNumPendingWithOldSublist(t *testing.T, fs StreamStore, subjects []string, startSeq uint64) {
	t.Helper()
	
	// Create new SimpleSublist
	sl := gsl.NewSublist[struct{}]()
	for _, subj := range subjects {
		sl.Insert(subj, struct{}{})
	}
	
	// Create old Sublist
	oldSl := NewSublistNoCache()
	for _, subj := range subjects {
		oldSl.Insert(&subscription{subject: []byte(subj)})
	}
	
	// Count matches using old sublist
	oldCount := uint64(0)
	for seq := startSeq; seq <= fs.State().LastSeq; seq++ {
		var smv StoreMsg
		sm, err := fs.LoadMsg(seq, &smv)
		if err != nil {
			continue
		}
		// Check if subject matches any in the old sublist
		if r := oldSl.Match(sm.subj); len(r.psubs) > 0 || len(r.qsubs) > 0 {
			oldCount++
		}
	}
	
	// Get count using new SimpleSublist
	newCount, _ := fs.NumPendingMulti(startSeq, sl, false)
	
	// Compare results
	require_Equal(t, newCount, oldCount)
}

func TestStoreMsgLoadNextMsgMulti(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"foo.*"}},
		func(t *testing.T, fs StreamStore) {
			// Put 1k msgs in
			for i := 0; i < 1000; i++ {
				subj := fmt.Sprintf("foo.%d", i)
				fs.StoreMsg(subj, nil, []byte("ZZZ"), 0)
			}

			var smv StoreMsg
			// Do multi load next with 1 wc entry.
			sl := gsl.NewSublist[struct{}]()
			sl.Insert("foo.>", struct{}{})
			for i, seq := 0, uint64(1); i < 1000; i++ {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				require_NoError(t, err)
				require_Equal(t, sm.subj, fmt.Sprintf("foo.%d", i))
				require_Equal(t, nseq, seq)
				seq++
			}
			
			// Compare with old Sublist implementation
			compareWithOldSublist(t, fs, []string{"foo.>"}, 1)

			// Now do multi load next with 1000 literal subjects.
			sl = gsl.NewSublist[struct{}]()
			var literalSubjects []string
			for i := 0; i < 1000; i++ {
				subj := fmt.Sprintf("foo.%d", i)
				sl.Insert(subj, struct{}{})
				literalSubjects = append(literalSubjects, subj)
			}
			for i, seq := 0, uint64(1); i < 1000; i++ {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				require_NoError(t, err)
				require_Equal(t, sm.subj, fmt.Sprintf("foo.%d", i))
				require_Equal(t, nseq, seq)
				seq++
			}
			
			// Compare with old Sublist implementation for literals
			compareWithOldSublist(t, fs, literalSubjects, 1)

			// Check that we can pull out 3 individuals.
			sl = gsl.NewSublist[struct{}]()
			threeSubjects := []string{"foo.2", "foo.222", "foo.999"}
			for _, subj := range threeSubjects {
				sl.Insert(subj, struct{}{})
			}
			sm, seq, err := fs.LoadNextMsgMulti(sl, 1, &smv)
			require_NoError(t, err)
			require_Equal(t, sm.subj, "foo.2")
			require_Equal(t, seq, 3)
			sm, seq, err = fs.LoadNextMsgMulti(sl, seq+1, &smv)
			require_NoError(t, err)
			require_Equal(t, sm.subj, "foo.222")
			require_Equal(t, seq, 223)
			sm, seq, err = fs.LoadNextMsgMulti(sl, seq+1, &smv)
			require_NoError(t, err)
			require_Equal(t, sm.subj, "foo.999")
			require_Equal(t, seq, 1000)
			_, seq, err = fs.LoadNextMsgMulti(sl, seq+1, &smv)
			require_Error(t, err)
			require_Equal(t, seq, 1000)
			
			// Compare with old Sublist implementation for specific subjects
			compareWithOldSublist(t, fs, threeSubjects, 1)

			// Test with mixed wildcards and literals
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.*", struct{}{}) // matches all foo.N subjects
			sl.Insert("foo.5", struct{}{}) // specific literal (redundant but tests mixed usage)
			sl.Insert("foo.999", struct{}{}) // specific literal (redundant but tests mixed usage)
			// First match should be foo.0
			sm, seq, err = fs.LoadNextMsgMulti(sl, 1, &smv)
			require_NoError(t, err)
			require_Equal(t, sm.subj, "foo.0")
			require_Equal(t, seq, 1)
			// Next match should be foo.1
			sm, seq, err = fs.LoadNextMsgMulti(sl, seq+1, &smv)
			require_NoError(t, err)
			require_Equal(t, sm.subj, "foo.1")
			require_Equal(t, seq, 2)

			// Test with specific subject ranges
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.20", struct{}{})
			sl.Insert("foo.200", struct{}{})
			sl.Insert("foo.299", struct{}{})
			count := 0
			for seq := uint64(1); seq <= 1000; {
				_, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				count++
				seq = nseq + 1
			}
			require_Equal(t, count, 3) // Should match exactly foo.20, foo.200, foo.299

			// Test with full wildcard and specific exclusions
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.*", struct{}{})
			count = 0
			for seq := uint64(1); seq <= 1000; {
				_, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				count++
				seq = nseq + 1
			}
			require_Equal(t, count, 1000)

			// Test empty sublist
			sl = gsl.NewSublist[struct{}]()
			_, _, err = fs.LoadNextMsgMulti(sl, 1, &smv)
			require_Error(t, err)

			// Test with specific subjects in numeric ranges
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.1", struct{}{})
			sl.Insert("foo.10", struct{}{})
			sl.Insert("foo.100", struct{}{})
			matches := make(map[string]bool)
			for seq := uint64(1); seq <= 1000; {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				matches[sm.subj] = true
				seq = nseq + 1
			}
			// Should match exactly foo.1, foo.10, foo.100
			require_Equal(t, len(matches), 3)

			// Test NumPendingMulti with various patterns
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.>", struct{}{})
			total, _ := fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, 1000)
			
			// Compare NumPendingMulti with old Sublist
			compareNumPendingWithOldSublist(t, fs, []string{"foo.>"}, 1)

			sl = gsl.NewSublist[struct{}]()
			specificSubjects := []string{"foo.2", "foo.20", "foo.200"}
			for _, subj := range specificSubjects {
				sl.Insert(subj, struct{}{})
			}
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, 3)
			
			// Compare NumPendingMulti with old Sublist
			compareNumPendingWithOldSublist(t, fs, specificSubjects, 1)

			// Test with specific subject that doesn't exist
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.1001", struct{}{})
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, 0)

			// Test edge cases and boundary conditions
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.0", struct{}{}) // First subject
			sl.Insert("foo.999", struct{}{}) // Last subject
			count = 0
			for seq := uint64(1); seq <= 1000; {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				require_True(t, sm.subj == "foo.0" || sm.subj == "foo.999")
				count++
				seq = nseq + 1
			}
			require_Equal(t, count, 2)

			// Test with overlapping filters
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("foo.*", struct{}{}) // Matches all
			sl.Insert("foo.5", struct{}{}) // Redundant specific match
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, 1000) // Should still be 1000, not double-counted
		},
	)
}

func TestStoreMsgLoadNextMsgMultiHierarchical(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"events.>"}},
		func(t *testing.T, fs StreamStore) {
			// Store hierarchical subjects
			subjects := []string{
				"events.user.login",
				"events.user.logout", 
				"events.user.signup",
				"events.order.created",
				"events.order.cancelled",
				"events.payment.success",
				"events.payment.failed",
				"events.system.startup",
				"events.system.shutdown",
				"events.metrics.cpu",
			}
			
			for i, subj := range subjects {
				_, _, err := fs.StoreMsg(subj, nil, []byte(fmt.Sprintf("msg%d", i)), 0)
				require_NoError(t, err)
			}
			
			var smv StoreMsg
			
			// Test with > wildcard (multi-level)
			sl := gsl.NewSublist[struct{}]()
			sl.Insert("events.>", struct{}{})
			total, _ := fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, uint64(len(subjects)))
			
			// Compare with old Sublist implementation
			compareWithOldSublist(t, fs, []string{"events.>"}, 1)
			
			// Test with * wildcard (single level)
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("events.user.*", struct{}{})
			count := 0
			for seq := uint64(1); seq <= uint64(len(subjects)); {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				require_True(t, strings.HasPrefix(sm.subj, "events.user."))
				count++
				seq = nseq + 1
			}
			require_Equal(t, count, 3) // login, logout, signup
			
			// Compare with old Sublist implementation
			compareWithOldSublist(t, fs, []string{"events.user.*"}, 1)
			
			// Test with mixed hierarchical patterns
			sl = gsl.NewSublist[struct{}]()
			mixedSubjects := []string{"events.order.*", "events.payment.*"}
			for _, subj := range mixedSubjects {
				sl.Insert(subj, struct{}{})
			}
			count = 0
			for seq := uint64(1); seq <= uint64(len(subjects)); {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				require_True(t, strings.HasPrefix(sm.subj, "events.order.") || strings.HasPrefix(sm.subj, "events.payment."))
				count++
				seq = nseq + 1
			}
			require_Equal(t, count, 4) // created, cancelled, success, failed
			
			// Compare with old Sublist implementation
			compareWithOldSublist(t, fs, mixedSubjects, 1)
			
			// Test NumPendingMulti with hierarchical patterns
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("events.user.*", struct{}{})
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, uint64(3))
			
			// Compare NumPendingMulti with old Sublist
			compareNumPendingWithOldSublist(t, fs, []string{"events.user.*"}, 1)
			
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("events.system.*", struct{}{})
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, uint64(2))
			
			// Test with non-matching hierarchical pattern
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("events.nonexistent.*", struct{}{})
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, uint64(0))
		},
	)
}

func TestStoreMsgLoadNextMsgMultiDeeplyNested(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "zzz", Subjects: []string{"A.>"}},
		func(t *testing.T, fs StreamStore) {
			// Store deeply nested hierarchical subjects
			subjects := []string{
				// 7-level deep subjects
				"A.B.C.D.E.F.G",     // 1 - under A.B
				"A.B.C.D.E.F.H",     // 2 - under A.B
				"A.B.C.D.E.F.I",     // 3 - under A.B
				"A.B.C.D.E.G.H",     // 4 - under A.B
				"A.B.C.D.E.G.I",     // 5 - under A.B
				// 6-level deep subjects
				"A.B.C.D.E.F",       // 6 - under A.B
				"A.B.C.D.E.G",       // 7 - under A.B
				"A.B.C.D.E.H",       // 8 - under A.B
				// 5-level deep subjects
				"A.B.C.D.E",         // 9 - under A.B
				"A.B.C.D.F",         // 10 - under A.B
				"A.B.C.X.Y",         // 11 - under A.B
				// Different branches
				"A.B.X.Y.Z.W.Q",     // 12 - under A.B
				"A.B.X.Y.Z.W.R",     // 13 - under A.B
				"A.C.D.E.F.G.H",     // NOT under A.B
				"A.C.D.E.F.G.I",     // NOT under A.B
				// Mixed depths
				"A.B.C",             // 14 - under A.B
				"A.B.D",             // 15 - under A.B
				"A.X.Y.Z",           // NOT under A.B
				// Very deep - 10 levels
				"A.B.C.D.E.F.G.H.I.J", // 16 - under A.B
				"A.B.C.D.E.F.G.H.I.K", // 17 - under A.B
			}
			
			for i, subj := range subjects {
				_, _, err := fs.StoreMsg(subj, nil, []byte(fmt.Sprintf("msg%d", i)), 0)
				require_NoError(t, err)
			}
			
			var smv StoreMsg
			
			// Test with > wildcard at different levels
			testCases := []struct {
				pattern  string
				expected int
				desc     string
			}{
				{"A.>", len(subjects), "all subjects under A"},
				{"A.B.>", 17, "all subjects under A.B"},
				{"A.B.C.>", 13, "all subjects under A.B.C"},
				{"A.B.C.D.>", 12, "all subjects under A.B.C.D"},
				{"A.B.C.D.E.>", 10, "all subjects under A.B.C.D.E"},
				{"A.B.C.D.E.F.>", 5, "all subjects under A.B.C.D.E.F"},
				{"A.B.C.D.E.F.G.>", 2, "all subjects under A.B.C.D.E.F.G"},
				{"A.C.>", 2, "all subjects under A.C"},
			}
			
			for _, tc := range testCases {
				sl := gsl.NewSublist[struct{}]()
				sl.Insert(tc.pattern, struct{}{})
				total, _ := fs.NumPendingMulti(1, sl, false)
				require_Equal(t, total, uint64(tc.expected))
				
				// Verify with old sublist
				compareNumPendingWithOldSublist(t, fs, []string{tc.pattern}, 1)
				
				// Count actual matches
				count := 0
				for seq := uint64(1); seq <= uint64(len(subjects)); {
					_, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
					if err != nil {
						break
					}
					count++
					seq = nseq + 1
				}
				require_Equal(t, count, tc.expected)
			}
			
			// Test with * wildcard at specific levels - verify they work
			sl := gsl.NewSublist[struct{}]()
			sl.Insert("A.B.C.D.E.F.*", struct{}{})
			total, _ := fs.NumPendingMulti(1, sl, false)
			require_True(t, total > 0) // Should match G, H, I
			compareNumPendingWithOldSublist(t, fs, []string{"A.B.C.D.E.F.*"}, 1)
			
			// Test deep wildcard patterns
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("A.*.*.*.*.*.G", struct{}{})
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_True(t, total > 0) // Should match some 7-level paths ending in G
			compareNumPendingWithOldSublist(t, fs, []string{"A.*.*.*.*.*.G"}, 1)
			
			// Test complex mixed patterns
			sl = gsl.NewSublist[struct{}]()
			complexPatterns := []string{
				"A.B.C.D.E.F.>",  // Matches some deep subjects
				"A.C.D.E.F.G.*",  // Matches some subjects under A.C
			}
			for _, pattern := range complexPatterns {
				sl.Insert(pattern, struct{}{})
			}
			
			// Count unique matches
			matches := make(map[string]bool)
			for seq := uint64(1); seq <= uint64(len(subjects)); {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				matches[sm.subj] = true
				seq = nseq + 1
			}
			require_True(t, len(matches) > 0) // Should match some subjects
			
			// Compare with old sublist
			compareWithOldSublist(t, fs, complexPatterns, 1)
			
			// Test very specific deep patterns
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("A.B.C.D.E.F.G.H.I.J", struct{}{})
			sl.Insert("A.B.C.D.E.F.G.H.I.K", struct{}{})
			count := 0
			for seq := uint64(1); seq <= uint64(len(subjects)); {
				sm, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				require_True(t, sm.subj == "A.B.C.D.E.F.G.H.I.J" || sm.subj == "A.B.C.D.E.F.G.H.I.K")
				count++
				seq = nseq + 1
			}
			require_Equal(t, count, 2)
			
			// Test edge case: pattern deeper than any subject
			sl = gsl.NewSublist[struct{}]()
			sl.Insert("A.B.C.D.E.F.G.H.I.J.K.L.M.N.O.P", struct{}{})
			total, _ = fs.NumPendingMulti(1, sl, false)
			require_Equal(t, total, 0)
			
			// Test partial match at various depths
			partialPatterns := []string{
				"A.B.C.D.E.F",
				"A.B.C.D.E.G",
			}
			sl = gsl.NewSublist[struct{}]()
			for _, pattern := range partialPatterns {
				sl.Insert(pattern, struct{}{})
			}
			
			exactMatches := 0
			for seq := uint64(1); seq <= uint64(len(subjects)); {
				_, nseq, err := fs.LoadNextMsgMulti(sl, seq, &smv)
				if err != nil {
					break
				}
				exactMatches++
				seq = nseq + 1
			}
			require_Equal(t, exactMatches, 2) // A.B.C.D.E.F and A.B.C.D.E.G
			
			// Final comparison with old sublist for all patterns
			compareWithOldSublist(t, fs, partialPatterns, 1)
		},
	)
}

func TestStoreDeleteSlice(t *testing.T) {
	ds := DeleteSlice{2}
	var deletes []uint64
	ds.Range(func(seq uint64) bool {
		deletes = append(deletes, seq)
		return true
	})
	require_Len(t, len(deletes), 1)
	require_Equal(t, deletes[0], 2)

	first, last, num := ds.State()
	require_Equal(t, first, 2)
	require_Equal(t, last, 2)
	require_Equal(t, num, 1)
}

func TestStoreDeleteRange(t *testing.T) {
	dr := DeleteRange{First: 2, Num: 1}
	var deletes []uint64
	dr.Range(func(seq uint64) bool {
		deletes = append(deletes, seq)
		return true
	})
	require_Len(t, len(deletes), 1)
	require_Equal(t, deletes[0], 2)

	first, last, num := dr.State()
	require_Equal(t, first, 2)
	require_Equal(t, last, 2)
	require_Equal(t, num, 1)
}

func TestStoreSubjectStateConsistency(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "TEST", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			getSubjectState := func() SimpleState {
				t.Helper()
				ss := fs.SubjectsState("foo")
				return ss["foo"]
			}
			var smp StoreMsg
			expectFirstSeq := func(eseq uint64) {
				t.Helper()
				sm, _, err := fs.LoadNextMsg("foo", false, 0, &smp)
				require_NoError(t, err)
				require_Equal(t, sm.seq, eseq)
			}
			expectLastSeq := func(eseq uint64) {
				t.Helper()
				sm, err := fs.LoadLastMsg("foo", &smp)
				require_NoError(t, err)
				require_Equal(t, sm.seq, eseq)
			}

			// Publish an initial batch of messages.
			for i := 0; i < 4; i++ {
				_, _, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
			}

			// Expect 4 msgs, with first=1, last=4.
			ss := getSubjectState()
			require_Equal(t, ss.Msgs, 4)
			require_Equal(t, ss.First, 1)
			expectFirstSeq(1)
			require_Equal(t, ss.Last, 4)
			expectLastSeq(4)

			// Remove first message, ss.First is lazy so will only mark ss.firstNeedsUpdate.
			removed, err := fs.RemoveMsg(1)
			require_NoError(t, err)
			require_True(t, removed)

			// Will update first, so corrects to seq 2.
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 3)
			require_Equal(t, ss.First, 2)
			expectFirstSeq(2)
			require_Equal(t, ss.Last, 4)
			expectLastSeq(4)

			// Remove last message, ss.Last is lazy so will only mark ss.lastNeedsUpdate.
			removed, err = fs.RemoveMsg(4)
			require_NoError(t, err)
			require_True(t, removed)

			// Will update last, so corrects to 3.
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 2)
			require_Equal(t, ss.First, 2)
			expectFirstSeq(2)
			require_Equal(t, ss.Last, 3)
			expectLastSeq(3)

			// Remove first message again.
			removed, err = fs.RemoveMsg(2)
			require_NoError(t, err)
			require_True(t, removed)

			// Since we only have one message left, must update ss.First and ensure ss.Last equals.
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 1)
			require_Equal(t, ss.First, 3)
			expectFirstSeq(3)
			require_Equal(t, ss.Last, 3)
			expectLastSeq(3)

			// Publish some more messages so we can test another scenario.
			for i := 0; i < 3; i++ {
				_, _, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
			}

			// Just check the state is complete again.
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 4)
			require_Equal(t, ss.First, 3)
			expectFirstSeq(3)
			require_Equal(t, ss.Last, 7)
			expectLastSeq(7)

			// Remove last sequence, ss.Last is lazy so doesn't get updated.
			removed, err = fs.RemoveMsg(7)
			require_NoError(t, err)
			require_True(t, removed)

			// Remove first sequence, ss.First is lazy so doesn't get updated.
			removed, err = fs.RemoveMsg(3)
			require_NoError(t, err)
			require_True(t, removed)

			// Remove (now) first sequence. Both ss.First and ss.Last are lazy and both need to be recalculated later.
			removed, err = fs.RemoveMsg(5)
			require_NoError(t, err)
			require_True(t, removed)

			// ss.First and ss.Last should both be recalculated and equal each other.
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 1)
			require_Equal(t, ss.First, 6)
			expectFirstSeq(6)
			require_Equal(t, ss.Last, 6)
			expectLastSeq(6)

			// We store a new message for ss.Last and remove it after, which marks it to be recalculated.
			_, _, err = fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)
			removed, err = fs.RemoveMsg(8)
			require_NoError(t, err)
			require_True(t, removed)
			// This will be the new ss.Last message, so reset ss.lastNeedsUpdate
			_, _, err = fs.StoreMsg("foo", nil, nil, 0)
			require_NoError(t, err)

			// ss.First should remain the same, but ss.Last should equal the last message.
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 2)
			require_Equal(t, ss.First, 6)
			expectFirstSeq(6)
			require_Equal(t, ss.Last, 9)
			expectLastSeq(9)
		},
	)
}

func TestStoreSubjectStateConsistencyOptimization(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "TEST", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			fillMsgs := func(c int) {
				t.Helper()
				for i := 0; i < c; i++ {
					_, _, err := fs.StoreMsg("foo", nil, nil, 0)
					require_NoError(t, err)
				}
			}
			removeMsgs := func(seqs ...uint64) {
				t.Helper()
				for _, seq := range seqs {
					removed, err := fs.RemoveMsg(seq)
					require_NoError(t, err)
					require_True(t, removed)
				}
			}
			getSubjectState := func() (ss *SimpleState) {
				t.Helper()
				if f, ok := fs.(*fileStore); ok {
					ss, ok = f.lmb.fss.Find([]byte("foo"))
					require_True(t, ok)
				} else if ms, ok := fs.(*memStore); ok {
					ss, ok = ms.fss.Find([]byte("foo"))
					require_True(t, ok)
				} else {
					t.Fatal("Store not supported")
				}
				return ss
			}
			var smp StoreMsg
			expectSeq := func(seq uint64) {
				t.Helper()
				sm, _, err := fs.LoadNextMsg("foo", false, 0, &smp)
				require_NoError(t, err)
				require_Equal(t, sm.seq, seq)
				sm, err = fs.LoadLastMsg("foo", &smp)
				require_NoError(t, err)
				require_Equal(t, sm.seq, seq)
			}

			// results in ss.Last, ss.First is marked lazy (when we hit ss.Msgs-1==1).
			fillMsgs(3)
			removeMsgs(2, 1)
			ss := getSubjectState()
			require_Equal(t, ss.Msgs, 1)
			require_Equal(t, ss.First, 3)
			require_Equal(t, ss.Last, 3)
			require_False(t, ss.firstNeedsUpdate)
			require_False(t, ss.lastNeedsUpdate)
			expectSeq(3)

			// ss.First is marked lazy first, then ss.Last is marked lazy (when we hit ss.Msgs-1==1).
			fillMsgs(2)
			removeMsgs(3, 5)
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 1)
			require_Equal(t, ss.First, 3)
			require_Equal(t, ss.Last, 5)
			require_True(t, ss.firstNeedsUpdate)
			require_True(t, ss.lastNeedsUpdate)
			expectSeq(4)

			// ss.Last is marked lazy first, then ss.First is marked lazy (when we hit ss.Msgs-1==1).
			fillMsgs(2)
			removeMsgs(7, 4)
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 1)
			require_Equal(t, ss.First, 4)
			require_Equal(t, ss.Last, 7)
			require_True(t, ss.firstNeedsUpdate)
			require_True(t, ss.lastNeedsUpdate)
			expectSeq(6)

			// ss.Msgs=1, results in ss.First, ss.Last is marked lazy (when we hit ss.Msgs-1==1).
			fillMsgs(2)
			removeMsgs(9, 8)
			ss = getSubjectState()
			require_Equal(t, ss.Msgs, 1)
			require_Equal(t, ss.First, 6)
			require_Equal(t, ss.Last, 6)
			require_False(t, ss.firstNeedsUpdate)
			require_False(t, ss.lastNeedsUpdate)
			expectSeq(6)
		},
	)
}

func TestStoreMaxMsgsPerUpdateBug(t *testing.T) {
	config := func() StreamConfig {
		return StreamConfig{Name: "TEST", Subjects: []string{"foo"}, MaxMsgsPer: 0}
	}
	testAllStoreAllPermutations(
		t, false, config(),
		func(t *testing.T, fs StreamStore) {
			for i := 0; i < 5; i++ {
				_, _, err := fs.StoreMsg("foo", nil, nil, 0)
				require_NoError(t, err)
			}

			ss := fs.State()
			require_Equal(t, ss.Msgs, 5)
			require_Equal(t, ss.FirstSeq, 1)
			require_Equal(t, ss.LastSeq, 5)

			// Update max messages per-subject from 0 (infinite) to 1.
			// Since the per-subject limit was not specified before, messages should be removed upon config update.
			cfg := config()
			if _, ok := fs.(*fileStore); ok {
				cfg.Storage = FileStorage
			} else {
				cfg.Storage = MemoryStorage
			}
			cfg.MaxMsgsPer = 1
			err := fs.UpdateConfig(&cfg)
			require_NoError(t, err)

			// Only one message should remain.
			ss = fs.State()
			require_Equal(t, ss.Msgs, 1)
			require_Equal(t, ss.FirstSeq, 5)
			require_Equal(t, ss.LastSeq, 5)

			// Update max messages per-subject from 0 (infinite) to an invalid value (< -1).
			cfg.MaxMsgsPer = -2
			err = fs.UpdateConfig(&cfg)
			require_NoError(t, err)
			require_Equal(t, cfg.MaxMsgsPer, -1)
		},
	)
}

func TestStoreCompactCleansUpDmap(t *testing.T) {
	config := func() StreamConfig {
		return StreamConfig{Name: "TEST", Subjects: []string{"foo"}, MaxMsgsPer: 0}
	}
	for cseq := uint64(2); cseq <= 4; cseq++ {
		t.Run(fmt.Sprintf("Compact(%d)", cseq), func(t *testing.T) {
			testAllStoreAllPermutations(
				t, false, config(),
				func(t *testing.T, fs StreamStore) {
					dmapEntries := func() int {
						if fss, ok := fs.(*fileStore); ok {
							return fss.dmapEntries()
						} else if mss, ok := fs.(*memStore); ok {
							mss.mu.RLock()
							defer mss.mu.RUnlock()
							return mss.dmap.Size()
						} else {
							return 0
						}
					}

					// Publish messages, should have no interior deletes.
					for i := 0; i < 3; i++ {
						_, _, err := fs.StoreMsg("foo", nil, nil, 0)
						require_NoError(t, err)
					}
					require_Len(t, dmapEntries(), 0)

					// Removing one message in the middle should be an interior delete.
					_, err := fs.RemoveMsg(2)
					require_NoError(t, err)
					require_Len(t, dmapEntries(), 1)

					// Compacting must always clean up the interior delete.
					_, err = fs.Compact(cseq)
					require_NoError(t, err)
					require_Len(t, dmapEntries(), 0)

					// Validate first/last sequence.
					state := fs.State()
					fseq := uint64(3)
					if fseq < cseq {
						fseq = cseq
					}
					require_Equal(t, state.FirstSeq, fseq)
					require_Equal(t, state.LastSeq, 3)
				})
		})
	}
}

func TestStoreTruncateCleansUpDmap(t *testing.T) {
	config := func() StreamConfig {
		return StreamConfig{Name: "TEST", Subjects: []string{"foo"}, MaxMsgsPer: 0}
	}
	for tseq := uint64(0); tseq <= 1; tseq++ {
		t.Run(fmt.Sprintf("Truncate(%d)", tseq), func(t *testing.T) {
			testAllStoreAllPermutations(
				t, false, config(),
				func(t *testing.T, fs StreamStore) {
					dmapEntries := func() int {
						if fss, ok := fs.(*fileStore); ok {
							return fss.dmapEntries()
						} else if mss, ok := fs.(*memStore); ok {
							mss.mu.RLock()
							defer mss.mu.RUnlock()
							return mss.dmap.Size()
						} else {
							return 0
						}
					}

					// Publish messages, should have no interior deletes.
					for i := 0; i < 3; i++ {
						_, _, err := fs.StoreMsg("foo", nil, nil, 0)
						require_NoError(t, err)
					}
					require_Len(t, dmapEntries(), 0)

					// Removing one message in the middle should be an interior delete.
					_, err := fs.RemoveMsg(2)
					require_NoError(t, err)
					require_Len(t, dmapEntries(), 1)

					// Truncating must always clean up the interior delete.
					err = fs.Truncate(tseq)
					require_NoError(t, err)
					require_Len(t, dmapEntries(), 0)

					// Validate first/last sequence.
					state := fs.State()
					fseq := uint64(1)
					if fseq > tseq {
						fseq = tseq
					}
					require_Equal(t, state.FirstSeq, fseq)
					require_Equal(t, state.LastSeq, tseq)
				})
		})
	}
}

// https://github.com/nats-io/nats-server/issues/6709
func TestStorePurgeExZero(t *testing.T) {
	testAllStoreAllPermutations(
		t, false,
		StreamConfig{Name: "TEST", Subjects: []string{"foo"}},
		func(t *testing.T, fs StreamStore) {
			// Simple purge all.
			_, err := fs.Purge()
			require_NoError(t, err)
			ss := fs.State()
			require_Equal(t, ss.FirstSeq, 1)
			require_Equal(t, ss.LastSeq, 0)

			// PurgeEx(seq=0) must be equal.
			_, err = fs.PurgeEx(_EMPTY_, 0, 0)
			require_NoError(t, err)
			ss = fs.State()
			require_Equal(t, ss.FirstSeq, 1)
			require_Equal(t, ss.LastSeq, 0)
		},
	)
}

func TestStoreUpdateConfigTTLState(t *testing.T) {
	config := func() StreamConfig {
		return StreamConfig{Name: "TEST", Subjects: []string{"foo"}}
	}
	testAllStoreAllPermutations(
		t, false, config(),
		func(t *testing.T, fs StreamStore) {
			cfg := config()
			switch fs.(type) {
			case *fileStore:
				cfg.Storage = FileStorage
			case *memStore:
				cfg.Storage = MemoryStorage
			}

			// TTLs disabled at this point so this message should survive.
			seq, _, err := fs.StoreMsg("foo", nil, nil, 1)
			require_NoError(t, err)
			time.Sleep(2 * time.Second)
			_, err = fs.LoadMsg(seq, nil)
			require_NoError(t, err)

			// Now enable TTLs.
			cfg.AllowMsgTTL = true
			require_NoError(t, fs.UpdateConfig(&cfg))

			// TTLs enabled at this point so this message should be cleaned up.
			seq, _, err = fs.StoreMsg("foo", nil, nil, 1)
			require_NoError(t, err)
			time.Sleep(2 * time.Second)
			_, err = fs.LoadMsg(seq, nil)
			require_Error(t, err)

			// Now disable TTLs again.
			cfg.AllowMsgTTL = false
			require_NoError(t, fs.UpdateConfig(&cfg))

			// TTLs disabled again so this message should survive.
			seq, _, err = fs.StoreMsg("foo", nil, nil, 1)
			require_NoError(t, err)
			time.Sleep(2 * time.Second)
			_, err = fs.LoadMsg(seq, nil)
			require_NoError(t, err)
		},
	)
}
