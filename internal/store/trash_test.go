package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

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

// A service whose kit was deleted for good since comes back without it,
// with its parts, and says it dropped the link.
func TestRestoredServiceDropsItsPurgedKit(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1000})
	kin := stock.KitInput{VehicleID: v, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 100}}}
	kin.Normalize()
	kit, err := s.CreateKit(ctx, kin)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v, KitID: &kit, Title: "Oil service", PerformedOn: time.Now(),
		Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	svcTrash, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err != nil {
		t.Fatal(err)
	}
	kitTrash, err := s.DeleteKit(ctx, kit, cur(t, s, store.WhatKit, kit))
	if err != nil {
		t.Fatal(err)
	}
	if others, err := s.PurgeTrash(ctx, kitTrash); err != nil || others != 0 {
		t.Fatalf("purge kit = %d, %v", others, err)
	}

	res := mustRestore(t, s, svcTrash)
	if res.Dropped != 1 || res.ID != svc {
		t.Errorf("restored = %+v, want the kit link dropped", res)
	}
	r, err := s.GetService(ctx, svc)
	if err != nil || r.KitID != nil || len(r.Parts) != 1 {
		t.Errorf("service = %+v, %v", r, err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 900 {
		t.Errorf("oil = %v, want 900", it.Quantity)
	}
	assertLedger(t, s)
}

// An item's shopping entry doesn't come back while the item is archived:
// archived items are off the list.
func TestRestoredShoppingEntryNeedsItsItemInUse(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	entry, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0)})
	if err != nil {
		t.Fatal(err)
	}
	trashID, err := s.DeleteShopping(ctx, entry, cur(t, s, store.WhatShopping, entry))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveItem(ctx, pads, cur(t, s, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash(ctx, trashID)
	wantConflict(t, "restore for an archived item", err, "Its item is archived")
	if err := s.RestoreItem(ctx, pads, cur(t, s, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	if e, err := s.GetShopping(ctx, entry); err != nil || *e.Quantity != 2 {
		t.Errorf("entry = %+v, %v", e, err)
	}
}

// Undoing a Bought after what it touched changed since.
func TestUndoBoughtAfterChanges(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		// since changes things after the Bought; the undo should then fail
		// with conflict, or bring the list to want (pads entry quantity),
		// leaving qty on stock.
		since    func(t *testing.T, s *store.Store, pads int64)
		conflict string
		want     float64
		qty      float64
		newEntry bool // the entry it ends on isn't the one ticked off
	}{
		{"purchase deleted meanwhile", func(t *testing.T, s *store.Store, pads int64) {
			buys, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MovePurchase})
			if _, _, err := s.DeleteMovement(ctx, buys[0].ID, cur(t, s, store.WhatMovement, buys[0].ID)); err != nil {
				t.Fatal(err)
			}
		}, "", 2, 0, false},
		{"item archived", func(t *testing.T, s *store.Store, pads int64) {
			if _, err := s.ArchiveItem(ctx, pads, cur(t, s, store.WhatItem, pads)); err != nil {
				t.Fatal(err)
			}
		}, "Its item is archived", 0, 2, false},
		{"listed again", func(t *testing.T, s *store.Store, pads int64) {
			if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(1.0)}); err != nil {
				t.Fatal(err)
			}
		}, "", 3, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newStore(t)
			pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
			entry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0)})
			_, trashID, err := s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 2, TotalPrice: ptr(20.0)})
			if err != nil {
				t.Fatal(err)
			}
			tc.since(t, s, pads)
			res, err := s.RestoreTrash(ctx, trashID)
			if tc.conflict != "" {
				wantConflict(t, "undo", err, tc.conflict)
			} else if err != nil {
				t.Fatalf("undo: %v", err)
			} else if (res.ID != entry) != tc.newEntry {
				t.Errorf("undo ended on entry %d (ticked off %d)", res.ID, entry)
			}
			list, _ := s.ListShopping(ctx)
			var got float64
			for _, e := range list {
				if e.ItemID != nil && *e.ItemID == pads && e.Quantity != nil {
					got = *e.Quantity
				}
			}
			if got != tc.want || len(list) > 1 {
				t.Errorf("list = %+v, want one entry for %v", list, tc.want)
			}
			if it := mustGet(t, s, pads); it.Quantity != tc.qty {
				t.Errorf("pads = %v, want %v", it.Quantity, tc.qty)
			}
			assertLedger(t, s)
		})
	}
}

