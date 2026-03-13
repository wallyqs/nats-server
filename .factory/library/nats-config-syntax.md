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
