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

// Exercises the phase 2 fallback cases of startingSequenceForSources: a
// catch-all (empty filter) source and two sources transformed onto the SAME
// destination subject (shared stored subject), mixed with a distinct-subject
// source that takes the phase 1 index fast path.
func TestJetStreamStartingSequenceForSourcesAmbiguity(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	for _, o := range []struct{ name, subj string }{{"A", "a"}, {"C", "c"}, {"D", "d"}, {"E", "e"}} {
		jsStreamCreate(t, nc, &StreamConfig{Name: o.name, Subjects: []string{o.subj}, Storage: FileStorage})
	}

	jsStreamCreate(t, nc, &StreamConfig{
		Name:     "agg",
		Subjects: []string{"direct"},
		Storage:  FileStorage,
		Sources: []*StreamSource{
			{Name: "A", FilterSubject: "a"}, // distinct subject -> phase 1
			{Name: "C"},                     // empty (catch-all) filter -> phase 2
			{Name: "D", SubjectTransforms: []SubjectTransformConfig{{Source: "d", Destination: "m"}}}, // shared dest -> phase 2
			{Name: "E", SubjectTransforms: []SubjectTransformConfig{{Source: "e", Destination: "m"}}}, // shared dest -> phase 2
		},
	})

	counts := map[string]string{"a": "A", "c": "C", "d": "D", "e": "E"}
	expect := map[string]int{"A": 4, "C": 3, "D": 5, "E": 8}
	total := 0
	for subj, name := range counts {
		for i := 0; i < expect[name]; i++ {
			_, err := js.Publish(subj, nil)
			require_NoError(t, err)
		}
		total += expect[name]
	}

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("agg")
		if err != nil {
			return err
		}
		if si.State.Msgs != uint64(total) {
			return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, total)
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

	for name, n := range expect {
		if got[name] != uint64(n) {
			t.Fatalf("source %q: expected starting seq %d, got %d", name, n, got[name])
		}
	}
}

// Validates the index fast path in setStartingSequenceForSources (the
// STREAM.UPDATE twin), with a distinct-subject source (phase 1) and a
// catch-all source (phase 2). We clear the in-memory sseq and confirm it is
// recovered correctly.
func TestJetStreamSetStartingSequenceForSourcesIndex(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	jsStreamCreate(t, nc, &StreamConfig{Name: "P", Subjects: []string{"p"}, Storage: FileStorage})
	jsStreamCreate(t, nc, &StreamConfig{Name: "Q", Subjects: []string{"q"}, Storage: FileStorage})
	jsStreamCreate(t, nc, &StreamConfig{Name: "R", Subjects: []string{"r"}, Storage: FileStorage})

	jsStreamCreate(t, nc, &StreamConfig{
		Name:     "agg3",
		Subjects: []string{"direct"},
		Storage:  FileStorage,
		Sources: []*StreamSource{
			{Name: "P", FilterSubject: "p"}, // distinct subject -> phase 1
			{Name: "Q"},                     // empty (catch-all) filter -> phase 2
			{Name: "R", SubjectTransforms: []SubjectTransformConfig{{Source: "r", Destination: "tr"}}}, // concrete transform dest -> phase 1
		},
	})

	expect := map[string]int{"P": 6, "Q": 9, "R": 4}
	total := 0
	for subj, name := range map[string]string{"p": "P", "q": "Q", "r": "R"} {
		for i := 0; i < expect[name]; i++ {
			_, err := js.Publish(subj, nil)
			require_NoError(t, err)
		}
		total += expect[name]
	}

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("agg3")
		if err != nil {
			return err
		}
		if si.State.Msgs != uint64(total) {
			return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, total)
		}
		return nil
	})

	mset, err := s.globalAccount().lookupStream("agg3")
	require_NoError(t, err)

	mset.mu.Lock()
	iNames := make(map[string]struct{}, len(mset.sources))
	for iname, si := range mset.sources {
		iNames[iname] = struct{}{}
		// Clear so we verify the function actually recovers the sequence.
		si.sseq, si.dseq = 0, 0
	}
	mset.setStartingSequenceForSources(iNames)
	got := make(map[string]uint64, len(mset.sources))
	for _, si := range mset.sources {
		got[si.name] = si.sseq
	}
	mset.mu.Unlock()

	for name, n := range expect {
		if got[name] != uint64(n) {
			t.Fatalf("source %q: expected starting seq %d, got %d", name, n, got[name])
		}
	}
}

// A templated subject transform (destination contains a {{...}} mapping token)
// cannot be resolved by the phase 1 index fast path, so it must be recovered by
// the phase 2 reverse scan. This locks in that the scan handles templated
// transform sources correctly via both startingSequenceForSources and
// setStartingSequenceForSources.
func TestJetStreamStartingSequenceForSourcesTemplatedTransform(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	jsStreamCreate(t, nc, &StreamConfig{Name: "T", Subjects: []string{"tin.>"}, Storage: FileStorage})

	jsStreamCreate(t, nc, &StreamConfig{
		Name:     "aggT",
		Subjects: []string{"direct"},
		Storage:  FileStorage,
		Sources: []*StreamSource{
			// Templated destination -> phase 1 defers, phase 2 must recover it.
			{Name: "T", SubjectTransforms: []SubjectTransformConfig{{Source: "tin.*", Destination: "tout.{{wildcard(1)}}"}}},
		},
	})

	const n = 7
	for i := 0; i < n; i++ {
		_, err := js.Publish("tin.1", nil)
		require_NoError(t, err)
	}
	// Bury under direct publishes.
	for i := 0; i < 5_000; i++ {
		_, err := js.Publish("direct", nil)
		require_NoError(t, err)
	}

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("aggT")
		if err != nil {
			return err
		}
		if si.State.Msgs != uint64(n+5_000) {
			return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, n+5_000)
		}
		return nil
	})

	mset, err := s.globalAccount().lookupStream("aggT")
	require_NoError(t, err)

	// Full-rebuild path.
	mset.mu.Lock()
	mset.startingSequenceForSources()
	var full uint64
	for _, si := range mset.sources {
		full = si.sseq
	}
	mset.mu.Unlock()
	if full != uint64(n) {
		t.Fatalf("startingSequenceForSources: expected %d, got %d", n, full)
	}

	// Update-twin path (clear then recover).
	mset.mu.Lock()
	iNames := make(map[string]struct{}, len(mset.sources))
	for iname, si := range mset.sources {
		iNames[iname] = struct{}{}
		si.sseq, si.dseq = 0, 0
	}
	mset.setStartingSequenceForSources(iNames)
	var upd uint64
	for _, si := range mset.sources {
		upd = si.sseq
	}
	mset.mu.Unlock()
	if upd != uint64(n) {
		t.Fatalf("setStartingSequenceForSources: expected %d, got %d", n, upd)
	}
}
