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

//go:build !skip_js_tests

package server

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
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

// Two sources with templated transforms onto the same wildcard destination
// space (gout.*) with distinct rendered subjects. Exercises phase 1 resolving
// a wildcard transform via transformUntokenize + LoadLastMsg, and the phase 2
// fallback narrowing on the wildcard form (gout.*) rather than ">".
func TestJetStreamStartingSequenceForSourcesSharedWildcardTransform(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	jsStreamCreate(t, nc, &StreamConfig{Name: "G", Subjects: []string{"gin.>"}, Storage: FileStorage})
	jsStreamCreate(t, nc, &StreamConfig{Name: "H", Subjects: []string{"hin.>"}, Storage: FileStorage})

	jsStreamCreate(t, nc, &StreamConfig{
		Name:     "aggW",
		Subjects: []string{"direct"},
		Storage:  FileStorage,
		Sources: []*StreamSource{
			{Name: "G", SubjectTransforms: []SubjectTransformConfig{{Source: "gin.*", Destination: "gout.{{wildcard(1)}}"}}},
			{Name: "H", SubjectTransforms: []SubjectTransformConfig{{Source: "hin.*", Destination: "gout.{{wildcard(1)}}"}}},
		},
	})

	expect := map[string]int{"G": 4, "H": 6}
	// G -> gout.a, H -> gout.b ; both match gout.* but are distinct subjects.
	for i := 0; i < expect["G"]; i++ {
		_, err := js.Publish("gin.a", nil)
		require_NoError(t, err)
	}
	for i := 0; i < expect["H"]; i++ {
		_, err := js.Publish("hin.b", nil)
		require_NoError(t, err)
	}
	for i := 0; i < 3_000; i++ {
		_, err := js.Publish("direct", nil)
		require_NoError(t, err)
	}

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("aggW")
		if err != nil {
			return err
		}
		if si.State.Msgs != uint64(expect["G"]+expect["H"]+3_000) {
			return fmt.Errorf("waiting for sourcing: have %d", si.State.Msgs)
		}
		return nil
	})

	mset, err := s.globalAccount().lookupStream("aggW")
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

