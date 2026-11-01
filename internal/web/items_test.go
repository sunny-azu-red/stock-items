package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

func TestStockFilterAndExport(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()

	rec := a.get("/stock?v=" + itoa(k390))
	expect(t, rec, http.StatusOK, "KTM 390 stock", "Rear Brake Pads", "M3.0 Ready To Use",
		`data-copy-url="/stock/export.txt?v=`+itoa(k390)+`"`, `href="/stock/export.csv?v=`+itoa(k390)+`"`)
	if strings.Contains(rec.Body.String(), "Hiflofiltro") {
		t.Error("KTM 1290-only item shown in KTM 390 filter")
	}

	want := "KTM 390\n------------\n1x - OEM Rear Brake Pads\n525 ml - Silkolene 15W50 PRO 4 XP\n\n\n" +
		"KTM 390 + KTM 1290\n------------------\n640 ml - Motorex M3.0 Ready To Use\n6x - OEM Oil Filter O-Ring\n"
	rec = a.get("/stock/export.txt?v=" + itoa(k390))
	expect(t, rec, http.StatusOK)
	if rec.Body.String() != want {
		t.Errorf("export.txt =\n%s\nwant\n%s", rec.Body.String(), want)
	}

	oil := itemID(t, a.store, "Silkolene")
	expect(t, a.post("/items/"+itoa(oil)+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
		"total_price": {"19.90"}, "supplier": {"Moto shop"}, "occurred_on": {"2026-10-29"}}), http.StatusSeeOther)
	rec = a.get("/stock/export.csv")
	expect(t, rec, http.StatusOK, "id,brand,name", "last_price,price_per,stock_value,last_bought_on,last_shop", "Hiflofiltro",
		",19.90,L,30.35,2026-10-29,Moto shop")
	if ct := rec.Header().Get("Content-Disposition"); !strings.Contains(ct, "stock-2026-10-29.csv") {
		t.Errorf("Content-Disposition = %q", ct)
	}
	expect(t, a.get("/stock?q=nothing-like-this"), http.StatusOK, "Nothing matches")
}

func TestItemFormFlow(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()

	// Validation errors re-render the form with the typed values.
	rec := a.post("/items", url.Values{"name": {""}, "unit": {"ml"}, "quantity": {"2 kg"}, "brand": {"Motul"}})
	expect(t, rec, http.StatusUnprocessableEntity, "Name is required", "kg cannot be used", `value="Motul"`)

	form := url.Values{
		"name": {"7100 10W50"}, "brand": {"Motul"}, "unit": {"ml"}, "quantity": {"4"}, "quantity_unit": {"l"},
		"min_quantity": {"1"}, "min_unit": {"l"}, "vehicle": {itoa(k390), itoa(k1290)},
		"sku_code": {"104098", "104100", ""}, "sku_kind": {"manufacturer", "manufacturer", "oem"},
		"sku_label": {"1 L", "4 L", ""}, "sku_pack": {"1", "4", ""}, "sku_pack_unit": {"l", "l", "ml"},
	}
	rec = a.post("/items", form)
	expect(t, rec, http.StatusSeeOther)
	loc := rec.Header().Get("Location")
	expect(t, a.get(loc), http.StatusOK, "Motul 7100 10W50", "4 L", "104100", "keep at least 1 L",
		`href="https://duckduckgo.com/?q=104100&#43;site%3Ade" target="_blank"`, `title="Search on duckduckgo.com (site:de)"`)
	expect(t, a.get("/stock"), http.StatusOK, `— Search on duckduckgo.com (site:de)"`)

	it, err := a.store.GetItem(context.Background(), itemID(t, a.store, "104098"))
	if err != nil {
		t.Fatal(err)
	}
	if it.Quantity != 4000 || len(it.SKUs) != 2 || *it.SKUs[1].PackSize != 4000 || len(it.VehicleIDs) != 2 {
		t.Errorf("saved item = %+v", it)
	}

	// Duplicate SKUs are rejected on update.
	form.Set("sku_code", "")
	form["sku_code"] = []string{"104098", "104098", ""}
	expect(t, a.post(loc, form), http.StatusUnprocessableEntity, "Duplicate SKU code")

	// Save & add another keeps vehicles and unit.
	for _, k := range []string{"sku_code", "sku_label", "sku_pack"} {
		form[k] = []string{"", "", ""}
	}
	form.Set("name", "Another oil")
	form.Set("next", "new")
	rec = a.post("/items", form)
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); !strings.HasPrefix(l, "/items/new?") || !strings.Contains(l, "unit=ml") {
		t.Errorf("next location = %q", l)
	}

	// Duplicate starts a new item without saving anything, then delete.
	before, _ := a.store.ListItems(context.Background(), store.ItemFilter{})
	dup := a.get("/items/new?from=" + loc[len("/items/"):])
	expect(t, dup, http.StatusOK, `value="7100 10W50 (copy)"`, `value="Motul"`, `value="104098"`, `name="quantity" value=""`)
	after, _ := a.store.ListItems(context.Background(), store.ItemFilter{})
	if len(after) != len(before) {
		t.Errorf("duplicate saved an item: %d → %d", len(before), len(after))
	}
	expect(t, a.postV(loc+"/delete", nil), http.StatusSeeOther)
	expect(t, a.get(loc), http.StatusNotFound)
}

