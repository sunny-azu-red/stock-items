package store_test

import (
	"errors"
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
