# Review: PR #7840 — memstore: speed up LoadNextMsg when using wildcard filter

**Author:** @sciascid
**PR:** https://github.com/nats-io/nats-server/pull/7840

## Summary

This PR optimizes `LoadNextMsg` in the memstore when using wildcard filters. It introduces:

1. **`MatchUntil` on `SubjectTree`** — A new traversal method that allows early termination via a callback return value.
2. **`nextWildcardMatchLocked`** — Uses `MatchUntil` to find sequence bounds for wildcard matches, stopping early when a subject with `First <= start` is found.
3. **`nextLiteralMatchLocked`** — Extracted literal subject bound lookup.
4. **`shouldLinearScan`** — Extracted heuristic for choosing between linear scan and tree-based lookup.

The refactoring cleanly separates concerns and eliminates the intermediate `subs` slice allocation from the old code path.

## Correctness Analysis

### `MatchUntil` (stree.go)

The implementation is clean. The internal `match` function is modified to return `bool`, propagating the callback's stop signal through all recursive paths. Every `return`, callback invocation, and recursive `match` call correctly handles the early-termination signal. The existing `Match` method wraps the new callback signature with an adapter that always returns `true`, preserving backward compatibility.

### `nextWildcardMatchLocked` — Early termination is correct

The early termination condition (`first <= start` → stop) is the key insight of this PR. The reasoning is sound:

- When `first <= start`, we know at least one visited subject has `First <= start` **and** `Last >= start` (the `start > ss.Last` skip guarantees this). So the returned range `[start, last]` contains at least one matching message.
- The linear scan in `loadNextMsgLocked` checks **all** matching subjects in the scan range (via `subjectIsSubsetMatch`), not just the subjects that were visited during `MatchUntil`. So messages from unvisited subjects within the range are still found.
- The narrower bounds from early termination reduce the scan range, which is the source of the performance improvement.
- Unvisited subjects with messages **beyond** the returned `last` are not a concern because `LoadNextMsg` only needs to find the *next* matching message. Subsequent calls with incrementing `start` will discover those messages.

I verified this holds regardless of the subject tree's traversal order (which is insertion-order dependent in `node4`, not lexicographic — per the comment in `node4.go:30`).

### `nextLiteralMatchLocked`

Straightforward. Agree with @neilalexander's suggestion to simplify the conditional to `return max(start, ss.First), ss.Last, true`.

### `recalculateForSubj` — unconditional call

The new code calls `recalculateForSubj` unconditionally (the old code guarded it with `ss.firstNeedsUpdate || ss.lastNeedsUpdate`). This is functionally correct since `recalculateForSubj` checks those flags internally. The function call overhead is negligible.

## Bug: Benchmark does not reset state between iterations

In `Benchmark_MemStoreLoadNextMsgFiltered`, the `start` and `count` variables are declared **outside** the `b.Loop()` loop and never reset:

```go
var start uint64
var count uint64
expectedMatches := uint64(tc.msgs / tc.matchingMsgEvery)

for b.Loop() {
    for {
        _, start, err = ms.LoadNextMsg(tc.filter, tc.wc, start, &smv)
        start++
        if err == ErrStoreEOF {
            require_Equal(b, count, expectedMatches)
            break
        }
        require_NoError(b, err)
        count++
    }
}
```

After the first `b.Loop()` iteration, `start` is past `LastSeq` and `count == expectedMatches`. Every subsequent iteration immediately hits `ErrStoreEOF` and does no meaningful work. The `require_Equal(b, count, expectedMatches)` still passes because `count` was never reset.

**Fix:** Reset both variables at the start of each iteration:

```go
for b.Loop() {
    start = 0
    count = 0
    for {
        ...
    }
}
```

Without this fix, the benchmark only measures one iteration amortized over many no-op iterations, producing artificially low ns/op numbers.

## Suggestion: Reconsider the `linearScanMaxFSS` threshold for wildcards

The `shouldLinearScan` condition is preserved identically from the original code:

```go
return isAll || 2*int(ms.state.LastSeq-start) < ms.fss.Size() || (wc && ms.fss.Size() > linearScanMaxFSS)
```

The third condition `(wc && ms.fss.Size() > 256)` forces a linear scan for **any** wildcard when the subject tree has more than 256 **total** entries (not just matching ones). This threshold was appropriate for the old `Match()` + `Find()` approach, which was O(matching subjects). But `MatchUntil` with early termination can be significantly faster:

- It only visits matching branches of the tree (not all entries).
- It stops as soon as it finds a subject with `First <= start`.
- In the best case, it visits a single matching subject.

With the current threshold, the wildcard optimization only triggers when there are 256 or fewer total subjects in the store — a condition that may rarely hold in production workloads with many subjects. Consider raising or removing this threshold to allow `MatchUntil` to benefit more scenarios. The benchmark's `wildcard_linear_scan` case (1000+ subjects) demonstrates exactly this: it's forced into linear scan even though `MatchUntil` would likely be faster.

## `MatchUntil` return value

