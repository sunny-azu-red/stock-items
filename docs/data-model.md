# Data model

The schema is created by [`internal/db/migrations`](../internal/db/migrations). Migrations are applied automatically when the app starts. Goose records them in `goose_db_version`.

```
vehicles ─┬─< item_vehicles >─┬─ items ─┬─< item_skus
          │                   │         ├── item_photos (1:1)
          │                   │         ├─< stock_movements >── service_records
          ├─< service_kits ─< service_kit_lines >─┘                   │
          └─< service_records ────────────────────────────────────────┘
items ─< shopping_entries (or free text)
trash (JSON snapshots of deleted rows; no foreign keys)
```

## Tables

**vehicles:** name (unique, case-insensitive), kind (`motorcycle`/`car`/`other`), make, model, year, plate, VIN, chip colour, `odometer_km`, notes, `archived_at`.

**items:** name, brand, category, `unit` (`piece`/`ml`/`g`), `quantity`, `min_quantity` (low-stock threshold), `expires_on`, location, notes.
- `quantity >= 0` is enforced by a check constraint.
- Quantities are always in the base unit: 1.5 L is stored as `1500`.
- `archived_at` is set when an item that other records refer to is archived instead of deleted (see below).
- `expires_on` is set on the item form, or replaced by a purchase that gives the new batch's best-before date (in the same transaction as the movement).

**item_skus:** many part numbers per item: `code`, `kind` (`oem`/`manufacturer`/`ean`/`retailer`/`other`; `ean` codes must be a GTIN-8/12/13/14 with a valid check digit), `label` (e.g. "4 L can"), `pack_size` (base unit). Codes are unique per item.

**item_vehicles:** many-to-many tags.
- A vehicle with services or kits can't be deleted (`store.DeleteVehicle` returns a `ConflictError`); it is archived instead (`ArchiveVehicle`): it leaves the vehicle lists, pickers, filters and the sidebar, and its kits leave reminders and suggestions (`ListKits` skips them unless asked by id or vehicle), but its services, kits, labour costs and item tags stay. It takes no new services or kits, and no kit moves to it (`checkActiveVehicle`); restoring a deleted service or kit of it waits until it is restored too. `ListVehicles` returns archived vehicles too, flagged, for chips and lookups. A vehicle without either is deleted, which only removes its tags.
- Older versions deleted vehicles with history, cascading to their kits and services; the parts those services used stayed in the ledger with `service_record_id` cleared and the service title as note, and can be edited like any stock change.
- An item with no tags is "General" stock.

**Deleting an item:** the foreign keys on `item_id` cascade, so a delete would also remove its movements, kit lines and shopping entry. `store.DeleteItem` therefore refuses an item with purchases, service movements or kit lines (`store.ItemUsage`) with a `ConflictError`. Such items are archived instead (`ArchiveItem` sets `archived_at` and moves its shopping entry to the trash). `ListItems` hides archived items unless `ItemFilter.Archived` asks for them; lookups by id (`GetItem`) always find them. Kit lines carry `Archived`: kit readiness, `SuggestPurchases`, the kit shortfall and a service started from the kit skip them. Archived items take no stock changes (nor edits of their quantity or history; `applyMovement`, `lockMovement` and restoring a stock change refuse them in the transaction). A service's own parts move with it: editing it may keep an archived part as it was (`checkArchivedParts`), and deleting or restoring it gives back or takes again what it used. Kit lines may name archived items (pickers just don't offer them). Archived items can't go on the shopping list (`AddShopping`/`EnsureShopping` return `ErrNotFound`), and the vehicles' item counts leave them out.

**item_photos:** one photo per item, stored as JPEG (`image` up to 1600 px, `thumb` 320 px square).

**stock_movements:** the ledger.
- `kind`: `initial` (stock already on the shelf when the item was created), `purchase`, `use`, `correction` (count/edit/quick +), `service`.
- A new item's starting stock is a `purchase` when a price or shop is entered on the form (`stock.ItemInput.StartMovement`), otherwise `initial`. Only priced purchases feed the last price, stock value and spending.
- `delta`: signed, in base units, rounded to three decimals before it is applied.
- Optional `total_price` and `supplier` (purchases only: `stock.MovementInput.Validate`), `note`, `occurred_on`. `occurred_on` must be between 1990-01-01 and tomorrow (`stock.CheckEventDate`).
- `service_record_id` is set for parts used by a service.

**service_kits:** a named list of parts for a recurring job on one vehicle, with optional `interval_km` / `interval_months`, `notes` and `archived_at`. A kit with logged services can't be deleted (that would set their `kit_id` to NULL and lose the kit's history); it is archived instead, which takes it out of `ListKits` (unless `IncludeArchived`), reminders (`Kit.Due` is `none`) and suggestions.

**service_kit_lines:** item and needed quantity. Each item appears at most once per kit (rows of the same item on the form are added up, at most `stock.MaxQuantity`), and an index on `item_id` serves "which kits use this item".

**service_records:** a performed job: date, odometer, title, notes, labour cost, optional kit. `odometer_moved` says whether logging it moved the vehicle's odometer forward (NULL for services logged before the column existed), and `odometer_before` is the reading it moved from (NULL if the vehicle had none). `odometer_at_log` is the vehicle's reading when a service without its own reading was logged (NULL otherwise, and for services logged before the column existed); km reminders count from it if the service was logged (`created_at`) within a week of `performed_on`.

