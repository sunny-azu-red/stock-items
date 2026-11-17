# Design & UI Conventions

This document defines the interface standards, component rules, link styles, and layout alignments for Pitbox. Follow these rules whenever creating or modifying templates and styles.

---

## 1. Interactive Links & Affordance

Links must communicate their interactive nature clearly without relying on unstyled browser defaults. We use six link tiers:

| Link Tier | Default Styling | Hover Styling | When to Use |
|---|---|---|---|
| **SKU / Part Numbers (`.sku-link`)** | `font-family: var(--mono); text-decoration: underline dotted; text-underline-offset: 3px;` | `color: var(--accent); text-decoration-style: solid;` | **Exclusively** for part numbers and SKUs. Signals external part number lookup. |
| **Data & Fact Links (`.facts a`)** | `font-weight: 600; text-decoration: underline; text-underline-offset: 3px;` | `color: var(--accent);` | For clickable data values embedded in data lists (e.g. "Last done" date in schedule). |
| **Inline Paragraph Links** | `text-decoration: underline; text-underline-offset: 3px;` | `color: var(--accent);` | Links inside body text, empty states, or hints (e.g. *"No vehicles yet. Add one..."*). |
| **Contextual & Content Links** | `color: inherit; text-decoration: none; font-weight: 600;` | `color: var(--accent);` | Titles in lists (`.list .title`), table items (`.table a`), card links (`.more`). Row or container context already signals clickability. |
| **Reset & Filter Actions** | `color: var(--muted); text-decoration: none; font-weight: 600;` | `color: var(--accent);` | Secondary reset links like `✕ Reset order` (icon on left when right-aligned) or `Clear filters ✕` (icon on right when left-aligned) (`.reset-link`, `.sort-reset`). Never hardcode static accent color on these. |
| **Metadata Actions** | `color: var(--muted); text-decoration: none; font-weight: 600;` | `color: var(--accent);` | `+N more` SKUs (`.sku-more`). |

> [!NOTE]
> Global link rule in `app.css`: `a { color: inherit; text-decoration: none; }` without a global `a:hover { color: var(--accent); }`. Specific content links (`.table a`, `.item-name`, `.title`, `.facts a`, `.more`) and inline paragraph links (`p a`, `.hint a`, `.card-body > a:not(.btn)`) define their styling, while component links (`.btn`, `.chip`, `.badge`, `.tile`, `.vehicle-card`, `.kit-row`) strictly control their own states. Buttons, chips, and badges must never inherit text underlines or container hover colors.
> Vehicle titles in lists (`.list .title.vehicle-title:hover`) highlight with their vehicle color (`var(--chip)`), matching the vehicle icon instead of clashing with accent.
> Secondary metadata in fact definitions (`.facts dd .muted`) uses `<span class="muted small">({{...}})</span>` (`font-weight: 500; font-size: .85rem; color: var(--muted);`) so historical context (e.g. past service odometer readings) does not visually compete with primary figures (e.g. *Odometer now*).

---

## 2. Chips vs. Badges

Chips and badges serve two completely different purposes and have distinct visual styles, but share a standardized height of **26px** so they align side-by-side:

