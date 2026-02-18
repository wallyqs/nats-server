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

Results below cover two dimensions:
- **Block size comparison** (64KB messages across 2GB/4GB/8GB MaxBytes)
- **Message size + consumer scaling** (8MB blocks, 4GB MaxBytes, all message sizes and consumer counts)

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

### 8MB Blocks, 4GB MaxBytes — Full Consumer × Message Size Matrix

Run on branch `release/v2.12.3`. All results below use BlkSz=8MB, MaxBytes=4GB.

#### Throughput (MB/s)

| Consumers | Fill | 1KB    | 4KB    | 8KB    | 16KB    | 64KB   |
|-----------|------|--------|--------|--------|---------|--------|
| 1         | 25%  | 175.82 | 553.42 | 902.49 | 1274.51 | 35.60  |
| 1         | 75%  | 176.12 | 492.31 | 903.84 | 1328.12 | 102.10 |
| 10        | 25%  | 128.13 | 511.18 | 777.54 | 950.83  | 36.41  |
| 10        | 75%  | 82.86  | 480.64 | 774.03 | 939.43  | 97.09  |
| 40        | 25%  | 183.09 | 491.21 | 777.33 | 1021.83 | 34.78  |
| 40        | 75%  | 140.51 | 501.62 | 665.40 | 964.12  | 97.71  |
| 100       | 25%  | 164.99 | 556.17 | 858.42 | 1264.46 | 34.62  |
| 100       | 75%  | 168.45 | 586.51 | 958.20 | 1335.23 | 100.51 |
| 150       | 25%  | 143.30 | 440.70 | 774.83 | 1259.54 | 32.32  |
| 150       | 75%  | 160.43 | 540.90 | 945.87 | 1457.08 | 47.05  |
| 200       | 25%  | 123.86 | 436.49 | 761.95 | 1316.61 | 29.25  |
| 200       | 75%  | 147.84 | 505.49 | 875.31 | 1478.42 | 39.85  |

#### Heap Delta (MB)

| Consumers | Fill | 1KB   | 4KB   | 8KB   | 16KB  | 64KB  |
|-----------|------|-------|-------|-------|-------|-------|
| 1         | 25%  | 364.8 | 379.3 | 386.9 | 403.3 | 363.5 |
| 1         | 75%  | 447.5 | 386.6 | 411.8 | 398.7 | 373.7 |
| 10        | 25%  | 821.3 | 460.3 | 439.3 | 419.5 | 408.2 |
| 10        | 75%  | 1,507 | 761.3 | 573.0 | 515.1 | 434.6 |
| 40        | 25%  | 799.6 | 468.0 | 469.7 | 461.9 | 619.7 |
| 40        | 75%  | 1,935 | 728.0 | 587.2 | 488.3 | 697.1 |
| 100       | 25%  | 906.4 | 629.6 | 636.7 | 640.4 | 635.9 |
| 100       | 75%  | 1,825 | 814.6 | 785.3 | 824.9 | 1,446 |
| 150       | 25%  | 863.0 | 701.9 | 795.2 | 741.1 | 862.1 |
| 150       | 75%  | 1,912 | 894.7 | 886.2 | 1,035 | 3,638 |
| 200       | 25%  | 1,021 | 895.4 | 935.8 | 1,012 | 1,072 |
| 200       | 75%  | 1,974 | 1,003 | 1,044 | 1,334 | 4,365 |

#### Allocations

| Consumers | Fill | 1KB   | 4KB   | 8KB   | 16KB  | 64KB  |
|-----------|------|-------|-------|-------|-------|-------|
| 1         | 25%  | 26.0M | 6.5M | 3.3M | 1.6M | 376K |
| 1         | 75%  | 78.1M | 19.6M | 9.8M | 5.0M | 1.1M |
| 10        | 25%  | 36.9M | 9.2M | 4.6M | 2.3M | 537K |
| 10        | 75%  | 114.6M | 27.5M | 13.7M | 7.0M | 1.5M |
| 40        | 25%  | 67.3M | 16.9M | 8.4M | 4.1M | 757K |
| 40        | 75%  | 203.3M | 50.7M | 25.3M | 12.4M | 2.2M |
| 100       | 25%  | 129.2M | 31.3M | 15.0M | 6.9M | 705K |
| 100       | 75%  | 387.8M | 94.3M | 45.2M | 20.7M | 2.0M |
| 150       | 25%  | 180.1M | 42.9M | 19.9M | 8.5M | 525K |
| 150       | 75%  | 540.0M | 128.6M | 59.9M | 25.6M | 1.4M |
| 200       | 25%  | 230.4M | 53.6M | 24.2M | 9.5M | 539K |
| 200       | 75%  | 690.6M | 161.1M | 72.8M | 28.7M | 1.6M |

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

### Throughput — All Block Sizes (2GB, 10 & 40 Consumers, 64KB msgs)

- **2MB, 4MB, and 8MB** are closely grouped at 17-20 MB/s (25% fill) and 49-58 MB/s (75% fill).
- **16MB is consistently the slowest**, losing ~45% throughput at low fill and ~15% at high fill.
- Throughput is **stable across concurrency levels** — going from 10 to 40 consumers barely changes MB/s, indicating the bottleneck is storage I/O rather than consumer contention.

### Throughput by Message Size (8MB blocks, 4GB)

At 75% fill, message size is the dominant factor for throughput:

| MsgSz | 1 consumer | 40 consumers | 200 consumers | Scaling 1→200 |
|-------|------------|--------------|---------------|---------------|
| 16KB  | **1,328**  | **964**      | **1,478**     | ~stable       |
| 8KB   | 904        | 665          | 875           | ~stable       |
| 4KB   | 492        | 502          | 505           | ~stable       |
| 1KB   | 176        | 141          | 148           | ~stable       |
| 64KB  | 102        | 98           | 40            | **-61%**      |

