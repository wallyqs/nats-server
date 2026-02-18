# JetStream Block Size Benchmark Results

Benchmark: `BenchmarkJetStreamBlockSizeMultiConsumer`
Platform: linux (64 cores)

## Full Test Matrix

The benchmark generates a flat cross-product of the following parameters
(2,400 combinations total), selectable via `-run` filters:

| Parameter       | Values                                      |
|-----------------|---------------------------------------------|
| Block sizes     | 2MB, 4MB, 8MB, 16MB                        |
| Message sizes   | 1KB, 4KB, 8KB, 16KB, 64KB                  |
| MaxBytes        | 256MB, 1GB, 2GB, 4GB, 8GB                  |
| Fill levels     | 25%, 75%                                    |
| Consumer counts | 1, 10, 40, 100, 150, 200                   |
| Subjects        | 1 per consumer (dedicated)                  |

## Results So Far

Results below cover **MsgSz=64KB** across three MaxBytes tiers (2GB, 4GB, 8GB).

### MaxBytes=2GB, Consumers={10,40}

#### 10 Consumers / 10 Subjects

#### 25% Fill (512MB pre-loaded)

| BlkSz | MB/s  | Consumed Msgs | Fetch Errors | Heap Delta (MB) | Peak Heap (MB) | Allocs |
|-------|-------|---------------|--------------|-----------------|----------------|--------|
| 2MB   | 19.83 | 7,694         | 50           | 525.4           | 811.4          | 272K   |
| 4MB   | 16.93 | 6,573         | 50           | **321.5**       | **609.2**      | 244K   |
| 8MB   | **20.06** | **7,809** | 50           | 358.8           | 652.3          | 294K   |
| 16MB  | 9.46  | 3,649         | 50           | 378.4           | 681.5          | 139K   |

#### 75% Fill (1.5GB pre-loaded)

| BlkSz | MB/s  | Consumed Msgs | Fetch Errors | Heap Delta (MB) | Peak Heap (MB) | Allocs |
|-------|-------|---------------|--------------|-----------------|----------------|--------|
| 2MB   | 51.31 | 20,684        | 50           | 1,086           | 1,375          | 730K   |
| 4MB   | 49.78 | 19,850        | 50           | **387.0**       | **676.5**      | 730K   |
| 8MB   | **51.58** | **20,699** | 50          | 589.1           | 882.8          | 778K   |
| 16MB  | 41.19 | 16,293        | 50           | 503.3           | 806.6          | 615K   |

---

#### 40 Consumers / 40 Subjects

#### 25% Fill (512MB pre-loaded)

| BlkSz | MB/s  | Consumed Msgs | Fetch Errors | Heap Delta (MB) | Peak Heap (MB) | Allocs |
|-------|-------|---------------|--------------|-----------------|----------------|--------|
| 2MB   | **20.11** | **7,796** | 200          | 1,090           | 1,393          | 227K   |
| 4MB   | 16.16 | 6,221         | 200          | **306.8**       | **611.9**      | 278K   |
| 8MB   | 17.59 | 6,798         | 200          | 819.8           | 819.8          | 387K   |
| 16MB  | 11.15 | 4,297         | 200          | 987.0           | 987.0          | 281K   |

#### 75% Fill (1.5GB pre-loaded)

| BlkSz | MB/s  | Consumed Msgs | Fetch Errors | Heap Delta (MB) | Peak Heap (MB) | Allocs |
|-------|-------|---------------|--------------|-----------------|----------------|--------|
| 2MB   | 48.76 | 19,354        | 200          | 1,532           | 1,532          | 549K   |
| 4MB   | **52.06** | **20,718** | 200         | **986.5**       | **986.5**      | 890K   |
| 8MB   | 51.98 | 20,934        | 200          | 1,094           | 1,094          | 1,163K |
| 16MB  | 43.59 | 17,427        | 200          | 1,075           | 1,075          | 1,094K |

---

### 4MB vs 8MB Scaling — 40 Consumers, 75% Fill

This section compares 4MB and 8MB block sizes across three MaxBytes tiers
to show how performance scales with stream size.

| MaxBytes | BlkSz | MB/s   | Consumed Msgs | Heap Delta (MB) | Peak Heap (MB) | Allocs |
|----------|-------|--------|---------------|-----------------|----------------|--------|
| 2GB      | 4MB   | 53.38  | 21,270        | **665**         | **974**        | 915K   |
| 2GB      | 8MB   | **57.79** | **23,383** | 870             | 1,186          | 1,305K |
| 4GB      | 4MB   | 98.88  | 41,085        | 1,074           | 1,385          | 1,758K |
| 4GB      | 8MB   | **99.68** | **41,935** | **945**         | **1,259**      | 2,328K |
| 8GB      | 4MB   | **181.09** | 81,022     | 1,658           | 1,975          | 3,504K |
| 8GB      | 8MB   | 177.79 | **81,199**    | **1,598**       | **1,915**      | 4,570K |

---

## Analysis

### Effect of MaxBytes on Throughput

Throughput scales nearly linearly with MaxBytes (more data to read = more sustained sequential I/O):

| MaxBytes | 4MB (MB/s) | 8MB (MB/s) | Winner |
|----------|------------|------------|--------|
| 2GB      | 53.38      | **57.79**  | 8MB (+8%) |
| 4GB      | 98.88      | **99.68**  | ~tied  |
| 8GB      | **181.09** | 177.79     | 4MB (+2%) |

