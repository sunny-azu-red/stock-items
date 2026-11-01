package store_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// AddShopping checks an item's amount against the unit it reads under the
// item's lock, as EnsureShopping does: the amount came from a page that
// showed the item's unit then.
func TestAddShoppingChecksTheItemsUnit(t *testing.T) {
	s, ctx := newStore(t)
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece})
	for _, tc := range []struct {
		name string
		in   stock.ShoppingInput
		ok   bool
	}{
		{"read in another unit", stock.ShoppingInput{ItemID: &oil, Quantity: ptr(2.0), Unit: stock.UnitPiece}, false},
		{"fractional pieces", stock.ShoppingInput{ItemID: &pads, Quantity: ptr(1.5), Unit: stock.UnitPiece}, false},
		{"finer than 0.01 ml", stock.ShoppingInput{ItemID: &oil, Quantity: ptr(1.005), Unit: stock.UnitML}, false},
		{"no unit given", stock.ShoppingInput{ItemID: &oil, Quantity: ptr(500.0)}, true},
		{"its unit", stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0), Unit: stock.UnitPiece}, true},
		{"no amount", stock.ShoppingInput{ItemID: &pads, Unit: stock.UnitGram}, true},
	} {
		_, err := s.AddShopping(ctx, tc.in)
		var invalid stock.Errors
		switch {
		case tc.ok && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case !tc.ok && (!errors.As(err, &invalid) || invalid["quantity"] == ""):
			t.Errorf("%s: want a quantity error, got %v", tc.name, err)
		}
	}
	list, err := s.ListShopping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]float64{}
	for _, e := range list {
		if e.Quantity != nil {
			got[*e.ItemID] = *e.Quantity
		}
	}
	if len(list) != 2 || got[oil] != 500 || got[pads] != 2 {
		t.Errorf("shopping list = %+v", list)
	}
}

// Linking a free-text entry to an item keeps what the text said in the
// note, unless it was just the item's name.
func TestLinkShoppingKeepsTheText(t *testing.T) {
	for _, tc := range []struct {
		name, text, note string
		listed           bool // the item has an entry already, noted "sintered"
		want             string
	}{
		{"own entry", "the cheap ones from the market", "rear", false, "the cheap ones from the market; rear"},
		{"own entry, no note", "the cheap ones from the market", "", false, "the cheap ones from the market"},
		{"own entry, the item's name", " brake PADS ", "rear", false, "rear"},
		{"own entry, brand and name", "EBC Brake Pads", "", false, ""},
		{"merged", "the cheap ones from the market", "rear", true, "sintered; the cheap ones from the market; rear"},
		{"merged, the item's name", "Brake Pads", "rear", true, "sintered; rear"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := newStore(t)
			pads := mustItem(t, s, stock.ItemInput{Brand: "EBC", Name: "Brake Pads", Unit: stock.UnitPiece})
			if tc.listed {
				if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Note: "sintered"}); err != nil {
					t.Fatal(err)
				}
			}
			free, err := s.AddShopping(ctx, stock.ShoppingInput{FreeText: tc.text, Note: tc.note})
			if err != nil {
				t.Fatal(err)
			}
			id, _, err := s.LinkShopping(ctx, free, cur(t, s, store.WhatShopping, free), pads)
			if err != nil {
				t.Fatal(err)
			}
			if e, err := s.GetShopping(ctx, id); err != nil || e.Note != tc.want || e.FreeText != "" {
				t.Errorf("entry = %+v, %v; want note %q", e, err, tc.want)
			}
		})
	}
}

func TestShoppingList(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece})

	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(1.0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0), Note: "sintered"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureShopping(ctx, pads, 2, ""); err != nil {
		t.Fatal(err)
	}
	freeID, err := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListShopping(ctx)
	if len(list) != 2 || *list[0].Quantity != 3 || list[0].Note != "sintered" || list[1].Title() != "Chain lube" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: ptr(int64(424242))}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown item: want ErrNotFound, got %v", err)
	}

	if _, _, err := s.MarkBought(ctx, list[0].ID, cur(t, s, store.WhatShopping, list[0].ID), &stock.MovementInput{Delta: 3, TotalPrice: ptr(42.0), Supplier: "KTM dealer"}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, pads); it.Quantity != 3 {
		t.Errorf("pads after purchase = %v", it.Quantity)
	}
	if _, _, err := s.MarkBought(ctx, freeID, cur(t, s, store.WhatShopping, freeID), nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListShopping(ctx); len(list) != 0 {
		t.Errorf("list should be empty: %+v", list)
	}
	if _, err := s.DeleteShopping(ctx, freeID, cur(t, s, store.WhatShopping, freeID)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)
}

