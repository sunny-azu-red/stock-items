# Roadmap

Ideas to come back to, with why they were left for later. Nothing here is decided or promised; an idea moves into the app (and out of this list) when it is built.

## Spending: items, labour and the total

**Now:** the dashboard's "Spent in YEAR" tile counts only what was paid for items (priced purchases, `store.GetStats`), and opens that year's purchases in Activity, so the tile and its list add up. Labour costs are shown on the Services page (the service log's "labour in total", each vehicle's service history), not on the dashboard. Decided on 2 November 2026: the dashboard's tiles are about the items.

**Idea:** one place that shows what the vehicles cost, all together:

- items + labour = total, for the year (and perhaps per vehicle);
- a bigger card than the tiles, or a page of its own, so the three figures and their parts fit, each opening its list (purchases in Activity, services with labour in the service log);
- later perhaps per month, or this year against last.

Before building it, decide whether parts taken from stock for a service count as that service's cost (they were already counted when bought), so nothing is counted twice.
