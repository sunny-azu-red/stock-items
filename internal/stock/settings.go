package stock

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Settings are the app's preferences and the thresholds of its rules, set
// on the Settings page and kept in the settings table's one row. Every
// default is the value the app had before it had settings
// (DefaultSettings), so nothing changes until a setting is. The fields
// follow the page and the table: units and formats, lists, stock,
// services, warranty.
type Settings struct {
	Version int

	// Currency labels every price and total; nothing is converted.
	Currency string
	// DistanceUnit labels every distance (odometer readings, intervals,
	// the warranty), e.g. "km" or "mi". It is only a label: distances are
	// stored and compared as typed.
	DistanceUnit string
	// NumberFormat sets the thousands separator and the decimal mark of
	// the numbers pages show. Form values and exports keep 1500.5.
	NumberFormat NumberFormat
	// DateFormat is the Go layout of the dates pages show. Date boxes and
	// exports keep 2006-01-02.
	DateFormat string
	// DaysAgo: a date this many days back or fewer reads "3 days ago"
	// (today and yesterday as words); 0 shows every date in full.
	DaysAgo int
	// DashboardRecent is how many stock changes the dashboard shows.
	DashboardRecent int

	ActivityPageSize int // stock changes on one Activity page
	ServicesPageSize int // services on one page of the log
	ItemHistory      int // stock changes on an item's page
	VehicleServices  int // services on a vehicle's page

	// SKUSearchURL is the search link of a part number: the number goes in
	// place of its one {q}. SKUSearchPrefix, unless empty, is added to
	// every search, e.g. "site:de".
	SKUSearchURL    string
	SKUSearchPrefix string
	// ExpiringDays: an item in stock whose best-before date is this close
	// is "expiring soon".
	ExpiringDays int
	// Categories are offered for an item's category, after those in use.
	Categories []string

	// A service is due soon DueSoonDays before its date, and within
	// DueSoonPercent of its interval's distance (at least
	// DueSoonMinDistance) before its reading; never more than half the
	// interval (dueFrom).
	DueSoonDays        int
	DueSoonPercent     int
	DueSoonMinDistance int
	// WorkNames are offered for a kit's name and a service's Work
	// performed, after those in use.
	WorkNames []string

	// The warranty ends soon within WarrantySoonDistance of its end
	// reading or WarrantySoonDays of its end date.
	WarrantySoonDistance int
	WarrantySoonDays     int
	// A service the warranty asks for may be done up to GracePercent of
	// its distance and GraceDays past its limits and still keep it
	// (WarrantyGrace); GracePercent also sets the break-in window.
	GracePercent int
	GraceDays    int
}

// NumberFormat is how the pages write numbers: its thousands separator and
// decimal mark.
type NumberFormat string

// The number formats, as stored.
const (
	NumberSpaceDot   NumberFormat = "space_dot"   // 1 500.50 (a narrow no-break space)
	NumberSpaceComma NumberFormat = "space_comma" // 1 500,50
	NumberCommaDot   NumberFormat = "comma_dot"   // 1,500.50
	NumberDotComma   NumberFormat = "dot_comma"   // 1.500,50
)

// NumberFormats are the formats the Settings page offers, in its order.
var NumberFormats = []NumberFormat{NumberSpaceDot, NumberSpaceComma, NumberCommaDot, NumberDotComma}

// narrowSpace is a narrow no-break space. Unlike a comma or point it reads
// the same with decimal points and decimal commas: "1 500" is never 1.5.
const narrowSpace = "\u202f"

// Separators returns the format's thousands separator and decimal mark.
func (f NumberFormat) Separators() (thousands, decimal string) {
	switch f {
	case NumberSpaceComma:
		return narrowSpace, ","
	case NumberCommaDot:
		return ",", "."
	case NumberDotComma:
		return ".", ","
	}
	return narrowSpace, "."
}

// Example is 1500.5 written in the format, as the Settings page offers it.
func (f NumberFormat) Example() string {
	t, d := f.Separators()
	return "1" + t + "500" + d + "50"
}

// DateFormats are the date layouts the Settings page offers, in its order.
var DateFormats = []string{"2 Jan 2006", "02.01.2006", "2006-01-02", "02/01/2006", "01/02/2006"}

// DateExample is 2 January 2026 in a layout, as the Settings page offers it.
func DateExample(layout string) string {
	return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Format(layout)
}

// DefaultSKUSearchURL is the part number search of a new install.
const DefaultSKUSearchURL = "https://www.google.com/search?q={q}"

