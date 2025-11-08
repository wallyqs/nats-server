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
	"runtime"
	"testing"
	"time"
)

// Benchmark to verify that json.NewDecoder uses less memory than json.Unmarshal
// when decoding consumer assignments.
func BenchmarkConsumerAssignmentDecode(b *testing.B) {
	now := time.Now()
	// Create a sample consumer assignment
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
			FilterSubject:  "test.>",
		},
		Group: &raftGroup{
			Name:      "test-group",
			Peers:     []string{"peer1", "peer2", "peer3"},
			Storage:   FileStorage,
			Preferred: "peer1",
		},
	}
	ca.ConfigJSON, _ = json.Marshal(ca.Config)

	// Encode to JSON buffer
	var bb bytes.Buffer
	json.NewEncoder(&bb).Encode(ca)
	buf := bb.Bytes()

	b.Run("NewDecoder", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var result consumerAssignment
			decoder := json.NewDecoder(bytes.NewReader(buf))
			if err := decoder.Decode(&result); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var result consumerAssignment
			if err := json.Unmarshal(buf, &result); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Benchmark to verify that json.NewDecoder uses less memory than json.Unmarshal
// when decoding stream assignments.
func BenchmarkStreamAssignmentDecode(b *testing.B) {
	s := RunBasicJetStreamServer(b)
	defer s.Shutdown()

	now := time.Now()
	// Create a sample stream assignment
	sa := &streamAssignment{
		Client: &ClientInfo{
			Start:   &now,
			Host:    "localhost",
			ID:      12345,
			Account: "test-account",
		},
		Created: now,
		Config: &StreamConfig{
			Name:         "test-stream",
			Subjects:     []string{"test.>"},
			Retention:    LimitsPolicy,
			MaxConsumers: -1,
			MaxMsgs:      -1,
			MaxBytes:     -1,
			Discard:      DiscardOld,
			MaxAge:       0,
			MaxMsgsPer:   -1,
			MaxMsgSize:   -1,
			Storage:      FileStorage,
			Replicas:     3,
		},
		Group: &raftGroup{
			Name:      "test-stream-group",
			Peers:     []string{"peer1", "peer2", "peer3"},
			Storage:   FileStorage,
			Preferred: "peer1",
		},
	}
	sa.ConfigJSON, _ = json.Marshal(sa.Config)

	// Encode to JSON buffer
	var bb bytes.Buffer
	json.NewEncoder(&bb).Encode(sa)
	buf := bb.Bytes()

	b.Run("NewDecoder", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var result streamAssignment
			decoder := json.NewDecoder(bytes.NewReader(buf))
			if err := decoder.Decode(&result); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var result streamAssignment
			if err := json.Unmarshal(buf, &result); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Test that measures memory savings from clearing ConfigJSON after decode.
func TestConfigJSONMemorySavings(t *testing.T) {
	numAssignments := 1000
	assignments := make([][]byte, numAssignments)

	now := time.Now()
	for i := 0; i < numAssignments; i++ {
		ca := &consumerAssignment{
			Client: &ClientInfo{
				Start:   &now,
				Host:    "localhost",
				ID:      uint64(12345 + i),
				Account: "test-account",
			},
			Created: now,
			Name:    "consumer-" + string(rune(i)),
			Stream:  "stream-" + string(rune(i%10)),
			Config: &ConsumerConfig{
				Durable:        "consumer-" + string(rune(i)),
				DeliverSubject: "deliver.subject",
				AckPolicy:      AckExplicit,
				MaxAckPending:  1000,
			},
			Group: &raftGroup{
				Name:    "group-" + string(rune(i)),
				Peers:   []string{"peer1", "peer2", "peer3"},
				Storage: FileStorage,
			},
		}
		ca.ConfigJSON, _ = json.Marshal(ca.Config)

		var bb bytes.Buffer
		json.NewEncoder(&bb).Encode(ca)
		assignments[i] = bb.Bytes()
	}

	t.Run("With-ConfigJSON-cleared", func(t *testing.T) {
		runtime.GC()
		var m1, m2 runtime.MemStats
		runtime.ReadMemStats(&m1)

		// Decode and store assignments (ConfigJSON will be cleared by decodeConsumerAssignmentConfig)
		stored := make([]*consumerAssignment, numAssignments)
		for i, buf := range assignments {
			var err error
			stored[i], err = decodeConsumerAssignment(buf)
			if err != nil {
				t.Fatal(err)
			}
		}

		runtime.ReadMemStats(&m2)
		t.Logf("With ConfigJSON cleared - HeapAlloc: %d bytes, TotalAlloc delta: %d bytes",
			m2.HeapAlloc, m2.TotalAlloc-m1.TotalAlloc)

		// Keep reference so they don't get GC'd
		_ = stored
	})

	t.Run("Without-ConfigJSON-cleared", func(t *testing.T) {
		runtime.GC()
		var m1, m2 runtime.MemStats
		runtime.ReadMemStats(&m1)

		// Decode and store assignments, manually preserving ConfigJSON
		stored := make([]*consumerAssignment, numAssignments)
		for i, buf := range assignments {
			var ca consumerAssignment
			if err := json.Unmarshal(buf, &ca); err != nil {
				t.Fatal(err)
			}
			// Manually decode config while keeping ConfigJSON
			var cfg ConsumerConfig
			if err := json.Unmarshal(ca.ConfigJSON, &cfg); err != nil {
				t.Fatal(err)
			}
			ca.Config = &cfg
			// ConfigJSON is NOT cleared here
			stored[i] = &ca
		}

		runtime.ReadMemStats(&m2)
		t.Logf("Without ConfigJSON cleared - HeapAlloc: %d bytes, TotalAlloc delta: %d bytes",
			m2.HeapAlloc, m2.TotalAlloc-m1.TotalAlloc)

		// Keep reference so they don't get GC'd
		_ = stored
	})
}

// Test that measures actual peak memory usage when decoding many assignments.
func TestConsumerAssignmentMemoryUsage(t *testing.T) {
	numAssignments := 1000
	assignments := make([][]byte, numAssignments)

	now := time.Now()
	for i := 0; i < numAssignments; i++ {
		ca := &consumerAssignment{
			Client: &ClientInfo{
				Start:   &now,
				Host:    "localhost",
				ID:      uint64(12345 + i),
				Account: "test-account",
			},
			Created: now,
			Name:    "consumer-" + string(rune(i)),
			Stream:  "stream-" + string(rune(i%10)),
			Config: &ConsumerConfig{
				Durable:        "consumer-" + string(rune(i)),
				DeliverSubject: "deliver.subject",
				AckPolicy:      AckExplicit,
				MaxAckPending:  1000,
			},
			Group: &raftGroup{
				Name:    "group-" + string(rune(i)),
				Peers:   []string{"peer1", "peer2", "peer3"},
				Storage: FileStorage,
			},
		}
		ca.ConfigJSON, _ = json.Marshal(ca.Config)

		var bb bytes.Buffer
		json.NewEncoder(&bb).Encode(ca)
		assignments[i] = bb.Bytes()
	}

	t.Run("NewDecoder", func(t *testing.T) {
		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		for _, buf := range assignments {
			var result consumerAssignment
			decoder := json.NewDecoder(bytes.NewReader(buf))
			if err := decoder.Decode(&result); err != nil {
				t.Fatal(err)
			}
		}

		runtime.ReadMemStats(&m2)
		t.Logf("NewDecoder - Alloc: %d bytes, TotalAlloc: %d bytes, Mallocs: %d",
			m2.Alloc-m1.Alloc, m2.TotalAlloc-m1.TotalAlloc, m2.Mallocs-m1.Mallocs)
	})

	t.Run("Unmarshal", func(t *testing.T) {
		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		for _, buf := range assignments {
			var result consumerAssignment
			if err := json.Unmarshal(buf, &result); err != nil {
				t.Fatal(err)
			}
		}

		runtime.ReadMemStats(&m2)
		t.Logf("Unmarshal - Alloc: %d bytes, TotalAlloc: %d bytes, Mallocs: %d",
			m2.Alloc-m1.Alloc, m2.TotalAlloc-m1.TotalAlloc, m2.Mallocs-m1.Mallocs)
	})
}

// Test that simulates the real-world scenario of decoding many consumer assignments
// during meta entry application, measuring total memory allocations.
func BenchmarkApplyManyConsumerAssignments(b *testing.B) {
	// Create 100 different consumer assignments to simulate a realistic scenario
	numAssignments := 100
	assignments := make([][]byte, numAssignments)

	now := time.Now()
	for i := 0; i < numAssignments; i++ {
		ca := &consumerAssignment{
			Client: &ClientInfo{
				Start:   &now,
				Host:    "localhost",
				ID:      uint64(12345 + i),
				Account: "test-account",
			},
			Created: now,
			Name:    "consumer-" + string(rune(i)),
			Stream:  "stream-" + string(rune(i%10)),
			Config: &ConsumerConfig{
				Durable:        "consumer-" + string(rune(i)),
				DeliverSubject: "deliver.subject",
				AckPolicy:      AckExplicit,
				MaxAckPending:  1000,
			},
			Group: &raftGroup{
				Name:    "group-" + string(rune(i)),
				Peers:   []string{"peer1", "peer2", "peer3"},
				Storage: FileStorage,
			},
		}
		ca.ConfigJSON, _ = json.Marshal(ca.Config)

		var bb bytes.Buffer
		json.NewEncoder(&bb).Encode(ca)
		assignments[i] = bb.Bytes()
	}

	b.Run("NewDecoder-100-assignments", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, buf := range assignments {
				var result consumerAssignment
				decoder := json.NewDecoder(bytes.NewReader(buf))
				if err := decoder.Decode(&result); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("Unmarshal-100-assignments", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, buf := range assignments {
				var result consumerAssignment
				if err := json.Unmarshal(buf, &result); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