// End-to-end rollout/restart test: a sourcing stream pulls from several origins
// (across both recovery phases), the server is hard-restarted (as in a rolling
// upgrade), and then more is published to the origins. After recovery, sourcing
// must resume exactly where it left off — every origin message sourced exactly
// once, with no gap and no duplicate. This exercises the real recovery path
// (setLeader -> setupSourceConsumers -> startingSequenceForSources) rather than
// calling the resolver directly.
func TestJetStreamSourcingResumeAfterRolloutRestart(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	// Origins chosen to span both recovery phases:
	//   O1,O2 distinct concrete subjects  -> phase 1 (index fast path)
	//   O3    templated subject transform -> phase 2 (reverse scan)
	//   O4    catch-all (empty filter)     -> phase 2 (reverse scan)
	type origin struct {
		name, subj, pub string
		transform       bool
		dest            string
		catchall        bool
	}
	origins := []origin{
		{name: "O1", subj: "s1", pub: "s1"},
		{name: "O2", subj: "s2", pub: "s2"},
		{name: "O3", subj: "s3.*", pub: "s3.a", transform: true, dest: "tout.{{wildcard(1)}}"},
		{name: "O4", subj: "s4", pub: "s4", catchall: true},
	}

	var sources []*StreamSource
	for _, o := range origins {
		_, err := jsStreamCreate(t, nc, &StreamConfig{Name: o.name, Subjects: []string{o.subj}, Storage: FileStorage})
		require_NoError(t, err)
		ss := &StreamSource{Name: o.name}
		switch {
		case o.transform:
			ss.SubjectTransforms = []SubjectTransformConfig{{Source: o.subj, Destination: o.dest}}
		case o.catchall:
			// leave FilterSubject empty -> catch-all
		default:
			ss.FilterSubject = o.subj
		}
		sources = append(sources, ss)
	}

	// Sources-only hub, so every stored message carries a JSStreamSource header.
	_, err := jsStreamCreate(t, nc, &StreamConfig{Name: "agg", Storage: FileStorage, Sources: sources})
	require_NoError(t, err)

	// publishBatch publishes per-origin counts to their origin streams.
	publishBatch := func(js nats.JetStreamContext, counts map[string]int) {
		for _, o := range origins {
			for i := 0; i < counts[o.name]; i++ {
				_, err := js.Publish(o.pub, nil)
				require_NoError(t, err)
			}
		}
	}
	waitForAgg := func(js nats.JetStreamContext, want uint64) {
		t.Helper()
		checkFor(t, 20*time.Second, 100*time.Millisecond, func() error {
			si, err := js.StreamInfo("agg")
			if err != nil {
				return err
			}
			if si.State.Msgs != want {
				return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, want)
			}
			return nil
		})
	}

	// Batch 1, before the restart.
	batch1 := map[string]int{"O1": 3, "O2": 5, "O3": 4, "O4": 6}
	want1 := 3 + 5 + 4 + 6
	publishBatch(js, batch1)
	waitForAgg(js, uint64(want1))

	// Hard restart the server (rolling-upgrade style), preserving the store dir.
	port := s.opts.Port
	sd := s.StoreDir()
	nc.Close()
	s.Shutdown()
	s.WaitForShutdown()
	s = RunJetStreamServerOnPort(port, sd)
	defer s.Shutdown()

	nc, js = jsClientConnect(t, s)
	defer nc.Close()

	// Batch 2, after the restart — sourcing must resume and pull exactly these.
	batch2 := map[string]int{"O1": 7, "O2": 2, "O3": 9, "O4": 3}
	total := map[string]int{}
	want2 := want1
	for _, o := range origins {
		total[o.name] = batch1[o.name] + batch2[o.name]
		want2 += batch2[o.name]
	}
	publishBatch(js, batch2)
	waitForAgg(js, uint64(want2))

	// Verify exactly-once: scan every stored message, group the origin sequences
	// by source stream, and require each to be precisely 1..total with no gap and
	// no duplicate. A missed resume would leave a gap; a re-sourced run would add
	// duplicates (sourced messages have no Nats-Msg-Id dedupe).
	mset, err := s.globalAccount().lookupStream("agg")
	require_NoError(t, err)

	var state StreamState
	mset.store.FastState(&state)
	seen := map[string][]uint64{}
	for seq := state.FirstSeq; seq <= state.LastSeq; seq++ {
		sm, err := mset.getMsg(seq)
		require_NoError(t, err)
		ss := getHeader(JSStreamSource, sm.Header)
		require_True(t, len(ss) > 0)
		sname, _, osseq := streamAndSeq(string(ss))
		seen[sname] = append(seen[sname], osseq)
	}

	for _, o := range origins {
		got := seen[o.name]
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		if len(got) != total[o.name] {
			t.Fatalf("source %q: sourced %d messages, want %d (seqs=%v)", o.name, len(got), total[o.name], got)
		}
		for i, sseq := range got {
			if sseq != uint64(i+1) {
				t.Fatalf("source %q: non-contiguous origin seqs (gap/duplicate) at index %d: got %d, want %d (all=%v)",
					o.name, i, sseq, i+1, got)
			}
		}
	}
}

