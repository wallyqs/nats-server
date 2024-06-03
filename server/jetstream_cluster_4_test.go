// Copyright 2022-2024 The NATS Authors
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
// +build !skip_js_tests,!skip_js_cluster_tests_4

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"
)

func TestJetStreamClusterWorkQueueStreamDiscardNewDesync(t *testing.T) {
	t.Run("max msgs", func(t *testing.T) {
		testJetStreamClusterWorkQueueStreamDiscardNewDesync(t, &nats.StreamConfig{
			Name:      "WQTEST_MM",
			Subjects:  []string{"messages.*"},
			Replicas:  3,
			MaxAge:    10 * time.Minute,
			MaxMsgs:   100,
			Retention: nats.WorkQueuePolicy,
			Discard:   nats.DiscardNew,
		})
	})
	t.Run("max bytes", func(t *testing.T) {
		testJetStreamClusterWorkQueueStreamDiscardNewDesync(t, &nats.StreamConfig{
			Name:      "WQTEST_MB",
			Subjects:  []string{"messages.*"},
			Replicas:  3,
			MaxAge:    10 * time.Minute,
			MaxBytes:  1 * 1024 * 1024,
			Retention: nats.WorkQueuePolicy,
			Discard:   nats.DiscardNew,
		})
	})
}

func testJetStreamClusterWorkQueueStreamDiscardNewDesync(t *testing.T, sc *nats.StreamConfig) {
	conf := `
	listen: 127.0.0.1:-1
	server_name: %s
	jetstream: {
		store_dir: '%s',
	}
	cluster {
		name: %s
		listen: 127.0.0.1:%d
		routes = [%s]
	}
        system_account: sys
        no_auth_user: js
	accounts {
	  sys {
	    users = [
	      { user: sys, pass: sys }
	    ]
	  }
	  js {
	    jetstream = enabled
	    users = [
	      { user: js, pass: js }
	    ]
	  }
	}`
	c := createJetStreamClusterWithTemplate(t, conf, sc.Name, 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	cnc, cjs := jsClientConnect(t, c.randomServer())
	defer cnc.Close()

	_, err := js.AddStream(sc)
	require_NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	psub, err := cjs.PullSubscribe("messages.*", "consumer")
	require_NoError(t, err)

	stepDown := func() {
		_, err = nc.Request(fmt.Sprintf(JSApiStreamLeaderStepDownT, sc.Name), nil, time.Second)
	}

	// Messages will be produced and consumed in parallel, then once there are
	// enough errors a leader election will be triggered.
	var (
		wg          sync.WaitGroup
		received    uint64
		errCh       = make(chan error, 100_000)
		receivedMap = make(map[string]*nats.Msg)
	)
	wg.Add(1)
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			case <-tick.C:
				msgs, err := psub.Fetch(10, nats.MaxWait(200*time.Millisecond))
				if err != nil {
					// The consumer will continue to timeout here eventually.
					continue
				}
				for _, msg := range msgs {
					received++
					receivedMap[msg.Subject] = msg
					msg.Ack()
				}
			}
		}
	}()

	shouldDrop := make(map[string]error)
	wg.Add(1)
	go func() {
		payload := []byte(strings.Repeat("A", 1024))
		tick := time.NewTicker(1 * time.Millisecond)
		for i := 1; ; i++ {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			case <-tick.C:
				subject := fmt.Sprintf("messages.%d", i)
				_, err := js.Publish(subject, payload, nats.RetryAttempts(0))
				if err != nil {
					errCh <- err
				}
				// Capture the messages that have failed.
				if err != nil {
					shouldDrop[subject] = err
				}
			}
		}
	}()

	// Collect enough errors to cause things to get out of sync.
	var errCount int
Setup:
	for {
		select {
		case err = <-errCh:
			errCount++
			if errCount%500 == 0 {
				stepDown()
			} else if errCount >= 2000 {
				// Stop both producing and consuming.
				cancel()
				break Setup
			}
		case <-time.After(5 * time.Second):
			// Unblock the test and continue.
			cancel()
			break Setup
		}
	}

	// Both goroutines should be exiting now..
	wg.Wait()

	// Let acks propagate for stream checks.
	time.Sleep(250 * time.Millisecond)

	// Check messages that ought to have been dropped.
	for subject := range receivedMap {
		found, ok := shouldDrop[subject]
		if ok {
			t.Errorf("Should have dropped message published on %q since got error: %v", subject, found)
		}
	}
}

// https://github.com/nats-io/nats-server/issues/5071
func TestJetStreamClusterStreamPlacementDistribution(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 5)
	defer c.shutdown()

	s := c.randomNonLeader()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	for i := 1; i <= 10; i++ {
		_, err := js.AddStream(&nats.StreamConfig{
			Name:     fmt.Sprintf("TEST:%d", i),
			Subjects: []string{fmt.Sprintf("foo.%d.*", i)},
			Replicas: 3,
		})
		require_NoError(t, err)
	}

	// 10 streams, 3 replicas div 5 servers.
	expectedStreams := 10 * 3 / 5
	for _, s := range c.servers {
		jsz, err := s.Jsz(nil)
		require_NoError(t, err)
		require_Equal(t, jsz.Streams, expectedStreams)
	}
}

func TestJetStreamClusterSourceWorkingQueueWithLimit(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "WQ3", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{Name: "test", Subjects: []string{"test"}, Replicas: 3})
	require_NoError(t, err)

	_, err = js.AddStream(&nats.StreamConfig{Name: "wq", MaxMsgs: 100, Discard: nats.DiscardNew, Retention: nats.WorkQueuePolicy,
		Sources: []*nats.StreamSource{{Name: "test"}}, Replicas: 3})
	require_NoError(t, err)

	sendBatch := func(subject string, n int) {
		for i := 0; i < n; i++ {
			_, err = js.Publish(subject, []byte(strconv.Itoa(i)))
			require_NoError(t, err)
		}
	}
	// Populate each one.
	sendBatch("test", 300)

	checkFor(t, 3*time.Second, 250*time.Millisecond, func() error {
		si, err := js.StreamInfo("wq")
		require_NoError(t, err)
		if si.State.Msgs != 100 {
			return fmt.Errorf("Expected 100 msgs, got state: %+v", si.State)
		}
		return nil
	})

	_, err = js.AddConsumer("wq", &nats.ConsumerConfig{Durable: "wqc", FilterSubject: "test", AckPolicy: nats.AckExplicitPolicy})
	require_NoError(t, err)

	ss, err := js.PullSubscribe("test", "wqc", nats.Bind("wq", "wqc"))
	require_NoError(t, err)
	// we must have at least one message on the transformed subject name (ie no timeout)
	f := func(done chan bool) {
		for i := 0; i < 300; i++ {
			m, err := ss.Fetch(1, nats.MaxWait(3*time.Second))
			require_NoError(t, err)
			p, err := strconv.Atoi(string(m[0].Data))
			require_NoError(t, err)
			require_Equal(t, p, i)
			time.Sleep(11 * time.Millisecond)
			err = m[0].Ack()
			require_NoError(t, err)
		}
		done <- true
	}

	var doneChan = make(chan bool)
	go f(doneChan)

	checkFor(t, 6*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("wq")
		require_NoError(t, err)
		if si.State.Msgs > 0 && si.State.Msgs <= 100 {
			return fmt.Errorf("Expected 0 msgs, got: %d", si.State.Msgs)
		} else if si.State.Msgs > 100 {
			t.Fatalf("Got more than our 100 message limit: %+v", si.State)
		}
		return nil
	})

	select {
	case <-doneChan:
		ss.Drain()
	case <-time.After(5 * time.Second):
		t.Fatalf("Did not receive completion signal")
	}
}

func TestJetStreamClusterConsumerPauseViaConfig(t *testing.T) {
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

	jsTestPause_CreateOrUpdateConsumer(t, nc, ActionCreate, "TEST", ConsumerConfig{
		Name:     "my_consumer",
		Replicas: 3,
	})

	sub, err := js.PullSubscribe("foo", "", nats.Bind("TEST", "my_consumer"))
	require_NoError(t, err)

	stepdown := func() {
		t.Helper()
		_, err := nc.Request(fmt.Sprintf(JSApiConsumerLeaderStepDownT, "TEST", "my_consumer"), nil, time.Second)
		require_NoError(t, err)
		c.waitOnConsumerLeader(globalAccountName, "TEST", "my_consumer")
	}

	publish := func(wait time.Duration) {
		t.Helper()
		for i := 0; i < 5; i++ {
			_, err = js.Publish("foo", []byte("OK"))
			require_NoError(t, err)
		}
		msgs, err := sub.Fetch(5, nats.MaxWait(wait))
		require_NoError(t, err)
		require_Equal(t, len(msgs), 5)
	}

	// This should be fast as there's no deadline.
	publish(time.Second)

	// Now we're going to set the deadline.
	deadline := jsTestPause_PauseConsumer(t, nc, "TEST", "my_consumer", time.Now().Add(time.Second*3))
	c.waitOnAllCurrent()

	// It will now take longer than 3 seconds.
	publish(time.Second * 5)
	require_True(t, time.Now().After(deadline))

	// The next set of publishes after the deadline should now be fast.
	publish(time.Second)

	// We'll kick the leader, but since we're after the deadline, this
	// should still be fast.
	stepdown()
	publish(time.Second)

	// Now we're going to do an update and then immediately kick the
	// leader. The pause should still be in effect afterwards.
	deadline = jsTestPause_PauseConsumer(t, nc, "TEST", "my_consumer", time.Now().Add(time.Second*3))
	c.waitOnAllCurrent()
	publish(time.Second * 5)
	require_True(t, time.Now().After(deadline))

	// The next set of publishes after the deadline should now be fast.
	publish(time.Second)
}

