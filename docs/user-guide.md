# User guide

## First steps

1. **Add your vehicles** (Vehicles → Add vehicle). Give each a short name such as "KTM 390" and a colour. That name and colour are used everywhere. Enter the odometer if you want km-based reminders.
2. **Add your items** (Add item, or press `n`):
   - **Name and brand.** "OEM" works fine as a brand for original parts.
   - **Fits these vehicles.** Click every vehicle the part fits. Leave all of them unselected for general stock (cleaners, grease, tools).
   - **Counted in.** Pieces, volume (ml / L) or weight (g / kg). Choose once; it can't be changed after stock changes are recorded, or once the item is in a kit or on the shopping list with a quantity.
   - **On stock.** The amount you have right now, e.g. `525` ml or `1.5` L.
   - **Bought it just now?** (new items only, optional). Enter the total price and/or the shop, and the date if it wasn't today. The starting stock is then recorded as a purchase, so the item shows a last price, a purchase count, and counts in the stock value and spending. Leave price and shop empty for stock you already had; it is recorded as *Initial stock* on the date given.
   - **Keep at least** (optional). Below this the item shows as *low* and is suggested on the shopping list.
   - **SKUs.** Add every number you might search for: the OEM part number, the manufacturer number, one row per pack size.
   - Use **Save & add another** to enter many items in a row. It keeps the vehicles and unit you picked.
   - **Duplicate** (on an item's page) starts a similar item with the same fields, part numbers and vehicles, but no stock, no best-before date and no barcodes, so scanning still finds the original.

## Daily use

- **Stock page.** Click vehicle chips to filter. With two or more vehicles selected, choose *fits any* or *fits all*. Search matches name, brand, location, notes and any SKU, with or without dashes and spaces.
- **−/+ buttons** change stock by one step: 1 piece, 100 ml or 50 g. For exact amounts, open the item:
  - **Bought**: amount, total price, shop and date. Prices give you a stock value and spending totals. **Best before** (optional) replaces the item's date with the new batch's, so an expired item stops showing as expired. Leave it empty to keep the current date.
  - **Used**: take an amount from stock.
  - **Count**: set the stock to what is actually on the shelf.
- **Fix a mistake in the history.** Every row in an item's history (and on the Activity page) has a pencil: change the amount, price, shop, date or note, or delete the entry. The stock moves by the difference, so a wrong price no longer sticks in the last price, stock value and spending. If the stock was used in the meantime, a change that would take away more than is left is refused; record the use or do a count first. Parts used by a service are fixed by editing the service (or, if the vehicle and so the service are gone, directly). A count (including quick +) is edited as a change: whether it added or took stock away, and by how much. Quick − is recorded as a use.
- **Copy as text** copies the current view in the plain format from the original request. That's handy for chats and notes.
- **CSV** downloads the current view for a spreadsheet: quantities, SKUs (with label and pack size), and for items bought with a price the last price (per piece, L or kg), the stock value at that price, and the date and shop of that purchase.
- **Activity → CSV** downloads every stock change (or only the kind you filtered on) with amounts, prices, shops and services, e.g. for your own spending overview.
- **Expiring** (dashboard tile and stock filter) lists items with stock left that are past or within 60 days of their best-before date. Used-up items don't show there.
- To edit an item's amount, type `0` if there is none left. A blank field is refused so the stock is never emptied by accident.
- Dates of purchases, stock changes and services can't be in the future (tomorrow is allowed for time zones) or before 1990. That keeps a year typo from becoming the last price or counting in the spending.

## Service kits and reminders

A **kit** is the parts list of a recurring job on one vehicle, e.g. *KTM 390 – Oil service*: 1× oil filter, 1.7 L oil, 2× O-ring.

On kit and service part rows, pick the unit next to the quantity: choosing an item offers its units and selects L or kg for liquids and weights, so `1.7` means 1.7 L. A unit typed into the quantity (`1700 ml`) wins. Each row shows what is on stock.

- The kit page shows, per part, what you need, what you have and what is missing.
- **Add missing to shopping list** puts exactly the shortfall on the list.
- Set **Repeat every** km and/or months to get reminders on the dashboard: *On track*, *Due soon* or *Overdue*.
- **Do this service** opens the service form with the kit's parts filled in. Adjust quantities, remove what you didn't use, enter the odometer and save. The parts are taken from stock and the vehicle's odometer is updated. If parts are short, every short row is marked and nothing is saved.
- Made a mistake? **Edit** the service (on its page): change the date, odometer, title, labour, notes or parts. The old parts go back on stock and the new ones are taken; if a part is short, nothing changes. Or delete the service. Its parts go back on stock, and if it moved the vehicle's odometer (and you haven't changed the odometer since), the odometer goes back too.

