package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

func TestVehicleFlow(t *testing.T) {
	a := newApp(t)
	expect(t, a.post("/vehicles", url.Values{"name": {""}, "color": {"nope"}}), http.StatusUnprocessableEntity, "Name is required")
	rec := a.post("/vehicles", url.Values{"name": {"Golf"}, "kind": {"car"}, "color": {"#2563eb"}, "year": {"2015"}, "odometer": {"150 000"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	expect(t, a.get(path), http.StatusOK, "Golf", "2015", `value="150000"`)
	expect(t, a.post("/vehicles", url.Values{"name": {"golf"}}), http.StatusUnprocessableEntity, "already exists")
	expect(t, a.post(path+"/odometer", url.Values{"odometer": {"151000"}}), http.StatusSeeOther)
	expect(t, a.postV(path, url.Values{"name": {"VW Golf"}, "kind": {"car"}, "color": {"#2563eb"}}), http.StatusSeeOther)
	expect(t, a.get("/vehicles"), http.StatusOK, "VW Golf")
	expect(t, a.postV(path+"/delete", nil), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusNotFound)
}

func TestArchiveVehicleFlow(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	path := "/vehicles/" + itoa(k390)
	expect(t, a.get(path), http.StatusOK, `action="`+path+`/delete"`)
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain"}, "performed_on": {"2026-10-29"},
		"odometer": {"12000"}}), http.StatusSeeOther)
	page := a.get(path)
	expect(t, page, http.StatusOK, `action="`+path+`/archive"`)
	expectNot(t, page, `action="`+path+`/delete"`)

	expect(t, a.postV(path+"/archive", nil), http.StatusSeeOther)
	page = a.get(path)
	expect(t, page, http.StatusOK, "Archived", `action="`+path+`/restore"`, "Restore this vehicle to log services or add kits")
	// Nothing new for it: no service, kit or item buttons, no odometer form.
	expectNot(t, page, `href="/services/new?vehicle=`, `href="/kits/new?vehicle=`, `href="/items/new?vehicle=`, `action="`+path+`/odometer"`)
	expectNot(t, a.get("/vehicles"), `href="`+path+`"`)
	expect(t, a.get("/vehicles"), http.StatusOK, "1 archived vehicle")
	expect(t, a.get("/vehicles?archived=1"), http.StatusOK, `href="`+path+`"`, "Active vehicles", `<span class="badge badge-muted" title="Archived `, `<svg class="icon" aria-hidden="true"><use href="#i-archive"></use></svg> Archived</span>`)
	// Pickers leave it out; items tagged with it keep the tag when saved.
	form := a.get("/services/new")
	expect(t, form, http.StatusOK, `<span class="dot"></span>KTM 1290</label>`)
	expectNot(t, form, `<span class="dot"></span>KTM 390</label>`)
	pads := itemID(t, a.store, "Rear Brake")
	expect(t, a.get("/items/"+itoa(pads)+"/edit"), http.StatusOK, `name="vehicle" value="`+itoa(k390)+`" checked`)

	// The odometer can't go below the service's reading.
	rec := a.post(path+"/odometer", url.Values{"odometer": {"11000"}})
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "12\u202f000 km") {
		t.Errorf("flash = %+v", f)
	}
	expect(t, a.postV(path+"/restore", nil), http.StatusSeeOther)
	expect(t, a.get("/vehicles"), http.StatusOK, `href="`+path+`"`)
}

