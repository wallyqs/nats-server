package server

import (
	"testing"
)

// Benchmark the trie-based header matching
func BenchmarkIndexJsHdrTrie(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n" +
		"Nats-Msg-Id: test123\r\n" +
		"Nats-Expected-Stream: mystream\r\n" +
		"Nats-Expected-Last-Sequence: 100\r\n" +
		"Nats-Rollup: subject\r\n" +
		"Nats-TTL: 3600\r\n" +
		"Nats-Batch-Id: batch123\r\n" +
		"Nats-Schedule: @at 2025-01-01T00:00:00Z\r\n" +
		"\r\n")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, idx := indexJsHdr(hdr)
		if idx != nil {
			idx.returnToPool()
		}
	}
}

// Benchmark just the trie matching itself
func BenchmarkMatchHdrKey(b *testing.B) {
	keys := [][]byte{
		[]byte(JSMsgId),
		[]byte(JSExpectedStream),
		[]byte(JSExpectedLastSeq),
		[]byte(JSExpectedLastSubjSeq),
		[]byte(JSMsgRollup),
		[]byte(JSMessageTTL),
		[]byte(JSBatchId),
		[]byte(JSSchedulePattern),
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, key := range keys {
			_ = matchHdrKey(key)
		}
	}
}

// Benchmark with various header combinations
func BenchmarkIndexJsHdrSmall(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n" +
		"Nats-Msg-Id: test123\r\n" +
		"\r\n")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, idx := indexJsHdr(hdr)
		if idx != nil {
			idx.returnToPool()
		}
	}
}

func BenchmarkIndexJsHdrLarge(b *testing.B) {
	hdr := []byte("NATS/1.0\r\n" +
		"Nats-Msg-Id: test123\r\n" +
		"Nats-Expected-Stream: mystream\r\n" +
		"Nats-Expected-Last-Sequence: 100\r\n" +
		"Nats-Expected-Last-Subject-Sequence: 50\r\n" +
		"Nats-Expected-Last-Subject-Sequence-Subject: foo.bar\r\n" +
		"Nats-Expected-Last-Msg-Id: prev123\r\n" +
		"Nats-Rollup: subject\r\n" +
		"Nats-TTL: 3600\r\n" +
		"Nats-Incr: 1\r\n" +
		"Nats-Batch-Id: batch123\r\n" +
		"Nats-Batch-Sequence: 5\r\n" +
		"Nats-Batch-Commit: true\r\n" +
		"Nats-Schedule: @at 2025-01-01T00:00:00Z\r\n" +
		"Nats-Schedule-TTL: 86400\r\n" +
		"Nats-Schedule-Target: foo.target\r\n" +
		"Nats-Scheduler: scheduler.service\r\n" +
		"Nats-Schedule-Next: purge\r\n" +
		"Nats-Required-Api-Level: 1\r\n" +
		"\r\n")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, idx := indexJsHdr(hdr)
		if idx != nil {
			idx.returnToPool()
		}
	}
}
