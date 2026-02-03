# Code Review: PR #7792 — Consumer inactivity threshold in clustered mode should wait for metalayer

**PR:** https://github.com/nats-io/nats-server/pull/7792
**Author:** neilalexander
**File changed:** `server/consumer.go` (+66 −49)
**Status:** Draft

## Summary

This PR refactors `deleteNotActive()` in `server/consumer.go` to prevent premature
local deletion of consumers in clustered mode. The key change is that a consumer
should **not** be deleted locally until the metalayer has achieved quorum on the
delete proposal. Previously, `defer o.delete()` ensured the consumer was always
deleted locally regardless of metalayer outcome, which could lead to ghost consumers
or data loss.

## Detailed Analysis of Changes

### 1. Removal of `defer o.delete()` (core fix)

**Old behavior:** `defer o.delete()` at the top meant the consumer was unconditionally
deleted locally, even if the metalayer never processed the proposal.

**New behavior:** `o.delete()` is only called in specific, safe paths:
- Non-clustered / direct consumer: delete immediately (safe, no metalayer needed)
- No consumer assignment exists: delete locally (nothing to coordinate)
- Assignment was replaced or removed AND consumer not yet closed: forcible cleanup with a warning
- Consumer's quit channel fires (`oqch`): consumer was already stopped by the metalayer processing the proposal

This is the right approach. The old `defer o.delete()` was fundamentally unsafe in a
clustered environment because it could delete the local consumer state before the
cluster had consensus.

### 2. New `oqch` monitoring (`consumer.go:2045`)

The PR captures `oqch := o.qch` while holding `o.mu.Lock()`, then monitors it in the
retry loop. When the metalayer processes the delete proposal and the consumer is stopped
normally, `o.qch` is closed (in `stopWithFlags` at line 6075-6077), which causes this
goroutine to exit cleanly via `<-oqch`. This avoids unnecessarily retrying after the
consumer has already been properly cleaned up.

### 3. Guard clauses replacing nested conditionals (`consumer.go:2061-2083`)

The restructuring into early returns for `cc == nil`, `meta == nil`, and `ca == nil`
improves readability. However, there are concerns about the `cc == nil` and `meta == nil`
paths (see Issues below).

### 4. Retry mechanism: `time.After` replaces `Ticker` (`consumer.go:2090-2130`)

**Old:** Ticker-based loop that waits, checks, then calls `ForwardProposal` on retry.
**New:** Calls `ForwardProposal` at the START of each iteration, then waits with
`time.After(duration)`.

This means the very first proposal is sent immediately when entering the loop, rather
than waiting for the first tick. This is arguably better since it avoids an unnecessary
initial delay.

**Minor concern — `time.After` timer leak:** Each loop iteration allocates a `time.After`
timer. If the loop exits via `qch`, `cqch`, or `oqch` before the timer fires, the
underlying timer is not cleaned up until it expires. Given the durations (30s-5min), this
is a minor leak but not practically significant. If thoroughness is desired,
`time.NewTimer` + `defer timer.Stop()` would prevent it.

### 5. Changed assignment comparison (`consumer.go:2114`)

**Old:** `reflect.DeepEqual(nca, ca)` — value equality comparing the freshly-fetched
assignment against the one captured at function start.

**New:** `nca != o.consumerAssignment()` — pointer comparison against the consumer's
live `o.ca` field.

This is a semantic change:
- Old check: "Is the assignment structurally identical to what we captured?"
- New check: "Is the assignment the same object the consumer currently holds?"

The pointer comparison should work correctly because `js.consumerAssignment()` returns
a pointer into the JS state map, and `o.consumerAssignment()` returns `o.ca`. If a new
consumer with the same name is created, it gets a fresh `*consumerAssignment` pointer.
Note that `o.consumerAssignment()` acquires `o.mu.RLock()` internally while `nca` was
fetched under `js.mu.RLock()` — there is a window between releasing `js.mu` and
acquiring `o.mu` where `o.ca` could change. This should not cause correctness issues
(worst case: one extra retry iteration).

### 6. Backoff calculation (`consumer.go:2129`)

**Old:** `interval *= 2` with a check `if interval < cnaMax`.
**New:** `duration = min(duration*2, cnaMax)`.

Cleaner and functionally equivalent.

## Issues Found

### Bug: Possible infinite retry when consumer is closed but assignment still exists

At line 2114-2120 (in the new code):
```go
if nca == nil || (nca != o.consumerAssignment() && !o.isClosed()) {
    s.Warnf("Consumer '%s > %s > %s' forcibly cleaned up due to assignment change", acc, stream, name)
    o.delete()
    return
}
```

If `nca == o.consumerAssignment()` (assignment unchanged) but `o.isClosed()` is true,
we fall through to the retry. The consumer is already closed, but we will keep logging
"not cleaned up, retrying" and calling `ForwardProposal` indefinitely.

In practice this should not happen because a closed consumer would have its `qch` closed,
causing `oqch` to fire in the select. But if there is any edge case where `o.qch` was nil
when captured (e.g., consumer was not fully initialized), this could spin forever.

### Bug: Typo in comment

Line with `ca == nil` check: **"had might as well"** should be **"might as well"**.

### Design concern: Silent return on `cc == nil` / `meta == nil`

When `cc == nil` or `meta == nil`, the function returns without deleting the consumer
and without logging a warning. An inactive ephemeral consumer that triggers
`deleteNotActive` but hits this path will silently remain alive. Consider:
- Logging a warning so operators have visibility into the situation
- Whether the `dtmr` will re-fire and retry later (it will not — the timer was already consumed)

This could leave ghost consumers that are inactive but never cleaned up until the server
restarts.

## Assessment Summary

| Aspect | Assessment |
|--------|------------|
| Core fix (no premature delete) | Sound and addresses a real safety issue |
| Guard clause restructuring | Improves readability |
| `oqch` monitoring | Good addition for clean exit |
| Ticker to `time.After` | Fine, minor timer leak (acceptable) |
| Pointer vs DeepEqual comparison | Reasonable change, slightly different semantics |
| Edge case: closed consumer + unchanged assignment | Low risk but theoretically could loop |
| Silent early returns on missing cc/meta | Could leave ghost consumers without logging |
| Typo: "had might as well" | Minor fix needed |

Overall this is a well-motivated change that fixes a real safety issue. The main items
to address are:
1. The silent early returns when `cc`/`meta` is nil (consider at minimum a warning log)
2. The typo ("had might as well" -> "might as well")
3. The theoretical edge case around closed-but-not-quit consumers
