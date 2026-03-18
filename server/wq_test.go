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

// This test file is skipped by default to avoid accidentally running (e.g. `go test ./server`)
//go:build !skip_js_tests && include_wq_tests

package server

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"
)

func TestLongClusterStreamOrphanMsgsAndReplicasDrifting(t *testing.T) {
	checkInterestStateT = 4 * time.Second
	checkInterestStateJ = 1
	defer func() {
		checkInterestStateT = defaultCheckInterestStateT
		checkInterestStateJ = defaultCheckInterestStateJ
	}()

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

	const (
		numPartitionConsumers = 10
		numProducers          = 50
		numRoguePublishers    = 10
		producerMsgCount      = 200_000
		partition             = "EEEEE"
	)

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

	consumerSubject := func(i int) string {
		return fmt.Sprintf("MSGS.%s.*.H.100XY.*.*.WQ.00000000000%d", partition, i)
	}
	consumerName := func(i int) string {
		return fmt.Sprintf("consumer:%s:%d", partition, i)
	}

	// fetchAndAck performs graduated fetches (1, 100, 1000) and acknowledges all messages.
	fetchAndAck := func(psub *nats.Subscription) {
		for _, batch := range []int{1, 100, 1000} {
			msgs, err := psub.Fetch(batch, nats.MaxWait(200*time.Millisecond))
			if err != nil {
				return
			}
			for _, msg := range msgs {
				msg.Ack()
			}
		}
	}

	// startConsumers creates pull subscribers and runs fetch loops until ctx is done or the optional
	// autoCloseAfter duration elapses.
	startConsumers := func(t *testing.T, c *cluster, wg *sync.WaitGroup, ctx context.Context, count int, autoCloseAfter time.Duration) {
		t.Helper()
		mp := nats.MaxAckPending(10000)
		mw := nats.PullMaxWaiting(1000)
		aw := nats.AckWait(5 * time.Second)

		for i := 0; i < numPartitionConsumers; i++ {
			subject := consumerSubject(i)
			consumer := consumerName(i)
			for n := 0; n < count; n++ {
				cpnc, cpjs := jsClientConnect(t, c.randomServer())
				defer cpnc.Close()

				psub, err := cpjs.PullSubscribe(subject, consumer, mp, mw, aw)
				if err != nil {
					t.Logf("ERROR: %v", err)
					continue
				}

				if autoCloseAfter > 0 {
					time.AfterFunc(autoCloseAfter, func() {
						cpnc.Close()
					})
				}

				wg.Add(1)
				go func() {
					defer wg.Done()
					tick := time.NewTicker(1 * time.Millisecond)
					defer tick.Stop()
					for {
						if cpnc.IsClosed() {
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-tick.C:
							fetchAndAck(psub)
						}
					}
				}()
			}
		}
	}

	// startProducers launches goroutines that publish messages with dedup IDs.
	startProducers := func(t *testing.T, c *cluster, wg *sync.WaitGroup, ctx context.Context) {
		t.Helper()
		for i := 0; i < numProducers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pnc, pjs := jsClientConnect(t, c.randomServer())
				defer pnc.Close()

				for i := 1; i < producerMsgCount; i++ {
					select {
					case <-ctx.Done():
						return
					default:
					}
					for _, subject := range subjects {
						msgID := nats.MsgId(nuid.Next())
						pjs.PublishAsync(subject, payload, msgID)
						pjs.Publish(subject, payload, msgID, nats.AckWait(250*time.Millisecond))
						pjs.Publish(subject, payload, msgID, nats.AckWait(250*time.Millisecond))
					}
				}
			}()
		}
	}

	// startRoguePublishers launches goroutines that repeatedly publish with the same message ID.
	startRoguePublishers := func(t *testing.T, c *cluster, wg *sync.WaitGroup, ctx context.Context) {
		t.Helper()
		for i := 0; i < numRoguePublishers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pnc, pjs := jsClientConnect(t, c.randomServer())
				defer pnc.Close()

				msgID := nats.MsgId("1234567890")
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}
					for _, subject := range subjects {
						pjs.PublishAsync(subject, payload, msgID, nats.RetryAttempts(0), nats.RetryWait(0))
						pjs.Publish(subject, payload, msgID, nats.AckWait(1*time.Millisecond), nats.RetryAttempts(0), nats.RetryWait(0))
						pjs.Publish(subject, payload, msgID, nats.AckWait(1*time.Millisecond), nats.RetryAttempts(0), nats.RetryWait(0))
					}
				}
			}()
		}
	}

	// shutdownAndRestart performs a shutdown (or lame duck) and restarts the server.
	shutdownAndRestart := func(c *cluster, s *Server, ldm bool) {
		if ldm {
			s.lameDuckMode()
		} else {
			s.Shutdown()
		}
		s.WaitForShutdown()
		c.restartServer(s)
	}

	// runDisruptors starts optional route disconnections and client reconnections in the background.
	runDisruptors := func(t *testing.T, c *cluster, wg *sync.WaitGroup, ctx context.Context, params *testParams) {
		t.Helper()
		if params.reconnectRoutes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range time.NewTicker(10 * time.Second).C {
					select {
					case <-ctx.Done():
						return
					default:
					}
					s := c.servers[rand.Intn(len(c.servers))]
					t.Logf("Disconnecting routes from %v", s.Name())
					s.mu.Lock()
					var routes []*client
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

		if params.reconnectClients {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range time.NewTicker(10 * time.Second).C {
					select {
					case <-ctx.Done():
						return
					default:
					}
					s := c.servers[rand.Intn(len(c.servers))]
					for _, client := range s.clients {
						client.closeConnection(Kicked)
					}
				}
			}()
		}
	}

	// runRestarts performs server restarts according to the test parameters.
	runRestarts := func(t *testing.T, c *cluster, wg *sync.WaitGroup, ctx context.Context, params *testParams, sc *nats.StreamConfig) {
		t.Helper()
		wg.Add(1)
		time.AfterFunc(10*time.Second, func() {
			defer wg.Done()
			for i := 0; i < params.restarts; i++ {
				select {
				case <-ctx.Done():
					return
				default:
				}

				switch {
				case params.restartLeader:
					s := c.streamLeader("js", sc.Name)
					shutdownAndRestart(c, s, params.ldmRestart)
				case params.restartAny:
					s := c.servers[rand.Intn(len(c.servers))]
					shutdownAndRestart(c, s, params.ldmRestart)
				case params.rolloutRestart:
					for _, s := range c.servers {
						shutdownAndRestart(c, s, params.ldmRestart)
						if params.checkHealthz {
							hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
							defer hcancel()
							for range time.NewTicker(2 * time.Second).C {
								select {
								case <-hctx.Done():
								default:
								}
								if status := s.healthz(nil); status.StatusCode == 200 {
									break
								}
							}
						}
					}
				}
				c.waitOnClusterReady()
			}
		})
	}

	getStreamDetails := func(t *testing.T, srv *Server) *StreamDetail {
		t.Helper()
		jsz, err := srv.Jsz(&JSzOptions{Accounts: true, Streams: true, Consumer: true})
		require_NoError(t, err)
		if len(jsz.AccountDetails) > 0 && len(jsz.AccountDetails[0].Streams) > 0 {
			return &jsz.AccountDetails[0].Streams[0]
		}
		t.Error("Could not find account details")
		return nil
	}

	checkState := func(t *testing.T, c *cluster, sc *nats.StreamConfig) error {
		t.Helper()
		leaderSrv := c.streamLeader("js", sc.Name)
		if leaderSrv == nil {
			return fmt.Errorf("no leader found for stream")
		}
		streamLeader := getStreamDetails(t, leaderSrv)
		var errs []error
		for _, srv := range c.servers {
			if srv == leaderSrv {
				continue
			}
			stream := getStreamDetails(t, srv)
			if stream == nil {
				return fmt.Errorf("stream not found")
			}
			if stream.State.Msgs != streamLeader.State.Msgs {
				errs = append(errs, fmt.Errorf("Leader %v has %d messages, Follower %v has %d messages",
					stream.Cluster.Leader, streamLeader.State.Msgs, srv, stream.State.Msgs))
			}
			if stream.State.FirstSeq != streamLeader.State.FirstSeq {
				errs = append(errs, fmt.Errorf("Leader %v FirstSeq is %d, Follower %v is at %d",
					stream.Cluster.Leader, streamLeader.State.FirstSeq, srv, stream.State.FirstSeq))
			}
			if stream.State.LastSeq != streamLeader.State.LastSeq {
				errs = append(errs, fmt.Errorf("Leader %v LastSeq is %d, Follower %v is at %d",
					stream.Cluster.Leader, streamLeader.State.LastSeq, srv, stream.State.LastSeq))
			}
		}
		return errors.Join(errs...)
	}

	checkMsgsEqual := func(t *testing.T, c *cluster, sc *nats.StreamConfig) {
		t.Helper()
		state := getStreamDetails(t, c.streamLeader("js", sc.Name)).State
		var msets []*stream
		for _, s := range c.servers {
			acc, err := s.LookupAccount("js")
			require_NoError(t, err)
			mset, err := acc.lookupStream(sc.Name)
			require_NoError(t, err)
			msets = append(msets, mset)
		}
		for seq := state.FirstSeq; seq <= state.LastSeq; seq++ {
			var expectedErr error
			var msgId string
			var smv StoreMsg
			for i, mset := range msets {
				mset.mu.RLock()
				sm, err := mset.store.LoadMsg(seq, &smv)
				mset.mu.RUnlock()
				if err != nil || expectedErr != nil {
					if msgId != _EMPTY_ {
						t.Fatalf("Expected MsgId %q for seq %d, but got error: %v", msgId, seq, err)
					} else if expectedErr == nil {
						expectedErr = err
					} else {
						require_Error(t, err, expectedErr)
					}
					continue
				}
				if msgId == _EMPTY_ && i == 0 {
					msgId = string(sm.hdr)
				} else if msgId != string(sm.hdr) {
					t.Fatalf("MsgIds do not match for seq %d: %q vs %q", seq, msgId, sm.hdr)
				}
			}
		}
	}

	checkConsumerPending := func(t *testing.T, js nats.JetStreamContext, sc *nats.StreamConfig) {
		t.Helper()
		err := checkForErr(time.Minute, time.Second, func() error {
			var consumerPending int
			consumers := make(map[string]int)
			for i := 0; i < numPartitionConsumers; i++ {
				name := consumerName(i)
				ci, err := js.ConsumerInfo(sc.Name, name)
				require_NoError(t, err)
				pending := int(ci.NumPending)
				consumers[name] = pending
				consumerPending += pending
			}
			if consumerPending > 0 {
				si, err := js.StreamInfo(sc.Name, &nats.StreamInfoRequest{SubjectsFilter: ">"})
				require_NoError(t, err)
				streamPending := int(si.State.Msgs)
				if streamPending != consumerPending {
					return fmt.Errorf("Unexpected number of pending messages, stream=%d, consumers=%d \n subjects: %+v\nconsumers: %+v",
						streamPending, consumerPending, si.State.Subjects, consumers)
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("%v", err)
		}
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

		// Create initial pull subscribers (no active fetching, just registration).
		mp := nats.MaxAckPending(10000)
		mw := nats.PullMaxWaiting(1000)
		aw := nats.AckWait(5 * time.Second)
		for i := 0; i < numPartitionConsumers; i++ {
			_, err := cjs.PullSubscribe(consumerSubject(i), consumerName(i), mp, mw, aw)
			require_NoError(t, err)
		}
		// Create a single consumer that does no activity to verify low ack cleanup.
		_, err = cjs.PullSubscribe("MSGS.ZZ.>", "consumer:ZZ:0", mp, mw, aw)
		require_NoError(t, err)

		pctx, pcancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer pcancel()

		var wg sync.WaitGroup

		startProducers(t, c, &wg, pctx)
		startRoguePublishers(t, c, &wg, pctx)

		// Let messages accumulate before starting consumers.
		time.Sleep(15 * time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		// First wave: 5 consumers per partition, auto-close after 15s.
		startConsumers(t, c, &wg, ctx, 5, 15*time.Second)
		// Second wave: 10 consumers per partition, run until context done.
		startConsumers(t, c, &wg, ctx, 10, 0)

		runDisruptors(t, c, &wg, ctx, params)
		runRestarts(t, c, &wg, ctx, params, sc)

		<-ctx.Done()
		wg.Wait()

		if sc.Replicas > 1 {
			checkFor(t, time.Minute, time.Second, func() error {
				return checkState(t, c, sc)
			})
			checkMsgsEqual(t, c, sc)
		}

		checkConsumerPending(t, js, sc)
	}

	defaultPlacement := &nats.Placement{Tags: []string{"test"}}

	// File based with single replica and discard old policy.
	t.Run("R1F", func(t *testing.T) {
		test(t, &testParams{restartAny: true, restarts: 1}, &nats.StreamConfig{
			Name:        "OWQTEST_R1F",
			Subjects:    []string{"MSGS.>"},
			Replicas:    1,
			MaxAge:      30 * time.Minute,
			Duplicates:  5 * time.Minute,
			Retention:   nats.WorkQueuePolicy,
			Discard:     nats.DiscardOld,
			AllowRollup: true,
			Placement:   defaultPlacement,
		})
	})

	// Clustered memory based with discard new policy and max msgs limit.
	t.Run("R3M", func(t *testing.T) {
		test(t, &testParams{restartAny: true, ldmRestart: true, restarts: 1, checkHealthz: true}, &nats.StreamConfig{
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
			Placement:   defaultPlacement,
		})
	})

	// Clustered file based with discard new policy and max msgs limit.
	t.Run("R3F_DN", func(t *testing.T) {
		test(t, &testParams{restartAny: true, ldmRestart: true, restarts: 1, checkHealthz: true}, &nats.StreamConfig{
			Name:        "OWQTEST_R3F_DN",
			Subjects:    []string{"MSGS.>"},
			Replicas:    3,
			MaxAge:      30 * time.Minute,
			MaxMsgs:     100_000,
			Duplicates:  5 * time.Minute,
			Retention:   nats.WorkQueuePolicy,
			Discard:     nats.DiscardNew,
			AllowRollup: true,
			Placement:   defaultPlacement,
		})
	})

	// Clustered file based with discard old policy and max msgs limit.
	t.Run("R3F_DO", func(t *testing.T) {
		test(t, &testParams{restartAny: true, ldmRestart: true, restarts: 1, checkHealthz: true}, &nats.StreamConfig{
			Name:        "OWQTEST_R3F_DO",
			Subjects:    []string{"MSGS.>"},
			Replicas:    3,
			MaxAge:      30 * time.Minute,
			MaxMsgs:     100_000,
			Duplicates:  5 * time.Minute,
			Retention:   nats.WorkQueuePolicy,
			Discard:     nats.DiscardOld,
			AllowRollup: true,
			Placement:   defaultPlacement,
		})
	})

	// Clustered file based with discard old policy and no limits.
	t.Run("R3F_DO_NOLIMIT", func(t *testing.T) {
		test(t, &testParams{restartAny: true, ldmRestart: true, restarts: 1, checkHealthz: true}, &nats.StreamConfig{
			Name:       "OWQTEST_R3F_DO_NOLIMIT",
			Subjects:   []string{"MSGS.>"},
			Replicas:   3,
			Duplicates: 30 * time.Second,
			Discard:    nats.DiscardOld,
			Placement:  defaultPlacement,
		})
	})
}