// DefaultSettings are the settings of a new install: what the app did
// before it had settings. The settings table's column defaults are the
// same (TestSettingsComplete).
func DefaultSettings() Settings {
	return Settings{
		Version:      1,
		Currency:     "EUR",
		DistanceUnit: "km",
		NumberFormat: NumberSpaceDot,
		DateFormat:   "2 Jan 2006",
		DaysAgo:      30, DashboardRecent: 8,
		ActivityPageSize: 200, ServicesPageSize: 100, ItemHistory: 100, VehicleServices: 10,
		SKUSearchURL: DefaultSKUSearchURL,
		ExpiringDays: 60,
		Categories: []string{"Oil & Fluids", "Filters", "Brakes", "Gaskets & O-Rings", "Chain & Sprockets",
			"Electrical", "Tyres", "Cleaning & Care", "Hardware", "Tools",
			"Maintenance Kits", "Bearings & Seals", "Controls & Cables", "Suspension"},
		DueSoonDays: 30, DueSoonPercent: 10, DueSoonMinDistance: 500,
		WorkNames: []string{"Oil Service", "Air Filter", "Brake Pads (Front)", "Brake Pads (Rear)",
			"Brake Fluid", "Cabin Filter", "Clutch Fluid", "Coolant",
			"Spark Plugs", "Valve Check"},
		WarrantySoonDistance: 1000, WarrantySoonDays: 30,
		GracePercent: 10, GraceDays: 30,
	}
}

// current is the settings in use: the saved row, which main reads at
// start and the Settings page replaces when it saves (Use). The app is one
// process, so a copy in memory is the row.
var current atomic.Pointer[Settings]

// Current returns a copy of the settings in use (DefaultSettings until
// Use): changing it changes nothing else.
func Current() Settings {
	s := current.Load()
	if s == nil {
		return DefaultSettings()
	}
	c := *s
	c.Categories = slices.Clone(s.Categories)
	c.WorkNames = slices.Clone(s.WorkNames)
	return c
}

// Use makes s the settings in use, whatever they were (main at start,
// tests).
func Use(s Settings) {
	s.Categories = slices.Clone(s.Categories)
	s.WorkNames = slices.Clone(s.WorkNames)
	current.Store(&s)
}

// UseSaved makes s, just saved, the settings in use, unless a later save
// is in use already: two saves close together each put theirs in use
// after their commit, in either order, and the row's version says which is
// newer.
func UseSaved(s Settings) {
	s.Categories = slices.Clone(s.Categories)
	s.WorkNames = slices.Clone(s.WorkNames)
	for {
		old := current.Load()
		if old != nil && old.Version >= s.Version {
			return
		}
		if current.CompareAndSwap(old, &s) {
			return
		}
	}
}

// SettingsInput is the Settings page's form.
type SettingsInput struct {
	Settings
	FormKey string
}

// The limits of the settings, as the settings table's CHECKs have them
// (one list line's length is Go's alone: a CHECK can't see into an array).
const (
	maxLabel     = 10  // currency and distance unit
	maxListItems = 100 // categories, work names
	maxListItem  = 80  // one category or work name
	maxSearchURL = 500
	maxPrefix    = 100
)

