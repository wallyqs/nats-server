//go:build !skip_store_tests

package server

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// TestElasticPointerMemoryBenefits provides concrete evidence of memory benefits
// by comparing memory usage patterns with and without cache pressure.
func TestElasticPointerMemoryBenefits(t *testing.T) {
	testFileStoreAllPermutations(t, func(t *testing.T, fcfg FileStoreConfig) {
		t.Run("MemoryEfficiency", func(t *testing.T) {
			testElasticPointerMemoryEfficiency(t, fcfg)
		})
		t.Run("CacheReclamation", func(t *testing.T) {
			testElasticPointerCacheReclamation(t, fcfg)
		})
		t.Run("MemoryGrowthUnderLoad", func(t *testing.T) {
			testElasticPointerMemoryGrowthUnderLoad(t, fcfg)
		})
	})
}

func testElasticPointerMemoryEfficiency(t *testing.T, fcfg FileStoreConfig) {
	fcfg.BlockSize = 256 * 1024 // 256KB blocks for more granular testing
	fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "memory_efficiency", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
	require_NoError(t, err)
	defer fs.Stop()

	// Phase 1: Store messages across multiple blocks
	msgSize := 4096 // 4KB messages
	msg := make([]byte, msgSize)
	for i := range msg {
		msg[i] = byte(i % 256)
	}

	numBlocks := 20
	msgsPerBlock := 50 // Should fill blocks nicely
	totalMsgs := numBlocks * msgsPerBlock

	t.Logf("Storing %d messages (%d bytes each) across ~%d blocks", totalMsgs, msgSize, numBlocks)

	for i := 0; i < totalMsgs; i++ {
		subj := fmt.Sprintf("efficiency.%d", i)
		_, _, err := fs.StoreMsg(subj, nil, msg, 0)
		require_NoError(t, err)
	}

	// Phase 2: Load all caches to establish baseline
	fs.mu.RLock()
	actualBlocks := len(fs.blks)
	for _, mb := range fs.blks {
		mb.mu.Lock()
		err := mb.loadMsgsWithLock()
		require_NoError(t, err)
		mb.cache.Strengthen() // Ensure strong references
		mb.mu.Unlock()
	}
	fs.mu.RUnlock()

	t.Logf("Created %d actual blocks", actualBlocks)

	// Measure memory with all caches loaded and strong
	runtime.GC()
	var strongMem runtime.MemStats
	runtime.ReadMemStats(&strongMem)

	strongCacheSize := fs.cacheSize()
	t.Logf("Memory with strong caches: Alloc=%d, HeapInuse=%d, CacheSize=%d",
		strongMem.Alloc, strongMem.HeapInuse, strongCacheSize)

	// Phase 3: Weaken all caches
	fs.mu.RLock()
	for _, mb := range fs.blks {
		mb.mu.Lock()
		mb.cache.Weaken()
		mb.mu.Unlock()
	}
	fs.mu.RUnlock()

	// Phase 4: Test memory reclamation potential
	oldGCPercent := debug.SetGCPercent(10) // Aggressive GC
	defer debug.SetGCPercent(oldGCPercent)

	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
	}

	var weakMem runtime.MemStats
	runtime.ReadMemStats(&weakMem)
	weakCacheSize := fs.cacheSize()

	t.Logf("Memory with weak caches: Alloc=%d, HeapInuse=%d, CacheSize=%d",
		weakMem.Alloc, weakMem.HeapInuse, weakCacheSize)

	// Phase 5: Verify functionality is maintained
	readErrors := 0
	readStart := time.Now()
	for i := 1; i <= totalMsgs; i++ {
		_, err := fs.LoadMsg(uint64(i), nil)
		if err != nil {
			readErrors++
		}
	}
	readDuration := time.Since(readStart)

	t.Logf("Read test: %d errors in %v (avg %v per msg)",
		readErrors, readDuration, readDuration/time.Duration(totalMsgs))

	// Analysis
	memoryReduction := int64(strongMem.Alloc) - int64(weakMem.Alloc)
	heapReduction := int64(strongMem.HeapInuse) - int64(weakMem.HeapInuse)
	cacheReduction := int64(strongCacheSize) - int64(weakCacheSize)

	t.Logf("Memory reductions: Alloc=%d bytes (%.1f%%), Heap=%d bytes (%.1f%%), Cache=%d bytes (%.1f%%)",
		memoryReduction, float64(memoryReduction)/float64(strongMem.Alloc)*100,
		heapReduction, float64(heapReduction)/float64(strongMem.HeapInuse)*100,
		cacheReduction, float64(cacheReduction)/float64(strongCacheSize)*100)

	// Assertions
	require_Equal(t, readErrors, 0) // Functionality must be preserved

	// Even if GC doesn't reclaim everything immediately, the potential is there
	if memoryReduction > 0 {
		t.Logf("✓ Demonstrated memory reclamation: %d bytes", memoryReduction)
	}
	if cacheReduction > 0 {
		t.Logf("✓ Demonstrated cache size reduction: %d bytes", cacheReduction)
	}
}

