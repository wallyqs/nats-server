// Copyright 2019-2025 The NATS Authors
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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestSimpleTwoServerLeafNodeQueueDistribution_LocalPreference demonstrates that
// in a simple hub-spoke topology, LOCAL queue subscribers are PREFERRED over
// remote leafnode subscribers. This is by design in NATS.
func TestSimpleTwoServerLeafNodeQueueDistribution_LocalPreference(t *testing.T) {
	// Server A - Hub: accepts leafnode connections
	confA := createConfFile(t, []byte(`
		server_name: SERVER_A
		listen: 127.0.0.1:-1
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`))
	serverA, optsA := RunServerWithConfig(confA)
	defer serverA.Shutdown()

	// Server B - Spoke: connects to Server A via leafnode
	confB := createConfFile(t, []byte(fmt.Sprintf(`
		server_name: SERVER_B
		listen: 127.0.0.1:-1
		leafnodes {
			remotes = [{ url: nats-leaf://127.0.0.1:%d }]
		}
	`, optsA.LeafNode.Port)))
	serverB, _ := RunServerWithConfig(confB)
	defer serverB.Shutdown()

	// Wait for leafnode connection to be established
	checkLeafNodeConnected(t, serverB)
	checkLeafNodeConnectedCount(t, serverA, 1)

	ncA := natsConnect(t, serverA.ClientURL())
	defer ncA.Close()

	ncB := natsConnect(t, serverB.ClientURL())
	defer ncB.Close()

	var countA, countB atomic.Int32

	// Queue subscriber on Server A (hub)
	natsQueueSub(t, ncA, "test.subject", "myqueue", func(_ *nats.Msg) {
		countA.Add(1)
	})
	natsFlush(t, ncA)

	// Queue subscriber on Server B (spoke)
	natsQueueSub(t, ncB, "test.subject", "myqueue", func(_ *nats.Msg) {
		countB.Add(1)
	})
	natsFlush(t, ncB)

	// Wait for subscription propagation
	checkFor(t, time.Second, 15*time.Millisecond, func() error {
		if n := serverA.GlobalAccount().Interest("test.subject"); n != 2 {
			return fmt.Errorf("expected interest count 2 on Server A, got %d", n)
		}
		return nil
	})

	// Publish from Server A - LOCAL subscriber (A) gets ALL messages
	t.Run("publish_from_hub_local_preferred", func(t *testing.T) {
		countA.Store(0)
		countB.Store(0)
		total := 100

		for i := 0; i < total; i++ {
			natsPub(t, ncA, "test.subject", []byte("hello"))
		}
		natsFlush(t, ncA)

		time.Sleep(100 * time.Millisecond)

		a := int(countA.Load())
		b := int(countB.Load())
		t.Logf("From Hub: Server A (local) received %d, Server B (remote) received %d", a, b)

		// LOCAL subscribers are preferred - Server A gets all messages
		if a != total {
			t.Fatalf("Expected Server A (local) to receive all %d messages, got %d", total, a)
		}
		if b != 0 {
			t.Fatalf("Expected Server B (remote) to receive 0 messages, got %d", b)
		}
	})

	// Publish from Server B - LOCAL subscriber (B) gets ALL messages
	t.Run("publish_from_spoke_local_preferred", func(t *testing.T) {
		countA.Store(0)
		countB.Store(0)
		total := 100

		for i := 0; i < total; i++ {
			natsPub(t, ncB, "test.subject", []byte("hello"))
		}
		natsFlush(t, ncB)

		time.Sleep(100 * time.Millisecond)

		a := int(countA.Load())
		b := int(countB.Load())
		t.Logf("From Spoke: Server A (remote) received %d, Server B (local) received %d", a, b)

		// LOCAL subscribers are preferred - Server B gets all messages
		if a != 0 {
			t.Fatalf("Expected Server A (remote) to receive 0 messages, got %d", a)
		}
		if b != total {
			t.Fatalf("Expected Server B (local) to receive all %d messages, got %d", total, b)
		}
	})
}

