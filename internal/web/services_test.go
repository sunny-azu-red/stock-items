package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

func TestKitServiceAndShoppingFlow(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	oil := itemID(t, a.store, "Silkolene")

	rec := a.post("/kits", url.Values{
		"vehicle_id": {itoa(k390)}, "name": {"Oil service"}, "interval_km": {"7,500"}, "interval_months": {"12"},
		"line_item": {itoa(oil), itoa(pads), ""}, "line_qty": {"1.7 L", "1", ""},
	})
	expect(t, rec, http.StatusSeeOther)
	kitPath := rec.Header().Get("Location")
	expect(t, a.get(kitPath), http.StatusOK, "1 part short", "1\u202f175 ml", "Not done yet")
	expect(t, a.get("/"), http.StatusOK, "Oil service", "Upcoming service")
	expect(t, a.get("/shopping"), http.StatusOK, "Short for KTM 390: Oil service")

	// Logging the service fails while oil is short.
	svc := url.Values{
		"vehicle_id": {itoa(k390)}, "kit_id": {kitPath[len("/kits/"):]}, "title": {"Oil service"},
		"performed_on": {"2026-10-20"}, "odometer_km": {"12000"},
		"part_item": {itoa(oil), itoa(pads)}, "part_qty": {"1.7 L", "1"},
	}
	expect(t, a.post("/services", svc), http.StatusUnprocessableEntity, "Only 525 ml in stock")

	// Shortfall to the shopping list, then mark it bought.
	expect(t, a.post(kitPath+"/shortfall", nil), http.StatusSeeOther)
	list, _ := a.store.ListShopping(context.Background())
	if len(list) != 1 || *list[0].Quantity != 1175 {
		t.Fatalf("shopping = %+v", list)
	}
	entry := "/shopping/" + itoa(list[0].ID)
	expect(t, a.get(entry+"/bought"), http.StatusOK, `value="1175"`)
	expect(t, a.postV(entry+"/bought", url.Values{"amount": {"4"}, "amount_unit": {"l"}, "total_price": {"45"}}), http.StatusSeeOther)
	expect(t, a.get("/shopping/export.txt"), http.StatusOK, "Shopping list")

	rec = a.post("/services", svc)
	expect(t, rec, http.StatusSeeOther)
	svcPath := rec.Header().Get("Location")
	expect(t, a.get(svcPath), http.StatusOK, "1.7 L", "12\u202f000 km")
	expect(t, a.get(kitPath), http.StatusOK, "Last done", "19\u202f500 km")
	expect(t, a.get("/services"), http.StatusOK, "Oil service")

	v, _ := a.store.GetVehicle(context.Background(), k390)
	if *v.OdometerKm != 12000 {
		t.Errorf("odometer = %d", *v.OdometerKm)
	}
	expect(t, a.postV(svcPath+"/delete", nil), http.StatusSeeOther)
	if it, _ := a.store.GetItem(context.Background(), oil); it.Quantity != 4525 {
		t.Errorf("oil after deleting service = %v", it.Quantity)
	}

	// Free text entries and suggestions.
	expect(t, a.post("/shopping", url.Values{"free_text": {"Chain lube"}, "quantity": {"2"}}), http.StatusSeeOther)
	expect(t, a.post("/shopping", url.Values{}), http.StatusUnprocessableEntity, "Pick an item or describe")
	expect(t, a.post("/shopping/suggestions", url.Values{"item_id": {itoa(pads)}, "qty": {"2"}}), http.StatusSeeOther)
	expect(t, a.get("/shopping"), http.StatusOK, "Chain lube", "OEM Rear Brake Pads")

	expect(t, a.postV(kitPath+"/delete", nil), http.StatusSeeOther)
}

func TestServiceDateIsChecked(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-12-01"}}),
		http.StatusUnprocessableEntity, "Date cannot be in the future")
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"}}),
		http.StatusSeeOther)
}

