#!/usr/bin/env bash
# Runs every check CI runs (except e2e): formatting, vet, staticcheck, tests.
# Usage: scripts/check.sh            (integration tests need DB_* or .env)
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"

echo "==> gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "These files need gofmt -w:"; echo "$unformatted"; exit 1
fi

echo "==> go vet"
go vet ./...

echo "==> staticcheck"
go tool staticcheck ./...

echo "==> go test"
go test -count=1 ./...

echo "All checks passed."