// itemAtMax creates an item holding stock.MaxQuantity pieces.
func itemAtMax(t *testing.T, a *testApp) int64 {
	t.Helper()
	in := stock.ItemInput{Name: "Washers", Unit: stock.UnitPiece, Quantity: stock.MaxQuantity}
	in.Normalize()
	id, err := a.store.CreateItem(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStockFilterDetails(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	// Duplicate ids must not break "fits all".
	expect(t, a.get("/stock?v="+itoa(k390)+"&v="+itoa(k390)+"&match=all"), http.StatusOK, "Rear Brake Pads")
	// The parts htmx swaps out of band are always present.
	rec := a.get("/stock?v=" + itoa(k390) + "&v=" + itoa(k1290))
	expect(t, rec, http.StatusOK, `id="stock-head"`, `id="filter-all"`, `id="match-toggle"`, "Fits all")
	// Without JS every stock change form is reachable.
	expect(t, a.get("/items/"+itoa(itemID(t, a.store, "Silkolene"))), http.StatusOK, `[role="tabpanel"][hidden] { display: grid !important; }`)
}

func TestItemEditFormDoesNotUndoStockChanges(t *testing.T) {
	a := newApp(t)
	a.seed()
	id := itemID(t, a.store, "Silkolene")
	path := "/items/" + itoa(id)
	edit := a.get(path + "/edit")
	expect(t, edit, http.StatusOK, `name="quantity_was" value="525"`)
	version := formVersion(t, edit.Body.String(), path)
	// Someone uses 25 ml, and quick-adds some, while the form is open...
	expect(t, a.post(path+"/movements", url.Values{"kind": {"use"}, "amount": {"25"}}), http.StatusSeeOther)
	expect(t, a.post(path+"/adjust", url.Values{"dir": {"up"}}), http.StatusSeeOther)
	expect(t, a.post(path+"/adjust", url.Values{"dir": {"down"}}), http.StatusSeeOther)
	// ...then the form is saved with the quantity it showed. Stock changes
	// don't make the form stale.
	form := url.Values{"name": {"15W50 PRO 4 XP"}, "brand": {"Silkolene"}, "unit": {"ml"}, "quantity": {"525"},
		"quantity_unit": {"ml"}, "quantity_was": {"525"}, "notes": {"Changed"}, "version": {version}}
	expect(t, a.post(path, form), http.StatusSeeOther)
	if it, _ := a.store.GetItem(context.Background(), id); it.Quantity != 500 || it.Notes != "Changed" {
		t.Errorf("quantity = %v notes = %q, want 500 and Changed", it.Quantity, it.Notes)
	}
}

func TestSKUErrorsLandOnTheirRow(t *testing.T) {
	a := newApp(t)
	form := url.Values{"name": {"Filter"}, "unit": {"piece"},
		"sku_code": {"   ", "123"}, "sku_kind": {"oem", "ean"}, "sku_label": {"", ""}, "sku_pack": {"", ""}, "sku_pack_unit": {"pcs", "pcs"}}
	rec := a.post("/items", form)
	expect(t, rec, http.StatusUnprocessableEntity, "Not a valid barcode")
	body := rec.Body.String()
	row := strings.Index(body, `value="123"`)
	msg := strings.Index(body, "Not a valid barcode")
	if row < 0 || msg < row {
		t.Errorf("barcode error is not on the row with the code (row at %d, message at %d)", row, msg)
	}
}

func TestNewItemBoughtJustNow(t *testing.T) {
	a := newApp(t)
	expect(t, a.get("/items/new"), http.StatusOK, "Bought it just now?", `name="bought_on" value="2026-10-29"`)

	form := url.Values{"name": {"M3.0 Ready To Use"}, "brand": {"Motorex"}, "unit": {"ml"}, "quantity": {"1"}, "quantity_unit": {"l"},
		"total_price": {"67"}, "supplier": {"moto24"}, "bought_on": {"2026-10-29"}}
	rec := a.post("/items", form)
	expect(t, rec, http.StatusSeeOther)
	page := a.get(rec.Header().Get("Location"))
	expect(t, page, http.StatusOK, "67.00 EUR / L", "<dt>Purchases</dt><dd>1</dd>", "moto24", "Purchase")
	if st, _ := a.store.GetStats(context.Background(), time.Now()); st.StockValue != 67 || st.Spent12m != 67 {
		t.Errorf("stats = %+v, want 67 value and spending", st)
	}

	// A price needs an amount, and a purchase can't be in the future.
	bad := url.Values{"name": {"Coolant"}, "unit": {"ml"}, "total_price": {"20"}}
	expect(t, a.post("/items", bad), http.StatusUnprocessableEntity, "Enter how much you bought", `value="20"`)
	bad = url.Values{"name": {"Coolant"}, "unit": {"ml"}, "quantity": {"1"}, "supplier": {"Shop"}, "bought_on": {"2026-11-05"}}
	expect(t, a.post("/items", bad), http.StatusUnprocessableEntity, "Date cannot be in the future")

	// Without a price or shop it is stock that was already there.
	expect(t, a.post("/items", url.Values{"name": {"Old grease"}, "unit": {"g"}, "quantity": {"300"}, "bought_on": {"2026-10-29"}}), http.StatusSeeOther)
	expect(t, a.get("/items/"+itoa(itemID(t, a.store, "Old grease"))), http.StatusOK, "Initial stock", "<dt>Purchases</dt><dd>0</dd>")
}

func TestItemEditForm(t *testing.T) {
	a := newApp(t)
	a.seed()
	id := itemID(t, a.store, "Silkolene")
	path := "/items/" + itoa(id)
	rec := a.get(path + "/edit")
	expect(t, rec, http.StatusOK)
	expectNot(t, rec, "Bought it just now?", `name="total_price"`)

	// A blank quantity must not silently empty the stock.
	form := url.Values{"name": {"15W50 PRO 4 XP"}, "brand": {"Silkolene"}, "unit": {"ml"}, "quantity": {""},
		"quantity_unit": {"ml"}, "quantity_was": {"525"}}
	expect(t, a.postV(path, form), http.StatusUnprocessableEntity, "Enter the amount on stock (0 if none)")
	form.Set("quantity", "0")
	expect(t, a.postV(path, form), http.StatusSeeOther)
	if it, _ := a.store.GetItem(context.Background(), id); it.Quantity != 0 {
		t.Errorf("quantity = %v, want 0 after an explicit 0", it.Quantity)
	}
}

func TestItemPageCountsAllPurchases(t *testing.T) {
	a := newApp(t)
	a.seed()
	id := itemID(t, a.store, "Rear Brake")
	// More purchases than the item page loads history rows for.
	for range 101 {
		if err := a.store.AddMovement(context.Background(), stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: 1}); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, a.get("/items/"+itoa(id)), http.StatusOK, "<dt>Purchases</dt><dd>101</dd>")
}

func TestEmptyItemsAreNotExpiring(t *testing.T) {
	a := newApp(t)
	expired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	in := stock.ItemInput{Name: "Used up coolant", Unit: stock.UnitML, ExpiresOn: &expired}
	in.Normalize()
	id, err := a.store.CreateItem(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, a.get("/"), http.StatusOK, `Expiring</span><span class="value">0</span>`)
	expectNot(t, a.get("/items/"+itoa(id)), "Expired")
}

func TestArchiveItemFlow(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	ring := itemID(t, a.store, "O-Ring")
	path := "/items/" + itoa(oil)

	// Unused: Delete, no Archive.
	page := a.get(path)
	expect(t, page, http.StatusOK, `action="`+path+`/delete"`)
	expectNot(t, page, `action="`+path+`/archive"`)

	// In a kit: Archive instead of Delete.
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(oil), itoa(ring)}, "line_qty": {"1.7 L", "1"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	page = a.get(path)
	expect(t, page, http.StatusOK, `action="`+path+`/archive"`, "stays in 1 kit")
	expectNot(t, page, `action="`+path+`/delete"`)
	rec = a.post(path+"/delete", url.Values{"version": {a.versionOf(path + "/archive")}}) // a hand-made POST
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != path {
		t.Errorf("refused delete went to %q", l)
	}
	if _, err := a.store.GetItem(ctx, oil); err != nil {
		t.Fatalf("item in a kit was deleted: %v", err)
	}

	expect(t, a.postV(path+"/archive", nil), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Archived", `action="`+path+`/restore"`)
	stockPage := a.get("/stock")
	expectNot(t, stockPage, "15W50 PRO 4 XP")
	expect(t, a.get("/stock?archived=1"), http.StatusOK, "15W50 PRO 4 XP")
	expect(t, a.get(kit), http.StatusOK, "15W50 PRO 4 XP", "badge-muted\" title=\"This item is archived")

	// Saving the kit keeps the archived line.
	expect(t, a.get(kit+"/edit"), http.StatusOK, `value="`+itoa(oil)+`" `)
	expect(t, a.postV(kit, url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(oil), itoa(ring)}, "line_qty": {"1.7 L", "1"}}), http.StatusSeeOther)
	if k, _ := a.store.GetKit(ctx, mustID(t, kit)); len(k.Lines) != 2 {
		t.Errorf("kit lines after save = %+v", k.Lines)
	}
	// Its shortfall is not bought again.
	expect(t, a.post(kit+"/shortfall", nil), http.StatusSeeOther)
	if list, _ := a.store.ListShopping(ctx); len(list) != 0 {
		t.Errorf("shopping = %+v", list)
	}

	expect(t, a.postV(path+"/restore", nil), http.StatusSeeOther)
	expect(t, a.get("/stock"), http.StatusOK, "15W50 PRO 4 XP")

	// On the shopping list: archiving says where the entry went.
	expect(t, a.post(path+"/shopping", nil), http.StatusSeeOther)
	rec = a.postV(path+"/archive", nil)
	if f := flashOf(t, rec); !strings.Contains(f.Message, "shopping list entry is in the trash") {
		t.Errorf("flash = %+v", f)
	}
	expect(t, a.get("/trash"), http.StatusOK, "Shopping list: ")
}