**trash:** what was deleted and can be restored: `kind` (`service`, `kit`, `movement`, `shopping`, `item`, `vehicle`, `photo`, `bought`), a `label`, and `payload`, a JSON snapshot of every row the delete removed (`{"table": [rows…]}`; for `bought` the entry as it was plus the purchase's `movement_id`). Written in the same transaction as the delete (`store/trash.go`), kept until the trash is emptied or the entry is deleted for good. `RestoreTrash` re-inserts the rows with their original ids (`jsonb_populate_recordset`) and redoes their stock effect under a lock, refusing to go below zero; a reference to a row that is itself in the trash refuses the restore until that one is restored (`needRow`: "Restore Item: X first"), so the order of restores never loses a link; references deleted for good are dropped (tags, kit lines, a service's kit) or refuse the restore (a service's vehicle or parts). Only the snapshot's columns are inserted, so columns added later get their defaults. A restore reports how many links it dropped (`Restored.Dropped`). `PurgeTrash` also deletes the entries that need a purged item or vehicle (for an item: deleted stock changes, photos, list entries and services that used it; for a vehicle: its deleted services and kits), since they could never be restored. A GIN index on `payload` serves the containment lookups (`payload @> {"items": [{"id": 5}]}`).

**shopping_entries:** linked to an item (at most one entry per item) or free text, with an optional quantity and note. `unit` is the unit of a free-text entry's quantity (`stock.ParseAmountAnyUnit` picks it from the typed suffix); linked entries use the item's unit.
- Marking a linked entry bought records a purchase. If less was bought than the entry's quantity, the entry stays with the rest (`store.MarkBought`).
- A free-text entry can be linked to an existing item with the same name (`store.ItemsNamed`, `store.LinkShopping`): the item is restored if archived, and the entry is merged into the item's own entry if it has one.
- A free-text entry can become a new item: `store.CreateItemFromShopping` creates the item with its starting purchase and deletes the entry in one transaction.

## Invariants

1. `items.quantity` always equals `sum(stock_movements.delta)` for the item. Every change goes through `store.applyMovement` inside a transaction, and `store_test.go` asserts this after each scenario. The exceptions change recorded movements together with their effect, so the sum still holds:
   - deleting or editing a service (5);
   - editing or deleting a single movement (`store.UpdateMovement`/`DeleteMovement`, see 6);
   - restoring from the trash (rows come back together with their quantity change), including undoing a Bought.
2. A movement can never make a quantity negative. You get `InsufficientStockError` instead, and the transaction rolls back. A service with one missing part logs nothing.
3. An item's unit cannot change once it has stock history, is used in a kit, or has a quantity on the shopping list, counting what is in the trash, which would come back in the old unit.
4. Logging a service moves the vehicle odometer forward. It never moves it backwards. Deleting a service that moved it (`odometer_moved`) puts it back to the highest of `odometer_before` and the vehicle's other services' readings, or to no reading if there are none, unless the odometer was changed since. Services that didn't move it, and services logged before `odometer_moved` existed, leave it alone. A vehicle's reading can't be changed to less than its highest service reading, and a service's reading must fit between the services logged on earlier and later days (`checkVehicleOdometer`, `checkServiceOdometer`; service changes hold the vehicle's lock, so two saved at once take turns), also when a deleted service is restored.
5. Deleting a service deletes its `service` movements and puts the quantities back on stock. Editing a service (`store.UpdateService`) does the same for its old parts, takes the new ones, and redoes its odometer change, in one transaction; its vehicle can't change.
6. A single movement other than `service` can be edited (amount, price, shop, date, note) or deleted; the item's quantity moves by the difference. The change is refused if the quantity would go below zero. A `correction` is edited as a signed change (added or took away, and how much), and `initial` stock that gets a price or shop becomes a `purchase` (`stock.ApplyEdit`).

## Derived values (not stored)

| Value | Computed in |
|---|---|
| Low stock | `quantity < min_quantity` (`stock.Item.IsLow`) |
| Expiring | stock left and `expires_on` past or within 60 days of the app's date (`stock.Item.ExpiryState`, `store.ItemFilter.Expiring` with `Today`) |
| Kit readiness and shortfall | `stock.Kit.Readiness`, `KitLine.Short` |
| Service due state (`none`/`never`/`unknown`/`ok`/`soon`/`overdue`) | `stock.Kit.Due`: last service with the kit + interval, compared to the current odometer and today. `unknown` means a km-only interval that can't be worked out: no current odometer, or no reading to count from. Km count from the last service's reading; if it was logged without one, from the vehicle's reading at the time (`odometer_at_log`, if logged within a week of the service); otherwise (logged late, before that was kept, or with no reading on the vehicle) from the latest service with the kit that has a reading. "Soon" means within 30 days or max(500 km, 10 % of the interval), but never more than half the interval (for days as for km); exactly at the due date or km is still "soon", one day or km past it is "overdue". Month intervals end on the same day of the month, or the month's last day (31 Jan + 1 month = 28 Feb). History logged with the kit on another vehicle is ignored. |
| Purchase suggestions | `stock.SuggestPurchases`: below minimum or short for any kit, and not already on the list |
| Stock value | quantity × last purchase price per unit, for items that aren't archived (`store.GetStats`) |
| Purchase count (item page) | `store.CountPurchases` |
| Spent in the last 12 months | priced purchases plus labour cost dated in the 12 months up to and including the app's date (`store.GetStats`); a purchase dated tomorrow counts from tomorrow |
