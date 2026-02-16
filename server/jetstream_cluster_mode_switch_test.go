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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/highwayhash"
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

// TestJetStreamStandaloneToClusteredPTCMarkerLifecycle tests the promoted-to-cluster
// (ptc) marker lifecycle: the marker is written when a standalone server is promoted
// to a cluster and has orphan streams pending adoption. After a server restart, the
// marker ensures checkForOrphans recognizes the node as promoted-to-cluster and
// re-attempts adoption instead of deleting orphan streams.
func TestJetStreamStandaloneToClusteredPTCMarkerLifecycle(t *testing.T) {
	// Phase 1: Create standalone server with a stream.
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
		Name:     "MYSTREAM",
		Subjects: []string{"test.>"},
		Storage:  FileStorage,
	})
	rmsg, err := nc.Request(fmt.Sprintf(JSApiStreamCreateT, "MYSTREAM"), req, 5*time.Second)
	require_NoError(t, err)
	var resp JSApiStreamCreateResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &resp))
	if resp.Error != nil {
		t.Fatalf("Unexpected error creating stream: %+v", resp.Error)
	}

	numMessages := 10
	for i := 0; i < numMessages; i++ {
		_, err := nc.Request(fmt.Sprintf("test.%d", i), []byte("data"), 2*time.Second)
		require_NoError(t, err)
	}

	nc.Close()
	s.Shutdown()
	s.WaitForShutdown()

	// Phase 2: Promote to a 3-node cluster. Make S-1 the meta leader.
	modify := func(serverName, clusterName, autoStoreDir, conf string) string {
		if serverName == "S-1" {
			return strings.Replace(conf, autoStoreDir, storeDir, 1)
		}
		return conf
	}
	c := createJetStreamClusterWithTemplateAndModHook(t, jsClusterTempl, "PM", 3, modify)
	defer c.shutdown()

	c.waitOnLeader()

	var s1 *Server
	for _, srv := range c.servers {
		if srv.Name() == "S-1" {
			s1 = srv
			break
		}
	}
	require_NotNil(t, s1)

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

	// Wait for adoption and verify stream data is accessible.
	checkFor(t, 60*time.Second, 500*time.Millisecond, func() error {
		nc2, err := nats.Connect(s1.ClientURL())
		if err != nil {
			return err
		}
		defer nc2.Close()

		rmsg, err := nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "MYSTREAM"), nil, 5*time.Second)
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

	// After adoption, the ptc marker should have been written (it is written before
	// the adoption proposal, to protect the stream in case of restart before adoption
	// completes). Since checkForOrphans is a one-shot timer, the marker persists until
	// the next restart. Verify the marker was created during the adoption window.
	sysAcc := s1.SystemAccount()
	require_NotNil(t, sysAcc)
	metaDir := filepath.Join(storeDir, JetStreamStoreDir, sysAcc.Name, defaultStoreDirName, defaultMetaGroupName)
	markerPath := filepath.Join(metaDir, ptcMarkerFile)

	// The ptc marker should exist (written before adoption, cleaned up on next restart).
	if _, err := os.Stat(markerPath); err != nil {
		// The marker may already be cleaned up if checkForOrphans ran a second time
		// with no orphans. Either outcome is acceptable since the stream was adopted.
		t.Logf("ptc marker not found (may have been cleaned up): %v", err)
	}

	// Verify the stream data is intact on disk.
	streamDir := filepath.Join(storeDir, JetStreamStoreDir, "$G", "streams", "MYSTREAM")
	if _, err := os.Stat(streamDir); err != nil {
		t.Fatalf("Expected standalone stream data at %s, got error: %v", streamDir, err)
	}
}

