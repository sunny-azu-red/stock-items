# Conventions and rules

These rules keep the app simple, safe and consistent. They apply to people and coding agents alike ([AGENTS.md](../AGENTS.md) is the summary).

## Secrets and data safety

- `.env` holds real credentials and is git-ignored. Never commit it, paste it into issues, or print it in logs. `.env.example` contains placeholders only.
- Tests and experiments never use the `public` schema. Use `internal/testdb` or `DB_SCHEMA=e2e_*` / `test_*`. `db.DropSchema` refuses other names on purpose.
- Destructive UI actions (delete item, vehicle, kit or service) ask for confirmation.

## Database

- Migrations are append-only: `internal/db/migrations/0000N_short_description.sql` in goose format with `Up` and `Down` sections.
- Use constraints for invariants that must never break: non-negative quantity, valid enums, foreign keys.
- Never hard-delete a row that other records depend on through a cascade. Refuse it and offer an archive instead, as `DeleteItem` does, so history, services and spending don't change behind the user's back.
- Write all SQL in `internal/store`. Use positional parameters (`$1`), never string-concatenated values. Dynamic filters go through the `sqlArgs` helper.
- Writes that touch more than one row run in `Store.inTx`.
- Every change to an item's quantity goes through `applyMovement`, so the ledger stays complete. The exceptions fix mistakes by changing a recorded entry together with its effect: `DeleteService` (deletes a service's movements), and `UpdateMovement`/`DeleteMovement` (revise one movement through `reviseMovement`, which moves the stock by the difference under the item's row lock and refuses to go below zero).
- Lock several items in id order inside one transaction, so concurrent requests cannot deadlock.

## Domain

- Quantities are stored in base units (pieces, ml, g) and converted only at the edges (`stock.ParseAmount`, `stock.FormatQuantity`, `stock.SplitForInput`).
- Pure rules (validation, grouping, due dates, suggestions, exports, how a new item's starting stock is recorded, plausible dates) live in `internal/stock`, have no I/O and are unit tested.
- Money figures (last price, stock value, spending) read only `purchase` movements. Anything that adds stock the user paid for must be recorded as a purchase, not as `initial` or `correction`.
- Inputs are cleaned with `Normalize()` and checked with `Validate()`, which returns `stock.Errors` (field → message).

## Web

- Every page is server-rendered and every form works without JavaScript (POST, then a 303 redirect with a flash message). htmx and `static/app.js` are progressive enhancements only.
- Invalid forms re-render with status **422**, keeping exactly what the user typed, with messages next to the fields.
- Never turn a blank field into a destructive value: a blank quantity on the edit form is an error, not 0.
- Parse dates of things that already happened (purchases, stock changes, services) with `parseEventDate`, which applies `stock.CheckEventDate`. Best-before dates use `parseOptDate`; they can be far in the future.
- Templates: pages in `templates/pages`, reusable blocks in `templates/partials`, helpers in `render.go`. Use `withQuery` for URLs built from encoded query strings.
- No external CDNs or fonts at runtime. Vendor assets under `static/vendor` with their licence files. (The Lucide icons are inlined as a sprite in `templates/partials/icons.html`; their licence is in `static/vendor`.)
- The layout must work from 360 px phone width up. The e2e suite fails if any page scrolls sideways.
- Accessibility: real `<label>`s for inputs, buttons for actions, links for navigation, `aria-label` on icon-only buttons.

## Code style

- `gofmt`, `go vet` and `staticcheck` must be clean (`scripts/check.sh`).
- Exported identifiers get a short doc comment. Comments explain *why*, not *what*.
- Prefer the standard library. Add a dependency only when it clearly pays for itself.
- Tests are table-driven where it helps. DB tests assert the ledger invariant after changing stock.

## Commits

- Small, focused commits with imperative subject lines ("Add shopping list suggestions").
- Run `scripts/check.sh` (and the e2e tests for UI changes) before committing.
