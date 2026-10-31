package web

import (
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// A past service or kit of a vehicle archived since can still be undone:
// bringing back a deleted record isn't new work for the vehicle.
func TestUndoForAnArchivedVehicle(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene")
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Chain"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Top up"}, "performed_on": {"2026-10-20"},
		"part_item": {itoa(oil)}, "part_qty": {"25"}})
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")
	kitUndo := flashOf(t, a.postV(kit+"/delete", nil)).Undo
	svcUndo := flashOf(t, a.postV(svc+"/delete", nil)).Undo
	if kitUndo == 0 || svcUndo == 0 {
		t.Fatalf("undo ids %d, %d", kitUndo, svcUndo)
	}
	// Archived in another tab (with neither left, its page offers Delete).
	if err := a.store.ArchiveVehicle(context.Background(), k390, cur(t, a.store, store.WhatVehicle, k390)); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		undo int64
		to   string
	}{{svcUndo, svc}, {kitUndo, kit}} {
		rec := a.post("/trash/"+itoa(tt.undo)+"/restore", nil)
		expect(t, rec, http.StatusSeeOther)
		if f := flashOf(t, rec); f.Kind == "error" {
			t.Errorf("undo %s: %s", tt.to, f.Message)
		}
		if l := rec.Header().Get("Location"); l != tt.to {
			t.Errorf("undo went to %s, want %s", l, tt.to)
		}
	}
	if got := a.quantity(t, oil); got != 500 {
		t.Errorf("oil = %v, want 500: the service's part taken again", got)
	}
	a.ledger(t)
	// Still no new work: neither a new service nor a new kit.
	expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"x"}, "performed_on": {"2026-10-29"}}),
		http.StatusUnprocessableEntity, "is archived")
}

// The services page of an archived vehicle offers no new kit or service,
// and its kits show no reminder, as on the kit's own page.
func TestServicesPageOfAnArchivedVehicle(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Chain"}, "interval_months": {"6"}}), http.StatusSeeOther)
	page := "/services?v=" + itoa(k390)
	expect(t, a.get(page), http.StatusOK, `href="/kits/new?vehicle=`+itoa(k390)+`"`, `href="/services/new?vehicle=`+itoa(k390)+`"`, "Not done yet")
	expect(t, a.postV("/vehicles/"+itoa(k390)+"/archive", nil), http.StatusSeeOther)
	rec := a.get(page)
	expect(t, rec, http.StatusOK, "Chain")
	expectNot(t, rec, `href="/kits/new`, `href="/services/new`, "Not done yet")
	// The archived vehicles list shows its next kit without a reminder too.
	expectNot(t, a.get("/vehicles?archived=1"), "Not done yet")
}

// Adding a reading to a service logged without one, then removing it
// again, leaves the km reminder counting from the odometer when it was
// logged, as before.
func TestAServiceKeepsItsOdometerAtLog(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed() // at 10,000 km
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"}, "interval_km": {"5000"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	fields := url.Values{"vehicle_id": {itoa(k390)}, "kit_id": {itoa(mustID(t, kit))}, "title": {"Oil service"}, "performed_on": {"2026-10-29"}}
	rec = a.post("/services", fields)
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")
	// Logged on the day it was done, as far as the reminder is concerned
	// (the test clock is fixed, the database's isn't).
	if _, err := a.pool.Exec(context.Background(), `UPDATE service_records SET created_at = '2026-10-29 12:00Z'`); err != nil {
		t.Fatal(err)
	}
	due := "<dd>" + stock.FormatKm(15000) + "</dd>"
	expect(t, a.get(kit), http.StatusOK, due)

	fields.Set("odometer_km", "10500")
	expect(t, a.postV(svc, fields), http.StatusSeeOther)
	expect(t, a.get(kit), http.StatusOK, "<dd>"+stock.FormatKm(15500)+"</dd>")
	fields.Del("odometer_km")
	expect(t, a.postV(svc, fields), http.StatusSeeOther)
	rec = a.get(kit)
	expect(t, rec, http.StatusOK, due)
	expectNot(t, rec, "Needs odometer")
}

// A count is dated the day picked in its form, like a purchase or a use.
func TestACountKeepsItsDate(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	rec := a.post(itemPath(oil)+"/movements", url.Values{"kind": {"correction"}, "amount": {"500"}, "occurred_on": {"2026-10-20"}})
	expect(t, rec, http.StatusSeeOther)
	moves, err := a.store.ListMovements(context.Background(), store.MovementFilter{ItemID: oil, Kind: stock.MoveCorrection})
	if err != nil || len(moves) != 1 {
		t.Fatalf("counts = %+v, %v", moves, err)
	}
	if got := moves[0].OccurredOn.Format(time.DateOnly); got != "2026-10-20" {
		t.Errorf("count dated %s, want 2026-10-20", got)
	}
	if got := a.quantity(t, oil); got != 500 {
		t.Errorf("oil = %v, want 500", got)
	}
	// Its stock change can be deleted and undone.
	rec = a.postV("/movements/"+itoa(moves[0].ID)+"/delete", nil)
	undo := flashOf(t, rec).Undo
	if undo == 0 {
		t.Fatalf("delete flash = %+v", flashOf(t, rec))
	}
	expect(t, a.post("/trash/"+itoa(undo)+"/restore", nil), http.StatusSeeOther)
	if m, err := a.store.GetMovement(context.Background(), moves[0].ID); err != nil || m.OccurredOn.Format(time.DateOnly) != "2026-10-20" {
		t.Errorf("restored count = %+v, %v", m, err)
	}
	a.ledger(t)
}

