package web

import (
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// keepSettings puts back the settings in use when the test ends: a save on
// the Settings page puts its own in use (stock.Use).
func keepSettings(t *testing.T) {
	t.Helper()
	old := stock.Current()
	t.Cleanup(func() { stock.Use(old) })
}

// settingsForm is the Settings page's form holding set, as its fields
// name them.
func settingsForm(set stock.Settings) url.Values {
	f := url.Values{}
	for name, v := range settingValues(set) {
		f.Set(name, v)
	}
	return f
}

// saveSettings opens the Settings page, as another tab would, and saves
// the defaults as edit changes them.
func saveSettings(t *testing.T, a *testApp, edit func(*stock.Settings)) {
	t.Helper()
	set := stock.DefaultSettings()
	edit(&set)
	rec := submit(a, "/settings", openForm(t, a, "/settings", "/settings"), settingsForm(set))
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind == "error" || rec.Header().Get("Location") != "/settings" {
		t.Fatalf("saving the settings: %+v, to %s", f, rec.Header().Get("Location"))
	}
}

// Every column of the settings table is a field of the Settings page, in
// the table's order, with its "?" and its default for the changed marker:
// a setting added without its field (AGENTS.md, "Preferences and
// thresholds live in settings") fails here.
func TestSettingsComplete(t *testing.T) {
	var names []string
	for _, sec := range settingSections {
		names = append(names, sec.Names...)
	}
	if !slices.Equal(names, store.SettingsColumns) {
		t.Errorf("the page's fields =\n%q\nwant the table's columns, in order\n%q", names, store.SettingsColumns)
	}
	if len(settingDefs) != len(store.SettingsColumns) {
		t.Errorf("%d setting fields, want one a column (%d)", len(settingDefs), len(store.SettingsColumns))
	}
	a := newApp(t)
	rec := a.get("/settings")
	expect(t, rec, http.StatusOK, "<h1>Settings</h1>", "Every setting is at its default", "Save settings")
	body := rec.Body.String()
	for _, d := range settingDefs {
		field := regexp.MustCompile(`(?s)<div class="field[^"]*" data-setting data-kind="` + string(d.Kind) +
			`" data-default="[^"]*">.*?name="` + d.Name + `"`).FindString(body)
		if field == "" {
			t.Errorf("%s: no field with its kind and default", d.Name)
			continue
		}
		if !strings.Contains(field, `id="`+d.Name+`-tip"`) || d.Tip == "" || !strings.HasSuffix(d.Tip, ".") {
			t.Errorf("%s: no \"?\", or its text isn't a sentence", d.Name)
		}
	}
	// At the defaults: nothing marked, and Reset all hidden. It asks
	// nothing: it only fills the boxes (app.js), for Save settings.
	expect(t, rec, http.StatusOK, `<button class="btn" type="button" data-reset-all hidden>`)
	expectNot(t, rec, `class="field changed`, ` changed" data-setting`, `show-modal`)
	if strings.Count(body, `data-changed-count hidden`) != len(settingSections)-1 {
		t.Errorf("section counts not all hidden")
	}
}

// The gear beside Appearance leads to Settings, and the main menu doesn't.
func TestSettingsInTheShell(t *testing.T) {
	a := newApp(t)
	expect(t, a.get("/"), http.StatusOK, `<a class="icon-btn appearance-btn" href="/settings" title="Settings (,)" aria-label="Settings" aria-keyshortcuts=",">`)
	rec := a.get("/settings")
	expect(t, rec, http.StatusOK, `href="/settings" title="Settings (,)" aria-label="Settings" aria-keyshortcuts="," aria-current="page"`,
		`popovertarget="more" class="current" aria-current="page"`)
	expectNot(t, rec, `<nav class="nav" aria-label="Main">`+"\n"+`      <a href="/settings"`)
}

// Saved settings show on every page at once; the Settings page marks what
// differs from its default, with the default on its ↺, and shows Reset
// all; the defaults saved (what Reset all and Save settings do) put them
// all back.
func TestSaveSettings(t *testing.T) {
	keepSettings(t)
	a := newApp(t)
	saveSettings(t, a, func(s *stock.Settings) {
		s.Currency, s.NumberFormat, s.DateFormat = "LEI", stock.NumberDotComma, "02.01.2006"
		s.SKUSearchPrefix = "site:ro"
		s.Categories = []string{"Lubricants", "Fasteners"}
	})
	if got := stock.Current(); got.Currency != "LEI" || got.Version != 2 {
		t.Errorf("in use: %+v", got)
	}

	rec := a.get("/settings")
	expect(t, rec, http.StatusOK, `value="LEI"`, `title="Reset to EUR"`, `title="Reset to none"`,
		`title="Reset to the 14 built-in categories"`, `title="Reset to 1`+" "+`500.50"`,
		"5 settings differ from their defaults", `data-changed-count>2 changed</span>`,
		`<option value="dot_comma" selected>1.500,50</option>`, `<option value="space_dot">1`+" "+`500.50 (default)</option>`,
		`<button class="btn" type="button" data-reset-all>`, `<input type="hidden" name="version" value="2">`)
	if n := strings.Count(rec.Body.String(), ` changed" data-setting`); n != 5 {
		t.Errorf("%d fields marked changed, want 5", n)
	}

	// An item with a part number and a purchase.
	rec = a.post("/items", url.Values{"name": {"Chain lube"}, "unit": {"piece"}, "quantity": {"0"},
		"sku_code": {"CL1"}, "sku_kind": {"manufacturer"}, "sku_brand": {""}, "sku_pack": {""}, "sku_pack_unit": {"pcs"}})
	expect(t, rec, http.StatusSeeOther)
	item := rec.Header().Get("Location")
	expect(t, a.post(item+"/movements", url.Values{"kind": {"bought"}, "amount": {"1500"}, "total_price": {"2469"},
		"occurred_on": {"2026-10-20"}}), http.StatusSeeOther)
	expect(t, a.get(item), http.StatusOK, "1,65 LEI", "20.10.2026", "1.500 pcs", "q=CL1&#43;site%3Aro")
	expect(t, a.get("/items/new"), http.StatusOK, `<option value="Lubricants">`)
	expectNot(t, a.get("/items/new"), `<option value="Filters">`)

	// Reset all, then Save settings: the defaults' boxes saved.
	saveSettings(t, a, func(*stock.Settings) {})
	got := stock.Current()
	want := stock.DefaultSettings()
	want.Version = 3
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after Reset all: %+v", got)
	}
	expect(t, a.get("/settings"), http.StatusOK, "Every setting is at its default")
}

