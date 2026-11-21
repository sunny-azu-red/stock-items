# Roadmap

Ideas to come back to, with why they were left for later. Nothing here is decided or promised; an idea moves into the app (and out of this list) when it is built.

## Spending: items, labour and the total

**Now:** the dashboard's "Spent in YEAR" tile counts only what was paid for items (priced purchases, `store.GetStats`), and opens that year's purchases in Activity, so the tile and its list add up. Labour costs are shown on the Services page (the "Labour in total" footer of Service history, there and on each vehicle's page), not on the dashboard. Decided on 2 November 2026: the dashboard's tiles are about the items.

**Planned design & details:** one place that shows what the vehicles cost, all together:

- **Formula & no double-counting:**
  - Total Spending = Items bought + Service labour.
  - Parts taken from stock for a service were already paid for when bought (`kind = 'bought'`), so they are *never* counted twice. A service's parts cost is displayed on the service detail page for insight into job cost, but garage-wide and vehicle spending summaries add only `Priced purchases + Service labour`.
- **Per-vehicle attribution** (not yet what the dashboard shows while working on a vehicle: there "Stock value" and "Spent" count the items its stock list shows, its own, shared and General, so each tile adds up to the list it opens; revisit that when this is built):
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

## A vehicle for free-text shopping entries

**Now:** which vehicles a shopping entry is for is worked out from its item's tags and the kits that use it (`stock.ShoppingEntry.For`), never stored. A free-text entry ("Chain lube") has no item, so it is on every vehicle's part of the list, like General stock. Decided on 16 November 2026: storing a vehicle on entries would need a link table (an item has one entry, and a shared item two vehicles need is one line), with its own trash, version and lock rules, and it would go stale when an item is retagged.

**If it is wanted:** a nullable `vehicle_id` on free-text entries only, set when one is added while working on a vehicle, would scope them too. Deleting a vehicle would then set it to null (and restoring the vehicle wouldn't bring the link back), and the Bought flow that turns free text into an item would carry it over as the item's tag.

## Old shopping notes that name a vehicle

Notes saved before 16 November 2026 read "Short for GreyJoy: Air Filter"; new ones say "Short for the Air Filter kit" and leave the vehicle to the chips. Old notes are saved text and were left as they are; rewriting them would be a data migration on user-editable text, for a few entries that go away when bought.

## The web platform: what the redesign left for later

The November 2026 redesign targets Baseline 2025 in current Chrome and Firefox ([AGENTS.md](../AGENTS.md#browser-support-and-the-web-platform)) and uses HTML and CSS before JavaScript. A few things stayed in `app.js` or as they were, waiting on a browser or on a better design:

- **Hints that open on hover without a script.** `interestfor` (a popover shown while its button is hovered or focused) would replace the hints' hover code, and CSS anchor positioning their placement; until both are in Chrome and Firefox, `app.js` does these few lines.
- **Toasts as popovers.** A `popover="manual"` toast would sit in the top layer above the sheets, but a live region that is shown rather than changed isn't announced reliably by screen readers. They stay a plain `role="status"` box.
- **Focus on the first error from the server.** An `autofocus` on the first invalid field would need every form's handler to know its field order; two lines of `app.js` do it now.
- **Capitals trimmed with `text-box`.** `text-box: trim-both cap alphabetic` (in Chrome and Firefox) would centre text in its box at any font size, but its boxes are a fraction of a pixel tall (.735em), which the whole-pixel rule forbids. The type scale uses the sizes both browsers centre instead ([design.md](design.md#text-centred-in-its-box)).
- **Tables as cards on a phone.** Activity and a kit's parts keep their columns at 640px and scroll inside their card on a narrow screen. A card per row would read better on a phone; it needs its own markup for the same data, so it waits until the tables are used more there.
