package store_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
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

func mustRestore(t *testing.T, s *store.Store, trashID int64) store.Restored {
	t.Helper()
	res, err := s.RestoreTrash(context.Background(), trashID)
	if err != nil {
		t.Fatalf("restore %d: %v", trashID, err)
	}
	return res
}

func TestTrashRestoresAService(t *testing.T) {
	s, ctx := newStore(t)
	km := 10000
	vin := stock.VehicleInput{Name: "KTM 390", OdometerKm: &km}
	vin.Normalize()
	v, _ := s.CreateVehicle(ctx, vin)
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 2000})
	kit := stock.KitInput{VehicleID: v, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 1700}}}
	kit.Normalize()
	kitID, _ := s.CreateKit(ctx, kit)
	reading := 12000
	in := stock.ServiceInput{VehicleID: v, KitID: &kitID, Title: "Oil service", PerformedOn: time.Now(), OdometerKm: &reading,
		LaborCost: ptr(80.0), Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 1700}}}
	in.Normalize()
	svc, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	trashID, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, oil).Quantity; got != 2000 {
		t.Fatalf("oil after delete = %v", got)
	}
	entries, _ := s.ListTrash(ctx)
	if len(entries) != 1 || entries[0].Kind != store.TrashService || !strings.Contains(entries[0].Label, "Oil service (KTM 390") {
		t.Errorf("trash = %+v", entries)
	}

	res := mustRestore(t, s, trashID)
	if res.Kind != store.TrashService || res.ID != svc {
		t.Errorf("restored = %+v", res)
	}
	r, err := s.GetService(ctx, svc)
	if err != nil || len(r.Parts) != 1 || r.KitID == nil || *r.KitID != kitID || *r.LaborCost != 80 {
		t.Errorf("restored service = %+v, %v", r, err)
	}
	if got := mustGet(t, s, oil).Quantity; got != 300 {
		t.Errorf("oil after restore = %v, want 300", got)
	}
	if veh, _ := s.GetVehicle(ctx, v); *veh.OdometerKm != 12000 {
		t.Errorf("odometer = %d, want 12000", *veh.OdometerKm)
	}
	if n, _ := s.CountTrash(ctx); n != 0 {
		t.Errorf("trash still has %d", n)
	}
	assertLedger(t, s)

	// Deleted again, but the oil got used meanwhile: the restore is refused
	// and nothing changes.
	trashID, _ = s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MoveUse, Delta: -1000}); err != nil {
		t.Fatal(err)
	}
	var shorts *store.ShortPartsError
	if _, err := s.RestoreTrash(ctx, trashID); !errors.As(err, &shorts) {
		t.Fatalf("restore with too little oil: want ShortPartsError, got %v", err)
	}
	if _, err := s.GetService(ctx, svc); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("service should still be deleted: %v", err)
	}
	if n, _ := s.CountTrash(ctx); n != 1 {
		t.Errorf("trash = %d, want the entry kept", n)
	}
	assertLedger(t, s)
}

func TestTrashRestoresMovementsKitsAndShopping(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 500})

	// A deleted purchase comes back with its stock.
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, TotalPrice: ptr(20.0)}); err != nil {
		t.Fatal(err)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MovePurchase})
	_, trashID, err := s.DeleteMovement(ctx, moves[0].ID, cur(t, s, store.WhatMovement, moves[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if res := mustRestore(t, s, trashID); res.ID != oil {
		t.Errorf("restored = %+v", res)
	}
	if got := mustGet(t, s, oil).Quantity; got != 1500 {
		t.Errorf("oil = %v, want 1500", got)
	}
	if m, err := s.GetMovement(ctx, moves[0].ID); err != nil || *m.TotalPrice != 20 {
		t.Errorf("movement = %+v, %v", m, err)
	}

	// A deleted (unused) kit comes back with its lines.
	kit := stock.KitInput{VehicleID: v, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 1700}}}
	kit.Normalize()
	kitID, _ := s.CreateKit(ctx, kit)
	trashID, err = s.DeleteKit(ctx, kitID, cur(t, s, store.WhatKit, kitID))
	if err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	if k, err := s.GetKit(ctx, kitID); err != nil || len(k.Lines) != 1 {
		t.Errorf("kit = %+v, %v", k, err)
	}

	// Shopping: a removed entry comes back, unless the item is listed again.
	entry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(1000.0), Note: "4 L can"})
	trashID, err = s.DeleteShopping(ctx, entry, cur(t, s, store.WhatShopping, entry))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil}); err != nil {
		t.Fatal(err)
	}
	var conflict *store.ConflictError
	if _, err := s.RestoreTrash(ctx, trashID); !errors.As(err, &conflict) {
		t.Errorf("restoring onto a listed item: want conflict, got %v", err)
	}
	list, _ := s.ListShopping(ctx)
	if _, err := s.DeleteShopping(ctx, list[0].ID, cur(t, s, store.WhatShopping, list[0].ID)); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	if e, err := s.GetShopping(ctx, entry); err != nil || e.Note != "4 L can" {
		t.Errorf("entry = %+v, %v", e, err)
	}
	assertLedger(t, s)
}