// TestDistributedQueueAcrossLeafnodes_PublisherOnDifferentServer demonstrates
// that distributed queue semantics WORK when the publisher is on a server
// that has NO local queue subscribers.
//
// Topology:
//   Server A (Hub) - Publisher connects here (NO queue subscribers)
//   Server B (Spoke) - Queue subscriber here
//   Server C (Spoke) - Queue subscriber here
func TestDistributedQueueAcrossLeafnodes_PublisherOnDifferentServer(t *testing.T) {
	// Server A - Hub: accepts leafnode connections, publisher connects here
	confA := createConfFile(t, []byte(`
		server_name: SERVER_A
		listen: 127.0.0.1:-1
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`))
	serverA, optsA := RunServerWithConfig(confA)
	defer serverA.Shutdown()

	// Server B - Spoke: connects to Server A, has queue subscriber
	confB := createConfFile(t, []byte(fmt.Sprintf(`
		server_name: SERVER_B
		listen: 127.0.0.1:-1
		leafnodes {
			remotes = [{ url: nats-leaf://127.0.0.1:%d }]
		}
	`, optsA.LeafNode.Port)))
	serverB, _ := RunServerWithConfig(confB)
	defer serverB.Shutdown()

	// Server C - Spoke: connects to Server A, has queue subscriber
	confC := createConfFile(t, []byte(fmt.Sprintf(`
		server_name: SERVER_C
		listen: 127.0.0.1:-1
		leafnodes {
			remotes = [{ url: nats-leaf://127.0.0.1:%d }]
		}
	`, optsA.LeafNode.Port)))
	serverC, _ := RunServerWithConfig(confC)
	defer serverC.Shutdown()

	// Wait for leafnode connections
	checkLeafNodeConnected(t, serverB)
	checkLeafNodeConnected(t, serverC)
	checkLeafNodeConnectedCount(t, serverA, 2)

	// Publisher connects to Server A (no local queue subscribers)
	ncPub := natsConnect(t, serverA.ClientURL())
	defer ncPub.Close()

	// Queue subscribers on Server B and C
	ncB := natsConnect(t, serverB.ClientURL())
	defer ncB.Close()
	ncC := natsConnect(t, serverC.ClientURL())
	defer ncC.Close()

	var countB, countC atomic.Int32

	natsQueueSub(t, ncB, "test.subject", "myqueue", func(_ *nats.Msg) {
		countB.Add(1)
	})
	natsFlush(t, ncB)

	natsQueueSub(t, ncC, "test.subject", "myqueue", func(_ *nats.Msg) {
		countC.Add(1)
	})
	natsFlush(t, ncC)

	// Wait for subscription propagation
	checkFor(t, time.Second, 15*time.Millisecond, func() error {
		if n := serverA.GlobalAccount().Interest("test.subject"); n != 2 {
			return fmt.Errorf("expected interest count 2 on Server A, got %d", n)
		}
		return nil
	})

	// Publish from Server A (hub) - should distribute to B and C
	total := 1000
	for i := 0; i < total; i++ {
		natsPub(t, ncPub, "test.subject", []byte("hello"))
	}
	natsFlush(t, ncPub)

	// Wait for all messages
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		received := int(countB.Load() + countC.Load())
		if received != total {
			return fmt.Errorf("expected %d messages, got %d", total, received)
		}
		return nil
	})

	b := int(countB.Load())
	c := int(countC.Load())
	t.Logf("Server B received %d, Server C received %d (total %d)", b, c, b+c)

	// Both should receive a fair share (at least 10% each)
	if b < total/10 {
		t.Fatalf("Server B received too few: %d (expected at least %d)", b, total/10)
	}
	if c < total/10 {
		t.Fatalf("Server C received too few: %d (expected at least %d)", c, total/10)
	}
}

// TestDistributedQueueWithLeafnodeCluster demonstrates distributed queue semantics
// using a cluster of leafnodes, which is the recommended topology for true
// distributed queue behavior.
//
// Topology:
//   HUB Cluster: H1 <-> H2 (clustered)
//   LEAF Cluster: L1 <-> L2 (clustered), both connect to HUB cluster
//   Publisher on HUB, Queue subscribers on LEAF cluster
func TestDistributedQueueWithLeafnodeCluster(t *testing.T) {
	// Create HUB cluster (2 nodes)
	hc := createClusterWithName(t, "HUB", 2)
	defer hc.shutdown()

	// Create LEAF cluster (2 nodes) connecting to HUB
	c1 := `
		server_name: LEAF1
		listen: 127.0.0.1:-1
		cluster { name: LEAF_CLUSTER, listen: 127.0.0.1:-1 }
		leafnodes { remotes = [{ url: nats-leaf://127.0.0.1:%d }] }
	`
	lconf1 := createConfFile(t, []byte(fmt.Sprintf(c1, hc.opts[0].LeafNode.Port)))
	ln1, lopts1 := RunServerWithConfig(lconf1)
	defer ln1.Shutdown()

	c2 := `
		server_name: LEAF2
		listen: 127.0.0.1:-1
		cluster { name: LEAF_CLUSTER, listen: 127.0.0.1:-1, routes = [ nats-route://127.0.0.1:%d] }
		leafnodes { remotes = [{ url: nats-leaf://127.0.0.1:%d }] }
	`
	lconf2 := createConfFile(t, []byte(fmt.Sprintf(c2, lopts1.Cluster.Port, hc.opts[1].LeafNode.Port)))
	ln2, _ := RunServerWithConfig(lconf2)
	defer ln2.Shutdown()

	// Wait for cluster formation
	checkClusterFormed(t, ln1, ln2)
	checkLeafNodeConnected(t, ln1)
	checkLeafNodeConnected(t, ln2)

	// Queue subscribers on LEAF cluster
	nc1 := natsConnect(t, ln1.ClientURL())
	defer nc1.Close()
	nc2 := natsConnect(t, ln2.ClientURL())
	defer nc2.Close()

	var count1, count2 atomic.Int32

	natsQueueSub(t, nc1, "foo", "queue1", func(_ *nats.Msg) {
		count1.Add(1)
	})
	natsFlush(t, nc1)

	natsQueueSub(t, nc2, "foo", "queue1", func(_ *nats.Msg) {
		count2.Add(1)
	})
	natsFlush(t, nc2)

	// Wait for propagation to HUB
	for _, s := range hc.servers {
		checkFor(t, time.Second, 15*time.Millisecond, func() error {
			if n := s.GlobalAccount().Interest("foo"); n != 2 {
				return fmt.Errorf("expected interest 2 on HUB, got %d", n)
			}
			return nil
		})
	}

	// Publisher on HUB
	ncPub := natsConnect(t, hc.servers[0].ClientURL())
	defer ncPub.Close()

	total := 1000
	for i := 0; i < total; i++ {
		natsPub(t, ncPub, "foo", []byte("hello"))
	}
	natsFlush(t, ncPub)

	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		received := int(count1.Load() + count2.Load())
		if received != total {
			return fmt.Errorf("expected %d, got %d", total, received)
		}
		return nil
	})

	c1count := int(count1.Load())
	c2count := int(count2.Load())
	t.Logf("LEAF1 received %d, LEAF2 received %d", c1count, c2count)

	// Both should receive fair share
	if c1count < total/10 {
		t.Fatalf("LEAF1 received too few: %d", c1count)
	}
	if c2count < total/10 {
		t.Fatalf("LEAF2 received too few: %d", c2count)
	}
}

