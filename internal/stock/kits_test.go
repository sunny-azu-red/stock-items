package stock

import (
	"slices"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestKitReadiness(t *testing.T) {
	k := Kit{Lines: []KitLine{
		{ItemName: "Oil filter", Unit: UnitPiece, Need: 1, Have: 2},
		{ItemName: "Oil", Unit: UnitML, Need: 1700, Have: 525},
		{ItemName: "O-ring", Unit: UnitPiece, Need: 2, Have: 2},
	}}
	r := k.ReadinessAt(k.VehicleOdometer, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if r.Lines != 3 || r.ShortLines != 1 || r.Ready() {
		t.Errorf("readiness = %+v ready=%v", r, r.Ready())
	}
	if s := k.Lines[1].Short(); s != 1175 {
		t.Errorf("short = %v, want 1175", s)
	}
	if (Kit{}).ReadinessAt(nil, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)).Ready() {
		t.Error("an empty kit must not be ready")
	}
}

func TestKitDue(t *testing.T) {
	today := time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	last := func(daysAgo, odo int) *ServiceRef {
		return &ServiceRef{PerformedOn: today.AddDate(0, 0, -daysAgo), Odometer: &odo}
	}
	tests := []struct {
		name       string
		kit        Kit
		currentOdo *int
		want       DueState
	}{
		{"no interval", Kit{}, nil, DueNone},
		{"ok by distance", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, ptr(11000), DueOK},
		{"soon by distance (threshold 600)", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, ptr(15500), DueSoon},
		{"due exactly at the odometer target is not over yet", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, ptr(16000), DueSoon},
		{"overdue by distance", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, ptr(16001), DueOverdue},
		{"ok by date", Kit{IntervalMonths: ptr(12), LastService: last(100, 0)}, nil, DueOK},
		{"soon by date", Kit{IntervalMonths: ptr(12), LastService: last(350, 0)}, nil, DueSoon},
		{"overdue by date", Kit{IntervalMonths: ptr(12), LastService: last(400, 0)}, nil, DueOverdue},
		{"date overdue wins over distance ok", Kit{IntervalDistance: ptr(6000), IntervalMonths: ptr(12), LastService: last(400, 10000)}, ptr(10100), DueOverdue},
		{"distance interval but no odometer", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, nil, DueUnknown},
		{"short interval is not soon right after", Kit{IntervalDistance: ptr(300), LastService: last(1, 10000)}, ptr(10010), DueOK},
		{"monthly is not soon right after", Kit{IntervalMonths: ptr(1), LastService: last(0, 0)}, nil, DueOK},
		{"monthly is soon in its second half", Kit{IntervalMonths: ptr(1), LastService: last(20, 0)}, nil, DueSoon},
		{"distance from an earlier reading when the last service has none",
			Kit{IntervalDistance: ptr(6000), LastService: &ServiceRef{PerformedOn: today}, LastOdometer: ptr(10000)}, ptr(16500), DueOverdue},
		{"distance from the odometer when a service without a reading was logged",
			Kit{IntervalDistance: ptr(6000), LastService: &ServiceRef{PerformedOn: today, OdometerAtLog: ptr(16000)}, LastOdometer: ptr(10000)}, ptr(16100), DueOK},
		{"the service's own reading wins over the odometer at the time",
			Kit{IntervalDistance: ptr(6000), LastService: &ServiceRef{PerformedOn: today, Odometer: ptr(15000), OdometerAtLog: ptr(16000)}}, ptr(20600), DueSoon},
		{"new vehicle ok by distance",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: ptr(0)}, ptr(1000), DueOK},
		{"new vehicle soon by distance",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: ptr(0)}, ptr(5500), DueSoon},
		{"new vehicle overdue by distance",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: ptr(0)}, ptr(6001), DueOverdue},
		{"new vehicle ok by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -100))}, nil, DueOK},
		{"new vehicle soon by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -350))}, nil, DueSoon},
		{"new vehicle overdue by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -400))}, nil, DueOverdue},
		{"new vehicle date overdue wins over distance ok",
			Kit{IntervalDistance: ptr(6000), IntervalMonths: ptr(12), VehicleOdometerAtPurchase: ptr(0), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -400))}, ptr(1000), DueOverdue},
		{"past service ignores vehicle purchase baseline",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: ptr(0), LastService: last(10, 10000)}, ptr(11000), DueOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.kit.Due(tt.currentOdo, today)
			if got.State != tt.want {
				t.Errorf("state = %s, want %s (due %+v)", got.State, tt.want, got)
			}
		})
	}
}

