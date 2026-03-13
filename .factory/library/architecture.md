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