// TestDistributedQueueClusteredTopology_Comprehensive is a comprehensive test
// demonstrating distributed queue semantics with a clustered topology.
//
// Topology:
//
//	                    ┌─────────────────────────────────────────────────────────────┐
//	                    │                      HUB Cluster                            │
//	                    │   ┌────────────┐          ┌────────────┐                    │
//	                    │   │   HUB1     │◄────────►│   HUB2     │   (clustered)      │
//	                    │   │            │  routes  │            │                    │
//	                    │   └─────┬──────┘          └──────┬─────┘                    │
//	                    └─────────┼────────────────────────┼──────────────────────────┘
//	                              │ leafnode               │ leafnode
//	                              ▼                        ▼
//	                    ┌─────────────────────────────────────────────────────────────┐
//	                    │                     LEAF Cluster                            │
//	                    │   ┌────────────┐          ┌────────────┐                    │
//	                    │   │   LEAF1    │◄────────►│   LEAF2    │   (clustered)      │
//	                    │   │            │  routes  │            │                    │
//	                    │   └────────────┘          └────────────┘                    │
//	                    └─────────────────────────────────────────────────────────────┘
//
// This test covers:
//  1. Publishing from different HUB nodes - messages distributed across LEAF cluster
//  2. Multiple queue subscribers per LEAF node - all receive fair share
//  3. Queue subscribers on both HUB and LEAF - local preference applies
//  4. Dynamic subscriber addition - new subscribers join the distribution
func TestDistributedQueueClusteredTopology_Comprehensive(t *testing.T) {
	// Create HUB cluster (2 nodes)
	hc := createClusterWithName(t, "HUB", 2)
	defer hc.shutdown()

	// Create LEAF cluster (2 nodes) connecting to HUB
	leafConf1 := `
		server_name: LEAF1
		listen: 127.0.0.1:-1
		cluster { name: LEAF_CLUSTER, listen: 127.0.0.1:-1 }
		leafnodes { remotes = [{ url: nats-leaf://127.0.0.1:%d }] }
	`
	lconf1 := createConfFile(t, []byte(fmt.Sprintf(leafConf1, hc.opts[0].LeafNode.Port)))
	leaf1, lopts1 := RunServerWithConfig(lconf1)
	defer leaf1.Shutdown()

	leafConf2 := `
		server_name: LEAF2
		listen: 127.0.0.1:-1
		cluster { name: LEAF_CLUSTER, listen: 127.0.0.1:-1, routes = [ nats-route://127.0.0.1:%d] }
		leafnodes { remotes = [{ url: nats-leaf://127.0.0.1:%d }] }
	`
	lconf2 := createConfFile(t, []byte(fmt.Sprintf(leafConf2, lopts1.Cluster.Port, hc.opts[1].LeafNode.Port)))
	leaf2, _ := RunServerWithConfig(lconf2)
	defer leaf2.Shutdown()

	// Wait for cluster formation and leafnode connections
	checkClusterFormed(t, leaf1, leaf2)
	checkLeafNodeConnected(t, leaf1)
	checkLeafNodeConnected(t, leaf2)

	// Verify HUB cluster sees leafnode connections
	for i, s := range hc.servers {
		checkLeafNodeConnectedCount(t, s, 1)
		t.Logf("HUB%d has leafnode connection", i+1)
	}

	// Test 1: Publishing from HUB1, queue subscribers on LEAF cluster
	t.Run("publish_from_hub1_to_leaf_cluster", func(t *testing.T) {
		ncLeaf1 := natsConnect(t, leaf1.ClientURL())
		defer ncLeaf1.Close()
		ncLeaf2 := natsConnect(t, leaf2.ClientURL())
		defer ncLeaf2.Close()

		var countLeaf1, countLeaf2 atomic.Int32

		natsQueueSub(t, ncLeaf1, "test.hub1", "workers", func(_ *nats.Msg) {
			countLeaf1.Add(1)
		})
		natsFlush(t, ncLeaf1)

		natsQueueSub(t, ncLeaf2, "test.hub1", "workers", func(_ *nats.Msg) {
			countLeaf2.Add(1)
		})
		natsFlush(t, ncLeaf2)

		// Wait for subscription propagation to HUB
		for _, s := range hc.servers {
			checkFor(t, time.Second, 15*time.Millisecond, func() error {
				if n := s.GlobalAccount().Interest("test.hub1"); n != 2 {
					return fmt.Errorf("expected interest 2, got %d", n)
				}
				return nil
			})
		}

		// Publish from HUB1
		ncPub := natsConnect(t, hc.servers[0].ClientURL())
		defer ncPub.Close()

		total := 1000
		for i := 0; i < total; i++ {
			natsPub(t, ncPub, "test.hub1", []byte("from hub1"))
		}
		natsFlush(t, ncPub)

		checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
			received := int(countLeaf1.Load() + countLeaf2.Load())
			if received != total {
				return fmt.Errorf("expected %d, got %d", total, received)
			}
			return nil
		})

		l1 := int(countLeaf1.Load())
		l2 := int(countLeaf2.Load())
		t.Logf("From HUB1: LEAF1=%d, LEAF2=%d (distribution: %.1f%% / %.1f%%)",
			l1, l2, float64(l1)*100/float64(total), float64(l2)*100/float64(total))

		if l1 < total/10 || l2 < total/10 {
			t.Fatalf("Uneven distribution: LEAF1=%d, LEAF2=%d", l1, l2)
		}
	})

	// Test 2: Publishing from HUB2, queue subscribers on LEAF cluster
	t.Run("publish_from_hub2_to_leaf_cluster", func(t *testing.T) {
		ncLeaf1 := natsConnect(t, leaf1.ClientURL())
		defer ncLeaf1.Close()
		ncLeaf2 := natsConnect(t, leaf2.ClientURL())
		defer ncLeaf2.Close()

		var countLeaf1, countLeaf2 atomic.Int32

		natsQueueSub(t, ncLeaf1, "test.hub2", "workers", func(_ *nats.Msg) {
			countLeaf1.Add(1)
		})
		natsFlush(t, ncLeaf1)

		natsQueueSub(t, ncLeaf2, "test.hub2", "workers", func(_ *nats.Msg) {
			countLeaf2.Add(1)
		})
		natsFlush(t, ncLeaf2)

		// Wait for subscription propagation
		for _, s := range hc.servers {
			checkFor(t, time.Second, 15*time.Millisecond, func() error {
				if n := s.GlobalAccount().Interest("test.hub2"); n != 2 {
					return fmt.Errorf("expected interest 2, got %d", n)
				}
				return nil
			})
		}

		// Publish from HUB2 (different node)
		ncPub := natsConnect(t, hc.servers[1].ClientURL())
		defer ncPub.Close()

		total := 1000
		for i := 0; i < total; i++ {
			natsPub(t, ncPub, "test.hub2", []byte("from hub2"))
		}
		natsFlush(t, ncPub)

		checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
			received := int(countLeaf1.Load() + countLeaf2.Load())
			if received != total {
				return fmt.Errorf("expected %d, got %d", total, received)
			}
			return nil
		})

		l1 := int(countLeaf1.Load())
		l2 := int(countLeaf2.Load())
		t.Logf("From HUB2: LEAF1=%d, LEAF2=%d (distribution: %.1f%% / %.1f%%)",
			l1, l2, float64(l1)*100/float64(total), float64(l2)*100/float64(total))

		if l1 < total/10 || l2 < total/10 {
			t.Fatalf("Uneven distribution: LEAF1=%d, LEAF2=%d", l1, l2)
		}
	})

	// Test 3: Multiple queue subscribers per LEAF node
	t.Run("multiple_subscribers_per_leaf", func(t *testing.T) {
		ncLeaf1a := natsConnect(t, leaf1.ClientURL())
		defer ncLeaf1a.Close()
		ncLeaf1b := natsConnect(t, leaf1.ClientURL())
		defer ncLeaf1b.Close()
		ncLeaf2a := natsConnect(t, leaf2.ClientURL())
		defer ncLeaf2a.Close()
		ncLeaf2b := natsConnect(t, leaf2.ClientURL())
		defer ncLeaf2b.Close()

		var countLeaf1a, countLeaf1b, countLeaf2a, countLeaf2b atomic.Int32

		// 2 subscribers on LEAF1
		natsQueueSub(t, ncLeaf1a, "test.multi", "workers", func(_ *nats.Msg) {
			countLeaf1a.Add(1)
		})
		natsFlush(t, ncLeaf1a)
		natsQueueSub(t, ncLeaf1b, "test.multi", "workers", func(_ *nats.Msg) {
			countLeaf1b.Add(1)
		})
		natsFlush(t, ncLeaf1b)

		// 2 subscribers on LEAF2
		natsQueueSub(t, ncLeaf2a, "test.multi", "workers", func(_ *nats.Msg) {
			countLeaf2a.Add(1)
		})
		natsFlush(t, ncLeaf2a)
		natsQueueSub(t, ncLeaf2b, "test.multi", "workers", func(_ *nats.Msg) {
			countLeaf2b.Add(1)
		})
		natsFlush(t, ncLeaf2b)

		// Wait for subscription propagation
		// Note: Interest count is 2 (one per leafnode), not 4, because queue weight
		// is aggregated per leafnode connection. The leafnode sends the total count
		// of queue subscribers as the weight.
		for _, s := range hc.servers {
			checkFor(t, time.Second, 15*time.Millisecond, func() error {
				if n := s.GlobalAccount().Interest("test.multi"); n < 2 {
					return fmt.Errorf("expected interest >= 2, got %d", n)
				}
				return nil
			})
		}

		ncPub := natsConnect(t, hc.servers[0].ClientURL())
		defer ncPub.Close()

		total := 1000
		for i := 0; i < total; i++ {
			natsPub(t, ncPub, "test.multi", []byte("multi"))
		}
		natsFlush(t, ncPub)

		checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
			received := int(countLeaf1a.Load() + countLeaf1b.Load() + countLeaf2a.Load() + countLeaf2b.Load())
			if received != total {
				return fmt.Errorf("expected %d, got %d", total, received)
			}
			return nil
		})

		l1a := int(countLeaf1a.Load())
		l1b := int(countLeaf1b.Load())
		l2a := int(countLeaf2a.Load())
		l2b := int(countLeaf2b.Load())
		l1Total := l1a + l1b
		l2Total := l2a + l2b

		t.Logf("LEAF1: sub_a=%d, sub_b=%d (total=%d)", l1a, l1b, l1Total)
		t.Logf("LEAF2: sub_a=%d, sub_b=%d (total=%d)", l2a, l2b, l2Total)
		t.Logf("Distribution: LEAF1=%.1f%%, LEAF2=%.1f%%",
			float64(l1Total)*100/float64(total), float64(l2Total)*100/float64(total))

		// Both LEAF nodes should receive messages
		if l1Total < total/10 || l2Total < total/10 {
			t.Fatalf("Uneven distribution across LEAFs: LEAF1=%d, LEAF2=%d", l1Total, l2Total)
		}
	})

	// Test 4: Queue subscribers on both HUB and LEAF - demonstrates local preference
	t.Run("subscribers_on_hub_and_leaf_local_preference", func(t *testing.T) {
		ncHub1 := natsConnect(t, hc.servers[0].ClientURL())
		defer ncHub1.Close()
		ncLeaf1 := natsConnect(t, leaf1.ClientURL())
		defer ncLeaf1.Close()

		var countHub1, countLeaf1 atomic.Int32

		// Queue subscriber on HUB1
		natsQueueSub(t, ncHub1, "test.mixed", "workers", func(_ *nats.Msg) {
			countHub1.Add(1)
		})
		natsFlush(t, ncHub1)

		// Queue subscriber on LEAF1
		natsQueueSub(t, ncLeaf1, "test.mixed", "workers", func(_ *nats.Msg) {
			countLeaf1.Add(1)
		})
		natsFlush(t, ncLeaf1)

		// Wait for subscription propagation
		checkFor(t, time.Second, 15*time.Millisecond, func() error {
			if n := hc.servers[0].GlobalAccount().Interest("test.mixed"); n != 2 {
				return fmt.Errorf("expected interest 2, got %d", n)
			}
			return nil
		})

		// Publish from HUB1 - LOCAL subscriber (HUB1) should get ALL messages
		ncPub := natsConnect(t, hc.servers[0].ClientURL())
		defer ncPub.Close()

		total := 100
		for i := 0; i < total; i++ {
			natsPub(t, ncPub, "test.mixed", []byte("mixed"))
		}
		natsFlush(t, ncPub)

		time.Sleep(100 * time.Millisecond)

		h1 := int(countHub1.Load())
		l1 := int(countLeaf1.Load())

		t.Logf("Publishing from HUB1: HUB1 (local)=%d, LEAF1 (remote)=%d", h1, l1)

		// Local preference: HUB1's local subscriber should get all messages
		if h1 != total {
			t.Fatalf("Expected HUB1 (local) to receive all %d messages, got %d", total, h1)
		}
		if l1 != 0 {
			t.Fatalf("Expected LEAF1 (remote) to receive 0 messages, got %d", l1)
		}
	})

	// Test 5: Dynamic subscriber addition
	t.Run("dynamic_subscriber_addition", func(t *testing.T) {
		ncLeaf1 := natsConnect(t, leaf1.ClientURL())
		defer ncLeaf1.Close()

		var countLeaf1, countLeaf2 atomic.Int32

		// Start with only LEAF1 subscriber
		natsQueueSub(t, ncLeaf1, "test.dynamic", "workers", func(_ *nats.Msg) {
			countLeaf1.Add(1)
		})
		natsFlush(t, ncLeaf1)

		// Wait for propagation
		for _, s := range hc.servers {
			checkFor(t, time.Second, 15*time.Millisecond, func() error {
				if n := s.GlobalAccount().Interest("test.dynamic"); n != 1 {
					return fmt.Errorf("expected interest 1, got %d", n)
				}
				return nil
			})
		}

		ncPub := natsConnect(t, hc.servers[0].ClientURL())
		defer ncPub.Close()

		// Send first batch - only LEAF1 receives
		batch1 := 100
		for i := 0; i < batch1; i++ {
			natsPub(t, ncPub, "test.dynamic", []byte("batch1"))
		}
		natsFlush(t, ncPub)

		checkFor(t, time.Second, 15*time.Millisecond, func() error {
			if n := int(countLeaf1.Load()); n != batch1 {
				return fmt.Errorf("expected %d, got %d", batch1, n)
			}
			return nil
		})

		t.Logf("Batch 1 (only LEAF1): LEAF1=%d", countLeaf1.Load())

		// Now add LEAF2 subscriber
		ncLeaf2 := natsConnect(t, leaf2.ClientURL())
		defer ncLeaf2.Close()

		natsQueueSub(t, ncLeaf2, "test.dynamic", "workers", func(_ *nats.Msg) {
			countLeaf2.Add(1)
		})
		natsFlush(t, ncLeaf2)

		// Wait for propagation
		for _, s := range hc.servers {
			checkFor(t, time.Second, 15*time.Millisecond, func() error {
				if n := s.GlobalAccount().Interest("test.dynamic"); n != 2 {
					return fmt.Errorf("expected interest 2, got %d", n)
				}
				return nil
			})
		}

		// Reset counters for batch 2
		countLeaf1.Store(0)
		countLeaf2.Store(0)

		// Send second batch - should distribute to both
		batch2 := 1000
		for i := 0; i < batch2; i++ {
			natsPub(t, ncPub, "test.dynamic", []byte("batch2"))
		}
		natsFlush(t, ncPub)

		checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
			received := int(countLeaf1.Load() + countLeaf2.Load())
			if received != batch2 {
				return fmt.Errorf("expected %d, got %d", batch2, received)
			}
			return nil
		})

		l1 := int(countLeaf1.Load())
		l2 := int(countLeaf2.Load())
		t.Logf("Batch 2 (both LEAFs): LEAF1=%d, LEAF2=%d (%.1f%% / %.1f%%)",
			l1, l2, float64(l1)*100/float64(batch2), float64(l2)*100/float64(batch2))

		if l1 < batch2/10 || l2 < batch2/10 {
			t.Fatalf("Uneven distribution after adding subscriber: LEAF1=%d, LEAF2=%d", l1, l2)
		}
	})
}

