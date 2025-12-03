// Copyright 2024-2025 The NATS Authors
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

//go:build !skip_msgtrace_tests

package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestMsgTraceHeaderCorruptionWithLeafNode tests that the traceparent header key
// is not corrupted when tracing messages across leaf nodes.
func TestMsgTraceHeaderCorruptionWithLeafNode(t *testing.T) {
	// Create hub server
	confHub := createConfFile(t, []byte(`
		port: -1
		server_name: "Hub"
		accounts {
			A { users: [{user: "a", password: "pwd"}]}
		}
		leafnodes {
			port: -1
		}
	`))
	hub, ohub := RunServerWithConfig(confHub)
	defer hub.Shutdown()

	// Create leaf server
	confLeaf := createConfFile(t, []byte(fmt.Sprintf(`
		port: -1
		server_name: "Leaf"
		leafnodes {
			remotes [
				{
					url: "nats://a:pwd@127.0.0.1:%d"
				}
			]
		}
	`, ohub.LeafNode.Port)))
	leaf, _ := RunServerWithConfig(confLeaf)
	defer leaf.Shutdown()

	checkLeafNodeConnected(t, hub)
	checkLeafNodeConnected(t, leaf)

	// Connect to hub
	ncHub := natsConnect(t, hub.ClientURL(), nats.UserInfo("a", "pwd"))
	defer ncHub.Close()

	// Subscribe to trace destination on hub
	traceSub := natsSubSync(t, ncHub, "my.trace.subj")
	natsFlush(t, ncHub)

	// Subscribe to the subject on leaf
	ncLeaf := natsConnect(t, leaf.ClientURL())
	defer ncLeaf.Close()
	sub := natsSubSync(t, ncLeaf, "foo")
	natsFlush(t, ncLeaf)

	// Wait for subscription to propagate
	checkSubInterest(t, hub, "A", "foo", time.Second)

	// Test with different case variations of traceparent header
	testCases := []struct {
		name      string
		headerKey string
	}{
		{"lowercase", "traceparent"},
		{"uppercase", "TRACEPARENT"},
		{"mixed case 1", "TraceParent"},
		{"mixed case 2", "TrAcEpArEnT"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Run multiple iterations to check for race conditions or intermittent issues
			for iter := 0; iter < 10; iter++ {
				msg := nats.NewMsg("foo")
				msg.Header.Set(MsgTraceDest, traceSub.Subject)
				traceParentValue := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
				msg.Header.Set(tc.headerKey, traceParentValue)
				msg.Data = []byte("hello!")

				err := ncHub.PublishMsg(msg)
				require_NoError(t, err)

				// Wait for the application message
				appMsg := natsNexMsg(t, sub, time.Second)
				require_Equal[string](t, string(appMsg.Data), "hello!")

				// Verify the traceparent header in the delivered message
				// The header key should be "traceparent" (lowercase) in the delivered message
				// because the server rewrites it to lowercase
				deliveredHeaderVal := appMsg.Header.Get(traceParentHdr)
				if deliveredHeaderVal != traceParentValue {
					// Check if it's under a different case key
					allHeaders := appMsg.Header
					t.Logf("Delivered message headers: %v", allHeaders)
					t.Fatalf("Expected delivered message to have %q header with value %q, got %q",
						traceParentHdr, traceParentValue, deliveredHeaderVal)
				}

				// Check the trace events
				for i := 0; i < 2; i++ { // We expect 2 trace events (hub and leaf)
					traceMsg := natsNexMsg(t, traceSub, time.Second)

					// Check raw JSON to see if the key is correctly serialized
					jsonStr := string(traceMsg.Data)
					if !strings.Contains(jsonStr, `"traceparent"`) {
						t.Logf("Raw JSON: %s", jsonStr)
						t.Fatalf("Expected raw JSON to contain %q key", "traceparent")
					}

					var e MsgTraceEvent
					err = json.Unmarshal(traceMsg.Data, &e)
					require_NoError(t, err)

					// Verify the traceparent header key is present and correct
					headerMap := e.Request.Header
					if headerMap == nil {
						t.Fatalf("Expected non-nil header map in trace event from %s", e.Server.Name)
					}

					// The key should be "traceparent" (lowercase) regardless of input case
					val, ok := headerMap[traceParentHdr]
					if !ok {
						// Print what keys we have for debugging
						keys := make([]string, 0, len(headerMap))
						for k := range headerMap {
							keys = append(keys, fmt.Sprintf("%q", k))
						}
						t.Fatalf("Expected %q key in header map from %s, got keys: %v",
							traceParentHdr, e.Server.Name, keys)
					}

					// Verify the value is correct
					if len(val) != 1 || val[0] != traceParentValue {
						t.Fatalf("Expected traceparent value %q, got %v from %s",
							traceParentValue, val, e.Server.Name)
					}
				}

				// Make sure no more trace messages
				if msg, err := traceSub.NextMsg(100 * time.Millisecond); err == nil {
					t.Fatalf("Did not expect more trace messages, got: %s", msg.Data)
				}
			}
		})
	}
}
