// Copyright 2016-2025 The NATS Authors
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
	"strings"
	"testing"
)

// Alternative implementations using strings.IndexByte for comparison.

func tokenAtIndexByte(subject string, index uint8) string {
	ti := uint8(1)
	s := subject
	for ti < index {
		i := strings.IndexByte(s, btsep)
		if i < 0 {
			return _EMPTY_
		}
		s = s[i+1:]
		ti++
	}
	i := strings.IndexByte(s, btsep)
	if i < 0 {
		return s
	}
	return s[:i]
}

func numTokensStringsCount(subject string) int {
	if len(subject) == 0 {
		return 0
	}
	return strings.Count(subject, ".") + 1
}

func numTokensIndexByteLoop(subject string) int {
	if len(subject) == 0 {
		return 0
	}
	count := 1
	s := subject
	for {
		i := strings.IndexByte(s, btsep)
		if i < 0 {
			break
		}
		count++
		s = s[i+1:]
	}
	return count
}

func tokenizeSubjectIntoSliceIndexByte(tts []string, subject string) []string {
	for {
		i := strings.IndexByte(subject, btsep)
		if i < 0 {
			break
		}
		tts = append(tts, subject[:i])
		subject = subject[i+1:]
	}
	tts = append(tts, subject)
	return tts
}

// --- Test subjects ---

var benchSubjects = []struct {
	name    string
	subject string
}{
	{"2tok_short", "foo.bar"},
	{"3tok_medium", "foo.bar.baz"},
	{"4tok_typical", "accounts.user.123.inbox"},
	{"5tok_long", "nats.server.accounts.user.messages"},
	{"8tok_vlong", "org.dept.team.project.service.api.v2.endpoint"},
}

// ==================== tokenAt benchmarks ====================

func BenchmarkTokenAt_ManualLoop(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			nt := uint8(numTokens(tc.subject))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tokenAt(tc.subject, 1)
				tokenAt(tc.subject, nt/2+1)
				tokenAt(tc.subject, nt)
			}
		})
	}
}

func BenchmarkTokenAt_IndexByte(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			nt := uint8(numTokens(tc.subject))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tokenAtIndexByte(tc.subject, 1)
				tokenAtIndexByte(tc.subject, nt/2+1)
				tokenAtIndexByte(tc.subject, nt)
			}
		})
	}
}

func BenchmarkTokenAtLast_ManualLoop(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			nt := uint8(numTokens(tc.subject))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tokenAt(tc.subject, nt)
			}
		})
	}
}

func BenchmarkTokenAtLast_IndexByte(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			nt := uint8(numTokens(tc.subject))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tokenAtIndexByte(tc.subject, nt)
			}
		})
	}
}

func BenchmarkTokenAtFirst_ManualLoop(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tokenAt(tc.subject, 1)
			}
		})
	}
}

func BenchmarkTokenAtFirst_IndexByte(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tokenAtIndexByte(tc.subject, 1)
			}
		})
	}
}

// ==================== numTokens benchmarks ====================

func BenchmarkNumTokens_ManualLoop(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				numTokens(tc.subject)
			}
		})
	}
}

func BenchmarkNumTokens_StringsCount(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				numTokensStringsCount(tc.subject)
			}
		})
	}
}

func BenchmarkNumTokens_IndexByteLoop(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				numTokensIndexByteLoop(tc.subject)
			}
		})
	}
}

// ==================== tokenizeSubjectIntoSlice benchmarks ====================

func BenchmarkTokenizeSlice_ManualLoop(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			tts := make([]string, 0, 16)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tts = tokenizeSubjectIntoSlice(tts[:0], tc.subject)
			}
		})
	}
}

func BenchmarkTokenizeSlice_IndexByte(b *testing.B) {
	for _, tc := range benchSubjects {
		b.Run(tc.name, func(b *testing.B) {
			tts := make([]string, 0, 16)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tts = tokenizeSubjectIntoSliceIndexByte(tts[:0], tc.subject)
			}
		})
	}
}

// ==================== Correctness tests ====================

func TestTokenAtIndexByte_Correctness(t *testing.T) {
	subjects := []string{
		"foo.bar",
		"foo.bar.baz",
		"a.b.c.d.e",
		"single",
		"accounts.user.123.inbox",
		"nats.server.accounts.user.messages",
		"org.dept.team.project.service.api.v2.endpoint",
	}
	for _, subj := range subjects {
		nt := uint8(numTokens(subj))
		for idx := uint8(1); idx <= nt; idx++ {
			got := tokenAtIndexByte(subj, idx)
			want := tokenAt(subj, idx)
			if got != want {
				t.Errorf("tokenAtIndexByte(%q, %d) = %q, want %q", subj, idx, got, want)
			}
		}
		got := tokenAtIndexByte(subj, nt+1)
		want := tokenAt(subj, nt+1)
		if got != want {
			t.Errorf("out of range: tokenAtIndexByte(%q, %d) = %q, want %q", subj, nt+1, got, want)
		}
	}
}

func TestNumTokensIndexByte_Correctness(t *testing.T) {
	subjects := []string{"", "foo", "foo.bar", "foo.bar.baz", "a.b.c.d.e.f.g.h"}
	for _, subj := range subjects {
		got1 := numTokensStringsCount(subj)
		got2 := numTokensIndexByteLoop(subj)
		want := numTokens(subj)
		if got1 != want {
			t.Errorf("numTokensStringsCount(%q) = %d, want %d", subj, got1, want)
		}
		if got2 != want {
			t.Errorf("numTokensIndexByteLoop(%q) = %d, want %d", subj, got2, want)
		}
	}
}

func TestTokenizeSliceIndexByte_Correctness(t *testing.T) {
	subjects := []string{"foo.bar", "foo.bar.baz", "a.b.c.d.e", "single"}
	for _, subj := range subjects {
		got := tokenizeSubjectIntoSliceIndexByte(nil, subj)
		want := tokenizeSubjectIntoSlice(nil, subj)
		if len(got) != len(want) {
			t.Errorf("len mismatch for %q: got %d, want %d", subj, len(got), len(want))
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("tokenizeSlice(%q)[%d] = %q, want %q", subj, i, got[i], want[i])
			}
		}
	}
}
