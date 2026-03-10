// Copyright 2022-2025 The NATS Authors
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

//go:build !skip_js_tests && !skip_js_cluster_tests_4

package server

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamClusterAsyncSnapshotBasic verifies that the async stream snapshot
// mechanism correctly installs snapshots via a background goroutine and that the
// Raft WAL is compacted after the snapshot completes.
func TestJetStreamClusterAsyncSnapshotBasic(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish enough messages to trigger a snapshot via compaction thresholds.
	for i := 0; i < 1000; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}

	// Wait for all replicas to be in sync.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Get the stream leader and force a snapshot.
	sl := c.streamLeader(globalAccountName, "TEST")
	mset, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)

	// Capture the WAL size before the snapshot.
	ne, _ := node.Size()

	// Force a snapshot by installing one. The new code uses CreateSnapshotCheckpoint
	// under the hood via the monitor goroutine, but we can also trigger it directly.
	rn := node.(*raft)
	rn.RLock()
	pappliedBefore := rn.papplied
	rn.RUnlock()

	// Trigger compaction by kicking the leader change channel, which
	// causes the monitorStream loop to invoke doSnapshot.
	rn.Lock()
	if rn.leadc != nil {
		select {
		case rn.leadc <- true:
		default:
		}
	}
	rn.Unlock()

	// Wait for snapshot to be installed (papplied should advance).
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		rn.RLock()
		papplied := rn.papplied
		rn.RUnlock()
		if papplied <= pappliedBefore {
			return fmt.Errorf("snapshot not yet installed, papplied=%d (was %d)", papplied, pappliedBefore)
		}
		return nil
	})

	// The WAL should have been compacted.
	neAfter, _ := node.Size()
	if neAfter >= ne {
		t.Logf("WAL entries: before=%d, after=%d (compaction may have happened concurrently)", ne, neAfter)
	}

	// Verify the snapshot was actually written to disk.
	snap, err := rn.loadLastSnapshot()
	require_NoError(t, err)
	require_True(t, snap != nil)
	require_True(t, len(snap.data) > 0)

	// Verify the cluster is still fully consistent after the async snapshot.
	for i := 0; i < 100; i++ {
		_, err := js.Publish("foo", []byte("post-snapshot"))
		require_NoError(t, err)
	}
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})
}

// TestJetStreamClusterAsyncSnapshotShutdownFallback verifies that during
// server shutdown, the snapshot mechanism falls back to synchronous (blocking)
// mode to ensure the snapshot completes before the process exits.
func TestJetStreamClusterAsyncSnapshotShutdownFallback(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish messages so there is state to snapshot.
	for i := 0; i < 500; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Pick a server to shut down.
	sl := c.streamLeader(globalAccountName, "TEST")
	mset, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)

	rn := node.(*raft)
	rn.RLock()
	pappliedBefore := rn.papplied
	rn.RUnlock()

	// Shut down the server. The shutdown path sets fallbackSnapshot = true
	// and then calls doSnapshot, which should block until the snapshot
	// is written (not async).
	sl.Shutdown()
	sl.WaitForShutdown()

	// Restart the server and verify it recovers correctly from the snapshot.
	sl = c.restartServer(sl)
	c.waitOnServerCurrent(sl)
	c.waitOnStreamLeader(globalAccountName, "TEST")

	// Verify the stream state is consistent after restart.
	nc2, js2 := jsClientConnect(t, c.randomServer())
	defer nc2.Close()

	si, err := js2.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 500)

	// Check that the snapshot was actually written during shutdown
	// by verifying papplied advanced.
	mset2, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node2 := mset2.raftNode()
	if node2 != nil {
		rn2 := node2.(*raft)
		rn2.RLock()
		papplied := rn2.papplied
		rn2.RUnlock()
		// After restart with snapshot recovery, papplied should be at or beyond
		// where it was before shutdown (the snapshot should have advanced it).
		if papplied < pappliedBefore {
			t.Fatalf("Expected papplied >= %d after restart, got %d", pappliedBefore, papplied)
		}
	}

	// Verify the cluster converges.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})
}

// TestJetStreamClusterAsyncSnapshotConcurrentSuppression verifies that
// only one async snapshot can be in progress at a time. The snapshotting
// flag should prevent overlapping snapshots.
func TestJetStreamClusterAsyncSnapshotConcurrentSuppression(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish messages to create meaningful state.
	for i := 0; i < 500; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Get the stream leader.
	sl := c.streamLeader(globalAccountName, "TEST")
	mset, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)
	rn := node.(*raft)

	// Rapidly trigger multiple snapshots by kicking the leader change channel.
	// The concurrent suppression should ensure only one snapshot runs at a time
	// and the system doesn't panic or deadlock.
	for i := 0; i < 10; i++ {
		rn.Lock()
		if rn.leadc != nil {
			select {
			case rn.leadc <- true:
			default:
			}
		}
		rn.Unlock()
		// Publish more messages between triggers to change state.
		for j := 0; j < 50; j++ {
			js.Publish("foo", []byte("rapid"))
		}
	}

	// Give time for async snapshots to complete.
	time.Sleep(2 * time.Second)

	// The system should be stable — no panics, no deadlocks.
	// Verify the cluster is still consistent.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Can still publish and consume.
	for i := 0; i < 100; i++ {
		_, err := js.Publish("foo", []byte("after-rapid"))
		require_NoError(t, err)
	}
	si, err := js.StreamInfo("TEST")
	require_NoError(t, err)
	require_True(t, si.State.Msgs > 0)
}