// Differential / property test: for randomized source layouts (mixed source
// kinds, random counts, randomly interleaved so each source's last message
// lands at a random store depth), the index-based resolver must produce the
// exact same per-source starting sequence as an independent brute-force
// reference computed from the same store (the most recent origin sequence per
// source). This exercises phase 1 and phase 2 across many shapes the
// hand-written cases don't enumerate.
func TestJetStreamStartingSequenceForSourcesDifferential(t *testing.T) {
	// Source kinds, each mapping to a single origin stream.
	const (
		kindDistinct  = iota // distinct concrete subject       -> phase 1
		kindCatchall         // empty filter (catch-all)        -> phase 2
		kindConcrete         // concrete subject transform      -> phase 1
		kindTemplated        // templated (wildcard) transform  -> phase 2
		numKinds
	)

	// A handful of fixed seeds keeps the test reproducible while still covering
	// many layouts; the failing seed is reported for replay.
	for _, seed := range []int64{1, 2, 3, 5, 8, 13, 21, 34} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))

			s := RunBasicJetStreamServer(t)
			defer s.Shutdown()
			nc, js := jsClientConnect(t, s)
			defer nc.Close()

			type src struct {
				name, originSubj, pubSubj string
				count                     int
			}

			n := 2 + rng.Intn(5) // 2..6 sources
			var srcs []src
			var sources []*StreamSource
			for i := 0; i < n; i++ {
				name := fmt.Sprintf("O%d", i)
				kind := rng.Intn(numKinds)
				sp := src{name: name, count: rng.Intn(8)} // 0..7 (0 exercises the never-sourced case)
				ss := &StreamSource{Name: name}
				switch kind {
				case kindDistinct:
					sp.originSubj, sp.pubSubj = fmt.Sprintf("d%d", i), fmt.Sprintf("d%d", i)
					ss.FilterSubject = sp.pubSubj
				case kindCatchall:
					sp.originSubj, sp.pubSubj = fmt.Sprintf("c%d", i), fmt.Sprintf("c%d", i)
					// empty filter -> catch-all
				case kindConcrete:
					sp.originSubj, sp.pubSubj = fmt.Sprintf("x%d", i), fmt.Sprintf("x%d", i)
					ss.SubjectTransforms = []SubjectTransformConfig{{Source: sp.pubSubj, Destination: fmt.Sprintf("tx%d", i)}}
				case kindTemplated:
					sp.originSubj, sp.pubSubj = fmt.Sprintf("w%d.*", i), fmt.Sprintf("w%d.a", i)
					ss.SubjectTransforms = []SubjectTransformConfig{{Source: sp.originSubj, Destination: fmt.Sprintf("tw%d.{{wildcard(1)}}", i)}}
				}
				_, err := jsStreamCreate(t, nc, &StreamConfig{Name: name, Subjects: []string{sp.originSubj}, Storage: FileStorage})
				require_NoError(t, err)
				srcs = append(srcs, sp)
				sources = append(sources, ss)
			}

			_, err := jsStreamCreate(t, nc, &StreamConfig{Name: "agg", Storage: FileStorage, Sources: sources})
			require_NoError(t, err)

			// Build an interleaved publish plan and shuffle it, so each source's
			// last message ends up at a random depth in the hub store.
			var plan []string
			total := 0
			for _, sp := range srcs {
				for i := 0; i < sp.count; i++ {
					plan = append(plan, sp.pubSubj)
				}
				total += sp.count
			}
			rng.Shuffle(len(plan), func(i, j int) { plan[i], plan[j] = plan[j], plan[i] })
			for _, subj := range plan {
				_, err := js.Publish(subj, nil)
				require_NoError(t, err)
			}

			checkFor(t, 20*time.Second, 100*time.Millisecond, func() error {
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

			// Brute-force reference: most recent origin sequence per source stream,
			// computed by scanning the whole store independently of the resolver.
			ref := map[string]uint64{}
			var state StreamState
			mset.store.FastState(&state)
			var smv StoreMsg
			for seq := state.FirstSeq; seq <= state.LastSeq; seq++ {
				sm, err := mset.store.LoadMsg(seq, &smv)
				if err != nil {
					continue
				}
				ss := getHeader(JSStreamSource, sm.hdr)
				if len(ss) == 0 {
					continue
				}
				sname, _, osseq := streamAndSeq(string(ss))
				if osseq > ref[sname] {
					ref[sname] = osseq
				}
			}

			// Resolver under test.
			mset.mu.Lock()
			mset.startingSequenceForSources()
			got := make(map[string]uint64, len(mset.sources))
			for _, si := range mset.sources {
				got[si.name] = si.sseq
			}
			mset.mu.Unlock()

			for _, sp := range srcs {
				if got[sp.name] != ref[sp.name] {
					t.Fatalf("seed %d: source %q resolved sseq=%d, reference (brute-force scan)=%d (count=%d)",
						seed, sp.name, got[sp.name], ref[sp.name], sp.count)
				}
				// Sanity: the reference must also equal the number we published
				// (origin seq == count), confirming sourcing actually completed.
				if ref[sp.name] != uint64(sp.count) {
					t.Fatalf("seed %d: source %q reference sseq=%d but published %d", seed, sp.name, ref[sp.name], sp.count)
				}
			}
		})
	}
}

