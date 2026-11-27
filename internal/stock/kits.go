package stock

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Readiness summarises whether stock covers a kit.
type Readiness struct {
	Lines      int
	ShortLines int
}

// Ready reports whether every line is covered (and the kit is not empty).
func (r Readiness) Ready() bool { return r.Lines > 0 && r.ShortLines == 0 }

// ReadinessOf checks a slice of kit lines against the current stock. Lines
// for archived items don't count: they are no longer kept, and the kit
// should be edited.
func ReadinessOf(lines []KitLine) Readiness {
	var r Readiness
	for _, l := range lines {
		if l.Archived {
			continue
		}
		r.Lines++
		if l.Short() > 0 {
			r.ShortLines++
		}
	}
	return r
}

// Listed reports whether what the line is missing is on the shopping list:
// listed holds the amount each item's entry asks for (base units; an entry
// without an amount isn't in it). Lines that miss nothing, and archived
// ones, which nothing is added for, are never listed.
func (l KitLine) Listed(listed map[int64]float64) bool {
	short := l.Short()
	if short <= 0 || l.Archived {
		return false
	}
	q, ok := listed[l.ItemID]
	return ok && q >= short
}

// ToList counts the lines that miss something not yet on the shopping list
// (see Listed): what the kit's Add missing would add.
func ToList(lines []KitLine, listed map[int64]float64) int {
	n := 0
	for _, l := range lines {
		if l.Short() > 0 && !l.Archived && !l.Listed(listed) {
			n++
		}
	}
	return n
}

// ActiveLines returns the lines of the service due next: the intermediate
// one's, or the full ones (a break-in's too).
func (k Kit) ActiveLines(currentOdo int, today time.Time) []KitLine {
	if k.NextIsIntermediate(currentOdo, today) {
		return k.InterLines
	}
	return k.Lines
}

// ReadinessAt checks the lines of the kit's next due schedule against the
// current stock.
func (k Kit) ReadinessAt(currentOdo int, today time.Time) Readiness {
	return ReadinessOf(k.ActiveLines(currentOdo, today))
}

// DueState is the reminder state of a kit with an interval.
type DueState string

const (
	DueNone    DueState = "none" // no interval configured
	DueOK      DueState = "ok"
	DueSoon    DueState = "soon"
	DueOverdue DueState = "overdue"
)

// Rank orders states from most to least urgent.
func (s DueState) Rank() int {
	switch s {
	case DueOverdue:
		return 0
	case DueSoon:
		return 1
	case DueOK:
		return 2
	}
	return 3
}

// Label is a short badge text.
func (s DueState) Label() string {
	switch s {
	case DueOverdue:
		return "Overdue"
	case DueSoon:
		return "Due soon"
	case DueOK:
		return "On track"
	}
	return "No interval"
}

// Due is the computed reminder of a kit.
type Due struct {
	State        DueState
	DueDate      *time.Time
	DueOdometer  *int
	DistanceLeft *int
	DaysLeft     *int
}

// dueSoonDistance returns the distance within which a distance interval
// counts as "due soon": the settings' share of the interval
// (Settings.DueSoonPercent, 10%), at least their minimum (DueSoonMinDistance,
// 500 in the distance unit), but never more than half of it (or a short
// interval is "soon" right away).
func dueSoonDistance(intervalDistance int) int {
	set := Current()
	return min(max(set.DueSoonMinDistance, intervalDistance*set.DueSoonPercent/100), intervalDistance/2)
}

// Start is where a vehicle's schedules count from before its first
// service with a kit: the manual's milestones, from 0 and the day it was
// first registered (Vehicle.FirstRegisteredOn; the purchase day for a
// vehicle bought new, Vehicle.BoughtNew), as if the first owner had kept to
// them. Only the user's own
// services are logged, so the first one due is the next milestone after the
// purchase (Purchased, at the reading Odo): LittleJoy, bought at 2 148 km,
// owes its 15 000 km service at 15 000, not 17 148.
type Start struct {
	Registered time.Time
	Purchased  time.Time
	Odo        int
}

