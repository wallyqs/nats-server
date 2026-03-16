# Fast Producer Stall Mechanism: Design Analysis

## 1. How the Stall Mechanism Works Today

### Overview

The "fast producer stall" is a backpressure mechanism in the NATS server that
slows down producers when a subscriber's outbound buffer is filling up faster
than it can be flushed to the network. Without it, fast producers would rapidly
overflow the subscriber's buffer until the subscriber is disconnected as a "slow
consumer."

### The Three Lifecycle Phases

**Phase 1: Stall Gate Creation** (`client.go:2517`)

When data is queued into a client's outbound buffer via `queueOutbound()`, if
the pending bytes (`c.out.pb`) exceed 75% of the maximum pending limit
(`c.out.mp`), a stall channel is created:

```go
if c.out.pb > c.out.mp/4*3 && c.out.stc == nil {
    c.out.stc = make(chan struct{})
}
```

This channel acts as a gate — producers will block on it.

**Phase 2: Producers Block** (`client.go:3818, stalledWait at 3582`)

In `deliverMsg()`, when the *producing* client (`c`) is delivering a message to
the *subscribing* client (`client`), it checks whether the subscriber has a
stall gate open:

```go
if (c.kind == CLIENT || c.kind == JETSTREAM) && client.out.stc != nil {
    client.stalledWait(c)
}
```

`stalledWait()` then:
1. Checks the producer hasn't exceeded its total stall budget (10ms per readLoop)
2. Picks a timeout: 2ms (normal) or 5ms (buffer at max pending)
3. Blocks on `select { case <-stall: case <-delay.C: }`
4. Increments per-client and server-wide stall counters

**Phase 3: Stall Gate Release** (`client.go:1833`)

In `flushOutbound()`, after the subscriber's writeLoop successfully writes data
to the network, the stall gate is released if the buffer has drained
sufficiently (below 75%) or all data was written:

```go
if c.out.stc != nil && (n == attempted || c.out.pb < c.out.mp/4*3) {
    close(c.out.stc)
    c.out.stc = nil
}
```

Closing the channel unblocks all producers waiting on it simultaneously.

### Constants

| Constant | Value | Purpose |
|---|---|---|
| `stallClientMinDuration` | 2ms | Normal stall timeout per delivery |
| `stallClientMaxDuration` | 5ms | Stall timeout when buffer is at max |
| `stallTotalAllowed` | 10ms | Max total stall time per readLoop iteration |

### The ReadLoop Budget

Each readLoop iteration tracks total stall time in `producer.in.tst`. Once a
producer has spent 10ms stalled across all deliveries in a single readLoop pass,
it stops stalling and delivers without blocking. This prevents a single slow
subscriber from monopolizing the producer's goroutine indefinitely. After the
readLoop iteration completes, `tst` is reset to zero, and a warning is logged if
it exceeded `stallClientMaxDuration`.

---

## 2. The Bug We Fixed

### The Problem

The stall gate check was originally:

```go
if c.kind == CLIENT && client.out.stc != nil {
```

This only stalled **external client** producers. JetStream internal clients
(`c.kind == JETSTREAM`), which deliver messages from streams to push consumer
subscribers, were **exempt** from the stall gate.

**Consequence:** When a push consumer's subscriber was slow:
1. The stall gate would be created on the subscriber's outbound buffer
2. External `CLIENT` producers would respect the gate and block
3. The JetStream internal client would ignore the gate and keep flooding the
   subscriber's buffer
4. The buffer would never drain below 75% because JetStream kept adding to it
5. The stall gate channel was never closed (released)
6. External `CLIENT` producers remained blocked **indefinitely**
7. This manifested as "client stalling" — regular publishers appeared to hang

### The Fix

```go
if (c.kind == CLIENT || c.kind == JETSTREAM) && client.out.stc != nil {
```

Now JetStream internal clients also block on the stall gate, allowing the
subscriber's buffer to actually drain and the gate to release.

---

## 3. Design Critique

### What the Current Design Gets Right

1. **Simplicity**: The channel-based gate is elegant — one channel creation, one
   close, all blocked producers unblock atomically via Go's channel semantics.

2. **Bounded impact on producers**: The 10ms-per-readLoop budget ensures a
   producer can never be fully starved by a slow consumer. After exhausting the
   budget, it delivers without waiting.

3. **Self-healing**: If the subscriber disconnects, `closeConnection()` closes
   the stall channel, unblocking all producers immediately.

