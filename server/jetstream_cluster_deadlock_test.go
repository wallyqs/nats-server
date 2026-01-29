// Copyright 2026 The NATS Authors
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

//go:build !skip_js_tests && !skip_js_cluster_tests

package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamClusterWorkqueuePeerRemoveDuringRecovery tests the deadlock
// scenario where a workqueue stream with many deleted messages recovers on
// a node while a peer-remove is triggered concurrently. This matches the
// production incident:
//
//	config: {
//	  name: "unique_ids",
//	  retention: "workqueue",
//	  replicas: 3,
//	  max_msgs_per_subject: -1,
//	  max_age: 0,
//	}
//	state: {
//	  messages: 1,083,118
//	  num_deleted: 6,173,268
//	}
//
// The scenario:
//  1. 5-node cluster with R3 workqueue stream
//  2. Stream accumulates many messages and deletions (consumer acks)
//  3. A stream replica is shut down (simulating crash)
//  4. While it's recovering, a peer-remove is issued against that node
//  5. This can cause I/O errors during recovery (missing files, directory
//     removal) which may trigger a panic or early return in
//     newFileStoreWithCreated (lines 557-589), leaving fs.mu locked
//  6. The deferred cleanupOldMeta goroutine then deadlocks on RLock
//
// Use NATS_LOGGING=info to see server logs during the test.
func TestJetStreamClusterWorkqueuePeerRemoveDuringRecovery(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	// Connect to the meta leader for API operations.
	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	// Create workqueue stream with R3, matching production config.
	streamName := "unique_ids"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"unique_ids.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
	})
	require_NoError(t, err)

	c.waitOnStreamLeader(globalAccountName, streamName)
	t.Log("Stream created, leader elected")

	// Create a consumer to ack messages (workqueue deletion).
	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   "worker",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, streamName, "worker")

	// Publish a batch of messages.
	numMessages := 5000
	t.Logf("Publishing %d messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		subject := fmt.Sprintf("unique_ids.item.%d", i%50)
		_, err := js.Publish(subject, []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Error publishing message %d: %v", i, err)
		}
	}

	si, err := js.StreamInfo(streamName)
	require_NoError(t, err)
	t.Logf("Stream state after publish: Msgs=%d, FirstSeq=%d, LastSeq=%d",
		si.State.Msgs, si.State.FirstSeq, si.State.LastSeq)

	// Consume and ack most messages to simulate workqueue deletions.
	sub, err := js.PullSubscribe("unique_ids.>", "worker")
	require_NoError(t, err)

	ackedCount := 0
	toAck := numMessages - 100 // Leave some unacked
	for ackedCount < toAck {
		batch := toAck - ackedCount
		if batch > 100 {
			batch = 100
		}
		msgs, err := sub.Fetch(batch, nats.MaxWait(5*time.Second))
		if err != nil {
			t.Logf("Fetch error after %d acks: %v", ackedCount, err)
			break
		}
		for _, m := range msgs {
			if err := m.Ack(); err != nil {
				t.Logf("Ack error: %v", err)
			}
			ackedCount++
		}
	}

	t.Logf("Acked %d messages (simulating workqueue consumer)", ackedCount)

	// Wait for ack processing to complete.
	checkFor(t, 10*time.Second, 500*time.Millisecond, func() error {
		si, err := js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		if si.State.Msgs > uint64(numMessages-ackedCount+100) {
			return fmt.Errorf("waiting for acks to process: %d msgs remaining", si.State.Msgs)
		}
		return nil
	})

	si, err = js.StreamInfo(streamName)
	require_NoError(t, err)
	t.Logf("Stream state after acks: Msgs=%d, FirstSeq=%d, LastSeq=%d, Deleted=%d",
		si.State.Msgs, si.State.FirstSeq, si.State.LastSeq, si.State.NumDeleted)

	// Pick a non-leader replica to shut down and later peer-remove.
	targetServer := c.randomNonStreamLeader(globalAccountName, streamName)
	require_True(t, targetServer != nil)
	targetName := targetServer.Name()
	t.Logf("Target server for shutdown + peer-remove: %s", targetName)

	// Shut down the target server (simulating crash).
	t.Logf("Shutting down %s...", targetName)
	targetServer.Shutdown()
	targetServer.WaitForShutdown()
	t.Logf("Server %s is down", targetName)

	// Wait for stream to stabilize with remaining replicas.
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Reconnect to a live server in case we were connected to the shutdown one.
	nc.Close()
	nc, js = jsClientConnect(t, c.leader())
	defer nc.Close()

	// Restart the server (triggers recovery of filestore).
	t.Logf("Restarting %s (will trigger filestore recovery)...", targetName)
	targetServer = c.restartServer(targetServer)

	// Immediately issue peer-remove while the server is recovering.
	// This is the critical race: the recovery process is rebuilding state
	// while the peer-remove triggers file/directory cleanup.
	t.Logf("Issuing peer-remove for %s during recovery...", targetName)

	removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
	removeReq := []byte(`{"peer":"` + targetName + `"}`)
	resp, err := nc.Request(removeSub, removeReq, 10*time.Second)
	require_NoError(t, err)

	var rpResp JSApiStreamRemovePeerResponse
	err = json.Unmarshal(resp.Data, &rpResp)
	require_NoError(t, err)
	t.Logf("Peer remove response: Success=%v, Error=%+v", rpResp.Success, rpResp.Error)

	// Wait for leader to stabilize after peer-remove.
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Verify peer was removed.
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == targetName {
				return fmt.Errorf("peer %s still present in replicas", targetName)
			}
		}
		return nil
	})

	t.Logf("Peer %s removed successfully", targetName)
	t.Logf("Stream cluster: leader=%s, replicas=%d", si.Cluster.Leader, len(si.Cluster.Replicas))

	// Verify stream is still functional.
	_, err = js.Publish("unique_ids.verify", []byte("after-peer-remove"))
	require_NoError(t, err)

	si, err = js.StreamInfo(streamName)
	require_NoError(t, err)
	t.Logf("Final stream state: Msgs=%d, FirstSeq=%d, LastSeq=%d, Deleted=%d",
		si.State.Msgs, si.State.FirstSeq, si.State.LastSeq, si.State.NumDeleted)

	// Verify the restarted server is still functioning (not deadlocked).
	// Try to connect and perform operations.
	nc2, err := nats.Connect(targetServer.ClientURL())
	if err != nil {
		// Server may have been fully removed from cluster, this is acceptable.
		t.Logf("Note: could not connect to removed peer %s: %v (expected if fully removed)", targetName, err)
	} else {
		nc2.Close()
		t.Logf("Removed peer %s is still responsive (not deadlocked)", targetName)
	}
}

