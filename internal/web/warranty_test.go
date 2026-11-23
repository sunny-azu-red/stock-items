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

// Each state shows where it was designed to: the vehicle page's line and
// facts, the vehicle's card, the dashboard (only when it ends soon) and the
// kit page (while it runs).
func TestVehicleWarrantyShows(t *testing.T) {
	tests := []struct {
		name, endsOn string
		page, card   []string // on the vehicle page, on its card
		dash, kit    []string
		not          []string // on none of the four
	}{
		{name: "runs", endsOn: "2028-03-01",
			page: []string{`<span class="badge badge-ok"><svg class="icon" aria-hidden="true"><use href="#i-shield-check"></use></svg> Under warranty</span>`, "Warranty ends", "1 Mar 2028 · 100 000 km"},
			card: []string{`<use href="#i-shield-check"></use></svg> Under warranty</span>`},
			kit:  []string{"<dt>Warranty ends</dt><dd>1 Mar 2028 · 100 000 km</dd>"},
			not:  []string{"Warranty ends soon"}},
		{name: "ends soon", endsOn: "2026-12-15",
			page: []string{`<span class="badge badge-warn"><svg class="icon" aria-hidden="true"><use href="#i-shield-alert"></use></svg> Warranty ends soon</span>`},
			card: []string{`<use href="#i-shield-alert"></use></svg> Warranty ends soon</span>`},
			dash: []string{`<span class="badge-line"><span class="badge badge-warn">`, "· warranty ends in 22 days"},
			kit:  []string{"<dt>Warranty ends</dt><dd>15 Dec 2026 · 100 000 km <span class=\"badge badge-warn\">"}},
		{name: "ended", endsOn: "2026-11-01",
			page: []string{`<span class="badge badge-muted"><svg class="icon" aria-hidden="true"><use href="#i-shield"></use></svg> Warranty ended</span>`, "<dt>Warranty ended</dt><dd>1 Nov 2026 · 100\u202f000 km</dd>"},
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
			if tt.dash == nil {
				expectNot(t, a.get("/"), `<span class="badge-line">`, "warranty ends in")
			}
			for _, p := range []string{path, "/vehicles", "/", kit} {
				expectNot(t, a.get(p), tt.not...)
			}
		})
	}
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
