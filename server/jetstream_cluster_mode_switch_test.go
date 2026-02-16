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

//go:build !skip_js_tests && !skip_js_cluster_tests_4

package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamStandaloneToClusteredModeSwitch tests that a server started in
// standalone mode can be reconfigured to run in clustered mode without losing
// stream data, consumers, or message state.
func TestJetStreamStandaloneToClusteredModeSwitch(t *testing.T) {
	// Phase 1: Run a standalone JetStream server, create streams and publish data.
	storeDir := t.TempDir()

	opts := DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.ServerName = "S1"
	s := RunServer(&opts)

	nc, err := nats.Connect(s.ClientURL())
	require_NoError(t, err)

	// Create a file-backed stream.
	req, _ := json.Marshal(&StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo.>"},
		Storage:  FileStorage,
	})
	rmsg, err := nc.Request(fmt.Sprintf(JSApiStreamCreateT, "TEST"), req, 5*time.Second)
	require_NoError(t, err)
	var resp JSApiStreamCreateResponse
	err = json.Unmarshal(rmsg.Data, &resp)
	require_NoError(t, err)
	if resp.Error != nil {
		t.Fatalf("Unexpected error creating stream: %+v", resp.Error)
	}

	// Publish messages.
	numMessages := 100
	for i := 0; i < numMessages; i++ {
		_, err := nc.Request(fmt.Sprintf("foo.%d", i), []byte(fmt.Sprintf("msg-%d", i)), 2*time.Second)
		require_NoError(t, err)
	}

	// Create a durable consumer.
	consReq, _ := json.Marshal(&CreateConsumerRequest{
		Stream: "TEST",
		Config: ConsumerConfig{
			Durable:       "dur",
			AckPolicy:     AckExplicit,
			DeliverPolicy: DeliverAll,
		},
	})
	rmsg, err = nc.Request(fmt.Sprintf(JSApiDurableCreateT, "TEST", "dur"), consReq, 5*time.Second)
	require_NoError(t, err)
	var consResp JSApiConsumerCreateResponse
	err = json.Unmarshal(rmsg.Data, &consResp)
	require_NoError(t, err)
	if consResp.Error != nil {
		t.Fatalf("Unexpected error creating consumer: %+v", consResp.Error)
	}

	nc.Close()
	s.Shutdown()
	s.WaitForShutdown()

	// Phase 2: Restart as part of a 3-node cluster using the same store directory.
	// The modify callback swaps the auto-generated storeDir for our standalone one
	// on the first server (S-1) only.
	modify := func(serverName, clusterName, autoStoreDir, conf string) string {
		if serverName == "S-1" {
			return strings.Replace(conf, autoStoreDir, storeDir, 1)
		}
		return conf
	}
	c := createJetStreamClusterWithTemplateAndModHook(t, jsClusterTempl, "C1", 3, modify)
	defer c.shutdown()

	c.waitOnLeader()

	// Ensure S-1 (the server with standalone data) becomes the meta leader.
	// This is required because only the meta leader can propose stream adoptions.
	var s1 *Server
	for _, srv := range c.servers {
		if srv.Name() == "S-1" {
			s1 = srv
			break
		}
	}
	if s1 == nil {
		t.Fatalf("Could not find server S-1")
	}

	// Step down non-S-1 leaders until S-1 becomes meta leader.
	checkFor(t, 20*time.Second, 500*time.Millisecond, func() error {
		if s1.JetStreamIsLeader() {
			return nil
		}
		leader := c.leader()
		if leader != nil && leader != s1 {
			nc2, err := nats.Connect(leader.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
			if err != nil {
				return err
			}
			defer nc2.Close()
			nc2.Request(JSApiLeaderStepDown, nil, 2*time.Second)
		}
		return fmt.Errorf("waiting for S-1 to become meta leader")
	})

	// Wait for adoption proposals to be processed (checkForOrphans runs ~30s after recovery).
	checkFor(t, 60*time.Second, 500*time.Millisecond, func() error {
		nc2, err := nats.Connect(s1.ClientURL())
		if err != nil {
			return err
		}
		defer nc2.Close()

		rmsg, err := nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "TEST"), nil, 5*time.Second)
		if err != nil {
			return err
		}
		var siResp JSApiStreamInfoResponse
		if err := json.Unmarshal(rmsg.Data, &siResp); err != nil {
			return err
		}
		if siResp.Error != nil {
			return fmt.Errorf("stream info error: %+v", siResp.Error)
		}
		if siResp.State.Msgs != uint64(numMessages) {
			return fmt.Errorf("expected %d messages, got %d", numMessages, siResp.State.Msgs)
		}
		return nil
	})

	// Full verification with a fresh connection.
	nc2, err := nats.Connect(s1.ClientURL())
	require_NoError(t, err)
	defer nc2.Close()

	// Verify consumer exists.
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiConsumerInfoT, "TEST", "dur"), nil, 5*time.Second)
	require_NoError(t, err)
	var ciResp JSApiConsumerInfoResponse
	err = json.Unmarshal(rmsg.Data, &ciResp)
	require_NoError(t, err)
	if ciResp.Error != nil {
		t.Fatalf("Consumer info error: %+v", ciResp.Error)
	}
	if ciResp.Config.Durable != "dur" {
		t.Fatalf("Expected durable name %q, got %q", "dur", ciResp.Config.Durable)
	}

	// Verify we can still publish new messages.
	_, err = nc2.Request("foo.new", []byte("new-message"), 2*time.Second)
	require_NoError(t, err)

	// Check total is now numMessages + 1.
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "TEST"), nil, 5*time.Second)
	require_NoError(t, err)
	var siResp2 JSApiStreamInfoResponse
	err = json.Unmarshal(rmsg.Data, &siResp2)
	require_NoError(t, err)
	if siResp2.Error != nil {
		t.Fatalf("Stream info error: %+v", siResp2.Error)
	}
	if siResp2.State.Msgs != uint64(numMessages+1) {
		t.Fatalf("Expected %d messages after publish, got %d", numMessages+1, siResp2.State.Msgs)
	}
}