// TestJetStreamClusterWorkqueueLeaderStepdownDuringRecovery tests a
// scenario where a stream leader crashes and comes back while the
// remaining replicas may have issued a leader stepdown. This is another
// path that can trigger I/O issues during recovery.
func TestJetStreamClusterWorkqueueLeaderStepdownDuringRecovery(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	streamName := "workqueue_test"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"work.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Create consumer for workqueue.
	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   "processor",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, streamName, "processor")

	// Publish messages.
	numMessages := 3000
	t.Logf("Publishing %d messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		_, err := js.Publish(fmt.Sprintf("work.task.%d", i%20), []byte(fmt.Sprintf("payload-%d", i)))
		if err != nil {
			t.Fatalf("Publish error at msg %d: %v", i, err)
		}
	}

	// Consume and ack to create deletions.
	sub, err := js.PullSubscribe("work.>", "processor")
	require_NoError(t, err)

	ackedCount := 0
	toAck := numMessages - 50
	for ackedCount < toAck {
		batch := toAck - ackedCount
		if batch > 100 {
			batch = 100
		}
		msgs, err := sub.Fetch(batch, nats.MaxWait(5*time.Second))
		if err != nil {
			t.Logf("Fetch error after %d acks: %v", ackedCount, err)
			break
		}
		for _, m := range msgs {
			m.Ack()
			ackedCount++
		}
	}
	t.Logf("Acked %d messages", ackedCount)

	// Wait for deletions to settle.
	checkFor(t, 10*time.Second, 500*time.Millisecond, func() error {
		si, err := js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		if si.State.Msgs > uint64(numMessages-ackedCount+100) {
			return fmt.Errorf("waiting for deletions, %d msgs remaining", si.State.Msgs)
		}
		return nil
	})

	si, err := js.StreamInfo(streamName)
	require_NoError(t, err)
	t.Logf("State before leader shutdown: Msgs=%d, Deleted=%d", si.State.Msgs, si.State.NumDeleted)

	// This time, shut down the stream LEADER (not a follower).
	leader := c.streamLeader(globalAccountName, streamName)
	require_True(t, leader != nil)
	leaderName := leader.Name()
	t.Logf("Shutting down stream leader: %s", leaderName)
	leader.Shutdown()
	leader.WaitForShutdown()

	// A new leader should be elected.
	c.waitOnStreamLeader(globalAccountName, streamName)
	t.Log("New stream leader elected")

	// Reconnect.
	nc.Close()
	nc, js = jsClientConnect(t, c.leader())
	defer nc.Close()

	// Publish more messages while the old leader is down.
	t.Log("Publishing 500 more messages while old leader is down...")
	for i := 0; i < 500; i++ {
		_, err := js.Publish(fmt.Sprintf("work.new.%d", i), []byte(fmt.Sprintf("new-%d", i)))
		if err != nil {
			t.Logf("Publish during recovery: %v", err)
			break
		}
	}

	// Now restart the old leader.
	t.Logf("Restarting old leader %s...", leaderName)
	leader = c.restartServer(leader)

	// Immediately issue a peer-remove against the recovering leader.
	t.Logf("Issuing peer-remove for recovering server %s...", leaderName)
	removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
	resp, err := nc.Request(removeSub, []byte(`{"peer":"`+leaderName+`"}`), 10*time.Second)
	require_NoError(t, err)

	var rpResp JSApiStreamRemovePeerResponse
	json.Unmarshal(resp.Data, &rpResp)
	t.Logf("Peer remove response: Success=%v", rpResp.Success)

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Verify the stream is still healthy.
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == leaderName {
				return fmt.Errorf("old leader %s still present", leaderName)
			}
		}
		return nil
	})

	t.Logf("Old leader %s removed. Stream state: Msgs=%d, Deleted=%d",
		leaderName, si.State.Msgs, si.State.NumDeleted)

	// Verify stream is functional.
	_, err = js.Publish("work.verify", []byte("after-recovery"))
	require_NoError(t, err)
	t.Log("Stream operational after leader recovery + peer-remove")
}

