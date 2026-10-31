# Architecture

Pitbox is a classic server-rendered web application. The browser asks for HTML pages and submits forms. There is no separate JSON API and no single-page app. A small amount of [htmx](https://htmx.org) makes a few interactions feel instant: live filtering and quick −/+ on stock rows.

```
browser ──HTTP──▶ internal/web ──▶ internal/store ──SQL──▶ PostgreSQL
                     │                  │
                     └──── internal/stock (pure rules) ◀───┘
```

## Packages

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/pitbox` | Flags, logging, config, DB, migrations, HTTP server with graceful shutdown, `-healthcheck` probe for Docker | everything |
| `internal/config` | Reads env vars and the `.env` file in the working directory (in a source checkout, the module's), and refuses a search URL or currency that can't work | `godotenv` |
| `internal/db` | Opens the pgx pool (sessions in UTC, with optional `search_path`), runs goose migrations embedded from `migrations/`, guarded `DropSchema` for tests | `pgx`, `goose` |
| `internal/stock` | Domain types and **pure** business rules: unit parsing and formatting, validation, grouping items by vehicle set, text and CSV export (stock and history), kit readiness, service due dates, purchase suggestions, starting stock (purchase vs initial), editing recorded stock changes, item usage (archive vs delete), date sanity checks | stdlib only |
| `internal/store` | All SQL. Each method is one use case. Multi-row writes run in a single transaction (`inTx`) | `pgx`, `stock` |
| `internal/web` | Routes, middleware, handlers, form parsing, template rendering, photo processing | `store`, `stock` |
| `internal/testdb` | Creates a migrated throwaway schema per test | `db`, `config` |

## Request flow

1. `http.ServeMux` routes using Go 1.22+ patterns (`POST /items/{id}/adjust`).
2. Middleware (outermost first): panic recovery → request logging (`log/slog`) → security headers (CSP, `nosniff`, frame denial) → `http.CrossOriginProtection` (CSRF protection based on `Sec-Fetch-Site` / `Origin`).
3. Handlers are `func(w, r) error`. `Server.wrap` turns `store.ErrNotFound` into a 404 page, a form over the size limit into a 413 page, a `store.ConflictError` or `store.StaleError` on a POST into an error message back where the user was, a `store.AlreadySavedError` into "This was saved already" there, a cancelled request into no response, and other errors into a logged 500.
4. Forms are parsed into small `*Form` structs (raw strings, to re-render exactly what was typed) and converted into `stock.*Input` values. Validation lives in `stock` (`Normalize` + `Validate`). Errors re-render the page with status 422. Edit forms and record buttons post the record's `version`; the store refuses a stale one (`store.StaleError`), which an edit form shows as a banner with a Reload link, keeping what was typed, and a button as a message on the record's page. Create and edit forms also post a one-time `form_key`; a repeat of a save that went through is answered "This was saved already" with a redirect to the record, before the version is looked at.
5. Success means **POST → 303 redirect → GET**, with a one-time flash message in a cookie. htmx requests get `HX-Redirect`, or a partial plus an `HX-Trigger` toast.

## Templates

- `templates/layout.html` is the page shell: sidebar on desktop, tab bar on phones.
- `templates/partials/*.html` are shared blocks such as the stock row (`item-row`), stock sections, kit rows, chips, and the movement table.
- `templates/pages/*.html` define `content` for each page. Each page is parsed together with the layout and partials into its own template set at startup, so a broken template fails fast.
- Template helper functions live in `render.go` (`qty`, `money`, `km`, `date`, `icon`, `withQuery`, …).

## Static files

`internal/web/static` is embedded. URLs carry a content hash (`/static/app.css?v=…`) and are served with `Cache-Control: immutable`. htmx is vendored in `static/vendor`, and the Lucide icons are an inline sprite in `templates/partials/icons.html`, so the app needs no internet access.

## Key design decisions

- **Go with server rendering instead of a SPA with an API.** For a single-user CRUD app this means one binary, no build step for the frontend, and fewer moving parts. Every form works without JavaScript.
- **Quantities in base units** (pieces, ml, g) stored as `numeric(12,2)`: at most two decimals, as the pages show them, so "1.234 g" (which reads as 1234 g) and "have 0.23 g, need 0.23 g" can't happen. Conversion happens only at the edges, and `stock.ParseAmount` refuses finer input rather than round it.
- **Days are decided in Go, in the app's zone.** Postgres keeps instants in UTC and every session is pinned to UTC, so the database server's zone never matters. Which calendar day it is (today, the day a service was logged, what is expiring, "Deleted today") is worked out in Go in the zone `TZ` names, or the machine's, and passed to SQL as a `date` parameter. One rule in one place, instead of SQL and Go disagreeing around midnight; a test fails on SQL like `current_date` or `created_at::date`.
- **Stock ledger.** Every quantity change is a row in `stock_movements`, written in the same transaction as the quantity update. History, prices, stock value and "undo a service" all come from this.
- **Items are grouped by their exact vehicle set.** "KTM 390 + KTM 1290" is one section and "KTM 390" another, which matches how people think about shared parts.
- **Row locks in one order.** Writes that touch several rows lock them in a fixed order (a service, its vehicle, its kit, a shopping entry, the items in id order, a stock change after its item, trash rows in id order), so concurrent requests wait for each other instead of deadlocking. Where a row can only be found after a later lock (a shopping entry added while an item is archived), it is locked with `NOWAIT`, and the archive steps back to a savepoint and starts over in order. Checks that an insert depends on (a kit not archived, an item's unit) hold their row `FOR SHARE` until commit, since the insert's own foreign-key lock doesn't stop an update. The full list is in [conventions.md](conventions.md#database); `internal/store/locks_test.go` lines up each pair that once deadlocked or let wrong data in.
- **Optimistic concurrency with versions.** Two tabs (or two people) editing the same record don't overwrite each other: each record has a `version`, forms carry it, and the store compares it under the row lock it takes anyway. Stock quantity and odometer readings don't change it, as they are deltas or readings that the forms already guard (`quantity_was`, `odometer_was`); restoring from the trash does, so a form opened before a delete can't save over the restored record.
- **One-time form keys.** A double click, a retry of a save that seems stuck, or Back and Save sends the same form again. Each form carries a random key, which the save's transaction claims in `form_submissions` as its first statement; Postgres makes a second claim of the key wait for the first transaction, then either find it taken (do nothing, go to the record) or, if the first rolled back, go ahead. So the page can unlock a form that seems stuck ("Still saving… try again") without risking a second service or purchase. The key also has to come before the version check: an edit's own first save moves the version on.
- **Archive instead of delete**
 for items, vehicles and kits that other records refer to, so history and spending never change behind the user's back.
- **Deletes go to a trash.** Each delete stores a JSON snapshot of the rows it removes in `trash`, in the same transaction, which gives every delete an Undo and a *Recently deleted* page. Restoring re-inserts the rows with their ids and redoes their stock effect (`store/trash.go`). Permanently deleting an entry also deletes the entries that can't be restored without it.
- **Photos in Postgres** (`bytea`, resized to 1600 px plus a 320 px thumbnail). A `pg_dump` is a complete backup. Uploads are decoded one at a time and refused over 50 megapixels, so one decode takes at most about 1.4 GB (a progressive CMYK JPEG; an ordinary phone JPEG about 270 MB).
