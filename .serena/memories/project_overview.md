# NATS Server overview
- Purpose: Go implementation of the NATS messaging server (CNCF), exposing publish/subscribe, request/reply, JetStream streaming and clustering.
- Entry: `main.go` wires CLI flags to `server.ConfigureOptions` and `server.NewServer`; binary name `nats-server`.
- Language: Go (module `github.com/nats-io/nats-server/v2`, go 1.25).
- Key dirs: `server/` core runtime (JetStream, clustering, auth, stores), `conf/` sample configs, `test/` integration/system tests, `logger/` logging, `internal/` helper packages (e.g., LDAP), `util/` helpers, `scripts/` CI/test utilities, `docker/` deployment assets.
- Notable tooling: `go generate` directive in `main.go` for `server/errors_gen.go`. GolangCI config at `.golangci.yml` with goimports formatting.
- Docs: top-level README (project links); ADRs moved to https://github.com/nats-io/nats-architecture-and-design/ (see doc/README.md).
- Licensing: Apache-2.0 (LICENSE).