func TestTrashUndoesBought(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	entry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(3.0), Note: "sintered"})

	// Bought in part: undo removes the purchase and puts the 3 back.
	left, trashID, err := s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 1, TotalPrice: ptr(30.0)})
	if err != nil || left != 2 {
		t.Fatalf("bought = %v, %v", left, err)
	}
	if res := mustRestore(t, s, trashID); res.Kind != store.TrashBought {
		t.Errorf("restored = %+v", res)
	}
	if e, _ := s.GetShopping(ctx, entry); *e.Quantity != 3 {
		t.Errorf("entry = %+v", e)
	}
	if it := mustGet(t, s, pads); it.Quantity != 0 {
		t.Errorf("pads = %v", it.Quantity)
	}
	if n, _ := s.CountPurchases(ctx, pads); n != 0 {
		t.Errorf("purchases = %d", n)
	}

	// Bought in full: the entry comes back. Used since: refused.
	_, trashID, _ = s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 3})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MoveUse, Delta: -2}); err != nil {
		t.Fatal(err)
	}
	var short *store.InsufficientStockError
	if _, err := s.RestoreTrash(ctx, trashID); !errors.As(err, &short) {
		t.Errorf("undo after using it: want InsufficientStockError, got %v", err)
	}
	uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MoveUse})
	if _, _, err := s.DeleteMovement(ctx, uses[0].ID, cur(t, s, store.WhatMovement, uses[0].ID)); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	if e, err := s.GetShopping(ctx, entry); err != nil || *e.Quantity != 3 || e.Note != "sintered" {
		t.Errorf("entry = %+v, %v", e, err)
	}
	assertLedger(t, s)
}

func TestTrashRestoresItemsVehiclesAndPhotos(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	id := mustItem(t, s, stock.ItemInput{Name: "Bolt", Unit: stock.UnitPiece, Quantity: 5, VehicleIDs: []int64{v},
		SKUs: []stock.SKU{{Code: "M8", Kind: stock.SKUOther, Label: "M8x20"}}})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPhoto(ctx, id, "image/jpeg", []byte{0xff, 0xd8, 1, 2}, []byte{0xff, 0xd8}); err != nil {
		t.Fatal(err)
	}

	// The photo alone.
	trashID, err := s.DeletePhoto(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	if ph, err := s.GetPhoto(ctx, id, false); err != nil || len(ph.Data) != 4 || ph.Data[2] != 1 {
		t.Errorf("photo = %+v, %v", ph, err)
	}

	// The whole item: fields, SKUs, tags, photo and history.
	trashID, err = s.DeleteItem(ctx, id, cur(t, s, store.WhatItem, id))
	if err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	it := mustGet(t, s, id)
	if it.Quantity != 3 || len(it.SKUs) != 1 || it.SKUs[0].Label != "M8x20" || len(it.VehicleIDs) != 1 || !it.HasPhoto {
		t.Errorf("item = %+v", it)
	}
	if moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: id}); len(moves) != 2 {
		t.Errorf("history = %+v", moves)
	}
	assertLedger(t, s)

	// A vehicle: its tags come back; a vehicle with its name meanwhile blocks it.
	trashID, err = s.DeleteVehicle(ctx, v, cur(t, s, store.WhatVehicle, v))
	if err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); len(it.VehicleIDs) != 0 {
		t.Fatalf("tags after deleting the vehicle = %v", it.VehicleIDs)
	}
	other := mustVehicle(t, s, "KTM 390")
	var conflict *store.ConflictError
	if _, err := s.RestoreTrash(ctx, trashID); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "KTM 390") {
		t.Errorf("name taken: want conflict, got %v", err)
	}
	if _, err := s.DeleteVehicle(ctx, other, cur(t, s, store.WhatVehicle, other)); err != nil {
		t.Fatal(err)
	}
	if res := mustRestore(t, s, trashID); res.ID != v {
		t.Errorf("restored = %+v", res)
	}
	if it := mustGet(t, s, id); len(it.VehicleIDs) != 1 || it.VehicleIDs[0] != v {
		t.Errorf("tags = %v", it.VehicleIDs)
	}
}

