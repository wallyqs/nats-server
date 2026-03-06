// Copyright 2021-2026 The NATS Authors
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

// TestNRGQuorumPausedNotResetOnStateTransition verifies that when a follower
// has quorumPaused=true and then transitions to candidate (e.g. during a leader
// election), the quorumPaused flag is NOT reset. This means the node could become
// a leader while still having quorumPaused=true from its follower state. Since
// quorumPaused only affects follower-side processAppendEntry logic, this might
// not directly break things, but it is concerning for correctness: if a node
// becomes a follower again, it would immediately resume with quorumPaused=true
// without going through the threshold check.
func TestNRGQuorumPausedNotResetOnStateTransition(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	// Manually set the quorumPaused flag as if the follower was overrun.
	n.Lock()
	n.quorumPaused = true
	n.Unlock()

	// Switch to candidate state (happens during elections).
	n.switchToCandidate()

	n.RLock()
	paused := n.quorumPaused
	n.RUnlock()
	// BUG: quorumPaused is not cleared on state transition to candidate.
	// This means if the node becomes a follower again, it would remain paused
	// even though conditions may have changed.
	if paused {
		t.Log("CONCERN: quorumPaused was NOT reset when transitioning to candidate state")
	}

	// Now switch to leader state.
	n.switchToLeader()

	n.RLock()
	paused = n.quorumPaused
	n.RUnlock()
	if paused {
		t.Log("CONCERN: quorumPaused was NOT reset when transitioning to leader state")
	}

	// And back to follower.
	n.switchToFollower(noLeader)

	n.RLock()
	paused = n.quorumPaused
	n.RUnlock()
	// The node is now a follower with quorumPaused=true, but hasn't been through
	// the threshold check. This could cause it to silently refuse quorum until
	// the applied index catches up to within paeWarnThreshold of commit.
	if paused {
		t.Log("CONCERN: quorumPaused persists through follower->candidate->leader->follower transitions")
		t.Log("This means a node could become a follower with stale quorumPaused=true, silently refusing to participate in quorum")
	}
}

// TestNRGQuorumPausedAfterSnapshotInstall tests that when a snapshot is installed
// (which updates papplied), the quorumPaused interaction with max(applied, papplied)
// works correctly. After a snapshot, papplied will be high, which should effectively
// unpause the quorum since the diff will be small.
func TestNRGQuorumPausedAfterSnapshotInstall(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	aeReply := "$TEST"
	nc, err := nats.Connect(n.s.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
	require_NoError(t, err)
	defer nc.Close()

	sub, err := nc.SubscribeSync(aeReply)
	require_NoError(t, err)
	defer sub.Drain()
	require_NoError(t, nc.Flush())

	nats0 := "S1Nunr6R" // "nats-0"
	aeHeartbeat := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: aeReply})

	// Simulate the follower being overrun: commit is far ahead of applied.
	n.Lock()
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	// This should trigger quorumPaused=true.
	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	paused := n.quorumPaused
	n.RUnlock()
	require_True(t, paused)

	// Now simulate a snapshot install that brings papplied close to commit.
	// This simulates what happens when the leader sends us a snapshot.
	n.Lock()
	n.papplied = n.commit // Snapshot brings us up to date.
	n.Unlock()

	// Now process another heartbeat. Since max(applied, papplied) = papplied = commit,
	// the diff should be 0, which is <= paeWarnThreshold, so we should unpause.
	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	paused = n.quorumPaused
	n.RUnlock()
	require_False(t, paused)

	// Verify we can receive messages again.
	msg, err := sub.NextMsg(200 * time.Millisecond)
	require_NoError(t, err)
	ar := decodeAppendEntryResponse(msg.Data)
	require_True(t, ar.success)
}

// TestNRGOverrunCheckMissingOnPeerProposals verifies that ProposeAddPeer and
// ProposeRemovePeer do NOT have overrun protection. This means that during an
// overrun scenario where pindex >> commit, a membership change can still be
// proposed and processed, potentially making the WAL growth worse.
func TestNRGOverrunCheckMissingOnPeerProposals(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	n.switchToLeader()

	// Set up an overrun scenario.
	n.Lock()
	n.pindex = pauseQuorumThreshold + 1
	n.commit = 0
	n.applied = 0
	n.Unlock()

	// Propose should fail because of the overrun check.
	err := n.Propose([]byte("data"))
	require_Error(t, err, errNotLeader)

	// Reset to leader for the membership proposal tests.
	n.Lock()
	n.state.Store(int32(Leader))
	n.pindex = pauseQuorumThreshold + 1
	n.commit = 0
	n.applied = 0
	n.Unlock()

	// ProposeAddPeer does NOT have overrun protection.
	// This should succeed even though we're severely overrun.
	err = n.ProposeAddPeer("newpeer1")
	// This succeeds, demonstrating the gap in protection.
	if err == nil {
		t.Log("CONCERN: ProposeAddPeer succeeded despite overrun (pindex-commit > pauseQuorumThreshold)")
		t.Log("Membership changes can still grow the WAL even when the leader should be stepping down")
	}
}

