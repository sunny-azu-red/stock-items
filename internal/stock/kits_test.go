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
	r := k.ReadinessAt(k.VehicleOdometerKm, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
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
	last := func(daysAgo, km int) *ServiceRef {
		return &ServiceRef{PerformedOn: today.AddDate(0, 0, -daysAgo), OdometerKm: &km}
	}
	tests := []struct {
		name      string
		kit       Kit
		currentKm *int
		want      DueState
	}{
		{"no interval", Kit{}, nil, DueNone},
		{"never done", Kit{IntervalKm: ptr(6000)}, ptr(1000), DueNever},
		{"ok by km", Kit{IntervalKm: ptr(6000), LastService: last(10, 10000)}, ptr(11000), DueOK},
		{"soon by km (threshold 600)", Kit{IntervalKm: ptr(6000), LastService: last(10, 10000)}, ptr(15500), DueSoon},
		{"due exactly at the km is not over yet", Kit{IntervalKm: ptr(6000), LastService: last(10, 10000)}, ptr(16000), DueSoon},
		{"overdue by km", Kit{IntervalKm: ptr(6000), LastService: last(10, 10000)}, ptr(16001), DueOverdue},
		{"ok by date", Kit{IntervalMonths: ptr(12), LastService: last(100, 0)}, nil, DueOK},
		{"soon by date", Kit{IntervalMonths: ptr(12), LastService: last(350, 0)}, nil, DueSoon},
		{"overdue by date", Kit{IntervalMonths: ptr(12), LastService: last(400, 0)}, nil, DueOverdue},
		{"date overdue wins over km ok", Kit{IntervalKm: ptr(6000), IntervalMonths: ptr(12), LastService: last(400, 10000)}, ptr(10100), DueOverdue},
		{"km interval but no odometer", Kit{IntervalKm: ptr(6000), LastService: last(10, 10000)}, nil, DueUnknown},
		{"short interval is not soon right after", Kit{IntervalKm: ptr(300), LastService: last(1, 10000)}, ptr(10010), DueOK},
		{"monthly is not soon right after", Kit{IntervalMonths: ptr(1), LastService: last(0, 0)}, nil, DueOK},
		{"monthly is soon in its second half", Kit{IntervalMonths: ptr(1), LastService: last(20, 0)}, nil, DueSoon},
		{"km from an earlier reading when the last service has none",
			Kit{IntervalKm: ptr(6000), LastService: &ServiceRef{PerformedOn: today}, LastOdometerKm: ptr(10000)}, ptr(16500), DueOverdue},
		{"km from the odometer when a service without a reading was logged",
			Kit{IntervalKm: ptr(6000), LastService: &ServiceRef{PerformedOn: today, OdometerAtLog: ptr(16000)}, LastOdometerKm: ptr(10000)}, ptr(16100), DueOK},
		{"the service's own reading wins over the odometer at the time",
			Kit{IntervalKm: ptr(6000), LastService: &ServiceRef{PerformedOn: today, OdometerKm: ptr(15000), OdometerAtLog: ptr(16000)}}, ptr(20600), DueSoon},
		{"new vehicle ok by km",
			Kit{IntervalKm: ptr(6000), VehiclePurchasedKm: ptr(0)}, ptr(1000), DueOK},
		{"new vehicle soon by km",
			Kit{IntervalKm: ptr(6000), VehiclePurchasedKm: ptr(0)}, ptr(5500), DueSoon},
		{"new vehicle overdue by km",
			Kit{IntervalKm: ptr(6000), VehiclePurchasedKm: ptr(0)}, ptr(6001), DueOverdue},
		{"new vehicle ok by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -100))}, nil, DueOK},
		{"new vehicle soon by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -350))}, nil, DueSoon},
		{"new vehicle overdue by date",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -400))}, nil, DueOverdue},
		{"new vehicle date overdue wins over km ok",
			Kit{IntervalKm: ptr(6000), IntervalMonths: ptr(12), VehiclePurchasedKm: ptr(0), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -400))}, ptr(1000), DueOverdue},
		{"new vehicle km interval but no odometer",
			Kit{IntervalKm: ptr(6000), VehiclePurchasedKm: ptr(0)}, nil, DueUnknown},
		{"new vehicle km and date interval with purchase km only and no odometer",
			Kit{IntervalKm: ptr(6000), IntervalMonths: ptr(12), VehiclePurchasedKm: ptr(0)}, nil, DueUnknown},
		{"new vehicle km interval but only purchase date set",
			Kit{IntervalKm: ptr(6000), VehiclePurchasedOn: ptr(today.AddDate(0, 0, -100))}, ptr(1000), DueNever},
		{"new vehicle date interval but only purchase km set",
			Kit{IntervalMonths: ptr(12), VehiclePurchasedKm: ptr(0)}, ptr(1000), DueNever},
		{"past service ignores vehicle purchase baseline",
			Kit{IntervalKm: ptr(6000), VehiclePurchasedKm: ptr(0), LastService: last(10, 10000)}, ptr(11000), DueOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.kit.Due(tt.currentKm, today)
			if got.State != tt.want {
				t.Errorf("state = %s, want %s (due %+v)", got.State, tt.want, got)
			}
		})
	}
}