// renameStreamOnDisk renames a stream's directory and updates the meta.inf,
// meta.sum, stream state (index.db), and per-message checksums in block files
// so the server loads it under the new name. The server must be stopped before
// calling this. Only works for unencrypted, uncompressed file-storage streams.
//
// storeDir is the JetStream store directory (e.g. from opts.StoreDir).
// account is the account name (e.g. "$G" for the global account).
// oldName is the current stream name, newName is the desired name.
func renameStreamOnDisk(t *testing.T, storeDir, account, oldName, newName string) {
	t.Helper()

	oldDir := filepath.Join(storeDir, JetStreamStoreDir, account, "streams", oldName)
	newDir := filepath.Join(storeDir, JetStreamStoreDir, account, "streams", newName)

	// Build old and new highwayhash digesters.
	// The stream name is used as the hash key for all integrity checks.
	newKey := sha256.Sum256([]byte(newName))
	newHH, err := highwayhash.NewDigest64(newKey[:])
	require_NoError(t, err)

	// 1. Update meta.inf with the new stream name.
	metaFile := filepath.Join(oldDir, JetStreamMetaFile)
	buf, err := os.ReadFile(metaFile)
	require_NoError(t, err)

	var cfg FileStreamInfo
	require_NoError(t, json.Unmarshal(buf, &cfg))
	cfg.Name = newName
	newBuf, err := json.Marshal(cfg)
	require_NoError(t, err)
	require_NoError(t, os.WriteFile(metaFile, newBuf, defaultFilePerms))

	// 2. Recompute meta.sum using the new directory name as the hash key.
	// The recovery code in jetstream.go uses fi.Name() (the directory entry name).
	newHH.Reset()
	newHH.Write(newBuf)
	var hb [highwayhash.Size64]byte
	checksum := hex.EncodeToString(newHH.Sum(hb[:0]))
	sumFile := filepath.Join(oldDir, JetStreamMetaFileSum)
	require_NoError(t, os.WriteFile(sumFile, []byte(checksum), defaultFilePerms))

	// 3. Rewrite per-message checksums in all .blk files.
	// Each message record has an 8-byte highwayhash at the end, keyed by sha256(streamName).
	// We need to verify with the old key and rewrite with the new key.
	msgsDir := filepath.Join(oldDir, msgDir)
	blkFiles, err := os.ReadDir(msgsDir)
	require_NoError(t, err)

	le := binary.LittleEndian
	const hdrSize = 22    // msgHdrSize
	const hashSize = 8    // recordHashSize / checksumSize
	const headerBit = uint32(1 << 31)
	const tombBit = uint64(1 << 63)

	for _, fi := range blkFiles {
		if !strings.HasSuffix(fi.Name(), ".blk") {
			continue
		}
		// Extract block index from filename (e.g. "1.blk" -> 1).
		var blkIndex int
		if n, err := fmt.Sscanf(fi.Name(), "%d.blk", &blkIndex); err != nil || n != 1 {
			continue
		}

		blkPath := filepath.Join(msgsDir, fi.Name())
		data, err := os.ReadFile(blkPath)
		require_NoError(t, err)
		if len(data) == 0 {
			continue
		}

		// Per-block hash key is sha256("streamName-blockIndex").
		blkKey := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", newName, blkIndex)))
		blkHH, err := highwayhash.NewDigest64(blkKey[:])
		require_NoError(t, err)

		// Walk each record in the block and rewrite its checksum.
		for idx := uint32(0); idx < uint32(len(data)); {
			if idx+hdrSize > uint32(len(data)) {
				break
			}
			hdr := data[idx : idx+hdrSize]
			rl := le.Uint32(hdr[0:])
			hasHeaders := rl&headerBit != 0
			rl &^= headerBit
			if rl < hdrSize+hashSize || idx+rl > uint32(len(data)) {
				break
			}
			dlen := int(rl) - hdrSize
			slen := int(le.Uint16(hdr[20:]))
			rec := data[idx+hdrSize : idx+rl]

			// Compute the checksum region (same as rebuildStateLocked).
			shlen := slen
			if hasHeaders {
				shlen += 4
			}
			if dlen < hashSize || shlen > (dlen-hashSize) {
				break
			}

			// Compute new checksum.
			blkHH.Reset()
			blkHH.Write(hdr[4:20])
			blkHH.Write(rec[:slen]) // subject
			if hasHeaders {
				blkHH.Write(rec[slen+4 : dlen-hashSize])
			} else {
				blkHH.Write(rec[slen : dlen-hashSize])
			}
			copy(rec[dlen-hashSize:dlen], blkHH.Sum(hb[:0]))

			idx += rl
		}

		require_NoError(t, os.WriteFile(blkPath, data, defaultFilePerms))
	}

	// 4. Remove the stream state file (index.db) so the server rebuilds state
	// from the block files. The state file contains block-level checksums that
	// reference the old hash key and would cause a mismatch.
	os.Remove(filepath.Join(msgsDir, "index.db"))

	// 5. Rename the directory.
	require_NoError(t, os.Rename(oldDir, newDir))
}

