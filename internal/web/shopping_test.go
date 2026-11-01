package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

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
		"total_price": {"60"}, "supplier": {"Moto shop"}, "bought_on": {"2026-10-29"}, "version": {formVersion(t, rec.Body.String(), "/items")}}
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
	rec = a.post("/items", form)
	expect(t, rec, http.StatusUnprocessableEntity, "The shopping entry is gone")
	expectNot(t, rec, `name="shopping_id"`)
	expect(t, a.get("/items/new?shopping="+entry), http.StatusNotFound)

	// An entry for an existing item keeps the purchase form.
	pads := itemID(t, a.store, "Rear Brake")
	linked, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads})
	expect(t, a.get("/items/new?shopping="+itoa(linked)), http.StatusNotFound)
	expect(t, a.get("/shopping/"+itoa(linked)+"/bought"), http.StatusOK, `name="amount"`, `name="total_price"`)

	// Or just tick a free-text entry off.
	free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Zip ties"})
	expect(t, a.postV("/shopping/"+itoa(free)+"/bought", nil), http.StatusSeeOther)
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

	expect(t, a.postV(path, url.Values{"amount": {"1"}, "amount_unit": {"l"}, "occurred_on": {"2062-10-29"}}),
		http.StatusUnprocessableEntity, "Date cannot be in the future")
	expect(t, a.postV(path, url.Values{"amount": {"1"}, "amount_unit": {"l"}, "expires_on": {"2029-06-30"}}), http.StatusSeeOther)
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

func TestNewItemFromShoppingNeedsAnAmount(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube"})
	expect(t, a.post("/items", url.Values{"shopping_id": {itoa(free)}, "name": {"Chain lube"}, "unit": {"piece"}}),
		http.StatusUnprocessableEntity, "Enter how much you bought")
	if _, err := a.store.GetShopping(ctx, free); err != nil {
		t.Errorf("entry should still be on the list: %v", err)
	}
}

func TestShoppingListNeeds(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	pads := itemID(t, a.store, "Rear Brake")
	// The kit needs 1.7 L of oil; 525 ml is on stock.
	expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(oil)}, "line_qty": {"1.7 L"}}), http.StatusSeeOther)

	// The item page's button lists what the kit is short of.
	expect(t, a.post("/items/"+itoa(oil)+"/shopping", nil), http.StatusSeeOther)
	list, _ := a.store.ListShopping(ctx)
	if len(list) != 1 || *list[0].Quantity != 1175 || !strings.Contains(list[0].Note, "Short for KTM 390") {
		t.Fatalf("shopping = %+v", list)
	}
	expectNot(t, a.get("/shopping"), "already listed") // fully covered: no suggestion

	// Listed for less than needed: still suggested, with what's listed.
	if _, err := a.store.DeleteShopping(ctx, list[0].ID, cur(t, a.store, store.WhatShopping, list[0].ID)); err != nil {
		t.Fatal(err)
	}
	expect(t, a.post("/shopping", url.Values{"item_id": {itoa(oil)}, "quantity": {"1"}, "quantity_unit": {"ml"}}), http.StatusSeeOther)
	expect(t, a.get("/shopping"), http.StatusOK, "1 ml already listed")

	// A bare number takes the unit select; text typed next to an item becomes its note.
	expect(t, a.post("/shopping", url.Values{"item_id": {itoa(pads)}, "free_text": {"sintered ones"}, "quantity": {"2"}, "quantity_unit": {"pcs"}}),
		http.StatusSeeOther)
	expect(t, a.post("/shopping", url.Values{"free_text": {"Coolant"}, "quantity": {"4"}, "quantity_unit": {"l"}}), http.StatusSeeOther)
	for _, e := range mustShopping(t, a) {
		switch e.Title() {
		case "OEM Rear Brake Pads":
			if e.Note != "sintered ones" || *e.Quantity != 2 {
				t.Errorf("pads entry = %+v", e)
			}
		case "Coolant":
			if e.ItemUnit != stock.UnitML || *e.Quantity != 4000 {
				t.Errorf("coolant entry = %+v", e)
			}
		}
	}
}

func mustShopping(t *testing.T, a *testApp) []stock.ShoppingEntry {
	t.Helper()
	list, err := a.store.ListShopping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestBuyingSomethingYouAlreadyHave(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "silkolene 15w50 pro 4 xp", Quantity: ptr(1000.0), Unit: stock.UnitML})
	page := "/shopping/" + itoa(free)
	expect(t, a.get(page+"/bought"), http.StatusOK, "Already have it?", `action="`+page+`/link"`, "Add to this item")
	expect(t, a.get("/items/new?shopping="+itoa(free)), http.StatusOK, "Already have it?")

	rec := a.postV(page+"/link", url.Values{"item_id": {itoa(oil)}})
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != page+"/bought" {
		t.Fatalf("location = %q", l)
	}
	expect(t, a.get(page+"/bought"), http.StatusOK, `name="amount" value="1"`, `value="l" selected`)
	expect(t, a.postV(page+"/bought", url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"20"}}), http.StatusSeeOther)
	if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 1525 {
		t.Errorf("oil = %v, want 1525", it.Quantity)
	}
}

func TestShoppingFlowsThatRaceOtherTabs(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()

	// The entry is ticked off in another tab while the new-item form is open.
	free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube"})
	if _, _, err := a.store.MarkBought(ctx, free, cur(t, a.store, store.WhatShopping, free), nil); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"shopping_id": {itoa(free)}, "name": {"Chain lube"}, "unit": {"piece"}, "quantity": {"1"}}
	rec := a.post("/items", form)
	expect(t, rec, http.StatusUnprocessableEntity, "The shopping entry is gone", `value="Chain lube"`)
	expectNot(t, rec, `name="shopping_id"`)
	form.Del("shopping_id") // what the re-rendered form posts
	expect(t, a.post("/items", form), http.StatusSeeOther)
	itemID(t, a.store, "Chain lube")

	// "Add to this item" clicked twice.
	oil := itemID(t, a.store, "Silkolene")
	entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "silkolene 15w50 pro 4 xp"}) // its Bought page offers the item
	link := "/shopping/" + itoa(entry) + "/link"
	version := a.versionOf(link)
	expect(t, a.post(link, url.Values{"item_id": {itoa(oil)}, "version": {version}}), http.StatusSeeOther)
	rec = a.post(link, url.Values{"item_id": {itoa(oil)}, "version": {version}}) // from the same page
	if f := flashOf(t, rec); f.Message != "" {
		t.Errorf("second click flash = %+v", f)
	}
	if l := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || l != "/shopping/"+itoa(entry)+"/bought" {
		t.Errorf("second click = %d %q", rec.Code, l)
	}

	// An item archived meanwhile can't be added.
	pads := itemID(t, a.store, "Rear Brake")
	if _, err := a.store.ArchiveItem(ctx, pads, cur(t, a.store, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	expect(t, a.post("/shopping", url.Values{"item_id": {itoa(pads)}}), http.StatusUnprocessableEntity, "archived or deleted meanwhile")
}

func TestNewItemFromShoppingKeepsMatchesAfterAnError(t *testing.T) {
	a := newApp(t)
	a.seed()
	free, _ := a.store.AddShopping(context.Background(), stock.ShoppingInput{FreeText: "rear brake pads"})
	expect(t, a.post("/items", url.Values{"shopping_id": {itoa(free)}, "name": {"Rear Brake Pads"}, "unit": {"piece"}}),
		http.StatusUnprocessableEntity, "Enter how much you bought", "Already have it?")
}
