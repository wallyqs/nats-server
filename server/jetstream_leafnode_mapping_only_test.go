// Copyright 2020-2025 The NATS Authors
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

//go:build !skip_js_tests

package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestJetStreamLeafNodeSourceWithMappingOnly tests that a hub server can create
// a stream that sources from a stream on a connected leafnode using only subject
// mappings (no JetStream domains required). The mapping must be on the leafnode
// side to transform incoming $JS.{prefix}.API.> requests to $JS.API.>
func TestJetStreamLeafNodeSourceWithMappingOnly(t *testing.T) {
	// Hub server - no domain, no mapping needed.
	// Requests to $JS.LN.API.> will flow through to leafnode since
	// there's no local handler for that subject.
	hubConf := `
		listen: 127.0.0.1:-1
		server_name: HUB
		jetstream {
			store_dir: '%s'
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			listen: 127.0.0.1:-1
		}
	`

	// Leafnode server - no domain, but with mapping.
	// The mapping transforms incoming $JS.LN.API.> requests to $JS.API.>
	// so the local JetStream can handle them.
	leafConf := `
		listen: 127.0.0.1:-1
		server_name: LEAF
		jetstream {
			store_dir: '%s'
		}
		accounts {
			A {
				jetstream: enabled
				users: [{user: a, password: a}]
				mappings = {
					"$JS.LN.API.>": "$JS.API.>"
				}
			}
			$SYS { users: [{user: admin, password: admin}] }
		}
		leafnodes {
			remotes [
				{ url: "nats://a:a@127.0.0.1:%d", account: A }
				{ url: "nats://admin:admin@127.0.0.1:%d", account: "$SYS" }
			]
		}
	`

	// Start the hub server.
	hubConfFile := createConfFile(t, []byte(fmt.Sprintf(hubConf, t.TempDir())))
	sHub, oHub := RunServerWithConfig(hubConfFile)
	defer sHub.Shutdown()

	// Start the leafnode server.
	leafConfFile := createConfFile(t, []byte(fmt.Sprintf(leafConf, t.TempDir(), oHub.LeafNode.Port, oHub.LeafNode.Port)))
	sLeaf, _ := RunServerWithConfig(leafConfFile)
	defer sLeaf.Shutdown()

	// Wait for leafnode connections (account A + system account = 2).
	checkLeafNodeConnectedCount(t, sHub, 2)
	checkLeafNodeConnectedCount(t, sLeaf, 2)

	// Connect to the leafnode and create a stream.
	ncLeaf := natsConnect(t, sLeaf.ClientURL(), nats.UserInfo("a", "a"))
	defer ncLeaf.Close()

	jsLeaf, err := ncLeaf.JetStream()
	require_NoError(t, err)

	// Create a stream on the leafnode.
	_, err = jsLeaf.AddStream(&nats.StreamConfig{
		Name:     "LEAF_STREAM",
		Subjects: []string{"leaf.>"},
	})
	require_NoError(t, err)

	// Publish some messages to the leafnode stream.
	for i := 0; i < 5; i++ {
		_, err = jsLeaf.Publish("leaf.data", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// Verify the leafnode stream has the messages.
	si, err := jsLeaf.StreamInfo("LEAF_STREAM")
	require_NoError(t, err)
	require_Equal(t, si.State.Msgs, uint64(5))

	// Connect to the hub.
	ncHub := natsConnect(t, sHub.ClientURL(), nats.UserInfo("a", "a"))
	defer ncHub.Close()

	jsHub, err := ncHub.JetStream()
	require_NoError(t, err)

	// Create a sourced stream on the hub that pulls from the leafnode's stream.
	// The External.APIPrefix tells JetStream to use $JS.LN.API.> for API calls.
	// These requests flow to the leafnode where the mapping transforms them
	// to $JS.API.> for local handling.
	_, err = jsHub.AddStream(&nats.StreamConfig{
		Name: "HUB_SOURCED",
		Sources: []*nats.StreamSource{{
			Name:     "LEAF_STREAM",
			External: &nats.ExternalStream{APIPrefix: "$JS.LN.API"},
		}},
	})
	require_NoError(t, err)

	// Wait for the sourced stream to sync all messages from the leafnode.
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_SOURCED")
		if err != nil {
			return fmt.Errorf("could not get stream info: %v", err)
		}
		if si.State.Msgs != 5 {
			return fmt.Errorf("expected 5 msgs, got %d", si.State.Msgs)
		}
		return nil
	})

	// Publish more messages to the leafnode and verify they sync to the hub.
	for i := 5; i < 10; i++ {
		_, err = jsLeaf.Publish("leaf.data", []byte(fmt.Sprintf("msg-%d", i)))
		require_NoError(t, err)
	}

	// Verify all 10 messages arrive in the hub's sourced stream.
	checkFor(t, 5*time.Second, 100*time.Millisecond, func() error {
		si, err := jsHub.StreamInfo("HUB_SOURCED")
		if err != nil {
			return fmt.Errorf("could not get stream info: %v", err)
		}
		if si.State.Msgs != 10 {
			return fmt.Errorf("expected 10 msgs, got %d", si.State.Msgs)
		}
		return nil
	})
}
