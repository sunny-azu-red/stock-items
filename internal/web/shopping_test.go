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
	expect(t, a.post("/shopping", url.Values{"item": {"Chain lube"}, "quantity": {"2"}, "note": {"the white one"}}), http.StatusSeeOther)
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
	expect(t, a.post(item+"/movements", url.Values{"kind": {"bought"}, "amount": {"1"}, "amount_unit": {"l"}, "expires_on": {"2030-01-31"}}), http.StatusSeeOther)
	expect(t, a.post(item+"/movements", url.Values{"kind": {"used"}, "amount": {"100"}, "amount_unit": {"ml"}, "expires_on": {"2020-01-01"}}), http.StatusSeeOther)
	expect(t, a.post(item+"/movements", url.Values{"kind": {"used"}, "amount": {"100"}, "amount_unit": {"ml"}, "occurred_on": {"1980-01-01"}}),
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
	expect(t, a.post("/shopping", url.Values{"item": {"Coolant"}, "quantity": {"4 L"}}), http.StatusSeeOther)
	expect(t, a.post("/shopping", url.Values{"item": {"Grease"}, "quantity": {"2 bananas"}}), http.StatusUnprocessableEntity, "Unknown unit &#34;bananas&#34;: use pcs, ml, L, g or kg")
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
	// The kit needs 1.7 L of oil; 525 ml is in stock.
	expect(t, a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(oil)}, "line_qty": {"1.7 L"}}), http.StatusSeeOther)

	// The item page's button lists what the kit is short of.
	expect(t, a.post("/items/"+itoa(oil)+"/shopping", nil), http.StatusSeeOther)
	list, _ := a.store.ListShopping(ctx)
	if len(list) != 1 || *list[0].Quantity != 1175 || !strings.Contains(list[0].Note, "Short for the Oil service kit") {
		t.Fatalf("shopping = %+v", list)
	}
	expectNot(t, a.get("/shopping"), "already listed") // fully covered: no suggestion

	// Listed for less than needed: still suggested, with what's listed.
	if _, err := a.store.DeleteShopping(ctx, list[0].ID, cur(t, a.store, store.WhatShopping, list[0].ID)); err != nil {
		t.Fatal(err)
	}
	expect(t, a.post("/shopping", url.Values{"item_id": {itoa(oil)}, "quantity": {"1"}, "quantity_unit": {"ml"}}), http.StatusSeeOther)
	expect(t, a.get("/shopping"), http.StatusOK, "1 ml already listed")

	// A bare number takes the unit select.
	expect(t, a.post("/shopping", url.Values{"item_id": {itoa(pads)}, "item": {"OEM Rear Brake Pads"}, "quantity": {"2"}, "quantity_unit": {"pcs"}}),
		http.StatusSeeOther)
	expect(t, a.post("/shopping", url.Values{"item": {"Coolant"}, "quantity": {"4"}, "quantity_unit": {"l"}}), http.StatusSeeOther)
	for _, e := range mustShopping(t, a) {
		switch e.Title() {
		case "OEM Rear Brake Pads":
			if e.Note != "" || *e.Quantity != 2 {
				t.Errorf("pads entry = %+v", e)
			}
		case "Coolant":
			if e.ItemUnit != stock.UnitML || *e.Quantity != 4000 {
				t.Errorf("coolant entry = %+v", e)
			}
		}
	}
}

