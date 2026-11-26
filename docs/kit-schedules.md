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

Worked out from the history every time it is read (`warrantyWithKits`, `WarrantyKit.misses`), never stored. For each kit of the vehicle that is ticked **Required by the warranty** and not archived (`store.loadWarrantyKits` loads its break-in and Full services, oldest first):

1. **The break-in** (only when the kit has one on this vehicle): the kit's first Break-in or Full service must be read at most Break-in at + 10% (1 100). Done later, or not done while the odometer is past that, loses it (B2, L3). A Full logged on time counts as the break-in (B7); an intermediate doesn't (L13).
2. **Each Full**: within the kit's distance + 10% and months + 30 days of the last Full, or of the purchase before the first one (never of the break-in). Done later, or not done while past them, loses it (L7, L9, L11).
3. **Intermediates never count**, early, late or skipped (W12).

Every limit that passed fell while the warranty ran (it runs from the purchase until its end), so no dates need comparing. Each Full is checked from the one before it, late or not, so a kit can miss several. `Warranty.Misses` lists every miss of every kit, sorted by **the day it lost the warranty** (`WarrantyMiss.LostOn`); the first (`FirstMiss`) is the one that lost it:

| The miss | `LostOn` | Shown as |
|---|---|---|
| Its date (+ 30 days) passed, its reading didn't | the day after the date + 30 days (`LostOnExact`) | 1 Feb 2026 |
| Its date passed and its reading too | the day after the date + 30 days: the reading may have passed earlier, on a day no reading was kept | by 1 Feb 2026 |
| Only its reading passed, done late | the day it was done | by 1 Jun 2026 |
| Only its reading passed, not done | today (sorted last) | past 16 500 km |

A service still not done is never sorted behind one done late since (it was dated today before 2026-11-26).

The vehicle page shows it in two places. Its facts say when (*Warranty lost*: the first miss's day, linking to the card). A red card under the Odometer, **Warranty lost**, lists every miss, the first first: the kit (linked) and kind, due by and the latest it could be done (the limit + 10% and 30 days), and what happened: done at a reading on a day (linked to the service) or not logged, and when it lost the warranty.

- **Backfilling clears it**: logging the missed services with their real dates and readings (B10 → L4), or correcting a wrongly entered reading, puts the warranty back.
- **Otherwise it stays lost**: on-time services after a late Full don't bring it back (L14). Decided 2026-11-26.
- A kit not ticked never loses it (L5). Ticking only decides that; it doesn't change the kit's schedule.
- **A ticked kit can't be archived** (`store.ArchiveKit`: "Untick Required by the warranty before archiving Oil Service: its services count for the warranty."): only kits that aren't archived count, so archiving would drop its misses. Unticking is the one way to say the warranty no longer asks for a kit.
- **A kit's history is its own.** A new kit for the same job starts from the purchase with no services (and a service can't move to another kit), so each service due since then counts as missed: edit a kit rather than replacing it. The kit form's hint says so. Duplicate starts unticked: the other vehicle's warranty may not ask for it.
- **Judged by the kit's numbers as they are now.** Changing a ticked kit's distance, months or Break-in at changes the verdict on services already done: GreyJoy's Full at 12 822 km on 28 Apr 2026 is on time on a 15 000 km kit, late on a 10 000 km one (limit 11 000). Decided 2026-11-26: the kit's numbers are the manual's and rarely change; changing them back gives the warranty back.
- The **Overdue** badge still shows from the limit itself: the app aims at the limit, and the grace only decides the warranty.

## The schedules

| | Under warranty (`active`, `soon`) | No warranty, ended or lost |
|---|---|---|
| **Break-in** | Due at Break-in at, by distance only. Only as the kit's first service on the vehicle, once. No schedule counts from it. | The same. |
| **Intermediate** | A step after the last service of any kind (`InterDue`). | The same. |
| **Full** | Its own distance and months from the last Full (`FullDue`). Intermediates never move it. | Without halfway: the full interval from the last service. With halfway, **services take turns**: one step after an intermediate, two steps (the full distance, twice the months) after anything else. While the break-in is waited for, its own interval from the purchase (below). |

Losing or ending the warranty changes the Full the same day, also for services done while it ran: a light rider whose Full is a month past its year sees an overdue Intermediate next, with the Full at 24 months (W13). Decided 2026-11-26, as for an ended warranty on 2026-11-23: "if the warranty ended, it should not force you into an overdue full service"; looked at again and kept the same day, in the fresh-eyes review. Without a warranty the Full can stay in 24 months for a light rider (N9, N10): decided, the distance matters more than the duration.

**While the break-in is waited for** (`breakInPending`: nothing logged, the vehicle not past its first full distance), the Full counts the kit's own distance and months from the purchase, **with a warranty or without**: the first service is the break-in or the Full, never an intermediate, so nothing takes turns yet. Without a warranty it used to count two steps (10 000 km or 24 months), so a light rider (50 km a month on a 10 000 km / 12 month kit) saw "Break-in, in … km" with no date for 20 months, then an intermediate 245 days overdue the day after the break-in; and a lost warranty turned an overdue Full back into a break-in with no date for 8 months (the full scan's 9 "Full → Break-in" cases). Now the Full comes in its year (counting as the break-in), then one service a year in turns. After the break-in (or the Full in its place), the schedules work as above.