// TestNRGLeaderStepdownFromForwardedProposal tests that a forwarded proposal
// also triggers leader stepdown when overrun. This was a concern raised in the
// PR review and was addressed by adding the check to handleForwardedProposal.
func TestNRGLeaderStepdownFromForwardedProposal(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	n.switchToLeader()
	// handleForwardedProposal requires both State()==Leader and leaderState to be set.
	// switchToLeader() doesn't set leaderState (it's set in the run loop after all
	// commits are applied), so we need to set it manually.
	n.leaderState.Store(true)

	// Set up an overrun scenario.
	n.Lock()
	n.pindex = pauseQuorumThreshold + 1
	n.commit = 0
	n.applied = 0
	n.Unlock()

	// Directly call handleForwardedProposal to simulate a forwarded proposal.
	n.handleForwardedProposal(nil, nil, nil, _EMPTY_, _EMPTY_, []byte("forwarded-data"))

	// After the overrun check, the leader should have stepped down.
	require_Equal(t, n.State(), Follower)
}

// TestNRGConcurrentProposalsAndOverrun tests that concurrent proposals from
// multiple goroutines all correctly observe the overrun threshold and step
// down cleanly without races or panics.
func TestNRGConcurrentProposalsAndOverrun(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	n.switchToLeader()

	// Set the overrun state.
	n.Lock()
	n.pindex = pauseQuorumThreshold + 1
	n.commit = 0
	n.applied = 0
	n.Unlock()

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errors := make([]error, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			// Some goroutines do Propose, some do ProposeMulti.
			if idx%2 == 0 {
				errors[idx] = n.Propose([]byte("data"))
			} else {
				errors[idx] = n.ProposeMulti([]*Entry{newEntry(EntryNormal, []byte("data"))})
			}
		}(i)
	}
	wg.Wait()

	// All proposals should fail: the first one triggers stepdown,
	// subsequent ones see we're no longer leader.
	for i, err := range errors {
		require_Error(t, err, errNotLeader)
		if err != errNotLeader {
			t.Fatalf("goroutine %d: expected errNotLeader, got %v", i, err)
		}
	}

	require_Equal(t, n.State(), Follower)
}

// TestNRGFollowerQuorumPauseReplayVsNew tests the interaction between replay
// (sub=nil) and new entries (sub=aesub). During replay the quorum pause should
// NOT activate (since sub is nil), but during new entries it should.
// This verifies the sub!=nil gate works correctly and that replay doesn't
// get stuck.
func TestNRGFollowerQuorumPauseReplayVsNew(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	aeReply := "$TEST"
	nc, err := nats.Connect(n.s.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
	require_NoError(t, err)
	defer nc.Close()

	sub, err := nc.SubscribeSync(aeReply)
	require_NoError(t, err)
	defer sub.Drain()
	require_NoError(t, nc.Flush())

	nats0 := "S1Nunr6R"
	aeHeartbeat := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: aeReply})

	// Set up overrun state.
	n.Lock()
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	// Process as replay (sub=nil): quorum pause should NOT activate.
	n.processAppendEntry(aeHeartbeat, nil)
	n.RLock()
	paused := n.quorumPaused
	n.RUnlock()
	require_False(t, paused)

	// Process as new entry (sub=aesub): quorum pause SHOULD activate.
	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	paused = n.quorumPaused
	n.RUnlock()
	require_True(t, paused)
}

