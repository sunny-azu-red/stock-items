package stock

import "time"

// WarrantyState says where a vehicle's warranty stands.
type WarrantyState string

const (
	WarrantyNone   WarrantyState = "none"   // no end date or reading entered
	WarrantyActive WarrantyState = "active" // runs, and not near its end
	WarrantySoon   WarrantyState = "soon"   // runs, but ends soon
	WarrantyEnded  WarrantyState = "ended"
	// WarrantyLost: it would still run, but a service it asks for was done
	// late or not at all (Warranty.Missed).
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
	// Missed is the service that lost it (WarrantyLost), nil otherwise.
	Missed *WarrantyMiss
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
}

// WarrantyMiss is a service the warranty asked for that was done past its
// limit and the grace (WarrantyGrace, WarrantyGraceDays), or is past them
// and not done.
type WarrantyMiss struct {
	KitID int64
	Kit   string
	Tier  KitTier // TierBreakIn or TierFull
	// DueOdometer and DueDate are the limit, without the grace.
	DueOdometer *int
	DueDate     *time.Time
	// Done is the service done late; nil when it hasn't been done.
	Done *ServiceRef
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
// runs, whether a kit it asks for (kits) missed a service: then it is lost.
// Every limit that passed fell while it ran (it runs from the purchase to
// its end), so once it has ended it shows as ended, lost or not.
func warrantyWithKits(endsOn *time.Time, endsAt *int, purchasedOn time.Time, purchaseOdo int, kits []WarrantyKit, odo int, today time.Time) Warranty {
	w := WarrantyOf(endsOn, endsAt, odo, today)
	if !w.Runs() {
		return w
	}
	// The earliest miss of all its kits: the day it was done late, or
	// today for one not done.
	when := func(m *WarrantyMiss) time.Time {
		if m.Done != nil {
			return m.Done.PerformedOn
		}
		return today
	}
	for _, k := range kits {
		if m := k.missed(purchasedOn, purchaseOdo, odo, today); m != nil && (w.Missed == nil || when(m).Before(when(w.Missed))) {
			w.State, w.Missed = WarrantyLost, m
		}
	}
	return w
}

// missed returns the first service of the kit the warranty asked for that
// was done past its limit and the grace, or is past them now (the odometer
// odo, the day today) and not done, or nil.
//   - The break-in, on a vehicle bought below the kit's Break-in at: by its
//     reading only, done as the kit's first break-in or full service (one
//     logged as Full on time counts; an intermediate one doesn't).
//   - Each full service: its distance and months after the last full one,
//     or after the purchase before the first (no schedule counts from the
//     break-in, Kit.counted).
func (k WarrantyKit) missed(purchasedOn time.Time, purchaseOdo, odo int, today time.Time) *WarrantyMiss {
	var full []ServiceRef
	for _, s := range k.Services {
		if s.Tier == TierFull || s.Tier == TierBreakIn {
			full = append(full, s)
		}
	}
	if k.BreakInAt != nil && *k.BreakInAt > purchaseOdo {
		limit := *k.BreakInAt + WarrantyGrace(*k.BreakInAt)
		miss := &WarrantyMiss{KitID: k.KitID, Kit: k.Name, Tier: TierBreakIn, DueOdometer: ptrTo(*k.BreakInAt)}
		if len(full) > 0 && full[0].Odometer > limit {
			done := full[0]
			miss.Done = &done
			return miss
		}
		if len(full) == 0 && odo > limit {
			return miss
		}
	}
	if k.IntervalDistance == nil && k.IntervalMonths == nil {
		return nil
	}
	fromDate, fromOdo := purchasedOn, purchaseOdo
	late := func(atOdo int, on time.Time) *WarrantyMiss {
		m := &WarrantyMiss{KitID: k.KitID, Kit: k.Name, Tier: TierFull}
		over := false
		if k.IntervalDistance != nil {
			due := fromOdo + *k.IntervalDistance
			m.DueOdometer = &due
			over = over || atOdo > due+WarrantyGrace(*k.IntervalDistance)
		}
		if k.IntervalMonths != nil {
			due := addMonths(DateOnly(fromDate), *k.IntervalMonths)
			m.DueDate = &due
			over = over || DateOnly(on).After(due.AddDate(0, 0, WarrantyGraceDays))
		}
		if over {
			return m
		}
		return nil
	}
	for _, s := range full {
		if s.Tier != TierFull {
			continue
		}
		if m := late(s.Odometer, s.PerformedOn); m != nil {
			done := s
			m.Done = &done
			return m
		}
		fromDate, fromOdo = s.PerformedOn, s.Odometer
	}
	return late(odo, today)
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