// TestJetStreamStandaloneToClusteredNonLeaderAdoption tests that when a server with
// standalone data joins a cluster and is NOT the meta leader, the streams are still
// adopted via internal messaging to the leader.
func TestJetStreamStandaloneToClusteredNonLeaderAdoption(t *testing.T) {
	// Phase 1: Create standalone server with data.
	storeDir := t.TempDir()

	opts := DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.ServerName = "S1"
	s := RunServer(&opts)

	nc, err := nats.Connect(s.ClientURL())
	require_NoError(t, err)

	req, _ := json.Marshal(&StreamConfig{
		Name:     "ORDERS",
		Subjects: []string{"orders.>"},
		Storage:  FileStorage,
	})
	rmsg, err := nc.Request(fmt.Sprintf(JSApiStreamCreateT, "ORDERS"), req, 5*time.Second)
	require_NoError(t, err)
	var resp JSApiStreamCreateResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &resp))
	if resp.Error != nil {
		t.Fatalf("Unexpected error creating stream: %+v", resp.Error)
	}

	numMessages := 50
	for i := 0; i < numMessages; i++ {
		_, err := nc.Request(fmt.Sprintf("orders.%d", i), []byte(fmt.Sprintf("order-%d", i)), 2*time.Second)
		require_NoError(t, err)
	}

	consReq, _ := json.Marshal(&CreateConsumerRequest{
		Stream: "ORDERS",
		Config: ConsumerConfig{
			Durable:       "processor",
			AckPolicy:     AckExplicit,
			DeliverPolicy: DeliverAll,
		},
	})
	rmsg, err = nc.Request(fmt.Sprintf(JSApiDurableCreateT, "ORDERS", "processor"), consReq, 5*time.Second)
	require_NoError(t, err)
	var consResp JSApiConsumerCreateResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &consResp))
	if consResp.Error != nil {
		t.Fatalf("Unexpected error creating consumer: %+v", consResp.Error)
	}

	nc.Close()
	s.Shutdown()
	s.WaitForShutdown()

	// Phase 2: Start a 3-node cluster where S-3 has the standalone data.
	// S-1 or S-2 will likely become leader, so S-3 must use the non-leader
	// adoption path (requestAdoptionFromLeader).
	modify := func(serverName, clusterName, autoStoreDir, conf string) string {
		if serverName == "S-3" {
			return strings.Replace(conf, autoStoreDir, storeDir, 1)
		}
		return conf
	}
	c := createJetStreamClusterWithTemplateAndModHook(t, jsClusterTempl, "NL", 3, modify)
	defer c.shutdown()

	c.waitOnLeader()

	// Ensure S-3 is NOT the leader. If it is, step it down so we test the non-leader path.
	var s3 *Server
	for _, srv := range c.servers {
		if srv.Name() == "S-3" {
			s3 = srv
			break
		}
	}
	require_NotNil(t, s3)

	if s3.JetStreamIsLeader() {
		nc2, err := nats.Connect(s3.ClientURL(), nats.UserInfo("admin", "s3cr3t!"))
		require_NoError(t, err)
		nc2.Request(JSApiLeaderStepDown, nil, 2*time.Second)
		nc2.Close()
		c.waitOnLeader()
		// Wait for a different leader.
		checkFor(t, 10*time.Second, 250*time.Millisecond, func() error {
			if s3.JetStreamIsLeader() {
				return fmt.Errorf("S-3 is still leader")
			}
			return nil
		})
	}

	// Wait for the adoption to complete via internal messaging.
	// checkForOrphans fires ~30s after recovery, then S-3 sends adoption request to the leader.
	leader := c.leader()
	require_NotNil(t, leader)

	checkFor(t, 60*time.Second, 500*time.Millisecond, func() error {
		nc2, err := nats.Connect(leader.ClientURL())
		if err != nil {
			return err
		}
		defer nc2.Close()

		rmsg, err := nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "ORDERS"), nil, 5*time.Second)
		if err != nil {
			return err
		}
		var siResp JSApiStreamInfoResponse
		if err := json.Unmarshal(rmsg.Data, &siResp); err != nil {
			return err
		}
		if siResp.Error != nil {
			return fmt.Errorf("stream info error: %+v", siResp.Error)
		}
		if siResp.State.Msgs != uint64(numMessages) {
			return fmt.Errorf("expected %d messages, got %d", numMessages, siResp.State.Msgs)
		}
		return nil
	})

	// Verify consumer was adopted too.
	nc2, err := nats.Connect(leader.ClientURL())
	require_NoError(t, err)
	defer nc2.Close()

	rmsg, err = nc2.Request(fmt.Sprintf(JSApiConsumerInfoT, "ORDERS", "processor"), nil, 5*time.Second)
	require_NoError(t, err)
	var ciResp JSApiConsumerInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &ciResp))
	if ciResp.Error != nil {
		t.Fatalf("Consumer info error: %+v", ciResp.Error)
	}
	if ciResp.Config.Durable != "processor" {
		t.Fatalf("Expected durable name %q, got %q", "processor", ciResp.Config.Durable)
	}

	// Verify new publishes work.
	_, err = nc2.Request("orders.new", []byte("new-order"), 2*time.Second)
	require_NoError(t, err)

	rmsg, err = nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "ORDERS"), nil, 5*time.Second)
	require_NoError(t, err)
	var siResp2 JSApiStreamInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &siResp2))
	if siResp2.Error != nil {
		t.Fatalf("Stream info error: %+v", siResp2.Error)
	}
	if siResp2.State.Msgs != uint64(numMessages+1) {
		t.Fatalf("Expected %d messages after publish, got %d", numMessages+1, siResp2.State.Msgs)
	}
}