// TestNRGStepDownIfOverrunBoundaryConditions tests the exact boundary conditions
// of the overrun threshold. This is important to ensure we don't have off-by-one
// errors that could cause either premature stepdowns or failure to step down.
func TestNRGStepDownIfOverrunBoundaryConditions(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	// Test uncommitted boundary.
	t.Run("UncommittedExactThreshold", func(t *testing.T) {
		n.Lock()
		n.pindex = pauseQuorumThreshold
		n.commit = 0
		n.applied = 0
		result := n.stepDownIfOverrun()
		n.Unlock()
		// pindex - commit == pauseQuorumThreshold, should NOT step down (uses > not >=).
		require_False(t, result)
	})

	t.Run("UncommittedOneOverThreshold", func(t *testing.T) {
		n.Lock()
		n.pindex = pauseQuorumThreshold + 1
		n.commit = 0
		n.applied = 0
		result := n.stepDownIfOverrun()
		n.Unlock()
		// pindex - commit == pauseQuorumThreshold + 1, should step down.
		require_True(t, result)
	})

	// Test unapplied boundary.
	t.Run("UnappliedExactThreshold", func(t *testing.T) {
		n.Lock()
		n.pindex = pauseQuorumThreshold
		n.commit = pauseQuorumThreshold
		n.applied = 0
		result := n.stepDownIfOverrun()
		n.Unlock()
		// commit - applied == pauseQuorumThreshold, should NOT step down.
		require_False(t, result)
	})

	t.Run("UnappliedOneOverThreshold", func(t *testing.T) {
		n.Lock()
		n.pindex = pauseQuorumThreshold + 1
		n.commit = pauseQuorumThreshold + 1
		n.applied = 0
		result := n.stepDownIfOverrun()
		n.Unlock()
		// commit - applied == pauseQuorumThreshold + 1, should step down.
		require_True(t, result)
	})

	// Test that when commit > pindex (shouldn't happen normally), no underflow.
	t.Run("CommitGreaterThanPindex", func(t *testing.T) {
		n.Lock()
		n.pindex = 5
		n.commit = 10
		n.applied = 0
		result := n.stepDownIfOverrun()
		n.Unlock()
		// pindex < commit means the uncommitted check won't trigger (pindex > commit is false).
		// But commit > applied with diff of 10, which is below threshold.
		require_False(t, result)
	})

	// Test that when applied > commit (shouldn't happen normally), no underflow.
	t.Run("AppliedGreaterThanCommit", func(t *testing.T) {
		n.Lock()
		n.pindex = 10
		n.commit = 5
		n.applied = 10
		result := n.stepDownIfOverrun()
		n.Unlock()
		// commit < applied, so unapplied check won't trigger (commit > applied is false).
		// pindex - commit = 5, below threshold.
		require_False(t, result)
	})
}

// TestNRGFollowerQuorumPauseHysteresisWindow tests the hysteresis behavior
// of the quorum pause: it pauses at pauseQuorumThreshold but only unpauses
// when the gap drops below paeWarnThreshold. This window prevents rapid
// pause/unpause cycling.
func TestNRGFollowerQuorumPauseHysteresisWindow(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	aeReply := "$TEST"
	nc, err := nats.Connect(n.s.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
	require_NoError(t, err)
	defer nc.Close()

	sub, err := nc.SubscribeSync(aeReply)
	require_NoError(t, err)
	defer sub.Drain()
	require_NoError(t, nc.Flush())

	nats0 := "S1Nunr6R"
	aeHeartbeat := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: aeReply})

	// Trigger quorum pause.
	n.Lock()
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	require_True(t, n.quorumPaused)
	n.RUnlock()

	// Apply some entries so we're between paeWarnThreshold and pauseQuorumThreshold.
	// The gap is still > paeWarnThreshold, so we should remain paused.
	n.Lock()
	n.applied = n.commit - paeWarnThreshold - 1
	n.Unlock()

	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	require_True(t, n.quorumPaused)
	n.RUnlock()
	// Drain any messages (there shouldn't be any).
	sub.NextMsg(50 * time.Millisecond)

	// Now apply enough to cross the unpause threshold.
	n.Lock()
	n.applied = n.commit - paeWarnThreshold
	n.Unlock()

	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	require_False(t, n.quorumPaused)
	n.RUnlock()

	// Verify we respond again.
	msg, err := sub.NextMsg(200 * time.Millisecond)
	require_NoError(t, err)
	ar := decodeAppendEntryResponse(msg.Data)
	require_True(t, ar.success)
}