func TestTrashPurgeAndEmpty(t *testing.T) {
	s, ctx := newStore(t)
	for _, name := range []string{"A", "B", "C"} {
		id := mustItem(t, s, stock.ItemInput{Name: name, Unit: stock.UnitPiece})
		if _, err := s.DeleteItem(ctx, id, cur(t, s, store.WhatItem, id)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := s.ListTrash(ctx)
	if len(entries) != 3 || entries[0].Label != "Item: C" {
		t.Fatalf("trash = %+v", entries)
	}
	if _, err := s.PurgeTrash(ctx, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, entries[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("restore purged: want ErrNotFound, got %v", err)
	}
	if err := s.EmptyTrash(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountTrash(ctx); n != 0 {
		t.Errorf("trash = %d", n)
	}
}

func TestTrashRestoreOrderLosesNothing(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	v := mustVehicle(t, s, "KTM 390")
	other := mustVehicle(t, s, "KTM 1290")
	ring := mustItem(t, s, stock.ItemInput{Name: "O-ring", Unit: stock.UnitPiece, Quantity: 2, VehicleIDs: []int64{v}})
	var conflict *store.ConflictError

	// A kit, then the item on it: restoring the kit first is held up until
	// the item is back, so its line isn't dropped.
	kit := stock.KitInput{VehicleID: other, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: ring, Quantity: 1}}}
	kit.Normalize()
	kitID, _ := s.CreateKit(ctx, kit)
	kitTrash, err := s.DeleteKit(ctx, kitID, cur(t, s, store.WhatKit, kitID))
	if err != nil {
		t.Fatal(err)
	}
	itemTrash, err := s.DeleteItem(ctx, ring, cur(t, s, store.WhatItem, ring))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, kitTrash); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "Restore Item: O-ring first") {
		t.Fatalf("kit before its item: want conflict naming the item, got %v", err)
	}
	mustRestore(t, s, itemTrash)
	mustRestore(t, s, kitTrash)
	if k, _ := s.GetKit(ctx, kitID); len(k.Lines) != 1 {
		t.Errorf("kit lines = %+v", k.Lines)
	}

	// A vehicle, then an item tagged with it: both orders keep the tag.
	vehicleTrash, err := s.DeleteVehicle(ctx, v, cur(t, s, store.WhatVehicle, v))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteKit(ctx, kitID, cur(t, s, store.WhatKit, kitID)); err != nil { // so the O-ring can be deleted
		t.Fatal(err)
	}
	itemTrash, err = s.DeleteItem(ctx, ring, cur(t, s, store.WhatItem, ring))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, vehicleTrash); !errors.As(err, &conflict) {
		t.Fatalf("vehicle before its tagged item: want conflict, got %v", err)
	}
	mustRestore(t, s, itemTrash)
	mustRestore(t, s, vehicleTrash)
	if it := mustGet(t, s, ring); len(it.VehicleIDs) != 1 || it.VehicleIDs[0] != v {
		t.Errorf("tags = %v", it.VehicleIDs)
	}

	// A service, then its kit: the service waits for its kit.
	kit.VehicleID, kit.Lines = v, nil
	kitID, _ = s.CreateKit(ctx, kit)
	in := stock.ServiceInput{VehicleID: v, KitID: &kitID, Title: "Oil service", PerformedOn: time.Now(),
		Parts: []stock.KitLineInput{{ItemID: ring, Quantity: 1}}}
	in.Normalize()
	svc, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	svcTrash, _ := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	kitTrash, err = s.DeleteKit(ctx, kitID, cur(t, s, store.WhatKit, kitID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, svcTrash); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "Kit: Oil service") {
		t.Fatalf("service before its kit: want conflict, got %v", err)
	}
	mustRestore(t, s, kitTrash)
	mustRestore(t, s, svcTrash)
	if r, _ := s.GetService(ctx, svc); r.KitID == nil || *r.KitID != kitID {
		t.Errorf("service kit = %v", r.KitID)
	}

	// A service whose part item is in the trash says which one.
	svcTrash, _ = s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if _, err := s.DeleteItem(ctx, ring, cur(t, s, store.WhatItem, ring)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, svcTrash); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "O-ring") {
		t.Errorf("service with a trashed part: want conflict naming it, got %v", err)
	}
	assertLedger(t, s)
}

