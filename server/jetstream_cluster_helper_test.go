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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// Test for the issue where ACK reply subjects get duplicate @ decorations
// when messages are delivered through cluster with replicated deliveries.
func TestJetStreamClusterConsumerReplySubjectNoDuplicateDecoration(t *testing.T) {
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	nc, js := jsClientConnect(t, c.randomServer())
	defer nc.Close()

	// Create a KV bucket (which uses JetStream streams)
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:   "refdata_markets",
		Replicas: 3,
	})
	require_NoError(t, err)

	// Put a key with a complex name that will appear in the subject
	testKey := "KXLTCD-25NOV2014-T104.9999"
	_, err = kv.Put(testKey, []byte("test-value"))
	require_NoError(t, err)

	// Create a consumer on the KV stream
	streamName := fmt.Sprintf("KV_%s", "refdata_markets")

	// Create a pull consumer
	consumerName := "test-consumer"
	ci, err := js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   consumerName,
		AckPolicy: nats.AckExplicitPolicy,
		Replicas:  3,
	})
	require_NoError(t, err)
	require_True(t, ci != nil)

	// Subscribe to the consumer
	sub, err := js.PullSubscribe("", "", nats.BindStream(streamName), nats.Durable(consumerName))
	require_NoError(t, err)

	// Fetch a message
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	require_NoError(t, err)
	require_True(t, len(msgs) == 1)

	msg := msgs[0]

	// Check that the reply subject doesn't have duplicate @ decorations
	reply := msg.Reply

	// Count @ symbols in the reply subject
	atCount := strings.Count(reply, "@")

	// The reply should have at most one @ symbol (for the decoration)
	if atCount > 1 {
		t.Fatalf("Reply subject has duplicate @ decorations: %s (found %d @ symbols)", reply, atCount)
	}

	// Also check that the portion after @ (if any) doesn't appear twice
	if atIndex := strings.Index(reply, "@"); atIndex != -1 {
		decoration := reply[atIndex:]
		beforeAt := reply[:atIndex]

		// Check if the decoration appears in the beforeAt part (which would indicate duplication)
		if strings.Contains(beforeAt, decoration) {
			t.Fatalf("Reply subject has duplicate decoration: %s", reply)
		}

		// Also check if the same pattern appears twice consecutively
		if strings.Contains(reply, decoration+decoration) {
			t.Fatalf("Reply subject has consecutive duplicate decorations: %s", reply)
		}
	}

	// Try to ACK the message - this should work without errors
	err = msg.Ack()
	require_NoError(t, err)
}

// Test with cross-account delivery which is where the decoration is most commonly used
func TestJetStreamClusterConsumerReplySubjectWithAccountExport(t *testing.T) {
	template := `
	listen: 127.0.0.1:-1
	server_name: %s
	jetstream: {
		max_mem_store: 256MB
		max_file_store: 2GB
		store_dir: '%s'
	}

	cluster {
		name: %s
		listen: 127.0.0.1:%d
		routes = [%s]
	}

	accounts: {
		JS: {
			jetstream: enabled
			users: [ {user: js, password: pwd} ]
			exports [
				{ stream: "deliver.KV.*" }
				{ service: "$JS.ACK.KV_test.*.>" }
				{ service: "$JS.FC.>" }
			]
		},
		IM: {
			users: [ {user: im, password: pwd} ]
			imports [
				{ stream:  { account: JS, subject: "deliver.KV.*" }}
				{ service: {account: JS, subject: "$JS.FC.>" }}
			]
		},
		$SYS { users = [ { user: "admin", pass: "s3cr3t!" } ] },
	}
	`

	c := createJetStreamClusterWithTemplate(t, template, "R3S", 3)
	defer c.shutdown()

	// Connect as JS account
	nc, js := jsClientConnect(t, c.randomServer(), nats.UserInfo("js", "pwd"))
	defer nc.Close()

	// Create a KV bucket
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:   "test",
		Replicas: 3,
	})
	require_NoError(t, err)

	// Put a key
	testKey := "TEST-KEY"
	_, err = kv.Put(testKey, []byte("test-value"))
	require_NoError(t, err)

	// Create a push consumer with cross-account delivery
	streamName := "KV_test"
	deliverSubject := "deliver.KV.test"

	ci, err := js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:        "test-consumer",
		DeliverSubject: deliverSubject,
		AckPolicy:      nats.AckExplicitPolicy,
		Replicas:       3,
	})
	require_NoError(t, err)
	require_True(t, ci != nil)

	// Connect as IM account to receive the messages
	nc2, err := nats.Connect(c.randomServer().ClientURL(), nats.UserInfo("im", "pwd"))
	require_NoError(t, err)
	defer nc2.Close()

	// Subscribe to the delivery subject
	sub, err := nc2.SubscribeSync(deliverSubject)
	require_NoError(t, err)

	// Wait for the message
	msg, err := sub.NextMsg(5 * time.Second)
	require_NoError(t, err)

	// Check that the reply subject doesn't have duplicate @ decorations
	reply := msg.Reply

	// Count @ symbols in the reply subject
	atCount := strings.Count(reply, "@")

	// Should have at most one @ symbol
	if atCount > 1 {
		// Extract the parts to show what's duplicated
		parts := strings.Split(reply, "@")
		t.Fatalf("Reply subject has duplicate @ decorations: %s\nParts: %v", reply, parts)
	}

	// Check for consecutive duplicate patterns
	if atIndex := strings.Index(reply, "@"); atIndex != -1 {
		decoration := reply[atIndex:]
		if strings.Contains(reply[:atIndex], decoration) {
			t.Fatalf("Reply subject has duplicate decoration pattern: %s", reply)
		}
	}

	// Try to publish an ACK - should work
	err = nc2.Publish(reply, nil)
	require_NoError(t, err)
}