func TestDeletingAServicePutsTheOdometerBack(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"}, "odometer_km": {"120000"}})
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, `name="odometer_km" value="120000"`)
	expect(t, a.postV(rec.Header().Get("Location")+"/delete", nil), http.StatusSeeOther)
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, `name="odometer_km" value="10000"`)
}

func TestEditService(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Oil top-up"}, "performed_on": {"2026-10-29"},
		"odometer_km": {"11000"}, "labor_cost": {"1000"}, "part_item": {itoa(oil)}, "part_qty": {"100"}})
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")

	expect(t, a.get(svc), http.StatusOK, `href="`+svc+`/edit"`)
	expect(t, a.get(svc+"/edit"), http.StatusOK, "Edit service", `value="Oil top-up"`, `value="1000.00"`, `value="11000"`,
		`action="`+svc+`"`, "Save service", "in stock: 525 ml") // 425 ml left + the 100 ml this service returns

	expect(t, a.postV(svc, url.Values{"vehicle_id": {"999"}, "title": {"Oil top-up"}, "performed_on": {"2026-10-29"},
		"odometer_km": {"11000"}, "labor_cost": {"100"}, "part_item": {itoa(oil)}, "part_qty": {"5 L"}}),
		http.StatusUnprocessableEntity, "Only 525 ml in stock")
	rec = a.postV(svc, url.Values{"vehicle_id": {"999"}, "title": {"Oil top-up"}, "performed_on": {"2026-10-29"},
		"odometer_km": {"11000"}, "labor_cost": {"100"}, "part_item": {itoa(oil)}, "part_qty": {"200"}})
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != svc {
		t.Errorf("location = %q", l)
	}
	if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 325 {
		t.Errorf("oil = %v, want 325", it.Quantity)
	}
	got, _ := a.store.GetService(ctx, mustID(t, svc))
	if got.VehicleID != k390 || *got.LaborCost != 100 {
		t.Errorf("service = %+v", got)
	}
}

func TestPartRowsHaveUnits(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	pads := itemID(t, a.store, "Rear Brake")

	// A bare number with the L unit is litres; with no unit chosen it is the base unit.
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(oil), itoa(pads)}, "line_qty": {"1.7", "1"}, "line_unit": {"l", ""}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	k, _ := a.store.GetKit(ctx, mustID(t, kit))
	if len(k.Lines) != 2 || k.Lines[0].Need != 1700 || k.Lines[1].Need != 1 {
		t.Fatalf("kit lines = %+v", k.Lines)
	}
	// A unit that doesn't fit the item is explained on its row.
	expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"},
		"line_item": {itoa(pads)}, "line_qty": {"1"}, "line_unit": {"l"}}), http.StatusUnprocessableEntity, "L cannot be used for pieces")

	// Prefilled rows come with their unit and what is in stock.
	edit := a.get(kit + "/edit")
	expect(t, edit, http.StatusOK, `name="line_qty" value="1.7"`, `<option value="l" data-base="ml" selected>L</option>`, "in stock: 525 ml")

	// Every short part is marked at once.
	form := url.Values{"vehicle_id": {itoa(k390)}, "title": {"Oil service"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil), itoa(pads)}, "part_qty": {"2", "3"}, "part_unit": {"l", ""}}
	expect(t, a.post("/services", form), http.StatusUnprocessableEntity, "2 parts are short", "Only 525 ml in stock", "Only 1 pcs in stock")
}

func TestServicesShowLabour(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Valves"}, "performed_on": {"2026-10-29"},
		"labor_cost": {"350"}}), http.StatusSeeOther)
	expect(t, a.get("/services"), http.StatusOK, "350.00 EUR labour", "350.00 EUR labour in total")
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, "350.00 EUR labour")
}

