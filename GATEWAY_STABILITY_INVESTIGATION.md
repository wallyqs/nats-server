# Gateway Code Stability Investigation

**Date:** 2026-02-26
**Scope:** `server/gateway.go` (3,426 lines) and `server/gateway_test.go` (7,667 lines)

---

## Executive Summary

The gateway code is a mature, complex subsystem responsible for cross-cluster message routing in NATS. It implements a sophisticated interest propagation system with three modes (Optimistic, Transitioning, InterestOnly) and manages both inbound and outbound gateway connections with full-mesh topology via gossip.

Overall, the code is well-structured with good concurrency primitives. However, this investigation identified several stability concerns across four categories: **race conditions**, **error handling gaps**, **test flakiness risks**, and **design-level concerns**.

---

## 1. Race Conditions and Concurrency Issues

### 1.1 FIXME: TLS Config Race (HIGH — Acknowledged)

**Location:** `server/gateway.go:959`

```go
// FIXME: This can race with updateRemotesTLSConfig
tlsRequired := c.gw.cfg.TLSConfig != nil
```

The `sendGatewayConnect` function reads `c.gw.cfg.TLSConfig` while holding the client lock, but `updateRemotesTLSConfig` (called during config reload) may concurrently modify this field. The `gatewayCfg` has its own RWMutex but it is not acquired here. This is an acknowledged race that could cause incorrect TLS negotiation during a config reload, potentially leading to connection failures or insecure connections.

**Impact:** Connection failures or security degradation during TLS config reload.

### 1.2 outsim (sync.Map) Pointer Validity Window

**Location:** `server/gateway.go:2166-2207`

The `gatewayInterest()` function loads a pointer from `outsim` (a `sync.Map`) and then acquires a read lock on the pointed-to `outsie` struct:

```go
ei, accountInMap := c.gw.outsim.Load(acc)
// ...
e := ei.(*outsie)
e.RLock()
```

Between `Load()` returning and `RLock()` being acquired, the entry could be deleted from the map and the `outsie` struct could be modified by `processGatewayAccountUnsub` (line 1897) which stores `nil` in place of the entry. While this won't crash (the loaded pointer is still valid in memory), it means interest decisions may be based on stale data during the transition window. The `sync.Map` design is intentional to avoid lock contention on the hot path, but this creates a window for incorrect routing decisions.

**Impact:** Low — messages may be sent to gateways that just lost interest, or suppressed momentarily. Self-correcting via subsequent RS+/- protocols.

### 1.3 insim (Regular Map) Protected Only by Client Lock

**Location:** Multiple: lines 1360, 2221, 2259, 2374, 2805, 2848

The `insim` map (inbound subject interest map) is a regular `map[string]*insie` protected by the client mutex (`c.mu`). Access patterns are consistent — all accesses hold `c.mu` — but some patterns involve nested locking:

- `sendSubsToGateway` (line 1342) holds `gw.pasi.Lock()` and then acquires `c.mu.Lock()` (line 1384)
- `gatewayHandleSubjectNoInterest` (line 2830) holds `gw.pasi.Lock()` and then acquires `c.mu.Lock()` (line 2845)

This creates a consistent lock ordering of `pasi.Lock → c.mu.Lock`, which is correct. However, if any code path ever reverses this order, it would deadlock.

**Impact:** Low currently, but a latent deadlock risk if lock ordering is violated in future changes.

### 1.4 Mode Transition Race During Interest-Only Switch

**Location:** `server/gateway.go:3260-3314`

When switching an account to interest-only mode (`gatewaySwitchAccountToSendAllSubs`), the function:
1. Sets `e.ni = nil` and `e.mode = Transitioning` (under client lock)
2. Sends `gatewayCmdAllSubsStart` INFO
3. Launches a **goroutine** to send all subscriptions
4. The goroutine sends `gatewayCmdAllSubsComplete` when done

During step 3, the goroutine runs without the client lock while sending subscriptions via `sendAccountSubsToGateway`. Meanwhile, the outbound side may still be in the `Transitioning` state processing messages. The comment in `gatewayInterest()` (line 2183-2188) acknowledges this:

```go
// Unless each side has agreed on interest-only mode,
// we may be in transition to modeInterestOnly
// but until e.ni is nil, use it to know if we
// should suppress interest or not.
```

This is by design but means messages may be unnecessarily sent or suppressed during the transition window.

