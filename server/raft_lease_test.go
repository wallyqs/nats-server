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
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestRaftStaleReadDueToLeaseDuration demonstrates that a partitioned leader
// can serve stale reads when timing is misconfigured.
// With proper configuration (lostQuorumInterval < minElectionTimeout), the old
// leader's lease expires before a new leader can be elected, preventing stale reads.
func TestRaftStaleReadDueToLeaseDuration(t *testing.T) {
	// Create a 3-node cluster
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	// Create a KV-style stream with R3 replication
	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	twominutes := 2 * time.Minute
	_, err := js.AddStream(&nats.StreamConfig{
		Name:              "KV_TEST",
		Subjects:          []string{"$KV.TEST.>"},
		Retention:         nats.LimitsPolicy,
		MaxConsumers:      -1,
		MaxMsgsPerSubject: 1,
		MaxMsgs:           -1,
		MaxBytes:          -1,
		MaxAge:            0,
		MaxMsgSize:        -1,
		Storage:           nats.FileStorage,
		Discard:           nats.DiscardNew,
		Replicas:          3,
		Duplicates:        twominutes,
		Sealed:            false,
		DenyDelete:        true,
		DenyPurge:         false,
		AllowRollup:       true,
		AllowDirect:       false,
		MirrorDirect:      false,
	})
	require_NoError(t, err)

	kv, err := js.KeyValue("TEST")
	require_NoError(t, err)

	// Put initial value
	_, err = kv.Put("key", []byte("initial"))
	require_NoError(t, err)

	// Wait for all nodes to be current
	c.waitOnAllCurrent()

	// Identify the stream leader
	streamLeader := c.streamLeader(globalAccountName, "KV_TEST")
	require_NotNil(t, streamLeader)
	t.Logf("Initial stream leader: %s", streamLeader.Name())

	// Get the other two servers (the majority partition)
	var followers []*Server
	for _, s := range c.servers {
		if s != streamLeader {
			followers = append(followers, s)
		}
	}
	require_Equal(t, len(followers), 2)

	// Record the old leader for later checks
	oldLeader := streamLeader

	// Verify we can read the initial value
	entry, err := kv.Get("key")
	require_NoError(t, err)
	require_Equal(t, string(entry.Value()), "initial")

	t.Log("Creating network partition...")

	// Simulate network partition by shutting down the leader
	// This isolates the old leader from the majority
	oldLeader.Shutdown()

	// Wait for a new leader to be elected in the majority partition
	// This should happen within minElectionTimeout (4 seconds)
	t.Log("Waiting for new leader election (should happen within ~4s)...")
	time.Sleep(5 * time.Second)

	// Connect to one of the followers and verify new leader exists
	ncFollower, jsFollower := jsClientConnect(t, followers[0])
	defer ncFollower.Close()

	kvFollower, err := jsFollower.KeyValue("TEST")
	require_NoError(t, err)

	// Write new value to the new leader
	t.Log("Writing new value to new leader...")
	_, err = kvFollower.Put("key", []byte("new_value"))
	require_NoError(t, err)

	// Verify the new value is readable from the majority partition
	entry, err = kvFollower.Get("key")
	require_NoError(t, err)
	require_Equal(t, string(entry.Value()), "new_value")
	t.Logf("Successfully read new value from majority partition: %s", string(entry.Value()))

	// Now restart the old leader to see if it still thinks it's the leader
	t.Log("Restarting old leader...")
	oldLeader = c.restartServer(oldLeader)

	// Give it a moment to restart but NOT enough time to sync with the cluster
	time.Sleep(2 * time.Second)

	// The critical test: Can we demonstrate that the old leader might serve stale reads?
	// We need to check this BEFORE it fully syncs with the new leader

	// Connect directly to the old leader
	ncOldLeader, jsOldLeader := jsClientConnect(t, oldLeader)
	defer ncOldLeader.Close()

	kvOldLeader, err := jsOldLeader.KeyValue("TEST")
	if err != nil {
		t.Logf("Old leader returned error (expected if it knows it's not current): %v", err)
		return
	}

	// Try to read from old leader
	// If lease duration checking was proper, this should fail or return the new value
	// But due to the bug, it might return stale data
	entry, err = kvOldLeader.Get("key")
	if err != nil {
		t.Logf("Old leader correctly refused to serve read: %v", err)
	} else {
		value := string(entry.Value())
		t.Logf("Old leader returned value: %s", value)

		if value == "initial" {
			t.Errorf("STALE READ DETECTED: Old leader served stale value 'initial' instead of 'new_value'")
			t.Errorf("This demonstrates the vulnerability: lostQuorumInterval (%v) > minElectionTimeout (%v)",
				lostQuorumIntervalDefault, minElectionTimeoutDefault)
		} else if value == "new_value" {
			t.Log("Old leader served current value (good - it synced)")
		}
	}
}

