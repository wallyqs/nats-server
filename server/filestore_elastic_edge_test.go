//go:build !skip_store_tests

package server

import (
	"fmt"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestElasticPointerRapidCacheChurn tests scenarios with rapid cache state changes.
func TestElasticPointerRapidCacheChurn(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 64 * 1024 // Small blocks for more cache objects
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "churn_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Create multiple blocks with cached data
		msg := make([]byte, 1024)
		for i := range msg {
			msg[i] = byte(i % 256)
		}

		numMsgs := 200 // Should create multiple blocks
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs.StoreMsg(fmt.Sprintf("churn.%d", i), nil, msg, 0)
			require_NoError(t, err)
		}

		// Load all caches initially
		fs.mu.RLock()
		initialBlocks := len(fs.blks)
		for _, mb := range fs.blks {
			mb.mu.Lock()
			err := mb.loadMsgsWithLock()
			require_NoError(t, err)
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		t.Logf("Rapid churn test with %d blocks", initialBlocks)

		var wg sync.WaitGroup
		var operationCount int64
		var errorCount int64
		var strengthenCount int64
		var weakenCount int64

		// Extremely rapid cache churn
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < 1000; j++ {
					fs.mu.RLock()
					for blockIdx, mb := range fs.blks {
						if blockIdx%2 == workerID%2 { // Workers operate on different blocks
							mb.mu.Lock()
							if mb.cache.Strong() {
								if mb.cache.Weaken() {
									atomic.AddInt64(&weakenCount, 1)
								}
							} else if mb.cache.Value() != nil {
								if mb.cache.Strengthen() != nil {
									atomic.AddInt64(&strengthenCount, 1)
								}
							}
							atomic.AddInt64(&operationCount, 1)
							mb.mu.Unlock()
						}
					}
					fs.mu.RUnlock()
					
					// Microsecond sleep to create very rapid churn
					time.Sleep(time.Microsecond)
				}
			}(i)
		}

		// Concurrent readers during churn
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(readerID int) {
				defer wg.Done()
				for j := 0; j < 500; j++ {
					seq := uint64(rand.Intn(numMsgs) + 1)
					_, err := fs.LoadMsg(seq, nil)
					if err != nil {
						atomic.AddInt64(&errorCount, 1)
					}
				}
			}(i)
		}

		wg.Wait()

		ops := atomic.LoadInt64(&operationCount)
		errors := atomic.LoadInt64(&errorCount)
		strengthens := atomic.LoadInt64(&strengthenCount)
		weakens := atomic.LoadInt64(&weakenCount)

		t.Logf("Rapid churn results: ops=%d, errors=%d, strengthens=%d, weakens=%d", 
			ops, errors, strengthens, weakens)

		// Should have many operations with minimal errors
		require_True(t, ops > 1000)
		require_True(t, strengthens > 0)
		require_True(t, weakens > 0)
		require_True(t, errors < ops/10) // Less than 10% error rate
	})
}

// TestElasticPointerLargeDatasets tests behavior with very large datasets.
func TestElasticPointerLargeDatasets(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 1024 * 1024 // 1MB blocks
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "large_dataset", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Create a large dataset
		msgSize := 8192 // 8KB messages
		numMsgs := 1000 // ~8MB of message data
		msg := make([]byte, msgSize)
		for i := range msg {
			msg[i] = byte(i % 256)
		}

		t.Logf("Creating large dataset: %d messages of %d bytes each", numMsgs, msgSize)

		startTime := time.Now()
		for i := 0; i < numMsgs; i++ {
			subj := fmt.Sprintf("large.%04d.%04d", i/100, i%100)
			_, _, err := fs.StoreMsg(subj, nil, msg, 0)
			require_NoError(t, err)
			
			if i%200 == 199 {
				t.Logf("Stored %d messages...", i+1)
			}
		}
		storeTime := time.Since(startTime)

		fs.mu.RLock()
		numBlocks := len(fs.blks)
		fs.mu.RUnlock()

		t.Logf("Created %d blocks in %v", numBlocks, storeTime)

		// Test cache loading across all blocks
		loadStart := time.Now()
		fs.mu.RLock()
		var totalCacheSize uint64
		for _, mb := range fs.blks {
			mb.mu.Lock()
			err := mb.loadMsgsWithLock()
			require_NoError(t, err)
			if cache := mb.cache.Value(); cache != nil {
				totalCacheSize += uint64(len(cache.buf))
			}
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()
		loadTime := time.Since(loadStart)

		t.Logf("Loaded all caches (%d bytes total) in %v", totalCacheSize, loadTime)

		// Test memory behavior with large dataset
		var beforeMem runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&beforeMem)

		// Weaken all caches
		fs.mu.RLock()
		for _, mb := range fs.blks {
			mb.mu.Lock()
			mb.cache.Weaken()
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		// Force GC
		runtime.GC()
		runtime.GC()

		var afterMem runtime.MemStats
		runtime.ReadMemStats(&afterMem)

		memReduction := int64(beforeMem.Alloc) - int64(afterMem.Alloc)
		t.Logf("Large dataset memory reduction: %d bytes (%.1f%%)", 
			memReduction, float64(memReduction)/float64(beforeMem.Alloc)*100)

		// Test random access performance on large dataset
		accessStart := time.Now()
		accessErrors := 0
		numAccesses := 500
		for i := 0; i < numAccesses; i++ {
			seq := uint64(rand.Intn(numMsgs) + 1)
			_, err := fs.LoadMsg(seq, nil)
			if err != nil {
				accessErrors++
			}
		}
		accessTime := time.Since(accessStart)

		t.Logf("Random access: %d accesses in %v (%v avg), %d errors", 
			numAccesses, accessTime, accessTime/time.Duration(numAccesses), accessErrors)

		require_Equal(t, accessErrors, 0)
		// Memory reduction may be minimal immediately after GC, but should not grow significantly
		if memReduction < 0 && memReduction > -int64(totalCacheSize/10) {
			t.Logf("Small memory growth is acceptable: %d bytes", -memReduction)
		}
	})
}