// The edit form has no odometer (Add vehicle has, then the vehicle's page
// and logged services change it): a new reading, and a service that moves
// it on, while the form is open, neither makes the form stale nor is
// undone by its save, and a posted odometer changes nothing.
func TestVehicleEditFormHasNoOdometer(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	path := "/vehicles/" + itoa(k390)
	edit := a.get(path + "/edit")
	expect(t, edit, http.StatusOK, "<h2>Purchase</h2>", `name="purchased_on"`, `name="odometer_at_purchase"`)
	expectNot(t, edit, `name="odometer"`, `odometer_was`)
	version := formVersion(t, edit.Body.String(), path)
	expect(t, a.post(path+"/odometer", url.Values{"odometer": {"12000"}}), http.StatusSeeOther)
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain"}, "performed_on": {"2026-10-29"},
		"odometer": {"12500"}}), http.StatusSeeOther)
	expect(t, a.post(path, url.Values{"name": {"KTM 390"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"odometer": {"10000"}, "plate": {"B-123"}, "version": {version}}), http.StatusSeeOther)
	if v, _ := a.store.GetVehicle(context.Background(), k390); v.Odometer != 12500 || v.Plate != "B-123" {
		t.Errorf("vehicle = %+v", v)
	}
	// Back with an error, or stale, it still has none.
	rec := a.postV(path, url.Values{"name": {"KTM 390"}, "kind": {"motorcycle"}, "purchased_on": {""}})
	expect(t, rec, http.StatusUnprocessableEntity, "Purchase date is required", "<h2>Purchase</h2>")
	expectNot(t, rec, `name="odometer"`)
	rec = a.post(path, url.Values{"name": {"KTM 390"}, "kind": {"motorcycle"}, "version": {version}})
	expect(t, rec, http.StatusUnprocessableEntity, "changed since you opened it", "<h2>Purchase</h2>")
	expectNot(t, rec, `name="odometer"`)
	// Archived vehicles still show their kits.
	expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"}}), http.StatusSeeOther)
	expect(t, a.postV(path+"/archive", nil), http.StatusSeeOther)
	expect(t, a.get("/vehicles?archived=1"), http.StatusOK, "<strong>1</strong> kit")
}

// What is only shown (make, model, year, VIN, plate) is optional: a
// vehicle saves without, and its page shows a dash for each. What the
// schedules count from is required.
func TestVehicleDisplayFieldsAreOptional(t *testing.T) {
	a := newApp(t)
	form := a.get("/vehicles/new")
	expect(t, form, http.StatusOK, `<label for="make">Make</label>`, `<label for="vin">VIN</label>`,
		`<label for="odometer">Odometer (km)</label><span class="req"`, `<label for="purchased_on">Purchased on</label><span class="req"`)
	expect(t, a.post("/vehicles", url.Values{"name": {"Pedal bike"}, "kind": {"other"}, "odometer": {""}, "purchased_on": {""}, "bought": {""}}),
		http.StatusUnprocessableEntity, "Odometer is required", "Purchase date is required", "Choose New or Used or demo")
	// Bought used, what it was at and when it was first registered too.
	expect(t, a.post("/vehicles", url.Values{"name": {"Pedal bike"}, "kind": {"other"}, "bought": {"used"},
		"odometer_at_purchase": {""}, "first_registered_on": {""}}),
		http.StatusUnprocessableEntity, "Odometer at purchase is required", "First registered on is required")
	rec := a.post("/vehicles", url.Values{"name": {"Pedal bike"}, "kind": {"other"}})
	expect(t, rec, http.StatusSeeOther)
	page := a.get(rec.Header().Get("Location"))
	expect(t, page, http.StatusOK, "<dt>Make</dt><dd>—</dd>", "<dt>Model</dt><dd>—</dd>", "<dt>Year</dt><dd>—</dd>")
}

// A vehicle at 0 km (a pedal bike, or one bought new) shows its reading
// as 0 km wherever a reading shows, never as nothing.
func TestZeroKilometres(t *testing.T) {
	a := newApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"Pedal bike"}, "kind": {"other"}, "odometer": {"0"}})
	expect(t, rec, http.StatusSeeOther)
	id := itoa(mustID(t, rec.Header().Get("Location")))
	expect(t, a.get("/vehicles"), http.StatusOK, "<strong>0</strong><span>km</span>")
	expect(t, a.get("/"), http.StatusOK, "0 items · 0 km")
	expect(t, a.get("/services/new?vehicle="+id), http.StatusOK, `placeholder="Last reading 0 km"`, `data-odometer="0 km"`)
	rec = a.post("/services", url.Values{"vehicle_id": {id}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"}, "odometer": {"0"}})
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.get(rec.Header().Get("Location")), http.StatusOK, "<dt>Odometer</dt><dd>0 km</dd>")
	expect(t, a.get("/services"), http.StatusOK, "29 Oct 2026 · 0 km")
}

