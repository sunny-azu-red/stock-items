package stock

import (
	"testing"
	"time"
)

func TestWarrantyOf(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	today := day(2026, 11, 23)
	tests := []struct {
		name     string
		endsOn   *time.Time
		endsAt   *int
		odo      *int
		want     WarrantyState
		wantDays *int
		wantLeft *int
	}{
		{name: "none entered", odo: ptr(12000), want: WarrantyNone},
		{name: "date far off", endsOn: ptr(day(2028, 3, 1)), want: WarrantyActive, wantDays: ptr(464)},
		{name: "date in 31 days", endsOn: ptr(day(2026, 12, 24)), want: WarrantyActive, wantDays: ptr(31)},
		{name: "date in 30 days is soon", endsOn: ptr(day(2026, 12, 23)), want: WarrantySoon, wantDays: ptr(30)},
		{name: "its last day still counts", endsOn: ptr(today), want: WarrantySoon, wantDays: ptr(0)},
		{name: "the day after, ended", endsOn: ptr(day(2026, 11, 22)), want: WarrantyEnded, wantDays: ptr(-1)},
		{name: "reading far off", endsAt: ptr(100000), odo: ptr(12000), want: WarrantyActive, wantLeft: ptr(88000)},
		{name: "reading 1 000 km off is soon", endsAt: ptr(13000), odo: ptr(12000), want: WarrantySoon, wantLeft: ptr(1000)},
		{name: "reading 1 001 km off", endsAt: ptr(13001), odo: ptr(12000), want: WarrantyActive, wantLeft: ptr(1001)},
		{name: "at the reading still counts", endsAt: ptr(12000), odo: ptr(12000), want: WarrantySoon, wantLeft: ptr(0)},
		{name: "past the reading, ended", endsAt: ptr(11999), odo: ptr(12000), want: WarrantyEnded, wantLeft: ptr(-1)},
		{name: "reading without an odometer never ends", endsAt: ptr(100), want: WarrantyActive},
		{name: "reading without an odometer, the date ends it", endsOn: ptr(day(2026, 1, 1)), endsAt: ptr(100), want: WarrantyEnded, wantDays: ptr(-326)},
		{name: "past the reading ends it before the date", endsOn: ptr(day(2028, 3, 1)), endsAt: ptr(11000), odo: ptr(12000), want: WarrantyEnded, wantDays: ptr(464), wantLeft: ptr(-1000)},
		{name: "past the date ends it before the reading", endsOn: ptr(day(2026, 11, 1)), endsAt: ptr(100000), odo: ptr(12000), want: WarrantyEnded, wantDays: ptr(-22), wantLeft: ptr(88000)},
		{name: "soon by date, far by reading: only the date is said", endsOn: ptr(day(2026, 12, 15)), endsAt: ptr(100000), odo: ptr(12000), want: WarrantySoon, wantDays: ptr(22), wantLeft: ptr(88000)},
		{name: "soon by both", endsOn: ptr(day(2026, 11, 24)), endsAt: ptr(12800), odo: ptr(12000), want: WarrantySoon, wantDays: ptr(1), wantLeft: ptr(800)},
	}
	eq := func(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := WarrantyOf(tt.endsOn, tt.endsAt, tt.odo, today)
			if w.State != tt.want {
				t.Errorf("state = %s, want %s", w.State, tt.want)
			}
			if !eq(w.DaysLeft, tt.wantDays) {
				t.Errorf("days left = %v, want %v", deref(w.DaysLeft), deref(tt.wantDays))
			}
			if !eq(w.DistanceLeft, tt.wantLeft) {
				t.Errorf("distance left = %v, want %v", deref(w.DistanceLeft), deref(tt.wantLeft))
			}
			if w.Runs() != (tt.want == WarrantyActive || tt.want == WarrantySoon) {
				t.Errorf("Runs() = %v for %s", w.Runs(), w.State)
			}
		})
	}
}

// The day counts in the app's zone: just after local midnight is already
// the next day, whatever the instant is in UTC.
func TestWarrantyOfCountsDays(t *testing.T) {
	plus3 := time.FixedZone("UTC+3", 3*3600)
	endsOn := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	justAfterMidnight := time.Date(2026, 11, 24, 0, 30, 0, 0, plus3) // 23 Nov in UTC
	if got := WarrantyOf(&endsOn, nil, nil, justAfterMidnight).State; got != WarrantyEnded {
		t.Errorf("at 00:30 on the day after, state = %s, want ended", got)
	}
}

func TestWarrantyLabels(t *testing.T) {
	for s, want := range map[WarrantyState]string{
		WarrantyNone: "", WarrantyActive: "Under warranty", WarrantySoon: "Warranty ends soon", WarrantyEnded: "Warranty ended",
	} {
		if got := s.Label(); got != want {
			t.Errorf("%s.Label() = %q, want %q", s, got, want)
		}
	}
}

func TestVehicleWarrantyValidate(t *testing.T) {
	day := func(y int, m time.Month, d int) *time.Time { t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC); return &t }
	base := func(edit func(*VehicleInput)) Errors {
		in := VehicleInput{Name: "A", Kind: KindMotorcycle, Color: "#ffffff"}
		edit(&in)
		return in.Validate()
	}
	tests := []struct {
		name  string
		errs  Errors
		field string // "" for no error
	}{
		{"ticked with nothing", base(func(in *VehicleInput) { in.HasWarranty = true }), "warranty_ends_on"},
		{"ticked with a date", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsOn = true, day(2028, 3, 1) }), ""},
		{"ticked with a reading", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsAt = true, ptr(100000) }), ""},
		{"unticked with nothing", base(func(in *VehicleInput) {}), ""},
		// Bought used: its warranty may have ended before it was bought.
		{"date before the purchase", base(func(in *VehicleInput) {
			in.HasWarranty, in.PurchasedOn, in.WarrantyEndsOn = true, day(2024, 7, 4), day(2023, 11, 11)
		}), ""},
		{"date year out of range", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsOn = true, day(2101, 1, 1) }), "warranty_ends_on"},
		{"reading 0", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsAt = true, ptr(0) }), "warranty_ends_at"},
		{"reading over the maximum", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsAt = true, ptr(MaxOdometer+1) }), "warranty_ends_at"},
		{"reading below the purchase reading", base(func(in *VehicleInput) {
			in.HasWarranty, in.OdometerAtPurchase, in.WarrantyEndsAt = true, ptr(5000), ptr(4000)
		}), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.field == "" {
				if len(tt.errs) > 0 {
					t.Errorf("errors %v, want none", tt.errs)
				}
				return
			}
			if tt.errs[tt.field] == "" {
				t.Errorf("no error on %s (errors %v)", tt.field, tt.errs)
			}
		})
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
