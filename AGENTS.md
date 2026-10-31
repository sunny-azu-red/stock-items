# AGENTS.md

Guidance for AI coding agents (and humans) working in this repository. Read [docs/conventions.md](docs/conventions.md) for the full rules; this file is the short version.

## What this is

A server-rendered Go web app that tracks vehicle parts on stock. It uses Go stdlib `net/http` and `html/template`, pgx for Postgres, and htmx for small dynamic updates. It ships as a single binary with templates, static files and migrations embedded.

## Layout

```
cmd/pitbox/           main: config → db → migrations → HTTP server (+ -healthcheck for Docker)
internal/config/      env + .env loading
internal/db/          pgx pool, goose migrations (migrations/*.sql, embedded)
internal/stock/       pure domain logic: units, validation, grouping, exports, kits, suggestions
internal/store/       all SQL; multi-row writes in one transaction
internal/web/         routes, middleware, handlers, templates/, static/
internal/testdb/      throwaway schema helper for integration tests
e2e/                  Playwright tests (Node is only used here)
docs/                 documentation
.github/workflows/    CI: checks, govulncheck, Docker smoke test, e2e; publishes the image to GHCR
Dockerfile            distroless image; compose.yaml runs it (Portainer stack)
```

## Commands

Go lives at `~/.local/go/bin` on the dev machine. Prefix commands with `PATH=$HOME/.local/go/bin:$PATH` if `go` is not found.

| Task | Command |
|---|---|
| Run the app | `go run ./cmd/pitbox` |
| All checks (fmt, vet, staticcheck, tests) | `scripts/check.sh` |
| Unit tests only (no DB needed) | `go test ./internal/stock/` |
| E2E tests | `cd e2e && npx playwright test` |
| Docker image | `docker build -t pitbox .` |
| E2E screenshots | see [docs/development.md](docs/development.md#screenshots) |

## Hard rules

- **Never commit `.env`** or print its secrets. `.env.example` holds placeholders only.
- **Never run tests or experiments against the `public` schema.** Use `internal/testdb` (schemas `test_*`) or `DB_SCHEMA=e2e_*`. `db.DropSchema` refuses anything else. Keep it that way.
- **Migrations are append-only.** Add `internal/db/migrations/000NN_short_description.sql` with the next number (goose format, with `-- +goose Up` and `-- +goose Down`). Never edit a migration that has been committed.
- **Quantities are stored in base units:** pieces, millilitres, grams. Convert only at the edges with `stock.ParseAmount` and `stock.FormatQuantity`.
- **Amounts have at most two decimals of the base unit** (whole pieces). The columns are `numeric(12,2)`, and `stock.ParseAmount` refuses finer input (also after an L/kg conversion) instead of rounding it; amounts computed in code go through `stock.RoundQty`. Never add a third decimal back, and never round what a user typed. Trash restores refuse a finer snapshot amount, as the column would round it and break the ledger.
- **Every change to `items.quantity` goes through `applyMovement`** in `internal/store/movements.go`, inside a transaction. The ledger must always add up. The tests assert this. (The exceptions undo or fix a recorded entry together with its effect: `DeleteService`/`UpdateService`; `UpdateMovement`/`DeleteMovement`, which go through `reviseMovement`; and `RestoreTrash`, including undoing a Bought.)
- **Dates: instants in UTC, calendar days only in Go.** Timestamps (`timestamptz`: `created_at`, `deleted_at`, `archived_at`, …) are instants, stored in UTC; `db.Open` pins every session to `timezone=UTC`. A calendar day ("today", the day something was logged or deleted, what is expiring) is decided only in Go, in the app zone: `TZ` if set and valid, else the machine's (`config.location()`, which `main` puts in `time.Local`). Use `stock.DateOnly(time.Now())` (`appToday` in the store, `s.now()`/`s.today()` in web) and `stock.DateOnly(t.In(loc))` for an instant; date columns hold the day itself (pgx reads them as midnight UTC, `parseOptDate` makes the same). SQL never turns an instant into a day (no `current_date`, `::date` on a column, `AT TIME ZONE`, `date_trunc`, …; `$n::date` on a day Go worked out is fine, and `now() - interval '1 day'` stays an instant). Pages show times in the app zone (pgx scans `timestamptz` into `time.Local`). `TestNoZoneDependentSQL` in `internal/store` enforces the SQL part.
- **Money figures (last price, stock value, spending) read only `purchase` movements.** Stock the user paid for must be recorded as a purchase, never as `initial` or `correction`.
- **Every user delete goes to the trash.** Take a snapshot with `snapshot`/`putTrash` (`internal/store/trash.go`) in the same transaction and return the trash id; restoring is `RestoreTrash` (a ledger exception, above).
- **Don't hard-delete what other records depend on.** Items with purchases, service usage or kit lines, vehicles with services or kits, and kits with services are archived (`archived_at`), not deleted; `ListItems` and `ListKits` hide them by default, and web lists use `activeVehicles`.
- **Archived means no new work, enforced in the store.** Stock changes to an archived item, and new services or kits for an archived vehicle, and services with an archived kit, fail with a `ConflictError` inside the transaction; the pages only hide the buttons. A service's own parts move with it when it is edited (unchanged), deleted or restored; kit lines may name archived items. Restoring a deleted past service or kit of an archived vehicle is not new work: `RestoreTrash` allows it (Undo must keep working).
- **Take row locks in one order**, so two requests can't deadlock: a service row; its vehicle (`lockVehicle`; a kit's save locks its old and new vehicle, in id order); the kit (`FOR SHARE` when a service uses it, `checkServiceKit`); a shopping entry; the items, in id order; a stock change after its item (`lockMovement` reads the item id unlocked, locks the item, then the stock change); an item before its photo. Trash rows come before all of these, in id order (`RestoreTrash` locks its entry first; `PurgeTrash`, `EmptyTrash` lock all theirs). An insert only takes `KEY SHARE` on the rows its foreign keys point to, which an `UPDATE` doesn't wait for: when a check must hold until commit (a kit not archived, an item's unit for its kit lines), lock the row `FOR SHARE` before checking. A lookup that finds no row locks nothing; look again after the lock that keeps new rows out (`ArchiveItem`, `LinkShopping`), with `NOWAIT`, since that row now comes after the item: `ArchiveItem` then rolls back to a savepoint (which lets go of the item) and starts over taking the entry first; `LinkShopping` says "try again". Each lock fix gets a forced-interleaving test in `internal/store/locks_test.go` (a third transaction holds a row so the two calls queue up in the bad order), run with `-count=5`.
- **Edit forms and record buttons carry the record's version.** `items`, `vehicles`, `service_kits`, `service_records`, `stock_movements` and `shopping_entries` have a `version`; every edit form and every delete, archive, restore, Bought, link and remove button posts it (`<input type="hidden" name="version">`, read with `parseVersion`). The store checks it after the row lock the function already takes (no new locks) and fails with a `StaleError` (`Gone` when the row is no longer there); a missing version is stale. Edit forms re-render with 422, keeping what was typed, with a banner and a Reload link (`staleForm`); buttons flash and redirect (`staleButton`).
  - Bump (`version = version + 1`, written in the UPDATE, with a comment) on every write that changes what the record's edit form or page shows: the edit itself, archive/restore, a purchase's best-before date, un-archiving in `LinkShopping`, any change to a shopping entry's quantity or note. Child rows (SKUs, tags, kit lines, a service's parts) belong to the parent's version.
  - Don't bump on `items.quantity` (`applyMovement`, `reviseMovement`, `returnServiceParts`, `adjustStock`) or `vehicles.odometer_km` (`SetOdometer`, `moveOdometer`, `restoreOdometer`): those forms guard them with `quantity_was`/`odometer_was`, and a quick +/- or a logged service must not break an open edit form.
  - Restoring from the trash bumps (`restoreRows`), so a form opened before the delete can't save over the restored record.
  - Test every form and button over HTTP (`internal/web/versions_test.go`): open the page, change the record in "another tab", submit the stale form. Store tests alone missed browser bugs before.
