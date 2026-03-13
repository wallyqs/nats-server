# User Testing

Testing surface, validation approach, and resource cost classification.

---

## Validation Surface

This is a pure Go library with no interactive UI, CLI, or API surface.

**Primary surface:** `go test ./conf/v2/... -v` output analysis
**Tool:** Direct `go test` execution and output inspection
**Assertions validated by:** Running specific test cases that exercise the public API contracts

## Validation Concurrency

- `go test` is lightweight (~300 MB peak per invocation)
- Machine: 8 cores, 16 GB RAM, ~6 GB baseline usage
- Usable headroom: ~10 GB * 0.7 = 7 GB
- Each validator uses ~300 MB
- **Max concurrent validators: 5**

## Flow Validator Guidance: go-test

### Surface
The user surface is `go test` — validators run specific test cases by name and inspect output.

### Isolation
Each validator runs `go test` with a `-run` filter targeting specific test names. Since tests are read-only (no shared mutable state, no file writes, no network), all validators can run concurrently without interference.

### How to validate an assertion
1. Identify which test(s) cover the assertion (test names listed in assignment)
2. Run: `cd /Users/waldemarquevedo/repos/nats-dev/src/github.com/nats-io/nats-server-security && go test ./conf/v2/... -count=1 -v -run "<TestNamePattern>" 2>&1`
3. Inspect output for `--- PASS` or `--- FAIL`
4. For each assertion, verify the test exercises the specific behavior described in the assertion
5. Optionally read test source to confirm test coverage matches assertion requirements

### Test file locations
- Lexer tests: `conf/v2/lex_test.go`
- AST tests: `conf/v2/ast_test.go`
- Parser tests: `conf/v2/parse_test.go`
- Backwards compatibility tests: `conf/v2/compat_test.go`

### Repo root
`/Users/waldemarquevedo/repos/nats-dev/src/github.com/nats-io/nats-server-security`

### Constraints
- Do NOT modify any source files
- Do NOT modify test files
- Only run `go test` commands for inspection
- Each validator operates independently — no shared temp files or ports

## Flow Validator Guidance: go-test-server

### Surface
The user surface is `go test ./server/...` — validators run specific server test cases by name and inspect output. The server tests exercise ProcessConfigV2 and verify equivalence with ProcessConfigFile (v1).

### Isolation
Each validator runs `go test` with a `-run` filter targeting specific test names. Server tests may create temp files for config but clean up after themselves. All validators can run concurrently.

### How to validate an assertion
1. Identify which test(s) cover the assertion (test names listed in assignment)
2. Run: `cd /Users/waldemarquevedo/repos/nats-dev/src/github.com/nats-io/nats-server-security && go test ./server/... -count=1 -v -timeout 300s -run "<TestNamePattern>" 2>&1`
3. Inspect output for `--- PASS` or `--- FAIL`
4. For each assertion, verify the test exercises the specific behavior described in the assertion
5. Optionally read test source to confirm test coverage matches assertion requirements

### Test file locations
- Server v2 config tests: `server/opts_v2_test.go`
- Server custom unmarshalers: `server/opts_unmarshal.go`
- Server v2 config processor: `server/opts_v2.go`
- Server options struct with conf tags: `server/opts.go`

### Repo root
`/Users/waldemarquevedo/repos/nats-dev/src/github.com/nats-io/nats-server-security`

### Constraints
- Do NOT modify any source files
- Do NOT modify test files
- Only run `go test` commands for inspection
- Each validator operates independently
- Use `-timeout 300s` for server tests (they can be slower)