func TestDueSummary(t *testing.T) {
	d := Due{KmLeft: ptr(1200), DaysLeft: ptr(45)}
	if got := d.Summary(); got != "in 1 200 km · in 45 days" {
		t.Errorf("got %q", got)
	}
	d = Due{KmLeft: ptr(-300), DaysLeft: ptr(-1)}
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

func TestDueSummaryAtTheDueKm(t *testing.T) {
	for left, want := range map[int]string{-300: "300 km overdue", 0: "due now", 1200: "in 1 200 km"} {
		if got := (Due{KmLeft: &left}).Summary(); got != want {
			t.Errorf("%d km left: %q, want %q", left, got, want)
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
	d := Due{KmLeft: ptr(1200), DaysLeft: ptr(45)}
	if got := d.Summary(); got != "in 1\u202f200 mi · in 45 days" {
		t.Errorf("got %q", got)
	}
	if got := (Due{KmLeft: ptr(-300)}).Summary(); got != "300 mi overdue" {
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

func TestKitIntermediateSchedules(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	svc := func(id int64, on time.Time, km int, inter bool) *ServiceRef {
		return &ServiceRef{ID: id, PerformedOn: on, OdometerKm: &km, Intermediate: inter}
	}

	// 1. Warranty bike (1290): Full = 4000 km / 12 mo, Intermediate = 2000 km / nil months.
	t.Run("warranty mode (no intermediate months)", func(t *testing.T) {
		k := Kit{
			IntervalKm:      ptr(4000),
			IntervalMonths:  ptr(12),
			InterIntervalKm: ptr(2000),
			Lines:           []KitLine{{ItemID: 1, Need: 1, Have: 1}},
			InterLines:      []KitLine{{ItemID: 2, Need: 1, Have: 0}},
		}
		// After Full at 0 km on 2026-01-01: next is Intermediate at 2000 km.
		full1 := svc(1, day(2026, 1, 1), 0, false)
		k.LastService, k.LastFullService = full1, full1
		if !k.NextIsIntermediate(ptr(500), day(2026, 3, 1)) {
			t.Fatal("want NextIsIntermediate = true after full service")
		}
		due := k.Due(ptr(500), day(2026, 3, 1))
		if due.DueKm == nil || *due.DueKm != 2000 || due.DueDate != nil {
			t.Errorf("inter due = %+v, want 2000 km and no date", due)
		}
		if r := k.ReadinessAt(ptr(500), day(2026, 3, 1)); r.ShortLines != 1 {
			t.Errorf("readiness should check InterLines, got %+v", r)
		}

		// Collision right before warranty deadline (15 Dec 2026, 17 days left, at 2050 km):
		// Full is DueSoon by warranty date, so Full overrides Intermediate even though Intermediate is overdue by km.
		if k.NextIsIntermediate(ptr(2050), day(2026, 12, 15)) {
			t.Error("Full must override Intermediate when warranty deadline is DueSoon")
		}

		// After Intermediate at 2000 km on 2026-06-01: next is Full, and DueDate is still 2027-01-01!
		inter1 := svc(2, day(2026, 6, 1), 2000, true)
		k.LastService = inter1
		if k.NextIsIntermediate(ptr(2500), day(2026, 12, 5)) {
			t.Fatal("want NextIsIntermediate = false after intermediate service")
		}
		due = k.Due(ptr(2500), day(2026, 12, 5))
		if due.State != DueSoon || due.DueKm == nil || *due.DueKm != 4000 || due.DueDate == nil || !due.DueDate.Equal(day(2027, 1, 1)) {
			t.Errorf("full due after intermediate = %+v, want DueSoon at 4000 km / 2027-01-01", due)
		}
		if r := k.ReadinessAt(ptr(2500), day(2026, 12, 5)); !r.Ready() {
			t.Errorf("readiness should check Full Lines, got %+v", r)
		}
	})

	// 2. Non-warranty bike (390): Full = 4000 km / 12 mo, Intermediate = 2000 km / 12 mo.
	t.Run("alternating reset mode (intermediate months set)", func(t *testing.T) {
		k := Kit{
			IntervalKm:          ptr(4000),
			IntervalMonths:      ptr(12),
			InterIntervalKm:     ptr(2000),
			InterIntervalMonths: ptr(12),
		}
		full1 := svc(1, day(2026, 1, 1), 0, false)
		k.LastService, k.LastFullService = full1, full1
		// Low mileage (1500 km after 12 months): Intermediate is due by date (2027-01-01).
		if !k.NextIsIntermediate(ptr(1500), day(2027, 1, 1)) {
			t.Fatal("want NextIsIntermediate = true at 1500 km / 12 months")
		}
		// Log Intermediate at 1500 km on 2027-01-01: resets 12-month clock for Full to 2028-01-01 and km to 3500!
		inter1 := svc(2, day(2027, 1, 1), 1500, true)
		k.LastService = inter1
		if k.NextIsIntermediate(ptr(1600), day(2027, 2, 1)) {
			t.Fatal("want NextIsIntermediate = false after intermediate service")
		}
		due := k.Due(ptr(3000), day(2028, 1, 1))
		if due.DueKm == nil || *due.DueKm != 3500 || due.DueDate == nil || !due.DueDate.Equal(day(2028, 1, 1)) {
			t.Errorf("full due after alternating intermediate = %+v, want 3500 km / 2028-01-01", due)
		}
	})

	// Intermediate months shorter than full months: after an intermediate
	// service the full one is due one intermediate step later, in months as
	// in km, not a whole full interval later.
	t.Run("alternating mode with a shorter intermediate month interval", func(t *testing.T) {
		k := Kit{
			IntervalKm:          ptr(4000),
			IntervalMonths:      ptr(12),
			InterIntervalKm:     ptr(2000),
			InterIntervalMonths: ptr(6),
		}
		full1 := svc(1, day(2026, 1, 1), 0, false)
		k.LastService, k.LastFullService = full1, full1
		k.LastService = svc(2, day(2026, 7, 1), 2000, true)
		due := k.Due(ptr(2500), day(2026, 8, 1))
		if due.DueKm == nil || *due.DueKm != 4000 || due.DueDate == nil || !due.DueDate.Equal(day(2027, 1, 1)) {
			t.Errorf("full due after intermediate = %+v, want 4000 km / 2027-01-01", due)
		}
	})

	t.Run("new vehicle baseline with intermediate schedule", func(t *testing.T) {
		k := Kit{
			IntervalKm:         ptr(4000),
			IntervalMonths:     ptr(12),
			InterIntervalKm:    ptr(2000),
			VehiclePurchasedKm: ptr(0),
			VehiclePurchasedOn: ptr(day(2026, 1, 1)),
		}
		// At 500 km on 2026-03-01: next is Intermediate at 2000 km.
		if !k.NextIsIntermediate(ptr(500), day(2026, 3, 1)) {
			t.Fatal("want NextIsIntermediate = true from new vehicle baseline")
		}
		due := k.Due(ptr(500), day(2026, 3, 1))
		if due.DueKm == nil || *due.DueKm != 2000 || due.DueDate != nil {
			t.Errorf("inter due from new = %+v, want 2000 km", due)
		}
		// Once intermediate is logged at 2000 km, next is Full at 4000 km and
		// the annual warranty deadline remains anchored at 2027-01-01.
		inter1 := svc(1, day(2026, 6, 1), 2000, true)
		k.LastService = inter1
		if k.NextIsIntermediate(ptr(2500), day(2026, 7, 1)) {
			t.Fatal("want NextIsIntermediate = false after intermediate service logged")
		}
		fullDue := k.Due(ptr(2500), day(2026, 7, 1))
		if fullDue.DueKm == nil || *fullDue.DueKm != 4000 || fullDue.DueDate == nil || !fullDue.DueDate.Equal(day(2027, 1, 1)) {
			t.Errorf("full due after inter = %+v, want 4000 km / 2027-01-01", fullDue)
		}
	})

	t.Run("alternating mode promotes to full when full km threshold is reached", func(t *testing.T) {
		k := Kit{
			IntervalKm:          ptr(4000),
			IntervalMonths:      ptr(12),
			InterIntervalKm:     ptr(2000),
			InterIntervalMonths: ptr(12),
			LastService:         svc(1, day(2026, 1, 1), 0, false),
			LastFullService:     svc(1, day(2026, 1, 1), 0, false),
		}
		// Rode past both intermediate (2000 km) and full (4000 km) without logging intermediate:
		// Full service must take over.
		if k.NextIsIntermediate(ptr(4100), day(2026, 6, 1)) {
			t.Error("want NextIsIntermediate = false when full km is already overdue")
		}
	})
}

// The schedules of an expensive bike under warranty, and of one whose
// services simply alternate. Full every 15 000 km or 12 months, intermediate
// every 7 500 km: with no intermediate months (warranty), an intermediate
// service never moves the full service's deadline, which counts from the
// last full service or the purchase; with intermediate months (alternating),
// each service comes one intermediate step after the other, in km and in
// months. A month deadline with no day to count from is never "On track".
func TestWarrantyAndAlternatingSchedules(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	svc := func(on time.Time, km int, inter bool) *ServiceRef {
		return &ServiceRef{ID: 1, PerformedOn: on, OdometerKm: &km, Intermediate: inter}
	}
	type want struct {
		next    string // "full" or "inter"
		state   DueState
		dueKm   int
		dueDate string // "" for none
	}
	tests := []struct {
		name          string
		interMonths   *int
		boughtOn      *time.Time
		boughtKm      *int
		last, lastFul *ServiceRef
		km            int
		today         time.Time
		fullWant      want // FullDue, and which schedule is next
		uncounted     bool // MonthsUncounted
	}{
		{name: "warranty, new bike: intermediate next, full by the purchase", boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			km: 3000, today: d(2026, 6, 1), fullWant: want{"inter", DueOK, 15000, "2027-01-01"}},
		{name: "warranty, low miles: the full service's year comes first", boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			km: 5000, today: d(2026, 12, 10), fullWant: want{"full", DueSoon, 15000, "2027-01-01"}},
		{name: "warranty, low miles, past the year: overdue", boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			km: 5000, today: d(2027, 1, 2), fullWant: want{"full", DueOverdue, 15000, "2027-01-01"}},
		{name: "warranty, after an intermediate: the full deadline doesn't move", boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			last: svc(d(2026, 7, 1), 7500, true), km: 9000, today: d(2026, 8, 1), fullWant: want{"full", DueOK, 15000, "2027-01-01"}},
		{name: "warranty, a late intermediate doesn't push the full one back", boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			last: svc(d(2026, 8, 1), 8000, true), km: 14600, today: d(2026, 11, 1), fullWant: want{"full", DueSoon, 15000, "2027-01-01"}},
		{name: "warranty, after a full service: counts from it", boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			last: svc(d(2027, 1, 1), 15000, false), lastFul: svc(d(2027, 1, 1), 15000, false), km: 16000, today: d(2027, 2, 1),
			fullWant: want{"inter", DueOK, 30000, "2028-01-01"}},
		{name: "warranty, full then intermediate: still from the full one", boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			last: svc(d(2027, 7, 1), 22500, true), lastFul: svc(d(2027, 1, 1), 15000, false), km: 23000, today: d(2027, 12, 15),
			fullWant: want{"full", DueSoon, 30000, "2028-01-01"}},
		{name: "warranty, purchase date only", boughtOn: ptr(d(2026, 1, 1)),
			last: svc(d(2026, 7, 1), 7500, true), km: 9000, today: d(2026, 12, 10), fullWant: want{"full", DueSoon, 15000, "2027-01-01"}},
		{name: "warranty, no purchase date, only an intermediate: not done yet", last: svc(d(2026, 7, 1), 7500, true),
			km: 9000, today: d(2027, 8, 1), fullWant: want{"full", DueNever, 15000, ""}, uncounted: true},
		{name: "warranty, purchase km only, only an intermediate: not done yet", boughtKm: ptr(0), last: svc(d(2026, 7, 1), 7500, true),
			km: 9000, today: d(2026, 8, 1), fullWant: want{"full", DueNever, 15000, ""}, uncounted: true},
		{name: "warranty, purchase km only, no service: intermediate still next by km", boughtKm: ptr(0),
			km: 3000, today: d(2026, 6, 1), fullWant: want{"inter", DueNever, 15000, ""}, uncounted: true},
		{name: "alternating, after an intermediate: full one step later", interMonths: ptr(6), boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			last: svc(d(2026, 7, 1), 7500, true), km: 9000, today: d(2026, 8, 1), fullWant: want{"full", DueOK, 15000, "2027-01-01"}},
		{name: "alternating, after a full: intermediate next", interMonths: ptr(6), boughtOn: ptr(d(2026, 1, 1)), boughtKm: ptr(0),
			last: svc(d(2027, 1, 1), 15000, false), lastFul: svc(d(2027, 1, 1), 15000, false), km: 16000, today: d(2027, 2, 1),
			fullWant: want{"inter", DueOK, 30000, "2028-01-01"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := Kit{IntervalKm: ptr(15000), IntervalMonths: ptr(12), InterIntervalKm: ptr(7500), InterIntervalMonths: tt.interMonths,
				VehiclePurchasedOn: tt.boughtOn, VehiclePurchasedKm: tt.boughtKm, LastService: tt.last, LastFullService: tt.lastFul}
			km := tt.km
			got := k.FullDue(&km, tt.today)
			next := "full"
			if k.NextIsIntermediate(&km, tt.today) {
				next = "inter"
			}
			date := ""
			if got.DueDate != nil {
				date = got.DueDate.Format(time.DateOnly)
			}
			if next != tt.fullWant.next || got.State != tt.fullWant.state || got.DueKm == nil || *got.DueKm != tt.fullWant.dueKm || date != tt.fullWant.dueDate {
				t.Errorf("next %s, full %s at %v km by %q; want next %s, full %+v", next, got.State, got.DueKm, date, tt.fullWant.next, tt.fullWant)
			}
			if u := k.MonthsUncounted(&km, tt.today); u != tt.uncounted {
				t.Errorf("MonthsUncounted = %v, want %v", u, tt.uncounted)
			}
		})
	}
}

// Two cases the warranty table doesn't reach. Warranty mode with an
// intermediate under half the full one (15 000 / 5 000): the second
// intermediate service, at 10 000 km, is reminded too, while the full one
// keeps its own deadline. Alternating mode whose intermediate has months
// but no km: an intermediate service resets the months, but the full
// service's km still count from the last full one.
func TestIntermediateScheduleEdges(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	svc := func(on time.Time, km int, inter bool) *ServiceRef {
		return &ServiceRef{ID: 1, PerformedOn: on, OdometerKm: &km, Intermediate: inter}
	}
	km := func(n int) *int { return &n }

	full := svc(d(2026, 1, 1), 0, false)
	w := Kit{IntervalKm: ptr(15000), IntervalMonths: ptr(12), InterIntervalKm: ptr(5000), LastService: svc(d(2026, 4, 1), 5000, true), LastFullService: full}
	if !w.NextIsIntermediate(km(6000), d(2026, 5, 1)) {
		t.Error("15 000 / 5 000 after the intermediate at 5 000: want the next intermediate (10 000) first")
	}
	if due := w.Due(km(6000), d(2026, 5, 1)); due.DueKm == nil || *due.DueKm != 10000 {
		t.Errorf("15 000 / 5 000 due = %+v, want 10 000 km", due)
	}
	if f := w.FullDue(km(6000), d(2026, 5, 1)); f.DueKm == nil || *f.DueKm != 15000 || f.DueDate == nil || !f.DueDate.Equal(d(2027, 1, 1)) {
		t.Errorf("15 000 / 5 000 full due = %+v, want 15 000 km / 2027-01-01", f)
	}
	w.LastService = svc(d(2026, 7, 1), 10000, true)
	if w.NextIsIntermediate(km(11000), d(2026, 8, 1)) {
		t.Error("after the intermediate at 10 000: want the full service (15 000) next")
	}
	// The full one's year comes first: it wins even with an intermediate due sooner by km.
	w.LastService = svc(d(2026, 12, 1), 5000, true)
	if w.NextIsIntermediate(km(5500), d(2026, 12, 15)) {
		t.Error("full service due soon by date: want it next")
	}

	a := Kit{IntervalKm: ptr(10000), IntervalMonths: ptr(12), InterIntervalMonths: ptr(6), LastService: svc(d(2026, 7, 1), 6000, true), LastFullService: full}
	if due := a.FullDue(km(10500), d(2026, 10, 1)); due.State != DueOverdue || due.DueKm == nil || *due.DueKm != 10000 || due.DueDate == nil || !due.DueDate.Equal(d(2027, 1, 1)) {
		t.Errorf("alternating, intermediate by months only: full due = %+v, want overdue at 10 000 km / 2027-01-01", due)
	}
}