func TestTrashUndoesTwoPartialBoughts(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	entry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(3.0)})
	_, first, _ := s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 1})
	_, second, _ := s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 2})
	// Oldest first: the list asks for 1, then for all 3 again.
	mustRestore(t, s, first)
	if e := mustShoppingEntry(t, s); *e.Quantity != 1 {
		t.Errorf("after undoing the first = %v, want 1", *e.Quantity)
	}
	mustRestore(t, s, second)
	if e := mustShoppingEntry(t, s); *e.Quantity != 3 {
		t.Errorf("after undoing both = %v, want 3", *e.Quantity)
	}
	if it := mustGet(t, s, pads); it.Quantity != 0 {
		t.Errorf("pads = %v", it.Quantity)
	}
	assertLedger(t, s)
}

func TestTrashSnapshotsSurviveNewColumns(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	id := mustItem(t, s, stock.ItemInput{Name: "Bolt", Unit: stock.UnitPiece, Quantity: 1})
	trashID, err := s.DeleteItem(ctx, id, cur(t, s, store.WhatItem, id))
	if err != nil {
		t.Fatal(err)
	}
	// A snapshot taken before a column existed lacks its key.
	if _, err := pool.Exec(ctx, `UPDATE trash SET payload = jsonb_set(payload, '{items,0}', (payload #> '{items,0}') - 'archived_at' - 'updated_at')`); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
	if it := mustGet(t, s, id); it.Quantity != 1 || it.UpdatedAt.IsZero() {
		t.Errorf("item = %+v", it)
	}
}

// A deleted service comes back only if its reading still fits between
// the services logged since.
func TestRestoredServiceKeepsTheOdometerInOrder(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	day := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }
	logAt := func(on time.Time, km int) int64 {
		t.Helper()
		in := stock.ServiceInput{VehicleID: v, Title: "Check", PerformedOn: on, OdometerKm: &km}
		in.Normalize()
		id, err := s.CreateService(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := logAt(day(10), 5000)
	trashID, err := s.DeleteService(ctx, first, cur(t, s, store.WhatService, first))
	if err != nil {
		t.Fatal(err)
	}
	later := logAt(day(12), 4000)
	_, err = s.RestoreTrash(ctx, trashID)
	wantConflict(t, "restore", err, "logged on 12 Oct 2026, after this service")

	if _, err := s.DeleteService(ctx, later, cur(t, s, store.WhatService, later)); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, trashID)
}

// What is in the trash comes back in the unit it was recorded in, so it
// locks the unit like live records do.
func TestUnitLockedByTheTrash(t *testing.T) {
	s, ctx := newStore(t)
	in := stock.ItemInput{Name: "Oil", Unit: stock.UnitPiece, Quantity: 4}
	id := mustItem(t, s, in)
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: id})
	_, trashID, err := s.DeleteMovement(ctx, moves[0].ID, cur(t, s, store.WhatMovement, moves[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	in.Unit, in.Quantity = stock.UnitML, 0
	wantConflict(t, "with a deleted stock change", s.UpdateItem(ctx, id, itemV(t, s, id, in), time.Now()), "including in the trash")
	if _, err := s.PurgeTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}

	// A list entry with an amount locks it; one without doesn't.
	for _, qty := range []*float64{ptr(2.0), nil} {
		if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &id, Quantity: qty}); err != nil {
			t.Fatal(err)
		}
		list, _ := s.ListShopping(ctx)
		trashID, err = s.DeleteShopping(ctx, list[0].ID, cur(t, s, store.WhatShopping, list[0].ID))
		if err != nil {
			t.Fatal(err)
		}
		err := s.UpdateItem(ctx, id, itemV(t, s, id, in), time.Now())
		if qty != nil {
			wantConflict(t, "with a deleted list entry", err, "including in the trash")
			if _, err := s.PurgeTrash(ctx, trashID); err != nil {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Errorf("with a deleted entry without an amount: %v", err)
		}
	}
	if it := mustGet(t, s, id); it.Unit != stock.UnitML {
		t.Errorf("unit = %s", it.Unit)
	}
}