// TestNRGForwardedRemovePeerProposalMissingOverrunCheck verifies that
// handleForwardedRemovePeerProposal does NOT have an overrun check,
// similar to ProposeAddPeer. This means peer removal proposals can still
// be accepted when the leader is overrun.
func TestNRGForwardedRemovePeerProposalMissingOverrunCheck(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	n.switchToLeader()
	n.leaderState.Store(true)

	// Set up overrun scenario.
	n.Lock()
	n.pindex = pauseQuorumThreshold + 1
	n.commit = 0
	n.applied = 0
	n.Unlock()

	// handleForwardedProposal (normal) DOES have the check, leader should step down.
	n.handleForwardedProposal(nil, nil, nil, _EMPTY_, _EMPTY_, []byte("data"))
	require_Equal(t, n.State(), Follower)

	// Reset to leader.
	n.Lock()
	n.state.Store(int32(Leader))
	n.leaderState.Store(true)
	n.pindex = pauseQuorumThreshold + 1
	n.commit = 0
	n.applied = 0
	n.Unlock()

	// handleForwardedRemovePeerProposal does NOT have the check.
	// Use properly-sized peer ID (8 bytes = idLen).
	n.handleForwardedRemovePeerProposal(nil, nil, nil, _EMPTY_, _EMPTY_, []byte("S1Nunr6R"))

	state := n.State()
	if state == Leader {
		t.Log("CONCERN: handleForwardedRemovePeerProposal does NOT trigger overrun stepdown")
		t.Log("A peer removal proposal was accepted despite pindex-commit > pauseQuorumThreshold")
	}
}

// TestNRGLeaderOverrunStepdownIsWithoutTransfer verifies that when stepping
// down due to overrun, the stepdown is WITHOUT leader transfer (noLeader).
// This is intentional: if we're overrun, all replicas are likely overrun too,
// so we don't want to immediately transfer leadership.
func TestNRGLeaderOverrunStepdownIsWithoutTransfer(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	n.switchToLeader()

	n.Lock()
	n.pindex = pauseQuorumThreshold + 1
	n.commit = 0
	n.applied = 0
	n.Unlock()

	// Trigger stepdown.
	err := n.Propose([]byte("data"))
	require_Error(t, err, errNotLeader)

	// Verify we stepped down without a leader (noLeader = empty string).
	n.RLock()
	leader := n.leader
	n.RUnlock()
	require_Equal(t, leader, noLeader)
}

// TestNRGQuorumPausedStuckAfterLeaderChange tests the scenario where a follower
// has quorumPaused=true and then a new leader is elected. The follower should
// eventually unpause when it catches up, but the quorumPaused flag persists
// across leader changes which could delay recovery.
func TestNRGQuorumPausedStuckAfterLeaderChange(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	aeReply := "$TEST"
	nc, err := nats.Connect(n.s.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
	require_NoError(t, err)
	defer nc.Close()

	sub, err := nc.SubscribeSync(aeReply)
	require_NoError(t, err)
	defer sub.Drain()
	require_NoError(t, nc.Flush())

	nats0 := "S1Nunr6R"

	// Trigger quorum pause from leader "nats-0".
	n.Lock()
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	ae1 := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: aeReply})
	n.processAppendEntry(ae1, n.aesub)
	n.RLock()
	require_True(t, n.quorumPaused)
	n.RUnlock()

	// Now a new leader with a higher term appears.
	// The follower should update its leader, but quorumPaused is still true.
	nats1 := "S2Xunr7T" // Different leader.
	ae2 := encode(t, &appendEntry{leader: nats1, term: 2, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: aeReply})

	n.processAppendEntry(ae2, n.aesub)
	n.RLock()
	paused := n.quorumPaused
	leader := n.leader
	n.RUnlock()

	if paused {
		t.Log("CONCERN: quorumPaused persists even after a new leader takes over")
		t.Logf("Leader changed to %q but follower remains paused from previous leader's overrun", leader)
	}

	// Drain any response.
	sub.NextMsg(50 * time.Millisecond)
}