func TestDueSummary(t *testing.T) {
	d := Due{DistanceLeft: ptr(1200), DaysLeft: ptr(45)}
	if got := d.Summary(); got != "in 1 200 km · in 45 days" {
		t.Errorf("got %q", got)
	}
	d = Due{DistanceLeft: ptr(-300), DaysLeft: ptr(-1)}
	if got := d.Summary(); got != "300 km overdue · 1 day overdue" {
		t.Errorf("got %q", got)
	}
	if got := (Due{DaysLeft: ptr(0)}).Summary(); got != "due today" {
		t.Errorf("got %q", got)
	}
}

func TestFormatInt(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1 000", 1234567: "1 234 567", -4500: "-4 500"} {
		if got := FormatInt(in); got != want {
			t.Errorf("FormatInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAddMonthsClampsToMonthEnd(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		from, want time.Time
		n          int
	}{
		{d(2026, 1, 31), d(2026, 2, 28), 1},
		{d(2028, 1, 31), d(2028, 2, 29), 1},
		{d(2026, 8, 31), d(2027, 2, 28), 6},
		{d(2026, 3, 15), d(2027, 3, 15), 12},
	}
	for _, tt := range tests {
		if got := addMonths(tt.from, tt.n); !got.Equal(tt.want) {
			t.Errorf("addMonths(%s, %d) = %s, want %s", tt.from.Format("2006-01-02"), tt.n, got.Format("2006-01-02"), tt.want.Format("2006-01-02"))
		}
	}
}

func TestReadinessIgnoresArchivedLines(t *testing.T) {
	tests := []struct {
		name         string
		lines        []KitLine
		lines2       int
		short, ready bool
	}{
		{"archived short line", []KitLine{{Need: 1, Have: 0, Archived: true}, {Need: 1, Have: 1}}, 1, false, true},
		{"only archived lines", []KitLine{{Need: 1, Have: 5, Archived: true}}, 0, false, false},
		{"active short line", []KitLine{{Need: 2, Have: 1}, {Need: 1, Have: 0, Archived: true}}, 1, true, false},
	}
	for _, tt := range tests {
		r := Kit{Lines: tt.lines}.ReadinessAt(nil, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
		if r.Lines != tt.lines2 || (r.ShortLines > 0) != tt.short || r.Ready() != tt.ready {
			t.Errorf("%s: %+v ready=%v", tt.name, r, r.Ready())
		}
	}
}

func TestDueSummaryAtTheDueOdometer(t *testing.T) {
	for left, want := range map[int]string{-300: "300 km overdue", 0: "due now", 1200: "in 1 200 km"} {
		if got := (Due{DistanceLeft: &left}).Summary(); got != want {
			t.Errorf("%d left: %q, want %q", left, got, want)
		}
	}
}

// APP_DISTANCE only relabels distances: the numbers stay as they are.
func TestFormatDistanceUnit(t *testing.T) {
	old := DistanceUnit
	t.Cleanup(func() { DistanceUnit = old })
	DistanceUnit = "mi"
	if got := FormatDistance(12000); got != "12\u202f000 mi" {
		t.Errorf("FormatDistance = %q", got)
	}
	d := Due{DistanceLeft: ptr(1200), DaysLeft: ptr(45)}
	if got := d.Summary(); got != "in 1\u202f200 mi · in 45 days" {
		t.Errorf("got %q", got)
	}
	if got := (Due{DistanceLeft: ptr(-300)}).Summary(); got != "300 mi overdue" {
		t.Errorf("got %q", got)
	}
}

// The day a service was logged is the app zone's: just after local
// midnight is the next day, whatever the date is in UTC, and a DST change
// doesn't stretch or shrink the week.
func TestLoggedSoonAfter(t *testing.T) {
	plus3 := time.FixedZone("UTC+3", 3*60*60)
	plus2 := time.FixedZone("UTC+2", 2*60*60)
	bucharest, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal(err)
	}
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) } // as a date column reads
	utc := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, time.UTC) }
	tests := []struct {
		name        string
		performedOn time.Time
		loggedAt    time.Time
		loc         *time.Location
		want        bool
	}{
		{"UTC+3, 7 days by 00:30 local", day(10, 25), utc(10, 31, 21, 30), plus3, true},
		{"UTC+3, 8 days by 00:30 local (7 in UTC)", day(10, 24), utc(10, 31, 21, 30), plus3, false},
		{"UTC+3, 7 days at 23:59 local", day(10, 24), utc(10, 31, 20, 59), plus3, true},
		{"UTC+3, given in the zone itself", day(10, 24), time.Date(2026, 11, 1, 0, 30, 0, 0, plus3), plus3, false},
		{"UTC+2, 23:59:59 on day 7", day(11, 1), time.Date(2026, 11, 8, 21, 59, 59, 0, time.UTC), plus2, true},
		{"UTC+2, midnight starting day 8", day(11, 1), utc(11, 8, 22, 0), plus2, false},
		{"UTC, the same instants", day(10, 24), utc(10, 31, 21, 30), time.UTC, true},
		{"logged the day it was done", day(10, 31), utc(10, 31, 21, 30), plus3, true},
		{"dated tomorrow", day(11, 2), utc(10, 31, 21, 30), plus3, true},
		// Spring forward on 29 March: the 23-hour day is in the week.
		{"Bucharest, 23:59 on 29 Mar", day(3, 22), utc(3, 29, 20, 59), bucharest, true},
		{"Bucharest, 00:10 on 30 Mar", day(3, 22), utc(3, 29, 21, 10), bucharest, false},
		// Fall back on 25 October: 03:30 comes twice, and the day has 25 hours.
		{"Bucharest, first 03:30 on 25 Oct", day(10, 18), utc(10, 25, 0, 30), bucharest, true},
		{"Bucharest, second 03:30 on 25 Oct", day(10, 18), utc(10, 25, 1, 30), bucharest, true},
		{"Bucharest, second 03:30, 8 days", day(10, 17), utc(10, 25, 1, 30), bucharest, false},
		{"Bucharest, 23:59 on 25 Oct", day(10, 18), utc(10, 25, 21, 59), bucharest, true},
		{"Bucharest, 00:00 on 26 Oct", day(10, 18), utc(10, 25, 22, 0), bucharest, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LoggedSoonAfter(tt.loggedAt, tt.performedOn, tt.loc); got != tt.want {
				t.Errorf("LoggedSoonAfter(%s, %s, %s) = %v, want %v", tt.loggedAt.Format(time.RFC3339),
					tt.performedOn.Format(time.DateOnly), tt.loc, got, tt.want)
			}
		})
	}
}

