# Plan: Prevent stream deletion when standalone data joins existing cluster

## Problem

When a server with standalone JetStream data joins an **existing** cluster (one that already
has RAFT meta state), the server's `bootstrapped` flag is `true` (no prior `peers.idx` file),
but the meta leader's `bootstrapped` is `false` (it has existing state). Currently:

- **Non-leader bootstrapped node** (our node with data): `checkForOrphans()` skips deletion
  (our recent fix), but streams are never adopted — they sit in limbo.
- **If we lose the bootstrapped check somehow**: streams get deleted as orphans.
- **The meta leader has no idea** that the new node has standalone streams to adopt.

The core issue: only the meta leader can propose assignments, but the meta leader doesn't
have the standalone data and doesn't know it exists on the new node.

## Solution: Two-phase approach

### Phase 1: Non-leader bootstrapped nodes request adoption via internal messaging

Use an internal NATS subject so that a bootstrapped non-leader node can send adoption
requests to the meta leader. This follows the same pattern used by `streamResults`,
`consumerResults`, and other leader-only subscriptions in `processLeaderChange()`.

#### Changes to `server/jetstream_cluster.go`:

**1. Add constants and types (~line 52)**:
```go
const jsAdoptStreamRequestSubj = "$JSC.ADOPT"
```

**2. Add subscription field to `jetStreamCluster` struct (~line 83)**:
```go
adoptSub *subscription   // Subscription for adoption requests (leader only)
```

**3. Subscribe/unsubscribe in `processLeaderChange()` (~line 6752)**:
When becoming leader:
```go
cc.adoptSub, _ = s.systemSubscribe(jsAdoptStreamRequestSubj, _EMPTY_, false, nil, js.handleAdoptStreamRequest)
```
When stepping down:
```go
s.sysUnsubscribe(cc.adoptSub)
cc.adoptSub = nil
```

**4. Add handler `handleAdoptStreamRequest()`**:
- Decode the incoming adoption request (account, stream config, created time, node ID)
- Check if a stream with this name ALREADY exists in meta assignments:
  - If yes, log and skip (prevents duplicate stream name issue)
  - If no, construct a `streamAssignment` with the requesting node as sole peer and propose it
- Do the same for consumers (sent as part of the request)

**5. Modify `checkForOrphans()` non-leader path (~line 1436)**:
Instead of just logging and returning, send adoption requests:
```go
if bootstrapped && len(streams) > 0 && !meta.Leader() {
    js.requestAdoptionFromLeader(streams)
    // Clear bootstrapped after sending requests
    js.mu.Lock()
    if js.cluster != nil {
        js.cluster.bootstrapped = false
    }
    js.mu.Unlock()
    return
}
```

**6. Add `requestAdoptionFromLeader()` method**:
- For each orphaned stream, encode its config + account + created time + our node ID
- Publish to `jsAdoptStreamRequestSubj` via `sendInternalMsgLocked()`
- Include consumer information for each stream

### Phase 2: Guard against duplicate stream names

In both `adoptLocalStreams()` (leader path) and `handleAdoptStreamRequest()` (non-leader path):

Before proposing any stream assignment, check if the stream name already exists in
`cc.streams[accName][streamName]`. If it does:
- Log a warning: "stream '%s > %s' already exists in cluster, skipping adoption"
- Skip the proposal
- This prevents conflicts when both the cluster and the standalone node have a stream
  with the same name

#### Changes:

In `adoptLocalStreams()` (~line 1490), the existing check `asa == nil || asa[name] == nil`
already handles this — streams WITH assignments are not added to `toAdopt`. But add an
explicit log message when a standalone stream collides with an existing cluster stream:
```go
if asa != nil && asa[name] != nil {
    s.Noticef("JetStream skipping adoption of '%s > %s': stream already exists in cluster", accName, name)
}
```

### Phase 3: Replica count handling

In `adoptLocalStreams()` and `handleAdoptStreamRequest()`, normalize the replica count:
- Force `cfg.Replicas = 1` in the adopted stream config to match the R1 group
- This prevents the mismatch between `Config.Replicas` and `len(Group.Peers)`
- The operator can later scale up replicas using `$JS.API.STREAM.UPDATE`

## Test changes

### Test 1: Update existing test
The existing `TestJetStreamStandaloneToClusteredModeSwitch` already tests the leader-has-data
path. No changes needed.

### Test 2: New test `TestJetStreamStandaloneToClusteredNonLeaderAdoption`
- Phase 1: Create standalone server with streams/consumers/messages
- Phase 2: Start a 3-node cluster where S-2 (NOT S-1) has the standalone store dir
- Wait for the cluster to form (S-1 or S-3 becomes leader, not S-2)
- Verify that S-2 sends adoption requests to the leader
- Verify streams are adopted and data is preserved
- Verify the stream can accept new publishes

### Test 3: New test `TestJetStreamStandaloneToClusteredDuplicateStreamName`
- Phase 1: Create standalone server with stream "ORDERS"
- Phase 2: Start a 3-node cluster, create a DIFFERENT stream also named "ORDERS"
- Add the standalone node (with its "ORDERS" stream) to the cluster
- Verify the standalone "ORDERS" is NOT adopted (cluster version takes precedence)
- Verify the cluster "ORDERS" is intact
- Verify the standalone "ORDERS" data is preserved on disk (not deleted) but not in cluster

## Files to modify

1. `server/jetstream_cluster.go` — main implementation
2. `server/jetstream_cluster_mode_switch_test.go` — tests

## Encoding format for adoption requests

Use JSON encoding (consistent with other JetStream internal messages):
```go
type jsAdoptionRequest struct {
    Account   string          `json:"account"`
    Config    StreamConfig    `json:"config"`
    Created   time.Time       `json:"created"`
    NodeID    string          `json:"node_id"`
    Consumers []jsAdoptConsumerInfo `json:"consumers,omitempty"`
}

type jsAdoptConsumerInfo struct {
    Name    string         `json:"name"`
    Config  ConsumerConfig `json:"config"`
    Created time.Time      `json:"created"`
}
```
