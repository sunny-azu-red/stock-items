# Kit schedules and the warranty

How a service kit decides which service comes next, when it is due, and when the vehicle's warranty is lost. The rules live in `internal/stock` (`kits.go`, `warranty.go`, `validate.go`); the store loads what they need and enforces what can be saved (`internal/store/kits.go`, `services.go`, `vehicles.go`). Change the code and this file together.

The worked examples are the kit schedule matrix (artifact <https://claude.ai/artifact/CUe5MX3g8DFptHWuPQGgia>): every case ID below (B6, W11, L4, …) is one of its situations, run through this code on a sample kit (10 000 km / 12 months, halfway, break-in at 1 000, bought new on 1 Jan 2025), plus 120 simulated riders over three years.

## Terms

| Term | Meaning | Code |
|---|---|---|
| Kit | A recurring job on one vehicle: parts, and optionally a distance (`interval_distance`) and/or months (`interval_months`). | `stock.Kit` |
| Full | The kit's main service, with its full parts. | `TierFull` |
| Intermediate | With **Halfway between full services** ticked: a lighter service at half the distance, at the full months. Needs an even distance. | `Kit.Intermediate`, `InterStep` |
| Break-in | A new vehicle's first service at the kit's **Break-in at** reading, with the full parts. Only on a vehicle bought below that reading. | `TierBreakIn`, `Kit.HasBreakIn` |
| Step | Half the distance and the full months (with halfway). | `InterStep` |
| Counted from | The service a schedule counts from: the last service of any kind (`counted`, never the break-in) or the last Full (`LastFullService`); the purchase (`purchased_on`, `odometer_at_purchase`) before the first. | `Kit.counted` |
| Required by the warranty | A kit checkbox: the warranty asks for this kit's break-in and Full on time. | `Kit.WarrantyRequired`, `service_kits.warranty_required` |
| Grace | How late a required service may be before the warranty is lost: 10% of its distance (100 km on a break-in at 1 000, 1 500 on a 15 000 km interval) and 30 days, whatever the interval. | `WarrantyGrace`, `WarrantyGraceDays` |
| Last quarter | A quarter of the kit's own distance and of its months in days (2 500 km or about 91 days on 10 000 km / 12 months): the Full takes over from the intermediate inside it. | `FullTakesOver`, `Kit.fullClose` |

Schedules only count from the user's own services: nothing is logged before the purchase (date or reading), and a vehicle bought past Break-in at has no break-in ([data-model.md](data-model.md)).

## The warranty

A vehicle's warranty ends on `warranty_ends_on` (its last day) or past `warranty_ends_at` (a reading), whichever comes first (`stock.WarrantyOf`). Its state, worked out for today and the current odometer (`Vehicle.Warranty`, `Kit.Warranty`):

| State | Badge | When |
|---|---|---|
| `none` | nothing | No end entered. |
| `active` | Under warranty (green) | It runs. |
| `soon` | Warranty ends soon (amber) | It runs, with 30 days or 1 000 km left. |
| `lost` | Warranty lost (red) | It would run, but a required kit missed a service (below). |
| `ended` | Warranty ended (grey) | Past its day or reading. Shown even if it was lost first. |

`Warranty.Runs()` is true for `active` and `soon` only. **Lost and ended work the same as no warranty** for the schedules.

### When it is lost

Worked out from the history every time it is read (`warrantyWithKits`, `WarrantyKit.missed`), never stored. For each kit of the vehicle that is ticked **Required by the warranty** and not archived (`store.loadWarrantyKits` loads its break-in and Full services, oldest first):

1. **The break-in** (only when the kit has one on this vehicle): the kit's first Break-in or Full service must be read at most Break-in at + 10% (1 100). Done later, or not done while the odometer is past that, loses it (B2, L3). A Full logged on time counts as the break-in (B7); an intermediate doesn't (L13).
2. **Each Full**: within the kit's distance + 10% and months + 30 days of the last Full, or of the purchase before the first one (never of the break-in). Done later, or not done while past them, loses it (L7, L9, L11).
3. **Intermediates never count**, early, late or skipped (W12).

Every limit that passed fell while the warranty ran (it runs from the purchase until its end), so no dates need comparing. `Warranty.Missed` is the earliest miss of all the kits (by the day the late service was done, today for one not done). The vehicle page shows it ("Oil Service: full service due by 10 000 km or 1 Jan 2027, done at 11 200 km on 1 Oct 2026").

- **Backfilling clears it**: logging the missed services with their real dates and readings (B10 → L4), or correcting a wrongly entered reading, puts the warranty back.
- **Otherwise it stays lost**: on-time services after a late Full don't bring it back (L14). Decided 2026-11-26.
- A kit not ticked never loses it (L5). Ticking only decides that; it doesn't change the kit's schedule.
- Only kits that aren't archived count, and a kit's history is its own: archiving a required kit drops its misses, and a new kit replacing one starts from the purchase (its first Full is due an interval after it), so log its past services before ticking it. The kit form's hint says so. Duplicate starts unticked: the other vehicle's warranty may not ask for it.
- The **Overdue** badge still shows from the limit itself: the app aims at the limit, and the grace only decides the warranty.

## The schedules

| | Under warranty (`active`, `soon`) | No warranty, ended or lost |
|---|---|---|
| **Break-in** | Due at Break-in at, by distance only. Only as the kit's first service on the vehicle, once. No schedule counts from it. | The same. |
| **Intermediate** | A step after the last service of any kind (`InterDue`). | The same. |
| **Full** | Its own distance and months from the last Full (`FullDue`). Intermediates never move it. | Without halfway: the full interval from the last service. With halfway, **services take turns**: one step after an intermediate, two steps (the full distance, twice the months) after anything else. |

Losing or ending the warranty changes the Full the same day, also for services done while it ran: a light rider whose Full is a month past its year sees an overdue Intermediate next, with the Full at 24 months (W13). Decided 2026-11-26, as for an ended warranty on 2026-11-23: "if the warranty ended, it should not force you into an overdue full service". Without a warranty the Full can stay in 24 months for a light rider (N9, N10): decided, the distance matters more than the duration.

A **missed break-in** (`breakInMissed`): still waited for (nothing logged, the vehicle not past its first full distance) but the odometer is past Break-in at + 10%. A break-in can no longer be logged at that reading, so the Full is due in its place, at the break-in's reading or the kit's months after the purchase, overdue (`FullDue`; B2). Past the first full distance with nothing logged, the kit no longer waits for a break-in at all (B10, B11).

The **Full also comes before a waited-for break-in once it is close** (`breakInYields`: its last quarter, due soon or overdue): a light rider whose first Full's date comes before Break-in at (under 83 km a month on a 12-month kit) sees the Full about 3 months ahead, instead of a break-in due by distance only while that date passes. Logged by Break-in at + 10%, that Full counts as the break-in for the warranty.

## Which service is next

`Kit.Due` gives the next one, `NextIsBreakIn` and `NextIsIntermediate` which kind it is; Log service preselects that kind (`handlers_services.go`).

1. **Break-in**, if the kit waits for one, it isn't missed, and the Full isn't close (`NextIsBreakIn`).
2. **Full**, if the break-in gives way (missed, or the Full close), or an intermediate was done since the last Full (`InterSinceFull`).
3. **Full**, if it is in its **last quarter**, due soon or overdue (`fullClose`), with a warranty or without (W11, N6, E7, B11).
4. Under warranty only: **Full**, unless the intermediate falls due strictly before it by distance or by date (`dueBefore`).
5. Otherwise **Intermediate**.

Without halfway it is always the Full (after the break-in).

**Due soon** (the badge, `dueFrom`) is separate from the last quarter: within max(500, 10% of the distance), at most half the interval, or 30 days (at most half the interval in days). Exactly at the limit is still "soon", one unit or day past is "overdue". Months end on the same day of the month or the month's last day (`addMonths`).

## What is refused

**The service form** (`store.checkKitTier`, inside the transaction):

| Saving | Message |
|---|---|
| Intermediate with a kit without halfway | This kit has no intermediate service |
| Break-in with a kit without Break-in at, or on a vehicle bought past it | This kit has no break-in on this vehicle |
| Break-in after another service with the kit | This kit has an earlier service: only its first is the break-in |
| A second break-in | This kit has a break-in already |
| Intermediate as the first service of a kit with a break-in | This kit's first service is its break-in: log it as Break-in or Full |
| Break-in read past Break-in at + 10%, when it becomes a break-in or its reading changes | The break-in is due by 1 000 km (1 100 km at the latest): log this one as Full |

A missed break-in is still offered on Log service, to backfill one done on time; Intermediate isn't offered as a break-in kit's first service. A break-in saved late before the 10% rule still saves while its kind and reading stay (it counts as missed for the warranty). Services before the purchase, and with an archived kit or vehicle, are refused too ([data-model.md](data-model.md)).

**The kit form** (`KitInput.Validate`, and `store.checkKitBreakIn` where the vehicle is needed):

| Kit | Message |
|---|---|
| Halfway without a distance | Enter the distance: the intermediate service comes halfway through it |
| Halfway with an odd distance | Enter an even distance: the intermediate service comes halfway through it |
| Break-in at without a distance | Enter the distance: the break-in comes before it |
| Break-in at 0 | Break-in reading must be more than 0 |
| Break-in at at or past halfway (with halfway) | The break-in comes before the first 5 000 km: with Halfway ticked, the intermediate service is due there |
| Break-in at at or past the distance | The break-in comes before the first 10 000 km |
| Required by the warranty with no distance or months | Enter a distance or months: the warranty asks for the job on time |
| Break-in at at or below the vehicle's purchase reading (store) | LittleJoy was bought at 2 148 km, past its break-in |

The vehicle form refuses raising the purchase reading past a kit's Break-in at (`checkVehicleBreakIns`).

## Decisions

- 2026-11-23: the warranty, not the kit, decides strict or taking turns; the switch is the day after its end.
- 2026-11-24: one halfway intermediate service (an even distance); counting from the last service, like a trip meter; no schedule counts from the break-in (the manual's intervals run from the purchase); one break-in, the kit's first service; only the user's own services (the purchase floor).
- 2026-11-26 (matrix, second run):
  - Warranty lost, from history, for kits ticked Required by the warranty.
  - The grace is 10% of the distance and 30 days, uncapped. The user's research: dealers tolerate about 10% and a month; KTM's terms state no grace.
  - A late break-in is a Full: refused as Break-in. Past its grace, the Full is due in its place.
  - The Full takes over in the last quarter, in both modes.
  - Break-in at is below halfway.
  - Lost works as no warranty at once, and stays lost.
- 2026-11-26 (independent review and full scan): the Full comes before a waited-for break-in once close; an intermediate can't be a break-in kit's first service; late break-ins saved before the rule stay editable; the earliest miss is named; Duplicate starts unticked.

## Tests

- `internal/stock/kits_test.go`: `TestFullAndHalfService`, `TestBreakIn`, `TestFullTakesOver`.
- `internal/stock/warranty_test.go`: `TestWarrantyLost` (the grace edges), `TestWarrantyLostSchedulesInTurns`, `TestWarrantyLostNamesTheEarliestMiss`.
- `internal/stock/validate_test.go`: the kit form rows above.
- Over HTTP, `internal/web/warranty_test.go`: `TestBreakIn`, `TestBreakInLateRefused`, `TestWarrantyLostAndBackfilled`, `TestWarrantyLostHistory`, `TestWarrantyDecidesKitSchedule`.
- The full scan (the matrix's last section): every valid kit shape × 5 vehicles × 25 riders over four years, against eleven rules (R1–R11), with a throwaway harness run through `go run -overlay`.
