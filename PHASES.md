# Multi-Subject Filter Scaling — Phases

This document tracks the work to make JetStream consumers with many filter
subjects (10/30/50/90+) scale, and lays out concrete implementation plans for
the remaining phases.

Branch: `claude/better-multi-filter-v0qig`

---

## Background — the problem

A multi-filtered consumer's delivery loop (`loopAndGatherMsgs` →
`getNextMsg`, `server/consumer.go`) historically called
`store.LoadNextMsgMulti` **once per delivered message**. Each call:

- re-acquired the store read lock,
- re-selected the starting message block (`selectMsgBlockWithIndex`),
- walked the per-block subject tree (`fss`) intersected against the sublist of
  filter subjects (`IntersectGSL`), or linearly scanned the block — just to
  locate the *single* next match.

That search cost grows with the number of filter subjects and the number of
distinct subjects per block, so total delivery cost scaled like
`messages × filters` instead of `messages`. In the worst cases it was faster to
drop the filter, ship everything, and filter on the client.

**Core strategy:** do the multi-subject *search* once per batch, not once per
message. Cheap by-sequence reads (`LoadMsg`) carry the per-message path.

### Key code map

| Concern | Location |
|---|---|
| Consumer delivery loop | `server/consumer.go` `loopAndGatherMsgs`, `getNextMsg` |
| Multi-filter prefetch | `server/consumer.go` `getNextMultiFiltered`, `multiFilterPrefetch`, `mfseqs/mfidx/mflast` |
| File store batched search | `server/filestore.go` `LoadNextMsgsMulti`, `msgBlock.collectMatchingMulti` |
| File store per-msg search | `server/filestore.go` `LoadNextMsgMulti`, `msgBlock.firstMatchingMulti` |
| Stream-level subject index | `server/filestore.go` `psim *stree.SubjectTree[psi]`, `psi{total,fblk,lblk}`, `bim` |
| Per-block subject index | `server/filestore.go` `msgBlock.fss *stree.SubjectTree[SimpleState]` |
| Block skip helpers | `server/filestore.go` `checkSkipFirstBlockMulti`, `selectSkipFirstBlock` |
| Mem store search | `server/memstore.go` `LoadNextMsgMulti`, `LoadNextMsgsMulti`, `nextMultiMatchLocked`, `shouldLinearScanMulti`, `fss` |
| Sublist matching | `server/gsl/gsl.go` `HasInterest`, `MatchesFullWildcard`, `MatchesSingleFilter`; `server/stree/stree.go` `IntersectGSL` |
| Store interface | `server/store.go` `StreamStore` |

---

## Status at a glance

| Phase | Summary | Status |
|---|---|---|
| 1 | Batched store lookup + consumer prefetch buffer | ✅ Done |
| 2 | Stateful cursor + cached matched-subject set (psim generation) | ⚠️ Partial |
| 3 | memstore multi narrowed via `fss` | ✅ Done |
| 4 | Adaptive selectivity (sequential scan + post-filter) | ❌ Not started |
| 5 | v2 interior-block skipping + per-subject block tracking / merge heap | ❌ Not started |

Supporting artifacts (done): correctness tests
(`TestStoreLoadNextMsgsMulti`), scaling test
(`TestNoRaceFileStoreLoadNextMsgsMultiScaling`), benchmark
(`Benchmark_FileStoreLoadNextMsgsMulti`), and an HTML methodology report
(`docs/multi-filter-scaling-report.html`).

---

## Phase 1 — Batched lookup + consumer prefetch ✅

**Goal.** Stop searching once per delivered message; amortize the search across
a batch.

**What was implemented.**

- New store API on `StreamStore`:
  ```go
  LoadNextMsgsMulti(sl *gsl.SimpleSublist, start uint64, maxSeqs int, seqs *[]uint64) (n int, last uint64, err error)
  ```
  It performs the multi-subject search once and appends up to `maxSeqs`
  matching sequences (ascending) to `*seqs`. EOF contract mirrors
  `LoadNextMsgMulti`: `(0, lastSeq, ErrStoreEOF)`.
