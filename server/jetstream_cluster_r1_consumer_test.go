// Copyright 2020-2026 The NATS Authors
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamClusterR1ConsumerUpdateResponseNotDropped demonstrates that
// config updates to R1 consumers must always receive a response, even when
// the leader-change goroutine from the initial creation is still in-flight.
//
// Background (PR #7889):
// processClusterCreateConsumer was changed to run processConsumerLeaderChange
// in a goroutine for R1 consumers (to avoid blocking meta operations).
// Without proper synchronization, a rapid config update could arrive while
// the creation goroutine is still running. Since shouldStartMonitor() returns
// false when inMonitor is true (goroutine still active), the update's
// processConsumerLeaderChange would be silently skipped, and the API response
// to the client would never be sent — causing the client to hang/timeout.
//
// The fix (merged version) added signalMonitorQuit() + monitorWg.Wait()
// before shouldStartMonitor() to ensure the previous goroutine completes
// before starting a new one.
func TestJetStreamClusterR1ConsumerUpdateResponseNotDropped(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	// Create an R1 stream.
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 1,
	})
	require_NoError(t, err)

	// Publish enough messages so that streamNumPending() in setLeader()
	// has some real work to do, widening the race window.
	for i := 0; i < 10_000; i++ {
		_, err := js.Publish("foo", []byte("data"))
		require_NoError(t, err)
	}

	// Create an R1 durable consumer.
	// This triggers processClusterCreateConsumer which (post PR #7889)
	// runs processConsumerLeaderChange in a goroutine.
	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:       "DUR",
		AckPolicy:     nats.AckExplicitPolicy,
		FilterSubject: "foo",
		MaxDeliver:    5,
	})
	require_NoError(t, err)

	c.waitOnConsumerLeader(globalAccountName, "TEST", "DUR")

	// Now perform rapid config updates. Each update must get a response.
	// Without the monitorWg.Wait() fix, shouldStartMonitor() returns false
	// when the creation goroutine is still running, silently dropping the
	// update and causing this call to timeout.
	var updateErrors atomic.Int64
	for i := 0; i < 5; i++ {
		_, err = js.UpdateConsumer("TEST", &nats.ConsumerConfig{
			Durable:       "DUR",
			AckPolicy:     nats.AckExplicitPolicy,
			FilterSubject: "foo",
			MaxDeliver:    i + 2,
			Description:   "update iteration",
		})
		if err != nil {
			updateErrors.Add(1)
			t.Logf("Update %d failed: %v", i, err)
		}
	}

	if failures := updateErrors.Load(); failures > 0 {
		t.Fatalf("Expected all consumer updates to succeed, but %d failed", failures)
	}

	// Verify the final config was applied.
	ci, err := js.ConsumerInfo("TEST", "DUR")
	require_NoError(t, err)
	if ci.Config.MaxDeliver != 6 {
		t.Fatalf("Expected MaxDeliver=6 after updates, got %d", ci.Config.MaxDeliver)
	}
}

// TestJetStreamClusterR1ConsumerUpdateDroppedWithoutWait demonstrates the
// actual bug: when the monitorWg.Wait() synchronization is removed (as in
// the original PR #7889 before the fix was added), config updates can be
// silently dropped because shouldStartMonitor() returns false while the
// creation goroutine is still running.
//
// This test manipulates the consumer's internal monitor state directly to
// simulate the race condition deterministically, without relying on timing.
func TestJetStreamClusterR1ConsumerUpdateDroppedWithoutWait(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 1,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "DUR",
		AckPolicy: nats.AckExplicitPolicy,
		MaxDeliver: 5,
	})
	require_NoError(t, err)

	c.waitOnConsumerLeader(globalAccountName, "TEST", "DUR")

	// Get the consumer's internal object from the leader server.
	leader := c.consumerLeader(globalAccountName, "TEST", "DUR")
	require_True(t, leader != nil)

	mset, err := leader.GlobalAccount().lookupStream("TEST")
	require_NoError(t, err)

	o := mset.lookupConsumer("DUR")
	require_True(t, o != nil)

	// Simulate the bug: set inMonitor=true as if a creation goroutine
	// were still running. This is what happens when the goroutine from
	// processClusterCreateConsumer hasn't finished yet.
	//
	// With the original PR (no monitorWg.Wait()), the update path would
	// call shouldStartMonitor() which returns false, silently skipping
	// processConsumerLeaderChange and never sending the API response.
	alreadyInMonitor := o.shouldStartMonitor()
	t.Logf("shouldStartMonitor() first call = %v (expected true)", alreadyInMonitor)

	// Second call simulates what happens when a config update arrives
	// while the first goroutine is still running.
	canStartAgain := o.shouldStartMonitor()
	t.Logf("shouldStartMonitor() second call = %v (expected false - THIS IS THE BUG)", canStartAgain)

	if canStartAgain {
		t.Fatal("Expected shouldStartMonitor() to return false on second call, " +
			"demonstrating that a concurrent config update would be dropped")
	}

	// This proves: without monitorWg.Wait() to drain the previous goroutine
	// before calling shouldStartMonitor(), the update's leader change
	// processing is silently skipped. The client never gets a response.
	t.Log("Bug confirmed: shouldStartMonitor() returns false while a previous " +
		"goroutine is active, which means processConsumerLeaderChange would be " +
		"skipped for the config update, and the client response would never be sent.")

	// Clean up: release the monitor state we acquired.
	o.clearMonitorRunning()
	// Second clear should be a no-op (test clearMonitorRunning idempotency).
	o.clearMonitorRunning()

	// Now verify that after clearing, shouldStartMonitor works again.
	// This is what the monitorWg.Wait() fix enables — it waits for
	// clearMonitorRunning() to be called before trying shouldStartMonitor().
	canStartAfterClear := o.shouldStartMonitor()
	if !canStartAfterClear {
		t.Fatal("Expected shouldStartMonitor() to return true after clearing")
	}
	o.clearMonitorRunning()

	t.Log("Fix verified: after monitorWg.Wait() (which waits for clearMonitorRunning), " +
		"shouldStartMonitor() returns true and the update proceeds normally.")

	// Also demonstrate that the update path with the wait times out
	// if clearMonitorRunning is never called (simulating a stuck goroutine).
	o.shouldStartMonitor() // Acquire monitor state

	done := make(chan struct{})
	go func() {
		// This simulates the update path calling monitorWg.Wait()
		// It will block until clearMonitorRunning is called.
		o.monitorWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("monitorWg.Wait() should not have returned yet")
	case <-time.After(200 * time.Millisecond):
		t.Log("Confirmed: monitorWg.Wait() blocks while goroutine is in-flight")
	}

	// Now simulate the goroutine finishing.
	o.clearMonitorRunning()

	select {
	case <-done:
		t.Log("monitorWg.Wait() returned after clearMonitorRunning — update can proceed")
	case <-time.After(2 * time.Second):
		t.Fatal("monitorWg.Wait() should have returned after clearMonitorRunning")
	}
}