func TestServicesPaging(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	for i := range 101 {
		in := stock.ServiceInput{VehicleID: k390, Title: fmt.Sprintf("Check %d", i), PerformedOn: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i)}
		in.Normalize()
		if _, err := a.store.CreateService(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, a.get("/services"), http.StatusOK, `<span class="muted small">101`, `href="/services?page=2"`, "Check 100")
	page2 := a.get("/services?page=2")
	expect(t, page2, http.StatusOK, "Check 0", "Newer")
	expectNot(t, page2, "Check 100")
	expect(t, a.get("/services?page=99999999999999999999"), http.StatusOK)
	expect(t, a.get("/activity?page=9223372036854775807"), http.StatusOK)
}

func TestArchiveKitFlow(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"}, "interval_months": {"12"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	expect(t, a.get(kit), http.StatusOK, `action="`+kit+`/delete"`)
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "kit_id": {kit[len("/kits/"):]}, "title": {"Oil service"},
		"performed_on": {"2026-10-29"}}), http.StatusSeeOther)
	page := a.get(kit)
	expect(t, page, http.StatusOK, `action="`+kit+`/archive"`, "1 logged service")
	expectNot(t, page, `action="`+kit+`/delete"`)

	expect(t, a.postV(kit+"/archive", nil), http.StatusSeeOther)
	page = a.get(kit)
	expect(t, page, http.StatusOK, "Archived", `action="`+kit+`/restore"`)
	expectNot(t, page, "Do this service")
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, "Oil service", "badge badge-muted", "status-muted")
	expect(t, a.get("/services"), http.StatusOK, "1 archived kit →")
	expect(t, a.get("/services?archived=1"), http.StatusOK, "Oil service", "← Active kits")
	// Archiving the vehicle hides its kits from the all-vehicles services page,
	// but keeps them visible when filtered to that vehicle.
	expect(t, a.postV("/vehicles/"+itoa(k390)+"/archive", nil), http.StatusSeeOther)
	expectNot(t, a.get("/services"), "archived kit →")
	expect(t, a.get("/services?v="+itoa(k390)), http.StatusOK, "1 archived kit →")
	expect(t, a.postV("/vehicles/"+itoa(k390)+"/restore", nil), http.StatusSeeOther)
	expect(t, a.get("/services"), http.StatusOK, "1 archived kit →")
	expect(t, a.postV(kit+"/restore", nil), http.StatusSeeOther)
	expectNot(t, a.get("/services"), "archived kit →")
}

// A service's parts go and come back with it over HTTP: deleted, undone,
// deleted again, and purged along with its vehicle, the ledger adds up.
func TestServicePartsGoWithTheirService(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	had := a.quantity(t, oil)
	rec := a.post("/vehicles", url.Values{"name": {"Spare bike"}, "kind": {"motorcycle"}})
	expect(t, rec, http.StatusSeeOther)
	v := rec.Header().Get("Location")
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(mustID(t, v))}, "title": {"Top up"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil)}, "part_qty": {"25"}})
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")
	if got := a.quantity(t, oil); got != had-25 {
		t.Fatalf("oil = %v, want %v", got, had-25)
	}

	// A vehicle with a service isn't deleted (its page has no Delete; a
	// post from a page opened before the service still is refused).
	version := strconv.Itoa(cur(t, a.store, store.WhatVehicle, mustID(t, v)))
	if f := flashOf(t, a.post(v+"/delete", url.Values{"version": {version}})); f.Kind != "error" || !strings.Contains(f.Message, "Archive it instead") {
		t.Errorf("vehicle delete flash = %+v", f)
	}
	f := flashOf(t, a.postV(svc+"/delete", nil))
	if f.Undo == 0 || a.quantity(t, oil) != had {
		t.Fatalf("service delete: flash %+v, oil %v", f, a.quantity(t, oil))
	}
	a.ledger(t)
	expect(t, a.post("/trash/"+itoa(f.Undo)+"/restore", nil), http.StatusSeeOther)
	if got := a.quantity(t, oil); got != had-25 {
		t.Errorf("oil after Undo = %v, want %v", got, had-25)
	}
	expect(t, a.get(svc), http.StatusOK, "Top up")
	a.ledger(t)

	expect(t, a.postV(svc+"/delete", nil), http.StatusSeeOther)
	f = flashOf(t, a.postV(v+"/delete", nil))
	if f.Undo == 0 {
		t.Fatalf("vehicle delete flash = %+v", f)
	}
	if msg := flashOf(t, a.post("/trash/"+itoa(f.Undo)+"/purge", nil)).Message; msg != "Permanently deleted, with 1 more entry that needed it" {
		t.Errorf("purge flash = %q", msg)
	}
	if got := a.quantity(t, oil); got != had {
		t.Errorf("oil after the purge = %v, want %v", got, had)
	}
	a.ledger(t)
}