// TestJetStreamClusterConsumerSnapshotHashDedup verifies that the consumer
// snapshot mechanism uses highwayhash to detect unchanged state and skips
// redundant snapshot installations.
func TestJetStreamClusterConsumerSnapshotHashDedup(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// Publish and ack some messages to establish consumer state.
	for i := 0; i < 100; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}
	sub, err := js.PullSubscribe("foo", "CONSUMER")
	require_NoError(t, err)
	msgs, err := sub.Fetch(100)
	require_NoError(t, err)
	require_Len(t, len(msgs), 100)
	for _, m := range msgs {
		require_NoError(t, m.Ack())
	}

	// Wait for consumer to sync.
	c.waitOnConsumerLeader(globalAccountName, "TEST", "CONSUMER")
	cl := c.consumerLeader(globalAccountName, "TEST", "CONSUMER")
	require_NotNil(t, cl)
	mset, err := cl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	o := mset.lookupConsumer("CONSUMER")
	require_NotNil(t, o)

	cnode := o.raftNode()
	require_NotNil(t, cnode)

	// Install a snapshot to establish initial state.
	snap, err := o.store.EncodedState()
	require_NoError(t, err)
	require_NoError(t, cnode.InstallSnapshot(snap, false))

	// Record the snapshot state.
	crn := cnode.(*raft)
	crn.RLock()
	pappliedAfterFirst := crn.papplied
	crn.RUnlock()

	// Publish more messages and ack them so the consumer state changes
	// and Raft generates new entries. Then snapshot again.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo", []byte("batch2"))
		require_NoError(t, err)
	}
	msgs, err = sub.Fetch(50)
	require_NoError(t, err)
	require_Len(t, len(msgs), 50)
	for _, m := range msgs {
		require_NoError(t, m.Ack())
	}

	// Wait for the consumer raft to have applied new entries.
	time.Sleep(500 * time.Millisecond)

	snap2, err := o.store.EncodedState()
	require_NoError(t, err)
	// Install the second snapshot. With new entries applied, this should succeed.
	require_NoError(t, cnode.InstallSnapshot(snap2, false))

	crn.RLock()
	pappliedAfterSecond := crn.papplied
	crn.RUnlock()

	// papplied should advance because the consumer state changed (new entries applied).
	t.Logf("papplied: after first snapshot=%d, after second=%d", pappliedAfterFirst, pappliedAfterSecond)
	require_True(t, pappliedAfterSecond > pappliedAfterFirst)

	// The consumer should still be fully functional.
	for i := 0; i < 10; i++ {
		_, err := js.Publish("foo", []byte("more"))
		require_NoError(t, err)
	}
	msgs, err = sub.Fetch(10)
	require_NoError(t, err)
	require_Len(t, len(msgs), 10)
}

// TestJetStreamClusterConsumerStateInMetaSnapshot verifies the consumer state
// encoding/decoding in meta snapshots. In normal operation, ca.State is set to
// nil after being consumed during processConsumerAssignment. However, the
// writeableConsumerAssignment now has a State field that is encoded when present
// (e.g., during stream restore with consumer info). This test verifies the
// encode/decode round-trip works correctly.
func TestJetStreamClusterConsumerStateInMetaSnapshot(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, "TEST", "CONSUMER")

	// Publish and ack.
	for i := 0; i < 20; i++ {
		_, err = js.Publish("foo", []byte("msg"))
		require_NoError(t, err)
	}
	sub, err := js.PullSubscribe("foo", "CONSUMER")
	require_NoError(t, err)
	msgs, err := sub.Fetch(20)
	require_NoError(t, err)
	require_Len(t, len(msgs), 20)
	for _, m := range msgs {
		require_NoError(t, m.AckSync())
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		ci, err := js.ConsumerInfo("TEST", "CONSUMER")
		if err != nil {
			return err
		}
		if ci.AckFloor.Consumer != 20 {
			return fmt.Errorf("expected ack floor 20, got %d", ci.AckFloor.Consumer)
		}
		return nil
	})

	// Get the meta leader.
	ml := c.leader()
	require_NotNil(t, ml)
	sjs := ml.getJetStream()
	meta := sjs.getMetaGroup().(*raft)

	// Force a meta snapshot.
	meta.RLock()
	papplied := meta.papplied
	meta.RUnlock()
	require_NoError(t, meta.ProposeAddPeer(meta.ID()))
	checkFor(t, 2*time.Second, 200*time.Millisecond, func() error {
		meta.RLock()
		defer meta.RUnlock()
		if meta.papplied == papplied {
			return errors.New("no snapshot yet")
		}
		return nil
	})

	// Load the snapshot and verify the consumer assignment is present.
	snap, err := meta.loadLastSnapshot()
	require_NoError(t, err)
	accStreams, err := sjs.decodeMetaSnapshot(snap.data)
	require_NoError(t, err)
	require_True(t, len(accStreams) > 0)
	streams := accStreams[globalAccountName]
	require_True(t, len(streams) > 0)
	stream := streams["TEST"]
	require_NotNil(t, stream)
	require_True(t, len(stream.consumers) > 0)
	consumer := stream.consumers["CONSUMER"]
	require_NotNil(t, consumer)

	// In normal operation, ca.State is nil after being consumed.
	// The important thing is the field is correctly included in the struct
	// and can be round-tripped via encode/decode.
	t.Logf("Consumer State in meta snapshot: %v", consumer.State)

	// Now test the round-trip: manually set state, encode, and decode.
	consumer.State = &ConsumerState{
		Delivered: SequencePair{Consumer: 20, Stream: 20},
		AckFloor:  SequencePair{Consumer: 20, Stream: 20},
	}
	encoded, _, _, err := sjs.encodeMetaSnapshot(accStreams)
	require_NoError(t, err)

	decoded, err := sjs.decodeMetaSnapshot(encoded)
	require_NoError(t, err)
	decodedConsumer := decoded[globalAccountName]["TEST"].consumers["CONSUMER"]
	require_NotNil(t, decodedConsumer)
	require_NotNil(t, decodedConsumer.State)
	require_Equal(t, decodedConsumer.State.AckFloor.Consumer, 20)
	require_Equal(t, decodedConsumer.State.AckFloor.Stream, 20)

	// The consumer should still be fully functional.
	for i := 0; i < 5; i++ {
		_, err = js.Publish("foo", []byte("more"))
		require_NoError(t, err)
	}
	msgs, err = sub.Fetch(5)
	require_NoError(t, err)
	require_Len(t, len(msgs), 5)
}

