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

func TestSIMDNumTokens(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		want    int
	}{
		{"empty", "", 0},
		{"single", "foo", 1},
		{"two", "foo.bar", 2},
		{"three", "foo.bar.baz", 3},
		{"trailing_dot", "foo.bar.", 3},
		{"leading_dot", ".foo.bar", 3},
		{"only_dots", "...", 4},
		{"long_subject", "a.bb.ccc.dddd.eeeee.ffffff.ggggggg.hhhhhhhh", 8},
		{"no_dots_16", "abcdefghijklmnop", 1},
		{"dots_every_other_16", "a.b.c.d.e.f.g.h.", 9},
		{"exactly_16", "abcdefghijklmno.", 2},
		{"17_chars", "abcdefghijklmnop.", 2},
		{"32_tokens", strings.Repeat("a.", 31) + "a", 32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := numTokens(tc.subject)
			gotScalar := numTokensScalar(tc.subject)
			if got != tc.want {
				t.Fatalf("numTokens(%q) = %d, want %d", tc.subject, got, tc.want)
			}
			if got != gotScalar {
				t.Fatalf("numTokens(%q) = %d, scalar = %d — mismatch", tc.subject, got, gotScalar)
			}
		})
	}
}

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

// ---------------------------------------------------------------------------
// Benchmarks — compare SIMD-dispatched vs scalar for various subject lengths.
// ---------------------------------------------------------------------------

var benchSubjects = map[string]string{
	"short_10B":    "foo.bar.ba",                                                    // 10 bytes
	"medium_50B":   "account.user.session.token.validate.request.response.status.ok", // ~62 bytes
	"long_120B":    strings.Repeat("segment.", 14) + "last",                         // ~120 bytes
	"verylong_250": strings.Repeat("namespace.", 24) + "final",                      // ~250 bytes
}

// --- numTokens ---

func BenchmarkSIMDNumTokens(b *testing.B) {
	for label, subj := range benchSubjects {
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				numTokens(subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				numTokensScalar(subj)
			}
		})
	}
}

// --- tokenizeSubjectIntoSlice ---

func BenchmarkSIMDTokenize(b *testing.B) {
	for label, subj := range benchSubjects {
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			tts := make([]string, 0, 32)
			for i := 0; i < b.N; i++ {
				tts = tokenizeSubjectIntoSlice(tts[:0], subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			tts := make([]string, 0, 32)
			for i := 0; i < b.N; i++ {
				tts = tokenizeSubjectIntoSliceScalar(tts[:0], subj)
			}
		})
	}
}

// --- subjectIsLiteral ---

func BenchmarkSIMDIsLiteral(b *testing.B) {
	for label, subj := range benchSubjects {
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				subjectIsLiteral(subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				subjectIsLiteralScalar(subj)
			}
		})
	}
}

// --- subjectHasWildcard ---

func BenchmarkSIMDHasWildcard(b *testing.B) {
	for label, subj := range benchSubjects {
		b.Run(fmt.Sprintf("simd/%s", label), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				subjectHasWildcard(subj)
			}
		})
		b.Run(fmt.Sprintf("scalar/%s", label), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				subjectHasWildcardScalar(subj)
			}
		})
	}
}
