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
