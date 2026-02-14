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
