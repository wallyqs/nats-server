# JetStream Standalone-to-Cluster Mode Switching

## Overview

When a NATS server running in standalone mode is reconfigured to join an existing
cluster, its locally-created JetStream streams must be adopted into the cluster's
metadata layer. This document describes the mechanism, edge cases, and important
invariants to keep in mind.

---

## Core Concepts

### The PTC (Promoted-to-Cluster) Flag

When a server joins a cluster for the first time with no prior meta RAFT state, it
sets `cc.ptc = true` on the `jetStreamCluster` struct. This flag is initialized at
startup in `startClusterMode`:

```
ptc: bootstrap || hasPTCMarker(storeDir)
```

It is true if either:
- `bootstrap` is true (first time joining, no existing meta RAFT log), OR
- A `.ptc` marker file exists on disk from a previous run.

The ptc flag governs two critical behaviors:
1. **Orphan sweep protection**: Streams without meta assignments are adopted rather
   than deleted.
2. **Conflict preservation**: Streams whose names conflict with existing cluster
   assignments are stopped but their data is preserved on disk.

### The PTC Marker File

A zero-byte file named `.ptc` stored in the meta RAFT directory
(`<store_dir>/jetstream/<sys_account>/_js_/_meta_/.ptc`). It persists the ptc state
across server restarts. The lifecycle is:

| Condition | Action |
|---|---|
| Orphan streams exist | Marker is written (before adoption proposal) |
| Name conflicts exist (`ptcConflicts > 0`) | Marker is written |
| All streams adopted, no conflicts | Marker is removed |

### Stream Recovery from Disk

On startup, JetStream recovers all streams from disk into `jsa.streams` before the
meta RAFT layer replays. The recovery path:

1. Reads `<store_dir>/jetstream/<account>/streams/<name>/` directories
2. Validates `meta.inf` checksum against `meta.sum` using
   `highwayhash(sha256(directoryName))` as the key
3. Loads message data via either:
   - **Fast path** (`recoverFullState`): Reads `index.db`, validates outer checksum
     with `sha256(streamName)` key
   - **Slow path** (`recoverMsgs`): Rebuilds state from `.blk` files, validating
     per-message checksums

This means **all local streams are in memory before meta RAFT replay begins**.

---

## Adoption Flow

### Timeline

```
Server Start
  |
  v
Stream Recovery (all local streams loaded into jsa.streams)
  |
  v
Meta RAFT Replay (processStreamAssignment for each entry)
  |  - Streams assigned to this node: processClusterCreateStream
  |  - Streams NOT assigned to this node but exist locally:
  |      ptc=true  -> stop without deleting (preserve data)
  |      ptc=false -> removeStream (deletes data)
  |
  v
Meta Recovery Complete
  |
  v
30 seconds later: checkForOrphans fires
  |  - Finds streams in jsa.streams with no meta assignment -> orphans
  |  - ptc=true  -> adoptLocalStreams (propose via meta RAFT)
  |  - ptc=false -> delete orphans
  |
  v
Adoption proposals replayed -> streams become part of cluster
```

### Leader vs Non-Leader Adoption

- **Meta leader**: Calls `adoptLocalStreams()` directly, which iterates orphan
  streams and calls `proposeStreamAdoption()` for each.
- **Non-leader**: Calls `requestAdoptionFromLeader()`, sending an internal request
  to the meta leader, which calls `handleAdoptStreamRequest()`.

### What `proposeStreamAdoption` Does

1. Sets `Replicas = 1` (operator can scale up later)
2. Creates a single-peer `raftGroup` with this node as the only member
3. Proposes a `streamAssignment` via meta RAFT
4. Also proposes `consumerAssignment` for each consumer on the stream

---

## Edge Cases

### 1. Duplicate Stream Names

**Scenario**: Standalone server has stream "ORDERS"; the cluster already has a
different stream also named "ORDERS".

**What happens**:
- During meta RAFT replay, `processStreamAssignment` processes the cluster's
  "ORDERS" assignment.
