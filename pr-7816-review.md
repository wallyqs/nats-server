# PR #7816 Review: Various filestore fixes

**Author:** MauriceVanVeen
**Files changed:** 5 (+323, -81)
**Status:** Merged into `main`

## Overview

This PR bundles multiple correctness and safety fixes across the filestore and memstore subsystems. The changes address: lock safety, data integrity during truncation/compression, use-after-free on cached subject strings, lazy subject state recalculation, arithmetic underflow protection, and atomic access consistency. Each fix is accompanied by appropriate test coverage.

## Fix-by-Fix Analysis

### 1. Buffer recycling in `recoverMsgBlock` (filestore.go:1168-1181)

The `loadBlock(nil)` call draws from a block pool, but the buffer was never returned. The fix extracts the variable, adds `recycleMsgBlockBuf(buf)` after use. Straightforward resource leak fix. **Correct.**

### 2. Subject length validation accounting for header bit (filestore.go:1590, 5604, 7443, 8189)

This is one of the more significant correctness fixes. When `hbit` is set, the on-disk record contains a 4-byte header length prefix in addition to `slen`. The sanity check `slen > (dlen - recordHashSize)` would incorrectly pass for records with headers, because it didn't account for those extra 4 bytes. The fix introduces `shlen = slen + 4` when `hasHeaders` is true and uses `shlen` in the sanity check.

This is applied consistently across all four record-parsing paths:
- `rebuildStateFromBufLocked`
- `compactWithFloor`
- `indexCacheBuf`
- `msgFromBufEx`

**Correct and thorough** — the test `TestFileStoreCorruptionSetsHbitWithoutHeaders` validates that a record with `hbit` set but no actual headers is now properly rejected by all three code paths (msgFromBuf, indexCacheBuf, rebuildState).

### 3. Deferred TTL/schedule accounting during rebuild (filestore.go:1675-1693)

Previously, TTL entries and schedule entries were added to the global `fs.ttls` and `fs.scheduling` maps *inside* the per-message loop of `rebuildStateFromBufLocked`. The PR removes this block entirely from the rebuild loop.

The rationale: during recovery, messages haven't been fully validated yet, and the TTL/schedule processing should happen at a higher level after recovery completes (the caller `indexCacheBuf` handles this at filestore.go:7405+). The test `TestFileStoreRecoverTTLAndScheduleStateAndCounters` confirms TTL and schedule counters survive recovery. **Correct.**

### 4. Atomic access for `mb.first.seq` and `mb.last.seq` (filestore.go:1693, 1725-1740, 2301, 4132)

Several places were reading `mb.first.seq` / `mb.last.seq` without `atomic.LoadUint64`, which is inconsistent with the established convention throughout the file. The PR adds atomics to:
- `rebuildStateFromBufLocked` (line 1693: `mb.last.seq == 0` -> `atomic.LoadUint64(&mb.last.seq)`)
- `updateTrackingState` (lines 1725-1740: all reads made atomic)
- `recoverMsgs` (line 2301: `mb.first.seq == 0` -> `atomic.LoadUint64`)
- `NumPendingMulti` (line 4132: `mb.first.seq` -> `atomic.LoadUint64`)

**Correct.** This is important because these fields can be read without `mb.mu` held, relying on atomics for visibility.

### 5. Subject string copying to prevent use-after-free (filestore.go:5334-5434, 8297, memstore.go:1682)

This is a critical safety fix. The `cacheLookupNoCopy` path returns a `StoreMsg` whose `subj` field points directly into the cache buffer. If the cache is evicted or overwritten (e.g., during `eraseMsg` for secure delete, or after unlocking `mb.mu`), the subject string becomes dangling.

The PR refactors `removeMsgFromBlock` to:
1. Always use `cacheLookupNoCopy` initially (avoiding unnecessary copy)
2. Extract `subj`, `ts`, `lhdr`, `lmsg`, `ttl` into local variables while the lock is held
3. Call `copyString(subj)` at two critical points:
   - Before `mb.mu.Unlock()` when writing tombstones (line 5368)
   - Before `eraseMsg` which overwrites the cache (line 5396)

This is also applied to `SubjectForSeq` in both filestore (line 8297) and memstore (line 1682).

**Correct and well-reasoned.** The approach minimizes copies (only copies when actually needed) while eliminating the use-after-free.

### 6. Compression metadata: recording original size before compression (filestore.go:5653, 7070)

In `compactWithFloor`, the code was:
```go
cbuf, err := mb.cmp.Compress(nbuf)
meta := &CompressionInfo{OriginalSize: uint64(len(nbuf))}  // BUG: nbuf not reassigned yet
nbuf = append(meta.MarshalMetadata(), cbuf...)
```

But in `atomicOverwriteFile`, the code was:
```go
buf, err = alg.Compress(buf)  // buf is now compressed
meta := &CompressionInfo{OriginalSize: uint64(len(buf))}  // BUG: len(buf) is compressed size
```

Both are fixed by capturing `originalSize := len(buf/nbuf)` *before* the compress call. The `compactWithFloor` fix also simplifies by compressing in-place (`nbuf, err = ...` instead of separate `cbuf`).

**Correct.** Without this fix, decompression would use the wrong original size, potentially causing data corruption or panics.

### 7. Checksum fix in `truncate` (filestore.go:5996-6011)

