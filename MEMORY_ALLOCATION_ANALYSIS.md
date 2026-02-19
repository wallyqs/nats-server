# NATS Server — Memory Allocation Analysis

Analysis of dominant `alloc_space` sites from heap profiles taken on production
nodes.  All line references are to the current `main` branch.

---

## Executive Summary

The top 10 allocators fall into three categories:

| Category | Ranks | Cumulative alloc_space | Root cause |
|----------|-------|------------------------|------------|
| Message deserialization & copy | 1, 6 | 120–240 GB | Every stored message is copied out of the block cache so the 8 MB slabs can be recycled |
| Network I/O buffers | 2, 3, 5 | 121–348 GB | Per-connection read/write buffers grow via `make` without pooling; `sync.Pool` New functions fire when pool is cold |
| Compression & encoding | 4, 8 | 56–123 GB | s2 writer internal buffers reallocated on `Reset`; `EncodedStreamState` allocates per-call |

The single largest *cumulative* frame is `processMsgResults` (rank 10,
148–411 GB) because it is the fan-out hub — every allocation in the
delivery path rolls up into it.

---

## Per-Allocator Deep Dive

### 1. `msgFromBufEx` — 85–191 GB

**File:** `server/filestore.go:8180`

Every call with `doCopy=true` (via `msgFromBuf`) makes three allocations:

| What | Code | Avoidable? |
|------|------|------------|
| `new(StoreMsg)` | line 8225 | Partially — callers can pass a reusable `*StoreMsg` |
| `append(sm.buf, data[bi:end]...)` | lines 8247, 8262 | No — required to decouple from 8 MB slab |
| `string(data[:slen])` | line 8279 | Could intern high-cardinality subjects |

**Why copies are necessary:** The comment at line 8229 explains: block cache
buffers (`blkPoolBig` 8 MB slabs) are recycled, so callers must never hold
references into them.  The copy is the price of slab reuse.

**Opportunities:**
- **Pass `*StoreMsg` from caller** — `fetchMsg` already supports this (`sm`
  parameter); ensuring all hot-path callers reuse a single `StoreMsg` per
  goroutine avoids the `new(StoreMsg)` allocation.
- **Subject interning** — For streams with a bounded subject space,
  deduplicating subject strings would eliminate the per-message string
  allocation.  The `sm.clear()` method already preserves `sm.buf` capacity,
  so the append reuses backing memory on subsequent calls.

---

### 2 & 5. `init.func1` / `init.func2` (client.go) — 30–191 GB combined

**File:** `server/client.go:365-388`

Three `sync.Pool` tiers for outbound network buffers:

| Pool | Size | Constant |
|------|------|----------|
| `nbPoolSmall` | 512 B | `nbPoolSizeSmall` |
| `nbPoolMedium` | 4 KB | `nbPoolSizeMedium` |
| `nbPoolLarge` | 64 KB | `nbPoolSizeLarge` |

The `New` function fires when the pool is empty — this is the allocation
the profile attributes to `init.func1`/`init.func2`.

**Return path analysis:**  Buffers are reliably returned in all cases:
- `flushOutbound()` returns consumed buffers (line 1768)
- `flushAndClose()` returns all remaining buffers (lines 5730, 5738)
- WebSocket paths copy-and-return originals (lines 1493, 1512)

**Why the pool runs cold:**
1. GC clears `sync.Pool` contents every two cycles.
2. Burst traffic can drain the pool faster than `Put` refills it.
3. WebSocket/MQTT framed buffers have non-standard capacities and are
   silently dropped by `nbPoolPut` (line 419).

**Opportunities:**
- **Tune `GOGC`** — A higher `GOGC` (or `GOMEMLIMIT`) lets pools retain
  buffers longer between GC passes.
- **Pre-warm pools** on startup for expected connection counts.
- **Dedicated WebSocket buffer pool** — Framed buffers currently bypass
  `nbPoolPut`; a separate pool for WS frame sizes would recover them.

---

### 3. `readLoop` (cumulative) — 57–90 GB

**File:** `server/client.go:1358`

**Allocation pattern:**

| Site | When | Size |
|------|------|------|
| Initial buffer (line 1397) | Connection start | 512 B |
| Growth (line 1555) | Read fills buffer | 2× current, up to 64 KB |
| Shrink (line 1559) | Underutilized | ½ current, down to 64 B |
| WebSocket `wsGet()` | Per WS frame header | 1–8 B |
| WS decompression `io.ReadAll()` | Per compressed msg | Up to `MAX_PAYLOAD` |