### Vehicle Chips (`.chip`, `a.chip`)
* **Purpose**: Identifies a vehicle or vehicle set.
* **Shape**: Full pill (`border-radius: 99px`), 1px translucent border (`border: 1px solid color-mix(...)`), soft tinted background.
* **Dot**: Contains a `.dot` colored with the vehicle's theme color.
* **Dimensions**: Height **26px** (`font-size: .72rem`, the badges' size, `padding: 3px 9px`, `--lh: 1.5` set on the chip itself so a chip in a heading is 26px too). At `.78rem` its capitals stood 1px nearer the top ([text centred in its box](#text-centred-in-its-box)); the badges' size also keeps a chip and a badge side by side on one baseline. Chips that stand on their own, with no badge beside them (the sidebar's Work on and Stock by vehicle, the Switch tray), are `.82rem` in `padding-block: 2px` instead: still 26px and centred, and easier to read and tap.
* **Hover (as link)**: The 1px border illuminates with the vehicle's color (`border-color: var(--chip)`). **No text underline.**
* **Placement in Rows**: In compound item rows (e.g. `.kit-row`), the vehicle `.chip` sits on Line 1 next to the record title (`.title-row`), leaving Line 2 (`.meta`) as clean, unobstructed metadata text. Inside a vehicle's scope (below), a row leaves out that vehicle's chip.
* **Sidebar Navigation & Filters**: Vehicles in the sidebar (`.nav-vehicles`), filter bars, and tag lists are standard `.chip` / `chip-link` pills, standardizing vehicle tags everywhere.
* **Vehicle Entity Titles**: When a vehicle itself is the primary record of a card or list row (e.g. on `/vehicles` cards and the dashboard "Vehicles" card), it is represented as a title link with its vehicle kind icon (`{{icon .Kind.Icon}}`) colored with `var(--chip)` (`.vehicle-title`, `.vehicle-card .name`), distinguishing primary record titles from secondary reference chips.

#### Scope: say the vehicle once
A page about one vehicle names it once, at the top. Its rows and headings show a chip only for a *different* vehicle, which then says something ("Shared with KTM 1290"). A list that mixes vehicles keeps every chip, because there the chip tells the rows apart.

| Page | Names the vehicle by | Leaves out its chip on |
|---|---|---|
| A vehicle's page | its hero | kit rows, service rows, stock section headings |
| A kit's page | the header `chip-link` | service rows |
| `/services?v=ID` | the title ("KTM 390 services") and the active filter chip; while it is the vehicle worked on, the Working on bar (the title stays plain, the chips are hidden) | service rows, kit rows |
| `/stock?v=ID` (exactly one vehicle) | the title ("KTM 390 stock") and the active filter chip; while it is the vehicle worked on, the Working on bar (the title stays plain, the chips are hidden) | section headings, and list rows that fit only that vehicle |
| `/?v=ID` (the dashboard while working on it) | the Working on bar | Upcoming service's kit rows |
| `/shopping?v=ID` | the title ("KTM 390 shopping") and the active filter chip; while it is the vehicle worked on, the Working on bar | every row's chips: entries and suggestions show none in a vehicle's part, not even a shared entry's other vehicles (the whole list, one chip away, shows them) |
| An item fitting one vehicle | the header chip | the rows of that vehicle's kits |

A stock row (`item-row`) is the one row with several vehicles: it leaves out its chips only when it fits the scope vehicle alone, and otherwise shows all of them, so a shared item never reads as belonging to one of the others alone.

The partials take the scope as `"Here"` (a vehicle id, 0 for none): `stock-section`, `service-list`, and `item-row` (`rowFor`, kept across a quick +/- by a hidden `here` input). `kit-row` takes `kitView.HideVehicle` (`scopeKits`).

**With one active vehicle**, there is nothing to work on (no Work on chips, no bar), and that vehicle is the scope of every page (`soleVehicle`): rows leave out its chip everywhere (services, the dashboard's kits, an item's kits, and stock list rows that fit only it). Headers, section headings, filters and forms keep it, and a row of an archived vehicle or of General keeps its chip.

Stock section headings (`stock-section`) answer one question, which vehicles the items fit, each with an icon and a word up front: `warehouse` **Only** + chip, `tag` **Shared** + chips, `package` **General** (not tied to a vehicle). Inside a scope they read `warehouse` **Only this vehicle** and `tag` **Shared with** + the other vehicles' chips.

### Status & State Badges (`.badge`, `a.badge`)
* **Purpose**: Identifies states, statuses, quantities, kit tags, or categories (e.g. `1 PART MISSING`, `INTERMEDIATE: 1 PART MISSING`, `PARTS READY`, `ARCHIVED`, `OVERDUE`, `ON SHOPPING LIST`, `OUT OF STOCK`, `525 ML IN STOCK`).
* **Category Badge**: Item categories are standard badges (`.badge.badge-muted` with the tag icon). On an item page they sit right after the vehicle chips in the header's `.sub.flex` row, as a plain label; on a stock row they link to the stock filtered by that category (see "When a chip or badge is a link").
* **Shape**: Rounded rectangle (`border-radius: 6px`).
* **Typography**: Bold uppercase text (`font-size: .72rem`, `font-weight: 700`, `letter-spacing: .03em`).
* **Dimensions**: Height **26px** (`padding: 4px 8px`, `--lh: 1.5` on the badge itself: 18px).
* **Hover (as link)**: Container background color transition (e.g. `.badge-muted:hover { background: var(--surface-3); }`, `.badge-accent:hover { background: var(--accent); color: var(--on-accent); }`). **Never use a text underline on badges.**
* **A status that belongs to a button is part of it**, never a badge beside it: beside it, the badge is as far from its button as from the next one, and a wrapped row pairs them wrong. Log service's "Start from a kit" buttons (`.kit-start`) end in the kit's due badge, flush to the button's border behind a 1px divider (the way the sort pill's two halves share an edge), with `Due.Summary` as the button's title. Its text stands on the name's baseline, not centred (`padding-block: 5px 3px`): centred, the smaller uppercase text sits 1px higher, and side by side that reads as crooked. Every kit with a schedule ends in its status, On track too, as everywhere else a kit shows: a plain button then means only "no schedule", and the red and amber ones stand out against the green. A kit without a schedule is just its name.

### The stock badge

How much of an item there is, wherever it shows, is one badge: `{{template "stock-badge" (stockBadge item withQty)}}` (`internal/web/stockbadge.go`). It is never written by hand. Its level comes from `stock.Item.LevelFor(need)`, checked in this order:

| Level | When | State only (`withQty` false) | With quantity (`withQty` true) |
|---|---|---|---|
| `out` | nothing left, whatever is asked | `badge-danger` **Out of stock** | the same |
| `short` | a service's parts ask for more than there is (Log/Edit service only) | not used | `badge-danger`, x icon, **525 ml in stock**, title "Need 1.7 L" |
| `low` | below the item's minimum | `badge-warn`, triangle icon, **Low · min 2 pcs** | `badge-warn`, triangle icon, **1 pcs in stock**, title "Below the minimum of 2 pcs" |
| `ok` | everything else | no badge | `badge-muted` **6 pcs in stock** |

* **State only** where the page shows the quantity already: stock rows (`item-row`) and the item page header.
* **With quantity** where the badge is the only place it shows: an edit of a stock change, the kit and service part rows, the shopping list's suggestions and its "Already have it?" matches.
* **Part rows** (`line-row`): on the service form (`#part-lines[data-stock-need]`) the rows of one item are compared, together, with what is in stock, so a row that saving would refuse is red before Save. The kit form shows out, low or ok only: saving a kit takes nothing from stock. A refused save marks the short rows (`has-error`) without repeating the badge as an error. `app.js` keeps each row's badge up to date as the item, amount or unit changes, copying it from the page's `{{template "stock-badge-templates"}}` (one `<template id="stock-badges">`, whose text has `{have}`, `{need}`, `{min}`), so the markup and icons live only in the Go template.
* Every stock badge carries `data-stock-level`, for tests and scripts. It is a plain label, never a link.

### When a chip or badge is a link

A chip or badge is a link only when it takes you somewhere you would go next from it, so the same thing does the same everywhere:

| It | Links to | Where |
|---|---|---|
| **Goes up** to the record the page belongs to | that record | A service's header: its vehicle (`chip-link`) and its kit. A kit's header: its vehicle. |
| **Filters the stock** from a stock row (`item-row`) | `/stock?v=ID` (General: `/stock?general=1`), `/stock?cat=…` | The row's vehicle chips (`vehicle-chips` with `"Link" true`) and its category badge. |
| **Is a filter** | the filtered list | The filter bars (`chip-toggle`), and the sidebar's "Stock by vehicle" chips while there is one vehicle. |
| **Switches what you work on** | `POST /working-on`, then back to the page | The sidebar's Work on chips (on a phone, in the More sheet) and the bar's Switch: `button.chip` with `aria-pressed` (the one worked on is pressed, and tapping it stops), in a form of their own, never on a record's form. |

Everything else is a plain label (`{{template "chip" …}}`, `<span class="badge">`):

* Tags that only describe the record in its own header: an item's vehicles and category. The page already is that record.
* Chips and badges in list rows other than stock rows: kit rows, service lists, an item's "Service kits" card, the shopping list's entries and suggestions. A row that is a link itself (`.kit-row`, `.vehicle-card`) can't hold another link; the others match it.
* Status and state badges (due, parts ready/missing, stock, expiry, archived). The exceptions are **On shopping list**, on the item page and on a kit's parts card (where it stands in for Add missing once everything missing is listed), which links to `/shopping`: it says where the parts are, and nothing else on the page goes there. While a vehicle is worked on and the entry is on another vehicle's part of the list, it is a plain label, as the list it would open doesn't show it.
* Anything on a form (Log/Edit service, an edit of a stock change). A link there would leave what was typed; Cancel goes back. A kit start's due badge is part of its button: the button is the link, the badge isn't one of its own.

Only links that leave the app open a new tab (`target="_blank" rel="noopener noreferrer"`): SKU searches and the full-size photo. A form's hint may open a new tab when its point is to keep the form (the kit form's "add the item"). A chip or badge never does.