Two issues fixed:
1. The `truncate` method only handled `mb.cmp != NoCompression` for the load-decompress-truncate path, but encrypted blocks (`mb.bek != nil`) also need this treatment. The condition is now `mb.cmp != NoCompression || mb.bek != nil`.
2. The checksum copy was `copy(mb.lchk[0:], buf[:len(buf)-checksumSize])` -- copying from the *start* of the buffer instead of the *end*. Fixed to `buf[len(buf)-checksumSize:]`.

**Correct** — the checksum is always the last 8 bytes. The test `TestFileStoreCorrectChecksumAfterTruncate` validates this across all permutations (compressed, encrypted, both).

### 8. Arithmetic underflow protection in `NumPending` / `NumPendingMulti` (filestore.go:4036-4040, 4374-4378)

The `adjust` variable accumulates corrections during block scanning. If `adjust > total`, the subtraction `total -= adjust` would underflow (uint64). The fix clamps to zero. **Correct.**

### 9. Lock protection for `cloads` and `cacheSize` (filestore.go:8939-8964)

- `cacheLoads()`: was reading `mb.cloads` without holding `mb.mu`. Fixed by adding `mb.mu.RLock()/RUnlock()`.
- `cacheSize()`: was using `mb.mu.RLock()` but calls `mb.finishedWithCache()` which can mutate state. Changed to `mb.mu.Lock()` (write lock).

**Correct.** The `cacheSize` change is particularly important — `finishedWithCache()` modifies `mb.cache`, so a read lock is insufficient.

### 10. Missing lock releases on error paths (filestore.go:9063, 9167, 9530, 9777, 11969, 12335)

Multiple functions had error-return paths that forgot to release `fs.mu` or `o.mu`:
- `PurgeEx`: two paths (cache load error, tombstone write error)
- Compact (via `SKIP` label): tombstone write error
- `Truncate`: `newMsgBlockForWrite` error
- `consumerFileStore.writeState`: encrypt error
- `consumerFileStore.Stop`: encrypt error

All are fixed by adding the missing `Unlock()` before `return`. **Correct and critical** — these would cause deadlocks on error paths.

### 11. Lock ordering fix in `firstSeqForSubj` (filestore.go:5000-5010)

Previously the code updated `fs.psim` (which requires `fs.mu`) while only holding `mb.mu`. The fix reorders: first unlock `mb.mu`, then re-acquire `fs.mu`, then update `info.fblk`. This respects the lock ordering convention (fs.mu before mb.mu). **Correct.**

### 12. `SkipMsgs` race condition (filestore.go:4881-4893)

Reading `mb.dmap.Size()` and `mb.msgs` without holding `mb.mu`. Fixed by wrapping in `mb.mu.RLock()/RUnlock()` and caching the value. **Correct.**

### 13. Lazy subject state recalculation in `allLastSeqsLocked` and `loadLastLocked` (filestore.go:3530-3536, 8375-8380, memstore.go:793-797, 1739-1743)

When `ss.lastNeedsUpdate` is true, the cached `ss.Last` is stale. The fix adds recalculation checks before using `ss.Last` in both filestore and memstore implementations of `allLastSeqsLocked()` and `loadLastLocked()`. **Correct** — the test `TestFileStoreMultiLastSeqsAndLoadLastMsgWithLazySubjectState` validates this.

### 14. `filterIsAll` sorting fix (filestore.go:3564, memstore.go:814)

`filterIsAll` compares `filters` (sorted) against `fs.cfg.Subjects` element-by-element. But `cfg.Subjects` was never sorted, so the comparison could fail for equivalent sets in different order. Fixed by sorting both. **Correct** — test `TestJetStreamStoreFilterIsAll` verifies with `{"c","b","a"}` vs `{"b","a","c"}`.

**Note:** This mutates `fs.cfg.Subjects` in-place. Since this is called under `fs.mu.RLock()`, concurrent reads of `cfg.Subjects` ordering could theoretically race. However, `slices.Sort` on an already-sorted slice is a no-op after the first call, and the sorted order is equally valid for the config, so this is acceptable in practice. If this were a concern, sorting a copy would be safer, but it's a minor point.

### 15. Snapshot lock scope fix (filestore.go:11057)

The `fs.mu.Lock()` was only taken inside the `if fs.aek != nil` branch, but `fs.hh` (used later) also needs protection. The lock is now acquired before the `if` and released after, covering both the decryption and hash operations. **Correct.**

## Test Coverage

The PR adds 5 new tests:
1. `TestFileStoreCorrectChecksumAfterTruncate` — validates fix #7 across all permutations
2. `TestFileStoreRecoverTTLAndScheduleStateAndCounters` — validates fix #3
3. `TestFileStoreCorruptionSetsHbitWithoutHeaders` — validates fix #2 across all three code paths
4. `TestJetStreamStoreFilterIsAll` — validates fix #14 for both storage types
5. `TestFileStoreMultiLastSeqsAndLoadLastMsgWithLazySubjectState` — validates fix #13 for both storage types

Test coverage is thorough and appropriate for the fixes.

## Overall Assessment

This is a high-quality PR that addresses real concurrency and data-integrity bugs. The fixes are consistent with established codebase conventions (lock ordering, atomic access patterns, `copyString` usage, `*Locked` naming). The one minor observation is the in-place sort of `fs.cfg.Subjects` in `filterIsAll` (fix #14), which mutates config state under a read lock — functionally harmless but slightly unconventional.
