# Design: Scaling Multi-Subject Filters for JetStream Consumers

| | |
|---|---|
| **Status** | Implemented (Phases 1 & 3); Phases 2/4/5 proposed — see `PHASES.md`. **A pre-merge review pass fixed a data race and the delivery error handling; some test-coverage gaps remain — see §12.** |
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

> **Note (review correction).** `last` is documented above as the *last
> sequence considered*, but the implementation returns, on success (`n > 0`),
> the **last *matching* sequence** (`(*seqs)[len-1]`) — which can be lower than
> the last sequence actually scanned. On EOF (`n == 0`) it returns the stream's
> `LastSeq`. Callers must therefore only rely on `last` on the `n == 0` branch
> (which the consumer does — it serves matched sequences directly and uses
> `last` only for the EOF/`updateSkipped` path). Also: `*seqs` is **appended
> to, not truncated** — callers must reset it (`seqs[:0]`) before each refill
> (the consumer does this in `getNextMultiFiltered`). Finally, unlike
> `LoadNextMsgMulti`, the batched methods do **not** implement the `nil` /
> full-wildcard / single-filter fast-path delegations: a **nil sublist will
> panic** in the scan path. They are intended only for genuine 2+-entry
> sublists; direct store-interface callers must respect that (or guards should
> be added for symmetry). See §12.

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
  **Note:** unlike `firstMatchingMulti`, `collectMatchingMulti` does **not** use
  the per-block `fss` subject-tree intersection branch — it is an
  *unconditional* linear scan over `[start, lseq]` with one `HasInterest` per
  live message. The win comes from gathering many matches per block scan and
  from amortizing block re-entry / lock / `selectMsgBlock`, **not** from a
  cheaper per-block search. High-cardinality blocks with few matches among many
  subjects can therefore scan *more* than the per-message intersection path did
  (see §6 and Phase 4/5 in `PHASES.md`).

### 5.3 Mem store (`server/memstore.go`)

- `nextMultiMatchLocked(sl, start)` intersects the sublist against `ms.fss` to
  compute `[fseq, lseq]` bounds (lowest matching `First` ≥ start, highest
  `Last`), skipping leading/trailing gaps.
- `shouldLinearScanMulti(start)` mirrors **only the message-count-vs-subject-count
  term** of the single-filter heuristic: when `2*(LastSeq-start) < fss.Size()`, a
  plain linear scan beats walking the tree. It intentionally omits the
  single-filter `isAll` short-circuit and the `wc && fss.Size() > linearScanMaxFSS`
  (256) term, so a high-cardinality **wildcard** multi-filter will still attempt
  tree narrowing where the single-filter path would have chosen a linear scan.
  Precondition: callers clamp `start` into `[FirstSeq, LastSeq]` before calling,
  so `LastSeq-start ≥ 0` (the `int()` cast — and the unsigned subtraction —
  assume this; both `LoadNextMsgMulti` and `LoadNextMsgsMulti` return EOF for
  `start > LastSeq` before reaching the heuristic).
- `LoadNextMsgMulti` now uses these bounds (previously a naive linear walk);
  `LoadNextMsgsMulti` is added and shares the narrowing. **Note:** because the
  per-message `LoadNextMsgMulti` changed too, this is not purely a multi-filter
  *prefetch* change — see §10 and §12.

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
subjects in a block.

