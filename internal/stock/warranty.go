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
	// late or not at all (Warranty.Misses).
	WarrantyLost WarrantyState = "lost"
)

// WarrantySoonDistance is how close to the warranty's end reading (in the
// distance unit) it counts as ending soon. A date counts within
// DueSoonDays, as a service's does.
const WarrantySoonDistance = 1000

// Warranty is the computed state of a vehicle's warranty.
type Warranty struct {
	State WarrantyState
	// EndsOn and EndsAt are what was entered: the last day it covers, and
	// the odometer reading it ends past.
	EndsOn *time.Time
	EndsAt *int
	// Misses are the services it asked for that were done late or not at
	// all (WarrantyLost), the one that lost it first (FirstMiss); none
	// otherwise.
	Misses []WarrantyMiss
}

// FirstMiss is the miss that lost the warranty, the earliest by LostOn;
// nil when it isn't lost.
func (w Warranty) FirstMiss() *WarrantyMiss {
	if len(w.Misses) == 0 {
		return nil
	}
	return &w.Misses[0]
}

// WarrantyKit is a kit the warranty asks for on time (Kit.WarrantyRequired),
// as the warranty checks it: its schedule, and its break-in and full
// services on the vehicle, oldest first (intermediate services never count).
type WarrantyKit struct {
	KitID            int64
	Name             string
	IntervalDistance *int
	IntervalMonths   *int
	BreakInAt        *int
	Services         []ServiceRef
	// Readings are the vehicle's services with any kit or none, oldest
	// first: the readings the app knows a day for, which date a reading
	// passed with nothing logged (WarrantyMiss.LostOn).
	Readings []ServiceRef
}

// WarrantyMiss is a service the warranty asked for that was done past its
// limit and the grace (WarrantyGrace, WarrantyGraceDays), or is past them
// and not done.
type WarrantyMiss struct {
	KitID int64
	Kit   string
	Tier  KitTier // TierBreakIn or TierFull
	// DueOdometer and DueDate are the limit, without the grace;
	// LatestOdometer and LatestDate with it: the last reading and day it
	// could be done.
	DueOdometer    *int
	DueDate        *time.Time
	LatestOdometer *int
	LatestDate     *time.Time
	// Done is the service done late; nil when it hasn't been done.
	Done *ServiceRef
	// LostOn is the day the warranty was lost by this miss, as far as the
	// app can tell. With LostOnExact, the day after LatestDate: the date
	// alone was missed. Otherwise a reading was missed, on a day no reading
	// was kept, so it was lost by then: the day of the first service logged
	// past LatestOdometer (with any kit or none: WarrantyKit.Readings), or
	// the day after LatestDate if the date was missed too and that came
	// first. With no service logged past the reading, today: Dated is false
	// (the page says "past 16 500 km").
	LostOn      time.Time
	LostOnExact bool
	Dated       bool
}

// WarrantyGraceDays is how many days past its date a service the warranty
// asks for may still be done: dealers allow about a month, whatever the
// interval.
const WarrantyGraceDays = 30

