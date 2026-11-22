package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// optionOf is the <option> of item id in a page's item list.
func optionOf(t *testing.T, body string, id int64) string {
	t.Helper()
	m := regexp.MustCompile(`<option [^>]*data-id="` + itoa(id) + `"[^>]*>`).FindString(body)
	if m == "" {
		t.Fatalf("no option for item %d", id)
	}
	return m
}

// retag gives an item other vehicles, as its edit form saved would.
func retag(t *testing.T, a *testApp, id int64, vehicleIDs ...int64) {
	t.Helper()
	it, err := a.store.GetItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	in := stock.ItemInput{Brand: it.Brand, Name: it.Name, Unit: it.Unit, Quantity: it.Quantity, QuantityWas: &it.Quantity,
		VehicleIDs: vehicleIDs, Version: it.Version}
	in.Normalize()
	if err := a.store.UpdateItem(context.Background(), id, in, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// Every item list lists the items in the stock page's sections, each with
// its vehicles, for app.js to offer and head for the vehicle in scope:
// For it, then General; with none, Only …, Shared …, General.
func TestItemListsGroupLikeTheStockPage(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	pads, hiflo, ring := itemID(t, a.store, "Rear Brake"), itemID(t, a.store, "Hiflofiltro"), itemID(t, a.store, "O-Ring")
	rec := a.post("/items", url.Values{"brand": {"Motip"}, "name": {"Brake Cleaner"}, "unit": {"piece"}})
	expect(t, rec, http.StatusSeeOther)
	cleaner := mustID(t, rec.Header().Get("Location"))
	want := map[int64]string{
		pads:    `data-vehicles="` + itoa(k390) + `" data-group="Only KTM 390"`,
		hiflo:   `data-vehicles="` + itoa(k1290) + `" data-group="Only KTM 1290"`,
		ring:    `data-vehicles="` + itoa(k390) + ` ` + itoa(k1290) + `" data-group="Shared KTM 390 &#43; KTM 1290"`,
		cleaner: `data-vehicles="" data-group="General"`,
	}
	for _, path := range []string{"/kits/new", "/services/new", "/shopping"} {
		body := a.get(path).Body.String()
		for id, w := range want {
			if o := optionOf(t, body, id); !strings.Contains(o, w) {
				t.Errorf("%s: item %d's option %s, want %s", path, id, o, w)
			}
		}
		// In the stock page's order: one vehicle's, shared, General.
		if i, j, k := strings.Index(body, `data-id="`+itoa(pads)+`"`), strings.Index(body, `data-id="`+itoa(ring)+`"`),
			strings.Index(body, `data-id="`+itoa(cleaner)+`"`); i >= j || j >= k {
			t.Errorf("%s: options out of the stock page's order", path)
		}
		// No vehicle fixed by the page: the forms' Vehicle chips choose it,
		// and the whole shopping list has none.
		expectNot(t, a.get(path), `data-for=`)
	}
	// A vehicle's part of the shopping list offers its items, For it.
	body := a.get("/shopping?v=" + itoa(k390)).Body.String()
	if !strings.Contains(body, `<datalist id="shopping-items" data-for="`+itoa(k390)+`" data-for-name="KTM 390">`) {
		t.Error("a vehicle's part doesn't name its vehicle to its item list")
	}
	if strings.Contains(body, `data-id="`+itoa(hiflo)+`"`) {
		t.Error("KTM 390's part offers KTM 1290's item")
	}
	// A form that doesn't ask for the vehicle names it on its hidden field.
	expect(t, a.getWorking("/kits/new", itoa(k1290)), http.StatusOK, `<input type="hidden" name="vehicle_id" value="`+itoa(k1290)+`" data-name="KTM 1290">`)
}

// A kit or service form's Vehicle is single-choice chips: the one asked
// for (or the sole vehicle) picked, kept on a refused save, its error
// under it.
func TestVehicleChips(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	for _, path := range []string{"/kits/new", "/services/new"} {
		body := a.get(path).Body.String()
		if vehicleChecked(body, k390) || vehicleChecked(body, k1290) {
			t.Errorf("%s: a vehicle is picked though none was asked for", path)
		}
		expect(t, a.get(path), http.StatusOK, `<fieldset class="field span-all" id="vehicle_id">`, `<legend>Vehicle <span class="req"`,
			`<label class="chip chip-toggle" style="--chip:`, `<input type="radio" name="vehicle_id" value="`+itoa(k390)+`" data-name="KTM 390" data-odometer=`)
		if !vehicleChecked(a.get(path+"?vehicle="+itoa(k1290)).Body.String(), k1290) {
			t.Errorf("%s: the vehicle asked for isn't picked", path)
		}
	}
	// Refused for another field: the vehicle stays picked.
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k1290)}, "name": {""}})
	expect(t, rec, http.StatusUnprocessableEntity)
	if !vehicleChecked(rec.Body.String(), k1290) {
		t.Error("a refused kit form drops its vehicle")
	}
	// None picked: the error is the field's.
	rec = a.post("/services", url.Values{"title": {"Wash"}, "performed_on": {"2026-10-20"}})
	expect(t, rec, http.StatusUnprocessableEntity, `<fieldset class="field span-all has-error" id="vehicle_id" aria-describedby="vehicle_id-err">`,
		`<p class="field-error" id="vehicle_id-err">`)
}

