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

//go:build !skip_store_tests

package server

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
)

// Benchmark_FileStoreWorkQueueBlockSize benchmarks the core workqueue pattern
// (store messages, load, then remove) at different block sizes.
// This is used to evaluate the performance impact of changing the default
// block size for workqueue streams with MaxBytes from 8MB to 4MB.
func Benchmark_FileStoreWorkQueueBlockSize(b *testing.B) {
	blockSizeCases := []struct {
		name    string
		blkSize uint64
	}{
		{"BlkSz=4MB", 4 * 1024 * 1024},
		{"BlkSz=8MB", 8 * 1024 * 1024},
	}

	messageSizeCases := []struct {
		name string
		size int
	}{
		{"MsgSz=256B", 256},
		{"MsgSz=1KB", 1024},
		{"MsgSz=8KB", 8 * 1024},
		{"MsgSz=64KB", 64 * 1024},
	}

	for _, bc := range blockSizeCases {
		b.Run(bc.name, func(b *testing.B) {
			for _, mc := range messageSizeCases {
				b.Run(mc.name, func(b *testing.B) {
					cfg := FileStoreConfig{
						StoreDir:  b.TempDir(),
						BlockSize: bc.blkSize,
					}
					scfg := StreamConfig{
						Name:      "WQ",
						Subjects:  []string{"wq.>"},
						Storage:   FileStorage,
						Retention: WorkQueuePolicy,
						MaxBytes:  256 * 1024 * 1024, // 256MB
					}
					fs, err := newFileStore(cfg, scfg)
					require_NoError(b, err)
					defer fs.Stop()

					msg := make([]byte, mc.size)
					rand.Read(msg)

					b.SetBytes(int64(mc.size))
					b.ResetTimer()

					// Simulate a workqueue pattern: store and then remove.
					for i := 0; i < b.N; i++ {
						seq, _, err := fs.StoreMsg("wq.test", nil, msg, 0)
						if err != nil {
							b.Fatalf("StoreMsg error: %v", err)
						}
						_, err = fs.RemoveMsg(seq)
						if err != nil {
							b.Fatalf("RemoveMsg error: %v", err)
						}
					}
				})
			}
		})
	}
}

// Benchmark_FileStoreWorkQueuePublishBurst benchmarks bursty publishing
// into a workqueue-style filestore at different block sizes.
// This simulates a burst of messages arriving before consumers can drain them,
// which is the scenario where block size most affects memory usage.
func Benchmark_FileStoreWorkQueuePublishBurst(b *testing.B) {
	blockSizeCases := []struct {
		name    string
		blkSize uint64
	}{
		{"BlkSz=4MB", 4 * 1024 * 1024},
		{"BlkSz=8MB", 8 * 1024 * 1024},
	}

	messageSizeCases := []struct {
		name string
		size int
	}{
		{"MsgSz=256B", 256},
		{"MsgSz=1KB", 1024},
		{"MsgSz=8KB", 8 * 1024},
	}

	for _, bc := range blockSizeCases {
		b.Run(bc.name, func(b *testing.B) {
			for _, mc := range messageSizeCases {
				b.Run(mc.name, func(b *testing.B) {
					cfg := FileStoreConfig{
						StoreDir:  b.TempDir(),
						BlockSize: bc.blkSize,
					}
					scfg := StreamConfig{
						Name:      "WQ",
						Subjects:  []string{"wq.>"},
						Storage:   FileStorage,
						Retention: WorkQueuePolicy,
						MaxBytes:  256 * 1024 * 1024, // 256MB
					}
					fs, err := newFileStore(cfg, scfg)
					require_NoError(b, err)
					defer fs.Stop()

					msg := make([]byte, mc.size)
					rand.Read(msg)

					b.SetBytes(int64(mc.size))
					b.ResetTimer()

					// Pure publish burst — no consumption.
					for i := 0; i < b.N; i++ {
						_, _, err := fs.StoreMsg("wq.test", nil, msg, 0)
						if err != nil {
							b.Fatalf("StoreMsg error: %v", err)
						}
					}

					b.StopTimer()

					// Report memory stats to show the impact of block size.
					var m runtime.MemStats
					runtime.ReadMemStats(&m)
					b.ReportMetric(float64(m.HeapInuse)/(1024*1024), "heap-MB")
					b.ReportMetric(float64(fs.numMsgBlocks()), "blocks")
				})
			}
		})
	}
}