4. **Low overhead in the common case**: When `stc == nil` (buffer is healthy),
   the check is a single nil pointer comparison — essentially free.

### Concerns and Weaknesses

#### 3.1 Allowlist vs. Denylist for `c.kind`

The current fix uses an allowlist approach:

```go
if (c.kind == CLIENT || c.kind == JETSTREAM) && client.out.stc != nil {
```

This is fragile. Any future `kind` value (or internal producer type) that
delivers messages to subscribers will **silently bypass** the stall gate unless
someone remembers to add it here. The original bug was exactly this — `JETSTREAM`
was added as a client kind but not included in the stall check.

**Alternative considered — denylist approach:**

```go
if c.kind != ROUTER && c.kind != GATEWAY && c.kind != LEAF && client.out.stc != nil {
```

This would mean any new client kind defaults to respecting the stall gate.
However, this is also fragile — it might accidentally stall internal system
operations (e.g., `SYSTEM` or `ACCOUNT` clients) that should not be delayed.

**Better alternative — use a property, not a type check:**

Instead of checking `c.kind`, the client could carry a flag like
`c.respectsStallGate` or the stall check could be based on whether the producer
has a network readLoop that should respect backpressure. This decouples the
decision from the enumeration of client types.

#### 3.2 Blocking the Producer's Goroutine

The fundamental design choice is to block the producer's goroutine (readLoop) in
`stalledWait()`. This means:

- The producer's **entire readLoop** is paused — it cannot process other
  messages, PINGs, or protocol operations during the stall.
- For a JetStream internal client, this means **all consumers sharing that
  internal client** are blocked, not just the one delivering to the slow
  subscriber. JetStream uses a shared internal client (`c.kind == JETSTREAM`)
  for publishing, so stalling it has broader impact than stalling a single
  `CLIENT`.

**Alternative — per-delivery-subject flow control:**

Rather than blocking the goroutine, the JetStream consumer could use its own
native flow control mechanism. JetStream already has `FlowControl` and
`MaxAckPending` settings that stop the *consumer's delivery loop* when the
subscriber is behind. These operate at the correct granularity — per consumer,
not per goroutine.

However, the problem is that these are **opt-in consumer configuration
settings**. A push consumer created *without* FlowControl enabled has no
JetStream-level backpressure, and the stall gate is the only mechanism
preventing buffer overflows.

#### 3.3 JetStream Already Has Flow Control — Why Do We Need the Stall Gate Too?

JetStream's `FlowControl` (`consumer.go:5497`) works by:
1. Periodically sending a flow control message to the subscriber
2. Pausing delivery (`o.pbytes > o.maxpb`) until the client responds
3. Ramping up `maxpb` from `pblimit/16` to `pblimit` (32MB default) as the
   client keeps up

This is a higher-level, consumer-aware mechanism. But it has limitations:
- It requires explicit opt-in via consumer config (`FlowControl: true`)
- It operates at consumer granularity, not connection granularity
- Without it, a push consumer will happily deliver at full speed regardless of
  subscriber health

The stall gate operates at the **connection layer** and catches *all* overrun
scenarios, regardless of JetStream configuration. The two mechanisms are
complementary:

| Mechanism | Layer | Scope | Opt-in? | Granularity |
|---|---|---|---|---|
| Stall gate | Connection | All producers → one subscriber | Automatic | Per connection |
| JS FlowControl | Consumer | One consumer → one subscriber | Yes | Per consumer |
| MaxAckPending | Consumer | Limits unacked messages | Yes (default 1000) | Per consumer |
| Slow consumer disconnect | Connection | Kills the connection | Automatic | Per connection |

#### 3.4 The Stall Duration Constants Feel Arbitrary

The stall durations (2ms, 5ms, 10ms budget) are hardcoded constants with no
configuration knobs beyond the all-or-nothing `NoFastProducerStall` option.

- **2ms** and **5ms** are likely tuned for typical hardware but may be too
  short for high-latency networks or too long for low-latency in-memory
  workloads.
- The **10ms readLoop budget** means under sustained overrun, a producer spends
  at most ~10ms stalled per read batch, then delivers freely. This limits
  effectiveness: if the producer processes a large batch, it stalls briefly then
  floods again before the next readLoop iteration.

**Alternative — adaptive stall duration:**

The stall duration could be proportional to the buffer fill level or based on
observed drain rate. A buffer at 95% capacity warrants longer stalls than one at
76%.

#### 3.5 No Differentiation Between "Many Fast Producers" and "One Overwhelming Producer"

