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
	"testing"
)

func TestJetStreamClusterShardedSnapshot(t *testing.T) {
	// Test sharding calculation
	tests := []struct {
		streamCount int
		expected    int
	}{
		{500, 4},
		{5000, 8},
		{50000, 16},
		{500000, 32},
		{2000000, 64},
	}

	for _, test := range tests {
		got := calculateOptimalShards(test.streamCount)
		if got != test.expected {
			t.Errorf("calculateOptimalShards(%d) = %d, want %d", test.streamCount, got, test.expected)
		}
	}
}

func TestJetStreamClusterShardDistribution(t *testing.T) {
	// Test that sharding distributes streams evenly
	numShards := 16
	accounts := []string{"ACC1", "ACC2", "ACC3", "ACC4", "ACC5"}
	streamCounts := make(map[int]int)

	// Create 1000 streams across 5 accounts
	for _, acc := range accounts {
		for i := 0; i < 200; i++ {
			stream := fmt.Sprintf("STREAM_%d", i)
			shardID := calculateShardID(acc, stream, numShards)
			streamCounts[shardID]++
		}
	}

	// Check distribution
	minCount := 1000
	maxCount := 0
	for i := 0; i < numShards; i++ {
		count := streamCounts[i]
		if count < minCount {
			minCount = count
		}
		if count > maxCount {
			maxCount = count
		}
	}

	// Distribution should be reasonably even
	variance := float64(maxCount-minCount) / float64(1000/numShards)
	if variance > 0.5 { // Allow 50% variance
		t.Errorf("Shard distribution too uneven: min=%d, max=%d, variance=%.2f", minCount, maxCount, variance)
	}
}