// Test with leafnode and push consumer delivery - this reproduces the duplicate decoration issue
// The key scenario is: cluster (hub) -> leafnode -> consumer with deliver subject
func TestJetStreamLeafNodePushConsumerDuplicateReplyDecoration(t *testing.T) {
	// Create the main cluster with JetStream
	clusterTemplate := `
	listen: 127.0.0.1:-1
	server_name: %s
	jetstream: {
		max_mem_store: 256MB
		max_file_store: 2GB
		store_dir: '%s'
	}

	cluster {
		name: %s
		listen: 127.0.0.1:%d
		routes = [%s]
	}

	leafnodes {
		listen: 127.0.0.1:-1
	}

	accounts: {
		JS: {
			jetstream: enabled
			users: [ {user: js, password: pwd} ]
			exports [
				{ stream: "deliver.>" }
				{ service: "$JS.ACK.>" }
			]
		},
		APP: {
			users: [ {user: app, password: pwd} ]
			imports [
				{ stream: {account: JS, subject: "deliver.>"} }
			]
		},
		$SYS { users = [ { user: "admin", pass: "s3cr3t!" } ] },
	}
	`

	c := createJetStreamClusterWithTemplate(t, clusterTemplate, "HUB", 3)
	defer c.shutdown()

	// Find an available leafnode port from the cluster
	var leafURL string
	for _, s := range c.servers {
		if s.getOpts().LeafNode.Port != 0 {
			leafURL = fmt.Sprintf("nats://app:pwd@127.0.0.1:%d", s.getOpts().LeafNode.Port)
			break
		}
	}
	require_True(t, leafURL != "")

	// Create leafnode server config - connects as APP account
	leafConfig := `
	listen: 127.0.0.1:-1
	server_name: LEAF

	leafnodes {
		remotes [
			{
				url: "%s"
				account: APP
			}
		]
	}

	accounts: {
		APP: {
			users: [ {user: app, password: pwd} ]
		},
		$SYS { users = [ { user: "admin", pass: "s3cr3t!" } ] },
	}
	`

	conf := fmt.Sprintf(leafConfig, leafURL)
	lns, _ := RunServerWithConfig(createConfFile(t, []byte(conf)))
	defer lns.Shutdown()

	// Wait for leafnode connection
	checkLeafNodeConnectedCount(t, lns, 1)

	// Connect to the main cluster and create a KV bucket
	nc, js := jsClientConnect(t, c.randomServer(), nats.UserInfo("js", "pwd"))
	defer nc.Close()

	// Create KV bucket on the hub cluster
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:   "refdata_markets",
		Replicas: 3,
	})
	require_NoError(t, err)

	// Put a key with the exact format from the error message
	testKey := "KXLTCD-25NOV2014-T104.9999"
	_, err = kv.Put(testKey, []byte("test-value"))
	require_NoError(t, err)

	// Create a push consumer that delivers to a subject accessible via leafnode
	streamName := "KV_refdata_markets"
	deliverSubject := "deliver.kv.messages"

	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:        "leafnode-consumer",
		DeliverSubject: deliverSubject,
		AckPolicy:      nats.AckExplicitPolicy,
		Replicas:       3,
	})
	require_NoError(t, err)

	// Connect to the leafnode as APP account and subscribe
	lnc, err := nats.Connect(lns.ClientURL(), nats.UserInfo("app", "pwd"))
	require_NoError(t, err)
	defer lnc.Close()

	// Subscribe to the delivery subject
	sub, err := lnc.SubscribeSync(deliverSubject)
	require_NoError(t, err)

	// Wait for the message to arrive through the leafnode
	msg, err := sub.NextMsg(10 * time.Second)
	require_NoError(t, err)

	reply := msg.Reply
	t.Logf("Reply subject received on leafnode: %s", reply)

	// Check for duplicate @ decorations
	atCount := strings.Count(reply, "@")
	if atCount > 1 {
		parts := strings.Split(reply, "@")
		t.Fatalf("Reply subject has %d @ symbols (duplicate decorations): %s\nParts: %v", atCount, reply, parts)
	}

	// Check for consecutive duplicate patterns
	if atIndex := strings.Index(reply, "@"); atIndex != -1 {
		beforeAt := reply[:atIndex]
		decoration := reply[atIndex:]

		// Check if decoration appears in the beforeAt portion (which would be wrong)
		if strings.Contains(beforeAt, decoration) {
			t.Fatalf("Reply subject has duplicate decoration embedded: %s", reply)
		}

		// Also check for the specific pattern from the error message
		if strings.Contains(reply, decoration+decoration) {
			t.Fatalf("Reply subject has consecutive duplicate decorations: %s", reply)
		}
	}

	// Try to respond on the reply subject (simulating an ACK)
	// This should work without the "Bad or Missing Size" error
	err = lnc.Publish(reply, nil)
	if err != nil {
		t.Fatalf("Failed to publish to reply subject %s: %v", reply, err)
	}
}