// base is an interval's last milestone at or before the purchase: the last
// multiple of distance from 0 at or below the purchase reading, and the last
// step of months from Registered not after the purchase day (each counted
// from Registered, so month ends don't drift). Without a distance, the
// purchase reading; without months, the purchase day.
func (s Start) base(distance, months *int) (time.Time, int) {
	date, odo := DateOnly(s.Purchased), s.Odo
	if distance != nil && *distance > 0 {
		odo = s.Odo / *distance * *distance
	}
	if months != nil && *months > 0 {
		date, _ = s.dates(*months)
	}
	return date, odo
}

// dates are the last step of months from Registered at or before the
// purchase day, and the next one: the first due after it. Both count from
// Registered (31 May + 12 months is 31 May, though + 6 is 30 Nov).
func (s Start) dates(months int) (last, next time.Time) {
	bought, reg := DateOnly(s.Purchased), DateOnly(s.Registered)
	if !reg.Before(bought) {
		return bought, addMonths(bought, months)
	}
	y1, m1, _ := reg.Date()
	y2, m2, _ := bought.Date()
	n := ((y2-y1)*12 + int(m2-m1)) / months
	for n > 0 && addMonths(reg, n*months).After(bought) {
		n--
	}
	for !addMonths(reg, (n+1)*months).After(bought) {
		n++
	}
	return addMonths(reg, n*months), addMonths(reg, (n+1)*months)
}

// startOf is the Start of a vehicle first registered on registered, bought
// on purchased at the reading odo. A vehicle bought new (registered nil,
// Vehicle.BoughtNew) was registered when bought, at 0.
func startOf(registered *time.Time, purchased time.Time, odo int) Start {
	s := Start{Registered: purchased, Purchased: purchased, Odo: odo}
	if registered != nil {
		s.Registered = *registered
	}
	return s
}

// start is the vehicle's Start.
func (v Vehicle) start() Start {
	return startOf(v.FirstRegisteredOn, v.PurchasedOn, v.OdometerAtPurchase)
}

// start is the kit's vehicle's Start.
func (k Kit) start() Start {
	return startOf(k.VehicleFirstRegisteredOn, k.VehiclePurchasedOn, k.VehicleOdometerAtPurchase)
}

// dueFrom works out when a service is due: intervalDistance and
// intervalMonths after ref, the service it counts from, or after the
// interval's last milestone before the purchase without one (Start.base).
func dueFrom(ref *ServiceRef, start Start, intervalDistance, intervalMonths *int, currentOdo int, today time.Time) Due {
	if intervalDistance == nil && intervalMonths == nil {
		return Due{State: DueNone}
	}
	fromDate, fromOdo := start.base(intervalDistance, intervalMonths)
	if ref != nil {
		fromDate, fromOdo = ref.PerformedOn, ref.Odometer
	}
	var d Due
	soon, overdue := false, false
	if intervalDistance != nil {
		dueOdo := fromOdo + *intervalDistance
		left := dueOdo - currentOdo
		d.DueOdometer, d.DistanceLeft = &dueOdo, &left
		overdue = overdue || left < 0
		soon = soon || left <= dueSoonDistance(*intervalDistance)
	}
	if intervalMonths != nil {
		dueDate := addMonths(DateOnly(fromDate), *intervalMonths)
		if ref == nil {
			_, dueDate = start.dates(*intervalMonths)
		}
		days := int(dueDate.Sub(DateOnly(today)).Hours() / 24)
		d.DueDate, d.DaysLeft = &dueDate, &days
		// Settings.DueSoonDays, but never more than half the interval, or a
		// monthly service would be "soon" right after it was done.
		intervalDays := int(dueDate.Sub(DateOnly(fromDate)).Hours() / 24)
		overdue = overdue || days < 0
		soon = soon || days <= min(Current().DueSoonDays, intervalDays/2)
	}
	switch {
	case overdue:
		d.State = DueOverdue
	case soon:
		d.State = DueSoon
	default:
		d.State = DueOK
	}
	return d
}