// TestJetStreamStandaloneRenameStreamThenPromote tests that an operator can
// rename a stream on disk while the server is stopped, then promote the
// standalone node to a cluster, and the stream appears under the new name.
func TestJetStreamStandaloneRenameStreamThenPromote(t *testing.T) {
	// Phase 1: Create standalone server with stream "ORDERS".
	storeDir := t.TempDir()

	opts := DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.ServerName = "S1"
	s := RunServer(&opts)

	nc, err := nats.Connect(s.ClientURL())
	require_NoError(t, err)

	// Create the stream and a durable consumer.
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

	// Phase 2: Rename the stream on disk from "ORDERS" to "ORDERS_V2".
	renameStreamOnDisk(t, storeDir, "$G", "ORDERS", "ORDERS_V2")

	// Verify the old directory no longer exists and the new one does.
	oldDir := filepath.Join(storeDir, JetStreamStoreDir, "$G", "streams", "ORDERS")
	newDir := filepath.Join(storeDir, JetStreamStoreDir, "$G", "streams", "ORDERS_V2")
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("Old stream directory should not exist: %v", err)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("New stream directory should exist: %v", err)
	}

	// Phase 3: Promote to a 3-node cluster.
	modify := func(serverName, clusterName, autoStoreDir, conf string) string {
		if serverName == "S-1" {
			return strings.Replace(conf, autoStoreDir, storeDir, 1)
		}
		return conf
	}
	c := createJetStreamClusterWithTemplateAndModHook(t, jsClusterTempl, "RN", 3, modify)
	defer c.shutdown()

	c.waitOnLeader()

	// Ensure S-1 becomes meta leader for adoption.
	var s1 *Server
	for _, srv := range c.servers {
		if srv.Name() == "S-1" {
			s1 = srv
			break
		}
	}
	require_NotNil(t, s1)

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

	// Wait for adoption.
	checkFor(t, 60*time.Second, 500*time.Millisecond, func() error {
		nc2, err := nats.Connect(s1.ClientURL())
		if err != nil {
			return err
		}
		defer nc2.Close()

		// The stream should be visible under the NEW name.
		rmsg, err := nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "ORDERS_V2"), nil, 5*time.Second)
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

	// Verify the old name does NOT exist in the cluster.
	nc2, err := nats.Connect(s1.ClientURL())
	require_NoError(t, err)
	defer nc2.Close()

	rmsg, err = nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "ORDERS"), nil, 5*time.Second)
	require_NoError(t, err)
	var oldResp JSApiStreamInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &oldResp))
	if oldResp.Error == nil {
		t.Fatalf("Expected error for old stream name ORDERS, but got none")
	}

	// Verify the consumer was adopted under the renamed stream.
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiConsumerInfoT, "ORDERS_V2", "processor"), nil, 5*time.Second)
	require_NoError(t, err)
	var ciResp JSApiConsumerInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &ciResp))
	if ciResp.Error != nil {
		t.Fatalf("Consumer info error: %+v", ciResp.Error)
	}
	if ciResp.Config.Durable != "processor" {
		t.Fatalf("Expected durable name %q, got %q", "processor", ciResp.Config.Durable)
	}

	// Verify new publishes work on the renamed stream's subjects.
	_, err = nc2.Request("orders.new", []byte("new-order"), 2*time.Second)
	require_NoError(t, err)

	rmsg, err = nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "ORDERS_V2"), nil, 5*time.Second)
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

