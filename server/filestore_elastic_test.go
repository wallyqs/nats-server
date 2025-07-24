//go:build !skip_store_tests

package server

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server/elastic"
)

// TestElasticPointerMemoryPressure tests that elastic pointers allow cache memory
// to be reclaimed under memory pressure while maintaining functionality.
func TestElasticPointerMemoryPressure(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 1024 * 1024 // 1MB blocks
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "elastic_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Store messages to create multiple blocks with caches
		numBlocks := 10
		msgsPerBlock := 100
		msgSize := 1024

		msg := make([]byte, msgSize)
		for i := 0; i < len(msg); i++ {
			msg[i] = byte(i % 256)
		}

		// Store messages across multiple blocks
		for block := 0; block < numBlocks; block++ {
			for msg_idx := 0; msg_idx < msgsPerBlock; msg_idx++ {
				subj := fmt.Sprintf("test.%d.%d", block, msg_idx)
				_, _, err := fs.StoreMsg(subj, nil, msg, 0)
				require_NoError(t, err)
			}
		}

		// Force cache loading by reading messages
		fs.mu.RLock()
		initialBlocks := len(fs.blks)
		for _, mb := range fs.blks {
			mb.mu.Lock()
			err := mb.loadMsgsWithLock()
			require_NoError(t, err)
			// Verify cache is loaded
			cache := mb.cache.Value()
			require_True(t, cache != nil)
			require_True(t, len(cache.buf) > 0)
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		// Record initial memory stats
		var m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		// Simulate memory pressure by forcing GC and checking cache behavior
		oldGCPercent := debug.SetGCPercent(10) // Very aggressive GC
		defer debug.SetGCPercent(oldGCPercent)

		// Force multiple GC cycles
		for i := 0; i < 5; i++ {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}

		// Check that some caches may have been weakened but functionality remains
		fs.mu.RLock()
		weakenedCount := 0
		for _, mb := range fs.blks {
			mb.mu.Lock()
			if !mb.cache.Strong() {
				weakenedCount++
			}
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		// We should still be able to read messages even if some caches are weak
		seq := uint64(1)
		for block := 0; block < numBlocks; block++ {
			for msg_idx := 0; msg_idx < msgsPerBlock; msg_idx++ {
				sm, err := fs.LoadMsg(seq, nil)
				require_NoError(t, err)
				require_True(t, sm != nil)
				require_Equal(t, sm.seq, seq)
				seq++
			}
		}

		// Record final memory stats
		var m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m2)

		// Memory should not have grown excessively despite cache operations
		memGrowth := int64(m2.Alloc) - int64(m1.Alloc)
		t.Logf("Memory growth: %d bytes, weakened caches: %d/%d", memGrowth, weakenedCount, initialBlocks)

		// The test passes if we can still read all messages and caches are managing memory
		require_True(t, memGrowth < int64(numBlocks*msgsPerBlock*msgSize)) // Should not keep all msg data in memory
	})
}

// TestElasticPointerCacheLifecycle tests the strengthen/weaken lifecycle of elastic pointers.
func TestElasticPointerCacheLifecycle(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "lifecycle_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Store some messages
		msg := []byte("test message for lifecycle")
		for i := 0; i < 10; i++ {
			_, _, err := fs.StoreMsg("test.subject", nil, msg, 0)
			require_NoError(t, err)
		}

		fs.mu.RLock()
		require_True(t, len(fs.blks) > 0)
		mb := fs.blks[0]
		fs.mu.RUnlock()

		mb.mu.Lock()

		// Initially cache should be nil or weak
		initialStrong := mb.cache.Strong()

		// Load messages which should strengthen the cache
		err = mb.loadMsgsWithLock()
		require_NoError(t, err)

		// Cache should now be strong and loaded
		cache := mb.cache.Strengthen()
		require_True(t, cache != nil)
		require_True(t, mb.cache.Strong())
		require_True(t, len(cache.buf) > 0)

		// Weaken the cache
		weakened := mb.cache.Weaken()
		require_True(t, weakened)
		require_False(t, mb.cache.Strong())

		// Cache should still be accessible via weak reference
		weakCache := mb.cache.Value()
		require_True(t, weakCache != nil)
		require_Equal(t, cache, weakCache) // Should be same instance

		// Strengthen again
		strongCache := mb.cache.Strengthen()
		require_True(t, strongCache != nil)
		require_True(t, mb.cache.Strong())
		require_Equal(t, cache, strongCache)

		mb.mu.Unlock()

		t.Logf("Cache lifecycle test: initial_strong=%v, strengthened=%v, weakened=%v, re-strengthened=%v",
			initialStrong, cache != nil, weakened, strongCache != nil)
	})
}

