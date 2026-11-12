package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// getWorking is a GET from a device working on the vehicle with that id
// (any cookie value: "abc" too).
func (a *testApp) getWorking(path, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: workingCookie, Value: cookie})
	return a.do(req)
}

// postWorking is a.post from a device working on that vehicle.
func (a *testApp) postWorking(path string, form url.Values, cookie string) *httptest.ResponseRecorder {
	if !form.Has("form_key") {
		form.Set("form_key", newFormKey())
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: workingCookie, Value: cookie})
	return a.do(req)
}

// workingCookieSet is the working_on cookie the response sets, or nil.
func workingCookieSet(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == workingCookie {
			return c
		}
	}
	return nil
}

func expectRedirect(t *testing.T, rec *httptest.ResponseRecorder, to string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
		t.Errorf("got %d to %q, want 303 to %q", rec.Code, rec.Header().Get("Location"), to)
	}
}

const workingBar = `class="working-on"`

func TestWorkingOnSwitch(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	post := func(vehicle, ref string) *httptest.ResponseRecorder {
		return a.post("/working-on", url.Values{"vehicle": {vehicle}}, "Referer", "http://example.com"+ref)
	}
	// Working on a vehicle drops the vehicle from the page's address, so
	// the page shows the one now worked on; other filters stay.
	rec := post(itoa(k390), "/stock?general=1&q=oil&v="+itoa(k1290))
	expectRedirect(t, rec, "/stock?q=oil")
	c := workingCookieSet(rec)
	if c == nil || c.Value != itoa(k390) || c.MaxAge <= 0 || c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie = %+v", c)
	}
	for ref, want := range map[string]string{
		"/items/new?vehicle=" + itoa(k1290) + "&unit=ml": "/items/new?unit=ml",
		"/services?page=2&v=" + itoa(k1290):              "/services",
		"/?v=all":                                        "/",
		"/items/1":                                       "/items/1", // a record stays as it is
	} {
		expectRedirect(t, post(itoa(k1290), ref), want)
	}
	// An empty vehicle stops.
	rec = post("", "/stock?general=1&v="+itoa(k390))
	expectRedirect(t, rec, "/stock")
	if c := workingCookieSet(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("stopping cookie = %+v", c)
	}
}

func TestWorkingOnStaleCookie(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	// Only the plain digits the server writes: app.js compares them.
	for _, cookie := range []string{"99999", "abc", "+" + itoa(k390), "0" + itoa(k390)} {
		rec := a.getWorking("/stock", cookie)
		expect(t, rec, http.StatusOK, "Hiflofiltro")
		expectNot(t, rec, workingBar)
		if c := workingCookieSet(rec); c == nil || c.MaxAge >= 0 {
			t.Errorf("cookie %q not cleared: %+v", cookie, c)
		}
	}
	if err := a.store.ArchiveVehicle(context.Background(), k1290, cur(t, a.store, store.WhatVehicle, k1290)); err != nil {
		t.Fatal(err)
	}
	// An archived vehicle is no longer worked on.
	rec := a.getWorking("/stock", itoa(k1290))
	expect(t, rec, http.StatusOK)
	expectNot(t, rec, workingBar)
	if c := workingCookieSet(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("archived vehicle's cookie not cleared: %+v", c)
	}
	// With one active vehicle there is nothing to switch: no bar, no
	// redirect, no Work on chips; the cookie stays for when one comes back.
	rec = a.getWorking("/stock", itoa(k390))
	expect(t, rec, http.StatusOK, "Stock by vehicle")
	expectNot(t, rec, workingBar, `action="/working-on"`)
	if c := workingCookieSet(rec); c != nil {
		t.Errorf("one vehicle's cookie touched: %+v", c)
	}
}

