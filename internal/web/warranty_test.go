package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// warrantyToday is the clock of the warranty tests: 23 Nov 2026.
func warrantyApp(t *testing.T) *testApp {
	return newAppAt(t, func() time.Time { return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC) })
}

// The vehicle form's Has a warranty box shows its two fields; ticked, it
// needs one of them; unticked, it saves none, which clears a warranty.
func TestVehicleWarrantyForm(t *testing.T) {
	a := warrantyApp(t)
	expect(t, a.get("/vehicles/new"), http.StatusOK,
		`<label class="toggle"><input type="checkbox" id="has_warranty" name="has_warranty" value="1"> Has a warranty</label>`,
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
	expect(t, rec, http.StatusUnprocessableEntity, "Enter the day the warranty ends",
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
	expect(t, edit, http.StatusOK, `id="has_warranty" name="has_warranty" value="1"> Has a warranty`)
}

// A vehicle bought used can have had its warranty end before it was
// bought, and past its purchase reading: the warranty is the vehicle's.
func TestVehicleWarrantyBoughtUsed(t *testing.T) {
	a := warrantyApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"odometer": {"27102"}, "purchased_on": {"2024-07-04"}, "odometer_at_purchase": {"2148"},
		"has_warranty": {"1"}, "warranty_ends_on": {"2023-11-11"}, "warranty_ends_at": {"2000"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	expect(t, a.get(path), http.StatusOK, "Warranty ended</span>", "<dt>Warranty ended</dt><dd>11 Nov 2023 · 2\u202f000 km</dd>")
	// Its edit form has the box ticked, the ended warranty in it.
	expect(t, a.get(path+"/edit"), http.StatusOK, `id="has_warranty" name="has_warranty" value="1" checked> Has a warranty`,
		`name="warranty_ends_on" value="2023-11-11"`)
}

// A vehicle bought used: before the first service with a kit, its
// schedule and the warranty count from the manual's milestones, from 0 and
// First registered on (stock.Start). LittleJoy's Valve Check was due at
// 15 000 (16 500 at the latest), not 17 148: 15 000 past what it was
// bought at.
func TestFirstRegisteredOn(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	form := url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#f26b0f"},
		"odometer": {"27102"}, "purchased_on": {"2024-07-04"}, "odometer_at_purchase": {"2148"}, "first_registered_on": {"2021-11-11"},
		"has_warranty": {"1"}, "warranty_ends_on": {"2032-11-11"}}
	rec := a.post("/vehicles", form)
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	expect(t, a.get(path), http.StatusOK, "<dt>First registered</dt><dd>11 Nov 2021</dd>")
	expect(t, a.get(path+"/edit"), http.StatusOK, `name="first_registered_on" value="2021-11-11"`)

	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Valve Check"}, "interval_distance": {"15000"}, "warranty_required": {"1"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	expect(t, a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {itoa(mustID(t, kit))}, "kit_tier": {"full"}, "title": {"Valve Check"},
		"performed_on": {"2025-10-02"}, "odometer": {"21679"}}), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Due by 15\u202f000 km · 16\u202f500 km at the latest")

	// After a service of the user's own, the next counts from it, as a
	// dealer resets the dash: 21 679 + 15 000.
	expect(t, a.get(kit), http.StatusOK, "36\u202f679 km")

	// Refused after the purchase or in the future; what was typed comes back.
	for _, tt := range []struct{ on, msg string }{
		{"2024-07-05", "First registered on can&#39;t be after the purchase date"},
		{"2026-12-25", "Date cannot be in the future"},
	} {
		form.Set("name", "Demo "+tt.on)
		form.Set("first_registered_on", tt.on)
		expect(t, a.post("/vehicles", form), http.StatusUnprocessableEntity, tt.msg, `name="first_registered_on" value="`+tt.on+`"`)
	}
	// Empty is fine: a vehicle bought new.
	form.Set("name", "New one")
	form.Set("first_registered_on", "")
	expect(t, a.post("/vehicles", form), http.StatusSeeOther)
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
			page: []string{`<span class="badge badge-ok"><svg class="icon" aria-hidden="true"><use href="#i-shield-check"></use></svg> Under warranty</span>`, "Warranty ends", "1 Mar 2028 · 100\u202f000 km"},
			card: []string{`<use href="#i-shield-check"></use></svg> Under warranty</span>`},
			dash: []string{"· Under warranty</span>"},
			kit:  []string{"<dt>Warranty ends</dt><dd>1 Mar 2028 · 100\u202f000 km</dd>"},
			not:  []string{"Warranty ends soon"}},
		{name: "ends soon", endsOn: "2026-12-15", bar: "· Warranty ends soon",
			page: []string{`<span class="badge badge-warn"><svg class="icon" aria-hidden="true"><use href="#i-shield-alert"></use></svg> Warranty ends soon</span>`},
			card: []string{`<use href="#i-shield-alert"></use></svg> Warranty ends soon</span>`},
			dash: []string{"· Warranty ends soon</span>"},
			kit:  []string{"<dt>Warranty ends</dt><dd>15 Dec 2026 · 100\u202f000 km <span class=\"badge badge-warn\">"}},
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
// comes a step after it, in distance and in the full months.
func TestWarrantyDecidesKitSchedule(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	vehicle := url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#dc2626"}, "odometer": {"9000"},
		"purchased_on": {"2026-01-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2028-04-03"}}
	rec := a.post("/vehicles", vehicle)
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"15000"}, "interval_months": {"12"},
		"intermediate": {"1"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	// A late intermediate service, at 8 000 km instead of 7 500.
	expect(t, a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {itoa(mustID(t, kit))}, "kit_tier": {"intermediate"},
		"title": {"Oil Service (Intermediate)"}, "performed_on": {"2026-08-01"}, "odometer": {"8000"}}), http.StatusSeeOther)

	// Under warranty: the full service is still due at 15 000 km or a year after the purchase.
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>15\u202f000 km</dd>", "<dt>Due by</dt><dd>1 Jan 2027</dd>")

	// The warranty ended on 1 Nov 2026: the full service comes a step after
	// it, 7 500 km or 12 months on; nothing forces the purchase's year.
	vehicle.Set("warranty_ends_on", "2026-11-01")
	expect(t, a.postV(path, vehicle), http.StatusSeeOther)
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>15\u202f500 km</dd>", "<dt>Due by</dt><dd>1 Aug 2027</dd>")

	// No warranty at all: the same.
	vehicle.Del("has_warranty")
	expect(t, a.postV(path, vehicle), http.StatusSeeOther)
	expect(t, a.get(kit), http.StatusOK, "<dt>Due at</dt><dd>15\u202f500 km</dd>", "<dt>Due by</dt><dd>1 Aug 2027</dd>")
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