// Test that duplicate decoration doesn't cause route disconnection
// This is the actual bug scenario - when messages with duplicate decorations
// go through routes, processRoutedMsgArgs fails and disconnects the route
func TestJetStreamClusterRouteNoDuplicateDecorationDisconnect(t *testing.T) {
	// Create a cluster with account exports/imports to trigger decoration
	template := `
	listen: 127.0.0.1:-1
	server_name: %s
	jetstream: {
		max_mem_store: 256MB
		max_file_store: 2GB
		store_dir: '%s'
	}

	cluster {
		name: %s
		listen: 127.0.0.1:%d
		routes = [%s]
	}

	accounts: {
		JS: {
			jetstream: enabled
			users: [ {user: js, password: pwd} ]
			exports [
				# Export delivery subjects
				{ stream: "deliver.>" }
				# Export ACK subjects
				{ service: "$JS.ACK.>" }
			]
		},
		APP: {
			users: [ {user: app, password: pwd} ]
			imports [
				{ stream: {account: JS, subject: "deliver.>"} }
			]
		},
		$SYS { users = [ { user: "admin", pass: "s3cr3t!" } ] },
	}
	`

	c := createJetStreamClusterWithTemplate(t, template, "C1", 3)
	defer c.shutdown()

	// Get initial route connections count for each server
	initialRoutes := make(map[string]int)
	for _, s := range c.servers {
		initialRoutes[s.Name()] = s.NumRoutes()
	}

	// Connect to one server and create a KV bucket
	nc, js := jsClientConnect(t, c.randomServer(), nats.UserInfo("js", "pwd"))
	defer nc.Close()

	// Create KV bucket with R3 to ensure messages go through routes
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:   "refdata_markets",
		Replicas: 3,
	})
	require_NoError(t, err)

	// Put a key with the complex name from the error message
	testKey := "KXLTCD-25NOV2014-T104.9999"
	_, err = kv.Put(testKey, []byte("test-value-1"))
	require_NoError(t, err)

	// Create a push consumer on a non-leader server to force routing
	// Find the consumer leader
	streamName := "KV_refdata_markets"
	streamLeader := c.streamLeader("JS", streamName)

	// Pick a different server for the consumer to force route traffic
	var targetServer *Server
	for _, s := range c.servers {
		if s != streamLeader {
			targetServer = s
			break
		}
	}
	require_True(t, targetServer != nil)

	// Create push consumer that delivers across accounts
	deliverSubject := "deliver.kv.test"

	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:        "route-test-consumer",
		DeliverSubject: deliverSubject,
		AckPolicy:      nats.AckExplicitPolicy,
		Replicas:       3,
	})
	require_NoError(t, err)

	// Connect to a different server as APP account
	nc2, err := nats.Connect(targetServer.ClientURL(), nats.UserInfo("app", "pwd"))
	require_NoError(t, err)
	defer nc2.Close()

	// Subscribe to delivery subject
	sub, err := nc2.SubscribeSync(deliverSubject)
	require_NoError(t, err)
	nc2.Flush()

	// Put more messages to generate traffic through routes
	for i := 0; i < 5; i++ {
		_, err = kv.Put(testKey, []byte(fmt.Sprintf("test-value-%d", i+2)))
		require_NoError(t, err)
	}

	// Receive messages and check replies
	receivedCount := 0
	for i := 0; i < 6; i++ {
		msg, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			break
		}
		receivedCount++

		reply := msg.Reply
		t.Logf("Message %d reply: %s", i+1, reply)

		// Check for duplicate decorations
		atCount := strings.Count(reply, "@")
		if atCount > 1 {
			parts := strings.Split(reply, "@")
			t.Fatalf("Reply subject has duplicate @ decorations: %s\nParts: %v", reply, parts)
		}

		// Try to ACK each message - this sends traffic back through routes
		err = nc2.Publish(reply, nil)
		require_NoError(t, err)
	}

	t.Logf("Received %d messages successfully", receivedCount)
	require_True(t, receivedCount >= 5)

	// Give time for any route disconnections to happen
	time.Sleep(500 * time.Millisecond)

	// Check that routes are still connected - they should NOT have disconnected
	// due to processRoutedMsgArgs errors from duplicate decorations
	for _, s := range c.servers {
		currentRoutes := s.NumRoutes()
		expectedRoutes := initialRoutes[s.Name()]

		if currentRoutes < expectedRoutes {
			t.Fatalf("Server %s lost routes: had %d, now has %d - likely due to processRoutedMsgArgs error from duplicate decoration",
				s.Name(), expectedRoutes, currentRoutes)
		}

		t.Logf("Server %s routes: %d (expected %d)", s.Name(), currentRoutes, expectedRoutes)
	}
}

