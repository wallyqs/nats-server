// Copyright 2023-2024 The NATS Authors
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

package avl

import (
	"fmt"
	"math/rand"
	"testing"
)

// BenchmarkInsertSequential benchmarks inserting sequential sequences.
// This is the common case for stream message tracking.
func BenchmarkInsertSequential(b *testing.B) {
	for _, n := range []int{1000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var ss SequenceSet
				for seq := uint64(1); seq <= uint64(n); seq++ {
					ss.Insert(seq)
				}
			}
		})
	}
}

// BenchmarkInsertRandom benchmarks inserting sequences in random order.
// This stresses the AVL rebalancing path.
func BenchmarkInsertRandom(b *testing.B) {
	for _, n := range []int{1000, 10_000, 100_000} {
		// Pre-generate random sequences.
		rng := rand.New(rand.NewSource(42))
		seqs := make([]uint64, n)
		for i := range seqs {
			seqs[i] = uint64(rng.Int63n(int64(n) * 10))
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var ss SequenceSet
				for _, seq := range seqs {
					ss.Insert(seq)
				}
			}
		})
	}
}

// BenchmarkInsertDuplicate benchmarks inserting sequences that already exist.
// Tests the duplicate short-circuit optimization.
func BenchmarkInsertDuplicate(b *testing.B) {
	for _, n := range []int{1000, 10_000, 100_000} {
		// Build the set first.
		var ss SequenceSet
		for seq := uint64(1); seq <= uint64(n); seq++ {
			ss.Insert(seq)
		}
		// Pre-generate random sequences that are all duplicates.
		rng := rand.New(rand.NewSource(42))
		dups := make([]uint64, 1000)
		for i := range dups {
			dups[i] = uint64(rng.Int63n(int64(n))) + 1
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, seq := range dups {
					ss.Insert(seq)
				}
			}
		})
	}
}

// BenchmarkInsertBitOnly benchmarks inserting into existing nodes (bit flip only).
// Sequences within the same numEntries range share a node.
func BenchmarkInsertBitOnly(b *testing.B) {
	for _, nodes := range []int{10, 100, 1000} {
		// Create a tree with the given number of nodes by inserting one seq per node.
		var ss SequenceSet
		for i := 0; i < nodes; i++ {
			ss.Insert(uint64(i) * numEntries)
		}
		// Now insert additional sequences that fall into existing nodes.
		seqs := make([]uint64, 1000)
		rng := rand.New(rand.NewSource(42))
		for i := range seqs {
			nodeIdx := rng.Intn(nodes)
			offset := rng.Int63n(int64(numEntries))
			seqs[i] = uint64(nodeIdx)*numEntries + uint64(offset)
		}
		b.Run(fmt.Sprintf("nodes=%d", nodes), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, seq := range seqs {
					ss.Insert(seq)
				}
			}
		})
	}
}

// BenchmarkAbsorb benchmarks the Absorb method for merging non-overlapping sets.
// This simulates the deleteMap() use case.
func BenchmarkAbsorb(b *testing.B) {
	for _, numBlocks := range []int{10, 100, 500} {
		for _, deletesPerBlock := range []int{100, 1000} {
			name := fmt.Sprintf("blocks=%d/deletes=%d", numBlocks, deletesPerBlock)
			// Build source sets simulating block dmaps.
			srcs := make([]*SequenceSet, numBlocks)
			for i := 0; i < numBlocks; i++ {
				var ss SequenceSet
				base := uint64(i) * uint64(deletesPerBlock) * 10
				rng := rand.New(rand.NewSource(int64(i)))
				for j := 0; j < deletesPerBlock; j++ {
					ss.Insert(base + uint64(rng.Int63n(int64(deletesPerBlock)*10)))
				}
				srcs[i] = ss.Clone()
			}
			b.Run(name+"/Absorb", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					// Clone sources each iteration since Absorb takes ownership.
					clones := make([]*SequenceSet, len(srcs))
					for j, src := range srcs {
						clones[j] = src.Clone()
					}
					var dmap SequenceSet
					dmap.Absorb(clones...)
				}
			})
			b.Run(name+"/UnionInsert", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					var dmap SequenceSet
					for _, src := range srcs {
						src.Range(func(seq uint64) bool {
							dmap.Insert(seq)
							return true
						})
					}
				}
			})
		}
	}
}

// BenchmarkDelete benchmarks deleting sequences.
func BenchmarkDelete(b *testing.B) {
	for _, n := range []int{1000, 10_000, 100_000} {
		// Pre-generate delete order.
		rng := rand.New(rand.NewSource(42))
		seqs := make([]uint64, n)
		for i := range seqs {
			seqs[i] = uint64(i) + 1
		}
		rng.Shuffle(len(seqs), func(i, j int) { seqs[i], seqs[j] = seqs[j], seqs[i] })

		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				var ss SequenceSet
				for seq := uint64(1); seq <= uint64(n); seq++ {
					ss.Insert(seq)
				}
				b.StartTimer()
				for _, seq := range seqs {
					ss.Delete(seq)
				}
			}
		})
	}
}