func TestJetStreamClusterConsumerPauseViaEndpoint(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"push", "pull"},
		Replicas: 3,
	})
	require_NoError(t, err)

	t.Run("PullConsumer", func(t *testing.T) {
		_, err := js.AddConsumer("TEST", &nats.ConsumerConfig{
			Name: "pull_consumer",
		})
		require_NoError(t, err)

		sub, err := js.PullSubscribe("pull", "", nats.Bind("TEST", "pull_consumer"))
		require_NoError(t, err)

		// This should succeed as there's no pause, so it definitely
		// shouldn't take more than a second.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("pull", []byte("OK"))
			require_NoError(t, err)
		}
		msgs, err := sub.Fetch(10, nats.MaxWait(time.Second))
		require_NoError(t, err)
		require_Equal(t, len(msgs), 10)

		// Now we'll pause the consumer for 3 seconds.
		deadline := time.Now().Add(time.Second * 3)
		require_True(t, jsTestPause_PauseConsumer(t, nc, "TEST", "pull_consumer", deadline).Equal(deadline))
		c.waitOnAllCurrent()

		// This should fail as we'll wait for only half of the deadline.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("pull", []byte("OK"))
			require_NoError(t, err)
		}
		_, err = sub.Fetch(10, nats.MaxWait(time.Until(deadline)/2))
		require_Error(t, err, nats.ErrTimeout)

		// This should succeed after a short wait, and when we're done,
		// we should be after the deadline.
		msgs, err = sub.Fetch(10)
		require_NoError(t, err)
		require_Equal(t, len(msgs), 10)
		require_True(t, time.Now().After(deadline))

		// This should succeed as there's no pause, so it definitely
		// shouldn't take more than a second.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("pull", []byte("OK"))
			require_NoError(t, err)
		}
		msgs, err = sub.Fetch(10, nats.MaxWait(time.Second))
		require_NoError(t, err)
		require_Equal(t, len(msgs), 10)

		require_True(t, jsTestPause_PauseConsumer(t, nc, "TEST", "pull_consumer", time.Time{}).Equal(time.Time{}))
		c.waitOnAllCurrent()

		// This should succeed as there's no pause, so it definitely
		// shouldn't take more than a second.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("pull", []byte("OK"))
			require_NoError(t, err)
		}
		msgs, err = sub.Fetch(10, nats.MaxWait(time.Second))
		require_NoError(t, err)
		require_Equal(t, len(msgs), 10)
	})

	t.Run("PushConsumer", func(t *testing.T) {
		ch := make(chan *nats.Msg, 100)
		_, err = js.ChanSubscribe("push", ch, nats.BindStream("TEST"), nats.ConsumerName("push_consumer"))
		require_NoError(t, err)

		// This should succeed as there's no pause, so it definitely
		// shouldn't take more than a second.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("push", []byte("OK"))
			require_NoError(t, err)
		}
		for i := 0; i < 10; i++ {
			msg := require_ChanRead(t, ch, time.Second)
			require_NotEqual(t, msg, nil)
		}

		// Now we'll pause the consumer for 3 seconds.
		deadline := time.Now().Add(time.Second * 3)
		require_True(t, jsTestPause_PauseConsumer(t, nc, "TEST", "push_consumer", deadline).Equal(deadline))
		c.waitOnAllCurrent()

		// This should succeed after a short wait, and when we're done,
		// we should be after the deadline.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("push", []byte("OK"))
			require_NoError(t, err)
		}
		for i := 0; i < 10; i++ {
			msg := require_ChanRead(t, ch, time.Second*5)
			require_NotEqual(t, msg, nil)
			require_True(t, time.Now().After(deadline))
		}

		// This should succeed as there's no pause, so it definitely
		// shouldn't take more than a second.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("push", []byte("OK"))
			require_NoError(t, err)
		}
		for i := 0; i < 10; i++ {
			msg := require_ChanRead(t, ch, time.Second)
			require_NotEqual(t, msg, nil)
		}

		require_True(t, jsTestPause_PauseConsumer(t, nc, "TEST", "push_consumer", time.Time{}).Equal(time.Time{}))
		c.waitOnAllCurrent()

		// This should succeed as there's no pause, so it definitely
		// shouldn't take more than a second.
		for i := 0; i < 10; i++ {
			_, err = js.Publish("push", []byte("OK"))
			require_NoError(t, err)
		}
		for i := 0; i < 10; i++ {
			msg := require_ChanRead(t, ch, time.Second)
			require_NotEqual(t, msg, nil)
		}
	})
}

func TestJetStreamClusterConsumerPauseTimerFollowsLeader(t *testing.T) {
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

	deadline := time.Now().Add(time.Hour)
	jsTestPause_CreateOrUpdateConsumer(t, nc, ActionCreate, "TEST", ConsumerConfig{
		Name:       "my_consumer",
		PauseUntil: &deadline,
		Replicas:   3,
	})

	for i := 0; i < 10; i++ {
		c.waitOnConsumerLeader(globalAccountName, "TEST", "my_consumer")
		c.waitOnAllCurrent()

		for _, s := range c.servers {
			stream, err := s.gacc.lookupStream("TEST")
			require_NoError(t, err)

			consumer := stream.lookupConsumer("my_consumer")
			require_NotEqual(t, consumer, nil)

			isLeader := s.JetStreamIsConsumerLeader(globalAccountName, "TEST", "my_consumer")

			consumer.mu.RLock()
			hasTimer := consumer.uptmr != nil
			consumer.mu.RUnlock()

			require_Equal(t, isLeader, hasTimer)
		}

		_, err = nc.Request(fmt.Sprintf(JSApiConsumerLeaderStepDownT, "TEST", "my_consumer"), nil, time.Second)
		require_NoError(t, err)
	}
}

func TestJetStreamClusterConsumerPauseHeartbeats(t *testing.T) {
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

	deadline := time.Now().Add(time.Hour)
	dsubj := "deliver_subj"

	ci := jsTestPause_CreateOrUpdateConsumer(t, nc, ActionCreate, "TEST", ConsumerConfig{
		Name:           "my_consumer",
		PauseUntil:     &deadline,
		Heartbeat:      time.Millisecond * 100,
		DeliverSubject: dsubj,
	})
	require_True(t, ci.Config.PauseUntil.Equal(deadline))

	ch := make(chan *nats.Msg, 10)
	_, err = nc.ChanSubscribe(dsubj, ch)
	require_NoError(t, err)

	for i := 0; i < 20; i++ {
		msg := require_ChanRead(t, ch, time.Millisecond*200)
		require_Equal(t, msg.Header.Get("Status"), "100")
		require_Equal(t, msg.Header.Get("Description"), "Idle Heartbeat")
	}
}

func TestJetStreamClusterConsumerPauseAdvisories(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	checkAdvisory := func(msg *nats.Msg, shouldBePaused bool, deadline time.Time) {
		t.Helper()
		var advisory JSConsumerPauseAdvisory
		require_NoError(t, json.Unmarshal(msg.Data, &advisory))
		require_Equal(t, advisory.Stream, "TEST")
		require_Equal(t, advisory.Consumer, "my_consumer")
		require_Equal(t, advisory.Paused, shouldBePaused)
		require_True(t, advisory.PauseUntil.Equal(deadline))
	}

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	ch := make(chan *nats.Msg, 10)
	_, err = nc.ChanSubscribe(JSAdvisoryConsumerPausePre+".TEST.my_consumer", ch)
	require_NoError(t, err)

	deadline := time.Now().Add(time.Second)
	jsTestPause_CreateOrUpdateConsumer(t, nc, ActionCreate, "TEST", ConsumerConfig{
		Name:       "my_consumer",
		PauseUntil: &deadline,
		Replicas:   3,
	})

	// First advisory should tell us that the consumer was paused
	// on creation.
	msg := require_ChanRead(t, ch, time.Second*2)
	checkAdvisory(msg, true, deadline)
	require_Len(t, len(ch), 0) // Should only receive one advisory.

	// The second one for the unpause.
	msg = require_ChanRead(t, ch, time.Second*2)
	checkAdvisory(msg, false, deadline)
	require_Len(t, len(ch), 0) // Should only receive one advisory.

	// Now we'll pause the consumer for a second using the API.
	deadline = time.Now().Add(time.Second)
	require_True(t, jsTestPause_PauseConsumer(t, nc, "TEST", "my_consumer", deadline).Equal(deadline))

	// Third advisory should tell us about the pause via the API.
	msg = require_ChanRead(t, ch, time.Second*2)
	checkAdvisory(msg, true, deadline)
	require_Len(t, len(ch), 0) // Should only receive one advisory.

	// Finally that should unpause.
	msg = require_ChanRead(t, ch, time.Second*2)
	checkAdvisory(msg, false, deadline)
	require_Len(t, len(ch), 0) // Should only receive one advisory.

	// Now we're going to set the deadline into the future so we can
	// see what happens when we kick leaders or restart.
	deadline = time.Now().Add(time.Hour)
	require_True(t, jsTestPause_PauseConsumer(t, nc, "TEST", "my_consumer", deadline).Equal(deadline))

	// Setting the deadline should have generated an advisory.
	msg = require_ChanRead(t, ch, time.Second)
	checkAdvisory(msg, true, deadline)
	require_Len(t, len(ch), 0) // Should only receive one advisory.

	// Try to kick the consumer leader.
	srv := c.consumerLeader(globalAccountName, "TEST", "my_consumer")
	srv.JetStreamStepdownConsumer(globalAccountName, "TEST", "my_consumer")
	c.waitOnConsumerLeader(globalAccountName, "TEST", "my_consumer")

	// This shouldn't have generated an advisory.
	require_NoChanRead(t, ch, time.Second)
}

func TestJetStreamClusterConsumerPauseSurvivesRestart(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	checkTimer := func(s *Server) {
		stream, err := s.gacc.lookupStream("TEST")
		require_NoError(t, err)

		consumer := stream.lookupConsumer("my_consumer")
		require_NotEqual(t, consumer, nil)

		consumer.mu.RLock()
		timer := consumer.uptmr
		consumer.mu.RUnlock()
		require_True(t, timer != nil)
	}

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	deadline := time.Now().Add(time.Hour)
	jsTestPause_CreateOrUpdateConsumer(t, nc, ActionCreate, "TEST", ConsumerConfig{
		Name:       "my_consumer",
		PauseUntil: &deadline,
		Replicas:   3,
	})

	// First try with just restarting the consumer leader.
	srv := c.consumerLeader(globalAccountName, "TEST", "my_consumer")
	srv.Shutdown()
	c.restartServer(srv)
	c.waitOnAllCurrent()
	c.waitOnConsumerLeader(globalAccountName, "TEST", "my_consumer")
	leader := c.consumerLeader(globalAccountName, "TEST", "my_consumer")
	require_True(t, leader != nil)
	checkTimer(leader)

	// Then try restarting the entire cluster.
	c.stopAll()
	c.restartAllSamePorts()
	c.waitOnAllCurrent()
	c.waitOnConsumerLeader(globalAccountName, "TEST", "my_consumer")
	leader = c.consumerLeader(globalAccountName, "TEST", "my_consumer")
	require_True(t, leader != nil)
	checkTimer(leader)
}