// InterDue computes when the kit's intermediate service is due: a step
// (InterStep and InterMonths: half the distance at the full time, or half
// the time on a kit with a time only) after the latest service of either
// kind, or after the step's last milestone before the purchase (Start)
// before the first.
func (k Kit) InterDue(currentOdo int, today time.Time) Due {
	if !k.Intermediate || k.Archived() {
		return Due{State: DueNone}
	}
	return dueFrom(k.counted(), k.start(), k.InterStep(), k.InterMonths(), currentOdo, today)
}

// counted is the service the schedules count from: the latest one, unless
// that is the break-in, which no schedule counts from (the manual's
// intervals run from new): then the milestones (Start), or, for a break-in
// that is also the first full service, its date (breakInFull).
func (k Kit) counted() *ServiceRef {
	if k.LastService != nil && k.LastService.Tier == TierBreakIn {
		return k.breakInFull()
	}
	return k.LastService
}

// lastFull is the full service the warranty's schedule counts from: the
// last one, or, before the first, a break-in that is also the first full
// service (breakInFull).
func (k Kit) lastFull() *ServiceRef {
	if k.LastFullService != nil {
		return k.LastFullService
	}
	return k.breakInFull()
}

// breakInFull is the kit's break-in when it is also the first full service
// (BreakInIsFull: a light rider's, done once the first full service's time
// was close): the schedules then count the time from it, and the distance
// from 0 still (Start: a vehicle with a break-in was bought below its first
// milestone), as a manual counts it (a break-in at 1 000 km never moves the
// first full service past 10 000). Nil otherwise.
func (k Kit) breakInFull() *ServiceRef {
	if k.BreakIn == nil || !BreakInIsFull(*k.BreakIn, k.start(), k.IntervalMonths) {
		return nil
	}
	_, odo := k.start().base(k.IntervalDistance, nil)
	return &ServiceRef{ID: k.BreakIn.ID, PerformedOn: k.BreakIn.PerformedOn, Odometer: odo, Tier: TierBreakIn}
}

// BreakInIsFull reports whether a break-in also counts as the kit's first
// full service: it was done in the last quarter of the first full service's
// time (FullTakesOver; about 3 months on a 12-month kit, counted from start),
// due soon, or past it. A light rider reaches Break-in at near the end of the year, so
// that break-in is the yearly service too; a normal rider's, in the first
// months, restarts nothing. By time only: a kit without months has none.
func BreakInIsFull(breakIn ServiceRef, start Start, months *int) bool {
	if months == nil {
		return false
	}
	full := dueFrom(nil, start, nil, months, 0, breakIn.PerformedOn)
	if full.State == DueSoon || full.State == DueOverdue {
		return true
	}
	from, _ := start.base(nil, months)
	days := int(full.DueDate.Sub(from).Hours() / 24)
	return *full.DaysLeft <= days/FullTakesOver
}

