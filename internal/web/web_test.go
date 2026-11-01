package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

type testApp struct {
	t     *testing.T
	store *store.Store
	pool  *pgxpool.Pool
	h     http.Handler
}

func newApp(t *testing.T) *testApp {
	t.Helper()
	return newAppAt(t, func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) })
}

// newAppAt is newApp with the given clock.
func newAppAt(t *testing.T, now func() time.Time) *testApp {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	srv, err := New(st, Options{
		Currency: "EUR", Version: "test",
		SKUSearchURL: "https://duckduckgo.com/?q={q}", SKUSearchPrefix: "site:de",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testApp{t: t, store: st, pool: pool, h: srv.Handler()}
}

func (a *testApp) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a *testApp) get(path string) *httptest.ResponseRecorder {
	return a.do(httptest.NewRequest(http.MethodGet, path, nil))
}

// post sends form as a page opened just now would: with a new one-time
// form key, unless form has one (an empty one to send none).
func (a *testApp) post(path string, form url.Values, headers ...string) *httptest.ResponseRecorder {
	if !form.Has("form_key") {
		sent := url.Values{"form_key": {newFormKey()}}
		for k, v := range form {
			sent[k] = v
		}
		form = sent
	}
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

// cur reads a record's current version, for changes made "in another
// tab" straight through the store (0 when the record is gone).
func cur(t *testing.T, s *store.Store, what string, id int64) int {
	t.Helper()
	ctx := context.Background()
	var v int
	var err error
	switch what {
	case store.WhatItem:
		var it stock.Item
		it, err = s.GetItem(ctx, id)
		v = it.Version
	case store.WhatVehicle:
		var x stock.Vehicle
		x, err = s.GetVehicle(ctx, id)
		v = x.Version
	case store.WhatShopping:
		var e stock.ShoppingEntry
		e, err = s.GetShopping(ctx, id)
		v = e.Version
	default:
		t.Fatalf("cur: %s", what)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	return v
}

// postV posts form the way the page showing it does: with the version
// that page's form carries (see formVersion). Use post to send a form
// without one, or with a version taken earlier (a stale tab).
func (a *testApp) postV(path string, form url.Values, headers ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("version", a.versionOf(path))
	return a.post(path, form, headers...)
}

// versionOf opens the page with the form that posts to action, as a user
// would, and returns the version that form carries.
func (a *testApp) versionOf(action string) string {
	a.t.Helper()
	page := formPage(action)
	rec := a.get(page)
	if rec.Code != http.StatusOK {
		a.t.Fatalf("GET %s (for the form posting to %s) = %d", page, action, rec.Code)
	}
	return formVersion(a.t, rec.Body.String(), action)
}

// formsPosting returns the inside of each form in body that posts to
// action, in page order; it fails when there is none.
func formsPosting(t *testing.T, body, action string) []string {
	t.Helper()
	var forms []string
	for _, m := range regexp.MustCompile(`(?s)<form[^>]*action="`+regexp.QuoteMeta(action)+`"[^>]*>(.*?)</form>`).FindAllStringSubmatch(body, -1) {
		forms = append(forms, m[1])
	}
	if forms == nil {
		t.Fatalf("no form posts to %s", action)
	}
	return forms
}

var versionField = regexp.MustCompile(`name="version" value="([0-9]+)"`)

// formVersion finds the form posting to action in a page and returns its
// version field.
func formVersion(t *testing.T, body, action string) string {
	t.Helper()
	v := versionField.FindStringSubmatch(formsPosting(t, body, action)[0])
	if v == nil {
		t.Fatalf("the form posting to %s carries no version", action)
	}
	return v[1]
}

// formPage is the page that shows the form posting to action: an edit
// form (/items/5 → /items/5/edit), a record's buttons (/items/5/delete →
// /items/5), a shopping entry's Bought page, or the shopping list.
func formPage(action string) string {
	parts := strings.Split(strings.Trim(action, "/"), "/")
	switch {
	case len(parts) == 2, parts[0] == "movements":
		return "/" + parts[0] + "/" + parts[1] + "/edit"
	case parts[0] == "shopping" && parts[2] == "delete":
		return "/shopping"
	case parts[0] == "shopping":
		return "/shopping/" + parts[1] + "/bought"
	}
	return "/" + parts[0] + "/" + parts[1]
}

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
	// Copy as text is a link to the text, so it works without JS too.
	expect(t, a.get("/stock"), http.StatusOK, "Rear Brake Pads", "Shared", `href="/stock/export.txt" data-copy-url="/stock/export.txt"`)
	expect(t, a.get("/stock?view=list&sort=quantity"), http.StatusOK, "Hiflofiltro")
	expect(t, a.get("/vehicles"), http.StatusOK, "KTM 1290")
	expect(t, a.get("/vehicles/"+itoa(k390)), http.StatusOK, "Service kits", "Silkolene")
	expect(t, a.get("/vehicles/"+itoa(k390)+"/edit"), http.StatusOK, `value="KTM 390"`)
	expect(t, a.get("/items/"+itoa(oil)), http.StatusOK, "525 ml", "Initial stock")
	expect(t, a.get("/items/"+itoa(oil)+"/edit"), http.StatusOK, `value="525"`)
	expect(t, a.get("/kits/new?vehicle="+itoa(k390)), http.StatusOK, "For KTM 390")
	expect(t, a.get("/services/new?vehicle="+itoa(k390)), http.StatusOK, `value="10000"`)
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
	if cookies[0].HttpOnly {
		t.Error("theme cookie is HttpOnly: app.js can't re-apply it on a page Back restores")
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
		{"grouped price", item + "/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "total_price": {"1.500"}}, 422},
		{"grouped amount", item + "/movements", url.Values{"kind": {"purchase"}, "amount": {"1,500"}, "amount_unit": {"ml"}}, 422},
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
	for in, want := range map[string]int{"12 345": 12345, "12\u202f345": 12345, "12,345": 12345, "12.345": 12345, "1_000_000": 1000000, "7500": 7500, "-5": -5} {
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

func TestParseOptMoney(t *testing.T) {
	for in, want := range map[string]float64{"12,50": 12.5, "12.5": 12.5, "1500": 1500, "1500,00": 1500, ".5": 0.5, "0,500": 0.5, "1.5000": 1.5} {
		got, err := parseOptMoney(in)
		if err != nil || *got != want {
			t.Errorf("parseOptMoney(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	// "1.500" from a shop that groups thousands with a point is not 1.50.
	for _, in := range []string{"1.500", "1,500", "12.000", "1.234,56", "NaN", "1e3", "-5"} {
		if got, err := parseOptMoney(in); err == nil {
			t.Errorf("parseOptMoney(%q) = %v, want an error", in, *got)
		}
	}
	if got, err := parseOptMoney("  "); got != nil || err != nil {
		t.Errorf("blank: %v, %v", got, err)
	}
}

func TestFormatDecimal(t *testing.T) {
	for in, want := range map[float64]string{0: "0.00", 2.995: "3.00", 1234.5: "1\u202f234.50", -0.001: "0.00", -12.5: "-12.50"} {
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

func expectNot(t *testing.T, rec *httptest.ResponseRecorder, absent ...string) {
	t.Helper()
	for _, s := range absent {
		if strings.Contains(rec.Body.String(), s) {
			t.Errorf("response should not contain %q", s)
		}
	}
}

func mustID(t *testing.T, path string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(path[strings.LastIndex(path, "/")+1:], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// flashOf decodes the flash message a response sets.
func flashOf(t *testing.T, rec *httptest.ResponseRecorder) Flash {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name != flashCookie || c.MaxAge < 0 {
			continue
		}
		b, err := base64.RawURLEncoding.DecodeString(c.Value)
		var f Flash
		if err == nil {
			err = json.Unmarshal(b, &f)
		}
		if err != nil {
			t.Fatalf("flash cookie %q: %v", c.Value, err)
		}
		return f
	}
	return Flash{}
}

func TestBackSkipsPostOnlyPages(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	item := "/items/" + itoa(pads)
	// A failed form re-renders at /items/{id}/movements; going back there would 404.
	for _, ref := range []string{"http://example.com" + item + "/movements", "http://example.com/items"} {
		rec := a.post(item+"/shopping", nil, "Referer", ref)
		expect(t, rec, http.StatusSeeOther)
		if l := rec.Header().Get("Location"); l != item {
			t.Errorf("from %s: location = %q, want %q", ref, l, item)
		}
		rec = a.post("/theme", url.Values{"theme": {"dark"}}, "Referer", ref)
		if l := rec.Header().Get("Location"); l != "/" {
			t.Errorf("theme from %s: location = %q, want /", ref, l)
		}
	}
	rec := a.post(item+"/shopping", nil, "Referer", "http://example.com/stock?low=1")
	if l := rec.Header().Get("Location"); l != "/stock?low=1" {
		t.Errorf("location = %q, want /stock?low=1", l)
	}
}

func TestFlashesCountRealChanges(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	add := url.Values{"item_id": {itoa(pads)}, "qty": {"2"}}
	if f := flashOf(t, a.post("/shopping/suggestions", add)); f.Message != "Added 1 item to the shopping list" {
		t.Errorf("first add: %+v", f)
	}
	if f := flashOf(t, a.post("/shopping/suggestions", add)); !strings.Contains(f.Message, "already has it") {
		t.Errorf("second add: %+v", f)
	}
}

func TestPolish(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene")
	item := "/items/" + itoa(oil)

	for _, path := range []string{"/stock/export.csv", "/activity/export.csv"} {
		if cc := a.get(path).Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s Cache-Control = %q", path, cc)
		}
	}
	if f := flashOf(t, a.post(item+"/photo/delete", nil)); f.Message != "There was no photo to remove" {
		t.Errorf("photo delete flash = %+v", f)
	}
	// An item that is gone: a message, not a Not found page.
	gone := a.post("/items/424242/photo/delete", nil)
	expect(t, gone, http.StatusSeeOther)
	if f := flashOf(t, gone); f.Kind != "error" || !strings.Contains(f.Message, "deleted") {
		t.Errorf("photo delete of a gone item: flash = %+v", f)
	}

	expect(t, a.get("/stock"), http.StatusOK, `aria-label="Use 100 ml of Silkolene 15W50 PRO 4 XP"`, `aria-label="Add 1x of OEM Rear Brake Pads"`)

	// A bad date typed in one tab doesn't show up in the others.
	page := a.post(item+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "occurred_on": {"2062-01-01"}})
	expect(t, page, http.StatusUnprocessableEntity, `id="date-purchase" name="occurred_on" value="2062-01-01"`, `id="date-use" name="occurred_on" value="2026-10-29"`)

	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Top up"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil)}, "part_qty": {"25"}})
	expect(t, a.get(rec.Header().Get("Location")), http.StatusOK, "<th>Item</th>")
}

// Posts about things that changed meanwhile, say in another tab, end in
// a message where the user was, not an error page.
func TestRacesEndInMessages(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	pads := itemID(t, a.store, "Rear Brake")
	path := "/items/" + itoa(pads)

	// Put on the list after it was archived in another tab.
	if _, err := a.store.ArchiveItem(ctx, pads, cur(t, a.store, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	rec := a.post(path+"/shopping", nil)
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "archived or deleted meanwhile") {
		t.Errorf("flash = %+v", f)
	}
	if err := a.store.RestoreItem(ctx, pads, cur(t, a.store, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}

	// Bought, ticked off or removed twice.
	entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Zip ties"})
	expect(t, a.postV("/shopping/"+itoa(entry)+"/delete", nil), http.StatusSeeOther)
	for _, p := range []string{"/delete", "/bought"} {
		rec = a.post("/shopping/"+itoa(entry)+p, url.Values{"amount": {"1"}})
		expect(t, rec, http.StatusSeeOther)
		if f := flashOf(t, rec); f.Kind != "error" || f.Message != "That entry is no longer on the list" {
			t.Errorf("%s: flash = %+v", p, f)
		}
	}

	// A store conflict no handler expects: deleting a service would give
	// back more than the maximum.
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Pads"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(pads)}, "part_qty": {"1"}})
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")
	if err := a.store.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MoveCorrection, Delta: stock.MaxQuantity}); err != nil {
		t.Fatal(err)
	}
	rec = a.postV(svc+"/delete", nil, "Referer", "http://example.com"+svc)
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != svc {
		t.Errorf("went to %q, want back to %q", l, svc)
	}
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "would have more than") {
		t.Errorf("flash = %+v", f)
	}
}

// An empty list under a filter or past the last page says so, rather
// than that nothing was ever logged.
func TestEmptyStatesFollowFilters(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain"}, "performed_on": {"2026-10-29"}}),
		http.StatusSeeOther)
	expect(t, a.get("/services?page=9"), http.StatusOK, "Nothing on this page", `href="/services"`)
	expect(t, a.get("/services?v="+itoa(k1290)), http.StatusOK, "Nothing logged for this vehicle yet",
		`href="/services/new?vehicle=`+itoa(k1290)+`"`)
	expect(t, a.get("/activity?page=9"), http.StatusOK, "Nothing on this page", `href="/activity"`)
	expect(t, a.get("/activity?kind=correction"), http.StatusOK, "Nothing of this kind yet", "Show everything")
	expectNot(t, a.get("/activity?kind=correction"), "No activity yet")
}

func TestAccessibilityDetails(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	expect(t, a.get("/items/new"), http.StatusOK, `name="total_price" value="" inputmode="decimal" placeholder="optional" aria-describedby="price-tip"`)
	// Only the selected tab is in the tab order; the price label names the currency.
	expect(t, a.get("/items/"+itoa(pads)), http.StatusOK, `id="tab-purchase" aria-controls="panel-purchase" aria-selected="true">`,
		`id="tab-use" aria-controls="panel-use" aria-selected="false" tabindex="-1">`, "Total price (EUR)")
	// A fixed vehicle has no <label for> pointing at nothing.
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"}})
	expect(t, rec, http.StatusSeeOther)
	edit := a.get(rec.Header().Get("Location") + "/edit")
	expect(t, edit, http.StatusOK, `<span class="label">Vehicle</span>`)
	expectNot(t, edit, `for="vehicle_id"`)
	// The item picker is marked when it is what's wrong.
	expect(t, a.post("/shopping", url.Values{"item_id": {"999999"}}), http.StatusUnprocessableEntity,
		`<div class="field has-error">
            <label for="item_id">`)
	// Error messages are announced at once.
	page := httptest.NewRequest(http.MethodGet, "/trash", nil)
	for _, c := range a.post("/trash/999/restore", nil).Result().Cookies() {
		page.AddCookie(c)
	}
	expect(t, a.do(page), http.StatusOK, `class="flash flash-error" role="alert"`)
}