---

## 3. Section & Card Headers

* **Flex alignment**: All heading containers (`.card-head h2`, `.results-head h2`, `.section-head h2`) must use `display: flex; align-items: center; gap: 8px;` so icons, heading text, delimiters, and count badges center on the exact same vertical line.
* **Delimiters**: Separate a title from its item counter or secondary info using the middle dot ` · ` (e.g. `Stock for KillJoy · 0 items`).
* **Card Head Meta (`.card-head .meta`) & Results Count (`.results-head .count`)**: Sort-reset links, bullet delimiters, and count texts must be wrapped in an inline-flex container (`display: inline-flex; align-items: center; gap: 6px; color: var(--muted);`, at `.85rem` in a card head and `.9rem` in a results head).
* **One height** ([§11](#11-spacing)): every card head (`.card-head`) and stock section heading (`.section-head`) is 49px tall (a 20px line, or a 26px chip, in its padding, and the 1px border), whatever it holds. A section heading without chips (General) is as tall as one with them (`min-height`), and a 26px badge in a card head (the item page's **On shopping list**) hangs into the padding (`margin-block: -3px`). Only a head whose links wrap under the title on a narrow screen grows.
* **Card footer (`.card-foot`)**: A total that sums the card's list (Service history's "Labour in total") goes in a footer band under the list, not in the header, which keeps only the title, a count and its link. The label is on the left and the amount on the right; when the list has pages, Newer / Older take the left of the same row and the total sits together on the right.
* **A card's actions are in its footer** (`.card-foot.card-actions`), under what they act on: the vehicle page's **Add kit**, a kit parts card's **Add 2 missing** (or **On shopping list**, once everything missing is listed), Shopping's **Add all**. They start from the left, like the pager, and the footer keeps its 45px (7px around a 30px button, 9px around a 26px badge). The heading names the card: its title, a count, its link (**All services →**) and **Reset order**, and nothing you do there, so it never wraps for a button in a narrow column. You read the list, then act on it, as **Add part** sits under the rows on a form. A state of the record rather than an action on the list (the item page's **On shopping list**) may stay in the heading. Printed pages leave the footer out (`.card-actions`).

---

## 4. Page Headers

Every page starts with the same header: the heading and the record's buttons on one row, and one line under it that runs the full width, under the buttons too (the layout of GitHub's Primer PageHeader, Shopify Polaris and Atlassian's page header).

```html
<div class="page-head">
  <div class="page-title">
    <h1>…</h1>
    <div class="sub flex">                                   <!-- or <p class="sub"> for text alone -->
      {{template "archived-date-badge" …}}                    <!-- 1. Archived, when it is -->
      {{template "chip-link" …}}                              <!-- 2. vehicle chips -->
      <a class="badge badge-muted" …>…</a>                    <!-- 3. kit, item or category, then states -->
      <span>One sentence.</span>                              <!-- 4. text, if any -->
    </div>
  </div>
  <div class="actions">…</div>                               <!-- optional: the record's buttons -->
</div>
```

* **Heading row**: `.page-head` is a grid. Its first row is at least 38px (a button's height) with the heading centred in it, so the heading sits in the same place with or without buttons, level with them. Buttons keep their normal size. `.page-title` (and `.vehicle-hero`) are `display: contents`: they group the markup, and their children are the grid's items.
* **The line**: at most one line under the heading, 6px below the heading row, spanning the full width. In order: the **Archived** badge first when the record is archived (it changes what the page lets you do), then **chips**, then the other **badges** (the kit, item or category the record belongs to, then its states), then **text** in a `<span>`, if any. Text never comes before a chip or badge. A sentence on its own is a `<p class="sub">`.
* **The heading names the record or the action** and nothing else: no vehicle names after a ` · `. A vehicle that can't change on the page (a kit's service, an edit) is a chip on the line; when a field on the page still chooses it, it isn't shown. On a form's line, chips and badges are plain labels: nothing there is a link, and nothing opens a new tab (Cancel goes back).
* **The way back**: a record's page starts its buttons with a ghost ← button to where the record belongs, never to the page it came from: an item's to **Stock**, a service's to **Services**, a kit's to its vehicle (named; kits have no list), a vehicle's to **Vehicles** (not while you work on a vehicle: the bar's × leads there). An archived item or vehicle goes to the archived list it is in (`?archived=1`). It is a plain link: while you work on a vehicle, Stock and Services open its part.
* **Narrow screens** (under 1060px wide with the sidebar, and phones): the heading at the top, the line 6px below, then the buttons on their own row.
* **Vehicle page**: the coloured kind avatar sits left of the heading and the line and spans both, top to bottom: 70px (38 + 6 + 26) with a 38px icon, and the line is at least 26px so a text-only line lines it up too. On narrow screens it is 56px with a 30px icon, centred.
* The e2e test "every page header has the same spacing" checks the heading's place, the gap under it and the order of the line on every page, on desktop and phone.

---

## 5. Whole Pixels & Alignment Rules

Adhere to the existing whole-pixel constraints from [conventions.md](conventions.md):
* Line heights through `--lh` (rounded to even pixels), never raw `line-height`.
* Lengths in `px`, not `rem`.
* Centered-against heights must be even (icons, badges, chips, buttons).
* Never rely on browser default baselines for mixed-font or icon-containing inline elements; use flex containers with `align-items: center`.

### Text centred in its box

Chrome rounds the font's ascent and descent to whole pixels and puts the baseline on a whole pixel, so where Inter's capitals (.727em tall) land in a line is fixed by the font size. In a box of even height, only some sizes leave the same space above the capitals as below; the others leave a whole pixel more above, whatever the padding (a pixel of padding moves the text to a pixel low instead). With the root at 16px:

| Font size | In an even box |
|---|---|
| .68–.73rem (10.9–11.7px) | centred within .25px (badges, chips, table headers, nav counts: .72rem) |
| .74–.81rem (11.8–13px) | **1px nearer the top**: don't use in a box |
| .82–.90rem (13.1–14.4px) | centred within .25px (buttons .9rem, small buttons .82rem, chip toggles .88rem) |
| .91–.98rem (14.6–15.7px, the body's 15px) | **1px nearer the top**: don't use in a box |
| 1rem (16px) | centred within .2px (card and section headings) |

A box here is anything with its text centred in a background, a border or a bar: buttons, chips, badges, nav links, counts, table headers, heading bars. Text that isn't in a box (meta lines, hints) may use any size. The e2e test "every box sits on whole pixels and its text lines up" fails on a box's capitals more than .3px off its middle, and on a chip or badge that isn't 26px. The one exception is a kit start's status, which stands on the button name's baseline instead (above).

---

## 6. Table Row Alignment (Kit Lines)

Kit lines and tables use vertical centering (`vertical-align: middle` via standard `.table td`):
* Keeps the row status disc (`[✕]`, `[✓]`), item name block (including the stock meter), and quantity figures (`NEED`, `IN STOCK`, `MISSING`) vertically balanced.
* On mobile and narrow viewports where part names wrap onto multiple lines, middle alignment prevents large dead spaces under single-line quantities and maintains a balanced visual row.
* **Columns**: cells have 12px on each side (24px between columns), and the first and last cell the card's `--inset` ([§11](#11-spacing)), so the first column lines up with the card's heading and the last button sits as far from the edge as the heading's count. A table fits its card on a desktop; a phone may scroll it inside its `.table-wrap`, never the page.

---

## 7. Status Discs (`.status-icon`)

Circular 30px indicators (`width: 30px; height: 30px; border-radius: 50%; display: grid; place-items: center; flex: none;`) provide consistent visual anchors and state communication on list rows:
* **Service Kits** (`kit-row`, item page "Service kits"): Readiness disc communicating stock status:
  * `.status-ok` with `icon "check"`: All parts for the kit are in stock.
  * `.status-short` with `icon "x"`: One or more parts are missing.
  * `.status-muted` with `icon "minus"`: Kit has 0 part lines.
  * `.status-muted` with `icon "archive"`: Kit is archived.
* **A kit's parts** (`kit-parts-table`, a disc per row): `.status-ok` `check` (enough in stock), `.status-short` `x` (missing), `.status-listed` `shopping-cart` in the accent (missing, and the shopping list asks for at least that much: `stock.KitLine.Listed`), `.status-muted` `archive` (an archived item). Each disc says what it means in its `title` and `aria-label` ("175 ml missing, on the shopping list"), as a disc alone could pass for a button.

---

## 8. Working on a vehicle (`web/working.go`)

You pick the vehicle you work on, and the dashboard, Stock, Services, Activity and Shopping are about it, the way parts shops let you pick your vehicle once and show what fits it. It is a cookie (`working_on`, like the theme), so it belongs to the device: the phone in the garage works on the bike on the stand while the desktop sees every vehicle.

* **While the bar shows, a list page shows its vehicle.** Dashboard, Stock, Services, Activity and Shopping (below) always show the vehicle worked on (Stock and Activity with General). The cookie is never a hidden filter: an address that asks for anything else (no vehicle, `v=all`, another vehicle, General only, the vehicle without General, a vehicle that doesn't exist, an old bookmark, a link copied before) gets a 303 to the same address for the vehicle (`/stock?general=1&v=ID`, `/services?v=ID`, `/activity?general=1&v=ID`, `/?v=ID`), keeping its search, filters and sort (not its page number). So the address always says what the page shows, and the bar and the page never disagree. While it shows, titles are plain ("Stock"): the bar names the vehicle. Only an item's history (`/activity?item=`) is a record and keeps its address. Activity counts a vehicle's own services' parts, not the parts another vehicle's service took from a shared or General item (its subtitle says so); its vehicle and General chips, shown when no vehicle is worked on, work like the stock's. Without a vehicle worked on, a vehicle id that doesn't exist counts as none chosen.
* **The bar** (`working-on` partial) is the first thing in `<main>` on every page: the vehicle's kind icon in its colour, **Working on KTM 390** (a plain link to the vehicle's page, except on that page; on hover the name takes the vehicle's colour, like a vehicle title, never an underline), and its buttons as icons (their names are tooltips and screen readers' labels). On a desktop that is **All vehicles** (an ×, which stops) alone: the sidebar's Work on chips, right beside it, switch. On a phone, where the sidebar is hidden, the bar runs edge to edge at the top (a bottom border only, one row) and stays in view as the page scrolls, and it adds **Switch**: a `popover` tray under the whole bar, as tinted, past a dashed line, listing the other vehicles' Work on chips. It is the top counterpart of the More sheet: the page behind it goes grey, while the bar and its tray stay bright (the grey starts at the bar's bottom edge, which `app.js` measures as it opens), and a tap outside or Esc closes it. Nothing narrows a page without the bar saying so.
* **The bar is the vehicle control.** Meanwhile the pages hide their own vehicle and General chips: the vehicle's stock always comes with General, the items that fit any vehicle, so nothing floats on its own (Stock's filter keeps them in hidden fields), and Log service and Add kit don't ask for the vehicle. Links to another vehicle's list, which would show the worked-on one's, are hidden: a stock row's vehicle chips are labels, and the dashboard leaves out its vehicle cards. To see another vehicle, work on it (or stop).
* **Another vehicle is out of view.** What belongs to another vehicle, its page, its kits and services and their edit forms, and the pages about every vehicle, the Vehicles list (archived ones too) and Add vehicle, wait until you switch or stop: a GET gets a 303 to the vehicle worked on's page (a service: to Services, which is its list), with htmx too (`elsewhere`, `working.go`). One rule, so nothing on screen is about a vehicle the bar doesn't name. Links to them are names, not links: an item page's kits and its history's services of other vehicles. Switching on a vehicle's page opens the one now worked on, and stopping there opens the list. A save from a page opened before isn't refused (it changes what is there already, which isn't new work), and restoring another vehicle's vehicle, kit, service or shopping entry stays on the trash.
* **Shopping is the vehicle's part of the list** (`/shopping?v=ID`), with the same 303. An entry is on it when its item fits the vehicle or is General, or when one of the vehicle's active kits uses it (so a kit's shortfall, once added, stays on the list even for an item tagged elsewhere), and free text always is: it belongs to no vehicle. Which vehicles an entry is for is worked out from the item's tags and the kits (`stock.ShoppingEntry.For`), never stored: each item has one entry, so a shared item two vehicles need is one line, and a stored vehicle would go stale when an item is retagged. The suggestions are the vehicle's too: what fits it below its minimum, and its own kits' shortfalls; what is listed counts whoever it was added for. The Add form offers the vehicle's items and the General ones, so nothing added there disappears. At the shop the rest is easy to forget, so the list says **N more for other vehicles**, and the whole list is the bar's × away. The sidebar's and the tab bar's counts are the part's. Without a vehicle worked on, chips filter it like Services' (All vehicles · each vehicle), and what you do on a vehicle's part comes back to it (its forms and the Bought page carry `v`). On the whole list each entry and suggestion carries the vehicles it is for as chips (labels; none for General and free text; an archived vehicle's too, and never the sole active vehicle's, which is every page's scope). A vehicle's part has no row chips: it says the vehicle once. The vehicle is a chip, never text: a reason names what needs the part, **Short for the Air Filter kit** (`stock.ShortNote`), and a saved note keeps it so. The whole list's Copy as text ends each line with its vehicles, `[GreyJoy, KillJoy]`, as notes don't say whose.
* **Never narrowed:** what is shared: an item (it may fit several vehicles, or none) and its edit form, and Trash. They show the bar only. Trash is the undo safety net, newest first, and many of its entries belong to no one vehicle (an item that fits several, a General one, a shopping entry, a vehicle). Archives need nothing of their own: Stock's Archived is a filter on `/stock`, so it follows the vehicle; archived kits are on their vehicle's page; an archived vehicle can't be worked on. Exports (Copy as text, CSV) have no bar and follow their address only: the pages link to them with their own, a bookmarked one keeps what it asks for, and a vehicle page's Copy as text is that vehicle's. A search on the vehicle's stock says what it finds elsewhere: **N more in other vehicles** (the bar's All vehicles, right above, keeps the search).
* **New work is for the vehicle worked on**, like "archived means no new work". **Log service** and **Add kit** are for it without a Vehicle field, and Log service offers only its kits to start from; another vehicle's pages are out of view (above). An old link to another vehicle's form (`?vehicle=`, `?kit=`) opens the worked-on one's, and a save from a form opened before a switch comes back (422, keeping what was typed, with the Vehicle field and the other vehicle chosen) saying "You're working on GreyJoy: choose it, or work on LittleJoy first to log this service" (a kit's service, which can't change vehicle: "work on LittleJoy first to log this service"; a kit: "… to add this kit"). **Add item** starts with the vehicle ticked, but an item isn't work on a vehicle (it may fit several, or none), so its vehicles stay a choice, and its *add another* keeps a General item General. A save to another vehicle's existing record from a page opened before still goes through: that isn't new work.
* **The nav's Vehicles becomes Vehicle**, with the vehicle's kind icon in its colour instead of the warehouse, and opens its page: while you work on a vehicle, that is the vehicle page you want. `/vehicles` opens it too (above); stopping (the bar's ×) leads back to the list, and only then does a vehicle page show its **Vehicles** ghost button, first in its header.
* **The dashboard** leaves out its Vehicles panel meanwhile: the bar, the nav's Vehicle and Switch lead to the others. It counts what its lists open: the tiles link to the vehicle's stock (with General) and activity, so each number is the rows it opens; Stock value and Spent count those items too. The Shopping list tile and **N suggested →** count the vehicle's part of the list and open it; so does a vehicle's dashboard without one worked on (`/?v=ID`).
* **Where you choose it:** the sidebar's **Work on** chips (2 or more active vehicles; with one, it is off, the choice is cleared so it doesn't come back months later with a second vehicle, and the sidebar keeps "Stock by vehicle"), and the bar's Switch; on a phone, the sidebar's chips are in the More sheet.
* **On a phone** the tab bar is Dash · Stock · Services · Shop · **More**. More opens the sidebar itself as a sheet over the tab bar, without what the tab bar has (Stock, with its `/`) or Add item and Log service with their keys (every page has them, and a phone has no keyboard). It is a `popover`, like Switch: a tap outside or Esc closes it, and it closes when the page is left, so Back doesn't bring it open. The page behind goes grey; the sheet and the tab bar it comes from stay bright, and More is highlighted while it is open. Each popover meets what opened it along a dashed line (the sheet's covers the tab bar's top border, the tray's the bar's bottom one), is rounded on its free edge (`--sheet-radius`, 16px: the sheet's top corners, the tray's bottom ones) and square along the seam, and casts no shadow: the grey sets it apart, and a shadow would fall on the bright bar beside it. What is grey is out of reach: a tap there only closes the popover, it never opens what is under it. So the phone has what the desktop has, in one place: the theme, Vehicle or Vehicles, Activity, Trash and the Work on chips, and the pages need no shortcuts of their own to them (no Trash button on Activity, no Work on row on the dashboard). More is current on the pages it leads to.
* A vehicle archived or deleted stops being worked on (the cookie is cleared on the next page, and so is one that isn't the plain digits the server writes). A page that Back brings from before a switch reloads (`app.js`, on `pageshow`, comparing the cookie with the page's `data-working-on`, which is rendered even when the vehicles can't be read), unless it holds unsaved typing.

---

## 9. Capitalization: Catalog Entities vs. System Labels

Pitbox maintains a strict distinction in text capitalization:
* **Catalog Entities & Products (Title Case)**: Part names, brands, categories, services, vehicle names, and service kits use Title Case because they represent named catalog entities:
  * e.g. `Hiflofiltro Oil Filter`, `Oil Service (Intermediate)`, `Air Filter`, `Brake Pads (Rear)`, `Oil & Fluids`, `Gaskets & O-Rings`.
* **System Actions, Filters & Statuses (Sentence Case / Status Case)**: Actions, status indicators, notes, and filter options use sentence case or uppercase status:
  * e.g. `Starting stock`, `All types`, `All vehicles`, `Out of stock`, `Clear filters`, `Used in this service`, `1 PART MISSING`, `PARTS READY`.



---

## 10. Focus Rings

Where focus is always shows, the way browsers decide it (`:focus-visible`) and as GitHub's Primer does it. A new control gets its ring from its class, never one of its own.

* **Fields show it on any focus**, click or keyboard: anything you type into or choose from (`.input` on an input, select, textarea, suggestion, date or search field). You type next where the ring is. The ring is the field's own 1px border plus a 1px box-shadow in `--ring` (2px solid), then a 3px `--ring-soft` glow (`.input:focus`). `--ring` is the accent, or red (`--danger`, `--danger-soft`) in a field with an error (`.has-error`), so the field never mixes a red border with a purple ring. An open select keeps it too (`select.input:open`), as its list takes the focus from it.
* **Buttons and other clickables show it from the keyboard only** (`:focus-visible`): `.btn`, `.icon-btn`, the segmented toggles, sort links and chips get a 2px solid `--accent` outline, 2px off the control (1px for sort links), so it shows on a filled button too. A mouse click leaves no ring: it has done what it does, and a ring left behind looks stuck.
* **Strong enough for WCAG.** Both rings are visible on focus (2.4.7, AA) and at least 2px with 3:1 against the unfocused state (2.4.13 Focus Appearance, AAA). The pale glow alone counts for neither.
* **Nothing moves.** A ring is a box-shadow or an outline, never a thicker border, so a focused control keeps its box (section 5).
* **For a new control:** use `.input` for a field and `.btn` or `.icon-btn` for a button, and the ring comes with the class. A new kind of clickable control joins the `:focus-visible` outline rule in `app.css`. Never set `outline: none` without a ring in its place, and don't give a component a focus colour of its own.

---

## 11. Spacing

One scale, so a page reads as one thing: the same space wherever the same kind of space is. The values are tokens on `:root` in `app.css`. A new component uses them, never a number of its own.

| Space | Desktop | Phone (≤ 820px) | Where |
|---|---|---|---|
| Around the page | 24px on all four sides, the content filling the width (no maximum) | 16px; at the bottom 16px above the tab bar | `.main` |
| A page that is a form (Add/Edit item, vehicle, kit, service; a stock change; Bought) | `--form-width`: 1000px, its header and flash too | the page's width | `.page-form` |
| A form's fields | at most 4 a row, at least 220px each | one a row | `.form-grid` |
| A page's side column (details, photo, short lists) | 280–440px; the main column takes the rest | under the main column | `.split` |
| Vehicle cards | at least 360px, as many as fit | one a row | `.grid-3` |
| The sidebar's top and bottom | 24px, its header row 38px | (the More sheet) | `.sidebar`, `.sidebar-head` |
| Between cards, sections, tiles and columns | `--gap`: 16px | 16px | `.card + .card`, `.grid`, `.split`, `.tiles`, `.stack`, `.mt`, `.mb` |
| Inside a card, from its border to its text, left and right | `--inset`: 19px | 15px | every part of a card (below) |
| Between table columns | 12px each side; `--inset` at the table's edges | the same | `.table` ([§6](#6-table-row-alignment-kit-lines)) |
| A heading bar | 49px, whatever it holds | the same | `.card-head`, `.section-head` ([§3](#3-section--card-headers)) |

* **Wide windows: lists fill, the rest stops.** Lists, tables and the dashboard fill the window, as they get more readable with room. What doesn't is capped: a form stops at four fields a row (`.page-form`, `.form-grid`), so its fields don't stretch to 500px and its notes to 1600px; a side column stops at 440px (its photo once grew to 800px); vehicle cards keep their width instead of splitting the screen (`auto-fill`, not `auto-fit`). A new page that is a form gets `page-form` on its `<form class="card">`.
* **The same space on every side of the page.** The content fills the window beside the sidebar, so a wide window has no empty band on the right, and nothing at the end of a page or of a card adds a margin of its own: `.main > :last-child`, `.card-pad > :last-child`, `.card-body > :last-child` and `.empty > :last-child` have none (a `<p>` keeps its 12px only between paragraphs). On a desktop the last card then ends level with the sidebar's buttons.
* **The logo is level with the page heading.** The sidebar starts 24px down, as the page does, and its header row is as tall as the heading row (38px), so the logo and the heading share a centre line.
* **The theme button is a nav row's box.** 40px, its icon 11px in, pulled out by the row's inset, so its hover fills the same box as a nav link's and ends on the same edge as the active one's, while its icon lines up with the nav's counts. An icon-only control in the sidebar takes the nav row's box, not its own.
* **The inset is the card's, for every part.** `.card-pad`, `.card-head`, `.card-body`, `.form-section`, `.filters`, `.section-head`, `.item-row`, `.list > li`, `.kit-row`, `.pager`, `.card-foot`, `.tabs`, `.vehicle-card .body` and the table's outer cells all use `--inset` left and right, so text, icons and buttons line up from the card's heading down to its last row. On a phone it is 15px: with the 1px border, as far inside the card as the page's 16px margin is outside it. Top and bottom padding is the part's own (a row is shorter than a form section).
* **Kept on purpose:** a heading sits closer to what it labels than to what comes before it. The page header has 20px under it, a results heading (`.results-head`, "13 items" with its buttons) 20px above and 12px below, the Working on bar 20px under it (16px on a phone, where it runs edge to edge), a flash 16px. A dashboard tile (`.tile`) is padded 14px: its figures need the width, and nothing lines up against them.
* **For a new component:** space cards with `--gap` (or put them in a `.grid`, `.split` or `.stack`), pad a card's part with `--inset` left and right, and leave no margin under its last child.
* The e2e test "every page's spacing follows the scale" checks every page on desktop and phone: the space under the page, the gaps between cards, every part's inset on both sides, that tables fit their cards, that heading bars are one height, and the logo against the heading.