// A new bike's break-in (the manual's service at or before 1 000 km): the
// kit reminds it until something is logged with it, Log service starts as
// it with the full parts, and the full schedule doesn't count from it: the
// first full service is still 8 000 km or a year from the purchase. A kit
// has one break-in at most, and a service needs a reading.
func TestBreakIn(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"300"},
		"purchased_on": {"2026-06-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-05-31"}})
	expect(t, rec, http.StatusSeeOther)
	id := itoa(mustID(t, rec.Header().Get("Location")))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"8000"}, "interval_months": {"12"},
		"intermediate": {"1"}, "break_in_at": {"1000"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	kitID := mustID(t, kit)
	expect(t, a.get(kit), http.StatusOK, "Next: Break-in", "<dt>Break-in at</dt><dd>1\u202f000 km</dd>", "<dt>Due at</dt><dd>1\u202f000 km</dd>")
	expect(t, a.get(kit+"/edit"), http.StatusOK, `name="break_in_at" value="1000"`)

	// Log service from the kit: the break-in, its title, the full parts.
	form := a.get("/services/new?kit=" + itoa(kitID))
	expect(t, form, http.StatusOK, `value="break_in" autocomplete="off" data-kit-tier="full" data-kit-title="Oil Service (Break-in)" checked`,
		`value="Oil Service (Break-in)"`, `data-kit-parts="full"`)

	// A new service with this kit needs a reading.
	service := url.Values{"vehicle_id": {id}, "kit_id": {itoa(kitID)}, "kit_tier": {"break_in"}, "title": {"Oil Service (Break-in)"},
		"performed_on": {"2026-06-20"}}
	expect(t, a.post("/services", service), http.StatusUnprocessableEntity, "Odometer is required")
	service.Set("odometer", "960")
	rec = a.post("/services", service)
	expect(t, rec, http.StatusSeeOther)
	breakIn := rec.Header().Get("Location")
	expect(t, a.get(breakIn), http.StatusOK, "Oil Service · Break-in")

	// Done: no schedule counts from it. The half one next, from the
	// purchase (4 000 km, or a year on); the full one, under warranty,
	// 8 000 km or 1 Jun 2027, not 8 960.
	expect(t, a.get(kit), http.StatusOK, "Next: Intermediate", "<dt>Due at</dt><dd>4\u202f000 km</dd>", "<dt>Due by</dt><dd>1 Jun 2027</dd>",
		"<dt>Full due</dt><dd>8\u202f000 km or 1 Jun 2027</dd>", `(break-in)`)
	expectNot(t, a.get(kit), "Next: Break-in")
	// Log service no longer offers a break-in; a second one is refused:
	// after it, as not the kit's first; before it, as a second.
	expectNot(t, a.get("/services/new?kit="+itoa(kitID)), `value="break_in"`)
	service.Set("performed_on", "2026-07-01")
	service.Set("odometer", "1500")
	expect(t, a.post("/services", service), http.StatusUnprocessableEntity, "This kit has an earlier service: only its first is the break-in")
	service.Set("performed_on", "2026-06-10")
	service.Set("odometer", "500")
	expect(t, a.post("/services", service), http.StatusUnprocessableEntity, "This kit has a break-in already")

	// Its edit keeps it a break-in.
	expect(t, a.get(breakIn+"/edit"), http.StatusOK, `value="break_in" autocomplete="off" data-kit-tier="full" data-kit-title="Oil Service (Break-in)" checked`)

	// A kit without a Break-in at (an air filter every 15 000 km) has no
	// break-in: its first service is a full one, with no Schedule to pick.
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Air Filter"}, "interval_distance": {"15000"}})
	expect(t, rec, http.StatusSeeOther)
	airFilter := itoa(mustID(t, rec.Header().Get("Location")))
	expectNot(t, a.get("/services/new?kit="+airFilter), `name="kit_tier"`)
	expect(t, a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {airFilter}, "kit_tier": {"break_in"}, "title": {"Air Filter"},
		"performed_on": {"2026-06-20"}, "odometer": {"960"}}), http.StatusUnprocessableEntity, "This kit has no break-in on this vehicle")

	// A vehicle bought used, past a break-in, has none: its kits can't say
	// one (the form comes back with what was typed), and one saved before
	// that check is neither shown, reminded nor offered.
	rec = a.post("/vehicles", url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#e8a33d"}, "odometer": {"2500"},
		"purchased_on": {"2026-06-01"}, "odometer_at_purchase": {"2148"}})
	expect(t, rec, http.StatusSeeOther)
	used := itoa(mustID(t, rec.Header().Get("Location")))
	kitForm := url.Values{"vehicle_id": {used}, "name": {"Oil Service"}, "interval_distance": {"8000"}, "break_in_at": {"1000"}}
	expect(t, a.post("/kits", kitForm), http.StatusUnprocessableEntity, "LittleJoy was bought at 2\u202f148 km, past its break-in",
		`name="break_in_at" value="1000"`)
	kitForm.Del("break_in_at")
	rec = a.post("/kits", kitForm)
	expect(t, rec, http.StatusSeeOther)
	usedKit := rec.Header().Get("Location")
	if _, err := a.pool.Exec(context.Background(), `UPDATE service_kits SET break_in_at = 1000 WHERE id = $1`, mustID(t, usedKit)); err != nil {
		t.Fatal(err)
	}
	expectNot(t, a.get(usedKit), "Next: Break-in", "<dt>Break-in at</dt>")
	expectNot(t, a.get("/services/new?kit="+itoa(mustID(t, usedKit))), `name="kit_tier"`)

	// A new vehicle's purchase reading can't go past its kit's break-in.
	rec = a.post("/vehicles", url.Values{"name": {"Duke"}, "kind": {"motorcycle"}, "odometer": {"5000"},
		"purchased_on": {"2026-06-01"}, "odometer_at_purchase": {"0"}})
	expect(t, rec, http.StatusSeeOther)
	duke := rec.Header().Get("Location")
	expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(mustID(t, duke))}, "name": {"Oil Service"}, "interval_distance": {"8000"},
		"break_in_at": {"1000"}}), http.StatusSeeOther)
	expect(t, a.postV(duke, url.Values{"name": {"Duke"}, "kind": {"motorcycle"}, "purchased_on": {"2026-06-01"}, "odometer_at_purchase": {"1000"}}),
		http.StatusUnprocessableEntity, "Its kit Oil Service has a break-in at 1\u202f000 km: clear it there first")
}

