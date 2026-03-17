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
		noRoguePublisher bool
		numProducers     int // 0 means default (50)
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

		pctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

		numProducers := params.numProducers
		if numProducers == 0 {
			numProducers = 50
		}
		for i := 0; i < numProducers; i++ {
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
		if !params.noRoguePublisher {
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
		wg.Add(1)
		time.AfterFunc(10*time.Second, func() {
			defer wg.Done()
			for i := 0; i < params.restarts; i++ {
				select {
				case <-ctx.Done():
					return
				default:
					// Keep going
				}

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
			// Gather the stream mset from each replica.
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
						// If one of the msets reports an error for LoadMsg for this
						// particular sequence, then the same error should be reported
						// by all msets for that seq to prove consistency across replicas.
						// If one of the msets either returns no error or doesn't return
						// the same error, then that replica has drifted.
						if msgId != _EMPTY_ {
							t.Fatalf("Expected MsgId %q for seq %d, but got error: %v", msgId, seq, err)
						} else if expectedErr == nil {
							expectedErr = err
						} else {
							require_Error(t, err, expectedErr)
						}
						continue
					}
					// Only set expected msg ID if it's for the very first time.
					if msgId == _EMPTY_ && i == 0 {
						msgId = string(sm.hdr)
					} else if msgId != string(sm.hdr) {
						t.Fatalf("MsgIds do not match for seq %d: %q vs %q", seq, msgId, sm.hdr)
					}
				}
			}
		}

		// Wait for test to finish before checking state.
		wg.Wait()

		// If clustered, check whether leader and followers have drifted.
		if sc.Replicas > 1 {
			// If we have drifted do not have to wait too long, usually it's stuck for good.
			checkFor(t, time.Minute, time.Second, func() error {
				return checkState(t)
			})
			// If we succeeded now let's check that all messages are also the same.
			// We may have no messages but for tests that do we make sure each msg is the same
			// across all replicas.
			checkMsgsEqual(t)
		}

		err = checkForErr(time.Minute, time.Second, func() error {
			var consumerPending int
			var totalAckPending int
			consumers := make(map[string]int)
			consumersAck := make(map[string]int)
			consumersSseq := make(map[string]uint64)
			for i := 0; i < 10; i++ {
				consumerName := fmt.Sprintf("consumer:EEEEE:%d", i)
				ci, err := js.ConsumerInfo(sc.Name, consumerName)
				require_NoError(t, err)
				pending := int(ci.NumPending)
				consumers[consumerName] = pending
				consumerPending += pending
				ackPending := ci.NumAckPending
				consumersAck[consumerName] = ackPending
				totalAckPending += ackPending
				consumersSseq[consumerName] = ci.Delivered.Stream + 1
			}

			// Only check if there are any pending messages.
			if consumerPending > 0 || totalAckPending > 0 {
				// Check state of streams and consumers.
				si, err := js.StreamInfo(sc.Name, &nats.StreamInfoRequest{SubjectsFilter: ">"})
				require_NoError(t, err)
				streamPending := int(si.State.Msgs)
				totalConsumer := consumerPending + totalAckPending

				// The stream total should equal NumPending + NumAckPending across all consumers.
				// NumPending = messages not yet delivered (from consumer sseq onwards)
				// NumAckPending = messages delivered but not yet acked (below sseq, in pending map)
				if streamPending != totalConsumer {
					return fmt.Errorf("Unexpected number of pending messages, stream=%d, consumers(numPending)=%d, ackPending=%d, (numPending+ackPending)=%d, diff=%d\n subjects: %+v\nconsumers: %+v\nackPending: %+v\nsseq: %+v",
						streamPending, consumerPending, totalAckPending, totalConsumer, streamPending-totalConsumer,
						si.State.Subjects, consumers, consumersAck, consumersSseq)
				}

				// Additionally check for NumPending drift (the original check).
				// In a fully quiescent system with no in-flight messages, these should match.
				// Log a warning if there's a drift even if ackPending accounts for it.
				if streamPending != consumerPending && totalAckPending > 0 {
					t.Logf("NOTE: stream=%d != numPending=%d, but diff=%d is accounted for by ackPending=%d",
						streamPending, consumerPending, streamPending-consumerPending, totalAckPending)
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("%v", err)
		}
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

	// R1F without any restarts - isolate whether restart causes the drift.
	t.Run("R1F_NoRestart", func(t *testing.T) {
		params := &testParams{
			restartAny:     false,
			ldmRestart:     false,
			rolloutRestart: false,
			restarts:       0,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R1F_NR",
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

	// R1F without rogue publisher - isolate whether dedup collision causes drift.
	t.Run("R1F_NoRogue", func(t *testing.T) {
		params := &testParams{
			restartAny:       true,
			ldmRestart:       false,
			rolloutRestart:   false,
			restarts:         1,
			noRoguePublisher: true,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R1F_NOROGUE",
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

	// R1F minimal - no restarts, no rogue, fewer producers.
	t.Run("R1F_Minimal", func(t *testing.T) {
		params := &testParams{
			restartAny:       false,
			ldmRestart:       false,
			rolloutRestart:   false,
			restarts:         0,
			noRoguePublisher: true,
			numProducers:     5,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R1F_MIN",
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

	// R1F with multiple restarts and route disconnects to stress the system.
	t.Run("R1F_Stress", func(t *testing.T) {
		params := &testParams{
			restartAny:      true,
			ldmRestart:      false,
			rolloutRestart:  false,
			restarts:        3,
			reconnectRoutes: true,
		}
		test(t, params, &nats.StreamConfig{
			Name:        "OWQTEST_R1F_STRESS",
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
}
