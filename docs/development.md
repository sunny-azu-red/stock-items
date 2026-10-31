# Development

## Setup

1. Install Go 1.27.3 or newer (the version in `go.mod`). Without root access, unpack the official tarball into `~/.local/go` and add `~/.local/go/bin` to `PATH`.
2. `cp .env.example .env` and point it at a Postgres database. The user needs to be able to create schemas in that database. It does not need to create databases.
3. `go run ./cmd/pitbox` starts the app on `http://localhost:8080`.

Useful flags:

| Flag | Effect |
|---|---|
| `-migrate-only` | apply migrations and exit |
| `-debug` | log static file and health check requests too |
| `-reset-schema` | drop `DB_SCHEMA` before starting. Only allowed for `test_*` / `e2e_*` schemas; used by the e2e tests. |
| `-healthcheck` | ask the server on `HOST`:`PORT` for `/healthz` and exit 0 or 1; the Docker image's `HEALTHCHECK` |

To experiment without touching your real data, run with a separate schema:

```bash
DB_SCHEMA=e2e_playground PORT=8090 go run ./cmd/pitbox
```

## Tests

| Layer | Where | Needs DB | Run |
|---|---|---|---|
| Domain rules (units, grouping, export, due dates…) | `internal/stock/*_test.go` | no | `go test ./internal/stock/` |
| Store (SQL, transactions, ledger invariant) | `internal/store/store_test.go` | yes | `go test ./internal/store/` |
| HTTP handlers and templates | `internal/web/web_test.go` | yes | `go test ./internal/web/` |
| Browser end-to-end, desktop and phone | `e2e/tests/*.spec.ts` | yes | `cd e2e && npx playwright test` |

- Each DB test gets a fresh, migrated schema `test_<random>` that is dropped when the test ends. Without `DB_HOST` (and no `.env`) these tests are skipped.
- The e2e suite starts two servers, one per Playwright project, on the schemas `e2e_desktop` and `e2e_mobile`. They are reset at the start of each run and left in place afterwards so you can inspect them.
- The e2e tests use `@playwright/test` 1.63.0, pinned to match the Chromium build installed on the dev machine. After upgrading it, run `npx playwright install chromium`.
- `scripts/check.sh` runs what CI runs, except govulncheck, the e2e tests and the Docker smoke test (CI builds the image, starts it on the schema `e2e_docker` and waits for its health check to pass).

## CI

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on pull requests, pushes to `master` and `v*` tags:

1. **test:** `scripts/check.sh` against a Postgres 18 service, govulncheck (fails on a known vulnerability in Go or a dependency that the code reaches; its version is pinned in the workflow, and it is fetched there, not a module dependency), the Docker smoke test, then the Playwright suite. To run govulncheck locally: `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`.
2. **image** (pushes only, after **test** passes): builds `linux/amd64` (the only platform Pitbox runs on) and pushes `ghcr.io/sunny-azu-red/pitbox`, tagged `latest` from `master`, `sha-<commit>` from every push, and `1.2.3` and `1.2` from a `v1.2.3` tag. [Deployment](deployment.md#portainer) uses these images.

## Adding a feature (checklist)

1. **Schema change?** Add a new migration file. Don't edit old ones.
2. **Rules** go in `internal/stock` with table-driven tests.
3. **Queries** go in `internal/store`. Keep multi-row writes inside `inTx`, and route stock changes through `applyMovement`. When one transaction locks several items, lock them in id order (see `CreateService`) and `FOR NO KEY UPDATE`, after any service row and vehicle, or two requests can deadlock. Refuse archived things in the store, not just in the page.
4. **Handler** goes in `internal/web/handlers_*.go`: parse into a form struct, validate, call the store, redirect with a flash message.
5. **Template:** a page in `templates/pages`, plus a reusable block in `templates/partials` if needed.
6. Add a case to `web_test.go`, and to the e2e spec for anything interactive.
7. Run `scripts/check.sh` and `npx playwright test`.

## Screenshots

```bash
cd e2e
SCREENSHOT_DIR=/tmp/shots npx playwright test -g "set up|filter|quick|plan|screenshots"
cp /tmp/shots/{desktop-dashboard,desktop-stock,desktop-kit,mobile-stock}.png ../docs/screenshots/
```

This writes one PNG per page and device. Add `SCREENSHOT_SCHEME=dark` for the dark theme (the files end in `-dark`). Only the four the README shows are kept in `docs/screenshots`.