func TestSortKitLines(t *testing.T) {
	lines := []KitLine{
		{ItemID: 1, ItemName: "Oil filter", Unit: UnitPiece, Need: 1, Have: 0},    // short 1
		{ItemID: 2, ItemName: "engine oil", Unit: UnitML, Need: 1700, Have: 500},  // short 1200
		{ItemID: 3, ItemName: "Crush washer", Unit: UnitPiece, Need: 2, Have: 10}, // covered
		{ItemID: 4, ItemName: "Old gasket", Unit: UnitPiece, Need: 3, Have: 0, Archived: true},
		{ItemID: 5, ItemName: "Oil filter", Unit: UnitPiece, Need: 1, Have: 1},      // covered, same name
		{ItemID: 6, ItemName: "Assembly grease", Unit: UnitGram, Need: 10, Have: 5}, // short 5
	}
	tests := []struct {
		key  string
		desc bool
		want []int64
	}{
		{"", false, []int64{1, 2, 3, 4, 5, 6}}, // the kit's own order
		{"bogus", true, []int64{1, 2, 3, 4, 5, 6}},
		// Names ignore case; the two oil filters go by id.
		{KitSortItem, false, []int64{6, 3, 2, 1, 5, 4}},
		{KitSortItem, true, []int64{4, 5, 1, 2, 3, 6}},
		// Grouped by unit (g, ml, pieces) both ways; equal amounts by name.
		{KitSortNeed, true, []int64{6, 2, 4, 3, 1, 5}},
		{KitSortNeed, false, []int64{6, 2, 1, 5, 3, 4}},
		{KitSortHave, true, []int64{6, 2, 3, 5, 1, 4}},
		{KitSortHave, false, []int64{6, 2, 1, 4, 5, 3}},
		// The short lines first, by unit and amount, then the covered ones
		// (the archived one among them) by name.
		{KitSortMissing, true, []int64{6, 2, 1, 3, 5, 4}},
		{KitSortMissing, false, []int64{3, 5, 4, 6, 2, 1}},
	}
	for _, tt := range tests {
		var got []int64
		for _, l := range SortKitLines(lines, tt.key, tt.desc) {
			got = append(got, l.ItemID)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q desc=%v: got %v, want %v", tt.key, tt.desc, got, tt.want)
		}
	}
	if lines[0].ItemID != 1 || lines[5].ItemID != 6 {
		t.Error("SortKitLines changed the kit's own lines")
	}
}