// A service logged as Full before break-ins were kept (GreyJoy's first)
// can be marked one on its edit, as the kit's first service; a later one
// can't (else the schedules would count from the purchase years on).
func TestBreakInMarkedOnEdit(t *testing.T) {
	a := warrantyApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"12822"},
		"purchased_on": {"2025-04-04"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2028-04-03"}})
	expect(t, rec, http.StatusSeeOther)
	id := itoa(mustID(t, rec.Header().Get("Location")))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"15000"}, "interval_months": {"12"}, "intermediate": {"1"},
		"break_in_at": {"1000"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	first := url.Values{"vehicle_id": {id}, "kit_id": {itoa(mustID(t, kit))}, "kit_tier": {"full"}, "title": {"Oil Service (First)"},
		"performed_on": {"2025-04-30"}, "odometer": {"960"}}
	// A Full there is the break-in now: refused.
	expect(t, a.post("/services", first), http.StatusUnprocessableEntity,
		"This is the kit&#39;s break-in (due by 1 000 km, 1 100 km at the latest): log it as Break-in")
	// One saved before that rule (GreyJoy's first, logged as Full).
	first.Set("kit_tier", "break_in")
	rec = a.post("/services", first)
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")
	if _, err := a.pool.Exec(context.Background(), `UPDATE service_records SET kit_tier = 'full' WHERE id = $1`, mustID(t, svc)); err != nil {
		t.Fatal(err)
	}
	first.Set("kit_tier", "full")
	// Logged as Full: the full schedule counts from it (15 960 km).
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>15\u202f960 km</dd>", "<dt>Due by</dt><dd>30 Apr 2026</dd>")
	expect(t, a.get(svc+"/edit"), http.StatusOK, `value="break_in" autocomplete="off"`)
	first.Set("kit_tier", "break_in")
	first.Set("title", "Oil Service (Break-in)")
	expect(t, a.postV(svc, first), http.StatusSeeOther)
	// A break-in: from the purchase again (15 000 km, 4 Apr 2026).
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>15\u202f000 km</dd>", "<dt>Due by</dt><dd>4 Apr 2026</dd>")

	// A later service: its edit offers no break-in, and one is refused.
	later := url.Values{"vehicle_id": {id}, "kit_id": {itoa(mustID(t, kit))}, "kit_tier": {"full"}, "title": {"Oil Service"},
		"performed_on": {"2026-04-28"}, "odometer": {"12822"}}
	rec = a.post("/services", later)
	expect(t, rec, http.StatusSeeOther)
	laterSvc := rec.Header().Get("Location")
	expectNot(t, a.get(laterSvc+"/edit"), `value="break_in"`)
	// The first one a full service again: refused, it is the break-in.
	first.Set("kit_tier", "full")
	first.Set("title", "Oil Service (First)")
	expect(t, a.postV(svc, first), http.StatusUnprocessableEntity, "log it as Break-in")
	expectNot(t, a.get(laterSvc+"/edit"), `value="break_in"`)
	later.Set("kit_tier", "break_in")
	expect(t, a.postV(laterSvc, later), http.StatusUnprocessableEntity, "This kit has an earlier service: only its first is the break-in")
	expect(t, a.get(svc+"/edit"), http.StatusOK, `value="break_in" autocomplete="off"`)
}