> **Correction (review).** The original text here claimed the per-call search
> cost is "`~O(S)` subject-tree intersection." That is **not** what the batched
> filestore path does. `collectMatchingMulti` performs **no** per-block tree
> intersection — its cost is `O(sum of live messages in the touched blocks)`
> plus one `HasInterest` per live message. The **memstore** batched path *does*
> narrow via `IntersectGSL` when `shouldLinearScanMulti` is false. The reason
> the batched line is flat across filter counts is **amortization** (≈256× fewer
> searches) collapsing the per-message path to a by-sequence read whose cost is
> independent of `F` — *not* a cheaper per-block search. High-cardinality,
> **sparse** interiors get no intersection speedup in the batched filestore path
> (Phase 5).

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
| Redelivery / rewind | If `o.sseq <= o.mflast` the buffer is discarded and rebuilt from the new position. **Note:** the pull-consumer `o.sseq--` redo (request exceeds `max_bytes` → 409, or an expired request) *also* trips this and discards the whole 256-entry buffer — correct (never delivers wrong data) but costs a full rebuild, bounded to once per ill-fitting/expired pull. |
| Forward cursor jumps | Stale-but-ahead sequences dropped; buffer refills; still-valid matches remain valid (filter unchanged). |
| Filter set change | `updateConfig` calls `resetMultiFilterPrefetch`. |
| Removed message | Skipped; `o.sseq` advanced past it for forward progress (no re-scan on refill). **Caveat (review):** *all* non-`ErrStoreClosed` `LoadMsg` errors are treated as removals — including transient cache/corruption errors (`errPartialCache`/`errNoCache`/checksum) and block read errors — and durably advance `o.sseq`. This differs from the single-filter delivery loop's log-and-wait on such errors; confirm it is intended (§12). |
| Store closed mid-buffer | **Divergence (review):** on `ErrStoreClosed` the helper currently returns `(nil, seq, ErrStoreClosed)` with the buffered (nonzero) `seq`, which advances `o.sseq`; legacy `LoadNextMsgMulti` returned `skip = 0` (no advance). Filestore-only (memstore never returns `ErrStoreClosed`). Should return `(nil, 0, ErrStoreClosed)` — §12. |
| Partial batch + block error | filestore `LoadNextMsgsMulti` returns `(n>0, last=0, err)` when a *later* block errors after earlier matches; the consumer serves the buffered matches (it inspects only `n==0`) and the error resurfaces on the next refill. |
| EOF / `updateSkipped` / num-pending | Unchanged — helper returns the same `(nil, lastSeq, ErrStoreEOF)` contract the caller already handles. (Verified: EOF is deferred to the call *after* the last match in both the legacy and batched paths — behavior is identical.) |
| Sequence immutability | A buffered sequence resolves to its original message or to nothing — never a different message. |

**Tests.** `TestStoreLoadNextMsgsMulti` cross-checks batched output against
per-message `LoadNextMsgMulti` across both Memory and File stores, with
scattered subjects, interior deletes, batch sizes 1/7/64/10000, and EOF.

