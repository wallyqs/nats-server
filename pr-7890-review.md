# PR #7890 Review: Raft Batching

## Summary

This PR by @sciascid introduces Raft batching optimizations across 4 incremental
commits, targeting reduced I/O overhead, lower lock contention, and larger batch
sizes. It also adds a metrics infrastructure for observability.

## Commit-by-Commit Analysis

### Commit 1: "Baseline: collect metrics" (4dfdb87)

Adds a `server/metric` package (Counter, Histogram, Registry) and instruments
fsync counts and raft batch sizes. Exposes metrics via `$SYS.REQ.SERVER.*.METRICS.>`
system subjects.

### Commit 2: "Relax SyncAlways for replicated filestore" (1ea28df)

For replicated streams (R>1), disables per-write `SyncAlways` and enables
`SyncOnFlush` to defer syncing until snapshot creation time.

### Commit 3: "Reduce lock contention on raft mutex" (fb41346)

The core architectural change. Demotes `Propose()`/`ProposeMulti()` to read locks,
makes `storeToWAL()` callable without holding the raft mutex, and restructures
`sendAppendEntry()` so I/O happens outside the critical section.

### Commit 4: "Maximize batch size" (82a15b0)

Replaces hardcoded 256KB/512-entry limits with dynamic limits based on NATS
transport payload constraints and raises max entries to 65535.

---

## Detailed Review Findings

### Critical Issues

#### 1. Race condition in `sendMembershipChange()` — Index reservation gap

```go
n.Lock()
reserved := n.pindex + 1
n.membChangeIndex = reserved
n.Unlock()

index, err := n.sendAppendEntry([]*Entry{e}, true)  // lock released!

n.Lock()
defer n.Unlock()
assert.AlwaysOrUnreachable(index == reserved, ...)
```

Between `n.Unlock()` and `n.sendAppendEntry()`, the leader goroutine is the only
goroutine calling `sendAppendEntry`, so this is likely safe *by construction*
(enforced by the design constraint that only the leader loop calls
`sendAppendEntry`). However, this invariant is subtle and fragile. The assertion
helps catch violations, but only in Antithesis testing builds — in production this
would silently corrupt the membership change tracking.

**Recommendation**: Add a clear code comment documenting why this is safe (i.e.,
that `sendAppendEntry` is only called from the leader goroutine, serializing all
WAL writes). Consider whether the assertion should be a hard error in production
rather than just an Antithesis check.

#### 2. `Propose()` reads `n.werr` under RLock — potential stale read

```go
func (n *raft) Propose(data []byte) error {
    n.RLock()
    ...
    if werr := n.werr; werr != nil {
        n.RUnlock()
        return werr
    }
    n.RUnlock()
    n.prop.push(...)
    return nil
}
```

`n.werr` is set by `n.setWriteErrLocked()` which requires a write lock. Reading
it under `RLock` is safe from a memory-safety standpoint (Go's RWMutex guarantees
visibility). However, after the `RUnlock()`, there's a window where a write error
could be set before the proposal is pushed, meaning we might enqueue a proposal
that will fail at WAL write time. This is probably acceptable since the leader loop
will handle the error at WAL write time, but it's worth acknowledging.

#### 3. `storeToWAL` no longer checks `n.werr`

The old `storeToWAL` checked `n.werr` at the top. The new version relies on the
caller (`sendAppendEntry`) to check it. This is fine for the leader path where
`sendAppendEntry` checks `n.werr` under `RLock`. But in the **follower** path
(`processAppendEntry`), there's no `werr` check before calling `storeToWAL`:

```go
// Follower path:
n.Unlock()
size, seq, err := n.storeToWAL(ae)
n.Lock()
```

The old code had `n.werr` checked inside `storeToWAL`, providing protection on the
follower path too. The new code removes this safety net. If `n.werr` was set from a
prior write error, the follower will attempt another WAL write that is likely to
fail again (or worse, succeed inconsistently).

