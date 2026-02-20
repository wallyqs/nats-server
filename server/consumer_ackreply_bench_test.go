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

// ackReplyInfoBackward scans from the end of the subject to find numeric token
// boundaries, avoiding traversal of potentially long stream/consumer names.
// The ACK reply format is: $JS.ACK.<stream>.<consumer>.<dc>.<sseq>.<dseq>.<ts>.<pending>
// We only need dc, sseq, dseq — all at the end.
func ackReplyInfoBackward(subject string) (sseq, dseq, dc uint64) {
	if len(subject) < 12 || subject[:4] != "$JS." || subject[4:8] != "ACK." {
		return 0, 0, 0
	}

	// Scan backward to find the last 6 dot positions.
	// For 9 tokens we need 8 dots total. 2 are in "$JS.ACK." prefix.
	// The remaining 6 separate: stream.consumer.dc.sseq.dseq.ts.pending
	var dots [6]int
	n := 0
	for i := len(subject) - 1; i >= 8 && n < 6; i-- {
		if subject[i] == btsep {
			dots[n] = i
			n++
		}
	}
	if n != 6 {
		return 0, 0, 0
	}

	// dots[5] separates stream from consumer.
	// Verify no extra dots between prefix end (pos 8) and dots[5].
	// If dots[5] == 8, stream name is empty — invalid.
	// Verify no extra dots between prefix end (pos 8) and dots[5] (stream.consumer boundary).
	for i := 8; i < dots[5]; i++ {
		if subject[i] == btsep {
			return 0, 0, 0
		}
	}

	// dots layout (right to left):
	// dots[0] = between ts and pending
	// dots[1] = between dseq and ts
	// dots[2] = between sseq and dseq
	// dots[3] = between dc and sseq
	// dots[4] = between consumer and dc
	// dots[5] = between stream and consumer
	dc = uint64(parseAckReplyNum(subject[dots[4]+1 : dots[3]]))
	sseq = uint64(parseAckReplyNum(subject[dots[3]+1 : dots[2]]))
	dseq = uint64(parseAckReplyNum(subject[dots[2]+1 : dots[1]]))

	return sseq, dseq, dc
}

// replyInfoBackward is the backward-scan variant for replyInfo.
func replyInfoBackward(subject string) (sseq, dseq, dc uint64, ts int64, pending uint64) {
	if len(subject) < 12 || subject[:4] != "$JS." || subject[4:8] != "ACK." {
		return 0, 0, 0, 0, 0
	}

	var dots [6]int
	n := 0
	for i := len(subject) - 1; i >= 8 && n < 6; i-- {
		if subject[i] == btsep {
			dots[n] = i
			n++
		}
	}
	if n != 6 {
		return 0, 0, 0, 0, 0
	}

	for i := 8; i < dots[5]; i++ {
		if subject[i] == btsep {
			return 0, 0, 0, 0, 0
		}
	}

	dc = uint64(parseAckReplyNum(subject[dots[4]+1 : dots[3]]))
	sseq = uint64(parseAckReplyNum(subject[dots[3]+1 : dots[2]]))
	dseq = uint64(parseAckReplyNum(subject[dots[2]+1 : dots[1]]))
	ts = parseAckReplyNum(subject[dots[1]+1 : dots[0]])
	pending = uint64(parseAckReplyNum(subject[dots[0]+1:]))

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
		b.Run(tc.name+"/Backward", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sseq, dseq, dc := ackReplyInfoBackward(tc.subject)
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
		b.Run(tc.name+"/Backward", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sseq, dseq, dc, ts, pending := replyInfoBackward(tc.subject)
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
		sseq3, dseq3, dc3 := ackReplyInfoBackward(subject)
		if sseq1 != sseq2 || dseq1 != dseq2 || dc1 != dc2 {
			t.Fatalf("ackReplyInfo mismatch on %q: manual(%d,%d,%d) vs indexbyte(%d,%d,%d)",
				subject, sseq1, dseq1, dc1, sseq2, dseq2, dc2)
		}
		if sseq1 != sseq3 || dseq1 != dseq3 || dc1 != dc3 {
			t.Fatalf("ackReplyInfo mismatch on %q: manual(%d,%d,%d) vs backward(%d,%d,%d)",
				subject, sseq1, dseq1, dc1, sseq3, dseq3, dc3)
		}

		rsseq1, rdseq1, rdc1, rts1, rpend1 := replyInfo(subject)
		rsseq2, rdseq2, rdc2, rts2, rpend2 := replyInfoIndexByte(subject)
		rsseq3, rdseq3, rdc3, rts3, rpend3 := replyInfoBackward(subject)
		if rsseq1 != rsseq2 || rdseq1 != rdseq2 || rdc1 != rdc2 || rts1 != rts2 || rpend1 != rpend2 {
			t.Fatalf("replyInfo mismatch on %q: manual vs indexbyte", subject)
		}
		if rsseq1 != rsseq3 || rdseq1 != rdseq3 || rdc1 != rdc3 || rts1 != rts3 || rpend1 != rpend3 {
			t.Fatalf("replyInfo mismatch on %q: manual vs backward", subject)
		}
	}

	// Test with invalid subjects.
	for _, bad := range []string{
		"",
		"foo.bar",
		"$JS.NACK.a.b.1.2.3.4.5",
		"$JS.ACK.a.1.2.3.4.5",         // only 8 tokens
		"$JS.ACK.a.b.c.1.2.3.4.5",     // 10 tokens (extra dot in stream.consumer area)
		"$JS.ACK..consumer.1.2.3.4.5",  // empty stream name
		"$JS.ACK.stream..1.2.3.4.5",    // empty consumer name
	} {
		s1, d1, c1 := ackReplyInfo(bad)
		s2, d2, c2 := ackReplyInfoIndexByte(bad)
		s3, d3, c3 := ackReplyInfoBackward(bad)
		if s1 != s2 || d1 != d2 || c1 != c2 {
			t.Fatalf("mismatch on bad input %q: manual(%d,%d,%d) vs indexbyte(%d,%d,%d)",
				bad, s1, d1, c1, s2, d2, c2)
		}
		if s1 != s3 || d1 != d3 || c1 != c3 {
			t.Fatalf("mismatch on bad input %q: manual(%d,%d,%d) vs backward(%d,%d,%d)",
				bad, s1, d1, c1, s3, d3, c3)
		}
	}
}
