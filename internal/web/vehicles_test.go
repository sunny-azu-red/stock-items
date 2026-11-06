package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestVehicleFlow(t *testing.T) {
	a := newApp(t)
	expect(t, a.post("/vehicles", url.Values{"name": {""}, "color": {"nope"}}), http.StatusUnprocessableEntity, "Name is required")
	rec := a.post("/vehicles", url.Values{"name": {"Golf"}, "kind": {"car"}, "color": {"#2563eb"}, "year": {"2015"}, "odometer_km": {"150 000"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	expect(t, a.get(path), http.StatusOK, "Golf", "2015", `value="150000"`)
	expect(t, a.post("/vehicles", url.Values{"name": {"golf"}}), http.StatusUnprocessableEntity, "already exists")
	expect(t, a.post(path+"/odometer", url.Values{"odometer_km": {"151000"}}), http.StatusSeeOther)
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
		"odometer_km": {"12000"}}), http.StatusSeeOther)
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
	expect(t, form, http.StatusOK, ">KTM 1290</option>")
	expectNot(t, form, ">KTM 390</option>")
	pads := itemID(t, a.store, "Rear Brake")
	expect(t, a.get("/items/"+itoa(pads)+"/edit"), http.StatusOK, `name="vehicle" value="`+itoa(k390)+`" checked`)

	// The odometer can't go below the service's reading.
	rec := a.post(path+"/odometer", url.Values{"odometer_km": {"11000"}})
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "12\u202f000 km") {
		t.Errorf("flash = %+v", f)
	}
	expect(t, a.postV(path+"/restore", nil), http.StatusSeeOther)
	expect(t, a.get("/vehicles"), http.StatusOK, `href="`+path+`"`)
}

func TestVehicleEditFormKeepsANewerOdometer(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	path := "/vehicles/" + itoa(k390)
	edit := a.get(path + "/edit")
	expect(t, edit, http.StatusOK, `name="odometer_was" value="10000"`)
	version := formVersion(t, edit.Body.String(), path)
	// A new reading, and a service that moves it on, while the form is
	// open: neither makes the form stale.
	expect(t, a.post(path+"/odometer", url.Values{"odometer_km": {"12000"}}), http.StatusSeeOther)
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain"}, "performed_on": {"2026-10-29"},
		"odometer_km": {"12500"}}), http.StatusSeeOther)
	expect(t, a.post(path, url.Values{"name": {"KTM 390"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"odometer_km": {"10000"}, "odometer_was": {"10000"}, "plate": {"B-123"}, "version": {version}}), http.StatusSeeOther)
	if v, _ := a.store.GetVehicle(context.Background(), k390); *v.OdometerKm != 12500 || v.Plate != "B-123" {
		t.Errorf("vehicle = %+v", v)
	}
	// Archived vehicles still show their kits.
	expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"}}), http.StatusSeeOther)
	expect(t, a.postV(path+"/archive", nil), http.StatusSeeOther)
	expect(t, a.get("/vehicles?archived=1"), http.StatusOK, "<strong>1</strong> kit")
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

func TestVehiclePurchaseBaselineFlow(t *testing.T) {
	a := newApp(t)
	// Create vehicle with purchase baseline
	rec := a.post("/vehicles", url.Values{
		"name":         {"GreyJoy"},
		"kind":         {"motorcycle"},
		"color":        {"#2563eb"},
		"odometer_km":  {"500"},
		"purchased_on": {"2026-03-01"},
		"purchased_km": {"0"},
	})
	expect(t, rec, http.StatusSeeOther)
	vPath := rec.Header().Get("Location")

	// Vehicle show page renders purchase details
	show := a.get(vPath)
	expect(t, show, http.StatusOK, "GreyJoy", "Purchased", "1 Mar 2026", "0 km")

	// Edit form retains purchase values
	edit := a.get(vPath + "/edit")
	expect(t, edit, http.StatusOK, `name="purchased_on" value="2026-03-01"`, `name="purchased_km" value="0"`)

	// Create a kit with interval
	vID := mustID(t, vPath)
	rec = a.post("/kits", url.Values{
		"vehicle_id":  {itoa(vID)},
		"name":        {"Valve check"},
		"interval_km": {"15000"},
	})
	expect(t, rec, http.StatusSeeOther)
	kitPath := rec.Header().Get("Location")

	// Services list shows "On track" with remaining km, not "Not done yet"
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