// WarrantyGrace is how far past its reading a service the warranty asks
// for may still be done: 10% of its distance (100 km on a break-in at
// 1 000, 1 500 on a 15 000 interval), as dealers allow.
func WarrantyGrace(distance int) int { return distance / 10 }

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
		soon = soon || days <= DueSoonDays
	}
	if endsAt != nil {
		left := *endsAt - odo
		ended = ended || left < 0
		soon = soon || left <= WarrantySoonDistance
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
// lost, and Misses lists them all, the earliest by LostOn first (kits in
// their order on a tie). Every limit that passed fell while it ran (it runs
// from the purchase to its end), so once it has ended it shows as ended,
// lost or not.
func warrantyWithKits(endsOn *time.Time, endsAt *int, purchasedOn time.Time, purchaseOdo int, kits []WarrantyKit, odo int, today time.Time) Warranty {
	w := WarrantyOf(endsOn, endsAt, odo, today)
	if !w.Runs() {
		return w
	}
	for _, k := range kits {
		w.Misses = append(w.Misses, k.misses(purchasedOn, purchaseOdo, odo, today)...)
	}
	if len(w.Misses) > 0 {
		w.State = WarrantyLost
		slices.SortStableFunc(w.Misses, func(a, b WarrantyMiss) int { return a.LostOn.Compare(b.LostOn) })
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
//     late or not, or after the purchase before the first. No schedule
//     counts from the break-in, unless it is also the first full service
//     (BreakInIsFull): then its time counts, the distance still from the
//     purchase (Kit.breakInFull).
func (k WarrantyKit) misses(purchasedOn time.Time, purchaseOdo, odo int, today time.Time) []WarrantyMiss {
	var out []WarrantyMiss
	var full []ServiceRef
	for _, s := range k.Services {
		if s.Tier == TierFull || s.Tier == TierBreakIn {
			full = append(full, s)
		}
	}
	// passed dates a reading passed: the day of the first service logged
	// past it, if one was by the day on (a late service is one itself);
	// else on, not dated.
	passed := func(limit int, on time.Time) (time.Time, bool) {
		for _, r := range k.Readings {
			if r.Odometer > limit && !DateOnly(r.PerformedOn).After(DateOnly(on)) {
				return DateOnly(r.PerformedOn), true
			}
		}
		return DateOnly(on), false
	}
	if k.BreakInAt != nil && *k.BreakInAt > purchaseOdo {
		limit := *k.BreakInAt + WarrantyGrace(*k.BreakInAt)
		miss := WarrantyMiss{KitID: k.KitID, Kit: k.Name, Tier: TierBreakIn, DueOdometer: ptrTo(*k.BreakInAt), LatestOdometer: &limit}
		switch {
		case len(full) > 0 && full[0].Odometer > limit:
			done := full[0]
			miss.Done = &done
			miss.LostOn, _ = passed(limit, done.PerformedOn)
			miss.Dated = true
			out = append(out, miss)
		case len(full) == 0 && odo > limit:
			miss.LostOn, miss.Dated = passed(limit, today)
			out = append(out, miss)
		}
	}
	if k.IntervalDistance == nil && k.IntervalMonths == nil {
		return out
	}
	fromDate, fromOdo := purchasedOn, purchaseOdo
	// late checks a full service done (done; or not, by now) at the
	// reading atOdo on the day on, counted from the last full one.
	late := func(atOdo int, on time.Time, done bool) *WarrantyMiss {
		m := &WarrantyMiss{KitID: k.KitID, Kit: k.Name, Tier: TierFull}
		byOdo, byDate := false, false
		if k.IntervalDistance != nil {
			due, latest := fromOdo+*k.IntervalDistance, fromOdo+*k.IntervalDistance+WarrantyGrace(*k.IntervalDistance)
			m.DueOdometer, m.LatestOdometer = &due, &latest
			byOdo = atOdo > latest
		}
		if k.IntervalMonths != nil {
			due := addMonths(DateOnly(fromDate), *k.IntervalMonths)
			latest := due.AddDate(0, 0, WarrantyGraceDays)
			m.DueDate, m.LatestDate = &due, &latest
			byDate = DateOnly(on).After(latest)
		}
		switch {
		case byOdo:
			// By a reading: the first service logged past it, or the day
			// done; the date's day after if that came first.
			m.LostOn, m.Dated = passed(*m.LatestOdometer, on)
			m.Dated = m.Dated || done
			if byDate {
				if lost := m.LatestDate.AddDate(0, 0, 1); lost.Before(m.LostOn) {
					m.LostOn = lost
				}
				m.Dated = true
			}
		case byDate:
			m.LostOn, m.LostOnExact, m.Dated = m.LatestDate.AddDate(0, 0, 1), true, true
		default:
			return nil
		}
		return m
	}
	for _, s := range full {
		isFull := s.Tier == TierFull
		// A break-in that is also the first full service (a light rider's,
		// done once that service's time was close) counts as it, by its
		// time: the distance still counts from the purchase.
		if !isFull && !BreakInIsFull(s, purchasedOn, k.IntervalMonths) {
			continue
		}
		if m := late(s.Odometer, s.PerformedOn, true); m != nil {
			done := s
			m.Done = &done
			out = append(out, *m)
		}
		fromDate = s.PerformedOn
		if isFull {
			fromOdo = s.Odometer
		}
	}
	if m := late(odo, today, false); m != nil {
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
	return warrantyWithKits(v.WarrantyEndsOn, v.WarrantyEndsAt, v.PurchasedOn, v.OdometerAtPurchase, v.WarrantyKits, v.Odometer, today)
}

// Warranty works out the warranty of the kit's vehicle, from its odometer
// reading currentOdo, on the day today.
func (k Kit) Warranty(currentOdo int, today time.Time) Warranty {
	return warrantyWithKits(k.VehicleWarrantyEndsOn, k.VehicleWarrantyEndsAt, k.VehiclePurchasedOn, k.VehicleOdometerAtPurchase,
		k.VehicleWarrantyKits, currentOdo, today)
}