func TestKitAndServiceSuggestions(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()

	// New kit form contains default suggestions (including Clutch Fluid and Cabin Filter, without Chain & sprockets).
	kitNew := a.get("/kits/new")
	expect(t, kitNew, http.StatusOK,
		`<datalist id="kit-names">`,
		`<option value="Clutch Fluid">`,
		`<option value="Cabin Filter">`,
	)
	expectNot(t, kitNew, `<option value="Chain &amp; Sprockets">`, `<option value="Chain & Sprockets">`, `<option value="Chain &amp; sprockets">`, `<option value="Chain & sprockets">`)

	// Service form has matching service-titles suggestions.
	svcNew := a.get("/services/new")
	expect(t, svcNew, http.StatusOK,
		`<datalist id="service-titles">`,
		`<option value="Clutch Fluid">`,
		`<option value="Cabin Filter">`,
	)

	// Custom kit name dynamically appears in future kit and service forms.
	rec := a.post("/kits", url.Values{
		"vehicle_id": {itoa(k390)},
		"name":       {"Fork seals"},
	})
	expect(t, rec, http.StatusSeeOther)

	expect(t, a.get("/kits/new"), http.StatusOK, `<option value="Fork seals">`)
	expect(t, a.get("/services/new"), http.StatusOK, `<option value="Fork seals">`)
}