// A break-in read past Break-in at and its grace (10%) is refused: one done
// that late is a full service, logged as Full.
func TestBreakInLateRefused(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"300"},
		"purchased_on": {"2026-06-01"}, "odometer_at_purchase": {"0"}})
	expect(t, rec, http.StatusSeeOther)
	id := itoa(mustID(t, rec.Header().Get("Location")))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"10000"}, "interval_months": {"12"},
		"intermediate": {"1"}, "break_in_at": {"1000"}})
	expect(t, rec, http.StatusSeeOther)
	kitID := itoa(mustID(t, rec.Header().Get("Location")))
	// Past its grace with nothing logged: the full service is next, overdue
	// since 1 000 km, and Log service starts as Full; Break-in stays on the
	// form to backfill one done on time.
	expect(t, a.post("/vehicles/"+id+"/odometer", url.Values{"odometer": {"1200"}}), http.StatusSeeOther)
	kit := "/kits/" + kitID
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>1 000 km</dd>")
	expectNot(t, a.get(kit), "Next: Break-in")
	expect(t, a.get("/services/new?kit="+kitID), http.StatusOK, `value="full" autocomplete="off" data-kit-tier="full" data-kit-title="Oil Service" checked`,
		`value="break_in"`)

	service := url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {"break_in"}, "title": {"Oil Service (Break-in)"},
		"performed_on": {"2026-07-01"}, "odometer": {"1101"}}
	expect(t, a.post("/services", service), http.StatusUnprocessableEntity,
		"The break-in is due by 1 000 km (1 100 km at the latest): log this one as Full", `name="odometer" value="1101"`)
	service.Set("odometer", "1100")
	rec = a.post("/services", service)
	expect(t, rec, http.StatusSeeOther)
	// Nor can its edit move it past.
	breakIn := rec.Header().Get("Location")
	service.Set("odometer", "6000")
	expect(t, a.postV(breakIn, service), http.StatusUnprocessableEntity, "log this one as Full")

	// The kit form refuses a Break-in at past halfway.
	expect(t, a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service 2"}, "interval_distance": {"10000"}, "intermediate": {"1"},
		"break_in_at": {"6000"}}), http.StatusUnprocessableEntity,
		"The break-in comes before the first 5 000 km: with Halfway ticked, the intermediate service is due there")
}

// A kit ticked "Required by the warranty" whose break-in or full service
// was missed loses the vehicle's warranty: a red badge, what was missed on
// the vehicle page, and its kits scheduled as without one. Logging the
// missed services on time (backfilling) gets it back; an unticked kit
// never loses it.
func TestWarrantyLostAndBackfilled(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	vehicle := url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"10500"},
		"purchased_on": {"2026-01-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-12-31"}}
	rec := a.post("/vehicles", vehicle)
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	kitForm := url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"10000"}, "interval_months": {"12"},
		"intermediate": {"1"}, "break_in_at": {"1000"}}
	rec = a.post("/kits", kitForm)
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	kitID := itoa(mustID(t, kit))

	// Not ticked: the break-in missed doesn't count.
	expect(t, a.get(path), http.StatusOK, `<span class="badge badge-ok">`, "Under warranty")
	expect(t, a.get(kit+"/edit"), http.StatusOK,
		`<label class="toggle"><input type="checkbox" id="warranty_required" name="warranty_required" value="1"> Required by the warranty</label>`)

	// Ticked: lost, by the break-in (1 000 km, nothing logged at 10 500).
	kitForm.Set("warranty_required", "1")
	expect(t, a.postV(kit, kitForm), http.StatusSeeOther)
	expect(t, a.get(kit+"/edit"), http.StatusOK, `id="warranty_required" name="warranty_required" value="1" checked`)
	expect(t, a.get(kit), http.StatusOK, "<dt>Warranty</dt><dd>Required</dd>")
	expect(t, a.get(path), http.StatusOK, `<span class="badge badge-danger">`, "Warranty lost",
		`<dt>Warranty lost</dt><dd><a href="#warranty-lost">past 1 100 km</a></dd>`, "Oil Service · break-in</a>", "Not logged · lost past 1 100 km")
	expect(t, a.get("/vehicles"), http.StatusOK, "Warranty lost")
	// Lost: services take turns, so nothing logged means the full one, two
	// steps from the purchase (10 000 km, two years).
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due at</dt><dd>10 000 km</dd>", "<dt>Due by</dt><dd>1 Jan 2028</dd>")

	// Backfilled: the break-in at 1 000, then a full one late by distance
	// (11 200, past 11 000): still lost, by that full service.
	logged := func(tier, on, odo string) string {
		t.Helper()
		rec := a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {tier}, "title": {"Oil Service"},
			"performed_on": {on}, "odometer": {odo}})
		expect(t, rec, http.StatusSeeOther)
		return rec.Header().Get("Location")
	}
	logged("break_in", "2026-02-01", "1000")
	late := logged("full", "2026-10-01", "11200")
	expect(t, a.get(path), http.StatusOK, `<dt>Warranty lost</dt><dd><a href="#warranty-lost">by 1 Oct 2026</a></dd>`, "Oil Service · full service</a>",
		"Due by 10 000 km or 1 Jan 2027 · 11 000 km or 31 Jan 2027 at the latest", "11 200 km on 1 Oct 2026</a> · lost by 1 Oct 2026")
	// Its reading corrected to 10 900, inside the grace: back under warranty.
	expect(t, a.postV(late, url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {"full"}, "title": {"Oil Service"},
		"performed_on": {"2026-10-01"}, "odometer": {"10900"}}), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, `<span class="badge badge-ok">`, "Under warranty")
	expectNot(t, a.get(path), "Warranty lost")
	expect(t, a.get(kit), http.StatusOK, "Next: Intermediate", "<dt>Due at</dt><dd>15 900 km</dd>")
}