// Part number links put the number where the search URL's {q} is,
// escaped so that spaces, &, /, + and accents stay part of the one value,
// with or without the prefix. A URL without exactly one {q} is refused.
func TestPartNumberLinks(t *testing.T) {
	codes := []string{"77038005044", "AB 12", "A&B=C", "12/34", "A+B", "Ø8-ü", "50%"}
	a := newApp(t)
	in := stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece}
	for _, c := range codes {
		in.SKUs = append(in.SKUs, stock.SKU{Code: c, Kind: stock.SKUManufacturer})
	}
	in.Normalize()
	id, err := a.store.CreateItem(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, url, prefix string
		value             func(u *url.URL) string // the searched text, decoded
		rest              func(u *url.URL) bool   // the rest of the URL is intact
	}{
		{"the default", "https://www.google.com/search?q={q}", "",
			func(u *url.URL) string { return u.Query().Get("q") },
			func(u *url.URL) bool { return u.Host == "www.google.com" && u.Path == "/search" && len(u.Query()) == 1 }},
		{"the default, with a prefix", "https://www.google.com/search?q={q}", "site:de",
			func(u *url.URL) string { return u.Query().Get("q") },
			func(u *url.URL) bool { return u.Host == "www.google.com" && len(u.Query()) == 1 }},
		{"{q} in the query", "https://example.com/search?q={q}&lang=de", "site:de",
			func(u *url.URL) string { return u.Query().Get("q") },
			func(u *url.URL) bool { return u.Query().Get("lang") == "de" && len(u.Query()) == 2 }},
		{"{q} in the path", "https://parts.example.com/find/{q}/all?lang=de", "",
			func(u *url.URL) string { return strings.TrimSuffix(strings.TrimPrefix(u.Path, "/find/"), "/all") },
			func(u *url.URL) bool {
				return u.Query().Get("lang") == "de" && strings.Count(u.EscapedPath(), "/") == 3
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := New(a.store, Options{SKUSearchURL: tt.url, SKUSearchPrefix: tt.prefix,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, itemPath(id), nil))
			expect(t, rec, http.StatusOK)
			links := regexp.MustCompile(`<a class="sku-link" href="([^"]*)"`).FindAllStringSubmatch(rec.Body.String(), -1)
			if len(links) != len(codes) {
				t.Fatalf("%d part number links, want %d", len(links), len(codes))
			}
			for i, l := range links {
				u, err := url.Parse(html.UnescapeString(l[1]))
				if err != nil {
					t.Errorf("%s: %v", l[1], err)
					continue
				}
				want := codes[i]
				if tt.prefix != "" {
					want += " " + tt.prefix
				}
				if got := tt.value(u); got != want || !tt.rest(u) {
					t.Errorf("%s searches %q (rest intact: %v), want %q", l[1], got, tt.rest(u), want)
				}
			}
		})
	}
	for _, bad := range []string{"https://www.google.com/search?q=", "https://example.com/{q}?q={q}"} {
		if _, err := New(a.store, Options{SKUSearchURL: bad}); err == nil {
			t.Errorf("New accepted the search URL %q", bad)
		}
	}
}

// Photos in Recently deleted show their thumbnails, so several replaced
// photos of one item can be told apart, and the one brought back says
// where the newer one went.
func TestDeletedPhotosShowTheirThumbnails(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	for _, p := range []string{"first", "second", "third"} {
		if _, err := a.store.SetPhoto(ctx, oil, "image/jpeg", []byte(p+" photo"), []byte(p+" thumb")); err != nil {
			t.Fatal(err)
		}
	}
	trash, _ := a.store.ListTrash(ctx) // the first and second, replaced
	if len(trash) != 2 {
		t.Fatalf("trash = %+v", trash)
	}
	second := trash[0].ID
	rec := a.post("/trash/"+itoa(second)+"/restore", nil)
	f := flashOf(t, rec)
	if f.Message != "Restored the photo of Silkolene 15W50 PRO 4 XP. The newer photo is in Recently deleted." || f.Undo == 0 {
		t.Errorf("restore flash = %+v", f)
	}
	expect(t, a.get(itemPath(oil)+"/photo"), http.StatusOK, "second photo")

	trash, _ = a.store.ListTrash(ctx) // the third (just swapped out) and the first
	page := a.get("/trash")
	for i, want := range []string{"third thumb", "first thumb"} {
		src := "/trash/" + itoa(trash[i].ID) + "/thumb"
		expect(t, page, http.StatusOK, `<img class="thumb" src="`+src+`"`)
		rec := a.get(src)
		expect(t, rec, http.StatusOK, want)
		if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
			t.Errorf("%s: Content-Type %q", src, got)
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("%s: Cache-Control %q, want immutable", src, got)
		}
	}
	// Only photos have one.
	pads := itemID(t, a.store, "Rear Brake")
	del := flashOf(t, a.postV(itemPath(pads)+"/delete", nil)).Undo
	if del == 0 {
		t.Fatal("no Undo for the item delete")
	}
	expectNot(t, a.get("/trash"), `src="/trash/`+itoa(del)+`/thumb"`)
	expect(t, a.get("/trash/"+itoa(del)+"/thumb"), http.StatusNotFound)
	expect(t, a.get("/trash/999999/thumb"), http.StatusNotFound)

	// Undo of the swap brings the third back, as before.
	expect(t, a.post("/trash/"+itoa(f.Undo)+"/restore", nil), http.StatusSeeOther)
	expect(t, a.get(itemPath(oil)+"/photo"), http.StatusOK, "third photo")
}