func TestJetStreamClusterStreamOrphanMsgsAndReplicasDrifting(t *testing.T) {
	type testParams struct {
		restartAny       bool
		restartLeader    bool
		rolloutRestart   bool
		ldmRestart       bool
		restarts         int
		checkHealthz     bool
		reconnectRoutes  bool
		reconnectClients bool
	}
	test := func(t *testing.T, params *testParams, sc *nats.StreamConfig) {
		conf := `
		listen: 127.0.0.1:-1
		server_name: %s
		jetstream: {
			store_dir: '%s',
		}
		cluster {
			name: %s
			listen: 127.0.0.1:%d
			routes = [%s]
		}
		server_tags: ["test"]
		system_account: sys
		no_auth_user: js
		accounts {
			sys { users = [ { user: sys, pass: sys } ] }
			js {
				jetstream = enabled
				users = [ { user: js, pass: js } ]
		    }
		}`
		c := createJetStreamClusterWithTemplate(t, conf, sc.Name, 3)
		defer c.shutdown()

		// Update lame duck duration for all servers.
		for _, s := range c.servers {
			s.optsMu.Lock()
			s.opts.LameDuckDuration = 5 * time.Second
			s.opts.LameDuckGracePeriod = -5 * time.Second
			s.optsMu.Unlock()
		}

		nc, js := jsClientConnect(t, c.randomServer())
		defer nc.Close()

		cnc, cjs := jsClientConnect(t, c.randomServer())
		defer cnc.Close()

		_, err := js.AddStream(sc)
		require_NoError(t, err)

		pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Start producers
		var wg sync.WaitGroup

		// First call is just to create the pull subscribers.
		mp := nats.MaxAckPending(10000)
		mw := nats.PullMaxWaiting(1000)
		aw := nats.AckWait(5 * time.Second)

		for i := 0; i < 10; i++ {
			for _, partition := range []string{"EEEEE"} {
				subject := fmt.Sprintf("MSGS.%s.*.H.100XY.*.*.WQ.00000000000%d", partition, i)
				consumer := fmt.Sprintf("consumer:%s:%d", partition, i)
				_, err := cjs.PullSubscribe(subject, consumer, mp, mw, aw)
				require_NoError(t, err)
			}
		}

		// Create a single consumer that does no activity.
		// Make sure we still calculate low ack properly and cleanup etc.
		_, err = cjs.PullSubscribe("MSGS.ZZ.>", "consumer:ZZ:0", mp, mw, aw)
		require_NoError(t, err)

		subjects := []string{
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000000",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000001",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000002",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000003",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000004",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000005",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000006",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000007",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000008",
			"MSGS.EEEEE.P.H.100XY.1.100Z.WQ.000000000009",
		}
		payload := []byte(strings.Repeat("A", 1024))

		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				pnc, pjs := jsClientConnect(t, c.randomServer())
				defer pnc.Close()

				for i := 1; i < 200_000; i++ {
					select {
					case <-pctx.Done():
						wg.Done()
						return
					default:
					}
					for _, subject := range subjects {
						// Send each message a few times.
						msgID := nats.MsgId(nuid.Next())
						pjs.PublishAsync(subject, payload, msgID)
						pjs.Publish(subject, payload, msgID, nats.AckWait(250*time.Millisecond))
						pjs.Publish(subject, payload, msgID, nats.AckWait(250*time.Millisecond))
					}
				}
			}()
		}

		// Rogue publisher that sends the same msg ID everytime.
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				pnc, pjs := jsClientConnect(t, c.randomServer())
				defer pnc.Close()

				msgID := nats.MsgId("1234567890")
				for i := 1; ; i++ {
					select {
					case <-pctx.Done():
						wg.Done()
						return
					default:
					}
					for _, subject := range subjects {
						// Send each message a few times.
						pjs.PublishAsync(subject, payload, msgID, nats.RetryAttempts(0), nats.RetryWait(0))
						pjs.Publish(subject, payload, msgID, nats.AckWait(1*time.Millisecond), nats.RetryAttempts(0), nats.RetryWait(0))
						pjs.Publish(subject, payload, msgID, nats.AckWait(1*time.Millisecond), nats.RetryAttempts(0), nats.RetryWait(0))
					}
				}
			}()
		}

		// Let enough messages into the stream then start consumers.
		time.Sleep(15 * time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		for i := 0; i < 10; i++ {
			subject := fmt.Sprintf("MSGS.EEEEE.*.H.100XY.*.*.WQ.00000000000%d", i)
			consumer := fmt.Sprintf("consumer:EEEEE:%d", i)
			for n := 0; n < 5; n++ {
				cpnc, cpjs := jsClientConnect(t, c.randomServer())
				defer cpnc.Close()

				psub, err := cpjs.PullSubscribe(subject, consumer, mp, mw, aw)
				require_NoError(t, err)

				time.AfterFunc(15*time.Second, func() {
					cpnc.Close()
				})

				wg.Add(1)
				go func() {
					tick := time.NewTicker(1 * time.Millisecond)
					for {
						if cpnc.IsClosed() {
							wg.Done()
							return
						}
						select {
						case <-ctx.Done():
							wg.Done()
							return
						case <-tick.C:
							// Fetch 1 first, then if no errors Fetch 100.
							msgs, err := psub.Fetch(1, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}
							msgs, err = psub.Fetch(100, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}
							msgs, err = psub.Fetch(1000, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}
						}
					}
				}()
			}
		}

		for i := 0; i < 10; i++ {
			subject := fmt.Sprintf("MSGS.EEEEE.*.H.100XY.*.*.WQ.00000000000%d", i)
			consumer := fmt.Sprintf("consumer:EEEEE:%d", i)
			for n := 0; n < 10; n++ {
				cpnc, cpjs := jsClientConnect(t, c.randomServer())
				defer cpnc.Close()

				psub, err := cpjs.PullSubscribe(subject, consumer, mp, mw, aw)
				if err != nil {
					t.Logf("ERROR: %v", err)
					continue
				}

				wg.Add(1)
				go func() {
					tick := time.NewTicker(1 * time.Millisecond)
					for {
						select {
						case <-ctx.Done():
							wg.Done()
							return
						case <-tick.C:
							// Fetch 1 first, then if no errors Fetch 100.
							msgs, err := psub.Fetch(1, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}
							msgs, err = psub.Fetch(100, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}

							msgs, err = psub.Fetch(1000, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}
						}
					}
				}()
			}
		}

		// Periodically disconnect routes from one of the servers.
		if params.reconnectRoutes {
			wg.Add(1)
			go func() {
				for range time.NewTicker(10 * time.Second).C {
					select {
					case <-ctx.Done():
						wg.Done()
						return
					default:
					}

					// Force disconnecting routes from one of the servers.
					s := c.servers[rand.Intn(3)]
					var routes []*client
					t.Logf("Disconnecting routes from %v", s.Name())
					s.mu.Lock()
					for _, conns := range s.routes {
						routes = append(routes, conns...)
					}
					s.mu.Unlock()
					for _, r := range routes {
						r.closeConnection(ClientClosed)
					}
				}
			}()
		}

		// Periodically reconnect clients.
		if params.reconnectClients {
			reconnectClients := func(s *Server) {
				for _, client := range s.clients {
					client.closeConnection(Kicked)
				}
			}

			wg.Add(1)
			go func() {
				for range time.NewTicker(10 * time.Second).C {
					select {
					case <-ctx.Done():
						wg.Done()
						return
					default:
					}
					// Force reconnect clients from one of the servers.
					s := c.servers[rand.Intn(len(c.servers))]
					reconnectClients(s)
				}
			}()
		}

		// Restarts
		time.AfterFunc(10*time.Second, func() {
			for i := 0; i < params.restarts; i++ {
				switch {
				case params.restartLeader:
					// Find server leader of the stream and restart it.
					s := c.streamLeader("js", sc.Name)
					if params.ldmRestart {
						s.lameDuckMode()
					} else {
						s.Shutdown()
					}
					s.WaitForShutdown()
					c.restartServer(s)
				case params.restartAny:
					s := c.servers[rand.Intn(len(c.servers))]
					if params.ldmRestart {
						s.lameDuckMode()
					} else {
						s.Shutdown()
					}
					s.WaitForShutdown()
					c.restartServer(s)
				case params.rolloutRestart:
					for _, s := range c.servers {
						if params.ldmRestart {
							s.lameDuckMode()
						} else {
							s.Shutdown()
						}
						s.WaitForShutdown()
						c.restartServer(s)

						if params.checkHealthz {
							hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
							defer hcancel()

							for range time.NewTicker(2 * time.Second).C {
								select {
								case <-hctx.Done():
								default:
								}

								status := s.healthz(nil)
								if status.StatusCode == 200 {
									break
								}
							}
						}
					}
				}
				c.waitOnClusterReady()
			}
		})

		// Wait until context is done then check state.
		<-ctx.Done()

		var consumerPending int
		for i := 0; i < 10; i++ {
			ci, err := js.ConsumerInfo(sc.Name, fmt.Sprintf("consumer:EEEEE:%d", i))
			require_NoError(t, err)
			consumerPending += int(ci.NumPending)
		}

		getStreamDetails := func(t *testing.T, srv *Server) *StreamDetail {
			t.Helper()
			jsz, err := srv.Jsz(&JSzOptions{Accounts: true, Streams: true, Consumer: true})
			require_NoError(t, err)
			if len(jsz.AccountDetails) > 0 && len(jsz.AccountDetails[0].Streams) > 0 {
				stream := jsz.AccountDetails[0].Streams[0]
				return &stream
			}
			t.Error("Could not find account details")
			return nil
		}

		checkState := func(t *testing.T) error {
			t.Helper()

			leaderSrv := c.streamLeader("js", sc.Name)
			if leaderSrv == nil {
				return fmt.Errorf("no leader found for stream")
			}
			streamLeader := getStreamDetails(t, leaderSrv)
			var errs []error
			for _, srv := range c.servers {
				if srv == leaderSrv {
					// Skip self
					continue
				}
				stream := getStreamDetails(t, srv)
				if stream == nil {
					return fmt.Errorf("stream not found")
				}

				if stream.State.Msgs != streamLeader.State.Msgs {
					err := fmt.Errorf("Leader %v has %d messages, Follower %v has %d messages",
						stream.Cluster.Leader, streamLeader.State.Msgs,
						srv, stream.State.Msgs,
					)
					errs = append(errs, err)
				}
				if stream.State.FirstSeq != streamLeader.State.FirstSeq {
					err := fmt.Errorf("Leader %v FirstSeq is %d, Follower %v is at %d",
						stream.Cluster.Leader, streamLeader.State.FirstSeq,
						srv, stream.State.FirstSeq,
					)
					errs = append(errs, err)
				}
				if stream.State.LastSeq != streamLeader.State.LastSeq {
					err := fmt.Errorf("Leader %v LastSeq is %d, Follower %v is at %d",
						stream.Cluster.Leader, streamLeader.State.LastSeq,
						srv, stream.State.LastSeq,
					)
					errs = append(errs, err)
				}
			}
			if len(errs) > 0 {
				return errors.Join(errs...)
			}
			return nil
		}

		checkMsgsEqual := func(t *testing.T) {
			// These have already been checked to be the same for all streams.
			state := getStreamDetails(t, c.streamLeader("js", sc.Name)).State
			// Gather all the streams.
			var msets []*stream
			for _, s := range c.servers {
				acc, err := s.LookupAccount("js")
				require_NoError(t, err)
				mset, err := acc.lookupStream(sc.Name)
				require_NoError(t, err)
				msets = append(msets, mset)
			}
			for seq := state.FirstSeq; seq <= state.LastSeq; seq++ {
				var msgId string
				var smv StoreMsg
				for _, mset := range msets {
					mset.mu.RLock()
					sm, err := mset.store.LoadMsg(seq, &smv)
					mset.mu.RUnlock()
					require_NoError(t, err)
					if msgId == _EMPTY_ {
						msgId = string(sm.hdr)
					} else if msgId != string(sm.hdr) {
						t.Fatalf("MsgIds do not match for seq %d: %q vs %q", seq, msgId, sm.hdr)
					}
				}
			}
		}

		// Check state of streams and consumers.
		si, err := js.StreamInfo(sc.Name)
		require_NoError(t, err)

		// Only check if there are any pending messages.
		if consumerPending > 0 {
			streamPending := int(si.State.Msgs)
			if streamPending != consumerPending {
				t.Errorf("Unexpected number of pending messages, stream=%d, consumers=%d", streamPending, consumerPending)
			}
		}

		// If clustered, check whether leader and followers have drifted.
		if sc.Replicas > 1 {
			// If we have drifted do not have to wait too long, usually its stuck for good.
			checkFor(t, time.Minute, time.Second, func() error {
				return checkState(t)
			})
			// If we succeeded now let's check that all messages are also the same.
			// We may have no messages but for tests that do we make sure each msg is the same
			// across all replicas.
			checkMsgsEqual(t)
		}

		wg.Wait()
	}

	// Setting up test variations below:
	//
	// File based with single replica and discard old policy.
	t.Run("R1F", func(t *testing.T) {
		params := &testParams{
			restartAny:     true,
			ldmRestart:     false,
			rolloutRestart: false,
			restarts:       1,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R1F",
			Subjects:    []string{"MSGS.>"},
			Replicas:    1,
			MaxAge:      30 * time.Minute,
			Duplicates:  5 * time.Minute,
			Retention:   nats.WorkQueuePolicy,
			Discard:     nats.DiscardOld,
			AllowRollup: true,
			Placement: &nats.Placement{
				Tags: []string{"test"},
			},
		})
	})

	// Clustered memory based with discard new policy and max msgs limit.
	t.Run("R3M", func(t *testing.T) {
		params := &testParams{
			restartAny:     true,
			ldmRestart:     true,
			rolloutRestart: false,
			restarts:       1,
			checkHealthz:   false,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R3M",
			Subjects:    []string{"MSGS.>"},
			Replicas:    3,
			MaxAge:      30 * time.Minute,
			MaxMsgs:     100_000,
			Duplicates:  5 * time.Minute,
			Retention:   nats.WorkQueuePolicy,
			Discard:     nats.DiscardNew,
			AllowRollup: true,
			Storage:     nats.MemoryStorage,
			Placement: &nats.Placement{
				Tags: []string{"test"},
			},
		})
	})

	// Clustered file based with discard new policy and max msgs limit.
	t.Run("R3F_DN", func(t *testing.T) {
		params := &testParams{
			restartAny:     true,
			ldmRestart:     true,
			rolloutRestart: false,
			restarts:       1,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R3F_DN",
			Subjects:    []string{"MSGS.>"},
			Replicas:    3,
			MaxAge:      30 * time.Minute,
			MaxMsgs:     100_000,
			Duplicates:  5 * time.Minute,
			Retention:   nats.WorkQueuePolicy,
			Discard:     nats.DiscardNew,
			AllowRollup: true,
			Placement: &nats.Placement{
				Tags: []string{"test"},
			},
		})
	})

	// Clustered file based with discard old policy and max msgs limit.
	t.Run("R3F_DO", func(t *testing.T) {
		params := &testParams{
			restartAny:     true,
			ldmRestart:     true,
			rolloutRestart: false,
			restarts:       1,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R3F_DO",
			Subjects:    []string{"MSGS.>"},
			Replicas:    3,
			MaxAge:      30 * time.Minute,
			MaxMsgs:     100_000,
			Duplicates:  5 * time.Minute,
			Retention:   nats.WorkQueuePolicy,
			Discard:     nats.DiscardOld,
			AllowRollup: true,
			Placement: &nats.Placement{
				Tags: []string{"test"},
			},
		})
	})

	// Clustered file based with discard old policy and no limits.
	t.Run("R3F_DO_NOLIMIT", func(t *testing.T) {
		params := &testParams{
			restartAny:       false,
			ldmRestart:       true,
			rolloutRestart:   true,
			restarts:         3,
			checkHealthz:     true,
			reconnectRoutes:  true,
			reconnectClients: true,
		}
		test(t, params, &nats.StreamConfig{
			Name:       "OWQTEST_R3F_DO_NOLIMIT",
			Subjects:   []string{"MSGS.>"},
			Replicas:   3,
			Duplicates: 30 * time.Second,
			Discard:    nats.DiscardOld,
			Placement: &nats.Placement{
				Tags: []string{"test"},
			},
		})
	})
}

