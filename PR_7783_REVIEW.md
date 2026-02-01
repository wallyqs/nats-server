# Review: PR #7783 — [FIXED] Filestore race if rebuild during recovery

**Author:** MauriceVanVeen
**Branch:** `maurice/fs-rebuild-race` → `main`
**Files changed:** `server/filestore.go` (+15, -11)

## Summary

The PR addresses a race condition in `newFileStoreWithCreated` where `fs.state` fields
are accessed without holding `fs.mu` during certain recovery paths.

## Original PR (initial diff): Deadlock

The initial PR diff moved `fs.mu.Lock()` from line 556 (before enforcement/removal
operations) all the way to line 437 (right after struct creation). This would have
deadlocked because `recoverFullState()` and `recoverMsgs()` both call `fs.mu.Lock()`
internally, and Go's `sync.RWMutex` is not reentrant.

## Updated Commit (21f62d8): Correct Targeted Fix

The merged commit takes a more targeted approach that avoids the deadlock:

1. **Adds a temporary lock** around lines 512-535 — the section after `recoverMsgs()`
   returns where `prior.LastSeq > fs.state.LastSeq` is checked and `fs.state` is modified
   without lock protection. Explicit `fs.mu.Unlock()` calls are added on error return
   paths within this section.

2. **Moves the persistent lock** from just before enforcement operations to just before
   `recoverTTLState()` and `recoverMsgSchedulingState()`. This fixes the pre-existing
   bug where those functions (annotated `// Lock should be held.`) were being called
   without the lock.

This approach avoids the deadlock by never overlapping lock acquisitions with the internal
locks in `recoverFullState()` or `recoverMsgs()`.

## Implementation Applied

This branch implements the same fix as the merged commit 21f62d8:
- Temporary `fs.mu.Lock()`/`Unlock()` around the `prior.LastSeq` check section
- Persistent lock moved earlier to cover `recoverTTLState()` and `recoverMsgSchedulingState()`
- `recoverFullState()` and `recoverMsgs()` are left unchanged (keep their internal locks)

## Test Results

All filestore recovery tests pass with the race detector:
- `TestFileStoreRecoverWithRemovesAndNoIndexDB`
- `TestFileStoreRecoverFullStateDetectCorruptState`
- `TestFileStoreRecoverOnlyBlkFiles` (6 sub-tests)
- `TestFileStoreRecoverAfterRemoveOperation` (42 sub-tests)
- `TestFileStoreRecoverAfterCompact` (12 sub-tests)
- `TestFileStoreRecoverWithEmptyMessageBlock` (6 sub-tests)
- `TestFileStoreRecoverDoesNotResetStreamState` (6 sub-tests)
