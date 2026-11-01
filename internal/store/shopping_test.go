package store_test

import (
	"errors"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
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