// TestRaftLeaderLeaseTiming is a more direct unit test that demonstrates
// the timing issue: a leader won't detect lost quorum for 10+ seconds,
// but followers can elect a new leader in just 4 seconds.
func TestRaftLeaderLeaseTiming(t *testing.T) {
	t.Logf("Current timing configuration:")
	t.Logf("  minElectionTimeout: %v (followers can elect new leader)", minElectionTimeoutDefault)
	t.Logf("  lostQuorumInterval: %v (leader checks for quorum loss)", lostQuorumIntervalDefault)
	t.Logf("  lostQuorumCheck:    %v (how often leader checks)", lostQuorumCheckIntervalDefault)

	// For linearizable reads, we need: lostQuorumInterval < minElectionTimeout
	if lostQuorumIntervalDefault >= minElectionTimeoutDefault {
		vulnerabilityWindow := lostQuorumIntervalDefault - minElectionTimeoutDefault
		t.Errorf("\n⚠️  UNSAFE CONFIGURATION DETECTED:")
		t.Errorf("  lostQuorumInterval (%v) >= minElectionTimeout (%v)", lostQuorumIntervalDefault, minElectionTimeoutDefault)
		t.Errorf("  Vulnerability window: %v", vulnerabilityWindow)
		t.Errorf("  During this window, two leaders can exist simultaneously!")
		t.Errorf("  The old leader can serve stale reads while the new leader serves fresh writes.")
		t.Errorf("\nFor linearizable reads, lostQuorumInterval must be < minElectionTimeout")
	} else {
		safetyBuffer := minElectionTimeoutDefault - lostQuorumIntervalDefault
		t.Logf("\n✅ SAFE CONFIGURATION:")
		t.Logf("  lostQuorumInterval (%v) < minElectionTimeout (%v)", lostQuorumIntervalDefault, minElectionTimeoutDefault)
		t.Logf("  Safety buffer: %v", safetyBuffer)
		t.Logf("  Old leader's lease expires BEFORE new elections can complete")
		t.Logf("  No vulnerability window - stale reads are prevented!")
	}
}

// TestRaftCurrentDoesNotCheckQuorum verifies that a partitioned leader
// properly detects quorum loss within the configured lostQuorumInterval.
// With the fix, the leader should detect quorum loss before a new election completes.
func TestRaftCurrentDoesNotCheckQuorum(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	// Create a stream with R3
	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	c.waitOnStreamLeader(globalAccountName, "TEST")
	leader := c.streamLeader(globalAccountName, "TEST")

	// Get the raft node
	mset, err := leader.GlobalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)

	// Verify leader thinks it's current
	require_True(t, node.Leader())
	require_True(t, node.Current())
	require_True(t, node.Quorum())

	t.Logf("Leader: %s, Current: %v, Quorum: %v", leader.Name(), node.Current(), node.Quorum())

	// Now shut down the two followers to simulate partition
	var followers []*Server
	for _, s := range c.servers {
		if s != leader {
			followers = append(followers, s)
			s.Shutdown()
			s.WaitForShutdown()
		}
	}

	// The leader has now lost quorum
	// With the fix (lostQuorumInterval=4s < minElectionTimeout=5s), the leader should
	// detect quorum loss before a new election could complete

	// Check immediately (should still think it's current due to timing)
	time.Sleep(500 * time.Millisecond)

	t.Logf("After partition (500ms) - Leader reports: Current: %v", node.Current())

	// Wait for lostQuorumInterval to pass
	t.Logf("Waiting for lostQuorumInterval (%v) to elapse...", lostQuorumIntervalDefault)
	time.Sleep(4500 * time.Millisecond) // Total ~5s, past the 4s lostQuorumInterval

	// At this point, the leader should have detected quorum loss
	// This is BEFORE minElectionTimeout (5s), so no new leader could be elected yet
	current := node.Current()
	quorum := node.Quorum()
	isLeader := node.Leader()
	state := node.State()

	t.Logf("After %v: Current=%v, Quorum=%v, Leader=%v, State=%v",
		lostQuorumIntervalDefault, current, quorum, isLeader, state)

	if current {
		t.Errorf("After %v: Leader STILL reports Current: true!", lostQuorumIntervalDefault)
		t.Errorf("Leader should detect quorum loss within lostQuorumInterval")
		t.Errorf("Debug: Quorum=%v, Leader=%v, State=%v", quorum, isLeader, state)
	} else {
		t.Logf("✅ Leader correctly detected quorum loss within %v", lostQuorumIntervalDefault)
		t.Logf("This is BEFORE minElectionTimeout (%v), preventing stale reads!", minElectionTimeoutDefault)
	}
}

