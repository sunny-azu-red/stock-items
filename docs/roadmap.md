# Roadmap

Ideas to come back to, with why they were left for later. Nothing here is decided or promised; an idea moves into the app (and out of this list) when it is built.

## Spending: items, labour and the total

**Now:** the dashboard's "Spent in YEAR" tile counts only what was paid for items (priced purchases, `store.GetStats`), and opens that year's purchases in Activity, so the tile and its list add up. Labour costs are shown on the Services page (the service log's "labour in total", each vehicle's service history), not on the dashboard. Decided on 2 November 2026: the dashboard's tiles are about the items.

**Planned design & details:** one place that shows what the vehicles cost, all together:

- **Formula & no double-counting:**
  - Total Spending = Items bought + Service labour.
  - Parts taken from stock for a service were already paid for when bought (`kind = 'bought'`), so they are *never* counted twice. A service's parts cost is displayed on the service detail page for insight into job cost, but garage-wide and vehicle spending summaries add only `Priced purchases + Service labour`.
- **Per-vehicle attribution:**
  - Items tagged exclusively to one vehicle attribute their purchase cost to that vehicle.
  - Items tagged with multiple vehicles or untagged (general garage stock such as cleaners, grease, zip ties, bulk fluid) belong to *Shared & General stock*.
  - A vehicle's total cost = *Items bought for this vehicle + Labour on its services*.
  - Garage total = *All vehicle items + Shared/general items + All service labour*.
- **Surfaces & views:**
  - **Services page (`/services`):** A summary card (e.g. at the top or beside the filter) for the active year (or all-time), showing:
    - *Items bought* (links to `/activity?kind=bought&year=YYYY`)
    - *Service labour* (links to `/services?v=...`)
    - *Total* (`Items + Labour`)
  - **Vehicle page (`/vehicles/{id}`):** Under the Facts/Odometer column, a Spending breakdown for that vehicle (items purchased for it + total labour recorded on its services = vehicle total).
  - Filterable by year (matching the Activity and Services year filters) and all-time.
- **Future extensions:**
  - Month-by-month spending bar chart or breakdown;
  - Year-over-year comparison (this year vs last year).

## Small things noted on 2 November 2026

- **A purchase dated tomorrow.** Activity lists it at once, while "Spent in YEAR" counts it only from its date (`store.GetStats` stops at today). That is meant, but the two can disagree for a day. Either leave it, or keep tomorrow's purchases out of the Spent link's list too.
