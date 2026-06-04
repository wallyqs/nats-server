# Design: Scaling Multi-Subject Filters for JetStream Consumers

| | |
|---|---|
| **Status** | Implemented (Phases 1 & 3); Phases 2/4/5 proposed — see `PHASES.md` |
| **Branch** | `claude/better-multi-filter-v0qig` |
| **Components** | `server/consumer.go`, `server/filestore.go`, `server/memstore.go`, `server/store.go` |
| **Related** | `PHASES.md` (roadmap), `docs/multi-filter-scaling-report.html` (illustrated summary) |

---

## 1. Summary

JetStream consumers that filter on many subjects (10, 30, 50, 90+) delivered
messages slowly and with rising latency, because the server performed a full
multi-subject **search once per delivered message**. The cost of that search
grows with the number of filter subjects, so delivery scaled like
`messages × filters`.

This proposal **amortizes the search across a batch**: a new store API,
`LoadNextMsgsMulti`, performs the multi-subject search once and returns up to
*N* matching sequence numbers; the consumer prefetches a batch of sequences and
serves them with cheap by-sequence reads. Per-message delivery cost becomes
independent of the filter count.

Measured effect (file store, 100k scattered messages): the per-message path
grows from **233 ms** (10 filters) to **2.02 s** (90 filters); the batched path
stays flat at **~14–16 ms** — **16×–123× faster**, with allocations reduced from
8.1M to ~110k at 90 filters.

---

## 2. Background and problem

A consumer's delivery loop `loopAndGatherMsgs` (`server/consumer.go`) pulls one
message per iteration via `getNextMsg`. For a multi-filtered consumer that meant
one store call — `LoadNextMsgMulti` — per message. Each such call:

1. acquires the store read lock;
2. re-selects the starting message block (`selectMsgBlockWithIndex`);
3. inside the block, either walks the per-block subject tree `fss` intersected
   against the filter sublist (`IntersectGSL`), or linearly scans the block —
   to locate the **single** next matching message, then returns.

Step 3 is the expensive part, and its cost rises with the number of distinct
subjects in the block and the number of filter subjects. Doing it per message is
the bottleneck.

The mem store was worse: its `LoadNextMsgMulti` was an explicit *"simple linear
walk to get started"* — a full scan from the cursor with no subject-index
acceleration, degrading toward `O(M²)` across a delivery of sparse matches.

### Why operators saw "drop the filter and it's faster"

The unfiltered path is a contiguous sequential scan — cache-friendly, no tree
intersection. The multi-filter path paid the intersection/re-derivation tax on
every message. Past a certain filter count, shipping everything and filtering
client-side genuinely won. The goal is to make the server do the smart thing
without that tradeoff.

---

## 3. Goals and non-goals

**Goals**
- Make per-message delivery cost for multi-filtered consumers independent of the
  number of filter subjects.
- No change to delivery semantics, ordering, ack/redelivery behavior, or message
  freshness.
- Never be materially worse than the unfiltered baseline.
- Keep the change isolated to the multi-filter path (`o.filters != nil`).

**Non-goals (this phase)**
- Changing the single-filter or unfiltered paths.
- Eliminating the subject-tree intersection entirely (that is Phase 2).
- Interior-block skipping for very sparse streams (Phase 5).
- A new client/protocol surface — this is purely server-internal.

---

## 4. Design overview

Decouple **searching** (expensive, amortizable) from **reading** (cheap,
per-message):

```
getNextMsg (multi-filter)
      │
      ▼
getNextMultiFiltered ──► prefetch buffer of matching sequences (mfseqs)
      │                         ▲
      │ buffer empty?           │ once per ~256 msgs
      ▼                         │
LoadNextMsgsMulti  ─────────────┘   (the multi-subject search, batched)
      │
      ▼
LoadMsg(seq)  ── fresh body load, per delivered message
```

In steady state, delivering a message costs one `LoadMsg` by sequence — the same
as the unfiltered baseline operators already accept. The multi-subject search
runs roughly once per 256 messages instead of every message.

**Prefetch sequences, not bodies.** Buffering whole messages risks delivering a
message that was acked/removed between prefetch and delivery. Buffering only the
matching **sequence numbers** keeps body loads lazy and fresh; a removed
sequence simply fails its `LoadMsg` and is skipped. NATS sequences are immutable,
so a buffered sequence can never resolve to a different message.

---

## 5. Detailed design

### 5.1 Store API

Added to the `StreamStore` interface (`server/store.go`):

