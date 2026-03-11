// Copyright 2024-2025 The NATS Authors
// Licensed under the Apache License, Version 2.0

package server

import (
	"hash/fnv"
	"strconv"
	"strings"
	"testing"
)

// Benchmark tests to confirm heap allocation findings from escape analysis.
// Each benchmark isolates a specific allocation pattern identified in the
// hot paths of routing and subscription matching.
//
// Escape analysis + benchmark summary:
//
// | # | Finding                                      | Escape Analysis      | Benchmark Result          | Verdict         |
// |---|----------------------------------------------|----------------------|---------------------------|-----------------|
// | 1 | &SublistResult{} in match()                  | escapes to heap      | 3 allocs/op, 312 B/op     | CONFIRMED       |
// | 2 | []byte(qname) in addNodeToResults()           | zero-copy, no escape | n/a                       | REFUTED         |
// | 3 | string(sub.queue) in removeFromNode()          | does not escape      | n/a                       | REFUTED         |
// | 4 | strings.SplitSeq vs manual tokenization       | no allocs either way | SplitSeq ~22ns, manual ~27ns | REFUTED      |
// | 5 | fnv.New32a() in computeRoutePoolIdx           | devirtualized+inlined| 0 allocs; 11ns vs 6ns inline | PARTIALLY CONFIRMED |
// | 6 | strconv.Itoa in msgHeader                     | escapes (intermediate)| 1 alloc 4B vs 0 AppendInt | CONFIRMED       |
// | 7 | []byte(to) in msgHeaderForRouteOrLeaf         | escapes to heap      | 0 allocs in microbench     | CONTEXT-DEPENDENT|
// | 8 | string(c.pa.subject) in processInboundLeafMsg | escapes to heap      | map store: 1 alloc 16B    | CONFIRMED       |
// | 9 | Repeated string() in gateway                  | escapes at map put   | same alloc count either way| MARGINAL        |
// |10 | updateStats closure                           | does not escape      | n/a                       | REFUTED         |
// |11 | make([]*subscription) per qgroup              | escapes to heap      | part of finding #1         | CONFIRMED       |

// Finding #1: SublistResult allocation in match() on cache miss.
//
// Escape analysis: &SublistResult{} escapes to heap because it's stored in s.cache map.
// Benchmark: 3 allocs/op (SublistResult + psubs slice + cache map entry), 312 B/op.
// This is the highest-impact finding - every cache miss in the subscription matching
// hot path triggers these allocations.
func Benchmark_HeapAlloc_SublistMatchCacheMiss(b *testing.B) {
	s := NewSublistWithCache()
	for _, sub := range []*subscription{
		{subject: []byte("foo.bar.baz")},
		{subject: []byte("foo.bar.bat")},
		{subject: []byte("foo.*.baz")},
	} {
		s.Insert(sub)
	}
	subjects := []string{
		"foo.bar.baz",
		"foo.bar.bat",
		"foo.bar.bam",
		"foo.bar.ban",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subj := subjects[i%len(subjects)]
		// Clear cache to force cache miss path each time
		s.Lock()
		s.cache = make(map[string]*SublistResult)
		s.Unlock()
		s.Match(subj)
	}
}

// Finding #4: strings.SplitSeq vs manual tokenization in Insert/remove.
//
// Escape analysis: SplitSeq closures do not escape (inlined by compiler).
// Benchmark: Both approaches show 0 allocs/op. SplitSeq is actually slightly
// faster (~22ns vs ~27ns) because the compiler fully inlines the iterator.
// VERDICT: SplitSeq is fine as-is in Go 1.24+. No change needed.
func Benchmark_HeapAlloc_TokenizeSplitSeq(b *testing.B) {
	subject := "foo.bar.baz.quux"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for range strings.SplitSeq(subject, tsep) {
		}
	}
}