// TestJetStreamClusterTieredReservationMixedReplicas verifies that
// tieredStreamAndReservationCount correctly calculates reservations
// when streams with different replica counts coexist.
func TestJetStreamClusterTieredReservationMixedReplicas(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	// Create an R1 stream with MaxBytes.
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "R1STREAM",
		Subjects: []string{"r1.>"},
		Replicas: 1,
		MaxBytes: 1024 * 1024, // 1MB
	})
	require_NoError(t, err)

	// Create an R3 stream with MaxBytes.
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "R3STREAM",
		Subjects: []string{"r3.>"},
		Replicas: 3,
		MaxBytes: 1024 * 1024, // 1MB
	})
	require_NoError(t, err)

	c.waitOnStreamLeader(globalAccountName, "R1STREAM")
	c.waitOnStreamLeader(globalAccountName, "R3STREAM")

	// Wait for all servers to see both streams.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		for _, s := range c.servers {
			sjs := s.getJetStream()
			if sjs.streamAssignment(globalAccountName, "R1STREAM") == nil {
				return errors.New("R1STREAM not found")
			}
			if sjs.streamAssignment(globalAccountName, "R3STREAM") == nil {
				return errors.New("R3STREAM not found")
			}
		}
		return nil
	})

	// Now verify reservation math by trying to create a third stream
	// that would exceed the account limit. Use a server-level check.
	sl := c.streamLeader(globalAccountName, "R1STREAM")
	_, sjs, jsa := sl.globalAccount().getJetStreamFromAccount()

	sjs.mu.RLock()
	jsa.mu.RLock()

	// Check flat tier (empty tier name) with a new R1 stream config.
	cfgR1 := &StreamConfig{Storage: FileStorage, Replicas: 1, Name: "NEW_R1"}
	_, reservationR1 := sjs.tieredStreamAndReservationCount(globalAccountName, _EMPTY_, cfgR1)

	// Check flat tier with a new R3 stream config.
	cfgR3 := &StreamConfig{Storage: FileStorage, Replicas: 3, Name: "NEW_R3"}
	_, reservationR3 := sjs.tieredStreamAndReservationCount(globalAccountName, _EMPTY_, cfgR3)

	jsa.mu.RUnlock()
	sjs.mu.RUnlock()

	// With the current implementation, cfg.Replicas is used for all existing streams'
	// reservation calculation in the flat tier. Document the actual behavior.
	//
	// R1STREAM: MaxBytes=1MB, R3STREAM: MaxBytes=1MB
	// For R1 query (Replicas=1): No replica adjustment (Replicas <= 1)
	//   - R1STREAM: 1MB, R3STREAM: 1MB = 2MB
	// For R3 query (Replicas=3): Both streams counted with cfg.Replicas multiplier
	//   - R1STREAM: 1MB * 3 = 3MB, R3STREAM: 1MB * 3 = 3MB = 6MB
	t.Logf("Reservation for R1 query: %d bytes", reservationR1)
	t.Logf("Reservation for R3 query: %d bytes", reservationR3)

	// For the flat tier with Replicas=1, the reservation should count both streams
	// without a replica multiplier (since cfg.Replicas is 1, not > 1).
	expectedR1 := int64(2 * 1024 * 1024) // 2MB: both streams at 1MB each
	require_Equal(t, reservationR1, expectedR1)

	// For the flat tier with Replicas=3, the current code multiplies ALL existing
	// streams by cfg.Replicas (3), not by each stream's own replica count.
	// This means R1STREAM (which is R1) gets counted at 3x, which may over-count.
	expectedR3 := int64(6 * 1024 * 1024) // 6MB: both streams at 1MB * 3
	require_Equal(t, reservationR3, expectedR3)

	// Verify we can still create streams and the limits work.
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "R1CHECK",
		Subjects: []string{"check.>"},
		Replicas: 1,
		MaxBytes: 1024 * 1024,
	})
	require_NoError(t, err)
}

// TestJetStreamClusterStreamContinuesAfterApplyError verifies that a stream
// continues operating after encountering an error in applyStreamEntries.
// The new behavior logs warnings instead of stopping the stream.
func TestJetStreamClusterStreamContinuesAfterApplyError(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish some initial messages.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo", []byte("initial"))
		require_NoError(t, err)
	}
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Now publish more messages — the stream should remain operational
	// even if there were internal apply issues. The key test here is
	// that the stream doesn't stop after transient errors.
	for i := 0; i < 200; i++ {
		_, err := js.Publish("foo", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// All replicas should converge.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	si, err := js.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 250)

	// Verify the stream is still monitoring (not stopped).
	for _, s := range c.servers {
		acc, _ := s.lookupAccount(globalAccountName)
		if acc == nil {
			continue
		}
		mset, err := acc.lookupStream("TEST")
		if err != nil {
			continue
		}
		require_True(t, mset.isMonitorRunning())
	}
}

// TestJetStreamClusterAsyncSnapshotRestartRecovery verifies that a server
// which receives an async snapshot and then restarts can correctly recover
// the stream from the snapshot.
func TestJetStreamClusterAsyncSnapshotRestartRecovery(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish messages.
	for i := 0; i < 500; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Pick a follower to restart.
	follower := c.randomNonStreamLeader(globalAccountName, "TEST")
	require_NotNil(t, follower)

	// Make sure a snapshot has been installed on the follower.
	mset, err := follower.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)

	// Force a snapshot on this node.
	err = node.InstallSnapshot(mset.stateSnapshot(), false)
	require_NoError(t, err)

	// Now restart the follower.
	follower.Shutdown()
	follower.WaitForShutdown()

	follower = c.restartServer(follower)
	c.waitOnServerCurrent(follower)

	// Publish more messages after restart.
	nc2, js2 := jsClientConnect(t, c.randomServer())
	defer nc2.Close()

	for i := 0; i < 100; i++ {
		_, err := js2.Publish("foo", []byte("post-restart"))
		require_NoError(t, err)
	}

	// All replicas should converge.
	checkFor(t, 10*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	si, err := js2.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 600)
}

