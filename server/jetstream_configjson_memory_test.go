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
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// Test that demonstrates memory savings by clearing ConfigJSON.
func TestConfigJSONMemoryFootprint(t *testing.T) {
	now := time.Now()
	ca := &consumerAssignment{
		Client: &ClientInfo{
			Start:   &now,
			Host:    "localhost",
			ID:      12345,
			Account: "test-account",
		},
		Created: now,
		Name:    "test-consumer",
		Stream:  "test-stream",
		Config: &ConsumerConfig{
			Durable:        "test-consumer",
			DeliverSubject: "deliver.subject.with.a.longer.name.to.make.it.realistic",
			AckPolicy:      AckExplicit,
			MaxAckPending:  1000,
			FilterSubject:  "test.subject.filter.>",
		},
		Group: &raftGroup{
			Name:      "test-group",
			Peers:     []string{"peer1", "peer2", "peer3"},
			Storage:   FileStorage,
			Preferred: "peer1",
		},
	}
	ca.ConfigJSON, _ = json.Marshal(ca.Config)

	// Encode it
	var bb bytes.Buffer
	json.NewEncoder(&bb).Encode(ca)
	buf := bb.Bytes()

	// Case 1: Decode WITH ConfigJSON cleared (new behavior)
	ca1, err := decodeConsumerAssignment(buf)
	if err != nil {
		t.Fatal(err)
	}

	// Case 2: Decode WITHOUT ConfigJSON cleared (old behavior)
	var ca2 consumerAssignment
	if err := json.Unmarshal(buf, &ca2); err != nil {
		t.Fatal(err)
	}
	var cfg ConsumerConfig
	if err := json.Unmarshal(ca2.ConfigJSON, &cfg); err != nil {
		t.Fatal(err)
	}
	ca2.Config = &cfg
	// Don't clear ConfigJSON (old behavior)

	// Calculate memory footprint
	configJSONSize1 := len(ca1.ConfigJSON) // Should be 0
	configJSONSize2 := len(ca2.ConfigJSON) // Should be > 0

	t.Logf("ConfigJSON size with clearing: %d bytes", configJSONSize1)
	t.Logf("ConfigJSON size without clearing: %d bytes", configJSONSize2)
	t.Logf("Memory saved per assignment: %d bytes", configJSONSize2-configJSONSize1)

	if configJSONSize1 != 0 {
		t.Errorf("Expected ConfigJSON to be cleared (size 0), but got %d bytes", configJSONSize1)
	}

	if configJSONSize2 == 0 {
		t.Error("Expected ConfigJSON to be preserved in old behavior test")
	}

	// For 1000 assignments, calculate savings
	numAssignments := 1000
	totalSavings := (configJSONSize2 - configJSONSize1) * numAssignments
	t.Logf("For %d assignments, total memory saved: %d bytes (%.2f KB, %.2f MB)",
		numAssignments, totalSavings, float64(totalSavings)/1024, float64(totalSavings)/(1024*1024))

	// Verify Config is still accessible after clearing ConfigJSON
	if ca1.Config == nil {
		t.Fatal("Config should not be nil after clearing ConfigJSON")
	}
	if ca1.Config.Durable != "test-consumer" {
		t.Errorf("Config not properly decoded, got durable=%s", ca1.Config.Durable)
	}
}

// Test that encoding still works after ConfigJSON is cleared.
func TestEncodeAfterConfigJSONCleared(t *testing.T) {
	now := time.Now()
	ca := &consumerAssignment{
		Client: &ClientInfo{
			Start:   &now,
			Host:    "localhost",
			ID:      12345,
			Account: "test-account",
		},
		Created: now,
		Name:    "test-consumer",
		Stream:  "test-stream",
		Config: &ConsumerConfig{
			Durable:        "test-consumer",
			DeliverSubject: "deliver.subject",
			AckPolicy:      AckExplicit,
			MaxAckPending:  1000,
		},
		Group: &raftGroup{
			Name:      "test-group",
			Peers:     []string{"peer1", "peer2", "peer3"},
			Storage:   FileStorage,
			Preferred: "peer1",
		},
	}
	ca.ConfigJSON, _ = json.Marshal(ca.Config)

	// Encode it
	var bb bytes.Buffer
	json.NewEncoder(&bb).Encode(ca)
	buf := bb.Bytes()

	// Decode (which clears ConfigJSON)
	decoded, err := decodeConsumerAssignment(buf)
	if err != nil {
		t.Fatal(err)
	}

	// Verify ConfigJSON was cleared
	if len(decoded.ConfigJSON) != 0 {
		t.Errorf("Expected ConfigJSON to be cleared, got %d bytes", len(decoded.ConfigJSON))
	}

	// Now try to encode again (should regenerate ConfigJSON from Config)
	encoded := encodeAddConsumerAssignment(decoded)
	if len(encoded) == 0 {
		t.Fatal("Failed to encode consumer assignment after ConfigJSON was cleared")
	}

	// Decode again to verify round-trip works
	decoded2, err := decodeConsumerAssignment(encoded[1:]) // Skip the op byte
	if err != nil {
		t.Fatal(err)
	}

	// Verify the config is correct
	if decoded2.Config.Durable != "test-consumer" {
		t.Errorf("Round-trip failed, got durable=%s", decoded2.Config.Durable)
	}

	t.Log("Successfully encoded and decoded after ConfigJSON was cleared")
}