// MemoryStorage path: the resolver must work on a MemStore-backed sourcing
// stream too (which uses the linear LoadPrevMsgMulti and an index-light
// LoadLastMsg). Mixes a phase 1 source (distinct subject), a phase 2 catch-all,
// and a phase 2 concrete transform, all on memory storage.
func TestJetStreamStartingSequenceForSourcesMemStore(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	for _, o := range []struct{ name, subj string }{{"M1", "m1"}, {"M2", "m2"}, {"M3", "m3"}} {
		_, err := jsStreamCreate(t, nc, &StreamConfig{Name: o.name, Subjects: []string{o.subj}, Storage: MemoryStorage})
		require_NoError(t, err)
	}

	_, err := jsStreamCreate(t, nc, &StreamConfig{
		Name:     "aggM",
		Subjects: []string{"direct"},
		Storage:  MemoryStorage,
		Sources: []*StreamSource{
			{Name: "M1", FilterSubject: "m1"}, // distinct subject -> phase 1
			{Name: "M2"},                      // empty filter (catch-all) -> phase 2
			{Name: "M3", SubjectTransforms: []SubjectTransformConfig{{Source: "m3", Destination: "tm3"}}}, // concrete transform -> phase 1
		},
	})
	require_NoError(t, err)

	expect := map[string]int{"M1": 4, "M2": 6, "M3": 3}
	total := 0
	for subj, name := range map[string]string{"m1": "M1", "m2": "M2", "m3": "M3"} {
		for i := 0; i < expect[name]; i++ {
			_, err := js.Publish(subj, nil)
			require_NoError(t, err)
		}
		total += expect[name]
	}
	// Some direct traffic to spread depth.
	for i := 0; i < 200; i++ {
		_, err := js.Publish("direct", nil)
		require_NoError(t, err)
	}

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("aggM")
		if err != nil {
			return err
		}
		if si.State.Msgs != uint64(total+200) {
			return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, total+200)
		}
		return nil
	})

	mset, err := s.globalAccount().lookupStream("aggM")
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

// Exotic transform (partition): a partition() destination renders to a mapping
// token that can't be reduced to a subject wildcard, so the source is recovered
// by the phase 2 full-> fallback. Verify it still resolves to the correct origin
// sequence, alongside a distinct-subject source that takes the phase 1 fast path.
func TestJetStreamStartingSequenceForSourcesPartitionTransform(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	_, err := jsStreamCreate(t, nc, &StreamConfig{Name: "EVT", Subjects: []string{"evt.*"}, Storage: FileStorage})
	require_NoError(t, err)
	_, err = jsStreamCreate(t, nc, &StreamConfig{Name: "DST1", Subjects: []string{"d1"}, Storage: FileStorage})
	require_NoError(t, err)

	_, err = jsStreamCreate(t, nc, &StreamConfig{
		Name:     "aggP",
		Subjects: []string{"direct"},
		Storage:  FileStorage,
		Sources: []*StreamSource{
			// partition() destination -> phase 1 defers, phase 2 full-> recovers it.
			{Name: "EVT", SubjectTransforms: []SubjectTransformConfig{{Source: "evt.*", Destination: "p.{{partition(10,1)}}"}}},
			{Name: "DST1", FilterSubject: "d1"}, // distinct subject -> phase 1
		},
	})
	require_NoError(t, err)

	const evtN, dstN = 9, 5
	for i := 0; i < evtN; i++ {
		_, err := js.Publish("evt.k", nil)
		require_NoError(t, err)
	}
	for i := 0; i < dstN; i++ {
		_, err := js.Publish("d1", nil)
		require_NoError(t, err)
	}
	// Bury under direct publishes so a naive scan would have to walk back.
	for i := 0; i < 3_000; i++ {
		_, err := js.Publish("direct", nil)
		require_NoError(t, err)
	}

	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("aggP")
		if err != nil {
			return err
		}
		if si.State.Msgs != uint64(evtN+dstN+3_000) {
			return fmt.Errorf("waiting for sourcing: have %d", si.State.Msgs)
		}
		return nil
	})

	mset, err := s.globalAccount().lookupStream("aggP")
	require_NoError(t, err)

	mset.mu.Lock()
	mset.startingSequenceForSources()
	got := make(map[string]uint64, len(mset.sources))
	for _, si := range mset.sources {
		got[si.name] = si.sseq
	}
	mset.mu.Unlock()

	for name, n := range map[string]uint64{"EVT": evtN, "DST1": dstN} {
		if got[name] != n {
			t.Fatalf("source %q: expected starting seq %d, got %d", name, n, got[name])
		}
	}
}

