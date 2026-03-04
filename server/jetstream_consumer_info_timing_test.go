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
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamClusterConsumerInfoDuringInflightCreate demonstrates Race 1:
// When a consumer create is proposed to Raft but not yet committed (inflight),
// a concurrent consumer info request returns "consumer not found" because
// jsConsumerInfoRequest uses consumerAssignment() which only checks applied
// assignments, not consumerAssignmentOrInflight().
//
// On a 3-node cluster, we pause meta apply on a non-leader so that the Raft
// entry is committed (meta leader + one follower) but one node hasn't applied it.
// If the client is connected to that paused node and the info request reaches
// the meta leader before the proposal is applied, it would succeed. But from the
// paused node's perspective, the consumer assignment does not exist yet.
func TestJetStreamClusterConsumerInfoDuringInflightCreate(t *testing.T) {
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

	c.waitOnStreamLeader("$G", "TEST")

	// Pick a non-leader server and pause its meta apply.
	// This simulates a node that is slow to apply committed Raft entries.
	rs := c.randomNonLeader()
	sjs := rs.getJetStream()
	meta := sjs.getMetaGroup()
	require_NoError(t, meta.PauseApply())

	// Connect a second client directly to the meta leader for the create.
	ml := c.leader()
	ncLeader, jsLeader := jsClientConnect(t, ml)
	defer ncLeader.Close()

	// Create a consumer. The meta leader will propose and commit it.
	// The paused node won't apply it until we resume.
	_, err = jsLeader.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	c.waitOnConsumerLeader("$G", "TEST", "CONSUMER")

	// Now connect to the paused node and try consumer info.
	// The paused node hasn't applied the consumer assignment from meta yet.
	ncPaused, _ := jsClientConnect(t, rs)
	defer ncPaused.Close()

	// Send a raw consumer info request. The paused node doesn't know about
	// the consumer in its meta state. Since it is not the meta leader,
	// jsConsumerInfoRequest at line 4789 sees ca==nil && !isLeader and
	// returns silently (no response). The consumer leader (on another node)
	// will respond instead. So the client should still get a response.
	resp, err := ncPaused.Request(
		fmt.Sprintf(JSApiConsumerInfoT, "TEST", "CONSUMER"),
		nil, 2*time.Second,
	)
	// We expect this to succeed because the consumer leader on another node responds.
	require_NoError(t, err)

	var infoResp JSApiConsumerInfoResponse
	require_NoError(t, json.Unmarshal(resp.Data, &infoResp))
	if infoResp.Error != nil {
		t.Logf("Race 1 demonstrated: consumer info returned error while node hasn't applied assignment: %+v", infoResp.Error)
	} else {
		t.Logf("Consumer info succeeded (consumer leader responded): stream=%s consumer=%s",
			infoResp.Stream, infoResp.Name)
	}
	// Verify the info we got back is valid.
	require_True(t, infoResp.ConsumerInfo != nil)

	// Resume applies and verify everything converges.
	meta.ResumeApply()

	// Confirm all servers now have the consumer.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		for _, s := range c.servers {
			mset, err := s.globalAccount().lookupStream("TEST")
			if err != nil {
				return err
			}
			if mset.lookupConsumer("CONSUMER") == nil {
				return fmt.Errorf("consumer not found on %s", s.Name())
			}
		}
		return nil
	})
}

