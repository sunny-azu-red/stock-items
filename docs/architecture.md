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
| `internal/db` | Opens the pgx pool (with optional `search_path`), runs goose migrations embedded from `migrations/`, guarded `DropSchema` for tests | `pgx`, `goose` |
| `internal/stock` | Domain types and **pure** business rules: unit parsing and formatting, validation, grouping items by vehicle set, text and CSV export (stock and history), kit readiness, service due dates, purchase suggestions, starting stock (purchase vs initial), editing recorded stock changes, item usage (archive vs delete), date sanity checks | stdlib only |
| `internal/store` | All SQL. Each method is one use case. Multi-row writes run in a single transaction (`inTx`) | `pgx`, `stock` |
| `internal/web` | Routes, middleware, handlers, form parsing, template rendering, photo processing | `store`, `stock` |
| `internal/testdb` | Creates a migrated throwaway schema per test | `db`, `config` |

## Request flow

1. `http.ServeMux` routes using Go 1.22+ patterns (`POST /items/{id}/adjust`).
2. Middleware (outermost first): panic recovery → request logging (`log/slog`) → security headers (CSP, `nosniff`, frame denial) → `http.CrossOriginProtection` (CSRF protection based on `Sec-Fetch-Site` / `Origin`).
3. Handlers are `func(w, r) error`. `Server.wrap` turns `store.ErrNotFound` into a 404 page, a `store.ConflictError` on a POST into an error message back where the user was, and other errors into a logged 500.
4. Forms are parsed into small `*Form` structs (raw strings, to re-render exactly what was typed) and converted into `stock.*Input` values. Validation lives in `stock` (`Normalize` + `Validate`). Errors re-render the page with status 422.
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
- **Quantities in base units** (pieces, ml, g) stored as `numeric(12,3)`. Conversion happens only at the edges.
- **Stock ledger.** Every quantity change is a row in `stock_movements`, written in the same transaction as the quantity update. History, prices, stock value and "undo a service" all come from this.
- **Items are grouped by their exact vehicle set.** "KTM 390 + KTM 1290" is one section and "KTM 390" another, which matches how people think about shared parts.
- **Archive instead of delete** for items, vehicles and kits that other records refer to, so history and spending never change behind the user's back.
- **Deletes go to a trash.** Each delete stores a JSON snapshot of the rows it removes in `trash`, in the same transaction, which gives every delete an Undo and a *Recently deleted* page. Restoring re-inserts the rows with their ids and redoes their stock effect (`store/trash.go`). Deleting an entry for good also deletes the entries that can't be restored without it.
- **Photos in Postgres** (`bytea`, resized to 1600 px plus a 320 px thumbnail). A `pg_dump` is a complete backup.
