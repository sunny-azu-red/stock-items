package web

import (
	"context"
	"fmt"
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
		// A vehicle's page: the one now worked on (the other is out of view).
		"/vehicles/" + itoa(k390):           "/vehicles/" + itoa(k1290),
		"/vehicles/" + itoa(k390) + "/edit": "/vehicles/" + itoa(k1290),
		"/vehicles?archived=1":              "/vehicles/" + itoa(k1290),
	} {
		expectRedirect(t, post(itoa(k1290), ref), want)
	}
	// Stopping on a vehicle's page lists them all.
	expectRedirect(t, post("", "/vehicles/"+itoa(k390)), "/vehicles")
	expectRedirect(t, post("", "/vehicles/"+itoa(k390)+"/edit"), "/vehicles")
	// A vehicle that isn't there (or archived) can't be worked on: the list.
	expectRedirect(t, post("99999", "/vehicles/"+itoa(k390)), "/vehicles")
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
	// redirect, no Work on chips, and the choice stops, so it doesn't come
	// back months later when a second vehicle is added.
	rec = a.getWorking("/stock", itoa(k390))
	expect(t, rec, http.StatusOK, "Stock by vehicle")
	expectNot(t, rec, workingBar, `action="/working-on"`)
	if c := workingCookieSet(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("one vehicle's cookie not cleared: %+v", c)
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
		expect(t, rec, http.StatusOK, "<h1>Activity</h1>", "Chain lube", "Silkolene",
			"Every change to the stock that fits KTM 390 (General too), except parts other vehicles' services took")
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
		// They would show the worked-on vehicle's list instead. (Another
		// vehicle's page is out of view meanwhile; its own links to them.)
		expect(t, a.getWorking("/vehicles/"+w, w), http.StatusOK, "Stock list →", `href="/services?v=`+w+`"`)
		expect(t, a.get("/vehicles/"+itoa(k1290)), http.StatusOK, "Stock list →")
		// The dashboard leaves out its Vehicles panel (and its Stock →).
		dash := a.getWorking("/?v="+w, w)
		expect(t, dash, http.StatusOK)
		expectNot(t, dash, `All vehicles →`, `">Stock →`)
		expect(t, a.get("/"), http.StatusOK, `All vehicles →`, `href="/stock?v=`+w+`">Stock →`, `href="/stock?v=`+itoa(k1290)+`">Stock →`)
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
		for _, path := range []string{"/trash", "/items/" + itoa(itemID(t, a.store, "Hiflofiltro")), "/vehicles/" + w} {
			expect(t, a.getWorking(path, w), http.StatusOK, workingBar)
		}
	})
	t.Run("another vehicle is out of view", func(t *testing.T) {
		// What belongs to another vehicle (its page, kits and services, their
		// edit forms), the vehicles list and Add vehicle wait until you
		// switch or stop: an old link or bookmark opens the vehicle worked on.
		kin := stock.KitInput{VehicleID: k1290, Name: "Far kit", Lines: []stock.KitLineInput{{ItemID: ring, Quantity: 1, Unit: stock.UnitPiece}}}
		kin.Normalize()
		farKit, err := a.store.CreateKit(ctx, kin)
		if err != nil {
			t.Fatal(err)
		}
		services, err := a.store.ListServices(ctx, store.ServiceFilter{VehicleID: k1290})
		if err != nil || len(services) == 0 {
			t.Fatalf("the KTM 1290's services: %v, %v", services, err)
		}
		farService := itoa(services[0].ID)
		k, own := itoa(k1290), "/vehicles/"+w
		for path, want := range map[string]string{
			"/vehicles":                            own,
			"/vehicles?archived=1":                 own,
			"/vehicles/new":                        own,
			"/vehicles/" + k:                       own,
			"/vehicles/" + k + "/edit":             own,
			"/kits/" + itoa(farKit):                own,
			"/kits/" + itoa(farKit) + "/edit":      own,
			"/kits/" + itoa(farKit) + "?sort=name": own,
			"/services/" + farService:              "/services",
			"/services/" + farService + "/edit":    "/services",
		} {
			expectRedirect(t, a.getWorking(path, w), want)
			// Without a vehicle worked on, they open.
			expect(t, a.get(path), http.StatusOK)
		}
		// The vehicle's own pages open.
		expect(t, a.getWorking(own+"/edit", w), http.StatusOK)
		// An htmx request (a kit's sort) gets the whole page changed.
		req := httptest.NewRequest(http.MethodGet, "/kits/"+itoa(farKit)+"?sort=name", nil)
		req.Header.Set("HX-Request", "true")
		req.AddCookie(&http.Cookie{Name: workingCookie, Value: w})
		if rec := a.do(req); rec.Header().Get("HX-Redirect") != own {
			t.Errorf("htmx: HX-Redirect %q", rec.Header().Get("HX-Redirect"))
		}
		// Links to them are names: the shared ring's kits and history.
		rec := a.getWorking("/items/"+itoa(ring), w)
		expect(t, rec, http.StatusOK, `<span class="title">Far kit</span>`, "Big service")
		expectNot(t, rec, `href="/kits/`+itoa(farKit)+`"`, `href="/services/`+farService+`"`)
		rec = a.get("/items/" + itoa(ring))
		expect(t, rec, http.StatusOK, `href="/kits/`+itoa(farKit)+`"`, `href="/services/`+farService+`"`)
		// The vehicle's page offers working on it only while none is worked on.
		expectNot(t, a.getWorking(own, w), "working-off")
		expect(t, a.get(own), http.StatusOK, `class="working-on working-off"`)
		// A save from a page opened before isn't new work: it saves, and
		// lands on the vehicle worked on.
		v, err := a.store.GetVehicle(ctx, k1290)
		if err != nil {
			t.Fatal(err)
		}
		f := vehicleFormFrom(v)
		rec = a.postWorking("/vehicles/"+k, url.Values{"name": {f.Name}, "notes": {"Saved from before"}, "kind": {f.Kind}, "color": {f.Color},
			"odometer": {f.Odometer}, "odometer_was": {f.OdometerWas}, "version": {itoa(int64(f.Version))}}, w)
		expectRedirect(t, rec, "/vehicles/"+k)
		expectRedirect(t, a.getWorking("/vehicles/"+k, w), own)
		if v, err := a.store.GetVehicle(ctx, k1290); err != nil || v.Notes != "Saved from before" {
			t.Errorf("the save from before: %q, %v", v.Notes, err)
		}
		// Restoring another vehicle's kit shows the trash it came from.
		fk, err := a.store.GetKit(ctx, farKit)
		if err != nil {
			t.Fatal(err)
		}
		trashID, err := a.store.DeleteKit(ctx, farKit, fk.Version)
		if err != nil {
			t.Fatal(err)
		}
		req = httptest.NewRequest(http.MethodPost, "/trash/"+itoa(trashID)+"/restore", nil)
		req.AddCookie(&http.Cookie{Name: workingCookie, Value: w})
		expectRedirect(t, a.do(req), "/trash")
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
		rec = a.getWorking("/activity/export.csv?general=1", w)
		expect(t, rec, http.StatusOK, "Chain lube")
		expectNot(t, rec, "Silkolene", "Hiflofiltro")
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
		// Without a search or filter there is no note: the bar says it.
		expectNot(t, a.getWorking("/stock?general=1&v="+w, w), "more in other vehicles")
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
		// A new service or kit is for the vehicle worked on, without asking.
		for _, path := range []string{"/services/new", "/kits/new"} {
			rec := a.getWorking(path, w)
			expect(t, rec, http.StatusOK, `<input type="hidden" name="vehicle_id" value="`+w+`" data-name="KTM 390">`)
			expectNot(t, rec, `id="vehicle_id"`)
			expect(t, a.get(path), http.StatusOK, `id="vehicle_id"`)
			if !vehicleChecked(a.get(path+"?vehicle="+itoa(k1290)).Body.String(), k1290) {
				t.Errorf("%s: without a vehicle worked on, ?vehicle= doesn't choose it", path)
			}
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
	t.Run("no new work for another vehicle", func(t *testing.T) {
		// While a vehicle is worked on, new services and kits are for it,
		// like "archived means no new work". Its own records stay editable.
		kin := stock.KitInput{VehicleID: k1290, Name: "Big kit", Lines: []stock.KitLineInput{{ItemID: ring, Quantity: 1, Unit: stock.UnitPiece}}}
		kin.Normalize()
		kit, err := a.store.CreateKit(ctx, kin)
		if err != nil {
			t.Fatal(err)
		}
		k := itoa(k1290)
		// Its pages are out of view (above); the vehicle worked on's offer it.
		expect(t, a.getWorking("/vehicles/"+w, w), http.StatusOK, `href="/services/new?vehicle=`+w+`"`, `href="/kits/new?vehicle=`+w+`"`)
		expect(t, a.get("/kits/"+itoa(kit)), http.StatusOK, `href="/services/new?kit=`+itoa(kit)+`"`)
		// Old links open the worked-on vehicle's form.
		for path, want := range map[string]string{
			"/services/new?vehicle=" + k:     "/services/new",
			"/services/new?kit=" + itoa(kit): "/services/new",
			"/kits/new?vehicle=" + k:         "/kits/new",
		} {
			expectRedirect(t, a.getWorking(path, w), want)
		}
		// An item isn't work on a vehicle: its vehicles stay a choice.
		expect(t, a.getWorking("/items/new?vehicle="+k, w), http.StatusOK, `name="vehicle" value="`+k+`" checked`)
		// A form opened before the switch is refused, keeping what was typed.
		// The Vehicle field comes back with the other vehicle chosen and the
		// error under it, so it can be changed; the form key is kept.
		key := newFormKey()
		form := url.Values{"vehicle_id": {k}, "title": {"Tyre swap"}, "performed_on": {"2026-10-29"}, "form_key": {key}}
		rec := a.postWorking("/services", form, w)
		expect(t, rec, http.StatusUnprocessableEntity, "You&#39;re working on KTM 390: choose it, or work on KTM 1290 first to log this service",
			`value="Tyre swap"`, `id="vehicle_id"`, `value="`+key+`"`)
		if !vehicleChecked(rec.Body.String(), k1290) {
			t.Error("the refused form doesn't keep the other vehicle chosen")
		}
		rec = a.postWorking("/services", url.Values{"vehicle_id": {k}, "kit_id": {itoa(kit)}, "kit_name": {"Big kit"}, "title": {"Big kit"}, "performed_on": {"2026-10-29"}}, w)
		expect(t, rec, http.StatusUnprocessableEntity, "You&#39;re working on KTM 390: work on KTM 1290 first to log this service")
		rec = a.postWorking("/kits", url.Values{"vehicle_id": {k}, "name": {"Tyre kit"}}, w)
		expect(t, rec, http.StatusUnprocessableEntity, "You&#39;re working on KTM 390: choose it, or work on KTM 1290 first to add this kit", `value="Tyre kit"`)
		if services, _ := a.store.ListServices(ctx, store.ServiceFilter{VehicleID: k1290}); slices.ContainsFunc(services, func(r stock.ServiceRecord) bool { return r.Title == "Tyre swap" }) {
			t.Error("a service for another vehicle was saved")
		}
		// Working on that vehicle, the same form (and key) saves.
		if rec := a.postWorking("/services", form, k); rec.Code != http.StatusSeeOther {
			t.Errorf("the refused form, after switching: %d\n%s", rec.Code, truncate(rec.Body.String()))
		}
		// Without a vehicle worked on, they save.
		expect(t, a.post("/kits", url.Values{"vehicle_id": {k}, "name": {"Tyre kit"}}), http.StatusSeeOther)
		// Log service offers only the worked-on vehicle's kits to start from.
		rec = a.getWorking("/services/new", w)
		expect(t, rec, http.StatusOK)
		expectNot(t, rec, `href="/services/new?kit=`+itoa(kit)+`"`)
		expect(t, a.get("/services/new"), http.StatusOK, `href="/services/new?kit=`+itoa(kit)+`"`)
	})
	t.Run("add another keeps General", func(t *testing.T) {
		rec := a.postWorking("/items", url.Values{"name": {"Zip ties"}, "unit": {"piece"}, "quantity": {"10"}, "next": {"new"}}, w)
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "vehicle=") {
			t.Fatalf("add another: %d to %q", rec.Code, rec.Header().Get("Location"))
		}
		// The next form starts as General, not with the vehicle worked on.
		next := a.getWorking(rec.Header().Get("Location"), w)
		expect(t, next, http.StatusOK)
		expectNot(t, next, `name="vehicle" value="`+w+`" checked`)
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
		rec := a.getWorking("/trash", w)
		expect(t, rec, http.StatusOK, `action="/working-on"`,
			`name="vehicle" value="" aria-pressed="true"`, `name="vehicle" value="`+itoa(k1290)+`" aria-pressed="false"`)
		expectNot(t, rec, "Stock by vehicle")
		// The sidebar's Work on list is the one place to choose (on a phone,
		// in More): the chip of the one worked on is pressed, and tapping it
		// stops.
		if n := strings.Count(rec.Body.String(), `value="" aria-pressed="true"`); n != 1 {
			t.Errorf("the vehicle worked on is offered %d times, want once (the sidebar)", n)
		}
		if n := strings.Count(rec.Body.String(), `name="vehicle" value="`+itoa(k1290)+`"`); n != 1 {
			t.Errorf("the other vehicle is offered %d times, want once (the sidebar)", n)
		}
		rec = a.get("/trash")
		expect(t, rec, http.StatusOK, `name="vehicle" value="`+w+`" aria-pressed="false"`)
		// No Work on button is pressed (the Appearance panel presses its own).
		expectNot(t, rec, workingBar, `value="" aria-pressed="true"`)
	})
}

// While a vehicle is worked on, the nav's Vehicles is that vehicle's page
// (Vehicle, its kind icon); the bar's name is plain text, no link; the list
// is out of view until you stop.
func TestWorkingOnNav(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	w := itoa(k390)
	nav := `<a class="nav-vehicle" href="/vehicles/` + w + `"`
	barLink := `<a class="working-name" href="/vehicles/` + w + `">`
	for _, path := range []string{"/shopping?v=" + w, "/trash", "/stock?general=1&v=" + w} {
		rec := a.getWorking(path, w)
		expect(t, rec, http.StatusOK, nav, `"></use></svg>Vehicle</a>`, `<span class="working-name">`)
		expectNot(t, rec, `<a href="/vehicles" `, barLink)
	}
	// The list opens the vehicle's page.
	expectRedirect(t, a.getWorking("/vehicles", w), "/vehicles/"+w)
	own := a.getWorking("/vehicles/"+w, w)
	expect(t, own, http.StatusOK, `<span class="working-name">`, nav+` aria-current="page"`)
	expectNot(t, own, barLink, `<a class="btn btn-ghost" href="/vehicles">`)
	// Without one: the list, under its own name.
	rec := a.get("/shopping")
	expect(t, rec, http.StatusOK, `<a href="/vehicles" >`, `#i-warehouse"></use></svg>Vehicles</a>`)
	expectNot(t, rec, `class="nav-vehicle"`)
	// The phone's More opens the sidebar, which leaves out what the
	// tabbar has; Activity and Trash moved off the vehicles list into it.
	rec = a.getWorking("/activity?general=1&v="+w, w)
	expect(t, rec, http.StatusOK, `<div class="sidebar" id="more" popover`, `popovertarget="more" class="current"`, `href="/stock" class="tab-dup"`)
	expectNot(t, a.get("/vehicles"), `href="/activity"><svg`, `Trash (`)
	// One place to choose the vehicle, the sidebar's Work on list (in More
	// on a phone): the bar only stops, and Appearance, in the sheet, has
	// its way back to More.
	expect(t, rec, http.StatusOK, `<form class="chips work-on"`, `popovertarget="appearance" popovertargetaction="hide"`)
	expectNot(t, rec, `id="switch"`, `popovertarget="switch"`)
	expectNot(t, a.get("/shopping"), `popovertarget="more" class="current"`)
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

// While a vehicle is worked on, Shopping is its part of the list: entries
// for it (tagged with it, or used by its kits), General and free text,
// its suggestions, and every count. Without one, chips filter it.
func TestWorkingOnShopping(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	vehicle := func(name string) int64 {
		in := stock.VehicleInput{Name: name}
		in.Normalize()
		id, err := a.store.CreateVehicle(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	va, vb := vehicle("Alpha"), vehicle("Bravo")
	item := func(name string, qty float64, min *float64, unit stock.Unit, vehicles ...int64) int64 {
		in := stock.ItemInput{Name: name, Unit: unit, Quantity: qty, MinQuantity: min, VehicleIDs: vehicles}
		in.Normalize()
		id, err := a.store.CreateItem(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	pads := item("Pads", 0, ptr(2.0), stock.UnitPiece, va)
	grips := item("Grips", 0, ptr(2.0), stock.UnitPiece, vb)
	lube := item("Chain lube", 0, ptr(100.0), stock.UnitML)
	filter := item("Filter", 0, nil, stock.UnitPiece, vb) // Bravo's, but Alpha's kit uses it
	oil := item("Oil", 500, nil, stock.UnitML, va, vb)
	kit := func(vehicle int64, name string, line stock.KitLineInput) {
		in := stock.KitInput{VehicleID: vehicle, Name: name, Lines: []stock.KitLineInput{line}}
		in.Normalize()
		if _, err := a.store.CreateKit(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	kit(va, "Air Filter", stock.KitLineInput{ItemID: filter, Quantity: 1, Unit: stock.UnitPiece})
	kit(vb, "Oil service", stock.KitLineInput{ItemID: oil, Quantity: 3000, Unit: stock.UnitML})
	for _, id := range []int64{pads, grips, lube} {
		if _, err := a.store.EnsureShopping(ctx, id, 2, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Rags"}); err != nil {
		t.Fatal(err)
	}
	w, b := itoa(va), itoa(vb)
	alpha := "/shopping?v=" + w
	chip := func(name string) string { return `<span class="dot"></span>` + name + `</span>` }
	// rowChips matches a row's title followed by exactly these chips.
	rowChips := func(t *testing.T, body, title string, names ...string) {
		t.Helper()
		re := `>` + title + `</a>`
		for _, n := range names {
			re += `<span class="chip" style="[^"]*"><span class="dot"></span>` + n + `</span>`
		}
		re += `</span>`
		if !regexp.MustCompile(re).MatchString(body) {
			t.Errorf("%s: want the chips %v", title, names)
		}
	}

	t.Run("the address says the vehicle", func(t *testing.T) {
		for _, path := range []string{"/shopping", "/shopping?v=all", "/shopping?v=" + b, "/shopping?v=99999"} {
			expectRedirect(t, a.getWorking(path, w), alpha)
		}
		req := httptest.NewRequest(http.MethodGet, "/shopping?v="+b, nil)
		req.Header.Set("HX-Request", "true")
		req.AddCookie(&http.Cookie{Name: workingCookie, Value: w})
		if rec := a.do(req); rec.Header().Get("HX-Redirect") != alpha {
			t.Errorf("htmx: HX-Redirect %q", rec.Header().Get("HX-Redirect"))
		}
	})
	t.Run("its part", func(t *testing.T) {
		rec := a.getWorking(alpha, w)
		// Pads (tagged), lube (General), Rags (free text); not Grips.
		expect(t, rec, http.StatusOK, "<h1>Shopping</h1>", "<title>Shopping ·", ">Pads</a>", ">Chain lube</a>", ">Rags</span>",
			"3 entries · 1 more for other vehicles", `href="/shopping/export.txt?v=`+w+`"`,
			// Alpha's kit is short of Bravo's filter: suggested, said
			// without a vehicle.
			">Filter</a>", "Short for the Air Filter kit")
		expectNot(t, rec, ">Grips</a>", "Short for the Oil service kit", `aria-label="Filter by vehicle"`, chip("Alpha"), chip("Bravo"))
		// The form adds what shows here: Alpha's and General items.
		expect(t, rec, http.StatusOK, `<option value="Pads"`, `<option value="Chain lube"`, `<option value="Oil"`)
		expectNot(t, rec, `<option value="Grips"`, `<option value="Filter"`)
		// Every count is the part's: the sidebar, the tab bar, the dashboard.
		if n := strings.Count(rec.Body.String(), `<span class="count">3</span>`); n != 2 {
			t.Errorf("the sidebar and the tab bar say 3 %d times, want twice", n)
		}
		expect(t, a.getWorking("/?v="+w, w), http.StatusOK,
			`href="`+strings.ReplaceAll(alpha, "&", "&amp;")+`"><span class="label">`, `Shopping list</span><span class="value">3</span>`,
			// The filter, and the lube: 2 ml listed of the 100 ml it needs.
			`2 suggested →`)
	})
	t.Run("a kit's shortfall stays once added", func(t *testing.T) {
		rec := a.postWorking("/shopping/suggestions", url.Values{"item_id": {itoa(filter)}, "qty": {"1"}, "note": {"Short for the Air Filter kit"}}, w)
		expectRedirect(t, rec, "/shopping")
		rec = a.getWorking(alpha, w)
		expect(t, rec, http.StatusOK, "4 entries · 1 more for other vehicles", ">Filter</a>", `<span class="count">4</span>`)
	})
	t.Run("export", func(t *testing.T) {
		rec := a.getWorking("/shopping/export.txt?v="+w, w)
		expect(t, rec, http.StatusOK, "Pads", "Rags", "Filter")
		expectNot(t, rec, "Grips", "[")
		// The whole list says whose each part is; exports follow their
		// address only, whatever is worked on.
		expect(t, a.getWorking("/shopping/export.txt", w), http.StatusOK,
			"2 pcs - Pads [Alpha]\n", "2 pcs - Grips [Bravo]\n", "2 ml - Chain lube\n", "- Rags\n", "1 pcs - Filter (Short for the Air Filter kit) [Alpha, Bravo]\n")
	})
	t.Run("without a vehicle worked on", func(t *testing.T) {
		rec := a.get("/shopping")
		expect(t, rec, http.StatusOK, `aria-label="Filter by vehicle"`, `href="/shopping?v=`+b+`"`,
			"5 entries", "Short for the Oil service kit", `<span class="count">5</span>`)
		expectNot(t, rec, "more for other vehicles")
		// Each row says whose it is; General and free text say nothing.
		body := rec.Body.String()
		rowChips(t, body, "Grips", "Bravo")
		rowChips(t, body, "Pads", "Alpha")
		rowChips(t, body, "Filter", "Alpha", "Bravo") // tagged Bravo, Alpha's kit uses it
		rowChips(t, body, "Chain lube")
		rowChips(t, body, "Oil", "Bravo") // the suggestion: Bravo's kit
		// A chip shows a vehicle's part, titled with it.
		rec = a.get("/shopping?v=" + b)
		expect(t, rec, http.StatusOK, "<h1>Bravo shopping</h1>", "<title>Bravo shopping ·", "for Bravo (General too)",
			">Grips</a>", ">Filter</a>", ">Chain lube</a>", ">Rags</span>", "1 more for other vehicles", "Short for the Oil service kit")
		expectNot(t, rec, ">Pads</a>")
		// A vehicle that doesn't exist isn't chosen.
		expect(t, a.get("/shopping?v=99999"), http.StatusOK, "<h1>Shopping</h1>", "5 entries")
		// The dashboard's vehicle counts its part, and links to it.
		expect(t, a.get("/?v="+b), http.StatusOK, `href="/shopping?v=`+b+`"><span class="label">`, `Shopping list</span><span class="value">4</span>`)
	})
	t.Run("a chip's part stays chosen", func(t *testing.T) {
		// Without a vehicle worked on, what is done on a vehicle's part
		// comes back to it: its forms and the Bought page carry it (v).
		rec := a.get("/shopping?v=" + b)
		expect(t, rec, http.StatusOK, `<input type="hidden" name="v" value="`+b+`">`, `/bought?v=`+b+`"`)
		expectRedirect(t, a.post("/shopping", url.Values{"item": {"Gloves"}, "quantity": {"2"}, "v": {b}}), "/shopping?v="+b)
		expectRedirect(t, a.post("/shopping", url.Values{"item": {"Gloves"}, "quantity": {"2"}}), "/shopping")
		// A failed add comes back on that part, with its items.
		rec = a.post("/shopping", url.Values{"item": {""}, "v": {b}})
		expect(t, rec, http.StatusUnprocessableEntity, "<h1>Bravo shopping</h1>", `<option value="Grips"`)
		expectNot(t, rec, `<option value="Pads"`)
		list, err := a.store.ListShopping(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var gloves stock.ShoppingEntry
		for _, e := range list {
			if e.FreeText == "Gloves" {
				gloves = e
			}
		}
		bought := a.get(fmt.Sprintf("/shopping/%d/bought?v=%s", gloves.ID, b))
		expect(t, bought, http.StatusOK, `href="/shopping?v=`+b+`">Cancel</a>`, `<input type="hidden" name="v" value="`+b+`">`)
		rec = a.post(fmt.Sprintf("/shopping/%d/bought", gloves.ID), url.Values{"version": {itoa(int64(gloves.Version))}, "v": {b}})
		expectRedirect(t, rec, "/shopping?v="+b)
	})
	t.Run("what is out of view while working", func(t *testing.T) {
		// Grips is Bravo's: its item page says it is listed, without a link
		// to Alpha's part, which doesn't show it.
		rec := a.getWorking("/items/"+itoa(grips), w)
		expect(t, rec, http.StatusOK, `<span class="badge badge-accent">`)
		expectNot(t, rec, `<a class="badge badge-accent" href="/shopping">`)
		expect(t, a.getWorking("/items/"+itoa(pads), w), http.StatusOK, `<a class="badge badge-accent" href="/shopping">`)
		expect(t, a.get("/items/"+itoa(grips)), http.StatusOK, `<a class="badge badge-accent" href="/shopping">`)
		// Restoring Bravo's entry stays on the trash; Alpha's opens its part.
		for id, want := range map[int64]string{grips: "/trash", pads: "/shopping"} {
			var entry stock.ShoppingEntry
			list, err := a.store.ListShopping(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range list {
				if e.ItemID != nil && *e.ItemID == id {
					entry = e
				}
			}
			trashID, err := a.store.DeleteShopping(ctx, entry.ID, entry.Version)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/trash/"+itoa(trashID)+"/restore", nil)
			req.AddCookie(&http.Cookie{Name: workingCookie, Value: w})
			expectRedirect(t, a.do(req), want)
		}
	})
	t.Run("switching shows the next vehicle's part", func(t *testing.T) {
		rec := a.post("/working-on", url.Values{"vehicle": {b}}, "Referer", "http://example.com"+alpha)
		expectRedirect(t, rec, "/shopping")
		expectRedirect(t, a.getWorking("/shopping", b), "/shopping?v="+b)
	})
}

// With one active vehicle, Shopping's rows and its copy leave its chip out
// (it is every page's scope), and an archived vehicle's row keeps its own.
func TestShoppingSoleVehicle(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	vehicle := func(name string) stock.Vehicle {
		in := stock.VehicleInput{Name: name}
		in.Normalize()
		id, err := a.store.CreateVehicle(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		v, err := a.store.GetVehicle(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	only, old := vehicle("Only"), vehicle("Old")
	if err := a.store.ArchiveVehicle(ctx, old.ID, old.Version); err != nil {
		t.Fatal(err)
	}
	for name, vehicles := range map[string][]int64{"Pads": {only.ID}, "Tyre": {old.ID}} {
		in := stock.ItemInput{Name: name, Unit: stock.UnitPiece, VehicleIDs: vehicles}
		in.Normalize()
		id, err := a.store.CreateItem(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.EnsureShopping(ctx, id, 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	rec := a.get("/shopping")
	expect(t, rec, http.StatusOK, ">Pads</a></span>", `>Tyre</a><span class="chip"`, `<span class="dot"></span>Old</span>`)
	expectNot(t, rec, `<span class="dot"></span>Only</span>`, `aria-label="Filter by vehicle"`)
	rec = a.get("/shopping/export.txt")
	expect(t, rec, http.StatusOK, "1 pcs - Pads\n", "1 pcs - Tyre [Old]\n")
}

// TestWorkingOnOffer: a vehicle's own page offers working on it in the
// bar's place (layout.html, work-on-offer) while no vehicle is worked on,
// wherever the sidebar's Work on list is (2 or more active vehicles).
func TestWorkingOnOffer(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	own := "/vehicles/" + itoa(k390)
	offer := `<button class="working-on working-off" type="submit" name="vehicle" value="` + itoa(k390) + `"`
	rec := a.get(own)
	expect(t, rec, http.StatusOK, offer, "Work on KTM 390")
	expectNot(t, rec, workingBar)
	// Taking it works on the vehicle and comes back to its page, now with the bar.
	rec = a.post("/working-on", url.Values{"vehicle": {itoa(k390)}}, "Referer", "http://example.com"+own)
	expectRedirect(t, rec, own)
	if c := workingCookieSet(rec); c == nil || c.Value != itoa(k390) {
		t.Fatalf("cookie %+v", c)
	}
	rec = a.getWorking(own, itoa(k390))
	expect(t, rec, http.StatusOK, workingBar, "Working on <strong>KTM 390</strong>")
	expectNot(t, rec, "working-off")
	// Only on the vehicle's own page.
	for _, path := range []string{own + "/edit", "/stock", "/vehicles", "/"} {
		expectNot(t, a.get(path), "working-off")
	}
	// Not for an archived vehicle, which can't be worked on.
	if err := a.store.ArchiveVehicle(context.Background(), k1290, cur(t, a.store, store.WhatVehicle, k1290)); err != nil {
		t.Fatal(err)
	}
	expectNot(t, a.get("/vehicles/"+itoa(k1290)), "working-off")
	// With one active vehicle left there is nothing to choose.
	expectNot(t, a.get(own), "working-off")
}