// A vehicle comes back without its tags of items deleted for good since,
// and says how many it dropped.
func TestRestoredVehicleDropsTagsOfPurgedItems(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, VehicleIDs: []int64{v}})
	ring := mustItem(t, s, stock.ItemInput{Name: "Ring", Unit: stock.UnitPiece, VehicleIDs: []int64{v}})
	vTrash, err := s.DeleteVehicle(ctx, v, cur(t, s, store.WhatVehicle, v))
	if err != nil {
		t.Fatal(err)
	}
	ringTrash, err := s.DeleteItem(ctx, ring, cur(t, s, store.WhatItem, ring))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeTrash(ctx, ringTrash); err != nil {
		t.Fatal(err)
	}
	before := cur(t, s, store.WhatItem, pads)
	if res := mustRestore(t, s, vTrash); res.Dropped != 1 {
		t.Errorf("restored = %+v, want one tag dropped", res)
	}
	if it := mustGet(t, s, pads); len(it.VehicleIDs) != 1 || it.VehicleIDs[0] != v || it.Version != before+1 {
		t.Errorf("pads = %+v, want tagged again with a new version", it)
	}
}

// What a restore depends on, deleted without going to the trash (as
// before there was one), is reported, not restored around.
func TestRestoreNeedsWhatWasDeletedForGood(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 4})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MoveUse, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MoveUse})
	_, trashID, err := s.DeleteMovement(ctx, uses[0].ID, cur(t, s, store.WhatMovement, uses[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, pads); err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash(ctx, trashID)
	wantConflict(t, "restore", err, "Its item has been permanently deleted.")
	if n, _ := s.CountTrash(ctx); n != 1 {
		t.Errorf("trash = %d, want the entry kept", n)
	}
}

// Purging an entry restored meanwhile finds it gone: the purge read it,
// then waited for its lock while the restore took it out.
func TestPurgeTrashOfAnEntryRestoredMeanwhile(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	free, _ := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Zip ties"})
	trashID, err := s.DeleteShopping(ctx, free, cur(t, s, store.WhatShopping, free))
	if err != nil {
		t.Fatal(err)
	}
	h := hold(t, pool, `DELETE FROM trash WHERE id = $1`, trashID) // as RestoreTrash does
	var c calls
	var purged error
	c.run(func() { _, purged = s.PurgeTrash(ctx, trashID) })
	waitBlocked(t, pool, 1, []string{h.xid}, "trash")
	h.commit(t)
	c.wait()
	if !errors.Is(purged, store.ErrNotFound) {
		t.Errorf("purge = %v, want ErrNotFound", purged)
	}
	if _, err := s.PurgeTrash(ctx, trashID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("purge again = %v, want ErrNotFound", err)
	}
}

// The trash page shows a deleted photo's thumbnail from its snapshot.
func TestTrashThumb(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	if _, err := s.SetPhoto(ctx, pads, "image/webp", []byte("full image"), []byte{0, 1, 0xfe, 0xff}); err != nil {
		t.Fatal(err)
	}
	trashID, err := s.DeletePhoto(ctx, pads)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.TrashThumb(ctx, trashID)
	if err != nil || p.ContentType != "image/webp" || !bytes.Equal(p.Data, []byte{0, 1, 0xfe, 0xff}) || p.UpdatedAt.IsZero() {
		t.Errorf("thumb = %+v, %v", p, err)
	}

	itemTrash, err := s.DeleteItem(ctx, pads, cur(t, s, store.WhatItem, pads))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{itemTrash, itemTrash + 100} { // not a photo, no entry
		if _, err := s.TrashThumb(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("thumb of %d = %v, want ErrNotFound", id, err)
		}
	}
}
