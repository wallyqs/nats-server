# PR Review Summary: Sourcing Skip for DiscardNewPerSubject and Dedup Rejections

## Original PR (#7896)

The original PR attempted to fix sourcing into streams configured with
`DiscardNewPerSubject` by handling `ErrMaxMsgsPerSubject` in
`processInboundSourceMsg`. When a per-subject limit rejection occurred, the
original code called `retrySourceConsumerAtSeq(si.sseq)` — the same retry
path used for `errLastSeqMismatch`.

## Issues Identified

### 1. Retry Loop on Per-Subject Rejection

The core problem: `retrySourceConsumerAtSeq(si.sseq)` tears down the existing
ephemeral source consumer and creates a brand-new one starting at the **same
sequence** that was just rejected. Since the message at that sequence will
always be rejected (the per-subject slot is full), this creates an infinite
retry loop:

```
reject message at seq N → cancel consumer → create new consumer at seq N → reject again → ...
```

Each cycle involves a full `$JS.API.CONSUMER.CREATE` request, new deliver
subject allocation, subscription setup, and flow control initialization. The
backoff in `setupSourceConsumer` prevents CPU spin but does not prevent the
loop itself. All subsequent messages on other subjects are blocked until the
situation resolves externally (e.g., the full subject's message is deleted).

### 2. Missing Dedup (Duplicate Message ID) Handling

The original PR did not handle `errMsgIdDuplicate` rejections during sourcing.
When a source stream has a shorter dedup window than the destination, a message
ID can be accepted by the source (dedup expired) but rejected by the
destination (dedup still active). Without handling, this falls into the generic
error path which logs a warning and returns `false`, potentially disrupting
source processing.

### 3. Minor: Ambiguous Comment

A comment on the source dedup check (`// check for duplicates`) was ambiguous
— it could refer to message deduplication rather than what it actually checks
(duplicate source stream configurations).

## What Changed

### Fix: Skip Instead of Retry (`server/stream.go`)

```go
} else if errors.Is(err, ErrMaxMsgsPerSubject) || errors.Is(err, errMsgIdDuplicate) {
    return true
}
```

Both `ErrMaxMsgsPerSubject` and `errMsgIdDuplicate` are now handled by
returning `true` — meaning "message processed, advance the sequence." This
skips the rejected message and continues processing the batch. Zero consumer
recreation, zero API requests, zero blocking.

**Why this is correct:**
- **Per-subject rejection:** The message is legitimately undeliverable to this
  destination. Retrying will never succeed. Skipping lets subsequent messages
  on other subjects flow through.
- **Dedup rejection:** The destination already has this message (by ID).
  Skipping is the correct behavior — it's the same as what would happen if the
  message had been rejected during direct publish.

### Tests Added

| Test | Variant | What it validates |
|------|---------|-------------------|
| `TestJetStreamSourcingDedupWithoutDiscardNew` | Single + Cluster | Dedup skip works without DiscardNewPerSubject |
| `TestJetStreamSourcingDiscardNewPerSubjectMixedSubjects` | Single + Cluster | Per-subject rejection on one subject doesn't block others |
| `TestJetStreamSourcingDiscardNewPerSubjectWithDedup` | Single + Cluster | Combined per-subject + dedup rejections both skip correctly |
| `TestJetStreamSourcingDiscardNewPerSubjectNoRetryLoop` | Single | Burst of rejections on same subject doesn't cause retry loop; new subject arrives promptly |

### Raft Fix (Separate Concern, `server/raft.go`)

Entries from previous terms are no longer counted toward commit advancement.
This prevents a scenario where a leader commits entries that could still be
overwritten by a leader from an intermediate term it never observed. Existing
raft tests were updated to match the corrected term-checking behavior.

## Key Difference from Original PR

| Aspect | Original PR #7896 | This PR |
|--------|-------------------|---------|
| `ErrMaxMsgsPerSubject` handling | `retrySourceConsumerAtSeq(si.sseq)` | `return true` (skip) |
| `errMsgIdDuplicate` handling | Not handled | `return true` (skip) |
| Consumer recreation on rejection | Yes — infinite loop | No — zero overhead |
| Messages on other subjects | Blocked until external resolution | Processed immediately |
| Tests | None | 7 tests (4 single-server + 3 clustered) |
