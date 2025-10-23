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
	if index < 2 {
		return nil
	}
	if index >= hdrLen-1 || hdr[index-1] != '\n' || hdr[index-2] != '\r' {
		return nil
	}
	index += len(key) + 1
	if index >= hdrLen || hdr[index-1] != ':' {
		return nil
	}
	for index < hdrLen && hdr[index] == ' ' {
		index++
	}
	start := index
	for index < hdrLen {
		if hdr[index] == '\r' && index < hdrLen-1 && hdr[index+1] == '\n' {
			break
		}
		index++
	}
	return hdr[start:index:index]
}

// setHeaderOld is the OLD implementation that allocates
func setHeaderOld(key, val string, hdr []byte) []byte {
	prefix := []byte(key + ": ") // BUG: allocates!
	start := bytes.Index(hdr, prefix)
	if start >= 0 {
		valStart := start + len(prefix)
		valEnd := bytes.Index(hdr[valStart:], []byte("\r"))
		if valEnd < 0 {
			return hdr
		}
		valEnd += valStart
		suffix := make([]byte, len(hdr[valEnd:]))
		copy(suffix, hdr[valEnd:])
		newHdr := append(hdr[:valStart], val...)
		return append(newHdr, suffix...)
	}
	if len(hdr) > 0 && bytes.HasSuffix(hdr, []byte("\r\n")) {
		hdr = hdr[:len(hdr)-2]
		val += "\r\n"
	}
	hdr = append(hdr, []byte(key+": "+val+"\r\n")...)
	return hdr
}

// removeHeaderIfPresentOld is the OLD implementation that allocates
func removeHeaderIfPresentOld(hdr []byte, key string) []byte {
	start := bytes.Index(hdr, []byte(key+":")) // BUG: allocates!
	if start < 1 || hdr[start-1] != '\n' {
		return hdr
	}
	index := start + len(key)
	if index >= len(hdr) || hdr[index] != ':' {
		return hdr
	}
	end := bytes.Index(hdr[start:], []byte(_CRLF_))
	if end < 0 {
		return hdr
	}
	hdr = append(hdr[:start], hdr[start+end+len(_CRLF_):]...)
	if len(hdr) <= len(emptyHdrLine) {
		return nil
	}
	return hdr
}

// Benchmark sliceHeader - standard case
func BenchmarkSliceHeader(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, JSExpectedStream, "my-stream")
	hdr = genHeader(hdr, JSExpectedLastSeq, "22")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, JSExpectedLastSubjSeq, "24")
	hdr = genHeader(hdr, JSExpectedLastMsgId, "1")
	hdr = genHeader(hdr, "c", "3")

	b.Run("Old", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeaderOld(JSExpectedLastSubjSeq, hdr)
		}
	})

	b.Run("New", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeader(JSExpectedLastSubjSeq, hdr)
		}
	})
}

// Benchmark sliceHeader with ClientInfo (hot path in production)
func BenchmarkSliceHeaderClientInfo(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, ClientInfoHdr, `{"server":"nats-server","cluster":"local","domain":"hub"}`)

	b.Run("Old", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeaderOld(ClientInfoHdr, hdr)
		}
	})

	b.Run("New", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeader(ClientInfoHdr, hdr)
		}
	})
}

// Benchmark sliceHeader - not found (worst case)
func BenchmarkSliceHeaderNotFound(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, "c", "3")

	b.Run("Old", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeaderOld("NotFound", hdr)
		}
	})

	b.Run("New", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeader("NotFound", hdr)
		}
	})
}

// Benchmark sliceHeader - prefix collision (tests loop iterations)
func BenchmarkSliceHeaderPrefixCollision(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, JSExpectedLastSubjSeqSubj, "foo")
	hdr = genHeader(hdr, JSExpectedLastSubjSeq, "24")

	b.Run("Old", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeaderOld(JSExpectedLastSubjSeq, hdr)
		}
	})

	b.Run("New", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sliceHeader(JSExpectedLastSubjSeq, hdr)
		}
	})
}

// Benchmark setHeader
func BenchmarkSetHeader(b *testing.B) {
	b.Run("Old", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			hdr := []byte("NATS/1.0\r\n\r\n")
			hdr = genHeader(hdr, JSExpectedLastSeq, "22")
			hdr = setHeaderOld(JSExpectedLastSeq, "42", hdr)
		}
	})

	b.Run("New", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			hdr := []byte("NATS/1.0\r\n\r\n")
			hdr = genHeader(hdr, JSExpectedLastSeq, "22")
			hdr = setHeader(JSExpectedLastSeq, "42", hdr)
		}
	})
}

// Benchmark removeHeaderIfPresent
func BenchmarkRemoveHeaderIfPresent(b *testing.B) {
	b.Run("Old", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			hdr := []byte("NATS/1.0\r\n\r\n")
			hdr = genHeader(hdr, "a", "1")
			hdr = genHeader(hdr, JSExpectedLastSeq, "22")
			hdr = genHeader(hdr, "b", "2")
			hdr = removeHeaderIfPresentOld(hdr, JSExpectedLastSeq)
		}
	})

	b.Run("New", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			hdr := []byte("NATS/1.0\r\n\r\n")
			hdr = genHeader(hdr, "a", "1")
			hdr = genHeader(hdr, JSExpectedLastSeq, "22")
			hdr = genHeader(hdr, "b", "2")
			hdr = removeHeaderIfPresent(hdr, JSExpectedLastSeq)
		}
	})
}

// Benchmark getHeaderKeyIndex helper (used by all three functions)
func BenchmarkGetHeaderKeyIndex(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, JSExpectedStream, "my-stream")
	hdr = genHeader(hdr, JSExpectedLastSeq, "22")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, JSExpectedLastSubjSeq, "24")

	b.Run("Found", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = getHeaderKeyIndex(JSExpectedLastSubjSeq, hdr)
		}
	})

	b.Run("NotFound", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = getHeaderKeyIndex("NotFound", hdr)
		}
	})

	b.Run("PrefixCollision", func(b *testing.B) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, JSExpectedLastSubjSeqSubj, "foo")
		hdr = genHeader(hdr, JSExpectedLastSubjSeq, "24")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = getHeaderKeyIndex(JSExpectedLastSubjSeq, hdr)
		}
	})
}
