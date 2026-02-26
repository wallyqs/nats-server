# Gateway Code Stability Investigation

**Date:** 2026-02-26
**Scope:** `server/gateway.go` (3,426 lines) and `server/gateway_test.go` (7,667 lines)
**Focus Area:** Stale connection detection and reconnection recovery timing

---

## Executive Summary

The gateway code is a mature, complex subsystem responsible for cross-cluster message routing in NATS. It implements a sophisticated interest propagation system with three modes (Optimistic, Transitioning, InterestOnly) and manages both inbound and outbound gateway connections with full-mesh topology via gossip.

This investigation was prompted by reports that **gateway connections are prone to becoming stale and take too long to recover and reconnect**. The analysis found several concrete issues that explain this behavior, detailed in the new Section 5 below.

Overall, the code is well-structured with good concurrency primitives. However, this investigation identified several stability concerns across five categories: **stale connection detection gaps**, **reconnection timing issues**, **race conditions**, **error handling gaps**, and **test flakiness risks**.

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

## 5. Stale Connections and Slow Recovery (Reported Issue)

This section directly addresses the reported behavior: **gateway connections becoming stale and taking too long to recover**.

### 5.1 Stale Connection Detection Timeline

Gateway connections rely on the **ping/pong mechanism** to detect staleness. Here's how it works and where the delays accumulate:

**Key parameters (defaults):**

| Parameter | Default | Source |
|-----------|---------|--------|
| `PingInterval` | 2 minutes | `server/const.go:120` |
| `gwMaxPingInterval` | 15 seconds | `server/gateway.go:58` — caps `PingInterval` for gateways |
| `MaxPingsOut` | 2 | `server/const.go:123` |
| `firstPingInterval` | 1 second | `server/server.go:58` — only for initial ping |
| `WriteDeadline` | 10 seconds | `server/const.go:132` |

**Stale detection formula:**

For gateways, `adjustPingInterval()` (`server/client.go:5605`) caps the ping interval at `gwMaxPingInterval` (15s). With `MaxPingsOut=2`:

```
Time to detect stale = PingInterval * (MaxPingsOut + 1)
                     = min(PingInterval, 15s) * 3
                     = 15s * 3 = 45 seconds (worst case with defaults)
```

**However, the actual worst case is worse because:**

1. **The ping timer fires every `PingInterval`** — so the first ping may not go out for up to 15 seconds after the connection goes stale
2. **Each unanswered ping increments `ping.out`** but the connection is only closed when `ping.out + 1 > MaxPingsOut` (`server/client.go:5581`)
3. **With `MaxPingsOut=2`**: first ping at T+15s, second at T+30s, detection at T+45s

**Worst-case stale detection time: ~45 seconds** (with default settings).

### 5.2 Outbound Connection: Pre-INFO Stale Detection

For **outbound** gateway connections, there's a special stale detection path (`server/gateway.go:939-946`):

```go
// For outbound, we can't set the normal ping timer yet since the other
// side would fail with a parse error should it receive anything but the
// CONNECT protocol as the first protocol.
if solicit {
    c.watchForStaleConnection(adjustPingInterval(GATEWAY, opts.PingInterval), opts.MaxPingsOut)
}
```

`watchForStaleConnection` (`server/client.go:5620-5627`) sets a single timer for `PingInterval * (MaxPingsOut + 1)`. This means if the remote never sends its INFO protocol, the connection sits for `15s * 3 = 45 seconds` before being detected as stale. The test `TestGatewayOutboundDetectsStaleConnectionIfNoInfo` (`server/gateway_test.go:7473`) specifically validates this path.

**Problem:** During this 45-second window, the gateway appears connected but is completely non-functional. No messages flow and no alternative connection is attempted.

### 5.3 Half-Open TCP Connection Scenario

The most problematic scenario for stale connections is a **half-open TCP connection** (remote dies without sending FIN — e.g., network partition, VM crash, firewall drop):

1. **Read side:** The `readLoop` blocks on `reader.Read(b)` (`server/client.go:1358`). Without TCP keepalive at the OS level or a read deadline, this can block indefinitely.
2. **Write side:** Writes will eventually fail when the TCP send buffer fills, but only if messages are being sent. A gateway with no traffic won't detect the issue via writes.
3. **Ping/pong:** This is the **only** mechanism to detect the half-open case. With 15s intervals, it takes **up to 45 seconds** to detect.

**No read deadline is set on gateway connections.** Looking at `server/client.go:6361`, read deadlines are only set during TLS handshake, not during normal operation. This means detection relies entirely on the ping/pong cycle.

### 5.4 Reconnection Delay Analysis