Read buffers are **not pooled** — every resize does a bare `make([]byte, …)`.
This is the primary contributor to the 57–90 GB figure.

**Opportunities:**
- **Read buffer pool** — Mirror the `nbPool` pattern with a 3-tier
  `sync.Pool` for read buffers (512, 4096, 65536).  On resize, `Put` the
  old buffer back and `Get` the new size.
- **WebSocket decompression** — Replace `io.ReadAll()` with a pooled
  `bytes.Buffer` pre-sized to the expected payload.  The max payload is
  known from `MAX_PAYLOAD_SIZE`.

---

### 4. `s2.(*Writer).Reset` — 36–81 GB

**File:** `server/client.go:1687` (only Reset call site)

Each call to `cw.Reset(&bb)` resets the s2 compression writer to a new
`bytes.Buffer` destination.  The s2 library internally maintains compression
tables and output buffers; `Reset` preserves the tables but may reallocate
the output staging buffer if the new writer has different characteristics.

**Current pattern:**
```go
var bb bytes.Buffer
cw.Reset(&bb)            // reset to fresh buffer
for _, buf := range collapsed {
    cw.Write(buf)
}
cw.Close()
collapsed = append(net.Buffers(nil), bb.Bytes())
```

- One s2.Writer per connection (routes, leafnodes, auto-compress clients).
- Writers are **not pooled** across connections.
- On config reload, old writers are replaced without cleanup (`reload.go:457,953`).

**Opportunities:**
- **Pre-size the `bytes.Buffer`** — `bb.Grow(estimatedSize)` before `Reset`
  would avoid internal reallocation during `Write`.
- **Explicit Close on replacement** — When config reload replaces the writer,
  close the old one to release internal buffers sooner.
- **Pool writers for short-lived connections** — Route connections that
  reconnect frequently could benefit from a `sync.Pool` of `s2.Writer`.

---

### 6. `copyBytes` — 35–49 GB

**File:** `server/util.go:316`

```go
func copyBytes(src []byte) []byte {
    if len(src) == 0 { return nil }
    dst := make([]byte, len(src))
    copy(dst, src)
    return dst
}
```

Always allocates.  18 call sites; the highest-frequency ones:

| Call site | Frequency | File:Line |
|-----------|-----------|-----------|
| `processInboundJetStreamMsg` | Per published message | `stream.go:5410` |
| Consumer ACK processing | Per ACK | `consumer.go:2555` |
| Consumer pull requests | Per pull request | `consumer.go:4238` |
| JetStream API routed reqs | Per API request | `jetstream_api.go:862` |
| Memory store persistence | Per stored message | `memstore.go:255,258` |
| Direct-get header building | Per batch-get message | `stream.go:5241` |

**Opportunities:**
- **Accept pre-allocated buffer** — Add a `copyBytesInto(dst, src []byte)`
  variant that reuses caller-provided storage.
- **Eliminate redundant copies** — `memstore.go:255,258` copies `hdr` and
  `msg` separately then appends both into a third buffer.  A single
  allocation of `len(hdr)+len(msg)` with direct copy would halve allocations.
  (The code has a `TODO(dlc)` acknowledging this.)
- **Batch header construction** — `stream.go:5241` calls `copyBytes(hdr)`
  then chains 6 `genHeader()` calls, each allocating a `bytes.Buffer`.
  Building all headers in a single buffer would eliminate 6 intermediate
  allocations per message.

---

### 7. `init.func9` (filestore.go) — 27–42 GB

**File:** `server/filestore.go:988`

`blkPoolBig` is an 8 MB `sync.Pool`:

```go
var blkPoolBig = &sync.Pool{
    New: func() any {
        b := [defaultLargeBlockSize]byte{}  // 8 MB
        return &b
    },
}
```

Part of a 4-tier system: 256 KB, 1 MB, 4 MB, 8 MB.

**Return analysis:** `recycleMsgBlockBuf()` (line 1015) routes by capacity.
Buffers larger than 8 MB are intentionally dropped.  This is well-designed.

**Why the New function fires:**  Same as the client pools — GC clears the
pool, and burst block reads drain it.  Each 8 MB allocation is expensive.

**Opportunities:**
- **Reduce block size** — If the average message block is well under 8 MB,
  lowering `defaultLargeBlockSize` or using the medium pool more often
  reduces waste.
