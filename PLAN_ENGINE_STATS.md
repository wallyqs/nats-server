# Engine Stats Implementation Plan

## Overview

Add an `engine_stats` structure to the JetStream monitoring endpoint that provides:
1. Rolling average of pending API requests (`pending_avg`)
2. DIO (Disk I/O) channel acquisition statistics:
   - Total acquire count
   - Wait time percentiles (P50, P95, P99)

## Current State

### DIO Channel (`dios`)
- Located in `server/filestore.go:11668`
- Buffered channel used as a semaphore to limit concurrent disk I/O operations
- Capacity: 4-16 based on CPU cores (50% if > 32 cores)
- ~30 places in the codebase acquire/release the channel
- Pattern: `<-dios` to acquire, `dios <- struct{}{}` to release

### API Pending Average
- Already implemented in `jetStream.apiPendingAvg` (scaled int64)
- Updated in `jetstream_api.go` via `js.updatePendingAvg(pending)`

## Proposed Design

### 1. New Types (`server/filestore.go` or new `server/engine_stats.go`)

```go
// DIOStats holds disk I/O semaphore statistics.
type DIOStats struct {
    Acquires   uint64  `json:"acquires"`    // Total acquisitions
    WaitP50    float64 `json:"wait_p50_ms"` // 50th percentile wait time (ms)
    WaitP95    float64 `json:"wait_p95_ms"` // 95th percentile wait time (ms)
    WaitP99    float64 `json:"wait_p99_ms"` // 99th percentile wait time (ms)
    WaitMax    float64 `json:"wait_max_ms"` // Maximum wait time (ms)
}

// EngineStats holds JetStream engine-level statistics.
type EngineStats struct {
    PendingAvg float64   `json:"pending_avg"` // Rolling average of pending API requests
    DIO        *DIOStats `json:"dio"`         // Disk I/O semaphore stats
}
```

### 2. DIO Tracking State (global, in `filestore.go`)

```go
// DIO tracking state
var (
    dios          chan struct{}
    dioAcquires   atomic.Uint64
    dioHistogram  [6]atomic.Uint64 // Buckets: <1ms, 1-5ms, 5-10ms, 10-50ms, 50-100ms, >100ms
    dioMaxWait    atomic.Int64     // Max wait in nanoseconds
)

// Histogram bucket boundaries in nanoseconds
var dioBuckets = [5]int64{
    1_000_000,   // 1ms
    5_000_000,   // 5ms
    10_000_000,  // 10ms
    50_000_000,  // 50ms
    100_000_000, // 100ms
}
```

### 3. Wrapper Functions

```go
// acquireDIO acquires the disk I/O semaphore and tracks wait time.
func acquireDIO() {
    start := time.Now()
    <-dios

    // Track stats
    dioAcquires.Add(1)
    waitNs := time.Since(start).Nanoseconds()

    // Update histogram bucket
    bucket := len(dioBuckets) // Default to last bucket (>100ms)
    for i, limit := range dioBuckets {
        if waitNs < limit {
            bucket = i
            break
        }
    }
    dioHistogram[bucket].Add(1)

    // Update max (lock-free)
    for {
        old := dioMaxWait.Load()
        if waitNs <= old {
            break
        }
        if dioMaxWait.CompareAndSwap(old, waitNs) {
            break
        }
    }
}

// releaseDIO releases the disk I/O semaphore.
func releaseDIO() {
    dios <- struct{}{}
}

// getDIOStats returns current DIO statistics.
func getDIOStats() *DIOStats {
    // Calculate percentiles from histogram
    total := uint64(0)
    counts := make([]uint64, len(dioHistogram))
    for i := range dioHistogram {
        counts[i] = dioHistogram[i].Load()
        total += counts[i]
    }

    if total == 0 {
        return &DIOStats{}
    }

    // Approximate percentile calculation from histogram
    // Uses bucket midpoints for estimation
    ...

    return &DIOStats{
        Acquires: dioAcquires.Load(),
        WaitP50:  p50,
        WaitP95:  p95,
        WaitP99:  p99,
        WaitMax:  float64(dioMaxWait.Load()) / 1_000_000,
    }
}
```

### 4. Update Call Sites

Replace all ~30 occurrences of:
```go
<-dios
// ... I/O operation ...
dios <- struct{}{}
```

With:
```go
acquireDIO()
// ... I/O operation ...
releaseDIO()
```

### 5. Expose in JSInfo (monitor.go)

Add to `JSInfo` struct:
```go
type JSInfo struct {
    // ... existing fields ...
    Engine *EngineStats `json:"engine,omitempty"`
}
```

Populate in `Jsz()` function:
```go
jsi.Engine = &EngineStats{
    PendingAvg: float64(atomic.LoadInt64(&js.apiPendingAvg)) / 1000.0,
    DIO:        getDIOStats(),
}
```

## Implementation Steps

1. **Add DIO tracking state** - Add atomic counters and histogram to `filestore.go`
2. **Create wrapper functions** - `acquireDIO()`, `releaseDIO()`, `getDIOStats()`
3. **Update all call sites** - Replace `<-dios` / `dios <- struct{}{}` with wrapper functions
4. **Add EngineStats types** - Define structs for JSON serialization
5. **Expose in Jsz endpoint** - Add `Engine` field to `JSInfo` and populate it
6. **Add tests** - Verify stats are correctly tracked and exposed

## Histogram Buckets & Percentile Calculation

Using fixed buckets with midpoint estimation:

| Bucket | Range | Midpoint |
|--------|-------|----------|
| 0 | 0-1ms | 0.5ms |
| 1 | 1-5ms | 3ms |
| 2 | 5-10ms | 7.5ms |
| 3 | 10-50ms | 30ms |
| 4 | 50-100ms | 75ms |
| 5 | >100ms | 150ms* |

*For >100ms, could use running max or a higher estimate

Percentiles calculated by finding which bucket contains the Nth percentile and interpolating.

## JSON Output Example

```json
{
  "engine": {
    "pending_avg": 2.5,
    "dio": {
      "acquires": 1523456,
      "wait_p50_ms": 0.3,
      "wait_p95_ms": 5.2,
      "wait_p99_ms": 12.8,
      "wait_max_ms": 245.6
    }
  }
}
```

## Considerations

1. **Performance** - All updates are lock-free using atomics
2. **Accuracy** - Histogram-based percentiles are approximate but sufficient for monitoring
3. **Memory** - Fixed overhead: 6 atomic counters + 1 atomic int64 for max
4. **Backward Compatibility** - `engine` field is `omitempty`, won't affect existing clients
