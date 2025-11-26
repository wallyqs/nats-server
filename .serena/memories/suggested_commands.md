# Common commands
- Build binary: `go build ./...` (or `go build -o nats-server`); run with `./nats-server -c conf/nats-server.conf` or `go run main.go --help` to see flags.
- Full tests quick pass: `go test ./...`; heavier CI-style batches via `scripts/runTestsOnTravis.sh [compile|no_race_1_tests|no_race_2_tests|store_tests|js_tests|js_cluster_tests_1|...]` (uses tags like `skip_js_cluster_tests`).
- Coverage run: `./scripts/cov.sh` (runs tagged test sets, merges with gocovmerge, opens HTML if no args).
- Lint/format: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.4 && golangci-lint run` (uses goimports formatting); standard `go fmt ./...` acceptable locally.
- Generate errors: `go generate ./server` (main.go includes go:generate for `server/errors_gen.go`).
- Running targeted suites: examples from scripts/runTestsOnTravis.sh, e.g. `RACE=-race scripts/runTestsOnTravis.sh js_tests`, `scripts/runTestsOnTravis.sh mqtt_tests`, `scripts/runTestsOnTravis.sh non_srv_pkg_tests`. Adjust `TRAVIS_TAG` env when matching version tests.