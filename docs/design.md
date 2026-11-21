# Design & UI Conventions

This is the spec for how Pitbox looks and behaves on screen: tokens, components, link styles, layout and alignment. `internal/web/static/app.css` follows it; change this document first, then the CSS. The platform rules (current Chrome, native HTML first, the Modern Web Guidance skill) are in [AGENTS.md](../AGENTS.md#browser-support-and-the-web-platform).

---

## 1. Look and feel

Calm, friendly and dense enough for a garage full of parts:

* **Mellow, not stark.** Soft text, a muted accent, faint outlines and medium weights: nothing on the page shouts (see [Contrast](#contrast)).
* **Panels, outlined.** Cards sit on a lightly tinted page, set off by their own surface and a hairline in `--border`, with no shadow. Inside a card, rules between rows are the same hairline. Nothing has a shadow, what floats over the page included: the phone's tab bar and its sheets, popovers, a select's list and suggestions, and dialogs take the same `--border` hairline as a card; the Switch tray, edge to edge under its bar, has it along its bottom only; a toast, dark on a light page and light on a dark one, needs no edge; a hint (the "?") has its own accent tint and edge.
* **Colour means something.** The accent marks what you can do (the main button, the current page, focus). Status colours say how things are (green on track, amber low or due, red missing or out of stock). Each dashboard figure has a colour of its own, and a vehicle's colour washes what belongs to it (its stock section, its page's avatar, the Working on bar).
* **Tight corners.** Cards 8px, buttons and fields 6px, badges 4px. Only vehicle chips, counts and avatars' discs are round.
* **One typeface:** Manrope, semi-bold for headings and figures, medium for text.

---

## 2. Colour

All colours are tokens on `:root` in `app.css`, each written once as `light-dark(light, dark)`; `color-scheme` picks one: the system's setting, unless the theme cookie sets `data-theme` on `<html>`. A component never sets a colour of its own outside the tokens (the dashboard figures' tints and the vehicles' colours are data).

### The accent

The accent is worked out from two numbers, so it can change without touching a token:

| Custom property | Default | Range | Means |
|---|---|---|---|
| `--accent-h` | 280 (Indigo) | 0–359 | the hue on the colour wheel |
| `--accent-i` | 100 | 0–100 | intensity: 0 is grey (Graphite), 100 full colour |

`--accent`, `--accent-strong` (hover, text on soft fills), `--accent-soft` (selected fills) and `--on-accent` (text on the accent) come from these at fixed lightnesses, so every hue looks equally calm. The neutrals (`--bg`, `--surface*`, `--border`, `--text`, `--muted`) lean towards the hue as much as the intensity says, so a grey accent gives true greys. `--info` (dates, expiry) sits 45° away from the accent, so it never reads as a button.

### Contrast

The palette is mellow on purpose ("Mist"): text is a soft grey-blue rather than near black (near white in dark), the dark theme is a lighter slate, the accent is muted, and outlines are faint. This deliberately sits below the usual contrast advice (4.5:1 for text, 3:1 for field borders), Modern Web Guidance's included; the owner chose the calmer look over it. Don't raise colours back to meet those ratios. Where something must stand out, it does so through its own colour, not through more contrast everywhere: badges get livelier status colours (`--badge-*`), and the main button is a tint of the accent with accent text (`.btn-primary`), not a solid block.

### Appearance (theme.go)

The sidebar's palette button opens **Appearance** (on a phone, from the More sheet): Theme (System, Light, Dark) and Accent (presets Indigo, Cobalt, Ocean, Plum, Raspberry, Graphite; a Colour slider over the whole wheel; an Intensity slider; **Back to Indigo**). Both are remembered by the device in a cookie (`theme`; `accent` as `hue-intensity`, e.g. `332-74`), like the vehicle worked on: the phone in the garage can be dark and plum while the desktop is light and indigo. The server renders them on `<html>` (`data-theme`, and `style="--accent-h: …; --accent-i: …"`), so a page never flashes the default first. `app.js` shows a change on the spot and saves it in the background; without it, the forms post and come back.

### Status and data colours

| Token | Means |
|---|---|
| `--ok`, `--ok-soft` | on track, parts ready, enough in stock, a stock change in |
| `--warn`, `--warn-soft` | low stock, due soon |
| `--danger`, `--danger-soft` | missing, out of stock, overdue, a stock change out, errors |
| `--info`, `--info-soft` | dates: best before, expiring |
| `--badge-ok`, `--badge-warn`, `--badge-danger`, `--badge-info` (and `-soft`) | the same states on badges, at 1.35 times the chroma, so a badge reads at a glance on a quiet page |
| `--border` | card outlines and the rules between rows and under table headers |
| `--field-border` | fields' and outlined buttons' borders: a light line, not a hard edge |

Dashboard figures each have a fixed tint (`--t` on `.tile-items`, `.tile-low`, `.tile-expiring`, `.tile-shopping`, `.tile-value`, `.tile-spent`); Low stock and Expiring turn amber and red while they count something.

---

## 3. Type

* **Manrope** (vendored, SIL OFL; the system's sans-serif stands in for a letter it lacks), with tabular figures everywhere, so numbers line up in columns and don't shift. `word-spacing: .05em` on the body: Manrope's own space ran "13 items" together at small sizes.
* **Sizes are in rem**, so the browser's text size setting scales them; lengths stay in px (§5).

| Token | Size | For |
|---|---|---|
| `--text-xs` | .65rem (10.4px) | chips, badges, table headers, counts, keys, tab labels |
| `--text-sm` | .84rem (13.4px) | body text, fields, buttons, toggles, labels, nav, card links, meta in heading bars |
| h2, h3 | 1rem | card and section headings |
| h1 | 1.625rem (1.4375rem on a phone) | the page heading |

Weights: 650 for headings, figures and badges; 600 for names, labels and buttons; 500 for secondary links, nav and text. (Manrope is a variable font, so 650 is a real weight.) Headings balance their lines (`text-wrap: balance`); paragraphs avoid a lone last word (`text-wrap: pretty`).

### Text centred in its box

Chrome rounds the font's ascent and descent to whole pixels and puts the baseline on a whole pixel, so where Manrope's capitals (.735em tall; ascent 1.066em, descent .300em) land in a line is fixed by the font size. In a box of even height, only some sizes leave the same space above the capitals as below; the others leave a whole pixel more on one side, whatever the padding. Measured in Chromium with Manrope, whatever the line height:

| Font size | In an even box |
|---|---|
| .63–.73rem (10.1–11.7px) | centred: `--text-xs` .65rem (chips, badges, table headers, counts, tab labels) |
| .735–.79rem | off by a pixel: don't use in a box |
| .80–.90rem (12.8–14.4px) | centred: `--text-sm` .84rem (buttons, toggles, nav rows, fields) |
| .905–.965rem | off by a pixel: don't use in a box |
| .97–1.07rem (15.5–17.1px) | centred: 1rem (heading bars) |
| 1.14–1.24rem | centred |

A box is anything with its text centred in a background, a border or a bar: buttons, chips, badges, nav rows, counts, table headers, heading bars. Text not in a box (meta lines, hints, figures) may use any size. The e2e test "every box sits on whole pixels and its text lines up" fails on a box whose capitals are more than .3px off its middle, using Manrope's metrics. A kit start's status is the one exception: it stands on the button name's baseline (§10).

---

## 4. Shape and size

| Thing | Size | Radius |
|---|---|---|
| Card (panel) | — | `--radius` 8px |
| Field, button | `--control-h` 36px (a 20px or 16px line in its padding and border) | `--radius-sm` 6px |
| Small button, icon button, toggle, chip you pick | `--control-sm` 30px | 6px |
| Chip (vehicle) | 24px | pill |
| Badge (state) | 24px | `--radius-xs` 4px |
| Status disc | 28px | round |
| Heading bar (card head, section head) | 45px with its rule | — |
| Card footer | 47px with its rule | — |
| Nav row, Work on row | 36px | 6px |
| Tab bar (phone) | 58px, floating 10px above the bottom | 14px |

Every height that something is centred against is even, so centring lands on whole pixels (§16).

---

## 5. Spacing

One scale, so a page reads as one thing: the same space wherever the same kind of space is. The values are tokens on `:root`; a new component uses them, never a number of its own.

| Space | Desktop | Phone (≤ 820px) | Token / where |
|---|---|---|---|
| Around the page | 20px on all four sides, the content filling the width | 14px; at the bottom, the floating tab bar's space and 14px | `--page`, `.main` |
| Between cards, sections, tiles and columns | 12px | 12px | `--gap`: `.card + .card`, `.grid`, `.split`, `.tiles`, `.stack`, `.mt`, `.mb` |
| Inside a card, left and right, for every part of it | 16px | 14px | `--inset` |
| Under the page header | 18px | 18px | `.page-head` |
| Under the Working on bar | 18px | `--page` (it runs edge to edge) | `.working-on` |
| Above / below a results heading ("24 items" and its buttons) | 18px / 10px | the same | `.results-head` |
| Under a flash | 12px | 12px | `.flash` |
| Between table columns | 12px each side; `--inset` at the table's edges | the same | `.table` |
| A page that is a form | at most `--form-width` 980px, its header and flash too | the page's width | `.page-form` |
| A form's fields | at most 4 a row, at least 200px each | one a row | `.form-grid` |
| A page's side column | 300–480px; the main column takes the rest | under the main column (below 680px of content) | `.split` |
| Vehicle cards | at least 320px, as many as fit | one a row | `.grid-3` |

* **Wide windows: lists fill, the rest stops.** Lists, tables and the dashboard fill the window. A form stops at four fields a row, a side column at 480px, and vehicle cards keep their width (`auto-fill`, not `auto-fit`).
* **The same space on every side of the page.** Nothing at the end of a page or a card adds a margin of its own: `.main > :last-child`, `.card-pad > :last-child`, `.card-body > :last-child` and `.empty > :last-child` have none (a `<p>` keeps its 12px only between paragraphs).
* **The inset is the card's, for every part.** `.card-pad`, `.card-head`, `.card-body`, `.form-section`, `.filters`, `.section-head`, `.item-row`, `.list > li`, `.kit-row`, `.pager`, `.card-foot`, `.tabs`, `.vehicle-card .body` and the table's outer cells all use `--inset` left and right, so text, icons and buttons line up from the card's heading to its last row. Top and bottom padding is the part's own. The one exception is the item's photo (`.photo`), which fills its card's top edge to edge, trimmed by the card's corners, with a hairline above the buttons in their `.card-body`: one frame, not a photo inset in a card.
* **Kept on purpose:** a dashboard figure (`.tile`) is padded 12px 14px: its figures need the width, and nothing lines up against them.
* **The logo is level with the page heading.** The sidebar starts `--page` down, as the page does, and its header row is a control tall (36px), as the heading row is.
* **Spacing utilities win.** `.mt` and `.mb` live in the last cascade layer, so spacing asked for in the markup beats a component's own margins (§17).
* The e2e test "every page's spacing follows the scale" checks every page on a 2K screen, a laptop window and a phone.

---

## 6. Layout

* **Shell.** On a desktop: the sidebar (220px, on the page's background, no edge) beside the main column. On a phone (≤ 820px, the one viewport breakpoint): a tab bar floating 10px above the screen's bottom edge (Dash · Stock · Services · Shop · More), the current tab's icon in a soft box.
* **Components follow their own width, not the window's.** Every card is a size container, and so is `main` (`container: main`). Breakpoints are container queries:

| What | Changes at | Container |
|---|---|---|
| Page header: buttons on their own row; vehicle avatar 52px | 680px | main |
| `.split`: one column | 680px | main |
| Dashboard figures: two a row | 520px | main |
| Form buttons share the width | 520px | the card |
| Kit rows: badges under the name | 500px | the card |
| Stock rows: 60px quantity column | 540px | the card |
| SKU and part rows: two lines | 620px | the card |
| Sort by on its own line | 620px | the card |
| A table keeps 640px and scrolls inside its card | 660px | the card |

  Only the shell (tab bar, More sheet, Switch tray, the sticky Working on bar, toasts) follows the window, at 820px.
* **Confirmations** (only the trash's permanent deletes ask) are a modal `<dialog>`, the `confirm-dialog` partial: centred, 420px at most, on the grey `--scrim`; its button opens it itself (`commandfor`), Cancel has the focus first, and Esc or a click outside closes it. The page behind doesn't scroll while it or the photo viewer is open: `overscroll-behavior: contain` on the dialog and its `::backdrop`, never `overflow: hidden` on the page, which would take its scrollbar away and shift it sideways.
* **Hints** (the "?" after a label, the `tip` partial) are `popover="hint"`s in the top layer, so no card clips them, and need no JavaScript: hovering or focusing the "?" shows one for as long as it lasts (`interestfor`, after .15s), a click or tap keeps it open (`popovertarget`, action `show`), Esc or a click elsewhere closes it. It is washed in the accent's soft tint with a faint accent edge and no shadow, so it stands apart from the fields it covers. CSS anchor positioning puts it 6px under its "?", starting just left of it; near the screen's edge it ends at the "?" instead, or centres under it, 8px inside the screen. A suggestion list is anchored the same way (`app.js` opens it with its field as the source): under its field and as wide, above it when the whole list fits there and not below.
* **Popovers and sheets** (More, Switch, Appearance, suggestion lists) are `popover`s: Esc and a tap outside close them, without JavaScript. On a phone the page behind goes grey (`--scrim`); what is grey is out of reach (a tap there only closes the popover). More and Appearance float just above the tab bar, rounded on all corners; the Switch tray hangs under the Working on bar, as wide and as tinted, rounded at the bottom.

---

## 7. Page Headers

Every page starts with the same header: the heading and the record's buttons on one row, and one line under it that runs the full width, under the buttons too.

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

* **Heading row**: `.page-head` is a grid. Its first row is at least a button tall (36px) with the heading centred in it, so the heading sits in the same place with or without buttons. `.page-title` and `.vehicle-hero` are `display: contents`: they group the markup, and their children are the grid's items.
* **The line**: at most one line under the heading, 4px below the heading row, spanning the full width. In order: the **Archived** badge first when the record is archived, then **chips**, then the other **badges** (the kit, item or category the record belongs to, then its states), then **text** in a `<span>`. Text never comes before a chip or badge. A sentence on its own is a `<p class="sub">`.
* **The heading names the record or the action** and nothing else: no vehicle names after a ` · `. A vehicle that can't change on the page (a kit's service, an edit) is a chip on the line; when a field on the page still chooses it, it isn't shown. On a form's line, chips and badges are plain labels.
* **The way back**: a record's page starts its buttons with a ghost ← button to where the record belongs, never to the page it came from: an item's to **Stock**, a service's to **Services**, a kit's to its vehicle (named), a vehicle's to **Vehicles** (not while you work on a vehicle: the bar's × leads there). An archived item or vehicle goes to the archived list it is in (`?archived=1`).
* **Narrow content** (under 680px of main column): the heading, the line under it, then the buttons as an even grid (`minmax(96px, 1fr)` cells, each button filling its cell): tidy rows however many there are, and two share the width.
* **Vehicle page**: the kind icon in a 64px tile of the vehicle's colour, left of the heading and the line and spanning both; 52px under 680px.
* The e2e test "every page header has the same spacing" checks the heading's place, the gap under it and the order of the line on every page.

---

## 8. Cards, Heading Bars and Footers

* **Heading bars** (`.card-head`, `.section-head`) are 45px whatever they hold: a 20px line or a 24px chip in 10px padding, and the rule under it. A badge in a card head (the item page's **On shopping list**) hangs into the padding. Only a head whose links wrap under the title on a narrow card grows. Their headings are `display: flex; align-items: center; gap: 8px`, so icons, text, chips and counts share one centre line; the icon is muted.
* **A stock section's heading is washed in its vehicle's colour** (`--chip` on the section, from the "stock-section" partial); a shared section in the accent, General in grey.
* **Delimiters**: separate a title from its count or secondary info with the middle dot ` · ` (e.g. `Stock for KillJoy · 0 items`).
* **Meta in a heading bar** (`.card-head .meta`) and the results count (`.results-head .count`): inline-flex, `gap: 6px`, muted, `--text-sm`.
* **Card footer (`.card-foot`)**: a total that sums the card's list (Service history's "Labour in total") goes in a footer band under the list, not the header: the label on the left, the amount on the right; with pages, Newer / Older take the left and the total sits on the right. 47px with its rule: 8px around a 30px button.
* **A card's actions are in its footer** (`.card-foot.card-actions`), under what they act on: the vehicle page's **Add kit**, a kit parts card's **Add 2 missing** (or **On shopping list**, once everything missing is listed), Shopping's **Add all**. They start from the left, like the pager. The heading names the card (title, a count, its link such as **All services →**, and **Reset order**) and holds nothing you do there. A state of the record rather than an action on the list (the item page's **On shopping list**) may stay in the heading. Printed pages leave the footer out.

---

## 9. Links

Links say they are links without the browser's default blue. Six tiers:

| Link Tier | Default Styling | Hover Styling | When to Use |
|---|---|---|---|
| **SKU / Part Numbers (`.sku-link`)** | `font-family: var(--mono); text-decoration: underline dotted; text-underline-offset: 3px;` | `color: var(--accent-strong); text-decoration-style: solid;` | **Exclusively** for part numbers and SKUs. Signals an external part number lookup. |
| **Data & Fact Links (`.facts a`)** | bold, underlined (40% of the text colour), offset 3px | `color: var(--accent-strong);` | Clickable values in a facts list (e.g. "Last done"). |
| **Inline Paragraph Links** | underlined (40% of the text colour), offset 3px | `color: var(--accent-strong);` and a full underline | Links in body text, empty states and hints. |
| **Contextual & Content Links** | `color: inherit; font-weight: 600;` | `color: var(--accent-strong);` | Titles in lists (`.list .title`), table items (`.table a`), card links (`.more`, muted). Row context already says they are clickable. |
| **Reset & Filter Actions** | muted, bold | `color: var(--accent-strong);` | `✕ Reset order` (icon on the left when right-aligned), `Clear filters ✕` (`.reset-link`, `.sort-reset`). |
| **Metadata Actions** | muted, bold | `color: var(--accent-strong);` | `+N more` SKUs (`.sku-more`). |

> [!NOTE]
> The global rule is `a { color: inherit; text-decoration: none; }`. Running text (`p`, `.hint`, `.card-body`, `.welcome`, `.empty`) underlines its links that have no class; component links (`.btn`, `.chip`, `.badge`, `.tile`, `.vehicle-card`, `.kit-row`) control their own states and never inherit an underline or a container's hover colour. A vehicle title in a list (`.list .title.vehicle-title`) takes its vehicle's colour on hover, matching its icon.
> Secondary context in a fact (`.facts dd .muted`) is `<span class="muted small">(…)</span>` at weight 500, so history doesn't compete with the current figure.

---

## 10. Chips vs. Badges

Chips and badges do different jobs and look different, but are both **24px** tall at the same text size, so they share a baseline side by side.

### Vehicle Chips (`.chip`, `a.chip`)
* **Purpose**: names a vehicle or a set of vehicles.
* **Shape**: a pill, a 1px border of the vehicle's colour at 35%, a 13% tint behind, text 55% of the colour into the text colour.
* **Dot**: an 8px `.dot` in the vehicle's colour (`--chip`, from `chipStyle`).
* **Size**: 24px (`--text-xs`, weight 600, an 18px line in 2px padding and the border).
* **Hover (as a link)**: the border takes the full colour. **No underline.**
* **Chips you pick** (`.chip-toggle`: the filter bars, Fits these vehicles) are 30px, `--text-sm`, with a 6px radius like the other toggles; picked, they take the vehicle's colour.
* **In the sidebar** (and the More sheet), **Work on** and **Stock by vehicle** are a list like the nav above them: 36px rows, the vehicle's dot where the nav has its icon, the name where its labels start; the one worked on is lit in its colour. The phone's Switch tray keeps them as 30px chips.
* **Placement in rows**: in compound rows (`.kit-row`), the chip sits on the title line (`.title-row`), leaving the meta line plain. Inside a vehicle's scope (below), a row leaves out that vehicle's chip.
* **Vehicle entity titles**: where the vehicle itself is the record (the `/vehicles` cards, the dashboard's Vehicles card), it is a title with its kind icon in its colour (`.vehicle-title`, `.vehicle-card .name`), not a chip.

#### Scope: say the vehicle once
A page about one vehicle names it once, at the top. Its rows and headings show a chip only for a *different* vehicle, which then says something ("Shared with KTM 1290"). A list that mixes vehicles keeps every chip, because there the chip tells the rows apart.

| Page | Names the vehicle by | Leaves out its chip on |
|---|---|---|
| A vehicle's page | its hero | kit rows, service rows, stock section headings |
| A kit's page | the header `chip-link` | service rows |
| `/services?v=ID` | the title ("KTM 390 services") and the active filter chip; while it is the vehicle worked on, the Working on bar (the title stays plain, the chips are hidden) | service rows, kit rows |
| `/stock?v=ID` (exactly one vehicle) | the title ("KTM 390 stock") and the active filter chip; while it is the vehicle worked on, the Working on bar | section headings, and list rows that fit only that vehicle |
| `/?v=ID` (the dashboard while working on it) | the Working on bar | Upcoming service's kit rows |
| `/shopping?v=ID` | the title ("KTM 390 shopping") and the active filter chip; while it is the vehicle worked on, the Working on bar | every row's chips: entries and suggestions show none in a vehicle's part, not even a shared entry's other vehicles |
| An item fitting one vehicle | the header chip | the rows of that vehicle's kits |

A stock row (`item-row`) is the one row with several vehicles: it leaves out its chips only when it fits the scope vehicle alone, and otherwise shows all of them, so a shared item never reads as belonging to one of the others alone.

The partials take the scope as `"Here"` (a vehicle id, 0 for none): `stock-section`, `service-list`, and `item-row` (`rowFor`, kept across a quick +/- by a hidden `here` input). `kit-row` takes `kitView.HideVehicle` (`scopeKits`).

**With one active vehicle**, there is nothing to work on (no Work on list, no bar), and that vehicle is the scope of every page (`soleVehicle`): rows leave out its chip everywhere (services, the dashboard's kits, an item's kits, and stock list rows that fit only it). Headers, section headings, filters and forms keep it, and a row of an archived vehicle or of General keeps its chip.

Stock section headings (`stock-section`) answer one question, which vehicles the items fit, each with an icon and a word up front: `warehouse` **Only** + chip, `tag` **Shared** + chips, `package` **General** (not tied to a vehicle). Inside a scope they read `warehouse` **Only this vehicle** and `tag` **Shared with** + the other vehicles' chips.

### Status & State Badges (`.badge`, `a.badge`)
* **Purpose**: states, statuses, quantities, kit tags and categories (`1 PART MISSING`, `PARTS READY`, `ARCHIVED`, `OVERDUE`, `ON SHOPPING LIST`, `OUT OF STOCK`, `525 ML IN STOCK`).
* **Category badge**: item categories are `.badge.badge-muted` with the tag icon. On an item page they sit after the vehicle chips on the header's line, as a plain label; on a stock row they link to the stock filtered by that category.
* **Shape**: 4px radius, a soft fill of the badge's status colour (`--badge-*-soft`), that colour (`--badge-*`) for its text.
* **Typography**: capitals (`text-transform: uppercase`), `--text-xs`, weight 650, tracked out `.04em`; a 12px icon.
* **Size**: 24px (an 18px line in 3px padding).
* **Hover (as a link)**: the fill deepens (`.badge-muted:hover`), or turns into the accent (`.badge-accent:hover`). **Never an underline.**
* **A status that belongs to a button is part of it**, never a badge beside it. Log service's "Start from a kit" buttons (`.kit-start`) end in the kit's due badge, flush to the button's border behind a 1px divider, with `Due.Summary` as the button's title. Its text stands on the name's baseline, not centred. Every kit with a schedule ends in its status, On track too; a kit without a schedule is just its name.

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
* **Part rows** (`line-row`): on the service form (`#part-lines[data-stock-need]`) the rows of one item are compared, together, with what is in stock, so a row that saving would refuse is red before Save. The kit form shows out, low or ok only. A refused save marks the short rows (`has-error`) without repeating the badge as an error. `app.js` keeps each row's badge up to date, copying it from the page's `{{template "stock-badge-templates"}}`, so the markup and icons live only in the Go template.
* Every stock badge carries `data-stock-level`, for tests and scripts. It is a plain label, never a link.

### When a chip or badge is a link

A chip or badge is a link only when it takes you somewhere you would go next from it, so the same thing does the same everywhere:

| It | Links to | Where |
|---|---|---|
| **Goes up** to the record the page belongs to | that record | A service's header: its vehicle (`chip-link`) and its kit. A kit's header: its vehicle. |
| **Filters the stock** from a stock row (`item-row`) | `/stock?v=ID` (General: `/stock?general=1`), `/stock?cat=…` | The row's vehicle chips (`vehicle-chips` with `"Link" true`) and its category badge. |
| **Is a filter** | the filtered list | The filter bars (`chip-toggle`), and the sidebar's "Stock by vehicle" while there is one vehicle. |
| **Switches what you work on** | `POST /working-on`, then back to the page | The sidebar's Work on rows (on a phone, in the More sheet) and the bar's Switch: `button.chip` with `aria-pressed`, in a form of their own, never on a record's form. |

Everything else is a plain label (`{{template "chip" …}}`, `<span class="badge">`):

* Tags that only describe the record in its own header: an item's vehicles and category.
* Chips and badges in list rows other than stock rows: kit rows, service lists, an item's "Service kits" card, the shopping list's entries and suggestions. A row that is a link itself (`.kit-row`, `.vehicle-card`) can't hold another link; the others match it.
* Status and state badges (due, parts ready/missing, stock, expiry, archived). The exception is **On shopping list**, on the item page and on a kit's parts card, which links to `/shopping` (a plain label while a vehicle is worked on and the entry is on another vehicle's part of the list).
* Anything on a form. A link there would leave what was typed; Cancel goes back.

Only links that leave the app open a new tab (`target="_blank" rel="noopener noreferrer"`): SKU searches. The item's photo opens larger in a modal `<dialog>` on the page (`.lightbox`, opened with `command="show-modal"`; Close, Esc, Back or a tap outside closes it), never in a tab. A form's hint may open a new tab when its point is to keep the form (the kit form's "add the item"). A chip or badge never does.

---

## 11. Tables and Status Discs

* **Rows are centred vertically** (`vertical-align: middle`), so status discs, wrapped names and quantities stay balanced.
* **Columns**: cells have 12px on each side (24px between columns), and the first and last cell the card's `--inset`, so the first column lines up with the card's heading. Headers are `--text-xs` capitals on the card's surface, a hairline (`--border`) under them.
* **Narrow cards**: below 660px of card, a table in a `.table-wrap` keeps 640px and scrolls inside its card; the page itself never scrolls sideways.
* **Status discs (`.status-icon`)**, 28px circles with a 14px icon:
  * **Service kits** (`kit-row`, the item page's "Service kits"): `.status-ok` `check` (all parts in stock), `.status-short` `x` (missing), `.status-muted` `minus` (no part lines), `.status-muted` `archive` (archived).
  * **A kit's parts** also show what is in stock against what the kit needs as a `<meter>` under the item's name (6px, green, red when short; `aria-hidden`, as the row's figures say it).
  * **A kit's parts** (`kit-parts-table`, a disc per row): `.status-ok` `check`, `.status-short` `x` (missing), `.status-listed` `shopping-cart` in the accent (missing, and the shopping list asks for at least that much: `stock.KitLine.Listed`), `.status-muted` `archive` (an archived item). Each disc says what it means in its `title` and `aria-label`, as a disc alone could pass for a button.

---

## 12. Working on a vehicle (`web/working.go`)

You pick the vehicle you work on, and the dashboard, Stock, Services, Activity and Shopping are about it, the way parts shops let you pick your vehicle once and show what fits it. It is a cookie (`working_on`, like the theme and the accent), so it belongs to the device: the phone in the garage works on the bike on the stand while the desktop sees every vehicle.

* **While the bar shows, a list page shows its vehicle.** Dashboard, Stock, Services, Activity and Shopping (below) always show the vehicle worked on (Stock and Activity with General). The cookie is never a hidden filter: an address that asks for anything else (no vehicle, `v=all`, another vehicle, General only, the vehicle without General, a vehicle that doesn't exist, an old bookmark, a link copied before) gets a 303 to the same address for the vehicle (`/stock?general=1&v=ID`, `/services?v=ID`, `/activity?general=1&v=ID`, `/?v=ID`), keeping its search, filters and sort (not its page number). So the address always says what the page shows, and the bar and the page never disagree. While it shows, titles are plain ("Stock"): the bar names the vehicle. Only an item's history (`/activity?item=`) is a record and keeps its address. Activity counts a vehicle's own services' parts, not the parts another vehicle's service took from a shared or General item (its subtitle says so); its vehicle and General chips, shown when no vehicle is worked on, work like the stock's. Without a vehicle worked on, a vehicle id that doesn't exist counts as none chosen.
* **The bar** (`working-on` partial) is the first thing in `<main>` on every page, washed in the vehicle's colour: its kind icon in its colour, **Working on KTM 390** (a plain link to the vehicle's page, except on that page; on hover the name takes the vehicle's colour, never an underline), and its buttons as icons (their names are tooltips and screen readers' labels). On a desktop that is **All vehicles** (an ×, which stops) alone: the sidebar's Work on list, right beside it, switches. On a phone, where the sidebar is hidden, the bar runs edge to edge at the top and stays in view as the page scrolls, and it adds **Switch**: a `popover` tray under the whole bar, as tinted, rounded at the bottom, listing the other vehicles' Work on chips. The page behind it goes grey, the bar stays bright (the grey starts at the bar's bottom edge, which `app.js` measures as it opens), and a tap outside or Esc closes it. Nothing narrows a page without the bar saying so.
* **The bar is the vehicle control.** Meanwhile the pages hide their own vehicle and General chips: the vehicle's stock always comes with General, the items that fit any vehicle, so nothing floats on its own (Stock's filter keeps them in hidden fields), and Log service and Add kit don't ask for the vehicle. Links to another vehicle's list, which would show the worked-on one's, are hidden: a stock row's vehicle chips are labels, and the dashboard leaves out its vehicle cards. To see another vehicle, work on it (or stop).
* **Another vehicle is out of view.** What belongs to another vehicle, its page, its kits and services and their edit forms, and the pages about every vehicle, the Vehicles list (archived ones too) and Add vehicle, wait until you switch or stop: a GET gets a 303 to the vehicle worked on's page (a service: to Services, which is its list), with htmx too (`elsewhere`, `working.go`). One rule, so nothing on screen is about a vehicle the bar doesn't name. Links to them are names, not links: an item page's kits and its history's services of other vehicles. Switching on a vehicle's page opens the one now worked on, and stopping there opens the list. A save from a page opened before isn't refused (it changes what is there already, which isn't new work), and restoring another vehicle's vehicle, kit, service or shopping entry stays on the trash.
* **Shopping is the vehicle's part of the list** (`/shopping?v=ID`), with the same 303. An entry is on it when its item fits the vehicle or is General, or when one of the vehicle's active kits uses it (so a kit's shortfall, once added, stays on the list even for an item tagged elsewhere), and free text always is: it belongs to no vehicle. Which vehicles an entry is for is worked out from the item's tags and the kits (`stock.ShoppingEntry.For`), never stored: each item has one entry, so a shared item two vehicles need is one line, and a stored vehicle would go stale when an item is retagged. The suggestions are the vehicle's too: what fits it below its minimum, and its own kits' shortfalls; what is listed counts whoever it was added for. The Add form offers the vehicle's items and the General ones, so nothing added there disappears. At the shop the rest is easy to forget, so the list says **N more for other vehicles**, and the whole list is the bar's × away. The sidebar's and the tab bar's counts are the part's. Without a vehicle worked on, chips filter it like Services' (All vehicles · each vehicle), and what you do on a vehicle's part comes back to it (its forms and the Bought page carry `v`). On the whole list each entry and suggestion carries the vehicles it is for as chips (labels; none for General and free text; an archived vehicle's too, and never the sole active vehicle's, which is every page's scope). A vehicle's part has no row chips: it says the vehicle once. The vehicle is a chip, never text: a reason names what needs the part, **Short for the Air Filter kit** (`stock.ShortNote`), and a saved note keeps it so. The whole list's Copy as text ends each line with its vehicles, `[GreyJoy, KillJoy]`, as notes don't say whose.
* **Never narrowed:** what is shared: an item (it may fit several vehicles, or none) and its edit form, and Trash. They show the bar only. Trash is the undo safety net, newest first, and many of its entries belong to no one vehicle. Archives need nothing of their own: Stock's Archived is a filter on `/stock`, so it follows the vehicle; archived kits are on their vehicle's page; an archived vehicle can't be worked on. Exports (Copy as text, CSV) have no bar and follow their address only. A search on the vehicle's stock says what it finds elsewhere: **N more in other vehicles** (the bar's All vehicles, right above, keeps the search).
* **New work is for the vehicle worked on**, like "archived means no new work". **Log service** and **Add kit** are for it without a Vehicle field, and Log service offers only its kits to start from; another vehicle's pages are out of view (above). An old link to another vehicle's form (`?vehicle=`, `?kit=`) opens the worked-on one's, and a save from a form opened before a switch comes back (422, keeping what was typed, with the Vehicle field and the other vehicle chosen) saying "You're working on GreyJoy: choose it, or work on LittleJoy first to log this service" (a kit's service, which can't change vehicle: "work on LittleJoy first to log this service"; a kit: "… to add this kit"). **Add item** starts with the vehicle ticked, but an item isn't work on a vehicle (it may fit several, or none), so its vehicles stay a choice, and its *add another* keeps a General item General. A save to another vehicle's existing record from a page opened before still goes through: that isn't new work.
* **The nav's Vehicles becomes Vehicle**, with the vehicle's kind icon in its colour instead of the warehouse, and opens its page. `/vehicles` opens it too (above); stopping (the bar's ×) leads back to the list, and only then does a vehicle page show its **Vehicles** ghost button, first in its header.
* **The dashboard** leaves out its Vehicles panel meanwhile: the bar, the nav's Vehicle and Switch lead to the others. It counts what its lists open: the figures link to the vehicle's stock (with General) and activity, so each number is the rows it opens; Stock value and Spent count those items too. The Shopping list figure and **N suggested →** count the vehicle's part of the list and open it; so does a vehicle's dashboard without one worked on (`/?v=ID`).
* **Where you choose it:** the sidebar's **Work on** list (2 or more active vehicles; with one, it is off, the choice is cleared so it doesn't come back months later with a second vehicle, and the sidebar keeps "Stock by vehicle"), and the bar's Switch; on a phone, the sidebar's list is in the More sheet.
* **On a phone** the tab bar is Dash · Stock · Services · Shop · **More**. More opens the sidebar itself as a sheet floating above the tab bar, without what the tab bar has (Stock, with its `/`) or Add item and Log service with their keys (every page has them, and a phone has no keyboard). It is a `popover`, like Switch: a tap outside or Esc closes it, and it closes when the page is left, so Back doesn't bring it open. The page behind goes grey; More is highlighted while it is open. What is grey is out of reach: a tap there only closes the popover, it never opens what is under it. So the phone has what the desktop has, in one place: Appearance, Vehicle or Vehicles, Activity, Trash and the Work on list, and the pages need no shortcuts of their own to them. More is current on the pages it leads to.
* A vehicle archived or deleted stops being worked on (the cookie is cleared on the next page, and so is one that isn't the plain digits the server writes). A page that Back brings from before a switch reloads (`app.js`, on `pageshow`, comparing the cookie with the page's `data-working-on`, which is rendered even when the vehicles can't be read), unless it holds unsaved typing.

---

## 13. Capitalization: Catalog Entities vs. System Labels

Pitbox keeps a strict distinction in capitalization:
* **Catalog entities and products (Title Case)**: part names, brands, categories, services, vehicle names and service kits are named catalog entities: `Hiflofiltro Oil Filter`, `Oil Service (Intermediate)`, `Air Filter`, `Brake Pads (Rear)`, `Oil & Fluids`, `Gaskets & O-Rings`.
* **System actions, filters and statuses (sentence case)**: actions, status texts, notes and filter options are written in sentence case: `Starting stock`, `All types`, `All vehicles`, `Out of stock`, `Clear filters`, `Used in this service`, `1 part missing`. Badges show them in capitals through CSS (`text-transform`), so the text itself stays sentence case.

---

## 14. Focus Rings

Where focus is always shows, the way browsers decide it (`:focus-visible`). A new control gets its ring from its class, never one of its own.

* **Fields show it on any focus**, click or keyboard: anything you type into or choose from (`.input` on an input, select, textarea, suggestion, date or search field). You type next where the ring is. The ring is the field's own 1px border plus a 1px box-shadow in `--ring` (2px solid), then a 3px `--ring-soft` glow. `--ring` is the accent, or `--danger` in a field with an error (`.has-error`, or `:user-invalid` once you have left a required field empty), so a field never mixes a red border with an accent ring. An open select keeps it (`select.input:open`), as its list takes the focus from it.
* **Buttons and other clickables show it from the keyboard only** (`:focus-visible`): a 2px solid `--accent` outline, 2px off the control (1px for sort links), so it shows on a filled button too. A mouse click leaves no ring.
* **Forced colours.** Windows contrast themes drop box-shadows, so a field's focus also sets `outline: 2px solid transparent`, which the system paints as a real ring there. Cards' 1px `--border` outline is a real border for the same reason: the system draws it.
* **Always there, 2px.** Both rings show on focus (2.4.7) and are 2px of the accent; the glow alone isn't the ring. With the mellow palette the accent is softer than the 3:1 of 2.4.13 asks (see [Contrast](#contrast)).
* **Nothing moves.** A ring is a box-shadow or an outline, never a thicker border.
* **For a new control:** use `.input` for a field and `.btn` or `.icon-btn` for a button, and the ring comes with the class. Never `outline: none` without a ring in its place.

---

## 15. Motion

* **Page changes are instant**: no cross-fade (no `@view-transition`), so following a link and an in-place update (the stock's filters) feel the same.
* **Popovers and dialogs come in**: More, Appearance and the Switch tray slide a few pixels and fade, a confirmation and a hint fade (and grow) in (`@starting-style`), and go out the same way (`overlay`, `display` with `allow-discrete`). Toasts rise in; a flash fades in; a stock row flashes the accent's soft tint after a quick +/-.
* **Short and quiet**: .15–.3s, ease-out. Hover states fade in .15s.
* **Less motion when asked.** Under `prefers-reduced-motion: reduce` every transition and animation ends at once.

---

## 16. Whole Pixels & Alignment Rules

Every box starts and ends on a whole CSS pixel. Text is snapped to whole pixels and icons and borders aren't, so a box on a fraction puts its text off its icon (differently in each row) and blurs its borders. In `app.css`:

* Line heights through `--lh` (each element's line is `--lh` times its own font size, rounded to an even number of pixels), never `line-height`.
* Lengths in `px`; only font sizes are in `rem` (§3). No lengths in inline styles.
* Heights that something is centred against are even: icons `round(1.15em, 2px)`, controls 36/30px, chips and badges 24px, discs 28px.
* Ratio- or font-based sizes are rounded (`round(…, 2px)`, `round(down, …, 1px)`).
* Text in a box at a size that centres in Manrope (§3).
* Never rely on default baselines for mixed-font or icon-and-text inline content: use flex with `align-items: center`.
* The e2e test "every box sits on whole pixels and its text lines up" checks every page: text within 1px of its icons and neighbours, within .3px of its own box's middle, and chips and badges 24px.

---

## 17. The stylesheet

`app.css` is hand written, with no build step, in cascade layers declared up front:

```css
@layer reset, tokens, base, shell, components, pages, utilities;
```

* **reset**: box sizing, the whole-pixel line height, `[hidden]`, reduced motion. Its `!important` beats every later layer's.
* **tokens**: `:root` custom properties: colours, type, sizes, spacing.
* **base**: elements, running-text links, and the small utilities a component may refine (`.flex`, `.muted`, `.small`).
* **shell**: sidebar, tab bar, More sheet, the main column.
* **components**: one nested block per component, holding its variants, states, `:has()` forms and its `@container` rules.
* **pages**: rules for one page only (and print).
* **utilities**: spacing asked for in the markup (`.mt`, `.mb`), which wins over a component's own margins. Only spacing: a utility that set more would override the components that refine it.

Native nesting, with declarations before nested rules; never nest under a selector list (its `:is()` takes the list's highest specificity). No `@supports` and no fallbacks: what current Chrome has is used as it is (customizable `<select>`, `text-wrap: pretty`, exit transitions, `interestfor`).
