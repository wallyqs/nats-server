# Code Review: (2.14) Feature Flags

**Changeset:** `maurice/feature-flags` branch
**Author:** MauriceVanVeen
**Files changed:** 10 (2 new, 8 modified)

---

## Summary

This changeset introduces a feature flag infrastructure to the NATS server following ADR-53. It adds:

- A `feature_flags` configuration block parsed from server config
- A global registry of known flags with defaults (currently one: `js_ack_fc_v2`)
- Merged flag computation (user values override defaults, unknown flags filtered)
- Exposure of feature flags in `ServerInfo` (events) and `Varz` (monitoring)
- Startup logging of configured/unsupported flags

The design is clean and follows existing patterns established by `Tags` and `Metadata`. However, there are several issues ranging from a likely bug to test quality concerns.

---

## Issues

### 1. [Bug] Missing config reload handler for FeatureFlags

**Severity:** High
**Files:** `server/reload.go`

Both `Tags` and `Metadata` have dedicated reload option types and corresponding cases in `diffOptions()`:

```go
// reload.go:359-383
type tagsOption struct { noopOption }
type metadataOption struct { noopOption }

// reload.go:1357-1360
case "tags":
    diffOpts = append(diffOpts, &tagsOption{})
case "metadata":
    diffOpts = append(diffOpts, &metadataOption{})
```

`FeatureFlags` has neither. The `diffOptions` function iterates all exported `Options` fields via reflection (`reload.go:1291`). When it detects a change in `FeatureFlags` but finds no matching case, it hits the default at `reload.go:1765`:

```go
default:
    return nil, fmt.Errorf("config reload not supported for %s: old=%v, new=%v",
        field.Name, oldValue, newValue)
```

This means **any config reload will fail with an error** if feature flags are modified in the config file. The fact that `map[string]bool` was added to `imposeOrder` and `updateVarzConfigReloadableFields` was updated to call `getMergedFeatureFlags()` suggests reload support was intended.

**Fix:** Add a `featureFlagsOption` struct and a `case "featureflags":` in `diffOptions`, following the `tagsOption`/`metadataOption` pattern.

---

### 2. [Bug] Tests mutate global `featureFlags` without cleanup

**Severity:** High
**File:** `server/feature_flags_test.go`

`TestGetFeatureFlag` adds and deletes keys from the package-level `featureFlags` map:

```go
featureFlags["test_flag"] = true
// ...
delete(featureFlags, "test_flag")
```

`TestGetMergedFeatureFlags` **completely replaces** the global:

```go
featureFlags = make(map[string]bool)
featureFlags["test_flag"] = true
featureFlags["test_flag_default"] = true
```

After this test runs, the original `featureFlags` (containing `FeatureFlagJsAckFormatV2: false`) is permanently lost for the rest of the test process. This can cause:

- `TestMonitorVarzFeatureFlags` (in `monitor_test.go`) to fail or produce incorrect results, since it compares against the mutated global
- Any other test relying on the real feature flag defaults to break
- Non-deterministic failures depending on test execution order

**Fix:** Save and restore the original `featureFlags` map in each test:

```go
func TestGetFeatureFlag(t *testing.T) {
    orig := featureFlags
    defer func() { featureFlags = orig }()
    // ...
}
```

---

### 3. [Minor] Incorrect build tag on `feature_flags_test.go`

**Severity:** Low
**File:** `server/feature_flags_test.go:15`

```go
//go:build !skip_js_tests
```

Feature flags are a general server infrastructure feature, not JetStream-specific. While the first flag (`js_ack_fc_v2`) relates to JetStream, the framework itself is generic. This build tag would cause the core feature flag unit tests to be skipped when JetStream tests are excluded, even though the feature flag code itself has no JetStream dependency.

**Suggestion:** Remove the build tag unless there's a specific reason these tests should be skipped alongside JetStream tests.

---

### 4. [Minor] `printFeatureFlags` prints empty "Configured:" line

**Severity:** Low
**File:** `server/feature_flags.go`

If a user configures only unsupported flags (e.g., `feature_flags { unknown: true }`), the function still prints:

```
Feature flags:
Configured:
Unsupported: unknown
```

The "Configured:" line with an empty string is awkward.

**Suggestion:** Either skip the "Configured:" line when empty, or print "none".

---

### 5. [Observation] `getFeatureFlag` is unused in production code

**File:** `server/feature_flags.go`

The `getFeatureFlag(k string) bool` method is defined and tested but never called in production code within this changeset. Only `getMergedFeatureFlags()` is used (in `events.go` and `monitor.go`). Presumably `getFeatureFlag` is intended for future use when code paths need to branch on specific flags (like the `js_ack_fc_v2` behavior).

This is fine if follow-up work is planned, but worth noting for reviewers.

---

### 6. [Observation] `getFeatureFlag` comment about lock is misleading

**File:** `server/feature_flags.go`

The comment says "Options read lock should be held" but callers access Options through `getOpts()` (`server.go:1206`) which acquires/releases `optsMu.RLock` and returns a stable pointer. By the time `getFeatureFlag` executes, no lock is held. The method is safe because `getOpts()` returns an immutable snapshot, but the comment implies the caller should hold a lock, which doesn't match how it's actually called.

**Suggestion:** Clarify the comment, e.g., "Must be called on a stable Options reference (e.g., from getOpts())."

---

### 7. [Observation] Feature flags in `ServerInfo` are not updated on reload

**File:** `server/events.go:521`

```go
tags, metadata, featureFlags := opts.Tags, opts.Metadata, opts.getMergedFeatureFlags()
```

These are captured once before the event dispatch loop and never refreshed. If a config reload changes feature flags, `Varz` will reflect the change (via `updateVarzConfigReloadableFields`), but `ServerInfo` sent in events will retain the old values until server restart.

This is consistent with how `Tags` and `Metadata` behave, so it's by-design. However, given that feature flags control server behavior and operators may want to toggle them at runtime, it may be worth considering whether `ServerInfo` should pick up reloaded values.

---

## Positive Aspects

- The overall design is sound and follows existing codebase patterns well
- The merged-flags approach (as discussed in the review thread with ripienaar) is the right call - operators see the effective state of all flags
- The labeling in `printFeatureFlags` (enabled/opt-out/opt-in/disabled) clearly communicates the relationship between defaults and user overrides
- The `imposeOrder` addition in `reload.go` shows awareness of the reload infrastructure
- Good documentation in the `featureFlags` map variable describing the flag lifecycle

---

## Recommendation

The missing reload handler (#1) should be addressed before merge - it will cause config reloads to fail. The test global mutation (#2) should also be fixed to prevent flaky tests. The remaining items are minor suggestions.