func TestVehicleMakeModelSuggestions(t *testing.T) {
	a := newApp(t)
	// Fresh app has empty makes and models datalists.
	newForm := a.get("/vehicles/new")
	expect(t, newForm, http.StatusOK, `<datalist id="makes"></datalist>`, `<datalist id="models"></datalist>`)

	// Creating a vehicle adds its make and model to suggestions.
	rec := a.post("/vehicles", url.Values{
		"name":  {"LittleJoy"},
		"kind":  {"motorcycle"},
		"make":  {"KTM"},
		"model": {"390 Adventure"},
		"color": {"#f26b0f"},
	})
	expect(t, rec, http.StatusSeeOther)

	after := a.get("/vehicles/new")
	expect(t, after, http.StatusOK,
		`<datalist id="makes"><option value="KTM"></datalist>`,
		`<datalist id="models"><option value="390 Adventure"></datalist>`,
	)
}

// The distance unit setting relabels every distance and its form labels;
// the numbers are kept as typed.
func TestDistanceUnit(t *testing.T) {
	useSettings(t, func(s *stock.Settings) { s.DistanceUnit = "mi" })
	a := newApp(t)
	expect(t, a.get("/vehicles/new"), http.StatusOK, "Odometer (mi)", "Odometer at purchase (mi)", "<h2>Odometer</h2>")
	rec := a.post("/vehicles", url.Values{
		"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#2563eb"}, "odometer": {"12000"},
	})
	expect(t, rec, http.StatusSeeOther)
	vPath := rec.Header().Get("Location")
	expect(t, a.get(vPath), http.StatusOK, `aria-label="Odometer in mi"`)
	list := a.get("/vehicles")
	// A card shows the figure, then its unit under it.
	expect(t, list, http.StatusOK, "12\u202f000</strong><span>mi</span>")
	expectNot(t, list, "<span>km</span>")
	expect(t, a.get("/kits/new?vehicle="+itoa(mustID(t, vPath))), http.StatusOK,
		"Distance (mi)")
	expect(t, a.get("/services/new"), http.StatusOK, "Odometer (mi)")
}

func TestVehiclePurchaseBaselineFlow(t *testing.T) {
	a := newApp(t)
	// Create vehicle with purchase baseline
	rec := a.post("/vehicles", url.Values{
		"name":                 {"GreyJoy"},
		"kind":                 {"motorcycle"},
		"color":                {"#2563eb"},
		"odometer":             {"500"},
		"purchased_on":         {"2026-03-01"},
		"odometer_at_purchase": {"0"},
	})
	expect(t, rec, http.StatusSeeOther)
	vPath := rec.Header().Get("Location")

	// Vehicle show page renders purchase details
	show := a.get(vPath)
	expect(t, show, http.StatusOK, "GreyJoy", "<dt>Purchased</dt><dd>1 Mar 2026 · new</dd>")
	expectNot(t, show, "<dt>First registered</dt>")

	// Edit form retains purchase values
	edit := a.get(vPath + "/edit")
	expect(t, edit, http.StatusOK, `name="purchased_on" value="2026-03-01"`, `id="bought-new" name="bought" value="new" checked`)

	// Create a kit with interval
	vID := mustID(t, vPath)
	rec = a.post("/kits", url.Values{
		"vehicle_id":        {itoa(vID)},
		"name":              {"Valve check"},
		"interval_distance": {"15000"},
	})
	expect(t, rec, http.StatusSeeOther)
	kitPath := rec.Header().Get("Location")

	// Services list shows "On track" with the remaining distance
	services := a.get("/services")
	expect(t, services, http.StatusOK, "Valve check", "On track", "in 14\u202f500 km")
	expectNot(t, services, "Valve check.*last")

	// Kit detail page shows Never under Last done, but accurate Due at and Status
	kitShow := a.get(kitPath)
	expect(t, kitShow, http.StatusOK, "Never", "15\u202f000 km", "in 14\u202f500 km")

	// Dashboard includes the kit in upcoming
	dash := a.get("/")
	expect(t, dash, http.StatusOK, "Valve check", "On track")
}
