# Review: PR #7919 — `conf: support optional include? directive`

**Author:** olliehcrook
**Branch:** `olliehcrook/nats-server:optional-includes` → `main`
**Resolves:** #5297

---

## Summary

This PR adds an `include?` directive to the NATS server configuration parser.
When a file referenced by `include?` does not exist, it is silently ignored.
If the file exists but contains invalid configuration, it still produces an error.
This is a useful feature for deployments where some config fragments are optional.

---

## Review Findings

### 1. Significant code duplication in `conf/lex.go` (Major)

The PR adds ~80 lines of new lexer functions that are near-identical copies of existing `include` functions:

| Existing function | New duplicate |
|---|---|
| `lexIncludeStart` | `lexIncludeOptionalStart` |
| `lexInclude` | `lexIncludeOptional` |
| `lexIncludeQuotedString` | `lexIncludeOptionalQuotedString` |
| `lexIncludeDubQuotedString` | `lexIncludeOptionalDubQuotedString` |
| `lexIncludeString` | `lexIncludeOptionalString` |

The **only** difference between each pair is the `itemType` emitted (`itemInclude` vs `itemIncludeOptional`). This could be reduced to a single set of parameterized functions. For example, the lexer could pass the item type to emit via a closure or a field on the lexer struct:

```go
func (lx *lexer) lexIncludeStartWith(itemType itemType) stateFn {
    return func(lx *lexer) stateFn {
        r := lx.next()
        if isWhitespace(r) {
            return lexSkip(lx, lx.lexIncludeStartWith(itemType))
        }
        lx.backup()
        return lx.lexIncludeWith(itemType)
    }
}
```

Or simpler: store the pending include type on the `lexer` struct and have the existing functions check it when emitting. This would eliminate ~70 lines of duplicated code.

### 2. Code duplication in `conf/parse.go` (Moderate)

The `itemIncludeOptional` case in `processItem` is a copy of the `itemInclude` case with two additions:
1. The `errors.Is(err, os.ErrNotExist)` check
2. A `break` on missing file

These two cases could share code via a helper function or by combining them:

```go
case itemInclude, itemIncludeOptional:
    // ... shared parsing logic ...
    if err != nil {
        if it.typ == itemIncludeOptional && errors.Is(err, os.ErrNotExist) {
            break
        }
        return fmt.Errorf("error parsing include file '%s', %v", it.val, err)
    }
    // ... shared iteration logic ...
```

This would eliminate ~20 lines of duplication.

### 3. Error wrapping change — `%v` → `%w` in `ParseFile` (Minor, positive)

The change from `%v` to `%w` at `parse.go:93` is necessary for `errors.Is(err, os.ErrNotExist)` to work through the wrapped error in `ParseFile`. This is correct and a good improvement. However, it's worth noting this is a subtle behavioral change for **all** callers of `ParseFile` — any code doing string-based error matching (unlikely but possible) could be affected. This should be fine in practice, but it's worth calling out.

Note: `ParseFileWithChecks` already returns raw `os.ReadFile` errors (line 106: `return nil, err`), so `errors.Is` already works there without any change. The asymmetry in error handling between the two functions is pre-existing.

### 4. Test coverage (Positive)

The tests are well-structured and cover the three important scenarios:

- **`TestOptionalIncludeMissingFile`**: Verifies missing files are silently ignored (both normal and pedantic mode)
- **`TestOptionalIncludeInvalidFile`**: Verifies malformed config files still produce errors
- **`TestOptionalIncludeNestedRequiredIncludeMissing`**: Verifies that a required `include` inside an optional file still errors — this is an important edge case

The lexer tests (`TestOptionalInclude`, `TestMapOptionalInclude`) cover the three string forms (single-quoted, double-quoted, unquoted) and map-scoped includes.

### 5. Missing test: optional include with existing file (Minor)

There's no test verifying that `include?` actually works when the file **does** exist and contains valid configuration. While this is implicitly covered by the existing `include` tests (since they share the same parsing path), an explicit test would be valuable for completeness and regression protection.

### 6. Keyword parsing correctness (Positive)

The `keyCheckKeyword` function at `lex.go:480` uses `strings.ToLower(lx.input[lx.start:lx.pos])` to match keywords. Since `?` is not a whitespace or key separator, it's included in the key token, so `"include?"` correctly matches the new case. The space after `include?` is required (handled by `lexIncludeOptionalStart`), which is the expected UX: `include? path.conf`.

---

## Verdict

The feature is correct and the tests are solid. The main concern is the substantial code duplication in the lexer (~80 lines of near-identical functions). I'd recommend the author refactor to share code between `include` and `include?` paths before merging. The parser-side duplication is less critical but would also benefit from consolidation.

**Recommendation:** Request changes — consolidate duplicated lexer/parser code, add a test for `include?` with an existing valid file.