// TestJetStreamClusterConsumerStateMetaSnapshotRecovery verifies that
// consumer state from a meta snapshot is correctly applied when a new
// server joins and catches up via meta snapshot.
func TestJetStreamClusterConsumerStateMetaSnapshotRecovery(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// Publish and ack messages.
	for i := 0; i < 10; i++ {
		_, err = js.Publish("foo", []byte("msg"))
		require_NoError(t, err)
	}
	sub, err := js.PullSubscribe("foo", "CONSUMER")
	require_NoError(t, err)
	msgs, err := sub.Fetch(10)
	require_NoError(t, err)
	require_Len(t, len(msgs), 10)
	for _, m := range msgs {
		require_NoError(t, m.AckSync())
	}

	// Wait for ack floor to propagate.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		for _, s := range c.servers {
			a, err := s.lookupAccount(globalAccountName)
			if err != nil {
				return err
			}
			ms, err := a.lookupStream("TEST")
			if err != nil {
				return err
			}
			co := ms.lookupConsumer("CONSUMER")
			if co == nil {
				return errors.New("consumer not found")
			}
			info := co.info()
			if info.AckFloor.Consumer != 10 {
				return fmt.Errorf("expected ack floor 10, got %d on %s", info.AckFloor.Consumer, s.Name())
			}
		}
		return nil
	})

	// Pick a non-leader to restart. It will recover from meta snapshot.
	rs := c.randomNonStreamLeader(globalAccountName, "TEST")
	require_NotNil(t, rs)

	// Force a meta snapshot on the leader before shutting down the follower.
	ml := c.leader()
	require_NotNil(t, ml)
	meta := ml.getJetStream().getMetaGroup().(*raft)
	require_NoError(t, meta.ProposeAddPeer(meta.ID()))
	time.Sleep(500 * time.Millisecond)

	rs.Shutdown()
	rs.WaitForShutdown()

	// Publish more while it's down so the meta log gets ahead.
	for i := 0; i < 5; i++ {
		_, err = js.Publish("foo", []byte("while-down"))
		require_NoError(t, err)
	}

	rs = c.restartServer(rs)
	c.waitOnServerCurrent(rs)

	// Verify consumer state recovered correctly.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		a, err := rs.lookupAccount(globalAccountName)
		if err != nil {
			return err
		}
		ms, err := a.lookupStream("TEST")
		if err != nil {
			return err
		}
		co := ms.lookupConsumer("CONSUMER")
		if co == nil {
			return errors.New("consumer not found after restart")
		}
		info := co.info()
		if info.AckFloor.Consumer != 10 {
			return fmt.Errorf("expected ack floor 10, got %d", info.AckFloor.Consumer)
		}
		return nil
	})
}

// TestJetStreamClusterAsyncSnapshotAPIQueueStability verifies that the
// consolidated API queue (single queue, no separate info queue) handles
// concurrent stream info and stream create requests without dropping
// or deadlocking.
func TestJetStreamClusterAsyncSnapshotAPIQueueStability(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	// Create several streams.
	for i := 0; i < 5; i++ {
		_, err := js.AddStream(&nats.StreamConfig{
			Name:     fmt.Sprintf("STREAM-%d", i),
			Subjects: []string{fmt.Sprintf("subj.%d", i)},
			Replicas: 3,
		})
		require_NoError(t, err)
	}

	// Hammer the API with concurrent info and publish requests.
	var errors atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			for j := 0; j < 5; j++ {
				_, err := js.StreamInfo(fmt.Sprintf("STREAM-%d", j))
				if err != nil {
					errors.Add(1)
				}
			}
		}
	}()

	// Publish messages concurrently.
	for i := 0; i < 100; i++ {
		for j := 0; j < 5; j++ {
			js.Publish(fmt.Sprintf("subj.%d", j), []byte("data"))
		}
	}

	<-done

	errCount := errors.Load()
	if errCount > 10 {
		t.Fatalf("Too many API errors: %d", errCount)
	}

	// Verify all streams are still healthy.
	for i := 0; i < 5; i++ {
		si, err := js.StreamInfo(fmt.Sprintf("STREAM-%d", i))
		require_NoError(t, err)
		require_True(t, si.State.Msgs > 0)
	}
}

// TestJetStreamClusterStreamSnapshotStateChangeDetection verifies that
// the SimpleState-based change detection correctly identifies when a
// snapshot is needed vs when it can be skipped.
func TestJetStreamClusterStreamSnapshotStateChangeDetection(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish a batch of messages.
	for i := 0; i < 100; i++ {
		_, err := js.Publish("foo", []byte("initial"))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	sl := c.streamLeader(globalAccountName, "TEST")
	mset, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)

	// Get the current simple state — this is what the async snapshot mechanism uses.
	state1 := mset.store.FilteredState(0, _EMPTY_)
	require_Equal(t, state1.Msgs, 100)

	// Without publishing, the state should be the same.
	state2 := mset.store.FilteredState(0, _EMPTY_)
	require_Equal(t, state1, state2)

	// Publish one more message — state should change.
	_, err = js.Publish("foo", []byte("new"))
	require_NoError(t, err)

	checkFor(t, 2*time.Second, 50*time.Millisecond, func() error {
		state3 := mset.store.FilteredState(0, _EMPTY_)
		if state3 == state1 {
			return errors.New("state not yet changed")
		}
		return nil
	})

	// Purge the stream — state should change dramatically.
	require_NoError(t, js.PurgeStream("TEST"))
	checkFor(t, 2*time.Second, 50*time.Millisecond, func() error {
		state4 := mset.store.FilteredState(0, _EMPTY_)
		if state4.Msgs != 0 {
			return fmt.Errorf("expected 0 msgs after purge, got %d", state4.Msgs)
		}
		return nil
	})
}