- This node is NOT a member of that assignment.
- With `ptc=true`: The local stream is stopped (`mset.stop(false, false)`) but
  data is preserved on disk. `ptcConflicts` is incremented.
- With `ptc=false`: The local stream would be deleted by `removeStream`.
- When `checkForOrphans` fires, the stream is no longer in `jsa.streams` (it was
  stopped), so it is not found as an orphan.
- Because `ptcConflicts > 0`, the ptc marker is written/preserved.
- On restart, the cycle repeats: stream is recovered from disk, meta replay stops
  it again, data stays on disk.

**Resolution**: The operator must:
1. Stop the server
2. Rename the conflicting stream using `util/rename-stream.go`
3. Restart — the renamed stream will be adopted under its new name

**Important**: `adoptLocalStreams` explicitly skips streams where `asa[name] != nil`
(a cluster assignment already exists for that name). It logs:
`"JetStream skipping adoption of '<account> > <name>': stream already exists in cluster"`

### 2. Consumer-Stream Association is Structural

Consumer directories live inside the stream directory:
```
streams/<streamName>/obs/<consumerName>/
```

`ConsumerConfig` has **no stream name field**. The association is purely based on
directory hierarchy. This means:
- When a stream is renamed on disk, consumers are automatically moved (they're
  inside the stream directory).
- Consumer state (ack floor, pending counts) is preserved across renames.
- The `Stream` field on `ConsumerInfo` is populated at runtime from the parent
  stream, not stored in the consumer's config.

### 3. Stream Rename Requires Checksum Rewriting

NATS uses highwayhash for integrity at **three distinct levels**, each with a
different key derivation:

| Level | Key Derivation | File |
|---|---|---|
| `meta.inf` / `meta.sum` | `sha256(directoryName)` | `meta.inf`, `meta.sum` |
| Stream state (`index.db`) | `sha256(streamName)` via `fs.hh` | `msgs/index.db` |
| Per-message in `.blk` files | `sha256("streamName-blockIndex")` via `mb.hh` | `msgs/*.blk` |

**Critical**: The per-block key includes the block index. The function
`hashKeyForBlock(index uint32)` returns `fmt.Sprintf("%s-%d", fs.cfg.Name, index)`.
Block index is extracted from the filename (e.g., `1.blk` -> index 1).

A correct rename must:
1. Update `meta.inf` with the new name
2. Recompute `meta.sum` with `sha256(newName)` as the hash key
3. For each `.blk` file: rewrite every message record's 8-byte trailing checksum
   using `sha256("newName-blockIndex")` as the key
4. Remove `index.db` (or rewrite it — removing is simpler and more robust)
5. Rename the directory

If any step is wrong, the server either:
- Fails `recoverFullState` and falls back to `recoverMsgs` (if `index.db` is wrong)
- Silently drops messages with bad checksums during `recoverMsgs` (if `.blk`
  checksums are wrong)

### 4. The `index.db` Question

Removing `index.db` is not strictly necessary. If left with stale checksums:
1. `recoverFullState()` fails the outer checksum check → falls back to `recoverMsgs()`
2. `recoverMsgs()` rebuilds from `.blk` files → writes a fresh `index.db`
3. Server works correctly, just with a slower first startup

Removing it avoids a misleading checksum warning in logs and the overhead of reading
the entire state file before discovering the mismatch. For large streams, the fast
recovery path can be significantly faster, but after a rename the rebuild is
unavoidable either way.

### 5. `mset.stop(false, false)` vs `removeStream`

| | `mset.stop(false, false)` | `removeStream` / `mset.stop(true, false)` |
|---|---|---|
| Removes from `jsa.streams` | Yes | Yes |
| Stops consumers | Yes (without deleting) | Yes (with deleting) |
| Deletes store data | **No** | Yes (`store.Delete()`) |
| Deletes stream directory | **No** | Yes (`os.RemoveAll`) |
| Releases resources | Yes | Yes |

The ptc conflict path uses `stop(false, false)` to free memory while preserving
data on disk.

### 6. Store Directory Prefix