func TestCreateItemFromShopping(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece})
	linked, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0)})
	if err != nil {
		t.Fatal(err)
	}
	free, err := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube", Quantity: ptr(1.0)})
	if err != nil {
		t.Fatal(err)
	}

	in := stock.ItemInput{Name: "Chain lube", Unit: stock.UnitPiece, Quantity: 1, TotalPrice: ptr(45.0), Supplier: "Shop"}
	in.Normalize()
	// A form opened before the entry changed is stale, and saves nothing.
	var stale *store.StaleError
	if _, _, err := s.CreateItemFromShopping(ctx, free, 0, in); !errors.As(err, &stale) || stale.Gone {
		t.Fatalf("stale entry: got %v", err)
	}
	id, trashID, err := s.CreateItemFromShopping(ctx, free, cur(t, s, store.WhatShopping, free), in)
	if err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); it.Name != "Chain lube" || it.Quantity != 1 {
		t.Errorf("created item = %+v", it)
	}
	if n, _ := s.CountPurchases(ctx, id); n != 1 {
		t.Errorf("purchases = %d, want 1", n)
	}
	if list, _ := s.ListShopping(ctx); len(list) != 1 || list[0].ID != linked {
		t.Errorf("only the linked entry should be left: %+v", list)
	}

	// A second submit of the same form, or an entry that is already an
	// item, creates nothing.
	for _, entry := range []int64{free, linked} {
		if _, _, err := s.CreateItemFromShopping(ctx, entry, cur(t, s, store.WhatShopping, entry), in); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("entry %d: want ErrNotFound, got %v", entry, err)
		}
	}
	if items, _ := s.ListItems(ctx, store.ItemFilter{}); len(items) != 2 {
		t.Errorf("items = %v, want Brake Pads and one Chain lube", ids(items))
	}

	// The entry went to the trash: Undo puts it back, and the item stays.
	if _, err := s.RestoreTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	if e, err := s.GetShopping(ctx, free); err != nil || e.FreeText != "Chain lube" || e.Quantity == nil || *e.Quantity != 1 {
		t.Errorf("restored entry = %+v, %v", e, err)
	}
	mustGet(t, s, id)
	assertLedger(t, s)
}

func TestMarkBoughtKeepsTheRest(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece})
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
	padsEntry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(3.0)})
	oilEntry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil})

	tests := []struct {
		name  string
		entry int64
		delta float64
		left  float64
		count int // entries left on the list
	}{
		{"less than listed", padsEntry, 1, 2, 2},
		{"the rest", padsEntry, 2, 0, 1},
		{"no quantity listed", oilEntry, 4000, 0, 0},
	}
	for _, tt := range tests {
		left, _, err := s.MarkBought(ctx, tt.entry, cur(t, s, store.WhatShopping, tt.entry), &stock.MovementInput{Delta: tt.delta})
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		list, _ := s.ListShopping(ctx)
		if left != tt.left || len(list) != tt.count {
			t.Errorf("%s: left %v with %d entries, want %v with %d", tt.name, left, len(list), tt.left, tt.count)
		}
		i := slices.IndexFunc(list, func(e stock.ShoppingEntry) bool { return e.ID == tt.entry })
		if (i >= 0) != (tt.left > 0) || (i >= 0 && *list[i].Quantity != tt.left) {
			t.Errorf("%s: list = %+v", tt.name, list)
		}
	}
	if it := mustGet(t, s, pads); it.Quantity != 3 {
		t.Errorf("pads = %v, want 3", it.Quantity)
	}
	assertLedger(t, s)
}

func TestFreeTextShoppingKeepsItsUnit(t *testing.T) {
	s, ctx := newStore(t)
	in := stock.ShoppingInput{FreeText: "Coolant", Quantity: ptr(4000.0), Unit: stock.UnitML}
	in.Normalize()
	id, err := s.AddShopping(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetShopping(ctx, id)
	if err != nil || e.ItemUnit != stock.UnitML || *e.Quantity != 4000 {
		t.Errorf("entry = %+v, %v", e, err)
	}
	if got := stock.ExportShoppingText([]stock.ShoppingEntry{e}); !strings.Contains(got, "4 L") {
		t.Errorf("export = %q, want 4 L", got)
	}
}

func TestShoppingRefusesArchivedAndReportsChanges(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	old := mustItem(t, s, stock.ItemInput{Name: "Old pads", Unit: stock.UnitPiece})
	if _, err := s.ArchiveItem(ctx, old, cur(t, s, store.WhatItem, old)); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		name    string
		qty     float64
		changed bool
	}{
		{"new entry", 2, true},
		{"same amount again", 2, false},
		{"less", 1, false},
		{"more", 3, true},
	}
	for _, st := range steps {
		changed, err := s.EnsureShopping(ctx, pads, st.qty, "")
		if err != nil || changed != st.changed {
			t.Errorf("%s: changed = %v, %v; want %v", st.name, changed, err, st.changed)
		}
	}
	if list, _ := s.ListShopping(ctx); len(list) != 1 || *list[0].Quantity != 3 {
		t.Errorf("list = %+v", list)
	}
	if _, err := s.EnsureShopping(ctx, old, 1, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("EnsureShopping archived: want ErrNotFound, got %v", err)
	}
	if _, err := s.EnsureShopping(ctx, 424242, 1, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("EnsureShopping missing: want ErrNotFound, got %v", err)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &old}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AddShopping archived: want ErrNotFound, got %v", err)
	}
}