// TestJetStreamStandaloneToClusteredDuplicateStreamName tests that when a standalone
// server has a stream with the same name as one already in the cluster, the cluster
// stream takes precedence and the standalone stream is not adopted.
func TestJetStreamStandaloneToClusteredDuplicateStreamName(t *testing.T) {
	// Phase 1: Create standalone server with a stream named "EVENTS".
	storeDir := t.TempDir()

	opts := DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.ServerName = "S1"
	s := RunServer(&opts)

	nc, err := nats.Connect(s.ClientURL())
	require_NoError(t, err)

	// Create stream with subjects "standalone.>"
	req, _ := json.Marshal(&StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{"standalone.>"},
		Storage:  FileStorage,
	})
	rmsg, err := nc.Request(fmt.Sprintf(JSApiStreamCreateT, "EVENTS"), req, 5*time.Second)
	require_NoError(t, err)
	var resp JSApiStreamCreateResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &resp))
	if resp.Error != nil {
		t.Fatalf("Unexpected error creating stream: %+v", resp.Error)
	}

	// Publish some messages.
	for i := 0; i < 10; i++ {
		_, err := nc.Request(fmt.Sprintf("standalone.%d", i), []byte("data"), 2*time.Second)
		require_NoError(t, err)
	}

	nc.Close()
	s.Shutdown()
	s.WaitForShutdown()

	// Phase 2: Start a 3-node cluster and create a DIFFERENT stream also named "EVENTS".
	c := createJetStreamCluster(t, jsClusterTempl, "DUP", _EMPTY_, 3, 22_033, true)
	defer c.shutdown()

	c.waitOnLeader()
	leader := c.leader()

	nc2, err := nats.Connect(leader.ClientURL())
	require_NoError(t, err)
	defer nc2.Close()

	// Create cluster stream "EVENTS" with different subjects.
	clusterReq, _ := json.Marshal(&StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{"cluster.>"},
		Storage:  FileStorage,
		Replicas: 3,
	})
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiStreamCreateT, "EVENTS"), clusterReq, 5*time.Second)
	require_NoError(t, err)
	var clusterResp JSApiStreamCreateResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &clusterResp))
	if clusterResp.Error != nil {
		t.Fatalf("Unexpected error creating cluster stream: %+v", clusterResp.Error)
	}

	// Publish messages to the cluster stream.
	for i := 0; i < 20; i++ {
		_, err := nc2.Request(fmt.Sprintf("cluster.%d", i), []byte("cluster-data"), 2*time.Second)
		require_NoError(t, err)
	}
	nc2.Close()

	// Phase 3: Now add a 4th server that uses the standalone store dir.
	// This simulates adding a standalone server to an existing cluster.
	srvNew := c.addInNewServer()
	// Swap store dir: shutdown the new server, restart with standalone storeDir.
	newOpts := srvNew.getOpts().Clone()
	srvNew.Shutdown()
	srvNew.WaitForShutdown()
	// Remove the last server from the cluster list since we'll replace it.
	c.servers = c.servers[:len(c.servers)-1]
	c.opts = c.opts[:len(c.opts)-1]

	newOpts.StoreDir = storeDir
	newOpts.Port = -1
	srvNew = RunServer(newOpts)
	c.servers = append(c.servers, srvNew)
	c.opts = append(c.opts, newOpts)
	c.checkClusterFormed()

	// Wait for the new server to sync with the cluster.
	// The checkForOrphans on the new server should detect the duplicate and skip adoption.
	time.Sleep(35 * time.Second)

	// Verify the cluster stream "EVENTS" is intact with its original config.
	leader = c.leader()
	require_NotNil(t, leader)

	nc3, err := nats.Connect(leader.ClientURL())
	require_NoError(t, err)
	defer nc3.Close()

	rmsg, err = nc3.Request(fmt.Sprintf(JSApiStreamInfoT, "EVENTS"), nil, 5*time.Second)
	require_NoError(t, err)
	var siResp JSApiStreamInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &siResp))
	if siResp.Error != nil {
		t.Fatalf("Stream info error: %+v", siResp.Error)
	}

	// Should have the 20 cluster messages, NOT the 10 standalone ones.
	if siResp.State.Msgs != 20 {
		t.Fatalf("Expected 20 cluster messages, got %d", siResp.State.Msgs)
	}

	// Verify the stream subjects are from the cluster version, not standalone.
	if len(siResp.Config.Subjects) != 1 || siResp.Config.Subjects[0] != "cluster.>" {
		t.Fatalf("Expected cluster subjects [cluster.>], got %v", siResp.Config.Subjects)
	}

	// The standalone data should still exist on the new server's disk (not deleted),
	// but should NOT be part of the cluster metadata.
}

