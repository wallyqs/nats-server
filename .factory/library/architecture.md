# Architecture

Architectural decisions and patterns for the conf/v2 parser.

**What belongs here:** Design decisions, AST structure choices, API patterns, performance notes.

---

## v1 Architecture (Reference)

- Channel-based lexer (Rob Pike pattern): lexer emits tokens via `chan item`
- State functions: each state returns the next state function
- State function stack: push/pop for nested contexts (arrays, maps)
- Parser consumes tokens, builds `map[string]any` directly
- Pedantic mode wraps values in `*token` structs with position info
- Variable resolution walks context stack (innermost to outermost), then env vars

## v2 Architecture (Target)

- AST-first: parser builds AST nodes, not `map[string]any` directly
- AST nodes carry position info, comments, separator style
- Backwards-compatible API converts AST → `map[string]any`
- Marshal converts structs → NATS config text via reflection
- Unmarshal converts parsed data → structs via reflection + struct tags
- AST emission preserves comments, formatting, variables, includes

## Server Integration (ProcessConfigV2)

- **configV2Wrapper pattern**: `server/opts_v2.go` defines `configV2Wrapper` that embeds `*Options` with overlay fields for complex types (Listen, HTTP, HTTPS, JetStream, TLS, Authorization, Accounts). The v2 unmarshal engine populates simple fields directly into Options via embedded struct promotion, while complex/polymorphic fields are captured by overlay fields with custom Unmarshalers, then mapped to Options in a post-processing step.
- **Custom Unmarshalers**: `server/opts_unmarshal.go` implements `UnmarshalConfig(v any) error` on 8 types: `DurationValue`, `ListenValue`, `JetStreamValue`, `OCSPConfig`, `OCSPResponseCacheConfig`, `TLSConfigOpts`, `AuthorizationConfig`, `AccountsConfig`. These handle polymorphic inputs (bool/string/map) and reuse existing server helper functions.
- **Struct tags**: `server/opts.go` has `conf:"name|alias"` tags on Options and all 17+ nested structs. Fields tagged `conf:"-"` are internal/computed and skipped by Unmarshal.

## Known Pre-existing Issues

- `go vet ./server/...` reports 3 warnings in `server/stream.go` about `MarshalJSON() []byte` methods that should have signature `MarshalJSON() ([]byte, error)`. These are pre-existing in the upstream codebase and not introduced by the v2 parser work. They are safe to ignore for v2-related validation.
