package web

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// The Settings page (stock.Settings): one form, its sections from the
// widest reach to the narrowest (units and formats, lists, then stock,
// services and the warranty, in the nav's order), every field with its
// "?" and, when it differs from its default, a tinted box and a ↺ showing
// the default. settingDefs is the one list of the fields:
// TestSettingsComplete checks it against the settings table.

// settingKind is how a setting is entered.
type settingKind string

const (
	settingText   settingKind = "text"
	settingNumber settingKind = "number" // a whole number
	settingSelect settingKind = "select"
	settingList   settingKind = "list" // one per line
)

// settingDef is one field of the Settings page: its column and form name,
// its label, its "?", how it is entered, and how it reads and writes a
// stock.Settings.
type settingDef struct {
	Name  string
	Label string
	// Unit, for a number: "days", "%" or "distance" (the distance unit).
	Unit string
	Tip  string
	Kind settingKind
	Span string // "", "span-2" or "span-all" (Forms rules: 4, 2 or 1 a row)
	// get writes the setting as its box holds it; set reads it from the
	// box into a Settings, saying what is wrong ("" when nothing).
	get func(stock.Settings) string
	set func(*stock.Settings, string) string
	// shown is the default as people read it, for the ↺ ("EUR", "10%") and
	// its title ("Reset to the 14 built-in categories"); short is the ↺'s
	// own text when shown is long.
	shown func(stock.Settings) (short, long string)
	// options, for a select: the values and what each reads.
	options func() []settingOption
}

// settingOption is one choice of a select.
type settingOption struct{ Value, Label string }

// settingSection is a section of the page: its heading ("" for the first,
// which has none) and note.
type settingSection struct {
	Title string
	Note  string
	Names []string
}

var settingSections = []settingSection{
	{Names: []string{"currency", "distance_unit", "number_format", "date_format", "days_ago", "dashboard_recent"}},
	{Title: "Lists", Note: "How many rows the longer lists show at once. Fewer loads faster on a phone.",
		Names: []string{"item_history", "services_page_size", "vehicle_services", "activity_page_size"}},
	{Title: "Stock", Note: "Part number links, best-before warnings and the categories the item form offers.",
		Names: []string{"sku_search_url", "sku_search_prefix", "expiring_days", "categories"}},
	{Title: "Services", Note: "When a kit's next service shows Due soon, and the names its forms offer. Overdue always starts at the limit itself.",
		Names: []string{"due_soon_days", "due_soon_percent", "due_soon_min_distance", "work_names"}},
	{Title: "Warranty", Note: "When a warranty shows Ends soon, and how overdue a service it asks for may be before it's lost. " +
		"Changing these checks every vehicle again at once. The grace is applied, never shown: pages aim at the limit itself, since a dealer may not give it.",
		Names: []string{"warranty_soon_distance", "warranty_soon_days", "warranty_grace_percent", "warranty_grace_days"}},
}

// textSetting is a setting typed as text.
func textSetting(name, label, tip, span string, p func(*stock.Settings) *string, shown func(string) string) settingDef {
	return settingDef{Name: name, Label: label, Tip: tip, Kind: settingText, Span: span,
		get: func(s stock.Settings) string { return *p(&s) },
		set: func(s *stock.Settings, v string) string { *p(s) = v; return "" },
		shown: func(s stock.Settings) (string, string) {
			v := shown(*p(&s))
			return v, v
		},
	}
}

// numberSetting is a whole number, in unit.
func numberSetting(name, label, unit, tip, span string, p func(*stock.Settings) *int) settingDef {
	return settingDef{Name: name, Label: label, Unit: unit, Tip: tip, Kind: settingNumber, Span: span,
		get: func(s stock.Settings) string { return strconv.Itoa(*p(&s)) },
		set: func(s *stock.Settings, v string) string {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return "Enter a whole number"
			}
			*p(s) = n
			return ""
		},
		shown: func(s stock.Settings) (string, string) {
			n := *p(&s)
			switch unit {
			case "days":
				return stock.FormatInt(n), plural(n, "day")
			case "%":
				return strconv.Itoa(n) + "%", strconv.Itoa(n) + "%"
			case "distance":
				return stock.FormatInt(n), stock.FormatDistance(n)
			}
			return stock.FormatInt(n), stock.FormatInt(n)
		},
	}
}

