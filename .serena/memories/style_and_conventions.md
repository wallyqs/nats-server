# Style and conventions
- Go formatting: goimports formatter via GolangCI-Lint (`.golangci.yml`), so run `gofmt`/`goimports` on touched files.
- Linting: GolangCI enabled linters include forbidigo (no fmt.Print*), govet, ineffassign, misspell, staticcheck, unused. Some exclusions for generated/docs directories. Lint expects no new deps unless necessary.
- Testing culture: new features require tests; bug fixes should add regression tests. CI scripts run many targeted `go test` batches with tags (`skip_*`), so match existing naming/tag patterns (e.g., `TestNoRace`, `TestJetStream*`).
- Commits/PRs: use Signed-off-by trailer (`git commit -s`); keep PRs rebased on main, concise commit history.
- Versioning: server version injected via ldflags in some tests (see scripts). Avoid fmt.Print logging; use server logging facilities.
- Licensing/comment headers: files include Apache 2 headers; retain when creating new Go files.