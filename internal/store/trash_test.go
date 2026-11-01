package store_test

import (
	"context"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

// A snapshot's amounts are checked in their item's unit: part of a piece
// is refused like an amount finer than 0.01, whether the item is in the
// snapshot, still on stock, or the amount is a free-text entry's.
func TestRestoreRefusesPartsOfAPiece(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	set := func(t *testing.T, trashID int64, path, v string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE trash SET payload = jsonb_set(payload, $2::text[], $3::jsonb) WHERE id = $1`,
			trashID, path, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name  string
		trash func(t *testing.T) int64
		path  string // the amount to set, as a jsonb_set path
		ok    string // a whole number of pieces
	}{
		{"stock change of an item on stock", func(t *testing.T) int64 {
			pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 4})
			if err := s.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MoveUse, Delta: -1}); err != nil {
				t.Fatal(err)
			}
			uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MoveUse})
			_, trashID, err := s.DeleteMovement(ctx, uses[0].ID, cur(t, s, store.WhatMovement, uses[0].ID))
			if err != nil {
				t.Fatal(err)
			}
			return trashID
		}, "{stock_movements,0,delta}", "-2"},
		{"item in the snapshot", func(t *testing.T) int64 {
			ring := mustItem(t, s, stock.ItemInput{Name: "Ring", Unit: stock.UnitPiece, Quantity: 3})
			trashID, err := s.DeleteItem(ctx, ring, cur(t, s, store.WhatItem, ring))
			if err != nil {
				t.Fatal(err)
			}
			return trashID
		}, "{items,0,min_quantity}", "2"},
		{"free-text entry", func(t *testing.T) int64 {
			free, err := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Zip ties", Quantity: ptr(10.0), Unit: stock.UnitPiece})
			if err != nil {
				t.Fatal(err)
			}
			trashID, err := s.DeleteShopping(ctx, free, cur(t, s, store.WhatShopping, free))
			if err != nil {
				t.Fatal(err)
			}
			return trashID
		}, "{shopping_entries,0,quantity}", "12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trashID := tc.trash(t)
			set(t, trashID, tc.path, "1.5")
			_, err := s.RestoreTrash(ctx, trashID)
			wantConflict(t, "restore of 1.5 pieces", err, "part of a piece (1.5)")
			set(t, trashID, tc.path, tc.ok)
			mustRestore(t, s, trashID)
			assertLedger(t, s)
		})
	}
}