// Benchmark_FileStoreWorkQueueDrainPattern benchmarks the full workqueue
// lifecycle: fill the store with a burst, then drain all messages.
// This shows how block size affects the drain phase when blocks are
// being reclaimed.
func Benchmark_FileStoreWorkQueueDrainPattern(b *testing.B) {
	blockSizeCases := []struct {
		name    string
		blkSize uint64
	}{
		{"BlkSz=4MB", 4 * 1024 * 1024},
		{"BlkSz=8MB", 8 * 1024 * 1024},
	}

	// Fixed message size, vary number of messages in the burst.
	burstSizeCases := []struct {
		name  string
		count int
	}{
		{"Burst=1K", 1_000},
		{"Burst=10K", 10_000},
		{"Burst=50K", 50_000},
	}

	const messageSize = 1024 // 1KB messages

	for _, bc := range blockSizeCases {
		b.Run(bc.name, func(b *testing.B) {
			for _, sc := range burstSizeCases {
				b.Run(sc.name, func(b *testing.B) {
					for n := 0; n < b.N; n++ {
						b.StopTimer()

						cfg := FileStoreConfig{
							StoreDir:  b.TempDir(),
							BlockSize: bc.blkSize,
						}
						scfg := StreamConfig{
							Name:      "WQ",
							Subjects:  []string{"wq.>"},
							Storage:   FileStorage,
							Retention: WorkQueuePolicy,
							MaxBytes:  256 * 1024 * 1024,
						}
						fs, err := newFileStore(cfg, scfg)
						require_NoError(b, err)

						msg := make([]byte, messageSize)
						rand.Read(msg)

						// Fill phase (not timed).
						for i := 0; i < sc.count; i++ {
							_, _, err := fs.StoreMsg("wq.test", nil, msg, 0)
							if err != nil {
								b.Fatalf("StoreMsg error: %v", err)
							}
						}

						b.StartTimer()

						// Drain phase (timed) — this is what we're benchmarking.
						var smv StoreMsg
						for seq := uint64(1); seq <= uint64(sc.count); seq++ {
							_, err := fs.LoadMsg(seq, &smv)
							if err != nil {
								b.Fatalf("LoadMsg seq=%d error: %v", seq, err)
							}
							_, err = fs.RemoveMsg(seq)
							if err != nil {
								b.Fatalf("RemoveMsg seq=%d error: %v", seq, err)
							}
						}

						b.StopTimer()
						fs.Stop()
					}

					b.SetBytes(int64(messageSize) * int64(sc.count))
				})
			}
		})
	}
}

// Benchmark_FileStoreWorkQueueMultiSubjectBlockSize benchmarks workqueue
// operations across many subjects with different block sizes.
// Workqueue streams often have many subjects routed to different consumers,
// so this tests a realistic multi-subject pattern.
func Benchmark_FileStoreWorkQueueMultiSubjectBlockSize(b *testing.B) {
	blockSizeCases := []struct {
		name    string
		blkSize uint64
	}{
		{"BlkSz=4MB", 4 * 1024 * 1024},
		{"BlkSz=8MB", 8 * 1024 * 1024},
	}

	numSubjectsCases := []struct {
		name string
		n    int
	}{
		{"Subjs=10", 10},
		{"Subjs=100", 100},
		{"Subjs=1000", 1000},
	}

	const messageSize = 512 // 512B messages

	for _, bc := range blockSizeCases {
		b.Run(bc.name, func(b *testing.B) {
			for _, sc := range numSubjectsCases {
				b.Run(sc.name, func(b *testing.B) {
					cfg := FileStoreConfig{
						StoreDir:  b.TempDir(),
						BlockSize: bc.blkSize,
					}
					scfg := StreamConfig{
						Name:      "WQ",
						Subjects:  []string{"wq.>"},
						Storage:   FileStorage,
						Retention: WorkQueuePolicy,
						MaxBytes:  256 * 1024 * 1024,
					}
					fs, err := newFileStore(cfg, scfg)
					require_NoError(b, err)
					defer fs.Stop()

					// Build subjects list.
					subjects := make([]string, sc.n)
					for i := 0; i < sc.n; i++ {
						subjects[i] = fmt.Sprintf("wq.group.%d", i)
					}

					msg := make([]byte, messageSize)
					rand.Read(msg)

					b.SetBytes(int64(messageSize))
					b.ResetTimer()

					for i := 0; i < b.N; i++ {
						subj := subjects[i%sc.n]
						seq, _, err := fs.StoreMsg(subj, nil, msg, 0)
						if err != nil {
							b.Fatalf("StoreMsg error: %v", err)
						}
						_, err = fs.RemoveMsg(seq)
						if err != nil {
							b.Fatalf("RemoveMsg error: %v", err)
						}
					}
				})
			}
		})
	}
}