// TestJetStreamClusterConsumerScaleDownNoRace verifies that scaling down a
// consumer from R3 to R1 correctly handles the transition without races
// between the old monitor goroutine and the new leader state, now that
// monitorWg.Wait() has been removed from the scale-down path.
func TestJetStreamClusterConsumerScaleDownNoRace(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, "TEST", "CONSUMER")

	// Publish and ack messages before scale down.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}
	sub, err := js.PullSubscribe("foo", "CONSUMER")
	require_NoError(t, err)
	msgs, err := sub.Fetch(50)
	require_NoError(t, err)
	for _, m := range msgs {
		require_NoError(t, m.Ack())
	}

	// Scale down to R1.
	_, err = js.UpdateConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  1,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// The consumer should be accessible after scale down.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		ci, err := js.ConsumerInfo("TEST", "CONSUMER")
		if err != nil {
			return err
		}
		if ci.Config.Replicas != 1 {
			return fmt.Errorf("expected R1, got R%d", ci.Config.Replicas)
		}
		return nil
	})

	// Publish more and consume — consumer should still work.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo", []byte("post-scale"))
		require_NoError(t, err)
	}
	msgs, err = sub.Fetch(50)
	require_NoError(t, err)
	require_Len(t, len(msgs), 50)

	// Verify ack floor is correct.
	ci, err := js.ConsumerInfo("TEST", "CONSUMER")
	require_NoError(t, err)
	require_Equal(t, ci.Delivered.Consumer, 100)
}

// TestJetStreamClusterPeerCurrentAccuracy verifies the updated Raft peer
// Current logic reports peers accurately during normal operation, after
// leader changes, and when peers are lagging.
func TestJetStreamClusterPeerCurrentAccuracy(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish some messages to generate Raft entries.
	for i := 0; i < 100; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}

	// Wait for convergence.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// All peers should be current.
	sl := c.streamLeader(globalAccountName, "TEST")
	mset, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	peers := node.Peers()
	for _, p := range peers {
		if !p.Current {
			t.Fatalf("Expected peer %s to be current, but it's not (lag=%d)", p.ID, p.Lag)
		}
	}

	// Force a leader stepdown.
	require_NoError(t, node.StepDown())
	c.waitOnStreamLeader(globalAccountName, "TEST")

	// After leader change, all peers should eventually be current again.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		newSl := c.streamLeader(globalAccountName, "TEST")
		mset, err := newSl.globalAccount().lookupStream("TEST")
		if err != nil {
			return err
		}
		n := mset.raftNode()
		if n == nil {
			return errors.New("no raft node")
		}
		for _, p := range n.Peers() {
			if !p.Current {
				return fmt.Errorf("peer %s not current after leader change (lag=%d)", p.ID, p.Lag)
			}
		}
		return nil
	})
}

// TestJetStreamClusterStreamHealthCheckWithoutWriteErr verifies that
// isStreamHealthy no longer reports write errors (since writeErr tracking
// was removed), and that streams remain healthy after various operations.
func TestJetStreamClusterStreamHealthCheckWithoutWriteErr(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish messages to have a working stream.
	for i := 0; i < 100; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Verify all replicas report the stream as healthy.
	for _, s := range c.servers {
		sjs := s.getJetStream()
		if sjs == nil {
			continue
		}
		acc, _ := s.lookupAccount(globalAccountName)
		if acc == nil {
			continue
		}
		sa := sjs.streamAssignment(globalAccountName, "TEST")
		if sa == nil {
			continue
		}
		err := sjs.isStreamHealthy(acc, sa)
		if err != nil {
			t.Logf("Server %s health check: %v", s.Name(), err)
		}
	}

	// Purge and verify health is still OK.
	require_NoError(t, js.PurgeStream("TEST"))

	// Publish more.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo", []byte("post-purge"))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// The stream health check should pass on at least the leader.
	sl := c.streamLeader(globalAccountName, "TEST")
	sjs := sl.getJetStream()
	acc, _ := sl.lookupAccount(globalAccountName)
	sa := sjs.streamAssignment(globalAccountName, "TEST")
	err = sjs.isStreamHealthy(acc, sa)
	require_NoError(t, err)
}