**Impact:** Medium — brief window of incorrect message routing during mode transitions. Self-correcting once transition completes.

---

## 2. Error Handling Gaps

### 2.1 Panics on JSON Marshal Failure

**Locations:** `server/gateway.go:659` and `server/gateway.go:982`

```go
// Line 659 (generateInfoJSON)
b, err := json.Marshal(g.info)
if err != nil {
    panic(err)
}

// Line 982 (sendGatewayConnect)
b, err := json.Marshal(cinfo)
if err != nil {
    panic(err)
}
```

Both locations panic if JSON marshaling fails. While `json.Marshal` on these structs should never fail (they contain only basic types), a panic would crash the entire server. Other similar call sites in the same file (e.g., line 3283) silently discard the error with `b, _ := json.Marshal(&info)`.

**Impact:** Very low probability but catastrophic if triggered — entire server crashes.

### 2.2 TODO: Missing Protocol Violation Check

**Location:** `server/gateway.go:3195`

```go
// TODO(ik): Should we close connection with protocol violation
// error if that happens?
ei, _ := c.gw.outsim.Load(account)
```

When receiving `gatewayCmdAllSubsStart`, if there's no existing entry for the account, the code creates a new one instead of treating this as a protocol violation. A malicious or buggy remote gateway could trigger unnecessary state allocation.

**Impact:** Low — mostly correctness concern; could lead to minor memory waste from invalid commands.

### 2.3 TODO: 0.0.0.0 Address Advertising

**Location:** `server/gateway.go:618`

```go
// TODO(ik): Should we fail here (prevent starting)? If not, we
// are going to "advertise" the 0.0.0.0:<port> url
```

When no non-local IP can be resolved and the listen address is `0.0.0.0`, the server advertises an unreachable address. Remote gateways will attempt to connect to `0.0.0.0` which resolves to loopback, causing connection failures.

**Impact:** Medium in certain network configurations — gateway mesh fails to form.

### 2.4 Hardcoded Interest-Only Mode Threshold

**Location:** `server/gateway.go:2858`

```go
// TODO(ik): pick some threshold as to when
// we need to switch mode
if len(e.ni) >= gatewayMaxRUnsubBeforeSwitch {
```

The threshold for switching from optimistic to interest-only mode is hardcoded at 1000 RS- protocols. This may not be optimal for all deployment sizes. Too low causes premature switching (increased subscription traffic); too high causes excessive RS- traffic.

**Impact:** Performance — may not be optimal for all deployment scales.

---

## 3. Test Stability Risks

### 3.1 Timing-Dependent Tests (29 `time.Sleep` calls)

The test suite has 29 instances of `time.Sleep()` ranging from 1ms to 1 second. The most concerning patterns:

| Test | Line | Sleep | Risk |
|------|------|-------|------|
| `TestGatewaySolicitDelay` | 717 | 500ms | Checks timing-based solicit delay |
| `TestGatewaySolicitDelayWithImplicitOutbounds` | 758 | 750ms | Verifies no double connection |
| `TestGatewaySingleOutbound` | 5991 | 500ms | Waits for reconnection attempts |
| `TestGatewayTLSCertificate*` | 6698 | 1s | Long wait for fail case |
| `TestGatewayRaceOnClose` | 3709 | 200ms | Delay before shutdown in race test |
| `TestGatewayComplexSetup` | 2842 | 100ms | In retry loop — restarts server |

`TestGatewayComplexSetup` (line 2778) is particularly fragile: it uses a retry loop with `time.Sleep(100ms)` and restarts a server if the outbound gateway doesn't connect to the expected server within 10 iterations.

### 3.2 Global State Mutations

Multiple tests modify package-level variables:
- **`init()` function** (line 42): Sets all gateway delays to 15ms for tests
- **9 tests** call `GatewayDoNotForceInterestOnlyMode(true/false)`
- **Tests modifying `gatewayMaxRUnsubBeforeSwitch`** and **`gatewayMaxPingInterval`**

These are restored via `defer` but if a test panics before cleanup, subsequent tests may behave differently. Since Go runs tests in the same process, this creates inter-test dependencies.

### 3.3 No Skipped Tests

No gateway tests are marked as skipped (`t.Skip`), which means all 81 tests run in CI. This is positive for coverage but means any flakiness directly impacts CI reliability.

### 3.4 Race-Specific Tests (3 tests)

