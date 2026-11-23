package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// warrantyToday is the clock of the warranty tests: 23 Nov 2026.
func warrantyApp(t *testing.T) *testApp {
	return newAppAt(t, func() time.Time { return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC) })
}

// The vehicle form's Under warranty box shows its two fields; ticked, it
// needs one of them; unticked, it saves none, which clears a warranty.
func TestVehicleWarrantyForm(t *testing.T) {
	a := warrantyApp(t)
	expect(t, a.get("/vehicles/new"), http.StatusOK,
		`<label class="toggle"><input type="checkbox" id="has_warranty" name="has_warranty" value="1"> Under warranty</label>`,
		`name="warranty_ends_on" value=""`, `name="warranty_ends_at" value=""`)

	base := url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"}, "odometer": {"12000"}}
	with := func(extra url.Values) url.Values {
		f := url.Values{}
		for k, v := range base {
			f[k] = v
		}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}

	// Ticked with nothing: the box stays ticked, with the error.
	rec := a.post("/vehicles", with(url.Values{"has_warranty": {"1"}}))
	expect(t, rec, http.StatusUnprocessableEntity, "Enter when the warranty ends: a date, a reading or both",
		`id="has_warranty" name="has_warranty" value="1" checked>`)

	// Unticked: whatever its fields hold is dropped.
	rec = a.post("/vehicles", with(url.Values{"name": {"Golf"}, "warranty_ends_on": {"2028-03-01"}, "warranty_ends_at": {"100000"}}))
	expect(t, rec, http.StatusSeeOther)
	golf := mustID(t, rec.Header().Get("Location"))
	if v, _ := a.store.GetVehicle(context.Background(), golf); v.WarrantyEndsOn != nil || v.WarrantyEndsAt != nil {
		t.Errorf("unticked saved a warranty: %v, %v", v.WarrantyEndsOn, v.WarrantyEndsAt)
	}

	rec = a.post("/vehicles", with(url.Values{"has_warranty": {"1"}, "warranty_ends_on": {"2028-03-01"}, "warranty_ends_at": {"100 000"}}))
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := mustID(t, path)
	v, _ := a.store.GetVehicle(context.Background(), id)
	if v.WarrantyEndsOn == nil || v.WarrantyEndsOn.Format("2006-01-02") != "2028-03-01" || v.WarrantyEndsAt == nil || *v.WarrantyEndsAt != 100000 {
		t.Fatalf("warranty = %v, %v", v.WarrantyEndsOn, v.WarrantyEndsAt)
	}
	expect(t, a.get(path+"/edit"), http.StatusOK, `id="has_warranty" name="has_warranty" value="1" checked>`,
		`name="warranty_ends_on" value="2028-03-01"`, `name="warranty_ends_at" value="100000"`)

	// A field's own error keeps what was typed.
	expect(t, a.postV(path, with(url.Values{"has_warranty": {"1"}, "warranty_ends_on": {"2028-03-01"}, "warranty_ends_at": {"0"}})),
		http.StatusUnprocessableEntity, "Warranty reading must be more than 0", `name="warranty_ends_on" value="2028-03-01"`)

	// Unticking on the edit form clears it.
	expect(t, a.postV(path, with(url.Values{"warranty_ends_on": {"2028-03-01"}, "warranty_ends_at": {"100000"}})), http.StatusSeeOther)
	if v, _ := a.store.GetVehicle(context.Background(), id); v.WarrantyEndsOn != nil || v.WarrantyEndsAt != nil {
		t.Errorf("unticking kept the warranty: %v, %v", v.WarrantyEndsOn, v.WarrantyEndsAt)
	}
	edit := a.get(path + "/edit")
	expect(t, edit, http.StatusOK, `id="has_warranty" name="has_warranty" value="1"> Under warranty`)
}

// A vehicle bought used can have had its warranty end before it was
// bought, and past its purchase reading: the warranty is the vehicle's.
func TestVehicleWarrantyBoughtUsed(t *testing.T) {
	a := warrantyApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"odometer": {"27102"}, "purchased_on": {"2024-07-04"}, "odometer_at_purchase": {"2148"},
		"has_warranty": {"1"}, "warranty_ends_on": {"2023-11-11"}, "warranty_ends_at": {"2000"}})
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.get(rec.Header().Get("Location")), http.StatusOK, "Warranty ended</span>", "<dt>Warranty ended</dt><dd>11 Nov 2023 · 2\u202f000 km</dd>")
}

