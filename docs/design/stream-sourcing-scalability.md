# Design Proposal: Scaling JetStream Stream Sourcing

| | |
|---|---|
| **Status** | Draft / Proposal |
| **Area** | JetStream — stream sourcing (`server/stream.go`, `server/consumer.go`) |
| **Author** | source-consumers-linear-scan investigation |
| **Date** | 2026-06-06 |
| **Related** | `jetstream_sourcing_scaling_test.go`, report `docs/reports/source-consumers-linear-scan.html` |

---

## 1. Summary

JetStream stream **sourcing** scales poorly in the number of configured sources `S`.
Several control-plane operations — consumer setup on leader election/restart, `STREAM.UPDATE`,
and `STREAM.INFO` — are **O(S²)** because they map a source *index name* (`iname`) back to its
`*StreamSource` config through a linear walk (`stream.streamSource()`, `stream.go:3793`) that is
itself called once per source.

The message **data path is already O(1)** and is not changed by this proposal.

We propose maintaining two small derived indexes alongside `cfg.Sources` so that
`iname → config` and `streamName → sources` become O(1) lookups. This collapses the affected
operations from O(S²) to O(S) with no behavioural change, no wire-format change, and no migration.

---

## 2. Motivating scenario

> A JetStream instance runs on an **edge leafnode site** with a stream in its own **JS domain**.
> From the **HUB** side we create a stream that **sources** from that edge leafnode stream. Repeat
> across many edge sites.

![Edge/HUB sourcing topology](diagrams/06-topology.svg)

This topology stresses exactly the code paths that scale badly, and adds a second amplifier on top:

1. **Many external sources on one hub stream.** The hub's aggregating stream lists one source per
   edge site. Each source is *external* (`StreamSource.External` set, with a per-domain
   `ApiPrefix` such as `$JS.edge1.API.>`). Setting up / refreshing these consumers, and answering
   `STREAM.INFO`, all run the O(S²) paths described in §3.

2. **Stream-name collisions across domains.** Edge sites commonly use the *same* stream name
   (e.g. `ORDERS`) in different JS domains. The sourcing `iname` distinguishes them only by
   `getHash(External.ApiPrefix)` (`composeIName`, `stream.go:1079`). Because every source shares the
   same `Name`, `createSourcingConsumerHash` (`consumer.go:6354`) takes its **strict-hash branch**
   *and* performs an O(S) by-name walk — once per source — yielding another independent O(S²) factor,
   plus a `getHash(ApiPrefix)` recomputation each time.

So the edge/hub fan-in is the worst-case combination: large `S`, all-external, all name-colliding.

---

## 3. Background & problem

A sourcing stream keeps two halves of state, in two different shapes:

![Map vs slice asymmetry](diagrams/01-asymmetry.svg)

* **Runtime state** lives in a map — `mset.sources map[string]*sourceInfo` (`stream.go:515`),
  keyed by `iname`, so `mset.sources[iname]` is O(1).
* **Config** lives in a slice — `mset.cfg.Sources []*StreamSource` — with no `iname` index.

Whenever code holds an `iname` and needs the *config* (the external `ApiPrefix`/`DeliverPrefix`,
filter, transforms, start policy), it must walk the slice:

```go
// stream.go:3793
func (mset *stream) streamSource(iname string) *StreamSource {
    for _, ssi := range mset.cfg.Sources { // O(S)
        if ssi.iname == iname {
            return ssi
        }
    }
    return nil
}
```

That walk is cheap alone, but it is invoked from inside loops that already iterate over every source:

![Loop inside a loop becomes O(S^2)](diagrams/02-quadratic.svg)

### Affected operations

| Operation | Trigger | Location | Cost today |
|---|---|---|---|
| `setupSourceConsumers` → `trySetupSourceConsumer` → `streamSource` | leader election / restart / R1 start | `stream.go:4805`, `3911` | **O(S²)** |
| `STREAM.UPDATE` source diff (+ `getSourcingConsumerIName`) | config update | `stream.go:2504`, `2509` | **O(S²)** |
| `createSourcingConsumerHash` by-name dedup walk | per source in update/setup | `consumer.go:6354` | **O(S²)** when names collide |
| `sourcesInfo` → `sourceInfo` → `streamSource` | `STREAM.INFO` | `stream.go:2953`, `2992` | **O(S²)** |
| `setStartingSequenceForSources` (the standing `// TODO ... linear walk`) | config update | `stream.go:4566`, `4641` | up to **O(M·S²)** |
| `startingSequenceForSources` legacy-header match | setup / restart | `stream.go:4694`, `4789` | up to **O(M·S)** |

`M` = number of messages scanned backwards through the store; `S` = number of configured sources.

### What is *not* a problem

The hot path stays O(1): each source's delivery subscription closes over its own `si`
(`stream.go:4161`) and hands it straight to `processInboundSourceMsg(si, …)` (`stream.go:4314`).
Messages are multiplexed onto one goroutine (`processAllSourceMsgs`, `stream.go:4187`).

![Hot path vs control plane](diagrams/04-hot-vs-control.svg)

---

## 4. Goals / Non-goals

**Goals**

* Make the source control-plane O(S) instead of O(S²).
* Keep the change behaviour-preserving: no wire format, no config, no on-disk change.
* Specifically remove the cost amplifiers in the edge→hub external/name-colliding case.

**Non-goals**

* Changing the data-path throughput model or sharding the single source goroutine
  (`TODO(dlc)` at `stream.go:4187`) — separate, larger work.
* Changing how `iname`/`cname` are composed, or the source header format.

---

## 5. Proposal

Derive two indexes from `cfg.Sources`, rebuilt whenever the sources list changes, under `mset.mu`.

![Proposed indexes](diagrams/05-proposal.svg)