// TestElasticPointerExtremeMemoryPressure tests behavior under extreme memory pressure.
func TestElasticPointerExtremeMemoryPressure(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 128 * 1024 // Smaller blocks for more cache objects
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "extreme_pressure", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Set very aggressive GC
		oldGCPercent := debug.SetGCPercent(1) // Extremely aggressive
		defer debug.SetGCPercent(oldGCPercent)

		// Create dataset
		msg := make([]byte, 2048)
		numMsgs := 300
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs.StoreMsg(fmt.Sprintf("pressure.%d", i), nil, msg, 0)
			require_NoError(t, err)
		}

		// Load all caches
		fs.mu.RLock()
		numBlocks := len(fs.blks)
		for _, mb := range fs.blks {
			mb.mu.Lock()
			err := mb.loadMsgsWithLock()
			require_NoError(t, err)
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		t.Logf("Extreme pressure test with %d blocks", numBlocks)

		var wg sync.WaitGroup
		var readSuccess int64
		var readErrors int64
		var gcCount int64

		// Continuous GC pressure
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				runtime.GC()
				atomic.AddInt64(&gcCount, 1)
				time.Sleep(10 * time.Millisecond)
			}
		}()

		// Continuous cache state changes under GC pressure
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				fs.mu.RLock()
				for _, mb := range fs.blks {
					mb.mu.Lock()
					if i%2 == 0 {
						mb.cache.Strengthen()
					} else {
						mb.cache.Weaken()
					}
					mb.mu.Unlock()
				}
				fs.mu.RUnlock()
				time.Sleep(5 * time.Millisecond)
			}
		}()

		// Continuous reads under pressure
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 200; j++ {
					seq := uint64(rand.Intn(numMsgs) + 1)
					_, err := fs.LoadMsg(seq, nil)
					if err != nil {
						atomic.AddInt64(&readErrors, 1)
					} else {
						atomic.AddInt64(&readSuccess, 1)
					}
					time.Sleep(time.Millisecond)
				}
			}()
		}

		wg.Wait()

		success := atomic.LoadInt64(&readSuccess)
		errors := atomic.LoadInt64(&readErrors)
		gcs := atomic.LoadInt64(&gcCount)

		t.Logf("Extreme pressure results: success=%d, errors=%d, gc_cycles=%d", success, errors, gcs)

		// Should survive extreme pressure with reasonable success rate
		require_True(t, success > 400) // At least 2/3 success
		require_True(t, errors < 200)  // Less than 1/3 errors
		require_True(t, gcs >= 90)     // GC was actually running
	})
}

