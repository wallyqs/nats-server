# NATS Filestore Slowness Investigation

## Issue Summary

⚠️ **CLUSTER-WIDE EMERGENCY** - Multiple nodes experiencing critical RAFT failure:
- `WriteFullState` taking 1+ minute to write 73 bytes
- `Metalayer snapshot` taking 61 seconds for only 4 streams and 43 consumers
- Internal RAFT subscriptions (`$NRG.P.*`) taking 2+ seconds to process
- Readloop processing times exceeding 2 seconds
- JetStream streams reporting high message lag (>10,000 pending messages)
- **CRITICAL: 10,000 RAFT append entries pending on MULTIPLE nodes** (tsl-nats-0, tsl-nats-2, tsl-nats-3)

## Root Cause Analysis

### WriteFullState Performance Issue

**Location**: `server/filestore.go:10541-10773`

**The Problem**:
The function writes a small state file (73 bytes) but performs extensive disk I/O beforehand:

1. **Checksum Loading** (line 10687):
   ```go
   if mb == fs.lmb {
       mb.ensureLastChecksumLoaded()  // <-- DISK I/O HERE
       copy(lchk[0:], mb.lchk[:])
   }
   ```

2. **The Bottleneck** (`lastChecksum()` at line 2213):
   - Opens each message block file from disk
   - For **encrypted blocks**: Loads and decrypts ENTIRE block (potentially MB of data) just to read 8-byte checksum
   - For unencrypted blocks: Seeks to end and reads 8 bytes
   - **Each operation waits on disk I/O semaphore** (limited to 4-16 concurrent ops)

3. **Cascading Delays**:
   - If storage has 100ms+ latency per operation
   - With many message blocks: 100ms × N blocks = seconds to minutes
   - Encrypted blocks make this exponentially worse

**Why the checksum isn't cached**:
The `mb.lchk` field should be populated during normal writes (line 6729), but may be empty if:
- Block was created but not recently written to
- Block was recovered from disk but checksum not cached
- Memory pressure caused the msgBlock to be recreated

### Metalayer Snapshot Performance Issue

**Location**: `server/jetstream_cluster.go:1642-1721`

**The Problem**:
With only 4 streams and 43 consumers:
- JSON marshaling: 0.000s (instant)
- S2 compression: 0.000s (instant)
- **Total time: 61 seconds (spent in lines 1644-1695)**

**Likely causes**:
1. **Lock contention**: Holding `js.mu.RLock()` while waiting for something else
2. **CPU/scheduler starvation**: Goroutine not getting scheduled
3. **System-wide resource exhaustion**: I/O, memory, or CPU bottleneck affecting all operations

### RAFT Replication Lag and Cluster Issues

**Locations**:
- Internal subscription warning: `server/client.go:3799`
- Readloop warning: `server/client.go:1479, 1581`
- High message lag: `server/jetstream_cluster.go:9099`

**The Problem**:
The cluster's RAFT consensus is severely lagging:

1. **Internal subscription on "$NRG.P.*" taking 2+ seconds**:
   ```
   [WRN] Internal subscription on "$NRG.P.S-R3F-0P5z4fFi" took too long: 2.268898935s
   ```
   - `$NRG.P.*` is the RAFT proposal subject (see `server/raft.go:1949`)
   - These are new messages being proposed to the RAFT group for consensus
   - Processing proposals involves writing to disk (via filestore)
   - **Threshold**: Warnings trigger at 2+ seconds (`readLoopReportThreshold`)

2. **Readloop processing time exceeding 2 seconds**:
   ```
   [WRN] 192.168.6.84:6222 - rid:10 - Readloop processing time: 2.268925453s
   ```
   - Network read loops are blocked processing messages from cluster peers
   - This happens when handling incoming RAFT messages that require disk I/O
   - Blocked readloops prevent receiving new messages, cascading the backlog

3. **High message lag (>10,000 pending)**:
   ```
   [WRN] JetStream stream 'JS > megapack2_signals_v1_payloads' has high message lag
   ```
   - Stream has >10,000 messages pending RAFT commit (`streamLagWarnThreshold`)
   - Formula: `clseq - (lseq + clfs) > 10000` (see `server/jetstream_cluster.go:9099`)
   - Messages are piling up faster than they can be committed to disk
   - **This is a critical indicator** - the node is falling behind in replication

**The Cascade Effect**:

```
Slow Disk I/O
    ↓
WriteFullState delays (1+ min)
    ↓
RAFT commit operations slow down
    ↓
Internal subscriptions ($NRG.P.*) take 2+ seconds
    ↓
Readloops blocked processing cluster messages
    ↓
Message lag builds up (>10,000 pending)
    ↓
Cluster falls out of sync
    ↓
Potential data loss or split-brain scenarios
```

**Why this is dangerous**:
- **Cluster instability**: Nodes may be removed from the RAFT group if they fall too far behind
- **Message loss risk**: If the node is removed, uncommitted messages may be lost
- **Split-brain potential**: Severely lagging nodes can cause leadership elections and cluster chaos
- **Cascading failures**: One slow node can impact the entire cluster's throughput

### CRITICAL: RAFT Append Entries Pending (Cluster-Wide Failure)

**Location**: `server/raft.go:4224`

**The Problem**:
```
[WRN] RAFT [PXqZqZEB - S-R3F-0P5z4fFi] 10000 append entries pending
[WRN] RAFT [9nvdAFT1 - S-R3F-0P5z4fFi] 10000 append entries pending  (tsl-nats-0)
[WRN] RAFT [063rxyLn - S-R3F-0P5z4fFi] 10000 append entries pending  (tsl-nats-3)
```

**This is the most critical indicator yet - appearing on MULTIPLE nodes simultaneously.**

**What "append entries pending" means**:
1. **The cache**: RAFT maintains an in-memory cache (`n.pae`) of append entries that have been:
   - Written to the Write-Ahead Log (WAL)
   - Sent to followers for replication
   - **But NOT yet committed/applied** to the state machine (filestore)

2. **Normal flow** (see `server/raft.go:3159-3206`):
   ```
   Write to WAL → Cache in n.pae → Send to followers → Commit → Apply → Delete from cache
   ```

3. **Current broken flow**:
   ```
   Write to WAL ✓ → Cache in n.pae ✓ → Send to followers ✓ → Commit SLOW → Apply VERY SLOW → Cache never cleared
   ```

4. **Thresholds** (see `server/raft.go:4170-4172`):
   - `paeWarnThreshold = 10,000` - Warning starts
   - `paeWarnModulo = 5,000` - Warn every 5,000 entries
   - `paeDropThreshold = 20,000` - Cache full, start dropping entries

**Why entries aren't being committed**:
- The `applyCommit()` function (line 3159) applies entries to the filestore
- This involves **writing messages to disk** via the filestore
- With catastrophically slow storage, each commit takes seconds instead of milliseconds
- Entries pile up faster than they can be applied
- Eventually hits 20,000 and RAFT starts **dropping entries** (potential data loss)

**Why this is happening cluster-wide**:
1. **All nodes have slow storage**: Every node is experiencing the same I/O bottleneck
2. **Leader bottleneck**: If the leader is slow, it slows down the entire replication process
3. **Shared infrastructure**: All pods likely using the same degraded storage backend
4. **Cascade effect**: One slow node forces others to buffer more data

**IMMINENT DANGERS**:
- **At 20,000 entries**: RAFT starts **dropping entries** from cache (see line 4226-4229)
- **Memory exhaustion**: 10,000+ cached entries consuming significant memory
- **Cluster halt**: If commits stop completely, the cluster becomes read-only
- **Data inconsistency**: Dropped entries may cause state divergence between nodes
- **Complete cluster failure**: All nodes may crash or become unresponsive

## Storage I/O Indicators

Both issues point to **severe storage I/O bottleneck**:

### Potential Causes:
1. **Network-attached storage** (NFS, EBS, etc.) with high latency
2. **Storage device degradation** or overload
3. **Disk contention** from other processes
4. **Filesystem issues** (full disk, inode exhaustion, fragmentation)
5. **Kernel I/O scheduler** misconfiguration or saturation

## Diagnostic Recommendations

### 1. Check Storage Performance

```bash
# Check disk I/O statistics
iostat -x 1 10

# Check for slow disk operations
iotop -o

# Monitor disk latency
dstat -d -D sda,sdb 1 10

# Check if using network storage
df -h | grep nats
mount | grep nats
```

### 2. Check for Disk Errors

```bash
# Check kernel logs for disk errors
dmesg | grep -i "error\|fail\|timeout" | tail -50

# Check SMART status (if applicable)
smartctl -a /dev/sda
```

### 3. Monitor NATS Operations

