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
		{"overdue by km", Kit{IntervalKm: ptr(6000), LastService: last(10, 10000)}, ptr(16000), DueOverdue},
		{"ok by date", Kit{IntervalMonths: ptr(12), LastService: last(100, 0)}, nil, DueOK},
		{"soon by date", Kit{IntervalMonths: ptr(12), LastService: last(350, 0)}, nil, DueSoon},
		{"overdue by date", Kit{IntervalMonths: ptr(12), LastService: last(400, 0)}, nil, DueOverdue},
		{"date overdue wins over km ok", Kit{IntervalKm: ptr(6000), IntervalMonths: ptr(12), LastService: last(400, 10000)}, ptr(10100), DueOverdue},
		{"km interval but no odometer", Kit{IntervalKm: ptr(6000), LastService: last(10, 10000)}, nil, DueUnknown},
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
