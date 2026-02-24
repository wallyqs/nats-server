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
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Correctness tests — verify SIMD dispatch matches scalar for all functions.
// ---------------------------------------------------------------------------

func TestSIMDTokenizeSubjectIntoSlice(t *testing.T) {
	cases := []struct {
		name    string
		subject string
	}{
		{"empty", ""},
		{"single", "foo"},
		{"two", "foo.bar"},
		{"three", "foo.bar.baz"},
		{"trailing_dot", "foo.bar."},
		{"leading_dot", ".foo.bar"},
		{"only_dots", "..."},
		{"long_subject", "a.bb.ccc.dddd.eeeee.ffffff.ggggggg.hhhhhhhh"},
		{"no_dots_16", "abcdefghijklmnop"},
		{"dots_every_other_16", "a.b.c.d.e.f.g.h."},
		{"exactly_16", "abcdefghijklmno."},
		{"17_chars", "abcdefghijklmnop."},
		{"32_tokens", strings.Repeat("a.", 31) + "a"},
		{"very_long", strings.Repeat("token.", 50) + "last"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenizeSubjectIntoSlice(nil, tc.subject)
			gotScalar := tokenizeSubjectIntoSliceScalar(nil, tc.subject)
			if len(got) != len(gotScalar) {
				t.Fatalf("tokenizeSubjectIntoSlice(%q): len=%d, scalar len=%d", tc.subject, len(got), len(gotScalar))
			}
			for i := range got {
				if got[i] != gotScalar[i] {
					t.Fatalf("tokenizeSubjectIntoSlice(%q)[%d] = %q, scalar = %q", tc.subject, i, got[i], gotScalar[i])
				}
			}
		})
	}
}

func TestSIMDSubjectIsLiteral(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		want    bool
	}{
		{"empty", "", true},
		{"literal", "foo.bar.baz", true},
		{"star_middle", "foo.*.baz", false},
		{"star_start", "*.bar.baz", false},
		{"star_end", "foo.bar.*", false},
		{"gt_end", "foo.bar.>", false},
		{"gt_start", ">.bar.baz", false},
		{"star_alone", "*", false},
		{"gt_alone", ">", false},
		{"embedded_star", "foo.b*r.baz", true},   // not at token boundary
		{"embedded_gt", "foo.b>r.baz", true},      // not at token boundary
		{"long_with_wildcard", strings.Repeat("a.", 20) + "*", false},
		{"long_literal", strings.Repeat("a.", 20) + "b", true},
		{"long_no_dots_16", "abcdefghijklmnop", true},
		{"wildcard_at_16_boundary", "abcdefghijklmno.*", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subjectIsLiteral(tc.subject)
			gotScalar := subjectIsLiteralScalar(tc.subject)
			if got != tc.want {
				t.Fatalf("subjectIsLiteral(%q) = %v, want %v", tc.subject, got, tc.want)
			}
			if got != gotScalar {
				t.Fatalf("subjectIsLiteral(%q) = %v, scalar = %v — mismatch", tc.subject, got, gotScalar)
			}
		})
	}
}

func TestSIMDSubjectHasWildcard(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		want    bool
	}{
		{"empty", "", false},
		{"literal", "foo.bar.baz", false},
		{"star_middle", "foo.*.baz", true},
		{"star_start", "*.bar.baz", true},
		{"star_end", "foo.bar.*", true},
		{"gt_end", "foo.bar.>", true},
		{"gt_start", ">.bar.baz", true},
		{"star_alone", "*", true},
		{"gt_alone", ">", true},
		{"embedded_star", "foo.b*r.baz", false},
		{"embedded_gt", "foo.b>r.baz", false},
		{"long_with_wildcard", strings.Repeat("a.", 20) + "*", true},
		{"long_literal", strings.Repeat("a.", 20) + "b", false},
		{"long_no_dots_16", "abcdefghijklmnop", false},
		{"wildcard_at_16_boundary", "abcdefghijklmno.*", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subjectHasWildcard(tc.subject)
			gotScalar := subjectHasWildcardScalar(tc.subject)
			if got != tc.want {
				t.Fatalf("subjectHasWildcard(%q) = %v, want %v", tc.subject, got, tc.want)
			}
			if got != gotScalar {
				t.Fatalf("subjectHasWildcard(%q) = %v, scalar = %v — mismatch", tc.subject, got, gotScalar)
			}
		})
	}
}