func TestArchivedItemsTakeNoStockChanges(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	pads := itemID(t, a.store, "Rear Brake")
	item := "/items/" + itoa(pads)
	if _, err := a.store.ArchiveItem(ctx, pads, cur(t, a.store, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	page := a.get(item)
	expect(t, page, http.StatusOK, "Restore this item to record purchases")
	expectNot(t, page, `id="panel-purchase"`)
	expectNot(t, a.get("/stock?archived=1"), `action="`+item+`/adjust"`)

	rec := a.post(item+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}})
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind != "error" {
		t.Errorf("flash = %+v", f)
	}
	rec = a.post(item+"/adjust", url.Values{"dir": {"up"}}, "HX-Request", "true")
	expect(t, rec, http.StatusOK)
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "Restore") {
		t.Errorf("toast = %q", rec.Header().Get("HX-Trigger"))
	}
	if it, _ := a.store.GetItem(ctx, pads); it.Quantity != 1 {
		t.Errorf("quantity = %v, want 1", it.Quantity)
	}

	// A kit that only misses the archived part is ready, and logging it
	// from the kit leaves that part out.
	rec = a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"},
		"line_item": {itoa(pads), itoa(itemID(t, a.store, "O-Ring"))}, "line_qty": {"4", "1"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	kitPage := a.get(kit)
	expect(t, kitPage, http.StatusOK, "Parts ready")
	expectNot(t, kitPage, "Add missing to shopping list")
	form := a.get("/services/new?kit=" + kit[len("/kits/"):])
	ring := itoa(itemID(t, a.store, "O-Ring"))
	expect(t, form, http.StatusOK, `<option value="`+ring+`" data-have="6x" data-placeholder="e.g. 2" data-unit="piece" selected>`)
	expectNot(t, form, `<option value="`+itoa(pads)+`" data-have="1x" data-placeholder="e.g. 2" data-unit="piece" selected>`)
}