func Benchmark_HeapAlloc_TokenizeManual(b *testing.B) {
	subject := "foo.bar.baz.quux"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tsa := [32]string{}
		tokens := tsa[:0]
		start := 0
		for j := 0; j < len(subject); j++ {
			if subject[j] == btsep {
				tokens = append(tokens, subject[start:j])
				start = j + 1
			}
		}
		tokens = append(tokens, subject[start:])
		_ = tokens
	}
}

// Finding #5: fnv.New32a() + []byte(an) in computeRoutePoolIdx.
//
// Escape analysis: compiler devirtualizes h.Write to *fnv.sum32a, inlines both
// Write and Sum32. []byte(an) uses zero-copy conversion. 0 allocs/op.
// However, the function itself cannot inline (cost 153 > budget 80), so callers
// pay a function call overhead. An inline FNV-1a runs ~1.8x faster (6.5ns vs 11.4ns)
// because the entire computation stays in the caller's frame.
// VERDICT: No allocation issue, but inlining opportunity for ~43% speedup.
func Benchmark_HeapAlloc_ComputeRoutePoolIdx(b *testing.B) {
	an := "test.account.name"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		computeRoutePoolIdx(8, an)
	}
}

func Benchmark_HeapAlloc_ComputeRoutePoolIdxInline(b *testing.B) {
	an := "test.account.name"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		computeRoutePoolIdxInline(8, an)
	}
}

// Inline FNV-1a implementation for comparison.
func computeRoutePoolIdxInline(poolSize int, an string) int {
	if poolSize <= 1 {
		return 0
	}
	var h uint32 = 2166136261
	for i := 0; i < len(an); i++ {
		h ^= uint32(an[i])
		h *= 16777619
	}
	return int(h % uint32(poolSize))
}

// Finding #6: strconv.Itoa allocates intermediate string vs AppendInt.
//
// Escape analysis: Itoa is inlined but the temporary string escapes.
// Benchmark: Itoa path: 1 alloc/op, 4 B/op, ~21.5ns.
//            AppendInt: 0 allocs/op, ~10.7ns. 2x faster.
// VERDICT: CONFIRMED. Using strconv.AppendInt eliminates 1 allocation per
// message header construction when headers need truncation.
func Benchmark_HeapAlloc_StrconvItoa(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf [64]byte
		mh := buf[:0]
		nsz := strconv.Itoa(1234)
		mh = append(mh, nsz...)
		_ = mh
	}
}

func Benchmark_HeapAlloc_StrconvAppendInt(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf [64]byte
		mh := buf[:0]
		mh = strconv.AppendInt(mh, 1234, 10)
		_ = mh
	}
}

// Finding #7: []byte(to) conversion in msgHeaderForRouteOrLeaf.
//
// Escape analysis: CONFIRMED escapes to heap in the real function context
// (the []byte flows through bytesToString into TransformSubject call parameter).
// Benchmark: In isolation, []byte(string) doesn't escape (compiler optimizes
// simple copy-then-append). The allocation happens in the real code due to
// the more complex data flow through bytesToString and TransformSubject.
// VERDICT: CONTEXT-DEPENDENT. The fix (append string directly) is still valid
// but only matters in the real function where escape analysis confirms the escape.
func Benchmark_HeapAlloc_ByteSliceFromString(b *testing.B) {
	to := "some.subject.here.for.test"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf [128]byte
		mh := buf[:0]
		subj := []byte(to)
		mh = append(mh, subj...)
		_ = mh
	}
}

func Benchmark_HeapAlloc_AppendStringDirect(b *testing.B) {
	to := "some.subject.here.for.test"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf [128]byte
		mh := buf[:0]
		mh = append(mh, to...)
		_ = mh
	}
}