func TestSIMDSubjectIsValid(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		want    bool
	}{
		// Invalid: empty, leading/trailing/consecutive dots.
		{"empty", "", false},
		{"dot_only", ".", false},
		{"leading_dot", ".foo", false},
		{"trailing_dot", "foo.", false},
		{"consecutive_dots", "foo..bar", false},
		{"triple_dots", "foo...bar", false},
		// Invalid: '>' wildcard not at end.
		{"gt_not_last", ">.bar", false},
		{"gt_mid", "foo.>.bar", false},
		// Invalid: whitespace in tokens.
		{"space_single", "foo. .bar", false},
		{"tab_single", "foo.\t.bar", false},
		{"space_multi", "foo.ba r.baz", false},
		{"tab_multi", "foo.ba\tr.baz", false},
		{"newline_multi", "foo.ba\nr.baz", false},
		{"cr_multi", "foo.ba\rr.baz", false},
		{"ff_multi", "foo.ba\fr.baz", false},
		// Valid: simple subjects.
		{"single_token", "foo", true},
		{"two_tokens", "foo.bar", true},
		{"three_tokens", "foo.bar.baz", true},
		// Valid: wildcards.
		{"star_alone", "*", true},
		{"gt_alone", ">", true},
		{"star_end", "foo.bar.*", true},
		{"gt_end", "foo.bar.>", true},
		{"star_mid", "foo.*.baz", true},
		// Valid: embedded wildcard chars (not at token boundaries).
		{"embedded_star", "foo*", true},
		{"embedded_star2", "foo*bar", true},
		{"embedded_gt", "foo>", true},
		{"embedded_gt2", "foo>bar", true},
		{"double_star_token", "foo.**", true},
		{"double_gt_token", "foo.>>", true},
		// Long subjects exercising SIMD path.
		{"long_valid", "NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH.ABCDEFGHIJKLM", true},
		{"long_with_gt_end", "NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.>", true},
		{"long_with_star", "NATS0.ABCDEFGHIJKLMNOPQRSTUV.*.ABCDEFGH", true},
		{"long_consecutive_dots", "NATS0.ABCDEFGHIJKLMNOPQRSTUV..ABCDEFGH", false},
		{"long_with_space", "NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH IJKLM.ABCDEFGH", false},
		{"long_gt_not_last", "NATS0.ABCDEFGHIJKLMNOPQRSTUV.>.ABCDEFGH", false},
		// Boundary: exactly 16 bytes, 17 bytes, 15 bytes.
		{"exactly_16", "abcdefghijklmno.", false},   // trailing dot
		{"exactly_16_valid", "abcde.ghijklmnop", true},
		{"exactly_17", "abcde.ghijklmnopq", true},
		{"15_bytes", "abcde.ghijklmno", true},
		// Cross-chunk boundary: consecutive dots at positions 15-16.
		{"dots_at_chunk_boundary", "abcdefghijklmno..qrstuvwxyz012345", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subjectIsValid(tc.subject)
			gotScalar := subjectIsValidScalar(tc.subject)
			if got != tc.want {
				t.Fatalf("subjectIsValid(%q) = %v, want %v", tc.subject, got, tc.want)
			}
			if got != gotScalar {
				t.Fatalf("subjectIsValid(%q) = %v, scalar = %v — mismatch", tc.subject, got, gotScalar)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Benchmarks — compare SIMD-dispatched vs scalar for various subject lengths.
// ---------------------------------------------------------------------------

var simdBenchSubjects = []string{
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV",
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH",
	"$JS.ACK.asdf-asdf-events.asdf-asdf-events.1.284222.291929.1671900992627312000.0",
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH.ABCDEFGHIJKLM",
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH.ABCDEFGHIJKLM.ABCDEFGHIJ",
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH.ABCDEFGHIJKLM.ABCDEFGHIJ.NATS0",
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH.ABCDEFGHIJKLM.ABCDEFGHIJ.NATS0.ABCDEFGH",
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH.ABCDEFGHIJKLM.ABCDEFGHIJ.NATS0.ABCDEFGH.ABCDEFGHIJKL",
	"NATS0.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGHIJKLMNOPQRSTUV.ABCDEFGH.ABCDEFGHIJKLM.ABCDEFGHIJ.NATS0.ABCDEFGH.ABCDEFGHIJKL.ABCDEFGHIJKLMNOPQRSTU",
}

// --- tokenizeSubjectIntoSlice ---

func BenchmarkSIMDTokenize(b *testing.B) {
	for _, subj := range simdBenchSubjects {
		label := fmt.Sprintf("len=%d/tokens=%d", len(subj), len(strings.Split(subj, ".")))
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			tsa := [32]string{}
			b.ReportAllocs()
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				tokenizeSubjectIntoSlice(tsa[:0], subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			tsa := [32]string{}
			b.ReportAllocs()
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				tokenizeSubjectIntoSliceScalar(tsa[:0], subj)
			}
		})
	}
}

// --- subjectIsLiteral ---

func BenchmarkSIMDIsLiteral(b *testing.B) {
	for _, subj := range simdBenchSubjects {
		label := fmt.Sprintf("len=%d/tokens=%d", len(subj), len(strings.Split(subj, ".")))
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				subjectIsLiteral(subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				subjectIsLiteralScalar(subj)
			}
		})
	}
}

// --- subjectHasWildcard ---

func BenchmarkSIMDHasWildcard(b *testing.B) {
	for _, subj := range simdBenchSubjects {
		label := fmt.Sprintf("len=%d/tokens=%d", len(subj), len(strings.Split(subj, ".")))
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				subjectHasWildcard(subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				subjectHasWildcardScalar(subj)
			}
		})
	}
}

// --- subjectIsValid ---

func BenchmarkSIMDIsValid(b *testing.B) {
	for _, subj := range simdBenchSubjects {
		label := fmt.Sprintf("len=%d/tokens=%d", len(subj), len(strings.Split(subj, ".")))
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				subjectIsValid(subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			b.SetBytes(int64(len(subj)))
			for i := 0; i < b.N; i++ {
				subjectIsValidScalar(subj)
			}
		})
	}
}
