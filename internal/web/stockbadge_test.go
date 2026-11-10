package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// newItem adds an item straight through the store.
func (a *testApp) newItem(in stock.ItemInput) int64 {
	a.t.Helper()
	in.Normalize()
	id, err := a.store.CreateItem(context.Background(), in)
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

// shortBadge is a part row's badge when it asks for need and only have is
// in stock.
func shortBadge(have, need string) string {
	return `<span class="badge badge-danger" data-stock-level="short" title="Need ` + need +
		`"><svg class="icon" aria-hidden="true"><use href="#i-x"></use></svg> ` + have + ` in stock</span>`
}

// haveBadge is the badge of an item with enough in stock, where the badge
// carries the quantity.
func haveBadge(have string) string {
	return `<span class="badge badge-muted" data-stock-level="ok">` + have + ` in stock</span>`
}

// lowBadge is the badge of an item below its minimum, with the quantity.
func lowBadge(have, min string) string {
	return `<span class="badge badge-warn" data-stock-level="low" title="Below the minimum of ` + min +
		`"><svg class="icon" aria-hidden="true"><use href="#i-triangle-alert"></use></svg> ` + have + ` in stock</span>`
}

// noShortRow fails when a part row's badge says short (the page's
// <template id="stock-badges"> holds one with {need} for app.js).
func noShortRow(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if m := shortRowRe.FindString(rec.Body.String()); m != "" {
		t.Errorf("a part row is short: %s", m)
	}
}

var shortRowRe = regexp.MustCompile(`data-stock-level="short" title="Need [^{][^"]*"`)

const outBadge = `<span class="badge badge-danger" data-stock-level="out">Out of stock</span>`

func TestNewStockBadge(t *testing.T) {
	oil := stock.Item{Unit: stock.UnitML, Quantity: 525, MinQuantity: ptr(1000.0)}
	pads := stock.Item{Unit: stock.UnitPiece, Quantity: 6}
	tests := []struct {
		name    string
		it      stock.Item
		need    float64
		withQty bool
		want    stockBadge
	}{
		{"out", stock.Item{Unit: stock.UnitPiece}, 1, true, stockBadge{Level: stock.LevelOut, Text: "Out of stock"}},
		{"out, state only", stock.Item{Unit: stock.UnitPiece}, 0, false, stockBadge{Level: stock.LevelOut, Text: "Out of stock"}},
		{"short", oil, 1700, true, stockBadge{Level: stock.LevelShort, Text: "525 ml in stock", Title: "Need 1.7 L"}},
		{"low", oil, 500, true, stockBadge{Level: stock.LevelLow, Text: "525 ml in stock", Title: "Below the minimum of 1 L"}},
		{"low, state only", oil, 0, false, stockBadge{Level: stock.LevelLow, Text: "Low · min 1 L"}},
		{"enough", pads, 2, true, stockBadge{Level: stock.LevelOK, Text: "6 pcs in stock"}},
		{"enough, state only", pads, 0, false, stockBadge{Level: stock.LevelOK}},
	}
	for _, tt := range tests {
		if got := newStockBadge(tt.it, tt.need, tt.withQty); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestDescribeRows(t *testing.T) {
	items := []stock.Item{
		{ID: 1, Unit: stock.UnitML, Quantity: 1500},
		{ID: 2, Unit: stock.UnitPiece, Quantity: 1},
	}
	rows := func() []lineRow {
		return []lineRow{
			{ItemID: 1, Qty: "1", Unit: "l"},
			{ItemID: 1, Qty: "600 ml"},
			{ItemID: 2, Qty: "lots"},
			{ItemID: 3, Qty: "1"}, // not in the list: no badge
		}
	}
	// A service: the two oil rows ask for 1.6 L of 1.5 L together.
	r := rows()
	describeRows(r, items, true)
	want := []stock.StockLevel{stock.LevelShort, stock.LevelShort, stock.LevelOK, ""}
	for i, w := range want {
		if r[i].Badge.Level != w {
			t.Errorf("service row %d: level %q, want %q", i, r[i].Badge.Level, w)
		}
	}
	if r[0].Badge.Title != "Need 1.6 L" {
		t.Errorf("title = %q", r[0].Badge.Title)
	}
	// A kit asks for nothing from stock.
	r = rows()
	describeRows(r, items, false)
	for i, w := range []stock.StockLevel{stock.LevelOK, stock.LevelOK, stock.LevelOK, ""} {
		if r[i].Badge.Level != w {
			t.Errorf("kit row %d: level %q, want %q", i, r[i].Badge.Level, w)
		}
	}
}

// The service form shows each part's stock badge before saving: out of
// stock and short in red, and what is in stock otherwise. A refused save
// marks the short rows without repeating the badge as an error.
func TestServiceFormStockBadges(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene") // 525 ml
	ring := itemID(t, a.store, "O-Ring")   // 6 pcs
	filter := a.newItem(stock.ItemInput{Name: "Oil Filter Service Kit", Brand: "KTM", Unit: stock.UnitPiece, Quantity: 0, VehicleIDs: []int64{k390}})
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(filter), itoa(oil), itoa(ring)}, "line_qty": {"1", "1.7 L", "2"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")

	form := a.get("/services/new?kit=" + kit[len("/kits/"):])
	expect(t, form, http.StatusOK, outBadge, shortBadge("525 ml", "1.7 L"), haveBadge("6 pcs"),
		`id="part-lines" data-stock-need`, `<template id="stock-badges" data-units="`)
	expectNot(t, form, "in stock:")

	// The kit form shows stock, but nothing is short there.
	edit := a.get(kit + "/edit")
	expect(t, edit, http.StatusOK, outBadge, haveBadge("525 ml"), haveBadge("6 pcs"))
	expectNot(t, edit, "data-stock-need")
	noShortRow(t, edit)

	// Saving refuses the short parts and marks their rows.
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "kit_id": {kit[len("/kits/"):]}, "title": {"Oil service"},
		"performed_on": {"2026-10-20"}, "part_item": {itoa(filter), itoa(oil), itoa(ring)}, "part_qty": {"1", "1.7 L", "2"}})
	expect(t, rec, http.StatusUnprocessableEntity, "2 parts are short", outBadge, shortBadge("525 ml", "1.7 L"), haveBadge("6 pcs"))
	expectNot(t, rec, "Only 525 ml in stock", "Only 0 pcs in stock")
	if n := strings.Count(rec.Body.String(), `has-error" data-short`); n != 2 {
		t.Errorf("%d rows marked short, want 2", n)
	}
}

// Every page that shows stock uses the one stock badge.
func TestStockBadgeAcrossPages(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	low := a.newItem(stock.ItemInput{Name: "Brake Pads", Brand: "Bajaj", Unit: stock.UnitPiece, Quantity: 1, MinQuantity: ptr(2.0), VehicleIDs: []int64{k390}})
	empty := a.newItem(stock.ItemInput{Name: "Front Brake Pads", Brand: "Bajaj", Unit: stock.UnitPiece, Quantity: 0, VehicleIDs: []int64{k390}})

	// Stock rows and the item page: state only, as the number is there.
	stateLow := `<span class="badge badge-warn" data-stock-level="low"><svg class="icon" aria-hidden="true"><use href="#i-triangle-alert"></use></svg> Low · min 2 pcs</span>`
	expect(t, a.get("/stock"), http.StatusOK, stateLow, outBadge)
	expect(t, a.get(itemPath(low)), http.StatusOK, stateLow)
	expect(t, a.get(itemPath(empty)), http.StatusOK, outBadge)
	expectNot(t, a.get(itemPath(itemID(t, a.store, "O-Ring"))), `data-stock-level=`)

	// Edit purchase: with the quantity.
	ctx := context.Background()
	expect(t, a.post(itemPath(low)+"/movements", url.Values{"kind": {"used"}, "amount": {"1"}}), http.StatusSeeOther)
	expect(t, a.post(itemPath(low)+"/movements", url.Values{"kind": {"bought"}, "amount": {"1"}}), http.StatusSeeOther)
	moves, err := a.store.ListMovements(ctx, store.MovementFilter{ItemID: low, Kind: stock.MoveBought})
	if err != nil || len(moves) != 1 {
		t.Fatalf("purchases = %v, %v", moves, err)
	}
	expect(t, a.get("/movements/"+itoa(moves[0].ID)+"/edit"), http.StatusOK, lowBadge("1 pcs", "2 pcs"))

	// Shopping suggestions, and the matches of a free-text entry.
	page := a.get("/shopping")
	expect(t, page, http.StatusOK, lowBadge("1 pcs", "2 pcs"), "Below minimum of 2 pcs</span>")
	expectNot(t, page, "· have ")
	free, err := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "bajaj brake pads", Quantity: ptr(2.0), Unit: stock.UnitPiece})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, a.get("/shopping/"+itoa(free)+"/bought"), http.StatusOK, "Already have it?", lowBadge("1 pcs", "2 pcs"))
}