// TestShoppingItemField is Add to the list's one Item field: a stock item,
// picked or named, or something new (shoppingItem).
func TestShoppingItemField(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil, pads, filter := itemID(t, a.store, "Silkolene"), itemID(t, a.store, "Rear Brake"), itemID(t, a.store, "Hiflofiltro")
	// add posts the form to an empty list and returns the entry it made.
	add := func(t *testing.T, form url.Values) stock.ShoppingEntry {
		t.Helper()
		for _, e := range mustShopping(t, a) {
			if _, err := a.store.DeleteShopping(ctx, e.ID, e.Version); err != nil {
				t.Fatal(err)
			}
		}
		expect(t, a.post("/shopping", form), http.StatusSeeOther)
		list := mustShopping(t, a)
		if len(list) != 1 {
			t.Fatalf("shopping = %+v, want one entry", list)
		}
		return list[0]
	}
	linked := func(t *testing.T, e stock.ShoppingEntry, id int64) {
		t.Helper()
		if e.ItemID == nil || *e.ItemID != id || e.FreeText != "" {
			t.Errorf("entry = %+v, want item %d", e, id)
		}
	}
	free := func(t *testing.T, e stock.ShoppingEntry, text string) {
		t.Helper()
		if e.ItemID != nil || e.FreeText != text {
			t.Errorf("entry = %+v, want something new %q", e, text)
		}
	}

	t.Run("picked", func(t *testing.T) {
		e := add(t, url.Values{"item": {"Silkolene 15W50 PRO 4 XP"}, "item_id": {itoa(oil)}, "quantity": {"1"}, "quantity_unit": {"l"}})
		linked(t, e, oil)
		if *e.Quantity != 1000 {
			t.Errorf("quantity = %v, want 1000 ml", *e.Quantity)
		}
	})
	t.Run("typed exact name", func(t *testing.T) {
		linked(t, add(t, url.Values{"item": {"  silkolene 15w50 pro 4 xp "}}), oil)
		linked(t, add(t, url.Values{"item": {"rear brake pads"}, "quantity": {"2"}}), pads) // the name alone
	})
	t.Run("picked, then renamed in another tab", func(t *testing.T) {
		// The page clears item_id once the text no longer names the item, so
		// a posted one stands: the item was renamed since the page was shown.
		linked(t, add(t, url.Values{"item": {"Silkolene 15W50 (old name)"}, "item_id": {itoa(oil)}}), oil)
	})
	t.Run("something new with a unit", func(t *testing.T) {
		e := add(t, url.Values{"item": {"Coolant"}, "quantity": {"4 L"}})
		free(t, e, "Coolant")
		if e.ItemUnit != stock.UnitML || *e.Quantity != 4000 {
			t.Errorf("coolant = %+v, want 4 L", e)
		}
	})
	t.Run("a vehicle's part names only its items", func(t *testing.T) {
		free(t, add(t, url.Values{"item": {"Hiflofiltro Oil Filter"}, "v": {itoa(k390)}}), "Hiflofiltro Oil Filter")
		linked(t, add(t, url.Values{"item": {"Hiflofiltro Oil Filter"}}), filter)
	})
	t.Run("a form from before Item", func(t *testing.T) {
		free(t, add(t, url.Values{"free_text": {"Chain lube"}}), "Chain lube")
		linked(t, add(t, url.Values{"free_text": {"OEM Rear Brake Pads"}}), pads)
		linked(t, add(t, url.Values{"item_id": {itoa(pads)}, "quantity": {"2"}}), pads)
		// Both filled in: the item, with the text as a note, as then.
		e := add(t, url.Values{"item_id": {itoa(pads)}, "free_text": {"sintered ones"}, "note": {"rear"}})
		linked(t, e, pads)
		if e.Note != "sintered ones; rear" {
			t.Errorf("note = %q, want the text, then the note", e.Note)
		}
	})
	t.Run("empty", func(t *testing.T) {
		before := len(mustShopping(t, a))
		rec := a.post("/shopping", url.Values{"item": {"  "}, "quantity": {"3"}, "note": {"the white one"}})
		expect(t, rec, http.StatusUnprocessableEntity, `<div class="field span-2 has-error">`, `id="item-err"`, `value="3"`, `value="the white one"`)
		if n := strings.Count(rec.Body.String(), "Choose an item or type what to buy"); n != 1 {
			t.Errorf("the error shows %d times, want once", n)
		}
		if n := len(mustShopping(t, a)); n != before {
			t.Errorf("entries = %d, want %d", n, before)
		}
	})
	t.Run("repeated", func(t *testing.T) {
		form := url.Values{"item": {"Chain wax"}, "form_key": {newFormKey()}}
		e := add(t, form)
		expectSavedAlready(t, a.post("/shopping", form), "/shopping")
		if list := mustShopping(t, a); len(list) != 1 || list[0].ID != e.ID {
			t.Errorf("shopping = %+v, want Chain wax once", list)
		}
	})
	t.Run("two items of that name", func(t *testing.T) {
		in := stock.ItemInput{Brand: "KTM", Name: "Oil Filter", Unit: stock.UnitPiece}
		in.Normalize()
		ktm, err := a.store.CreateItem(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		free(t, add(t, url.Values{"item": {"Oil Filter"}}), "Oil Filter") // Bought offers both
		linked(t, add(t, url.Values{"item": {"KTM Oil Filter"}}), ktm)
	})
	t.Run("archived", func(t *testing.T) {
		if _, err := a.store.ArchiveItem(ctx, filter, cur(t, a.store, store.WhatItem, filter)); err != nil {
			t.Fatal(err)
		}
		e := add(t, url.Values{"item": {"Hiflofiltro Oil Filter"}})
		free(t, e, "Hiflofiltro Oil Filter")
		expect(t, a.get("/shopping/"+itoa(e.ID)+"/bought"), http.StatusOK, "Restore and add to it")
		expectNot(t, a.get("/shopping"), `<option value="Hiflofiltro Oil Filter"`)
	})
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

// TestShoppingCopyInCard: Copy as text goes with the list it copies, in the
// "On the list" card's foot, not in the page header; with nothing on the
// list there is none.
func TestShoppingCopyInCard(t *testing.T) {
	a := newApp(t)
	a.seed()
	expectNot(t, a.get("/shopping"), "Copy as text")
	expect(t, a.post("/shopping", url.Values{"item": {"Chain lube"}, "quantity": {"2"}}), http.StatusSeeOther)
	rec := a.get("/shopping")
	expect(t, rec, http.StatusOK, `<div class="card-foot card-actions"><a class="btn btn-sm" href="/shopping/export.txt" data-copy-url="/shopping/export.txt">`)
	if strings.Contains(headActions(t, rec), "Copy as text") || strings.Contains(headActions(t, rec), "actions") {
		t.Errorf("the shopping list's header has buttons:\n%s", headActions(t, rec))
	}
}