// The history behind Warranty lost, over HTTP: an intermediate can't be a
// break-in kit's first service, a break-in saved late before that rule
// still saves unchanged, deleting and restoring a late Full is seen at
// once, unticking the kit gives the warranty back, and a duplicate isn't
// ticked.
func TestWarrantyLostHistory(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"900"},
		"purchased_on": {"2025-11-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-12-31"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	kitForm := url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"10000"}, "interval_months": {"12"},
		"intermediate": {"1"}, "break_in_at": {"1000"}, "warranty_required": {"1"}}
	rec = a.post("/kits", kitForm)
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	kitID := itoa(mustID(t, kit))
	svc := func(tier, on, odo string) url.Values {
		return url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {tier}, "title": {"Oil Service"}, "performed_on": {on}, "odometer": {odo}}
	}

	// No intermediate first: not offered, and refused.
	expectNot(t, a.get("/services/new?kit="+kitID), `value="intermediate"`)
	expect(t, a.post("/services", svc("intermediate", "2025-12-01", "900")), http.StatusUnprocessableEntity,
		"This kit&#39;s first service is its break-in: log it as Break-in or Full")

	// A break-in saved at 1 500 before the 10% rule (as old data may
	// hold): its edit saves while its reading stays, not when it moves.
	rec = a.post("/services", svc("break_in", "2025-12-01", "1000"))
	expect(t, rec, http.StatusSeeOther)
	breakIn := rec.Header().Get("Location")
	if _, err := a.pool.Exec(context.Background(), `UPDATE service_records SET odometer = 1500 WHERE id = $1`, mustID(t, breakIn)); err != nil {
		t.Fatal(err)
	}
	old := svc("break_in", "2025-12-01", "1500")
	old.Set("notes", "Dealer")
	expect(t, a.postV(breakIn, old), http.StatusSeeOther)
	old.Set("odometer", "1600")
	expect(t, a.postV(breakIn, old), http.StatusUnprocessableEntity, "log this one as Full")
	expect(t, a.get(path), http.StatusOK, "Oil Service · break-in</a>", "1 500 km on 1 Dec 2025</a> · lost by 1 Dec 2025")
	if _, err := a.pool.Exec(context.Background(), `UPDATE service_records SET odometer = 1000 WHERE id = $1`, mustID(t, breakIn)); err != nil {
		t.Fatal(err)
	}

	// A late Full (11 500, past 11 000): lost. Deleted (the odometer goes
	// back with it, and its year isn't up), the warranty is back; restored,
	// it is the miss again.
	rec = a.post("/services", svc("full", "2026-10-01", "11500"))
	expect(t, rec, http.StatusSeeOther)
	full := rec.Header().Get("Location")
	expect(t, a.get(path), http.StatusOK, "11 500 km on 1 Oct 2026</a>")
	undo := flashOf(t, saveFresh(t, a, full, full+"/delete", nil)).Undo
	if undo == 0 {
		t.Fatal("no Undo")
	}
	expect(t, a.get(path), http.StatusOK, "Under warranty")
	expectNot(t, a.get(path), "Warranty lost")
	expect(t, a.post("/trash/"+itoa(undo)+"/restore", nil), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "11 500 km on 1 Oct 2026</a>")

	// Unticked: the warranty is back.
	kitForm.Del("warranty_required")
	expect(t, a.postV(kit, kitForm), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Under warranty")
	expectNot(t, a.get(path), "Warranty lost")

	// A duplicate starts unticked.
	kitForm.Set("warranty_required", "1")
	expect(t, a.postV(kit, kitForm), http.StatusSeeOther)
	expect(t, a.get("/kits/new?from="+kitID), http.StatusOK, `id="warranty_required" name="warranty_required" value="1">`)
}