The stall gate is a single channel on the subscriber. When closed, it unblocks
*all* waiting producers simultaneously. This creates a thundering-herd effect:
if 100 producers are stalled, all 100 resume at once and can immediately refill
the buffer, creating the gate again.

**Alternative — token-bucket or semaphore-based approach:**

Instead of a binary gate, use a semaphore or token bucket that admits N
producers at a time after a stall is released. This would smooth out the burst.

#### 3.6 Stall Gate Creation and Release Thresholds Are the Same (75%)

The gate is created when `pb > mp*3/4` and released when `pb < mp*3/4`. This
means the gate can oscillate rapidly — created, released after a small drain,
created again immediately. This is essentially a busy-wait pattern wrapped in a
channel.

**Alternative — hysteresis:**

Use different thresholds for creation and release (e.g., create at 75%, release
at 50%). This would prevent rapid oscillation and give the buffer meaningful
breathing room before producers resume.

---

## 4. Alternative Approaches We Could Have Taken

### 4.1 Make JetStream Consumer Respect Subscriber Backpressure Natively

Instead of extending the connection-layer stall gate to JETSTREAM clients, we
could have made the JetStream consumer's delivery loop (`loopAndGatherMsgs`)
check the subscriber's buffer health directly:

```go
// In loopAndGatherMsgs, before delivering:
if sub.client != nil {
    sub.client.mu.Lock()
    overrun := sub.client.out.pb > sub.client.out.mp/4*3
    sub.client.mu.Unlock()
    if overrun {
        goto waitForMsgs  // pause delivery at consumer level
    }
}
```

**Pros:**
- Operates at the correct granularity (per consumer, not per internal client)
- Doesn't block the JetStream internal client's goroutine
- Integrates with existing consumer pause/resume machinery

**Cons:**
- More invasive change
- Requires plumbing subscriber buffer state into the consumer layer
- Only covers JetStream — doesn't help with other internal producers

### 4.2 Enable FlowControl by Default for Push Consumers

The simplest "JetStream-native" fix would be to enable `FlowControl` by default
for all push consumers. This would ensure JetStream's own backpressure handles
slow subscribers without relying on the connection-layer stall gate.

**Pros:**
- Uses existing, well-tested infrastructure
- Per-consumer granularity
- No goroutine blocking

**Cons:**
- Breaking change for existing consumer configs
- Adds overhead (flow control messages) even when not needed
- Doesn't help non-JetStream scenarios

### 4.3 Rate-Limit the Internal Client's Delivery, Not Block It

Instead of blocking the JetStream internal client in `stalledWait()`, implement
a delivery rate limiter that slows down message delivery without fully blocking
the goroutine:

```go
if c.kind == JETSTREAM && client.out.stc != nil {
    // Don't block; instead signal the consumer to slow down
    if sub.consumer != nil {
        sub.consumer.signalSlowSubscriber()
    }
    return false // skip this delivery, let consumer retry later
}
```

**Pros:**
- Non-blocking
- Consumer can intelligently decide how to back off

**Cons:**
- Requires refactoring the delivery path to support "retry later" semantics
- More complex, harder to reason about correctness

---

## 5. Assessment of Our Fix

### Why the Stall Gate Extension Is the Right Fix *For Now*

1. **Minimal blast radius**: Adding `|| c.kind == JETSTREAM` is a one-line
   change that uses an existing, proven mechanism.

2. **Correctness**: The stall gate already handles all the edge cases — timeouts,
   connection closure, budget limits. Extending it to JETSTREAM inherits all of
   these guarantees.

3. **The budget protects against goroutine starvation**: The 10ms-per-readLoop
   budget in `stalledWait()` means the JetStream internal client won't be
   blocked indefinitely, limiting the blast radius to other consumers sharing
   that internal client.

4. **Consistency**: It's the same mechanism used for regular clients. Treating
   JetStream differently *was* the bug.

### What Could Be Improved Longer-Term

1. **Replace kind-based allowlist with a trait/flag** to prevent future omissions.

2. **Add hysteresis** to the stall gate creation/release thresholds.

3. **Consider making FlowControl the default** for push consumers, so the
   JetStream layer handles backpressure before it reaches the connection layer.

4. **Add metrics/logging** that distinguish JetStream stalls from client stalls
   to aid debugging. Currently, the debug message says "Timed out of fast
   producer stall" for both, with no indication of which kind of producer
   stalled.

5. **Evaluate whether SYSTEM and ACCOUNT clients** should also respect the stall
   gate, or whether the current exemption is intentional and correct.
