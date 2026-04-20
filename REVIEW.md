# Review: Defer consumer starting seq scan off meta goroutine

## Overview

The change defers `selectStartingSeqNo` for clustered, non-direct consumers
from `addConsumerWithAssignment` (which runs on the meta apply goroutine) into
`setLeader(true)`. This prevents the potentially expensive starting-seq scan
(`FilteredState`, `MultiLastSeqs`, `GetSeqFromTime`) from blocking Raft meta
consensus. `setLeader` now returns an error so create paths can surface scan
failures back to the API and step down to trigger re-election.

Changes:
- `server/consumer.go` (+47 / -15): defer the scan in `addConsumerWithAssignment`,
  run it in `setLeader` gated by `needsSelect`, propagate errors, guard
  `infoWithSnapAndReply` against zero dseq/sseq.
- `server/jetstream_cluster.go` (+14 / -4): plumb the new error out of
  `processClusterCreateConsumer` and `processConsumerLeaderChangeWithAssignment`,
  initialize `o.dseq = 1` in `applyConsumerEntries` so followers are not stuck
  at zero.
- `server/jetstream_cluster_3_test.go` (+61): new
  `TestJetStreamClusterConsumerSelectStartingSeqDeferred` exercising the R3
  happy path.

## Correctness

### The `needsSelect` gate is tight, but relies on subtle invariants

```go
needsSelect := isLeader && !wasLeader && o.dseq == 0 &&
    (o.store == nil || !o.store.HasState())
```

- `wasLeader := o.leader.Swap(isLeader)` is the serialization point — only one
  concurrent `setLeader(true)` call observes `wasLeader == false`, so the scan
  cannot double-fire from parallel invocations.
- `selectStartingSeqNo` terminates with `o.dseq = 1` (consumer.go:6204), so the
  `o.dseq == 0` clause correctly prevents re-runs after a successful scan, even
  across a `setLeader(false)` / `setLeader(true)` cycle where the store still
  has no persisted state.
- If the scan itself returned an error (see below), `o.dseq` stays at 0 and a
  subsequent leader election will retry, which is the desired recovery
  behavior.

One edge: the check reads `o.dseq`, `o.store`, and `o.store.HasState()` under
RLock, then re-acquires a write lock before running the scan. Between the
unlock and relock nothing in the same-generation leader transition can set
`dseq` (only `applyConsumerEntries` and `selectStartingSeqNo` do), so in
practice this is safe. Worth a short comment next to `needsSelect` calling out
that `selectStartingSeqNo` is what eventually clears the gate — it is
non-obvious from the surrounding code.

### Error recovery in `setLeader`

```go
if err := o.selectStartingSeqNo(); err != nil {
    o.srv.Errorf(... )
    o.leader.Store(false)
    node := o.node
    o.mu.Unlock()
    if node != nil {
        _ = node.StepDown()
    }
    return err
}
```

- Reverting `o.leader` to `false` is correct: the preceding `Swap(true)` is the
  only state mutation that has happened on this code path, so the rollback is
  complete.
- `StepDown` is called without the consumer lock held — good, since it can
  block on the Raft layer.
- `o.srv` is read without the lock; it is set once at construction, so this is
  fine.
- `o.node` is captured under the consumer lock before unlock — fine.

Caveat: if `selectStartingSeqNo` fails on every replica (e.g. a persistent
store error on `FilteredState`), the group will thrash through elections. That
is arguably the right behavior — a consumer that cannot pick a start sequence
should not be serving — but the failure mode is different from the previous
"fail the create synchronously" shape and worth a follow-up to ensure the
create response semantics (see below) are aligned.

### `processClusterCreateConsumer` response handling

```go
err = o.setLeader(true)
if err != nil {
    resp.Error = NewJSConsumerCreateError(err, Unless(err))
    s.sendAPIErrResponse(...)
} else {
    resp.ConsumerInfo = setDynamicConsumerInfoMetadata(o.info())
    s.sendAPIResponse(...)
}
```

Because `setLeader(true)` now steps down on scan failure, another replica will
pick up leadership and likely succeed. By then the API caller has already
received a create error while the consumer actually exists in the cluster
metadata — subsequent retries will fail with "consumer already exists". This
is a behavioral change from the pre-PR flow where scan errors surfaced
synchronously at `addConsumerWithAssignment` before the assignment was
committed.

Options worth considering:
1. On `setLeader` error here, proactively tear the consumer assignment back
   out of meta so a client retry succeeds.
2. Or, only step down on specific retryable errors and keep the consumer
   otherwise healthy.

