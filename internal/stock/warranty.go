package stock

import (
	"strings"
	"time"
)

// WarrantyState says where a vehicle's warranty stands.
type WarrantyState string

const (
	WarrantyNone   WarrantyState = "none"   // no end date or reading entered
	WarrantyActive WarrantyState = "active" // runs, and not near its end
	WarrantySoon   WarrantyState = "soon"   // runs, but ends soon
	WarrantyEnded  WarrantyState = "ended"
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
	// DaysLeft and DistanceLeft count to each end that is known.
	DaysLeft     *int
	DistanceLeft *int
}

// WarrantyOf works out a warranty that ends on endsOn (the last day it
// covers) or past the reading endsAt, whichever comes first, given the
// vehicle's odometer and today (a calendar day in the app zone). An unknown
// odometer never ends it; the date still can.
func WarrantyOf(endsOn *time.Time, endsAt *int, odo *int, today time.Time) Warranty {
	w := Warranty{State: WarrantyNone, EndsOn: endsOn, EndsAt: endsAt}
	if endsOn == nil && endsAt == nil {
		return w
	}
	ended, soon := false, false
	if endsOn != nil {
		// Both are midnight UTC days, so a DST change can't make one 23 hours.
		days := int(DateOnly(*endsOn).Sub(DateOnly(today)).Hours() / 24)
		w.DaysLeft = &days
		ended = ended || days < 0
		soon = soon || days <= DueSoonDays
	}
	if endsAt != nil && odo != nil {
		left := *endsAt - *odo
		w.DistanceLeft = &left
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

// Runs reports whether the warranty is in force (ending soon or not).
func (w Warranty) Runs() bool { return w.State == WarrantyActive || w.State == WarrantySoon }

// EndsIn says how soon a warranty that ends soon ends, counting only what
// is near: "in 22 days", "today", "in 800 km", or both joined with "or"
// ("in 22 days or 800 km"). An end that is still far off is left out.
func (w Warranty) EndsIn() string {
	var parts []string
	if d := w.DaysLeft; d != nil && *d >= 0 && *d <= DueSoonDays {
		if *d == 0 {
			parts = append(parts, "today")
		} else {
			parts = append(parts, "in "+plural(*d, "day"))
		}
	}
	if l := w.DistanceLeft; l != nil && *l >= 0 && *l <= WarrantySoonDistance {
		if len(parts) == 0 {
			parts = append(parts, "in "+FormatDistance(*l))
		} else {
			parts = append(parts, FormatDistance(*l))
		}
	}
	return strings.Join(parts, " or ")
}

// Label is the warranty's badge text, "" without one.
func (s WarrantyState) Label() string {
	switch s {
	case WarrantyActive:
		return "Under warranty"
	case WarrantySoon:
		return "Warranty ends soon"
	case WarrantyEnded:
		return "Warranty ended"
	}
	return ""
}

// Warranty works out the vehicle's warranty on the day today.
func (v Vehicle) Warranty(today time.Time) Warranty {
	return WarrantyOf(v.WarrantyEndsOn, v.WarrantyEndsAt, v.Odometer, today)
}

// Warranty works out the warranty of the kit's vehicle, from its odometer
// reading currentOdo, on the day today.
func (k Kit) Warranty(currentOdo *int, today time.Time) Warranty {
	return WarrantyOf(k.VehicleWarrantyEndsOn, k.VehicleWarrantyEndsAt, currentOdo, today)
}