func TestQuickAdjustRowKeepsChipsHidden(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	rec := a.post("/items/"+itoa(pads)+"/adjust", url.Values{"dir": {"up"}, "hide_chips": {"1"}}, "HX-Request", "true")
	expect(t, rec, http.StatusOK, `name="hide_chips" value="1"`)
	expectNot(t, rec, `class="chip`)
	rec = a.post("/items/"+itoa(pads)+"/adjust", url.Values{"dir": {"up"}}, "HX-Request", "true")
	expect(t, rec, http.StatusOK, `class="chip`)
}

func TestAmountErrorsSayWhy(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	expect(t, a.post("/items/"+itoa(pads)+"/movements", url.Values{"kind": {"use"}, "amount": {"1.5"}}),
		http.StatusUnprocessableEntity, "Pieces must be a whole number")
	expect(t, a.post("/items/"+itoa(pads)+"/movements", url.Values{"kind": {"use"}, "amount": {""}}),
		http.StatusUnprocessableEntity, "Enter an amount, e.g. 2")
}

func TestStockFilterLinksKeepTheView(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	rec := a.get("/stock?v=" + itoa(k390) + "&q=oil&view=list&sort=quantity&cat=FILTERS")
	expect(t, rec, http.StatusOK,
		`id="filter-all" href="/stock?cat=FILTERS&amp;q=oil&amp;sort=quantity&amp;view=list"`,
		`href="/stock?sort=quantity&amp;view=list">clear filters</a>`,
		`<option value="quantity" selected>Quantity</option>`, "<option selected>Filters</option>")
}