// Purging an item or vehicle for good also purges what could only be
// restored with it; restores say what links they had to leave out.
func TestTrashPurgeTakesDependentsAlong(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	kin := stock.KitInput{VehicleID: v, Name: "Chain"}
	kin.Normalize()
	kit, _ := s.CreateKit(ctx, kin)
	kitTrash, err := s.DeleteKit(ctx, kit, cur(t, s, store.WhatKit, kit))
	if err != nil {
		t.Fatal(err)
	}
	vehicleTrash, err := s.DeleteVehicle(ctx, v, cur(t, s, store.WhatVehicle, v))
	if err != nil {
		t.Fatal(err)
	}
	other := mustVehicle(t, s, "Duke")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitPiece, Quantity: 3})
	keep := mustItem(t, s, stock.ItemInput{Name: "Bolt", Unit: stock.UnitPiece})
	okin := stock.KitInput{VehicleID: other, Name: "Oil", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 1}, {ItemID: keep, Quantity: 1}}}
	okin.Normalize()
	oilKit, _ := s.CreateKit(ctx, okin)
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: oil})
	_, moveTrash, err := s.DeleteMovement(ctx, moves[0].ID, cur(t, s, store.WhatMovement, moves[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	oilKitTrash, err := s.DeleteKit(ctx, oilKit, cur(t, s, store.WhatKit, oilKit))
	if err != nil {
		t.Fatal(err)
	}
	itemTrash, err := s.DeleteItem(ctx, oil, cur(t, s, store.WhatItem, oil))
	if err != nil {
		t.Fatal(err)
	}

	if n, err := s.PurgeTrash(ctx, vehicleTrash); err != nil || n != 1 {
		t.Errorf("purge vehicle = %d, %v; want its kit too", n, err)
	}
	if n, err := s.PurgeTrash(ctx, itemTrash); err != nil || n != 1 {
		t.Errorf("purge item = %d, %v; want its stock change too", n, err)
	}
	for _, id := range []int64{kitTrash, moveTrash} {
		if _, err := s.RestoreTrash(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("entry %d should be gone with what it needed: %v", id, err)
		}
	}
	// The other kit only listed the oil: it comes back without that line.
	res := mustRestore(t, s, oilKitTrash)
	if res.Dropped != 1 {
		t.Errorf("dropped = %d, want 1", res.Dropped)
	}
	if k, _ := s.GetKit(ctx, oilKit); len(k.Lines) != 1 || k.Lines[0].ItemID != keep {
		t.Errorf("kit lines = %+v", k.Lines)
	}
}

// A snapshot holding an amount finer than 0.01 (only possible by hand
// since migration 00012) is not restored: the column would round it while
// the stock moved by the exact amount, and the ledger would stop adding up.
func TestRestoreRefusesAmountsFinerThanTwoDecimals(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1000})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MoveUse, Delta: -250.5}); err != nil {
		t.Fatal(err)
	}
	moves, err := s.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveUse})
	if err != nil || len(moves) != 1 {
		t.Fatalf("uses: %v, %v", moves, err)
	}
	_, trashID, err := s.DeleteMovement(ctx, moves[0].ID, cur(t, s, store.WhatMovement, moves[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	setDelta := func(d string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE trash SET payload = jsonb_set(payload, '{stock_movements,0,delta}', $2::jsonb) WHERE id = $1`,
			trashID, d); err != nil {
			t.Fatal(err)
		}
	}
	setDelta("-250.505")
	var conflict *store.ConflictError
	if _, err := s.RestoreTrash(ctx, trashID); !errors.As(err, &conflict) || !strings.Contains(err.Error(), "finer than 0.01 (-250.505)") {
		t.Fatalf("restore of -250.505: want a ConflictError, got %v", err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 1000 {
		t.Errorf("oil after the refused restore = %v, want 1000", it.Quantity)
	}
	assertLedger(t, s)
	// Two decimals, written with a third zero as numeric(12,3) did: fine.
	setDelta("-250.500")
	mustRestore(t, s, trashID)
	if it := mustGet(t, s, oil); it.Quantity != 749.5 {
		t.Errorf("oil after the restore = %v, want 749.5", it.Quantity)
	}
	assertLedger(t, s)
}
