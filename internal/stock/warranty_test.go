package stock

import (
	"testing"
	"time"
)

func TestWarrantyOf(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	today := day(2026, 11, 23)
	tests := []struct {
		name   string
		endsOn *time.Time
		endsAt *int
		odo    int
		want   WarrantyState
	}{
		{name: "none entered", odo: 12000, want: WarrantyNone},
		{name: "date far off", endsOn: ptr(day(2028, 3, 1)), want: WarrantyActive},
		{name: "date in 31 days", endsOn: ptr(day(2026, 12, 24)), want: WarrantyActive},
		{name: "date in 30 days is soon", endsOn: ptr(day(2026, 12, 23)), want: WarrantySoon},
		{name: "its last day still counts", endsOn: ptr(today), want: WarrantySoon},
		{name: "the day after, ended", endsOn: ptr(day(2026, 11, 22)), want: WarrantyEnded},
		{name: "reading far off", endsAt: ptr(100000), odo: 12000, want: WarrantyActive},
		{name: "reading 1 000 km off is soon", endsAt: ptr(13000), odo: 12000, want: WarrantySoon},
		{name: "reading 1 001 km off", endsAt: ptr(13001), odo: 12000, want: WarrantyActive},
		{name: "at the reading still counts", endsAt: ptr(12000), odo: 12000, want: WarrantySoon},
		{name: "past the reading, ended", endsAt: ptr(11999), odo: 12000, want: WarrantyEnded},
		{name: "past the reading ends it before the date", endsOn: ptr(day(2028, 3, 1)), endsAt: ptr(11000), odo: 12000, want: WarrantyEnded},
		{name: "past the date ends it before the reading", endsOn: ptr(day(2026, 11, 1)), endsAt: ptr(100000), odo: 12000, want: WarrantyEnded},
		{name: "soon by date, far by reading: soon", endsOn: ptr(day(2026, 12, 15)), endsAt: ptr(100000), odo: 12000, want: WarrantySoon},
		{name: "soon by both", endsOn: ptr(day(2026, 11, 24)), endsAt: ptr(12800), odo: 12000, want: WarrantySoon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := WarrantyOf(tt.endsOn, tt.endsAt, tt.odo, today)
			if w.State != tt.want {
				t.Errorf("state = %s, want %s", w.State, tt.want)
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
	if got := WarrantyOf(&endsOn, nil, 0, justAfterMidnight).State; got != WarrantyEnded {
		t.Errorf("at 00:30 on the day after, state = %s, want ended", got)
	}
}

func TestWarrantyLabels(t *testing.T) {
	for s, want := range map[WarrantyState]string{
		WarrantyNone: "", WarrantyActive: "Under warranty", WarrantySoon: "Warranty ends soon", WarrantyEnded: "Warranty ended",
		WarrantyLost: "Warranty lost",
	} {
		if got := s.Label(); got != want {
			t.Errorf("%s.Label() = %q, want %q", s, got, want)
		}
	}
}

func TestVehicleWarrantyValidate(t *testing.T) {
	day := func(y int, m time.Month, d int) *time.Time { t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC); return &t }
	base := func(edit func(*VehicleInput)) Errors {
		in := VehicleInput{Name: "A", Kind: KindMotorcycle, Color: "#ffffff", Make: "KTM", Model: "390", Year: ptr(2024), VIN: "VBK1",
			Odometer: ptr(10000), PurchasedOn: day(2024, 1, 1), OdometerAtPurchase: ptr(0)}
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
		// The day is required; the reading is optional.
		{"ticked with a reading only", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsAt = true, ptr(100000) }), "warranty_ends_on"},
		{"ticked with both", base(func(in *VehicleInput) {
			in.HasWarranty, in.WarrantyEndsOn, in.WarrantyEndsAt = true, day(2028, 3, 1), ptr(100000)
		}), ""},
		{"unticked with nothing", base(func(in *VehicleInput) {}), ""},
		// Bought used: its warranty may have ended before it was bought.
		{"date before the purchase", base(func(in *VehicleInput) {
			in.HasWarranty, in.PurchasedOn, in.WarrantyEndsOn = true, day(2024, 7, 4), day(2023, 11, 11)
		}), ""},
		{"date year out of range", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsOn = true, day(2101, 1, 1) }), "warranty_ends_on"},
		{"reading 0", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsAt = true, ptr(0) }), "warranty_ends_at"},
		{"reading over the maximum", base(func(in *VehicleInput) { in.HasWarranty, in.WarrantyEndsAt = true, ptr(MaxOdometer+1) }), "warranty_ends_at"},
		{"reading below the purchase reading", base(func(in *VehicleInput) {
			in.HasWarranty, in.OdometerAtPurchase, in.WarrantyEndsOn, in.WarrantyEndsAt = true, ptr(5000), day(2028, 3, 1), ptr(4000)
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

// A warranty is lost when a kit it asks for (WarrantyKit) had its break-in
// or a full service done past its limit and the grace (10% of the distance,
// 30 days), or is past them and not done. Logging the missing services on
// time (backfilling) gets it back, and intermediate services never count.
func TestWarrantyLost(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	bought := day(2025, 1, 1)
	svc := func(tier KitTier, on time.Time, odo int) ServiceRef {
		return ServiceRef{ID: int64(odo) + 1, PerformedOn: on, Odometer: odo, Tier: tier}
	}
	oil := func(dist int, breakIn *int, s ...ServiceRef) WarrantyKit {
		return WarrantyKit{KitID: 1, Name: "Oil", IntervalDistance: ptr(dist), IntervalMonths: ptr(12), BreakInAt: breakIn, Services: s}
	}
	b := ptr(1000)
	tests := []struct {
		name     string
		kits     []WarrantyKit
		endsOn   time.Time
		odo      int
		today    time.Time
		want     WarrantyState
		wantTier KitTier
	}{
		{"break-in on time", []WarrantyKit{oil(10000, b, svc(TierBreakIn, day(2025, 2, 1), 1000))}, day(2030, 1, 1), 3000, day(2025, 4, 1), WarrantyActive, ""},
		{"break-in at 1 100, the grace", []WarrantyKit{oil(10000, b, svc(TierBreakIn, day(2025, 2, 1), 1100))}, day(2030, 1, 1), 3000, day(2025, 4, 1), WarrantyActive, ""},
		{"B6 break-in done at 6 000, logged as Full", []WarrantyKit{oil(10000, b, svc(TierFull, day(2025, 6, 1), 6000))}, day(2030, 1, 1), 6200, day(2025, 6, 6), WarrantyLost, TierBreakIn},
		{"break-in at 1 101", []WarrantyKit{oil(10000, b, svc(TierBreakIn, day(2025, 2, 1), 1101))}, day(2030, 1, 1), 3000, day(2025, 4, 1), WarrantyLost, TierBreakIn},
		{"no break-in yet at 1 100", []WarrantyKit{oil(10000, b)}, day(2030, 1, 1), 1100, day(2025, 2, 1), WarrantyActive, ""},
		{"B10 kit added late, nothing logged at 10 500", []WarrantyKit{oil(10000, b)}, day(2030, 1, 1), 10500, day(2025, 10, 1), WarrantyLost, TierBreakIn},
		{"B10 backfilled on time", []WarrantyKit{oil(10000, b, svc(TierBreakIn, day(2025, 2, 1), 1000), svc(TierFull, day(2025, 9, 1), 9800))}, day(2030, 1, 1), 10500, day(2025, 10, 1), WarrantyActive, ""},
		{"B17 only an intermediate first: the break-in missed", []WarrantyKit{oil(10000, b, svc(TierFull, day(2025, 12, 1), 10000))}, day(2030, 1, 1), 10200, day(2025, 12, 5), WarrantyLost, TierBreakIn},
		{"full at 11 000, the grace", []WarrantyKit{oil(10000, nil, svc(TierFull, day(2025, 10, 1), 11000))}, day(2030, 1, 1), 12000, day(2025, 11, 1), WarrantyActive, ""},
		{"full at 11 001", []WarrantyKit{oil(10000, nil, svc(TierFull, day(2025, 10, 1), 11001))}, day(2030, 1, 1), 12000, day(2025, 11, 1), WarrantyLost, TierFull},
		{"GreyJoy: full at 16 500 on a 15 000 kit", []WarrantyKit{oil(15000, nil, svc(TierFull, day(2025, 10, 1), 16500))}, day(2030, 1, 1), 17000, day(2025, 11, 1), WarrantyActive, ""},
		{"GreyJoy: full at 16 501", []WarrantyKit{oil(15000, nil, svc(TierFull, day(2025, 10, 1), 16501))}, day(2030, 1, 1), 17000, day(2025, 11, 1), WarrantyLost, TierFull},
		{"full 30 days past the year", []WarrantyKit{oil(10000, nil, svc(TierFull, day(2026, 1, 31), 4000))}, day(2030, 1, 1), 4500, day(2026, 2, 5), WarrantyActive, ""},
		{"full 31 days past the year", []WarrantyKit{oil(10000, nil, svc(TierFull, day(2026, 2, 1), 4000))}, day(2030, 1, 1), 4500, day(2026, 2, 5), WarrantyLost, TierFull},
		{"no full yet, 30 days past the year", []WarrantyKit{oil(10000, nil)}, day(2030, 1, 1), 4000, day(2026, 1, 31), WarrantyActive, ""},
		{"no full yet, 31 days past the year", []WarrantyKit{oil(10000, nil)}, day(2030, 1, 1), 4000, day(2026, 2, 1), WarrantyLost, TierFull},
		{"the second full counts from the first", []WarrantyKit{oil(10000, nil, svc(TierFull, day(2025, 9, 1), 9000), svc(TierFull, day(2026, 6, 1), 20001))}, day(2030, 1, 1), 21000, day(2026, 7, 1), WarrantyLost, TierFull},
		{"intermediates never count", []WarrantyKit{oil(10000, nil, svc(TierIntermediate, day(2025, 6, 1), 9000))}, day(2030, 1, 1), 9500, day(2025, 7, 1), WarrantyActive, ""},
		{"a second kit missed", []WarrantyKit{oil(10000, nil), {KitID: 2, Name: "Brake fluid", IntervalMonths: ptr(24)}}, day(2030, 1, 1), 5000, day(2027, 3, 1), WarrantyLost, TierFull},
		{"ended shows over lost", []WarrantyKit{oil(10000, b)}, day(2025, 9, 30), 10500, day(2025, 10, 1), WarrantyEnded, ""},
		{"no warranty: never lost", []WarrantyKit{oil(10000, b)}, time.Time{}, 10500, day(2025, 10, 1), WarrantyNone, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var endsOn *time.Time
			if !tt.endsOn.IsZero() {
				endsOn = &tt.endsOn
			}
			v := Vehicle{Odometer: tt.odo, PurchasedOn: bought, WarrantyEndsOn: endsOn, WarrantyKits: tt.kits}
			w := v.Warranty(tt.today)
			if w.State != tt.want {
				t.Fatalf("state = %s, want %s (missed %+v)", w.State, tt.want, w.Missed)
			}
			if (w.Missed != nil) != (tt.want == WarrantyLost) || (w.Missed != nil && w.Missed.Tier != tt.wantTier) {
				t.Errorf("missed = %+v, want a %q", w.Missed, tt.wantTier)
			}
		})
	}
}

// Once lost, the kits are scheduled as without a warranty: services take
// turns from the last one (B6: a Full at 6 000, the intermediate at 11 000,
// the full one at 16 000).
func TestWarrantyLostSchedulesInTurns(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	full := ServiceRef{ID: 1, PerformedOn: day(2025, 6, 1), Odometer: 6000, Tier: TierFull}
	k := Kit{IntervalDistance: ptr(10000), IntervalMonths: ptr(12), Intermediate: true, BreakInAt: ptr(1000),
		VehiclePurchasedOn: day(2025, 1, 1), VehicleWarrantyEndsOn: ptr(day(2030, 12, 31)),
		LastService: &full, LastFullService: &full, FirstServiceID: 1}
	k.VehicleWarrantyKits = []WarrantyKit{{KitID: 1, Name: "Oil", IntervalDistance: k.IntervalDistance, IntervalMonths: k.IntervalMonths,
		BreakInAt: k.BreakInAt, Services: []ServiceRef{full}}}
	today := day(2025, 6, 6)
	if got := k.Warranty(6200, today).State; got != WarrantyLost {
		t.Fatalf("state = %s, want lost", got)
	}
	if k.UnderWarranty(6200, today) {
		t.Error("lost: want no longer under warranty")
	}
	checkNext(t, k, 6200, today, "inter", DueOK, ptr(11000), "2026-06-01")
	if f := k.FullDue(6200, today); f.DueOdometer == nil || *f.DueOdometer != 16000 {
		t.Errorf("full due = %v, want 16 000", deref(f.DueOdometer))
	}
}
