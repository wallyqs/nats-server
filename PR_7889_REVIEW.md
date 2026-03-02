# Review: PR #7889 — [IMPROVED] R1 Consumer leader process in goroutine

**Author:** MauriceVanVeen
**File changed:** `server/jetstream_cluster.go` (+18 / -1)

## Summary

The PR moves `js.processConsumerLeaderChange(o, true)` for R1 (single-replica) consumers from being called synchronously on the meta processing path into a separate goroutine using the `shouldStartMonitor()` / `startGoRoutine()` pattern. This mirrors how replicated consumers launch `monitorConsumer` in a goroutine.

**Motivation:** `processConsumerLeaderChange` → `o.setLeader(true)` performs significant work: `readStoredState()`, `streamNumPending()`, `checkPending()`, setting up subscriptions, and spawning delivery goroutines. Doing this synchronously blocks meta operations.

## The Change

```go
// Before (line 5820):
js.processConsumerLeaderChange(o, true)

// After:
if o.shouldStartMonitor() {
    started := s.startGoRoutine(
        func() {
            defer s.grWG.Done()
            defer o.clearMonitorRunning()
            js.processConsumerLeaderChange(o, true)
        },
        pprofLabels{...},
    )
    if !started {
        o.clearMonitorRunning()
    }
}
```

## Issues

### 1. Config updates can silently drop leader processing (Correctness Bug)

When an R1 consumer config is updated, `processClusterCreateConsumer` is called again (via `jetstream_cluster.go:5482`). The code at lines 5816-5817 sets `ca.responded = false` so that `processConsumerLeaderChange` will send the API response to the waiting client.

With this PR, if the goroutine from the *initial* create (or a previous update) hasn't exited yet, `o.shouldStartMonitor()` returns `false` because `o.inMonitor` is still `true`. This means:

- `processConsumerLeaderChange` is **never called** for the config update
- The API response is never sent (`ca.responded` was set to `false` but nobody sets it back to `true` or sends the response)
- **The client will hang or timeout** waiting for the config update response

This is the most serious issue. The `shouldStartMonitor()` / `inMonitor` mechanism was designed for long-lived `monitorConsumer` loops where only one should ever run. Reusing it for a short-lived, fire-and-forget goroutine means concurrent calls are silently dropped rather than queued or executed.

**Scenario:** Create R1 consumer → goroutine starts → immediately update config → `shouldStartMonitor()` returns false → update response never sent.

The clustered path avoids this because the `monitorConsumer` loop handles leader changes via the `lch` channel — updates don't need to spawn new goroutines, the existing loop processes them. The R1 path has no such loop.

### 2. `resetAndWaitOnConsumers` cannot interrupt the R1 goroutine

`resetAndWaitOnConsumers` (`stream.go:7039-7057`) calls `o.signalMonitorQuit()` followed by `o.monitorWg.Wait()` to drain monitor goroutines during stream shutdown. The R1 goroutine doesn't listen to `mqch` — it just runs `processConsumerLeaderChange` and exits. The quit signal has no effect.

`monitorWg.Wait()` will eventually return when `processConsumerLeaderChange` completes naturally, but since the *entire motivation* of this PR is that `setLeader()` is expensive, this means `resetAndWaitOnConsumers` could block for a meaningful period on an operation it cannot cancel. This somewhat undermines the graceful shutdown mechanism.

### 3. Scale-down timing window

When a consumer scales from R>1 to R1 (lines 5797-5809), `clearNode()` is called and the function returns early before reaching the changed code. The old `monitorConsumer` goroutine will exit when the raft node stops, calling `clearMonitorRunning()`. However, if `processClusterCreateConsumer` is subsequently called for this now-R1 consumer *before* the old monitor goroutine has fully exited and cleared `inMonitor`, `shouldStartMonitor()` will return `false` and the consumer will fail to initialize as leader.

This is a narrow timing window but could occur under load during scale-down operations.

## Positive Aspects

- **Sound motivation** — `o.setLeader(true)` is genuinely heavy work (`streamNumPending`, `readStoredState`, subscription setup, spawning delivery goroutines) and blocking meta on it is a real issue.
- **Correct goroutine lifecycle** — `defer s.grWG.Done()` and `defer o.clearMonitorRunning()` properly mirror the `monitorConsumer` pattern.
- **Proper failure handling** — `clearMonitorRunning()` is called when `startGoRoutine` returns false.
- **pprof labels** — Good observability with consumer/stream/account labels.

## Suggestions

The core tension is that `shouldStartMonitor()` / `clearMonitorRunning()` is a "one goroutine at a time" mechanism, but the R1 path needs "every call must be processed." A few approaches to consider:

**Option A: Don't gate on `shouldStartMonitor()` for R1.** Simply spawn the goroutine unconditionally (just use `startGoRoutine` without the `shouldStartMonitor` guard). This trades the possibility of multiple concurrent `processConsumerLeaderChange` calls for the guarantee that config updates are never dropped. Since `processConsumerLeaderChange` already holds locks appropriately, concurrent calls should be safe, and the second call's `setLeader(true)` with `wasLeader=true` will short-circuit (lines 1492-1518 in consumer.go).

**Option B: Process synchronously on updates, async only for initial creation.** Add a condition like `if !wasExisting` around the goroutine launch, keeping the synchronous path for config updates. This preserves the original response guarantees for updates while getting the async benefit for initial creation.

**Option C: Use a dedicated channel/queue for R1 leader processing.** Instead of spawning a goroutine per call, start a single worker goroutine for R1 consumers and send work items to it. This keeps the "don't block meta" benefit while ensuring all operations are processed in order.