// The break-in window over HTTP: while a kit waits for the break-in, the
// break-in is next, due by Break-in at or the kit's year (also once the
// warranty is lost); a Full as the kit's first service by Break-in at + 10%
// is refused; a break-in done when the year is close is the first Full too,
// so the time counts from it and the distance from the purchase.
func TestBreakInWindowOverHTTP(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	kitFor := func(vehicle url.Values, required bool) (kit, kitID string) {
		t.Helper()
		rec := a.post("/vehicles", vehicle)
		expect(t, rec, http.StatusSeeOther)
		form := url.Values{"vehicle_id": {itoa(mustID(t, rec.Header().Get("Location")))}, "name": {"Oil Service"},
			"interval_distance": {"10000"}, "interval_months": {"12"}, "intermediate": {"1"}, "break_in_at": {"1000"}}
		if required {
			form.Set("warranty_required", "1")
		}
		rec = a.post("/kits", form)
		expect(t, rec, http.StatusSeeOther)
		kit = rec.Header().Get("Location")
		return kit, itoa(mustID(t, kit))
	}
	// No warranty, 450 km since 1 Jan 2026: the break-in, by 1 000 km or
	// 1 Jan 2027, and Log service starts as it.
	kit, kitID := kitFor(url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"450"},
		"purchased_on": {"2026-01-01"}, "odometer_at_purchase": {"0"}}, false)
	expect(t, a.get(kit), http.StatusOK, "Next: Break-in", "<dt>Due at</dt><dd>1 000 km</dd>", "<dt>Due by</dt><dd>1 Jan 2027</dd>")
	expect(t, a.get("/services/new?kit="+kitID), http.StatusOK,
		`value="break_in" autocomplete="off" data-kit-tier="full" data-kit-title="Oil Service (Break-in)" checked`)

	// Under warranty, ticked, nothing done by 31 Oct 2026 (a month past the
	// year): lost, and the break-in stays next, overdue by its date.
	kit, _ = kitFor(url.Values{"name": {"LittleJoy"}, "kind": {"motorcycle"}, "color": {"#e8a33d"}, "odometer": {"500"},
		"purchased_on": {"2025-10-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-09-30"}}, true)
	expect(t, a.get(kit), http.StatusOK, "Next: Break-in", "<dt>Due by</dt><dd>1 Oct 2026</dd>", "Overdue")

	// Under warranty, ticked, 900 km 39 days before the year: the break-in,
	// due soon. A Full there is refused; the break-in saves, keeps the
	// warranty, and is the first Full too: the intermediate service next at
	// 5 000 km (from the purchase) or 23 Nov 2027 (a year after it).
	kit, kitID = kitFor(url.Values{"name": {"KillJoy"}, "kind": {"motorcycle"}, "color": {"#2563eb"}, "odometer": {"900"},
		"purchased_on": {"2026-01-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2030-12-31"}}, true)
	expect(t, a.get(kit), http.StatusOK, "Next: Break-in", "<dt>Due at</dt><dd>1 000 km</dd>", "<dt>Due by</dt><dd>1 Jan 2027</dd>", "Due soon")
	k, err := a.store.GetKit(context.Background(), mustID(t, kit))
	if err != nil {
		t.Fatal(err)
	}
	vehicle := "/vehicles/" + itoa(k.VehicleID)
	service := url.Values{"vehicle_id": {itoa(k.VehicleID)}, "kit_id": {kitID}, "kit_tier": {"full"}, "title": {"Oil Service"},
		"performed_on": {"2026-11-23"}, "odometer": {"1000"}}
	expect(t, a.post("/services", service), http.StatusUnprocessableEntity,
		"This is the kit&#39;s break-in (due by 1 000 km, 1 100 km at the latest): log it as Break-in")
	service.Set("kit_tier", "break_in")
	expect(t, a.post("/services", service), http.StatusSeeOther)
	expect(t, a.get(vehicle), http.StatusOK, "Under warranty")
	expectNot(t, a.get(vehicle), "Warranty lost")
	expect(t, a.get(kit), http.StatusOK, "Next: Intermediate", "<dt>Due at</dt><dd>5 000 km</dd>", "<dt>Due by</dt><dd>23 Nov 2027</dd>")
}

// A kit ticked Required by the warranty can't be archived: archiving would
// drop what it missed and give the warranty back. Its Archive button says
// to untick it first; unticked (which gives the warranty back, as it
// should), it archives.
func TestArchiveRequiredKit(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"11500"},
		"purchased_on": {"2025-11-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-12-31"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	kitForm := url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"10000"}, "interval_months": {"12"},
		"warranty_required": {"1"}}
	rec = a.post("/kits", kitForm)
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	// A Full late by distance (11 500, past 11 000): lost.
	expect(t, a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {itoa(mustID(t, kit))}, "kit_tier": {"full"}, "title": {"Oil Service"},
		"performed_on": {"2026-11-01"}, "odometer": {"11500"}}), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Warranty lost")

	rec = a.postV(kit+"/archive", nil)
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind != "error" || f.Message != `Untick "Required by the warranty" before archiving Oil Service: its services count for the warranty.` {
		t.Errorf("flash = %+v", f)
	}
	if rec.Header().Get("Location") != kit {
		t.Errorf("redirect = %s, want %s", rec.Header().Get("Location"), kit)
	}
	expectNot(t, a.get(kit), "Archived")
	expect(t, a.get(path), http.StatusOK, "Warranty lost")

	kitForm.Del("warranty_required")
	expect(t, a.postV(kit, kitForm), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Under warranty")
	rec = a.postV(kit+"/archive", nil)
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind == "error" || f.Message != "Archived Oil Service. Its services keep it; restore it any time." {
		t.Errorf("flash = %+v", f)
	}
	expect(t, a.get(kit), http.StatusOK, "Archived")
	// Archived, it can't be ticked again (the warranty counts only kits that
	// aren't): the form comes back with the box ticked and a banner.
	kitForm.Set("warranty_required", "1")
	expect(t, a.postV(kit, kitForm), http.StatusUnprocessableEntity, "Restore Oil Service before ticking &#34;Required by the warranty&#34;.",
		`id="warranty_required" name="warranty_required" value="1" checked`)
	expect(t, a.get(path), http.StatusOK, "Under warranty")
}

