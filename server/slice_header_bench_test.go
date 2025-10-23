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
	"bytes"
	"testing"
)

// sliceHeaderOld is the OLD implementation that allocates due to string concatenation
func sliceHeaderOld(key string, hdr []byte) []byte {
	if len(hdr) == 0 {
		return nil
	}
	index := bytes.Index(hdr, stringToBytes(key+":")) // BUG: key+":" allocates!
	hdrLen := len(hdr)
	// Check that we have enough characters, this will handle the -1 case of the key not
	// being found and will also handle not having enough characters for trailing CRLF.
	if index < 2 {
		return nil
	}
	// There should be a terminating CRLF.
	if index >= hdrLen-1 || hdr[index-1] != '\n' || hdr[index-2] != '\r' {
		return nil
	}
	// The key should be immediately followed by a : separator.
	index += len(key) + 1
	if index >= hdrLen || hdr[index-1] != ':' {
		return nil
	}
	// Skip over whitespace before the value.
	for index < hdrLen && hdr[index] == ' ' {
		index++
	}
	// Collect together the rest of the value until we hit a CRLF.
	start := index
	for index < hdrLen {
		if hdr[index] == '\r' && index < hdrLen-1 && hdr[index+1] == '\n' {
			break
		}
		index++
	}
	return hdr[start:index:index]
}

// Benchmark the OLD implementation with string concatenation allocation
func BenchmarkSliceHeaderOld(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, JSExpectedStream, "my-stream")
	hdr = genHeader(hdr, JSExpectedLastSeq, "22")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, JSExpectedLastSubjSeq, "24")
	hdr = genHeader(hdr, JSExpectedLastMsgId, "1")
	hdr = genHeader(hdr, "c", "3")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sliceHeaderOld(JSExpectedLastSubjSeq, hdr)
	}
}

// Benchmark the NEW implementation without string concatenation
func BenchmarkSliceHeaderNew(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, JSExpectedStream, "my-stream")
	hdr = genHeader(hdr, JSExpectedLastSeq, "22")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, JSExpectedLastSubjSeq, "24")
	hdr = genHeader(hdr, JSExpectedLastMsgId, "1")
	hdr = genHeader(hdr, "c", "3")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sliceHeader(JSExpectedLastSubjSeq, hdr)
	}
}

// Benchmark with a more realistic scenario - ClientInfoHdr in hot path
func BenchmarkSliceHeaderOld_ClientInfo(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, ClientInfoHdr, `{"server":"test","cluster":"local","domain":"hub"}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sliceHeaderOld(ClientInfoHdr, hdr)
	}
}

func BenchmarkSliceHeaderNew_ClientInfo(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, ClientInfoHdr, `{"server":"test","cluster":"local","domain":"hub"}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sliceHeader(ClientInfoHdr, hdr)
	}
}

// Benchmark worst case - header not found (searches entire buffer)
func BenchmarkSliceHeaderOld_NotFound(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, "c", "3")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sliceHeaderOld("NotFound", hdr)
	}
}

func BenchmarkSliceHeaderNew_NotFound(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, "c", "3")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sliceHeader("NotFound", hdr)
	}
}
