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

**Also: Counted becomes Adjusted, everywhere.** "Counted" fits the item page's count (set the stock to what is on the shelf), but quick + and an edited count are recorded under it too, and those are adjustments, not counts. Adjusted reads right for all of them. So the stored value above becomes `adjusted`, not `counted`, and the pages change with it:

- the label (`stock.MovementKind.Label`), Activity's chip, the item page's tab (Count → Adjust) and its form (the legend "The count", "Counted on"), the flash "Counted: … in stock", and "Stock already matches the count";
- the words: the table in [conventions.md](conventions.md#words) and the line in `AGENTS.md`, with Counted moved to the retired forms so `TestVocabulary` catches any left over;
- the user guide, the README, the tests that name it (`internal/web/formkeys_test.go`, `TestEditACount`) and the screenshots;
- not "Counted in" on the item form: that is the item's unit, another thing, and keeps its words.

## Small things noted on 2 November 2026

- **Long amounts on a phone's stock list.** The quantity column is 58px on a phone (`.item-row`, `app.css`), and an amount like "3 825 ml" needs about 63px, so it runs into the gap beside it. It was like this before the consistency pass. A wider or content-sized column moves every row, so choose it by looking at real lists: a wider fixed column, a smaller font for long amounts, or the unit on a second line.
- **A purchase dated tomorrow.** Activity lists it at once, while "Spent in YEAR" counts it only from its date (`store.GetStats` stops at today). That is meant, but the two can disagree for a day. Either leave it, or keep tomorrow's purchases out of the Spent link's list too.
- **The e2e "pick a year" test at New Year's midnight.** It takes the year from the test machine's clock (`new Date().getFullYear()`), the app from its own; a run that crosses midnight on 31 December could fail once. Read the year the page shows instead.