// TestJetStreamClusterConsumerInfoBeforeRaftGroupStarted demonstrates Race 2:
// After the meta Raft commits the consumer assignment but before the consumer
// Raft group is fully created on a member node, consumer info returns partial
// information (config and created time, but no state or cluster info).
//
// We pause the meta apply on a non-leader, create a consumer, and then resume.
// The brief window where the assignment exists but the Raft group hasn't started
// yet triggers the code path at jetstream_api.go:4834-4851.
func TestJetStreamClusterConsumerInfoBeforeRaftGroupStarted(t *testing.T) {
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
	c.waitOnStreamLeader("$G", "TEST")

	// Pick a non-leader and pause its meta apply. This node will receive
	// the meta Raft entries in its log but won't process them.
	rs := c.randomNonLeader()
	sjs := rs.getJetStream()
	meta := sjs.getMetaGroup()
	require_NoError(t, meta.PauseApply())

	// Create consumer from the leader.
	ml := c.leader()
	ncLeader, jsLeader := jsClientConnect(t, ml)
	defer ncLeader.Close()

	_, err = jsLeader.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "CONSUMER2",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)
	c.waitOnConsumerLeader("$G", "TEST", "CONSUMER2")

	// Resume apply on the paused node. The node will now apply the consumer
	// assignment. There's a brief window where the assignment is applied but
	// the consumer Raft group node hasn't been fully initialized yet (node==nil).
	// During this window, consumer info from this node returns partial info.
	meta.ResumeApply()

	// Poll consumer info rapidly from the paused node to try to catch it
	// during the window where assignment exists but Raft group is starting.
	ncPaused, _ := jsClientConnect(t, rs)
	defer ncPaused.Close()

	var sawPartialInfo bool
	for i := 0; i < 50; i++ {
		resp, err := ncPaused.Request(
			fmt.Sprintf(JSApiConsumerInfoT, "TEST", "CONSUMER2"),
			nil, 2*time.Second,
		)
		if err != nil {
			continue
		}
		var infoResp JSApiConsumerInfoResponse
		if err := json.Unmarshal(resp.Data, &infoResp); err != nil {
			continue
		}
		if infoResp.ConsumerInfo != nil {
			// Check if we got partial info (no cluster info or empty delivered state).
			if infoResp.Cluster == nil {
				sawPartialInfo = true
				t.Logf("Race 2 demonstrated: got partial consumer info (no cluster info) at iteration %d: "+
					"stream=%s consumer=%s config=%+v",
					i, infoResp.Stream, infoResp.Name, infoResp.Config)
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !sawPartialInfo {
		t.Log("Race 2 window not caught (expected - the window is very small). " +
			"Consumer Raft group initialized too quickly.")
	}

	// Verify everything converges.
	c.waitOnConsumerLeader("$G", "TEST", "CONSUMER2")
	ci, err := js.ConsumerInfo("TEST", "CONSUMER2")
	require_NoError(t, err)
	require_True(t, ci != nil)
	require_True(t, ci.Cluster != nil)
}

// TestJetStreamClusterConsumerInfoScatterBeforeLeaderElected demonstrates Race 3:
// When a consumer Raft group has been created on all members but no leader has
// been elected yet, the code at jetstream_api.go:4862 allows ALL members to
// respond to consumer info. This means the client may receive multiple responses
// (scatter), and each response contains only config + defaults, no real state.
//
// This test creates consumers rapidly and checks if we can observe the brief
// window where the consumer Raft group exists but hasn't elected a leader.
func TestJetStreamClusterConsumerInfoScatterBeforeLeaderElected(t *testing.T) {
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
	c.waitOnStreamLeader("$G", "TEST")

	// Publish some messages so we can check NumPending differences.
	for i := 0; i < 10; i++ {
		_, err = js.Publish("foo", []byte("msg"))
		require_NoError(t, err)
	}

	// Create multiple consumers rapidly and check info immediately after each.
	// We look for the window where consumer info returns but with incomplete state.
	var sawIncompleteInfo bool
	for i := 0; i < 20; i++ {
		consName := fmt.Sprintf("C_%d", i)

		// Create consumer using raw API request for minimal overhead.
		createReq := CreateConsumerRequest{
			Stream: "TEST",
			Config: ConsumerConfig{
				Name:      consName,
				AckPolicy: AckExplicit,
			},
			Action: ActionCreate,
		}
		reqData, err := json.Marshal(createReq)
		require_NoError(t, err)

		createSubj := fmt.Sprintf("$JS.API.CONSUMER.CREATE.TEST.%s", consName)
		createResp, err := nc.Request(createSubj, reqData, 5*time.Second)
		require_NoError(t, err)

		var cr JSApiConsumerCreateResponse
		require_NoError(t, json.Unmarshal(createResp.Data, &cr))
		if cr.Error != nil {
			t.Fatalf("Failed to create consumer %s: %+v", consName, cr.Error)
		}

		// Immediately query consumer info (no delay).
		infoResp, err := nc.Request(
			fmt.Sprintf(JSApiConsumerInfoT, "TEST", consName),
			nil, 2*time.Second,
		)
		require_NoError(t, err)

		var info JSApiConsumerInfoResponse
		require_NoError(t, json.Unmarshal(infoResp.Data, &info))

		if info.ConsumerInfo != nil && info.Cluster == nil {
			sawIncompleteInfo = true
			t.Logf("Race 3 demonstrated: consumer info returned without cluster info for %s "+
				"(consumer exists but leader may not be fully established)", consName)
		}

		// Clean up
		_ = js.DeleteConsumer("TEST", consName)
	}

	if !sawIncompleteInfo {
		t.Log("Race 3 window not caught (expected - leader election is usually fast). " +
			"All info responses had complete cluster info.")
	}
}

// TestJetStreamClusterConsumerInfoNotFoundDuringPipelinedCreate demonstrates
// the most impactful race: when a client pipelines a consumer create and info
// request (sends both without waiting for the create response), the info may
// arrive at the meta leader while the create proposal is still inflight.
//
// The meta leader uses consumerAssignment() which only checks applied assignments,
// not inflight proposals. So if the create Raft entry hasn't been applied yet,
// the info returns "consumer not found" from the meta leader.
//
// This is the core timing issue that PR #7898 (separate info queue) interacts with,
// though it exists independent of that PR.
func TestJetStreamClusterConsumerInfoNotFoundDuringPipelinedCreate(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	ml := c.leader()
	nc, js := jsClientConnect(t, ml)
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader("$G", "TEST")

	// Attempt to pipeline consumer create + info requests.
	// We send both asynchronously: the create via nc.Request in a goroutine,
	// and info immediately from the main goroutine. The idea is to hit the
	// window where the create proposal is submitted but not yet committed.
	var (
		notFoundCount int
		mu            sync.Mutex
	)

	for i := 0; i < 50; i++ {
		consName := fmt.Sprintf("PIPE_%d", i)

		// Use raw API for maximum control over timing.
		createReq := CreateConsumerRequest{
			Stream: "TEST",
			Config: ConsumerConfig{
				Name:      consName,
				AckPolicy: AckExplicit,
			},
			Action: ActionCreate,
		}
		reqData, err := json.Marshal(createReq)
		require_NoError(t, err)

		createSubj := fmt.Sprintf("$JS.API.CONSUMER.CREATE.TEST.%s", consName)
		infoSubj := fmt.Sprintf(JSApiConsumerInfoT, "TEST", consName)

		// Fire off the create in a goroutine.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = nc.Request(createSubj, reqData, 5*time.Second)
		}()

		// Immediately fire off an info request from the main goroutine.
		// This races with the create above.
		infoResp, err := nc.Request(infoSubj, nil, 2*time.Second)
		if err != nil {
			// Timeout means no one responded - the consumer doesn't exist yet
			// on any node and no node was the meta leader with ca!=nil.
			mu.Lock()
			notFoundCount++
			mu.Unlock()
		} else {
			var info JSApiConsumerInfoResponse
			if err := json.Unmarshal(infoResp.Data, &info); err == nil {
				if info.Error != nil {
					mu.Lock()
					notFoundCount++
					mu.Unlock()
					if i == 0 {
						t.Logf("Pipelined race: consumer info error for %s: %+v", consName, info.Error)
					}
				}
			}
		}

		wg.Wait()
		// Clean up for next iteration.
		_ = js.DeleteConsumer("TEST", consName)
		// Small pause to let cleanup complete.
		time.Sleep(50 * time.Millisecond)
	}

	t.Logf("Pipelined create+info: %d/50 info requests returned not-found or error "+
		"(demonstrates the inflight proposal visibility race)", notFoundCount)
}

// TestJetStreamClusterConsumerInfoPausedMetaApply demonstrates the most
// reliable reproduction of the timing issue: pause the meta apply on ONE
// non-leader node, then create a consumer, and immediately query info
// from the paused node.
//
// The meta leader + one follower can still commit and apply (majority 2/3).
// The consumer Raft group starts on those 2 nodes and elects a leader.
// But the paused node hasn't applied the consumer assignment yet, so
// consumerAssignment() returns nil while consumerAssignmentOrInflight()
// can find it in the inflight proposals.
func TestJetStreamClusterConsumerInfoPausedMetaApply(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	ml := c.leader()
	nc, js := jsClientConnect(t, ml)
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)
	c.waitOnStreamLeader("$G", "TEST")

	// Pause meta apply on ONE non-leader server. This allows the other two
	// nodes (meta leader + one follower) to form majority and create the consumer.
	pausedServer := c.randomNonLeader()
	pjs := pausedServer.getJetStream()
	pausedMeta := pjs.getMetaGroup()
	require_NoError(t, pausedMeta.PauseApply())

	// Create consumer - commits with 2/3 majority (meta leader + unpaushed follower).
	_, err = js.AddConsumer("TEST", &nats.ConsumerConfig{
		Durable:   "PAUSED_TEST",
		AckPolicy: nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// The consumer should be operational on 2 nodes. The paused node has
	// the entry in its Raft log but hasn't applied it.
	c.waitOnConsumerLeader("$G", "TEST", "PAUSED_TEST")

	// Verify: on the paused node, the consumer assignment should NOT be in
	// the applied state, but may be visible as inflight.
	pjs.mu.RLock()
	ca := pjs.consumerAssignment("$G", "TEST", "PAUSED_TEST")
	caInflight := pjs.consumerAssignmentOrInflight("$G", "TEST", "PAUSED_TEST")
	pjs.mu.RUnlock()

	t.Logf("Paused node state: consumerAssignment=%v, consumerAssignmentOrInflight=%v",
		ca != nil, caInflight != nil)

	if ca == nil {
		t.Log("Confirmed: consumerAssignment() returns nil on the paused node " +
			"(meta entry not yet applied).")
	}
	if caInflight != nil && ca == nil {
		t.Log("Confirmed: consumer is inflight (proposed but not applied) on the paused node. " +
			"consumerAssignmentOrInflight() finds it while consumerAssignment() does not.")
	}

	// Query consumer info from the paused node.
	// The paused node is not meta leader and ca==nil, so it silently returns nothing.
	// The consumer leader on another node will respond.
	ncPaused, _ := jsClientConnect(t, pausedServer)
	defer ncPaused.Close()

	resp, err := ncPaused.Request(
		fmt.Sprintf(JSApiConsumerInfoT, "TEST", "PAUSED_TEST"),
		nil, 2*time.Second,
	)
	require_NoError(t, err)

	var infoResp JSApiConsumerInfoResponse
	require_NoError(t, json.Unmarshal(resp.Data, &infoResp))

	if infoResp.ConsumerInfo != nil {
		t.Logf("Consumer info returned (from consumer leader on another node): "+
			"stream=%s name=%s cluster=%+v", infoResp.Stream, infoResp.Name, infoResp.Cluster)
	}
	if infoResp.Error != nil {
		t.Logf("Consumer info returned error: %+v", infoResp.Error)
	}

	// Resume the paused meta.
	pausedMeta.ResumeApply()

	// Verify everything converges.
	checkFor(t, 5*time.Second, 200*time.Millisecond, func() error {
		for _, s := range c.servers {
			acc, err := s.lookupAccount(globalAccountName)
			if err != nil {
				return err
			}
			mset, err := acc.lookupStream("TEST")
			if err != nil {
				return err
			}
			if mset.lookupConsumer("PAUSED_TEST") == nil {
				return fmt.Errorf("consumer not found on %s", s.Name())
			}
		}
		return nil
	})

	// After resume, paused node should also have the consumer in applied state.
	pjs.mu.RLock()
	caAfter := pjs.consumerAssignment("$G", "TEST", "PAUSED_TEST")
	pjs.mu.RUnlock()
	require_True(t, caAfter != nil)

	// Final consumer info should work perfectly.
	ci, err := js.ConsumerInfo("TEST", "PAUSED_TEST")
	require_NoError(t, err)
	require_True(t, ci != nil)
	require_True(t, ci.Cluster != nil)
	t.Logf("After resume, consumer info fully converged: leader=%s replicas=%d",
		ci.Cluster.Leader, len(ci.Cluster.Replicas))
}