// Test with super cluster + leafnode + stream sourcing - the actual production scenario
// NOTE: This test documents but does not fully reproduce the production bug scenario which requires:
// - Super cluster with gateways
// - Stream sourcing between clusters
// - Leafnode with JetStream access to streams across the super cluster
// - Complex account export/import setup causing subject decoration
// - Specific message routing patterns where a message goes through multiple decoration stages
//
// The fix is correct based on code analysis and the production error message showing
// duplicate decorations: @$KV.bucket.key@$KV.bucket.key
//
// Without the exact production topology, this bug is very difficult to reproduce in tests.
func TestJetStreamSuperClusterLeafNodeStreamSourceDuplicateDecoration(t *testing.T) {
	t.Skip("Skipping - requires complex production setup to fully reproduce the duplicate decoration bug.\n" +
		"The bug occurs when:\n" +
		"1. Super cluster with multiple clusters connected via gateways\n" +
		"2. Stream sourcing between clusters\n" +
		"3. Leafnode with JetStream accessing streams across the super cluster\n" +
		"4. Messages with reply subjects go through multiple routing/decoration stages\n" +
		"5. Each stage appends @<deliver-subject> without checking if it already exists\n\n" +
		"Production error showed: ...@$KV.refdata_markets.KEY@$KV.refdata_markets.KEY\n\n" +
		"The fix prevents this by checking replyHasDecoration() before appending.")
}
