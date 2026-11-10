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

// ActiveLines returns the lines for whichever schedule (Intermediate or
// Full) is due next.
func (k Kit) ActiveLines(currentKm *int, today time.Time) []KitLine {
	if k.NextIsIntermediate(currentKm, today) {
		return k.InterLines
	}
	return k.Lines
}

// ReadinessAt checks the lines of the kit's next due schedule against the
// current stock.
func (k Kit) ReadinessAt(currentKm *int, today time.Time) Readiness {
	return ReadinessOf(k.ActiveLines(currentKm, today))
}

// DueState is the reminder state of a kit with an interval.
type DueState string

const (
	DueNone    DueState = "none"    // no interval configured
	DueNever   DueState = "never"   // interval set but never performed
	DueUnknown DueState = "unknown" // not enough data (e.g. no odometer)
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
	case DueNever:
		return 2
	case DueUnknown:
		return 3
	case DueOK:
		return 4
	}
	return 5
}

// Label is a short badge text.
func (s DueState) Label() string {
	switch s {
	case DueOverdue:
		return "Overdue"
	case DueSoon:
		return "Due soon"
	case DueNever:
		return "Not done yet"
	case DueUnknown:
		return "Needs odometer"
	case DueOK:
		return "On track"
	}
	return "No interval"
}