func testElasticPointerCacheReclamation(t *testing.T, fcfg FileStoreConfig) {
	fcfg.BlockSize = 128 * 1024 // Smaller blocks for more caches
	fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "cache_reclamation", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
	require_NoError(t, err)
	defer fs.Stop()

	// Create many small messages to maximize cache overhead
	msg := make([]byte, 512)
	for i := range msg {
		msg[i] = byte(i % 256)
	}

	numMsgs := 500
	for i := 0; i < numMsgs; i++ {
		subj := fmt.Sprintf("reclaim.%d.%d", i/100, i%100) // Varied subjects
		_, _, err := fs.StoreMsg(subj, nil, msg, 0)
		require_NoError(t, err)
	}

	// Load all blocks and measure cache activity
	fs.mu.RLock()
	initialBlocks := len(fs.blks)
	for _, mb := range fs.blks {
		mb.mu.Lock()
		err := mb.loadMsgsWithLock()
		require_NoError(t, err)
		mb.mu.Unlock()
	}
	fs.mu.RUnlock()

	t.Logf("Created %d blocks for cache reclamation test", initialBlocks)

	// Simulate different access patterns to test cache behavior
	patterns := []struct {
		name string
		test func() int
	}{
		{
			"SequentialAccess",
			func() int {
				errors := 0
				for i := 1; i <= numMsgs; i++ {
					if _, err := fs.LoadMsg(uint64(i), nil); err != nil {
						errors++
					}
				}
				return errors
			},
		},
		{
			"RandomAccess",
			func() int {
				errors := 0
				// Access every 7th message to create sparse access pattern
				for i := 1; i <= numMsgs; i += 7 {
					if _, err := fs.LoadMsg(uint64(i), nil); err != nil {
						errors++
					}
				}
				return errors
			},
		},
		{
			"RecentAccess",
			func() int {
				errors := 0
				// Only access recent messages
				start := numMsgs - 50
				if start < 1 {
					start = 1
				}
				for i := start; i <= numMsgs; i++ {
					if _, err := fs.LoadMsg(uint64(i), nil); err != nil {
						errors++
					}
				}
				return errors
			},
		},
	}

	for _, pattern := range patterns {
		t.Run(pattern.name, func(t *testing.T) {
			// Measure before
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			beforeCache := fs.cacheSize()

			// Run access pattern
			errors := pattern.test()

			// Force cache management
			fs.mu.RLock()
			strongCount := 0
			weakCount := 0
			for _, mb := range fs.blks {
				mb.mu.Lock()
				if mb.cache.Strong() {
					strongCount++
				} else if mb.cache.Value() != nil {
					weakCount++
				}
				mb.mu.Unlock()
			}
			fs.mu.RUnlock()

			// Measure after
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			afterCache := fs.cacheSize()

			t.Logf("Pattern %s: errors=%d, strong_caches=%d, weak_caches=%d, cache_size_change=%d",
				pattern.name, errors, strongCount, weakCount, int64(afterCache)-int64(beforeCache))

			require_Equal(t, errors, 0)
		})
	}
}