// TestJetStreamStandaloneToClusteredPTCMarkerPersistence tests that the ptc marker
// file protects unadopted streams across server restarts. When a standalone stream
// cannot be adopted (e.g. duplicate name), the server writes a .ptc marker so the
// orphan sweep does not delete the stream data even after a restart.
func TestJetStreamStandaloneToClusteredPTCMarkerPersistence(t *testing.T) {
	// Phase 1: Create standalone server with a stream named "EVENTS".
	storeDir := t.TempDir()

	opts := DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.ServerName = "S1"
	s := RunServer(&opts)

	nc, err := nats.Connect(s.ClientURL())
	require_NoError(t, err)

	req, _ := json.Marshal(&StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{"standalone.>"},
		Storage:  FileStorage,
	})
	rmsg, err := nc.Request(fmt.Sprintf(JSApiStreamCreateT, "EVENTS"), req, 5*time.Second)
	require_NoError(t, err)
	var resp JSApiStreamCreateResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &resp))
	if resp.Error != nil {
		t.Fatalf("Unexpected error creating stream: %+v", resp.Error)
	}

	numMessages := 10
	for i := 0; i < numMessages; i++ {
		_, err := nc.Request(fmt.Sprintf("standalone.%d", i), []byte("data"), 2*time.Second)
		require_NoError(t, err)
	}

	nc.Close()
	s.Shutdown()
	s.WaitForShutdown()

	// Phase 2: Start a 3-node cluster with a stream also named "EVENTS".
	c := createJetStreamCluster(t, jsClusterTempl, "PTC", _EMPTY_, 3, 22_133, true)
	defer c.shutdown()

	c.waitOnLeader()
	leader := c.leader()

	nc2, err := nats.Connect(leader.ClientURL())
	require_NoError(t, err)

	clusterReq, _ := json.Marshal(&StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{"cluster.>"},
		Storage:  FileStorage,
		Replicas: 3,
	})
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiStreamCreateT, "EVENTS"), clusterReq, 5*time.Second)
	require_NoError(t, err)
	var clusterResp JSApiStreamCreateResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &clusterResp))
	if clusterResp.Error != nil {
		t.Fatalf("Unexpected error creating cluster stream: %+v", clusterResp.Error)
	}
	nc2.Close()

	// Phase 3: Add a 4th server using the standalone store dir.
	srvNew := c.addInNewServer()
	newOpts := srvNew.getOpts().Clone()
	newConfigFile := newOpts.ConfigFile
	srvNew.Shutdown()
	srvNew.WaitForShutdown()
	c.servers = c.servers[:len(c.servers)-1]
	c.opts = c.opts[:len(c.opts)-1]

	newOpts.StoreDir = storeDir
	newOpts.Port = -1
	srvNew = RunServer(newOpts)
	c.servers = append(c.servers, srvNew)
	c.opts = append(c.opts, newOpts)
	c.checkClusterFormed()

	// Wait for checkForOrphans to fire and attempt adoption (~30s).
	// The duplicate EVENTS stream will be skipped, and a .ptc marker written.
	time.Sleep(35 * time.Second)

	// Verify the .ptc marker file was created.
	sysAcc := srvNew.SystemAccount()
	require_NotNil(t, sysAcc)
	markerPath := filepath.Join(storeDir, sysAcc.Name, defaultStoreDirName, defaultMetaGroupName, ptcMarkerFile)
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("Expected ptc marker file at %s, got error: %v", markerPath, err)
	}

	// Verify the standalone stream data still exists on disk.
	streamDir := filepath.Join(storeDir, "$G", "streams", "EVENTS")
	if _, err := os.Stat(streamDir); err != nil {
		t.Fatalf("Expected standalone stream data at %s, got error: %v", streamDir, err)
	}

	// Phase 4: Restart the new server (simulates server restart).
	// Without the ptc marker, the orphan sweep would delete the stream data.
	srvNew.Shutdown()
	srvNew.WaitForShutdown()
	c.servers = c.servers[:len(c.servers)-1]
	c.opts = c.opts[:len(c.opts)-1]

	// Restart using the config file but with our standalone storeDir.
	// Rewrite the config to use the standalone storeDir.
	newOpts.ConfigFile = newConfigFile
	newOpts.StoreDir = storeDir
	newOpts.Port = -1
	srvNew = RunServer(newOpts)
	c.servers = append(c.servers, srvNew)
	c.opts = append(c.opts, newOpts)
	c.checkClusterFormed()

	// Wait for checkForOrphans to fire again after restart.
	time.Sleep(35 * time.Second)

	// Verify the standalone stream data is still on disk (not deleted by orphan sweep).
	if _, err := os.Stat(streamDir); err != nil {
		t.Fatalf("Stream data was deleted after restart despite ptc marker! Path: %s, Error: %v", streamDir, err)
	}

	// Verify the cluster stream is still intact.
	leader = c.leader()
	require_NotNil(t, leader)

	nc3, err := nats.Connect(leader.ClientURL())
	require_NoError(t, err)
	defer nc3.Close()

	rmsg, err = nc3.Request(fmt.Sprintf(JSApiStreamInfoT, "EVENTS"), nil, 5*time.Second)
	require_NoError(t, err)
	var siResp JSApiStreamInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &siResp))
	if siResp.Error != nil {
		t.Fatalf("Stream info error: %+v", siResp.Error)
	}
	if len(siResp.Config.Subjects) != 1 || siResp.Config.Subjects[0] != "cluster.>" {
		t.Fatalf("Expected cluster subjects [cluster.>], got %v", siResp.Config.Subjects)
	}
}
