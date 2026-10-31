package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

func day(m time.Month, d int) *time.Time {
	t := time.Date(2026, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestItemSortOrders(t *testing.T) {
	s, ctx := newStore(t)
	mk := map[string]int64{}
	add := func(label string, in stock.ItemInput) {
		mk[label] = mustItem(t, s, in)
	}
	// Created in this order, so "recent" starts out as A first.
	add("A", stock.ItemInput{Name: "Axle nut", Unit: stock.UnitPiece, Quantity: 4, Category: "Hardware", Location: "Shelf B"})
	add("B", stock.ItemInput{Name: "brake fluid", Unit: stock.UnitML, Quantity: 500, Category: "Fluids", ExpiresOn: day(12, 20)})
	add("C", stock.ItemInput{Name: "Chain lube", Unit: stock.UnitML, Quantity: 250, Location: "Shelf A", ExpiresOn: day(12, 1)})
	add("D1", stock.ItemInput{Name: "Disc bolt", Unit: stock.UnitPiece, Quantity: 4, Category: "Hardware", Location: "Shelf B"})
	add("D2", stock.ItemInput{Name: "Disc bolt", Unit: stock.UnitPiece, Quantity: 10, Category: "hardware", Location: "shelf a", ExpiresOn: day(12, 1)})
	add("E", stock.ItemInput{Name: "Engine grease", Unit: stock.UnitGram, Quantity: 100})
	// Renaming A makes it the most recently changed.
	upd := stock.ItemInput{Name: "Axle nut", Unit: stock.UnitPiece, Quantity: 4, QuantityWas: ptr(4.0), Category: "Hardware", Location: "Shelf B", Notes: "M10"}
	if err := s.UpdateItem(ctx, mk["A"], itemV(t, s, mk["A"], upd), time.Now()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		sort string
		desc bool
		want []string
	}{
		// Names ignore case; the two Disc bolts tie, and go by id.
		{"", false, []string{"A", "B", "C", "D1", "D2", "E"}},
		{store.SortName, false, []string{"A", "B", "C", "D1", "D2", "E"}},
		{store.SortName, true, []string{"E", "D2", "D1", "C", "B", "A"}},
		// No category comes last both ways; ties go by name, then id.
		{store.SortCategory, false, []string{"B", "A", "D1", "D2", "C", "E"}},
		{store.SortCategory, true, []string{"A", "D1", "D2", "B", "C", "E"}},
		{store.SortLocation, false, []string{"C", "D2", "A", "D1", "B", "E"}},
		{store.SortLocation, true, []string{"A", "D1", "C", "D2", "B", "E"}},
		// Grouped by unit (g, ml, pieces) both ways; equal amounts by name.
		{store.SortQuantity, false, []string{"E", "C", "B", "A", "D1", "D2"}},
		{store.SortQuantity, true, []string{"E", "B", "C", "D2", "A", "D1"}},
		// No best-before date comes last both ways.
		{store.SortExpiry, false, []string{"C", "D2", "B", "A", "D1", "E"}},
		{store.SortExpiry, true, []string{"B", "C", "D2", "A", "D1", "E"}},
		{store.SortRecent, true, []string{"A", "E", "D2", "D1", "C", "B"}},
		{store.SortRecent, false, []string{"B", "C", "D1", "D2", "E", "A"}},
		// Never part of the SQL: an unknown key sorts by name.
		{"id; DROP TABLE items", false, []string{"A", "B", "C", "D1", "D2", "E"}},
	}
	for _, tt := range tests {
		items := mustList(t, s, store.ItemFilter{Sort: tt.sort, Desc: tt.desc})
		var got []string
		for _, it := range items {
			for label, id := range mk {
				if id == it.ID {
					got = append(got, label)
				}
			}
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("sort %q desc=%v: got %v, want %v", tt.sort, tt.desc, got, tt.want)
		}
	}
}

func TestMovementSortOrders(t *testing.T) {
	s, ctx := newStore(t)
	filter := mustItem(t, s, stock.ItemInput{Name: "Air filter", Unit: stock.UnitPiece})
	fluid := mustItem(t, s, stock.ItemInput{Name: "brake fluid", Unit: stock.UnitML})
	var ids []int64
	move := func(item int64, kind stock.MovementKind, delta float64, price *float64, on *time.Time) {
		t.Helper()
		if err := s.AddMovement(ctx, stock.MovementInput{ItemID: item, Kind: kind, Delta: delta, TotalPrice: price, OccurredOn: on}); err != nil {
			t.Fatal(err)
		}
		moves, err := s.ListMovements(ctx, store.MovementFilter{})
		if err != nil {
			t.Fatal(err)
		}
		var newest int64
		for _, m := range moves {
			newest = max(newest, m.ID)
		}
		ids = append(ids, newest)
	}
	move(filter, stock.MovePurchase, 2, ptr(20.0), day(10, 1))   // m1
	move(fluid, stock.MovePurchase, 1000, ptr(15.0), day(10, 2)) // m2
	move(filter, stock.MoveUse, -1, nil, day(10, 3))             // m3
	move(fluid, stock.MoveUse, -250, nil, day(10, 3))            // m4
	move(fluid, stock.MoveCorrection, 50, nil, day(10, 4))       // m5
	move(filter, stock.MovePurchase, 1, ptr(20.0), day(10, 4))   // m6
	if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != 6 {
		t.Fatalf("ids = %v", ids)
	}
	assertLedger(t, s)

	tests := []struct {
		sort string
		desc bool
		want []int // m1..m6
	}{
		{"", false, []int{6, 5, 4, 3, 2, 1}}, // newest first; same day by id
		{store.MoveSortDate, true, []int{6, 5, 4, 3, 2, 1}},
		{store.MoveSortDate, false, []int{1, 2, 3, 4, 5, 6}},
		// Within an item (or kind, change, price), newest first.
		{store.MoveSortItem, false, []int{6, 3, 1, 5, 4, 2}},
		{store.MoveSortItem, true, []int{5, 4, 2, 6, 3, 1}},
		// By the labels shown: Correction, Purchase, Used.
		{store.MoveSortKind, false, []int{5, 6, 2, 1, 4, 3}},
		{store.MoveSortKind, true, []int{4, 3, 6, 2, 1, 5}},
		// Grouped by unit (ml before pieces) both ways.
		{store.MoveSortChange, false, []int{4, 5, 2, 3, 6, 1}},
		{store.MoveSortChange, true, []int{2, 5, 4, 1, 6, 3}},
		// No price comes last both ways.
		{store.MoveSortPrice, true, []int{6, 1, 2, 5, 4, 3}},
		{store.MoveSortPrice, false, []int{2, 6, 1, 5, 4, 3}},
		// Never part of the SQL: an unknown key sorts by date.
		{"m.id; DROP TABLE stock_movements", true, []int{6, 5, 4, 3, 2, 1}},
	}
	for _, tt := range tests {
		moves, err := s.ListMovements(ctx, store.MovementFilter{Sort: tt.sort, Desc: tt.desc})
		if err != nil {
			t.Fatal(err)
		}
		var got []int
		for _, m := range moves {
			got = append(got, slices.Index(ids, m.ID)+1)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("sort %q desc=%v: got m%v, want m%v", tt.sort, tt.desc, got, tt.want)
		}
	}

	// Pages don't overlap or skip in any order.
	for _, key := range []string{store.MoveSortItem, store.MoveSortKind, store.MoveSortPrice} {
		var paged []int64
		for off := 0; off < 6; off += 4 {
			moves, err := s.ListMovements(ctx, store.MovementFilter{Sort: key, Limit: 4, Offset: off})
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range moves {
				paged = append(paged, m.ID)
			}
		}
		if len(paged) != 6 || len(slices.Compact(slices.Sorted(slices.Values(paged)))) != 6 {
			t.Errorf("sort %q in pages of 4: %v", key, paged)
		}
	}
}
