# PR Review Summary: Fix Dedup Window During Sourcing

## Background: Intentional Backpressure via Retry

When sourcing into a stream configured with `DiscardNew` and a stream-level
limit (`MaxMsgs`/`MaxBytes`), the existing retry behavior is **intentional
backpressure**. The source consumer is torn down and recreated later,
effectively pausing sourcing until consumers drain messages from the
destination stream. Once room opens up, sourcing resumes automatically. This is
a production pattern used for work-queue/interest streams where you want to
limit the sourcing stream's size while consumers catch up.

**This backpressure behavior is correct and must not be changed.** The same
applies when `DiscardNewPerSubject` causes per-subject limit rejections — the
blocking behavior is consistent, and changing it requires an opt-in mechanism
(out of scope for this PR).

## The Bug: Broken Dedup Window During Sourcing

When a source stream has a shorter dedup window than the destination, a message
ID can be accepted by the source (dedup expired) but rejected by the
destination (dedup still active). Without explicit handling, `errMsgIdDuplicate`
falls into the generic error path which logs a warning and triggers a retry.
This is wrong — the message is genuinely a duplicate and should simply be
skipped.

Unlike `DiscardNew` backpressure (where room may open up later), a dedup
rejection is permanent for the life of the dedup window. Retrying will never
succeed for that message ID, so skipping is the only correct behavior.

## What This PR Changes

### Fix: Skip on Dedup Rejection (`server/stream.go`)

```go
} else if errors.Is(err, errMsgIdDuplicate) {
    // Duplicate message ID detected during sourcing. The destination
    // already has this message by ID within its dedup window. Skip
    // the message and continue processing.
    return true
}
```

The destination already has this message by ID. Skipping is correct — same as
direct-publish dedup behavior.

### Tests Added

| Test | Variant | What it validates |
|------|---------|-------------------|
| `TestJetStreamSourcingDedupWithoutDiscardNew` | Single-server | Dedup skip works: duplicate msg ID skipped, next message sourced |
| `TestJetStreamClusterSourcingDedupWithoutDiscardNew` | 3-node cluster | Same behavior verified in clustered mode |

### Raft Fix (Separate Concern, `server/raft.go`)

Entries from previous terms are no longer counted toward commit advancement.
This prevents a scenario where a leader commits entries that could still be
overwritten by a leader from an intermediate term it never observed. Existing
raft tests were updated to match the corrected term-checking behavior.

## Out of Scope (Discussed but Not Included)

**Skip on `DiscardNewPerSubject`**: When sourcing into a stream with
`DiscardNewPerSubject`, a per-subject limit rejection blocks sourcing of all
subjects (including those with available capacity). Skipping would allow other
subjects to continue flowing. However, this changes existing (albeit
undocumented) behavior and was deemed to require an opt-in mechanism via a new
source configuration flag. This is tracked separately from the dedup fix.