- **Retain across GC** — A small fixed-size ring buffer of 8 MB slabs
  (outside `sync.Pool`) would survive GC and serve as a warm cache for
  the most active streams.

---

### 8. `EncodedStreamState` — 20–42 GB

**Files:** `server/filestore.go:11194`, `server/memstore.go:2322`

Called during Raft snapshots (`jetstream_cluster.go:9405`).

**Allocation analysis:**

| Variant | Base buffer | Deleted-blocks encoding |
|---------|-------------|------------------------|
| filestore | `[1024]byte` (stack) | Uses `[4096]byte` scratch → efficient |
| memstore | `[1024]byte` (stack) | Calls `dmap.Encode(nil)` → **forces heap allocation** |

The memstore path is less optimized: passing `nil` to `SequenceSet.Encode`
forces it to `make([]byte, encLen)` internally, while the filestore path
passes a scratch buffer.

**Opportunities:**
- **Fix memstore path** — Add a stack-allocated scratch buffer (like
  filestore) and pass it to `dmap.Encode(scratch[:0])`.
- **Reduce snapshot frequency** — If snapshots are taken more often than
  needed, tuning the Raft snapshot interval reduces call frequency.

---

### 9. `bytes.growSlice` — 19–52 GB

This is not a single function but the Go runtime's slice growth mechanism
triggered by `append` exceeding capacity.  Major contributors:

- `genHeader()` chains — Each creates a `bytes.Buffer{}` that grows via
  append.  6 calls per direct-get message.
- `queueOutbound()` appends to `c.out.nb`.
- Various `fmt.Appendf(nil, ...)` calls (77 instances across the codebase)
  that start with a nil slice.

**Opportunities:**
- **Pre-size slices** — Where the final size is known or estimable, use
  `make([]byte, 0, estimatedCap)`.
- **Reduce `genHeader` chaining** — Build all headers in a single
  pre-sized `bytes.Buffer`.

---

### 10. `processMsgResults` (cumulative) — 148–411 GB

**File:** `server/client.go:4932` (538 lines)

This is the message fan-out hub.  It does not allocate much *directly*
but every child allocator rolls up into its cumulative count:

```
processMsgResults
├── deliverMsg → queueOutbound → nbPoolGet (ranks 2, 5)
├── msgHeader / msgHeaderForRouteOrLeaf → buffer construction
├── addSubToRouteTargets → route target slice growth
├── setHeader / setHopHeader (msg tracing) → bytes.Buffer, fmt.Sprintf
└── checkLeafClientInfoHeader → json.Marshal/Unmarshal
```

**Opportunities:**
- The allocations here are addressed by fixing the child allocators above.
- **Message tracing** adds significant overhead when enabled: each traced
  message allocates `MsgTraceEgress` structs, `fmt.Sprintf` strings, and
  `bytes.Buffer` instances.  Consider pooling trace event structures.
- **`addSubToRouteTargets`** uses a `[32]byte` inline buffer for queue
  strings (`routeTarget._qs`), which avoids most small allocations.  This
  is already well-optimized.

---

## Prioritized Recommendations

| Priority | Change | Targets | Estimated Savings |
|----------|--------|---------|-------------------|
| **P0** | Pool read buffers in `readLoop` (3-tier `sync.Pool`) | Rank 3 | 15–30 GB |
| **P0** | Tune `GOGC` / `GOMEMLIMIT` to retain pool buffers across GC | Ranks 2, 5, 7 | 30–60 GB |
| **P1** | Pre-size `bytes.Buffer` before `s2.Writer.Reset` | Rank 4 | 10–20 GB |
| **P1** | Fix memstore `EncodedStreamState` to pass scratch buffer | Rank 8 | 5–10 GB |
| **P1** | Consolidate `genHeader` chains into single-buffer construction | Ranks 6, 9 | 5–15 GB |
| **P2** | Eliminate double-copy in memstore (`copyBytes` + append) | Rank 6 | 3–5 GB |
| **P2** | Subject interning for bounded-cardinality streams | Rank 1 | 2–5 GB |
| **P2** | Pool `MsgTraceEgress` structs | Rank 10 | 1–3 GB |
| **P3** | Dedicated WebSocket buffer pool for framed messages | Ranks 2, 5 | 1–3 GB |
| **P3** | Warm-cache ring for 8 MB block slabs (survive GC) | Rank 7 | 2–5 GB |