```go
// LoadNextMsgsMulti appends up to maxSeqs sequences (ascending) at or after
// start matching any entry in the sublist to *seqs. Returns the count appended,
// the last sequence considered (for skip/EOF accounting), and ErrStoreEOF when
// no further matches exist. EOF contract mirrors LoadNextMsgMulti.
LoadNextMsgsMulti(sl *gsl.SimpleSublist, start uint64, maxSeqs int, seqs *[]uint64) (n int, last uint64, err error)
```

The `*[]uint64` is caller-owned and reused across refills to avoid per-batch
allocation.

### 5.2 File store (`server/filestore.go`)

- `LoadNextMsgsMulti` reuses the existing `psim` first-block skip (jump to the
  first block any matched subject can occupy), then iterates blocks calling
  `msgBlock.collectMatchingMulti`, accumulating matches until `maxSeqs` or EOF.
  On a first-block miss it consults `checkSkipFirstBlockMulti` to skip leading
  empty blocks (same mechanism `LoadNextMsgMulti` uses).
- `msgBlock.collectMatchingMulti` is the batched counterpart of
  `firstMatchingMulti`: a single sequential pass over the block that appends
  every matching sequence (testing `sl.HasInterest(subj)`), bounded by the
  remaining batch size. One block scan yields many matches instead of one.

### 5.3 Mem store (`server/memstore.go`)

- `nextMultiMatchLocked(sl, start)` intersects the sublist against `ms.fss` to
  compute `[fseq, lseq]` bounds (lowest matching `First` ≥ start, highest
  `Last`), skipping leading/trailing gaps.
- `shouldLinearScanMulti(start)` mirrors the single-filter heuristic: when
  `2*(LastSeq-start) < fss.Size()`, a plain linear scan beats walking the tree.
- `LoadNextMsgMulti` now uses these bounds (previously a naive linear walk);
  `LoadNextMsgsMulti` is added and shares the narrowing.

### 5.4 Consumer (`server/consumer.go`)

- New per-consumer state: `mfseqs []uint64` (prefetched sequences), `mfidx int`
  (cursor), `mflast uint64` (last sequence served — for rewind detection).
- `multiFilterPrefetch = 256` — batch depth.
- `getNextMultiFiltered(smp)` replaces the per-message `LoadNextMsgMulti` call in
  `getNextMsg`. It serves from the buffer, refills via `LoadNextMsgsMulti` when
  empty, and mirrors `LoadNextMsgMulti`'s exact return contract so the
  surrounding delivery, skip, and num-pending logic is untouched:

```go
if o.sseq <= o.mflast {           // rewound (e.g. redelivery) → buffer is stale
    o.resetMultiFilterPrefetch()
}
for {
    for o.mfidx < len(o.mfseqs) && o.mfseqs[o.mfidx] < o.sseq { o.mfidx++ }
    if o.mfidx >= len(o.mfseqs) {  // refill: the search, once per ~256 msgs
        n, last, err := store.LoadNextMsgsMulti(o.filters, o.sseq, 256, &o.mfseqs)
        if n == 0 { return nil, last, err }   // EOF, identical to before
    }
    seq := o.mfseqs[o.mfidx]; o.mfidx++; o.mflast = seq
    sm, err := store.LoadMsg(seq, smp)        // fresh body load
    if err != nil { o.sseq = seq + 1; continue } // removed since prefetch → skip
    return sm, seq, nil
}
```

- `resetMultiFilterPrefetch()` clears the buffer; called from `updateConfig`
  whenever the filter set changes.

---

## 6. Why it scales

Let **M** = messages delivered, **F** = filter subjects, **S** = distinct
subjects in a block. The search cost per call is roughly proportional to the
subject-tree intersection it performs (`~O(S)`), independent of how many filters
express it but rising with subject cardinality and filter overlap.

| Path | Per delivered message | Total over a delivery |
|---|---|---|
| Before (per message) | 1 full search | `O(M · search)` |
| After (batched) | 1 `LoadMsg` + `1/256` of a search | `O(M + (M/256)·search)` |

The search term is not made cheaper — it is **amortized ~256×** out of the
per-message path, which collapses to a by-sequence read whose cost is
independent of `F`. That is why the measured batched line is flat across filter
counts.

---

## 7. Correctness

| Concern | Handling |
|---|---|
| Stale bodies | Bodies loaded by sequence at delivery time; a removed message fails `LoadMsg` and is skipped, never delivered. |
| Redelivery / rewind | If `o.sseq <= o.mflast` the buffer is discarded and rebuilt from the new position. |
| Forward cursor jumps | Stale-but-ahead sequences dropped; buffer refills; still-valid matches remain valid (filter unchanged). |
| Filter set change | `updateConfig` calls `resetMultiFilterPrefetch`. |
| Removed message | Skipped; `o.sseq` advanced past it for forward progress (no re-scan on refill). |
| EOF / `updateSkipped` / num-pending | Unchanged — helper returns the same `(nil, lastSeq, ErrStoreEOF)` contract the caller already handles. |
| Sequence immutability | A buffered sequence resolves to its original message or to nothing — never a different message. |

