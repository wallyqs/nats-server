# Memory Analysis: 77 GB Peak During Raft Catchup

## Overview

Investigation of memory usage reaching 77 GB RSS during Raft catchup operations
with a 9 GB stream and 41 active consumers.

## Memory Breakdown

| Factor | Est. Memory | % of Total | Root Cause |
|--------|-------------|------------|------------|
| Catchup buffers (blkPoolBig) | ~20 GB | 26% | `sync.Pool` unbounded + buffer leaks |
| Consumer message loading | ~15 GB | 19% | No throttle during stream catchup |
| Freed-not-released (OS) | ~18 GB | 23% | Go/Linux MADV_FREE + heap fragmentation |
| Route send buffers | ~10 GB | 13% | No slow consumer detection for ROUTER |
| Raft pending entries | ~6 GB | 8% | No backpressure on leader |
| Raft metadata | ~4 GB | 5% | Normal overhead during catchup |
| Go runtime/other | ~4 GB | 5% | Goroutine stacks, mmap'd files |

## Detailed Findings

### 1. Catchup Buffers — `blkPoolBig` is unbounded (~20 GB)

**Key files:**
- `server/filestore.go:988` — `blkPoolBig` definition (8 MB per buffer)
- `server/filestore.go:996` — `getMsgBlockBuf` allocates from pool
- `server/jetstream_cluster.go:9897` — Global catchup limit (64 MB)
- `server/jetstream_cluster.go:9968` — `runCatchup` per-stream limits

The catchup flow control caps outstanding bytes at 64 MB globally
(`defaultMaxTotalCatchupOutBytes`), but the buffer pools themselves are
`sync.Pool` with no allocation cap. During catchup, each goroutine calling
`loadBlock` → `getMsgBlockBuf` can allocate new 8 MB buffers without limit.

Additionally, commit `0e6f613` fixed three buffer leak sites:
- `lastChecksum()` (line 2229): buffer not recycled for encrypted blocks
- `loadMsgsWithLock()` (line 7778): decompression produces new buffer, original leaked
- `compact()` (line 9393): buffer not recycled at SKIP label

These leaks cause buffers to exit the pool permanently, forcing continuous fresh
allocations of 8 MB buffers.

### 2. Consumer Messages — No throttling during catchup (~15 GB)

**Key files:**
- `server/consumer.go:4814` — `loopAndGatherMsgs` (main delivery loop)
- `server/jetstream_cluster.go:9268` — `isCatchingUp()` flag (unused in delivery)
- `server/filestore.go:8113` — `msgFromBufEx` (message parsing/allocation)
- `server/stream.go:6769` — `newJSPubMsg` (wrapper allocation)

The consumer delivery loop checks for consumer pause and push mode activity but
**never checks `mset.isCatchingUp()`**. The `isCatchingUp()` flag exists but is
only used in health checks (`isStreamHealthy`), not delivery control.

All 41 consumers continue loading, parsing, and buffering messages at full speed
during catchup. The same messages are loaded from disk 41 times simultaneously
while catchup is also loading the same blocks.

### 3. Raft Pending Entries — Soft limit, no backpressure (~6 GB)

**Key files:**
- `server/raft.go:166` — `pae` map (pending append entries)
- `server/raft.go:4166-4170` — Thresholds (20K drop, 10K warn)
- `server/raft.go:4217` — `cachePendingEntry` (silent drop after 20K)
- `server/raft.go:4180` — `sendAppendEntryLocked` (no pending check)
- `server/raft.go:3274` — `tryCommit` (needs quorum to advance)

The `paeDropThreshold` of 20,000 entries is a cache eviction threshold, not
backpressure. The leader's `sendAppendEntryLocked` has no check on pending
count before sending. With a slow follower blocking quorum, `tryCommit` can't
advance the commit index, so entries accumulate. At 5.2M entries with ~1 KB
each (3-4x in-memory overhead) = 5-6 GB.

### 4. Route Send Buffers — Routes bypass slow consumer detection (~10 GB)

**Key files:**
- `server/client.go:2488` — Slow consumer check gated on `c.kind == CLIENT`
- `server/client.go:714` — Routes use `WriteTimeoutPolicyRetry` (not Close)
- `server/raft.go:2969` — Raft catchup 2 MB `maxOutstanding`

Slow consumer detection at `client.go:2488` explicitly checks
`c.kind == CLIENT`. Router connections skip this entirely. They also retry
on write timeout instead of disconnecting. The outbound buffer on route
connections can grow without bound when the follower can't consume fast enough.

### 5. Freed Memory Not Released (~18 GB)

The 32-37 GB gap between Go heap (~40-45 GB) and RSS (~77 GB) is from Go
releasing memory via `madvise(MADV_FREE)`, which Linux doesn't reclaim until
memory pressure. The rapid allocation/deallocation cycle during catchup creates
heap fragmentation that the runtime retains.

## Potential Improvements

1. **Throttle consumers during catchup**: Check `mset.isCatchingUp()` in
   `loopAndGatherMsgs` and pause delivery. Would eliminate ~15 GB of duplicate
   message loading.

2. **Bound Raft pending entries with backpressure**: Pause proposals when
   pending exceeds threshold, rather than silently dropping from cache.

3. **Add slow consumer detection for routes**: Remove `c.kind == CLIENT` gate
   or add separate route-specific pending limit with logging.

4. **Buffer leak fixes (0e6f613)**: Already addresses three recycling bugs that
   cause steady pool growth during catchup.

5. **Memory tuning**: `GOMEMLIMIT` or `GOGC` tuning to help Go return memory
   more aggressively, reducing the freed-not-released gap.