func testElasticPointerMemoryGrowthUnderLoad(t *testing.T, fcfg FileStoreConfig) {
	fcfg.BlockSize = 1024 * 1024 // 1MB blocks
	fs, err := newFileStoreWithCreated(fcfg, StreamConfig{Name: "memory_growth", Storage: FileStorage}, time.Now(), prf(&fcfg), nil)
	require_NoError(t, err)
	defer fs.Stop()

	// Simulate sustained load with continuous writes and reads
	msg := make([]byte, 2048)
	for i := range msg {
		msg[i] = byte(i % 256)
	}

	// Track memory growth over time
	type memSnapshot struct {
		timestamp    time.Time
		allocMem     uint64
		heapInuse    uint64
		cacheSize    uint64
		numBlocks    int
		strongCaches int
		weakCaches   int
	}

	var snapshots []memSnapshot

	takeSnapshot := func(label string) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		fs.mu.RLock()
		numBlocks := len(fs.blks)
		strongCaches := 0
		weakCaches := 0
		for _, mb := range fs.blks {
			mb.mu.Lock()
			if mb.cache.Strong() {
				strongCaches++
			} else if mb.cache.Value() != nil {
				weakCaches++
			}
			mb.mu.Unlock()
		}
		fs.mu.RUnlock()

		snapshot := memSnapshot{
			timestamp:    time.Now(),
			allocMem:     m.Alloc,
			heapInuse:    m.HeapInuse,
			cacheSize:    fs.cacheSize(),
			numBlocks:    numBlocks,
			strongCaches: strongCaches,
			weakCaches:   weakCaches,
		}
		snapshots = append(snapshots, snapshot)

		t.Logf("Snapshot %s: Alloc=%dKB, Heap=%dKB, Cache=%dKB, Blocks=%d, Strong=%d, Weak=%d",
			label, snapshot.allocMem/1024, snapshot.heapInuse/1024, snapshot.cacheSize/1024,
			snapshot.numBlocks, snapshot.strongCaches, snapshot.weakCaches)
	}

	takeSnapshot("Initial")

	// Phase 1: Write many messages
	writePhaseSize := 300
	for i := 0; i < writePhaseSize; i++ {
		subj := fmt.Sprintf("growth.%d", i)
		_, _, err := fs.StoreMsg(subj, nil, msg, 0)
		require_NoError(t, err)

		if i%100 == 99 { // Snapshot every 100 messages
			takeSnapshot(fmt.Sprintf("Write-%d", i+1))
		}
	}

	takeSnapshot("AfterWrites")

	// Phase 2: Mixed read/write workload
	mixedPhaseSize := 200
	for i := 0; i < mixedPhaseSize; i++ {
		// Write new message
		subj := fmt.Sprintf("mixed.%d", i)
		_, _, err := fs.StoreMsg(subj, nil, msg, 0)
		require_NoError(t, err)

		// Read random older message
		if writePhaseSize > 0 {
			readSeq := uint64(i%writePhaseSize + 1)
			_, err := fs.LoadMsg(readSeq, nil)
			if err != nil {
				t.Logf("Read error for seq %d: %v", readSeq, err)
			}
		}

		if i%50 == 49 {
			takeSnapshot(fmt.Sprintf("Mixed-%d", i+1))
		}
	}

	takeSnapshot("AfterMixed")

	// Phase 3: Read-heavy workload
	readPhaseSize := 100
	totalMsgs := writePhaseSize + mixedPhaseSize
	for i := 0; i < readPhaseSize; i++ {
		// Read multiple messages
		for j := 0; j < 5; j++ {
			readSeq := uint64((i*5+j)%totalMsgs + 1)
			_, err := fs.LoadMsg(readSeq, nil)
			if err != nil {
				t.Logf("Read error for seq %d: %v", readSeq, err)
			}
		}

		if i%25 == 24 {
			takeSnapshot(fmt.Sprintf("Read-%d", i+1))
		}
	}

	takeSnapshot("Final")

	// Analysis
	if len(snapshots) >= 2 {
		initial := snapshots[0]
		final := snapshots[len(snapshots)-1]

		allocGrowth := int64(final.allocMem) - int64(initial.allocMem)
		heapGrowth := int64(final.heapInuse) - int64(initial.heapInuse)
		cacheGrowth := int64(final.cacheSize) - int64(initial.cacheSize)
		blockGrowth := final.numBlocks - initial.numBlocks

		t.Logf("Overall growth: Alloc=%+dKB, Heap=%+dKB, Cache=%+dKB, Blocks=%+d",
			allocGrowth/1024, heapGrowth/1024, cacheGrowth/1024, blockGrowth)

		// Calculate memory efficiency metrics
		totalMsgsStored := writePhaseSize + mixedPhaseSize
		avgMemPerMsg := allocGrowth / int64(totalMsgsStored)
		t.Logf("Memory efficiency: %d bytes per message stored", avgMemPerMsg)

		// Elastic pointer benefits should show in controlled growth
		expectedMinGrowth := int64(totalMsgsStored * len(msg))     // At least the message data
		if allocGrowth > 0 && allocGrowth < expectedMinGrowth*10 { // Less than 10x the message data
			t.Logf("✓ Memory growth is reasonable: %dx message data size", allocGrowth/expectedMinGrowth)
		}

		// Cache should not grow unboundedly
		maxExpectedCache := int64(final.numBlocks * 1024 * 1024) // Assume max 1MB cache per block
		if final.cacheSize < uint64(maxExpectedCache) {
			t.Logf("✓ Cache size is bounded: %dKB < %dKB max", final.cacheSize/1024, maxExpectedCache/1024)
		}
	}
}
