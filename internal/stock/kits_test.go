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
	if (Kit{}).ReadinessAt(0, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)).Ready() {
		t.Error("an empty kit must not be ready")
	}
}

func TestKitDue(t *testing.T) {
	today := time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	last := func(daysAgo, odo int) *ServiceRef {
		return &ServiceRef{PerformedOn: today.AddDate(0, 0, -daysAgo), Odometer: odo}
	}
	tests := []struct {
		name       string
		kit        Kit
		currentOdo int
		want       DueState
	}{
		{"no interval", Kit{}, 0, DueNone},
		{"ok by distance", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, 11000, DueOK},
		{"soon by distance (threshold 600)", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, 15500, DueSoon},
		{"due exactly at the odometer target is not over yet", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, 16000, DueSoon},
		{"overdue by distance", Kit{IntervalDistance: ptr(6000), LastService: last(10, 10000)}, 16001, DueOverdue},
		{"ok by date", Kit{IntervalMonths: ptr(12), LastService: last(100, 0)}, 0, DueOK},
		{"soon by date", Kit{IntervalMonths: ptr(12), LastService: last(350, 0)}, 0, DueSoon},
		{"overdue by date", Kit{IntervalMonths: ptr(12), LastService: last(400, 0)}, 0, DueOverdue},
		{"date overdue wins over distance ok", Kit{IntervalDistance: ptr(6000), IntervalMonths: ptr(12), LastService: last(400, 10000)}, 10100, DueOverdue},
		{"short interval is not soon right after", Kit{IntervalDistance: ptr(300), LastService: last(1, 10000)}, 10010, DueOK},
		{"monthly is not soon right after", Kit{IntervalMonths: ptr(1), LastService: last(0, 0)}, 0, DueOK},
		{"monthly is soon in its second half", Kit{IntervalMonths: ptr(1), LastService: last(20, 0)}, 0, DueSoon},
		{"new vehicle ok by distance",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: 0}, 1000, DueOK},
		{"new vehicle soon by distance",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: 0}, 5500, DueSoon},
		{"new vehicle overdue by distance",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: 0}, 6001, DueOverdue},
		{"new vehicle ok by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: today.AddDate(0, 0, -100)}, 0, DueOK},
		{"new vehicle soon by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: today.AddDate(0, 0, -350)}, 0, DueSoon},
		{"new vehicle overdue by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: today.AddDate(0, 0, -400)}, 0, DueOverdue},
		{"new vehicle date overdue wins over distance ok",
			Kit{IntervalDistance: ptr(6000), IntervalMonths: ptr(12), VehicleOdometerAtPurchase: 0, VehiclePurchasedOn: today.AddDate(0, 0, -400)}, 1000, DueOverdue},
		{"past service ignores vehicle purchase baseline",
			Kit{IntervalDistance: ptr(6000), VehicleOdometerAtPurchase: 0, LastService: last(10, 10000)}, 11000, DueOK},
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
		r := Kit{Lines: tt.lines}.ReadinessAt(0, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
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
		return &ServiceRef{ID: 1, PerformedOn: on, Odometer: odo, Tier: tierOf(inter)}
	}
	firstFull := svc(d(2025, 1, 1), 0, false)
	// kit is the bike bought new on 1 Jan 2024 and fully serviced on
	// 1 Jan 2025 at 0 km; then the services in history, the last full one
	// last among the full ones.
	kit := func(warrantyOn time.Time, history ...*ServiceRef) Kit {
		k := Kit{VehicleWarrantyEndsOn: &warrantyOn, IntervalDistance: ptr(8000), IntervalMonths: ptr(12), Intermediate: true,
			VehiclePurchasedOn: d(2024, 1, 1), VehicleOdometerAtPurchase: 0}
		for _, s := range append([]*ServiceRef{firstFull}, history...) {
			k.LastService = s
			if s.Intermediate() {
				k.InterSinceFull = true
			} else {
				k.LastFullService, k.InterSinceFull = s, false
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
			checkNext(t, tt.k, tt.odo, tt.today, tt.wantNext, tt.wantState, &tt.wantOdo, tt.wantDate)
		})
	}
	// While it isn't its turn, the full one's due counts the two steps from
	// the last full service: 8 000 km, 24 months.
	if f := kit(ended).FullDue(3000, d(2025, 3, 1)); f.DueOdometer == nil || *f.DueOdometer != 8000 || f.DueDate == nil || !f.DueDate.Equal(d(2027, 1, 1)) {
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
			VehiclePurchasedOn: bought, VehicleOdometerAtPurchase: 0,
			Lines: []KitLine{{ItemID: 1, Need: 1, Have: 1}}, InterLines: []KitLine{{ItemID: 2, Need: 1, Have: 0}}}
	}
	k := kit()
	if !k.NextIsBreakIn(300, d(2025, 4, 20)) || k.NextIsIntermediate(300, d(2025, 4, 20)) {
		t.Fatal("a new kit with a break-in reading: want the break-in next")
	}
	checkNext(t, k, 300, d(2025, 4, 20), "full", DueOK, ptr(1000), "")
	checkNext(t, k, 600, d(2025, 4, 25), "full", DueSoon, ptr(1000), "")
	checkNext(t, k, 1100, d(2025, 5, 5), "full", DueOverdue, ptr(1000), "")
	if r := k.ReadinessAt(300, d(2025, 4, 20)); !r.Ready() {
		t.Errorf("the break-in takes the full parts, which are in stock: %+v", r)
	}
	// Logged (at 960 km): no schedule counts from it. The half one next,
	// from the purchase (4 000 km, a year on); the full one at 8 000 km or
	// 4 Apr 2026, not 8 960.
	breakIn := &ServiceRef{ID: 1, PerformedOn: d(2025, 4, 30), Odometer: 960, Tier: TierBreakIn}
	k.LastService, k.FirstServiceID = breakIn, breakIn.ID
	if k.NextIsBreakIn(2000, d(2025, 6, 1)) {
		t.Error("after the break-in: want it no longer next")
	}
	checkNext(t, k, 2000, d(2025, 6, 1), "inter", DueOK, ptr(4000), "2026-04-04")
	// Without a warranty, the same: they take turns from the purchase.
	noWarranty := k
	noWarranty.VehicleWarrantyEndsOn = nil
	checkNext(t, noWarranty, 2000, d(2025, 6, 1), "inter", DueOK, ptr(4000), "2026-04-04")
	if f := noWarranty.FullDue(2000, d(2025, 6, 1)); f.DueOdometer == nil || *f.DueOdometer != 8000 {
		t.Errorf("no warranty, full due after the break-in = %v, want 8 000 km", deref(f.DueOdometer))
	}
	if f := k.FullDue(2000, d(2025, 6, 1)); f.DueOdometer == nil || *f.DueOdometer != 8000 || f.DueDate == nil || !f.DueDate.Equal(d(2026, 4, 4)) {
		t.Errorf("full due after the break-in = %v / %v, want 8 000 km / 2026-04-04", deref(f.DueOdometer), f.DueDate)
	}
	// Without an intermediate service, the same: the full one from the purchase.
	k.Intermediate, k.InterLines = false, nil
	checkNext(t, k, 2000, d(2025, 6, 1), "full", DueOK, ptr(8000), "2026-04-04")
	// And without a warranty too: the schedule runs from the purchase.
	k.VehicleWarrantyEndsOn = nil
	checkNext(t, k, 2000, d(2025, 6, 1), "full", DueOK, ptr(8000), "2026-04-04")

	// A vehicle bought used, past the break-in reading: no break-in.
	used := kit()
	used.VehicleOdometerAtPurchase = 2148
	if used.NextIsBreakIn(2500, d(2025, 6, 1)) {
		t.Error("bought at 2 148 km: want no break-in")
	}
	// A kit added long after: the vehicle is past its first full distance.
	late := kit()
	late.VehicleOdometer = 8000
	if late.NextIsBreakIn(late.VehicleOdometer, d(2025, 6, 1)) {
		t.Error("at 8 000 km, a kit's first full distance: want no break-in")
	}
	// Past Break-in at and its 10% grace, a break-in can't be logged at
	// the reading: the full service is next, overdue since 1 000 km.
	late.VehicleOdometer = 1100
	if !late.NextIsBreakIn(late.VehicleOdometer, d(2025, 6, 1)) {
		t.Error("at 1 100 km, the grace: want the break-in still next (late)")
	}
	for _, odo := range []int{1101, 7999} {
		late.VehicleOdometer = odo
		if late.NextIsBreakIn(odo, d(2025, 6, 1)) || late.NextIsIntermediate(odo, d(2025, 6, 1)) {
			t.Errorf("at %d km: want the break-in missed and the full service next", odo)
		}
		checkNext(t, late, odo, d(2025, 6, 1), "full", DueOverdue, ptr(1000), "2026-04-04")
	}
	// A light rider: the full service's date comes before Break-in at. In
	// its last quarter (about 3 months) the full service takes over, so its
	// date doesn't pass behind a break-in due by distance only.
	slow := kit()
	if !slow.NextIsBreakIn(700, d(2026, 1, 2)) {
		t.Error("700 km, 92 days before the year: want the break-in still")
	}
	if slow.NextIsBreakIn(700, d(2026, 1, 3)) || slow.NextIsIntermediate(700, d(2026, 1, 3)) {
		t.Error("700 km, 91 days before the year (a quarter): want the full service")
	}
	// It stands in for the break-in, so it is due by Break-in at too.
	checkNext(t, slow, 700, d(2026, 1, 3), "full", DueSoon, ptr(1000), "2026-04-04")
	// Archived: none either.
	archived := kit()
	archived.ArchivedAt = ptr(d(2025, 5, 1))
	if archived.NextIsBreakIn(300, d(2025, 5, 1)) || archived.Due(300, d(2025, 5, 1)).State != DueNone {
		t.Error("archived kit: want no break-in")
	}
}

// checkNext checks which schedule the kit offers next and when it is due.
// While the break-in is waited for, the full service counts the kit's own
// interval from the purchase, with a warranty or without: an intermediate
// can't come first, so taking turns (two steps, 24 months) would leave a
// light rider with no date until the break-in's reading, 20 months on at
// 50 km a month.
func TestBreakInWaitOwnInterval(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	kit := func() Kit {
		return Kit{IntervalDistance: ptr(10000), IntervalMonths: ptr(12), Intermediate: true, BreakInAt: ptr(1000),
			VehiclePurchasedOn: d(2025, 1, 1)}
	}
	// No warranty: the break-in, then the full service in its last quarter,
	// by 1 Jan 2026 (not 2027), and by 1 000 km as it stands in for the
	// break-in.
	k := kit()
	if !k.NextIsBreakIn(400, d(2025, 9, 1)) {
		t.Error("400 km in September: want the break-in")
	}
	if k.NextIsBreakIn(450, d(2025, 10, 2)) {
		t.Error("450 km, 91 days before the year: want the full service")
	}
	checkNext(t, k, 450, d(2025, 10, 2), "full", DueOK, ptr(1000), "2026-01-01")
	checkNext(t, k, 610, d(2026, 1, 2), "full", DueOverdue, ptr(1000), "2026-01-01")
	// Under warranty: the same as before.
	w := kit()
	w.VehicleWarrantyEndsOn = ptr(d(2029, 12, 31))
	checkNext(t, w, 450, d(2025, 10, 2), "full", DueOK, ptr(1000), "2026-01-01")
	// Ticked, under warranty, the full service not done a month past its
	// date: the warranty is lost, and the full service stays overdue (it
	// went back to a break-in with no date for eight months).
	lost := w
	lost.VehicleWarrantyKits = []WarrantyKit{{KitID: 1, Name: "Oil", IntervalDistance: lost.IntervalDistance, IntervalMonths: lost.IntervalMonths, BreakInAt: lost.BreakInAt}}
	if got := lost.Warranty(651, d(2026, 2, 1)).State; got != WarrantyLost {
		t.Fatalf("state = %s, want lost", got)
	}
	for _, at := range []struct {
		odo int
		day time.Time
	}{{651, d(2026, 2, 1)}, {1000, d(2026, 9, 1)}, {1050, d(2026, 10, 2)}} {
		if lost.NextIsBreakIn(at.odo, at.day) {
			t.Errorf("%d km on %s: want the overdue full service, not the break-in", at.odo, at.day.Format(time.DateOnly))
		}
		checkNext(t, lost, at.odo, at.day, "full", DueOverdue, ptr(1000), "2026-01-01")
	}
	// Once the break-in (or the full service in its place) is logged, they
	// take turns as before: the intermediate a year on, the full one two.
	full := &ServiceRef{ID: 1, PerformedOn: d(2026, 1, 1), Odometer: 600, Tier: TierFull}
	k.LastService, k.LastFullService, k.FirstServiceID = full, full, 1
	checkNext(t, k, 700, d(2026, 3, 1), "inter", DueOK, ptr(5600), "2027-01-01")
	if f := k.FullDue(700, d(2026, 3, 1)); f.DueDate == nil || !f.DueDate.Equal(d(2028, 1, 1)) {
		t.Errorf("full due = %v, want 2028-01-01", f.DueDate)
	}
}

// A full service standing in for the break-in is due by Break-in at too:
// it counts as the break-in only by Break-in at + 10%, and its own
// 10 000 km would hide that reading. A rider of 100 km a month saw "in
// 9 100 km" at 900 km and lost the warranty at 1 101 with nothing
// overdue before.
func TestBreakInStandInDue(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	k := Kit{IntervalDistance: ptr(10000), IntervalMonths: ptr(12), Intermediate: true, BreakInAt: ptr(1000),
		VehiclePurchasedOn: d(2025, 1, 1), VehicleWarrantyEndsOn: ptr(d(2029, 12, 31)), WarrantyRequired: true}
	k.VehicleWarrantyKits = []WarrantyKit{{KitID: 1, Name: "Oil", IntervalDistance: k.IntervalDistance, IntervalMonths: k.IntervalMonths, BreakInAt: k.BreakInAt}}
	checkNext(t, k, 798, d(2025, 9, 1), "full", DueSoon, ptr(1000), "")
	if !k.NextIsBreakIn(798, d(2025, 9, 1)) {
		t.Error("1 Sep, 798 km: want the break-in")
	}
	// 2 Oct, 900 km: the full service takes over, in 100 km.
	if k.NextIsBreakIn(900, d(2025, 10, 2)) {
		t.Error("2 Oct: want the full service")
	}
	checkNext(t, k, 900, d(2025, 10, 2), "full", DueSoon, ptr(1000), "2026-01-01")
	if due := k.Due(900, d(2025, 10, 2)); due.Summary() != "in 100 km · in 91 days" {
		t.Errorf("summary = %q", due.Summary())
	}
	checkNext(t, k, 1050, d(2025, 11, 17), "full", DueOverdue, ptr(1000), "2026-01-01")
	// Logged at 1 000 km on 2 Nov, it counts as the break-in: the warranty
	// holds, and the schedules count from it as a full service.
	full := ServiceRef{ID: 1, PerformedOn: d(2025, 11, 2), Odometer: 1000, Tier: TierFull}
	k.LastService, k.LastFullService, k.FirstServiceID = &full, &full, 1
	k.VehicleWarrantyKits[0].Services = []ServiceRef{full}
	if w := k.Warranty(1200, d(2025, 12, 2)); w.State != WarrantyActive {
		t.Errorf("warranty = %s, want active", w.State)
	}
	checkNext(t, k, 1200, d(2025, 12, 2), "inter", DueOK, ptr(6000), "2026-11-02")
}

func checkNext(t *testing.T, k Kit, odo int, today time.Time, wantNext string, wantState DueState, wantOdo *int, wantDate string) {
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

// The full service takes over from the intermediate one in the last quarter
// of the kit's interval (2 500 km or about 3 months on 10 000 km / 12
// months), with a warranty or without: a light rider sees the full service
// three months ahead, and a skipped intermediate service doesn't stay next
// until the full one is overdue.
func TestFullTakesOver(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	kit := func(warranty bool, last *ServiceRef) Kit {
		k := Kit{IntervalDistance: ptr(10000), IntervalMonths: ptr(12), Intermediate: true,
			VehiclePurchasedOn: d(2025, 1, 1), VehicleOdometerAtPurchase: 0}
		if warranty {
			k.VehicleWarrantyEndsOn = ptr(d(2030, 12, 31))
		}
		if last != nil {
			k.LastService, k.LastFullService = last, last
		}
		return k
	}
	fullAt := func(on time.Time, odo int) *ServiceRef {
		return &ServiceRef{ID: 1, PerformedOn: on, Odometer: odo, Tier: TierFull}
	}
	light := fullAt(d(2026, 1, 1), 3600)   // W11, W12
	normal := fullAt(d(2025, 9, 1), 10000) // W1, W7, W8, N6
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
		{"W11 light rider, 92 days before the year: still the intermediate", kit(true, light), 6600, d(2026, 10, 1), "inter", DueOK, 8600, "2027-01-01"},
		{"W11 light rider, 91 days before the year: the full one", kit(true, light), 6600, d(2026, 10, 2), "full", DueOK, 13600, "2027-01-01"},
		{"W12 light rider, 25 days before", kit(true, light), 7100, d(2026, 12, 7), "full", DueSoon, 13600, "2027-01-01"},
		{"W1 normal rider: the intermediate at 15 000", kit(true, normal), 12000, d(2025, 10, 1), "inter", DueOK, 15000, "2026-09-01"},
		{"W8 intermediate overdue, full 4 000 away: still the intermediate", kit(true, normal), 16000, d(2026, 1, 1), "inter", DueOverdue, 15000, "2026-09-01"},
		{"intermediate skipped, 2 501 before the full: the intermediate", kit(true, normal), 17499, d(2026, 2, 1), "inter", DueOverdue, 15000, "2026-09-01"},
		{"intermediate skipped, 2 500 before the full: the full one", kit(true, normal), 17500, d(2026, 2, 1), "full", DueOK, 20000, "2026-09-01"},
		{"N6 no warranty, intermediate skipped, at 19 200: the full one", kit(false, normal), 19200, d(2026, 4, 1), "full", DueSoon, 20000, "2027-09-01"},
		{"N6 no warranty, at 17 000: still the intermediate", kit(false, normal), 17000, d(2026, 4, 1), "inter", DueOverdue, 15000, "2026-09-01"},
		{"B11 no warranty, nothing logged, at 10 500: the full one", kit(false, nil), 10500, d(2025, 10, 1), "full", DueOverdue, 10000, "2027-01-01"},
		{"N9 no warranty, light rider: the intermediate at the year", kit(false, light), 6600, d(2026, 11, 1), "inter", DueOK, 8600, "2027-01-01"},
		{"no warranty, light rider, intermediate skipped: still it 93 days before the Full's 24 months", kit(false, light), 9000, d(2027, 9, 30), "inter", DueOverdue, 8600, "2027-01-01"},
		{"no warranty, light rider: the Full in the last quarter of a year before its 24 months", kit(false, light), 9000, d(2027, 10, 5), "full", DueOK, 13600, "2028-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkNext(t, tt.k, tt.odo, tt.today, tt.wantNext, tt.wantState, &tt.wantOdo, tt.wantDate)
		})
	}
}

