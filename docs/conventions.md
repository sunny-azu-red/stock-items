# Conventions and rules

These rules keep the app simple, safe and consistent. They apply to people and coding agents alike ([AGENTS.md](../AGENTS.md) is the summary).

## Secrets and data safety

- `.env` holds real credentials and is git-ignored. Never commit it, paste it into issues, or print it in logs. `.env.example` contains placeholders only.
- Tests and experiments never use the `public` schema. Use `internal/testdb` or `DB_SCHEMA=e2e_*` / `test_*`. `db.DropSchema` refuses other names on purpose.
- Deletes ask for confirmation (items, vehicles, kits, services, stock changes, photos, archiving) and then offer Undo. Deleting permanently from *Recently deleted* always asks. Removing a shopping-list entry doesn't ask; Undo covers it.

## Database

- Migrations are append-only: `internal/db/migrations/0000N_short_description.sql` in goose format with `Up` and `Down` sections.
- Use constraints for invariants that must never break: non-negative quantity, valid enums, foreign keys.
- Every delete a user can trigger takes a trash snapshot of the rows it removes, in its transaction (`snapshot`/`putTrash` in `store/trash.go`), and returns the trash id for the Undo button; restoring goes through `RestoreTrash`.
- Never hard-delete a row that other records depend on through a cascade. Refuse it and offer an archive instead, as `DeleteItem` does, so history, services and spending don't change behind the user's back.
- Write all SQL in `internal/store`. Use positional parameters (`$1`), never string-concatenated values. Dynamic filters go through the `sqlArgs` helper.
- Writes that touch more than one row run in `Store.inTx`.
- Every change to an item's quantity goes through `applyMovement`, so the ledger stays complete. The exceptions change a recorded entry together with its effect:
  - `DeleteService` and `UpdateService` return a service's parts through `returnServiceParts`; an update then takes the new ones through `applyMovement`.
  - `UpdateMovement`/`DeleteMovement` revise one movement through `reviseMovement`, which moves the stock by the difference under the item's row lock and refuses to go below zero.
  - `RestoreTrash` puts rows back with their effect (`takeMovements`, `adjustStock`), and undoing a Bought takes its purchase back through `reviseMovement`.
