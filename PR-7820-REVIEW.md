# Code Review: PR #7820 — [FIXED] Race condition when remapping group

**Author:** MauriceVanVeen
**Branch:** `maurice/remove-node-race` → `main`
**Files changed:** `server/jetstream_cluster.go`, `server/jetstream_cluster_2_test.go`

---

## Summary

This PR fixes a race condition in two code paths within `processClusterUpdateStream()` where `removeNode()` is called without waiting for the monitor goroutine (`monitorStream`) to fully exit. The fix adds `signalMonitorQuit()` + `monitorWg.Wait()` after `removeNode()` in both locations, consistent with the established pattern used in 3 other places in the codebase.

## The Race Condition

### Root Cause

In `processClusterUpdateStream()` (`jetstream_cluster.go`), there are two paths that call `mset.removeNode()` without ensuring the `monitorStream` goroutine has fully exited:

1. **Stream remapping** (~line 4500): When the Raft group name changes (`osa.Group.Name != sa.Group.Name`), the old node is removed and a new monitor is immediately started.

2. **R1 downgrade** (~line 4537): When replicas are reduced to 1 (`numReplicas == 1 && alreadyRunning`), the node is removed but the monitor is not properly stopped.

### How the Race Manifests

**Remap case (most critical):**

1. `removeNode()` calls `n.Delete()`, which closes the Raft node's quit channel (`qch`)
2. The old `monitorStream` goroutine receives `<-qch` and begins returning
3. But deferred cleanups haven't executed yet — specifically `clearMonitorRunning()` and `monitorWg.Done()`
4. The code immediately proceeds to create a new Raft group and start a new `monitorStream` goroutine
5. The new `monitorStream` calls `checkInMonitor()` which sees `inMonitor == true` (old monitor hasn't cleared it yet) and **immediately returns**
6. **Result:** No monitor running for the stream — the stream becomes unmonitored

**R1 downgrade case:**

1. `removeNode()` deletes the Raft node
2. The code proceeds to `stopClusterSubs()` and `clearAllCatchupPeers()` while the monitor goroutine may still be accessing shared state
3. **Result:** Potential data race on stream state

## The Fix

The fix adds two lines after each `removeNode()` call:

```go
mset.removeNode()
mset.signalMonitorQuit()  // Close the monitor's quit channel (mqch)
mset.monitorWg.Wait()     // Block until the monitor goroutine fully exits
```

This ensures:
- `signalMonitorQuit()` closes `mqch` as a secondary exit signal (belt-and-suspenders alongside `qch` already being closed by `removeNode()`)
- `monitorWg.Wait()` blocks until the old monitor has fully exited, including all deferred cleanups (`clearMonitorRunning()`, `monitorWg.Done()`, `n.Stop()`, drain of apply queue, etc.)

### Established Pattern

This exact `signalMonitorQuit()` + `monitorWg.Wait()` pattern is already used correctly in 3 other locations:

| Location | Code Path |
|----------|-----------|
| `jetstream_cluster.go:3272-3273` | Stream reset (after `node.Delete()`) |
| `jetstream_cluster.go:4470-4471` | Stream removal during non-shutdown |
| `jetstream_cluster.go:4971-4972` | Stream deletion (`processClusterRemoveStream`) |

The PR correctly adds it to the 2 remaining locations where it was missing.

## Test Changes

The PR adds a block to `TestJetStreamClusterReplicasChangeStreamInfo` that blocks metadata snapshots on all cluster servers before the R3→R1 replica downgrade. This is done by:

- Accessing the meta Raft group from each server
- Creating artificial progress entries that prevent snapshot installation
- This forces log replay during the replica change, widening the race window

This is a sound testing approach — by preventing snapshots, the Raft log stays large and the monitor goroutine spends more time in `applyStreamEntries`, increasing the likelihood of the race manifesting.

## Observations and Minor Concerns

### 1. `mqch` is not recreated after remap (minor, not a bug)

After `signalMonitorQuit()` sets `mset.mqch = nil`, the new monitor (in the remap case) starts with `mqch = nil`. A nil channel in a select blocks forever, so the `<-mqch` case becomes dead code for the new monitor. The new monitor can still exit via:
- `<-s.quitCh` (server shutdown)
- `<-qch` (new Raft node quit)

All existing shutdown paths delete/stop the Raft node before signaling `mqch`, so this is not a functional issue. However, it means any **future** code path that relies solely on `signalMonitorQuit()` to stop a remapped stream's monitor would silently fail to signal it.

**Suggestion:** Consider recreating `mqch` after the remap wait completes, before starting the new monitor. For example:

```go
mset.mu.Lock()
mset.mqch = make(chan struct{})
mset.mu.Unlock()
```

This would restore full functionality of all exit signal paths for the new monitor. However, this is a pre-existing concern (not introduced by this PR) and could be addressed separately.

### 2. Ordering: `removeNode()` before `signalMonitorQuit()`

The PR calls `removeNode()` first, then `signalMonitorQuit()`. This means the monitor may receive the node's `qch` signal before the `mqch` signal. Looking at the select cases:

```go
case <-mqch:
    if s.isShuttingDown() { doSnapshot() }
    return
case <-qch:
    // Raft node is closed, no use in trying to snapshot.
    return
```

The `<-qch` path skips snapshotting (correct for remap/downgrade — we don't want to snapshot when the node is being intentionally removed). The `<-mqch` path only snapshots during shutdown. Since Go select picks randomly among ready cases when multiple are ready, the monitor may exit via either path. Both are correct for this use case — neither will attempt to snapshot (since the server is not shutting down). This ordering is consistent with all other call sites.

## Verdict

**Approve.** The fix is correct, minimal, and follows the established pattern. It addresses a real race condition that could leave streams unmonitored (remap case) or cause data races on shared state (R1 downgrade case). The test addition is well-designed to reproduce the race.
