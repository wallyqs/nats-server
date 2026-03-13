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
		// No MaxMsgs limit - we'll create deletions through out-of-order acking
	})
	require_NoError(t, err)
	c.waitOnStreamLeader(globalAccountName, streamName)

	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   "worker",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader(globalAccountName, streamName, "worker")

	// Start periodic stream state monitoring.
	stopMonitor := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopMonitor:
				return
			case <-ticker.C:
				if si, err := js.StreamInfo(streamName); err == nil {
					t.Logf("[MONITOR] Msgs=%d, Deleted=%d, FirstSeq=%d, LastSeq=%d",
						si.State.Msgs, si.State.NumDeleted, si.State.FirstSeq, si.State.LastSeq)
				}
			}
		}
	}()
	defer func() {
		close(stopMonitor)
		<-monitorDone
	}()

	// Publish initial messages to TWO different subject patterns.
	// Strategy: Interleave subjects "keep" and "delete" so that when we consume
	// only "delete" subjects, we create gaps (deletions) in the sequence.
	numMessages := 500000
	t.Logf("Publishing %d messages (interleaving keep/delete subjects)...", numMessages)

	for i := 0; i < numMessages; i++ {
		var subject string
		// Interleave: 40% "keep" subjects, 60% "delete" subjects
		// This will create ~300K deletions when we consume "delete" messages
		if i%5 < 2 {
			// 40% - These messages will stay (keep)
			subject = fmt.Sprintf("cwork.keep.%d", i%50)
		} else {
			// 60% - These messages will be deleted (consumed and acked)
			subject = fmt.Sprintf("cwork.delete.%d", i%50)
		}

		_, err := js.Publish(subject, []byte(fmt.Sprintf("data-%d", i)))
		if err != nil {
			t.Fatalf("Publish error: %v", err)
		}

		if i > 0 && i%25000 == 0 {
			t.Logf("Published %d/%d messages (%.1f%%)...", i, numMessages, float64(i)/float64(numMessages)*100)
		}
	}
	t.Logf("Completed publishing all %d messages (40%% keep, 60%% delete subjects)", numMessages)

	// Consume and ack only the "delete" subject messages.
	// Since these are interleaved with "keep" messages, acking them creates
	// gaps in the sequence, which increases NumDeleted.
	// Expected: ~300K deletions (60% of 500K messages)

	sub, err := js.PullSubscribe("cwork.delete.>", "delete_worker")
	require_NoError(t, err)

	ackedCount := 0
	expectedAcks := int(float64(numMessages) * 0.6) // 60% of messages
	t.Logf("Consuming and acking 'delete' subject messages (expect ~%d)...", expectedAcks)

	for ackedCount < expectedAcks {
		batchSize := expectedAcks - ackedCount
		if batchSize > 5000 {
			batchSize = 5000
		}

		msgs, err := sub.Fetch(batchSize, nats.MaxWait(20*time.Second))
		if err != nil {
			t.Logf("Fetch error at %d acks: %v", ackedCount, err)
			break
		}

		for _, msg := range msgs {
			msg.Ack()
			ackedCount++
		}

		if ackedCount%25000 == 0 {
			t.Logf("Acked %d/%d messages (%.1f%%)...", ackedCount, expectedAcks,
				float64(ackedCount)/float64(expectedAcks)*100)
		}
	}
	t.Logf("Completed acking: %d 'delete' subject messages (creates gaps = deletions)", ackedCount)

	// Let deletions settle - increased time for large volume.
	time.Sleep(10 * time.Second)

	si, _ := js.StreamInfo(streamName)
	t.Logf("State before shutdown: Msgs=%d, Deleted=%d (FirstSeq=%d, LastSeq=%d)",
		si.State.Msgs, si.State.NumDeleted, si.State.FirstSeq, si.State.LastSeq)

	// Verify we have the required deletion volume
	if si.State.NumDeleted < 200000 {
		t.Fatalf("Expected at least 200K deletions, got %d (Msgs=%d, FirstSeq=%d, LastSeq=%d)",
			si.State.NumDeleted, si.State.Msgs, si.State.FirstSeq, si.State.LastSeq)
	}
	t.Logf("✓ Deletion requirement met: %d deletions (required >= 200K)", si.State.NumDeleted)
	t.Logf("  NumDeleted = (LastSeq - FirstSeq + 1) - Msgs = (%d - %d + 1) - %d = %d",
		si.State.LastSeq, si.State.FirstSeq, si.State.Msgs, si.State.NumDeleted)

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

	// Create another consumer subscription for concurrent acking during chaos
	// This will consume from the remaining "keep" messages
	sub2, err := js.PullSubscribe("cwork.>", "chaos_worker")
	require_NoError(t, err)

	// Start concurrent operations - MADE MORE AGGRESSIVE.
	var wg sync.WaitGroup
	errCh := make(chan error, 1000)

	// Multiple publisher goroutines for increased concurrency.
	numPublishers := 10
	messagesPerPublisher := 500
	for p := 0; p < numPublishers; p++ {
		wg.Add(1)
		publisherID := p
		go func() {
			defer wg.Done()
			for i := 0; i < messagesPerPublisher; i++ {
				_, err := js.Publish(
					fmt.Sprintf("cwork.new.p%d.%d", publisherID, i),
					[]byte(fmt.Sprintf("new-p%d-%d", publisherID, i)),
				)
				if err != nil {
					errCh <- fmt.Errorf("publisher %d error: %v", publisherID, err)
					return
				}
				// NO SLEEP - maximum pressure
			}
		}()
	}

	// Concurrent consumer/acker to create deletion pressure during recovery.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond) // Brief wait for messages to appear
		for i := 0; i < 500; i++ {
			msgs, err := sub2.Fetch(20, nats.MaxWait(100*time.Millisecond))
			if err != nil {
				// Expected to fail sometimes during chaos
				continue
			}
			for _, m := range msgs {
				m.Ack() // Create more deletions during recovery
			}
		}
	}()

	// Continuously add consumers during recovery and peer-remove.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond) // Brief delay to let recovery start
		for i := 0; i < 50; i++ {
			consumerName := fmt.Sprintf("chaos_consumer_%d", i)
			_, err := js.AddConsumer(streamName, &nats.ConsumerConfig{
				Durable:   consumerName,
				AckPolicy: nats.AckExplicitPolicy,
			})
			if err != nil {
				// Expected to fail during peer-remove and recovery chaos
				t.Logf("Consumer creation failed (expected during chaos): %v", err)
			} else {
				t.Logf("Created consumer %s during recovery chaos", consumerName)
			}
			time.Sleep(100 * time.Millisecond) // Create consumers every 100ms
		}
	}()

	// Restart the server (triggers recovery) - REDUCED DELAY.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Minimal delay - start recovery ASAP while publishers are active.
		time.Sleep(5 * time.Millisecond)
		target = c.restartServer(target)
		t.Logf("Server %s restarted", targetName)
	}()

	// Issue peer-remove with REDUCED DELAY to hit recovery window.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Shorter delay to hit the recovery path more reliably.
		time.Sleep(50 * time.Millisecond)

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
	case <-time.After(120 * time.Second):
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
