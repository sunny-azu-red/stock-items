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
	expect(t, a.get("/"), http.StatusOK, "Welcome to Pitbox")
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
		`href="https://www.google.com/search?q=104100&#43;site%3Aro" target="_blank"`)

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

func TestTheme(t *testing.T) {
	a := newApp(t)
	expect(t, a.get("/"), http.StatusOK, `<html lang="en">`, `content="light dark"`, `value="dark"`, `value="light"`)

	// A plain form post stores the cookie and goes back.
	req := httptest.NewRequest(http.MethodPost, "/theme", strings.NewReader("theme=dark"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "http://example.com/stock?low=1")
	rec := a.do(req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/stock?low=1" {
		t.Fatalf("theme redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != themeCookie || cookies[0].Value != "dark" || cookies[0].MaxAge <= 0 {
		t.Fatalf("theme cookie = %+v", cookies)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	expect(t, a.do(req), http.StatusOK, `<html lang="en" data-theme="dark">`, `name="color-scheme" content="dark"`)

	// app.js posts "system" to forget the choice; it gets a bare 204.
	rec = a.post("/theme", url.Values{"theme": {"system"}}, "HX-Request", "true")
	expect(t, rec, http.StatusNoContent)
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("theme cookie not cleared: %+v", c)
	}

	// Unknown cookie values are ignored.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: themeCookie, Value: `"><script>`})
	expect(t, a.do(req), http.StatusOK, `<html lang="en">`)
}

// TestHostileInput posts values people (or scripts) might send by accident
// or on purpose. None of them may end in a 500 or store nonsense.
func TestHostileInput(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene")
	pads := itemID(t, a.store, "Rear Brake")
	item := "/items/" + itoa(oil)

	cases := []struct {
		name   string
		path   string
		form   url.Values
		status int
	}{
		{"NaN price", item + "/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "total_price": {"NaN"}}, 422},
		{"Inf price", item + "/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "total_price": {"Inf"}}, 422},
		{"huge price", item + "/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "total_price": {"1e20"}}, 422},
		{"huge amount", item + "/movements", url.Values{"kind": {"purchase"}, "amount": {"99999999999"}}, 422},
		{"forged kind", item + "/movements", url.Values{"kind": {"initial"}, "amount": {"1"}}, 422},
		{"huge item quantity", "/items", url.Values{"name": {"X"}, "unit": {"piece"}, "quantity": {"1"}, "min_quantity": {"9999999999"}}, 422},
		{"huge odometer", "/vehicles", url.Values{"name": {"Big"}, "odometer_km": {"9999999999"}}, 422},
		{"huge odometer update", "/vehicles/" + itoa(k390) + "/odometer", url.Values{"odometer_km": {"9999999999"}}, 303},
		{"huge interval", "/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"K"}, "interval_km": {"99999999999"}}, 422},
		{"huge labour", "/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"S"}, "performed_on": {"2026-10-01"}, "labor_cost": {"1e15"}}, 422},
		{"NaN suggestion", "/shopping/suggestions", url.Values{"item_id": {itoa(pads)}, "qty": {"NaN"}}, 303},
		{"huge suggestion", "/shopping/suggestions", url.Values{"item_id": {itoa(pads)}, "qty": {"1e20"}}, 303},
		{"gone suggestion", "/shopping/suggestions", url.Values{"item_id": {"999999"}, "qty": {"1"}}, 303},
		{"huge shopping qty", "/shopping", url.Values{"free_text": {"Lube"}, "quantity": {"1e12"}}, 422},
		{"tiny suggestion", "/shopping/suggestions", url.Values{"item_id": {itoa(pads)}, "qty": {"0.0001"}}, 303},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := a.post(c.path, c.form)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d\n%s", rec.Code, c.status, truncate(rec.Body.String()))
			}
		})
	}
	list, err := a.store.ListShopping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("bad suggestions were stored: %+v", list)
	}
	if it, _ := a.store.GetItem(context.Background(), oil); it.Quantity != 525 {
		t.Errorf("oil quantity = %v, want 525", it.Quantity)
	}

	// A zero quantity falls back to "no quantity" instead of failing.
	expect(t, a.post("/items/"+itoa(pads)+"/shopping", url.Values{"quantity": {"0"}}), http.StatusSeeOther)

	// Quick + on an item that is already at the maximum.
	full := itemAtMax(t, a)
	rec := a.post("/items/"+itoa(full)+"/adjust", url.Values{"dir": {"up"}}, "HX-Request", "true")
	expect(t, rec, http.StatusOK)
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "error") {
		t.Errorf("want an error toast, got %q", rec.Header().Get("HX-Trigger"))
	}
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

func TestSafeBackRejectsOffSiteTargets(t *testing.T) {
	for ref, want := range map[string]string{
		"http://example.com/stock?low=1":         "/stock?low=1",
		"http://example.com/%5Cevil.example":     "/%5Cevil.example", // stays escaped: a path, not a host
		"http://example.com/%09/evil.example":    "/%09/evil.example",
		"http://example.com//evil.example":       "/fallback",
		"http://evil.example/stock":              "/fallback",
		"http://example.com/items/1%2F2?x=a%20b": "/items/1%2F2?x=a%20b",
	} {
		r := httptest.NewRequest(http.MethodPost, "/theme", nil)
		r.Header.Set("Referer", ref)
		if got := safeBack(r, "/fallback"); got != want {
			t.Errorf("safeBack(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestParseOptInt(t *testing.T) {
	for in, want := range map[string]int{"12 345": 12345, "12,345": 12345, "12.345": 12345, "1_000_000": 1000000, "7500": 7500, "-5": -5} {
		got, err := parseOptInt(in)
		if err != nil || *got != want {
			t.Errorf("parseOptInt(%q) = %v, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"1.5", "12,34", "1 2", "abc", "99999999999"} {
		if got, err := parseOptInt(in); err == nil {
			t.Errorf("parseOptInt(%q) = %d, want an error", in, *got)
		}
	}
}

func TestFormatDecimal(t *testing.T) {
	for in, want := range map[float64]string{0: "0.00", 2.995: "3.00", 1234.5: "1,234.50", -0.001: "0.00", -12.5: "-12.50"} {
		if got := formatDecimal(in); got != want {
			t.Errorf("formatDecimal(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestOversizedFormIs413(t *testing.T) {
	a := newApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"Big"}, "notes": {strings.Repeat("x", maxFormBytes)}})
	expect(t, rec, http.StatusRequestEntityTooLarge, "too large")
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
	expect(t, a.get(path+"/edit"), http.StatusOK, `name="quantity_was" value="525"`)
	// Someone uses 25 ml while the form is open...
	expect(t, a.post(path+"/movements", url.Values{"kind": {"use"}, "amount": {"25"}}), http.StatusSeeOther)
	// ...then the form is saved with the quantity it showed.
	form := url.Values{"name": {"15W50 PRO 4 XP"}, "brand": {"Silkolene"}, "unit": {"ml"}, "quantity": {"525"},
		"quantity_unit": {"ml"}, "quantity_was": {"525"}, "notes": {"Changed"}}
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

func expectNot(t *testing.T, rec *httptest.ResponseRecorder, absent ...string) {
	t.Helper()
	for _, s := range absent {
		if strings.Contains(rec.Body.String(), s) {
			t.Errorf("response should not contain %q", s)
		}
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
	if st, _ := a.store.GetStats(context.Background()); st.StockValue != 67 || st.Spent12m != 67 {
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
	expect(t, a.post(path, form), http.StatusUnprocessableEntity, "Enter the amount on stock (0 if none)")
	form.Set("quantity", "0")
	expect(t, a.post(path, form), http.StatusSeeOther)
	if it, _ := a.store.GetItem(context.Background(), id); it.Quantity != 0 {
		t.Errorf("quantity = %v, want 0 after an explicit 0", it.Quantity)
	}
}

func TestBuySomethingNewFromShoppingList(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	expect(t, a.post("/shopping", url.Values{"free_text": {"Chain lube"}, "quantity": {"2"}, "note": {"the white one"}}), http.StatusSeeOther)
	list, _ := a.store.ListShopping(ctx)
	if len(list) != 1 {
		t.Fatalf("shopping = %+v", list)
	}
	entry := itoa(list[0].ID)
	expect(t, a.get("/shopping"), http.StatusOK, `href="/shopping/`+entry+`/bought"`)
	expect(t, a.get("/shopping/"+entry+"/bought"), http.StatusOK,
		"Just tick it off", "Add to stock as a new item", `href="/items/new?shopping=`+entry+`"`)

	rec := a.get("/items/new?shopping=" + entry)
	expect(t, rec, http.StatusOK, `value="Chain lube"`, `name="shopping_id" value="`+entry+`"`, `name="quantity" value="2"`, "the white one")
	expectNot(t, rec, "Save &amp; add another")

	form := url.Values{"shopping_id": {entry}, "name": {"Chain lube"}, "unit": {"piece"}, "quantity": {"2"}, "quantity_unit": {"pcs"},
		"total_price": {"60"}, "supplier": {"Moto shop"}, "bought_on": {"2026-10-29"}}
	rec = a.post("/items", form)
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != "/shopping" {
		t.Errorf("location = %q, want /shopping", l)
	}
	if list, _ := a.store.ListShopping(ctx); len(list) != 0 {
		t.Errorf("entry should be gone: %+v", list)
	}
	expect(t, a.get("/items/"+itoa(itemID(t, a.store, "Chain lube"))), http.StatusOK, "30.00 EUR / piece", "<dt>Purchases</dt><dd>1</dd>")

	// Submitting again creates no second item.
	expect(t, a.post("/items", form), http.StatusUnprocessableEntity, "no longer exists")
	expect(t, a.get("/items/new?shopping="+entry), http.StatusNotFound)

	// An entry for an existing item keeps the purchase form.
	pads := itemID(t, a.store, "Rear Brake")
	linked, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads})
	expect(t, a.get("/items/new?shopping="+itoa(linked)), http.StatusNotFound)
	expect(t, a.get("/shopping/"+itoa(linked)+"/bought"), http.StatusOK, `name="amount"`, `name="total_price"`)

	// Or just tick a free-text entry off.
	free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Zip ties"})
	expect(t, a.post("/shopping/"+itoa(free)+"/bought", nil), http.StatusSeeOther)
	if _, err := a.store.GetShopping(ctx, free); err == nil {
		t.Error("ticked-off entry should be gone")
	}
}

func TestShoppingPartialPurchaseAndBestBefore(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(4000.0)})
	path := "/shopping/" + itoa(entry) + "/bought"

	expect(t, a.post(path, url.Values{"amount": {"1"}, "amount_unit": {"l"}, "occurred_on": {"2062-10-29"}}),
		http.StatusUnprocessableEntity, "Date cannot be in the future")
	expect(t, a.post(path, url.Values{"amount": {"1"}, "amount_unit": {"l"}, "expires_on": {"2029-06-30"}}), http.StatusSeeOther)
	e, err := a.store.GetShopping(ctx, entry)
	if err != nil || *e.Quantity != 3000 {
		t.Errorf("entry = %+v, %v; want 3 L still on the list", e, err)
	}
	it, _ := a.store.GetItem(ctx, oil)
	if it.Quantity != 1525 || it.ExpiresOn == nil || it.ExpiresOn.Format("2006-01-02") != "2029-06-30" {
		t.Errorf("oil = %v expiring %v", it.Quantity, it.ExpiresOn)
	}

	// The same from the item page; a use can't move the date.
	item := "/items/" + itoa(oil)
	expect(t, a.get(item), http.StatusOK, `name="expires_on"`, "Best before")
	expect(t, a.post(item+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"}, "expires_on": {"2030-01-31"}}), http.StatusSeeOther)
	expect(t, a.post(item+"/movements", url.Values{"kind": {"use"}, "amount": {"100"}, "amount_unit": {"ml"}, "expires_on": {"2020-01-01"}}), http.StatusSeeOther)
	expect(t, a.post(item+"/movements", url.Values{"kind": {"use"}, "amount": {"100"}, "amount_unit": {"ml"}, "occurred_on": {"1980-01-01"}}),
		http.StatusUnprocessableEntity, "Date is too far in the past")
	if it, _ := a.store.GetItem(ctx, oil); it.ExpiresOn.Format("2006-01-02") != "2030-01-31" {
		t.Errorf("expiry = %v, want 2030-01-31", it.ExpiresOn)
	}
}

func TestServiceDateIsChecked(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-12-01"}}),
		http.StatusUnprocessableEntity, "Date cannot be in the future")
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"}}),
		http.StatusSeeOther)
}

func TestSuggestionKeepsItsReason(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	expect(t, a.post("/shopping/suggestions", url.Values{"item_id": {itoa(pads)}, "qty": {"2"}, "note": {"Below minimum"}}), http.StatusSeeOther)
	list, _ := a.store.ListShopping(context.Background())
	if len(list) != 1 || list[0].Note != "Below minimum" {
		t.Errorf("shopping = %+v", list)
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

func TestFreeTextShoppingUnits(t *testing.T) {
	a := newApp(t)
	expect(t, a.post("/shopping", url.Values{"free_text": {"Coolant"}, "quantity": {"4 L"}}), http.StatusSeeOther)
	expect(t, a.post("/shopping", url.Values{"free_text": {"Grease"}, "quantity": {"2 bananas"}}), http.StatusUnprocessableEntity, "unknown unit")
	list, _ := a.store.ListShopping(context.Background())
	if len(list) != 1 {
		t.Fatalf("shopping = %+v", list)
	}
	expect(t, a.get("/shopping"), http.StatusOK, "4 L")
	rec := a.get("/items/new?shopping=" + itoa(list[0].ID))
	expect(t, rec, http.StatusOK, `name="quantity" value="4"`, `value="ml" checked`, `value="l" data-base="ml" selected`)
}

func TestDeletingAServicePutsTheOdometerBack(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"}, "odometer_km": {"120000"}})
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, `name="odometer_km" value="120000"`)
	expect(t, a.post(rec.Header().Get("Location")+"/delete", nil), http.StatusSeeOther)
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, `name="odometer_km" value="10000"`)
}

func TestActivityExportCSV(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	expect(t, a.post("/items/"+itoa(oil)+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
		"total_price": {"19.90"}, "supplier": {"Moto shop"}}), http.StatusSeeOther)
	expect(t, a.get("/activity?kind=purchase"), http.StatusOK, `href="/activity/export.csv?kind=purchase"`)

	rec := a.get("/activity/export.csv?kind=purchase")
	expect(t, rec, http.StatusOK, "date,item_id,item,kind,change", "Silkolene 15W50 PRO 4 XP,purchase,1000,+1 L,ml,19.90,19.90,L,Moto shop")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	if strings.Contains(rec.Body.String(), ",initial,") {
		t.Error("kind filter not applied")
	}
	expect(t, a.get("/activity/export.csv"), http.StatusOK, ",initial,")
}

func TestEditAndDeleteMovement(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	item := "/items/" + itoa(oil)
	expect(t, a.post(item+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
		"total_price": {"199"}, "supplier": {"Moto shop"}, "occurred_on": {"2026-10-29"}}), http.StatusSeeOther)
	purchases, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MovePurchase})
	edit := "/movements/" + itoa(purchases[0].ID)

	// Service parts have no edit link, other rows do.
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Top up"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil)}, "part_qty": {"25"}})
	expect(t, rec, http.StatusSeeOther)
	services, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveService})
	page := a.get(item)
	expect(t, page, http.StatusOK, `href="`+edit+`/edit"`)
	expectNot(t, page, `href="/movements/`+itoa(services[0].ID)+`/edit"`)
	expect(t, a.get("/movements/"+itoa(services[0].ID)+"/edit"), http.StatusNotFound)
	expect(t, a.get("/activity"), http.StatusOK, `href="`+edit+`/edit"`)

	expect(t, a.get(edit+"/edit"), http.StatusOK, "Edit purchase", `value="199.00"`, `value="Moto shop"`, `name="amount" value="1"`)
	expect(t, a.post(edit, url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"19.90"}, "occurred_on": {"2027-01-01"}}),
		http.StatusUnprocessableEntity, "Date cannot be in the future", `value="19.90"`)
	rec = a.post(edit, url.Values{"amount": {"2"}, "amount_unit": {"l"}, "total_price": {"39.80"}, "supplier": {"Moto shop"}, "occurred_on": {"2026-10-29"}})
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != item {
		t.Errorf("location = %q, want %q", l, item)
	}
	expect(t, a.get(item), http.StatusOK, "19.90 EUR / L", "2.5 L")

	// Taking away more than is left is refused with a hint.
	expect(t, a.post(item+"/movements", url.Values{"kind": {"use"}, "amount": {"2.4"}, "amount_unit": {"l"}}), http.StatusSeeOther)
	expect(t, a.post(edit+"/delete", nil), http.StatusUnprocessableEntity, "Only 100 ml is left on stock")

	uses, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveUse})
	expect(t, a.post("/movements/"+itoa(uses[0].ID)+"/delete", nil), http.StatusSeeOther)
	expect(t, a.post(edit+"/delete", nil), http.StatusSeeOther)
	if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 500 {
		t.Errorf("quantity = %v, want 500 (525 initial - 25 service)", it.Quantity)
	}
	expect(t, a.get(edit+"/edit"), http.StatusNotFound)
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
	rec = a.post(path+"/delete", nil)
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != path {
		t.Errorf("refused delete went to %q", l)
	}
	if _, err := a.store.GetItem(ctx, oil); err != nil {
		t.Fatalf("item in a kit was deleted: %v", err)
	}

	expect(t, a.post(path+"/archive", nil), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "Archived", `action="`+path+`/restore"`)
	stockPage := a.get("/stock")
	expectNot(t, stockPage, "15W50 PRO 4 XP")
	expect(t, a.get("/stock?archived=1"), http.StatusOK, "15W50 PRO 4 XP")
	expect(t, a.get(kit), http.StatusOK, "15W50 PRO 4 XP", "badge-muted\" title=\"This item is archived")

	// Saving the kit keeps the archived line.
	expect(t, a.get(kit+"/edit"), http.StatusOK, `value="`+itoa(oil)+`" `)
	expect(t, a.post(kit, url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(oil), itoa(ring)}, "line_qty": {"1.7 L", "1"}}), http.StatusSeeOther)
	if k, _ := a.store.GetKit(ctx, mustID(t, kit)); len(k.Lines) != 2 {
		t.Errorf("kit lines after save = %+v", k.Lines)
	}
	// Its shortfall is not bought again.
	expect(t, a.post(kit+"/shortfall", nil), http.StatusSeeOther)
	if list, _ := a.store.ListShopping(ctx); len(list) != 0 {
		t.Errorf("shopping = %+v", list)
	}

	expect(t, a.post(path+"/restore", nil), http.StatusSeeOther)
	expect(t, a.get("/stock"), http.StatusOK, "15W50 PRO 4 XP")
}

func mustID(t *testing.T, path string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(path[strings.LastIndex(path, "/")+1:], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
