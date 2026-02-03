# Review of PR #7790: [FIXED] Reuse msg/hdr from pooled jsPubMsg

**Author:** MauriceVanVeen
**Branch:** maurice/hdr-buf → main

## Summary

This PR fixes a long-standing bug in `jsPubMsg.returnToPool()` where `pm.hdr` and
`pm.msg` were set to `nil` before the truncation checks, making the subsequent
`pm.hdr = pm.hdr[:0]` dead code. This defeated the purpose of the sync.Pool by
preventing backing-array reuse for headers. The PR also adds missing
`returnToPool()` calls in several consumer code paths.

---

## Detailed Analysis

### 1. Core Fix: `returnToPool()` in `server/stream.go`

**The Bug (current code):**
```go
pm.subj, pm.dsubj, pm.reply, pm.hdr, pm.msg, pm.o = _EMPTY_, _EMPTY_, _EMPTY_, nil, nil, nil
if len(pm.buf) > 0 {
    pm.buf = pm.buf[:0]
}
if len(pm.hdr) > 0 {   // DEAD CODE: pm.hdr is nil, len(nil) == 0
    pm.hdr = pm.hdr[:0] // Never executes
}
```

Line 6803 sets `pm.hdr = nil`. The subsequent check `len(pm.hdr) > 0` on line 6807
is always false, so `pm.hdr = pm.hdr[:0]` never executes. The hdr backing array is
lost every time an object is returned to the pool.

This means in `newJSPubMsg`, the reuse path:
```go
if hdr != nil {
    hdr = append(m.hdr[:0], hdr...)
}
```
always does `append(nil, hdr...)` which allocates a new slice every time, completely
negating the pool optimization for headers.

**The Fix:**
```go
pm.subj, pm.dsubj, pm.reply, pm.o = _EMPTY_, _EMPTY_, _EMPTY_, nil
if len(pm.buf) > 0 {
    pm.buf = pm.buf[:0]
}
if len(pm.hdr) > 0 {
    pm.hdr = pm.hdr[:0]
}
if len(pm.msg) > 0 {
    pm.msg = pm.msg[:0]
}
```

Removes `pm.hdr` and `pm.msg` from the nil assignment. Both are now properly
truncated to zero length while preserving their backing arrays. This is correct and
consistent with how `pm.buf` was already handled.

**Verdict:** Correct and important fix. This is the core value of the PR.

**One subtlety to be aware of:** After `LoadMsg`, `hdr` is typically set as
`buf[0:hdrLen:hdrLen]` (a sub-slice of `buf` with capped capacity — see
`StoreMsg.copy()` at `store.go:745`). After `returnToPool`, `pm.hdr[:0]` retains
that capped capacity. In `newJSPubMsg`, `append(m.hdr[:0], newHdr...)` will only
reuse the backing array if the new header fits within the old header's capacity. If
the new header is larger, `append` allocates anyway. This limits the optimization's
effectiveness when header sizes vary significantly, but should work well for the
common case where headers are similar size.

**Also note:** Since `hdr` and `buf` often share the same backing array (via
`LoadMsg`), `append(m.hdr[:0], ...)` in `newJSPubMsg` writes into `m.buf`'s backing
array. After the struct assignment, `pm.hdr` and `pm.buf` overlap in the same
backing array. This is safe in current usage because nothing appends to `buf` in the
`newJSPubMsg` path — the message is sent and then returned to the pool. But it's a
subtle invariant worth documenting.

---

### 2. `releaseAnyPendingRequests()` in `server/consumer.go`

**Before:**
```go
var hdr []byte
if !isAssigned {
    hdr = []byte("NATS/1.0 409 Consumer Deleted\r\n\r\n")
}
// ...loop...
if hdr != nil {
    o.outq.send(newJSPubMsg(wr.reply, _EMPTY_, _EMPTY_, hdr, nil, nil, 0))
}
```

**After:**
```go
// ...loop...
if !isAssigned {
    hdr := []byte("NATS/1.0 409 Consumer Deleted\r\n\r\n")
    o.outq.send(newJSPubMsg(wr.reply, _EMPTY_, _EMPTY_, hdr, nil, nil, 0))
}
```

Each iteration now gets its own `hdr` allocation. This is defensive: with the
`returnToPool` fix enabling actual buffer reuse, each `newJSPubMsg` call's
`append(m.hdr[:0], hdr...)` reads from the caller's `hdr` (safe — read only). The
result is stored in the pool item's backing buffer. There isn't a data race with the
shared `hdr` variable in the old code, but the new per-iteration allocation is
cleaner and avoids any future subtle aliasing issues.

**Verdict:** Correct. Simplifies the control flow (replaces `if hdr != nil` with
`if !isAssigned`). The per-iteration allocation is a minor efficiency trade-off on a
cold path (consumer deletion/stop only), which is acceptable.

---