func TestInterStep(t *testing.T) {
	for _, tt := range []struct {
		distance *int
		inter    bool
		want     *int
	}{
		{ptr(8000), true, ptr(4000)},
		{ptr(15000), true, ptr(7500)},
		{ptr(7999), true, ptr(3999)}, // rounded down
		{ptr(8000), false, nil},
		{nil, true, nil},
	} {
		got := Kit{IntervalDistance: tt.distance, Intermediate: tt.inter}.InterStep()
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("InterStep(%v, %v) = %v, want %v", deref(tt.distance), tt.inter, deref(got), deref(tt.want))
		}
	}
}

func tierOf(inter bool) KitTier {
	if inter {
		return TierIntermediate
	}
	return TierFull
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// The schedule the app is meant for: an odometer kept up to date, a full
// service every 8 000 km or 12 months and one halfway (4 000 km, the same
// 12 months), counted from a logged service or the purchase. While the
// warranty runs, the full service comes every 12 months from the last full
// one, whether a half service was done in between or not; once it has
// ended, or without one, nothing forces a full one: services take turns,
// each a step and 12 months after the one before.
func TestFullAndHalfService(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	svc := func(on time.Time, odo int, inter bool) *ServiceRef {
		return &ServiceRef{ID: 1, PerformedOn: on, Odometer: &odo, Tier: tierOf(inter)}
	}
	firstFull := svc(d(2025, 1, 1), 0, false)
	// kit is the bike bought new on 1 Jan 2024 and fully serviced on
	// 1 Jan 2025 at 0 km; then the services in history, the last full one
	// last among the full ones.
	kit := func(warrantyOn time.Time, history ...*ServiceRef) Kit {
		k := Kit{VehicleWarrantyEndsOn: &warrantyOn, IntervalDistance: ptr(8000), IntervalMonths: ptr(12), Intermediate: true,
			VehiclePurchasedOn: ptr(d(2024, 1, 1)), VehicleOdometerAtPurchase: ptr(0)}
		for _, s := range append([]*ServiceRef{firstFull}, history...) {
			k.LastService, k.LastOdometer = s, s.Odometer
			if s.Intermediate() {
				k.InterSinceFull = true
			} else {
				k.LastFullService, k.LastFullOdometer, k.InterSinceFull = s, s.Odometer, false
			}
		}
		return k
	}
	runs, ended := d(2030, 1, 1), d(2024, 12, 31)
	half := svc(d(2025, 8, 1), 4000, true)
	lateHalf := svc(d(2025, 9, 1), 5000, true)
	tests := []struct {
		name      string
		k         Kit
		odo       int
		today     time.Time
		wantNext  string
		wantState DueState
		wantOdo   int
		wantDate  string
	}{
		{"warranty, after a full: the half one at 4 000 km", kit(runs), 3000, d(2025, 3, 1), "inter", DueOK, 4000, "2026-01-01"},
		{"warranty, low use, no half done: the full one at the year", kit(runs), 3000, d(2025, 12, 10), "full", DueSoon, 8000, "2026-01-01"},
		{"warranty, after a half: the full one at 8 000 km or its year", kit(runs, half), 6000, d(2025, 9, 1), "full", DueOK, 8000, "2026-01-01"},
		{"warranty, a late half doesn't push the full one back", kit(runs, lateHalf), 6000, d(2025, 12, 10), "full", DueSoon, 8000, "2026-01-01"},
		{"warranty, after the next full: the half one counts from it", kit(runs, half, svc(d(2025, 12, 20), 7900, false)), 8500, d(2026, 2, 1), "inter", DueOK, 11900, "2026-12-20"},
		{"no warranty, low use: the half one at the year, no full one forced", kit(ended), 3000, d(2025, 12, 10), "inter", DueSoon, 4000, "2026-01-01"},
		{"no warranty, after a half: the full one a half step later", kit(ended, half), 6000, d(2025, 12, 10), "full", DueOK, 8000, "2026-08-01"},
		{"no warranty, low use, a half at the year: the full one a year later", kit(ended, svc(d(2026, 1, 1), 3000, true)), 4500, d(2026, 6, 1), "full", DueOK, 7000, "2027-01-01"},
		{"warranty ended after the half: the full one no longer by the warranty's year", kit(d(2025, 11, 30), lateHalf), 6000, d(2025, 12, 10), "full", DueOK, 9000, "2026-09-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkNext(t, tt.k, &tt.odo, tt.today, tt.wantNext, tt.wantState, &tt.wantOdo, tt.wantDate)
		})
	}
	// While it isn't its turn, the full one's due counts the two steps from
	// the last full service: 8 000 km, 24 months.
	if f := kit(ended).FullDue(ptr(3000), d(2025, 3, 1)); f.DueOdometer == nil || *f.DueOdometer != 8000 || f.DueDate == nil || !f.DueDate.Equal(d(2027, 1, 1)) {
		t.Errorf("no warranty, full due two steps on = %v / %v, want 8 000 km / 2027-01-01", deref(f.DueOdometer), f.DueDate)
	}
}

