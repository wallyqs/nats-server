# Review: PR #7810 — Consumer with overlapping filter subjects but not subset

**PR**: https://github.com/nats-io/nats-server/pull/7810
**Author**: Maurice van Veen (@MauriceVanVeen)
**Fixes**: https://github.com/nats-io/nats-server/issues/7779

## Summary

This PR relaxes the consumer filter-subject overlap validation. Previously,
`SubjectsCollide` was used which rejects any two filters that could match the
same literal message (e.g. `event.foo.*` and `event.*.foo` both match
`event.foo.foo`). The PR replaces it with `subjectIsSubsetMatch` which only
rejects when one filter is a strict subset of the other (e.g. `event.*.foo` is
a subset of `event.>`).

## Change Analysis

### The one-line production change (`server/consumer.go:828`)

```go
// Before
if inner != outer && SubjectsCollide(subject, ssubject) {
// After
if inner != outer && subjectIsSubsetMatch(subject, ssubject) {
```

**Semantic difference:**

| Function | `event.foo.*` vs `event.*.foo` | `event.*.foo` vs `event.>` |
|---|---|---|
| `SubjectsCollide` | true (rejects) | true (rejects) |
| `subjectIsSubsetMatch` | false (allows) | true (rejects) |

Since the loop checks all `(i, j)` pairs where `i != j`, both
`subjectIsSubsetMatch(a, b)` and `subjectIsSubsetMatch(b, a)` are evaluated.
This correctly detects subset relationships regardless of order (e.g.
`event.*.foo` ⊂ `event.>` is caught even though `event.>` ⊄ `event.*.foo`).

### Safety: No duplicate delivery

`LoadNextMsgMulti` (memstore.go / filestore.go) iterates messages by sequence
number and calls `sl.HasInterest(subj)` which returns a boolean — match or
no-match. A message matching two overlapping filters is still delivered exactly
once because:

1. The store iterates sequentially by sequence number.
2. Upon finding *any* matching message it returns immediately.
3. The consumer advances `o.sseq` past that sequence.

### Safety: No double-counting in NumPending

`NumPendingMulti` uses `stree.IntersectGSL` which iterates the *subject tree*
(one entry per distinct subject string) and checks `sl.HasInterest` once per
subject. A subject like `event.foo.foo` that matches both `event.foo.*` and
`event.*.foo` is counted exactly once.

### Work-queue consumer check is unaffected

The inter-consumer overlap check for work-queue streams at
`server/jetstream_cluster.go:8675` still uses `SubjectsCollide`, which is
correct — different consumers on a work-queue stream must never receive the
same message.

## Test Review (`TestJetStreamConsumerAllowOverlappingSubjectsIfNotSubset`)

The test:

1. Creates a stream on `event.>`.
2. Publishes 16 messages: `event.{foo,bar,baz,oth}.{foo,bar,baz,oth}`.
3. Verifies that `event.>` + `event.*.foo` is still **rejected** (subset).
4. Verifies that `event.foo.*` + `event.*.foo` is now **allowed** (overlap but
   not subset).
5. Fetches messages and confirms exactly **7 unique messages** delivered in
   sequence order with no duplicates.

The expected count of 7 is correct:
- `event.foo.*` matches: `event.foo.{foo,bar,baz,oth}` → 4
- `event.*.foo` matches: `event.{bar,baz,oth}.foo` → 3 additional (event.foo.foo already counted)
- Total unique: **7**

The ordering check is also correct since `LoadNextMsgMulti` delivers in
stream-sequence order.

## Issues / Suggestions

### 1. Stale comment (minor)

At `server/consumer.go:822`, the comment reads:

```go
// Check subject filters do not overlap.
```

After this change, overlapping filters *are* allowed as long as neither is a
subset of the other. Consider updating to:

```go
// Check that no subject filter is a subset of another.
```

### 2. Consider adding a test for three-way overlap

The test covers the two-filter case. It may be worth adding a case with three
or more overlapping-but-not-subset filters (e.g. `event.foo.*`, `event.*.foo`,
`event.*.bar`) to exercise the `O(n²)` pair-check and confirm it doesn't
produce unexpected rejections.

### 3. Consider adding a test for the symmetric subset case

The test checks `event.>` + `event.*.foo` (one-way subset). A case like
`foo.*.*` + `foo.*.>` where the subset relationship may be less obvious could
strengthen coverage.

## Verdict

**Approve.** The change is minimal, targeted, and safe. The underlying delivery
and counting mechanisms (`LoadNextMsgMulti`, `IntersectGSL`) already handle
overlapping filters correctly by design — the previous `SubjectsCollide` check
was overly restrictive. The test is well-structured and verifies both the
rejection of true subsets and the acceptance + correct delivery of overlapping
non-subset filters.

The only suggestion is to update the now-misleading comment at line 822.