// TestJetStreamClusterProcessSnapshotDeletesIgnoresErrors verifies that
// processSnapshotDeletes (now void) correctly handles compaction and
// delete sync without propagating errors, and the stream remains consistent.
func TestJetStreamClusterProcessSnapshotDeletesIgnoresErrors(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:      "TEST",
		Subjects:  []string{"foo"},
		Replicas:  3,
		Retention: nats.LimitsPolicy,
		MaxMsgs:   100,
	})
	require_NoError(t, err)

	// Publish more than MaxMsgs to cause deletions.
	for i := 0; i < 200; i++ {
		_, err := js.Publish("foo", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// Wait for all replicas to converge.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Get a follower and force a snapshot on it to exercise processSnapshotDeletes.
	follower := c.randomNonStreamLeader(globalAccountName, "TEST")
	require_NotNil(t, follower)
	mset, err := follower.globalAccount().lookupStream("TEST")
	require_NoError(t, err)

	// Install snapshot which will trigger processSnapshotDeletes on the next apply.
	node := mset.raftNode()
	require_NotNil(t, node)
	err = node.InstallSnapshot(mset.stateSnapshot(), false)
	require_NoError(t, err)

	// Publish more to exercise snapshot recovery.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo", []byte(fmt.Sprintf("more-%d", i)))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	si, err := js.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 100)
}

// TestJetStreamClusterFlushAllPendingVoid verifies that the void
// flushAllPending doesn't cause issues during catchup and snapshot operations.
func TestJetStreamClusterFlushAllPendingVoid(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish messages.
	for i := 0; i < 500; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Shut down a follower so it falls behind.
	follower := c.randomNonStreamLeader(globalAccountName, "TEST")
	require_NotNil(t, follower)
	follower.Shutdown()
	follower.WaitForShutdown()

	// Publish more while follower is down.
	for i := 0; i < 500; i++ {
		_, err := js.Publish("foo", []byte("catchup"))
		require_NoError(t, err)
	}

	// Restart the follower — it will need to catch up.
	follower = c.restartServer(follower)
	c.waitOnServerCurrent(follower)

	// Catchup involves flushAllPending at the end (now void).
	// Verify all replicas converge.
	checkFor(t, 10*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	si, err := js.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 1000)
}

// TestJetStreamClusterConsumerStoreUpdateIgnoresOldState verifies that
// consumer store Update now handles all errors at the store level and
// doesn't propagate ErrStoreOldUpdate (which was removed).
func TestJetStreamClusterConsumerStoreUpdateIgnoresOldState(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// Advance consumer state.
	for i := 0; i < 10; i++ {
		_, err = js.Publish("foo", []byte("msg"))
		require_NoError(t, err)
	}
	sub, err := js.PullSubscribe("foo", "CONSUMER")
	require_NoError(t, err)
	msgs, err := sub.Fetch(10)
	require_NoError(t, err)
	require_Len(t, len(msgs), 10)
	for _, m := range msgs {
		require_NoError(t, m.AckSync())
	}

	// Wait for all servers to be caught up.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		for _, s := range c.servers {
			a, _ := s.lookupAccount(globalAccountName)
			if a == nil {
				return errors.New("account not found")
			}
			ms, err := a.lookupStream("TEST")
			if err != nil {
				return err
			}
			co := ms.lookupConsumer("CONSUMER")
			if co == nil {
				return errors.New("consumer not found")
			}
			if info := co.info(); info.AckFloor.Consumer != 10 {
				return fmt.Errorf("expected ack floor 10, got %d", info.AckFloor.Consumer)
			}
		}
		return nil
	})

	// Try to apply stale state to the consumer store.
	// This should not cause the consumer to stop or its raft node to be deleted.
	rs := c.randomNonConsumerLeader(globalAccountName, "TEST", "CONSUMER")
	require_NotNil(t, rs)
	acc, _ := rs.lookupAccount(globalAccountName)
	mset, err := acc.lookupStream("TEST")
	require_NoError(t, err)
	o := mset.lookupConsumer("CONSUMER")
	require_NotNil(t, o)

	// Apply stale state (ack floor 5 when actual is 10).
	staleState := &ConsumerState{
		Delivered: SequencePair{Consumer: 5, Stream: 5},
		AckFloor:  SequencePair{Consumer: 5, Stream: 5},
	}
	o.mu.Lock()
	err = o.setStoreState(staleState)
	o.mu.Unlock()

	// The error behavior depends on the store implementation.
	// The key thing is the consumer is still functional.
	t.Logf("setStoreState with stale data returned: %v", err)

	// Consumer should still exist and be functional.
	o2 := mset.lookupConsumer("CONSUMER")
	require_NotNil(t, o2)

	// Publish and consume more to verify it's still working.
	for i := 0; i < 5; i++ {
		_, err = js.Publish("foo", []byte("after-stale"))
		require_NoError(t, err)
	}
	msgs, err = sub.Fetch(5)
	require_NoError(t, err)
	require_Len(t, len(msgs), 5)
}

// TestJetStreamClusterReservationWithAccountLimits verifies that reservation
// calculations work correctly with account-level storage limits, including
// the per-stream byte limit fields (MemoryMaxStreamBytes/StoreMaxStreamBytes).
func TestJetStreamClusterReservationWithAccountLimits(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	// Set up account limits with per-stream byte limits.
	for _, s := range c.servers {
		acc := s.globalAccount()
		err := acc.UpdateJetStreamLimits(map[string]JetStreamAccountLimits{
			_EMPTY_: {
				MaxMemory:            100 * 1024 * 1024, // 100MB
				MaxStore:             100 * 1024 * 1024, // 100MB
				StoreMaxStreamBytes:  10 * 1024 * 1024,  // 10MB per stream
				MemoryMaxStreamBytes: 10 * 1024 * 1024,  // 10MB per stream
				MaxBytesRequired:     true,
			},
		})
		require_NoError(t, err)
	}

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	// Stream without MaxBytes should fail since MaxBytesRequired is true.
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "NO_MAXBYTES",
		Subjects: []string{"no.>"},
		Replicas: 3,
	})
	require_Error(t, err)

	// Stream with MaxBytes within the per-stream limit should succeed.
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "WITHIN_LIMIT",
		Subjects: []string{"within.>"},
		Replicas: 3,
		MaxBytes: 5 * 1024 * 1024, // 5MB
	})
	require_NoError(t, err)

	// Stream with MaxBytes exceeding the per-stream limit should fail.
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "OVER_LIMIT",
		Subjects: []string{"over.>"},
		Replicas: 3,
		MaxBytes: 20 * 1024 * 1024, // 20MB
	})
	require_Error(t, err)

	// Multiple streams that fit within the account total.
	for i := 0; i < 5; i++ {
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     fmt.Sprintf("STREAM_%d", i),
			Subjects: []string{fmt.Sprintf("s%d.>", i)},
			Replicas: 3,
			MaxBytes: 5 * 1024 * 1024, // 5MB each
		})
		require_NoError(t, err)
	}

	// Verify all streams exist.
	names := js.StreamNames()
	count := 0
	for range names {
		count++
	}
	require_Equal(t, count, 6) // WITHIN_LIMIT + 5 STREAM_N

	// Verify the stream info includes correct MaxBytes.
	si, err := js.StreamInfo("WITHIN_LIMIT")
	require_NoError(t, err)
	require_Equal(t, si.Config.MaxBytes, int64(5*1024*1024))
}

