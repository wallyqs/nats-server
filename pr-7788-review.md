# Review: PR #7788 — (2.14) [IMPROVED] Filestore error handling

**Author:** MauriceVanVeen
**Resolves:** #7629 (Filesystem sync errors unchecked)
**Scope:** +1,492 −502 across 15 files

---

## Summary

This PR introduces comprehensive write error handling across the filestore, stream, raft, and monitoring layers. The core idea: no IO issue should go unnoticed. When a disk write fails, the error is captured as a sticky `werr` field, the affected stream stops accepting writes, replicated streams transition to observer mode to allow failover, and the healthz endpoint exposes the failure.

The design is solid and the coverage is thorough. Below are findings organized by severity.

---

## Critical Issues

### 1. `raft.setWriteErrLocked`: `n.shutdown()` inserted before `isPermissionError` / `isOutOfSpaceErr` handlers

**File:** `server/raft.go`, `setWriteErrLocked` method

The PR inserts `n.shutdown()` and `assert.Unreachable(...)` **before** the existing `isPermissionError` and `isOutOfSpaceErr` branches:

```go
n.werr = err
n.shutdown()                          // NEW — shuts down the node unconditionally
assert.Unreachable(...)               // NEW

if isPermissionError(err) {           // EXISTING — now effectively dead code
    go n.s.handleWritePermissionError()
}
if isOutOfSpaceErr(err) {             // EXISTING — now effectively dead code
    go n.s.handleOutOfSpace(nil)
}
```

**Problems:**
- `n.shutdown()` sets state to `Closed` unconditionally, so the permission-error and out-of-space handlers that follow are spawned against an already-closed node. `handleWritePermissionError` and `handleOutOfSpace` may not behave correctly.
- If `assert.Unreachable` aborts in test/debug builds (Antithesis SDK), those branches are completely unreachable.
- The old behavior for permission errors was to call `handleWritePermissionError` which disables JetStream system-wide. The old behavior for out-of-space was to call `handleOutOfSpace`. Both are important distinct responses that should still fire before or instead of the generic `shutdown()`.

**Suggestion:** Either (a) move `n.shutdown()` to after the specialized handlers, or (b) restructure so permission/space errors go through their existing handlers and `shutdown()` only applies to other write errors.

---

## Moderate Issues

### 2. `monitorStream` snapshot flush path: `n.Stop()` before `mset.setWriteErr(err)`

**File:** `server/jetstream_cluster.go`, ~line 2636

```go
if err := mset.flushAllPending(); err != nil {
    s.Errorf(...)
    n.Stop()                    // Sets raft state to Closed
    assert.Unreachable(...)
    mset.setWriteErr(err)       // Calls node.StepDown() + node.SetObserver(true) on stopped node
}
```

`setWriteErrLocked` calls `node.StepDown()` and `node.SetObserver(true)`. Since `n.Stop()` has already set the state to `Closed`:
- `StepDown()` returns `errNotLeader` immediately (no-op).
- `SetObserver(true)` mutates fields on a dead node (harmless but pointless).

**Suggestion:** Swap the order — call `mset.setWriteErr(err)` *before* `n.Stop()` so that `StepDown` and `SetObserver` take effect while the node is still alive. This allows a cleaner leadership transition before shutdown.

### 3. `batchGroup.readyForCommit`: flush errors silently reported as `BatchTimeout`

**File:** `server/jetstream_batching.go`, ~line 108

```go
// Before:
b.store.FlushAllPending()
return true

// After:
return b.store.FlushAllPending() == nil
```

When `FlushAllPending()` returns an error, `readyForCommit()` returns `false`. The call site in `stream.go` treats this identically to a timer-fired timeout — sending a `BatchTimeout` advisory and `JSAtomicPublishIncompleteBatchError` to the client.

**Problems:**
- The actual IO error is completely discarded — no logging, no distinct error code.
- Operators will see `BatchTimeout` when the real issue is a failing disk.

**Suggestion:** At minimum log the error. Ideally, propagate it so the call site can send a distinct advisory (e.g., `BatchFlushError`) rather than conflating it with a timeout.

---

## Minor Issues / Observations

### 4. Variable shadowing in `monitor.go` healthz

**File:** `server/monitor.go`, ~line 3683

```go
s, err := acc.lookupStream(stream)  // shadows outer `s` (*Server) with `s` (*stream)
...
if streamWerr := s.getWriteErr(); streamWerr != nil {  // s is *stream here, not *Server
```

This compiles correctly, but the shadowing of `s` (`*Server` → `*stream`) in a 100+ line function is a readability hazard. If someone later adds code in this block expecting `s` to be the server, they'll get surprising behavior.

**Suggestion:** Use a different variable name, e.g. `st` or `mset`, to avoid shadowing.

### 5. `SequenceSet.Encode` signature change is safe

The old `Encode` returned `([]byte, error)` but every code path returned `nil` error. The change to `[]byte` is correct, and all 4 call sites are properly updated.

### 6. `errAlreadyLeader` filtering in `processSnapshot` is correct

```go
if err := mset.processSnapshot(ss, ce.Index); err != nil && err != errAlreadyLeader {
```

This handles the benign race where leadership changes between the `!mset.IsLeader()` check and the `processSnapshot` call. Good fix.

### 7. New test `TestFileStoreSyncBlocksNoErrorOnConcurrentRemovedBlock`

The test verifies that `syncBlocks` doesn't set `werr` when a block is concurrently removed. Uses goroutine coordination with `sync.WaitGroup` and a deliberate `time.Sleep(200ms)` to set up the race. The test logic is sound, though the 200ms sleep makes it slightly fragile under extreme load.

---

## Design Observations

### Strengths

- **Thorough coverage**: Every mutating `fileStore` public API checks `fs.werr` on entry and persists new errors via `setWriteErr`. This is systematic and well-executed.
- **Set-once semantics**: `werr` is never cleared once set. This prevents error thrashing and ensures the first root-cause error is preserved.
- **Two-level error tracking**: Both `msgBlock.werr` and `fileStore.werr` exist, with block errors bubbling up to the filestore level in `syncBlocks`. This is a clean separation.
- **Automatic failover**: Replicated streams step down and enter observer mode, allowing healthy replicas to take over.
- **Healthz integration**: Both stream-level and meta-layer write errors are surfaced through `/healthz`, giving operators visibility.
- **Read operations unaffected**: `FilteredState` and other read-only operations don't check `werr`, correctly allowing reads to continue.

### Design Trade-offs Worth Noting

- **No recovery without restart**: Once any IO error hits, the stream permanently stops accepting writes. This is aggressive but matches the PR's stated goal. Transient IO errors (e.g., momentary NFS hiccup) will require a server restart even if the underlying issue resolves. This is a deliberate choice documented in the PR description.
- **`assert.Unreachable` is Antithesis-specific**: These calls are no-ops in production but may cause failures in Antithesis test environments. Developers unfamiliar with the Antithesis SDK might find these confusing.

---

## Summary Verdict

The PR addresses a real and important gap — silently dropped filesystem errors. The implementation is thorough and well-structured. The main concern is the **ordering issue in `raft.setWriteErrLocked`** where `n.shutdown()` is called before the existing permission-error and out-of-space handlers, effectively making them dead code. The `monitorStream` flush path ordering and the silent error swallowing in `readyForCommit` are secondary concerns worth addressing.