// FullDue computes when the kit's full service is next due. No schedule
// counts from a break-in, unless it is also the first full service (by its
// time only: counted, lastFull).
//   - A break-in missed (breakInMissed) is due as the full service, at
//     the break-in's reading (breakInDue).
//   - While the break-in is waited for (breakInPending), its own interval
//     from new (Start), with a warranty or without: the first service is
//     the break-in or the full one, so nothing takes turns yet.
//   - While the vehicle is under warranty (UnderWarranty), it counts its
//     own interval from the last full service, or from its last milestone
//     before the purchase (Start) before the first: intermediate services
//     never move it, as a warranty asks.
//
// Before the first service, every case counts from its own interval's last
// milestone before the purchase (Start.base).
//   - Without a warranty and without an intermediate service, from the
//     last service.
//   - Otherwise (no warranty, or it has ended) services take turns, each a
//     step (InterStep and InterMonths) after the one before: after an
//     intermediate service the full one is a step on, after anything else
//     two (the intermediate one comes first).
func (k Kit) FullDue(currentOdo int, today time.Time) Due {
	if (k.IntervalDistance == nil && k.IntervalMonths == nil) || k.Archived() {
		return Due{State: DueNone}
	}
	if k.breakInMissed(currentOdo) {
		// A break-in this overdue is a full service: due since the
		// break-in's reading, or the break-in's year.
		return k.breakInDue(currentOdo, today)
	}
	if k.breakInPending(currentOdo) {
		// Waiting for the break-in, the first service is the break-in or the
		// full one (never an intermediate), so nothing takes turns yet: the
		// kit's own interval from new, with a warranty or without.
		return dueFrom(nil, k.start(), k.IntervalDistance, k.IntervalMonths, currentOdo, today)
	}
	if k.UnderWarranty(currentOdo, today) {
		return dueFrom(k.lastFull(), k.start(), k.IntervalDistance, k.IntervalMonths, currentOdo, today)
	}
	last := k.counted()
	if !k.Intermediate {
		return dueFrom(last, k.start(), k.IntervalDistance, k.IntervalMonths, currentOdo, today)
	}
	steps := 2
	if k.InterSinceFull {
		steps = 1
	}
	var distance, months *int
	if step := k.InterStep(); step != nil {
		distance = ptrTo(steps * *step)
	}
	if m := k.InterMonths(); m != nil {
		months = ptrTo(steps * *m)
	}
	return dueFrom(last, k.start(), distance, months, currentOdo, today)
}

func ptrTo(n int) *int { return &n }

// NextIsBreakIn reports whether the kit's next service is a new vehicle's
// break-in: it has a reading for one (BreakInAt), nothing has been logged
// with it yet, the vehicle was bought below that reading and hasn't gone
// past its first full distance since (a kit added long after the break-in
// doesn't ask for one). The break-in takes the full parts (ActiveLines);
// its first service read by Break-in at + the grace (WarrantyGrace) must
// be it
// (store.checkKitTier), and no schedule counts from it unless it is also
// the first full service (breakInFull).
//
// Past Break-in at and its grace (WarrantyGrace) the break-in is missed
// (breakInMissed): it can no longer be logged at the reading, so the full
// service is next, due since the break-in's reading (FullDue).
func (k Kit) NextIsBreakIn(currentOdo int, today time.Time) bool {
	return k.breakInPending(currentOdo) && !k.breakInMissed(currentOdo)
}

// breakInPending reports whether the kit still waits for the vehicle's
// break-in: it has one, nothing has been logged with it, and the vehicle
// hasn't gone past its first full distance (from 0: a vehicle with a
// break-in was bought below its first milestone).
func (k Kit) breakInPending(currentOdo int) bool {
	if !k.HasBreakIn() || k.LastService != nil || k.Archived() {
		return false
	}
	_, from := k.start().base(k.IntervalDistance, nil)
	return k.IntervalDistance == nil || currentOdo < from+*k.IntervalDistance
}

// breakInMissed reports whether the break-in is still waited for but the
// vehicle is past Break-in at and its grace: then the full service is
// next, due at the break-in's reading or the kit's months (FullDue).
func (k Kit) breakInMissed(currentOdo int) bool {
	return k.breakInPending(currentOdo) && currentOdo > *k.BreakInAt+WarrantyGrace(*k.BreakInAt)
}

// HasBreakIn reports whether the kit has a break-in on its vehicle: it says
// at what reading (BreakInAt), and the vehicle was bought below it (new).
// Only then can a service with it be one (store.checkKitTier).
func (k Kit) HasBreakIn() bool {
	return k.BreakInAt != nil && k.VehicleOdometerAtPurchase < *k.BreakInAt
}

// BreakInDue computes when the break-in is due (NextIsBreakIn): at the
// reading BreakInAt, or the kit's months from new (Start), whichever comes
// first. A light rider reaches the year before Break-in at: then the
// break-in is the yearly service too (BreakInIsFull), and its date must
// not pass unseen.
func (k Kit) BreakInDue(currentOdo int, today time.Time) Due {
	if !k.NextIsBreakIn(currentOdo, today) {
		return Due{State: DueNone}
	}
	return k.breakInDue(currentOdo, today)
}