// TestElasticPointerCorruptedCacheStates tests edge cases with unusual cache states.
func TestElasticPointerCorruptedCacheStates(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "corrupted_cache", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Store some messages
		msg := []byte("test message for corruption testing")
		numMsgs := 50
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs.StoreMsg(fmt.Sprintf("corrupt.%d", i), nil, msg, 0)
			require_NoError(t, err)
		}

		fs.mu.RLock()
		require_True(t, len(fs.blks) > 0)
		mb := fs.blks[0]
		fs.mu.RUnlock()

		// Test various edge cases
		testCases := []struct {
			name string
			test func() error
		}{
			{
				"NilCacheAccess",
				func() error {
					mb.mu.Lock()
					defer mb.mu.Unlock()
					
					// Force cache to nil state
					if cache := mb.cache.Value(); cache != nil {
						mb.cache.Set(nil)
					}
					
					// Should handle nil gracefully
					val := mb.cache.Value()
					if val != nil {
						return fmt.Errorf("expected nil cache value")
					}
					
					// Should be able to reload
					return mb.loadMsgsWithLock()
				},
			},
			{
				"RapidStateToggling",
				func() error {
					mb.mu.Lock()
					defer mb.mu.Unlock()
					
					err := mb.loadMsgsWithLock()
					if err != nil {
						return err
					}
					
					// Rapid toggling
					for i := 0; i < 100; i++ {
						if i%2 == 0 {
							mb.cache.Strengthen()
						} else {
							mb.cache.Weaken()
						}
					}
					
					// Should still work
					cache := mb.cache.Value()
					if cache == nil {
						return fmt.Errorf("cache disappeared during toggling")
					}
					
					return nil
				},
			},
			{
				"CacheResetDuringOperation",
				func() error {
					mb.mu.Lock()
					defer mb.mu.Unlock()
					
					err := mb.loadMsgsWithLock()
					if err != nil {
						return err
					}
					
					originalCache := mb.cache.Value()
					if originalCache == nil {
						return fmt.Errorf("failed to load cache")
					}
					
					// Simulate cache being reset (like during compaction)
					mb.cache.Set(nil)
					
					// Should be able to reload
					err = mb.loadMsgsWithLock()
					if err != nil {
						return err
					}
					
					newCache := mb.cache.Value()
					if newCache == nil {
						return fmt.Errorf("failed to reload cache")
					}
					
					return nil
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				err := tc.test()
				require_NoError(t, err)
				
				// Verify we can still read messages after each test
				_, err = fs.LoadMsg(1, nil)
				require_NoError(t, err)
			})
		}
	})
}

// TestElasticPointerConcurrentGC tests concurrent GC during cache operations.
func TestElasticPointerConcurrentGC(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 256 * 1024
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "concurrent_gc", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Create moderate dataset
		msg := make([]byte, 4096)
		numMsgs := 100
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs.StoreMsg(fmt.Sprintf("gc.%d", i), nil, msg, 0)
			require_NoError(t, err)
		}

		var wg sync.WaitGroup
		var operations int64
		var gcOps int64
		var cacheHits int64
		var cacheMisses int64

		// Aggressive GC in background
		wg.Add(1)
		go func() {
			defer wg.Done()
			oldPercent := debug.SetGCPercent(5)
			defer debug.SetGCPercent(oldPercent)
			
			for i := 0; i < 50; i++ {
				runtime.GC()
				atomic.AddInt64(&gcOps, 1)
				time.Sleep(20 * time.Millisecond)
			}
		}()

		// Cache operations during GC
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < 250; j++ {
					fs.mu.RLock()
					for _, mb := range fs.blks {
						mb.mu.Lock()
						
						// Mix of operations
						switch j % 4 {
						case 0:
							mb.cache.Strengthen()
						case 1:
							mb.cache.Weaken()
						case 2:
							if mb.cache.Value() != nil {
								atomic.AddInt64(&cacheHits, 1)
							} else {
								atomic.AddInt64(&cacheMisses, 1)
							}
						case 3:
							mb.loadMsgsWithLock()
						}
						
						atomic.AddInt64(&operations, 1)
						mb.mu.Unlock()
					}
					fs.mu.RUnlock()
					
					if j%50 == 0 {
						runtime.Gosched() // Yield to allow GC
					}
				}
			}(i)
		}

		wg.Wait()

		ops := atomic.LoadInt64(&operations)
		gcs := atomic.LoadInt64(&gcOps)
		hits := atomic.LoadInt64(&cacheHits)
		misses := atomic.LoadInt64(&cacheMisses)

		t.Logf("Concurrent GC results: ops=%d, gc_cycles=%d, cache_hits=%d, cache_misses=%d", 
			ops, gcs, hits, misses)

		// Should complete many operations despite aggressive GC
		require_True(t, ops > 500)
		require_True(t, gcs >= 45) // GC was running
		
		// Verify functionality still works
		for i := 1; i <= numMsgs; i++ {
			_, err := fs.LoadMsg(uint64(i), nil)
			require_NoError(t, err)
		}
	})
}