- `TestGatewayImplicitReconnectRace` — well-designed with channel-based coordination
- `TestGatewayRaceBetweenPubAndSub` — relies on `time.Sleep(100ms)` for setup timing
- `TestGatewayRaceOnClose` — relies on `time.Sleep(200ms)` before shutdown

The latter two could be more robust with channel-based synchronization instead of fixed sleeps.

---

## 4. Design-Level Observations

### 4.1 Goroutine Lifecycle Management

The gateway code spawns goroutines via `s.startGoRoutine()` with `s.grWG.Done()` for cleanup coordination. All 7 goroutine spawn points in gateway.go properly use this pattern:

| Line | Purpose | Cleanup |
|------|---------|---------|
| 492 | Solicit delay | `defer s.grWG.Done()` |
| 679 | Reconnect per-gateway | `defer s.grWG.Done()` (via `reconnectGateway`) |
| 928 | Read loop | Managed by client lifecycle |
| 931 | Write loop | Managed by client lifecycle |
| 1494 | Implicit gateway connect | `s.grWG.Done()` at end |
| 3302 | Send all subs (mode switch) | `defer s.grWG.Done()` |
| 3375 | Reply map expiration | `defer s.grWG.Done()` |

The goroutine at line 3302 (mode switch) deserves attention: it sends subscriptions to a specific client connection. If the connection closes during this operation, `enqueueProto` calls will fail silently since the client's write buffer will be closed. This is handled correctly by the write loop detecting closure.

### 4.2 Interest Mode Transition (Optimistic → InterestOnly)

Starting in v2.9.0, all accounts are forced to interest-only mode on inbound gateway connections (line 1237-1245). The old optimistic mode is being phased out but is still supported for backward compatibility. This dual-mode support adds complexity:

- `gwDoNotForceInterestOnlyMode` global flag (used only in tests)
- Conditional logic throughout interest propagation code
- 9 tests explicitly disable the forced mode to test legacy behavior

Once the optimistic mode is fully deprecated, significant simplification is possible.

### 4.3 Connection Registration — Double Registration Prevention

Lines 1152-1163 contain defensive code against a previously fixed bug where outbound connections could be registered twice:

```go
} else {
    // There was a bug that would cause a connection to possibly
    // be called twice resulting in reconnection of twice the
    // same outbound connection. The issue is fixed, but adding
    // defensive code...
    c.flags.set(noReconnect)
    c.mu.Unlock()
    c.closeConnection(WrongGateway)
    return
}
```

This is good defensive programming for a previously-observed stability issue.

### 4.4 Subscription Pool Reuse

Line 2524-2528 and usage at 2593/2775: A `sync.Pool` is used for subscription objects in `sendMsgToGateways`. The pool properly clears the `client` reference before returning to the pool (`sub.client = nil`). This prevents holding references to closed connections.

---

## 5. Summary of Findings

| Category | Issue | Severity | Status |
|----------|-------|----------|--------|
| Race Condition | TLS config race in `sendGatewayConnect` | High | Acknowledged (FIXME) |
| Race Condition | outsim pointer validity window | Low | By design |
| Race Condition | Mode transition message routing | Medium | By design |
| Deadlock Risk | pasi.Lock → c.mu.Lock ordering | Low | Correct currently |
| Error Handling | Panics on JSON marshal failure | Low probability, high impact | Open |
| Error Handling | Missing protocol violation check | Low | Open (TODO) |
| Error Handling | 0.0.0.0 address advertising | Medium | Open (TODO) |
| Performance | Hardcoded interest-only threshold | Low | Open (TODO) |
| Test Stability | 29 time.Sleep calls in tests | Medium | Ongoing risk |
| Test Stability | Global state mutations in tests | Low | Managed via defer |
| Test Stability | TestGatewayComplexSetup retry loop | Medium | Fragile pattern |

### Recommendations

1. **Fix the TLS config race** (line 959) by acquiring `cfg.RLock()` before reading `TLSConfig`.
2. **Replace panics** with error returns and connection closure in `generateInfoJSON` and `sendGatewayConnect`.
3. **Reduce test flakiness** by replacing `time.Sleep` patterns with channel-based synchronization where feasible.
4. **Consider making the interest-only threshold configurable** or adaptive based on account activity.
5. **Plan for optimistic mode removal** to simplify the codebase once backward compatibility is no longer needed.
