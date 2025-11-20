# Raft Lease Duration Analysis - Stale Read Vulnerability

## Summary

The NATS Raft implementation has a **critical timing configuration issue** that can lead to **stale reads** during network partitions. The vulnerability exists because:

```
lostQuorumInterval (10s) > minElectionTimeout (4s)
```

This creates a **6+ second window** where two leaders can exist simultaneously after a network partition, allowing the old leader to serve stale reads.

## The Problem

### Current Configuration (server/raft.go:258-268)

```go
const (
    minElectionTimeoutDefault      = 4 * time.Second
    maxElectionTimeoutDefault      = 9 * time.Second
    minCampaignTimeoutDefault      = 100 * time.Millisecond
    maxCampaignTimeoutDefault      = 8 * minCampaignTimeoutDefault
    hbIntervalDefault              = 1 * time.Second
    lostQuorumIntervalDefault      = hbIntervalDefault * 10 // 10 seconds ⚠️
    lostQuorumCheckIntervalDefault = hbIntervalDefault * 10 // 10 seconds ⚠️
    observerModeIntervalDefault    = 48 * time.Hour
    peerRemoveTimeoutDefault       = 5 * time.Minute
)
```

### The Dangerous Sequence

1. **T+0s**: Network partition occurs, isolating the current leader
2. **T+4s**: Majority partition elects a **new leader** (after `minElectionTimeout`)
3. **T+4s to T+10s**: **DANGER ZONE**
   - New leader accepts writes and serves reads with fresh data
   - Old leader still thinks it's the leader (hasn't detected quorum loss yet)
   - Old leader serves reads with **stale data**
4. **T+10s+**: Old leader finally detects quorum loss via `lostQuorumLocked()` check

## Root Cause Analysis

### 1. Leader Lease Not Properly Enforced

The `isCurrent()` function in `raft.go:1527-1604` has a critical flaw:

```go
func (n *raft) isCurrent(includeForwardProgress bool) bool {
    // ... checks omitted ...

    // Line 1550-1552: THE PROBLEM
    if n.State() == Leader {
        clearBehindState()
        return true  // ⚠️ Returns true WITHOUT checking quorum!
    }

    // ... follower checks ...
}
```

**The leader returns `true` immediately without verifying:**
- Whether it has quorum
- Whether its lease is still valid
- How long since last successful heartbeat to a quorum of followers

### 2. No Quorum Check Before Serving Reads

Direct get requests (`stream.go:4930`, `stream.go:5168`) serve reads directly from storage without checking:
- Leader quorum status
- Leader lease validity

```go
func (mset *stream) processDirectGetRequest(...) {
    // ... request parsing ...
    mset.getDirectRequest(&req, reply)  // No quorum/lease check!
}

func (mset *stream) getDirectRequest(req *JSApiMsgGetRequest, reply string) {
    mset.mu.RLock()
    defer mset.mu.RUnlock()

    store, name, s := mset.store, mset.cfg.Name, mset.srv
    // ... serves read directly from store, no safety checks ...
}
```

### 3. Delayed Quorum Loss Detection

The leader only checks for quorum loss every `lostQuorumCheck` interval (10 seconds):

```go
// raft.go:2663, 2717-2721
lq := time.NewTicker(lostQuorumCheck)  // 10 seconds
defer lq.Stop()

for n.State() == Leader {
    select {
    // ...
    case <-lq.C:
        if n.lostQuorum() {
            n.stepdown(noLeader)
            return
        }
    }
}
```

And `lostQuorumLocked()` checks if peers have been silent for `lostQuorumInterval` (10 seconds):

```go
// raft.go:2765-2782
func (n *raft) lostQuorumLocked() bool {
    // ...
    for id, peer := range n.peers {
        if id == n.id || time.Since(peer.ts) < lostQuorumInterval {  // 10 seconds
            if nc++; nc >= n.qn {
                return false
            }
        }
    }
    return true
}
```