// TestJetStreamClusterWorkqueueConcurrentPeerRemoveAndPublish tests the
// deadlock scenario with concurrent peer-remove, publishing, and consuming
// happening simultaneously during recovery. This stress-tests the recovery
// path to increase the chance of triggering I/O errors.
func TestJetStreamClusterWorkqueueConcurrentPeerRemoveAndPublish(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	streamName := "concurrent_wq"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"cwork.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   "worker",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, streamName, "worker")

	// Publish initial messages.
	numMessages := 2000
	t.Logf("Publishing %d initial messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		_, err := js.Publish(fmt.Sprintf("cwork.item.%d", i%30), []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Publish error: %v", err)
		}
	}

	// Consume and ack most messages.
	sub, err := js.PullSubscribe("cwork.>", "worker")
	require_NoError(t, err)

	ackedCount := 0
	toAck := numMessages - 50
	for ackedCount < toAck {
		batch := toAck - ackedCount
		if batch > 100 {
			batch = 100
		}
		msgs, err := sub.Fetch(batch, nats.MaxWait(5*time.Second))
		if err != nil {
			break
		}
		for _, m := range msgs {
			m.Ack()
			ackedCount++
		}
	}
	t.Logf("Acked %d messages", ackedCount)

	// Let deletions settle.
	time.Sleep(2 * time.Second)

	si, _ := js.StreamInfo(streamName)
	t.Logf("State: Msgs=%d, Deleted=%d", si.State.Msgs, si.State.NumDeleted)

	// Pick target for removal.
	target := c.randomNonStreamLeader(globalAccountName, streamName)
	require_True(t, target != nil)
	targetName := target.Name()
	t.Logf("Target for shutdown + peer-remove: %s", targetName)

	// Shut down target.
	target.Shutdown()
	target.WaitForShutdown()

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Reconnect.
	nc.Close()
	nc, js = jsClientConnect(t, c.leader())
	defer nc.Close()

	// Start concurrent operations.
	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	// Goroutine 1: Keep publishing.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, err := js.Publish(fmt.Sprintf("cwork.new.%d", i), []byte(fmt.Sprintf("new-%d", i)))
			if err != nil {
				errCh <- fmt.Errorf("publish error during recovery: %v", err)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Goroutine 2: Restart the server (triggers recovery).
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Small delay to let publishes start.
		time.Sleep(50 * time.Millisecond)
		target = c.restartServer(target)
		t.Logf("Server %s restarted", targetName)
	}()

	// Goroutine 3: Issue peer-remove after a brief delay.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Wait a bit for the server to start recovering.
		time.Sleep(200 * time.Millisecond)

		removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
		resp, err := nc.Request(removeSub, []byte(`{"peer":"`+targetName+`"}`), 10*time.Second)
		if err != nil {
			errCh <- fmt.Errorf("peer-remove request error: %v", err)
			return
		}
		var rpResp JSApiStreamRemovePeerResponse
		json.Unmarshal(resp.Data, &rpResp)
		t.Logf("Peer remove result: Success=%v, Error=%+v", rpResp.Success, rpResp.Error)
	}()

	// Wait for all concurrent operations.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("All concurrent operations completed")
	case <-time.After(60 * time.Second):
		t.Fatal("TIMEOUT: Concurrent operations deadlocked!")
	}

	// Drain errors.
	close(errCh)
	for err := range errCh {
		t.Logf("Concurrent error: %v", err)
	}

	// Verify stream health.
	c.waitOnStreamLeader(globalAccountName, streamName)

	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == targetName {
				return fmt.Errorf("peer %s still present", targetName)
			}
		}
		return nil
	})

	// Final publish to verify stream is working.
	_, err = js.Publish("cwork.final", []byte("done"))
	require_NoError(t, err)

	si, _ = js.StreamInfo(streamName)
	t.Logf("Final state: Msgs=%d, Deleted=%d, Leader=%s, Replicas=%d",
		si.State.Msgs, si.State.NumDeleted, si.Cluster.Leader, len(si.Cluster.Replicas))

	// Verify the removed server is still responsive (not deadlocked).
	nc3, err := nats.Connect(target.ClientURL())
	if err != nil {
		t.Logf("Note: Removed peer %s not connectable: %v", targetName, err)
	} else {
		nc3.Close()
		t.Logf("Removed peer %s is responsive (no deadlock)", targetName)
	}
}

// TestJetStreamClusterWorkqueuePeerRemoveCorruptedStore tests a peer-remove
// with corrupted block files on the target server, matching the production
// scenario where we saw:
//
//	[WRN] Filestore [...] loadBlock error: message block data missing
//	[ERR] RAFT ... Critical write error: Error creating msg block file: ...no such file or directory
//
// By removing block files after shutdown and before restart, we force
// I/O errors during filestore recovery. Combined with a peer-remove,
// this can trigger the deadlock in newFileStoreWithCreated where
// fs.mu.Lock() is held without defer unlock (lines 557-589).
func TestJetStreamClusterWorkqueuePeerRemoveCorruptedStore(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	streamName := "unique_ids"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"unique_ids.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Create consumer.
	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   "worker",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, streamName, "worker")

	// Publish messages.
	numMessages := 5000
	t.Logf("Publishing %d messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		_, err := js.Publish(fmt.Sprintf("unique_ids.item.%d", i%50), []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Publish error at msg %d: %v", i, err)
		}
	}

	// Consume and ack most messages.
	sub, err := js.PullSubscribe("unique_ids.>", "worker")
	require_NoError(t, err)

	ackedCount := 0
	toAck := numMessages - 100
	for ackedCount < toAck {
		batch := toAck - ackedCount
		if batch > 100 {
			batch = 100
		}
		msgs, err := sub.Fetch(batch, nats.MaxWait(5*time.Second))
		if err != nil {
			break
		}
		for _, m := range msgs {
			m.Ack()
			ackedCount++
		}
	}
	t.Logf("Acked %d messages", ackedCount)

	// Wait for acks to settle.
	checkFor(t, 10*time.Second, 500*time.Millisecond, func() error {
		si, err := js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		if si.State.Msgs > uint64(numMessages-ackedCount+100) {
			return fmt.Errorf("waiting: %d msgs remaining", si.State.Msgs)
		}
		return nil
	})

	si, _ := js.StreamInfo(streamName)
	t.Logf("State after acks: Msgs=%d, FirstSeq=%d, LastSeq=%d, Deleted=%d",
		si.State.Msgs, si.State.FirstSeq, si.State.LastSeq, si.State.NumDeleted)

	// Pick target and get its store directory before shutdown.
	target := c.randomNonStreamLeader(globalAccountName, streamName)
	require_True(t, target != nil)
	targetName := target.Name()

	// Get the fileStore's directory.
	acc, err := target.lookupAccount(globalAccountName)
	require_NoError(t, err)
	mset, err := acc.lookupStream(streamName)
	require_NoError(t, err)
	fs := mset.store.(*fileStore)
	storeMsgDir := filepath.Join(fs.fcfg.StoreDir, msgDir)
	t.Logf("Target %s store msgs dir: %s", targetName, storeMsgDir)

	// Shut down the target.
	t.Logf("Shutting down %s...", targetName)
	target.Shutdown()
	target.WaitForShutdown()

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Corrupt the store: remove block files to trigger "message block data missing"
	// and remove the index to force full recovery.
	entries, err := os.ReadDir(storeMsgDir)
	if err != nil {
		t.Fatalf("Cannot read store dir: %v", err)
	}

	removedFiles := 0
	for _, e := range entries {
		name := e.Name()
		// Remove .blk files (block data) and index to force recovery with missing blocks.
		if filepath.Ext(name) == ".blk" || name == "index.db" {
			path := filepath.Join(storeMsgDir, name)
			if err := os.Remove(path); err != nil {
				t.Logf("Could not remove %s: %v", name, err)
			} else {
				removedFiles++
			}
		}
	}
	t.Logf("Removed %d files from store to simulate corruption", removedFiles)

	// Reconnect in case we were connected to the shutdown server.
	nc.Close()
	nc, js = jsClientConnect(t, c.leader())
	defer nc.Close()

	// Restart the server (triggers recovery with missing block files).
	t.Logf("Restarting %s with corrupted store...", targetName)
	target = c.restartServer(target)

	// Immediately issue peer-remove while the server recovers with corrupted files.
	t.Logf("Issuing peer-remove for %s during corrupted recovery...", targetName)
	removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
	resp, err := nc.Request(removeSub, []byte(`{"peer":"`+targetName+`"}`), 10*time.Second)
	require_NoError(t, err)

	var rpResp JSApiStreamRemovePeerResponse
	json.Unmarshal(resp.Data, &rpResp)
	t.Logf("Peer remove response: Success=%v, Error=%+v", rpResp.Success, rpResp.Error)

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Verify peer removal completes (this would hang if deadlocked).
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == targetName {
				return fmt.Errorf("peer %s still present", targetName)
			}
		}
		return nil
	})

	t.Logf("Peer %s removed after corrupted recovery", targetName)

	// Verify stream still works.
	_, err = js.Publish("unique_ids.verify", []byte("after-corrupt-recovery"))
	require_NoError(t, err)

	si, _ = js.StreamInfo(streamName)
	t.Logf("Final state: Msgs=%d, Deleted=%d, Leader=%s",
		si.State.Msgs, si.State.NumDeleted, si.Cluster.Leader)

	// Check that the removed server is responsive (not deadlocked).
	nc2, err := nats.Connect(target.ClientURL())
	if err != nil {
		t.Logf("Note: Removed peer %s not connectable: %v", targetName, err)
	} else {
		nc2.Close()
		t.Logf("Removed peer %s is responsive (not deadlocked)", targetName)
	}
}

