package stock

import (
	"slices"
	"time"
)

// WarrantyState says where a vehicle's warranty stands.
type WarrantyState string

const (
	WarrantyNone   WarrantyState = "none"   // no end date or reading entered
	WarrantyActive WarrantyState = "active" // runs, and not near its end
	WarrantySoon   WarrantyState = "soon"   // runs, but ends soon
	WarrantyEnded  WarrantyState = "ended"
	// WarrantyLost: it would still run, but a service it asks for was done
	// overdue or not at all (Warranty.Misses).
	WarrantyLost WarrantyState = "lost"
)

// Warranty is the computed state of a vehicle's warranty.
type Warranty struct {
	State WarrantyState
	// EndsOn and EndsAt are what was entered: the last day it covers, and
	// the odometer reading it ends past.
	EndsOn *time.Time
	EndsAt *int
	// Misses are the services it asked for that were done overdue or not at
	// all (WarrantyLost), sorted by the day each lost it; none otherwise.
	Misses []WarrantyMiss
}

// WarrantyKit is a kit the warranty asks for on time (Kit.WarrantyRequired),
// as the warranty checks it: its schedule, and its break-in and full
// services on the vehicle, oldest first (intermediate services never count).
type WarrantyKit struct {
	KitID            int64
	Name             string
	IntervalDistance *int
	IntervalMonths   *int
	// Intermediate is Kit.Intermediate: with a break-in on the vehicle, it
	// says the kit has more than one kind of service (WarrantyMiss.Kinds).
	Intermediate bool
	BreakInAt    *int
	Services     []ServiceRef
	// Readings are the vehicle's services with any kit or none, oldest
	// first: the readings the app knows a day for, which date a reading
	// passed with nothing logged (WarrantyMiss.lostOn).
	Readings []ServiceRef
}

// WarrantyMiss is a service the warranty asked for that was done past its
// limit and the grace (WarrantyGrace, Settings.GraceDays), or is past them
// and not done. The grace is applied, never shown: a dealer may not allow
// it, so the page names the limit itself (DueOdometer, DueDate), the one to
// aim at, and what the grace decides is unexported, out of the templates'
// reach.
type WarrantyMiss struct {
	KitID int64
	Kit   string
	Tier  KitTier // TierBreakIn or TierFull
	// Kinds says the kit has more than one kind of service on the vehicle
	// (halfway, or a break-in): the page then names the kind missed (Oil
	// Service · full service); a kit with one kind is named alone (Spark
	// Plugs).
	Kinds bool
	// DueOdometer and DueDate are the limits it missed, without the grace:
	// a full service due by 10 000 km or 1 Jan 2027 and missed by its
	// reading has no DueDate, so the page's "Was due by" never names a
	// limit still ahead.
	DueOdometer *int
	DueDate     *time.Time
	// Done is the service done overdue; nil when it hasn't been done.
	Done *ServiceRef
	// latestOdometer and latestDate are the limit with the grace: the last
	// reading and day it could be done.
	latestOdometer *int
	latestDate     *time.Time
	// lostOn is the day the warranty was lost by this miss, as far as the
	// app can tell, which orders Warranty.Misses. The day after latestDate
	// when the date alone was missed. Otherwise a reading was missed, on a
	// day no reading was kept, so it was lost by then: the day of the first
	// service logged past latestOdometer (with any kit or none:
	// WarrantyKit.Readings), or the day after latestDate if the date was
	// missed too and that came first. With no service logged past the
	// reading, today (sorted last).
	lostOn time.Time
}

// WarrantyGrace is how far past its reading a service the warranty asks
// for may still be done: Settings.GracePercent of its distance (10% by
// default, as dealers allow: 100 km on a break-in at 1 000, 1 500 on a
// 15 000 interval). How many days past its date is Settings.GraceDays
// (30, about a month, whatever the interval).
func WarrantyGrace(distance int) int { return distance * Current().GracePercent / 100 }

