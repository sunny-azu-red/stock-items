# AGENTS.md

Guidance for AI coding agents (and humans) working in this repository. Read [docs/conventions.md](docs/conventions.md) for the full rules; this file is the short version.

## What this is

A server-rendered Go web app that tracks vehicle parts on stock. It uses Go stdlib `net/http` and `html/template`, pgx for Postgres, and htmx for small dynamic updates. It ships as a single binary with templates, static files and migrations embedded.

## Layout

```
cmd/pitbox/      main: config → db → migrations → HTTP server
internal/config/      env + .env loading
internal/db/          pgx pool, goose migrations (migrations/*.sql, embedded)
internal/stock/       pure domain logic: units, validation, grouping, exports, kits, suggestions
internal/store/       all SQL; multi-row writes in one transaction
internal/web/         routes, middleware, handlers, templates/, static/
internal/testdb/      throwaway schema helper for integration tests
e2e/                  Playwright tests (Node is only used here)
docs/                 documentation
```

## Commands

Go lives at `~/.local/go/bin` on the dev machine. Prefix commands with `PATH=$HOME/.local/go/bin:$PATH` if `go` is not found.

| Task | Command |
|---|---|
| Run the app | `go run ./cmd/pitbox` |
| All checks (fmt, vet, staticcheck, tests) | `scripts/check.sh` |
| Unit tests only (no DB needed) | `go test ./internal/stock/` |
| E2E tests | `cd e2e && npx playwright test` |
| E2E screenshots | see [docs/development.md](docs/development.md#screenshots) |

## Hard rules

- **Never commit `.env`** or print its secrets. `.env.example` holds placeholders only.
- **Never run tests or experiments against the `public` schema.** Use `internal/testdb` (schemas `test_*`) or `DB_SCHEMA=e2e_*`. `db.DropSchema` refuses anything else. Keep it that way.
- **Migrations are append-only.** Add `internal/db/migrations/0000N_description.sql` (goose format, with `-- +goose Up` and `-- +goose Down`). Never edit a migration that has been committed.
- **Quantities are stored in base units:** pieces, millilitres, grams. Convert only at the edges with `stock.ParseAmount` and `stock.FormatQuantity`.
- **Every change to `items.quantity` goes through `applyMovement`** in `internal/store/movements.go`, inside a transaction. The ledger must always add up. The tests assert this. (The exceptions undo or fix a recorded entry together with its effect: `DeleteService`/`UpdateService`, and `UpdateMovement`/`DeleteMovement`, which go through `reviseMovement`.)
- **Money figures (last price, stock value, spending) read only `purchase` movements.** Stock the user paid for must be recorded as a purchase, never as `initial` or `correction`.
- **Don't hard-delete what other records depend on.** Items with purchases, service usage or kit lines, vehicles with services or kits, and kits with services are archived (`archived_at`), not deleted; `ListItems` and `ListKits` hide them by default, and web lists use `activeVehicles`.
- Business rules belong in `internal/stock` (pure, unit tested). SQL belongs in `internal/store`. HTTP and HTML belong in `internal/web`.
- Forms must work without JavaScript (plain POST and redirect). htmx and `app.js` only enhance.
- Build URLs with query strings in templates using the `withQuery` helper. Otherwise `html/template` escapes `=` in pre-encoded query strings.
- No external CDNs at runtime. Vendor assets into `internal/web/static/vendor/`.
- Run `scripts/check.sh` (and the e2e tests for UI changes) before committing. Make sure `gofmt -l .` prints nothing.

## Style

Match the surrounding code: short doc comments on exported identifiers, table-driven tests, errors wrapped with context, no new dependencies without a good reason.