**Recommendation**: Either re-add the `werr` check inside `storeToWAL`, or add it
explicitly in the follower's `processAppendEntry` before calling `storeToWAL`.

### Moderate Issues

#### 4. `Histogram` is not thread-safe

The `Histogram` struct uses plain `int` fields and a `[]int` slice with no
synchronization:

```go
type Histogram struct {
    cnt int
    min int
    max int
    bin []int
}
```

`Push()` does non-atomic read-modify-write operations. If `Push` is called
concurrently (e.g., from metrics collection on different goroutines), this is a
data race. Currently it appears `batchHist.Push()` is only called from
`sendAppendEntry`, which the PR constrains to the leader goroutine, so it's likely
safe. But `MarshalJSON()` (called from the metrics query handler on an arbitrary
goroutine) reads the same fields without synchronization.

**Recommendation**: Either add a mutex to `Histogram`, or document clearly that
it must only be accessed from a single writer goroutine and that `MarshalJSON`
reads are inherently racy (approximate). At minimum, use `atomic` operations for
`cnt`/`min`/`max`, or protect reads in `MarshalJSON`/`Query` with the registry's
lock.

#### 5. `Histogram` allocation: `NewHistogram(100000)` allocates a 100K-element slice

```go
n.batchHist = metric.NewHistogram(100000)
```

This allocates an `[]int` of 100,000 elements (~800KB on 64-bit) per Raft group.
For servers with hundreds or thousands of streams/consumers, this adds up quickly.
The histogram is tracking batch sizes, which are bounded by `math.MaxUint16`
(65535), so 100K bins is already oversized.

**Recommendation**: Consider a more space-efficient histogram implementation (e.g.,
logarithmic bucketing, HDR histogram, or a smaller bin count matching `MaxUint16`).
Even `MaxUint16` bins would be ~512KB — consider whether coarser granularity
suffices (e.g., 1024 bins with logarithmic mapping).

#### 6. `FlushAllPending()` locking inconsistency

The PR changes `FlushAllPending()` to:
```go
func (fs *fileStore) FlushAllPending() {
    if fs.fcfg.SyncOnFlush {
        fs.syncBlocks()
    } else {
        fs.mu.Lock()
        defer fs.mu.Unlock()
        fs.checkAndFlushLastBlock()
    }
}
```

`syncBlocks()` handles its own locking, but `fs.fcfg.SyncOnFlush` is read without
holding the lock. If `SyncOnFlush` is always set at construction time and never
mutated, this is fine. But this should be validated.

#### 7. `shouldStore()` behavioral change for `EntryLeaderTransfer`

```go
// Old: return ae != nil && len(ae.entries) > 0
// New: excludes EntryLeaderTransfer from storage
```

This means leader transfer entries are no longer stored in the WAL. The PR
description says this is intentional — leader transfer entries are ephemeral and
don't need persistence. This seems reasonable since they're only meaningful during
the current term, but this is a semantic change that should be explicitly
documented and tested for crash recovery scenarios where a leader transfer was
in-flight.

#### 8. Missing `n.active = time.Now()` update on follower path

In the new follower code:
```go
n.Unlock()
size, seq, err := n.storeToWAL(ae)
n.Lock()
if err != nil { ... return }
n.bytes += size
n.pterm = ae.term
n.pindex = seq
n.active = time.Now()
```

This looks correct. But in the old code, `n.active` was set in
`sendAppendEntryLocked` after `storeToWAL`. In the new code for the leader path,
`n.active` is set inside the re-locked section of `sendAppendEntry`. Both paths
look correct.

### Minor Issues / Style

#### 9. Registry locking during `Query` does JSON marshal under lock

```go
func (r *Registry) Query(path string) ([]byte, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    // ... json.MarshalIndent under lock
}
```

`json.MarshalIndent` of the entire subtree while holding the read lock could be
slow for large metric trees. This could cause contention with metric writers.
Consider building the result map under the lock and marshaling outside it.