At minimum this regression-class deserves a test (an injected
`selectStartingSeqNo` failure verifying that the client can recover).

### `processConsumerLeaderChangeWithAssignment` error propagation

```go
if lerr := o.setLeader(isLeader); lerr != nil && err == nil {
    err = lerr
}
```

Preserves `ca.err` when already set. Reasonable. But note that on the
`hasResponded` branch (line 6830), the error is silently dropped — the caller
has already been told the consumer was created. That is consistent with
existing behavior and probably fine, but another data point that scan errors
need a durable recovery story beyond the create RPC.

### `infoWithSnapAndReply` zero-seq guard

```go
dseq, sseq := o.dseq, o.sseq
if dseq <= 0 { dseq = 1 }
if sseq <= 0 { sseq = 1 }
...
Delivered: SequenceInfo{
    Consumer: dseq - 1,
    Stream:   sseq - 1,
},
```

Correct — without this guard, the transient `0` on followers before
`applyConsumerEntries` runs would underflow to `^uint64(0)` in the `Info`
response. Two small notes:

- `dseq`/`sseq` are `uint64`; `<= 0` reads as signed and should be `== 0` for
  clarity.
- This masks a real window where the consumer reports `Delivered: 0/0` before
  its start seq has been chosen. Consumers of `info()` that make assumptions
  about monotonicity during that window may see values that later "jump" to
  the scanned sequence. Worth mentioning in the code comment.

### `applyConsumerEntries` dseq bootstrap

```go
if !o.isLeader() && sseq > o.sseq {
    o.sseq = sseq
}
if o.dseq == 0 {
    o.dseq = 1
}
```

Covers the follower case where `selectStartingSeqNo` never ran locally. This
runs inside the same entry-apply path, which already held the consumer lock
via `o.mu.Lock()` above — confirmed correct. The initialization is also safe
for the leader: once the scan has run, `dseq` is already 1, so this is a
no-op.

## Style / Conventions

- Moving `standalone := ...` up and reusing it below is a nice cleanup.
- The new `o.srv.Errorf("JetStream consumer '%s > %s > %s' ...")` format
  matches the surrounding logging conventions (acc/stream/name triple).
- The inline comment explaining the deferral reason is good; recommend adding
  one more line near `needsSelect` pointing at `selectStartingSeqNo` setting
  `dseq = 1` as the gate-clearing mechanism, because it is load-bearing.

## Test coverage

The new test covers the R3 happy path with default `DeliverPolicy` (DeliverAll,
cheap scan). Gaps worth closing:

- **Expensive policies** — `DeliverLast`, `DeliverLastPerSubject`,
  `DeliverByStartTime` are the exact reason this PR exists. A test with one
  of these (and `num_pending > 0`) would exercise the deferred path on the
  code that actually benefits.
- **Scan failure path** — inject a failure from `FilteredState`/`MultiLastSeqs`
  and assert: (a) API returns an error, (b) node steps down, (c) another
  replica becomes leader and the consumer ends up healthy (or the assignment
  is cleaned up — see point above). This is the most risk-bearing new path in
  the PR and is currently uncovered.
- **R1** — confirm standalone still runs the scan inline via the
  `config.Direct || standalone` branch. Easy to add as a one-liner assertion.
- **Re-election while scanning** — leader loses quorum mid-scan; ensure the
  consumer still converges.

## Performance

This is the whole point of the PR, and the direction is right: the meta apply
goroutine is a hot single-writer path, and any per-consumer-creation storage
scan contends with every other meta operation in the cluster. Moving the scan
to the per-consumer leader transition is a sound trade — scans now run in
parallel across unrelated consumers and only block the consumer's own Raft
group.

No new allocations or hot-path overhead introduced. The added
`o.store.HasState()` call on every leader transition is cheap.

## Security

No surface area change. Error messages include account/stream/consumer names,
consistent with the rest of the file.

## Summary

Sound direction and a meaningful perf improvement. The core mechanics
(`wasLeader` swap, `dseq == 0` gate, stepdown-on-error) are correct. Two
things I would want to see before merging:

1. A decision and test around what happens to the consumer assignment when
   `setLeader(true)` fails on the create path — the caller sees an error but
   the consumer is still in meta.
2. At least one test with an expensive `DeliverPolicy` (the case the PR
   actually targets) and one with an injected scan failure.

Minor nits: `<= 0` → `== 0` on unsigned counters; add a one-line comment
pointing out that `selectStartingSeqNo` setting `dseq = 1` is what prevents
re-runs.