- **16KB messages achieve the highest throughput** across all consumer counts, peaking at **1,478 MB/s** with 200 consumers at 75% fill.
- **1KB-16KB messages scale flat** with consumer count — throughput is unaffected by concurrency.
- **64KB messages degrade sharply at high concurrency** — dropping from ~100 MB/s at 1-40 consumers to 40 MB/s at 200 consumers. This is the only message size where throughput collapses under load.
- The throughput ordering (16KB > 8KB > 4KB > 1KB >> 64KB) is consistent. 64KB is the **slowest in MB/s** despite being the largest message size, suggesting per-message overhead (ack round-trips, block transitions with only ~128 msgs per 8MB block) dominates.

### Consumer Scaling (8MB blocks, 4GB, 75% fill)

Throughput is remarkably stable from 1 to 200 consumers for most message sizes. The exception is 64KB, which degrades at 150+ consumers. This means:
- For 1-16KB messages, the bottleneck is purely storage I/O — adding consumers is free.
- For 64KB messages, consumer count becomes a bottleneck, likely due to increased block contention and mmap pressure.

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

**Memory by message size (8MB blocks, 4GB, 75% fill):**

| MsgSz | 1c     | 40c    | 100c   | 200c    | Scaling 1→200 |
|-------|--------|--------|--------|---------|---------------|
| 1KB   | 448 MB | 1,935  | 1,825  | 1,974   | 4.4x          |
| 4KB   | 387 MB | 728    | 815    | 1,003   | 2.6x          |
| 8KB   | 412 MB | 587    | 785    | 1,044   | 2.5x          |
| 16KB  | 399 MB | 488    | 825    | 1,334   | 3.3x          |
| 64KB  | 374 MB | 697    | 1,446  | **4,365** | **11.7x**   |

- **At 1 consumer**, all message sizes use ~370-450 MB — message size barely matters.
- **1KB messages** consistently use the most heap because the high message count (3.1M msgs at 75% fill) creates more internal state.
- **64KB messages explode at high concurrency**: heap delta goes from 374 MB (1c) to **4.4 GB** (200c) — an **11.7x** increase. This is by far the worst scaling of any message size. At 150c it's already 3.6 GB.
- **4-8KB messages scale best**, staying under 1.1 GB even at 200 consumers.

### Consumed Messages

Tracks throughput closely. At 75% fill with 40 consumers:
- 4MB: 20,718 (best)
- 8MB: 20,934
- 2MB: 19,354
- 16MB: 17,427 (worst — leaves ~15% of messages unconsumed within the time budget)

---

## Recommendation

### Block Size (64KB messages)

**4MB and 8MB are both strong choices; the best pick depends on stream size.**

| Criteria              | 2GB Streams | 4-8GB Streams | Worst    |
|-----------------------|-------------|---------------|----------|
| Throughput            | 8MB (+8%)   | ~tied         | 16MB     |
| Heap delta            | **4MB** (-24%) | 8MB (-4 to -12%) | 2MB |
| Allocations / GC      | **4MB** (-30%) | **4MB** (-30%) | 8MB  |
| Concurrency scaling   | **4MB**     | —             | 2MB      |

**For streams up to 2GB**, 4MB is the clear winner: it uses 24% less memory than 8MB, scales flat with concurrency at low fill, and the throughput gap is modest.

**For large streams (4-8GB)**, 8MB becomes competitive: it matches or slightly beats 4MB on throughput and has lower heap delta at scale due to fewer total blocks. However, 4MB still produces ~30% fewer allocations, reducing GC pressure.

**General guidance:** 4MB is the safer default across the widest range of workloads. 8MB is worth considering for large, high-throughput streams where minimizing heap is more important than allocation count. Both are dramatically better than 2MB (memory) and 16MB (throughput).

### Message Size (8MB blocks, 4GB)

| MsgSz | Throughput   | Memory at Scale | Best For                        |
|-------|--------------|-----------------|---------------------------------|
| 16KB  | Best (1.5 GB/s) | Moderate     | Maximum throughput              |
| 8KB   | Very good    | **Best**        | Balanced throughput + memory    |
| 4KB   | Good         | **Best**        | Memory-sensitive workloads      |
| 1KB   | Moderate     | High (per-msg state) | Small payload workloads  |
| 64KB  | **Worst**    | **Worst at >100c** | Avoid at high concurrency  |

**Key finding: 64KB messages are a pathological case.** Despite being the largest message size, they have the lowest throughput in MB/s and the worst memory scaling — heap delta reaches **4.4 GB** at 200 consumers. Workloads with 64KB messages and high fan-out should be sized carefully.

**4-16KB messages are the sweet spot** for 8MB blocks at 4GB: high throughput, predictable memory, and flat consumer scaling.

## Open Questions

- **4MB blocks across all message sizes at 4GB:** Does 4MB's memory advantage over 8MB hold for smaller messages, or is it specific to 64KB?
- **2MB and 16MB blocks at 4GB:** Do the extremes fare even worse at scale across different message sizes?
- **64KB pathology root cause:** Is the 64KB degradation caused by block boundary effects (~128 msgs per 8MB block), ack overhead, or mmap churn? Would 4MB blocks (fewer msgs per block, more blocks) perform differently?
- **Small streams (256MB):** With fewer total blocks, does block size matter at all?
- **25% fill at 4-8GB:** Does the memory crossover between 4MB and 8MB also appear at lower fill levels?
