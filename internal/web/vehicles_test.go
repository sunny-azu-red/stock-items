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
	expect(t, a.get("/vehicles?archived=1"), http.StatusOK, `href="`+path+`"`)
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