## Test Results

Created test file: `server/raft_lease_test.go`

### Test 1: `TestRaftLeaderLeaseTiming`

**Result:** ✅ PASS (demonstrates the vulnerability)

```
Current timing configuration:
  minElectionTimeout: 4s (followers can elect new leader)
  lostQuorumInterval: 10s (leader checks for quorum loss)
  lostQuorumCheck:    10s (how often leader checks)

Vulnerability window: 6s
During this window, two leaders can exist simultaneously!
The old leader can serve stale reads while the new leader serves fresh writes.

UNSAFE CONFIGURATION: lostQuorumInterval (10s) >= minElectionTimeout (4s)
For linearizable reads, lostQuorumInterval must be < minElectionTimeout
```

### Test 2: `TestRaftCurrentDoesNotCheckQuorum`

**Result:** ✅ PASS (demonstrates leader continues to report Current=true after losing quorum)

```
Leader: S-2, Current: true, Quorum: true
After partition - Leader still reports: Current: true
Waiting 5 seconds...
After 5s: Leader STILL reports Current: true (UNSAFE!)
This leader has lost quorum but will still serve reads!
A new leader could have been elected in the majority partition by now.
Waiting for lostQuorumInterval (10s)...
After 10s: Leader STILL reports Current: true - this should not happen!
```

## The Fix

To prevent stale reads, the **lease duration must be shorter than the election timeout**:

```
lostQuorumInterval < minElectionTimeout
```

### Recommended Configuration

```go
const (
    minElectionTimeoutDefault      = 4 * time.Second
    maxElectionTimeoutDefault      = 9 * time.Second
    hbIntervalDefault              = 1 * time.Second
    lostQuorumIntervalDefault      = 2 * time.Second  // ✅ Changed from 10s to 2s
    lostQuorumCheckIntervalDefault = 1 * time.Second  // ✅ Changed from 10s to 1s
    // ...
)
```

This ensures:
- Leader detects quorum loss within **2 seconds**
- Leader checks for quorum loss every **1 second**
- Followers elect new leader after **4 seconds** minimum
- **No overlap** where both leaders think they're current

### Additional Safeguards (Optional)

1. **Read-time quorum check**: Modify `isCurrent()` to verify quorum even for leaders:

```go
func (n *raft) isCurrent(includeForwardProgress bool) bool {
    // ... existing checks ...

    if n.State() == Leader {
        // ✅ Add quorum check even for leaders
        if n.lostQuorumLocked() {
            return false
        }
        clearBehindState()
        return true
    }

    // ... rest of function ...
}
```

2. **Lease-based reads**: Implement explicit lease tracking that expires before new elections can complete.

## Impact

**Severity: HIGH**

This vulnerability can lead to:
- **Stale reads**: Clients reading outdated data from a partitioned leader
- **Data inconsistency**: Different clients seeing different states during the vulnerability window
- **Violation of linearizability**: The system cannot guarantee that reads reflect the most recent writes

**Affected Operations:**
- JetStream Direct Get requests
- KV bucket reads
- Any operation that calls `Current()` or `Healthy()` to determine if a node can serve reads

## References

- `server/raft.go:258-268` - Timing constants
- `server/raft.go:1527-1604` - `isCurrent()` implementation
- `server/raft.go:2765-2782` - `lostQuorumLocked()` implementation
- `server/stream.go:4930` - `processDirectGetRequest()`
- `server/stream.go:5168` - `getDirectRequest()`
- `server/raft_lease_test.go` - Test cases demonstrating the vulnerability

## Running the Tests

```bash
export NO_PROXY="localhost,127.0.0.1,169.254.169.254,metadata.google.internal,.svc.cluster.local,.local,*.google.com"
go test -v -run TestRaftLeaderLeaseTiming ./server/ -timeout 30s
go test -v -run TestRaftCurrentDoesNotCheckQuorum ./server/ -timeout 2m
```