// A kit with a time only (Coolant every 48 months) has its intermediate
// service halfway through the time: at 24 months, then the Full, taking
// turns. With a distance, the intermediate service is halfway through the
// distance at the full time, as before.
func TestHalfwayByTime(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	in := KitInput{VehicleID: 1, Name: "Coolant", IntervalMonths: ptr(48), Intermediate: true}
	if errs := in.Validate(); errs.Any() {
		t.Fatalf("48 months, halfway: %v", errs)
	}
	for _, tt := range []struct {
		in         KitInput
		field, msg string
	}{
		{KitInput{VehicleID: 1, Name: "Coolant", IntervalMonths: ptr(9), Intermediate: true}, "interval_months",
			"Enter an even number of months: the intermediate service comes halfway through the time"},
		{KitInput{VehicleID: 1, Name: "Coolant", Intermediate: true}, "interval_distance",
			"Enter a distance or time: the intermediate service comes halfway through it"},
	} {
		if got := tt.in.Validate()[tt.field]; got != tt.msg {
			t.Errorf("%+v: %s = %q, want %q", tt.in, tt.field, got, tt.msg)
		}
	}

	coolant := func(warranty bool) Kit {
		k := Kit{IntervalMonths: ptr(48), Intermediate: true, VehiclePurchasedOn: d(2025, 1, 1)}
		if warranty {
			k.VehicleWarrantyEndsOn = ptr(d(2034, 12, 31))
		}
		return k
	}
	if m := coolant(false).InterMonths(); m == nil || *m != 24 {
		t.Errorf("InterMonths = %v, want 24", deref(m))
	}
	// Nothing logged: the intermediate service at 24 months, with a
	// warranty or without.
	for _, w := range []bool{false, true} {
		checkNext(t, coolant(w), 0, d(2025, 6, 1), "inter", DueOK, nil, "2027-01-01")
	}
	// Done late (1 Jul 2027): under warranty the Full keeps its 48 months
	// from the purchase; without, it is a step (24 months) after it.
	inter := ServiceRef{ID: 1, PerformedOn: d(2027, 7, 1), Tier: TierIntermediate}
	for _, tt := range []struct {
		warranty bool
		want     string
	}{{true, "2029-01-01"}, {false, "2029-07-01"}} {
		k := coolant(tt.warranty)
		k.LastService, k.InterSinceFull, k.FirstServiceID = &inter, true, 1
		checkNext(t, k, 0, d(2027, 8, 1), "full", DueOK, nil, tt.want)
	}
	// Skipped under warranty: overdue, until the Full's last quarter (its
	// last 12 months), when the Full takes over.
	w := coolant(true)
	checkNext(t, w, 0, d(2027, 6, 1), "inter", DueOverdue, nil, "2027-01-01")
	checkNext(t, w, 0, d(2028, 1, 2), "full", DueOK, nil, "2029-01-01")
	// After a Full without a warranty: the intermediate service 24 months
	// on, the Full 48.
	full := ServiceRef{ID: 2, PerformedOn: d(2029, 1, 1), Tier: TierFull}
	n := coolant(false)
	n.LastService, n.LastFullService, n.FirstServiceID = &full, &full, 2
	checkNext(t, n, 0, d(2029, 3, 1), "inter", DueOK, nil, "2031-01-01")
	if f := n.FullDue(0, d(2029, 3, 1)); f.DueDate == nil || !f.DueDate.Equal(d(2033, 1, 1)) {
		t.Errorf("full due = %v, want 2033-01-01", f.DueDate)
	}
	// With a distance, halfway is by distance, at the full time.
	km := coolant(false)
	km.IntervalDistance = ptr(10000)
	if m := km.InterMonths(); m == nil || *m != 48 {
		t.Errorf("with a distance: InterMonths = %v, want 48", deref(m))
	}
	checkNext(t, km, 0, d(2025, 6, 1), "inter", DueOK, ptr(5000), "2029-01-01")
	// Ticked: only the Fulls count. The intermediate service late or not
	// done never loses the warranty; a Full 31 days past its 48 months does.
	wk := WarrantyKit{KitID: 1, Name: "Coolant", IntervalMonths: ptr(48)}
	v := Vehicle{PurchasedOn: d(2025, 1, 1), WarrantyEndsOn: ptr(d(2034, 12, 31)), WarrantyKits: []WarrantyKit{wk}}
	if got := v.Warranty(d(2028, 6, 1)).State; got != WarrantyActive {
		t.Errorf("intermediate never done: %s, want active", got)
	}
	if got := v.Warranty(d(2029, 2, 1)).State; got != WarrantyLost {
		t.Errorf("no Full 31 days past 48 months: %s, want lost", got)
	}
}