- `filestore.go`: `LoadNextMsgsMulti` reuses the `psim` first-block skip, then
  iterates blocks calling `msgBlock.collectMatchingMulti`, which gathers all of
  a block's matches in a single sequential pass (cache-friendly), bounded by the
  remaining batch size.
- `consumer.go`: `getNextMultiFiltered` replaces the per-message
  `LoadNextMsgMulti` call. It keeps a per-consumer buffer of prefetched
  sequences (`mfseqs`, cursor `mfidx`, last-served `mflast`), serves them with
  fresh `LoadMsg` lookups, and refills via `LoadNextMsgsMulti` only when empty.

**Design choice — prefetch sequences, not bodies.** A buffered body could be
delivered stale if the message is acked/removed between prefetch and delivery.
Buffering only sequence numbers keeps body loads lazy and fresh; a removed
sequence simply fails its `LoadMsg` and is skipped. Sequences are immutable in
NATS, so a buffered sequence can never point at a different message.

**Correctness handling.**
- Rewind (e.g. redelivery retry moving `o.sseq` back): if `o.sseq <= o.mflast`
  the buffer is discarded and rebuilt.
- Forward cursor jumps: stale-but-ahead sequences are dropped; buffer refills.
- Filter set change: `updateConfig` calls `resetMultiFilterPrefetch`.
- Removed message: skip and advance `o.sseq` past it (forward progress; no
  re-scan on refill).
- EOF / `updateSkipped` / num-pending accounting: unchanged, because the helper
  returns the same `(nil, lastSeq, ErrStoreEOF)` contract the caller expects.

**Validation.** `Benchmark_FileStoreLoadNextMsgsMulti` shows the batched path
flat (~10–13 ms/op, ~110k allocs) across 10→90 filters while the per-message
path grows linearly (170 ms → 1.67 s; up to 8.1M allocs). ~16×–130× faster.

**Known limitation.** Prefetch depth is a fixed constant
(`multiFilterPrefetch = 256`); see Phase 4 notes for tuning it to the pull
request batch size.

---

## Phase 3 — memstore narrowed via `fss` ✅

**Goal.** Remove the memstore's naive linear `O(M)` multi-filter walk.

**What was implemented.**

- `nextMultiMatchLocked(sl, start)`: intersects the sublist against `ms.fss`
  (`IntersectGSL`), computing the lowest matching `First` (≥ start) and highest
  `Last` across matched subjects to produce `[fseq, lseq]` bounds, skipping
  leading/trailing gaps.
- `shouldLinearScanMulti(start)`: mirrors the single-filter `shouldLinearScan`
  heuristic — when `2*(LastSeq-start) < fss.Size()` a plain linear scan is
  cheaper than walking the subject tree, so skip the narrowing.
- `LoadNextMsgMulti` now uses these bounds; `LoadNextMsgsMulti` (batched) is
  added alongside and shares the same narrowing.

**Validation.** `TestStoreLoadNextMsgsMulti` runs across both Memory and File
stores and cross-checks batched output against per-message `LoadNextMsgMulti`
with scattered subjects and interior deletes.

---

## Phase 2 — Stateful cursor + cached matched-subject set ⚠️ Partial

**What is already true.** The consumer prefetch buffer is stateful *across
calls* — the expensive search runs once per ~256 delivered messages rather than
once per message.

**What is missing.** Every refill still re-derives everything from `o.sseq`:

1. it re-selects the starting block, and
2. inside each block, `collectMatchingMulti` / `firstMatchingMulti` re-walk the
   block `fss` and re-test `HasInterest` against the sublist.

The set of stream subjects matching the sublist changes only when *new
subjects* appear in the stream. Re-deriving it on every refill is wasted work
for high subject-cardinality streams.

### Approach A — cache the matched-subject set, keyed by a psim generation

Add a monotonically increasing generation counter to the file store that bumps
whenever a *new subject* is inserted into `psim` (not on every message — only on
first sighting of a subject). Cache, per consumer (or per filter sublist), the
resolved set of matching subjects plus the generation it was computed at.