// WarrantyOf works out a warranty that ends on endsOn (the last day it
// covers) or past the reading endsAt, whichever comes first, given the
// vehicle's odometer and today (a calendar day in the app zone).
func WarrantyOf(endsOn *time.Time, endsAt *int, odo int, today time.Time) Warranty {
	w := Warranty{State: WarrantyNone, EndsOn: endsOn, EndsAt: endsAt}
	if endsOn == nil && endsAt == nil {
		return w
	}
	ended, soon := false, false
	if endsOn != nil {
		// Both are midnight UTC days, so a DST change can't make one 23 hours.
		days := int(DateOnly(*endsOn).Sub(DateOnly(today)).Hours() / 24)
		ended = ended || days < 0
		soon = soon || days <= Current().WarrantySoonDays
	}
	if endsAt != nil {
		left := *endsAt - odo
		ended = ended || left < 0
		soon = soon || left <= Current().WarrantySoonDistance
	}
	switch {
	case ended:
		w.State = WarrantyEnded
	case soon:
		w.State = WarrantySoon
	default:
		w.State = WarrantyActive
	}
	return w
}

// warrantyWithKits works out a warranty as WarrantyOf does, and, while it
// runs, whether the kits it asks for (kits) missed a service: then it is
// lost, and Misses lists them all, the earliest by lostOn first (kits in
// their order on a tie). Every limit that passed fell while it ran (it runs
// from the purchase to its end, and the first limit is the first milestone
// after the purchase, Start), so once it has ended it shows as ended, lost
// or not.
func warrantyWithKits(endsOn *time.Time, endsAt *int, start Start, kits []WarrantyKit, odo int, today time.Time) Warranty {
	w := WarrantyOf(endsOn, endsAt, odo, today)
	if !w.Runs() {
		return w
	}
	for _, k := range kits {
		w.Misses = append(w.Misses, k.misses(start, odo, today)...)
	}
	if len(w.Misses) > 0 {
		w.State = WarrantyLost
		slices.SortStableFunc(w.Misses, func(a, b WarrantyMiss) int { return a.lostOn.Compare(b.lostOn) })
	}
	return w
}

