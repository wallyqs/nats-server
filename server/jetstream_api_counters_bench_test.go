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
	"sync"
	"sync/atomic"
	"testing"
)

// Benchmark the current sync.Map approach
func BenchmarkAPICountersSyncMap(b *testing.B) {
	var counters sync.Map
	pattern := JSApiStreamCreate

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			counterVal, _ := counters.LoadOrStore(pattern, new(int64))
			counter := counterVal.(*int64)
			atomic.AddInt64(counter, 1)
		}
	})
}

// Benchmark the old mutex approach for comparison
func BenchmarkAPICountersMutex(b *testing.B) {
	var mu sync.RWMutex
	counters := make(map[string]*int64)
	pattern := JSApiStreamCreate

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			counter := counters[pattern]
			if counter == nil {
				counter = new(int64)
				counters[pattern] = counter
			}
			mu.Unlock()
			atomic.AddInt64(counter, 1)
		}
	})
}

// Benchmark with multiple patterns to test real-world scenario
func BenchmarkAPICountersMultiplePatterns(b *testing.B) {
	var counters sync.Map
	patterns := []string{
		JSApiStreamCreate,
		JSApiStreamInfo,
		JSApiStreams,
		JSApiConsumerCreate,
		JSApiConsumerInfo,
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pattern := patterns[i%len(patterns)]
			counterVal, _ := counters.LoadOrStore(pattern, new(int64))
			counter := counterVal.(*int64)
			atomic.AddInt64(counter, 1)
			i++
		}
	})
}

// Benchmark concurrent reads and writes
func BenchmarkAPICountersReadWrite(b *testing.B) {
	var counters sync.Map
	patterns := []string{
		JSApiStreamCreate,
		JSApiStreamInfo,
		JSApiStreams,
		JSApiConsumerCreate,
		JSApiConsumerInfo,
	}

	// Pre-populate some counters
	for _, pattern := range patterns {
		counters.Store(pattern, new(int64))
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				// 10% reads (simulating stats collection)
				stats := make(map[string]uint64)
				counters.Range(func(key, value interface{}) bool {
					pattern := key.(string)
					counter := value.(*int64)
					stats[pattern] = uint64(atomic.LoadInt64(counter))
					return true
				})
			} else {
				// 90% writes (simulating API calls)
				pattern := patterns[i%len(patterns)]
				counterVal, _ := counters.LoadOrStore(pattern, new(int64))
				counter := counterVal.(*int64)
				atomic.AddInt64(counter, 1)
			}
			i++
		}
	})
}

// Benchmark the memory allocation overhead
func BenchmarkAPICountersAllocation(b *testing.B) {
	var counters sync.Map
	patterns := []string{
		"pattern1", "pattern2", "pattern3", "pattern4", "pattern5",
		"pattern6", "pattern7", "pattern8", "pattern9", "pattern10",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pattern := patterns[i%len(patterns)]
		counterVal, _ := counters.LoadOrStore(pattern, new(int64))
		counter := counterVal.(*int64)
		atomic.AddInt64(counter, 1)
	}
}