// Seeded store-state edge cases that can't be built through the JS client.
// We create the sourcing stream (and empty origins, so the source consumers
// stay idle), then write crafted messages directly into its store and run the
// resolver. Covers: pre-2.10 source headers (stream-name-only, no iname),
// subject overlap (a source's stored subject also carries other sources' /
// header-less messages), and interior deletes before a source's last message.
func TestJetStreamStartingSequenceForSourcesSeededEdges(t *testing.T) {
	// New-format header: "<name> <originSeq> <filter> <dest> <orig>".
	newHdr := func(name string, originSeq uint64, filter, dest, orig string) []byte {
		return genHeader(nil, JSStreamSource, fmt.Sprintf("%s %d %s %s %s", name, originSeq, filter, dest, orig))
	}
	// Pre-2.10 header: "<name> <originSeq>" (no iname); matched by stream name.
	oldHdr := func(name string, originSeq uint64) []byte {
		return genHeader(nil, JSStreamSource, fmt.Sprintf("%s %d", name, originSeq))
	}

	// setup creates empty origins + a sources-only hub and returns its mset.
	setup := func(t *testing.T, s *Server, nc *nats.Conn, sources []*StreamSource, originSubjs map[string]string) *stream {
		for name, subj := range originSubjs {
			_, err := jsStreamCreate(t, nc, &StreamConfig{Name: name, Subjects: []string{subj}, Storage: FileStorage})
			require_NoError(t, err)
		}
		_, err := jsStreamCreate(t, nc, &StreamConfig{Name: "aggS", Storage: FileStorage, Sources: sources})
		require_NoError(t, err)
		mset, err := s.globalAccount().lookupStream("aggS")
		require_NoError(t, err)
		return mset
	}

	resolve := func(mset *stream) map[string]uint64 {
		mset.mu.Lock()
		defer mset.mu.Unlock()
		mset.startingSequenceForSources()
		got := make(map[string]uint64, len(mset.sources))
		for _, si := range mset.sources {
			got[si.name] = si.sseq
		}
		return got
	}

	t.Run("pre-2.10 header", func(t *testing.T) {
		s := RunBasicJetStreamServer(t)
		defer s.Shutdown()
		nc, _ := jsClientConnect(t, s)
		defer nc.Close()

		mset := setup(t, s, nc,
			[]*StreamSource{{Name: "OLD", FilterSubject: "old"}},
			map[string]string{"OLD": "old"})

		// Three pre-2.10 "old" messages (origin seqs 1..3), then header-less noise.
		for i := uint64(1); i <= 3; i++ {
			_, _, err := mset.store.StoreMsg("old", oldHdr("OLD", i), nil, 0)
			require_NoError(t, err)
		}
		for i := 0; i < 50; i++ {
			_, _, err := mset.store.StoreMsg("noise", nil, nil, 0)
			require_NoError(t, err)
		}

		if got := resolve(mset)["OLD"]; got != 3 {
			t.Fatalf("pre-2.10: OLD resolved sseq=%d, want 3", got)
		}
	})

	t.Run("subject overlap", func(t *testing.T) {
		s := RunBasicJetStreamServer(t)
		defer s.Shutdown()
		nc, _ := jsClientConnect(t, s)
		defer nc.Close()

		// Two sources filtering the SAME stored subject, plus header-less direct
		// publishes on it. The index points at the last "shared" message (which is
		// header-less), so phase 1 defers both and phase 2 disambiguates by iname.
		mset := setup(t, s, nc,
			[]*StreamSource{{Name: "A", FilterSubject: "shared"}, {Name: "B", FilterSubject: "shared"}},
			map[string]string{"A": "ina", "B": "inb"})

		// Interleave A (origin 1..4) and B (origin 1..6) onto "shared".
		_, _, err := mset.store.StoreMsg("shared", newHdr("A", 1, "shared", fwcs, "shared"), nil, 0)
		require_NoError(t, err)
		_, _, err = mset.store.StoreMsg("shared", newHdr("B", 1, "shared", fwcs, "shared"), nil, 0)
		require_NoError(t, err)
		_, _, err = mset.store.StoreMsg("shared", newHdr("A", 4, "shared", fwcs, "shared"), nil, 0)
		require_NoError(t, err)
		_, _, err = mset.store.StoreMsg("shared", newHdr("B", 6, "shared", fwcs, "shared"), nil, 0)
		require_NoError(t, err)
		// Header-less direct publishes on the same subject, AFTER both sources'
		// last messages, so the index lands on a non-source message.
		for i := 0; i < 10; i++ {
			_, _, err := mset.store.StoreMsg("shared", nil, nil, 0)
			require_NoError(t, err)
		}

		got := resolve(mset)
		if got["A"] != 4 || got["B"] != 6 {
			t.Fatalf("subject overlap: A=%d (want 4) B=%d (want 6)", got["A"], got["B"])
		}
	})

	t.Run("interior delete of last subject msg", func(t *testing.T) {
		s := RunBasicJetStreamServer(t)
		defer s.Shutdown()
		nc, _ := jsClientConnect(t, s)
		defer nc.Close()

		mset := setup(t, s, nc,
			[]*StreamSource{{Name: "D", FilterSubject: "del"}},
			map[string]string{"D": "del"})

		// Five "del" messages (origin 1..5) at hub seqs 1..5, then noise.
		var delSeqs []uint64
		for i := uint64(1); i <= 5; i++ {
			seq, _, err := mset.store.StoreMsg("del", newHdr("D", i, "del", fwcs, "del"), nil, 0)
			require_NoError(t, err)
			delSeqs = append(delSeqs, seq)
		}
		for i := 0; i < 30; i++ {
			_, _, err := mset.store.StoreMsg("noise", nil, nil, 0)
			require_NoError(t, err)
		}
		// Delete the LAST "del" message (origin seq 5). loadLast must now skip it
		// and return origin seq 4.
		ok, err := mset.store.RemoveMsg(delSeqs[len(delSeqs)-1])
		require_NoError(t, err)
		require_True(t, ok)

		if got := resolve(mset)["D"]; got != 4 {
			t.Fatalf("interior delete: D resolved sseq=%d, want 4 (last msg deleted)", got)
		}
	})
}