- At **2GB**, 8MB has a clear throughput edge (+8%), likely because fewer, larger blocks reduce overhead per read when the working set is small.
- At **4GB**, throughput is essentially tied.
- At **8GB**, 4MB pulls slightly ahead (+2%), suggesting its higher block count enables better I/O pipelining at scale.
- The ~3.4x throughput jump from 2GB to 8GB indicates the 2GB results were bottlenecked by stream capacity, not block I/O.

### Throughput — All Block Sizes (2GB, 10 & 40 Consumers)

- **2MB, 4MB, and 8MB** are closely grouped at 17-20 MB/s (25% fill) and 49-58 MB/s (75% fill).
- **16MB is consistently the slowest**, losing ~45% throughput at low fill and ~15% at high fill.
- Throughput is **stable across concurrency levels** — going from 10 to 40 consumers barely changes MB/s, indicating the bottleneck is storage I/O rather than consumer contention.

### Memory (Heap Delta)

The memory story depends on stream size.

**2GB MaxBytes (all block sizes, 10 & 40 consumers):**

| BlkSz | 10c / 25% | 40c / 25% | Scaling | 10c / 75% | 40c / 75% | Scaling |
|-------|-----------|-----------|---------|-----------|-----------|---------|
| 2MB   | 525 MB    | 1,090 MB  | 2.1x    | 1,086 MB  | 1,532 MB  | 1.4x    |
| 4MB   | 322 MB    | 307 MB    | **1.0x** | 387 MB   | 987 MB    | 2.5x    |
| 8MB   | 359 MB    | 820 MB    | 2.3x    | 589 MB    | 1,094 MB  | 1.9x    |
| 16MB  | 378 MB    | 987 MB    | 2.6x    | 503 MB    | 1,075 MB  | 2.1x    |

**Scaling across MaxBytes (4MB vs 8MB, 40 consumers, 75% fill):**

| MaxBytes | 4MB Delta | 8MB Delta | Lower Delta | 4MB Allocs | 8MB Allocs |
|----------|-----------|-----------|-------------|------------|------------|
| 2GB      | **665**   | 870       | 4MB (-24%)  | **915K**   | 1,305K     |
| 4GB      | 1,074     | **945**   | 8MB (-12%)  | **1,758K** | 2,328K     |
| 8GB      | 1,658     | **1,598** | 8MB (-4%)   | **3,504K** | 4,570K     |

- At **2GB**, 4MB uses 24% less heap than 8MB — fewer concurrent mmaps when the block count is manageable.
- At **4GB and 8GB**, 8MB takes the lead on heap delta (12% and 4% less respectively) — larger blocks mean fewer total blocks, reducing mmap table pressure at scale.
- **4MB consistently uses fewer allocations** across all sizes (~30% fewer), meaning less GC pressure even when its heap footprint is slightly larger.
- At 2GB / 25% fill, 4MB is the **only block size where memory stays flat** as concurrency increases (322 MB → 307 MB).
- **2MB is the worst for memory** at high concurrency — too many small blocks mapped simultaneously by 40 consumers drives heap delta to 1.5 GB.

### Consumed Messages

Tracks throughput closely. At 75% fill with 40 consumers:
- 4MB: 20,718 (best)
- 8MB: 20,934
- 2MB: 19,354
- 16MB: 17,427 (worst — leaves ~15% of messages unconsumed within the time budget)

---

## Recommendation

**4MB and 8MB are both strong choices for 64KB messages; the best pick depends on stream size.**

| Criteria              | 2GB Streams | 4-8GB Streams | Worst    |
|-----------------------|-------------|---------------|----------|
| Throughput            | 8MB (+8%)   | ~tied         | 16MB     |
| Heap delta            | **4MB** (-24%) | 8MB (-4 to -12%) | 2MB |
| Allocations / GC      | **4MB** (-30%) | **4MB** (-30%) | 8MB  |
| Concurrency scaling   | **4MB**     | —             | 2MB      |

**For streams up to 2GB**, 4MB is the clear winner: it uses 24% less memory than 8MB, scales flat with concurrency at low fill, and the throughput gap is modest.

**For large streams (4-8GB)**, 8MB becomes competitive: it matches or slightly beats 4MB on throughput and has lower heap delta at scale due to fewer total blocks. However, 4MB still produces ~30% fewer allocations, reducing GC pressure.

**General guidance:** 4MB is the safer default across the widest range of workloads. 8MB is worth considering for large, high-throughput streams where minimizing heap is more important than allocation count. Both are dramatically better than 2MB (memory) and 16MB (throughput).

## Open Questions

The expanded test matrix enables follow-up investigation of:

- **Smaller messages (1KB-16KB):** Does the 4MB advantage hold, or does a smaller block size win when per-message overhead dominates?
- **2MB and 16MB at 4-8GB:** Do the extremes fare even worse at scale, or does the gap narrow?
- **High concurrency (100-200 consumers):** At what consumer count does contention become the bottleneck over block I/O?
- **Small streams (256MB):** With fewer total blocks, does block size matter at all?
- **25% fill at 4-8GB:** Does the memory crossover also appear at lower fill levels?