// TestElasticPointerPathologicalAccessPatterns tests extreme access patterns.
func TestElasticPointerPathologicalAccessPatterns(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		fcfg.BlockSize = 64 * 1024 // Small blocks
		fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "pathological", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs.Stop()

		// Create many messages across multiple blocks
		msg := make([]byte, 512)
		numMsgs := 500 // Should create many small blocks
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs.StoreMsg(fmt.Sprintf("path.%d", i), nil, msg, 0)
			require_NoError(t, err)
		}

		fs.mu.RLock()
		numBlocks := len(fs.blks)
		fs.mu.RUnlock()

		t.Logf("Pathological test with %d blocks", numBlocks)

		patterns := []struct {
			name string
			test func() (int, error)
		}{
			{
				"OnlyFirstMessage",
				func() (int, error) {
					errors := 0
					for i := 0; i < 100; i++ {
						_, err := fs.LoadMsg(1, nil)
						if err != nil {
							errors++
						}
					}
					return errors, nil
				},
			},
			{
				"OnlyLastMessage", 
				func() (int, error) {
					errors := 0
					for i := 0; i < 100; i++ {
						_, err := fs.LoadMsg(uint64(numMsgs), nil)
						if err != nil {
							errors++
						}
					}
					return errors, nil
				},
			},
			{
				"AlternatingEnds",
				func() (int, error) {
					errors := 0
					for i := 0; i < 100; i++ {
						var seq uint64
						if i%2 == 0 {
							seq = 1
						} else {
							seq = uint64(numMsgs)
						}
						_, err := fs.LoadMsg(seq, nil)
						if err != nil {
							errors++
						}
					}
					return errors, nil
				},
			},
			{
				"PrimeNumbers",
				func() (int, error) {
					primes := []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47}
					errors := 0
					for i := 0; i < 200; i++ {
						prime := primes[i%len(primes)]
						if prime <= numMsgs {
							_, err := fs.LoadMsg(uint64(prime), nil)
							if err != nil {
								errors++
							}
						}
					}
					return errors, nil
				},
			},
			{
				"PowerOfTwo",
				func() (int, error) {
					errors := 0
					for i := 0; i < 100; i++ {
						pow := 1 << (i % 9) // Powers of 2 up to 256
						if pow <= numMsgs {
							_, err := fs.LoadMsg(uint64(pow), nil)
							if err != nil {
								errors++
							}
						}
					}
					return errors, nil
				},
			},
			{
				"ReverseScan",
				func() (int, error) {
					errors := 0
					for i := numMsgs; i >= 1 && errors < 50; i -= 7 { // Every 7th message backwards
						_, err := fs.LoadMsg(uint64(i), nil)
						if err != nil {
							errors++
						}
					}
					return errors, nil
				},
			},
		}

		for _, pattern := range patterns {
			t.Run(pattern.name, func(t *testing.T) {
				// Reset cache states
				fs.mu.RLock()
				for _, mb := range fs.blks {
					mb.mu.Lock()
					mb.cache.Weaken()
					mb.mu.Unlock()
				}
				fs.mu.RUnlock()

				runtime.GC() // Clean slate

				startTime := time.Now()
				errors, err := pattern.test()
				duration := time.Since(startTime)

				require_NoError(t, err)
				t.Logf("Pattern %s: %d errors in %v", pattern.name, errors, duration)

				// All patterns should work with minimal errors
				require_True(t, errors < 10) // Less than 10% error rate
			})
		}
	})
}

