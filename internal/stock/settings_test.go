package stock

import (
	"strings"
	"testing"
	"time"
)

// useSettings puts settings in use until the test ends: the defaults, as
// edit changes them. Tests here don't run in parallel.
func useSettings(t *testing.T, edit func(*Settings)) {
	t.Helper()
	old := Current()
	set := DefaultSettings()
	edit(&set)
	Use(set)
	t.Cleanup(func() { Use(old) })
}

// Current is the defaults until something is put in use, and a copy:
// changing what it returns changes nothing.
func TestCurrentIsACopy(t *testing.T) {
	useSettings(t, func(*Settings) {})
	c := Current()
	c.Currency = "XXX"
	c.Categories[0] = "Changed"
	if got := Current(); got.Currency != "EUR" {
		t.Errorf("Currency = %q after changing a copy", got.Currency)
	}
	set := DefaultSettings()
	Use(set)
	set.Categories[0] = "Changed after Use"
	if got := Current().Categories[0]; got != "Oil & Fluids" {
		t.Errorf("Categories[0] = %q after changing what was put in use", got)
	}
}

func TestNumberFormats(t *testing.T) {
	tests := []struct {
		f                           NumberFormat
		decimal, integer, qty, litr string
	}{
		{NumberSpaceDot, "1 234.50", "1 234 567", "1 525 ml", "1.5 L"},
		{NumberSpaceComma, "1 234,50", "1 234 567", "1 525 ml", "1,5 L"},
		{NumberCommaDot, "1,234.50", "1,234,567", "1,525 ml", "1.5 L"},
		{NumberDotComma, "1.234,50", "1.234.567", "1.525 ml", "1,5 L"},
	}
	for _, tt := range tests {
		t.Run(string(tt.f), func(t *testing.T) {
			useSettings(t, func(s *Settings) { s.NumberFormat = tt.f })
			if got := FormatDecimal(1234.5); got != tt.decimal {
				t.Errorf("FormatDecimal = %q, want %q", got, tt.decimal)
			}
			if got := FormatInt(1234567); got != tt.integer {
				t.Errorf("FormatInt = %q, want %q", got, tt.integer)
			}
			if got := FormatQuantity(1525, UnitML); got != tt.qty {
				t.Errorf("FormatQuantity(1525 ml) = %q, want %q", got, tt.qty)
			}
			if got := FormatQuantity(1500, UnitML); got != tt.litr {
				t.Errorf("FormatQuantity(1500 ml) = %q, want %q", got, tt.litr)
			}
			// Form values and exports stay machine numbers.
			if got := FormatNumber(1234.5); got != "1234.5" {
				t.Errorf("FormatNumber = %q, want 1234.5 whatever the format", got)
			}
			if got := FormatMoney(1234.5); got != "1234.50" {
				t.Errorf("FormatMoney = %q, want 1234.50 whatever the format", got)
			}
		})
	}
}

