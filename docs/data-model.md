# Data model

The schema is created by [`internal/db/migrations`](../internal/db/migrations). Migrations are applied automatically when the app starts. Goose records them in `goose_db_version`.

```
vehicles ─┬─< item_vehicles >─┬─ items ─┬─< item_skus
          │                   │         ├── item_photos (1:1)
          │                   │         ├─< stock_movements >── service_records
          ├─< service_kits ─< service_kit_lines >─┘                   │
          └─< service_records ────────────────────────────────────────┘
items ─< shopping_entries (or free text)
```

## Tables

**vehicles:** name (unique, case-insensitive), kind (`motorcycle`/`car`/`other`), make, model, year, plate, VIN, chip colour, `odometer_km`, notes.

**items:** name, brand, category, `unit` (`piece`/`ml`/`g`), `quantity`, `min_quantity` (low-stock threshold), `expires_on`, location, notes.
- `quantity >= 0` is enforced by a check constraint.
- Quantities are always in the base unit: 1.5 L is stored as `1500`.
- `expires_on` is set on the item form, or replaced by a purchase that gives the new batch's best-before date (in the same transaction as the movement).

**item_skus:** many part numbers per item: `code`, `kind` (`oem`/`manufacturer`/`ean`/`retailer`/`other`; `ean` codes must be a GTIN-8/12/13/14 with a valid check digit), `label` (e.g. "4 L can"), `pack_size` (base unit). Codes are unique per item.

**item_vehicles:** many-to-many tags.
- Deleting a vehicle removes its tags, but its items stay. Its kits and service records are deleted too. The parts those services used stay used: their `service` movements remain in the ledger (with `service_record_id` cleared and the service title as note).
- An item with no tags is "General" stock.

**item_photos:** one photo per item, stored as JPEG (`image` up to 1600 px, `thumb` 320 px square).

**stock_movements:** the ledger.
- `kind`: `initial` (stock already on the shelf when the item was created), `purchase`, `use`, `correction` (count/edit/quick +), `service`.
- A new item's starting stock is a `purchase` when a price or shop is entered on the form (`stock.ItemInput.StartMovement`), otherwise `initial`. Only priced purchases feed the last price, stock value and spending.
- `delta`: signed, in base units, rounded to three decimals before it is applied.
- Optional `total_price`, `supplier`, `note`, `occurred_on`. `occurred_on` must be between 1990-01-01 and tomorrow (`stock.CheckEventDate`).
- `service_record_id` is set for parts used by a service.

**service_kits:** a named list of parts for a recurring job on one vehicle, with optional `interval_km` / `interval_months`.

**service_kit_lines:** item and needed quantity. Each item appears at most once per kit.

**service_records:** a performed job: date, odometer, title, notes, labour cost, optional kit.

**shopping_entries:** linked to an item (at most one entry per item) or free text, with an optional quantity and note. `unit` is the unit of a free-text entry's quantity (`stock.ParseAmountAnyUnit` picks it from the typed suffix); linked entries use the item's unit.
- Marking a linked entry bought records a purchase. If less was bought than the entry's quantity, the entry stays with the rest (`store.MarkBought`).
- A free-text entry can become a new item: `store.CreateItemFromShopping` creates the item with its starting purchase and deletes the entry in one transaction.

## Invariants

1. `items.quantity` always equals `sum(stock_movements.delta)` for the item. Every change goes through `store.applyMovement` inside a transaction, and `store_test.go` asserts this after each scenario. The one exception is deleting a service (5), which removes movements together with their effect, so the sum still holds.
2. A movement can never make a quantity negative. You get `InsufficientStockError` instead, and the transaction rolls back. A service with one missing part logs nothing.
3. An item's unit cannot change once it has stock history, is used in a kit, or has a quantity on the shopping list.
4. Logging a service moves the vehicle odometer forward. It never moves it backwards.
5. Deleting a service deletes its `service` movements and puts the quantities back on stock.

## Derived values (not stored)

| Value | Computed in |
|---|---|
| Low stock | `quantity < min_quantity` (`stock.Item.IsLow`) |
| Expiring | stock left and `expires_on` past or within 60 days (`stock.Item.ExpiryState`, `store.ItemFilter.Expiring`) |
| Kit readiness and shortfall | `stock.Kit.Readiness`, `KitLine.Short` |
| Service due state (`never`/`ok`/`soon`/`overdue`) | `stock.Kit.Due`: last service with the kit + interval, compared to the current odometer and today. "Soon" means within 30 days or max(500 km, 10 % of the interval), but never more than half the interval. Month intervals end on the same day of the month, or the month's last day (31 Jan + 1 month = 28 Feb). History logged with the kit on another vehicle is ignored. |
| Purchase suggestions | `stock.SuggestPurchases`: below minimum or short for any kit, and not already on the list |
| Stock value | quantity × last purchase price per unit (`store.GetStats`) |
| Purchase count (item page) | `store.CountPurchases` |
| Spent in the last 12 months | priced purchases plus labour cost with a date in the last 12 months (`store.GetStats`) |