// TestJetStreamClusterStreamDeleteDeadlockOnPermissionError tests the deadlock
// that occurs when newFileStoreWithCreated encounters a permission error during
// expireMsgsOnRecover() and returns without releasing fs.mu.Lock().
//
// The exact deadlock path (filestore.go):
//
//	defer func() {
//	    go fs.cleanupOldMeta()  // Line 552: will try RLock after return
//	}()
//	fs.mu.Lock()  // Line 557
//	...
//	if fs.cfg.MaxAge != 0 {
//	    err := fs.expireMsgsOnRecover()
//	    if isPermissionError(err) {
//	        return nil, err  // Line 577: LOCK LEAKED!
//	    }
//	}
//	...
//	fs.mu.Unlock()  // Line 589: never reached
//
// This test uses MaxAge > 0 to trigger expireMsgsOnRecover(), and makes
// the msgs directory read-only to cause permission errors during file
// operations (block close/remove), reproducing the early return at line 577.
//
// When combined with a peer-remove, the stream.stop() → store.Delete()
// path also tries to acquire fs.mu.Lock(), adding another blocked goroutine.
func TestJetStreamClusterStreamDeleteDeadlockOnPermissionError(t *testing.T) {
	// Skip if running as root since root bypasses file permissions.
	if os.Getuid() == 0 {
		t.Skip("Skipping: running as root bypasses file permission checks")
	}

	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	streamName := "MAXAGE_TEST"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"maxage.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
		MaxAge:    1 * time.Hour, // MaxAge > 0 triggers expireMsgsOnRecover
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Publish messages.
	numMessages := 2000
	t.Logf("Publishing %d messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		_, err := js.Publish(fmt.Sprintf("maxage.item.%d", i%20), []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Publish error: %v", err)
		}
	}

	si, _ := js.StreamInfo(streamName)
	t.Logf("State: Msgs=%d, FirstSeq=%d, LastSeq=%d",
		si.State.Msgs, si.State.FirstSeq, si.State.LastSeq)

	// Pick target and get its store directory.
	target := c.randomNonStreamLeader(globalAccountName, streamName)
	require_True(t, target != nil)
	targetName := target.Name()

	acc, err := target.lookupAccount(globalAccountName)
	require_NoError(t, err)
	mset, err := acc.lookupStream(streamName)
	require_NoError(t, err)
	fs := mset.store.(*fileStore)
	storeMsgDir := filepath.Join(fs.fcfg.StoreDir, msgDir)
	t.Logf("Target %s store msgs dir: %s", targetName, storeMsgDir)

	// Shut down target.
	t.Logf("Shutting down %s...", targetName)
	target.Shutdown()
	target.WaitForShutdown()

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Make the msgs directory read-only to cause permission errors during recovery.
	// expireMsgsOnRecover() calls dirtyCloseWithRemove() which tries to delete files.
	// On a read-only directory, this fails with EPERM, triggering isPermissionError()
	// at line 576, causing the early return at line 577 WITHOUT fs.mu.Unlock().
	if err := os.Chmod(storeMsgDir, 0555); err != nil {
		t.Fatalf("Failed to chmod: %v", err)
	}
	// Restore permissions on cleanup so temp dir can be removed.
	defer os.Chmod(storeMsgDir, 0755)

	// Also remove the index to force full recovery.
	indexPath := filepath.Join(storeMsgDir, "index.db")
	// Need to temporarily restore write permission to delete the index.
	os.Chmod(storeMsgDir, 0755)
	os.Remove(indexPath)
	os.Chmod(storeMsgDir, 0555)
	t.Log("Made msgs directory read-only and removed index to force recovery with permission errors")

	// Reconnect.
	nc.Close()
	nc, js = jsClientConnect(t, c.leader())
	defer nc.Close()

	// Restart the server. Recovery will hit permission errors.
	t.Logf("Restarting %s (should trigger permission error during recovery)...", targetName)

	// Use a goroutine with timeout since recovery might deadlock.
	restartDone := make(chan struct{})
	go func() {
		target = c.restartServer(target)
		close(restartDone)
	}()

	select {
	case <-restartDone:
		t.Log("Server restarted")
	case <-time.After(30 * time.Second):
		// Restore permissions so cleanup works.
		os.Chmod(storeMsgDir, 0755)
		t.Fatal("TIMEOUT: Server restart hung - possible deadlock during recovery!")
	}

	// Give the server a moment to process RAFT entries.
	time.Sleep(2 * time.Second)

	// Issue peer-remove. If the filestore lock is leaked, the stream
	// stop/delete will try to acquire the lock and hang.
	t.Logf("Issuing peer-remove for %s...", targetName)
	removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
	resp, err := nc.Request(removeSub, []byte(`{"peer":"`+targetName+`"}`), 10*time.Second)
	if err != nil {
		t.Logf("Peer remove request error: %v (may indicate deadlock on target)", err)
	} else {
		var rpResp JSApiStreamRemovePeerResponse
		json.Unmarshal(resp.Data, &rpResp)
		t.Logf("Peer remove response: Success=%v, Error=%+v", rpResp.Success, rpResp.Error)
	}

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Verify peer was removed (would hang if deadlocked).
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == targetName {
				return fmt.Errorf("peer %s still present", targetName)
			}
		}
		return nil
	})

	t.Logf("Peer %s removed", targetName)

	// Verify stream is functional.
	_, err = js.Publish("maxage.verify", []byte("after-perm-error"))
	require_NoError(t, err)

	si, _ = js.StreamInfo(streamName)
	t.Logf("Final state: Msgs=%d, Leader=%s", si.State.Msgs, si.Cluster.Leader)

	// Restore permissions before checking target server.
	os.Chmod(storeMsgDir, 0755)

	// Check target is responsive.
	nc2, err := nats.Connect(target.ClientURL())
	if err != nil {
		t.Logf("Note: Target %s not connectable: %v", targetName, err)
	} else {
		nc2.Close()
		t.Logf("Target %s is responsive (not deadlocked)", targetName)
	}
}