// Config-update scoping: STREAM.UPDATE adding/removing sources must recompute
// only the affected sources and preserve the resume sequence of the rest. This
// covers the needsStartingSeqNum path and setStartingSequenceForSources's
// promise to touch only the inames it is given.
func TestJetStreamSourcingResumeConfigUpdateScoping(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	for _, o := range []struct{ name, subj string }{{"U1", "ua"}, {"U2", "ub"}, {"U3", "uc"}} {
		_, err := jsStreamCreate(t, nc, &StreamConfig{Name: o.name, Subjects: []string{o.subj}, Storage: FileStorage})
		require_NoError(t, err)
	}
	src := func(name, subj string) *StreamSource { return &StreamSource{Name: name, FilterSubject: subj} }
	all := []*StreamSource{src("U1", "ua"), src("U2", "ub"), src("U3", "uc")}

	_, err := jsStreamCreate(t, nc, &StreamConfig{Name: "aggU", Subjects: []string{"direct"}, Storage: FileStorage, Sources: all})
	require_NoError(t, err)

	expect := map[string]uint64{"U1": 4, "U2": 6, "U3": 3}
	total := 0
	for subj, name := range map[string]string{"ua": "U1", "ub": "U2", "uc": "U3"} {
		for i := 0; i < int(expect[name]); i++ {
			_, err := js.Publish(subj, nil)
			require_NoError(t, err)
		}
		total += int(expect[name])
	}
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("aggU")
		if err != nil {
			return err
		}
		if si.State.Msgs != uint64(total) {
			return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, total)
		}
		return nil
	})

	mset, err := s.globalAccount().lookupStream("aggU")
	require_NoError(t, err)
	in := func(name, subj string) string { return (&StreamSource{Name: name, FilterSubject: subj}).composeIName() }
	i1, i2, i3 := in("U1", "ua"), in("U2", "ub"), in("U3", "uc")

	// Strong scoping check on the function itself: poison U1/U3 and clear U2, then
	// recompute only {U2}. U2 must be recovered; U1/U3 must be left untouched.
	mset.mu.Lock()
	mset.sources[i1].sseq, mset.sources[i3].sseq = 999, 777
	mset.sources[i2].sseq = 0
	mset.setStartingSequenceForSources(map[string]struct{}{i2: {}})
	g1, g2, g3 := mset.sources[i1].sseq, mset.sources[i2].sseq, mset.sources[i3].sseq
	// Restore correct state for the rest of the test.
	mset.startingSequenceForSources()
	mset.mu.Unlock()
	if g2 != 6 {
		t.Fatalf("scoped recompute: U2 sseq=%d, want 6", g2)
	}
	if g1 != 999 || g3 != 777 {
		t.Fatalf("scoped recompute touched non-target sources: U1=%d (want 999) U3=%d (want 777)", g1, g3)
	}

	sseqOf := func(iname string) uint64 {
		mset.mu.RLock()
		defer mset.mu.RUnlock()
		if si := mset.sources[iname]; si != nil {
			return si.sseq
		}
		return 0
	}
	has := func(iname string) bool {
		mset.mu.RLock()
		defer mset.mu.RUnlock()
		_, ok := mset.sources[iname]
		return ok
	}

	// End-to-end: remove U2 via STREAM.UPDATE. U1/U3 must be preserved, U2 gone.
	_, err = jsStreamUpdate(t, nc, &StreamConfig{Name: "aggU", Subjects: []string{"direct"}, Storage: FileStorage,
		Sources: []*StreamSource{src("U1", "ua"), src("U3", "uc")}})
	require_NoError(t, err)
	if has(i2) {
		t.Fatalf("U2 still present after removal")
	}
	if sseqOf(i1) != 4 || sseqOf(i3) != 3 {
		t.Fatalf("removal recomputed survivors: U1=%d (want 4) U3=%d (want 3)", sseqOf(i1), sseqOf(i3))
	}

	// Re-add U2. It must be recovered from the store (=6); U1/U3 preserved.
	_, err = jsStreamUpdate(t, nc, &StreamConfig{Name: "aggU", Subjects: []string{"direct"}, Storage: FileStorage,
		Sources: []*StreamSource{src("U1", "ua"), src("U2", "ub"), src("U3", "uc")}})
	require_NoError(t, err)
	if got := sseqOf(i2); got != 6 {
		t.Fatalf("re-added U2 sseq=%d, want 6 (recovered from store)", got)
	}
	if sseqOf(i1) != 4 || sseqOf(i3) != 3 {
		t.Fatalf("re-add recomputed survivors: U1=%d (want 4) U3=%d (want 3)", sseqOf(i1), sseqOf(i3))
	}
}