func TestArchivedItemHistoryAndExports(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	pads := itemID(t, a.store, "Rear Brake")
	moves, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: pads})
	if _, err := a.store.ArchiveItem(ctx, pads, cur(t, a.store, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	rec := a.get("/movements/" + itoa(moves[0].ID) + "/edit")
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind != "error" {
		t.Errorf("flash = %+v", f)
	}
	// A hand-made POST, as the page isn't shown.
	expect(t, a.post("/movements/"+itoa(moves[0].ID)+"/delete", url.Values{"version": {strconv.Itoa(moves[0].Version)}}), http.StatusSeeOther)
	if it, _ := a.store.GetItem(ctx, pads); it.Quantity != 1 {
		t.Errorf("quantity = %v", it.Quantity)
	}

	// An archived vehicle keeps its name in the exports.
	if err := a.store.ArchiveVehicle(ctx, k390, cur(t, a.store, store.WhatVehicle, k390)); err != nil {
		t.Fatal(err)
	}
	expect(t, a.get("/stock/export.csv"), http.StatusOK, "KTM 390; KTM 1290")
	expect(t, a.get("/stock/export.txt?view=list"), http.StatusOK, "KTM 390 + KTM 1290")
}

func TestDuplicateLeavesBarcodesAndExpiry(t *testing.T) {
	a := newApp(t)
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	in := stock.ItemInput{Name: "Coolant", Unit: stock.UnitML, Quantity: 1000, ExpiresOn: &exp,
		SKUs: []stock.SKU{{Code: "7611197108900", Kind: stock.SKUBarcode}, {Code: "M30", Kind: stock.SKUManufacturer}}}
	in.Normalize()
	id, err := a.store.CreateItem(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	rec := a.get("/items/new?from=" + itoa(id))
	expect(t, rec, http.StatusOK, `value="M30"`, `name="expires_on" value=""`)
	expectNot(t, rec, "7611197108900")
}

// Blanking the name of something being edited keeps its saved name in
// the heading and title.
func TestEditHeadingsKeepTheSavedName(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"}})
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")
	for _, tt := range []struct{ path, want string }{
		{"/items/" + itoa(pads), "Edit OEM Rear Brake Pads"},
		{"/vehicles/" + itoa(k390), "Edit KTM 390"},
		{kit, "Edit Brakes"},
		{svc, "Edit Chain clean"},
	} {
		expect(t, a.get(tt.path+"/edit"), http.StatusOK, "<title>"+tt.want)
		page := a.post(tt.path, url.Values{"vehicle_id": {itoa(k390)}, "unit": {"piece"}, "quantity": {"1"}, "performed_on": {"2026-10-29"}})
		expect(t, page, http.StatusUnprocessableEntity, "<title>"+tt.want)
		if tt.path != svc { // a service's heading is "Edit service · vehicle"
			expect(t, page, http.StatusUnprocessableEntity, "<h1>"+tt.want+"</h1>")
		}
	}
}