`opts.StoreDir` does **not** include the `"jetstream/"` prefix.
`js.config.StoreDir` **does** (it's added during JetStream initialization).

When constructing paths from server options (e.g., in tests), you must include
`JetStreamStoreDir` (`"jetstream/"`) explicitly:
```
filepath.Join(opts.StoreDir, JetStreamStoreDir, account, "streams", name)
```

### 7. Meta RAFT Replay Ordering

During recovery, meta RAFT entries are batched into `recoveryUpdates`:
1. Consumer removals are processed first
2. Stream removals second
3. Stream additions third (this is where `processStreamAssignment` fires)
4. Stream updates fourth
5. Consumer additions/updates last

`checkForOrphans` fires 30 seconds **after** meta recovery completes, via
`time.AfterFunc(30*time.Second, js.checkForOrphans)`. This means all meta entries
have been replayed and `ptcConflicts` is finalized before the orphan check runs.

### 8. Restart Cycle for Conflicting Streams

On each restart of a server with unresolved name conflicts:
1. Stream is recovered from disk into `jsa.streams` (ptc marker exists → `ptc=true`)
2. Meta RAFT replays → `processStreamAssignment` stops the stream, increments
   `ptcConflicts` (reset to 0 on each startup since it's in-memory)
3. `checkForOrphans` → no orphans in `jsa.streams`, but `ptcConflicts > 0` →
   writes ptc marker
4. Data preserved, cycle repeats on next restart

This is stable and intentional. The data persists until the operator resolves the
conflict.

### 9. Message Record Format in `.blk` Files

Each message record in a block file has this layout:
```
[4 bytes: record length (high bit = has headers)]
[8 bytes: sequence number]
[8 bytes: timestamp]
[2 bytes: subject length]
[subject bytes]
[optional: 4 bytes header length (if headerBit set)]
[payload bytes]
[8 bytes: highwayhash checksum]
```

The checksum covers: `hdr[4:20]` (sequence + timestamp), subject, and
payload/headers — but NOT the record length or the checksum itself.

### 10. Non-Leader Adoption Request Path

When the ptc node is not the meta leader:
1. `requestAdoptionFromLeader` sends a `jsAdoptionRequest` to the leader
2. The leader's `handleAdoptStreamRequest` checks if the stream name already exists
   in cluster metadata
3. If it exists: logs a notice and skips (same duplicate-name protection)
4. If not: proposes adoption with the requesting node as the single peer

---

## The `rename-stream` Tool

`util/rename-stream.go` is a standalone CLI tool that performs offline stream
renaming. It must be run while the server is stopped.

### Usage
```
go run util/rename-stream.go -dir <store_dir> -old <name> -new <name> [-account $G] [-dry-run]
```

### Steps Performed
1. Updates `meta.inf` with the new stream name
2. Recomputes `meta.sum` with `sha256(newName)` key
3. Rewrites per-message checksums in all `.blk` files using per-block keys
4. Removes `index.db` so server rebuilds state on startup
5. Renames the stream directory

### Workflow for Resolving Name Conflicts
```
1. Stop the promoted server
2. Run: go run util/rename-stream.go -dir /data/nats -old ORDERS -new ORDERS_STANDALONE
3. Start the server
4. The renamed stream is adopted as "ORDERS_STANDALONE"
5. Both streams now coexist in the cluster
```

---

## Test Coverage

| Test | What It Verifies |
|---|---|
| `TestJetStreamStandaloneToClusteredModeSwitch` | Basic adoption: stream and consumers promoted to cluster, data intact |
| `TestJetStreamStandaloneToClusteredNonLeaderAdoption` | Adoption works when the ptc node is not the meta leader |
| `TestJetStreamStandaloneToClusteredDuplicateStreamName` | Conflicting stream data preserved on disk, survives restart, cluster stream intact |
| `TestJetStreamStandaloneToClusteredPTCMarkerLifecycle` | Marker written during adoption, stream data intact on disk |
| `TestJetStreamStandaloneRenameStreamThenPromote` | Offline rename + promote: renamed stream adopted with correct data |
| `TestJetStreamStandaloneRenameStreamConsumerState` | Consumer ack state (ack floor, pending) preserved across rename |