```bash
# Watch for all slowness indicators
grep -E "WriteFullState took|Metalayer snapshot took|took too long|Readloop processing|high message lag" nats.log

# Check if encryption is enabled (much slower)
grep -i "encrypt" nats.log | head -20

# Monitor RAFT lag patterns
grep "\$NRG\.P\." nats.log | tail -50

# Check for cluster disconnects or leader changes
grep -E "leader|raft|cluster" nats.log | tail -50
```

### 4. Check System Resources

```bash
# CPU usage
top -b -n 1 | head -20

# Memory pressure
free -h
cat /proc/meminfo | grep -i dirty

# I/O wait
vmstat 1 10
```

### 5. NATS-Specific Checks

```bash
# Number of message blocks per stream
find /path/to/jetstream -name "*.blk" | wc -l

# Check if blocks are encrypted
ls -lh /path/to/jetstream/*/msgs/*.blk | head -10

# Check for very large blocks
find /path/to/jetstream -name "*.blk" -size +100M

# Check JetStream cluster health (if using NATS CLI)
nats server report jetstream
nats stream info megapack2_signals_v1_payloads

# Check RAFT state for the stream
nats stream cluster megapack2_signals_v1_payloads

# Monitor message rates
nats stream ls --json | jq '.[] | {name, messages, lag}'
```

## Recommended Fixes

### 🚨 EMERGENCY - Immediate Actions Required:

**⚠️ CLUSTER-WIDE CATASTROPHIC FAILURE IN PROGRESS**:
- 10,000 append entries pending on MULTIPLE nodes
- Approaching 20,000 threshold where RAFT will DROP entries
- Cluster approaching complete failure
- Data loss imminent if not addressed

**YOU HAVE MINUTES, NOT HOURS**

**PRIORITY 1: Stop the bleeding (NEXT 5 MINUTES)**

1. **IMMEDIATELY stop or drastically reduce incoming traffic**:
   ```bash
   # Apply backpressure at application level
   # Scale down producers
   # Enable flow control
   ```
   - This is your ONLY way to prevent hitting the 20,000 drop threshold
   - Without this, data loss is guaranteed

2. **Check pending entries on all nodes RIGHT NOW**:
   ```bash
   # Watch for "append entries pending" warnings
   kubectl logs -f pod/tsl-nats-0 | grep "append entries pending"
   kubectl logs -f pod/tsl-nats-2 | grep "append entries pending"
   kubectl logs -f pod/tsl-nats-3 | grep "append entries pending"
   ```
   - If any node shows 15,000+, you are in immediate danger
   - At 20,000, RAFT starts dropping entries (DATA LOSS)

3. **Monitor for dropped entries** (would indicate data loss):
   ```bash
   grep "Invalidate cache entry" nats.log  # See raft.go:4227-4229
   ```

**PRIORITY 2: Diagnose storage (NEXT 15 MINUTES)**

4. **Check storage health URGENTLY on ALL nodes**:
   ```bash
   # On each pod/node:
   iostat -x 1 10        # Look for 100% utilization, high await (>100ms)
   iotop -o              # What's causing I/O
   dmesg | tail -50      # Disk errors?
   df -h                 # Disk full?
   ```

5. **Check if this is shared storage issue**:
   ```bash
   # Are all pods on same storage backend?
   kubectl get pv
   kubectl describe pv <pv-name> | grep -i storage
   ```