func TestJetStreamClusterConsumerNRGCleanup(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:      "TEST",
		Subjects:  []string{"foo"},
		Storage:   nats.MemoryStorage,
		Retention: nats.WorkQueuePolicy,
		Replicas:  3,
	})
	require_NoError(t, err)

	// First call is just to create the pull subscribers.
	_, err = js.PullSubscribe("foo", "dlc")
	require_NoError(t, err)

	require_NoError(t, js.DeleteConsumer("TEST", "dlc"))

	// Now delete the stream.
	require_NoError(t, js.DeleteStream("TEST"))

	// Now make sure we cleaned up the NRG directories for the stream and consumer.
	var numConsumers, numStreams int
	for _, s := range c.servers {
		sd := s.JetStreamConfig().StoreDir
		nd := filepath.Join(sd, "$SYS", "_js_")
		f, err := os.Open(nd)
		require_NoError(t, err)
		dirs, err := f.ReadDir(-1)
		require_NoError(t, err)
		for _, fi := range dirs {
			if strings.HasPrefix(fi.Name(), "C-") {
				numConsumers++
			} else if strings.HasPrefix(fi.Name(), "S-") {
				numStreams++
			}
		}
	}
	require_Equal(t, numConsumers, 0)
	require_Equal(t, numStreams, 0)
}