func TestKitStatusIcons(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene")

	// 1. Kit with no parts (e.g. Valve check).
	rec := a.post("/kits", url.Values{
		"vehicle_id": {itoa(k390)},
		"name":       {"Valve check"},
	})
	expect(t, rec, http.StatusSeeOther)
	valvesPath := rec.Header().Get("Location")

	// On /services: empty kit has status-muted with minus and "No parts" badge.
	svcPage := a.get("/services")
	expect(t, svcPage, http.StatusOK,
		"Valve check",
		`<span class="status-icon status-muted"><svg class="icon" aria-hidden="true"><use href="#i-minus"></use></svg></span>`,
		`<span class="badge badge-muted">No parts</span>`,
	)

	// 2. Kit with short parts.
	rec = a.post("/kits", url.Values{
		"vehicle_id": {itoa(k390)},
		"name":       {"Brake kit"},
		"line_item":  {itoa(oil)},
		"line_qty":   {"9999 L"},
	})
	expect(t, rec, http.StatusSeeOther)
	shortPath := rec.Header().Get("Location")

	svcPage = a.get("/services")
	expect(t, svcPage, http.StatusOK,
		"Brake kit",
		`<span class="status-icon status-short"><svg class="icon" aria-hidden="true"><use href="#i-x"></use></svg></span>`,
		`<span class="badge badge-danger"><svg class="icon" aria-hidden="true"><use href="#i-x"></use></svg> 1 part short</span>`,
	)
	expect(t, a.get(shortPath), http.StatusOK,
		`<td class="status-col"><span class="status-icon status-short"><svg class="icon" aria-hidden="true"><use href="#i-x"></use></svg></span></td>`,
	)

	// 3. Kit with ready parts.
	rec = a.post("/kits", url.Values{
		"vehicle_id": {itoa(k390)},
		"name":       {"Oil service"},
		"line_item":  {itoa(oil)},
		"line_qty":   {"100 ml"},
	})
	expect(t, rec, http.StatusSeeOther)
	readyPath := rec.Header().Get("Location")

	svcPage = a.get("/services")
	expect(t, svcPage, http.StatusOK,
		"Oil service",
		`<span class="status-icon status-ok"><svg class="icon" aria-hidden="true"><use href="#i-check"></use></svg></span>`,
		`<span class="badge badge-ok"><svg class="icon" aria-hidden="true"><use href="#i-check"></use></svg> Parts ready</span>`,
	)
	expect(t, a.get(readyPath), http.StatusOK,
		`<td class="status-col"><span class="status-icon status-ok"><svg class="icon" aria-hidden="true"><use href="#i-check"></use></svg></span></td>`,
		`<h2><svg class="icon" aria-hidden="true"><use href="#i-package"></use></svg> Parts</h2>`,
	)

	// 4. Kit with archived part line in kit_show.html.
	// Archive the item used in the ready kit.
	expect(t, a.postV(fmt.Sprintf("/items/%d/archive", oil), nil), http.StatusSeeOther)
	expect(t, a.get(readyPath), http.StatusOK,
		`<td class="status-col"><span class="status-icon status-muted"><svg class="icon" aria-hidden="true"><use href="#i-archive"></use></svg></span></td>`,
	)

	// 5. Item detail page: "Service kits" shows archived badge inline.
	expect(t, a.post("/services", url.Values{
		"vehicle_id":   {itoa(k390)},
		"kit_id":       {readyPath[len("/kits/"):]},
		"title":        {"Oil service"},
		"performed_on": {"2026-10-29"},
	}), http.StatusSeeOther)
	// 6. Service history: completed service appears in the service log.
	expect(t, a.get("/services"), http.StatusOK,
		`<a class="title" href="/services/`, "Oil service",
	)
	expect(t, a.postV(readyPath+"/archive", nil), http.StatusSeeOther)
	expect(t, a.get(fmt.Sprintf("/items/%d", oil)), http.StatusOK,
		`<span class="title-row"><a class="title" href="`+readyPath+`">Oil service</a><span class="badge badge-muted" title="Archived `,
		`<svg class="icon" aria-hidden="true"><use href="#i-archive"></use></svg> Archived</span></span>`,
	)
	_ = valvesPath
}

func TestVehiclePurchaseBaselineEnforcedAcrossServicesAndOdometer(t *testing.T) {
	a := newApp(t)
	a.seed()
	k390 := a.vehicle(t, "KTM 390")
	vPath := vehiclePath(k390)

	// Set purchase baseline: 2026-06-01 at 1000 km, current odometer 5000 km.
	expect(t, a.postV(vPath, url.Values{
		"name":         {"KTM 390"},
		"kind":         {"motorcycle"},
		"odometer_km":  {"5000"},
		"purchased_on": {"2026-06-01"},
		"purchased_km": {"1000"},
	}), http.StatusSeeOther)

	// SetOdometer below purchased_km is refused.
	rec := a.post(vPath+"/odometer", url.Values{"odometer_km": {"800"}})
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "purchase odometer") {
		t.Fatalf("SetOdometer below purchased_km: flash = %+v", f)
	}

	// Logging a service before purchased_on or below purchased_km is refused.
	rec = a.post("/services", url.Values{
		"vehicle_id":   {itoa(k390)},
		"title":        {"Early service"},
		"performed_on": {"2026-05-01"},
		"odometer_km":  {"1500"},
	})
	expect(t, rec, http.StatusUnprocessableEntity, "Earlier than 1 Jun 2026, when the vehicle was purchased")

	rec = a.post("/services", url.Values{
		"vehicle_id":   {itoa(k390)},
		"title":        {"Low km service"},
		"performed_on": {"2026-07-01"},
		"odometer_km":  {"900"},
	})
	expect(t, rec, http.StatusUnprocessableEntity, "Lower than 1\u202f000 km, the vehicle&#39;s purchase odometer")

	// Raising purchased_km above the current odometer while KeepOdometer is true
	// is refused cleanly instead of failing the DB CHECK.
	expect(t, a.postV(vPath, url.Values{
		"name":         {"KTM 390"},
		"kind":         {"motorcycle"},
		"odometer_km":  {"5000"},
		"odometer_was": {"5000"},
		"purchased_on": {"2026-06-01"},
		"purchased_km": {"6000"},
	}), http.StatusUnprocessableEntity, "Purchase odometer cannot be higher than the current odometer")

	// Deleting a service that moved the odometer from 5000 -> 12000 after
	// purchased_km was raised to 8000 clamps odometer_km to 8000 (not 5000).
	rec = a.post("/services", url.Values{
		"vehicle_id":   {itoa(k390)},
		"title":        {"Big trip service"},
		"performed_on": {"2026-08-01"},
		"odometer_km":  {"12000"},
	})
	expect(t, rec, http.StatusSeeOther)
	svcPath := rec.Header().Get("Location")
	expect(t, a.postV(vPath, url.Values{
		"name":         {"KTM 390"},
		"kind":         {"motorcycle"},
		"odometer_km":  {"12000"},
		"odometer_was": {"12000"},
		"purchased_on": {"2026-06-01"},
		"purchased_km": {"8000"},
	}), http.StatusSeeOther)
	expect(t, a.postV(svcPath+"/delete", nil), http.StatusSeeOther)
	expect(t, a.get(vPath), http.StatusOK, `name="odometer_km" value="8000"`)
}