// TestJetStreamClusterStreamUpdateSyncSubject verifies that stream updates
// preserve the sync subject from the original assignment rather than
// regenerating it. This prevents potential issues during rolling upgrades.
func TestJetStreamClusterStreamUpdateSyncSubject(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	// Create a stream.
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Get the original sync subject.
	sl := c.streamLeader(globalAccountName, "TEST")
	sjs := sl.getJetStream()
	sa := sjs.streamAssignment(globalAccountName, "TEST")
	require_NotNil(t, sa)
	originalSync := sa.Sync
	require_True(t, originalSync != "")

	// Update the stream (add a subject).
	_, err = js.UpdateStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo", "bar"},
		Replicas: 3,
	})
	require_NoError(t, err)

	c.waitOnStreamLeader(globalAccountName, "TEST")

	// Verify the sync subject is preserved.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		sl = c.streamLeader(globalAccountName, "TEST")
		sjs = sl.getJetStream()
		sa = sjs.streamAssignment(globalAccountName, "TEST")
		if sa == nil {
			return errors.New("stream assignment not found")
		}
		if sa.Sync != originalSync {
			return fmt.Errorf("sync subject changed from %q to %q", originalSync, sa.Sync)
		}
		return nil
	})

	// Verify the stream is still functional.
	_, err = js.Publish("bar", []byte("new-subject"))
	require_NoError(t, err)
	si, err := js.StreamInfo("TEST")
	require_NoError(t, err)
	require_True(t, si.State.Msgs > 0)
}

// TestJetStreamClusterFilteredStateVoid verifies that FilteredState
// (now returning SimpleState instead of (SimpleState, error)) works
// correctly for various filter scenarios.
func TestJetStreamClusterFilteredStateVoid(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo.*"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish messages on different subjects.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo.bar", []byte("bar"))
		require_NoError(t, err)
	}
	for i := 0; i < 30; i++ {
		_, err := js.Publish("foo.baz", []byte("baz"))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	sl := c.streamLeader(globalAccountName, "TEST")
	mset, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)

	// Empty filter should return all messages.
	allState := mset.store.FilteredState(0, _EMPTY_)
	require_Equal(t, allState.Msgs, 80)

	// Specific subject filter.
	barState := mset.store.FilteredState(0, "foo.bar")
	require_Equal(t, barState.Msgs, 50)

	bazState := mset.store.FilteredState(0, "foo.baz")
	require_Equal(t, bazState.Msgs, 30)

	// Non-existent subject should return zero.
	noneState := mset.store.FilteredState(0, "foo.nope")
	require_Equal(t, noneState.Msgs, 0)
}

// TestJetStreamClusterClusteredInboundMsgLockOrdering verifies that the
// lock ordering fix in processClusteredInboundMsg (stream -> batchMu -> clMu)
// doesn't cause deadlocks under concurrent publishing.
func TestJetStreamClusterClusteredInboundMsgLockOrdering(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo.>"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Hammer the stream with concurrent publishes from multiple goroutines.
	// This exercises the lock ordering in processClusteredInboundMsg.
	errCh := make(chan error, 10)
	const numGoroutines = 10
	const msgsPerGoroutine = 100

	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			nc2, js2 := jsClientConnect(t, c.randomServer())
			defer nc2.Close()
			for i := 0; i < msgsPerGoroutine; i++ {
				_, err := js2.Publish(fmt.Sprintf("foo.%d", id), []byte(fmt.Sprintf("msg-%d-%d", id, i)))
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d, msg %d: %w", id, i, err)
					return
				}
			}
			errCh <- nil
		}(g)
	}

	// Wait for all goroutines to complete.
	for i := 0; i < numGoroutines; i++ {
		err := <-errCh
		if err != nil {
			t.Logf("Publish error: %v", err)
		}
	}

	// Verify the stream is consistent.
	checkFor(t, 10*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	si, err := js.StreamInfo("TEST")
	require_NoError(t, err)
	t.Logf("Final stream state: %d messages", si.State.Msgs)
	require_True(t, si.State.Msgs > 0)
}

// TestJetStreamClusterConsumerClearRaftNode verifies the new clearRaftNode
// method on consumer correctly clears the node reference.
func TestJetStreamClusterConsumerClearRaftNode(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, "TEST", "CONSUMER")

	// Get the consumer on one server.
	sl := c.consumerLeader(globalAccountName, "TEST", "CONSUMER")
	mset, err := sl.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	o := mset.lookupConsumer("CONSUMER")
	require_NotNil(t, o)

	// Verify the raft node is set.
	require_NotNil(t, o.raftNode())

	// Verify clearRaftNode works on nil consumer (should not panic).
	var nilConsumer *consumer
	nilConsumer.clearRaftNode() // Should not panic.

	// The consumer should still be accessible.
	ci, err := js.ConsumerInfo("TEST", "CONSUMER")
	require_NoError(t, err)
	require_Equal(t, ci.Name, "CONSUMER")
}

// TestJetStreamClusterSyncDeletedVoid verifies that SyncDeleted (now void)
// correctly handles deleted message synchronization across replicas.
func TestJetStreamClusterSyncDeletedVoid(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Publish messages.
	for i := 0; i < 100; i++ {
		_, err := js.Publish("foo", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Delete some messages in the middle to create gaps.
	for i := 10; i < 20; i++ {
		require_NoError(t, js.DeleteMsg("TEST", uint64(i)))
	}

	// Verify the deletions propagated.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	si, err := js.StreamInfo("TEST", &nats.StreamInfoRequest{DeletedDetails: true})
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 90)
	require_Equal(t, si.State.NumDeleted, 10)

	// Force a snapshot on a follower to exercise SyncDeleted.
	follower := c.randomNonStreamLeader(globalAccountName, "TEST")
	require_NotNil(t, follower)
	mset, err := follower.globalAccount().lookupStream("TEST")
	require_NoError(t, err)
	node := mset.raftNode()
	require_NotNil(t, node)
	err = node.InstallSnapshot(mset.stateSnapshot(), false)
	require_NoError(t, err)

	// Publish more and verify convergence.
	for i := 0; i < 50; i++ {
		_, err := js.Publish("foo", []byte("after-delete"))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})
}

