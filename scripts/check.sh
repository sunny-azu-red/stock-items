#!/usr/bin/env bash
# Runs every check CI runs (except govulncheck, the lock tests' repeat run,
# the migrations-unchanged check, e2e and the Docker smoke test):
# formatting, go.mod tidiness, lint, tests with the race detector and
# coverage.
# Usage: scripts/check.sh            (integration tests need DB_* or .env)
# Writes coverage.out (gitignored) for `go tool cover -html=coverage.out`.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"

echo "==> gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "These files need gofmt -w:"; echo "$unformatted"; exit 1
fi

echo "==> go mod tidy -diff / verify"
go mod tidy -diff
go mod verify

echo "==> go vet"
go vet ./...

# golangci-lint (.golangci.yml) includes staticcheck; CI runs it with a
# pinned version. Without it installed, staticcheck (a go.mod tool) still runs.
if command -v golangci-lint >/dev/null; then
  echo "==> golangci-lint"
  golangci-lint run
else
  echo "==> staticcheck (golangci-lint not installed)"
  go tool staticcheck ./...
fi

# The race detector needs cgo, so a C compiler; Go turns cgo off without
# one. CI has gcc, so it always runs with -race.
race=()
if [ "$(go env CGO_ENABLED)" = 1 ]; then
  race=(-race)
  echo "==> go test -race"
else
  echo "==> go test (no -race: it needs cgo and a C compiler)"
fi
go test -count=1 "${race[@]}" -coverprofile=coverage.out -coverpkg=./internal/...,./cmd/... ./...
echo "Coverage: $(go tool cover -func=coverage.out | tail -1 | awk '{print $NF}')"

echo "All checks passed."
