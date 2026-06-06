// Copyright 2026 The NATS Authors
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
	"time"
)

// Validates the index fast path (phase 1) plus the reverse-scan fallback
// (phase 2) in startingSequenceForSources: each source's recovered sseq must
// equal the last origin sequence that was sourced, regardless of whether the
// source resolves via the per-subject index (distinct concrete subject) or via
// the fallback scan (subject transform).
func TestJetStreamStartingSequenceForSourcesIndexFastPath(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Distinct-subject origins (resolve via the index fast path) and one that the
	// sourcing stream pulls through a subject transform (forces the phase 2 scan).
	type srcSpec struct {
		origin    string
		subject   string
		transform bool
		dest      string
		count     int // messages published to the origin (== expected last origin seq)
	}
	specs := []srcSpec{
		{origin: "O1", subject: "s1", count: 3},
		{origin: "O2", subject: "s2", count: 7},
		{origin: "O3", subject: "s3", count: 11},
		{origin: "O4", subject: "s4", transform: true, dest: "t4", count: 5},
	}

	var sources []*StreamSource
	for _, sp := range specs {
		jsStreamCreate(t, nc, &StreamConfig{Name: sp.origin, Subjects: []string{sp.subject}, Storage: FileStorage})
		ss := &StreamSource{Name: sp.origin}
		if sp.transform {
			ss.SubjectTransforms = []SubjectTransformConfig{{Source: sp.subject, Destination: sp.dest}}
		} else {
			ss.FilterSubject = sp.subject
		}
		sources = append(sources, ss)
	}

	jsStreamCreate(t, nc, &StreamConfig{
		Name:     "agg",
		Subjects: []string{"direct"},
		Storage:  FileStorage,
		Sources:  sources,
	})

	// Publish to each origin.
	total := 0
	for _, sp := range specs {
		for i := 0; i < sp.count; i++ {
			_, err := js.Publish(sp.subject, nil)
			require_NoError(t, err)
		}
		total += sp.count
	}

	// Bury the sourced messages under a lot of direct publishes to the agg stream,
	// so a naive reverse scan would have to walk back over all of them.
	const direct = 20_000
	for i := 0; i < direct; i++ {
		_, err := js.Publish("direct", nil)
		require_NoError(t, err)
	}

	// Wait until everything has been sourced.
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("agg")
		if err != nil {
			return err
		}
		if want := uint64(total + direct); si.State.Msgs != want {
			return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, want)
		}
		return nil
	})

	mset, err := s.globalAccount().lookupStream("agg")
	require_NoError(t, err)

	mset.mu.Lock()
	mset.startingSequenceForSources()
	got := make(map[string]uint64, len(mset.sources))
	for _, si := range mset.sources {
		got[si.name] = si.sseq
	}
	mset.mu.Unlock()

	for _, sp := range specs {
		if got[sp.origin] != uint64(sp.count) {
			t.Fatalf("source %q: expected starting seq %d, got %d", sp.origin, sp.count, got[sp.origin])
		}
	}
}