// A form with mistakes comes back with what was typed and the errors under
// the boxes; a stale one says so; a repeat says it was saved already.
func TestSettingsFormErrors(t *testing.T) {
	keepSettings(t)
	a := newApp(t)
	form := settingsForm(stock.DefaultSettings())
	form.Set("sku_search_url", "https://www.google.com/search")
	form.Set("activity_page_size", "lots")
	form.Set("currency", "Romanian lei")
	rec := submit(a, "/settings", "1", form)
	expect(t, rec, http.StatusUnprocessableEntity, "Mark where the part number goes with {q}", "Enter a whole number",
		"At most 10 characters", `value="https://www.google.com/search"`, `value="lots"`, `value="Romanian lei"`,
		`id="sku_search_url-err"`)
	if stock.Current().Currency != "EUR" {
		t.Error("a refused form changed the settings in use")
	}

	// Stale: saved in another tab since this one was opened.
	opened := openForm(t, a, "/settings", "/settings")
	saveSettings(t, a, func(s *stock.Settings) { s.Currency = "LEI" })
	form = settingsForm(stock.DefaultSettings())
	form.Set("currency", "USD")
	rec = submit(a, "/settings", opened, form)
	expect(t, rec, http.StatusUnprocessableEntity, "The settings were changed since you opened them", `value="USD"`)
	if stock.Current().Currency != "LEI" {
		t.Error("a stale form changed the settings")
	}

	// The same form twice: saved once.
	key := formKey(t, a, "/settings", "/settings")
	form.Set("currency", "RON")
	first := a.post("/settings", with(form, key, "2"))
	expect(t, first, http.StatusSeeOther)
	again := a.post("/settings", with(form, key, "2"))
	expect(t, again, http.StatusSeeOther)
	if f := flashOf(t, again); !strings.Contains(f.Message, "saved already") {
		t.Errorf("a repeat: %+v, want saved already", f)
	}
	if v := stock.Current().Version; v != 3 {
		t.Errorf("version %d after a repeat, want 3", v)
	}
	// Without a key: refused.
	form.Set("form_key", "")
	expect(t, submit(a, "/settings", "3", form), http.StatusUnprocessableEntity)
}

// The grace settings decide the warranty over HTTP: a break-in at 1 150 km
// is on time with a 20% grace, and loses the warranty at 10%.
func TestGraceSettingOverHTTP(t *testing.T) {
	keepSettings(t)
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"3000"},
		"purchased_on": {"2026-01-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-12-31"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"10000"}, "interval_months": {"12"},
		"break_in_at": {"1000"}, "warranty_required": {"1"}})
	expect(t, rec, http.StatusSeeOther)
	kitID := itoa(mustID(t, rec.Header().Get("Location")))
	service := url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {"break_in"}, "title": {"Oil Service (Break-in)"},
		"performed_on": {"2026-03-01"}, "odometer": {"1150"}}
	expect(t, a.post("/services", service), http.StatusUnprocessableEntity, "too late for it")

	saveSettings(t, a, func(s *stock.Settings) { s.GracePercent = 20 })
	expect(t, a.post("/services", service), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Under warranty")

	saveSettings(t, a, func(s *stock.Settings) {})
	expect(t, a.get(path), http.StatusOK, "Warranty lost", "Was due by 1\u202f000 km")
}