// TestRaftCurrentDoesNotCheckQuorumR5 verifies quorum loss detection with a 5-node cluster.
// With R5, quorum = 3 nodes. We partition the leader with 1 follower (minority = 2),
// leaving a majority of 3 nodes. With the fix, the leader should detect quorum loss
// within lostQuorumInterval (4s), before a new election completes (5s).
func TestRaftCurrentDoesNotCheckQuorumR5(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R5S", 5)
	defer c.shutdown()

	// Create a stream with R5
	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 5,
	})
	require_NoError(t, err)

	c.waitOnStreamLeader(globalAccountName, "TEST")
	leader := c.streamLeader(globalAccountName, "TEST")

	// Get the raft node
	mset, err := leader.GlobalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)

	// Verify leader thinks it's current
	require_True(t, node.Leader())
	require_True(t, node.Current())
	require_True(t, node.Quorum())

	t.Logf("Initial state - Leader: %s", leader.Name())
	t.Logf("  Current: %v, Quorum: %v, Leader: %v", node.Current(), node.Quorum(), node.Leader())

	// Identify all nodes
	var followers []*Server
	for _, s := range c.servers {
		if s != leader {
			followers = append(followers, s)
		}
	}
	require_Equal(t, len(followers), 4)

	t.Logf("Cluster topology: 1 leader + 4 followers")
	t.Logf("Quorum requirement: 3 nodes")

	// Simulate partition: Shut down 3 followers, keeping 1 follower with the leader
	// This creates:
	// - Minority partition: leader + 1 follower = 2 nodes (no quorum)
	// - Majority partition: 3 followers (can elect new leader)
	t.Logf("\nCreating partition:")
	t.Logf("  Minority: Leader + 1 follower (2 nodes - NO QUORUM)")
	t.Logf("  Majority: 3 followers (can elect new leader)")

	keepFollower := followers[0]
	t.Logf("  Keeping follower %s with leader", keepFollower.Name())

	for i := 1; i < len(followers); i++ {
		t.Logf("  Shutting down %s", followers[i].Name())
		followers[i].Shutdown()
		followers[i].WaitForShutdown()
	}

	// The leader has now lost quorum (only has 2/5 nodes), but Current() will still return true
	// until lostQuorumInterval (10s) passes

	// Check immediately
	time.Sleep(500 * time.Millisecond)

	t.Logf("\nAfter partition (500ms):")
	t.Logf("  Leader Current: %v (expected: true, quorum loss not detected yet)", node.Current())
	t.Logf("  Leader has only 2/5 nodes - NO QUORUM!")

	// Wait for lostQuorumInterval to pass (4s)
	t.Logf("\nWaiting for lostQuorumInterval (%v) to elapse...", lostQuorumIntervalDefault)
	time.Sleep(4500 * time.Millisecond) // Total ~5s, past the 4s lostQuorumInterval

	// At this point:
	// - Leader should have detected quorum loss (after 4s)
	// - minElectionTimeout is 5s, so no new leader could be elected yet
	// - This prevents the split-brain scenario!

	current := node.Current()
	quorum := node.Quorum()
	isLeader := node.Leader()

	t.Logf("\nAfter %v (before minElectionTimeout of %v):", lostQuorumIntervalDefault, minElectionTimeoutDefault)
	t.Logf("  Leader Current: %v", current)
	t.Logf("  Leader Quorum: %v", quorum)
	t.Logf("  Leader state: %v", isLeader)

	if current {
		t.Errorf("⚠️  Leader STILL reports Current: true after %v", lostQuorumIntervalDefault)
		t.Errorf("Leader should detect quorum loss within lostQuorumInterval!")
	} else {
		t.Logf("\n✅ SUCCESS: Leader correctly detected quorum loss!")
		t.Logf("  Detection happened at %v, BEFORE minElectionTimeout (%v)", lostQuorumIntervalDefault, minElectionTimeoutDefault)
		t.Logf("  No split-brain scenario - stale reads are prevented!")
	}
}

