# Review: PR #7883 - [FIXED] Race condition when scaling down/up consumer

**Author:** Maurice van Veen
**Branch:** `maurice/remove-consumer-signal` → `main`
**Commit:** `a5b786f`

---

## Summary

This PR fixes a race condition where the consumer monitor goroutine might not
have fully stopped before `deleteWithoutAdvisory()` is called during consumer
re-assignment or scale-down. It does this by:

1. Extracting consumer removal logic into a new `removeConsumer()` helper
   (mirroring the existing `removeStream()` pattern).
2. Adding `signalMonitorQuit()` + `monitorWg.Wait()` before deleting the consumer.
3. Fixing the scale-down-to-1 path in `processClusterCreateConsumer` to wait for
   the monitor goroutine before calling `setLeader(true)`.

---

## Code Review

### Positive aspects

1. **Follows established pattern.** The new `removeConsumer()` at line 5490 is
   structurally identical to `removeStream()` at line 4767. Both:
   - Step down and delete the raft node
   - Clear `Group.node` / `err` under `js.mu`
   - Check `shuttingDown` to skip waiting during shutdown
   - Signal monitor quit and wait
   - Then delete/stop

   This consistency makes the code easier to reason about.

2. **Removes dead code.** The `clearRaftNode()` method and unused `numReplicas`
   variable are cleaned up.

3. **Simplifies the else branch.** The old code had ~50 lines of complex
   leader-specific logic (peer selection for migration, `UpdateKnownPeers`, etc.)
   that is now replaced by a simple `node.StepDown(nca.Group.Preferred)` +
   `node.Delete()`. This is correct because:
   - The raft node is about to be deleted anyway, so peer management is unnecessary.
   - `StepDown(Preferred)` achieves the same intent as the manual peer selection.

4. **Fixes ordering in scale-down-to-1 (line 5776-5783).** Moving
   `o.setLeader(true)` to *after* the monitor quit wait is important because
   `setLeader(true)` triggers consumer processing (delivery, ack tracking, etc.)
   and this must not overlap with a still-running monitor goroutine.

### Issues and concerns

#### 1. Dropping `UpdateKnownPeers` — potential concern (Medium)

The old code called `node.UpdateKnownPeers(ca.Group.Peers)` before stepping down
to inform the raft node about the new peer set. The new code skips this entirely
and jumps straight to `StepDown` + `Delete`. Since the node is being deleted
immediately, this is likely fine — but if `StepDown` relies on knowing which peers
are valid to transfer leadership to, the stale peer list could cause a suboptimal
step-down target. In practice, `nca.Group.Preferred` should override this, so it's
likely not an issue, but worth verifying.

#### 2. No consumer existence guard before `removeConsumer` (Low)

The new code at line 5480-5484:
```go
} else if mset, _ := acc.lookupStream(sa.Config.Name); mset != nil {
    if o := mset.lookupConsumer(ca.Name); o != nil {
        s.removeConsumer(o, ca)
    }
}
```

The old code proceeded even when `o` was nil — it still cleared `ca.Group.node`
and `ca.err` under the lock. The new code only enters `removeConsumer` when the
consumer actually exists locally. If the consumer was already cleaned up but
`ca.Group.node` / `ca.err` still have stale values, they won't be cleared.

However, looking at the flow: `ca` is the *new* consumer assignment object that
was just constructed, and at line 5404 it copies `oca.Group.node` from the old
assignment. If the consumer doesn't exist locally but the assignment still has a
stale node reference, that reference won't be cleaned up.

**Recommendation:** Consider still clearing `ca.Group.node = nil` and
`ca.err = nil` even when the consumer lookup returns nil, to prevent stale state
from persisting in the assignment map. This could be done with a simple else clause
or by moving the cleanup outside the `if o != nil` check.

#### 3. `monitorWg.Wait()` blocking the meta apply loop (Medium)

Both `removeConsumer()` and the scale-down-to-1 path call
`o.monitorWg.Wait()` synchronously. These are called from
`processConsumerAssignment`, which runs in the meta raft group's apply loop. If
the monitor goroutine is slow to exit (e.g., it's in the middle of a long
operation or waiting on I/O), this will block the meta apply loop on that
server, preventing it from processing subsequent raft entries.

The stream equivalent (`removeStream`) has the same pattern (line 4790-4791), so
this is consistent. But the stream version at line 3591-3596 wraps the wait in a
goroutine (`go func() { ... }`) for some callers. If the monitor takes long to
shut down, this could affect cluster health.

The `isShuttingDown` check (line 5512) is a good guard for the shutdown case, but
doesn't protect against slow monitor exits during normal operation.

#### 4. Test: potential hang on failure path

The test at line 7973 does `wg.Add(1)` to artificially hold the wait group open.
If the test fails before reaching `wg.Done()` at line 7994 (which it does in our
environment with "context deadline exceeded" at line 7980), the deferred
`c.shutdown()` may hang because the server's shutdown path encounters the
unreleased wait group. This is exactly what we observed — the test hung for ~289
seconds before being killed.

**Recommendation:** Consider using `t.Cleanup()` or a deferred `wg.Done()` call
immediately after the `wg.Add(1)` to ensure the wait group is always released,
even on test failure:

```go
wg.Add(1)
t.Cleanup(func() { wg.Done() })
```

But this needs care to not double-Done. A safer approach might be to use an
`atomic.Bool` guard.

#### 5. Test: `errors.Is` direction on line 8036 (Low)

```go
if !errors.Is(NewJSStreamNotFoundError(), err) {
```

The standard `errors.Is(err, target)` convention has `err` as the first argument
(the error to check) and `target` as the second (the sentinel). Here the arguments
are reversed. While `NewJSStreamNotFoundError()` likely produces a comparable
error type, the reversed argument order deviates from the standard Go convention
and could mask issues if the error types have asymmetric `Is()` implementations.

---

## Test execution results

The test `TestJetStreamClusterScaleDownWaitsForMonitorRoutineQuit` **fails** in
this environment:

```
jetstream_cluster_3_test.go:7980: require no error, but got: context deadline exceeded
```

The `js.UpdateConsumer("TEST", ccfg)` call times out. This appears to be because:
- The default nats.go JetStream API timeout (5s) may be too short for a 3-node
  cluster operation in a resource-constrained environment.
- After the timeout, the test hangs during `c.shutdown()` because the artificial
  `wg.Add(1)` was never balanced with `wg.Done()`.

Other JetStream cluster tests (`TestJetStreamClusterStreamUpdateMaxConsumersLimit`)
pass successfully in this environment, confirming the cluster infrastructure works.

---

## Verdict

The core fix is sound and follows the established pattern from `removeStream()`.
The main concerns are:

1. **Stale assignment state** when consumer doesn't exist locally (concern #2) —
   this is the most actionable item.
2. **Test robustness** — the test can hang on failure, and the `errors.Is` argument
   order is non-standard.
3. **Blocking meta apply** — consistent with existing stream code, but worth noting.

**Recommendation:** Approve with minor changes to address concern #2 (stale
assignment cleanup) and concern #4 (test hang prevention).
