# Review: PR #7959 — [FIXED] ParseFileWithChecksDigest deletes referenced config entries

**Author:** Daniele Sciascia (@sciascid)
**Files changed:** `conf/parse.go`, `conf/parse_test.go`

## Summary

This PR fixes a bug where `ParseFileWithChecksDigest` was deleting config entries that happened to be referenced as variables. For example, given:

```
port = 4222
monitor_port = $port
```

The `port` key would be deleted from the returned config map because it was marked as a "used variable" during resolution of `$port`. This is clearly wrong — `port` is a real config key, not a disposable helper variable.

## The Bug (Root Cause)

In `processItem()` (`conf/parse.go:408-414`), when resolving a `$variable` reference, the original token is marked with `usedVariable = true`:

```go
case *token:
    tk.usedVariable = true  // marks the SOURCE token
    p.setValue(&token{it, tk.Value(), false, fp})
```

The now-removed `cleanupUsedEnvVars()` would then iterate the parsed map and **delete** any entry whose token had `usedVariable == true`. This meant any config key referenced by another key via `$` was removed from the final result — a data-loss bug.

## The Fix

The PR:
1. **Removes `cleanupUsedEnvVars()`** entirely — this function was the direct cause of the bug.
2. **Extracts `configDigest()`** as a clean helper for SHA-256 digest computation.
3. **Simplifies `ParseFileWithChecksDigest()`** to delegate to `ParseFileWithChecks()` (avoiding code duplication of `os.ReadFile` + `parse()`) and then compute the digest without any entry cleanup.

## Correctness Assessment

**The fix is correct.** The only caller of `ParseFileWithChecksDigest` is `server/opts.go:980` (`Options.ProcessConfigFile`), which uses the returned map for full config processing. Deleting entries from this map was silently dropping real configuration.

## Trade-offs

**Digest stability:** Configs using variable substitution will now produce different digests than before the fix. On upgrade, this will trigger a one-time config reload log message, which is harmless. The digest is only used for informational logging (in `reload.go:1914` and `server.go:2330`), not for correctness-critical decisions.

**Loss of normalization:** Previously, two semantically equivalent configs (one using `$var` substitution, one with inline values) would produce the same digest (after the variables were stripped). Now they will produce different digests. This is the correct behavior — they ARE different config files and should be distinguishable.

## Test Coverage

- **New test:** `TestParseFileWithChecksDigestPreservesConfigKeyUsedAsVariable` directly validates the bug fix scenario (`port = 4222; monitor_port = $port` → `port` key preserved).
- **Updated digests:** Two existing `TestParseDigest` test cases have updated expected digests to reflect the corrected (non-stripping) behavior.
- **All existing tests pass** — I ran both `./conf/...` and the relevant server tests (`TestConfigFile*`, `TestConfigReload*`, `TestReload`, `TestRoutes*`) and they all pass.

## Minor Observations

1. The `configDigest` helper is currently only used by `ParseFileWithChecksDigest`, but extracting it is still good practice for readability and potential reuse.
2. The `usedVariable` field on `token` is still set during variable resolution but no longer has any consumer. It could be cleaned up in a follow-up, but leaving it is fine — it's a zero-cost boolean on a struct that already exists.

## Verdict

**LGTM.** This is a clean, focused bug fix with good test coverage. The change is minimal and correct. The only side effect (digest values changing for variable-using configs) is harmless and expected.