You can also log a service without a kit (Services → Log service), e.g. for a one-off repair.

## Shopping list

- Add items from your stock (with a quantity) or free text ("chain lube"). Pick the unit next to the quantity, or type it (`4 L`, `500 g`); for an item from your stock the units follow the item. A free-text entry keeps its unit, and adding it to stock later starts the new item in that unit. Text typed next to a chosen item is kept as its note. Adding an item that's already listed adds to its quantity and keeps both notes.
- **Suggestions** list items below their minimum and parts missing for any kit, as long as the list asks for less than that (it then shows how much is already listed). Add them one by one or all at once. **Add to shopping list** on an item's page uses the same amount.
- **Bought** on an entry for a stock item records the purchase (amount, price, shop, date, optional best before). If you bought less than listed, the rest stays on the list; otherwise the entry is removed.
- **Bought** on a free-text entry asks what to do. If an item with that name is already on stock (or archived), it is offered first: **Add to this item** links the entry to it (restoring it if archived) and opens the usual Bought form. Otherwise:
  - **Add to stock as a new item** opens the new-item form with the name, quantity and note filled in. Add the price and shop under *Bought it just now?*. Saving creates the item with its purchase and removes the entry.
  - **Just tick it off** removes the entry, for things you don't keep on stock.
- Suggestions keep their reason ("Below minimum", "Short for KTM 390: Oil service") as the entry's note.
- **Copy as text** gives you the list for the shop.

## Removing items and vehicles

- An item that nothing else refers to can be **deleted** (on its page), together with its own history.
- An item that has purchases, was used in a service, or is part of a kit gets **Archive** instead. Deleting it would change those records: your spending, the parts of past services, the kit. An archived item disappears from the stock list, dashboard, item pickers and suggestions, and comes off the shopping list. Its history, services and kits stay as they were. Archived kit lines are marked so you can edit the kit; they don't make the kit "short" and aren't taken when you do the service. An archived item takes no purchases, uses or counts, and its stock and history can't be edited, until you restore it.
- Find archived items with the **Archived** filter on the stock page, and bring one back with **Restore**.
- Vehicles work the same way: a vehicle with services or kits gets **Archive vehicle** (on its page) instead of Delete. It leaves the vehicle lists, pickers and reminders; its services, kits, labour costs and item tags stay. Archived vehicles are listed from the link under the vehicles; **Restore vehicle** brings one back.
- The odometer can't be set lower than a logged service's reading, or cleared once services have readings: fix or delete that service first. Saving the vehicle's edit form keeps a reading entered elsewhere in the meantime, unless you changed the odometer field. A service's reading also has to fit between the services logged before and after it.

## Tips

- Photos help to recognise similar parts: open an item → Add photo. Phone photos are rotated and resized automatically.
- A USB or Bluetooth barcode scanner "types" into the search box. Put EAN codes in the SKU list and scanning finds the item.
- Shortcuts: `/` searches stock from any page, `n` opens a new item.
- The app follows your system's light or dark mode. The sun / moon button (top of the sidebar; on a phone, top of the dashboard) overrides it on that device. Switch back to what your system shows to follow it again.
- The *Activity* page lists every stock change across all items, 200 per page (**Older** / **Newer**), filtered by kind if you like. An item's page shows its newest 100 changes; **All entries →** opens its whole history there. The CSV button downloads what the page is filtered on.
- The services list (100 per page) and each vehicle show the labour cost of every service and the total.