func TestAddShoppingKeepsBothNotes(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	for _, note := range []string{"sintered", "", "from the dealer", "sintered"} {
		if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Note: note}); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := s.ListShopping(ctx); len(list) != 1 || list[0].Note != "sintered; from the dealer" {
		t.Errorf("note = %q", list[0].Note)
	}
}

func TestLinkShopping(t *testing.T) {
	s, ctx := newStore(t)
	lube := mustItem(t, s, stock.ItemInput{Name: "Chain lube", Brand: "Motul", Unit: stock.UnitML})
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake pads", Unit: stock.UnitPiece})
	if _, err := s.ArchiveItem(ctx, lube, cur(t, s, store.WhatItem, lube)); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"chain lube", " Motul Chain Lube "} {
		if got, _ := s.ItemsNamed(ctx, text); len(got) != 1 || got[0].ID != lube {
			t.Errorf("ItemsNamed(%q) = %v", text, ids(got))
		}
	}

	// A free-text entry becomes the archived item's entry, which is restored.
	free, _ := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube", Quantity: ptr(400.0), Unit: stock.UnitML, Note: "the white one"})
	entry, dropped, err := s.LinkShopping(ctx, free, cur(t, s, store.WhatShopping, free), lube)
	if err != nil || entry != free || dropped {
		t.Fatalf("link = %d, %v, %v", entry, dropped, err)
	}
	if it := mustGet(t, s, lube); it.Archived() {
		t.Error("item should be restored")
	}
	if e, _ := s.GetShopping(ctx, free); e.ItemID == nil || *e.ItemID != lube || *e.Quantity != 400 || e.Note != "the white one" {
		t.Errorf("linked entry = %+v", e)
	}
	if _, _, err := s.LinkShopping(ctx, free, cur(t, s, store.WhatShopping, free), pads); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("relinking: want ErrNotFound, got %v", err)
	}

	// When the item is listed already, the two entries merge; a quantity in
	// another unit is dropped.
	listed, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0), Note: "sintered"})
	other, _ := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Brake pads", Quantity: ptr(500.0), Unit: stock.UnitML, Note: "rear"})
	entry, dropped, err = s.LinkShopping(ctx, other, cur(t, s, store.WhatShopping, other), pads)
	if err != nil || entry != listed || !dropped {
		t.Fatalf("merge = %d, %v, %v; want %d with the ml amount dropped", entry, dropped, err, listed)
	}
	if e, _ := s.GetShopping(ctx, listed); *e.Quantity != 2 || e.Note != "sintered; rear" {
		t.Errorf("merged entry = %+v", e)
	}
	if _, err := s.GetShopping(ctx, other); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("free entry should be gone: %v", err)
	}
}

func TestEnsureShoppingAddsTheReason(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(1.0), Note: "sintered"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureShopping(ctx, pads, 2, "Short for KTM 390: Brakes"); err != nil {
		t.Fatal(err)
	}
	if e := mustShoppingEntry(t, s); e.Note != "sintered; Short for KTM 390: Brakes" || *e.Quantity != 2 {
		t.Errorf("entry = %+v", e)
	}
}

func mustShoppingEntry(t *testing.T, s *store.Store) stock.ShoppingEntry {
	t.Helper()
	list, err := s.ListShopping(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("shopping = %+v, %v", list, err)
	}
	return list[0]
}

// Archiving an item that is on the shopping list puts its entry in the
// trash, from where it comes back once the item is restored.
func TestArchivedItemsShoppingEntryGoesToTheTrash(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0), Note: "front"}); err != nil {
		t.Fatal(err)
	}
	trashID, err := s.ArchiveItem(ctx, pads, cur(t, s, store.WhatItem, pads))
	if err != nil || trashID == 0 {
		t.Fatalf("archive = %d, %v", trashID, err)
	}
	if list, _ := s.ListShopping(ctx); len(list) != 0 {
		t.Errorf("list = %+v", list)
	}
	var conflict *store.ConflictError
	if _, err := s.RestoreTrash(ctx, trashID); !errors.As(err, &conflict) {
		t.Errorf("restore while archived: want ConflictError, got %v", err)
	}
	if err := s.RestoreItem(ctx, pads, cur(t, s, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	list, _ := s.ListShopping(ctx)
	if len(list) != 1 || *list[0].Quantity != 2 || list[0].Note != "front" {
		t.Errorf("list = %+v", list)
	}
	// Not on the list: nothing goes to the trash.
	if _, err := s.DeleteShopping(ctx, list[0].ID, cur(t, s, store.WhatShopping, list[0].ID)); err != nil {
		t.Fatal(err)
	}
	if trashID, err := s.ArchiveItem(ctx, pads, cur(t, s, store.WhatItem, pads)); err != nil || trashID != 0 {
		t.Errorf("archive off the list = %d, %v", trashID, err)
	}
}