> **Review caveats — coverage is narrower than this paragraph implies.**
> - **No consumer-level coverage.** Nothing exercises `getNextMultiFiltered`,
>   the prefetch buffer, rewind reset, filter-change reset, or the
>   EOF/`updateSkipped` path. The existing tests are store-level only.
> - **Self-comparing oracle.** `TestStoreLoadNextMsgsMulti` uses
>   `LoadNextMsgMulti` as ground truth, but the two share
>   `nextMultiMatchLocked`/`shouldLinearScanMulti`, so a shared-helper bug
>   corrupts both sides equally and the test still passes. Use an independent
>   brute-force oracle.
> - **Race detector.** The suite is *not* clean under `-race`:
>   `memStore.nextMultiMatchLocked` calls `recalculateForSubj`, which mutates
>   the **shared** `fss` `SimpleState` (a tree-resident pointer) in place while
>   both `LoadNextMsgsMulti` and the now-modified `LoadNextMsgMulti` hold only
>   `RLock`. Single-filter `LoadNextMsg` does the equivalent mutation under a
>   write `Lock`. This is a real data race (§12); the existing tests are
>   single-threaded and do not surface it.
> - The change also leaves several scenarios untested: wildcard/overlapping/
>   zero-match/duplicate filter entries, multi-block stores, batch sizes around
>   the 256 prefetch boundary, store reload, and compression/encryption (the
>   current test sets `compressionAndEncryption=false`).
>
> See §12 and the companion test plan for the full list.

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
- **Scope containment.** The new prefetch *path* is gated on `o.filters != nil`,
  so single-filter and unfiltered consumers take the same call site as before.
  **But this is not purely additive:** the per-message `memStore.LoadNextMsgMulti`
  itself changed (it now narrows via `fss` instead of a linear walk), so any
  multi-filter caller of the *non-batched* method changed behavior too — notably
  `checkStateForInterestStream` (`consumer.go` interest-stream reconciliation),
  which is not on the prefetch path and remains per-message. Single-filter and
  unfiltered consumers are unaffected; multi-filter consumers are affected on
  both paths. (See §12 — this also widens the data race's blast radius.)
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

---

## 12. Review findings & open issues

A pre-merge review pass (correctness / concurrency / coverage / performance /
design) surfaced the items below. Each was confirmed against the code unless
marked otherwise. They are the inputs to the companion **test plan**; the
inline notes in §5–§10 above point back here. Items marked **✅ Fixed** were
addressed on this branch; the rest remain open.

**Must-fix before merge**

1. **✅ Fixed — Data race in `memStore` multi-filter narrowing (High).**
   `nextMultiMatchLocked` → `recalculateForSubj` mutated the shared `fss`
   `SimpleState` in place under `RLock`; single-filter `LoadNextMsg` does the
   same mutation under a write `Lock`. Two concurrent multi-filter readers (or a
   multi reader racing the single-filter path) write/write the same struct.
   **Fix:** both `LoadNextMsgMulti` and `LoadNextMsgsMulti` now take `ms.mu.Lock()`.
   Regression test `TestMemStoreLoadNextMsgsMultiConcurrentRace` reproduces the
   race under `-race` before the fix and is clean after. (Note: the Phase 3
   change had also added this to the pre-existing `LoadNextMsgMulti`, affecting
   `checkStateForInterestStream`; the lock fix covers that path too.)

**Should-fix / decide intentionally**

2. **✅ Fixed — `ErrStoreClosed` advanced `o.sseq` (Med).** `getNextMultiFiltered`
   now returns `(nil, 0, err)` for any non-removal error (see item 3), matching
   `LoadNextMsgMulti`'s contract and no longer corrupting the cursor on close.
3. **✅ Fixed — Over-broad "removed" classification (Med).** Only a genuine
   removal (`ErrStoreMsgNotFound`, `errDeletedMsg`, or a nil message with no
   error) is now skipped with `o.sseq` advanced; every other error is surfaced
   to the delivery loop (which logs/retries or terminates on close) instead of
   silently dropping a possibly-live message. Covered by
   `TestJetStreamConsumerMultiFilterRemovalMidDelivery`.
4. **✅ Fixed — Missing guards on `LoadNextMsgsMulti` (Low).** A nil sublist now
   returns `(0, 0, ErrStoreEOF)` (both stores) instead of panicking; the 2+-entry
   precondition is documented (§5.1). `TestStoreLoadNextMsgsMultiNilSublist`.
   (Full-wildcard / single-filter sublists already produce correct results via
   the scan, so no delegation is required for correctness.)

**Doc/heuristic accuracy (addressed inline above)**

5. `last` return value = last *match*, not last *considered* (§5.1).
6. `collectMatchingMulti` is an unconditional linear scan, not an `fss`
   intersection; §6 complexity model corrected accordingly (§5.2, §6).
7. `shouldLinearScanMulti` mirrors only one term of the single-filter heuristic
   (§5.3).
8. Scope is not "byte-for-byte unaffected" — the per-message `LoadNextMsgMulti`
   changed too (§10).

**Test-suite gaps (drive the test plan)**

9. **Largely addressed.** Added store-level: `TestStoreLoadNextMsgsMultiBruteForceOracle`
   (independent brute-force oracle across both stores + the full cipher/compression
   matrix, deletes around the 256 boundary), `...WildcardsAndOverlap`,
   `TestFileStoreLoadNextMsgsMultiMultiBlockAndReload` (many blocks + Stop/reopen),
   and `...NilSublist`. Added consumer end-to-end (file + mem):
   `TestJetStreamConsumerMultiFilterPrefetchOracle`, `...RemovalMidDelivery`,
   `...UpdateFilterSet`, `...Redelivery`, and the R3
   `TestNoRaceJetStreamClusterMultiFilterConsumer` (with a consumer leader
   stepdown). Added benchmarks: `Benchmark_FileStoreLoadNextMsgsMultiSelectivity`
   (with an unfiltered baseline arm), `...Cardinality`, and the previously-missing
   `Benchmark_MemStoreLoadNextMsgsMulti`. **Still open (nice-to-have,
   non-blocking):** an allocations-per-op ceiling *assertion* test (flat across
   filter count).
10. **✅ Fixed — flaky scaling assertion.**
    `TestNoRaceFileStoreLoadNextMsgsMultiScaling` no longer gates on wall-clock;
    it asserts on search-call counts (per-message ≈ `M+1`, batched ≈
    `ceil(M/256)+1`), with wall-clock kept only as a logged signal. An
    allocations-per-op ceiling test (flat across filter count) is still a useful
    addition.

**Open questions (confirm at runtime)**

- Does the always-linear `collectMatchingMulti` ever go *net slower* than the
  per-message path at high subject cardinality / low selectivity? **Answered (no,
  in the tested range).** `Benchmark_FileStoreLoadNextMsgsMultiCardinality`
  (200k msgs, 10 filters): batched beats per-message at every cardinality —
  ~16× at 1k subjects, narrowing to ~5× at 100k (batched ~13 ms → ~27 ms as
  matches grow sparser, per-message ~150–215 ms). `...Selectivity` shows batched
  stays flat (~13–24 ms across 1→90% match) and within ~1.5× of the unfiltered
  "ship everything" baseline even at 90% selectivity. So batching is not a
  regression anywhere measured; the narrowing margin at very high cardinality /
  sparse interiors is the Phase 4/5 opportunity, not a correctness or
  worse-than-baseline risk. (Numbers are dev-machine, relative; cardinality
  beyond 100k was not measured due to store-size.)
- Is the search/body-read split window (cache expiry between prefetch and
  `LoadMsg` + a genuine block error) actually reachable in practice, and can any
  transient class durably skip a *live* message? Settle with fault injection.