func TestWorkingOnPages(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	ctx := context.Background()
	gin := stock.ItemInput{Name: "Chain lube", Unit: stock.UnitPiece, Quantity: 2}
	gin.Normalize()
	if _, err := a.store.CreateItem(ctx, gin); err != nil {
		t.Fatal(err)
	}
	ring := itemID(t, a.store, "Oil Filter O-Ring")
	big := stock.ServiceInput{VehicleID: k1290, Title: "Big service", PerformedOn: stock.DateOnly(time.Now()),
		Parts: []stock.KitLineInput{{ItemID: ring, Quantity: 1, Unit: stock.UnitPiece}}}
	big.Normalize()
	if _, err := a.store.CreateService(ctx, big); err != nil {
		t.Fatal(err)
	}
	w := itoa(k390)

	t.Run("list pages go to the vehicle", func(t *testing.T) {
		for path, want := range map[string]string{
			"/":                           "/?v=" + w,
			"/stock":                      "/stock?general=1&v=" + w,
			"/stock?cat=Filters":          "/stock?cat=Filters&general=1&v=" + w,
			"/stock?view=list":            "/stock?general=1&v=" + w + "&view=list",
			"/services":                   "/services?v=" + w,
			"/services?archived=1&page=2": "/services?archived=1&v=" + w,
			"/activity":                   "/activity?general=1&v=" + w,
			"/activity?kind=bought":       "/activity?general=1&kind=bought&v=" + w,
		} {
			expectRedirect(t, a.getWorking(path, w), want)
		}
	})
	t.Run("they show it", func(t *testing.T) {
		rec := a.getWorking("/stock?general=1&v="+w, w)
		// The bar names the vehicle; the title doesn't say it again.
		expect(t, rec, http.StatusOK, "<title>Stock ·", "<h1>Stock</h1>", workingBar, "Working on <strong>KTM 390</strong>", "Chain lube", "Only this vehicle")
		expectNot(t, rec, "Hiflofiltro", "Clear filters")
		expect(t, a.getWorking("/services?v="+w, w), http.StatusOK, "<h1>Services</h1>")
		rec = a.getWorking("/activity?general=1&v="+w, w)
		expect(t, rec, http.StatusOK, "<h1>Activity</h1>", "Chain lube", "Silkolene")
		expectNot(t, rec, "Hiflofiltro", "Big service") // B's service took the shared ring: not A's
	})
	t.Run("dashboard", func(t *testing.T) {
		rec := a.getWorking("/?v="+w, w)
		// Pads, oil, ring, M3.0 and the General lube fit the KTM 390.
		expect(t, rec, http.StatusOK, "<h1>Dashboard</h1>",
			`href="/stock?general=1&amp;v=`+w+`"><span class="label">`, `<span class="value">5</span>`,
			`href="/stock?general=1&amp;low=1&amp;v=`+w+`"`, `href="/activity?general=1&amp;kind=bought&amp;v=`+w+`&amp;year=2026"`,
			`href="/services/new?vehicle=`+w+`"`)
		expectNot(t, rec, "Big service")
	})
	t.Run("the bar wins over the address", func(t *testing.T) {
		// Whatever else an address asks for (an old bookmark, a link from
		// before), a page with the bar shows the vehicle with General,
		// keeping the rest of the address. An item's history is a record.
		k := itoa(k1290)
		for path, want := range map[string]string{
			"/stock?v=all":                        "/stock?general=1&v=" + w,
			"/stock?v=" + k:                       "/stock?general=1&v=" + w,
			"/stock?v=" + w:                       "/stock?general=1&v=" + w,
			"/stock?general=1":                    "/stock?general=1&v=" + w,
			"/stock?v=99999&q=oil":                "/stock?general=1&q=oil&v=" + w,
			"/stock?match=all&v=" + w + "&v=" + k: "/stock?general=1&v=" + w,
			"/stock?was_sort=name&was_dir=asc":    "/stock?general=1&v=" + w,
			"/services?v=all":                     "/services?v=" + w,
			"/services?v=" + k + "&archived=1":    "/services?archived=1&v=" + w,
			"/services?v=99999":                   "/services?v=" + w,
			"/activity?v=all":                     "/activity?general=1&v=" + w,
			"/activity?v=" + w:                    "/activity?general=1&v=" + w,
			"/activity?general=1&v=all":           "/activity?general=1&v=" + w,
			"/activity?v=" + k + "&page=3":        "/activity?general=1&v=" + w,
			"/?v=all":                             "/?v=" + w,
			"/?v=" + k:                            "/?v=" + w,
		} {
			expectRedirect(t, a.getWorking(path, w), want)
		}
		// The filter of a page opened before: the whole page changes.
		req := httptest.NewRequest(http.MethodGet, "/stock?was_sort=name&was_dir=asc&v="+k, nil)
		req.Header.Set("HX-Request", "true")
		req.AddCookie(&http.Cookie{Name: workingCookie, Value: w})
		if rec := a.do(req); rec.Header().Get("HX-Redirect") != "/stock?general=1&v="+w {
			t.Errorf("htmx filter: HX-Redirect %q", rec.Header().Get("HX-Redirect"))
		}
		expect(t, a.getWorking("/activity?item="+itoa(itemID(t, a.store, "Hiflofiltro")), w), http.StatusOK, "History of")
	})
	t.Run("links to another vehicle's list are hidden", func(t *testing.T) {
		// They would show the worked-on vehicle's list instead.
		other := a.getWorking("/vehicles/"+itoa(k1290), w)
		expect(t, other, http.StatusOK)
		expectNot(t, other, "Stock list →", `href="/services?v=`+itoa(k1290)+`"`)
		expect(t, a.getWorking("/vehicles/"+w, w), http.StatusOK, "Stock list →", `href="/services?v=`+w+`"`)
		expect(t, a.get("/vehicles/"+itoa(k1290)), http.StatusOK, "Stock list →")
		dash := a.getWorking("/?v="+w, w)
		expect(t, dash, http.StatusOK, `href="/stock?v=`+w+`">Stock →`)
		expectNot(t, dash, `href="/stock?v=`+itoa(k1290)+`">Stock →`)
		// Stock rows' vehicle chips are labels, also after a quick +/-.
		list := a.getWorking("/stock?general=1&v="+w+"&view=list", w)
		expect(t, list, http.StatusOK, `<span class="dot"></span>KTM 1290</span>`)
		expectNot(t, list, `title="Show KTM 1290 stock"`, `title="Show KTM 390 stock"`)
		req := httptest.NewRequest(http.MethodPost, "/items/"+itoa(ring)+"/adjust", strings.NewReader("dir=up&here="+w))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		req.AddCookie(&http.Cookie{Name: workingCookie, Value: w})
		rec := a.do(req)
		expect(t, rec, http.StatusOK, `<span class="dot"></span>KTM 1290</span>`)
		expectNot(t, rec, `title="Show KTM 1290 stock"`)
	})
	t.Run("records and global pages are not filtered", func(t *testing.T) {
		for _, path := range []string{"/shopping", "/vehicles", "/trash", "/items/" + itoa(itemID(t, a.store, "Hiflofiltro")), "/vehicles/" + itoa(k1290)} {
			expect(t, a.getWorking(path, w), http.StatusOK, workingBar)
		}
	})
	t.Run("exports follow their address only", func(t *testing.T) {
		// No bar on an export: a bookmarked one keeps showing what it
		// asks for, and a vehicle page's Copy as text that vehicle's.
		expect(t, a.getWorking("/stock/export.txt", w), http.StatusOK, "Silkolene", "Hiflofiltro")
		rec := a.getWorking("/stock/export.txt?v="+itoa(k1290), w)
		expect(t, rec, http.StatusOK, "Hiflofiltro")
		expectNot(t, rec, "Silkolene")
		rec = a.getWorking("/stock/export.csv?general=1&v="+w, w)
		expect(t, rec, http.StatusOK, "Silkolene")
		expectNot(t, rec, "Hiflofiltro")
		expect(t, a.getWorking("/activity/export.csv", w), http.StatusOK, "Silkolene", "Hiflofiltro")
		rec = a.getWorking("/activity/export.csv?general=1&v="+w, w)
		expect(t, rec, http.StatusOK, "Silkolene")
		expectNot(t, rec, "Hiflofiltro")
	})
	t.Run("an unknown vehicle isn't chosen", func(t *testing.T) {
		// Without a vehicle worked on, a deleted vehicle's link shows every
		// vehicle, not an empty list.
		expect(t, a.get("/stock?v=99999"), http.StatusOK, "Silkolene", "Hiflofiltro")
		expect(t, a.get("/services?v=99999"), http.StatusOK, "Big service", "<h1>Services</h1>")
		expect(t, a.get("/activity?v=99999"), http.StatusOK, "<h1>Activity</h1>", "Hiflofiltro")
	})
	t.Run("a search says what is elsewhere", func(t *testing.T) {
		// "Oil Filter" finds the shared ring here, and the KTM 1290's filter.
		rec := a.getWorking("/stock?general=1&q=Oil+Filter&v="+w, w)
		expect(t, rec, http.StatusOK, "O-Ring", "<span>1 more in other vehicles</span>")
	})
	t.Run("the bar is the vehicle control", func(t *testing.T) {
		// The pages' vehicle and General chips are hidden; the stock's filter
		// keeps its vehicles, with General, in hidden fields.
		rec := a.getWorking("/stock?general=1&v="+w, w)
		expect(t, rec, http.StatusOK, `<input type="hidden" name="v" value="`+w+`">`, `<input type="hidden" name="general" value="1">`)
		expectNot(t, rec, `id="filter-all"`, `name="v" value="`+itoa(k1290)+`"`, `type="checkbox" name="general"`)
		expectNot(t, a.getWorking("/services?v="+w, w), `aria-label="Filter by vehicle"`)
		rec = a.getWorking("/activity?general=1&v="+w, w)
		expect(t, rec, http.StatusOK)
		expectNot(t, rec, ">All vehicles</a>", ">General</a>")
		// Without one, the chips are there.
		expect(t, a.get("/stock"), http.StatusOK, `id="filter-all"`)
		expect(t, a.get("/services"), http.StatusOK, `aria-label="Filter by vehicle"`)
		expect(t, a.get("/activity"), http.StatusOK, ">All vehicles</a>")
	})
	t.Run("add forms start with it", func(t *testing.T) {
		checked := regexp.MustCompile(`name="vehicle" value="` + w + `" checked`)
		if !checked.MatchString(a.getWorking("/items/new", w).Body.String()) {
			t.Error("Add item doesn't start with the vehicle")
		}
		if body := a.getWorking("/items/new?vehicle="+itoa(k1290), w).Body.String(); checked.MatchString(body) {
			t.Error("an explicit vehicle lost to the one worked on")
		}
		// A new service or kit is for the vehicle worked on, without asking;
		// a link that names another one shows the field with it.
		for _, path := range []string{"/services/new", "/kits/new"} {
			rec := a.getWorking(path, w)
			expect(t, rec, http.StatusOK, `<input type="hidden" name="vehicle_id" value="`+w+`">`)
			expectNot(t, rec, `id="vehicle_id"`)
			expect(t, a.getWorking(path+"?vehicle="+itoa(k1290), w), http.StatusOK, `id="vehicle_id"`)
			if !regexp.MustCompile(`<option value="` + itoa(k1290) + `"[^>]*selected>`).MatchString(a.getWorking(path+"?vehicle="+itoa(k1290), w).Body.String()) {
				t.Errorf("%s: an explicit vehicle lost to the one worked on", path)
			}
			expect(t, a.get(path), http.StatusOK, `id="vehicle_id"`)
		}
		// Saved without the field, it is the vehicle's.
		rec := a.postWorking("/kits", url.Values{"vehicle_id": {w}, "name": {"Chain service"}}, w)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("saving a kit while working: %d\n%s", rec.Code, truncate(rec.Body.String()))
		}
		kits, err := a.store.ListKits(ctx, store.KitFilter{VehicleID: k390})
		if err != nil || !slices.ContainsFunc(kits, func(k stock.Kit) bool { return k.Name == "Chain service" }) {
			t.Errorf("the kit isn't the KTM 390's: %v, %v", kits, err)
		}
	})
	t.Run("quick adjust has no bar", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/items/"+itoa(ring)+"/adjust", strings.NewReader("dir=up"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		req.AddCookie(&http.Cookie{Name: workingCookie, Value: w})
		rec := a.do(req)
		expect(t, rec, http.StatusOK, `id="item-`+itoa(ring)+`"`)
		expectNot(t, rec, workingBar)
	})
	t.Run("sidebar", func(t *testing.T) {
		rec := a.getWorking("/vehicles", w)
		expect(t, rec, http.StatusOK, `action="/working-on"`,
			`name="vehicle" value="" aria-pressed="true"`, `name="vehicle" value="`+itoa(k1290)+`" aria-pressed="false"`)
		expectNot(t, rec, "Stock by vehicle")
		// The bar's Switch lists the others only; the sidebar's chip of the
		// one worked on is pressed, and tapping it stops.
		if n := strings.Count(rec.Body.String(), `value="" aria-pressed="true"`); n != 1 {
			t.Errorf("the vehicle worked on is offered %d times, want once (the sidebar)", n)
		}
		if n := strings.Count(rec.Body.String(), `name="vehicle" value="`+itoa(k1290)+`"`); n != 2 {
			t.Errorf("the other vehicle is offered %d times, want twice (the sidebar and Switch)", n)
		}
		rec = a.get("/vehicles")
		expect(t, rec, http.StatusOK, `name="vehicle" value="`+w+`" aria-pressed="false"`)
		expectNot(t, rec, workingBar, `aria-pressed="true"`)
	})
}

// An empty stock while working on a vehicle says so; it once failed (500)
// on a page asking for every vehicle.
func TestWorkingOnEmptyStock(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	var ids []int64
	for _, name := range []string{"A", "B"} {
		in := stock.VehicleInput{Name: name}
		in.Normalize()
		id, err := a.store.CreateVehicle(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	w := itoa(ids[0])
	expect(t, a.getWorking("/stock?general=1&v="+w, w), http.StatusOK, "Nothing in stock for A")
	expect(t, a.getWorking("/stock?general=1&v="+w+"&view=list", w), http.StatusOK, "Nothing in stock for A")
	expectRedirect(t, a.getWorking("/stock?v=all", w), "/stock?general=1&v="+w)
}

// The page renders the cookie for app.js even when it can't list the
// vehicles, so app.js doesn't reload it over and over.
func TestWorkingCookieValue(t *testing.T) {
	for raw, want := range map[string]string{"7": "7", "+7": "", "07": "", " 7": "", "abc": "", "0": "", "-7": ""} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: workingCookie, Value: raw})
		if got := workingCookieValue(req); got != want {
			t.Errorf("cookie %q = %q, want %q", raw, got, want)
		}
	}
}