```go
// stream struct, near stream.go:515
sources             map[string]*sourceInfo   // existing runtime state
cfgSources          map[string]*StreamSource // NEW: iname        -> config   (O(1))
cfgSourceNameCount  map[string]int           // NEW: stream name  -> count    (dedup, O(1))
```

```go
// Lock held. Pure derived state — rebuilt on any cfg.Sources mutation.
func (mset *stream) rebuildSourcesIndex() {
    cs := make(map[string]*StreamSource, len(mset.cfg.Sources))
    nc := make(map[string]int, len(mset.cfg.Sources))
    for _, ssi := range mset.cfg.Sources {
        if ssi.iname == _EMPTY_ {
            ssi.setIndexName()
        }
        cs[ssi.iname] = ssi
        nc[ssi.Name]++
    }
    mset.cfgSources, mset.cfgSourceNameCount = cs, nc
}

// O(1) replacement for the linear walk.
func (mset *stream) streamSource(iname string) *StreamSource {
    return mset.cfgSources[iname]
}
```

**Call sites that must rebuild the index** (the only places `cfg.Sources` changes):

* initial config load / `resetSourceInfo` (`stream.go:4659`),
* the `STREAM.UPDATE` source diff where sources are appended/removed (`stream.go:2530`, `2570`).

**Tier 1 — iname index (highest impact, smallest change).** Replacing `streamSource()` with the map
read drops `setupSourceConsumers`, `sourceInfo`/`sourcesInfo`, and the two `streamSource()` calls in
`setStartingSequenceForSources` (the `// TODO`) from O(S²)/O(S) to O(1) each.

**Tier 2 — name-count + name index (covers the edge/hub case).**
* In `createSourcingConsumerHash`, replace the by-name loop (`consumer.go:6361`) with
  `if mset.cfgSourceNameCount[ssi.Name] > 1 { … strict hash … }`.
* For legacy-header matching (`stream.go:4789`, `4638`), add a `map[streamName][]*StreamSource`
  (or reuse the count map to gate a fallback) so the inner by-name walk becomes a lookup.
* Optionally memoize `getHash(ApiPrefix)` per source so external `iname`/`cname`/INFO construction
  stops re-hashing.

**Tier 3 — validation.** Add a `BenchmarkSetupSourceConsumers` over `S ∈ {100, 500, 1000}` and a
down-scaled, *un-skipped* variant of `TestStreamSourcingScalingSourcingManyBenchmark`
(`jetstream_sourcing_scaling_test.go:110`), including an external/domain case. Run with `-race`.

### Expected effect

![Complexity before/after](diagrams/03-curves.svg)

| Operation | Before | After |
|---|---|---|
| `setupSourceConsumers` | O(S²) | **O(S)** |
| `STREAM.UPDATE` (sources) | O(S²) | **O(S)** |
| `STREAM.INFO` (`sourcesInfo`) | O(S²) | **O(S)** |
| `createSourcingConsumerHash` dedup | O(S) per call | **O(1)** per call |
| `setStartingSequenceForSources` inner | O(S²) walk | **O(1)** lookups |

---

## 6. Alternatives considered

* **Sort `cfg.Sources` and binary-search by `iname`.** O(log S) not O(1), and re-sorting on every
  mutation complicates the append-based update path; a map is simpler and faster.
* **Store the `*StreamSource` pointer on `sourceInfo`.** Tempting, but `sourceInfo` is keyed by
  `iname` already and some callers only have the config slice; a standalone index covers all callers
  uniformly and keeps the two structs decoupled.
* **Do nothing / document only.** The edge→hub fan-in is a real, growing topology; leaving O(S²) in
  place means leader elections and INFO polls degrade visibly as sites are added.

---

## 7. Risks & compatibility

* **Index drift.** The derived maps must be rebuilt on *every* `cfg.Sources` mutation. Mitigation:
  funnel all mutations through `rebuildSourcesIndex()` and assert `len(cfgSources) == len(cfg.Sources)`
  in a debug/test build.
* **Uniqueness.** `iname` is already validated unique per stream (`stream.go:1963-1974`,
  *"check sources for duplicates"*), so the map key is well-defined.
* **Concurrency.** The indexes are read/written only under `mset.mu`, exactly like `mset.sources`.
* **Wire/disk/config:** unchanged. Pure in-memory performance change.

---

## 8. Rollout

1. Tier 1 (iname index) as a self-contained, easy-to-review PR + benchmark.
2. Tier 2 (name index + hash memoization) in a follow-up, motivated by the edge/hub benchmark.
3. Keep the existing scaling test as the acceptance gate; add the external/domain variant.

---

## Appendix — key references

| Symbol | File:line | Role |
|---|---|---|
| `streamSource` | `stream.go:3793` | the linear scan (Tier 1 target) |
| `sources` map / `cfg.Sources` slice | `stream.go:515` | the asymmetry |
| `composeIName` / `composeCName` | `stream.go:1079` / `1129` | iname incl. `getHash(ApiPrefix)` |
| `setupSourceConsumers` | `stream.go:4805` | O(S²) setup loop |
| `trySetupSourceConsumer` | `stream.go:3897` | external `ApiPrefix` rewrite, per-source `streamSource` |
| `setStartingSequenceForSources` | `stream.go:4566` | TODO-flagged O(S²) |
| `startingSequenceForSources` | `stream.go:4694` | reverse-scan resume |
| `createSourcingConsumerHash` | `consumer.go:6354` | by-name dedup walk (Tier 2 target) |
| `sourceInfo` / `sourcesInfo` | `stream.go:2964` / `2953` | INFO O(S²) |
| `processInboundSourceMsg` | `stream.go:4314` | O(1) hot path (unchanged) |
