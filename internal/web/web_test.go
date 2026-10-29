package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

type testApp struct {
	t     *testing.T
	store *store.Store
	h     http.Handler
}

func newApp(t *testing.T) *testApp {
	t.Helper()
	st := store.New(testdb.New(t))
	srv, err := New(st, Options{
		Currency: "EUR", Version: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testApp{t: t, store: st, h: srv.Handler()}
}

func (a *testApp) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a *testApp) get(path string) *httptest.ResponseRecorder {
	return a.do(httptest.NewRequest(http.MethodGet, path, nil))
}

func (a *testApp) post(path string, form url.Values, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return a.do(req)
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, contains ...string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d\n%s", rec.Code, status, truncate(rec.Body.String()))
	}
	body := rec.Body.String()
	for _, c := range contains {
		if !strings.Contains(body, c) {
			t.Errorf("response does not contain %q\n%s", c, truncate(body))
		}
	}
}

func truncate(s string) string {
	if i := strings.Index(s, `<main`); i >= 0 {
		s = s[i:]
	}
	if len(s) > 3000 {
		return s[:3000] + "…"
	}
	return s
}

// seed creates the example inventory from the original request.
func (a *testApp) seed() (k390, k1290 int64) {
	a.t.Helper()
	ctx := context.Background()
	mk := func(name string) int64 {
		in := stock.VehicleInput{Name: name, OdometerKm: ptr(10000)}
		in.Normalize()
		id, err := a.store.CreateVehicle(ctx, in)
		if err != nil {
			a.t.Fatal(err)
		}
		return id
	}
	k390, k1290 = mk("KTM 390"), mk("KTM 1290")
	item := func(brand, name string, unit stock.Unit, qty float64, vids ...int64) {
		in := stock.ItemInput{Brand: brand, Name: name, Unit: unit, Quantity: qty, VehicleIDs: vids}
		in.Normalize()
		if _, err := a.store.CreateItem(ctx, in); err != nil {
			a.t.Fatal(err)
		}
	}
	item("OEM", "Rear Brake Pads", stock.UnitPiece, 1, k390)
	item("Silkolene", "15W50 PRO 4 XP", stock.UnitML, 525, k390)
	item("Hiflofiltro", "Oil Filter", stock.UnitPiece, 1, k1290)
	item("Motorex", "10W50 Power Synt 4T", stock.UnitML, 1500, k1290)
	item("OEM", "Oil Filter O-Ring", stock.UnitPiece, 6, k390, k1290)
	item("Motorex", "M3.0 Ready To Use", stock.UnitML, 640, k390, k1290)
	return k390, k1290
}

func ptr[T any](v T) *T { return &v }

func itemID(t *testing.T, s *store.Store, query string) int64 {
	t.Helper()
	items, err := s.ListItems(context.Background(), store.ItemFilter{Query: query})
	if err != nil || len(items) != 1 {
		t.Fatalf("find %q: %v (%d results)", query, err, len(items))
	}
	return items[0].ID
}

func TestEmptyPagesRender(t *testing.T) {
	a := newApp(t)
	for _, p := range []string{"/", "/stock", "/stock?view=list", "/vehicles", "/vehicles/new", "/items/new",
		"/services", "/services/new", "/kits/new", "/shopping", "/activity"} {
		t.Run(p, func(t *testing.T) { expect(t, a.get(p), http.StatusOK, "</html>") })
	}
	expect(t, a.get("/"), http.StatusOK, "Welcome to Stock Items")
}

func TestPagesRenderWithData(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene")

	expect(t, a.get("/"), http.StatusOK, "KTM 390", "Dashboard")
	expect(t, a.get("/stock"), http.StatusOK, "Rear Brake Pads", "Shared", "Copy as text")
	expect(t, a.get("/stock?view=list&sort=quantity"), http.StatusOK, "Hiflofiltro")
	expect(t, a.get("/vehicles"), http.StatusOK, "KTM 1290")
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, "Service kits", "Silkolene")
	expect(t, a.get("/vehicles/"+itoa(k390)+"/edit"), http.StatusOK, `value="KTM 390"`)
	expect(t, a.get("/items/"+itoa(oil)), http.StatusOK, "525 ml", "Initial stock")
	expect(t, a.get("/items/"+itoa(oil)+"/edit"), http.StatusOK, `value="525"`)
	expect(t, a.get("/kits/new?vehicle="+itoa(k390)), http.StatusOK, "For KTM 390")
	expect(t, a.get("/services/new?vehicle="+itoa(k390)), http.StatusOK, `value="10000"`)
}

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

	rec = a.get("/stock/export.csv")
	expect(t, rec, http.StatusOK, "id,brand,name", "Hiflofiltro")
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
	expect(t, a.get(loc), http.StatusOK, "Motul 7100 10W50", "4 L", "104100", "keep at least 1 L")

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

	// Duplicate and delete.
	rec = a.post(loc+"/duplicate", nil)
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.get(rec.Header().Get("Location")), http.StatusOK, "7100 10W50 (copy)")
	expect(t, a.post(loc+"/delete", nil), http.StatusSeeOther)
	expect(t, a.get(loc), http.StatusNotFound)
}

