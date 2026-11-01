# Development

## Setup

1. Install Go 1.27.3 or newer (the version in `go.mod`). Without root access, unpack the official tarball into `~/.local/go` and add `~/.local/go/bin` to `PATH`.
2. Install the libvips command line tools, which process photos: `sudo apt install libvips-tools` (Debian/Ubuntu; it brings the JPEG, PNG, WebP, GIF and HEIC loaders). Without them the app starts with a warning, photo uploads are refused, and the photo tests skip (in CI they fail instead).
3. `cp .env.example .env` and point it at a Postgres database. The user needs to be able to create schemas in that database. It does not need to create databases. Change `DB_SCHEMA` to `dev`: the example's `public` is for deploying (the real app's data), and a development build refuses to start on it (see below).
4. Optional: [golangci-lint](https://golangci-lint.run/) v2 (the version CI pins in `ci.yml`), which `scripts/check.sh` runs when it is on `PATH` (otherwise it runs staticcheck), and gcc, which the race detector needs (`sudo apt install gcc`; without it `check.sh` runs the tests without `-race`).
5. `go run ./cmd/pitbox` starts the app on `http://localhost:8080`.

Useful flags:

| Flag | Effect |
|---|---|
| `-migrate-only` | apply migrations and exit |
| `-debug` | log static file and health check requests too |
| `-reset-schema` | drop `DB_SCHEMA` before starting. Only allowed for `test_*` / `e2e_*` schemas; used by the e2e tests. |
| `-healthcheck` | ask the server on `HOST`:`PORT` for `/healthz` and exit 0 or 1; the Docker image's `HEALTHCHECK` |

The real data lives in `public`, and starting the app applies new migrations to its schema. So a development build (`go run`, `go build` without `-ldflags "-X main.version=…"`, or a version ending in `-dirty`, as `git describe --dirty` gives with uncommitted changes) refuses to start with `DB_SCHEMA=public` unless `APP_ALLOW_PUBLIC_SCHEMA=1` is set. Develop on `dev`, a copy of the real data made from a backup (`pg_dump -n public`, restored into a scratch database, `ALTER SCHEMA public RENAME TO dev`, dumped with `-n dev` and restored). Refresh it the same way when you need newer data.

To experiment without touching even that, run with a throwaway schema:

```bash
DB_SCHEMA=e2e_playground PORT=8090 go run ./cmd/pitbox
```

## Tests

| Layer | Where | Needs DB | Run |
|---|---|---|---|
| Domain rules (units, grouping, export, due dates…) | `internal/stock/*_test.go` | no | `go test ./internal/stock/` |
| Settings, `.env`, the `public` guard, health check | `internal/config/*_test.go`, `cmd/pitbox/main_test.go` | no | `go test ./internal/config/ ./cmd/...` |
| Migration files: numbering, names, goose markers | `internal/db/migrations_order_test.go` | no | `go test -run TestMigrationFiles ./internal/db/` |
| Migrations, UTC sessions | `internal/db/migrations_test.go` | yes | `go test ./internal/db/` |
| Store (SQL, transactions, ledger invariant, lock order, SQL without zones) | `internal/store/*_test.go` | yes | `go test ./internal/store/` |
| HTTP handlers and templates (versions, form keys, sorting, the trash round trip of every kind) | `internal/web/*_test.go` | yes | `go test ./internal/web/` |
| Photo processing (formats, rotation, limits, vips failures, the vips child's peak memory) | `internal/web/photo_test.go` | no, but libvips-tools | `go test -run Photo ./internal/web/` (`-short` skips the memory test, which makes 50-100 MP images) |
| Browser end-to-end, desktop and phone | `e2e/tests/*.spec.ts` | yes | `cd e2e && npx playwright test` |

- Tests live next to the code they test, one file per feature (`items_test.go`, `trash_test.go`, …), as is usual in Go; shared helpers are in `store_test.go` and `web_test.go`. The store's are in the package `store_test` (they use only what the package exports); the web tests are in `web` itself.
- Each DB test gets a fresh, migrated schema `test_<random>` that is dropped when the test ends. Without `DB_HOST` (and no `.env`) these tests are skipped.
- The e2e suite starts two servers, one per Playwright project, on the schemas `e2e_desktop` and `e2e_mobile`. They are reset at the start of each run and left in place afterwards so you can inspect them.
- The e2e tests use a pinned `@playwright/test` (Dependabot proposes updates). Run `npx playwright install chromium` the first time and after each upgrade.
- `scripts/check.sh` checks formatting, `go mod tidy` and `go mod verify`, vet, lint (golangci-lint, or staticcheck without it), and runs the tests with `-race` (when gcc is there) and coverage (`coverage.out`; `go tool cover -html=coverage.out` shows it). CI runs it too, plus what it leaves out: golangci-lint itself, govulncheck, the lock tests five times over, the check that committed migrations are unchanged, the Docker smoke test (CI builds the image, starts it read-only on the schema `e2e_docker` and waits for its health check to pass) and the e2e tests.
- The forced-interleaving lock tests must stay in `internal/store/locks_test.go`: CI runs every test in that file with `-race -count=5`. Locally: `go test -count=5 -run "$(grep -oP '^func \KTest\w+' internal/store/locks_test.go | paste -sd'|')" ./internal/store/`.

## CI

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on pull requests, pushes to `master` and `v*` tags:

1. **test:** on a pull request, first refuses any change to a committed migration (adding one is fine). Then installs `libvips-tools` and runs `scripts/check.sh` against a Postgres 18 service (the coverage total goes in the run's summary, `coverage.out` is kept as an artifact), the lock tests with `-race -count=5`, govulncheck (fails on a known vulnerability in Go or a dependency that the code reaches; its version is pinned in the workflow, and it is fetched there, not a module dependency), golangci-lint ([`.golangci.yml`](../.golangci.yml)), the Docker smoke test, then the Playwright suite. To run govulncheck locally: `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`.
2. **image** (pushes only, after **test** passes): builds `linux/amd64` (the only platform the image is for) with an SBOM and provenance, pushes `ghcr.io/sunny-azu-red/pitbox`, and scans it with Trivy (fails on a HIGH or CRITICAL vulnerability that has a fix). Tags: `latest` and `sha-<commit>` from `master`; `1.2.3` and `1.2` from an optional `v1.2.3` tag, which never moves `latest`. [Deployment](deployment.md#updates-and-versions) uses these images.
3. **release** (optional `v*` tags only, after **image**): builds static `linux-amd64` and `linux-arm64` binaries and creates the GitHub Release with generated notes, the binaries and `SHA256SUMS`.

Actions are pinned to commit SHAs and the base images to digests; [Dependabot](../.github/dependabot.yml) proposes monthly updates for them, Go modules and the e2e's npm packages.

## Adding a feature (checklist)

1. **Schema change?** Add a new migration file. Don't edit old ones.
2. **Rules** go in `internal/stock` with table-driven tests.
3. **Queries** go in `internal/store`. Keep multi-row writes inside `inTx` (`inFormTx` for a form's save), and route stock changes through `applyMovement`. When one transaction locks several items, lock them in id order (see `CreateService`) and `FOR NO KEY UPDATE`, after any service row and vehicle, or two requests can deadlock. Refuse archived things in the store, not just in the page.
4. **Handler** goes in `internal/web/handlers_*.go`: parse into a form struct, validate, call the store, redirect with a flash message.
5. **Template:** a page in `templates/pages`, plus a reusable block in `templates/partials` if needed.
6. Test it over HTTP in the feature's test file in `internal/web` (and in `versions_test.go` and `formkeys_test.go` for a new form or button, and in `trash_matrix_test.go` for a new kind of trash entry), and in the e2e spec for anything interactive.
7. Run `scripts/check.sh` and `npx playwright test`.

## Screenshots

```bash
scripts/screenshots.sh
```

This retakes `docs/screenshots`: every page and form, on desktop and phone, in light and dark (`docs/screenshots/<desktop|mobile>/<light|dark>/<page>.png`). It runs the whole e2e suite; the screenshot test comes late in it, so the pages have photos, history and trash to show. Then it recompresses the PNGs losslessly (about a third smaller) and lists what changed. Commit your changes first (it refuses otherwise). The sidebar in the screenshots shows a pinned version (`6b1ea22`, set in the script) that looks like a commit but isn't the latest one, so no commit changes every screenshot: a re-run only changes the pages that did. Chromium's anti-aliasing varies by a colour level or two between runs; a screenshot within that of the committed one keeps the committed file. The README shows four of them by path, so it stays up to date; the rest are there to browse. A new page goes in the `shots` list of the `screenshots` test in `e2e/tests/garage.spec.ts`.