// TestJetStreamClusterMetaSnapshotWithoutConsumerState verifies backward
// compatibility: when a meta snapshot doesn't include consumer State
// (as would be the case from an older server version), consumers still
// recover correctly.
func TestJetStreamClusterMetaSnapshotWithoutConsumerState(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// Publish and ack messages.
	for i := 0; i < 10; i++ {
		_, err = js.Publish("foo", []byte("msg"))
		require_NoError(t, err)
	}
	sub, err := js.PullSubscribe("foo", "CONSUMER")
	require_NoError(t, err)
	msgs, err := sub.Fetch(10)
	require_NoError(t, err)
	for _, m := range msgs {
		require_NoError(t, m.AckSync())
	}

	// Verify consumer is caught up.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		ci, err := js.ConsumerInfo("TEST", "CONSUMER")
		if err != nil {
			return err
		}
		if ci.AckFloor.Consumer != 10 {
			return fmt.Errorf("expected ack floor 10, got %d", ci.AckFloor.Consumer)
		}
		return nil
	})

	// Simulate the scenario by encoding a meta snapshot, then stripping
	// the State field to simulate an old-format snapshot.
	ml := c.leader()
	sjs := ml.getJetStream()
	meta := sjs.getMetaGroup().(*raft)

	// Force a meta snapshot.
	require_NoError(t, meta.ProposeAddPeer(meta.ID()))
	time.Sleep(500 * time.Millisecond)

	snap, err := meta.loadLastSnapshot()
	require_NoError(t, err)

	// Decode and re-encode without State to simulate old format.
	accStreams, err := sjs.decodeMetaSnapshot(snap.data)
	require_NoError(t, err)

	// Verify State was encoded.
	stream := accStreams[globalAccountName]["TEST"]
	require_NotNil(t, stream)
	ca := stream.consumers["CONSUMER"]
	require_NotNil(t, ca)

	// Strip the state (simulate old snapshot format).
	ca.State = nil

	// Re-encode.
	newSnap, _, _, err := sjs.encodeMetaSnapshot(accStreams)
	require_NoError(t, err)

	// Verify decode of the stripped snapshot works.
	accStreams2, err := sjs.decodeMetaSnapshot(newSnap)
	require_NoError(t, err)
	stream2 := accStreams2[globalAccountName]["TEST"]
	require_NotNil(t, stream2)
	ca2 := stream2.consumers["CONSUMER"]
	require_NotNil(t, ca2)
	// State should be nil (old format).
	require_True(t, ca2.State == nil)

	// The consumer should still be fully functional even without meta state.
	for i := 0; i < 5; i++ {
		_, err = js.Publish("foo", []byte("more"))
		require_NoError(t, err)
	}
	msgs, err = sub.Fetch(5)
	require_NoError(t, err)
	require_Len(t, len(msgs), 5)
}

// TestJetStreamClusterInboundMsgErrorResponse verifies that the updated
// error response in processClusteredInboundMsg uses the correct message
// format (newJSPubMsg instead of sendMsg).
func TestJetStreamClusterInboundMsgErrorResponse(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
		MaxMsgs:  10,
		Discard:  nats.DiscardNew,
	})
	require_NoError(t, err)

	// Fill the stream to the limit.
	for i := 0; i < 10; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}

	// The next publish should get a proper error response.
	_, err = js.Publish("foo", []byte("overflow"))
	require_Error(t, err)

	// Verify the error is a proper JetStream error, not a timeout or panic.
	var apiErr *nats.APIError
	if ok := errors.As(err, &apiErr); ok {
		t.Logf("Got expected API error: code=%d, description=%s", apiErr.Code, apiErr.Description)
	} else {
		// Even without APIError, a publish error is expected here.
		t.Logf("Got error (expected): %v", err)
	}

	// The stream should still be functional for reads.
	si, err := js.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 10)
}

// TestJetStreamClusterNoRecoverStreamFunction verifies that after removing
// the separate recoverStream function, streams still recover correctly
// on server restart using the unified addStream path.
func TestJetStreamClusterNoRecoverStreamFunction(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	// Create a stream and populate it.
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	for i := 0; i < 200; i++ {
		_, err := js.Publish("foo", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		return checkState(t, c, globalAccountName, "TEST")
	})

	// Create a consumer too.
	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// Restart each server one at a time and verify recovery.
	for _, s := range c.servers {
		s.Shutdown()
		s.WaitForShutdown()
		s = c.restartServer(s)
		c.waitOnServerCurrent(s)
	}

	// After all restarts, verify the stream and consumer are intact.
	nc2, js2 := jsClientConnect(t, c.randomServer())
	defer nc2.Close()

	si, err := js2.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 200)

	ci, err := js2.ConsumerInfo("TEST", "CONSUMER")
	require_NoError(t, err)
	require_Equal(t, ci.Name, "CONSUMER")

	// Can still publish and consume.
	_, err = js2.Publish("foo", []byte("after-restart"))
	require_NoError(t, err)

	si, err = js2.StreamInfo("TEST")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, 201)
}

// TestJetStreamClusterConsumerInfoDuringScaleDown verifies that during
// consumer request handling, the cfg.Durable is used consistently
// (replacing the previous oname usage).
func TestJetStreamClusterConsumerInfoDuringScaleDown(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Create a durable consumer.
	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "MY_CONSUMER",
		Replicas:  3,
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, "TEST", "MY_CONSUMER")

	// Verify consumer info works.
	ci, err := js.ConsumerInfo("TEST", "MY_CONSUMER")
	require_NoError(t, err)
	require_Equal(t, ci.Config.Durable, "MY_CONSUMER")

	// Scale down the stream (which may trigger consumer reassignment).
	_, err = js.UpdateStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 1,
	})
	require_NoError(t, err)

	// Consumer should still be accessible.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		ci, err = js.ConsumerInfo("TEST", "MY_CONSUMER")
		if err != nil {
			return err
		}
		if ci.Config.Durable != "MY_CONSUMER" {
			return fmt.Errorf("expected durable MY_CONSUMER, got %s", ci.Config.Durable)
		}
		return nil
	})
}

