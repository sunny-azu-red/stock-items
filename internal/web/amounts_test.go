package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// grease adds an item counted in grams, with qty on stock.
func (a *testApp) grease(qty float64) int64 {
	a.t.Helper()
	in := stock.ItemInput{Brand: "Motorex", Name: "Grease", Unit: stock.UnitGram, Quantity: qty}
	in.Normalize()
	id, err := a.store.CreateItem(context.Background(), in)
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

// Amounts are kept with at most two decimals of ml or g, and whole pieces.
// Every form refuses a finer amount with a reason (one typed in L or kg
// says what it comes to), keeps what was typed, and saves nothing; it is
// not rounded away.
func TestAmountsFinerThanTwoDecimalsAreRefused(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	oil := itemID(t, a.store, "Silkolene")
	grease := a.grease(100)
	twoG := "use at most two decimals of g"
	kg := "1.234567 kg is 1234.567 g: use at most two decimals of g"
	whole := "pieces must be a whole number"
	newItem := func(unit, qty, sub string) url.Values {
		return url.Values{"name": {"Assembly paste"}, "unit": {unit}, "quantity": {qty}, "quantity_unit": {sub}}
	}
	kit := func(item int64, qty, unit string) url.Values {
		return url.Values{"vehicle_id": {itoa(k390)}, "name": {"Chain"}, "line_item": {itoa(item)}, "line_qty": {qty}, "line_unit": {unit}}
	}
	service := func(item int64, qty, unit string) url.Values {
		return url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain"}, "performed_on": {"2026-10-20"},
			"part_item": {itoa(item)}, "part_qty": {qty}, "part_unit": {unit}}
	}
	use := func(qty, unit string) url.Values {
		return url.Values{"kind": {"use"}, "amount": {qty}, "amount_unit": {unit}}
	}

	for _, tt := range []struct {
		name, path string
		form       url.Values
		want       string
	}{
		{"item: 0.234 g", "/items", newItem("g", "0.234", "g"), twoG},
		{"item: 1.234567 kg", "/items", newItem("g", "1.234567", "kg"), kg},
		{"item: 1.0004 pieces", "/items", newItem("piece", "1.0004", "pcs"), whole},
		{"item: minimum 0.001 ml", "/items", url.Values{"name": {"X"}, "unit": {"ml"}, "min_quantity": {"0.001"}, "min_unit": {"ml"}},
			"use at most two decimals of ml"},
		{"item: pack size 0.000001 L", "/items", url.Values{"name": {"X"}, "unit": {"ml"}, "sku_code": {"SK-1"}, "sku_kind": {"oem"},
			"sku_pack": {"0.000001"}, "sku_pack_unit": {"l"}}, "0.000001 L is 0.001 ml"},
		{"use: 0.234 g", itemPath(grease) + "/movements", use("0.234", "g"), "Use at most two decimals of g"},
		{"use: 1.234567 kg", itemPath(grease) + "/movements", use("1.234567", "kg"), kg},
		{"use: 1.0004 pieces", itemPath(pads) + "/movements", use("1.0004", "pcs"), "Pieces must be a whole number"},
		{"kit: 0.234 g", "/kits", kit(grease, "0.234", "g"), twoG},
		{"kit: 1.234567 kg", "/kits", kit(grease, "1.234567", "kg"), kg},
		{"kit: 1.0004 pieces", "/kits", kit(pads, "1.0004", "pcs"), whole},
		{"service: 0.234 g", "/services", service(grease, "0.234", "g"), twoG},
		{"service: 1.234567 kg", "/services", service(grease, "1.234567", "kg"), kg},
		{"service: 1.0004 pieces", "/services", service(pads, "1.0004", "pcs"), whole},
		{"service: 0.001 L", "/services", service(oil, "0.000001", "l"), "0.000001 L is 0.001 ml"},
		{"shopping: 0.234 g", "/shopping", url.Values{"free_text": {"Paste"}, "quantity": {"0.234"}, "quantity_unit": {"g"}}, twoG},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := a.post(tt.path, tt.form)
			typed := tt.form.Get("quantity") + tt.form.Get("amount") + tt.form.Get("line_qty") + tt.form.Get("part_qty") +
				tt.form.Get("min_quantity") + tt.form.Get("sku_pack")
			expect(t, rec, http.StatusUnprocessableEntity, tt.want, `value="`+typed+`"`)
		})
	}
	// Nothing was saved, and no stock moved.
	var items, kits, services, shopping int
	if err := a.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM items), (SELECT count(*) FROM service_kits),
		(SELECT count(*) FROM service_records), (SELECT count(*) FROM shopping_entries)`).Scan(&items, &kits, &services, &shopping); err != nil {
		t.Fatal(err)
	}
	if items != 7 || kits != 0 || services != 0 || shopping != 0 {
		t.Errorf("saved: %d items, %d kits, %d services, %d shopping entries; want only the 7 items", items, kits, services, shopping)
	}
	if got := a.quantity(t, grease); got != 100 {
		t.Errorf("grease = %v, want 100", got)
	}
	a.ledger(t)

	// Two decimals of g are fine, also typed in kg: 1.2345 kg is 1234.5 g.
	rec := a.post("/items", newItem("g", "1.2345", "kg"))
	expect(t, rec, http.StatusSeeOther)
	if got := a.quantity(t, mustID(t, rec.Header().Get("Location"))); got != 1234.5 {
		t.Errorf("1.2345 kg stored as %v g, want 1234.5", got)
	}
	expect(t, a.post(itemPath(grease)+"/movements", use("0.25", "g")), http.StatusSeeOther)
	if got := a.quantity(t, grease); got != 99.75 {
		t.Errorf("grease = %v, want 99.75", got)
	}
	a.ledger(t)
}

// With amounts kept to two decimals, as the pages show them, "not enough
// stock" can't name the amount asked for: using 0.231 g of 0.23 g used to
// fail with "Only 0.23 g on stock" (and "have 0.23 g, need 0.23 g" where
// the whole error shows). Now the amount itself is refused.
func TestNotEnoughStockNeverReadsTheSameAmountTwice(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	grease := a.grease(0.23)
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain"}, "performed_on": {"2026-10-20"},
		"part_item": {itoa(grease)}, "part_qty": {"0.231"}, "part_unit": {"g"}})
	expect(t, rec, http.StatusUnprocessableEntity, "use at most two decimals of g")
	expectNot(t, rec, "Only 0.23 g on stock")
	rec = a.post(itemPath(grease)+"/movements", url.Values{"kind": {"use"}, "amount": {"0.231"}, "amount_unit": {"g"}})
	expect(t, rec, http.StatusUnprocessableEntity, "Use at most two decimals of g")
	expectNot(t, rec, "Only 0.23 g on stock")
	// One hundredth more than there is says so plainly.
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain"}, "performed_on": {"2026-10-20"},
		"part_item": {itoa(grease)}, "part_qty": {"0.24"}, "part_unit": {"g"}})
	expect(t, rec, http.StatusUnprocessableEntity, "Only 0.23 g on stock")
	if got := a.quantity(t, grease); got != 0.23 {
		t.Errorf("grease = %v, want 0.23", got)
	}
	a.ledger(t)
}

// The suggestions' hidden amounts are checked like typed ones: a forged
// fractional piece or a third decimal of ml is not added (nor rounded).
func TestSuggestionAmountsAreChecked(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	oil := itemID(t, a.store, "Silkolene")
	ring := itemID(t, a.store, "O-Ring")
	rec := a.post("/shopping/suggestions", url.Values{"item_id": {itoa(pads), itoa(oil), itoa(ring)},
		"qty": {"1.5", "12.255", "2"}, "note": {"", "", ""}})
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Message != "Added 1 item to the shopping list" {
		t.Errorf("flash = %q", f.Message)
	}
	list, err := a.store.ListShopping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ItemID == nil || *list[0].ItemID != ring {
		t.Errorf("listed: %+v; want only the O-rings", list)
	}
	expect(t, a.post("/shopping/suggestions", url.Values{"item_id": {itoa(oil)}, "qty": {"12.25"}}), http.StatusSeeOther)
	if q := shoppingQty(t, a, oil); q != 12.25 {
		t.Errorf("oil listed with %v, want 12.25", q)
	}
}

func shoppingQty(t *testing.T, a *testApp, item int64) float64 {
	t.Helper()
	list, err := a.store.ListShopping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if e.ItemID != nil && *e.ItemID == item && e.Quantity != nil {
			return *e.Quantity
		}
	}
	return 0
}