- `filestore.go`:
  - Add `fs.psimGen uint64`, incremented in the code path that inserts a brand
    new entry into `psim` (subject seen for the first time).
  - Expose a cheap accessor, e.g. `(fs *fileStore) SubjectsEpoch() uint64`.
  - Optionally accept a caller-provided "resolved subjects" structure in a new
    batched entry point so the store can skip the `psim`/`fss` intersection when
    the epoch is unchanged.
- `consumer.go`:
  - Store `o.mfSubjects []string` (or a compact id set) and `o.mfSubjectsGen`.
  - On refill, if `store.SubjectsEpoch() == o.mfSubjectsGen`, reuse
    `o.mfSubjects`; else recompute (one `IntersectGSL` over `psim`) and update.

With the resolved literal-subject set in hand, the block scan can be replaced by
per-subject lookups (see Approach B) instead of a `HasInterest` test per
message.

**Trade-off.** Only helps when subject cardinality is high enough that the
intersection walk is a measurable fraction of refill cost. For low-cardinality
streams the current code is already cheap. Keep it behind the existing batched
path so low-cardinality consumers are unaffected.

### Approach B — persistent store-side iterator

Return an opaque iterator from the store that carries `{blockIndex, intra-block
position, resolved subject set, epoch}` and resumes instead of re-selecting the
block each refill.

- New type `multiIter` in `filestore.go` holding the cursor and a reference to
  the resolved subject set; `LoadNextMsgsMultiIter(it *multiIter, max, *seqs)`.
- Invalidate the iterator when: the epoch changes, the block it points at is
  expired/compacted/removed, or the consumer rewinds.
- Consumer holds `*multiIter` next to the prefetch buffer; resets it in the same
  places `resetMultiFilterPrefetch` is called today.

**Risk.** Iterator lifetime vs. block expiry/compaction is the tricky part —
the store mutates underneath. Safer to keep the iterator advisory (a hint that
is validated against `psim`/`bim` on use) rather than holding block pointers.

**Validation.** Extend `TestStoreLoadNextMsgsMulti` with a variant that mutates
subjects mid-iteration (adds a new subject, triggering an epoch bump) and
asserts the cached set is refreshed; add a high-cardinality benchmark case
(e.g. 100k distinct subjects, 90 filters) to `Benchmark_FileStoreLoadNextMsgsMulti`.

---

## Phase 4 — Adaptive selectivity ❌ Not started

**Goal.** Codify the operator instinct "just drop the filter and post-filter."
When a filter set matches a large fraction of the remaining stream, a contiguous
sequential scan with an inline interest test is cheaper (and more cache-friendly)
than the subject-tree machinery — but unlike dropping the filter, the server
still only delivers matching messages.

This is the same linear-vs-intersection decision that already exists *inside* a
block (`firstMatchingMulti`, `filestore.go` ~`if uint64(mb.fss.Size()) <
lseq-start`), lifted to the batch / consumer level.

### Implementation

1. **Estimate selectivity.** `LoadNextMsgsMulti` already gathers `psi.total` per
   matched subject during the first-call `psim` intersection. Sum it to get
   `matched` and compare to remaining stream messages (`state.LastSeq - start`).
   Define `selectivity = matched / remaining`.
2. **Choose a strategy** when `selectivity >= adaptiveScanThreshold` (start with
   ~0.5, make it a tunable const):
   - **High selectivity →** scan sequences contiguously from `start`, testing
     `sl.HasInterest(subj)` inline, collecting matches. No `fss` intersection,
     no per-subject ranges — just a linear walk that the OS/page cache loves.
   - **Low selectivity →** keep today's `psim`/`fss` block-skipping path.
3. **Where.** Implement as a branch at the top of `LoadNextMsgsMulti` (and the
   memstore equivalent), selecting between `collectMatchingMulti` (linear
   already) and a future sparse gatherer; for filestore the linear path is
   essentially what `collectMatchingMulti` does today, so Phase 4 is mostly
   *choosing not to* attempt block-skip / intersection when selectivity is high,
   plus avoiding the per-refill `psim` walk.