// TestJetStreamClusterStreamDeleteConsumerFolderDuringRecovery tests the scenario
// where a consumer's store folder is removed DURING server recovery while the
// stream is processing tombstones. This matches the production deadlock scenario:
//
// During newFileStoreWithCreated (filestore.go):
//
//	defer func() { go fs.cleanupOldMeta() }()  // Line 552: tries RLock later
//	fs.mu.Lock()                                // Line 557: acquired without defer
//	if len(fs.tombs) > 0 {                      // Line 560: tombstone processing
//	    for _, seq := range fs.tombs {
//	        fs.removeMsg(seq, false, true, false) // I/O on message blocks
//	    }
//	}
//	fs.mu.Unlock()                              // Line 589: not reached on panic
//
// By removing the consumer's obs/ folder during recovery, we force errors in
// the consumer's filestore initialization while the stream is still processing
// tombstones under its lock. If the consumer recovery failure cascades or the
// concurrent file removal triggers a panic in any recovery path, the lock leak
// at line 557 causes cleanupOldMeta to deadlock on RLock.
func TestJetStreamClusterStreamDeleteConsumerFolderDuringRecovery(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	streamName := "unique_ids"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"unique_ids.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Create consumer.
	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   "worker",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, streamName, "worker")

	// Publish messages.
	numMessages := 5000
	t.Logf("Publishing %d messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		subject := fmt.Sprintf("unique_ids.item.%d", i%50)
		_, err := js.Publish(subject, []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Publish error at msg %d: %v", i, err)
		}
	}

	// Consume and ack most messages to create tombstones.
	sub, err := js.PullSubscribe("unique_ids.>", "worker")
	require_NoError(t, err)

	ackedCount := 0
	toAck := numMessages - 100
	for ackedCount < toAck {
		batch := toAck - ackedCount
		if batch > 100 {
			batch = 100
		}
		msgs, err := sub.Fetch(batch, nats.MaxWait(5*time.Second))
		if err != nil {
			t.Logf("Fetch error after %d acks: %v", ackedCount, err)
			break
		}
		for _, m := range msgs {
			m.Ack()
			ackedCount++
		}
	}
	t.Logf("Acked %d messages (creating tombstones)", ackedCount)

	// Wait for ack processing.
	checkFor(t, 10*time.Second, 500*time.Millisecond, func() error {
		si, err := js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		if si.State.Msgs > uint64(numMessages-ackedCount+100) {
			return fmt.Errorf("waiting for acks: %d msgs remaining", si.State.Msgs)
		}
		return nil
	})

	si, _ := js.StreamInfo(streamName)
	t.Logf("State after acks: Msgs=%d, Deleted=%d", si.State.Msgs, si.State.NumDeleted)

	// Pick target replica and capture its store directories.
	target := c.randomNonStreamLeader(globalAccountName, streamName)
	require_True(t, target != nil)
	targetName := target.Name()

	acc, err := target.lookupAccount(globalAccountName)
	require_NoError(t, err)
	mset, err := acc.lookupStream(streamName)
	require_NoError(t, err)
	fs := mset.store.(*fileStore)
	storeDir := fs.fcfg.StoreDir
	consumerObsDir := filepath.Join(storeDir, consumerDir)
	storeMsgDir := filepath.Join(storeDir, msgDir)
	t.Logf("Target %s store dir: %s", targetName, storeDir)
	t.Logf("Consumer obs dir: %s", consumerObsDir)
	t.Logf("Msgs dir: %s", storeMsgDir)

	// Shut down the target.
	t.Logf("Shutting down %s...", targetName)
	target.Shutdown()
	target.WaitForShutdown()

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Reconnect.
	nc.Close()
	nc, js = jsClientConnect(t, c.leader())
	defer nc.Close()

	// Start a goroutine that aggressively removes the consumer's obs/ folder
	// during server restart. This races with the recovery process:
	// - The server recreates the obs/ directory during startup
	// - We immediately remove it, causing the consumer recovery to fail
	// - This happens while the stream is processing tombstones under fs.mu.Lock()
	stopRemoval := make(chan struct{})
	removalDone := make(chan struct{})
	removalCount := 0

	go func() {
		defer close(removalDone)
		for {
			select {
			case <-stopRemoval:
				return
			default:
			}
			// Try to remove the consumer obs directory.
			// During startup, the server creates this directory then recovers consumers.
			// By removing it mid-recovery, we force consumer initialization to fail.
			if _, err := os.Stat(consumerObsDir); err == nil {
				if err := os.RemoveAll(consumerObsDir); err == nil {
					removalCount++
				}
			}
			// Also try removing individual msg block files to disrupt
			// tombstone processing in the stream's removeMsg() calls.
			if entries, err := os.ReadDir(storeMsgDir); err == nil {
				for _, e := range entries {
					if filepath.Ext(e.Name()) == ".blk" {
						os.Remove(filepath.Join(storeMsgDir, e.Name()))
					}
				}
			}
			// Tight loop to maximize chance of hitting the race window.
			time.Sleep(time.Millisecond)
		}
	}()

	// Restart the server. This triggers newFileStoreWithCreated which:
	// 1. Processes tombstones under fs.mu.Lock() (stream store)
	// 2. Recovers consumer stores (also calls newFileStoreWithCreated)
	// Our goroutine races to remove files during both of these operations.
	t.Logf("Restarting %s while consumer folder removal goroutine is active...", targetName)

	restartDone := make(chan struct{})
	go func() {
		target = c.restartServer(target)
		close(restartDone)
	}()

	// Wait for restart with a timeout to detect deadlock.
	select {
	case <-restartDone:
		t.Logf("Server %s restarted successfully", targetName)
	case <-time.After(30 * time.Second):
		close(stopRemoval)
		<-removalDone
		t.Fatalf("TIMEOUT: Server restart hung (possible deadlock during recovery with consumer folder removal)")
	}

	// Stop the removal goroutine.
	close(stopRemoval)
	<-removalDone
	t.Logf("Consumer folder removal goroutine stopped (removed obs dir %d times)", removalCount)

	// Give recovery time to settle.
	time.Sleep(2 * time.Second)

	// Issue peer-remove. If the stream's fs.mu is leaked, this will hang
	// because stream.stop() -> store.Delete() needs fs.mu.Lock().
	t.Logf("Issuing peer-remove for %s...", targetName)
	peerRemoveDone := make(chan struct{})
	go func() {
		defer close(peerRemoveDone)
		removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
		resp, err := nc.Request(removeSub, []byte(`{"peer":"`+targetName+`"}`), 10*time.Second)
		if err != nil {
			t.Logf("Peer remove request error: %v", err)
			return
		}
		var rpResp JSApiStreamRemovePeerResponse
		json.Unmarshal(resp.Data, &rpResp)
		t.Logf("Peer remove response: Success=%v, Error=%+v", rpResp.Success, rpResp.Error)
	}()

	select {
	case <-peerRemoveDone:
		t.Log("Peer remove completed")
	case <-time.After(30 * time.Second):
		t.Fatal("TIMEOUT: Peer remove hung - possible deadlock (fs.mu leaked during recovery)")
	}

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Verify peer was removed.
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == targetName {
				return fmt.Errorf("peer %s still present", targetName)
			}
		}
		return nil
	})

	t.Logf("Peer %s removed", targetName)

	// Verify stream is functional.
	_, err = js.Publish("unique_ids.verify", []byte("after-consumer-folder-removal"))
	require_NoError(t, err)

	si, _ = js.StreamInfo(streamName)
	t.Logf("Final state: Msgs=%d, Deleted=%d, Leader=%s",
		si.State.Msgs, si.State.NumDeleted, si.Cluster.Leader)

	// Check target is responsive (not deadlocked).
	nc2, err := nats.Connect(target.ClientURL())
	if err != nil {
		t.Logf("Note: Target %s not connectable: %v", targetName, err)
	} else {
		nc2.Close()
		t.Logf("Target %s is responsive (not deadlocked)", targetName)
	}
}