Once a stale connection is **detected**, the reconnection path introduces further delays:

**Step 1: `reconnectGateway`** (`server/gateway.go:689-702`)
```go
delay := time.Duration(rand.Intn(100)) * time.Millisecond  // 0-100ms jitter
if !cfg.isImplicit() {
    delay += gatewayReconnectDelay  // +1 second for explicit gateways
}
```

- **Explicit gateways:** 1.0–1.1 seconds delay before first attempt
- **Implicit gateways:** 0–100ms delay

**Step 2: `solicitGateway` loop** (`server/gateway.go:706-785`)
- **TCP dial timeout:** 1 second per URL (`DEFAULT_ROUTE_DIAL`, `server/const.go:156`)
- **Between-attempt delay:** starts at 1 second (`gatewayConnectDelay`)
- **Exponential backoff** (when `ConnectBackoff=true`): 1s → 2s → 4s → 8s → 16s → 30s (capped at `gatewayConnectMaxDelay`)
- **No jitter on backoff delays** — only the initial reconnect has jitter

**Worst-case reconnection timeline (explicit gateway, backoff enabled, remote unavailable):**

| Step | Delay | Cumulative |
|------|-------|------------|
| Stale detection | 45s | 45s |
| reconnectGateway delay | 1.1s | 46.1s |
| Attempt 1: wait 1s + dial 1s | 2s | 48.1s |
| Attempt 2: wait 2s + dial 1s | 3s | 51.1s |
| Attempt 3: wait 4s + dial 1s | 5s | 56.1s |
| Attempt 4: wait 8s + dial 1s | 9s | 65.1s |
| Attempt 5: wait 16s + dial 1s | 17s | 82.1s |
| Attempt 6+: wait 30s + dial 1s | 31s each | 113.1s+ |

**Total time from connection going stale to reconnection (if remote comes back after attempt 5): ~82 seconds.**

### 5.5 Specific Gaps Identified

#### Gap 1: No Gateway-Specific Ping Configuration

Unlike routes (`opts.Cluster.PingInterval`) and websocket (`opts.Websocket.PingInterval`), **gateways have no dedicated PingInterval setting** (`server/opts.go:114-140`). They rely on the global `PingInterval` capped at 15 seconds. Operators who want faster stale detection for gateways must lower the global `PingInterval`, which also affects all clients.

**Location:** `server/client.go:5551` — gateway falls through to the global `opts.PingInterval` with no gateway-specific override.

#### Gap 2: No Gateway-Specific MaxPingsOut Configuration

Similarly, there is no `Gateway.MaxPingsOut` setting. Routes have `opts.Cluster.MaxPingsOut` (`server/client.go:5578-5579`), but gateways use the global `MaxPingsOut` (default: 2).

**Location:** `server/client.go:5577` — uses global `opts.MaxPingsOut` for gateways.

#### Gap 3: Exponential Backoff Has No Jitter

The exponential backoff in `solicitGateway` (`server/gateway.go:776-782`) doubles the delay but adds **no jitter**:

```go
if opts.Gateway.ConnectBackoff {
    attemptDelay *= 2
    if attemptDelay > gatewayConnectMaxDelay {
        attemptDelay = gatewayConnectMaxDelay
    }
}
```

When multiple gateways lose connectivity simultaneously (e.g., network partition recovery), they all retry at the same exponential intervals, creating **thundering herd** reconnection storms. Compare with `reconnectGateway` (line 692) which does add jitter for the initial delay, but this jitter doesn't carry into the backoff.

#### Gap 4: Write Timeout Policy Defaults to Retry (Not Close)

For gateways, the `WriteTimeoutPolicy` defaults to `WriteTimeoutPolicyRetry` (`server/client.go:731`):

```go
case GATEWAY:
    if c.out.wtp = opts.Gateway.WriteTimeout; c.out.wtp == WriteTimeoutPolicyDefault {
        c.out.wtp = WriteTimeoutPolicyRetry
    }
```

This means when a write deadline is exceeded, the gateway connection is marked as a **slow consumer but NOT closed** (`server/client.go:1890-1895`). It stays open in a degraded state, retrying writes. For a truly stale connection, this means the write side may keep retrying for a long time before the ping/pong mechanism finally closes the connection.

#### Gap 5: Implicit Gateway Removal After ConnectRetries

For implicit (auto-discovered) gateways, if `ConnectRetries` is exceeded and there is no inbound connection, **the gateway is permanently deleted** (`server/gateway.go:767`):

```go
delete(s.gateway.remotes, cfg.Name)
```

This means if a network partition lasts longer than `ConnectRetries * backoff_delays`, the gateway is forgotten and won't automatically re-establish even when the network recovers. The only way to recover is if the remote side initiates a new connection.