// https://github.com/nats-io/nats-server/issues/4878
func TestClusteredInterestConsumerFilterEdit(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	s := c.randomNonLeader()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	_, err := js.AddStream(&nats.StreamConfig{
		Name:      "INTEREST",
		Retention: nats.InterestPolicy,
		Subjects:  []string{"interest.>"},
		Replicas:  3,
	})
	require_NoError(t, err)

	_, err = js.AddConsumer("INTEREST", &nats.ConsumerConfig{
		Durable:       "C0",
		FilterSubject: "interest.>",
		AckPolicy:     nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	for i := 0; i < 10; i++ {
		_, err = js.Publish(fmt.Sprintf("interest.%d", i), []byte(strconv.Itoa(i)))
		require_NoError(t, err)
	}

	// we check we got 10 messages
	nfo, err := js.StreamInfo("INTEREST")
	require_NoError(t, err)
	if nfo.State.Msgs != 10 {
		t.Fatalf("expected 10 messages got %d", nfo.State.Msgs)
	}

	// now we lower the consumer interest from all subjects to 1,
	// then check the stream state and check if interest behavior still works
	_, err = js.UpdateConsumer("INTEREST", &nats.ConsumerConfig{
		Durable:       "C0",
		FilterSubject: "interest.1",
		AckPolicy:     nats.AckExplicitPolicy,
	})
	require_NoError(t, err)

	// we should now have only one message left
	nfo, err = js.StreamInfo("INTEREST")
	require_NoError(t, err)
	if nfo.State.Msgs != 1 {
		t.Fatalf("expected 1 message got %d", nfo.State.Msgs)
	}
}

func TestJetStreamClusterDoubleAckRedelivery(t *testing.T) {
	t.Run("with-restarts", func(t *testing.T) {
		testJetStreamClusterDoubleAckRedelivery(t, true, false)
	})
	t.Run("with-leader-changes", func(t *testing.T) {
		testJetStreamClusterDoubleAckRedelivery(t, false, true)
	})
}

func testJetStreamClusterDoubleAckRedelivery(t *testing.T, restarts, leaderChanges bool) {
	conf := `
		listen: 127.0.0.1:-1
		server_name: %s
		jetstream: {
			store_dir: '%s',
		}
		cluster {
			name: %s
			listen: 127.0.0.1:%d
			routes = [%s]
		}
		server_tags: ["test"]
		system_account: sys
		no_auth_user: js
		accounts {
			sys { users = [ { user: sys, pass: sys } ] }
			js {
				jetstream = enabled
				users = [ { user: js, pass: js } ]
		    }
		}`
	c := createJetStreamClusterWithTemplate(t, conf, "R3F", 3)
	defer c.shutdown()
	for _, s := range c.servers {
		s.optsMu.Lock()
		s.opts.LameDuckDuration = 15 * time.Second
		s.opts.LameDuckGracePeriod = -15 * time.Second
		s.optsMu.Unlock()
	}
	s := c.randomNonLeader()

	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	sc, err := js.AddStream(&nats.StreamConfig{
		Name:     "LIMITS",
		Subjects: []string{"foo.>"},
		Replicas: 3,
		Storage:  nats.FileStorage,
	})
	require_NoError(t, err)

	stepDown := func() {
		_, err = nc.Request(fmt.Sprintf(JSApiStreamLeaderStepDownT, sc.Config.Name), nil, time.Second)
		if err != nil {
			t.Logf("----------> %v", err)
		}
	}
	consumerName := "ABC"
	stepDownConsumer := func() {
		_, err = nc.Request(fmt.Sprintf(JSApiConsumerLeaderStepDownT, sc.Config.Name, consumerName), nil, time.Second)
		if err != nil {
			t.Logf("----------> %v", err)
		}
	}
	testDuration := 3 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	var wg sync.WaitGroup
	producer := func(name string) {
		wg.Add(1)
		nc, js := jsClientConnect(t, s)
		defer nc.Close()
		defer wg.Done()

		i := 0
		payload := []byte(strings.Repeat("Z", 1024))
		for range time.NewTicker(1 * time.Millisecond).C {
			select {
			case <-ctx.Done():
				return
			default:
			}
			msgID := nats.MsgId(fmt.Sprintf("%s:%d", name, i))
			js.PublishAsync("foo.bar", payload, msgID, nats.RetryAttempts(10))
			i++
		}
	}
	go producer("A")
	go producer("B")
	go producer("C")

	sub, err := js.PullSubscribe("foo.bar", consumerName, nats.AckWait(5*time.Second), nats.MaxAckPending(1000), nats.PullMaxWaiting(1000))
	if err != nil {
		t.Fatal(err)
	}

	type ackResult struct {
		ack         *nats.Msg
		original    *nats.Msg
		redelivered *nats.Msg
	}
	received := make(map[string]int64)
	acked := make(map[string]*ackResult)
	rerrors := make(map[string]error)
	extraRedeliveries := 0

	// -------------------------------
	getStreamDetails := func(t *testing.T, srv *Server, streamName string) *StreamDetail {
		t.Helper()
		jsz, err := srv.Jsz(&JSzOptions{Accounts: true, Streams: true, Consumer: true})
		require_NoError(t, err)
		if len(jsz.AccountDetails) > 0 && len(jsz.AccountDetails[0].Streams) > 0 {
			details := jsz.AccountDetails[0]
			for _, stream := range details.Streams {
				if stream.Name == streamName {
					return &stream
				}
			}
			t.Error("Could not find stream details")
		}
		t.Log("Could not find account details")
		return nil
	}
	pctx, pcancel := context.WithTimeout(ctx, testDuration)
	defer pcancel()
	checkState := func(t *testing.T, streamName string, stepDownOnDrift bool) error {
		t.Helper()

		leaderSrv := c.streamLeader("js", streamName)
		if leaderSrv == nil {
			return nil
		}
		t.Logf("-------------------------------------------------------------------------------------------------------------------")
		streamLeader := getStreamDetails(t, leaderSrv, streamName)
		errs := make([]error, 0)
		t.Logf("| %-10s | %-10s | msgs:%-10d | delta:%-10d | %-10s | first:%-10d | last:%-10d |", leaderSrv.Name(), streamName, streamLeader.State.Msgs, 0, "LEADER", streamLeader.State.FirstSeq, streamLeader.State.LastSeq)
		for _, srv := range c.servers {
			if srv == leaderSrv {
				// Skip self
				continue
			}
			stream := getStreamDetails(t, srv, streamName)
			if stream == nil {
				continue
			}
			var status string
			switch {
			case streamLeader.State.Msgs > stream.State.Msgs:
				status = "DRIFT+"
			case streamLeader.State.Msgs == stream.State.Msgs:
				status = "INSYNC"
			case streamLeader.State.Msgs < stream.State.Msgs:
				status = "DRIFT-"
			}
			t.Logf("| %-10s | %-10s | msgs:%-10d | delta:%-10d | %-10s | first:%-10d | last:%-10d |", srv.Name(), streamName, stream.State.Msgs, int(streamLeader.State.Msgs)-int(stream.State.Msgs), status, stream.State.FirstSeq, stream.State.LastSeq)
			if stream.State.Msgs != streamLeader.State.Msgs {
				err := fmt.Errorf("Leader %v has %d messages, Follower %v has %d messages",
					stream.Cluster.Leader, streamLeader.State.Msgs,
					srv.Name(), stream.State.Msgs,
				)
				errs = append(errs, err)
			}
			if status == "DRIFT+" || status == "DRIFT-" {
				select {
				case <-pctx.Done():
					// Do not cause more step downs after stopping producers.
					continue
				default:
				}
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Wait until publishing is done then check again.
	StreamCheck:
		for range time.NewTicker(5 * time.Second).C {
			t.Logf("===================================================================================================================")
			select {
			case <-pctx.Done():
				break StreamCheck
			default:
			}
			checkState(t, sc.Config.Name, true)
		}
	}()

	wg.Add(1)
	go func() {
		nc, js = jsClientConnect(t, s)
		defer nc.Close()
		defer wg.Done()

		fetch := func(t *testing.T, batchSize int) {
			msgs, err := sub.Fetch(batchSize, nats.MaxWait(500*time.Millisecond))
			if err != nil {
				return
			}

			for _, msg := range msgs {
				meta, err := msg.Metadata()
				if err != nil {
					t.Error(err)
					continue
				}

				msgID := msg.Header.Get(nats.MsgIdHdr)
				// if meta.NumDelivered > 1 {
				if err, ok := rerrors[msgID]; ok {
					t.Logf("Redelivery (num_delivered: %v) after failed Ack Sync: %+v - %+v - error: %v", meta.NumDelivered, msg.Reply, msg.Header, err)
				}
				// else {
				// t.Logf("Redelivery (num_delivered: %v): %+v - %+v", meta.NumDelivered, msg.Reply, msg.Header)
				// }
				if resp, ok := acked[msgID]; ok {
					t.Errorf("Redelivery (num_delivered: %v) after successful Ack Sync: msgID:%v - redelivered:%v - original:%+v - ack:%+v",
						meta.NumDelivered, msgID, msg.Reply, resp.original.Reply, resp.ack)
					resp.redelivered = msg
					extraRedeliveries++
				}
				// }
				received[msgID]++

			Retries:
				for i := 0; i < 10; i++ {
					resp, err := nc.Request(msg.Reply, []byte("+ACK"), 500*time.Millisecond)
					if err != nil {
						t.Logf("Error: %v %v", msgID, err)
						rerrors[msgID] = err
					} else {
						acked[msgID] = &ackResult{resp, msg, nil}
						break Retries
					}
				}
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			fetch(t, 1)
			fetch(t, 50)
		}
	}()

	if leaderChanges {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hctx, hcancel := context.WithTimeout(ctx, 3*time.Minute)
			defer hcancel()
			for range time.NewTicker(5 * time.Second).C {
				select {
				case <-hctx.Done():
					return
				default:
				}
				t.Logf("STEPPING DOWN!")
				stepDown()
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			hctx, hcancel := context.WithTimeout(ctx, 3*time.Minute)
			defer hcancel()
			for range time.NewTicker(7 * time.Second).C {
				select {
				case <-hctx.Done():
					return
				default:
				}
				t.Logf("STEPPING DOWN CONSUMER LEADER!")
				stepDownConsumer()
			}
		}()
	}

	// Let messages be produced before introducing more conditions to test.
	<-time.After(15 * time.Second)

	if restarts {
		// Cause a couple of step downs before the restarts as well.
		time.AfterFunc(5*time.Second, func() { stepDown() })
		time.AfterFunc(10*time.Second, func() { stepDown() })

	NextServer:
		for _, s := range c.servers {
			s.lameDuckMode()
			s.WaitForShutdown()
			s = c.restartServer(s)

			hctx, hcancel := context.WithTimeout(ctx, 60*time.Second)
			defer hcancel()
			for range time.NewTicker(2 * time.Second).C {
				select {
				case <-hctx.Done():
					t.Logf("WRN: Timed out waiting for healthz from %s", s)
					continue NextServer
				default:
				}

				status := s.healthz(nil)
				if status.StatusCode == 200 {
					continue NextServer
				}
			}
			// Pause in-between server restarts.
			time.Sleep(10 * time.Second)
		}
	}
	// -------------------------------

	// Stop all producer and consumer goroutines to check results.
	if restarts {
		cancel()
	}
	<-ctx.Done()
	wg.Wait()
	if extraRedeliveries > 0 {
		t.Fatalf("Received %v redeliveries after a successful ack", extraRedeliveries)
	}
}

func TestJetStreamClusterBusyStreams(t *testing.T) {
	t.Skip("Too long for CI at the moment")
	type streamSetup struct {
		config    *nats.StreamConfig
		consumers []*nats.ConsumerConfig
		subjects  []string
	}
	type job func(t *testing.T, nc *nats.Conn, js nats.JetStreamContext, c *cluster)
	type testParams struct {
		cluster         string
		streams         []*streamSetup
		producers       int
		consumers       int
		restartAny      bool
		restartWait     time.Duration
		ldmRestart      bool
		rolloutRestart  bool
		restarts        int
		checkHealthz    bool
		jobs            []job
		expect          job
		duration        time.Duration
		producerMsgs    int
		producerMsgSize int
	}
	test := func(t *testing.T, test *testParams) {
		conf := `
                listen: 127.0.0.1:-1
                http: 127.0.0.1:-1
                server_name: %s
                jetstream: {
                        domain: "cloud"
                        store_dir: '%s',
                }
                cluster {
                        name: %s
                        listen: 127.0.0.1:%d
                        routes = [%s]
                }
                server_tags: ["test"]
                system_account: sys

                no_auth_user: js
                accounts {
                        sys { users = [ { user: sys, pass: sys } ] }

                        js  { jetstream = enabled
                              users = [ { user: js, pass: js } ]
                        }
                }`
		c := createJetStreamClusterWithTemplate(t, conf, test.cluster, 3)
		defer c.shutdown()
		for _, s := range c.servers {
			s.optsMu.Lock()
			s.opts.LameDuckDuration = 15 * time.Second
			s.opts.LameDuckGracePeriod = -15 * time.Second
			s.optsMu.Unlock()
		}

		nc, js := jsClientConnect(t, c.randomServer())
		defer nc.Close()

		var wg sync.WaitGroup
		for _, stream := range test.streams {
			stream := stream
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := js.AddStream(stream.config)
				require_NoError(t, err)

				for _, consumer := range stream.consumers {
					_, err := js.AddConsumer(stream.config.Name, consumer)
					require_NoError(t, err)
				}
			}()
		}
		wg.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), test.duration)
		defer cancel()
		for _, stream := range test.streams {
			payload := []byte(strings.Repeat("A", test.producerMsgSize))
			stream := stream
			subjects := stream.subjects

			// Create publishers on different connections that sends messages
			// to all the consumers subjects.
			var n atomic.Uint64
			for i := 0; i < test.producers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					nc, js := jsClientConnect(t, c.randomServer())
					defer nc.Close()

					for range time.NewTicker(1 * time.Millisecond).C {
						select {
						case <-ctx.Done():
							return
						default:
						}

						for _, subject := range subjects {
							msgID := nats.MsgId(fmt.Sprintf("n:%d", n.Load()))
							_, err := js.Publish(subject, payload, nats.AckWait(200*time.Millisecond), msgID)
							if err == nil {
								if nn := n.Add(1); int(nn) >= test.producerMsgs {
									return
								}
							}
						}
					}
				}()
			}

			// Create multiple parallel pull subscribers per consumer config.
			for i := 0; i < test.consumers; i++ {
				for _, consumer := range stream.consumers {
					wg.Add(1)

					consumer := consumer
					go func() {
						defer wg.Done()

						for attempts := 0; attempts < 60; attempts++ {
							_, err := js.ConsumerInfo(stream.config.Name, consumer.Name)
							if err != nil {
								t.Logf("WRN: Failed creating pull subscriber: %v - %v - %v - %v",
									consumer.FilterSubject, stream.config.Name, consumer.Name, err)
							}
						}
						sub, err := js.PullSubscribe(consumer.FilterSubject, "", nats.Bind(stream.config.Name, consumer.Name))
						if err != nil {
							t.Logf("WRN: Failed creating pull subscriber: %v - %v - %v - %v",
								consumer.FilterSubject, stream.config.Name, consumer.Name, err)
							return
						}
						require_NoError(t, err)

						for range time.NewTicker(100 * time.Millisecond).C {
							select {
							case <-ctx.Done():
								return
							default:
							}

							msgs, err := sub.Fetch(1, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}

							msgs, err = sub.Fetch(100, nats.MaxWait(200*time.Millisecond))
							if err != nil {
								continue
							}
							for _, msg := range msgs {
								msg.Ack()
							}
						}
					}()
				}
			}
		}

		for _, job := range test.jobs {
			go job(t, nc, js, c)
		}
		if test.restarts > 0 {
			wg.Add(1)
			time.AfterFunc(test.restartWait, func() {
				defer wg.Done()
				for i := 0; i < test.restarts; i++ {
					switch {
					case test.restartAny:
						s := c.servers[rand.Intn(len(c.servers))]
						if test.ldmRestart {
							s.lameDuckMode()
						} else {
							s.Shutdown()
						}
						s.WaitForShutdown()
						c.restartServer(s)
					case test.rolloutRestart:
						for _, s := range c.servers {
							if test.ldmRestart {
								s.lameDuckMode()
							} else {
								s.Shutdown()
							}
							s.WaitForShutdown()
							s = c.restartServer(s)

							if test.checkHealthz {
								hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
								defer hcancel()

							Healthz:
								for range time.NewTicker(2 * time.Second).C {
									select {
									case <-hctx.Done():
										break Healthz
									default:
									}

									status := s.healthz(nil)
									if status.StatusCode == 200 {
										break Healthz
									}
								}
							}
						}
					}
					c.waitOnClusterReady()
				}
			})
		}
		test.expect(t, nc, js, c)
		cancel()
		wg.Wait()
	}
	stepDown := func(nc *nats.Conn, streamName string) {
		nc.Request(fmt.Sprintf(JSApiStreamLeaderStepDownT, streamName), nil, time.Second)
	}
	getStreamDetails := func(t *testing.T, c *cluster, accountName, streamName string) *StreamDetail {
		t.Helper()
		srv := c.streamLeader(accountName, streamName)
		jsz, err := srv.Jsz(&JSzOptions{Accounts: true, Streams: true, Consumer: true})
		require_NoError(t, err)
		for _, acc := range jsz.AccountDetails {
			if acc.Name == accountName {
				for _, stream := range acc.Streams {
					if stream.Name == streamName {
						return &stream
					}
				}
			}
		}
		t.Error("Could not find account details")
		return nil
	}
	checkMsgsEqual := func(t *testing.T, c *cluster, accountName, streamName string) {
		state := getStreamDetails(t, c, accountName, streamName).State
		var msets []*stream
		for _, s := range c.servers {
			acc, err := s.LookupAccount(accountName)
			require_NoError(t, err)
			mset, err := acc.lookupStream(streamName)
			require_NoError(t, err)
			msets = append(msets, mset)
		}
		for seq := state.FirstSeq; seq <= state.LastSeq; seq++ {
			var msgId string
			var smv StoreMsg
			for _, mset := range msets {
				mset.mu.RLock()
				sm, err := mset.store.LoadMsg(seq, &smv)
				mset.mu.RUnlock()
				require_NoError(t, err)
				if msgId == _EMPTY_ {
					msgId = string(sm.hdr)
				} else if msgId != string(sm.hdr) {
					t.Fatalf("MsgIds do not match for seq %d: %q vs %q", seq, msgId, sm.hdr)
				}
			}
		}
	}
	checkConsumer := func(t *testing.T, c *cluster, accountName, streamName, consumerName string) {
		t.Helper()
		var leader string
		for _, s := range c.servers {
			jsz, err := s.Jsz(&JSzOptions{Accounts: true, Streams: true, Consumer: true})
			require_NoError(t, err)
			for _, acc := range jsz.AccountDetails {
				if acc.Name == accountName {
					for _, stream := range acc.Streams {
						if stream.Name == streamName {
							for _, consumer := range stream.Consumer {
								if leader == "" {
									leader = consumer.Cluster.Leader
								} else if leader != consumer.Cluster.Leader {
									t.Errorf("There are two leaders for %s/%s: %s vs %s",
										stream.Name, consumer.Name, leader, consumer.Cluster.Leader)
								}
							}
						}
					}
				}
			}
		}
	}

	t.Run("R1F/rescale/R3F/sources:10/limits", func(t *testing.T) {
		testDuration := 3 * time.Minute
		totalStreams := 10
		streams := make([]*streamSetup, totalStreams)
		sources := make([]*nats.StreamSource, totalStreams)
		for i := 0; i < totalStreams; i++ {
			name := fmt.Sprintf("test:%d", i)
			st := &streamSetup{
				config: &nats.StreamConfig{
					Name:      name,
					Subjects:  []string{fmt.Sprintf("test.%d.*", i)},
					Replicas:  1,
					Retention: nats.LimitsPolicy,
				},
			}
			st.subjects = append(st.subjects, fmt.Sprintf("test.%d.0", i))
			sources[i] = &nats.StreamSource{Name: name}
			streams[i] = st
		}

		// Create Source consumer.
		sourceSetup := &streamSetup{
			config: &nats.StreamConfig{
				Name:      "source-test",
				Replicas:  1,
				Retention: nats.LimitsPolicy,
				Sources:   sources,
			},
			consumers: make([]*nats.ConsumerConfig, 0),
		}
		cc := &nats.ConsumerConfig{
			Name:          "A",
			Durable:       "A",
			FilterSubject: "test.>",
			AckPolicy:     nats.AckExplicitPolicy,
		}
		sourceSetup.consumers = append(sourceSetup.consumers, cc)
		streams = append(streams, sourceSetup)

		scale := func(replicas int, wait time.Duration) job {
			return func(t *testing.T, nc *nats.Conn, js nats.JetStreamContext, c *cluster) {
				config := sourceSetup.config
				time.AfterFunc(wait, func() {
					config.Replicas = replicas
					for i := 0; i < 10; i++ {
						_, err := js.UpdateStream(config)
						if err == nil {
							return
						}
						time.Sleep(1 * time.Second)
					}
				})
			}
		}

		expect := func(t *testing.T, nc *nats.Conn, js nats.JetStreamContext, c *cluster) {
			// The source stream should not be stuck or be different from the other streams.
			time.Sleep(testDuration + 1*time.Minute)
			accName := "js"
			streamName := "source-test"

			// Check a few times to see if there are no changes in the number of messages.
			var changed bool
			var prevMsgs uint64
			for i := 0; i < 10; i++ {
				sinfo, err := js.StreamInfo(streamName)
				if err != nil {
					t.Logf("Error: %v", err)
					time.Sleep(2 * time.Second)
					continue
				}
				prevMsgs = sinfo.State.Msgs
			}
			for i := 0; i < 10; i++ {
				sinfo, err := js.StreamInfo(streamName)
				if err != nil {
					t.Logf("Error: %v", err)
					time.Sleep(2 * time.Second)
					continue
				}
				changed = prevMsgs != sinfo.State.Msgs
				prevMsgs = sinfo.State.Msgs
				time.Sleep(2 * time.Second)
			}
			if !changed {
				// Doing a leader step down should not cause the messages to change.
				stepDown(nc, streamName)

				for i := 0; i < 10; i++ {
					sinfo, err := js.StreamInfo(streamName)
					if err != nil {
						t.Logf("Error: %v", err)
						time.Sleep(2 * time.Second)
						continue
					}
					changed = prevMsgs != sinfo.State.Msgs
					prevMsgs = sinfo.State.Msgs
					time.Sleep(2 * time.Second)
				}
				if changed {
					t.Error("Stream msgs changed after the step down")
				}
			}

			/////////////////////////////////////////////////////////////////////////////////////////
			//                                                                                     //
			//  The number of messages sourced should match the count from all the other streams.  //
			//                                                                                     //
			/////////////////////////////////////////////////////////////////////////////////////////
			var expectedMsgs uint64
			for i := 0; i < totalStreams; i++ {
				name := fmt.Sprintf("test:%d", i)
				sinfo, err := js.StreamInfo(name)
				require_NoError(t, err)
				expectedMsgs += sinfo.State.Msgs
			}
			sinfo, err := js.StreamInfo(streamName)
			require_NoError(t, err)

			gotMsgs := sinfo.State.Msgs
			if gotMsgs != expectedMsgs {
				t.Errorf("stream with sources has %v messages, but total sourced messages should be %v", gotMsgs, expectedMsgs)
			}
			checkConsumer(t, c, accName, streamName, "A")
			checkMsgsEqual(t, c, accName, streamName)
		}
		test(t, &testParams{
			cluster:        t.Name(),
			streams:        streams,
			producers:      10,
			consumers:      10,
			restarts:       1,
			rolloutRestart: true,
			ldmRestart:     true,
			checkHealthz:   true,
			// TODO(dlc) - If this overlaps with the scale jobs this test will fail.
			// Leaders will be elected with partial state.
			restartWait: 65 * time.Second,
			jobs: []job{
				scale(3, 15*time.Second),
				scale(1, 30*time.Second),
				scale(3, 60*time.Second),
			},
			expect:          expect,
			duration:        testDuration,
			producerMsgSize: 1024,
			producerMsgs:    100_000,
		})
	})

	t.Run("rollouts", func(t *testing.T) {
		shared := func(t *testing.T, sc *nats.StreamConfig, tp *testParams) func(t *testing.T) {
			return func(t *testing.T) {
				testDuration := 3 * time.Minute
				totalStreams := 30
				consumersPerStream := 5
				streams := make([]*streamSetup, totalStreams)
				for i := 0; i < totalStreams; i++ {
					name := fmt.Sprintf("test:%d", i)
					st := &streamSetup{
						config: &nats.StreamConfig{
							Name:      name,
							Subjects:  []string{fmt.Sprintf("test.%d.*", i)},
							Replicas:  3,
							Discard:   sc.Discard,
							Retention: sc.Retention,
							Storage:   sc.Storage,
							MaxMsgs:   sc.MaxMsgs,
							MaxBytes:  sc.MaxBytes,
							MaxAge:    sc.MaxAge,
						},
						consumers: make([]*nats.ConsumerConfig, 0),
					}
					for j := 0; j < consumersPerStream; j++ {
						subject := fmt.Sprintf("test.%d.%d", i, j)
						name := fmt.Sprintf("A:%d:%d", i, j)
						cc := &nats.ConsumerConfig{
							Name:          name,
							Durable:       name,
							FilterSubject: subject,
							AckPolicy:     nats.AckExplicitPolicy,
						}
						st.consumers = append(st.consumers, cc)
						st.subjects = append(st.subjects, subject)
					}
					streams[i] = st
				}
				expect := func(t *testing.T, nc *nats.Conn, js nats.JetStreamContext, c *cluster) {
					time.Sleep(testDuration + 1*time.Minute)
					accName := "js"
					for i := 0; i < totalStreams; i++ {
						streamName := fmt.Sprintf("test:%d", i)
						checkMsgsEqual(t, c, accName, streamName)
					}
				}
				test(t, &testParams{
					cluster:         t.Name(),
					streams:         streams,
					producers:       10,
					consumers:       10,
					restarts:        tp.restarts,
					rolloutRestart:  tp.rolloutRestart,
					ldmRestart:      tp.ldmRestart,
					checkHealthz:    tp.checkHealthz,
					restartWait:     tp.restartWait,
					expect:          expect,
					duration:        testDuration,
					producerMsgSize: 1024,
					producerMsgs:    100_000,
				})
			}
		}
		for prefix, st := range map[string]nats.StorageType{"R3F": nats.FileStorage, "R3M": nats.MemoryStorage} {
			t.Run(prefix, func(t *testing.T) {
				for rolloutType, params := range map[string]*testParams{
					// Rollouts using graceful restarts and checking healthz.
					"ldm": {
						restarts:       1,
						rolloutRestart: true,
						ldmRestart:     true,
						checkHealthz:   true,
						restartWait:    45 * time.Second,
					},
					// Non graceful restarts calling Shutdown, but using healthz on startup.
					"term": {
						restarts:       1,
						rolloutRestart: true,
						ldmRestart:     false,
						checkHealthz:   true,
						restartWait:    45 * time.Second,
					},
				} {
					t.Run(rolloutType, func(t *testing.T) {
						t.Run("limits", shared(t, &nats.StreamConfig{
							Retention: nats.LimitsPolicy,
							Storage:   st,
						}, params))
						t.Run("wq", shared(t, &nats.StreamConfig{
							Retention: nats.WorkQueuePolicy,
							Storage:   st,
						}, params))
						t.Run("interest", shared(t, &nats.StreamConfig{
							Retention: nats.InterestPolicy,
							Storage:   st,
						}, params))
						t.Run("limits:dn:max-per-subject", shared(t, &nats.StreamConfig{
							Retention:         nats.LimitsPolicy,
							Storage:           st,
							MaxMsgsPerSubject: 1,
							Discard:           nats.DiscardNew,
						}, params))
						t.Run("wq:dn:max-msgs", shared(t, &nats.StreamConfig{
							Retention: nats.WorkQueuePolicy,
							Storage:   st,
							MaxMsgs:   10_000,
							Discard:   nats.DiscardNew,
						}, params))
						t.Run("wq:dn-per-subject:max-msgs", shared(t, &nats.StreamConfig{
							Retention:            nats.WorkQueuePolicy,
							Storage:              st,
							MaxMsgs:              10_000,
							MaxMsgsPerSubject:    100,
							Discard:              nats.DiscardNew,
							DiscardNewPerSubject: true,
						}, params))
					})
				}
			})
		}
	})
}