4. **Prefetch depth tuning (related).** Pass the waiting pull request's batch
   size / max-bytes down so `multiFilterPrefetch` adapts (small batches →
   smaller prefetch, avoiding over-reading; large batches → deeper prefetch).
   Source: `o.nextWaiting(sz)` in `consumer.go`.

**Risk.** Low. It only changes *which* already-implemented strategy runs; both
produce identical results. The threshold needs benchmarking to avoid
pathological mid-range choices.

**Validation.** Add benchmark cases spanning selectivity (filters matching ~1%,
~25%, ~50%, ~90% of the stream) and confirm the adaptive path tracks the better
of the two strategies at each point.

---

## Phase 5 — v2 interior-block skipping + merge heap ❌ Not started

**Goal.** Today block-skipping only works on the *first* probed block
(`checkSkipFirstBlockMulti`); interior empty blocks are scanned. The `psi` entry
only records `fblk`/`lblk` (first/last block for a subject), so we cannot tell
whether a subject has any message in an arbitrary interior block. This is the
pre-existing `// For v2 will track all blocks that have matches for psim` TODO.

### Approach A — track per-subject block membership in `psim`

Extend `psi` to record the set of blocks a subject appears in (a compact bitset
or sorted block-index slice), maintained on store/remove.

- `filestore.go`: change `psi` from `{total, fblk, lblk}` to also carry
  `blocks` (e.g. `*avl.SequenceSet` of block indices, or a roaring-style
  bitset). Update everywhere `psim` is mutated (store, remove, compaction,
  block expiry).
- New helper `nextBlockIndexMulti(sl, afterIdx) (int, error)`: intersect the
  sublist against `psim`, union the per-subject block sets, and return the
  smallest block index `> afterIdx` present in the union — enabling interior
  skips on *every* block, not just the first.
- `LoadNextMsgsMulti` / `LoadNextMsgMulti`: when a block yields no matches, call
  `nextBlockIndexMulti` to jump to the next block that can contain a match.

**Cost.** Memory per subject grows from 2 × uint32 to O(#blocks-containing-it);
mitigated by bitsets and the fact that most subjects live in few blocks. This is
the largest change and most invasive to the store's write paths — gate it
carefully and benchmark memory.

### Approach B — per-subject min-heap merge (literal-heavy filters)

For filter sets that are mostly literal subjects, maintain a min-heap of
`(nextSeq, subject)` and pop the minimum to deliver, then advance that subject's
cursor to its next sequence. Steady-state delivery becomes `O(log F)` per
message independent of block density.

- Requires per-subject *sequence* iteration, which `fss`/`psim` do not provide
  directly (they store First/Last/total, not the full sequence list). Options:
  - derive next-seq per subject by scanning that subject's `[First,Last]` range
    within the current block (bounded), or
  - build the heap lazily from Approach A's per-block membership so each pop only
    touches blocks known to contain the subject.
- Best combined with Approach A; on its own it still pays block scans to find a
  subject's next sequence.

**When to do this.** Only if Phases 1/3/4 do not close the gap at very high
filter counts with very sparse interior matches. For most consumer workloads the
batched linear gather per matching block (Phase 1) already removes the
per-message tax.

**Validation.** A norace test with matches deliberately clustered into a few
blocks separated by many empty interior blocks, asserting the number of blocks
touched per batch is bounded by the number of blocks actually containing
matches (instrument via a counter), plus a memory benchmark for the extended
`psi`.

---

## Suggested order for the remaining work

1. **Phase 4** — cheapest, highest value, low risk; also folds in prefetch-depth
   tuning. Mostly a strategy-selection change over already-implemented paths.
2. **Phase 2 (Approach A)** — helps high subject-cardinality streams; medium
   effort, isolated behind the batched path.
3. **Phase 5** — largest/most invasive; only if profiling shows interior
   sparseness is still a bottleneck after 1/2/3/4.
