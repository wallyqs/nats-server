---
name: go-library-worker
description: Implements Go library features with TDD, targeting the conf/v2 parser package and server integration
---

# Go Library Worker

NOTE: Startup and cleanup are handled by `worker-base`. This skill defines the WORK PROCEDURE.

## When to Use This Skill

Use for features that implement Go code in `conf/v2/` or `server/`:
- Lexer, parser, AST types in conf/v2/
- Marshal/Unmarshal functions in conf/v2/
- Server integration (ProcessConfigV2, struct tags on Options)
- Cross-area integration tests

## Work Procedure

### 1. Understand the Feature

Read the feature description, preconditions, expectedBehavior, and verificationSteps carefully. Read `AGENTS.md` for coding conventions and boundaries. If the feature references v1 behavior, read the relevant v1 source files (`conf/lex.go`, `conf/parse.go`, `conf/lex_test.go`, `conf/parse_test.go`).

### 2. Read Existing v2 Code

Before writing anything, read all existing files in `conf/v2/` to understand:
- What types and functions already exist
- What conventions have been established
- What you can build on vs what you need to create

### 3. Write Tests First (TDD - MANDATORY)

Write failing tests BEFORE implementation. Tests go in `conf/v2/*_test.go` files.

For each expected behavior in the feature description:
1. Write a test function that exercises the behavior
2. Run `go test ./conf/v2/... -count=1 -run TestXxx` to confirm it fails (red)
3. Record the failure in your notes

Test naming convention: `TestXxx` where Xxx describes the behavior being tested.

For backwards-compatibility features, write cross-validation tests that:
- Parse the same input with both `conf.Parse()` (v1) and `v2.Parse()` (v2)
- Compare results via `reflect.DeepEqual`
- Use test inputs from v1's test files

For round-trip features, write tests that:
- Start with known input
- Parse -> transform -> emit -> re-parse
- Compare with original

### 4. Implement to Pass Tests

Write the minimum code needed to make tests pass (green). Follow these rules:
- All exported types/functions need Go doc comments
- Match v1 naming conventions where applicable
- No external dependencies (stdlib only)
- Never modify files outside `conf/v2/`

### 5. Run Full Verification

After implementation, run ALL verification:

```bash
# v2 tests pass
go test ./conf/v2/... -count=1 -v

# Go vet clean
go vet ./conf/v2/...

# v1 baseline still passes
go test ./conf/... -count=1

# Build succeeds
go build ./conf/v2/...

# If server/ files were modified, run server tests:
# go test ./server/... -count=1 -timeout 600s -p 1
# NOTE: Server tests are slow (~5-10 min). Run with -run flag to target specific tests when iterating.
# For full suite, use the timeout and -p 1 flags to avoid parallel resource contention.
```

ALL commands must succeed for the packages you modified. If any fail, fix the issue before proceeding.

### 6. Manual Verification

For each key behavior, manually verify by running specific tests and inspecting output:
- Run targeted tests with `-v` flag to see detailed output
- For backwards-compat features: verify v1 vs v2 output comparison explicitly
- For marshal features: inspect the actual marshaled text output
- For error cases: verify error messages contain expected content

Each manual check = one `interactiveChecks` entry in the handoff.

### 7. Review Your Code

Before completing, review your own code:
- No debug prints or TODO comments left behind
- All test cases cover the expectedBehavior items
- Error messages include position info where applicable
- Code follows v1 conventions (check `conf/lex.go` and `conf/parse.go` for style)

## Example Handoff

```json
{
  "salientSummary": "Implemented the v2 lexer covering all 15 token types. Wrote 45 test cases in lex_test.go (TDD: all started red, now green). Cross-validated against v1 lexer output for 12 representative inputs. go test, go vet, and v1 baseline all pass.",
  "whatWasImplemented": "Full v2 lexer in conf/v2/lex.go: channel-based state machine emitting all v1 token types plus comment tokens. Handles strings (quoted/unquoted/block), integers with size suffixes, floats, bools, datetime, arrays, maps, variables, includes, IP addresses, bcrypt special case, JSON compatibility, escape sequences, CRLF support.",
  "whatWasLeftUndone": "",
  "verification": {
    "commandsRun": [
      {"command": "go test ./conf/v2/... -count=1 -v", "exitCode": 0, "observation": "45 tests passed, 0 failed"},
      {"command": "go vet ./conf/v2/...", "exitCode": 0, "observation": "no issues found"},
      {"command": "go test ./conf/... -count=1", "exitCode": 0, "observation": "v1 baseline passes (12 tests)"},
      {"command": "go build ./conf/v2/...", "exitCode": 0, "observation": "builds cleanly"}
    ],
    "interactiveChecks": [
      {"action": "Ran TestScalarTokenTypes with -v, inspected each token type output", "observed": "All 15 scalar types lexed correctly: string, int, float, bool, datetime, variable, include match v1 output exactly"},
      {"action": "Ran cross-validation test comparing v1 and v2 lex output for cluster config", "observed": "Token streams identical: 47 tokens, all types and values match"},
      {"action": "Verified CRLF handling: same input with \\n vs \\r\\n produces identical tokens", "observed": "Token values and line numbers match for both line ending styles"}
    ]
  },
  "tests": {
    "added": [
      {"file": "conf/v2/lex_test.go", "cases": [
        {"name": "TestScalarTokenTypes", "verifies": "All scalar value types lexed correctly"},
        {"name": "TestKeySeparators", "verifies": "Equals, colon, whitespace key separators"},
        {"name": "TestEscapeSequences", "verifies": "All escape sequences in quoted and unquoted strings"},
        {"name": "TestCommentLexing", "verifies": "Hash and slash comments emitted as tokens"},
        {"name": "TestCRLFSupport", "verifies": "CRLF treated same as LF"}
      ]}
    ]
  },
  "discoveredIssues": []
}
```

## When to Return to Orchestrator

- Feature depends on AST types or parser functions that don't exist yet
- v1 behavior is ambiguous or contradictory for a specific edge case
- Existing v2 code has bugs that block this feature
- Cannot achieve backwards compatibility without modifying v1 (not allowed)
- Test infrastructure issues (go test fails to run, module issues)