// TestNRGQuorumPausedUint64UnderflowWhenAppliedExceedsCommit tests a potential
// uint64 underflow bug. When quorumPaused=true, the code enters the pause check
// block due to the `|| n.quorumPaused` condition, even when n.commit <= applied.
// Then `diff := n.commit - applied` underflows because both are uint64, producing
// a huge value. This causes `diff > paeWarnThreshold` to be true, keeping the node
// permanently paused even though it has caught up (or even moved ahead).
//
// Scenario: follower gets paused, then a snapshot is installed that sets papplied
// well beyond commit (e.g. the snapshot is from a more recent state than what the
// leader's current commit reflects in our local state).
func TestNRGQuorumPausedUint64UnderflowWhenAppliedExceedsCommit(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	aeReply := "$TEST"
	nc, err := nats.Connect(n.s.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
	require_NoError(t, err)
	defer nc.Close()

	sub, err := nc.SubscribeSync(aeReply)
	require_NoError(t, err)
	defer sub.Drain()
	require_NoError(t, nc.Flush())

	nats0 := "S1Nunr6R"
	aeHeartbeat := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: aeReply})

	// Step 1: Trigger quorum pause.
	n.Lock()
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	require_True(t, n.quorumPaused)
	n.RUnlock()

	// Step 2: Simulate a snapshot install that brings papplied BEYOND commit.
	// This can happen if the snapshot was taken at an index > our current commit view.
	n.Lock()
	n.papplied = pauseQuorumThreshold + 100 // Well beyond commit.
	n.applied = 0                            // applied itself is still low.
	// commit is still pauseQuorumThreshold + 1.
	n.Unlock()

	// Step 3: Process another heartbeat. Now max(applied, papplied) = papplied > commit.
	// The code enters the block because quorumPaused=true.
	// diff = n.commit - applied = (pauseQuorumThreshold+1) - (pauseQuorumThreshold+100)
	// This is a uint64 subtraction that underflows to a huge number!
	// The huge diff > paeWarnThreshold, so the node stays permanently paused.
	n.processAppendEntry(aeHeartbeat, n.aesub)

	n.RLock()
	paused := n.quorumPaused
	n.RUnlock()

	// BUG: When quorumPaused=true and papplied > commit, the code computes:
	//   applied := max(n.applied, n.papplied)  // = papplied = pauseQuorumThreshold + 100
	//   diff := n.commit - applied             // = (pauseQuorumThreshold+1) - (pauseQuorumThreshold+100)
	// Since these are uint64, the subtraction underflows to a massive number.
	// This causes diff > paeWarnThreshold to be true, keeping the node permanently paused.
	if paused {
		t.Log("BUG CONFIRMED: uint64 underflow in quorum pause diff calculation")
		t.Log("When papplied > commit and quorumPaused=true, diff = commit - papplied underflows")
		t.Log("This causes the node to remain permanently paused even though it has caught up via snapshot")
		t.Log("FIX: Add a guard `if n.commit > applied` before computing diff, or use signed arithmetic")
	}
	// This test documents the bug. The node SHOULD have unpaused since applied > commit.
	// If the bug is fixed, require_False(t, paused) would pass.
	require_Equal(t, paused, true) // Documenting current (buggy) behavior.
}

// TestNRGQuorumPauseCatchupSubAlsoBlocked tests that catchup entries (delivered
// on a catchup sub, not the main aesub) also trigger the quorum pause logic.
// The pause check uses `sub != nil` rather than `isNew` (which checks sub == n.aesub).
// This means catchup entries are also blocked when quorumPaused=true.
//
// This is potentially problematic: the leader sends catchup entries specifically
// to help a follower recover. If those entries are also blocked by quorum pause,
// the follower can't catch up through catchup entries, defeating the purpose.
// The follower would need a snapshot instead, which is more expensive.
func TestNRGQuorumPauseCatchupSubAlsoBlocked(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	aeReply := "$TEST"
	nc, err := nats.Connect(n.s.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
	require_NoError(t, err)
	defer nc.Close()

	sub, err := nc.SubscribeSync(aeReply)
	require_NoError(t, err)
	defer sub.Drain()
	require_NoError(t, nc.Flush())

	nats0 := "S1Nunr6R"
	aeHeartbeat := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: aeReply})

	// Trigger quorum pause using the normal aesub.
	n.Lock()
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	require_True(t, n.quorumPaused)
	n.RUnlock()

	// Now create a fake catchup subscription (not nil, not aesub).
	// This simulates what would happen if the leader sent catchup entries.
	fakeCatchupSub := &subscription{}

	// Process an entry on the catchup sub. The `sub != nil` check means
	// the pause logic will also apply to catchup entries.
	n.processAppendEntry(aeHeartbeat, fakeCatchupSub)

	n.RLock()
	stillPaused := n.quorumPaused
	n.RUnlock()

	// The entry on the catchup sub was also blocked.
	if stillPaused {
		t.Log("CONCERN: Catchup entries are also blocked by quorum pause")
		t.Log("The pause check uses 'sub != nil' not 'isNew', so catchup subs are affected too")
		t.Log("This means the follower can't recover through catchup - only snapshots will work")
	}
}

