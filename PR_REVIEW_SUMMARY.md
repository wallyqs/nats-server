# PR Review Summary: Sourcing Skip for DiscardNewPerSubject and Dedup Rejections

## Background: Intentional Backpressure via Retry

When sourcing into a stream configured with `DiscardNew` and a **stream-level**
limit (`MaxMsgs`), the current retry behavior is **intentional backpressure**.
The source consumer is torn down and recreated later, effectively pausing
sourcing until consumers drain messages from the destination stream. Once room
opens up, sourcing resumes automatically. This is a production pattern used for
work-queue/interest streams where you want to limit the sourcing stream's size
while consumers catch up.

**This backpressure behavior must not be changed.** Our fix does not touch it —
we only handle `ErrMaxMsgsPerSubject` (per-subject limit), not `ErrMaxMsgs`
(stream-level limit).

## Original PR (#7896)

The original PR fixed sourcing into streams configured with
`DiscardNewPerSubject` by skipping messages that hit the per-subject limit
rather than blocking. It also extended the fix to handle duplicate message IDs
(`errMsgIdDuplicate`), which fixes currently broken dedup-window behavior
during sourcing. The per-subject skip was made opt-in via a new boolean flag in
the source configuration for safer mergeability.

## Why Per-Subject Skip Is Different from Stream-Level Blocking

Stream-level `DiscardNew` blocking is correct: the entire stream is full, so
pausing sourcing until room opens is the right thing to do.

Per-subject `DiscardNewPerSubject` blocking is problematic: only **one
subject** is full, but messages on **other subjects** still have room. The
retry mechanism (`retrySourceConsumerAtSeq(si.sseq)`) recreates the consumer
starting at the rejected sequence. Since the per-subject slot is still full,
the same message gets rejected again, blocking all subsequent messages —
including those on subjects with available capacity.

```
reject msg on foo.1 (full) → cancel consumer → recreate at same seq → reject again → ...
meanwhile foo.2, foo.3, etc. are blocked even though they have room
```

## What This PR Changes

### 1. Skip on Per-Subject Rejection and Dedup (`server/stream.go`)

```go
} else if errors.Is(err, ErrMaxMsgsPerSubject) || errors.Is(err, errMsgIdDuplicate) {
    return true
}
```

- **`ErrMaxMsgsPerSubject`:** The per-subject slot is full. Retrying will never
  succeed (unlike stream-level, where consumers can drain room). Skip the
  message so other subjects continue flowing.
- **`errMsgIdDuplicate`:** The destination already has this message by ID.
  Skipping is correct — same as direct-publish dedup behavior. This fixes
  currently broken dedup-window handling during sourcing.

**What is NOT changed:** Stream-level `DiscardNew` (`ErrMaxMsgs`) still goes
through the existing error path. The intentional backpressure behavior is
preserved.

### 2. Tests Added

| Test | Variant | What it validates |
|------|---------|-------------------|
| `TestJetStreamSourcingDedupWithoutDiscardNew` | Single + Cluster | Dedup skip works without DiscardNewPerSubject |
| `TestJetStreamSourcingDiscardNewPerSubjectMixedSubjects` | Single + Cluster | Per-subject rejection on one subject doesn't block others |
| `TestJetStreamSourcingDiscardNewPerSubjectWithDedup` | Single + Cluster | Combined per-subject + dedup rejections both skip correctly |
| `TestJetStreamSourcingDiscardNewPerSubjectNoRetryLoop` | Single | Burst of rejections doesn't block messages on other subjects |

### 3. Raft Fix (Separate Concern, `server/raft.go`)

Entries from previous terms are no longer counted toward commit advancement.
This prevents a scenario where a leader commits entries that could still be
overwritten by a leader from an intermediate term it never observed. Existing
raft tests were updated to match the corrected term-checking behavior.

## Difference from Original PR #7896

| Aspect | Original PR #7896 | This PR |
|--------|-------------------|---------|
| `ErrMaxMsgsPerSubject` handling | Skip (opt-in via config flag) | Skip (unconditional) |
| `errMsgIdDuplicate` handling | Skip | Skip |
| Opt-in config flag | Yes — new boolean in source config | No — always skips |
| Stream-level `DiscardNew` backpressure | Preserved | Preserved |
| Tests | Included | 7 tests (4 single-server + 3 clustered) |

## Open Question: Opt-In Flag

The original PR made per-subject skipping opt-in via a new `SkipOnDiscardNew`
(or similar) boolean in the source configuration. This is more conservative and
avoids changing default behavior for existing deployments that may rely on the
current blocking semantics even for per-subject limits. Whether this PR should
also adopt an opt-in approach is a design decision worth discussing.