// TestJetStreamMirrorAcrossIndependentLeafnodes demonstrates JetStream stream
// mirroring between two independent leafnode servers (NO cluster routes between them).
// Communication happens through the HUB server.
//
// Topology:
//
//	                         ┌────────────────────────┐
//	                         │         HUB            │
//	                         │   (accepts leafnodes)  │
//	                         │                        │
//	                         │   leafnodes {          │
//	                         │     listen: -1         │
//	                         │   }                    │
//	                         └───────────┬────────────┘
//	                                     │
//	                    ┌────────────────┴────────────────┐
//	                    │                                 │
//	            leafnode remote                   leafnode remote
//	                    │                                 │
//	                    ▼                                 ▼
//	    ┌───────────────────────────┐     ┌───────────────────────────┐
//	    │          LEAF1            │     │          LEAF2            │
//	    │   JetStream domain: L1    │     │   JetStream domain: L2    │
//	    │                           │     │                           │
//	    │   Stream: SOURCE-STREAM   │────►│   Stream: MIRROR-STREAM   │
//	    │   (original data)         │     │   (mirror of SOURCE)      │
//	    └───────────────────────────┘     └───────────────────────────┘
//	                    │                                 │
//	              NO ROUTES                         NO ROUTES
//	           (independent)                     (independent)
//
// Key insight: LEAF1 and LEAF2 are NOT clustered. They communicate only through
// the HUB via leafnode connections. JetStream mirroring uses the External API
// prefix to route requests through the leafnode connection.
func TestJetStreamMirrorAcrossIndependentLeafnodes(t *testing.T) {
	// HUB server - accepts leafnode connections, no JetStream needed on HUB
	// (JetStream is on the leaf nodes)
	hubConf := createConfFile(t, []byte(`
		server_name: HUB
		listen: 127.0.0.1:-1
		accounts {
			JS { users = [ { user: "js", pass: "js" } ]; jetstream: enabled }
			$SYS { users = [ { user: "admin", pass: "admin" } ] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`))
	hub, hubOpts := RunServerWithConfig(hubConf)
	defer hub.Shutdown()

	// LEAF1 - JetStream enabled with domain "L1"
	leaf1Conf := createConfFile(t, []byte(fmt.Sprintf(`
		server_name: LEAF1
		listen: 127.0.0.1:-1
		jetstream {
			store_dir: '%s'
			domain: L1
		}
		accounts {
			JS { users = [ { user: "js", pass: "js" } ]; jetstream: enabled }
			$SYS { users = [ { user: "admin", pass: "admin" } ] }
		}
		leafnodes {
			remotes = [{
				url: nats-leaf://js:js@127.0.0.1:%d
				account: "JS"
			}]
		}
	`, t.TempDir(), hubOpts.LeafNode.Port)))
	leaf1, _ := RunServerWithConfig(leaf1Conf)
	defer leaf1.Shutdown()

	// LEAF2 - JetStream enabled with domain "L2" (NO cluster routes to LEAF1!)
	leaf2Conf := createConfFile(t, []byte(fmt.Sprintf(`
		server_name: LEAF2
		listen: 127.0.0.1:-1
		jetstream {
			store_dir: '%s'
			domain: L2
		}
		accounts {
			JS { users = [ { user: "js", pass: "js" } ]; jetstream: enabled }
			$SYS { users = [ { user: "admin", pass: "admin" } ] }
		}
		leafnodes {
			remotes = [{
				url: nats-leaf://js:js@127.0.0.1:%d
				account: "JS"
			}]
		}
	`, t.TempDir(), hubOpts.LeafNode.Port)))
	leaf2, _ := RunServerWithConfig(leaf2Conf)
	defer leaf2.Shutdown()

	// Wait for leafnode connections
	checkLeafNodeConnected(t, leaf1)
	checkLeafNodeConnected(t, leaf2)
	checkLeafNodeConnectedCount(t, hub, 2)

	t.Log("Topology established: HUB with 2 independent leafnodes (LEAF1, LEAF2)")

	// Connect to LEAF1 and create the source stream
	ncLeaf1, err := nats.Connect(leaf1.ClientURL(), nats.UserInfo("js", "js"))
	require_NoError(t, err)
	defer ncLeaf1.Close()

	jsLeaf1, err := jetstream.New(ncLeaf1)
	require_NoError(t, err)

	// Create source stream on LEAF1
	ctx := t.Context()
	sourceStream, err := jsLeaf1.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "SOURCE-STREAM",
		Subjects: []string{"events.>"},
	})
	require_NoError(t, err)
	t.Logf("Created SOURCE-STREAM on LEAF1 (domain: L1)")

	// Connect to LEAF2 and create the mirror stream
	ncLeaf2, err := nats.Connect(leaf2.ClientURL(), nats.UserInfo("js", "js"))
	require_NoError(t, err)
	defer ncLeaf2.Close()

	jsLeaf2, err := jetstream.New(ncLeaf2)
	require_NoError(t, err)

	// Create mirror stream on LEAF2 that mirrors from LEAF1
	// The key is using External.APIPrefix to reach LEAF1's JetStream domain
	mirrorStream, err := jsLeaf2.CreateStream(ctx, jetstream.StreamConfig{
		Name: "MIRROR-STREAM",
		Mirror: &jetstream.StreamSource{
			Name: "SOURCE-STREAM",
			External: &jetstream.ExternalStream{
				APIPrefix: "$JS.L1.API", // Route to LEAF1's JetStream domain
			},
		},
	})
	require_NoError(t, err)
	t.Logf("Created MIRROR-STREAM on LEAF2 (domain: L2) mirroring from LEAF1")

	// Publish messages to the source stream on LEAF1
	numMsgs := 100
	for i := 0; i < numMsgs; i++ {
		_, err := jsLeaf1.Publish(ctx, fmt.Sprintf("events.%d", i), []byte(fmt.Sprintf("message-%d", i)))
		require_NoError(t, err)
	}
	t.Logf("Published %d messages to SOURCE-STREAM on LEAF1", numMsgs)

	// Verify source stream has all messages
	sourceInfo, err := sourceStream.Info(ctx)
	require_NoError(t, err)
	if sourceInfo.State.Msgs != uint64(numMsgs) {
		t.Fatalf("Expected %d messages in SOURCE-STREAM, got %d", numMsgs, sourceInfo.State.Msgs)
	}

	// Wait for mirror to sync all messages
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		mirrorInfo, err := mirrorStream.Info(ctx)
		if err != nil {
			return err
		}
		if mirrorInfo.State.Msgs != uint64(numMsgs) {
			return fmt.Errorf("expected %d messages in MIRROR-STREAM, got %d", numMsgs, mirrorInfo.State.Msgs)
		}
		return nil
	})

	mirrorInfo, _ := mirrorStream.Info(ctx)
	t.Logf("MIRROR-STREAM on LEAF2 has %d messages (synced from LEAF1)", mirrorInfo.State.Msgs)

	// Verify we can read messages from the mirror
	consumer, err := mirrorStream.CreateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "test-consumer",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	require_NoError(t, err)

	// Fetch and verify messages
	msgs, err := consumer.Fetch(numMsgs)
	require_NoError(t, err)

	receivedCount := 0
	for msg := range msgs.Messages() {
		receivedCount++
		msg.Ack()
	}

	if receivedCount != numMsgs {
		t.Fatalf("Expected to receive %d messages from mirror, got %d", numMsgs, receivedCount)
	}
	t.Logf("Successfully read %d messages from MIRROR-STREAM on LEAF2", receivedCount)

	// Test: Publish more messages and verify they sync
	t.Run("continuous_sync", func(t *testing.T) {
		additionalMsgs := 50
		for i := 0; i < additionalMsgs; i++ {
			_, err := jsLeaf1.Publish(ctx, fmt.Sprintf("events.additional.%d", i), []byte(fmt.Sprintf("additional-%d", i)))
			require_NoError(t, err)
		}

		expectedTotal := numMsgs + additionalMsgs

		// Wait for mirror to sync
		checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
			mirrorInfo, err := mirrorStream.Info(ctx)
			if err != nil {
				return err
			}
			if mirrorInfo.State.Msgs != uint64(expectedTotal) {
				return fmt.Errorf("expected %d messages, got %d", expectedTotal, mirrorInfo.State.Msgs)
			}
			return nil
		})

		t.Logf("Mirror synced additional messages: now has %d total", expectedTotal)
	})

	t.Log("SUCCESS: JetStream mirroring works across independent leafnodes through HUB!")
}