// With one vehicle, a new kit or service is for it.
func TestSoleVehicleIsPicked(t *testing.T) {
	a := newApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"Only bike"}, "kind": {"motorcycle"}})
	expect(t, rec, http.StatusSeeOther)
	only := mustID(t, rec.Header().Get("Location"))
	for _, path := range []string{"/kits/new", "/services/new"} {
		if !vehicleChecked(a.get(path).Body.String(), only) {
			t.Errorf("%s: the only vehicle isn't picked", path)
		}
	}
}

// A part must fit the vehicle: tagged with it, or General. Shared items
// fit each of theirs. An item retagged since a kit used it is flagged on
// the kit's next save, not dropped.
func TestPartsFitTheVehicle(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	ring := itemID(t, a.store, "O-Ring") // both bikes'
	rec := a.post("/items", url.Values{"brand": {"Motip"}, "name": {"Brake Cleaner"}, "unit": {"piece"}, "quantity": {"3"}})
	expect(t, rec, http.StatusSeeOther)
	cleaner := mustID(t, rec.Header().Get("Location"))
	for _, v := range []int64{k390, k1290} {
		expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(v)}, "name": {"Shared and General"},
			"line_item": {itoa(ring), itoa(cleaner)}, "line_qty": {"1", "1"}}), http.StatusSeeOther)
		expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(v)}, "title": {"Clean"}, "performed_on": {"2026-10-20"},
			"part_item": {""}, "part_item_text": {"motip brake cleaner"}, "part_qty": {"1"}}), http.StatusSeeOther)
	}
	// Another vehicle's item, on a service: picked or typed.
	hiflo := itemID(t, a.store, "Hiflofiltro")
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Filter"}, "performed_on": {"2026-10-20"},
		"part_item": {itoa(hiflo)}, "part_item_text": {"Hiflofiltro Oil Filter"}, "part_qty": {"1"}}), http.StatusUnprocessableEntity,
		"“Hiflofiltro Oil Filter” isn&#39;t for KTM 390: pick one from the list")

	// Retagged after a kit used it: the kit's edit form says so at the row.
	pads := itemID(t, a.store, "Rear Brake")
	rec = a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"}, "line_item": {itoa(pads)}, "line_qty": {"1"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	retag(t, a, pads, k1290)
	k, err := a.store.GetKit(context.Background(), mustID(t, kit))
	if err != nil {
		t.Fatal(err)
	}
	rec = a.post(kit, url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"}, "version": {itoa(int64(k.Version))},
		"line_item": {itoa(pads)}, "line_item_text": {"OEM Rear Brake Pads"}, "line_qty": {"1"}})
	expect(t, rec, http.StatusUnprocessableEntity, "“OEM Rear Brake Pads” isn&#39;t for KTM 390: pick one from the list",
		`name="line_item_text" value="OEM Rear Brake Pads"`)
}

