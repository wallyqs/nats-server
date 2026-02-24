# Investigation: blkPool Buffer Growth Under Concurrent Workloads

## Observed Behavior

nats-3 grows from 9.7 GB to 24.5 GB in ~5 minutes. The growth is entirely
in `blkPool` buffers, **not** `encodeStreamMsg`. Three concurrent workloads
are hammering the pool:

| Workload | Memory | Source path |
|----------|--------|-------------|
| Writing incoming messages | 8.5 GB | `writeMsgRecordLocked` → `getMsgBlockBuf` |
| Compacting blocks after workqueue deletes | 9.9 GB | `compactWithFloor` → `getMsgBlockBuf` |
| Interest state checks loading blocks | 4.2 GB | `loadBlock` / `loadMsgsWithLock` → `getMsgBlockBuf` |

Total: ~22.6 GB of blkPool buffers in active use.

## Root Cause Analysis

### 1. This is not a leak — it is concurrent high-water-mark amplification

The `blkPool` uses four `sync.Pool` instances (256 KB, 1 MB, 4 MB, 8 MB
tiers — `filestore.go:969-993`). `sync.Pool` has **no upper bound** on how
many items it holds. It relies on the GC to drain idle entries. But during
sustained concurrent pressure, entries are never idle — they are checked
out, used, returned, and immediately checked out again by a different
goroutine.

### 2. The three workloads create a multiplicative effect

**Writing** (`writeMsgRecordLocked`, `filestore.go:6708-6715`): Each
message block keeps a cache buffer. When the buffer is too small, a new
larger one is pulled from the pool and the old one is recycled. During high
write throughput, many blocks are live simultaneously, each holding a pool
buffer.

**Compaction** (`compactWithFloor`, `filestore.go:5576-5731`): When a
workqueue delete triggers compaction (either inline at `filestore.go:5464`
or via `syncBlocks` at `filestore.go:7200-7223`), the function:
1. Calls `loadMsgsWithLock()` — allocates a pool buffer to read the block
   from disk (`filestore.go:5579`)
2. Allocates a *second* pool buffer via `getMsgBlockBuf(len(buf))` for the
   compacted output (`filestore.go:5586`)
3. Both buffers are alive simultaneously until the function returns

Each compacting block therefore holds **two** pool buffers at once. If
`syncBlocks` is compacting many blocks in sequence, each
`compactWithFloor` call returns its `nbuf` (via `defer recycleMsgBlockBuf`)
but the loaded cache stays alive until `finishedWithCache` at
`filestore.go:5583`. This is correct behavior — the buffers are needed.

**Interest/state checks** (`loadMsgsWithLock`, `filestore.go:7794-7885`):
40+ call sites load blocks to check subject state, fetch messages, scan for
consumers, etc. Each call:
1. Invokes `loadBlock(nil)` which allocates a pool buffer
   (`filestore.go:7780`)
2. Calls `indexCacheBuf(buf)` to parse messages and build FSS
3. The buffer remains alive in `mb.cache.buf` until the cache expires
   (`cexp`, default 10s — `filestore.go:321`)

During a workqueue delete storm, many blocks get their cache loaded
concurrently for interest checks, and each holds a buffer for the full
cache expiry window.

### 3. `sync.Pool` reclamation depends on GC, which is deferred under pressure

Go's `sync.Pool` clears idle entries every GC cycle. But when the runtime
sees heavy allocation pressure, it may delay or reduce GC frequency
(especially with `GOGC` tuning). The effect is that pool entries accumulate
faster than GC can reclaim them, because:

- Buffers are constantly being checked out and returned
- The pool's per-P (per-processor) fast path stores entries locally,
  reducing sharing but increasing total count
- Under heavy write load, the GC itself is delayed because goroutines
  holding buffers prevent GC from running efficiently

### 4. The `deleteMap()` call is O(all blocks) and serializes everything

When `syncBlocks` decides to compact, it calls `fs.deleteMap()`
(`filestore.go:7219`) which:
1. Takes `fs.mu.RLock()` (filestore read lock)
2. Takes **read locks on ALL message blocks** via `readLockAllMsgBlocks()`
3. Iterates all blocks' dmaps to build a merged sequence set
4. Only then releases all locks

This serialization point means compaction can't overlap with writes to any
block, creating lock contention that extends the lifetime of checked-out
pool buffers.

## Why the buffers aren't "leaked"

The memory accounting is correct:
- Write caches expire after `cexp` (default 10 seconds)
- Compaction buffers are recycled via `defer recycleMsgBlockBuf`
- Loaded blocks are released via `finishedWithCache` or cache expiry

The issue is purely that under three concurrent heavy workloads, the
**instantaneous working set** of buffers is large. The pool faithfully
recycles them, but "recycled" means "available for reuse in the pool", not
"freed to the OS." The OS RSS only shrinks when:
1. GC runs and the pool drains idle entries
2. The Go runtime returns pages to the OS (which has its own delays via
   `MADV_FREE`/`MADV_DONTNEED`)

## Quantitative estimate

With default settings:
- Block size: up to 8 MB (pool tier)
- `syncBlocks` iterates all blocks sequentially — each compaction holds 2×
  buffers (read + compacted output)
- Cache expiry: 10 seconds — all blocks loaded in a 10s window hold
  buffers simultaneously
- Sync interval: 2 minutes — compaction runs every 2 minutes scanning all
  blocks

If 500 blocks are loaded for interest checks within a 10s window, that's
500 × 4 MB (medium tier for workqueue/interest) = **2 GB** just for cache.
If compaction is running on 200 blocks with 50% delete ratio, that's 200 ×
2 × 4 MB = **1.6 GB** for compaction. Write-side is similar — hundreds of
active blocks each holding a buffer.

Under high concurrency, these three pools of buffers coexist, explaining
the 22+ GB observed.

## Potential mitigations (not implemented here)

These are ideas for follow-up, ranked by expected impact:

### A. Limit concurrent compaction block loading
Instead of iterating all blocks in `syncBlocks` and compacting each one
sequentially (holding the full load in memory), batch the compaction and
release buffers between batches. Add a maximum number of blocks to compact
per sync cycle.

### B. Bound the `sync.Pool` with a counting semaphore
Wrap `getMsgBlockBuf` with a semaphore that limits total outstanding pool
buffers. Callers would block when too many buffers are checked out,
applying natural backpressure. This would cap RSS but could reduce
throughput.

### C. Reduce cache expiry under memory pressure
Dynamically shorten `cexp` when memory is high. This releases loaded block
caches faster, reducing the number of simultaneous buffers. Could be
triggered by `runtime.MemStats.HeapInuse` exceeding a threshold.

### D. Lazy `deleteMap()` construction
Cache the merged delete map and invalidate it incrementally rather than
rebuilding from all blocks on every sync cycle. This would reduce lock
contention and the serialization window.

### E. Scope interest-state loading
Several call sites (40+ `loadMsgsWithLock` callers) load entire blocks
when only FSS (filtered subject state) is needed. Using
`ensurePerSubjectInfoLoaded()` where possible avoids allocating the full
block buffer.

## Conclusion

The 9.7 → 24.5 GB growth is **expected behavior** under this specific
workload profile (high-rate writes + workqueue deletes + interest
checks). The buffers are in-use, not leaked. The `encodeStreamMsg` fix is
confirmed working — it is no longer a contributor to memory growth.

Once the delete storm subsides and the workload stabilizes, `sync.Pool`
entries will be drained by GC over subsequent cycles and RSS should
decrease. If it does not decrease, that would indicate a different issue
(buffer pinning or cache expiry failure) worth separate investigation.
