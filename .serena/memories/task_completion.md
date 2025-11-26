# Before finishing a task
- Format Go code (gofmt/goimports); keep forbidigo rules in mind (avoid fmt.Print*).
- Run relevant tests: start with `go test ./...` or targeted `go test ./server -run=Test<Case>`; for JetStream/clustering use `scripts/runTestsOnTravis.sh` options; for coverage `./scripts/cov.sh`.
- Run lint when possible: `golangci-lint run` (after installing pinned version).
- Update docs/config samples if behavior or flags change; maintain Apache headers on new files.
- Ensure commits/PR description include `Signed-off-by:` and reference issues if applicable; rebase on latest main.