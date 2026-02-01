# Review: PR #7783 — [FIXED] Filestore race if rebuild during recovery

**Author:** MauriceVanVeen
**Branch:** `maurice/fs-rebuild-race` → `main`
**Files changed:** `server/filestore.go` (+11, -11)

## Summary

The PR moves the `fs.mu.Lock()` acquisition in `newFileStoreWithCreated` from line 556
(just before enforcement/removal operations) to line 437 (immediately after the `fileStore`
struct is created). The intent is to hold the lock during the entire recovery process,
not just the enforcement phase.

The comment is changed from:
```
// Lock while we do enforcements and removals.
```
to:
```
// Lock during the whole recovery.
```

## Critical Issue: Deadlock

**This PR will deadlock on every call to `newFileStoreWithCreated`.** Moving `fs.mu.Lock()`
to line 437 means the lock is held when the following functions are called:

1. **`recoverFullState()`** at line 493 — internally calls `fs.mu.Lock()` at
   `filestore.go:1764`
2. **`recoverMsgs()`** at line 508 — internally calls `fs.mu.Lock()` at
   `filestore.go:2273`

Go's `sync.RWMutex` is **not reentrant**. Calling `Lock()` on the same goroutine that
already holds the lock will deadlock immediately. This means every filestore creation
will hang indefinitely.

### Required fix

To make the PR's intent work, `recoverFullState()` and `recoverMsgs()` must be changed
to **not acquire the lock themselves**. They should become "lock should be held on entry"
functions (similar to how `recoverTTLState` and `recoverMsgSchedulingState` are already
annotated). Specifically:

- Remove `fs.mu.Lock()` / `defer fs.mu.Unlock()` from `recoverFullState()` (lines 1764-1765)
- Remove `fs.mu.Lock()` / `defer fs.mu.Unlock()` from `recoverMsgs()` (lines 2273-2274)
- Add `// Lock should be held.` comments to both functions
- Update the test at `filestore_test.go:8518` which calls `fs.recoverFullState()` directly —
  it would need to hold `fs.mu.Lock()` before calling

## The Underlying Problem

The PR description states that `recoverMsgBlock` may trigger a rebuild without the
filestore lock, creating a race on `fs.state.LastSeq`. However, analyzing the current
code:

- `recoverMsgBlock` (line 1148) is annotated `// Lock held on entry` and is only called
  from `recoverMsgs()` which holds `fs.mu`
- `recoverFullState()` holds `fs.mu` when it calls `mb.rebuildState()` at lines 1986
  and 2029
- `mb.rebuildState()` only acquires `mb.mu` (the message block lock), not `fs.mu`, and
  only operates on `mb.*` fields

So during the actual recovery functions, `fs.state` access is already protected. The
**real unprotected window** is in `newFileStoreWithCreated` at lines 498-531, between
`recoverFullState()` releasing its lock and the lock acquisition at line 556. In this
window:

- Line 501: `fs.state = StreamState{}` — reset without lock
- Line 502: `fs.psim`, `fs.tsl` modified without lock
- Line 503-505: `fs.bim`, `fs.blks`, `fs.tombs` modified without lock
- Line 514: `prior.LastSeq > fs.state.LastSeq` read without lock

However, since `fs` has not been returned from the constructor yet, it's unclear who
the concurrent accessor would be. The `ats.Register()` call at line 438 only starts a
global time-tracking goroutine that never accesses the filestore.

## Pre-existing Bugs This Would Fix (if deadlock is resolved)

The PR's approach of holding the lock earlier would fix two pre-existing issues where
functions are called without the lock they require:

1. **`recoverTTLState()`** (line 536) — has comment `// Lock should be held.` at
   line 2050 but is called without `fs.mu` held
2. **`recoverMsgSchedulingState()`** (line 543) — has comment `// Lock should be held.`
   at line 2131 but is called without `fs.mu` held

Both of these are correctly called under lock in `UpdateConfig()` (lines 694, 700) where
`fs.mu` is held from line 676, but are NOT under lock in `newFileStoreWithCreated`.

## Missing Test

The PR has no test demonstrating the race condition. A test using Go's race detector
(`-race` flag) that exercises the recovery path with concurrent access would help
validate the fix and prevent regressions.

## Recommendations

1. **Fix the deadlock** by removing the internal lock acquisition from `recoverFullState()`
   and `recoverMsgs()`, making them "lock should be held on entry" functions
2. **Update the test** at `filestore_test.go:8518` to hold the lock before calling
   `recoverFullState()` directly
3. **Add a race test** that demonstrates the original problem
4. **Clarify the race scenario** in the PR description — identify what concurrent
   accessor can reach `fs.state` during construction, since the filestore hasn't been
   returned yet
