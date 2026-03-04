# Review of PR #7882 — Stream backup/snapshot and restore v2

Reference: https://github.com/nats-io/nats-server/pull/7882

## Summary

PR #7882 introduces a V2 stream snapshot and restore mechanism. This review
focuses on two data races identified and fixed in commit e888eb3, and checks
whether the PR addresses them. It also flags new potential issues.

## Bug 1: `Consumers()` iterator uses wrong lock

**Status: Fixed in PR #7882**

The `Consumers()` method on `fileStore` iterates `fs.cfs`, which is guarded by
`fs.cmu`. However, the original code acquired `fs.mu` (the main filestore
mutex) instead. Since `AddConsumer` and other mutations of `fs.cfs` use
`fs.cmu`, acquiring `fs.mu` provides no synchronization — resulting in a data
race under `-race`.

The PR correctly uses `fs.cmu.RLock()`:

```go
func (fs *fileStore) Consumers() iter.Seq[ConsumerStore] {
    return func(yield func(ConsumerStore) bool) {
        fs.cmu.RLock()
        defer fs.cmu.RUnlock()
        for _, v := range fs.cfs {
            if !yield(v) {
                return
            }
        }
    }
}
```

## Bug 2: `state.Consumers = 0` race in `streamSnapshotV2`

**Status: Fixed in PR #7882**

`CreateStreamSnapshotV2` populates a `StreamState` on the stack and passes a
pointer to the snapshot goroutine, then immediately returns it by value in
`SnapshotResult`. Writing `state.Consumers = 0` through the pointer in the
goroutine races with the caller reading `state` on return.

The PR creates a local copy and mutates that instead:

```go
var streamState = *state
if !includeConsumers {
    streamState.Consumers = 0
}
```

Only `streamState` (the local copy) is marshaled into the tar archive. The
caller's `state` is never touched from the goroutine.

## New potential issues

### 1. Message loading loop may fail on deleted trailing messages

```go
for seq := state.FirstSeq - 1; seq < state.LastSeq; {
    if _, seq, err = store.LoadNextMsg(fwcs, true, seq+1, &sm); err != nil {
        errCh <- fmt.Sprintf("couldn't load next message after seq %d: %s", seq+1, err)
        return
    }
}
```

If messages at the tail of the stream have been deleted, `LoadNextMsg` may
return `ErrStoreEOF` while `seq < state.LastSeq` still holds, causing the
snapshot to abort with a misleading error. Additionally, `seq` is reassigned by
`LoadNextMsg` even on error, so the error message may report the wrong sequence
number.

### 2. Restore relies on exact consumer count from `state.json`

```go
for range nstate.Consumers {
    hdr, err := tr.Next()
    ...
    name, found := strings.CutPrefix(hdr.Name, "consumers/")
    if !found {
        return nil, fmt.Errorf("expected consumer, found %q", hdr.Name)
    }
```

The restore path reads exactly `nstate.Consumers` tar entries and expects each
to be a `consumers/` entry. If the snapshot goroutine exits early (e.g., a
`sysRequest` failure for one consumer in clustered mode), the archive will be
truncated — fewer consumer entries than the count in `state.json`. The restore
will then attempt to parse a `msgs/` entry (or hit EOF) as a consumer, producing
a confusing error. Checking the tar entry name prefix in a loop (stopping when
it's no longer `consumers/`) would be more robust than relying on the count.

### 3. Minor: `LoadNextMsg` error after seq reassignment

In the message loading loop, `seq` is the second return value from
`LoadNextMsg`. On error, this value may be stale or unexpected. The error
format string references `seq+1` but `seq` has already been overwritten by the
failed call's return. Consider capturing the next-seq-to-load in a separate
variable before the call.