// FirstSeq > 1: after limits/age expiry advances the stream's first sequence,
// the resolver must still resume correctly — the phase 1 index lookup and the
// phase 2 reverse scan must terminate at FirstSeq, not seq 1. A phase 1 source
// (distinct subject) and a phase 2 source (catch-all) are both exercised with
// their last messages retained near the end of a windowed store.
func TestJetStreamSourcingResumeFirstSeqAdvanced(t *testing.T) {
	s := RunBasicJetStreamServer(t)
	defer s.Shutdown()
	nc, js := jsClientConnect(t, s)
	defer nc.Close()

	_, err := jsStreamCreate(t, nc, &StreamConfig{Name: "F1", Subjects: []string{"fa"}, Storage: FileStorage})
	require_NoError(t, err)
	_, err = jsStreamCreate(t, nc, &StreamConfig{Name: "F2", Subjects: []string{"fb"}, Storage: FileStorage})
	require_NoError(t, err)

	_, err = jsStreamCreate(t, nc, &StreamConfig{
		Name:     "aggF",
		Subjects: []string{"direct"},
		Storage:  FileStorage,
		Sources: []*StreamSource{
			{Name: "F1", FilterSubject: "fa"}, // distinct subject -> phase 1
			{Name: "F2"},                      // catch-all -> phase 2 reverse scan
		},
	})
	require_NoError(t, err)

	// Fill the front of the store with direct traffic first...
	const direct = 200
	for i := 0; i < direct; i++ {
		_, err := js.Publish("direct", nil)
		require_NoError(t, err)
	}
	checkFor(t, 10*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("aggF")
		if err != nil {
			return err
		}
		if si.State.Msgs != direct {
			return fmt.Errorf("waiting for direct: have %d want %d", si.State.Msgs, direct)
		}
		return nil
	})

	// ...then source a few messages that land after it and stay retained.
	const f1n, f2n = 4, 3
	for i := 0; i < f1n; i++ {
		_, err := js.Publish("fa", nil)
		require_NoError(t, err)
	}
	for i := 0; i < f2n; i++ {
		_, err := js.Publish("fb", nil)
		require_NoError(t, err)
	}
	want := uint64(direct + f1n + f2n)
	checkFor(t, 15*time.Second, 100*time.Millisecond, func() error {
		si, err := js.StreamInfo("aggF")
		if err != nil {
			return err
		}
		if si.State.Msgs != want {
			return fmt.Errorf("waiting for sourcing: have %d want %d", si.State.Msgs, want)
		}
		return nil
	})

	// Advance FirstSeq past the direct prefix (models age/limits expiry), keeping
	// only the sourced tail (seqs direct+1 .. want).
	require_NoError(t, js.PurgeStream("aggF", &nats.StreamPurgeRequest{Sequence: uint64(direct + 1)}))

	mset, err := s.globalAccount().lookupStream("aggF")
	require_NoError(t, err)

	var state StreamState
	mset.store.FastState(&state)
	if state.FirstSeq <= 1 {
		t.Fatalf("expected FirstSeq to have advanced past 1, got %d", state.FirstSeq)
	}

	mset.mu.Lock()
	mset.startingSequenceForSources()
	got := map[string]uint64{}
	for _, si := range mset.sources {
		got[si.name] = si.sseq
	}
	mset.mu.Unlock()

	if got["F1"] != f1n || got["F2"] != f2n {
		t.Fatalf("resume with FirstSeq=%d: F1=%d (want %d) F2=%d (want %d)", state.FirstSeq, got["F1"], f1n, got["F2"], f2n)
	}
}