// listSetting is a list, one per line.
func listSetting(name, label, tip, what string, p func(*stock.Settings) *[]string) settingDef {
	return settingDef{Name: name, Label: label, Tip: tip, Kind: settingList, Span: "span-all",
		get: func(s stock.Settings) string { return strings.Join(*p(&s), "\n") },
		set: func(s *stock.Settings, v string) string { *p(s) = strings.Split(v, "\n"); return "" },
		shown: func(s stock.Settings) (string, string) {
			return "built-in", fmt.Sprintf("the %d built-in %s", len(*p(&s)), what)
		},
	}
}

// selectSetting is one of a few choices.
func selectSetting(name, label, tip string, p func(*stock.Settings) *string, options func() []settingOption) settingDef {
	label1 := func(v string) string {
		for _, o := range options() {
			if o.Value == v {
				return o.Label
			}
		}
		return v
	}
	return settingDef{Name: name, Label: label, Tip: tip, Kind: settingSelect, options: options,
		get: func(s stock.Settings) string { return *p(&s) },
		set: func(s *stock.Settings, v string) string { *p(s) = v; return "" },
		shown: func(s stock.Settings) (string, string) {
			l := label1(*p(&s))
			return l, l
		},
	}
}

// same is the text itself, or "none" for nothing.
func same(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

var settingDefs = []settingDef{
	textSetting("currency", "Currency",
		"Shown with every price and total. Only a label: prices stay as typed, nothing is converted. Up to 10 characters, such as LEI, €, USD.",
		"", func(s *stock.Settings) *string { return &s.Currency }, same),
	textSetting("distance_unit", "Distance unit",
		"Odometer readings, service intervals and the warranty. Only a label: switching km to mi doesn't convert the readings saved. Up to 10 characters.",
		"", func(s *stock.Settings) *string { return &s.DistanceUnit }, same),
	selectSetting("number_format", "Numbers",
		"Thousands and decimals on every page. Forms take a dot or a comma as the decimal mark whatever this says, and the CSV exports' number columns always write 1500.50 (their display columns follow this).",
		func(s *stock.Settings) *string { return (*string)(&s.NumberFormat) },
		func() []settingOption {
			var o []settingOption
			for _, f := range stock.NumberFormats {
				o = append(o, settingOption{string(f), f.Example()})
			}
			return o
		}),
	selectSetting("date_format", "Dates",
		"Every date the pages show. Date boxes follow your browser's own format, and the exports write 2026-01-02. Trash entries keep the format they were deleted with.",
		func(s *stock.Settings) *string { return &s.DateFormat },
		func() []settingOption {
			var o []settingOption
			for _, f := range stock.DateFormats {
				o = append(o, settingOption{f, stock.DateExample(f)})
			}
			return o
		}),
	numberSetting("days_ago", "“Days ago” up to", "days",
		"Dates this many days back or fewer read as “3 days ago”, today and yesterday as words. 0 shows every date in full.",
		"span-2", func(s *stock.Settings) *int { return &s.DaysAgo }),
	numberSetting("dashboard_recent", "Recent changes on the dashboard", "",
		"How many of the latest stock changes the dashboard shows, from 1 to 50.",
		"span-2", func(s *stock.Settings) *int { return &s.DashboardRecent }),
	numberSetting("item_history", "Changes on an item's page", "",
		"The item's history, newest first, from 10 to 1 000. Older ones are on Activity, filtered to the item.",
		"", func(s *stock.Settings) *int { return &s.ItemHistory }),
	numberSetting("services_page_size", "Services per page", "",
		"Services on one page of Service history (Services), the older ones behind Older, from 10 to 1 000.",
		"", func(s *stock.Settings) *int { return &s.ServicesPageSize }),
	numberSetting("vehicle_services", "Services on a vehicle's page", "",
		"The latest services in a vehicle's Service history card, from 1 to 100. All services → shows the rest.",
		"", func(s *stock.Settings) *int { return &s.VehicleServices }),
	numberSetting("activity_page_size", "Activity per page", "",
		"Stock changes on one Activity page, the older ones behind Older, from 10 to 1 000.",
		"", func(s *stock.Settings) *int { return &s.ActivityPageSize }),
	textSetting("sku_search_url", "Part number search",
		"Where a part number links to: {q} is where the number goes, and an item's brand goes in with it. Any http(s) address with one {q}, such as https://www.ebay.com/sch/i.html?_nkw={q}.",
		"span-2", func(s *stock.Settings) *string { return &s.SKUSearchURL }, func(v string) string {
			if v == stock.DefaultSKUSearchURL {
				return "Google"
			}
			return v
		}),
	textSetting("sku_search_prefix", "Search prefix",
		"Added to every part number search, to narrow it to a country or a shop, such as site:ro or site:shop.example.",
		"", func(s *stock.Settings) *string { return &s.SKUSearchPrefix }, same),
	numberSetting("expiring_days", "Best before soon", "days",
		"Items in stock this close to their best-before date get an amber Expires badge and are on the dashboard's Expiring card and in the Expiring filter. An item with nothing left has nothing to expire.",
		"", func(s *stock.Settings) *int { return &s.ExpiringDays }),
	listSetting("categories", "Categories offered",
		"Offered as you type an item's category, after the ones in use. One per line. Removing one here changes no item, and categories in use are always offered.",
		"categories", func(s *stock.Settings) *[]string { return &s.Categories }),
	numberSetting("due_soon_days", "Due soon", "days",
		"A service due by date shows Due soon this many days ahead, but never more than half the kit's months, so a monthly kit isn't soon right away.",
		"", func(s *stock.Settings) *int { return &s.DueSoonDays }),
	numberSetting("due_soon_percent", "Due soon", "%",
		"A service due by reading shows Due soon within this share of the kit's distance (10% is 1 000 {dist} before a 10 000 {dist} service), never more than half of it.",
		"", func(s *stock.Settings) *int { return &s.DueSoonPercent }),
	numberSetting("due_soon_min_distance", "…but at least", "distance",
		"The reading's Due soon never starts closer than this, so a short interval doesn't turn Due soon only at the last moment.",
		"span-2", func(s *stock.Settings) *int { return &s.DueSoonMinDistance }),
	listSetting("work_names", "Work names offered",
		"Offered for a kit's name and a service's Work performed, after the ones in use. One per line. Removing one here changes no kit or service.",
		"names", func(s *stock.Settings) *[]string { return &s.WorkNames }),
	numberSetting("warranty_soon_distance", "Ends soon", "distance",
		"The warranty shows Ends soon this close to its end reading.",
		"", func(s *stock.Settings) *int { return &s.WarrantySoonDistance }),
	numberSetting("warranty_soon_days", "Ends soon", "days",
		"The warranty shows Ends soon this many days before its end date.",
		"", func(s *stock.Settings) *int { return &s.WarrantySoonDays }),
	numberSetting("warranty_grace_percent", "Grace", "%",
		"How far past its reading a service the warranty asks for still keeps it, and how far past Break-in at a service still counts as the break-in: 10% is 100 {dist} on a break-in at 1 000 {dist}. Dealers allow about 10%; some terms state none, so 0 is the strict choice.",
		"", func(s *stock.Settings) *int { return &s.GracePercent }),
	numberSetting("warranty_grace_days", "Grace", "days",
		"How many days past its date a service the warranty asks for still keeps it: about a month, whatever the interval.",
		"", func(s *stock.Settings) *int { return &s.GraceDays }),
}

// settingByName returns the field named name.
func settingByName(name string) settingDef {
	for _, d := range settingDefs {
		if d.Name == name {
			return d
		}
	}
	panic("no setting " + name)
}

// settingField is a field as the page shows it.
type settingField struct {
	settingDef
	Value   string // what its box holds
	Default string // its default as its box would hold it (data-default)
	Short   string // the ↺'s text: the default
	Long    string // the ↺'s title: "Reset to …"
	Changed bool
	Options []settingOption
}

// TipText is the field's "?", in the distance unit in use.
func (f settingField) TipText() string {
	return strings.ReplaceAll(f.Tip, "{dist}", stock.Current().DistanceUnit)
}

// Required reports whether the field can't be left empty: every number,
// and every text but the search prefix.
func (f settingField) Required() bool {
	return f.Kind == settingNumber || (f.Kind == settingText && f.Name != "sku_search_prefix")
}

// LabelText is the label with its unit: "Due soon (days)".
func (f settingField) LabelText() string {
	switch f.Unit {
	case "":
		return f.Label
	case "distance":
		return f.Label + " (" + stock.Current().DistanceUnit + ")"
	}
	return f.Label + " (" + f.Unit + ")"
}

// settingSectionView is a section with its fields.
type settingSectionView struct {
	Title, Note string
	Fields      []settingField
	Changed     int
}

// settingsPage is the Settings page.
type settingsPage struct {
	Sections []settingSectionView
	Version  int
	FormKey  string
	Errors   stock.Errors
	Stale    *staleNote
	Back     string
	Changed  int // fields whose box differs from its default: Reset all shows
}

// sameSetting reports whether two box values are the same setting: text
// trimmed, a list by its lines (trimmed, blank ones left out).
func sameSetting(kind settingKind, a, b string) bool {
	if kind == settingList {
		return slices.Equal(listLines(a), listLines(b))
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func listLines(v string) []string {
	var out []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// newSettingsPage lays out the page with values: each box as it holds it,
// the saved settings or what was typed.
func newSettingsPage(values map[string]string) settingsPage {
	def := stock.DefaultSettings()
	p := settingsPage{Errors: stock.Errors{}}
	for _, sec := range settingSections {
		sv := settingSectionView{Title: sec.Title, Note: sec.Note}
		for _, name := range sec.Names {
			d := settingByName(name)
			short, long := d.shown(def)
			f := settingField{settingDef: d, Value: values[name], Default: d.get(def), Short: short, Long: "Reset to " + long}
			f.Changed = !sameSetting(d.Kind, f.Value, f.Default)
			if d.options != nil {
				f.Options = d.options()
			}
			if f.Changed {
				sv.Changed++
			}
			sv.Fields = append(sv.Fields, f)
		}
		p.Changed += sv.Changed
		p.Sections = append(p.Sections, sv)
	}
	return p
}

// settingValues are the boxes of saved settings.
func settingValues(set stock.Settings) map[string]string {
	v := map[string]string{}
	for _, d := range settingDefs {
		v[d.Name] = d.get(set)
	}
	return v
}

func (s *Server) settingsEdit(w http.ResponseWriter, r *http.Request) error {
	set, err := s.store.GetSettings(r.Context())
	if err != nil {
		return err
	}
	p := newSettingsPage(settingValues(set))
	p.Version, p.FormKey = set.Version, newFormKey()
	p.Back = s.back(r, "/")
	s.renderSettings(w, r, http.StatusOK, p)
	return nil
}

func (s *Server) settingsUpdate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	key := parseFormKey(r)
	settingsPath := func(int64) string { return "/settings" }
	// Saved already comes first: the first save moved the version on.
	if done, err := s.savedBefore(w, r, key, store.WhatSettings, settingsPath, "/settings"); done || err != nil {
		return err
	}
	values := map[string]string{}
	in := stock.SettingsInput{FormKey: key}
	in.Version = parseVersion(r)
	errs := stock.Errors{}
	checkFormKey(errs, key)
	for _, d := range settingDefs {
		v := r.PostFormValue(d.Name)
		if d.Kind != settingList {
			v = strings.TrimSpace(v)
		}
		values[d.Name] = v
		if msg := d.set(&in.Settings, v); msg != "" {
			errs.Add(d.Name, msg)
		}
	}
	for k, v := range in.Validate() {
		if _, bad := errs[k]; !bad {
			errs.Add(k, v)
		}
	}
	var stale *staleNote
	if !errs.Any() {
		set, err := s.store.UpdateSettings(r.Context(), in)
		if err == nil {
			stock.UseSaved(set)
			s.redirect(w, r, "/settings", "Saved settings")
			return nil
		}
		if s.savedAlready(w, r, err, settingsPath, "/settings") {
			return nil
		}
		if stale = staleForm(err, "/settings"); stale == nil && !formConflict(errs, err) {
			return err
		}
	}
	p := newSettingsPage(values)
	p.Version, p.FormKey, p.Errors, p.Stale = in.Version, key, errs, stale
	p.Back = postedBack(r, "/")
	s.renderSettings(w, r, http.StatusUnprocessableEntity, p)
	return nil
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, p settingsPage) {
	s.render(w, r, status, "settings", View{Title: "Settings", Nav: "settings", Data: p})
}