// breakInDue is when the break-in's reading or year comes: BreakInAt, or the
// kit's months from its last milestone before the purchase (Start.base).
// The reading counts from the purchase reading, so it is "due soon" within
// half of what is left to ride from there (as on a vehicle bought at 0),
// not of the whole Break-in at.
func (k Kit) breakInDue(currentOdo int, today time.Time) Due {
	from := &ServiceRef{Odometer: k.VehicleOdometerAtPurchase}
	byReading := dueFrom(from, k.start(), ptrTo(*k.BreakInAt-from.Odometer), nil, currentOdo, today)
	if k.IntervalMonths == nil {
		return byReading
	}
	return byReading.or(dueFrom(nil, k.start(), nil, k.IntervalMonths, currentOdo, today))
}

// or puts together a Due by a reading only (d) and one by a date only (o),
// whichever comes first, as dueFrom does for an interval with both.
func (d Due) or(o Due) Due {
	d.DueDate, d.DaysLeft = o.DueDate, o.DaysLeft
	if o.State.Rank() < d.State.Rank() {
		d.State = o.State
	}
	return d
}

// UnderWarranty reports whether the kit's vehicle is under warranty
// (Warranty): its full service then keeps its own deadline (FullDue).
func (k Kit) UnderWarranty(currentOdo int, today time.Time) bool {
	return k.Warranty(currentOdo, today).Runs()
}

// NextIsIntermediate reports whether the intermediate service is next.
//   - Once it has been done since the last full service, the full one is.
//   - The full one takes over once it is close (fullClose), with a warranty
//     or without: an intermediate service skipped, or never reached by a
//     light rider, doesn't stay next until the full one is overdue.
//   - Without a warranty running, they take turns.
//   - Under warranty the full service keeps its own deadline: it also comes
//     first when the intermediate one wouldn't fall due before it
//     (dueBefore).
func (k Kit) NextIsIntermediate(currentOdo int, today time.Time) bool {
	if !k.Intermediate || k.Archived() || k.InterSinceFull || k.breakInPending(currentOdo) {
		return false
	}
	full := k.FullDue(currentOdo, today)
	if k.fullClose(full) {
		return false
	}
	if !k.UnderWarranty(currentOdo, today) {
		return true
	}
	return dueBefore(k.InterDue(currentOdo, today), full)
}

// FullTakesOver is the share of the kit's interval (its distance, and its
// months in days) left before the full service, within which the full
// service is next rather than the intermediate one: the last quarter.
const FullTakesOver = 4

// fullClose reports whether the full service (full, FullDue) is close
// enough to come before an intermediate one: overdue, due soon, or within
// the last quarter of the kit's interval (FullTakesOver: 2 500 km or about 3
// months on a 10 000 km / 12 month kit), by distance or by date.
func (k Kit) fullClose(full Due) bool {
	if full.State == DueSoon || full.State == DueOverdue {
		return true
	}
	if full.DistanceLeft != nil && k.IntervalDistance != nil && *full.DistanceLeft <= *k.IntervalDistance/FullTakesOver {
		return true
	}
	if full.DaysLeft != nil && k.IntervalMonths != nil {
		days := int(full.DueDate.Sub(addMonths(*full.DueDate, -*k.IntervalMonths)).Hours() / 24)
		if *full.DaysLeft <= days/FullTakesOver {
			return true
		}
	}
	return false
}

// dueBefore reports whether a may fall due strictly before b. Each falls
// due at whichever of its odometer target and date comes first, and a
// distance doesn't turn into days, so a comes first when it is earlier in
// either: in an odometer target or date both have, or in one b has none of
// (a full service with no day to count its months from must not hide an
// intermediate one's date). A tie in one goes by the other; a tie in all
// goes to b.
func dueBefore(a, b Due) bool {
	if a.DueOdometer != nil && (b.DueOdometer == nil || *a.DueOdometer < *b.DueOdometer) {
		return true
	}
	return a.DueDate != nil && (b.DueDate == nil || a.DueDate.Before(*b.DueDate))
}