// Validate checks the form and tidies it: text trimmed, list lines
// trimmed with blank ones and repeats (ignoring case) left out.
func (in *SettingsInput) Validate() Errors {
	errs := Errors{}
	in.Currency = strings.TrimSpace(in.Currency)
	in.DistanceUnit = strings.TrimSpace(in.DistanceUnit)
	in.SKUSearchURL = strings.TrimSpace(in.SKUSearchURL)
	in.SKUSearchPrefix = strings.TrimSpace(in.SKUSearchPrefix)
	label := func(field, v, what string) {
		switch n := utf8.RuneCountInString(v); {
		case n == 0:
			errs.Add(field, "Enter the "+what)
		case n > maxLabel:
			errs.Add(field, fmt.Sprintf("At most %d characters", maxLabel))
		}
	}
	label("currency", in.Currency, "currency")
	label("distance_unit", in.DistanceUnit, "distance unit")
	if !slices.Contains(NumberFormats, in.NumberFormat) {
		errs.Add("number_format", "Choose one of the formats")
	}
	if !slices.Contains(DateFormats, in.DateFormat) {
		errs.Add("date_format", "Choose one of the formats")
	}
	between := func(field string, v, lo, hi int) {
		if v < lo || v > hi {
			errs.Add(field, fmt.Sprintf("Enter a number from %s to %s", FormatInt(lo), FormatInt(hi)))
		}
	}
	between("days_ago", in.DaysAgo, 0, 365)
	between("dashboard_recent", in.DashboardRecent, 1, 50)
	between("activity_page_size", in.ActivityPageSize, 10, 1000)
	between("services_page_size", in.ServicesPageSize, 10, 1000)
	between("item_history", in.ItemHistory, 10, 1000)
	between("vehicle_services", in.VehicleServices, 1, 100)
	if msg := CheckSearchURL(in.SKUSearchURL); msg != "" {
		errs.Add("sku_search_url", msg)
	}
	if utf8.RuneCountInString(in.SKUSearchPrefix) > maxPrefix {
		errs.Add("sku_search_prefix", fmt.Sprintf("At most %d characters", maxPrefix))
	}
	between("expiring_days", in.ExpiringDays, 1, 365)
	in.Categories = tidyList(errs, "categories", in.Categories)
	between("due_soon_days", in.DueSoonDays, 0, 365)
	between("due_soon_percent", in.DueSoonPercent, 0, 50)
	between("due_soon_min_distance", in.DueSoonMinDistance, 0, 100000)
	in.WorkNames = tidyList(errs, "work_names", in.WorkNames)
	between("warranty_soon_distance", in.WarrantySoonDistance, 0, 100000)
	between("warranty_soon_days", in.WarrantySoonDays, 0, 365)
	between("warranty_grace_percent", in.GracePercent, 0, 50)
	between("warranty_grace_days", in.GraceDays, 0, 365)
	return errs
}

// tidyList trims a list's lines, leaves out blank ones and repeats
// (ignoring case, the first spelling kept) and checks what is left.
func tidyList(errs Errors, field string, lines []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || seen[strings.ToLower(l)] {
			continue
		}
		seen[strings.ToLower(l)] = true
		if utf8.RuneCountInString(l) > maxListItem {
			errs.Add(field, fmt.Sprintf("%q is too long: at most %d characters a line", l, maxListItem))
		}
		out = append(out, l)
	}
	if len(out) > maxListItems {
		errs.Add(field, fmt.Sprintf("At most %d lines", maxListItems))
	}
	return out
}

// CheckSearchURL explains what is wrong with a part number search URL, or
// returns "": it must be a full http(s) URL with exactly one {q}, where the
// part number goes.
func CheckSearchURL(s string) string {
	if utf8.RuneCountInString(s) > maxSearchURL {
		return fmt.Sprintf("At most %d characters", maxSearchURL)
	}
	u, err := url.Parse(strings.ReplaceAll(s, "{q}", "x"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "Enter a full http(s) address, such as " + DefaultSKUSearchURL
	}
	switch n := strings.Count(s, "{q}"); {
	case n == 0:
		return "Mark where the part number goes with {q}, as in " + DefaultSKUSearchURL
	case n > 1:
		return fmt.Sprintf("Use {q} once, not %d times", n)
	}
	return ""
}

// FormatDate writes a calendar day in the date format in use.
func FormatDate(t time.Time) string { return t.Format(Current().DateFormat) }

// FormatLongDate writes a day as a heading does, with its weekday:
// "Friday, 27 November 2026" in the default format (the month in full),
// "Friday, 27.11.2026" in the others.
func FormatLongDate(t time.Time) string {
	if f := Current().DateFormat; f != "2 Jan 2006" {
		return t.Format("Monday, ") + t.Format(f)
	}
	return t.Format("Monday, 2 January 2006")
}

// DecimalExample writes an example number for a form's placeholder with
// the decimal mark in use: "1.5", or "1,5" (forms take either).
func DecimalExample(s string) string { return decimalMark(s) }

// FormatDecimal writes f with two decimals and thousands separators, in
// the number format in use: "1 234.50".
func FormatDecimal(f float64) string {
	return grouped(fmt.Sprintf("%.2f", f), Current().NumberFormat)
}

// grouped rewrites a number as strconv writes it ("-1234.5") in a number
// format: thousands separated, its decimal mark.
func grouped(s string, f NumberFormat) string {
	sep, dec := f.Separators()
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	if strings.Trim(whole+frac, "0123456789") != "" { // NaN, Inf: as it is
		return sign + s
	}
	if sign != "" && strings.Trim(whole+frac, "0") == "" {
		sign = "" // not "-0.00"
	}
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	if hasFrac {
		b.WriteString(dec)
		b.WriteString(frac)
	}
	return sign + b.String()
}
