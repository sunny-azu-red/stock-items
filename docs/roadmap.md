# Roadmap

Ideas to come back to, with why they were left for later. Nothing here is decided or promised; an idea moves into the app (and out of this list) when it is built.

## Spending: items, labour and the total

**Now:** the dashboard's "Spent in YEAR" tile counts only what was paid for items (priced purchases, `store.GetStats`), and opens that year's purchases in Activity, so the tile and its list add up. Labour costs are shown on the Services page (the service log's "labour in total", each vehicle's service history), not on the dashboard. Decided on 2 November 2026: the dashboard's tiles are about the items.

**Idea:** one place that shows what the vehicles cost, all together:

- items + labour = total, for the year (and perhaps per vehicle);
- a bigger card than the tiles, or a page of its own, so the three figures and their parts fit, each opening its list (purchases in Activity, services with labour in the service log);
- later perhaps per month, or this year against last.

Before building it, decide whether parts taken from stock for a service count as that service's cost (they were already counted when bought), so nothing is counted twice.

## Kinds of stock change: one name in the database, the code and the pages

**Now:** the pages say Bought, Used, Counted, Service and Starting stock (`stock.MovementKind.Label`), but the stored values are still `purchase`, `use`, `correction`, `service` and `initial`, and so are the Go constants (`MovePurchase`, `MoveUse`, `MoveCorrection`, `MoveInitial`). Anyone reading the code or the database translates between the two. Noted on 2 November 2026, when the labels were renamed without a migration.

**Idea:** rename the stored values and the code to the words the pages use (for example `bought`, `used`, `counted`, `service`, `starting`), so a kind reads the same everywhere and `Label` only capitalises. What it touches, so nothing is missed:

- a migration: update `stock_movements.kind`, and swap its CHECK constraint (`00001_init.sql`), in one transaction, with a Down that maps back;
- the trash: the snapshots of deleted stock changes (`trash.payload`) carry the old kind values, and Undo restores them as they are, so the migration rewrites them too (or `RestoreTrash` maps old to new);
- SQL in `internal/store` that names kinds (`kind = 'purchase'` in the money figures, the service parts' CHECK in `00014`);
- URLs and forms: `?kind=purchase` (Activity, the dashboard's Spent link), the item page's hidden `kind` field; old bookmarks could be redirected;
- the CSV's `kind` column, which spreadsheets may read: say so in the release notes;
- the Go constants and every test that names a kind.

Worth doing in one go, after a backup, with an e2e test that undoes a stock change deleted before the migration.