// A stale edit form comes back with the warranty as it was typed.
func TestVehicleWarrantyStaleForm(t *testing.T) {
	a := warrantyApp(t)
	k390, _ := a.seed()
	path := "/vehicles/" + itoa(k390)
	mine := openForm(t, a, path+"/edit", path)
	expect(t, a.postV(path, url.Values{"name": {"KTM 390"}, "kind": {"motorcycle"}, "color": {"#f26b0f"}, "plate": {"B-1"}}), http.StatusSeeOther)
	expect(t, submit(a, path, mine, url.Values{"name": {"KTM 390"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"has_warranty": {"1"}, "warranty_ends_on": {"2028-03-01"}, "warranty_ends_at": {"100000"}}),
		http.StatusUnprocessableEntity, "changed since you opened it",
		`id="has_warranty" name="has_warranty" value="1" checked>`, `name="warranty_ends_on" value="2028-03-01"`, `name="warranty_ends_at" value="100000"`)
}

// Each state shows where it was designed to: the vehicle page's line (the
// badge) and facts, the vehicle's card and the dashboard's row (its words,
// without the badge's colour) and the kit page (while it runs).
func TestVehicleWarrantyShows(t *testing.T) {
	tests := []struct {
		name, endsOn string
		bar          string   // the Working on bar's words after the name
		page, card   []string // on the vehicle page, on its card
		dash, kit    []string
		not          []string // on none of the four
	}{
		{name: "runs", endsOn: "2028-03-01", bar: "· Under warranty",
			page: []string{`<span class="badge badge-ok"><svg class="icon" aria-hidden="true"><use href="#i-shield-check"></use></svg> Under warranty</span>`, "Warranty ends", "1 Mar 2028 · 100 000 km"},
			card: []string{`<use href="#i-shield-check"></use></svg> Under warranty</span>`},
			dash: []string{"· Under warranty</span>"},
			kit:  []string{"<dt>Warranty ends</dt><dd>1 Mar 2028 · 100 000 km</dd>"},
			not:  []string{"Warranty ends soon"}},
		{name: "ends soon", endsOn: "2026-12-15", bar: "· Warranty ends soon",
			page: []string{`<span class="badge badge-warn"><svg class="icon" aria-hidden="true"><use href="#i-shield-alert"></use></svg> Warranty ends soon</span>`},
			card: []string{`<use href="#i-shield-alert"></use></svg> Warranty ends soon</span>`},
			dash: []string{"· Warranty ends soon</span>"},
			kit:  []string{"<dt>Warranty ends</dt><dd>15 Dec 2026 · 100 000 km <span class=\"badge badge-warn\">"}},
		{name: "ended", endsOn: "2026-11-01", bar: "· Warranty ended",
			page: []string{`<span class="badge badge-muted"><svg class="icon" aria-hidden="true"><use href="#i-shield"></use></svg> Warranty ended</span>`, "<dt>Warranty ended</dt><dd>1 Nov 2026 · 100\u202f000 km</dd>"},
			card: []string{`<use href="#i-shield"></use></svg> Warranty ended</span>`},
			dash: []string{"· Warranty ended</span>"},
			not:  []string{"Under warranty</span>", "Warranty ends soon", "<dt>Warranty ends</dt><dd>1 Nov"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := warrantyApp(t)
			rec := a.post("/vehicles", url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
				"make": {"KTM"}, "model": {"390 Adventure"}, "year": {"2021"}, "odometer": {"12000"},
				"has_warranty": {"1"}, "warranty_ends_on": {tt.endsOn}, "warranty_ends_at": {"100000"}})
			expect(t, rec, http.StatusSeeOther)
			path := rec.Header().Get("Location")
			rec = a.post("/kits", url.Values{"vehicle_id": {itoa(mustID(t, path))}, "name": {"Oil Service"}, "interval_distance": {"7500"}, "interval_months": {"12"}})
			expect(t, rec, http.StatusSeeOther)
			kit := rec.Header().Get("Location")

			page := a.get(path)
			// The line: badges, then the subtitle; the kind is the tile's icon.
			expect(t, page, http.StatusOK, append(tt.page, "<span>KTM 390 Adventure (2021)</span>")...)
			expectNot(t, page, "· Motorcycle")
			expect(t, a.get("/vehicles"), http.StatusOK, tt.card...)
			expect(t, a.get("/"), http.StatusOK, tt.dash...)
			expect(t, a.get(kit), http.StatusOK, tt.kit...)
			for _, p := range []string{path, "/vehicles", "/", kit} {
				expectNot(t, a.get(p), tt.not...)
			}
			// While it is worked on (the bar needs a second vehicle), the bar
			// says it on every page, its own too, muted.
			expect(t, a.post("/vehicles", url.Values{"name": {"KillJoy"}, "kind": {"car"}, "color": {"#2563eb"}}), http.StatusSeeOther)
			id := itoa(mustID(t, path))
			for _, p := range []string{"/trash", path} {
				expect(t, a.getWorking(p, id), http.StatusOK, `Working on <strong>LittleJoy</strong> <span class="working-meta">`+tt.bar+"</span>")
			}
		})
	}
}

