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
	expect(t, a.post("/services", svc), http.StatusUnprocessableEntity, "Only 525 ml on stock")

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
		`action="`+svc+`"`, "Save changes", "on stock: 525 ml") // 425 ml left + the 100 ml this service returns

	expect(t, a.postV(svc, url.Values{"vehicle_id": {"999"}, "title": {"Oil top-up"}, "performed_on": {"2026-10-29"},
		"odometer_km": {"11000"}, "labor_cost": {"100"}, "part_item": {itoa(oil)}, "part_qty": {"5 L"}}),
		http.StatusUnprocessableEntity, "Only 525 ml on stock")
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

	// Prefilled rows come with their unit and what is on stock.
	edit := a.get(kit + "/edit")
	expect(t, edit, http.StatusOK, `name="line_qty" value="1.7"`, `<option value="l" data-base="ml" selected>L</option>`, "on stock: 525 ml")

	// Every short part is marked at once.
	form := url.Values{"vehicle_id": {itoa(k390)}, "title": {"Oil service"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil), itoa(pads)}, "part_qty": {"2", "3"}, "part_unit": {"l", ""}}
	expect(t, a.post("/services", form), http.StatusUnprocessableEntity, "2 parts are short", "Only 525 ml on stock", "Only 1x on stock")
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
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, "Oil service <span class=\"badge badge-muted\">Archived</span>")
	expect(t, a.postV(kit+"/restore", nil), http.StatusSeeOther)
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