#### Gap 6: 1-Second Fixed Reconnect Delay for Explicit Gateways

`reconnectGateway` adds a fixed 1-second `gatewayReconnectDelay` for explicit gateways (`server/gateway.go:693-694`). This is not configurable. For latency-sensitive deployments, this adds an unnecessary fixed floor to recovery time.

### 5.6 Best-Case vs Worst-Case Recovery Summary

| Scenario | Detection Time | Reconnection Time | Total |
|----------|---------------|-------------------|-------|
| Clean disconnect (FIN received) | Immediate | 1.0-1.1s + dial | ~2.2s |
| Half-open TCP, no backoff | Up to 45s | 1.0-1.1s + dial | ~47s |
| Half-open TCP, with backoff, remote comes back after 3 attempts | Up to 45s | 1.1s + 2s + 3s + 5s | ~56s |
| Half-open TCP, with backoff, 30s cap reached | Up to 45s | 1.1s + 67s+ | ~113s+ |

---

## 6. Summary of Findings

| # | Category | Issue | Severity | Status |
|---|----------|-------|----------|--------|
| 1 | **Stale Detection** | **No gateway-specific PingInterval setting** | **High** | Gap |
| 2 | **Stale Detection** | **No gateway-specific MaxPingsOut setting** | **High** | Gap |
| 3 | **Stale Detection** | **45s worst-case stale detection time** | **High** | By design |
| 4 | **Reconnection** | **No jitter on exponential backoff (thundering herd)** | **High** | Gap |
| 5 | **Reconnection** | **Fixed 1s reconnect delay for explicit gateways** | **Medium** | Gap |
| 6 | **Reconnection** | **Implicit gateways deleted after ConnectRetries** | **Medium** | By design |
| 7 | **Write Side** | **WriteTimeoutPolicy defaults to Retry, not Close** | **Medium** | By design |
| 8 | Race Condition | TLS config race in `sendGatewayConnect` | High | Acknowledged (FIXME) |
| 9 | Race Condition | outsim pointer validity window | Low | By design |
| 10 | Race Condition | Mode transition message routing | Medium | By design |
| 11 | Deadlock Risk | pasi.Lock → c.mu.Lock ordering | Low | Correct currently |
| 12 | Error Handling | Panics on JSON marshal failure | Low probability, high impact | Open |
| 13 | Error Handling | Missing protocol violation check | Low | Open (TODO) |
| 14 | Error Handling | 0.0.0.0 address advertising | Medium | Open (TODO) |
| 15 | Performance | Hardcoded interest-only threshold | Low | Open (TODO) |
| 16 | Test Stability | 29 time.Sleep calls in tests | Medium | Ongoing risk |
| 17 | Test Stability | Global state mutations in tests | Low | Managed via defer |
| 18 | Test Stability | TestGatewayComplexSetup retry loop | Medium | Fragile pattern |

### Recommendations (Ordered by Impact on Stale Connection / Slow Recovery Issues)

1. **Add gateway-specific `PingInterval` and `MaxPingsOut` configuration** to `GatewayOpts` (like routes have `Cluster.PingInterval` and `Cluster.MaxPingsOut`). This would allow operators to tune stale detection independently. For example, setting `Gateway.PingInterval=5s` and `Gateway.MaxPingsOut=1` would reduce detection from 45s to ~10s.

2. **Add jitter to the exponential backoff** in `solicitGateway` (line 776). When a network partition heals and multiple gateways reconnect simultaneously, the lack of jitter creates thundering herd behavior. Adding `rand.Intn(attemptDelay/4)` jitter would spread reconnection attempts.

3. **Make `gatewayReconnectDelay` configurable** — the fixed 1-second delay before reconnection attempts begin adds latency to every explicit gateway recovery. Allowing operators to tune this (or set it to 0) would improve recovery time.

4. **Consider defaulting `WriteTimeoutPolicy` to `Close` for gateways** instead of `Retry`. A gateway connection that repeatedly fails write deadlines is likely stale and should be replaced rather than retried indefinitely. At minimum, add a max-retry count before closure.

5. **Fix the TLS config race** (line 959) by acquiring `cfg.RLock()` before reading `TLSConfig`.

6. **Replace panics** with error returns and connection closure in `generateInfoJSON` and `sendGatewayConnect`.

7. **Reduce test flakiness** by replacing `time.Sleep` patterns with channel-based synchronization where feasible.

8. **Consider making the interest-only threshold configurable** or adaptive based on account activity.

9. **Plan for optimistic mode removal** to simplify the codebase once backward compatibility is no longer needed.
