// Copyright 2024 The NATS Authors
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

// ackReplyInfoIndexByte uses strings.IndexByte to tokenize the subject.
// Kept here for benchmark comparison against the manual loop in production.
func ackReplyInfoIndexByte(subject string) (sseq, dseq, dc uint64) {
	tsa := [expectedNumReplyTokens]string{}
	tokens := tsa[:0]
	for {
		idx := strings.IndexByte(subject, btsep)
		if idx < 0 {
			tokens = append(tokens, subject)
			break
		}
		tokens = append(tokens, subject[:idx])
		subject = subject[idx+1:]
	}
	if len(tokens) != expectedNumReplyTokens || tokens[0] != "$JS" || tokens[1] != "ACK" {
		return 0, 0, 0
	}
	dc = uint64(parseAckReplyNum(tokens[4]))
	sseq, dseq = uint64(parseAckReplyNum(tokens[5])), uint64(parseAckReplyNum(tokens[6]))

	return sseq, dseq, dc
}

// replyInfoIndexByte uses strings.IndexByte to tokenize the subject.
// Kept for benchmark comparison.
func replyInfoIndexByte(subject string) (sseq, dseq, dc uint64, ts int64, pending uint64) {
	tsa := [expectedNumReplyTokens]string{}
	tokens := tsa[:0]
	for {
		idx := strings.IndexByte(subject, btsep)
		if idx < 0 {
			tokens = append(tokens, subject)
			break
		}
		tokens = append(tokens, subject[:idx])
		subject = subject[idx+1:]
	}
	if len(tokens) != expectedNumReplyTokens || tokens[0] != "$JS" || tokens[1] != "ACK" {
		return 0, 0, 0, 0, 0
	}
	dc = uint64(parseAckReplyNum(tokens[4]))
	sseq, dseq = uint64(parseAckReplyNum(tokens[5])), uint64(parseAckReplyNum(tokens[6]))
	ts = parseAckReplyNum(tokens[7])
	pending = uint64(parseAckReplyNum(tokens[8]))

	return sseq, dseq, dc, ts, pending
}

var (
	// Short names (~80 bytes total).
	sampleAckReplyShort = "$JS.ACK.asdf-asdf-events.asdf-asdf-events.1.284222.291929.1671900992627312000.0"

	// Long consumer name (~200+ bytes total) to simulate realistic production subjects
	// where consumer names can be 114-195 characters.
	sampleAckReplyLong = "$JS.ACK.orders-stream-production." +
		"durable-consumer-orders-processing-pipeline-us-east-1-partition-42-group-alpha-with-retry-policy-exponential-backoff-v2" +
		".1.284222.291929.1671900992627312000.0"
)

func BenchmarkAckReplyInfo(b *testing.B) {
	for _, tc := range []struct {
		name    string
		subject string
	}{
		{"Short", sampleAckReplyShort},
		{"Long", sampleAckReplyLong},
	} {
		b.Run(tc.name+"/ManualLoop", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sseq, dseq, dc := ackReplyInfo(tc.subject)
				if sseq == 0 || dseq == 0 || dc == 0 {
					b.Fatal("unexpected zero result")
				}
			}
		})
		b.Run(tc.name+"/IndexByte", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sseq, dseq, dc := ackReplyInfoIndexByte(tc.subject)
				if sseq == 0 || dseq == 0 || dc == 0 {
					b.Fatal("unexpected zero result")
				}
			}
		})
	}
}

func BenchmarkReplyInfo(b *testing.B) {
	for _, tc := range []struct {
		name    string
		subject string
	}{
		{"Short", sampleAckReplyShort},
		{"Long", sampleAckReplyLong},
	} {
		b.Run(tc.name+"/ManualLoop", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sseq, dseq, dc, ts, pending := replyInfo(tc.subject)
				if sseq == 0 || dseq == 0 || dc == 0 || ts == 0 || pending != 0 {
					b.Fatal("unexpected result")
				}
			}
		})
		b.Run(tc.name+"/IndexByte", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sseq, dseq, dc, ts, pending := replyInfoIndexByte(tc.subject)
				if sseq == 0 || dseq == 0 || dc == 0 || ts == 0 || pending != 0 {
					b.Fatal("unexpected result")
				}
			}
		})
	}
}

func TestAckReplyInfoEquivalence(t *testing.T) {
	for _, subject := range []string{sampleAckReplyShort, sampleAckReplyLong} {
		sseq1, dseq1, dc1 := ackReplyInfo(subject)
		sseq2, dseq2, dc2 := ackReplyInfoIndexByte(subject)
		if sseq1 != sseq2 || dseq1 != dseq2 || dc1 != dc2 {
			t.Fatalf("ackReplyInfo mismatch on %q: manual(%d,%d,%d) vs indexbyte(%d,%d,%d)",
				subject, sseq1, dseq1, dc1, sseq2, dseq2, dc2)
		}

		sseq3, dseq3, dc3, ts1, pending1 := replyInfo(subject)
		sseq4, dseq4, dc4, ts2, pending2 := replyInfoIndexByte(subject)
		if sseq3 != sseq4 || dseq3 != dseq4 || dc3 != dc4 || ts1 != ts2 || pending1 != pending2 {
			t.Fatalf("replyInfo mismatch on %q: manual(%d,%d,%d,%d,%d) vs indexbyte(%d,%d,%d,%d,%d)",
				subject, sseq3, dseq3, dc3, ts1, pending1, sseq4, dseq4, dc4, ts2, pending2)
		}
	}

	// Test with invalid subjects.
	for _, bad := range []string{"", "foo.bar", "$JS.NACK.a.b.1.2.3.4.5"} {
		s1, d1, c1 := ackReplyInfo(bad)
		s2, d2, c2 := ackReplyInfoIndexByte(bad)
		if s1 != s2 || d1 != d2 || c1 != c2 {
			t.Fatalf("mismatch on bad input %q: manual(%d,%d,%d) vs indexbyte(%d,%d,%d)",
				bad, s1, d1, c1, s2, d2, c2)
		}
	}
}
