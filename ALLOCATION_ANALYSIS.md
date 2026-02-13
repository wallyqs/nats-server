# Allocation Analysis: encodeStreamMsgAllowCompressAndBatch

## Executive Summary

The function `encodeStreamMsgAllowCompressAndBatch` in `server/jetstream_cluster.go:8974` is causing excessive allocations (15.1 GB, 83% of total allocations). This analysis identifies the root causes and provides optimization recommendations.

## Function Location

- **File**: `server/jetstream_cluster.go`
- **Line**: 8974
- **Called from**:
  - `server/stream.go:6645` (hot path - called in batching loops)
  - `server/jetstream_cluster.go:8966` (wrapper function)

## Root Causes of Excessive Allocations

### 1. Multiple Buffer Allocations Per Call

The function creates **two separate buffers** for each invocation:

```go
// Line 8999: First buffer allocation
buf := make([]byte, 1, elen)

// Line 9029: Second buffer allocation when compression is needed
nbuf := make([]byte, s2.MaxEncodedLen(elen))
```

### 2. Excessive S2 Compression Buffer Size

When `total > compressThreshold` (8KB), the function allocates a worst-case compression buffer:

```go
nbuf := make([]byte, s2.MaxEncodedLen(elen))
```

**Problem**: `s2.MaxEncodedLen()` returns the absolute worst-case size, which is approximately:
- `input_size + (input_size / 64) + 64KB`

For an 8KB message:
- Input: ~8,200 bytes
- Overhead: ~128 bytes + 65,536 bytes
- **Total allocation: ~73KB** (9x larger than actual data!)

### 3. No Buffer Pooling

Unlike other high-performance paths in the codebase (see `server/filestore.go:970-1012`, `server/raft.go:2295+`), this function does **not use `sync.Pool`** for buffer reuse.

**Impact**: Every call allocates fresh memory, which must be:
- Allocated from heap
- Zero-initialized
- Garbage collected later

### 4. High Call Frequency

The function is called in loops during batching operations:

```go
// server/stream.go:6645 - called for each message in a batch
esm := encodeStreamMsgAllowCompressAndBatch(bsubj, _reply, bhdr, bmsg, mset.clseq, ts, false, batchId, seq, isCommit)
entries = append(entries, newEntry(EntryNormal, esm))
```

With batch sizes potentially in the hundreds or thousands, this multiplies the allocation problem.

### 5. Multiple Append Operations

Lines 9006-9025 perform numerous `append()` operations:

```go
buf = le.AppendUint16(buf, uint16(blen))
buf = append(buf, batchId[:blen]...)
buf = binary.AppendUvarint(buf, batchSeq)
// ... 8 more append operations
```

While `elen` pre-calculates capacity, any slight miscalculation can trigger slice reallocation and copying.

## Memory Allocation Breakdown (Example 8KB Message)

```
Message size: 8,192 bytes
Subject length: ~50 bytes
Headers: ~200 bytes
Total data: ~8,450 bytes

First buffer (buf):
  Allocated: 8,450 bytes + overhead = ~8,500 bytes

Compression buffer (nbuf):
  Allocated: s2.MaxEncodedLen(8,500) = ~73,000 bytes
  Actually used: ~6,000 bytes (if compression is effective)
  Wasted: ~67,000 bytes

Total allocated per call: ~81,500 bytes
Total wasted: ~67,000 bytes (82% waste)
```

## Optimization Recommendations

### Priority 1: Add Buffer Pooling (High Impact)

Implement `sync.Pool` similar to `filestore.go:996-1012`:

```go
var encodePoolSmall = &sync.Pool{
    New: func() any {
        b := make([]byte, 0, 16*1024)  // 16KB
        return &b
    },
}

var encodePoolMedium = &sync.Pool{
    New: func() any {
        b := make([]byte, 0, 64*1024)  // 64KB
        return &b
    },
}

var encodePoolLarge = &sync.Pool{
    New: func() any {
        b := make([]byte, 0, 256*1024) // 256KB
        return &b
    },
}

func getEncodeBuf(size int) []byte {
    switch {
    case size <= 16*1024:
        buf := encodePoolSmall.Get().(*[]byte)
        return (*buf)[:0]
    case size <= 64*1024:
        buf := encodePoolMedium.Get().(*[]byte)
        return (*buf)[:0]
    default:
        buf := encodePoolLarge.Get().(*[]byte)
        return (*buf)[:0]
    }
}

func recycleEncodeBuf(buf []byte) {
    switch cap(buf) {
    case 16*1024:
        encodePoolSmall.Put(&buf)
    case 64*1024:
        encodePoolMedium.Put(&buf)
    case 256*1024:
        encodePoolLarge.Put(&buf)
    }
}
```

**Expected Impact**: 70-90% reduction in allocations

### Priority 2: Optimize Compression Buffer Allocation (High Impact)

Instead of allocating `s2.MaxEncodedLen(elen)`, use a more realistic estimate:

```go
// Instead of:
nbuf := make([]byte, s2.MaxEncodedLen(elen))

// Use pooled buffer or realistic size:
compressedSize := elen + (elen / 10) // 10% overhead estimate
nbuf := getEncodeBuf(compressedSize)
```

S2 compression typically achieves 30-70% reduction for repetitive data. Worst case is near-original size, not the theoretical maximum.

**Expected Impact**: 60-80% reduction in compression-path allocations

### Priority 3: Reuse Compression Buffer Within Function (Medium Impact)

If compression doesn't save space, avoid keeping the oversized `nbuf`:

```go
// Current code keeps nbuf even if compression doesn't help
if len(ebuf) < len(buf) {
    buf = nbuf[:len(ebuf)+opIndex+1]
} else {
    // Should recycle nbuf here if using pools
}
```

### Priority 4: Single-Buffer Approach (Medium Impact - Complex)

For non-compressed messages, consider pre-allocating the exact size:

```go
// Calculate exact size upfront
exactSize := 1 + 8 + 8 + int(slen+rlen+hlen+mlen) + (2+2+2+4+8) + ...
buf := make([]byte, exactSize)

// Use direct indexing instead of append
idx := 0
buf[idx] = byte(streamMsgOp)
idx++
binary.LittleEndian.PutUint64(buf[idx:], lseq)
idx += 8
// etc.
```

This eliminates all append reallocations but requires careful index management.

## Benchmarking Suggestions

Create a benchmark to measure improvements:

```go
func BenchmarkEncodeStreamMsg(b *testing.B) {
    subject := "test.subject"
    msg := make([]byte, 8192)
    hdr := make([]byte, 200)

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        buf := encodeStreamMsgAllowCompressAndBatch(
            subject, "", hdr, msg, uint64(i), 0, false, "", 0, false,
        )
        _ = buf
    }
}
```

## Expected Results

With Priority 1 + Priority 2 optimizations:
- **Allocation reduction**: 80-95%
- **Memory pressure**: Significantly reduced
- **GC overhead**: Significantly reduced
- **Throughput**: Likely 10-30% improvement on batching operations

## References

- Buffer pooling pattern: `server/filestore.go:970-1012`
- Pool usage in raft: `server/raft.go:2295-2382`
- S2 compression library: github.com/klauspost/compress/s2
- Compression threshold: `server/jetstream_cluster.go:8971` (8KB)

## Next Steps

1. Implement Priority 1 (buffer pooling) as it provides the highest ROI
2. Add benchmarks to measure improvement
3. Implement Priority 2 if additional gains are needed
4. Profile again to verify allocation reduction
5. Consider Priority 3-4 only if necessary
