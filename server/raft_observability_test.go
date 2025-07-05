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
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestRaftEmptyLogProtectionObservability(t *testing.T) {
	// Create a cluster where we can test empty log protection with observability
	c := createJetStreamClusterExplicit(t, "R3S", 3)
	defer c.shutdown()

	s1 := c.servers[0]
	nc, js := jsClientConnect(t, s1)
	defer nc.Close()

	// Create a stream with R=3 and protection
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST",
		Subjects: []string{"foo"},
		Replicas: 3,
	})
	require_NoError(t, err)

	// Wait for stream to be ready
	c.waitOnStreamLeader(globalAccountName, "TEST")

	// Get the leader's stream
	var leaderNode RaftNode
	for _, s := range c.servers {
		mset, err := s.GlobalAccount().lookupStream("TEST")
		require_NoError(t, err)
		if mset != nil && mset.isLeader() {
			leaderNode = mset.raftNode()
			break
		}
	}
	require_NotNil(t, leaderNode)

	// Test that we can get metrics from the leader
	metrics := leaderNode.GetProtectionMetrics()
	require_NotNil(t, metrics)
	
	// Verify the metrics structure
	require_Equal(t, metrics.LogProtectionEnabled, true) // R=3 should have protection
	require_Equal(t, metrics.Recovered, true)
	require_Equal(t, metrics.EmptyLog, false) // Leader should have log entries
	require_Equal(t, metrics.VoteRejections, uint64(0))
	require_Equal(t, metrics.CandidacyRejections, uint64(0))
	require_Equal(t, metrics.RecoveryRejections, uint64(0))

	// Log the metrics for visibility
	t.Logf("Stream TEST protection metrics: %+v", metrics)
}

func TestRaftProtectionRejectOnEmptyLog(t *testing.T) {
	// Test the rejectOnEmptyLog logic directly
	n := &raft{
		created:       time.Now(),
		logProtection: true,
		pterm:         0, // Empty log
		preferred:     "other-node",
		protectionMetrics: struct {
			voteRejections      uint64
			candidacyRejections uint64
			recoveryRejections  uint64
			lastProtectionTime  time.Time
		}{},
	}

	// Test 1: Should reject because we have empty log and are not preferred
	rejected := n.rejectOnEmptyLog("candidate-id")
	require_Equal(t, rejected, true)

	// Test 2: Should not reject if we're the preferred leader
	n.preferred = n.id
	rejected = n.rejectOnEmptyLog(n.id)
	require_Equal(t, rejected, false)

	// Test 3: Should not reject if we have log entries
	n.preferred = "other-node"
	n.pterm = 1
	rejected = n.rejectOnEmptyLog("candidate-id")
	require_Equal(t, rejected, false)

	// Test 4: Should not reject if protection is disabled
	n.pterm = 0
	n.logProtection = false
	rejected = n.rejectOnEmptyLog("candidate-id")
	require_Equal(t, rejected, false)

	// Test 5: Should not reject after timeout when no preferred leader
	n.logProtection = true
	n.preferred = _EMPTY_
	n.created = time.Now().Add(-6 * time.Minute) // Past the 5 minute timeout
	rejected = n.rejectOnEmptyLog("candidate-id")
	require_Equal(t, rejected, false)
}