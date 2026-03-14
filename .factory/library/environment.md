# Environment

Environment variables, external dependencies, and setup notes.

**What belongs here:** Required env vars, external API keys/services, dependency quirks, platform-specific notes.
**What does NOT belong here:** Service ports/commands (use `.factory/services.yaml`).

---

## Go Version
- Go 1.26.0 (darwin/arm64)
- Module: `github.com/nats-io/nats-server/v2`

## Dependencies
- stdlib only — no external dependencies for conf/v2

## NATS_CONFIG_V2 Environment Variable
- When set (non-empty), the server uses `ProcessConfigV2` (conf/v2 parser) instead of `ProcessConfigFile` (v1 parser)
- Affects both initial config loading (`ConfigureOptions`) and config reload (`Reload`)
- Defined as `envConfigV2` constant in `server/opts_v2.go`
- Checked via `useConfigV2()` helper at each call site

## Platform
- macOS (darwin/arm64), 8 cores, 16 GB RAM