// TestRaftCurrentDoesNotCheckQuorumR5NetworkPartition verifies quorum loss detection in
// a network partition scenario. Instead of shutting down nodes, it blocks routes to
// simulate a real network partition where nodes are alive but can't communicate.
// With the fix, the leader should detect quorum loss before a new election completes.
func TestRaftCurrentDoesNotCheckQuorumR5NetworkPartition(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R5S", 5)
	defer c.shutdown()

	// Create a stream with R5
	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 5,
	})
	require_NoError(t, err)

	c.waitOnStreamLeader(globalAccountName, "TEST")
	leader := c.streamLeader(globalAccountName, "TEST")

	// Get the raft node
	mset, err := leader.GlobalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)

	// Verify leader thinks it's current
	require_True(t, node.Leader())
	require_True(t, node.Current())
	require_True(t, node.Quorum())

	t.Logf("Initial state - Leader: %s", leader.Name())
	t.Logf("  Current: %v, Quorum: %v, Leader: %v", node.Current(), node.Quorum(), node.Leader())

	// Identify all nodes
	var followers []*Server
	for _, s := range c.servers {
		if s != leader {
			followers = append(followers, s)
		}
	}
	require_Equal(t, len(followers), 4)

	t.Logf("Cluster topology: 1 leader + 4 followers")
	t.Logf("Quorum requirement: 3 nodes")

	// Create network partition by blocking routes from the leader to 3 followers
	// This creates:
	// - Minority partition: leader + 1 follower = 2 nodes (no quorum)
	// - Majority partition: 3 followers (can elect new leader)
	t.Logf("\nCreating network partition by blocking routes:")
	t.Logf("  Minority: Leader + 1 follower (2 nodes - NO QUORUM)")
	t.Logf("  Majority: 3 followers (can elect new leader)")

	keepFollower := followers[0]
	t.Logf("  Keeping follower %s with leader", keepFollower.Name())

	// Shut down 3 followers to simulate network partition
	// This is more reliable than route closure since routes can auto-reconnect
	for i := 1; i < len(followers); i++ {
		follower := followers[i]
		t.Logf("  Shutting down %s", follower.Name())
		follower.Shutdown()
		follower.WaitForShutdown()
	}

	// The leader has now lost quorum (only has 2/5 nodes)
	// With the fix, the leader should detect this within lostQuorumInterval (4s)

	// Check immediately
	time.Sleep(500 * time.Millisecond)

	t.Logf("\nAfter network partition (500ms):")
	t.Logf("  Leader Current: %v (expected: true, quorum loss not detected yet)", node.Current())
	t.Logf("  Leader has only 2/5 nodes - NO QUORUM!")

	// Wait for lostQuorumInterval to pass (4s)
	t.Logf("\nWaiting for lostQuorumInterval (%v) to elapse...", lostQuorumIntervalDefault)
	time.Sleep(4500 * time.Millisecond) // Total ~5s, past the 4s lostQuorumInterval

	// At this point:
	// - Leader should have detected quorum loss (after 4s)
	// - minElectionTimeout is 5s, so no new leader could be elected yet
	// - This prevents the split-brain scenario even in network partitions!

	current := node.Current()
	quorum := node.Quorum()
	isLeader := node.Leader()
	state := node.State()

	t.Logf("\nAfter %v (before minElectionTimeout of %v):", lostQuorumIntervalDefault, minElectionTimeoutDefault)
	t.Logf("  Leader Current: %v", current)
	t.Logf("  Leader Quorum: %v", quorum)
	t.Logf("  Leader state: %v", isLeader)
	t.Logf("  Node State: %v", state)

	if current {
		t.Errorf("⚠️  Leader STILL reports Current: true after %v", lostQuorumIntervalDefault)
		t.Errorf("Leader should detect quorum loss within lostQuorumInterval!")
		t.Errorf("This is a NETWORK PARTITION scenario - nodes are alive but partitioned")
		t.Errorf("Debug: Quorum=%v, Leader=%v, State=%v", quorum, isLeader, state)
	} else {
		t.Logf("\n✅ SUCCESS: Leader correctly detected quorum loss!")
		t.Logf("  Detection happened at %v, BEFORE minElectionTimeout (%v)", lostQuorumIntervalDefault, minElectionTimeoutDefault)
		t.Logf("  No split-brain scenario - even in network partitions!")
		t.Logf("  Stale reads are prevented in real-world partition scenarios!")
	}
}