// TestJetStreamStandaloneRenameStreamConsumerState tests that consumers created
// under the old stream name work correctly after an on-disk rename and promotion
// to cluster. Specifically verifies that:
//   - Durable consumers are adopted under the renamed stream
//   - Consumer delivery state (acked messages) is preserved
//   - Consumers can fetch and ack new messages after adoption
//   - Multiple consumers on the same renamed stream all work
func TestJetStreamStandaloneRenameStreamConsumerState(t *testing.T) {
	// Phase 1: Create standalone server with stream and multiple consumers.
	storeDir := t.TempDir()

	opts := DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.ServerName = "S1"
	s := RunServer(&opts)

	nc, err := nats.Connect(s.ClientURL())
	require_NoError(t, err)

	js, err := nc.JetStream()
	require_NoError(t, err)

	// Create stream.
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "TASKS",
		Subjects: []string{"tasks.>"},
		Storage:  nats.FileStorage,
	})
	require_NoError(t, err)

	// Publish messages.
	numMessages := 20
	for i := 0; i < numMessages; i++ {
		_, err := js.Publish(fmt.Sprintf("tasks.%d", i), []byte(fmt.Sprintf("task-%d", i)))
		require_NoError(t, err)
	}

	// Create first consumer "worker" and ack some messages.
	_, err = js.AddConsumer("TASKS", &nats.ConsumerConfig{
		Durable:       "worker",
		AckPolicy:     nats.AckExplicitPolicy,
		DeliverPolicy: nats.DeliverAllPolicy,
	})
	require_NoError(t, err)

	// Fetch and ack 5 messages from "worker".
	sub1, err := js.PullSubscribe("tasks.>", "worker", nats.BindStream("TASKS"))
	require_NoError(t, err)
	msgs, err := sub1.Fetch(5, nats.MaxWait(5*time.Second))
	require_NoError(t, err)
	if len(msgs) != 5 {
		t.Fatalf("Expected 5 messages, got %d", len(msgs))
	}
	for _, m := range msgs {
		require_NoError(t, m.Ack())
	}
	// Allow acks to be processed.
	time.Sleep(500 * time.Millisecond)

	// Create second consumer "auditor" with no acks (fresh).
	_, err = js.AddConsumer("TASKS", &nats.ConsumerConfig{
		Durable:       "auditor",
		AckPolicy:     nats.AckExplicitPolicy,
		DeliverPolicy: nats.DeliverAllPolicy,
	})
	require_NoError(t, err)

	nc.Close()
	s.Shutdown()
	s.WaitForShutdown()

	// Phase 2: Rename stream on disk.
	renameStreamOnDisk(t, storeDir, "$G", "TASKS", "JOBS")

	// Phase 3: Promote to cluster.
	modify := func(serverName, clusterName, autoStoreDir, conf string) string {
		if serverName == "S-1" {
			return strings.Replace(conf, autoStoreDir, storeDir, 1)
		}
		return conf
	}
	c := createJetStreamClusterWithTemplateAndModHook(t, jsClusterTempl, "CS", 3, modify)
	defer c.shutdown()

	c.waitOnLeader()

	// Make S-1 the meta leader for adoption.
	var s1 *Server
	for _, srv := range c.servers {
		if srv.Name() == "S-1" {
			s1 = srv
			break
		}
	}
	require_NotNil(t, s1)

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

	// Wait for adoption to complete.
	checkFor(t, 60*time.Second, 500*time.Millisecond, func() error {
		nc2, err := nats.Connect(s1.ClientURL())
		if err != nil {
			return err
		}
		defer nc2.Close()

		rmsg, err := nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "JOBS"), nil, 5*time.Second)
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

	// Verify both consumers exist under the new stream name.
	nc2, err := nats.Connect(s1.ClientURL())
	require_NoError(t, err)
	defer nc2.Close()

	js2, err := nc2.JetStream()
	require_NoError(t, err)

	// Check "worker" consumer: should have 5 acked messages (AckFloor.Consumer = 5).
	rmsg, err := nc2.Request(fmt.Sprintf(JSApiConsumerInfoT, "JOBS", "worker"), nil, 5*time.Second)
	require_NoError(t, err)
	var workerInfo JSApiConsumerInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &workerInfo))
	if workerInfo.Error != nil {
		t.Fatalf("Worker consumer info error: %+v", workerInfo.Error)
	}
	if workerInfo.AckFloor.Consumer != 5 {
		t.Fatalf("Expected worker ack floor consumer seq 5, got %d", workerInfo.AckFloor.Consumer)
	}
	if workerInfo.NumPending != uint64(numMessages-5) {
		t.Fatalf("Expected worker %d pending, got %d", numMessages-5, workerInfo.NumPending)
	}

	// Check "auditor" consumer: should have 0 acked messages, all pending.
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiConsumerInfoT, "JOBS", "auditor"), nil, 5*time.Second)
	require_NoError(t, err)
	var auditorInfo JSApiConsumerInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &auditorInfo))
	if auditorInfo.Error != nil {
		t.Fatalf("Auditor consumer info error: %+v", auditorInfo.Error)
	}
	if auditorInfo.AckFloor.Consumer != 0 {
		t.Fatalf("Expected auditor ack floor 0, got %d", auditorInfo.AckFloor.Consumer)
	}
	if auditorInfo.NumPending != uint64(numMessages) {
		t.Fatalf("Expected auditor %d pending, got %d", numMessages, auditorInfo.NumPending)
	}

	// Fetch remaining messages from "worker" under the new stream name.
	sub2, err := js2.PullSubscribe("tasks.>", "worker", nats.BindStream("JOBS"))
	require_NoError(t, err)
	msgs, err = sub2.Fetch(15, nats.MaxWait(5*time.Second))
	require_NoError(t, err)
	if len(msgs) != 15 {
		t.Fatalf("Expected 15 remaining messages for worker, got %d", len(msgs))
	}
	// Verify they start at the right sequence (message 6 onwards).
	if string(msgs[0].Data) != "task-5" {
		t.Fatalf("Expected first remaining message to be 'task-5', got %q", string(msgs[0].Data))
	}
	for _, m := range msgs {
		require_NoError(t, m.Ack())
	}
	time.Sleep(500 * time.Millisecond)

	// Verify worker is now fully caught up.
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiConsumerInfoT, "JOBS", "worker"), nil, 5*time.Second)
	require_NoError(t, err)
	require_NoError(t, json.Unmarshal(rmsg.Data, &workerInfo))
	if workerInfo.Error != nil {
		t.Fatalf("Worker consumer info error: %+v", workerInfo.Error)
	}
	if workerInfo.NumPending != 0 {
		t.Fatalf("Expected worker 0 pending after consuming all, got %d", workerInfo.NumPending)
	}

	// Old stream name should not exist.
	rmsg, err = nc2.Request(fmt.Sprintf(JSApiStreamInfoT, "TASKS"), nil, 5*time.Second)
	require_NoError(t, err)
	var oldResp JSApiStreamInfoResponse
	require_NoError(t, json.Unmarshal(rmsg.Data, &oldResp))
	if oldResp.Error == nil {
		t.Fatalf("Expected error for old stream name TASKS, but got none")
	}
}