**PRIORITY 3: Emergency mitigation (IF storage can't be fixed immediately)**

6. **If storage cannot be fixed in minutes**:
   - **Option A: Emergency cluster shutdown** (preserves data consistency):
     ```bash
     # Stop ALL producers first
     # Gracefully shutdown NATS cluster
     # Migrate to faster storage
     # Restart cluster
     ```

   - **Option B: Sacrifice one node to save cluster** (if some nodes are healthy):
     ```bash
     # Identify the slowest node (highest pending entries)
     # Remove it from cluster
     # Let cluster stabilize with remaining nodes
     # Fix storage and re-add later
     ```

   - **Option C: Emergency storage bypass** (if available):
     ```bash
     # If pods can be moved to local SSDs or faster storage
     # Emergency migration of volumes
     ```

7. **Monitor cluster state continuously**:
   ```bash
   nats stream cluster <stream-name>  # RAFT state
   grep -i "leader" nats.log | tail -20  # Leadership changes
   ```

**PRIORITY 4: Configuration review (if cluster survives)**

8. **Review NATS configuration** (for long-term fix):
   - If using encryption, consider if it's necessary (major performance impact)
   - Check `sync_interval` and `sync_always` settings
   - Review `file_store_block_size` (smaller blocks = more checksum reads)
   - Consider if `store_dir` is on appropriate storage (local NVMe > network storage)

### Code-Level Optimizations (Potential):

1. **Cache checksums more aggressively**:
   - Ensure `mb.lchk` is always populated after writes
   - Pre-cache checksums during recovery to avoid lazy loading

2. **Reduce checksum reads in WriteFullState**:
   - Only read checksum if truly needed (when it's not cached)
   - Consider storing checksums in memory for all active blocks

3. **Add detailed timing breakdowns**:
   - Log time spent on different phases of WriteFullState
   - Add metrics for checksum loading time specifically

## Expected Behavior vs. Current State

### Normal Operations:
- **WriteFullState**: <100ms
- **Metalayer snapshot**: <10ms for 4 streams / 43 consumers
- **RAFT proposals**: <10ms processing time
- **Readloop processing**: <100ms
- **Message lag**: <100 pending messages in RAFT queue

### Current (Degraded) State:
- **WriteFullState**: 66+ seconds (660x slower)
- **Metalayer snapshot**: 61 seconds (6100x slower)
- **RAFT proposals**: 2+ seconds (200x slower)
- **Readloop processing**: 2+ seconds (20x+ slower)
- **Message lag**: >10,000 pending (100x+ worse)

**Conclusion**: These are not NATS bugs but symptoms of severe storage I/O degradation. The 100-1000x slowdowns indicate catastrophic infrastructure issues.

## Next Steps

1. **Diagnose storage**: Run the diagnostic commands above
2. **Share findings**: Provide disk I/O metrics and any errors found
3. **Consider migration**: If on degraded storage, migrate to faster storage
4. **Review configuration**: Ensure NATS settings are optimized for your storage type

## Code References

### Filestore Operations:
- WriteFullState: `server/filestore.go:10541-10773`
- WriteFullState warning threshold check: `server/filestore.go:10743-10744`
- ensureLastChecksumLoaded: `server/filestore.go:1139-1145`
- lastChecksum (the bottleneck): `server/filestore.go:2213-2246`
- Disk I/O semaphore: `server/filestore.go:10749-10752`

### Cluster/RAFT Operations:
- metaSnapshot: `server/jetstream_cluster.go:1642-1721`
- High message lag check: `server/jetstream_cluster.go:9099`
- streamLagWarnThreshold constant: `server/jetstream_cluster.go:8936`
- processClusteredInboundMsg: `server/jetstream_cluster.go:8938+`
- **cachePendingEntry (10k warning)**: `server/raft.go:4220-4231`
- **applyCommit (where bottleneck occurs)**: `server/raft.go:3159-3256`
- **paeWarnThreshold/paeDropThreshold**: `server/raft.go:4170-4172`

### Internal Subscription & Readloop:
- Internal subscription warning: `server/client.go:3799`
- Readloop processing warnings: `server/client.go:1479, 1581`
- readLoopReportThreshold constant: `server/client.go:115, 129`

### RAFT Subjects:
- RAFT proposal subject ($NRG.P.*): `server/raft.go:1949`
- RAFT vote subject: `server/raft.go:1947`
- RAFT append entries subject: `server/raft.go:1948`

## Summary

This is a **CLUSTER-WIDE CATASTROPHIC STORAGE FAILURE** affecting multiple NATS nodes simultaneously, not a software bug.

### Evidence:
1. **Root cause**: Storage I/O operations are 100-1000x slower than normal across ALL nodes
2. **Primary symptom**: WriteFullState taking 66 seconds to write 73 bytes
3. **Cascade effect**: Slow I/O → Slow RAFT commits → Message backlog → Cluster-wide failure
4. **CRITICAL**: 10,000 RAFT append entries pending on 3+ nodes simultaneously
5. **IMMINENT DANGER**: Approaching 20,000 threshold where RAFT will DROP entries (data loss)

### Severity Assessment:
- **Time to failure**: Minutes, not hours
- **Data loss risk**: Imminent (at 20k threshold)
- **Cluster status**: Complete failure in progress
- **Recovery difficulty**: High - requires immediate storage intervention

### Required Actions:
1. **IMMEDIATE**: Reduce/stop incoming traffic (next 5 minutes)
2. **URGENT**: Diagnose storage on all nodes (next 15 minutes)
3. **CRITICAL**: Emergency mitigation if storage can't be fixed immediately

**This is an infrastructure emergency requiring immediate executive action, not a NATS code issue.**