Regarding @neilalexander's question: the `bool` return value from `MatchUntil` (completed vs. stopped early) is not used in production code — `nextWildcardMatchLocked` discards it. It's only used in `TestSubjectTreeMatchUntil`. Returning it is still good API design as it could be useful for future optimizations or callers that need to know if the full tree was traversed.

## Test coverage

The tests are well-structured:

- `TestMemStoreNextWildcardMatch`: Thorough coverage of wildcard bounds at various start positions, including past-end and non-matching filters.
- `TestMemStoreNextLiteralMatch`: Same for literal filters.
- `TestSubjectTreeMatchUntil`: Validates early termination behavior directly.
- `TestStoreLoadNextMsgWildcardStartBeforeFirstMatch`: Important regression test via `testAllStoreAllPermutations` — covers the edge case where all matching subjects have `First > start`, exercising both memstore and filestore. This was likely the motivating scenario for the PR.

## Minor nits

1. Missing comma after "A match was found" (`memstore.go`, comment in `nextWildcardMatchLocked`):
   ```
   // A match was found adjust the bounds accordingly
   ```
   Should be:
   ```
   // A match was found, adjust the bounds accordingly.
   ```

2. The `if/else` in `nextWildcardMatchLocked` could be simplified since the two branches differ only in the return value:
   ```go
   // Current:
   if first <= start {
       return false
   } else {
       return true
   }

   // Simpler:
   return first > start
   ```

3. **Unnecessary `string(subj)` allocation in `nextWildcardMatchLocked`**: The `MatchUntil` callback unconditionally calls `ms.recalculateForSubj(string(subj), ss)`, which converts `[]byte → string` on every visited subject. The old code guarded this with `ss.firstNeedsUpdate || ss.lastNeedsUpdate` and used an already-allocated string. In steady state (no recent deletions), these flags are typically false, so the allocation is wasted. Consider:
   ```go
   if ss.firstNeedsUpdate || ss.lastNeedsUpdate {
       ms.recalculateForSubj(string(subj), ss)
   }
   ```

4. **`Match` wrapper closure**: The existing `Match` method now wraps its callback in a `func(...) bool { cb(...); return true }` closure on every call. There are 20+ `Match` callers across `memstore.go` and `filestore.go` (NumPending, numFilteredPending, etc.). This adds a small per-call heap allocation + indirect function call overhead. Unlikely to be measurable, but worth noting since some of these are hot paths. An alternative would be to keep a non-returning `matchAll` variant, but the maintenance cost of two `match` functions probably isn't worth it.

5. **Benchmark stream config mismatch**: The benchmark uses `Subjects: []string{"foo.*"}` but stores 3-token subjects like `"foo.baz.0"`. The memstore doesn't enforce subject validation at the storage layer (that's done by JetStream routing), so it works, but `Subjects: []string{"foo.>"}` would be more realistic.

6. **FileStore parallel opportunity**: The `fileStore.LoadNextMsg` (`filestore.go:8508`) has its own skip-ahead logic with a notably higher wildcard threshold (`wcMaxSizeToCheck = 64 * 1024` vs memstore's `linearScanMaxFSS = 256`). The `MatchUntil` pattern could eventually benefit `fileStore` too, but that's a separate effort.

## Benchmark Results (with `start`/`count` reset fix applied)

Both runs used the same benchmark code with `start` and `count` properly reset inside `b.Loop()`.

**Machine:** Intel Xeon Platinum 8581C @ 2.10GHz, 16 cores, Go 1.25.7 linux/amd64

| Test Case | Base (before PR) | PR (after) | Speedup |
|---|---|---|---|
| `wildcard_linear_scan` | 3,980 ms/op (1 iter) | 4,211 ms/op (1 iter) | ~same (expected: both use linear scan) |
| **`wildcard_bounded_scan`** | **4,012 ms/op (1 iter)** | **322.8 us/op (3768 iters)** | **~12,400x faster** |
| `literal_bounded_scan` | 1,218 ms/op (1 iter) | 1,245 ms/op (1 iter) | ~same (expected: same code path) |

The `wildcard_bounded_scan` case shows the dramatic improvement: from ~4 seconds to ~323 microseconds per full scan of 100 matching messages among 10M total. This confirms the `MatchUntil` + early termination optimization is highly effective for the targeted scenario (wildcard filter, bounded FSS, sparse matches).

The `wildcard_linear_scan` and `literal_bounded_scan` cases show no regression, as expected — these take the same code paths as before.

All PR tests pass:
- `TestMemStoreNextWildcardMatch` — PASS
- `TestMemStoreNextLiteralMatch` — PASS
- `TestStoreLoadNextMsgWildcardStartBeforeFirstMatch` (Memory + File) — PASS
- `TestSubjectTreeMatchUntil` — PASS

## Verdict

The core optimization is correct and well-designed. The `MatchUntil` approach elegantly avoids both the intermediate slice allocation and the redundant `Find()` lookups of the old code. The early termination logic is sound. Benchmark confirms ~12,400x speedup for the targeted wildcard bounded scan scenario with no regressions.

The benchmark reset bug should be fixed before merge to ensure the reported numbers are accurate. The `linearScanMaxFSS` threshold is worth revisiting as a follow-up to unlock the optimization for larger subject trees.
