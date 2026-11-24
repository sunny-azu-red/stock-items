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
// ones, or the full ones (a break-in's too).
func (k Kit) ActiveLines(currentOdo *int, today time.Time) []KitLine {
	if k.NextIsIntermediate(currentOdo, today) {
		return k.InterLines
	}
	return k.Lines
}

// ReadinessAt checks the lines of the kit's next due schedule against the
// current stock.
func (k Kit) ReadinessAt(currentOdo *int, today time.Time) Readiness {
	return ReadinessOf(k.ActiveLines(currentOdo, today))
}

// DueState is the reminder state of a kit with an interval.
type DueState string

const (
	DueNone DueState = "none" // no interval configured
	// DueUnknown: a distance with nothing to count it from (a service
	// logged without a reading, before readings were required, and none
	// earlier).
	DueUnknown DueState = "unknown"
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
	case DueUnknown:
		return 2
	case DueOK:
		return 3
	}
	return 4
}

// Label is a short badge text.
func (s DueState) Label() string {
	switch s {
	case DueOverdue:
		return "Overdue"
	case DueSoon:
		return "Due soon"
	case DueUnknown:
		return "Needs odometer"
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

// DueSoonDays is how many days ahead a date based service counts as "soon".
const DueSoonDays = 30

// LogWindowDays is how soon after a service it must be logged for the
// vehicle's odometer reading at the time to say where it was done.
const LogWindowDays = 7

// LoggedSoonAfter reports whether a service done on performedOn (a
// calendar day) was logged at loggedAt (an instant) within LogWindowDays
// of being done. The day it was logged is counted in loc, the app's zone,
// never the database's: just after local midnight is already the next day.
// A service dated after it was logged (tomorrow, at most) counts.
func LoggedSoonAfter(loggedAt, performedOn time.Time, loc *time.Location) bool {
	// Both are midnight UTC, so a DST change can't make a day 23 hours.
	days := DateOnly(loggedAt.In(loc)).Sub(DateOnly(performedOn)) / (24 * time.Hour)
	return days <= LogWindowDays
}

// dueSoonDistance returns the distance within which a distance interval
// counts as "due soon": 10% of the interval, at least 500 (in the distance
// unit), but never more than half of it (or a short interval is "soon" right
// away).
func dueSoonDistance(intervalDistance int) int {
	return min(max(500, intervalDistance/10), intervalDistance/2)
}

func dueFrom(ref *ServiceRef, fallbackOdo *int, baseDate *time.Time, baseOdo *int, intervalDistance, intervalMonths *int, currentOdo *int, today time.Time) Due {
	if intervalDistance == nil && intervalMonths == nil {
		return Due{State: DueNone}
	}
	var d Due
	soon, overdue, known := false, false, false
	var lastOdo *int
	var fromDate *time.Time
	if ref != nil {
		// A service logged without a reading counts from the vehicle's reading
		// at the time (odometer_at_log); without one, from the latest reading.
		lastOdo = cmp.Or(ref.Odometer, ref.OdometerAtLog, fallbackOdo)
		fromDate = &ref.PerformedOn
	} else {
		lastOdo = baseOdo
		fromDate = baseDate
	}
	if intervalDistance != nil && lastOdo != nil {
		dueOdo := *lastOdo + *intervalDistance
		d.DueOdometer = &dueOdo
		if currentOdo != nil {
			left := dueOdo - *currentOdo
			d.DistanceLeft = &left
			known = true
			threshold := dueSoonDistance(*intervalDistance)
			overdue = overdue || left < 0
			soon = soon || left <= threshold
		}
	}
	if intervalMonths != nil && fromDate != nil {
		dueDate := addMonths(DateOnly(*fromDate), *intervalMonths)
		d.DueDate = &dueDate
		days := int(dueDate.Sub(DateOnly(today)).Hours() / 24)
		d.DaysLeft = &days
		known = true
		// DueSoonDays, but never more than half the interval, or a monthly
		// service would be "soon" right after it was done.
		intervalDays := int(dueDate.Sub(DateOnly(*fromDate)).Hours() / 24)
		overdue = overdue || days < 0
		soon = soon || days <= min(DueSoonDays, intervalDays/2)
	}
	switch {
	case !known:
		d.State = DueUnknown
	case overdue:
		d.State = DueOverdue
	case soon:
		d.State = DueSoon
	default:
		d.State = DueOK
	}
	return d
}

// InterDue computes when the kit's next service in between is due: a step
// (InterStep, InterMonths) after the latest service of either kind, or
// after the purchase before the first.
func (k Kit) InterDue(currentOdo *int, today time.Time) Due {
	if !k.HasIntermediate() || k.Archived() {
		return Due{State: DueNone}
	}
	last, lastOdo := k.counted()
	return dueFrom(last, lastOdo, k.VehiclePurchasedOn, k.VehicleOdometerAtPurchase, k.InterStep(), k.InterMonths(), currentOdo, today)
}

// counted is the service the schedules count from (with the reading to fall
// back on when it has none): the latest one, unless that is the break-in,
// which no schedule counts from (the manual's intervals run from the
// purchase): then none, so they count from the purchase.
func (k Kit) counted() (*ServiceRef, *int) {
	if k.LastService != nil && k.LastService.Tier == TierBreakIn {
		return nil, nil
	}
	return k.LastService, k.LastOdometer
}

// FullDue computes when the kit's full service is next due. No schedule
// counts from a break-in (counted, LastFullService).
//   - While the vehicle is under warranty (UnderWarranty), it counts its
//     own interval from the last full service, or from the purchase before
//     the first: intermediate services never move it, as a warranty asks.
//   - Without a warranty and without intermediate services, from the last
//     service.
//   - Otherwise (no warranty, or it has ended) services take turns, each a
//     step (InterStep, InterMonths) after the one before: the full one
//     comes after the intermediate ones still to do, from the last service
//     of either kind.
func (k Kit) FullDue(currentOdo *int, today time.Time) Due {
	if (k.IntervalDistance == nil && k.IntervalMonths == nil) || k.Archived() {
		return Due{State: DueNone}
	}
	if k.UnderWarranty(currentOdo, today) {
		return dueFrom(k.LastFullService, k.LastFullOdometer, k.VehiclePurchasedOn, k.VehicleOdometerAtPurchase, k.IntervalDistance, k.IntervalMonths, currentOdo, today)
	}
	last, lastOdo := k.counted()
	if !k.HasIntermediate() {
		return dueFrom(last, lastOdo, k.VehiclePurchasedOn, k.VehicleOdometerAtPurchase, k.IntervalDistance, k.IntervalMonths, currentOdo, today)
	}
	steps := k.InBetween - min(k.InterSinceFull, k.InBetween) + 1
	var distance, months *int
	if step := k.InterStep(); step != nil {
		distance = ptrTo(steps * *step)
	}
	if m := k.InterMonths(); m != nil {
		months = ptrTo(steps * *m)
	}
	return dueFrom(last, lastOdo, k.VehiclePurchasedOn, k.VehicleOdometerAtPurchase, distance, months, currentOdo, today)
}

func ptrTo(n int) *int { return &n }

// NextIsBreakIn reports whether the kit's next service is a new vehicle's
// break-in: it has a reading for one (BreakInAt), nothing has been logged
// with it yet, the vehicle was bought below that reading and hasn't gone
// past its first full distance since (a kit added long after the break-in
// doesn't ask for one). The break-in takes the full parts (ActiveLines),
// and no schedule counts from it (counted, LastFullService).
func (k Kit) NextIsBreakIn() bool {
	if k.BreakInAt == nil || k.LastService != nil || k.Archived() {
		return false
	}
	bought := 0
	if k.VehicleOdometerAtPurchase != nil {
		bought = *k.VehicleOdometerAtPurchase
	}
	pastFirst := k.IntervalDistance != nil && k.VehicleOdometer != nil && *k.VehicleOdometer >= bought+*k.IntervalDistance
	return bought < *k.BreakInAt && !pastFirst
}

// BreakInDue computes when the break-in is due (NextIsBreakIn): at the
// reading BreakInAt, by distance only, counted from the purchase.
func (k Kit) BreakInDue(currentOdo *int, today time.Time) Due {
	if !k.NextIsBreakIn() {
		return Due{State: DueNone}
	}
	from := 0
	if k.VehicleOdometerAtPurchase != nil {
		from = *k.VehicleOdometerAtPurchase
	}
	return dueFrom(nil, nil, nil, &from, ptrTo(*k.BreakInAt-from), nil, currentOdo, today)
}

// UnderWarranty reports whether the kit's vehicle is under warranty
// (Warranty): its full service then keeps its own deadline (FullDue).
func (k Kit) UnderWarranty(currentOdo *int, today time.Time) bool {
	return k.Warranty(currentOdo, today).Runs()
}

// NextIsIntermediate reports whether a service in between is next.
//   - Once InBetween of them have been done since the last full service,
//     the full one is.
//   - Without a warranty running, they take turns.
//   - Under warranty the full service keeps its own deadline: it comes
//     first once it is due soon or overdue, or when the next service in
//     between wouldn't fall due before it (dueBefore).
func (k Kit) NextIsIntermediate(currentOdo *int, today time.Time) bool {
	if !k.HasIntermediate() || k.Archived() || k.InterSinceFull >= k.InBetween || k.NextIsBreakIn() {
		return false
	}
	if !k.UnderWarranty(currentOdo, today) {
		return true
	}
	full := k.FullDue(currentOdo, today)
	if full.State == DueSoon || full.State == DueOverdue {
		return false
	}
	return dueBefore(k.InterDue(currentOdo, today), full)
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
func (k Kit) Due(currentOdo *int, today time.Time) Due {
	if k.NextIsBreakIn() {
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

// DistanceUnit labels every distance the app shows: odometer readings and
// service intervals ("km" unless APP_DISTANCE says otherwise; main sets it
// once at startup). It is only a label: distances are stored and compared as
// typed.
var DistanceUnit = "km"

// FormatDistance renders a distance with thousands separators and the unit:
// "12 345 km".
func FormatDistance(n int) string { return FormatInt(n) + " " + DistanceUnit }

// thousandsSep is a narrow no-break space. Unlike a comma or point it reads
// the same with decimal points and decimal commas: "1 500" is never 1.5.
const thousandsSep = ' '

// FormatInt renders an integer with thousands separators.
func FormatInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(thousandsSep)
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

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