// TestJetStreamClusterStreamDeleteConsumerFolderRepeatedly tests repeated
// removal of the consumer folder across multiple server restart cycles.
// This increases the probability of hitting the race window where file
// removal occurs exactly during tombstone processing or consumer recovery.
func TestJetStreamClusterStreamDeleteConsumerFolderRepeatedly(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	streamName := "repeat_wq"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"repeat.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Create consumer.
	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   "worker",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, streamName, "worker")

	// Publish and ack messages to create tombstones.
	numMessages := 3000
	t.Logf("Publishing %d messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		_, err := js.Publish(fmt.Sprintf("repeat.item.%d", i%30), []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Publish error at msg %d: %v", i, err)
		}
	}

	sub, err := js.PullSubscribe("repeat.>", "worker")
	require_NoError(t, err)

	ackedCount := 0
	toAck := numMessages - 50
	for ackedCount < toAck {
		batch := toAck - ackedCount
		if batch > 100 {
			batch = 100
		}
		msgs, err := sub.Fetch(batch, nats.MaxWait(5*time.Second))
		if err != nil {
			break
		}
		for _, m := range msgs {
			m.Ack()
			ackedCount++
		}
	}
	t.Logf("Acked %d messages", ackedCount)

	// Wait for deletions.
	checkFor(t, 10*time.Second, 500*time.Millisecond, func() error {
		si, err := js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		if si.State.Msgs > uint64(numMessages-ackedCount+100) {
			return fmt.Errorf("waiting: %d msgs", si.State.Msgs)
		}
		return nil
	})

	si, _ := js.StreamInfo(streamName)
	t.Logf("State: Msgs=%d, Deleted=%d", si.State.Msgs, si.State.NumDeleted)

	// Pick target.
	target := c.randomNonStreamLeader(globalAccountName, streamName)
	require_True(t, target != nil)
	targetName := target.Name()

	// Get store directory before shutdown.
	acc, err := target.lookupAccount(globalAccountName)
	require_NoError(t, err)
	mset, err := acc.lookupStream(streamName)
	require_NoError(t, err)
	fs := mset.store.(*fileStore)
	storeDir := fs.fcfg.StoreDir
	consumerObsDir := filepath.Join(storeDir, consumerDir)
	storeMsgDir := filepath.Join(storeDir, msgDir)
	t.Logf("Target %s: obs=%s, msgs=%s", targetName, consumerObsDir, storeMsgDir)

	// Run multiple restart cycles, each time removing consumer folder during recovery.
	const restartCycles = 3
	for cycle := 0; cycle < restartCycles; cycle++ {
		t.Logf("=== Restart cycle %d/%d ===", cycle+1, restartCycles)

		// Shut down target.
		target.Shutdown()
		target.WaitForShutdown()

		c.waitOnStreamLeader(globalAccountName, streamName)

		// Reconnect.
		nc.Close()
		nc, js = jsClientConnect(t, c.leader())

		// Publish more messages while target is down to create more tombstones
		// on recovery.
		for i := 0; i < 500; i++ {
			js.Publish(fmt.Sprintf("repeat.new.%d.%d", cycle, i), []byte("new"))
		}

		// Start aggressive folder removal goroutine.
		stopRemoval := make(chan struct{})
		removalDone := make(chan struct{})

		go func() {
			defer close(removalDone)
			for {
				select {
				case <-stopRemoval:
					return
				default:
				}
				// Remove consumer folder.
				if _, err := os.Stat(consumerObsDir); err == nil {
					os.RemoveAll(consumerObsDir)
				}
				// Remove msg block files.
				if entries, err := os.ReadDir(storeMsgDir); err == nil {
					for _, e := range entries {
						if filepath.Ext(e.Name()) == ".blk" {
							os.Remove(filepath.Join(storeMsgDir, e.Name()))
						}
					}
				}
				time.Sleep(500 * time.Microsecond) // Very tight loop
			}
		}()

		// Restart with timeout.
		restartDone := make(chan struct{})
		go func() {
			target = c.restartServer(target)
			close(restartDone)
		}()

		select {
		case <-restartDone:
			t.Logf("Cycle %d: server restarted", cycle+1)
		case <-time.After(30 * time.Second):
			close(stopRemoval)
			<-removalDone
			t.Fatalf("Cycle %d: TIMEOUT - server restart hung (possible deadlock)", cycle+1)
		}

		close(stopRemoval)
		<-removalDone

		// Let recovery settle.
		time.Sleep(2 * time.Second)

		// Verify stream is still accessible.
		checkFor(t, 15*time.Second, 500*time.Millisecond, func() error {
			_, err := js.StreamInfo(streamName)
			return err
		})
	}

	// After all cycles, issue peer-remove to test for leaked locks.
	t.Logf("Issuing peer-remove for %s after %d restart cycles...", targetName, restartCycles)
	peerRemoveDone := make(chan struct{})
	go func() {
		defer close(peerRemoveDone)
		removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
		resp, err := nc.Request(removeSub, []byte(`{"peer":"`+targetName+`"}`), 10*time.Second)
		if err != nil {
			t.Logf("Peer remove error: %v", err)
			return
		}
		var rpResp JSApiStreamRemovePeerResponse
		json.Unmarshal(resp.Data, &rpResp)
		t.Logf("Peer remove: Success=%v", rpResp.Success)
	}()

	select {
	case <-peerRemoveDone:
		t.Log("Peer remove completed")
	case <-time.After(30 * time.Second):
		t.Fatal("TIMEOUT: Peer remove hung after repeated restarts - possible deadlock")
	}

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Verify stream health.
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == targetName {
				return fmt.Errorf("peer %s still present", targetName)
			}
		}
		return nil
	})

	_, err = js.Publish("repeat.verify", []byte("done"))
	require_NoError(t, err)

	si, _ = js.StreamInfo(streamName)
	t.Logf("Final state: Msgs=%d, Deleted=%d, Leader=%s",
		si.State.Msgs, si.State.NumDeleted, si.Cluster.Leader)

	// Check server is responsive.
	nc2, err := nats.Connect(target.ClientURL())
	if err != nil {
		t.Logf("Note: Target %s not connectable: %v", targetName, err)
	} else {
		nc2.Close()
		t.Logf("Target %s responsive (not deadlocked)", targetName)
	}
}