// A kit stays with its vehicle (its services are there; Duplicate makes one
// for another): its edit form shows the vehicle as a chip, with no Vehicle
// field, and a form posting another vehicle is refused.
func TestKitStaysWithItsVehicle(t *testing.T) {
	a := warrantyApp(t)
	k390, k1290 := a.seed()
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Chain"}, "interval_distance": {"1000"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	edit := a.get(kit + "/edit")
	expect(t, edit, http.StatusOK, `<input type="hidden" name="vehicle_id" value="`+itoa(k390)+`" data-name="KTM 390">`, `class="chip`)
	expectNot(t, edit, `name="vehicle_id" value="`+itoa(k1290)+`"`)
	expect(t, a.postV(kit, url.Values{"vehicle_id": {itoa(k1290)}, "name": {"Chain"}, "interval_distance": {"1000"}}),
		http.StatusUnprocessableEntity, "A kit stays with its vehicle: duplicate it for another vehicle.")
	expect(t, a.get("/kits/new"), http.StatusOK, `name="vehicle_id" value="`+itoa(k1290)+`"`) // a new kit still chooses
}

// A break-in forgotten while later services were logged can be backfilled:
// Log service offers Break-in until one is logged, the store takes it dated
// before the services already logged (as the kit's first), and refuses it
// dated after them. Backfilled on time, it gives the warranty back.
func TestBreakInBackfill(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"10500"},
		"purchased_on": {"2026-01-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-12-31"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	rec = a.post("/kits", url.Values{"vehicle_id": {id}, "name": {"Oil Service"}, "interval_distance": {"10000"}, "interval_months": {"12"},
		"intermediate": {"1"}, "break_in_at": {"1000"}, "warranty_required": {"1"}})
	expect(t, rec, http.StatusSeeOther)
	kitID := itoa(mustID(t, rec.Header().Get("Location")))
	svc := func(tier, on, odo string) url.Values {
		return url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {tier}, "title": {"Oil Service"}, "performed_on": {on}, "odometer": {odo}}
	}
	// The Full at 10 500 logged first: on time, but the break-in isn't.
	expect(t, a.post("/services", svc("full", "2026-11-01", "10500")), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Warranty lost")
	form := a.get("/services/new?kit=" + kitID)
	expect(t, form, http.StatusOK, `value="break_in" autocomplete="off" data-kit-tier="full" data-kit-title="Oil Service (Break-in)">`)
	// Dated after the Full: refused.
	expect(t, a.post("/services", svc("break_in", "2026-11-20", "10600")), http.StatusUnprocessableEntity,
		"This kit has an earlier service: only its first is the break-in")
	// Dated before it, at 1 000: saved, and the warranty is back.
	expect(t, a.post("/services", svc("break_in", "2026-02-01", "1000")), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Under warranty")
	expectNot(t, a.get(path), "Warranty lost")
	// Logged: no longer offered.
	expectNot(t, a.get("/services/new?kit="+kitID), `value="break_in"`)
}

// The vehicle page lists every service the warranty asked for that was
// missed, in a red card under the odometer, the first to lose it first: the
// kit (linked), what was due and the latest it could be done, what happened
// (the late service linked) and when it lost the warranty. The facts say
// only when it was lost, linking to the card. Backfilling a miss takes it
// off, and the date moves to the next one.
func TestWarrantyLostCard(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"20000"},
		"purchased_on": {"2025-01-01"}, "odometer_at_purchase": {"0"}, "has_warranty": {"1"}, "warranty_ends_on": {"2029-12-31"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	id := itoa(mustID(t, path))
	kit := func(name string, form url.Values) string {
		t.Helper()
		form.Set("vehicle_id", id)
		form.Set("name", name)
		form.Set("warranty_required", "1")
		rec := a.post("/kits", form)
		expect(t, rec, http.StatusSeeOther)
		return itoa(mustID(t, rec.Header().Get("Location")))
	}
	oil := kit("Oil Service", url.Values{"interval_distance": {"10000"}, "interval_months": {"12"}})
	valve := kit("Valve Check", url.Values{"interval_distance": {"15000"}})
	air := kit("Air Filter", url.Values{"interval_distance": {"15000"}})
	logged := func(kitID, on, odo string) string {
		t.Helper()
		rec := a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {"full"}, "title": {"Service"},
			"performed_on": {on}, "odometer": {odo}})
		expect(t, rec, http.StatusSeeOther)
		return rec.Header().Get("Location")
	}
	valveSvc := logged(valve, "2026-06-01", "16953")
	card := func() string {
		t.Helper()
		page := a.get(path)
		body := page.Body.String()
		i := strings.Index(body, `id="warranty-lost"`)
		if i < 0 {
			t.Fatal("no Warranty lost card")
		}
		return body[i : i+strings.Index(body[i:], "</section>")]
	}
	got := card()
	for _, want := range []string{
		`<h2 id="warranty-lost-h"><svg class="icon" aria-hidden="true"><use href="#i-shield-x"></use></svg> Warranty lost</h2>`,
		`<a class="title" href="/kits/` + oil + `">Oil Service · full service</a>`,
		"Due by 10\u202f000 km or 1 Jan 2026 · 11\u202f000 km or 31 Jan 2026 at the latest",
		"Not logged · lost by 1 Feb 2026",
		`<a class="title" href="/kits/` + valve + `">Valve Check · full service</a>`,
		"Due by 15\u202f000 km · 16\u202f500 km at the latest",
		`Done at <a href="` + valveSvc + `">16` + "\u202f" + `953 km on 1 Jun 2026</a> · lost by 1 Jun 2026`,
		`<a class="title" href="/kits/` + air + `">Air Filter · full service</a>`,
		// Its 16 500 km passed with nothing logged: dated by the first
		// service logged past it (the valve check's).
		"Not logged · lost by 1 Jun 2026",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("card lacks %q:\n%s", want, got)
		}
	}
	// The first to lose it first: Oil (1 Feb), Valve and Air (1 Jun, in the
	// kits' order).
	if o, v, f := strings.Index(got, "Oil Service ·"), strings.Index(got, "Valve Check ·"), strings.Index(got, "Air Filter ·"); o > v || v > f {
		t.Errorf("order: oil %d, valve %d, air %d", o, v, f)
	}
	expect(t, a.get(path), http.StatusOK, `<dt>Warranty lost</dt><dd><a href="#warranty-lost">by 1 Feb 2026</a></dd>`)

	// The oil change backfilled on time: off the list, the date moves on.
	logged(oil, "2026-01-20", "10500")
	got = card()
	if strings.Contains(got, "Oil Service") {
		t.Errorf("backfilled oil change still listed:\n%s", got)
	}
	expect(t, a.get(path), http.StatusOK, `<dt>Warranty lost</dt><dd><a href="#warranty-lost">by 1 Jun 2026</a></dd>`)

	// Unticked, the others go too, and so does the card.
	for _, k := range []string{valve, air} {
		expect(t, a.postV("/kits/"+k, url.Values{"vehicle_id": {id}, "name": {"Kit " + k}, "interval_distance": {"15000"}}), http.StatusSeeOther)
	}
	page := a.get(path)
	expect(t, page, http.StatusOK, "Under warranty")
	expectNot(t, page, `id="warranty-lost"`, "<dt>Warranty lost</dt>")
}