A **missed break-in** (`breakInMissed`): still waited for (nothing logged, the vehicle not past its first full distance) but the odometer is past Break-in at + 10%. A break-in can no longer be logged at that reading, so the Full is due in its place, at the break-in's reading or the kit's months after the purchase, overdue (`FullDue`; B2). Past the first full distance with nothing logged, the kit no longer waits for a break-in at all (B10, B11).

The **Full also comes before a waited-for break-in once it is close** (`breakInYields`: its last quarter, due soon or overdue): a light rider whose first Full's date comes before Break-in at (under 83 km a month on a 12-month kit) sees the Full about 3 months ahead, instead of a break-in due by distance only while that date passes. Logged by Break-in at + 10%, that Full counts as the break-in for the warranty. **It stands in for the break-in, so it is due by Break-in at too**, or its date, whichever comes first (`Kit.Due`): "Full · in 100 km · in 91 days". Showing its own 10 000 km hid the reading: a rider of 100 km a month saw "Full · in 9 100 km" at 900 km and lost the warranty at 1 101 km before anything showed overdue (found by the full scan's 50 km/month rider, 2026-11-26). Logged as Full rather than Break-in on purpose: it is also the yearly service, and a break-in restarts no schedule, so the Full would be overdue the next day.

## Which service is next

`Kit.Due` gives the next one, `NextIsBreakIn` and `NextIsIntermediate` which kind it is; Log service preselects that kind (`handlers_services.go`).

1. **Break-in**, if the kit waits for one, it isn't missed, and the Full isn't close (`NextIsBreakIn`).
2. **Full**, if the break-in gives way (missed, or the Full close: then due by Break-in at or the kit's months from the purchase), or an intermediate was done since the last Full (`InterSinceFull`).
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

Log service offers Break-in on a new service until one is logged with the kit (`Kit.BreakInLogged`), also after other services, to backfill one done on time: the store takes it only as the kit's first service, so it must be dated before them, and refuses it dated after ("This kit has an earlier service: only its first is the break-in"). It starts with the kind due next, never Break-in once something is logged. Intermediate isn't offered as a break-in kit's first service. A break-in saved late before the 10% rule still saves while its kind and reading stay (it counts as missed for the warranty). Services before the purchase, and with an archived kit or vehicle, are refused too ([data-model.md](data-model.md)).

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

The vehicle form refuses raising the purchase reading past a kit's Break-in at (`checkVehicleBreakIns`). A kit ticked Required by the warranty can't be archived (above).

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
- 2026-11-26 (fresh-eyes review; explainer: <https://claude.ai/artifact/GYB4vn83UCCw3eEMVXNZ7V>):
  - A ticked kit can't be archived: untick it first. No history carries over to a new kit; edit a kit rather than replace it.
  - Lost works as no warranty at once: kept (the Full → Break-in flip went with the next fix; W13 stays, as for an ended warranty).
  - While the break-in is waited for, the Full counts the kit's own interval in both modes.
  - Every miss is listed, in a red card on the vehicle page, sorted by the day it lost the warranty; the facts give the first one's day ("the day it actually happened; later misses can't lose it again").
  - Editing a ticked kit's numbers re-judges its history: documented, not prevented.
  - Log service offers Break-in until one is logged, to backfill it.
  - A Full standing in for the waited-for break-in is due by Break-in at too (the scan's slow rider lost the warranty without a warning). It stays a Full, not a break-in that restarts the clock: one rule fewer.

## Tests

- `internal/stock/kits_test.go`: `TestFullAndHalfService`, `TestBreakIn`, `TestBreakInWaitOwnInterval`, `TestBreakInStandInDue`, `TestFullTakesOver`.
- `internal/stock/warranty_test.go`: `TestWarrantyLost` (the grace edges), `TestWarrantyLostSchedulesInTurns`, `TestWarrantyLostNamesTheEarliestMiss`, `TestWarrantyMisses` (every miss, `LostOn`).
- `internal/store/archive_test.go`: `TestArchiveKitRequiredByWarranty`.
- `internal/stock/validate_test.go`: the kit form rows above.
- Over HTTP, `internal/web/warranty_test.go`: `TestBreakIn`, `TestBreakInLateRefused`, `TestBreakInWaitWithoutWarranty`, `TestBreakInBackfill`, `TestWarrantyLostAndBackfilled`, `TestWarrantyLostHistory`, `TestWarrantyLostCard`, `TestArchiveRequiredKit`, `TestWarrantyDecidesKitSchedule`.
- e2e, `e2e/tests/garage.spec.ts`: "a missed service the warranty asks for shows in a red card" (and the page-wide tests then measure the card).
- The full scan (the matrix's last section): every valid kit shape × 5 vehicles × 30 riders over four years, against twelve rules (R1–R12), with a throwaway harness run through `go run -overlay`.
