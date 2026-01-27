# Investigation: accConnsUpdate Lock Optimization

## Summary

This document investigates the `accConnsUpdate` lock mechanism in the NATS server and proposes
sharded and lock-free alternatives to reduce contention on high-throughput servers.

## Current Implementation Analysis

### Location
- **Main function**: `server/events.go:2506-2513` - `accConnsUpdate()`
- **Helper function**: `server/events.go:2407-2443` - `sendAccConnsUpdate()`
- **Callers**: `server/accounts.go:978,1069` - `addClient()` and `removeClient()`

### Current Lock Hierarchy

```
addClient/removeClient flow:
  1. a.mu.Lock()          // Account lock - held during client map modification
  2. a.mu.Unlock()
  3. accConnsUpdate(a)
     +-- s.mu.Lock()       // SERVER-WIDE lock (major contention point)
     +-- sendAccConnsUpdate(a)
     |   +-- a.mu.Lock()   // Account lock (second acquisition)
     |   +-- a.statz()
     |   |   +-- a.stats.Lock() // Stats lock (third level)
     |   +-- a.mu.Unlock()
     +-- s.mu.Unlock()
```

### Problem Statement

Every client connect/disconnect event triggers `accConnsUpdate()`, which acquires the **global
server lock** (`s.mu`). On servers handling many concurrent connections, this creates severe
lock contention because:

1. All accounts compete for the same server-wide lock
2. The lock is held while building event messages, generating event IDs, and managing timers
3. Account lock is acquired twice (once in addClient/removeClient, again in sendAccConnsUpdate)

## Contention Points

### 1. Server Lock (`s.mu`) - CRITICAL

- **Location**: `server/events.go:2507`
- **Impact**: Every client connect/disconnect acquires the global server lock
- **Dependencies**:
  - `eventsEnabled()` check requires reading `s.sys` fields
  - `nextEventID()` requires server lock (line 2517)
  - Comparing with `s.gacc` (global account)

### 2. Account Lock Re-acquisition

- `a.mu` is released in `addClient`/`removeClient`, then re-acquired in `sendAccConnsUpdate`
- Creates potential race window between release and re-acquisition

### 3. Nested Lock Acquisition

- Multiple lock levels: `s.mu` -> `a.mu` -> `a.stats` mutex
- Complex lock ordering requirements

## Proposed Solutions

### Option 1: Lock-Free Event ID + Batched Updates (Recommended for High-Throughput)

#### A. Make event ID generation lock-free

```go
// Change in Server struct from:
eventIds *nuid.NUID

// To using per-goroutine or atomic approach:
type Server struct {
    eventIdGen atomic.Pointer[nuid.NUID]
    // Or use sync.Pool of NUIDs
}
```

#### B. Use atomic operations for connection counts

```go
// In Account struct:
type Account struct {
    // Change from:
    // sysclients   int32
    // nleafs       int32

    // To:
    sysclients   atomic.Int32
    nleafs       atomic.Int32
}
```

#### C. Decouple event sending from connection handling

```go
// Create a dedicated event channel per account
type Account struct {
    connUpdateCh chan struct{} // Coalescing channel
}

func (s *Server) accConnsUpdate(a *Account) {
    select {
    case a.connUpdateCh <- struct{}{}:
    default:
        // Update already pending, skip (coalesce)
    }
}

// Background goroutine coalesces updates
func (s *Server) accountEventLoop(a *Account) {
    for range a.connUpdateCh {
        drainPending(a.connUpdateCh)
        s.sendAccConnsUpdateLockFree(a)
    }
}
```

**Benefits:**
- Eliminates server-wide lock contention on hot path
- Coalesces rapid updates into single events
- Connection count reads remain consistent via atomics

**Drawbacks:**
- Requires goroutine per account
- Events are slightly delayed (batched)

### Option 2: Sharded Server Lock

Create a sharded lock based on account name hash:

```go
const numEventShards = 64

type Server struct {
    eventShards [numEventShards]struct {
        sync.Mutex
        // Per-shard event ID generator
        eventIds *nuid.NUID
    }
}

func (s *Server) getEventShard(accName string) int {
    h := fnv.New64a()
    h.Write([]byte(accName))
    return int(h.Sum64() % numEventShards)
}

func (s *Server) accConnsUpdate(a *Account) {
    if !s.eventsEnabledLockFree() || a == nil || a == s.gacc {
        return
    }

    idx := s.getEventShard(a.Name)
    shard := &s.eventShards[idx]

    shard.Lock()
    defer shard.Unlock()

    s.sendAccConnsUpdateWithShard(a, shard)
}
```