func TestKitIntermediateShortfallAndItemSuggestion(t *testing.T) {
	a := newApp(t)
	a.seed()
	k390 := a.vehicle(t, "KTM 390")
	oil := itemID(t, a.store, "Silkolene") // 525 ml in stock

	// Create an item with 0 stock used only in the intermediate tier.
	rec := a.post("/items", url.Values{
		"name": {"Intermediate Crush Washer"},
		"unit": {"piece"},
	})
	expect(t, rec, http.StatusSeeOther)
	washerID := mustID(t, rec.Header().Get("Location"))

	// Create a kit where the Full tier is ready (needs 500 ml oil) and the
	// Intermediate tier is short (needs 2 washers), while Full is currently active
	// (no past full service, so NextIsIntermediate is false in warranty mode).
	rec = a.post("/kits", url.Values{
		"vehicle_id":        {itoa(k390)},
		"name":              {"Warranty service"},
		"interval_km":       {"15000"},
		"interval_months":   {"12"},
		"has_inter":         {"1"},
		"inter_interval_km": {"5000"},
		"line_item":         {itoa(oil)},
		"line_qty":          {"500 ml"},
		"inter_line_item":   {itoa(washerID)},
		"inter_line_qty":    {"2"},
	})
	expect(t, rec, http.StatusSeeOther)
	kitURL := rec.Header().Get("Location")

	// kit_show offers the "Add missing to shopping list" button even though the active Full tier is ready.
	expect(t, a.get(kitURL), http.StatusOK, `action="`+kitURL+`/shortfall"`)

	// Clicking "Add to shopping list" on the washer's item page suggests the 2 pcs needed by the intermediate tier.
	expect(t, a.post(itemPath(washerID)+"/shopping", nil), http.StatusSeeOther)
	expect(t, a.get("/shopping"), http.StatusOK, "Intermediate Crush Washer", "2 pcs", "Short for KTM 390: Warranty service")
}