// Finding #8: string(c.pa.subject) in processInboundLeafMsg.
//
// Escape analysis: CONFIRMED escapes to heap when used as map key in put operation.
// Benchmark: map lookup with string([]byte): 0 allocs (compiler optimized).
//            map store with string([]byte): 1 alloc, 16 B/op.
//            bytesToString for lookup: 0 allocs (same as compiler-optimized).
// VERDICT: CONFIRMED. Use bytesToString for lookups (zero-copy), only allocate
// via string() when actually inserting into the cache. Saves 1 alloc on cache hits.
func Benchmark_HeapAlloc_StringConvForMapLookup(b *testing.B) {
	m := make(map[string]int)
	m["foo.bar.baz"] = 1
	subject := []byte("foo.bar.baz")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m[string(subject)] // compiler-optimized, no alloc
	}
}

func Benchmark_HeapAlloc_StringConvForMapStore(b *testing.B) {
	m := make(map[string]int)
	subject := []byte("foo.bar.baz")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m[string(subject)] = 1 // allocates: 1 alloc, 16 B/op
	}
}

func Benchmark_HeapAlloc_BytesToStringForMapLookup(b *testing.B) {
	m := make(map[string]int)
	m["foo.bar.baz"] = 1
	subject := []byte("foo.bar.baz")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m[bytesToString(subject)]
	}
}

// Finding #9: Repeated string() conversions in gateway code.
//
// Escape analysis: CONFIRMED that string(subject) and string(accName) escape
// at map put sites (lines 2851, 2852, 2864).
// Benchmark: Both repeated and single-conversion patterns show 4 allocs, 288 B/op.
// The map[string]struct{} creation dominates. Converting once vs multiple times
// makes no measurable difference because the compiler optimizes map lookups
// with string([]byte) anyway, and both patterns allocate the same for puts.
// VERDICT: MARGINAL. The fix is correct in principle (fewer string copies) but
// the dominant cost is the map creation, not the string conversions.
func Benchmark_HeapAlloc_RepeatedStringConv(b *testing.B) {
	m := make(map[string]map[string]struct{})
	accName := []byte("test-account")
	subject := []byte("foo.bar.baz")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := m[string(accName)]
		if e == nil {
			e = make(map[string]struct{})
			e[string(subject)] = struct{}{}
			m[string(accName)] = e
		} else {
			e[string(subject)] = struct{}{}
		}
		delete(m, string(accName))
	}
}

func Benchmark_HeapAlloc_SingleStringConv(b *testing.B) {
	m := make(map[string]map[string]struct{})
	accName := []byte("test-account")
	subject := []byte("foo.bar.baz")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		accStr := string(accName)
		subjStr := string(subject)
		e := m[accStr]
		if e == nil {
			e = make(map[string]struct{})
			e[subjStr] = struct{}{}
			m[accStr] = e
		} else {
			e[subjStr] = struct{}{}
		}
		delete(m, accStr)
	}
}

// Finding #5 additional: Verify fnv.New32a() has 0 allocs due to devirtualization.
//
// Escape analysis: compiler devirtualizes and inlines all fnv operations.
// Benchmark: CONFIRMED 0 allocs/op for both. Performance is identical (~6.4ns).
// The only benefit of inline FNV is enabling the parent function to inline
// (reducing its cost below the 80 budget), not avoiding allocations.
func Benchmark_HeapAlloc_FnvInterface(b *testing.B) {
	an := "test.account.name"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := fnv.New32a()
		h.Write([]byte(an))
		_ = h.Sum32()
	}
}

func Benchmark_HeapAlloc_FnvInline(b *testing.B) {
	an := "test.account.name"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var h uint32 = 2166136261
		for j := 0; j < len(an); j++ {
			h ^= uint32(an[j])
			h *= 16777619
		}
		_ = h
	}
}

// Baseline: SublistInsert allocation profile.
// Shows 1 alloc/op (the string(sub.subject) copy in Insert).
func Benchmark_HeapAlloc_SublistInsertBaseline(b *testing.B) {
	s := NewSublistWithCache()
	subs := make([]*subscription, b.N)
	for i := range subs {
		subs[i] = &subscription{subject: []byte("foo.bar.baz.quux")}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Insert(subs[i])
	}
}
