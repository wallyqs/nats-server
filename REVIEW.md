# Code Review: Cron Schedules Implementation

## Overview

This change adds cron schedule support to the NATS server's message scheduling
system, building on the existing `@at` and `@every` patterns. It introduces a
new `server/cron.go` file (adapted from `robfig/cron`), extends
`parseMsgSchedule` with timezone support via a new `tz` parameter, and adds the
`Nats-Schedule-Time-Zone` header.

**Files changed:** 4 (1 new, 3 modified)
- `server/cron.go` — new, ~327 lines
- `server/scheduler.go` — modified `parseMsgSchedule`, `getScheduledMessages`
- `server/stream.go` — new constant, refactored `getMessageSchedule`/`nextMessageSchedule`
- `server/jetstream_test.go` — restructured into subtests, added cron and timezone tests

---

## Strengths

1. **Clean adaptation of robfig/cron.** Only the necessary parsing logic was
   extracted, avoiding a full dependency. The MIT license attribution is properly
   included alongside the Apache 2.0 header.

2. **Good use of bitwise scheduling.** The bit-field approach for representing
   valid times per field is efficient and well-proven from the upstream library.

3. **Defensive "skip ahead" logic.** Both `@every` and cron patterns correctly
   detect when a schedule would have fired in the past (e.g. after a server
   restart) and advance to the next future occurrence, preventing a burst of
   catch-up firings.

4. **Refactoring of `getMessageSchedule`.** Eliminating the duplicated
   `parseMsgSchedule` call by delegating to `nextMessageSchedule` is a clean
   improvement.

5. **Test restructuring.** Moving to subtests (`t.Run`) improves readability and
   allows running individual schedule type tests in isolation. The table-driven
   tests for predefined patterns (`@yearly`, `@monthly`, etc.) are well
   structured.

---

## Issues and Suggestions

### High

#### 1. Default timezone behavior when `tz` is empty

In `parseCron`, when `tz` is `_EMPTY_` (`""`), `time.LoadLocation("")` returns
the `time.UTC` timezone. This works but is implicit. If the intent is that an
empty timezone should default to UTC, it would be clearer to document this
explicitly in `parseCron` or to pass `"UTC"` explicitly from `parseMsgSchedule`
when `tz` is empty. This avoids relying on an undocumented behavior of
`time.LoadLocation`.

```go
// Suggestion in parseCron:
if tz == "" {
    tz = "UTC"
}
loc, err := time.LoadLocation(tz)
```

#### 2. The `Nats-Schedule-Time-Zone` header is not stripped on delivery

In `getScheduledMessages`, outbound headers are cleaned up by removing
`Nats-Schedule-*` prefixed headers via `removeHeaderIfPrefixPresent(hdr,
"Nats-Schedule-")`. The new `Nats-Schedule-Time-Zone` header would be caught by
this prefix strip, which is good. However, it's worth verifying this is
intentional — if consumers ever need to know the original timezone, it would be
lost. This is probably fine since the scheduled message's purpose is to produce
a clean output message.

#### 3. Timezone rejected for `@at` and `@every` without clear user feedback

When `tz` is non-empty for `@at` or `@every` patterns, the function returns
`false` for `ok` with no specific error. At the call sites
(`getScheduledMessages`, `nextMessageSchedule`), this silently removes the
schedule or treats it as invalid. Consider whether a log warning would help
operators diagnose misconfigured schedules, since "my schedule silently
disappeared" can be hard to debug.

### Medium

#### 4. `parseCron` accepts `?` as equivalent to `*` in all fields

In `getRange`, `?` is treated identically to `*` for every field. Standard cron
implementations typically only allow `?` in the day-of-month and day-of-week
fields (meaning "no specific value"). Accepting `?` everywhere is more
permissive than standard cron, which could surprise users migrating from other
cron systems. Consider validating that `?` is only used in fields 3 (DOM) and 5
(DOW).

#### 5. `dayMatches` semantics for DOM/DOW interaction

The `dayMatches` function uses OR when both day-of-month and day-of-week are
explicitly specified (neither has `starBit`), and AND when either has `starBit`.
This matches the original `robfig/cron` behavior and Vixie cron semantics, but
it's the opposite of what many users expect (Quartz cron, for example, uses AND
for explicit fields). This is a known source of confusion and worth documenting.

#### 6. No validation of cron field count for predefined patterns

The predefined patterns (`@yearly`, `@monthly`, etc.) are correctly expanded to
6-field patterns before being passed to `parseCron`. Good. However, if a user
passes something like `@yearly extra_stuff`, it falls through to `parseCron`
which will reject it due to field count. Consider whether `@yearly` etc. should
be matched more strictly (e.g. exact match only, which the current `switch`
already does) — actually, this is already correct since `switch` does exact
matching. No action needed.

#### 7. Year limit of 5 years may not be sufficient for some cron patterns

The `yearLimit` of `next.Year() + 5` in `parseCron` means patterns like
`0 0 0 29 2 *` (Feb 29 only) will work fine, but very sparse patterns could
theoretically fail. Five years seems reasonable for a messaging system, but it
might be worth documenting this limit.

### Low

#### 8. `strings.FieldsFuncSeq` requires Go 1.24+

The use of `strings.FieldsFuncSeq` in `getField` requires Go 1.24 or later.
The `go.mod` specifies `go 1.25.0` so this is fine for the project, but worth
noting if this code is ever extracted or backported.

#### 9. The `truncated` flag could use a brief comment

The `truncated` flag in `parseCron` controls whether to zero out sub-fields
when a higher-order field is incremented. While the comments in the "General
approach" block cover the concept, a one-line comment at each `truncated` check
explaining "reset lower-order fields when incrementing this field" would aid
future readers unfamiliar with the robfig/cron algorithm.

#### 10. Test uses `time.Now()` which can be flaky

The cron subtest does:
```go
now := time.Now().UTC().Round(time.Second)
sts, repeat, ok := parseMsgSchedule("* * * * * *", _EMPTY_, now.UnixNano())
require_Equal(t, now.Add(time.Second), sts)
```
If the test runs near a second boundary, `time.Now()` and the subsequent
assertion could cross a second boundary, leading to rare flaky failures. The
predefined-pattern tests avoid this by using a fixed future date
(`time.Now().Year()+2`), which is better. Consider using a fixed timestamp for
the `* * * * * *` test as well.

#### 11. `goto WRAP` pattern

The `goto WRAP` pattern is inherited from `robfig/cron` and works correctly.
It's arguably clearer than deeply nested loops for this algorithm, so no change
needed — just noting that this is an intentional style choice from the upstream
library.

---

## Summary

This is a well-executed implementation that cleanly extends the existing
scheduling system with cron support. The main areas to consider are:

- Making the empty-timezone-defaults-to-UTC behavior explicit
- Ensuring operators get feedback when timezone is incorrectly used with `@at`/`@every`
- Considering stricter `?` wildcard validation for cron field positions
- Using fixed timestamps in tests to avoid flakiness

Overall the code is clean, well-structured, and the test coverage is good for
the new functionality.