func TestServiceShortPartsShoppingForm(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene") // 525 ml in stock

	// Attempt to log a service needing 1.7 L of oil (short by 1175 ml)
	svc := url.Values{
		"vehicle_id":   {itoa(k390)},
		"title":        {"Oil service"},
		"performed_on": {"2026-10-20"},
		"odometer_km":  {"12000"},
		"part_item":    {itoa(oil)},
		"part_qty":     {"1.7 L"},
	}
	rec := a.post("/services", svc)
	expect(t, rec, http.StatusUnprocessableEntity,
		"Only 525 ml in stock",
		`form="service-short-form"`,
		"Add short parts to shopping list",
		`id="service-short-form"`,
		`value="`+itoa(oil)+`"`,
		`value="1175"`,
		"Short for Oil service",
	)

	// Post the shortfall to /shopping/suggestions as the form button does
	rec = a.post("/shopping/suggestions", url.Values{
		"item_id": {itoa(oil)},
		"qty":     {"1175"},
		"note":    {"Short for Oil service"},
	})
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.get("/shopping"), http.StatusOK, "Silkolene", "1\u202f175 ml", "Short for Oil service")
}

func TestKitPerTierShortfall(t *testing.T) {
	a := newApp(t)
	a.seed()
	k390 := a.vehicle(t, "KTM 390")
	oil := itemID(t, a.store, "Silkolene") // 525 ml in stock

	rec := a.post("/items", url.Values{
		"name": {"Spark Plug LMAR9AI-8"},
		"unit": {"piece"},
	})
	expect(t, rec, http.StatusSeeOther)
	plugID := mustID(t, rec.Header().Get("Location"))

	// Kit has Full tier needing 1.7 L oil (1175 ml short)
	// and Intermediate tier needing 2 spark plugs (2 pcs short)
	rec = a.post("/kits", url.Values{
		"vehicle_id":        {itoa(k390)},
		"name":              {"Major & Minor"},
		"interval_km":       {"15000"},
		"interval_months":   {"12"},
		"has_inter":         {"1"},
		"inter_interval_km": {"5000"},
		"line_item":         {itoa(oil)},
		"line_qty":          {"1.7 L"},
		"inter_line_item":   {itoa(plugID)},
		"inter_line_qty":    {"2"},
	})
	expect(t, rec, http.StatusSeeOther)
	kitURL := rec.Header().Get("Location")

	// 1. Request shortfall for intermediate tier only (intermediate=1)
	expect(t, a.post(kitURL+"/shortfall", url.Values{"intermediate": {"1"}}), http.StatusSeeOther)
	list, err := a.store.ListShopping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ItemID == nil || *list[0].ItemID != plugID {
		t.Fatalf("expected only plug in shopping list, got %+v", list)
	}

	// 2. Request shortfall for full tier only (intermediate=0)
	expect(t, a.post(kitURL+"/shortfall", url.Values{"intermediate": {"0"}}), http.StatusSeeOther)
	list, err = a.store.ListShopping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected both plug and oil in shopping list, got %+v", list)
	}
}

// The service form names the vehicle as a chip, before the kit badge and
// the sentence, only when it can't change on the form; the title names
// the action alone.
func TestServiceFormHeader(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene")
	in := stock.KitInput{VehicleID: k390, Name: "Oil service", IntervalKm: ptr(5000),
		Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 500, Unit: stock.UnitML}}}
	in.Normalize()
	kit, err := a.store.CreateKit(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	page := a.get("/services/new?kit=" + itoa(kit))
	expect(t, page, http.StatusOK, "<h1>Log service</h1>", `title="View vehicle in new tab"`, `title="View kit in new tab"`,
		"<span>The parts below are taken from stock when you save.</span>")
	body := page.Body.String()
	chip, badge, text := strings.Index(body, "View vehicle in new tab"), strings.Index(body, "View kit in new tab"), strings.Index(body, "The parts below")
	if chip > badge || badge > text {
		t.Errorf("want chip, kit badge, sentence in that order: %d, %d, %d", chip, badge, text)
	}
	// From the vehicle, the form's Vehicle field still chooses it: no chip.
	page = a.get("/services/new?vehicle=" + itoa(k390))
	expect(t, page, http.StatusOK, "<h1>Log service</h1>", `<p class="sub">The parts below are taken from stock when you save.</p>`)
	expectNot(t, page, "View vehicle in new tab", "<h1>Log service · ", "<title>Log service · KTM 390")
}