**Tests.** `TestStoreLoadNextMsgsMulti` cross-checks batched output against
per-message `LoadNextMsgMulti` across both Memory and File stores, with
scattered subjects, interior deletes, batch sizes 1/7/64/10000, and EOF. The
full upstream CI suite (stores, JetStream consumers, no-race, cluster matrix,
raft, jwt) passed on this branch, including under the race detector.

---

## 8. Benchmarks

`Benchmark_FileStoreLoadNextMsgsMulti` (file store, 100k messages round-robined
across 1,000 subjects; each op delivers all matches for the given filter count).
Run:

```
go test ./server/ -run '^$' -bench Benchmark_FileStoreLoadNextMsgsMulti -benchmem
```

| Filters | Before (ns/op) | After (ns/op) | Speedup | Before allocs/op | After allocs/op |
|--:|--:|--:|--:|--:|--:|
| 1  | 5,811,543 | 14,452,472 | 0.4× | 99,001 | 101,102 |
| 10 | 232,923,006 | 14,098,610 | **16.5×** | 983,978 | 101,996 |
| 30 | 670,734,617 | 15,511,205 | **43.2×** | 2,890,729 | 103,984 |
| 50 | 1,183,880,187 | 16,462,718 | **71.9×** | 4,718,275 | 105,972 |
| 90 | 2,022,731,448 | 16,493,157 | **122.6×** | 8,135,760 | 109,948 |

Observations:
- The "after" column is **flat** in both time (~14–16 ms) and allocations
  (~100–110k) regardless of filter count.
- The "before" column scales roughly linearly with the filter count — the
  `messages × filters` blow-up, visible in allocations too.
- `filters=1` is a baseline only: a single filter hits the `MatchesSingleFilter`
  fast path, so the per-message path wins and batching just adds a `LoadMsg`.
  Real consumers never use the multi path with one filter (`o.filters` is nil
  for a single filter), so this row is informational.

A companion timing test, `TestNoRaceFileStoreLoadNextMsgsMultiScaling`, asserts
the batched path is faster than the per-message path across 10/30/50/90 filters
and runs in CI (No-Race 2).

*Numbers measured on the development machine; treat as relative, not published
throughput figures.*

---

## 9. Alternatives considered

- **Prefetch message bodies, not sequences.** Saves the second `LoadMsg` lock,
  but risks delivering stale/removed messages and complicates invalidation.
  Rejected for correctness; the by-sequence read on the file store is a cache
  hit anyway.
- **Keep one-call-per-message but cache the intersection.** Helps high-cardinality
  streams but still pays per-message lock + block selection. Captured as **Phase
  2** rather than the primary fix.
- **Always drop to an unfiltered sequential scan + post-filter.** Fast when the
  filter is non-selective, wasteful when it is selective. Captured as the
  *adaptive* **Phase 4** so it is chosen only when selectivity warrants it.
- **Per-subject min-heap merge / v2 `psim` block tracking.** Largest change;
  only needed for very sparse interior matches. Captured as **Phase 5**.

---

## 10. Risks and rollout

- **Timing-based test flakiness.** `TestNoRaceFileStoreLoadNextMsgsMultiScaling`
  asserts a wall-clock comparison; on a noisy runner this *could* flake. The
  margin is wide (>10×) and it passed CI, but if it ever flakes the remedy is to
  assert on operation counts (search invocations / lock acquisitions) instead of
  time.
- **Memory.** The prefetch buffer is `multiFilterPrefetch` × 8 bytes (~2KB) per
  multi-filter consumer; reused across refills.
- **Scope containment.** All changes are gated on `o.filters != nil`. Single-
  filter and unfiltered consumers are byte-for-byte unaffected.
- **Rollout.** No config flag or protocol change; behavior is identical from the
  client's perspective, only faster. Ships as a normal server change.

---

## 11. Future work

See `PHASES.md` for detailed, code-referenced plans:

- **Phase 2** — cache the matched-subject set keyed by a `psim` generation
  counter (or a persistent store-side iterator) to skip the per-refill tree
  intersection on high-cardinality streams.
- **Phase 4** — adaptive selectivity: estimate match fraction from `psi.total`
  and pick a contiguous post-filter scan when the filter is non-selective; tune
  prefetch depth to the pull request's batch size.
- **Phase 5** — track per-subject block membership in `psim` for interior-block
  skipping, optionally with a `(nextSeq, subject)` min-heap for `O(log F)`
  steady-state delivery.

Suggested order: **4 → 2 → 5**.
