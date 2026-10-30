package stock

import (
	"fmt"
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

// Readiness checks every line of the kit against the current stock. Lines
// for archived items don't count: they are no longer kept, and the kit
// should be edited.
func (k Kit) Readiness() Readiness {
	var r Readiness
	for _, l := range k.Lines {
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

// Due computes when the kit's service is next due, given the vehicle's
// current odometer reading and today's date.
func (k Kit) Due(currentKm *int, today time.Time) Due {
	if (k.IntervalKm == nil && k.IntervalMonths == nil) || k.Archived() {
		return Due{State: DueNone}
	}
	if k.LastService == nil {
		return Due{State: DueNever}
	}
	var d Due
	soon, overdue, known := false, false, false
	lastKm := k.LastService.OdometerKm
	if lastKm == nil {
		lastKm = k.LastOdometerKm
	}
	if k.IntervalKm != nil && lastKm != nil {
		dueKm := *lastKm + *k.IntervalKm
		d.DueKm = &dueKm
		if currentKm != nil {
			left := dueKm - *currentKm
			d.KmLeft = &left
			known = true
			// 10% of the interval, at least 500 km, but never more than
			// half of it (or a short interval is "soon" right away).
			threshold := min(max(500, *k.IntervalKm/10), *k.IntervalKm/2)
			overdue = overdue || left < 0
			soon = soon || left <= threshold
		}
	}
	if k.IntervalMonths != nil {
		dueDate := addMonths(DateOnly(k.LastService.PerformedOn), *k.IntervalMonths)
		d.DueDate = &dueDate
		days := int(dueDate.Sub(DateOnly(today)).Hours() / 24)
		d.DaysLeft = &days
		known = true
		// DueSoonDays, but never more than half the interval, or a monthly
		// service would be "soon" right after it was done.
		intervalDays := int(dueDate.Sub(DateOnly(k.LastService.PerformedOn)).Hours() / 24)
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

// addMonths adds n months, clamping to the end of the month: 31 Jan + 1
// month is 28 (or 29) Feb, not 3 Mar as time.AddDate gives.
func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, t.Location())
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(d, last), 0, 0, 0, 0, t.Location())
}

// Summary describes the due date in words, e.g. "in 1,200 km or 45 days".
func (d Due) Summary() string {
	var parts []string
	if d.KmLeft != nil {
		switch {
		case *d.KmLeft < 0:
			parts = append(parts, FormatKm(-*d.KmLeft)+" over")
		case *d.KmLeft == 0:
			parts = append(parts, "due now")
		default:
			parts = append(parts, "in "+FormatKm(*d.KmLeft))
		}
	}
	if d.DaysLeft != nil {
		switch n := *d.DaysLeft; {
		case n < 0:
			parts = append(parts, plural(-n, "day")+" late")
		case n == 0:
			parts = append(parts, "today")
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