// A kit with a time only (Coolant every 48 months) can have an
// intermediate service, halfway through the time: an odd time is refused
// (keeping what was typed), the kit page says every 24 months and reminds
// it first, Log service starts as it with its parts, and the Full comes a
// step after it. A distance added later makes it halfway by distance, at
// the full time.
func TestHalfwayByTime(t *testing.T) {
	a := warrantyApp(t) // 23 Nov 2026
	rec := a.post("/vehicles", url.Values{"name": {"GreyJoy"}, "kind": {"motorcycle"}, "color": {"#d65151"}, "odometer": {"20000"},
		"purchased_on": {"2025-01-01"}, "odometer_at_purchase": {"0"}})
	expect(t, rec, http.StatusSeeOther)
	id := itoa(mustID(t, rec.Header().Get("Location")))
	item, err := a.store.CreateItem(context.Background(), stock.ItemInput{Name: "Coolant", Unit: stock.UnitPiece, Quantity: 5})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"vehicle_id": {id}, "name": {"Coolant"}, "interval_months": {"9"}, "intermediate": {"1"},
		"line_item": {itoa(item)}, "line_qty": {"2"}, "inter_line_item": {itoa(item)}, "inter_line_qty": {"1"}}
	expect(t, a.post("/kits", form), http.StatusUnprocessableEntity,
		"Enter an even number of months: the intermediate service comes halfway through the time",
		`name="interval_months" value="9"`, `id="intermediate" name="intermediate" value="1" checked`)
	form.Set("interval_months", "48")
	rec = a.post("/kits", form)
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	kitID := itoa(mustID(t, kit))
	page := a.get(kit)
	expect(t, page, http.StatusOK, "<dt>Full every</dt><dd>48 months</dd>", "<dt>Intermediate every</dt><dd>24 months</dd>",
		"Next: Intermediate", "<dt>Due by</dt><dd>1 Jan 2027</dd>")
	expectNot(t, page, "<dt>Due at</dt>")
	expect(t, a.get("/services/new?kit="+kitID), http.StatusOK,
		`value="intermediate" autocomplete="off" data-kit-tier="inter" data-kit-title="Coolant (Intermediate)" checked`)

	// Done today: the Full a step (24 months) after it, no warranty.
	expect(t, a.post("/services", url.Values{"vehicle_id": {id}, "kit_id": {kitID}, "kit_tier": {"intermediate"}, "title": {"Coolant (Intermediate)"},
		"performed_on": {"2026-11-23"}, "odometer": {"20000"}, "part_item": {itoa(item)}, "part_qty": {"1"}}), http.StatusSeeOther)
	expect(t, a.get(kit), http.StatusOK, "Next: Full", "<dt>Due by</dt><dd>23 Nov 2028</dd>")

	// A distance added: halfway by distance, at the full time.
	form.Set("interval_distance", "10000")
	expect(t, a.postV(kit, form), http.StatusSeeOther)
	expect(t, a.get(kit), http.StatusOK, "<dt>Intermediate every</dt><dd>5 000 km or 48 months</dd>")
}