// Due is the computed reminder of a kit.
type Due struct {
	State    DueState
	DueDate  *time.Time
	DueKm    *int
	KmLeft   *int
	DaysLeft *int
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

// dueSoonKm returns the distance threshold within which a km interval counts
// as "due soon": 10% of the interval, at least 500 km, but never more than
// half of it (or a short interval is "soon" right away).
func dueSoonKm(intervalKm int) int {
	return min(max(500, intervalKm/10), intervalKm/2)
}

func dueFrom(ref *ServiceRef, fallbackOdo *int, baseDate *time.Time, baseKm *int, intervalKm, intervalMonths *int, currentKm *int, today time.Time) Due {
	if intervalKm == nil && intervalMonths == nil {
		return Due{State: DueNone}
	}
	if ref == nil && baseDate == nil && baseKm == nil {
		return Due{State: DueNever}
	}
	var d Due
	soon, overdue, known := false, false, false
	var lastKm *int
	var fromDate *time.Time
	if ref != nil {
		// A service logged without a reading counts from the vehicle's reading
		// at the time; one logged before that was kept, from the latest reading.
		lastKm = cmp.Or(ref.OdometerKm, ref.OdometerAtLog, fallbackOdo)
		fromDate = &ref.PerformedOn
	} else {
		lastKm = baseKm
		fromDate = baseDate
	}
	if intervalKm != nil && lastKm != nil {
		dueKm := *lastKm + *intervalKm
		d.DueKm = &dueKm
		if currentKm != nil {
			left := dueKm - *currentKm
			d.KmLeft = &left
			known = true
			threshold := dueSoonKm(*intervalKm)
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
		if ref == nil && d.DueKm == nil && d.DueDate == nil {
			d.State = DueNever
		} else {
			d.State = DueUnknown
		}
	case overdue:
		d.State = DueOverdue
	case soon:
		d.State = DueSoon
	case intervalMonths != nil && fromDate == nil:
		// The km are fine, but there are months to count and no day to
		// count them from: only a purchase odometer, or (under a warranty
		// schedule) only intermediate services. The deadline is unknown,
		// so never "On track": a warranty's yearly service must not be
		// missed (MonthsUncounted).
		d.State = DueNever
	default:
		d.State = DueOK
	}
	return d
}

// MonthsUncounted reports whether the kit's full schedule repeats by months
// but has no day to count them from: no full service logged (nor, without
// an intermediate schedule, any service) and no purchase date on the
// vehicle. Its month deadline, often a warranty's, is then unknown: the
// kit says "Not done yet", and the pages ask for the purchase date.
func (k Kit) MonthsUncounted(currentKm *int, today time.Time) bool {
	if k.IntervalMonths == nil || k.Archived() {
		return false
	}
	return k.FullDue(currentKm, today).DueDate == nil
}

// InterDue computes when the kit's intermediate schedule is next due,
// counting from the latest service of any tier (full or intermediate),
// or from the vehicle's purchase baseline when no service has been logged yet.
func (k Kit) InterDue(currentKm *int, today time.Time) Due {
	if !k.HasIntermediate() || k.Archived() {
		return Due{State: DueNone}
	}
	return dueFrom(k.LastService, k.LastOdometerKm, k.VehiclePurchasedOn, k.VehiclePurchasedKm, k.InterIntervalKm, k.InterIntervalMonths, currentKm, today)
}

// FullDue computes when the kit's main (full) schedule is next due.
// When the last service was intermediate:
//   - If InterIntervalMonths is set (alternating reset mode), the full service
//     is due one intermediate interval (InterIntervalKm, InterIntervalMonths)
//     after the intermediate one.
//   - If InterIntervalMonths is nil (strict warranty mode), the full schedule
//     counts strictly from LastFullService (or the vehicle's purchase baseline)
//     so intermediate changes never push back the full service's deadline.
func (k Kit) FullDue(currentKm *int, today time.Time) Due {
	if (k.IntervalKm == nil && k.IntervalMonths == nil) || k.Archived() {
		return Due{State: DueNone}
	}
	if !k.HasIntermediate() {
		return dueFrom(k.LastService, k.LastOdometerKm, k.VehiclePurchasedOn, k.VehiclePurchasedKm, k.IntervalKm, k.IntervalMonths, currentKm, today)
	}
	lastFull, lastFullOdo := k.LastFullService, k.LastFullOdometerKm
	if lastFull == nil && k.LastService != nil && !k.LastService.Intermediate {
		lastFull, lastFullOdo = k.LastService, k.LastOdometerKm
	}
	lastWasInter := k.LastService != nil && k.LastService.Intermediate
	if lastWasInter {
		if k.InterIntervalMonths != nil {
			// Services alternate: the full one comes one intermediate step
			// after the intermediate, in km and in months alike.
			if k.InterIntervalKm == nil && k.IntervalKm != nil {
				// No intermediate step in km: the km still count from the
				// last full service, so the intermediate can't push them back.
				byDate := dueFrom(k.LastService, k.LastOdometerKm, k.VehiclePurchasedOn, k.VehiclePurchasedKm, nil, k.InterIntervalMonths, currentKm, today)
				byKm := dueFrom(lastFull, lastFullOdo, k.VehiclePurchasedOn, k.VehiclePurchasedKm, k.IntervalKm, nil, currentKm, today)
				return mergeDue(byKm, byDate)
			}
			return dueFrom(k.LastService, k.LastOdometerKm, k.VehiclePurchasedOn, k.VehiclePurchasedKm, cmp.Or(k.InterIntervalKm, k.IntervalKm), k.InterIntervalMonths, currentKm, today)
		}
		if lastFull == nil && k.VehiclePurchasedKm == nil {
			// Nothing to count the full km from but the intermediate
			// service: one intermediate step after its reading. Without an
			// intermediate step in km, the km can't be counted.
			var fromKm *int
			if k.InterIntervalKm != nil {
				fromKm = cmp.Or(k.LastService.OdometerKm, k.LastService.OdometerAtLog, k.LastOdometerKm)
			}
			if fromKm == nil && k.IntervalMonths == nil {
				return Due{State: DueUnknown}
			}
			return dueFrom(nil, nil, k.VehiclePurchasedOn, fromKm, cmp.Or(k.InterIntervalKm, k.IntervalKm), k.IntervalMonths, currentKm, today)
		}
	}
	return dueFrom(lastFull, lastFullOdo, k.VehiclePurchasedOn, k.VehiclePurchasedKm, k.IntervalKm, k.IntervalMonths, currentKm, today)
}

// mergeDue joins a reminder counted in km with one counted in months: the
// km of one, the date of the other, and the more urgent state (one that
// is known beats one that isn't, as dueFrom does for its two halves).
func mergeDue(byKm, byDate Due) Due {
	d := Due{DueKm: byKm.DueKm, KmLeft: byKm.KmLeft, DueDate: byDate.DueDate, DaysLeft: byDate.DaysLeft}
	switch {
	case byKm.State == DueOverdue || byDate.State == DueOverdue:
		d.State = DueOverdue
	case byKm.State == DueSoon || byDate.State == DueSoon:
		d.State = DueSoon
	case byKm.State == DueOK || byDate.State == DueOK:
		d.State = DueOK
	case byKm.State.Rank() <= byDate.State.Rank():
		d.State = byKm.State
	default:
		d.State = byDate.State
	}
	return d
}

// NextIsIntermediate reports whether the intermediate schedule is the next
// service due for the kit.
func (k Kit) NextIsIntermediate(currentKm *int, today time.Time) bool {
	if !k.HasIntermediate() || k.Archived() {
		return false
	}
	if k.LastService != nil && k.LastService.Intermediate {
		// Alternating: a full service follows an intermediate one.
		if k.InterIntervalMonths != nil {
			return false
		}
		// Warranty mode: the full service keeps its own deadline, and
		// another intermediate service comes first while it falls due
		// strictly before the full one (full every 15 000 km, intermediate
		// every 5 000: the one at 10 000). A tie, or a full service due
		// soon or overdue, goes to the full one.
		interDue, fullDue := k.InterDue(currentKm, today), k.FullDue(currentKm, today)
		if fullDue.State == DueSoon || fullDue.State == DueOverdue || interDue.State == DueNone || interDue.State == DueNever {
			return false
		}
		return dueBefore(interDue, fullDue)
	}
	interDue := k.InterDue(currentKm, today)
	if interDue.State == DueNone || interDue.State == DueNever {
		return false
	}
	fullDue := k.FullDue(currentKm, today)
	if k.InterIntervalMonths == nil && (fullDue.State == DueSoon || fullDue.State == DueOverdue) {
		return false
	}
	// A full schedule that only lacks a day to count its months from
	// (MonthsUncounted) isn't more urgent for that: the parts offered
	// follow the km as before.
	fullRank := fullDue.State.Rank()
	if fullDue.State == DueNever {
		fullRank = DueOK.Rank()
	}
	if fullRank < interDue.State.Rank() {
		return false
	}
	if (fullDue.State == DueSoon || fullDue.State == DueOverdue) && fullDue.KmLeft != nil && k.IntervalKm != nil && *fullDue.KmLeft <= dueSoonKm(*k.IntervalKm) {
		return false
	}
	return true
}

// dueBefore reports whether a falls due strictly before b: by km when both
// have a km target, else by date when both have one.
func dueBefore(a, b Due) bool {
	if a.DueKm != nil && b.DueKm != nil {
		return *a.DueKm < *b.DueKm
	}
	if a.DueDate != nil && b.DueDate != nil {
		return a.DueDate.Before(*b.DueDate)
	}
	return false
}

// Due computes when the kit's next active service (Intermediate or Full) is
// due, given the vehicle's current odometer reading and today's date.
func (k Kit) Due(currentKm *int, today time.Time) Due {
	if k.NextIsIntermediate(currentKm, today) {
		return k.InterDue(currentKm, today)
	}
	return k.FullDue(currentKm, today)
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
	if d.KmLeft != nil {
		switch {
		case *d.KmLeft < 0:
			parts = append(parts, FormatKm(-*d.KmLeft)+" overdue")
		case *d.KmLeft == 0:
			parts = append(parts, "due now")
		default:
			parts = append(parts, "in "+FormatKm(*d.KmLeft))
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

// FormatKm renders a distance with thousands separators: "12 345 km".
func FormatKm(km int) string { return FormatInt(km) + " km" }

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
