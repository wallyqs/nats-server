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
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/klauspost/compress/s2"
)

// makeCompressibleData creates a byte slice of exactly the requested size
// filled with a repeating pattern, simulating typical text/JSON payloads.
func makeCompressibleData(size int) []byte {
	const pattern = "the quick brown fox jumps over the lazy dog\n"
	buf := make([]byte, size)
	for i := 0; i < size; i += copy(buf[i:], pattern) {
	}
	return buf
}

// BenchmarkEncodeStreamMsg benchmarks the stream message encoding at various
// message sizes, both below and above the compression threshold (8KB).
// Run with: go test -run='^$' -bench='BenchmarkEncodeStreamMsg' -benchmem ./server/
func BenchmarkEncodeStreamMsg(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"256B", 256},
		{"1KB", 1024},
		{"4KB", 4 * 1024},
		{"8KB", 8 * 1024},           // At compression threshold
		{"16KB", 16 * 1024},         // Above threshold — compression kicks in
		{"64KB", 64 * 1024},         // Large message
		{"256KB", 256 * 1024},       // Very large message
		{"1MB", 1024 * 1024},        // 1MB message
	}

	subject := "TEST.stream.encode.benchmark"
	reply := "INBOX.reply123"
	hdr := []byte("NATS/1.0\r\nX-Test: true\r\n\r\n")
	ts := time.Now().UnixNano()

	for _, sz := range sizes {
		// Compressible data: repeated pattern (typical for JSON/text payloads).
		compressible := makeCompressibleData(sz.size)

		// Incompressible data: random bytes.
		incompressible := make([]byte, sz.size)
		rand.Read(incompressible)

		b.Run(fmt.Sprintf("compressible/%s", sz.name), func(b *testing.B) {
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			for b.Loop() {
				encodeStreamMsgAllowCompress(subject, reply, hdr, compressible, 1, ts, false)
			}
		})

		b.Run(fmt.Sprintf("incompressible/%s", sz.name), func(b *testing.B) {
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			for b.Loop() {
				encodeStreamMsgAllowCompress(subject, reply, hdr, incompressible, 1, ts, false)
			}
		})
	}
}

// BenchmarkEncodeStreamMsgBatch benchmarks stream message encoding with batch
// support, which exercises the additional batch ID and sequence encoding.
func BenchmarkEncodeStreamMsgBatch(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"4KB", 4 * 1024},
		{"16KB", 16 * 1024},
		{"64KB", 64 * 1024},
	}

	subject := "TEST.stream.batch"
	reply := ""
	hdr := []byte("NATS/1.0\r\n\r\n")
	ts := time.Now().UnixNano()
	batchId := "batch-abc123"

	for _, sz := range sizes {
		msg := makeCompressibleData(sz.size)

		b.Run(fmt.Sprintf("nobatch/%s", sz.name), func(b *testing.B) {
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			for b.Loop() {
				encodeStreamMsgAllowCompressAndBatch(subject, reply, hdr, msg, 1, ts, false, _EMPTY_, 0, false)
			}
		})

		b.Run(fmt.Sprintf("batch/%s", sz.name), func(b *testing.B) {
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			for b.Loop() {
				encodeStreamMsgAllowCompressAndBatch(subject, reply, hdr, msg, 1, ts, false, batchId, 42, false)
			}
		})
	}
}

// BenchmarkCompressBufPool benchmarks the compression buffer pool in isolation
// to show the benefit of pooling vs. allocating fresh each time.
func BenchmarkCompressBufPool(b *testing.B) {
	sizes := []int{16 * 1024, 64 * 1024, 256 * 1024, 1024 * 1024}

	for _, sz := range sizes {
		name := fmt.Sprintf("%dKB", sz/1024)

		b.Run(fmt.Sprintf("pool/%s", name), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				buf := getCompressBuf(sz)
				_ = buf[sz-1] // touch the buffer
				putCompressBuf(buf)
			}
		})

		b.Run(fmt.Sprintf("nopool/%s", name), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				buf := make([]byte, sz)
				_ = buf[sz-1] // touch the buffer
			}
		})
	}
}

// BenchmarkEncodeDecodeStreamMsg benchmarks the full roundtrip of encoding
// and decoding a stream message, which is the typical hot path during
// RAFT replication.
func BenchmarkEncodeDecodeStreamMsg(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"16KB", 16 * 1024},
		{"64KB", 64 * 1024},
	}

	subject := "TEST.roundtrip"
	reply := "INBOX.rt"
	hdr := []byte("NATS/1.0\r\nX-Test: true\r\n\r\n")
	ts := time.Now().UnixNano()

	for _, sz := range sizes {
		msg := makeCompressibleData(sz.size)

		b.Run(sz.name, func(b *testing.B) {
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			for b.Loop() {
				encoded := encodeStreamMsgAllowCompress(subject, reply, hdr, msg, 1, ts, false)
				op := entryOp(encoded[0])
				mbuf := encoded[1:]
				if op == compressedStreamMsgOp {
					var err error
					mbuf, err = s2.Decode(nil, mbuf)
					if err != nil {
						b.Fatal(err)
					}
				}
				_, _, _, _, _, _, _, err := decodeStreamMsg(mbuf)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
