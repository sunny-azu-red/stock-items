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
	r := k.Readiness()
	if r.Lines != 3 || r.ShortLines != 1 || r.Ready() {
		t.Errorf("readiness = %+v ready=%v", r, r.Ready())
	}
	if s := k.Lines[1].Short(); s != 1175 {
		t.Errorf("short = %v, want 1175", s)
	}
	if (Kit{}).Readiness().Ready() {
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
	if got := d.Summary(); got != "300 km over · 1 day late" {
		t.Errorf("got %q", got)
	}
	if got := (Due{DaysLeft: ptr(0)}).Summary(); got != "today" {
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
		r := Kit{Lines: tt.lines}.Readiness()
		if r.Lines != tt.lines2 || (r.ShortLines > 0) != tt.short || r.Ready() != tt.ready {
			t.Errorf("%s: %+v ready=%v", tt.name, r, r.Ready())
		}
	}
}

func TestDueSummaryAtTheDueKm(t *testing.T) {
	for left, want := range map[int]string{-300: "300 km over", 0: "due now", 1200: "in 1 200 km"} {
		if got := (Due{KmLeft: &left}).Summary(); got != want {
			t.Errorf("%d km left: %q, want %q", left, got, want)
		}
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