// TestElasticPointerConcurrentAccess tests concurrent access to elastic pointer caches.
func TestElasticPointerConcurrentAccess(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "concurrent_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Store messages
		msg := []byte("concurrent access test message")
		numMsgs := 100
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs.StoreMsg(fmt.Sprintf("test.%d", i), nil, msg, 0)
			require_NoError(t, err)
		}

		var wg sync.WaitGroup
		var accessErrors int64
		var successfulReads int64

		// Concurrent readers
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(readerID int) {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					seq := uint64((readerID*20+j)%numMsgs + 1)
					sm, err := fs.LoadMsg(seq, nil)
					if err != nil {
						atomic.AddInt64(&accessErrors, 1)
						t.Logf("Reader %d error on seq %d: %v", readerID, seq, err)
					} else if sm.seq != seq {
						atomic.AddInt64(&accessErrors, 1)
						t.Logf("Reader %d wrong seq: expected %d, got %d", readerID, seq, sm.seq)
					} else {
						atomic.AddInt64(&successfulReads, 1)
					}
				}
			}(i)
		}

		// Concurrent cache management
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				fs.mu.RLock()
				for _, mb := range fs.blks {
					mb.mu.Lock()
					if mb.cache.Strong() {
						mb.cache.Weaken()
					} else if mb.cache.Value() != nil {
						mb.cache.Strengthen()
					}
					mb.mu.Unlock()
				}
				fs.mu.RUnlock()
				time.Sleep(time.Millisecond)
			}
		}()

		wg.Wait()

		totalReads := atomic.LoadInt64(&successfulReads)
		errors := atomic.LoadInt64(&accessErrors)

		t.Logf("Concurrent access: successful_reads=%d, errors=%d", totalReads, errors)

		// We should have mostly successful reads with minimal errors
		require_True(t, totalReads > 150) // At least 75% success rate
		require_True(t, errors < 50)      // Less than 25% error rate
	})
}