func TestJetStreamClusterRouteDisconnectionRecovery(t *testing.T) {
	t.Run("limits", func(t *testing.T) {
		streams := 10
		consumers := 4
		producers := 40
		testJetStreamClusterRouteDisconnectionRecovery(t, streams, consumers, producers, nats.StreamConfig{
			Replicas:   3,
			MaxAge:     3 * time.Minute,
			Duplicates: 2 * time.Minute,
		})
	})
}

func testJetStreamClusterRouteDisconnectionRecovery(t *testing.T, streams, consumers, producers int, sc nats.StreamConfig) {
	conf := `
	listen: 127.0.0.1:-1
	server_name: %s
	jetstream: {
		store_dir: '%s',
	}
	cluster {
		name: "%s"
		listen: 127.0.0.1:%d
		routes = [%s]
	}
        system_account: sys
        no_auth_user: js
	accounts {
	  sys {
	    users = [
	      { user: sys, pass: sys }
	    ]
	  }
	  js {
	    jetstream = enabled
	    users = [
	      { user: js, pass: js }
	    ]
	  }
	}`
	c := createJetStreamClusterWithTemplate(t, conf, "hmsg", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	cnc, cjs := jsClientConnect(t, c.randomServer())
	defer cnc.Close()

	// Create the streams.
	for i := 0; i < streams; i++ {
		stream := sc
		stream.Name = fmt.Sprintf("STREAM_%d", i)
		stream.Subjects = []string{fmt.Sprintf("messages.%d.*", i)}
		_, err := js.AddStream(&stream)
		require_NoError(t, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	stepDown := func(streamName string) {
		nc.Request(fmt.Sprintf(JSApiStreamLeaderStepDownT, streamName), nil, time.Second)
	}
	checkHealthz := func(t *testing.T) {
		t.Helper()
		for _, srv := range c.servers {
			rerr := srv.healthz(nil)
			if rerr != nil {
				t.Logf("Healthz: %s - %v", srv.Name(), rerr)
			}
		}
	}
	getStreamDetails := func(t *testing.T, srv *Server, streamName string) *StreamDetail {
		t.Helper()
		jsz, err := srv.Jsz(&JSzOptions{Accounts: true, Streams: true, Consumer: true})
		require_NoError(t, err)
		if len(jsz.AccountDetails) > 0 && len(jsz.AccountDetails[0].Streams) > 0 {
			details := jsz.AccountDetails[0]
			for _, stream := range details.Streams {
				if stream.Name == streamName {
					return &stream
				}
			}
			t.Error("Could not find stream details")
		}
		t.Error("Could not find account details")
		return nil
	}
	pctx, pcancel := context.WithTimeout(ctx, 1*time.Minute)
	defer pcancel()
	checkState := func(t *testing.T, streamName string, stepDownOnDrift bool) error {
		t.Helper()

		leaderSrv := c.streamLeader("js", streamName)
		if leaderSrv == nil {
			return nil
		}
		t.Logf("-------------------------------------------------------------------------------------------------------------------")
		streamLeader := getStreamDetails(t, leaderSrv, streamName)
		errs := make([]error, 0)
		t.Logf("| %-10s | %-10s | msgs:%-10d | delta:%-10d | %-10s | first:%-10d | last:%-10d |", leaderSrv.Name(), streamName, streamLeader.State.Msgs, 0, "LEADER", streamLeader.State.FirstSeq, streamLeader.State.LastSeq)
		for _, srv := range c.servers {
			if srv == leaderSrv {
				// Skip self
				continue
			}
			stream := getStreamDetails(t, srv, streamName)
			if stream == nil {
				continue
			}
			var status string
			switch {
			case streamLeader.State.Msgs > stream.State.Msgs:
				status = "DRIFT+"
			case streamLeader.State.Msgs == stream.State.Msgs:
				status = "INSYNC"
			case streamLeader.State.Msgs < stream.State.Msgs:
				status = "DRIFT-"
			}
			t.Logf("| %-10s | %-10s | msgs:%-10d | delta:%-10d | %-10s | first:%-10d | last:%-10d |", srv.Name(), streamName, stream.State.Msgs, int(streamLeader.State.Msgs)-int(stream.State.Msgs), status, stream.State.FirstSeq, stream.State.LastSeq)
			if stream.State.Msgs != streamLeader.State.Msgs {
				err := fmt.Errorf("Leader %v has %d messages, Follower %v has %d messages",
					stream.Cluster.Leader, streamLeader.State.Msgs,
					srv.Name(), stream.State.Msgs,
				)
				errs = append(errs, err)
			}
			if status == "DRIFT+" || status == "DRIFT-" {
				select {
				case <-pctx.Done():
					// Do not cause more step downs after stopping producers.
					continue
				default:
				}
				if stepDownOnDrift {
					stepDown(streamName)
				}
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}

	var wg sync.WaitGroup
	type dupPair struct {
		subject string
		msgid   string
		latest  *nats.Msg
		past    *nats.Msg
	}

	type cmap struct {
		subject  string
		consumer string
		received map[string]*nats.Msg
		state    *nats.ConsumerInfo
	}

	dups := make(chan *dupPair, 64000)
	cmaps := make(chan *cmap, streams*producers*5)
	addConsumer := func(s, c, n int) {
		var receivedMap = make(map[string]*nats.Msg)
		consumerName := fmt.Sprintf("s:%d_c:%d_n:%d", s, c, n)
		subject := fmt.Sprintf("messages.%d.%d", s, c)
		psub, err := cjs.PullSubscribe(subject, consumerName)
		require_NoError(t, err)

		defer func() {
			state, _ := psub.ConsumerInfo()
			t.Logf("CONSUMER[%d] %v/%v :: GOT: %v - %+v", n, subject, consumerName, len(receivedMap), state)
			cmaps <- &cmap{subject, consumerName, receivedMap, state}
		}()

		tick := time.NewTicker(20 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			case <-tick.C:
				msgs, err := psub.Fetch(10, nats.MaxWait(200*time.Millisecond))
				if err != nil {
					// The consumer will continue to timeout here eventually.
					continue
				}

				// NextMsg:
				for _, msg := range msgs {
					msgid := msg.Header.Get("Nats-Msg-Id")
					if pastMsg, ok := receivedMap[msgid]; !ok {
						receivedMap[msgid] = msg
					} else {
						meta1, _ := msg.Metadata()
						meta2, _ := pastMsg.Metadata()
						if meta1.NumDelivered == 1 {
							t.Logf("DUPLICATE: %s || \n %+v || %v || %+v\nPAST: %v || %v || %v || %+v", msgid, msg.Subject, msg.Reply, meta1, pastMsg.Subject, pastMsg.Reply, pastMsg.Header.Get("Nats-Msg-Id"), meta2)
							dups <- &dupPair{msg.Subject, msgid, msg, pastMsg}

							// NOTE: Do not ack these?
							// continue NextMsg
						}
					}
					aerr := msg.Ack()
					if aerr != nil {
						t.Logf("Ack Errored for %v", msg.Reply)
					}
				}
			}
		}
	}

	// Setup multiple consumers fetching the same set of messages.
	for stream := 0; stream < streams; stream++ {
		for consumer := 0; consumer < consumers; consumer++ {
			for n := 0; n < 5; n++ {
				wg.Add(1)
				go addConsumer(stream, consumer, n)
			}
		}
	}

	// Do health checks on start which is what we do in k8s.
	wg.Add(1)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		for range time.NewTicker(5 * time.Second).C {
			select {
			case <-ctx.Done():
				wg.Done()
				// t.Logf("Stop checking healthz")
				return
			default:
			}
			checkHealthz(t)
		}
	}()

	producer := func(tctx context.Context, async bool) {
		wg.Add(1)

		nc, ljs := jsClientConnect(t, c.randomServer())
		defer nc.Close()

		producerid := nuid.Next()
		if !async {
			producerid = fmt.Sprintf("%s_SYNC", producerid)
		}
		payload := []byte(strings.Repeat("A", 1024*2))
		tick := time.NewTicker(1 * time.Millisecond)
		for i := 1; i < 50000; i++ {
			select {
			case <-tctx.Done():
				// t.Logf("Stopped publishing")
				wg.Done()
				return
			case <-tick.C:
				for stream := 0; stream < streams; stream++ {
					for consumer := 0; consumer < consumers; consumer++ {
						subject := fmt.Sprintf("messages.%d.%d", stream, consumer)
						msgid := fmt.Sprintf("s:%d_c:%d_i:%d_%s", stream, consumer, i, producerid)
						// Retry until it works'
					Attempts:
						for {
							if async {
								// Publish very fast, let it fail and get stalled publishing the same msg id as needed.
								_, err := ljs.PublishAsync(subject, payload, nats.RetryAttempts(30), nats.MsgId(msgid))
								if err != nil {
									select {
									case <-ljs.PublishAsyncComplete():
									case <-tctx.Done():
										wg.Done()
										return
									}
									continue Attempts
								}
								break Attempts
							} else {
								_, err := ljs.Publish(subject, payload, nats.RetryAttempts(10), nats.MsgId(msgid), nats.AckWait(500*time.Millisecond))
								if err != nil {
									t.Logf("ERROR: %v (%s:%s)", err, subject, msgid)
									continue Attempts
								}
								break Attempts
							}
						}
					}
				}
			}
		}
	}

	// Start parallel producers on different connections, allow some time for things to settle.
	time.Sleep(10 * time.Second)
	for i := 0; i < producers; i++ {
		go producer(pctx, false)
	}

	// Periodically disconnect one of the servers.
	wg.Add(1)
	go func() {
		for range time.NewTicker(10 * time.Second).C {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			default:
			}

			// Make sure there are stream leaders.
			for i := 0; i < streams; i++ {
				c.waitOnStreamLeader("js", fmt.Sprintf("STREAM_%d", i))
			}

			s := c.servers[rand.Intn(2)]
			var routes []*client
			s.mu.Lock()
			for _, conns := range s.routes {
				for _, r := range conns {
					routes = append(routes, r)
				}
			}
			s.mu.Unlock()

			// Force disconnect routes from first server.
			for _, r := range routes {
				r.closeConnection(ClientClosed)
			}
		}
	}()

	// Restart and wait on stream leaders.
	// time.AfterFunc(30*time.Second, func() {
	// 	c.lameDuckRestartAll()
	// 	for i := 0; i < streams; i++ {
	// 		c.waitOnStreamLeader("js", fmt.Sprintf("STREAM_%d", i))
	// 	}
	// })

	// Wait until publishing is done then check again.
StreamCheck:
	for range time.NewTicker(5 * time.Second).C {
		t.Logf("===================================================================================================================")
		select {
		case <-pctx.Done():
			break StreamCheck
		default:
		}
		for stream := 0; stream < streams; stream++ {
			streamName := fmt.Sprintf("STREAM_%d", stream)
			// Check the state and cause a leader election whenever a drift is detected.
			checkState(t, streamName, true)
		}
	}

	// Check the state from all streams a few times.
	var driftRecovered bool
	for i := 0; i < 5; i++ {
		var drift bool
		for stream := 0; stream < streams; stream++ {
			streamName := fmt.Sprintf("STREAM_%d", stream)
			leaderSrv := c.streamLeader("js", streamName)
			if leaderSrv == nil {
				t.Errorf("Stream has no leader %v", streamName)
				continue
			}
			streamLeader := getStreamDetails(t, leaderSrv, streamName)
			if streamLeader == nil {
				t.Errorf("Stream has no leader %v", streamName)
				continue
			}
			for _, srv := range c.servers {
				stream := getStreamDetails(t, srv, streamName)
				if stream.State.Msgs != streamLeader.State.Msgs {
					t.Logf("DRIFT %s (%s): follower has %d msgs but leader has %d", streamName, srv.Name(), stream.State.Msgs, streamLeader.State.Msgs)
					drift = true
				}
			}
			// checkState(t, streamName, false)
		}
		if !drift {
			t.Logf("There is no drift")
			driftRecovered = true
			break
		}
		time.Sleep(10 * time.Second)
	}
	if !driftRecovered {
		t.Errorf("Drift among replicas did not recover")
	}

	// Start publishing again at a better pace with synchronous subscribers.
	t.Logf("Resume publishing")
	nctx, ncancel := context.WithTimeout(ctx, 2*time.Minute)
	defer ncancel()

	for i := 0; i < 5; i++ {
		go producer(nctx, false)
	}

	// Check state of the streams.
Ticker:
	for range time.NewTicker(5 * time.Second).C {
		select {
		case <-nctx.Done():
			break Ticker
		default:
		}
		t.Logf("---------------------------------------------------------------------------------------------------------------")
		for stream := 0; stream < streams; stream++ {
			streamName := fmt.Sprintf("STREAM_%d", stream)
			checkState(t, streamName, false)
		}
	}
	totalDups := len(dups)
	if len(dups) > 0 {
		t.Errorf("Got duplicates with same msg id: %d", totalDups)
	}
	close(dups)
	for dup := range dups {
		t.Logf("MSG: %s / %s", dup.subject, dup.msgid)
		t0, _ := dup.past.Metadata()
		t1, _ := dup.latest.Metadata()
		t.Logf("   [1] - %+v", t0)
		t.Logf("   [2] - %+v", t1)
	}

	// Goroutines should be exiting now...
	cancel()

	t.Logf("Stopping.")
	wg.Wait()

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(map[string][]*cmap)

CheckResults:
	for {
		select {
		case <-ctx.Done():
			break CheckResults
		case cm := <-cmaps:
			if _, ok := results[cm.subject]; !ok {
				results[cm.subject] = []*cmap{cm}
			} else {
				results[cm.subject] = append(results[cm.subject], cm)
			}
		}
	}

	// Check all results
	for k, v := range results {
		t.Logf("On subject: %v:", k)
		for _, vv := range v {
			for msgID, _ := range vv.received {
				for _, vvv := range v {
					if _, ok := vvv.received[msgID]; !ok {
						t.Logf("\t[%v/%v] Could not find msgID %v being delivered to %v/%v", vv.subject, vv.consumer, msgID, vvv.subject, vvv.consumer)
					}
				}
			}
		}
	}
}

func TestJetStreamClusterCLFSOnDuplicatesTriggersStreamReset(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	nc2, js2 := jsClientConnect(t, c.randomServer())
	defer nc2.Close()

	streamName := "TESTW2"
	_, err := js.AddStream(&nats.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"foo"},
		Replicas:   3,
		Storage:    nats.FileStorage,
		MaxAge:     3 * time.Minute,
		Duplicates: 2 * time.Minute,
	})
	require_NoError(t, err)

	// Give the stream to be ready.
	time.Sleep(3 * time.Second)

	var wg sync.WaitGroup

	// The test will be successful if it runs for this long without dup issues.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	// -----------------------------------------------------------------------------------------------------------------------------
	checkHealthz := func(t *testing.T) {
		t.Helper()
		for _, srv := range c.servers {
			if srv == nil {
				continue
			}
			rerr := srv.healthz(nil)
			if rerr != nil {
				if srv == nil {
					continue
				}
				t.Logf("Healthz: %s - %v", srv.Name(), rerr)
			}
		}
	}
	getStreamDetails := func(t *testing.T, srv *Server, streamName string) *StreamDetail {
		t.Helper()
		jsz, err := srv.Jsz(&JSzOptions{Accounts: true, Streams: true, Consumer: true})
		require_NoError(t, err)
		if len(jsz.AccountDetails) > 0 && len(jsz.AccountDetails[0].Streams) > 0 {
			details := jsz.AccountDetails[0]
			for _, stream := range details.Streams {
				if stream.Name == streamName {
					return &stream
				}
			}
			t.Error("Could not find stream details")
		}
		t.Error("Could not find account details")
		return nil
	}
	checkState := func(t *testing.T, streamName string) error {
		t.Helper()

		leaderSrv := c.streamLeader("$G", streamName)
		if leaderSrv == nil {
			return nil
		}
		t.Logf("-------------------------------------------------------------------------------------------------------------------")
		streamLeader := getStreamDetails(t, leaderSrv, streamName)
		errs := make([]error, 0)
		t.Logf("| %-10s | %-10s | msgs:%-10d | delta:%-10d | %-10s | first:%-10d | last:%-10d |", leaderSrv.Name(), streamName, streamLeader.State.Msgs, 0, "LEADER", streamLeader.State.FirstSeq, streamLeader.State.LastSeq)
		for _, srv := range c.servers {
			if srv == leaderSrv {
				// Skip self
				continue
			}
			stream := getStreamDetails(t, srv, streamName)
			if stream == nil {
				continue
			}
			var status string
			switch {
			case streamLeader.State.Msgs > stream.State.Msgs:
				status = "DRIFT+"
			case streamLeader.State.Msgs == stream.State.Msgs:
				status = "INSYNC"
			case streamLeader.State.Msgs < stream.State.Msgs:
				status = "DRIFT-"
			}
			t.Logf("| %-10s | %-10s | msgs:%-10d | delta:%-10d | %-10s | first:%-10d | last:%-10d |", srv.Name(), streamName, stream.State.Msgs, int(streamLeader.State.Msgs)-int(stream.State.Msgs), status, stream.State.FirstSeq, stream.State.LastSeq)
			if stream.State.Msgs != streamLeader.State.Msgs {
				err := fmt.Errorf("Leader %v has %d messages, Follower %v has %d messages",
					stream.Cluster.Leader, streamLeader.State.Msgs,
					srv.Name(), stream.State.Msgs,
				)
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}

	go func() {
		for range time.NewTicker(1 * time.Second).C {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			default:
			}
			checkHealthz(t)
			checkState(t, streamName)
		}
	}()
	wg.Add(1)

	go func() {
		tick := time.NewTicker(10 * time.Second)
		for {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			case <-tick.C:
				c.streamLeader(globalAccountName, streamName).JetStreamStepdownStream(globalAccountName, streamName)
			}
		}
	}()
	wg.Add(1)

	for i := 0; i < 5; i++ {
		go func(i int) {
			var err error
			sub, err := js2.PullSubscribe("foo", fmt.Sprintf("A:%d", i))
			require_NoError(t, err)

			for {
				select {
				case <-ctx.Done():
					wg.Done()
					return
				default:
				}

				msgs, err := sub.Fetch(100, nats.MaxWait(200*time.Millisecond))
				if err != nil {
					continue
				}
				for _, msg := range msgs {
					msg.Ack()
				}
			}
		}(i)
		wg.Add(1)
	}

	// Sync producer that only does a couple of duplicates, cancel the test
	// if we get too many errors without responses.
	errCh := make(chan error, 10)
	go func() {
		// Try sync publishes normally in this state and see if it times out.
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			default:
			}

			var succeeded bool
			var failures int
			for n := 0; n < 10; n++ {
				_, err := js.Publish("foo", []byte("test"), nats.MsgId(fmt.Sprintf("sync:checking:%d", i)), nats.RetryAttempts(30), nats.AckWait(500*time.Millisecond))
				if err != nil {
					failures++
					continue
				}
				succeeded = true
			}
			if !succeeded {
				errCh <- fmt.Errorf("Too many publishes failed with timeout: failures=%d, i=%d", failures, i)
			}
		}
	}()
	wg.Add(1)

Loop:
	for n := uint64(0); true; n++ {
		select {
		case <-ctx.Done():
			break Loop
		case e := <-errCh:
			t.Error(e)
			break Loop
		default:

		}
		// Cause a lot of duplicates very fast until producer stalls.
		for i := 0; i < 128; i++ {
			msgID := nats.MsgId(fmt.Sprintf("id.%d.%d", n, 1))
			js.PublishAsync("foo", []byte("test"), msgID, nats.RetryAttempts(10))
		}
	}
	cancel()
	wg.Wait()
}
