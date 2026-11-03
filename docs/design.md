# Design & UI Conventions

This document defines the interface standards, component rules, link styles, and layout alignments for Pitbox. Follow these rules whenever creating or modifying templates and styles.

---

## 1. Interactive Links & Affordance

Links must communicate their interactive nature clearly without relying on unstyled browser defaults. We use four distinct link tiers:

| Link Tier | Default Styling | Hover Styling | When to Use |
|---|---|---|---|
| **SKU / Part Numbers (`.sku-link`)** | `font-family: var(--mono); text-decoration: underline dotted; text-underline-offset: 3px;` | `color: var(--accent); text-decoration-style: solid;` | **Exclusively** for part numbers and SKUs. Signals external part number lookup. |
| **Data & Fact Links (`.facts a`)** | `font-weight: 600; text-decoration: underline; text-underline-offset: 3px;` | `color: var(--accent);` | For clickable data values embedded in data lists (e.g. "Last done" date in schedule). |
| **Inline Paragraph Links** | `text-decoration: underline; text-underline-offset: 3px;` | `color: var(--accent);` | Links inside body text, empty states, or hints (e.g. *"No vehicles yet. Add one..."*). |
| **Contextual & Content Links** | `color: inherit; text-decoration: none; font-weight: 600;` | `color: var(--accent);` | Titles in lists (`.list .title`), table items (`.table a`), card links (`.more`). Row or container context already signals clickability. |
| **Reset & Filter Actions** | `color: var(--muted); text-decoration: none; font-weight: 600;` | `color: var(--accent);` | Secondary reset links like `✕ Reset order` (icon on left when right-aligned) or `Clear filters ✕` (icon on right when left-aligned) (`.reset-link`, `.sort-reset`). Never hardcode static accent color on these. |
| **Metadata Actions** | `color: var(--muted); text-decoration: none; font-weight: 700;` | `color: var(--accent);` | `+N more` SKUs (`.sku-more`). |

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
* **Placement in Rows**: In compound item rows (e.g. `.kit-row`), the vehicle `.chip` sits on Line 1 next to the record title (`.title-row`), leaving Line 2 (`.meta`) as clean, unobstructed metadata text.
* **Sidebar Navigation & Filters**: Vehicles in the sidebar (`.nav-vehicles`), filter bars, and tag lists are standard `.chip` / `chip-link` pills, standardizing vehicle tags everywhere.
* **Vehicle Entity Titles**: When a vehicle itself is the primary record of a card or list row (e.g. on `/vehicles` cards and the dashboard "Vehicles" card), it is represented as a title link with its vehicle kind icon (`{{icon .Kind.Icon}}`) colored with `var(--chip)` (`.vehicle-title`, `.vehicle-card .name`), distinguishing primary record titles from secondary reference chips.

### Status & State Badges (`.badge`, `a.badge`)
* **Purpose**: Identifies states, statuses, quantities, kit tags, or categories (e.g. `1 PART SHORT`, `PARTS READY`, `ARCHIVED`, `OVERDUE`, `ON SHOPPING LIST`, `MY KIT`, `FILTERS`).
* **Category Badge**: Item categories on item detail pages are standard badges (`a.badge.badge-muted`), positioned immediately next to the vehicle chips in `.item-head-meta`, with 26px height and badge hover transitions.
* **Shape**: Rounded rectangle (`border-radius: 6px`).
* **Typography**: Bold uppercase text (`font-size: .72rem`, `font-weight: 700`, `letter-spacing: .03em`).
* **Dimensions**: Height **26px** (`padding: 4px 8px`, line-height `18px`).
* **Hover (as link)**: Container background color transition (e.g. `.badge-muted:hover { background: var(--surface-3); }`, `.badge-accent:hover { background: var(--accent); color: var(--on-accent); }`). **Never use a text underline on badges.**

---

## 3. Section & Card Headers

* **Flex alignment**: All heading containers (`.card-head h2`, `.results-head h2`, `.section-head h2`) must use `display: flex; align-items: center; gap: 8px;` so icons, heading text, delimiters, and count badges center on the exact same vertical line.
* **Delimiters**: Separate a title from its item counter or secondary info using the middle dot ` · ` (e.g. `Stock for KillJoy · 0 items`).
* **Card Head Meta (`.card-head .meta`) & Results Count (`.results-head .count`)**: Sort-reset links, bullet delimiters, and count texts must be wrapped in an inline-flex container (`display: inline-flex; align-items: center; gap: 6px; font-size: .85rem; color: var(--muted);`).

---

## 4. Vehicle & Record Hero Headers

* **Hero subtitles**: Do not place badges inline within plain `<p class="sub">` text with floating text bullets.
* **Use flex containers**: Wrap the subtitle text and the status badge inside `.sub` (`display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-top: 4px;`):
  ```html
  <div class="sub">
    <span>{{with $v.Subtitle}}{{.}} · {{end}}{{$v.Kind.Label}}</span>
    {{if $v.Archived}}<span class="badge badge-muted">{{icon "archive"}} Archived {{date $v.ArchivedAt}}</span>{{end}}
  </div>
  ```
  This ensures the badge and text align cleanly on whole pixels without baseline drift.

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