// Shopping offers what each list's part is for, as every item list: a
// vehicle's part its items and the General ones; "Already have it?" too,
// with each item's vehicles as a stock row shows them.
func TestShoppingOffersThePartsItems(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	ctx := context.Background()
	// Two items of one name, one per bike.
	kit := map[int64]int64{}
	for _, v := range []int64{k390, k1290} {
		rec := a.post("/items", url.Values{"brand": {"KTM"}, "name": {"Oil Filter Service Kit"}, "unit": {"piece"}, "vehicle": {itoa(v)}})
		expect(t, rec, http.StatusSeeOther)
		kit[v] = mustID(t, rec.Header().Get("Location"))
	}
	// The whole list: two match, so it is something new.
	expect(t, a.post("/shopping", url.Values{"item": {"KTM Oil Filter Service Kit"}}), http.StatusSeeOther)
	// KTM 390's part: the name is its item.
	expect(t, a.post("/shopping", url.Values{"v": {itoa(k390)}, "item": {"KTM Oil Filter Service Kit"}, "quantity": {"2"}}), http.StatusSeeOther)
	var free int64
	for _, e := range mustShopping(t, a) {
		switch {
		case e.ItemID == nil:
			free = e.ID
		case *e.ItemID != kit[k390]:
			t.Errorf("KTM 390's part added item %d, want its own %d", *e.ItemID, kit[k390])
		}
	}
	if free == 0 {
		t.Fatal("the name two items have wasn't added as something new")
	}
	// Picked from KTM 390's part, then retagged: refused.
	retag(t, a, kit[k390], k1290)
	expect(t, a.post("/shopping", url.Values{"v": {itoa(k390)}, "item": {"KTM Oil Filter Service Kit"}, "item_id": {itoa(kit[k390])}}),
		http.StatusUnprocessableEntity, "“KTM Oil Filter Service Kit” isn&#39;t for KTM 390: pick one from the list")
	retag(t, a, kit[k390], k390)

	page := "/shopping/" + itoa(free) + "/bought"
	chip := func(v string) string { return `<span class="dot"></span>` + v + `</span>` }
	// From the whole list: both, each with its chip.
	all := a.get(page)
	expect(t, all, http.StatusOK, "Already have it?", chip("KTM 390"), chip("KTM 1290"))
	if n := strings.Count(all.Body.String(), "Add to this item"); n != 2 {
		t.Errorf("whole list: %d matches offered, want 2", n)
	}
	// From KTM 390's part: its own, without its chip.
	part := a.get(page + "?v=" + itoa(k390))
	expect(t, part, http.StatusOK, "Already have it?", `name="item_id" value="`+itoa(kit[k390])+`"`)
	expectNot(t, part, `name="item_id" value="`+itoa(kit[k1290])+`"`, chip("KTM 390"), chip("KTM 1290"))
	// Linking another vehicle's from a part (a page opened before a
	// retag) is refused.
	e, err := a.store.GetShopping(ctx, free)
	if err != nil {
		t.Fatal(err)
	}
	rec := a.post("/shopping/"+itoa(free)+"/link", url.Values{"v": {itoa(k390)}, "item_id": {itoa(kit[k1290])}, "version": {itoa(int64(e.Version))}})
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "isn't for that vehicle") {
		t.Errorf("linking another vehicle's item: flash %+v", f)
	}
	if e, _ := a.store.GetShopping(ctx, free); e.ItemID != nil {
		t.Error("another vehicle's item was linked")
	}
	// The new-item form from the entry offers both, with their chips.
	expect(t, a.get("/items/new?shopping="+itoa(free)), http.StatusOK, chip("KTM 390"), chip("KTM 1290"))
}
