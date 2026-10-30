package stock

import (
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
	if got := d.Summary(); got != "in 1,200 km · in 45 days" {
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
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
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
	for left, want := range map[int]string{-300: "300 km over", 0: "due now", 1200: "in 1,200 km"} {
		if got := (Due{KmLeft: &left}).Summary(); got != want {
			t.Errorf("%d km left: %q, want %q", left, got, want)
		}
	}
}