// TestJetStreamSourceAcrossIndependentLeafnodes demonstrates JetStream stream
// sourcing (aggregation) between two independent leafnode servers.
// This shows bi-directional sourcing where each leaf can source from the other.
//
// Topology: Same as mirror test, but using Sources instead of Mirror
func TestJetStreamSourceAcrossIndependentLeafnodes(t *testing.T) {
	// HUB server
	hubConf := createConfFile(t, []byte(`
		server_name: HUB
		listen: 127.0.0.1:-1
		accounts {
			JS { users = [ { user: "js", pass: "js" } ]; jetstream: enabled }
			$SYS { users = [ { user: "admin", pass: "admin" } ] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`))
	hub, hubOpts := RunServerWithConfig(hubConf)
	defer hub.Shutdown()

	// LEAF1 - JetStream domain "L1"
	leaf1Conf := createConfFile(t, []byte(fmt.Sprintf(`
		server_name: LEAF1
		listen: 127.0.0.1:-1
		jetstream {
			store_dir: '%s'
			domain: L1
		}
		accounts {
			JS { users = [ { user: "js", pass: "js" } ]; jetstream: enabled }
			$SYS { users = [ { user: "admin", pass: "admin" } ] }
		}
		leafnodes {
			remotes = [{
				url: nats-leaf://js:js@127.0.0.1:%d
				account: "JS"
			}]
		}
	`, t.TempDir(), hubOpts.LeafNode.Port)))
	leaf1, _ := RunServerWithConfig(leaf1Conf)
	defer leaf1.Shutdown()

	// LEAF2 - JetStream domain "L2"
	leaf2Conf := createConfFile(t, []byte(fmt.Sprintf(`
		server_name: LEAF2
		listen: 127.0.0.1:-1
		jetstream {
			store_dir: '%s'
			domain: L2
		}
		accounts {
			JS { users = [ { user: "js", pass: "js" } ]; jetstream: enabled }
			$SYS { users = [ { user: "admin", pass: "admin" } ] }
		}
		leafnodes {
			remotes = [{
				url: nats-leaf://js:js@127.0.0.1:%d
				account: "JS"
			}]
		}
	`, t.TempDir(), hubOpts.LeafNode.Port)))
	leaf2, _ := RunServerWithConfig(leaf2Conf)
	defer leaf2.Shutdown()

	// Wait for connections
	checkLeafNodeConnected(t, leaf1)
	checkLeafNodeConnected(t, leaf2)
	checkLeafNodeConnectedCount(t, hub, 2)

	// Connect to both leaves
	ncLeaf1, err := nats.Connect(leaf1.ClientURL(), nats.UserInfo("js", "js"))
	require_NoError(t, err)
	defer ncLeaf1.Close()
	jsLeaf1, err := jetstream.New(ncLeaf1)
	require_NoError(t, err)

	ncLeaf2, err := nats.Connect(leaf2.ClientURL(), nats.UserInfo("js", "js"))
	require_NoError(t, err)
	defer ncLeaf2.Close()
	jsLeaf2, err := jetstream.New(ncLeaf2)
	require_NoError(t, err)

	ctx := t.Context()

	// Create streams on each leaf that source from the other
	// LEAF1: ORDERS stream (local) + sources from LEAF2's ORDERS
	_, err = jsLeaf1.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "ORDERS-L1",
		Subjects: []string{"orders.region1.>"},
		Sources: []*jetstream.StreamSource{
			{
				Name:          "ORDERS-L2",
				FilterSubject: "orders.region2.>",
				External: &jetstream.ExternalStream{
					APIPrefix: "$JS.L2.API",
				},
			},
		},
	})
	require_NoError(t, err)
	t.Log("Created ORDERS-L1 on LEAF1 (sources from LEAF2)")

	// LEAF2: ORDERS stream (local) + sources from LEAF1's ORDERS
	_, err = jsLeaf2.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "ORDERS-L2",
		Subjects: []string{"orders.region2.>"},
		Sources: []*jetstream.StreamSource{
			{
				Name:          "ORDERS-L1",
				FilterSubject: "orders.region1.>",
				External: &jetstream.ExternalStream{
					APIPrefix: "$JS.L1.API",
				},
			},
		},
	})
	require_NoError(t, err)
	t.Log("Created ORDERS-L2 on LEAF2 (sources from LEAF1)")

	// Publish orders to each region
	numOrders := 50
	for i := 0; i < numOrders; i++ {
		_, err := jsLeaf1.Publish(ctx, fmt.Sprintf("orders.region1.%d", i), []byte(fmt.Sprintf("order-r1-%d", i)))
		require_NoError(t, err)
		_, err = jsLeaf2.Publish(ctx, fmt.Sprintf("orders.region2.%d", i), []byte(fmt.Sprintf("order-r2-%d", i)))
		require_NoError(t, err)
	}
	t.Logf("Published %d orders to each region", numOrders)

	// Wait for sourcing to complete - each stream should have both local and sourced messages
	expectedPerStream := numOrders * 2 // local + sourced

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		stream1, err := jsLeaf1.Stream(ctx, "ORDERS-L1")
		if err != nil {
			return err
		}
		info1, err := stream1.Info(ctx)
		if err != nil {
			return err
		}
		if info1.State.Msgs != uint64(expectedPerStream) {
			return fmt.Errorf("ORDERS-L1: expected %d messages, got %d", expectedPerStream, info1.State.Msgs)
		}
		return nil
	})

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		stream2, err := jsLeaf2.Stream(ctx, "ORDERS-L2")
		if err != nil {
			return err
		}
		info2, err := stream2.Info(ctx)
		if err != nil {
			return err
		}
		if info2.State.Msgs != uint64(expectedPerStream) {
			return fmt.Errorf("ORDERS-L2: expected %d messages, got %d", expectedPerStream, info2.State.Msgs)
		}
		return nil
	})

	stream1, _ := jsLeaf1.Stream(ctx, "ORDERS-L1")
	info1, _ := stream1.Info(ctx)
	stream2, _ := jsLeaf2.Stream(ctx, "ORDERS-L2")
	info2, _ := stream2.Info(ctx)

	t.Logf("ORDERS-L1 has %d messages (local region1 + sourced region2)", info1.State.Msgs)
	t.Logf("ORDERS-L2 has %d messages (local region2 + sourced region1)", info2.State.Msgs)

	t.Log("SUCCESS: Bi-directional JetStream sourcing works across independent leafnodes!")
}