// misses returns the services of the kit the warranty asked for that were
// done past their limit and the grace, or are past them now (the odometer
// odo, the day today) and not done, oldest first.
//   - The break-in, on a vehicle bought below the kit's Break-in at: by its
//     reading only, done as the kit's first break-in or full service (one
//     logged as Full on time counts; an intermediate one doesn't).
//   - Each full service: its distance and months after the last full one,
//     overdue or not, or, before the first, after the interval's last
//     milestone before the purchase (Start.base: the first owner's part is
//     taken as kept). No schedule counts from the break-in, unless it is
//     also the first full service (BreakInIsFull): then its time counts,
//     the distance still from 0 (Kit.breakInFull).
func (k WarrantyKit) misses(start Start, odo int, today time.Time) []WarrantyMiss {
	var out []WarrantyMiss
	kinds := k.Intermediate || (k.BreakInAt != nil && *k.BreakInAt > start.Odo)
	var full []ServiceRef
	for _, s := range k.Services {
		if s.Tier == TierFull || s.Tier == TierBreakIn {
			full = append(full, s)
		}
	}
	// passed dates a reading passed: the day of the first service logged
	// past it, if one was by the day on (an overdue service is one itself);
	// else on.
	passed := func(limit int, on time.Time) time.Time {
		for _, r := range k.Readings {
			if r.Odometer > limit && !DateOnly(r.PerformedOn).After(DateOnly(on)) {
				return DateOnly(r.PerformedOn)
			}
		}
		return DateOnly(on)
	}
	if k.BreakInAt != nil && *k.BreakInAt > start.Odo {
		limit := *k.BreakInAt + WarrantyGrace(*k.BreakInAt)
		miss := WarrantyMiss{KitID: k.KitID, Kit: k.Name, Tier: TierBreakIn, Kinds: kinds, DueOdometer: ptrTo(*k.BreakInAt), latestOdometer: &limit}
		switch {
		case len(full) > 0 && full[0].Odometer > limit:
			done := full[0]
			miss.Done = &done
			miss.lostOn = passed(limit, done.PerformedOn)
			out = append(out, miss)
		case len(full) == 0 && odo > limit:
			miss.lostOn = passed(limit, today)
			out = append(out, miss)
		}
	}
	if k.IntervalDistance == nil && k.IntervalMonths == nil {
		return out
	}
	fromDate, fromOdo := start.base(k.IntervalDistance, k.IntervalMonths)
	// counted says a full service has been counted: the first limit is the
	// first milestone after the purchase (Start.dates), the next ones count
	// from the full service before them.
	counted := false
	// overdue checks a full service done (or not, by now) at the reading atOdo
	// on the day on, counted from the last full one.
	overdue := func(atOdo int, on time.Time) *WarrantyMiss {
		m := &WarrantyMiss{KitID: k.KitID, Kit: k.Name, Tier: TierFull, Kinds: kinds}
		byOdo, byDate := false, false
		if k.IntervalDistance != nil {
			due, latest := fromOdo+*k.IntervalDistance, fromOdo+*k.IntervalDistance+WarrantyGrace(*k.IntervalDistance)
			m.DueOdometer, m.latestOdometer = &due, &latest
			byOdo = atOdo > latest
		}
		if k.IntervalMonths != nil {
			due := addMonths(DateOnly(fromDate), *k.IntervalMonths)
			if !counted {
				_, due = start.dates(*k.IntervalMonths)
			}
			latest := due.AddDate(0, 0, Current().GraceDays)
			m.DueDate, m.latestDate = &due, &latest
			byDate = DateOnly(on).After(latest)
		}
		switch {
		case byOdo:
			// By a reading: the first service logged past it, or the day
			// done; the date's day after if that came first.
			m.lostOn = passed(*m.latestOdometer, on)
			if byDate {
				if lost := m.latestDate.AddDate(0, 0, 1); lost.Before(m.lostOn) {
					m.lostOn = lost
				}
			}
		case byDate:
			m.lostOn = m.latestDate.AddDate(0, 0, 1)
		default:
			return nil
		}
		// Only the limits it missed: the other one may still be ahead.
		if !byOdo {
			m.DueOdometer = nil
		}
		if !byDate {
			m.DueDate = nil
		}
		return m
	}
	for _, s := range full {
		isFull := s.Tier == TierFull
		// A break-in that is also the first full service (a light rider's,
		// done once that service's time was close) counts as it, by its
		// time: the distance still counts from 0 (Start).
		if !isFull && !BreakInIsFull(s, start, k.IntervalMonths) {
			continue
		}
		if m := overdue(s.Odometer, s.PerformedOn); m != nil {
			done := s
			m.Done = &done
			out = append(out, *m)
		}
		fromDate, counted = s.PerformedOn, true
		if isFull {
			fromOdo = s.Odometer
		}
	}
	if m := overdue(odo, today); m != nil {
		out = append(out, *m)
	}
	return out
}

// Runs reports whether the warranty is in force (ending soon or not).
func (w Warranty) Runs() bool { return w.State == WarrantyActive || w.State == WarrantySoon }

// Label is the warranty's badge text, "" without one.
func (s WarrantyState) Label() string {
	switch s {
	case WarrantyActive:
		return "Under warranty"
	case WarrantySoon:
		return "Warranty ends soon"
	case WarrantyEnded:
		return "Warranty ended"
	case WarrantyLost:
		return "Warranty lost"
	}
	return ""
}

// Warranty works out the vehicle's warranty on the day today, lost if one
// of its WarrantyKits missed a service.
func (v Vehicle) Warranty(today time.Time) Warranty {
	return warrantyWithKits(v.WarrantyEndsOn, v.WarrantyEndsAt, v.start(), v.WarrantyKits, v.Odometer, today)
}

// Warranty works out the warranty of the kit's vehicle, from its odometer
// reading currentOdo, on the day today.
func (k Kit) Warranty(currentOdo int, today time.Time) Warranty {
	return warrantyWithKits(k.VehicleWarrantyEndsOn, k.VehicleWarrantyEndsAt, k.start(), k.VehicleWarrantyKits, currentOdo, today)
}