**Benefits:**
- Reduces lock contention by ~64x on multi-account systems
- Simple to implement and reason about
- Maintains event ordering within same account

**Drawbacks:**
- Doesn't help single-account setups
- Adds complexity with sharded event ID generators

### Option 3: Per-Account Event Lock

Replace server lock with per-account event lock:

```go
type Account struct {
    eventMu sync.Mutex  // Dedicated lock for event sending
    ctmr    *time.Timer
}

func (s *Server) accConnsUpdate(a *Account) {
    if !s.eventsEnabledLockFree() || a == nil || a == s.getGlobalAccountLockFree() {
        return
    }

    a.eventMu.Lock()
    defer a.eventMu.Unlock()

    s.sendAccConnsUpdateLockFree(a)
}
```

**Requirements for this to work:**
- Make `eventsEnabled()` lock-free using `atomic.Pointer` for `s.sys`
- Use lock-free event ID generation
- Ensure `sendq.push()` is thread-safe (already is via `ipQueue`)

**Benefits:**
- Complete elimination of global lock for this path
- No cross-account contention
- Maintains per-account serialization

## Recommended Approach: Hybrid Solution

Combine the best aspects of Options 1 and 3:

1. **Per-account event lock** - Replace `s.mu` usage with `a.eventMu`
2. **Lock-free event ID generation** - Use atomic NUID or sharded NUIDs
3. **Lock-free eventsEnabled check** - Use atomic pointer for sys
4. **Optional: Event coalescing** - For extremely high churn scenarios

### Implementation Plan

#### Phase 1: Lock-Free Prerequisites

1. Add `eventsEnabledLockFree()` using atomic checks:
   ```go
   func (s *Server) eventsEnabledLockFree() bool {
       sys := s.sysAccAtomic.Load() // New atomic field
       return sys != nil && sys.client != nil
   }
   ```

2. Make event ID generation thread-safe without server lock:
   ```go
   func (s *Server) nextEventIDLockFree() string {
       // Use sync.Pool of NUIDs or atomic operations
   }
   ```

#### Phase 2: Per-Account Event Lock

1. Add `eventMu sync.Mutex` to Account struct
2. Modify `accConnsUpdate()` to use account lock instead of server lock
3. Update `sendAccConnsUpdate()` to work without server lock held

#### Phase 3: Optional Enhancements

1. Atomic connection counts for lock-free reads
2. Event coalescing for high-churn scenarios

## Complexity vs Performance Trade-off

| Option | Complexity | Performance Gain | Risk |
|--------|------------|------------------|------|
| Lock-Free + Batched | High | Excellent | Medium |
| Sharded Server Lock | Low | Good (multi-account) | Low |
| Per-Account Lock | Medium | Very Good | Low |
| Hybrid (Recommended) | Medium-High | Excellent | Medium |

## Files to Modify

1. **`server/events.go`**:
   - `accConnsUpdate()` - Remove server lock dependency
   - `sendAccConnsUpdate()` - Work without server lock
   - Add `eventsEnabledLockFree()`
   - Add `nextEventIDLockFree()`

2. **`server/accounts.go`**:
   - Add `eventMu sync.Mutex` to Account struct
   - Consider atomic types for `sysclients`/`nleafs`

3. **`server/server.go`**:
   - Add atomic wrapper for `sys` field access
   - Consider sharded event ID generators

## Testing Considerations

1. **Correctness**: Ensure events are still delivered reliably
2. **Ordering**: Per-account event ordering should be maintained
3. **Performance**: Benchmark with high connection churn rate
4. **Race Detection**: Run with `-race` flag extensively
5. **Edge Cases**: Server shutdown, account deletion during event send

## Conclusion

The current `accConnsUpdate` implementation uses a server-wide lock that creates contention
under high connection churn. The recommended solution is a hybrid approach using:

1. Per-account event lock to eliminate global contention
2. Lock-free event ID generation
3. Lock-free `eventsEnabled()` checks

This approach provides excellent performance improvements with manageable implementation
complexity and risk.