// TestJetStreamClusterPeerRemoveWithConsumersDuringRecovery tests the deadlock
// scenario where a peer with multiple consumers is removed while it is still
// recovering from a restart and processing tombstones.
//
// When a stream peer-remove is issued:
//  1. removePeerFromStreamLocked() proposes new stream assignment AND
//     consumer reassignment/deletion for ALL consumers (jetstream_cluster.go:2048-2064)
//  2. On the removed peer, stream.stop() is called (stream.go:7053)
//  3. stream.stop() first stops ALL consumers (stream.go:7123-7134):
//     each consumer.stopWithFlags() acquires mset.mu.Lock(), then calls
//     consumer store.Delete() which acquires its own fs.mu.Lock()
//  4. Then stream.stop() calls store.Delete(false) (stream.go:7214) which
//     acquires the STREAM's fs.mu.Lock() (filestore.go:10338)
//
// If the stream's fileStore is still in newFileStoreWithCreated processing
// tombstones under fs.mu.Lock() (filestore.go:557), then store.Delete()
// at step 4 would block. Multiple consumers being cleaned up simultaneously
// creates maximum lock contention across stream and consumer mutexes.
//
// This test uses multiple durable consumers to maximize the interaction
// between consumer cleanup and stream recovery.
func TestJetStreamClusterPeerRemoveWithConsumersDuringRecovery(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ5", 5)
	defer c.shutdown()

	c.waitOnPeerCount(5)

	nc, js := jsClientConnect(t, c.leader())
	defer nc.Close()

	streamName := "unique_ids"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"unique_ids.>"},
		Replicas:  3,
		Retention: nats.WorkQueuePolicy,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Create multiple durable consumers. When the peer is removed, each
	// consumer gets reassigned (durable) or deleted (ephemeral), creating
	// multiple concurrent lock acquisition paths.
	consumerNames := []string{"worker-1", "worker-2", "worker-3", "worker-4"}
	for _, cName := range consumerNames {
		_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
			Durable:       cName,
			AckPolicy:     nats.AckExplicitPolicy,
			FilterSubject: fmt.Sprintf("unique_ids.%s.>", cName),
		})
		require_NoError(t, err)
		c.waitOnConsumerLeader(globalAccountName, streamName, cName)
	}
	t.Logf("Created %d consumers", len(consumerNames))

	// Publish messages across all consumer filter subjects.
	numMessages := 5000
	t.Logf("Publishing %d messages...", numMessages)
	for i := 0; i < numMessages; i++ {
		cIdx := i % len(consumerNames)
		subject := fmt.Sprintf("unique_ids.%s.item.%d", consumerNames[cIdx], i)
		_, err := js.Publish(subject, []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Publish error at msg %d: %v", i, err)
		}
	}

	// Consume and ack from each consumer to create tombstones.
	totalAcked := 0
	for _, cName := range consumerNames {
		sub, err := js.PullSubscribe(
			fmt.Sprintf("unique_ids.%s.>", cName),
			cName,
		)
		require_NoError(t, err)

		ackedCount := 0
		for ackedCount < (numMessages/len(consumerNames))-25 {
			batch := (numMessages / len(consumerNames)) - 25 - ackedCount
			if batch > 100 {
				batch = 100
			}
			msgs, err := sub.Fetch(batch, nats.MaxWait(5*time.Second))
			if err != nil {
				t.Logf("Fetch error for %s after %d acks: %v", cName, ackedCount, err)
				break
			}
			for _, m := range msgs {
				m.Ack()
				ackedCount++
			}
		}
		totalAcked += ackedCount
		t.Logf("Consumer %s: acked %d messages", cName, ackedCount)
	}
	t.Logf("Total acked: %d messages (creating tombstones)", totalAcked)

	// Wait for ack processing.
	checkFor(t, 15*time.Second, 500*time.Millisecond, func() error {
		si, err := js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		remaining := numMessages - totalAcked + 200
		if si.State.Msgs > uint64(remaining) {
			return fmt.Errorf("waiting for acks: %d msgs remaining", si.State.Msgs)
		}
		return nil
	})

	si, _ := js.StreamInfo(streamName)
	t.Logf("State after acks: Msgs=%d, Deleted=%d", si.State.Msgs, si.State.NumDeleted)

	// Pick a non-leader replica that hosts the stream.
	target := c.randomNonStreamLeader(globalAccountName, streamName)
	require_True(t, target != nil)
	targetName := target.Name()
	t.Logf("Target for shutdown + peer-remove: %s", targetName)

	// Shut down the target.
	t.Logf("Shutting down %s...", targetName)
	target.Shutdown()
	target.WaitForShutdown()

	c.waitOnStreamLeader(globalAccountName, streamName)

	// Reconnect.
	nc.Close()
	nc, js = jsClientConnect(t, c.leader())
	defer nc.Close()

	// Publish more messages while target is down so it has more to recover.
	t.Log("Publishing 1000 more messages while target is down...")
	for i := 0; i < 1000; i++ {
		cIdx := i % len(consumerNames)
		js.Publish(fmt.Sprintf("unique_ids.%s.new.%d", consumerNames[cIdx], i), []byte("new"))
	}

	// Restart the server. This triggers newFileStoreWithCreated for the
	// stream store (tombstone processing under fs.mu.Lock()) and for each
	// consumer store.
	t.Logf("Restarting %s...", targetName)
	restartDone := make(chan struct{})
	go func() {
		target = c.restartServer(target)
		close(restartDone)
	}()

	// Wait for restart to complete.
	select {
	case <-restartDone:
		t.Logf("Server %s restarted", targetName)
	case <-time.After(30 * time.Second):
		t.Fatal("TIMEOUT: Server restart hung - possible deadlock during recovery")
	}

	// Immediately issue peer-remove. This triggers stream.stop() on the
	// target which:
	// 1. Stops all 4 consumers (each acquiring mset.mu + consumer store locks)
	// 2. Then calls stream store.Delete() (acquires stream's fs.mu.Lock())
	// If stream recovery is still processing tombstones under fs.mu.Lock(),
	// the store.Delete() at step 2 will block.
	t.Logf("Issuing peer-remove for %s with %d consumers...", targetName, len(consumerNames))

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	// Goroutine 1: Issue stream-level peer-remove.
	wg.Add(1)
	go func() {
		defer wg.Done()
		removeSub := fmt.Sprintf(JSApiStreamRemovePeerT, streamName)
		resp, err := nc.Request(removeSub, []byte(`{"peer":"`+targetName+`"}`), 15*time.Second)
		if err != nil {
			errCh <- fmt.Errorf("peer-remove request error: %v", err)
			return
		}
		var rpResp JSApiStreamRemovePeerResponse
		json.Unmarshal(resp.Data, &rpResp)
		t.Logf("Stream peer-remove: Success=%v, Error=%+v", rpResp.Success, rpResp.Error)
	}()

	// Goroutine 2: Simultaneously delete individual consumers to create
	// more concurrent deletion pressure on the recovering peer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Small delay to let peer-remove start first.
		time.Sleep(100 * time.Millisecond)
		for _, cName := range consumerNames {
			err := js.DeleteConsumer(streamName, cName)
			if err != nil {
				t.Logf("Delete consumer %s: %v (may be expected during peer-remove)", cName, err)
			} else {
				t.Logf("Deleted consumer %s", cName)
			}
		}
	}()

	// Goroutine 3: Keep publishing to create ongoing I/O on the stream.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, err := js.Publish(fmt.Sprintf("unique_ids.worker-1.stress.%d", i), []byte("stress"))
			if err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Wait for all concurrent operations with a deadlock timeout.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("All concurrent operations completed")
	case <-time.After(60 * time.Second):
		t.Fatal("TIMEOUT: Concurrent operations deadlocked! " +
			"Likely fs.mu leaked during recovery while consumer cleanup was running")
	}

	close(errCh)
	for err := range errCh {
		t.Logf("Error: %v", err)
	}

	// Verify stream leader is available.
	c.waitOnStreamLeader(globalAccountName, streamName)

	// Verify peer was removed.
	checkFor(t, 30*time.Second, 500*time.Millisecond, func() error {
		si, err = js.StreamInfo(streamName)
		if err != nil {
			return err
		}
		for _, r := range si.Cluster.Replicas {
			if r.Name == targetName {
				return fmt.Errorf("peer %s still present", targetName)
			}
		}
		return nil
	})
	t.Logf("Peer %s removed", targetName)

	// Verify stream is functional.
	_, err = js.Publish("unique_ids.worker-1.verify", []byte("after-peer-remove-with-consumers"))
	require_NoError(t, err)

	si, _ = js.StreamInfo(streamName)
	t.Logf("Final state: Msgs=%d, Deleted=%d, Leader=%s, Replicas=%d",
		si.State.Msgs, si.State.NumDeleted, si.Cluster.Leader, len(si.Cluster.Replicas))

	// Verify the removed server is responsive (not deadlocked).
	nc2, err := nats.Connect(target.ClientURL())
	if err != nil {
		t.Logf("Note: Target %s not connectable: %v (expected if fully removed)", targetName, err)
	} else {
		nc2.Close()
		t.Logf("Target %s is responsive (not deadlocked)", targetName)
	}
}