// dueBefore compares two reminders by distance and by date: one comes
// first when it is earlier in either, or has a deadline in one the other
// has none in; a tie in one goes by the other, a tie in all to b.
func TestDueBefore(t *testing.T) {
	day := func(m time.Month) *time.Time { t := time.Date(2026, m, 1, 0, 0, 0, 0, time.UTC); return &t }
	due := func(odo *int, date *time.Time) Due { return Due{DueOdometer: odo, DueDate: date} }
	for _, tt := range []struct {
		name string
		a, b Due
		want bool
	}{
		{"sooner by distance", due(ptr(5000), day(9)), due(ptr(8000), day(9)), true},
		{"later by distance, sooner by date", due(ptr(9000), day(3)), due(ptr(8000), day(9)), true},
		{"a tie by distance goes by the date", due(ptr(8000), day(3)), due(ptr(8000), day(9)), true},
		{"a tie in all goes to b", due(ptr(8000), day(9)), due(ptr(8000), day(9)), false},
		{"later in both", due(ptr(9000), day(10)), due(ptr(8000), day(9)), false},
		{"b has no date", due(ptr(9000), day(10)), due(ptr(8000), nil), true},
		{"b has no distance", due(ptr(9000), day(10)), due(nil, day(9)), true},
		{"a has neither", due(nil, nil), due(ptr(8000), day(9)), false},
	} {
		if got := dueBefore(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: dueBefore = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A new bike's break-in (1 000 km, "at or before" in its manual) is reminded
// until something is logged with the kit, takes the full parts, and the full
// schedule doesn't count from it: under warranty the first full service is
// still 8 000 km or 12 months from the purchase, not from the break-in.
func TestBreakIn(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	bought := d(2025, 4, 4)
	kit := func() Kit {
		return Kit{VehicleWarrantyEndsOn: ptr(d(2028, 4, 3)), IntervalDistance: ptr(8000), IntervalMonths: ptr(12), Intermediate: true, BreakInAt: ptr(1000),
			VehiclePurchasedOn: &bought, VehicleOdometerAtPurchase: ptr(0),
			Lines: []KitLine{{ItemID: 1, Need: 1, Have: 1}}, InterLines: []KitLine{{ItemID: 2, Need: 1, Have: 0}}}
	}
	k := kit()
	if !k.NextIsBreakIn() || k.NextIsIntermediate(ptr(300), d(2025, 4, 20)) {
		t.Fatal("a new kit with a break-in reading: want the break-in next")
	}
	checkNext(t, k, ptr(300), d(2025, 4, 20), "full", DueOK, ptr(1000), "")
	checkNext(t, k, ptr(600), d(2025, 4, 25), "full", DueSoon, ptr(1000), "")
	checkNext(t, k, ptr(1100), d(2025, 5, 5), "full", DueOverdue, ptr(1000), "")
	if r := k.ReadinessAt(ptr(300), d(2025, 4, 20)); !r.Ready() {
		t.Errorf("the break-in takes the full parts, which are in stock: %+v", r)
	}
	// Logged (at 960 km): no schedule counts from it. The half one next,
	// from the purchase (4 000 km, a year on); the full one at 8 000 km or
	// 4 Apr 2026, not 8 960.
	breakIn := &ServiceRef{ID: 1, PerformedOn: d(2025, 4, 30), Odometer: ptr(960), Tier: TierBreakIn}
	k.LastService, k.LastOdometer, k.HasBreakIn = breakIn, ptr(960), true
	if k.NextIsBreakIn() {
		t.Error("after the break-in: want it no longer next")
	}
	checkNext(t, k, ptr(2000), d(2025, 6, 1), "inter", DueOK, ptr(4000), "2026-04-04")
	// Without a warranty, the same: they take turns from the purchase.
	noWarranty := k
	noWarranty.VehicleWarrantyEndsOn = nil
	checkNext(t, noWarranty, ptr(2000), d(2025, 6, 1), "inter", DueOK, ptr(4000), "2026-04-04")
	if f := noWarranty.FullDue(ptr(2000), d(2025, 6, 1)); f.DueOdometer == nil || *f.DueOdometer != 8000 {
		t.Errorf("no warranty, full due after the break-in = %v, want 8 000 km", deref(f.DueOdometer))
	}
	if f := k.FullDue(ptr(2000), d(2025, 6, 1)); f.DueOdometer == nil || *f.DueOdometer != 8000 || f.DueDate == nil || !f.DueDate.Equal(d(2026, 4, 4)) {
		t.Errorf("full due after the break-in = %v / %v, want 8 000 km / 2026-04-04", deref(f.DueOdometer), f.DueDate)
	}
	// Without an intermediate service, the same: the full one from the purchase.
	k.Intermediate, k.InterLines = false, nil
	checkNext(t, k, ptr(2000), d(2025, 6, 1), "full", DueOK, ptr(8000), "2026-04-04")
	// And without a warranty too: the schedule runs from the purchase.
	k.VehicleWarrantyEndsOn = nil
	checkNext(t, k, ptr(2000), d(2025, 6, 1), "full", DueOK, ptr(8000), "2026-04-04")

	// A vehicle bought used, past the break-in reading: no break-in.
	used := kit()
	used.VehicleOdometerAtPurchase = ptr(2148)
	if used.NextIsBreakIn() {
		t.Error("bought at 2 148 km: want no break-in")
	}
	// A kit added long after: the vehicle is past its first full distance.
	late := kit()
	late.VehicleOdometer = ptr(8000)
	if late.NextIsBreakIn() {
		t.Error("at 8 000 km, a kit's first full distance: want no break-in")
	}
	late.VehicleOdometer = ptr(7999)
	if !late.NextIsBreakIn() {
		t.Error("at 7 999 km: want the break-in still next (late)")
	}
	// Archived: none either.
	archived := kit()
	archived.ArchivedAt = ptr(d(2025, 5, 1))
	if archived.NextIsBreakIn() || archived.Due(ptr(300), d(2025, 5, 1)).State != DueNone {
		t.Error("archived kit: want no break-in")
	}
}

// checkNext checks which schedule the kit offers next and when it is due.
func checkNext(t *testing.T, k Kit, odo *int, today time.Time, wantNext string, wantState DueState, wantOdo *int, wantDate string) {
	t.Helper()
	next := "full"
	if k.NextIsIntermediate(odo, today) {
		next = "inter"
	}
	due := k.Due(odo, today)
	date := ""
	if due.DueDate != nil {
		date = due.DueDate.Format(time.DateOnly)
	}
	odoOK := (due.DueOdometer == nil) == (wantOdo == nil) && (wantOdo == nil || *due.DueOdometer == *wantOdo)
	if next != wantNext || due.State != wantState || !odoOK || date != wantDate {
		t.Errorf("next %s, due %s at %v by %q; want next %s, %s at %v by %q", next, due.State, deref(due.DueOdometer), date, wantNext, wantState, deref(wantOdo), wantDate)
	}
}

func TestKitLineListed(t *testing.T) {
	listed := map[int64]float64{1: 2, 2: 1.5, 3: 5}
	tests := []struct {
		name string
		line KitLine
		want bool
	}{
		{"listed more", KitLine{ItemID: 3, Need: 2, Have: 0}, true},
		{"listed exactly", KitLine{ItemID: 1, Need: 3.5, Have: 1.5}, true},
		{"listed less", KitLine{ItemID: 2, Need: 3.5, Have: 1.5}, false},
		{"not on the list", KitLine{ItemID: 4, Need: 1, Have: 0}, false},
		{"misses nothing", KitLine{ItemID: 3, Need: 1, Have: 1}, false},
		{"archived", KitLine{ItemID: 3, Need: 2, Have: 0, Archived: true}, false},
	}
	lines := make([]KitLine, len(tests))
	for i, tt := range tests {
		if got := tt.line.Listed(listed); got != tt.want {
			t.Errorf("%s: Listed = %v, want %v", tt.name, got, tt.want)
		}
		lines[i] = tt.line
	}
	// Listed less and not on the list are left to add.
	if n := ToList(lines, listed); n != 2 {
		t.Errorf("ToList = %d, want 2", n)
	}
	if n := ToList(lines, nil); n != 4 {
		t.Errorf("ToList with nothing listed = %d, want 4", n)
	}
}