func TestMovementsAndAdjust(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	path := "/items/" + itoa(oil)

	expect(t, a.post(path+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"}, "total_price": {"19,90"}, "supplier": {"Moto shop"}}), http.StatusSeeOther)
	expect(t, a.post(path+"/movements", url.Values{"kind": {"use"}, "amount": {"5"}, "amount_unit": {"l"}}), http.StatusUnprocessableEntity, "Only 1.525 L on stock")
	expect(t, a.post(path+"/movements", url.Values{"kind": {"use"}, "amount": {"25"}, "amount_unit": {"ml"}}), http.StatusSeeOther)
	expect(t, a.post(path+"/movements", url.Values{"kind": {"correction"}, "amount": {"1.2"}, "amount_unit": {"l"}}), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "1.2 L", "Moto shop", "19.90 EUR")

	// Quick adjust via htmx returns the row and a toast.
	rec := a.post(path+"/adjust", url.Values{"dir": {"down"}}, "HX-Request", "true")
	expect(t, rec, http.StatusOK, `id="item-`+itoa(oil)+`"`, "1.1 L")
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "toast") {
		t.Errorf("missing toast trigger: %v", rec.Header())
	}
	// Without htmx it redirects back.
	req := httptest.NewRequest(http.MethodPost, path+"/adjust", strings.NewReader("dir=up"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "http://example.com/stock?low=1")
	rec = a.do(req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/stock?low=1" {
		t.Errorf("adjust redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	expect(t, a.get("/activity"), http.StatusOK, "Quick adjust", "Purchase")
}

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
	expect(t, a.get(kitPath), http.StatusOK, "1 part short", "1.175 L", "Not done yet")
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
	expect(t, a.get(entry+"/bought"), http.StatusOK, `value="1.175"`)
	expect(t, a.post(entry+"/bought", url.Values{"amount": {"4"}, "amount_unit": {"l"}, "total_price": {"45"}}), http.StatusSeeOther)
	expect(t, a.get("/shopping/export.txt"), http.StatusOK, "Shopping list")

	rec = a.post("/services", svc)
	expect(t, rec, http.StatusSeeOther)
	svcPath := rec.Header().Get("Location")
	expect(t, a.get(svcPath), http.StatusOK, "1.7 L", "12,000 km")
	expect(t, a.get(kitPath), http.StatusOK, "Last done", "19,500 km")
	expect(t, a.get("/services"), http.StatusOK, "Oil service")

	v, _ := a.store.GetVehicle(context.Background(), k390)
	if *v.OdometerKm != 12000 {
		t.Errorf("odometer = %d", *v.OdometerKm)
	}
	expect(t, a.post(svcPath+"/delete", nil), http.StatusSeeOther)
	if it, _ := a.store.GetItem(context.Background(), oil); it.Quantity != 4525 {
		t.Errorf("oil after deleting service = %v", it.Quantity)
	}

	// Free text entries and suggestions.
	expect(t, a.post("/shopping", url.Values{"free_text": {"Chain lube"}, "quantity": {"2"}}), http.StatusSeeOther)
	expect(t, a.post("/shopping", url.Values{}), http.StatusUnprocessableEntity, "Pick an item or describe")
	expect(t, a.post("/shopping/suggestions", url.Values{"item_id": {itoa(pads)}, "qty": {"2"}}), http.StatusSeeOther)
	expect(t, a.get("/shopping"), http.StatusOK, "Chain lube", "OEM Rear Brake Pads")

	expect(t, a.post(kitPath+"/delete", nil), http.StatusSeeOther)
}

func TestVehicleFlow(t *testing.T) {
	a := newApp(t)
	expect(t, a.post("/vehicles", url.Values{"name": {""}, "color": {"nope"}}), http.StatusUnprocessableEntity, "Name is required")
	rec := a.post("/vehicles", url.Values{"name": {"Golf"}, "kind": {"car"}, "color": {"#2563eb"}, "year": {"2015"}, "odometer_km": {"150 000"}})
	expect(t, rec, http.StatusSeeOther)
	path := rec.Header().Get("Location")
	expect(t, a.get(path), http.StatusOK, "Golf", "2015", `value="150000"`)
	expect(t, a.post("/vehicles", url.Values{"name": {"golf"}}), http.StatusUnprocessableEntity, "already exists")
	expect(t, a.post(path+"/odometer", url.Values{"odometer_km": {"151000"}}), http.StatusSeeOther)
	expect(t, a.post(path, url.Values{"name": {"VW Golf"}, "kind": {"car"}, "color": {"#2563eb"}}), http.StatusSeeOther)
	expect(t, a.get("/vehicles"), http.StatusOK, "VW Golf")
	expect(t, a.post(path+"/delete", nil), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusNotFound)
}

func TestErrorsAndInfrastructure(t *testing.T) {
	a := newApp(t)
	expect(t, a.get("/items/999999"), http.StatusNotFound, "Not found")
	expect(t, a.get("/items/abc"), http.StatusNotFound)
	expect(t, a.get("/does-not-exist"), http.StatusNotFound)
	expect(t, a.get("/healthz"), http.StatusOK, `"status":"ok"`)

	rec := a.get("/static/app.css?v=x")
	expect(t, rec, http.StatusOK, "--accent")
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if csp := a.get("/").Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}

	// Cross-site form posts are rejected.
	rec = a.post("/vehicles", url.Values{"name": {"Evil"}}, "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST status = %d, want 403", rec.Code)
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