// Due computes when the kit's next service (break-in, intermediate or full)
// is due, given the vehicle's current odometer reading and today's date.
func (k Kit) Due(currentOdo int, today time.Time) Due {
	if k.NextIsBreakIn(currentOdo, today) {
		return k.BreakInDue(currentOdo, today)
	}
	if k.NextIsIntermediate(currentOdo, today) {
		return k.InterDue(currentOdo, today)
	}
	return k.FullDue(currentOdo, today)
}

// addMonths adds n months, clamping to the end of the month: 31 Jan + 1
// month is 28 (or 29) Feb, not 3 Mar as time.AddDate gives.
func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, t.Location())
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(d, last), 0, 0, 0, 0, t.Location())
}

// Summary describes the due date in words, e.g. "in 1 200 km · in 45 days".
func (d Due) Summary() string {
	var parts []string
	if d.DistanceLeft != nil {
		switch {
		case *d.DistanceLeft < 0:
			parts = append(parts, FormatDistance(-*d.DistanceLeft)+" overdue")
		case *d.DistanceLeft == 0:
			parts = append(parts, "due now")
		default:
			parts = append(parts, "in "+FormatDistance(*d.DistanceLeft))
		}
	}
	if d.DaysLeft != nil {
		switch n := *d.DaysLeft; {
		case n < 0:
			parts = append(parts, plural(-n, "day")+" overdue")
		case n == 0:
			parts = append(parts, "due today")
		default:
			parts = append(parts, "in "+plural(n, "day"))
		}
	}
	return strings.Join(parts, " · ")
}

// FormatDistance renders a distance with thousands separators and the
// distance unit in use (Settings.DistanceUnit): "12 345 km".
func FormatDistance(n int) string { return FormatInt(n) + " " + Current().DistanceUnit }

// FormatInt renders an integer with the thousands separator in use
// (Settings.NumberFormat).
func FormatInt(n int) string { return grouped(strconv.Itoa(n), Current().NumberFormat) }

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// Sort keys of a kit's parts table.
const (
	KitSortItem    = "item"
	KitSortNeed    = "need"
	KitSortHave    = "have"
	KitSortMissing = "missing"
)

// SortKitLines returns a kit's lines sorted by key (one of the KitSort*
// keys), descending if desc; an unknown key keeps the kit's own order.
// Amounts of different units don't compare, so the amount columns group by
// unit first, as the stock page does. By missing, the lines that are short
// come before the covered ones when descending (after them when
// ascending); an archived item's line counts as covered, as the page shows
// it, and the covered lines go by name. Ties go by name, then item id, so
// the order is stable.
func SortKitLines(lines []KitLine, key string, desc bool) []KitLine {
	missing := func(l KitLine) float64 {
		if l.Archived {
			return 0
		}
		return l.Short()
	}
	var amount func(KitLine) float64
	switch key {
	case KitSortItem:
	case KitSortNeed:
		amount = func(l KitLine) float64 { return l.Need }
	case KitSortHave:
		amount = func(l KitLine) float64 { return l.Have }
	case KitSortMissing:
		amount = missing
	default:
		return lines
	}
	flip := func(c int) int {
		if desc {
			return -c
		}
		return c
	}
	byName := func(a, b KitLine) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.ItemName), strings.ToLower(b.ItemName)), cmp.Compare(a.ItemID, b.ItemID))
	}
	out := slices.Clone(lines)
	slices.SortStableFunc(out, func(a, b KitLine) int {
		if amount == nil {
			return flip(byName(a, b))
		}
		if key == KitSortMissing {
			sa, sb := missing(a) > 0, missing(b) > 0
			if sa != sb {
				return flip(cmp.Compare(btoi(sa), btoi(sb)))
			}
			if !sa {
				return byName(a, b) // covered: nothing to compare but the name
			}
		}
		return cmp.Or(cmp.Compare(a.Unit, b.Unit), flip(cmp.Compare(amount(a), amount(b))), byName(a, b))
	})
	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
