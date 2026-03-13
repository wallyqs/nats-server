# NATS Config Syntax Quirks

Known syntax behaviors in the NATS config format that may trip up workers.

---

## Array Element Separators

NATS config arrays require elements to be separated by **newlines** or **commas without spaces after the comma** when writing inline. The parser's lexer treats certain whitespace patterns differently than typical JSON.

**Works:**
```
routes = [
  "nats://host1:4222"
  "nats://host2:4222"
]
```

```
routes = ["nats://host1:4222","nats://host2:4222"]
```

**Also works (newline-separated integers):**
```
ports = [
  4222
  4223
  4224
]
```

**May cause issues in test configs:** Writing `[4222, 4223, 4224]` (with spaces after commas) works for simple values but be aware that complex values may parse differently. When writing test configs, prefer newline-separated format for reliability.

**Source:** Discovered during unmarshal-complex-types implementation — multiple test edits were required to fix array syntax in test inputs.

---

## Duration Strings Must Be Quoted

When marshaling `time.Duration` values to NATS config text, the duration string (e.g., `5s`, `2m30s`, `100ms`) **must be quoted**. The NATS lexer interprets unquoted values with a numeric prefix followed by a letter suffix as size-suffixed integers (e.g., unquoted `2m` would be parsed as 2,000,000 — the `m` suffix means "million").

**Correct:** `ping_interval: "5s"`
**Wrong:** `ping_interval: 5s` (lexer interprets `5s` as unknown suffix, not a duration)

This applies to all Go-style duration strings emitted by `Marshal()`.

**Source:** Discovered during marshal-core implementation — initial implementation emitted unquoted durations which failed round-trip tests.