- Lock several items in id order inside one transaction, so concurrent requests cannot deadlock. Lock items `FOR NO KEY UPDATE` to change them (only `DeleteItem` takes `FOR UPDATE`), or `FOR SHARE` to only check them (`AddShopping`): inserting a kit line or movement holds a key-share lock on the item, which `FOR UPDATE` would conflict with. The order is: a service row, its vehicle (`lockVehicle`), the kit it uses (`FOR SHARE`, `checkServiceKit`), then items; a kit's save locks its old and new vehicle (id order) before the kit, and the kit's line items `FOR SHARE` (id order, `shareItems`) after it; a shopping entry before its item; an item before its stock changes (`lockMovement` reads the item id without a lock, locks the item, then the stock change, and checks the id again), as deleting an item locks it before cascading to them; an item before its photo; a vehicle's tags before the vehicle when deleting it; trash rows in id order (`PurgeTrash` and `EmptyTrash` lock all theirs before deleting).
- An insert holds only `KEY SHARE` on the rows its foreign keys name, and an `UPDATE` that leaves keys alone doesn't wait for it. When the insert depends on a check that must stay true until commit (the kit isn't archived, the item's unit is the one the kit line's amount was read in), lock the row `FOR SHARE` before checking. A `SELECT … FOR UPDATE` that finds no row locks nothing, so another transaction can insert it meanwhile: look again after taking the lock that keeps such inserts out (`ArchiveItem`, `LinkShopping`, the latter with `NOWAIT` since that entry comes after the item). Foreign-key errors from a row deleted meanwhile become a `ConflictError`, not a 500.
- Every lock-order fix has a forced-interleaving test in `internal/store/locks_test.go`: a third transaction holds a row so the two calls queue up (checked in `pg_locks`) in the order that went wrong, then lets go. It must fail (40P01, or wrong data) without the fix; run it with `-count=5`.
- Records that forms edit have a `version` (items, vehicles, kits, services, stock changes, shopping entries). Write the bump in the UPDATE (`version = version + 1`) with a short comment, on every write that changes what the record's edit form or page shows, and say at a deliberate non-bump why (stock quantity and odometer readings, which `quantity_was`/`odometer_was` guard). Check a version after the row lock the function already takes: `lockedVersion` after a `SELECT … FOR UPDATE`, or `versionedUpdate` for a single `UPDATE … AND version = $n`. Don't add a lock for it. `restoreRows` gives restored rows a new version.
- A store function behind a create or edit form runs in `inFormTx` with the form's key (`stock.*Input.FormKey`): the claim in `form_submissions` is the transaction's first statement, before any version check or lock, so it holds only its own new row and can't change the lock order. Don't claim a key anywhere else in the transaction, and don't run a form's work in two transactions.
- Archived things take no new work, and the store enforces it inside the transaction, not just the pages: no stock changes to an archived item (a service's own parts move with it when it is edited, deleted or restored), no new services or kits for an archived vehicle, no newly chosen archived kit. Bringing back a deleted past service or kit of an archived vehicle from the trash is allowed, as it isn't new work. Pages hide what the store refuses: the services page of an archived vehicle has no New kit or Log service, and its kits show no reminder. Kit lines may name archived items.

## Domain

- Quantities are stored in base units (pieces, ml, g) and converted only at the edges (`stock.ParseAmount`, `stock.FormatQuantity`, `stock.SplitForInput`).
- Pure rules (validation, grouping, due dates, suggestions, exports, how a new item's starting stock is recorded, plausible dates) live in `internal/stock`, have no I/O and are unit tested.
- Money figures (last price, stock value, spending) read only `purchase` movements. Anything that adds stock the user paid for must be recorded as a purchase, not as `initial` or `correction`.
- Inputs are cleaned with `Normalize()` and checked with `Validate()`, which returns `stock.Errors` (field → message). Text is limited to 200 characters (4,000 for notes on items, vehicles, kits and services); an over-long field without its own error slot reports on the form's summary (`limitText`).

## Web

- Every page is server-rendered and every form works without JavaScript (POST, then a 303 redirect with a flash message). htmx and `static/app.js` are progressive enhancements only.
- Invalid forms re-render with status **422**, keeping exactly what the user typed, with messages next to the fields.
- Every edit form and every button that acts on a record (delete, archive, restore, Bought, link, remove) carries the record's version in a hidden `version` field. A stale edit form (`store.StaleError`) re-renders with 422, keeping what was typed, with the banner "This … was changed since you opened this (another tab or person). Reload to see the latest, then make your change again." and a Reload link (`staleForm`, the `stale` partial). When the record was deleted, it says so and links to Recently deleted. A stale button changes nothing: it flashes the reason and redirects to the record as it is now, or to Recently deleted (`staleButton`). A form whose record was deleted meanwhile never 404s.
- Test forms and buttons over HTTP in `internal/web` (open the page, read the version from the rendered form, change the record in another request, submit), including Undo and Restore paths.
- Every form that creates or changes something carries a one-time key (`{{template "form-key" .FormKey}}`, a hidden `form_key`; the item page's three stock forms each have their own). A 422 re-render keeps the posted key; a fresh page gets a new one. The handler reads it with `parseFormKey`, calls `savedBefore` before it looks at anything the first save may have changed (the version, a ticked-off entry), and maps the store's `AlreadySavedError` with `savedAlready`: nothing is saved again, and it redirects where the first save did with "This was saved already (the first Save went through), so it wasn't saved again. Changes made in the form since weren't applied; check it here." When that record was deleted since, it says so and goes to Recently deleted. A form without a key (a page opened before keys existed) is not saved: 422 with "This form was opened before Pitbox was updated, so it was not saved. Check it and save again." and a fresh key, since it can't be told from a repeat of itself, and saving it blindly could take a service's parts twice.
- The submit guard in `app.js` locks a POST form once it is sent (htmx forms and the quick +/- excepted). A form with a key says "Saving…" on the button it was sent with; still on the page after 15 seconds, it unlocks and says "Still saving… try again", which is safe because of the key. Coming back with Back (`pageshow`) unlocks and restores the label. Without JavaScript there's no guard, and the key alone keeps a double click from saving twice.
- Test repeats over HTTP too (`formkeys_test.go`): two posts with one key at once (forced to overlap with a held lock), a repeat after the save, after a failed save, of an edit after its own save (not stale), and after Undo.
- Never turn a blank field into a destructive value: a blank quantity on the edit form is an error, not 0.
- Parse dates of things that already happened (purchases, stock changes, services) with `parseEventDate`, which applies `stock.CheckEventDate`. Best-before dates use `parseOptDate`; they can be far in the future.
- Templates: pages in `templates/pages`, reusable blocks in `templates/partials`, helpers in `render.go`. Use `withQuery` for URLs built from encoded query strings.
- No external CDNs or fonts at runtime. Vendor assets under `static/vendor` with their licence files. (The Lucide icons are inlined as a sprite in `templates/partials/icons.html`; their licence is in `static/vendor`.)
- The layout must work from 360 px phone width up. The e2e suite fails if any page scrolls sideways.
- Accessibility: real `<label>`s for inputs, buttons for actions, links for navigation, `aria-label` on icon-only buttons.
- Form hints: a hint about one field goes behind a "?" next to its label (`{{template "tip"}}` inside a `.label-row`, with the input's `aria-describedby` pointing at the tip's ID). A hint about a whole section stays visible as `<p class="hint">` under the section heading. Never put a "?" on a heading.

## Code style

- `gofmt`, `go vet` and `staticcheck` must be clean (`scripts/check.sh`).
- Exported identifiers get a short doc comment. Comments explain *why*, not *what*.
- Prefer the standard library. Add a dependency only when it clearly pays for itself.
- Tests are table-driven where it helps. DB tests assert the ledger invariant after changing stock.

## Commits

- Small, focused commits with imperative subject lines ("Add shopping list suggestions").
- Run `scripts/check.sh` (and the e2e tests for UI changes) before committing.
