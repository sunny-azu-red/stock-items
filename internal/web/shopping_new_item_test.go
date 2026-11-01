package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// Adding a free-text shopping entry to stock as a new item ticks the
// entry off: its form carries the entry's version, the entry goes to the
// trash, and Undo puts it back on the list (the item stays).
func TestNewItemFromShoppingEntry(t *testing.T) {
	ctx := context.Background()
	fields := url.Values{"name": {"Chain lube"}, "unit": {"piece"}, "quantity": {"2"}, "quantity_unit": {"pcs"}}
	open := func(t *testing.T, a *testApp, free int64) (key, version string) {
		t.Helper()
		page := "/items/new?shopping=" + itoa(free)
		fields.Set("shopping_id", itoa(free))
		return formKey(t, a, page, "/items"), openForm(t, a, page, "/items")
	}

	t.Run("stale entry", func(t *testing.T) {
		a := newApp(t)
		free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube", Quantity: ptr(2.0)})
		key, version := open(t, a, free)
		if version == "" {
			t.Fatal("the form carries no version")
		}
		// Removed and put back in another tab: a new version.
		trashID, err := a.store.DeleteShopping(ctx, free, cur(t, a.store, store.WhatShopping, free))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.RestoreTrash(ctx, trashID); err != nil {
			t.Fatal(err)
		}
		expect(t, a.post("/items", with(fields, key, version)), http.StatusUnprocessableEntity,
			"This shopping list entry was changed since you opened this", `href="/items/new?shopping=`+itoa(free)+`"`, `value="Chain lube"`)
		if items, _ := a.store.ListItems(ctx, store.ItemFilter{Query: "Chain lube"}); len(items) != 0 {
			t.Errorf("a stale form added %d items", len(items))
		}
		if _, err := a.store.GetShopping(ctx, free); err != nil {
			t.Errorf("entry: %v", err)
		}
	})

	t.Run("undo and repeat", func(t *testing.T) {
		a := newApp(t)
		free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube", Quantity: ptr(2.0), Note: "the white one"})
		key, version := open(t, a, free)
		rec := a.post("/items", with(fields, key, version))
		expect(t, rec, http.StatusSeeOther)
		if l := rec.Header().Get("Location"); l != "/shopping" {
			t.Errorf("location = %q, want /shopping", l)
		}
		item := itemID(t, a.store, "Chain lube")
		undo := flashOf(t, rec).Undo
		if undo == 0 {
			t.Fatalf("no Undo: %+v", flashOf(t, rec))
		}
		if _, err := a.store.GetShopping(ctx, free); err == nil {
			t.Error("the entry is still on the list")
		}

		rec = a.post("/trash/"+itoa(undo)+"/restore", nil)
		expect(t, rec, http.StatusSeeOther)
		if f := flashOf(t, rec); f.Kind == "error" {
			t.Fatalf("undo: %s", f.Message)
		}
		e, err := a.store.GetShopping(ctx, free)
		if err != nil || e.FreeText != "Chain lube" || e.Note != "the white one" || e.Quantity == nil || *e.Quantity != 2 {
			t.Errorf("entry after Undo = %+v, %v", e, err)
		}
		if a.quantity(t, item) != 2 {
			t.Errorf("the item should stay with its stock")
		}

		// The same form again: saved already, so no second item, and the
		// entry put back stays on the list.
		expectSavedAlready(t, a.post("/items", with(fields, key, version)), itemPath(item))
		if items, _ := a.store.ListItems(ctx, store.ItemFilter{Query: "Chain lube"}); len(items) != 1 {
			t.Errorf("items = %d, want 1", len(items))
		}
		if _, err := a.store.GetShopping(ctx, free); err != nil {
			t.Errorf("entry: %v", err)
		}
		a.ledger(t)
	})
}