// Round-trips the sources-snapshot envelope codec (option E) and confirms the
// three snapshot encodings are self-identifying.
func TestSourcesSnapshotEnvelopeRoundTrip(t *testing.T) {
	state := []byte{streamStateMagic, streamStateVersion, 7, 8, 9} // stand-in stream state
	seqs := map[string]uint64{"A a >": 4, "B b >": 99, "C c >": 1}

	wrapped := wrapSourcesSnapshot(state, seqs)
	if !isSourcesSnapshot(wrapped) {
		t.Fatalf("wrapped snapshot not detected as an envelope")
	}
	if isSourcesSnapshot(state) {
		t.Fatalf("plain stream state misdetected as an envelope")
	}

	gotState, gotSeqs, ok := unwrapSourcesSnapshot(wrapped)
	if !ok {
		t.Fatalf("failed to unwrap envelope")
	}
	if string(gotState) != string(state) {
		t.Fatalf("embedded state mismatch: got %v want %v", gotState, state)
	}
	if len(gotSeqs) != len(seqs) {
		t.Fatalf("map size mismatch: got %d want %d", len(gotSeqs), len(seqs))
	}
	for k, v := range seqs {
		if gotSeqs[k] != v {
			t.Fatalf("map[%q]=%d want %d", k, gotSeqs[k], v)
		}
	}

	// A plain (non-enveloped) state must report ok=false so the caller treats it verbatim.
	if _, _, ok := unwrapSourcesSnapshot(state); ok {
		t.Fatalf("plain state unexpectedly unwrapped as an envelope")
	}

	// Empty map round-trips.
	w2 := wrapSourcesSnapshot(state, nil)
	gs2, gm2, ok2 := unwrapSourcesSnapshot(w2)
	if !ok2 || string(gs2) != string(state) || len(gm2) != 0 {
		t.Fatalf("empty-map round-trip failed: ok=%v state=%v len=%d", ok2, gs2, len(gm2))
	}
}