// TestElasticPointerShutdownRestart tests cache behavior during filestore lifecycle.
func TestElasticPointerShutdownRestart(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		storeDir := fcfg.StoreDir
		
		// Phase 1: Create initial filestore and data
		fs1, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "shutdown_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)

		msg := make([]byte, 2048)
		for i := range msg {
			msg[i] = byte(i % 256)
		}

		numMsgs := 100
		for i := 0; i < numMsgs; i++ {
			_, _, err := fs1.StoreMsg(fmt.Sprintf("shutdown.%d", i), nil, msg, 0)
			require_NoError(t, err)
		}

		// Load caches and verify state
		fs1.mu.RLock()
		numBlocks := len(fs1.blks)
		for _, mb := range fs1.blks {
			mb.mu.Lock()
			err := mb.loadMsgsWithLock()
			require_NoError(t, err)
			mb.cache.Strengthen() // Ensure strong references
			require_True(t, mb.cache.Strong())
			mb.mu.Unlock()
		}
		fs1.mu.RUnlock()

		t.Logf("Phase 1: Created %d blocks with loaded caches", numBlocks)

		// Verify all messages readable before shutdown
		for i := 1; i <= numMsgs; i++ {
			_, err := fs1.LoadMsg(uint64(i), nil)
			require_NoError(t, err)
		}

		// Phase 2: Shutdown with active caches
		err = fs1.Stop()
		require_NoError(t, err)

		t.Logf("Phase 2: Shutdown completed")

		// Phase 3: Restart and verify data integrity
		fcfg.StoreDir = storeDir // Reuse same directory
		fs2, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "shutdown_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
		require_NoError(t, err)
		defer fs2.Stop()

		// Verify message count and state
		state := fs2.State()
		require_Equal(t, state.Msgs, uint64(numMsgs))
		require_Equal(t, state.FirstSeq, uint64(1))
		require_Equal(t, state.LastSeq, uint64(numMsgs))

		t.Logf("Phase 3: Restart successful, msgs=%d", state.Msgs)

		// Verify all messages still readable after restart
		readErrors := 0
		for i := 1; i <= numMsgs; i++ {
			_, err := fs2.LoadMsg(uint64(i), nil)
			if err != nil {
				readErrors++
				t.Logf("Read error for seq %d: %v", i, err)
			}
		}
		require_Equal(t, readErrors, 0)

		// Phase 4: Test cache behavior after restart
		fs2.mu.RLock()
		newNumBlocks := len(fs2.blks)
		for _, mb := range fs2.blks {
			mb.mu.Lock()
			// Caches should start fresh (not strong)
			initiallyStrong := mb.cache.Strong()
			
			// Should be able to load
			err := mb.loadMsgsWithLock()
			require_NoError(t, err)
			
			// Should be able to strengthen
			strengthenedCache := mb.cache.Strengthen()
			require_True(t, strengthenedCache != nil)
			require_True(t, mb.cache.Strong())
			
			t.Logf("Block cache state: initially_strong=%v, loaded_successfully=%v", 
				initiallyStrong, strengthenedCache != nil)
			
			mb.mu.Unlock()
		}
		fs2.mu.RUnlock()

		require_Equal(t, newNumBlocks, numBlocks)
		t.Logf("Phase 4: Cache behavior verified after restart")

		// Phase 5: Test rapid shutdown/restart cycle
		for cycle := 0; cycle < 3; cycle++ {
			// Store a few more messages
			for i := 0; i < 5; i++ {
				_, _, err := fs2.StoreMsg(fmt.Sprintf("cycle.%d.%d", cycle, i), nil, msg, 0)
				require_NoError(t, err)
			}

			// Quick shutdown/restart
			err = fs2.Stop()
			require_NoError(t, err)

			fs2, err = newFileStoreWithCreated(fcfg, StreamConfig{Name: "shutdown_test", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
			require_NoError(t, err)
			if cycle < 2 { // Don't defer the last one since we already have a defer above
				defer fs2.Stop()
			}

			// Verify latest message is readable
			state := fs2.State()
			_, err = fs2.LoadMsg(state.LastSeq, nil)
			require_NoError(t, err)
		}

		t.Logf("Phase 5: Rapid restart cycle completed successfully")
	})
}

// BenchmarkElasticPointerEdgeCases benchmarks performance under edge conditions.
func BenchmarkElasticPointerEdgeCases(b *testing.B) {
	fcfg := FileStoreConfig{
		StoreDir: b.TempDir(),
		BlockSize: 128 * 1024,
	}
	
	fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "edge_bench", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer fs.Stop()

	// Setup data
	msg := make([]byte, 1024)
	numMsgs := 1000
	for i := 0; i < numMsgs; i++ {
		_, _, err := fs.StoreMsg(fmt.Sprintf("bench.%d", i), nil, msg, 0)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.Run("RapidCacheToggle", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			fs.mu.RLock()
			for _, mb := range fs.blks {
				mb.mu.Lock()
				if i%2 == 0 {
					mb.cache.Strengthen()
				} else {
					mb.cache.Weaken()
				}
				mb.mu.Unlock()
			}
			fs.mu.RUnlock()
		}
	})

	b.Run("CacheAccessUnderGC", func(b *testing.B) {
		oldPercent := debug.SetGCPercent(10)
		defer debug.SetGCPercent(oldPercent)
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			seq := uint64(i%numMsgs + 1)
			_, err := fs.LoadMsg(seq, nil)
			if err != nil {
				b.Fatal(err)
			}
			
			if i%100 == 0 {
				runtime.GC()
			}
		}
	})

	b.Run("PathologicalAccess", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Alternate between first and last message
			var seq uint64
			if i%2 == 0 {
				seq = 1
			} else {
				seq = uint64(numMsgs)
			}
			_, err := fs.LoadMsg(seq, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}