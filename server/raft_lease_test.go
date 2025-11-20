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
// can serve stale reads because lostQuorumInterval (10s) > minElectionTimeout (4s).
// This creates a window where:
// 1. The majority partition elects a new leader (after ~4s)
// 2. The old leader still thinks it's leader (won't step down for ~10s)
// 3. The old leader can serve stale reads during this 6+ second window
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

	// The vulnerability window
	vulnerabilityWindow := lostQuorumIntervalDefault - minElectionTimeoutDefault
	t.Logf("\nVulnerability window: %v", vulnerabilityWindow)
	t.Logf("During this window, two leaders can exist simultaneously!")
	t.Logf("The old leader can serve stale reads while the new leader serves fresh writes.")

	// For linearizable reads, we need: lostQuorumInterval < minElectionTimeout
	if lostQuorumIntervalDefault >= minElectionTimeoutDefault {
		t.Errorf("UNSAFE CONFIGURATION: lostQuorumInterval (%v) >= minElectionTimeout (%v)",
			lostQuorumIntervalDefault, minElectionTimeoutDefault)
		t.Errorf("For linearizable reads, lostQuorumInterval must be < minElectionTimeout")
		t.Errorf("Recommended: lostQuorumInterval should be ~2-3 seconds")
	} else {
		t.Logf("Configuration is safe for linearizable reads")
	}
}

// TestRaftCurrentDoesNotCheckQuorum demonstrates that the Current() method
// returns true for a leader even if it has lost quorum.
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

	// The leader has now lost quorum, but Current() will still return true
	// until lostQuorumInterval (10s) passes

	// Check immediately (should still think it's current due to timing)
	time.Sleep(500 * time.Millisecond)

	t.Logf("After partition - Leader still reports: Current: %v", node.Current())

	// The issue: Current() returns true if State() == Leader (see raft.go:1550-1552)
	// It does NOT check if the leader has quorum!

	// Wait for 5 seconds (more than minElectionTimeout but less than lostQuorumInterval)
	t.Log("Waiting 5 seconds...")
	time.Sleep(5 * time.Second)

	// At this point:
	// - If there was a majority partition, they would have elected a new leader (after 4s)
	// - But this old leader STILL thinks it's current (won't detect loss until 10s)

	if node.Current() {
		t.Logf("After 5s: Leader STILL reports Current: true (UNSAFE!)")
		t.Logf("This leader has lost quorum but will still serve reads!")
		t.Logf("A new leader could have been elected in the majority partition by now.")
	} else {
		t.Logf("After 5s: Leader correctly reports Current: false")
	}

	// Wait for lostQuorumInterval
	t.Logf("Waiting for lostQuorumInterval (%v)...", lostQuorumIntervalDefault)
	time.Sleep(6 * time.Second) // Total ~11s

	// Now it should detect quorum loss
	if node.Current() {
		t.Errorf("After %v: Leader STILL reports Current: true - this should not happen!",
			lostQuorumIntervalDefault)
	} else {
		t.Logf("After %v: Leader finally detected quorum loss", lostQuorumIntervalDefault)
	}
}

// TestRaftCurrentDoesNotCheckQuorumR5 is similar to TestRaftCurrentDoesNotCheckQuorum
// but uses a 5-node cluster to more clearly demonstrate the vulnerability.
// With R5, quorum = 3 nodes. We partition the leader with 1 follower (minority = 2),
// leaving a majority of 3 nodes that can elect a new leader.
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
	t.Logf("  Leader Current: %v (should be false but is likely true due to bug)", node.Current())
	t.Logf("  Leader still has only 2/5 nodes - NO QUORUM!")

	// Wait for 5 seconds (more than minElectionTimeout but less than lostQuorumInterval)
	t.Logf("\nWaiting 5 seconds (more than minElectionTimeout of 4s)...")
	time.Sleep(5 * time.Second)

	// At this point:
	// - The majority partition (3 followers) could have elected a new leader (after 4s)
	// - But this old leader STILL thinks it's current (won't detect loss until 10s)
	// - This creates the SPLIT-BRAIN scenario

	current := node.Current()
	quorum := node.Quorum()
	isLeader := node.Leader()

	t.Logf("\nAfter 5 seconds:")
	t.Logf("  Leader Current: %v", current)
	t.Logf("  Leader Quorum: %v", quorum)
	t.Logf("  Leader state: %v", isLeader)

	if current {
		t.Logf("\n⚠️  VULNERABILITY DETECTED:")
		t.Logf("  Old leader (2/5 nodes) reports Current: true")
		t.Logf("  New leader (3/5 majority) may have been elected by now")
		t.Logf("  TWO LEADERS CAN SERVE READS WITH DIFFERENT DATA!")
		t.Logf("  Old leader has stale data, new leader has fresh writes")
		t.Errorf("UNSAFE: Leader reports Current=true despite having only 2/5 nodes (no quorum)")
	} else {
		t.Logf("✓ Leader correctly reports Current: false")
	}

	// Wait for lostQuorumInterval
	t.Logf("\nWaiting additional %v for quorum loss detection...", lostQuorumIntervalDefault-5*time.Second)
	time.Sleep(6 * time.Second) // Total ~11s

	// Now it should definitely detect quorum loss
	current = node.Current()
	t.Logf("\nAfter %v total:", lostQuorumIntervalDefault)
	t.Logf("  Leader Current: %v", current)

	if current {
		t.Errorf("CRITICAL: After %v, leader STILL reports Current: true with only 2/5 nodes!",
			lostQuorumIntervalDefault)
		t.Errorf("This means it could serve stale reads for the entire vulnerability window!")
	} else {
		t.Logf("✓ Leader finally detected quorum loss")
	}
}