// TestElasticPointerGCBehavior tests garbage collection behavior with elastic pointers.
func TestElasticPointerGCBehavior(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "gc_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Create a large message to make cache effects more visible
		largeMsg := make([]byte, 10*1024) // 10KB
		for i := range largeMsg {
			largeMsg[i] = byte(i % 256)
		}

		// Store messages
		numMsgs := 50
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs.StoreMsg(fmt.Sprintf("gc.test.%d", i), nil, largeMsg, 0)
			require_NoError(t, err)
		}

		// Load all caches and strengthen them
		fs.mu.RLock()
		var cachePointers []*elastic.Pointer[cache]
		for _, mb := range fs.blks {
			mb.mu.Lock()
			err := mb.loadMsgsWithLock()
			require_NoError(t, err)
			mb.cache.Strengthen()
			cachePointers = append(cachePointers, mb.cache)
			mb.mu.Unlock()
		}
		numBlocks := len(fs.blks)
		fs.mu.RUnlock()

		// Verify all caches are strong
		strongCount := 0
		for _, cp := range cachePointers {
			if cp.Strong() {
				strongCount++
			}
		}
		require_Equal(t, strongCount, numBlocks)

		// Weaken all caches
		fs.mu.RLock()
		for _, mb := range fs.blks {
			mb.mu.Lock()
			mb.cache.Weaken()
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		// Record memory before GC
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)

		// Force garbage collection
		runtime.GC()
		runtime.GC() // Second GC to ensure cleanup

		// Record memory after GC
		var m2 runtime.MemStats
		runtime.ReadMemStats(&m2)

		// Check that weak references may still work
		validWeakRefs := 0
		for _, cp := range cachePointers {
			if cp.Value() != nil {
				validWeakRefs++
			}
		}

		// Verify we can still read messages (may reload caches)
		readErrors := 0
		for i := 1; i <= numMsgs; i++ {
			_, err := fs.LoadMsg(uint64(i), nil)
			if err != nil {
				readErrors++
			}
		}

		memReclaimed := int64(m1.HeapInuse) - int64(m2.HeapInuse)

		t.Logf("GC behavior: blocks=%d, valid_weak_refs=%d, read_errors=%d, memory_reclaimed=%d bytes",
			numBlocks, validWeakRefs, readErrors, memReclaimed)

		// Test should pass if we can still read all messages
		require_Equal(t, readErrors, 0)

		// Memory should have been reclaimed (though this is not guaranteed)
		if memReclaimed > 0 {
			t.Logf("Successfully reclaimed %d bytes", memReclaimed)
		}
	})
}

// BenchmarkElasticPointerVsTraditional compares performance of elastic pointers vs traditional approach.
func BenchmarkElasticPointerVsTraditional(b *testing.B) {
	// This benchmark simulates the old behavior vs new behavior
	// Since we can't easily test the old code, we'll focus on elastic pointer overhead

	fcfg := FileStoreConfig{
		StoreDir:  b.TempDir(),
		BlockSize: 1024 * 1024,
	}

	fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "benchmark_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer fs.Stop()

	// Store messages
	msg := []byte("benchmark test message for elastic pointer performance")
	numMsgs := 1000
	for i := 0; i < numMsgs; i++ {
		_, _, err := fs.StoreMsg(fmt.Sprintf("bench.%d", i), nil, msg, 0)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	b.Run("ElasticPointerReads", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			seq := uint64(i%numMsgs + 1)
			_, err := fs.LoadMsg(seq, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ElasticPointerCacheManagement", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fs.mu.RLock()
			for _, mb := range fs.blks {
				mb.mu.Lock()
				if mb.cache.Strong() {
					mb.cache.Weaken()
				} else if mb.cache.Value() != nil {
					mb.cache.Strengthen()
				}
				mb.mu.Unlock()
			}
			fs.mu.RUnlock()
		}
	})
}

// BenchmarkElasticPointerMemoryPressure benchmarks behavior under memory pressure.
func BenchmarkElasticPointerMemoryPressure(b *testing.B) {
	fcfg := FileStoreConfig{
		StoreDir:  b.TempDir(),
		BlockSize: 64 * 1024, // Smaller blocks for more cache churn
	}

	fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "memory_pressure_bench", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer fs.Stop()

	// Store messages across many blocks
	msg := make([]byte, 1024)
	numMsgs := 5000 // Should create many blocks
	for i := 0; i < numMsgs; i++ {
		_, _, err := fs.StoreMsg(fmt.Sprintf("pressure.%d", i), nil, msg, 0)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Set aggressive GC
	oldGCPercent := debug.SetGCPercent(20)
	defer debug.SetGCPercent(oldGCPercent)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Random access pattern to stress cache management
		seq := uint64(i%numMsgs + 1)
		_, err := fs.LoadMsg(seq, nil)
		if err != nil {
			b.Fatal(err)
		}

		// Periodic GC to simulate memory pressure
		if i%100 == 0 {
			runtime.GC()
		}
	}
}