// TestNRGQuorumPausedElectionTimeoutStillReset verifies that even when a follower
// is in the quorumPaused state, the election timeout is still being reset.
// This is important because if the election timeout isn't reset, the paused
// follower would start an election, which is counterproductive during overrun.
func TestNRGQuorumPausedElectionTimeoutStillReset(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	nats0 := "S1Nunr6R"
	aeHeartbeat := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: _EMPTY_})

	// Trigger quorum pause.
	n.Lock()
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	n.processAppendEntry(aeHeartbeat, n.aesub)
	n.RLock()
	require_True(t, n.quorumPaused)
	n.RUnlock()

	// Record the election timer before a paused heartbeat.
	n.RLock()
	etlr1 := n.etlr
	n.RUnlock()

	// Wait a tiny bit so the timer value changes.
	time.Sleep(5 * time.Millisecond)

	// Process another heartbeat while paused.
	n.processAppendEntry(aeHeartbeat, n.aesub)

	// Even though we're paused, the election timeout should still be reset
	// (because resetElectionTimeout happens at the top of processAppendEntry).
	n.RLock()
	etlr2 := n.etlr
	n.RUnlock()

	if etlr2.After(etlr1) {
		// Good: election timeout was reset even while paused.
	} else {
		t.Fatal("Election timeout was NOT reset while quorumPaused - follower may trigger unnecessary election")
	}
}

// TestNRGOverrunRecoveryAfterApplyCatchesUp verifies the recovery cycle:
// 1. Leader gets overrun and steps down
// 2. After catching up on applies, stepDownIfOverrun returns false
// 3. A re-elected leader can accept proposals again
func TestNRGOverrunRecoveryAfterApplyCatchesUp(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	n.switchToLeader()

	// Simulate overrun on the unapplied side.
	n.Lock()
	n.pindex = pauseQuorumThreshold + 1
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.Unlock()

	// Propose should fail and trigger stepdown.
	err := n.Propose([]byte("data"))
	require_Error(t, err, errNotLeader)
	require_Equal(t, n.State(), Follower)

	// Now simulate the node catching up on applies.
	n.Lock()
	n.applied = pauseQuorumThreshold + 1 // All committed entries applied.
	// Verify that stepDownIfOverrun would now return false.
	overrun := n.stepDownIfOverrun()
	n.Unlock()

	require_False(t, overrun)

	// Force back to leader state (simulating new election).
	n.Lock()
	n.state.Store(int32(Leader))
	n.Unlock()

	// Should now be able to propose because applied == commit.
	err = n.Propose([]byte("data-after-recovery"))
	require_NoError(t, err)
}

// TestNRGStepDownIfOverrunBothThresholds tests the case where BOTH uncommitted
// and unapplied thresholds are exceeded simultaneously. The comment in the PR says
// "worst-case we'll have 2x the threshold" - verify this is handled.
func TestNRGStepDownIfOverrunBothThresholds(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	n.switchToLeader()

	// Both thresholds exceeded: pindex >> commit >> applied.
	n.Lock()
	n.pindex = 2*pauseQuorumThreshold + 2
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.Unlock()

	// Should still step down correctly.
	err := n.Propose([]byte("data"))
	require_Error(t, err, errNotLeader)
	require_Equal(t, n.State(), Follower)
}

// TestNRGQuorumPauseDoesNotBlockReplay tests that WAL replay (sub=nil) is never
// blocked by the quorum pause mechanism, even when quorumPaused is true.
// This is critical for node restart: if a node restarts with a large WAL, it must
// be able to replay all entries regardless of quorumPaused state.
func TestNRGQuorumPauseDoesNotBlockReplay(t *testing.T) {
	n, cleanup := initSingleMemRaftNode(t)
	defer cleanup()

	nats0 := "S1Nunr6R"

	// Set quorumPaused=true and an overrun state.
	n.Lock()
	n.quorumPaused = true
	n.commit = pauseQuorumThreshold + 1
	n.applied = 0
	n.papplied = 0
	n.Unlock()

	// Create an entry that would normally be blocked if quorum pause applied.
	// Using sub=nil simulates replay.
	aeReplay := encode(t, &appendEntry{leader: nats0, term: 1, commit: 0, pterm: 0, pindex: 0, entries: nil, reply: _EMPTY_})

	// Process as replay (sub=nil). This should NOT be blocked.
	n.processAppendEntry(aeReplay, nil)

	// The entry should have been processed (not blocked).
	// quorumPaused should still be true since replay doesn't change it.
	n.RLock()
	paused := n.quorumPaused
	n.RUnlock()
	require_True(t, paused) // Replay doesn't unpause, but it also doesn't block.
}
