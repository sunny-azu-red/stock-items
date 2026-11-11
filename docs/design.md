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
* **Dimensions**: Height **26px** (`font-size: .78rem`, `padding: 3px 9px`, line-height `18px`).
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
| `/services?v=ID` | the title ("KTM 390 services") and the active filter chip | service rows, kit rows |
| `/stock?v=ID` (exactly one vehicle) | the title ("KTM 390 stock") and the active filter chip | section headings, list rows (the other vehicles' chips stay) |
| An item fitting one vehicle | the header chip | the rows of that vehicle's kits |

The partials take the scope as `"Here"` (a vehicle id, 0 for none): `stock-section`, `service-list`, and `item-row` (`rowFor`, kept across a quick +/- by a hidden `here` input). `kit-row` takes `kitView.HideVehicle` (`scopeKits`).

Stock section headings (`stock-section`) each lead with an icon and a word: `package` **General** (not tied to a vehicle), `tag` **Shared** + chips, and `warehouse` **Vehicle** + chip (+ "only" when the stock is filtered by vehicle). Inside a scope they read `warehouse` **This vehicle only** and `tag` **Shared with** + the other vehicles' chips.

### Status & State Badges (`.badge`, `a.badge`)
* **Purpose**: Identifies states, statuses, quantities, kit tags, or categories (e.g. `1 PART SHORT`, `PARTS READY`, `ARCHIVED`, `OVERDUE`, `ON SHOPPING LIST`, `OUT OF STOCK`, `525 ML IN STOCK`).
* **Category Badge**: Item categories are standard badges (`.badge.badge-muted` with the tag icon). On an item page they sit right after the vehicle chips in the header's `.sub.flex` row, as a plain label; on a stock row they link to the stock filtered by that category (see "When a chip or badge is a link").
* **Shape**: Rounded rectangle (`border-radius: 6px`).
* **Typography**: Bold uppercase text (`font-size: .72rem`, `font-weight: 700`, `letter-spacing: .03em`).
* **Dimensions**: Height **26px** (`padding: 4px 8px`, line-height `18px`).
* **Hover (as link)**: Container background color transition (e.g. `.badge-muted:hover { background: var(--surface-3); }`, `.badge-accent:hover { background: var(--accent); color: var(--on-accent); }`). **Never use a text underline on badges.**

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
| **Is a filter** | the filtered list | The sidebar's "Stock by vehicle" chips and the filter bars (`chip-toggle`). |

Everything else is a plain label (`{{template "chip" …}}`, `<span class="badge">`):

* Tags that only describe the record in its own header: an item's vehicles and category. The page already is that record.
* Chips and badges in list rows other than stock rows: kit rows, service lists, an item's "Service kits" card. A row that is a link itself (`.kit-row`, `.vehicle-card`) can't hold another link; the others match it.
* Status and state badges (due, parts ready/short, stock, expiry, archived). The one exception is the item page's **On shopping list** badge, which links to `/shopping`: it says where the item is, and nothing else on the page goes there.
* Anything on a form (Log/Edit service, an edit of a stock change). A link there would leave what was typed; Cancel goes back.

Only links that leave the app open a new tab (`target="_blank" rel="noopener noreferrer"`): SKU searches and the full-size photo. A form's hint may open a new tab when its point is to keep the form (the kit form's "add the item"). A chip or badge never does.

---

## 3. Section & Card Headers

* **Flex alignment**: All heading containers (`.card-head h2`, `.results-head h2`, `.section-head h2`) must use `display: flex; align-items: center; gap: 8px;` so icons, heading text, delimiters, and count badges center on the exact same vertical line.
* **Delimiters**: Separate a title from its item counter or secondary info using the middle dot ` · ` (e.g. `Stock for KillJoy · 0 items`).
* **Card Head Meta (`.card-head .meta`) & Results Count (`.results-head .count`)**: Sort-reset links, bullet delimiters, and count texts must be wrapped in an inline-flex container (`display: inline-flex; align-items: center; gap: 6px; color: var(--muted);`, at `.85rem` in a card head and `.9rem` in a results head).

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

---

## 6. Table Row Alignment (Kit Lines)

Kit lines and tables use vertical centering (`vertical-align: middle` via standard `.table td`):
* Keeps the row status disc (`[✕]`, `[✓]`), item name block (including the stock meter), and quantity figures (`NEED`, `IN STOCK`, `MISSING`) vertically balanced.
* On mobile and narrow viewports where part names wrap onto multiple lines, middle alignment prevents large dead spaces under single-line quantities and maintains a balanced visual row.

---

## 7. Status Discs (`.status-icon`)

Circular 30px indicators (`width: 30px; height: 30px; border-radius: 50%; display: grid; place-items: center; flex: none;`) provide consistent visual anchors and state communication on list rows:
* **Service Kits** (`kit-row`, item page "Service kits"): Readiness disc communicating stock status:
  * `.status-ok` with `icon "check"`: All parts for the kit are in stock.
  * `.status-short` with `icon "x"`: One or more parts are missing/short.
  * `.status-muted` with `icon "minus"`: Kit has 0 part lines.
  * `.status-muted` with `icon "archive"`: Kit is archived.

---

## 8. Capitalization: Catalog Entities vs. System Labels

Pitbox maintains a strict distinction in text capitalization:
* **Catalog Entities & Products (Title Case)**: Part names, brands, categories, services, vehicle names, and service kits use Title Case because they represent named catalog entities:
  * e.g. `Hiflofiltro Oil Filter`, `Oil Service (Intermediate)`, `Air Filter`, `Brake Pads (Rear)`, `Oil & Fluids`, `Gaskets & O-Rings`.
* **System Actions, Filters & Statuses (Sentence Case / Status Case)**: Actions, status indicators, notes, and filter options use sentence case or uppercase status:
  * e.g. `Starting stock`, `All types`, `All vehicles`, `Out of stock`, `Clear filters`, `Used in this service`, `1 PART SHORT`, `PARTS READY`.