- **Create and edit forms carry a one-time form key, claimed first in the transaction.** Every form that creates or changes something (new item, vehicle, kit, service; the item page's purchase, use and count; shopping add, the item page's "Add to shopping list", Bought; every edit form) renders `{{template "form-key" .FormKey}}` (a new random key, or the posted one when it comes back with a 422). Quick +/- and buttons don't. The store function passes it (`stock.*Input.FormKey`) to `inFormTx` (`internal/store/formkeys.go`), whose first statement claims it in `form_submissions` and which records the saved record's id under it. A key a committed save holds fails with `AlreadySavedError`: nothing is done, and the handler redirects to the record with "This was saved already" (`savedAlready`); a claim of a key whose save is still running waits for it, and goes ahead if that rolled back.
  - Check the key before the version and before anything else the first save may have changed: handlers call `savedBefore` right after parsing the form, and the claim precedes the version check. A retried edit must say "saved already", not "changed since you opened this".
  - A form without a key is refused with 422 (`checkFormKey`): it can't be told from a repeat of itself. Undoing a Bought frees its key (it's in the trash snapshot). Keys are pruned after a day.
  - Test repeats over HTTP (`internal/web/formkeys_test.go`): at once, after the save, after a failed save, after Undo.
- **A service's parts always point at it; the database enforces it.** `kind = 'service'` exactly when `service_record_id` is set (a CHECK), and its foreign key is `ON DELETE RESTRICT`, so return and delete a service's parts before the service (`returnServiceParts`), never the other way round. Only non-`service` stock changes are edited on their own (`Movement.Editable`).
- Business rules belong in `internal/stock` (pure, unit tested). SQL belongs in `internal/store`. HTTP and HTML belong in `internal/web`.
- Forms must work without JavaScript (plain POST and redirect). htmx and `app.js` only enhance.
- **Sort keys are whitelisted, never interpolated.** A page reads `sort`/`dir` with its `sortSpec` (`internal/web/sort.go`): an unknown value falls back to the page's default. The store maps each key to its own ORDER BY (`itemOrder`, `movementOrder`; kit lines in Go, `stock.SortKitLines`) with a stable tiebreak ending in the id, and blanks last in both directions. Sortable headers use the `sort-th` partial (plain links, `aria-sort`).
- Build URLs with query strings in templates using the `withQuery` helper. Otherwise `html/template` escapes `=` in pre-encoded query strings.
- No external CDNs at runtime. Vendor assets into `internal/web/static/vendor/`.
- Run `scripts/check.sh` (and the e2e tests for UI changes) before committing. Make sure `gofmt -l .` prints nothing.

## Style

Match the surrounding code: short doc comments on exported identifiers, table-driven tests, errors wrapped with context, no new dependencies without a good reason.