// A field typed but not understood has its own error, and the box doesn't
// also ask for an end.
func TestVehicleWarrantyUnreadableField(t *testing.T) {
	a := warrantyApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"has_warranty": {"1"}, "warranty_ends_at": {"abc"}})
	expect(t, rec, http.StatusUnprocessableEntity, `name="warranty_ends_at" value="abc"`)
	expectNot(t, rec, "Enter when the warranty ends")
}

// An archived vehicle shows no warranty: no badge on its line (Archived
// instead), no stat on its card, nothing on its kits.
func TestVehicleWarrantyArchived(t *testing.T) {
	a := warrantyApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"}, "odometer": {"12000"},
		"has_warranty": {"1"}, "warranty_ends_on": {"2028-03-01"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"7500"}, "interval_months": {"12"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	expect(t, a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {itoa(mustID(t, kit))}, "title": {"Oil Service"},
		"performed_on": {"2026-11-01"}, "odometer": {"12000"}}), http.StatusSeeOther)
	expect(t, a.postV(path+"/archive", nil), http.StatusSeeOther)
	page := a.get(path)
	expect(t, page, http.StatusOK, "Archived", "<dt>Warranty ends</dt>") // the facts still say it
	expectNot(t, page, "Under warranty</span>")
	expectNot(t, a.get("/vehicles?archived=1"), "Under warranty")
	expectNot(t, a.get(kit), "<dt>Warranty ends</dt>")
}

// The warranty decides how an intermediate service counts: while it runs, a
// late one doesn't move the full service; once it has ended, the full one
// comes one intermediate step after it.
func TestWarrantyDecidesKitSchedule(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	vehicle := url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#dc2626"}, "odometer": {"9000"},
		"purchased_on": {"2026-01-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2028-04-03"}}
	rec := a.post("/vehicles", vehicle)
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"15000"}, "interval_months": {"12"},
		"has_inter": {"1"}, "inter_interval_distance": {"7500"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	// A late intermediate service, at 8 000 km instead of 7 500.
	expect(t, a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {itoa(mustID(t, kit))}, "kit_intermediate": {"1"},
		"title": {"Oil Service (Intermediate)"}, "performed_on": {"2026-08-01"}, "odometer": {"8000"}}), http.StatusSeeOther)

	// Under warranty: the full service is still due at 15 000 km or a year after the purchase.
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>15\u202f000 km</dd>", "<dt>Due by</dt><dd>1 Jan 2027</dd>")

	// The warranty ended on 1 Nov 2026: the full service comes one intermediate step after it.
	vehicle.Set("warranty_ends_on", "2026-11-01")
	expect(t, a.postV(path, vehicle), http.StatusSeeOther)
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>15\u202f500 km</dd>", "<dt>Due by</dt><dd>1 Jan 2027</dd>")

	// No warranty at all: the same.
	vehicle.Del("has_warranty")
	expect(t, a.postV(path, vehicle), http.StatusSeeOther)
	expect(t, a.get(kit), http.StatusOK, "<dt>Due at</dt><dd>15\u202f500 km</dd>")
}

// Delete and Undo bring the warranty back with the vehicle.
func TestVehicleWarrantyRestored(t *testing.T) {
	a := warrantyApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"has_warranty": {"1"}, "warranty_ends_on": {"2028-03-01"}, "warranty_ends_at": {"100000"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	undo := flashOf(t, saveFresh(t, a, path, path+"/delete", nil)).Undo
	if undo == 0 {
		t.Fatal("no Undo")
	}
	expect(t, a.post("/trash/"+itoa(undo)+"/restore", nil), http.StatusSeeOther)
	v, err := a.store.GetVehicle(context.Background(), mustID(t, path))
	if err != nil || v.WarrantyEndsOn == nil || v.WarrantyEndsOn.Format("2006-01-02") != "2028-03-01" || v.WarrantyEndsAt == nil || *v.WarrantyEndsAt != 100000 {
		t.Errorf("restored warranty = %v, %v (%v)", v.WarrantyEndsOn, v.WarrantyEndsAt, err)
	}
}
