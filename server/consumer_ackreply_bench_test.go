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
	"testing"
)

// ackReplyInfoManualLoop is the previous implementation that uses a manual
// byte-by-byte loop to tokenize the subject. Kept here for benchmark comparison
// against the current strings.IndexByte-based implementation.
func ackReplyInfoManualLoop(subject string) (sseq, dseq, dc uint64) {
	tsa := [expectedNumReplyTokens]string{}
	start, tokens := 0, tsa[:0]
	for i := 0; i < len(subject); i++ {
		if subject[i] == btsep {
			tokens = append(tokens, subject[start:i])
			start = i + 1
		}
	}
	tokens = append(tokens, subject[start:])
	if len(tokens) != expectedNumReplyTokens || tokens[0] != "$JS" || tokens[1] != "ACK" {
		return 0, 0, 0
	}
	dc = uint64(parseAckReplyNum(tokens[4]))
	sseq, dseq = uint64(parseAckReplyNum(tokens[5])), uint64(parseAckReplyNum(tokens[6]))

	return sseq, dseq, dc
}

// replyInfoManualLoop is the previous implementation that uses a manual
// byte-by-byte loop. Kept for benchmark comparison.
func replyInfoManualLoop(subject string) (sseq, dseq, dc uint64, ts int64, pending uint64) {
	tsa := [expectedNumReplyTokens]string{}
	start, tokens := 0, tsa[:0]
	for i := 0; i < len(subject); i++ {
		if subject[i] == btsep {
			tokens = append(tokens, subject[start:i])
			start = i + 1
		}
	}
	tokens = append(tokens, subject[start:])
	if len(tokens) != expectedNumReplyTokens || tokens[0] != "$JS" || tokens[1] != "ACK" {
		return 0, 0, 0, 0, 0
	}
	dc = uint64(parseAckReplyNum(tokens[4]))
	sseq, dseq = uint64(parseAckReplyNum(tokens[5])), uint64(parseAckReplyNum(tokens[6]))
	ts = parseAckReplyNum(tokens[7])
	pending = uint64(parseAckReplyNum(tokens[8]))

	return sseq, dseq, dc, ts, pending
}

var sampleAckReply = "$JS.ACK.asdf-asdf-events.asdf-asdf-events.1.284222.291929.1671900992627312000.0"

func BenchmarkAckReplyInfo(b *testing.B) {
	b.Run("ManualLoop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sseq, dseq, dc := ackReplyInfoManualLoop(sampleAckReply)
			if sseq == 0 || dseq == 0 || dc == 0 {
				b.Fatal("unexpected zero result")
			}
		}
	})
	b.Run("IndexByte", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sseq, dseq, dc := ackReplyInfo(sampleAckReply)
			if sseq == 0 || dseq == 0 || dc == 0 {
				b.Fatal("unexpected zero result")
			}
		}
	})
}

func BenchmarkReplyInfo(b *testing.B) {
	b.Run("ManualLoop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sseq, dseq, dc, ts, pending := replyInfoManualLoop(sampleAckReply)
			if sseq == 0 || dseq == 0 || dc == 0 || ts == 0 || pending != 0 {
				b.Fatal("unexpected result")
			}
		}
	})
	b.Run("IndexByte", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sseq, dseq, dc, ts, pending := replyInfo(sampleAckReply)
			if sseq == 0 || dseq == 0 || dc == 0 || ts == 0 || pending != 0 {
				b.Fatal("unexpected result")
			}
		}
	})
}

func TestAckReplyInfoIndexByte(t *testing.T) {
	// Verify both implementations return the same results.
	sseq1, dseq1, dc1 := ackReplyInfoManualLoop(sampleAckReply)
	sseq2, dseq2, dc2 := ackReplyInfo(sampleAckReply)
	if sseq1 != sseq2 || dseq1 != dseq2 || dc1 != dc2 {
		t.Fatalf("ackReplyInfo mismatch: manual(%d,%d,%d) vs indexbyte(%d,%d,%d)",
			sseq1, dseq1, dc1, sseq2, dseq2, dc2)
	}

	sseq3, dseq3, dc3, ts1, pending1 := replyInfoManualLoop(sampleAckReply)
	sseq4, dseq4, dc4, ts2, pending2 := replyInfo(sampleAckReply)
	if sseq3 != sseq4 || dseq3 != dseq4 || dc3 != dc4 || ts1 != ts2 || pending1 != pending2 {
		t.Fatalf("replyInfo mismatch: manual(%d,%d,%d,%d,%d) vs indexbyte(%d,%d,%d,%d,%d)",
			sseq3, dseq3, dc3, ts1, pending1, sseq4, dseq4, dc4, ts2, pending2)
	}

	// Test with invalid subjects.
	for _, bad := range []string{"", "foo.bar", "$JS.NACK.a.b.1.2.3.4.5"} {
		s1, d1, c1 := ackReplyInfoManualLoop(bad)
		s2, d2, c2 := ackReplyInfo(bad)
		if s1 != s2 || d1 != d2 || c1 != c2 {
			t.Fatalf("mismatch on bad input %q: manual(%d,%d,%d) vs indexbyte(%d,%d,%d)",
				bad, s1, d1, c1, s2, d2, c2)
		}
	}
}