### 3. Missing `returnToPool()` calls in `server/consumer.go`

The PR adds `pmsg.returnToPool()` before existing `pmsg = nil` assignments in
several locations. These are places where the code was setting `pmsg = nil`
(abandoning the reference) without first returning the pool object, causing pool
object leaks:

#### a) `getNextMsg()` — redelivery loop (around line 4437)
Adds `pmsg.returnToPool()` + `pmsg = nil` before `continue` in the
`ErrStoreMsgNotFound || errDeletedMsg` branch.

**Note:** When `err != nil`, the preceding block (lines 4432-4437) already calls
`returnToPool()` and sets `pmsg = nil`. So the added call is redundant in the common
case (the nil check in `returnToPool` makes the second call a no-op). This is
defensive coding for the theoretical edge case where `sm != nil && err != nil`. This
is fine but could be noted with a comment.

#### b) `getNextMsg()` — skip-list path (around line 4466)
Adds `pmsg.returnToPool()` before existing `pmsg = nil` in the `LoadMsg` error path.

**This is a real bug fix.** Without it:
1. `pmsg.returnToPool()` puts the object back in the pool (wait — actually looking
   at the current 2.14 code, `returnToPool()` is already called here at line 4470,
   but `pmsg = nil` is missing)
2. `return pmsg, 1, err` returns the now-pooled pointer to the caller
3. The caller has a pointer to a freed pool object (use-after-free)

The PR adds the missing `pmsg = nil` so the caller correctly receives nil on error.

#### c) `loopAndGatherMsgs()` — delivery failure path (around line 4978)
Adds `pmsg.returnToPool()` before `pmsg = nil` + `goto waitForMsgs`.

**Pool leak fix.** Without `returnToPool()`, the pool object is abandoned when the
code jumps to `waitForMsgs`.

#### d) `loopAndGatherMsgs()` — `qch` select cases (around lines 4989, 5010)
Adds `pmsg.returnToPool()` before `pmsg = nil` + `return` in two rate-limiting
select cases where the quit channel fires.

**Pool leak fix.** Same pattern — the pool object was abandoned on early return.

**Verdict:** All correct. These fix real pool object leaks and a potential
use-after-free in the skip-list path.

---

## Questions / Suggestions

### 1. Is `pm.msg[:0]` preservation necessary?

In `newJSPubMsg`, only `buf` and `hdr` are explicitly reused from the pool. `msg` is
passed through from the caller. And in the `getJSPubMsgFromPool()` → `LoadMsg()`
path, `StoreMsg.clear()` always nils `hdr` and `msg` before loading:

```go
// store.go:754
*sm = StoreMsg{_EMPTY_, nil, nil, sm.buf, 0, 0}
```

So the preserved `pm.msg[:0]` is overwritten in both code paths. The `msg`
preservation doesn't provide reuse benefit and holds a reference to the previous
message's data in the pool. While harmless (sync.Pool is GC-friendly), setting
`pm.msg = nil` would be slightly cleaner for GC by releasing the reference sooner.
That said, this is a very minor point and the consistent treatment of all slice
fields (`buf`, `hdr`, `msg`) has a code clarity benefit.

### 2. Consider reusing `msg` in `newJSPubMsg` too?

Currently `newJSPubMsg` only reuses `buf` and `hdr` from the pool. If `msg` were
also reused via `append(m.msg[:0], msg...)` (similar to how `hdr` is handled), it
could save allocations in callers that pass a `msg` parameter. This is a follow-up
optimization opportunity, not a requirement for this PR.

### 3. `hdr` capacity limitation

Since `StoreMsg.copy()` caps `hdr` capacity at the header length
(`buf[:hdrLen:hdrLen]`), the preserved `m.hdr[:0]` in the pool has limited capacity.
Headers in the `releaseAnyPendingRequests` path are 33 bytes; typical NATS headers
might be larger or smaller. The reuse only works when the new header fits within the
old capacity. Consider whether the `hdr` in `newJSPubMsg` should fall back to using
`m.buf[:0]` instead when `m.hdr` has insufficient capacity, to get the full buffer's
capacity for header reuse.

---

## Overall Assessment

**The PR is correct and fixes real bugs:**

1. A long-standing dead-code bug in `returnToPool()` that prevented header buffer
   reuse in the sync.Pool — this is the main fix
2. Several pool object leaks in `consumer.go` where objects were abandoned without
   being returned to the pool
3. A potential use-after-free in the skip-list path of `getNextMsg()` where a freed
   pool object was returned to the caller

The changes are well-targeted and minimal. The `releaseAnyPendingRequests` refactor
is a reasonable defensive change. The code is correct and the risk of regression is
low.

**Recommendation:** Approve with minor suggestion to consider whether `pm.msg[:0]`
preservation is needed (vs. `pm.msg = nil`), since `msg` is not reused from the pool
in either the `newJSPubMsg` or `LoadMsg` paths.