func TestFormatDecimal(t *testing.T) {
	useSettings(t, func(*Settings) {})
	for in, want := range map[float64]string{0: "0.00", 2.995: "3.00", 1234.5: "1 234.50", -0.001: "0.00", -12.5: "-12.50", -1234567.891: "-1 234 567.89"} {
		if got := FormatDecimal(in); got != want {
			t.Errorf("FormatDecimal(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDate(t *testing.T) {
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, layout := range DateFormats {
		useSettings(t, func(s *Settings) { s.DateFormat = layout })
		if got, want := FormatDate(day), DateExample(layout); got != want {
			t.Errorf("%s: FormatDate = %q, want %q", layout, got, want)
		}
	}
	useSettings(t, func(*Settings) {})
	if got := FormatDate(day); got != "2 Jan 2026" {
		t.Errorf("the default: %q", got)
	}
}

// The examples the Settings page offers.
func TestFormatExamples(t *testing.T) {
	got := []string{}
	for _, f := range NumberFormats {
		got = append(got, f.Example())
	}
	if strings.Join(got, " | ") != "1 500.50 | 1 500,50 | 1,500.50 | 1.500,50" {
		t.Errorf("number examples = %q", got)
	}
	got = got[:0]
	for _, f := range DateFormats {
		got = append(got, DateExample(f))
	}
	if strings.Join(got, " | ") != "2 Jan 2026 | 02.01.2026 | 2026-01-02 | 02/01/2026 | 01/02/2026" {
		t.Errorf("date examples = %q", got)
	}
}

func TestCheckSearchURL(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{DefaultSKUSearchURL, true},
		{"http://duckduckgo.com/?q={q}", true},
		{"https://example.com/search?q={q}&lang=de", true},
		{"https://parts.example.com/find/{q}", true},
		{"www.google.com/search?q={q}", false},
		{"javascript:alert(1)//{q}", false},
		{"https://www.google.com/search", false},
		{"https://example.com/search?q=&lang=de", false},
		{"https://example.com/{q}?q={q}", false},
		{"example.com/{q}", false},
		{"https://example.com/?q={q}&x=" + strings.Repeat("a", 500), false},
	}
	for _, tt := range tests {
		if msg := CheckSearchURL(tt.url); (msg == "") != tt.ok {
			t.Errorf("CheckSearchURL(%q) = %q, want ok %v", tt.url, msg, tt.ok)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	if errs := (&SettingsInput{Settings: DefaultSettings()}).Validate(); errs.Any() {
		t.Fatalf("the defaults: %v", errs)
	}
	tests := []struct {
		name  string
		edit  func(*Settings)
		field string // "" for none
	}{
		{"lei", func(s *Settings) { s.Currency = " lei " }, ""},
		{"no currency", func(s *Settings) { s.Currency = "  " }, "currency"},
		{"long currency", func(s *Settings) { s.Currency = "Romanian lei" }, "currency"},
		{"miles", func(s *Settings) { s.DistanceUnit = "mi" }, ""},
		{"long unit", func(s *Settings) { s.DistanceUnit = "nautical miles" }, "distance_unit"},
		{"a number format", func(s *Settings) { s.NumberFormat = NumberDotComma }, ""},
		{"no such number format", func(s *Settings) { s.NumberFormat = "apostrophe" }, "number_format"},
		{"no such date format", func(s *Settings) { s.DateFormat = "Jan 2" }, "date_format"},
		{"days ago 0", func(s *Settings) { s.DaysAgo = 0 }, ""},
		{"days ago -1", func(s *Settings) { s.DaysAgo = -1 }, "days_ago"},
		{"no recent changes", func(s *Settings) { s.DashboardRecent = 0 }, "dashboard_recent"},
		{"a page of 5", func(s *Settings) { s.ActivityPageSize = 5 }, "activity_page_size"},
		{"a page of 5 000", func(s *Settings) { s.ServicesPageSize = 5000 }, "services_page_size"},
		{"a history of 1 000", func(s *Settings) { s.ItemHistory = 1000 }, ""},
		{"a search without {q}", func(s *Settings) { s.SKUSearchURL = "https://www.google.com/search" }, "sku_search_url"},
		{"a long prefix", func(s *Settings) { s.SKUSearchPrefix = strings.Repeat("x", 101) }, "sku_search_prefix"},
		{"expiring 0 days", func(s *Settings) { s.ExpiringDays = 0 }, "expiring_days"},
		{"no categories", func(s *Settings) { s.Categories = nil }, ""},
		{"a long category", func(s *Settings) { s.Categories = []string{strings.Repeat("x", 81)} }, "categories"},
		{"a line 101 times is one line", func(s *Settings) { s.WorkNames = strings.Split(strings.Repeat("x\n", 101), "\n") }, ""},
		{"due soon 51%", func(s *Settings) { s.DueSoonPercent = 51 }, "due_soon_percent"},
		{"a grace of 0", func(s *Settings) { s.GracePercent, s.GraceDays = 0, 0 }, ""},
		{"a grace of 60%", func(s *Settings) { s.GracePercent = 60 }, "warranty_grace_percent"},
		{"warranty soon in 400 days", func(s *Settings) { s.WarrantySoonDays = 400 }, "warranty_soon_days"},
	}
	for _, tt := range tests {
		in := SettingsInput{Settings: DefaultSettings()}
		tt.edit(&in.Settings)
		errs := in.Validate()
		switch {
		case tt.field == "" && errs.Any():
			t.Errorf("%s: %v", tt.name, errs)
		case tt.field != "" && errs[tt.field] == "":
			t.Errorf("%s: no error on %s (%v)", tt.name, tt.field, errs)
		}
	}
	in := SettingsInput{Settings: DefaultSettings()}
	in.WorkNames = []string{" Oil Service ", "", "oil service", "Coolant"}
	in.Currency = " lei "
	if errs := in.Validate(); errs.Any() {
		t.Fatal(errs)
	}
	if strings.Join(in.WorkNames, "|") != "Oil Service|Coolant" || in.Currency != "lei" {
		t.Errorf("tidied: %q, %q", in.WorkNames, in.Currency)
	}
	in.Categories = nil
	for i := range 101 {
		in.Categories = append(in.Categories, "Category "+FormatInt(i))
	}
	if errs := in.Validate(); errs["categories"] == "" {
		t.Error("101 categories taken")
	}
}

// Due soon follows the settings: the days before a date, the share of a
// distance and its minimum, never more than half the interval.
func TestDueSoonSettings(t *testing.T) {
	today := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	last := func(daysAgo, odo int) *ServiceRef {
		return &ServiceRef{PerformedOn: today.AddDate(0, 0, -daysAgo), Odometer: odo}
	}
	byDist := Kit{IntervalDistance: ptr(10000), LastService: last(10, 0)}
	byDate := Kit{IntervalMonths: ptr(12), LastService: last(300, 0)} // due in 65 days
	tests := []struct {
		name string
		edit func(*Settings)
		kit  Kit
		odo  int
		want DueState
	}{
		{"10%: 1 000 before", func(*Settings) {}, byDist, 9000, DueSoon},
		{"10%: not 1 001 before", func(*Settings) {}, byDist, 8999, DueOK},
		{"20%: 2 000 before", func(s *Settings) { s.DueSoonPercent = 20 }, byDist, 8000, DueSoon},
		{"0%, at least 500", func(s *Settings) { s.DueSoonPercent = 0 }, byDist, 9499, DueOK},
		{"0%, at least 500: 500 before", func(s *Settings) { s.DueSoonPercent = 0 }, byDist, 9500, DueSoon},
		{"at least 3 000", func(s *Settings) { s.DueSoonMinDistance = 3000 }, byDist, 7000, DueSoon},
		{"at least 9 000 is still half the interval", func(s *Settings) { s.DueSoonMinDistance = 9000 }, byDist, 4999, DueOK},
		{"30 days: not 65 days before", func(*Settings) {}, byDate, 0, DueOK},
		{"70 days: 65 days before", func(s *Settings) { s.DueSoonDays = 70 }, byDate, 0, DueSoon},
		{"300 days is still half the year", func(s *Settings) { s.DueSoonDays = 300 }, Kit{IntervalMonths: ptr(12), LastService: last(100, 0)}, 0, DueOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSettings(t, tt.edit)
			if got := tt.kit.Due(tt.odo, today).State; got != tt.want {
				t.Errorf("state = %s, want %s", got, tt.want)
			}
		})
	}
}

// The warranty ends soon within the settings' distance and days.
func TestWarrantySoonSettings(t *testing.T) {
	today := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	in := func(days int) *time.Time { d := today.AddDate(0, 0, days); return &d }
	tests := []struct {
		name   string
		edit   func(*Settings)
		endsOn *time.Time
		endsAt *int
		want   WarrantyState
	}{
		{"60 days off, by default", func(*Settings) {}, in(60), nil, WarrantyActive},
		{"60 days off, soon within 90", func(s *Settings) { s.WarrantySoonDays = 90 }, in(60), nil, WarrantySoon},
		{"30 days off, soon within 0", func(s *Settings) { s.WarrantySoonDays = 0 }, in(30), nil, WarrantyActive},
		{"its last day, soon within 0", func(s *Settings) { s.WarrantySoonDays = 0 }, in(0), nil, WarrantySoon},
		{"3 000 off, by default", func(*Settings) {}, nil, ptr(15000), WarrantyActive},
		{"3 000 off, soon within 5 000", func(s *Settings) { s.WarrantySoonDistance = 5000 }, nil, ptr(15000), WarrantySoon},
		// The service due soon days no longer move it.
		{"60 days off, services soon within 90", func(s *Settings) { s.DueSoonDays = 90 }, in(60), nil, WarrantyActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSettings(t, tt.edit)
			if got := WarrantyOf(tt.endsOn, tt.endsAt, 12000, today).State; got != tt.want {
				t.Errorf("state = %s, want %s", got, tt.want)
			}
		})
	}
}

// The grace follows the settings: a share of the distance and days past
// the date, for every service the warranty asks for and the break-in.
func TestGraceSettings(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	oil := func(breakIn *int, s ...ServiceRef) []WarrantyKit {
		return []WarrantyKit{{KitID: 1, Name: "Oil", IntervalDistance: ptr(10000), IntervalMonths: ptr(12), BreakInAt: breakIn, Services: s}}
	}
	svc := func(tier KitTier, on time.Time, odo int) ServiceRef {
		return ServiceRef{ID: int64(odo) + 1, PerformedOn: on, Odometer: odo, Tier: tier}
	}
	tests := []struct {
		name  string
		edit  func(*Settings)
		kits  []WarrantyKit
		odo   int
		today time.Time
		want  WarrantyState
	}{
		{"full at 11 000, 10%", func(*Settings) {}, oil(nil, svc(TierFull, day(2025, 10, 1), 11000)), 12000, day(2025, 11, 1), WarrantyActive},
		{"full at 11 000, 5%", func(s *Settings) { s.GracePercent = 5 }, oil(nil, svc(TierFull, day(2025, 10, 1), 11000)), 12000, day(2025, 11, 1), WarrantyLost},
		{"full at 10 500, 5%", func(s *Settings) { s.GracePercent = 5 }, oil(nil, svc(TierFull, day(2025, 10, 1), 10500)), 12000, day(2025, 11, 1), WarrantyActive},
		{"full at 10 001, no grace", func(s *Settings) { s.GracePercent = 0 }, oil(nil, svc(TierFull, day(2025, 10, 1), 10001)), 12000, day(2025, 11, 1), WarrantyLost},
		{"full 31 days past the year, 30 days", func(*Settings) {}, oil(nil, svc(TierFull, day(2026, 2, 1), 4000)), 4500, day(2026, 2, 5), WarrantyLost},
		{"full 31 days past the year, 45 days", func(s *Settings) { s.GraceDays = 45 }, oil(nil, svc(TierFull, day(2026, 2, 1), 4000)), 4500, day(2026, 2, 5), WarrantyActive},
		{"full a day past the year, no grace", func(s *Settings) { s.GraceDays = 0 }, oil(nil, svc(TierFull, day(2026, 1, 2), 4000)), 4500, day(2026, 1, 5), WarrantyLost},
		{"break-in at 1 150, 10%", func(*Settings) {}, oil(ptr(1000), svc(TierBreakIn, day(2025, 2, 1), 1150)), 3000, day(2025, 4, 1), WarrantyLost},
		{"break-in at 1 150, 20%", func(s *Settings) { s.GracePercent = 20 }, oil(ptr(1000), svc(TierBreakIn, day(2025, 2, 1), 1150)), 3000, day(2025, 4, 1), WarrantyActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSettings(t, tt.edit)
			endsOn := day(2030, 1, 1)
			v := Vehicle{Odometer: tt.odo, PurchasedOn: day(2025, 1, 1), WarrantyEndsOn: &endsOn, WarrantyKits: tt.kits}
			if got := v.Warranty(tt.today).State; got != tt.want {
				t.Errorf("state = %s, want %s (missed %+v)", got, tt.want, firstMiss(v.Warranty(tt.today)))
			}
		})
	}
	// WarrantyGrace itself.
	useSettings(t, func(s *Settings) { s.GracePercent = 15 })
	if got := WarrantyGrace(1000); got != 150 {
		t.Errorf("WarrantyGrace(1000) at 15%% = %d", got)
	}
}