#### 10. Commented-out code in `metric.go`

```go
//  maps.Copy(result, mm)
for k, v := range mm {
    result[k] = v
}
```

The commented-out `maps.Copy` should be removed or used. Since `maps.Copy` was
added in Go 1.21 and NATS server likely supports it, this should just use
`maps.Copy`.

#### 11. `Counter` returned by value from `NewCounter()`

```go
func NewCounter() Counter { return Counter{} }
```

Then used as:
```go
n.aeCount = metric.NewCounter()
s.metrics.Add(..., &n.aeCount)
```

Returning a value type containing `atomic.Uint64` is fine since it's assigned to a
field and then addressed. But `Counter` should not be copied after first use (per
`sync/atomic` rules). This is currently safe but fragile — consider returning
`*Counter` from the constructor to make misuse harder.

#### 12. `EntryPeerState` no longer sent directly, now goes through proposal queue

In `AdjustClusterSize()`, `ProposeKnownPeers()`, and `switchToLeader()`, peer
state entries now go through the proposal queue instead of being sent directly.
This means they'll be processed asynchronously by `runAsLeader()`. In
`switchToLeader()`, the code waits for the result:

```go
index, err := n.sendAppendEntry([]*Entry{peerState}, true)
```

But `AdjustClusterSize()` and `ProposeKnownPeers()` just push to the queue and
return. This means the cluster size adjustment is not confirmed before returning —
the caller may assume the peer state was sent, but it's only queued.

#### 13. Error handling in `switchToLeader()` — silent return

```go
index, err := n.sendAppendEntry([]*Entry{peerState}, true)
if err != nil {
    return
}
```

If `sendAppendEntry` fails during `switchToLeader`, the node silently returns
without stepping down or logging. The node believes it's a leader but failed to
send its initial peer state. Consider stepping down or at minimum logging a
warning.

### Testing

#### 14. `TestNRGSendSnapshotInstallsSnapshot` spawns goroutine without cleanup

```go
go func() {
    n.runAsLeader()
}()
```

This goroutine is launched but there's no mechanism shown to stop it or wait for
it. If `runAsLeader()` blocks or panics, the test could leak or hang. Ensure
there's a cleanup mechanism (e.g., via `n.quit` channel in `t.Cleanup`).

---

## Architecture Assessment

The overall design is sound. The key insight — that WAL writes can be performed
outside the raft mutex since they're serialized by the leader goroutine — is
correct and should significantly reduce lock contention on hot paths. The proposal
queue approach for `EntryPeerState` and `EntrySnapshot` entries is clean.

The `SyncOnFlush` optimization for replicated streams is well-reasoned: since the
stream can recover from snapshot + raft log tail, per-write fsyncs are unnecessary.

The metrics infrastructure is a reasonable first pass, though the histogram memory
usage and thread-safety deserve attention before merging.

## Summary of Recommendations

| # | Severity | Issue |
|---|----------|-------|
| 1 | Medium | Document invariant protecting `sendMembershipChange` index reservation |
| 2 | Low | `werr` stale read in `Propose()` is acceptable but worth a comment |
| 3 | Medium | Follower path missing `werr` check before `storeToWAL` |
| 4 | Medium | `Histogram` not thread-safe for concurrent read (MarshalJSON) + write (Push) |
| 5 | Medium | `Histogram` allocates ~800KB per Raft group |
| 6 | Low | `SyncOnFlush` read without lock in `FlushAllPending` |
| 7 | Low | Document `EntryLeaderTransfer` storage exclusion behavior change |
| 8 | - | Follower `n.active` update looks correct |
| 9 | Low | JSON marshal under registry lock |
| 10 | Nit | Remove commented-out `maps.Copy` |
| 11 | Low | Consider returning `*Counter` from constructor |
| 12 | Low | `AdjustClusterSize` peer state now async |
| 13 | Medium | Silent failure in `switchToLeader` |
| 14 | Low | Test goroutine leak risk |
