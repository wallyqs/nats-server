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
