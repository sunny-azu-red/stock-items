package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

// These tests line two store calls up in the order that once deadlocked
// or let wrong data in: a third transaction holds a row both need, the
// calls queue up behind it (checked in pg_locks), and then it lets go.

func newStorePool(t *testing.T) (*store.Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := testdb.New(t)
	return store.New(pool), pool, context.Background()
}

// holder is a transaction holding row locks, for lining others up.
type holder struct {
	tx  pgx.Tx
	xid string
}

// hold begins a transaction that runs sql (a SELECT … FOR UPDATE, or a
// write) and keeps its locks until released.
func hold(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) *holder {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
	h := &holder{tx: tx}
	if err := tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text`).Scan(&h.xid); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *holder) release(t *testing.T) {
	t.Helper()
	if err := h.tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (h *holder) commit(t *testing.T) {
	t.Helper()
	if err := h.tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// blocked counts the sessions waiting for a transaction in xids or for a
// row of one of tables, and returns their own transaction ids.
func blocked(t *testing.T, pool *pgxpool.Pool, xids []string, tables []string) (int, []string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT DISTINCT w.pid, COALESCE(o.transactionid::text, '') FROM pg_locks w
		LEFT JOIN pg_locks o ON o.pid = w.pid AND o.locktype = 'transactionid' AND o.granted
		WHERE NOT w.granted
		  AND (w.locktype = 'transactionid' AND w.transactionid::text = ANY($1)
		       OR w.locktype = 'tuple' AND w.relation IN (SELECT unnest($2::text[])::regclass))`, xids, tables)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pids := map[int]bool{} // a session in a savepoint has two transaction ids
	var own []string
	for rows.Next() {
		var pid int
		var xid string
		if err := rows.Scan(&pid, &xid); err != nil {
			t.Fatal(err)
		}
		pids[pid] = true
		if xid != "" {
			own = append(own, xid)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return len(pids), own
}

// waitBlocked waits until n sessions wait for the holder (or a session
// waiting for it) or for a row of tables, and returns the transaction ids
// to wait for next: the holder's and theirs.
func waitBlocked(t *testing.T, pool *pgxpool.Pool, n int, xids []string, tables ...string) []string {
	t.Helper()
	for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(5 * time.Millisecond) {
		got, own := blocked(t, pool, xids, tables)
		if got >= n {
			return append(xids, own...)
		}
	}
	t.Fatalf("fewer than %d sessions wait for %v or rows of %v", n, xids, tables)
	return nil
}

// blocks reports whether a call that done closes for waits for xids or a
// row of tables (true), or finishes without waiting (false).
func blocks(t *testing.T, pool *pgxpool.Pool, done <-chan struct{}, n int, xids []string, tables ...string) bool {
	t.Helper()
	for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(5 * time.Millisecond) {
		select {
		case <-done:
			return false
		default:
		}
		if got, _ := blocked(t, pool, xids, tables); got >= n {
			return true
		}
	}
	t.Fatal("the call neither waited nor finished")
	return false
}

// run starts fn in a goroutine; wait waits for every fn started.
type calls struct{ wg sync.WaitGroup }

func (c *calls) run(fn func()) <-chan struct{} {
	done := make(chan struct{})
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer close(done)
		fn()
	}()
	return done
}

func (c *calls) wait() { c.wg.Wait() }

// noDeadlock fails on a deadlock or another database error.
func noDeadlock(t *testing.T, what string, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		t.Errorf("%s: %s %s", what, pgErr.Code, pgErr.Message)
	}
}

// Deleting an item while one of its stock changes is deleted: the stock
// change locks its item before itself, as the item's delete does before
// cascading to it, so they take turns. The stock change is then gone with
// the item, in the trash, and Undo brings both back.
func TestDeletingAnItemAndItsStockChangeTakeTurns(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 4})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MoveUse, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MoveUse})
	use := uses[0].ID
	itemV, useV := cur(t, s, store.WhatItem, pads), cur(t, s, store.WhatMovement, use)

	h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR NO KEY UPDATE`, pads)
	var c calls
	var trashID int64
	var delItem, delUse error
	c.run(func() { trashID, delItem = s.DeleteItem(ctx, pads, itemV) })
	xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
	c.run(func() { _, _, delUse = s.DeleteMovement(ctx, use, useV) })
	waitBlocked(t, pool, 2, xids, "items", "stock_movements")
	h.release(t)
	c.wait()

	noDeadlock(t, "delete item", delItem)
	noDeadlock(t, "delete stock change", delUse)
	if delItem != nil {
		t.Fatalf("delete item: %v", delItem)
	}
	var stale *store.StaleError
	if !errors.As(delUse, &stale) || !stale.Gone || !stale.InTrash {
		t.Errorf("delete stock change = %v, want gone with its item, in the trash", delUse)
	}
	mustRestore(t, s, trashID)
	if it := mustGet(t, s, pads); it.Quantity != 3 {
		t.Errorf("pads = %v after Undo, want 3", it.Quantity)
	}
	assertLedger(t, s)
}

// Undoing a Bought while its purchase is edited or deleted: both lock the
// item before the purchase, so they take turns. Undo wins here (it was
// first in line), and the edit finds the purchase gone.
func TestUndoingBoughtAndEditingThePurchaseTakeTurns(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(s *store.Store, id int64, version int) error
	}{
		{"edit", func(s *store.Store, id int64, version int) error {
			return s.UpdateMovement(context.Background(), id, stock.MovementEdit{Amount: 3, OccurredOn: ptr(time.Now()), Version: version})
		}},
		{"delete", func(s *store.Store, id int64, version int) error {
			_, _, err := s.DeleteMovement(context.Background(), id, version)
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, pool, ctx := newStorePool(t)
			pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
			entry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0)})
			_, trashID, err := s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 2, TotalPrice: ptr(30.0)})
			if err != nil {
				t.Fatal(err)
			}
			buys, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MovePurchase})
			buy := buys[0].ID
			buyV := cur(t, s, store.WhatMovement, buy)

			h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR NO KEY UPDATE`, pads)
			var c calls
			var undo, edit error
			c.run(func() { _, undo = s.RestoreTrash(ctx, trashID) })
			xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
			c.run(func() { edit = tt.edit(s, buy, buyV) })
			waitBlocked(t, pool, 2, xids, "items", "stock_movements")
			h.release(t)
			c.wait()

			noDeadlock(t, "undo", undo)
			noDeadlock(t, tt.name, edit)
			if undo != nil {
				t.Fatalf("undo: %v", undo)
			}
			if !errors.Is(edit, store.ErrNotFound) {
				t.Errorf("%s = %v, want the purchase gone", tt.name, edit)
			}
			if it := mustGet(t, s, pads); it.Quantity != 0 {
				t.Errorf("pads = %v, want 0", it.Quantity)
			}
			if e, err := s.GetShopping(ctx, entry); err != nil || *e.Quantity != 2 {
				t.Errorf("entry = %+v, %v; want it back asking for 2", e, err)
			}
			assertLedger(t, s)
		})
	}
}

// Archiving a kit while a service is being logged with it waits for the
// service: the service holds the kit (FOR SHARE) from the moment it
// checked that the kit isn't archived.
func TestArchivingAKitWaitsForAServiceLoggedWithIt(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	v := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1000})
	kin := stock.KitInput{VehicleID: v, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 100}}}
	kin.Normalize()
	kit, err := s.CreateKit(ctx, kin)
	if err != nil {
		t.Fatal(err)
	}
	kitV := cur(t, s, store.WhatKit, kit)

	// The service checks the kit, then waits for its part.
	h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR NO KEY UPDATE`, oil)
	var c calls
	var logged, archived error
	c.run(func() {
		_, logged = s.CreateService(ctx, stock.ServiceInput{VehicleID: v, KitID: &kit, Title: "Oil service", PerformedOn: time.Now(),
			Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 100}}})
	})
	xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
	done := c.run(func() { archived = s.ArchiveKit(ctx, kit, kitV) })
	if !blocks(t, pool, done, 2, xids, "service_kits") {
		t.Error("the kit was archived while a service was logged with it")
	}
	h.release(t)
	c.wait()
	noDeadlock(t, "log service", logged)
	noDeadlock(t, "archive kit", archived)
	if logged != nil || archived != nil {
		t.Errorf("log = %v, archive = %v; want both, one after the other", logged, archived)
	}
}

// Moving a kit to another vehicle while a service is logged for that
// vehicle with the kit: the kit's save locks both vehicles before the kit,
// as a service locks its vehicle before the kit, so they take turns.
func TestMovingAKitAndLoggingAServiceWithItTakeTurns(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	from, to := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
	kin := stock.KitInput{VehicleID: from, Name: "Chain"}
	kin.Normalize()
	kit, err := s.CreateKit(ctx, kin)
	if err != nil {
		t.Fatal(err)
	}
	move := stock.KitInput{VehicleID: to, Name: "Chain", Version: cur(t, s, store.WhatKit, kit)}
	move.Normalize()

	h := hold(t, pool, `SELECT 1 FROM vehicles WHERE id = $1 FOR NO KEY UPDATE`, to)
	var c calls
	var logged, moved error
	c.run(func() {
		_, logged = s.CreateService(ctx, stock.ServiceInput{VehicleID: to, KitID: &kit, Title: "Chain", PerformedOn: time.Now()})
	})
	xids := waitBlocked(t, pool, 1, []string{h.xid}, "vehicles")
	c.run(func() { moved = s.UpdateKit(ctx, kit, move) })
	waitBlocked(t, pool, 2, xids, "vehicles", "service_kits")
	h.release(t)
	c.wait()
	noDeadlock(t, "log service", logged)
	noDeadlock(t, "move kit", moved)
	if moved != nil {
		t.Errorf("move kit: %v", moved)
	}
	// The service came first, when the kit still belonged to the other one.
	var conflict *store.ConflictError
	if !errors.As(logged, &conflict) {
		t.Errorf("log service = %v, want the kit refused as another vehicle's", logged)
	}
}

// Changing an item's unit while a kit line for it is saved: the kit holds
// its line items (FOR SHARE) and checks the unit its amounts were read in,
// so a 1000 ml line can't turn into 1000 g, whichever comes first.
func TestUnitChangesAndKitLinesTakeTurns(t *testing.T) {
	t.Run("kit first", func(t *testing.T) {
		s, pool, ctx := newStorePool(t)
		v := mustVehicle(t, s, "KTM 390")
		oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
		ring := mustItem(t, s, stock.ItemInput{Name: "Ring", Unit: stock.UnitPiece})
		in := stock.KitInput{VehicleID: v, Name: "Oil service", Lines: []stock.KitLineInput{
			{ItemID: oil, Quantity: 1000, Unit: stock.UnitML}, {ItemID: ring, Quantity: 1, Unit: stock.UnitPiece}}}
		in.Normalize()
		grams := itemV(t, s, oil, stock.ItemInput{Name: "Oil", Unit: stock.UnitGram})
		grams.Normalize()

		// The kit holds the oil and waits for the ring.
		h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR UPDATE`, ring)
		var c calls
		var saved, changed error
		c.run(func() { _, saved = s.CreateKit(ctx, in) })
		xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
		done := c.run(func() { changed = s.UpdateItem(ctx, oil, grams, time.Now()) })
		if !blocks(t, pool, done, 2, xids, "items") {
			t.Error("the unit changed while a kit line for the item was being saved")
		}
		h.release(t)
		c.wait()
		noDeadlock(t, "save kit", saved)
		noDeadlock(t, "change unit", changed)
		if saved != nil {
			t.Fatalf("save kit: %v", saved)
		}
		wantConflict(t, "unit change after the kit", changed, "unit cannot be changed")
		if it := mustGet(t, s, oil); it.Unit != stock.UnitML {
			t.Errorf("oil unit = %s, want ml", it.Unit)
		}
	})

	t.Run("unit first", func(t *testing.T) {
		s, pool, ctx := newStorePool(t)
		v, w := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
		oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
		grams := itemV(t, s, oil, stock.ItemInput{Name: "Oil", Unit: stock.UnitGram, VehicleIDs: []int64{v}})
		grams.Normalize()
		in := stock.KitInput{VehicleID: w, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 1000, Unit: stock.UnitML}}}
		in.Normalize()

		// The unit change holds the oil and waits to tag it.
		h := hold(t, pool, `SELECT 1 FROM vehicles WHERE id = $1 FOR UPDATE`, v)
		var c calls
		var saved, changed error
		c.run(func() { changed = s.UpdateItem(ctx, oil, grams, time.Now()) })
		xids := waitBlocked(t, pool, 1, []string{h.xid}, "vehicles")
		done := c.run(func() { _, saved = s.CreateKit(ctx, in) })
		if !blocks(t, pool, done, 2, xids, "items") {
			t.Error("a kit line was saved while the item's unit changed")
		}
		h.release(t)
		c.wait()
		noDeadlock(t, "save kit", saved)
		noDeadlock(t, "change unit", changed)
		if changed != nil {
			t.Fatalf("change unit: %v", changed)
		}
		wantConflict(t, "kit after the unit change", saved, "unit")
		if kits, _ := s.ListKits(ctx, store.KitFilter{}); len(kits) != 0 {
			t.Errorf("kits = %+v, want none: its 1000 ml would read 1000 g", kits)
		}
	})
}

// Linking a free-text entry to an item that is put on the list meanwhile
// merges the two, rather than tripping over the one-entry-per-item index.
func TestLinkingAnEntryWhileItsItemIsListed(t *testing.T) {
	for _, tt := range []struct {
		name string
		add  func(s *store.Store, item int64) error
	}{
		{"add", func(s *store.Store, item int64) error {
			_, err := s.AddShopping(context.Background(), stock.ShoppingInput{ItemID: &item, Quantity: ptr(1.0)})
			return err
		}},
		{"ensure", func(s *store.Store, item int64) error {
			_, err := s.EnsureShopping(context.Background(), item, 1, "low")
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, pool, ctx := newStorePool(t)
			pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
			free, _ := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "brake pads", Quantity: ptr(2.0)})
			freeV := cur(t, s, store.WhatShopping, free)

			h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR UPDATE`, pads)
			var c calls
			var added, linked error
			c.run(func() { added = tt.add(s, pads) })
			xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
			c.run(func() { _, _, linked = s.LinkShopping(ctx, free, freeV, pads) })
			waitBlocked(t, pool, 2, xids, "items")
			h.release(t)
			c.wait()
			noDeadlock(t, tt.name, added)
			noDeadlock(t, "link", linked)
			if added != nil || linked != nil {
				t.Fatalf("%s = %v, link = %v", tt.name, added, linked)
			}
			list, _ := s.ListShopping(ctx)
			if len(list) != 1 || list[0].ItemID == nil || *list[0].ItemID != pads {
				t.Errorf("list = %+v, want one entry for the pads", list)
			}
		})
	}
}

// Emptying the trash while one entry is deleted for good, with an entry
// that goes along with it: both lock their trash rows in id order.
func TestEmptyingAndPurgingTheTrashTakeTurns(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 4})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MoveUse, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MoveUse})
	_, useTrash, err := s.DeleteMovement(ctx, uses[0].ID, cur(t, s, store.WhatMovement, uses[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	itemTrash, err := s.DeleteItem(ctx, pads, cur(t, s, store.WhatItem, pads))
	if err != nil {
		t.Fatal(err)
	}

	// Emptying waits at the stock change's entry, which purging the item
	// takes along.
	h := hold(t, pool, `SELECT 1 FROM trash WHERE id = $1 FOR UPDATE`, useTrash)
	var c calls
	var emptied, purged error
	c.run(func() { emptied = s.EmptyTrash(ctx) })
	xids := waitBlocked(t, pool, 1, []string{h.xid}, "trash")
	c.run(func() { _, purged = s.PurgeTrash(ctx, itemTrash) })
	waitBlocked(t, pool, 2, xids, "trash")
	h.release(t)
	c.wait()
	noDeadlock(t, "empty", emptied)
	noDeadlock(t, "purge", purged)
	if emptied != nil {
		t.Errorf("empty: %v", emptied)
	}
	if purged != nil && !errors.Is(purged, store.ErrNotFound) {
		t.Errorf("purge: %v", purged)
	}
	if n, _ := s.CountTrash(ctx); n != 0 {
		t.Errorf("trash = %d, want empty", n)
	}
}

// Saving an item tagged with a vehicle deleted meanwhile says so instead
// of failing on the foreign key.
func TestTaggingAVehicleDeletedMeanwhile(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	v := mustVehicle(t, s, "KTM 390")
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	in := itemV(t, s, pads, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, VehicleIDs: []int64{v}})
	in.Normalize()

	h := hold(t, pool, `DELETE FROM vehicles WHERE id = $1`, v)
	var c calls
	var saved error
	c.run(func() { saved = s.UpdateItem(ctx, pads, in, time.Now()) })
	waitBlocked(t, pool, 1, []string{h.xid}, "vehicles")
	h.commit(t)
	c.wait()
	noDeadlock(t, "save", saved)
	wantConflict(t, "tag a deleted vehicle", saved, "deleted meanwhile")
}

// Archiving an item while its shopping list entry, put on the list just
// after the archive looked for one, is ticked off: ticking off locks the
// entry, then the item; archiving must not lock the item, then that
// entry. Here the Bought goes first and the archive then trashes what is
// left of the entry, which Undo brings back once the item is restored.
func TestArchivingAnItemAndTickingOffItsEntryTakeTurns(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	padsV := cur(t, s, store.WhatItem, pads)

	// A share lock on the item: the archive waits for it, while putting
	// the item on the list (also FOR SHARE) goes ahead.
	h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR SHARE`, pads)
	var c calls
	var archived error
	var listTrash int64
	c.run(func() { listTrash, archived = s.ArchiveItem(ctx, pads, padsV) })
	xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
	entry, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(3.0)})
	if err != nil {
		t.Fatal(err)
	}
	var bought error
	c.run(func() {
		_, _, bought = s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 1, TotalPrice: ptr(10.0)})
	})
	waitBlocked(t, pool, 2, xids, "items")
	h.release(t)
	c.wait()
	noDeadlock(t, "archive", archived)
	noDeadlock(t, "bought", bought)
	if archived != nil || bought != nil {
		t.Fatalf("archive = %v, bought = %v", archived, bought)
	}
	if it := mustGet(t, s, pads); !it.Archived() || it.Quantity != 1 {
		t.Errorf("pads = %+v, want archived with the 1 bought", it)
	}
	if list, _ := s.ListShopping(ctx); len(list) != 0 || listTrash == 0 {
		t.Errorf("list = %+v, list trash = %d; want the rest of the entry in the trash", list, listTrash)
	}
	if err := s.RestoreItem(ctx, pads, cur(t, s, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, s, listTrash)
	if list, _ := s.ListShopping(ctx); len(list) != 1 || *list[0].Quantity != 2 {
		t.Errorf("list after Undo = %+v, want the 2 still to buy", list)
	}
	assertLedger(t, s)
}

// Restoring something of an item while the item is deleted: the restore
// finds the item, then waits for its lock while the delete commits. It
// then says the item must be restored first, rather than fail as if the
// trash entry were gone (or with a bare "no rows").
func TestRestoringWhileTheItemIsDeleted(t *testing.T) {
	for _, tt := range []struct {
		name  string
		trash func(t *testing.T, s *store.Store, item int64) int64
	}{
		{"stock change", func(t *testing.T, s *store.Store, item int64) int64 {
			ctx := context.Background()
			if err := s.AddMovement(ctx, stock.MovementInput{ItemID: item, Kind: stock.MoveUse, Delta: -1}); err != nil {
				t.Fatal(err)
			}
			uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: item, Kind: stock.MoveUse})
			_, trashID, err := s.DeleteMovement(ctx, uses[0].ID, cur(t, s, store.WhatMovement, uses[0].ID))
			if err != nil {
				t.Fatal(err)
			}
			return trashID
		}},
		{"shopping entry", func(t *testing.T, s *store.Store, item int64) int64 {
			ctx := context.Background()
			entry, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &item, Quantity: ptr(2.0)})
			if err != nil {
				t.Fatal(err)
			}
			trashID, err := s.DeleteShopping(ctx, entry, cur(t, s, store.WhatShopping, entry))
			if err != nil {
				t.Fatal(err)
			}
			return trashID
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, pool, ctx := newStorePool(t)
			pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 4})
			trashID := tt.trash(t, s, pads)

			// The item's delete, as DeleteItem makes it: in the trash.
			h := hold(t, pool, `DELETE FROM items WHERE id = $1`, pads)
			if _, err := h.tx.Exec(ctx, `INSERT INTO trash (kind, label, payload)
				VALUES ('item', 'Item: Pads', jsonb_build_object('items', jsonb_build_array(jsonb_build_object('id', $1::bigint))))`, pads); err != nil {
				t.Fatal(err)
			}
			var c calls
			var restored error
			c.run(func() { _, restored = s.RestoreTrash(ctx, trashID) })
			waitBlocked(t, pool, 1, []string{h.xid}, "items")
			h.commit(t)
			c.wait()
			noDeadlock(t, "restore", restored)
			wantConflict(t, "restore", restored, "Restore Item: Pads first")
			if errors.Is(restored, store.ErrNotFound) {
				t.Error("the trash entry looks gone")
			}
		})
	}
}

// Restoring a service or kit while what it belongs to is deleted: the
// restore locks the vehicle, kit and items before looking for them, so it
// sees them gone once the delete commits. A vehicle in the trash must be
// restored first; a kit or item deleted for good is dropped. Without the
// locks, the look passed and the insert failed on the foreign key.
func TestRestoringWhileWhatItBelongsToIsDeleted(t *testing.T) {
	trashed := `INSERT INTO trash (kind, label, payload)
		VALUES ('vehicle', 'Vehicle: KTM 390', jsonb_build_object('vehicles', jsonb_build_array(jsonb_build_object('id', $1::bigint))))`
	for _, tt := range []struct {
		name    string
		restore string // TrashService or TrashKit
		// gone deletes what the restore needs in the holder's transaction.
		gone     func(t *testing.T, h *holder, vehicle, kit, oil int64)
		conflict string // the restore's error ("" for none)
		dropped  int
	}{
		{"service, vehicle into the trash", store.TrashService, func(t *testing.T, h *holder, vehicle, kit, oil int64) {
			holderExec(t, h, `DELETE FROM vehicles WHERE id = $1`, vehicle)
			holderExec(t, h, trashed, vehicle)
		}, "Restore Vehicle: KTM 390 first", 0},
		{"service, kit for good", store.TrashService, func(t *testing.T, h *holder, vehicle, kit, oil int64) {
			holderExec(t, h, `DELETE FROM service_kits WHERE id = $1`, kit)
		}, "", 1},
		{"kit, vehicle into the trash", store.TrashKit, func(t *testing.T, h *holder, vehicle, kit, oil int64) {
			holderExec(t, h, `DELETE FROM vehicles WHERE id = $1`, vehicle)
			holderExec(t, h, trashed, vehicle)
		}, "Restore Vehicle: KTM 390 first", 0},
		{"kit, line item for good", store.TrashKit, func(t *testing.T, h *holder, vehicle, kit, oil int64) {
			holderExec(t, h, `DELETE FROM items WHERE id = $1`, oil)
		}, "", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, pool, ctx := newStorePool(t)
			v := mustVehicle(t, s, "KTM 390")
			oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
			kin := stock.KitInput{VehicleID: v, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 100}}}
			kin.Normalize()
			kit, err := s.CreateKit(ctx, kin)
			if err != nil {
				t.Fatal(err)
			}
			svc, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v, KitID: &kit, Title: "Oil service", PerformedOn: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			trashID, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
			if err != nil {
				t.Fatal(err)
			}
			if tt.restore == store.TrashKit {
				if trashID, err = s.DeleteKit(ctx, kit, cur(t, s, store.WhatKit, kit)); err != nil {
					t.Fatal(err)
				}
			}

			h := hold(t, pool, `SELECT 1`)
			tt.gone(t, h, v, kit, oil)
			var c calls
			var res store.Restored
			var restored error
			c.run(func() { res, restored = s.RestoreTrash(ctx, trashID) })
			waitBlocked(t, pool, 1, []string{h.xid})
			h.commit(t)
			c.wait()
			noDeadlock(t, "restore", restored)
			if tt.conflict != "" {
				wantConflict(t, "restore", restored, tt.conflict)
				return
			}
			if restored != nil {
				t.Fatalf("restore: %v", restored)
			}
			if res.Dropped != tt.dropped {
				t.Errorf("dropped = %d, want %d", res.Dropped, tt.dropped)
			}
		})
	}
}

// holderExec runs sql in the holder's transaction.
func holderExec(t *testing.T, h *holder, sql string, args ...any) {
	t.Helper()
	if _, err := h.tx.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// Restoring a service while another is logged for its vehicle with the
// same parts: both lock the vehicle, then the parts in id order, so they
// take turns and both take their parts.
func TestRestoringAndLoggingServicesTakeTurns(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	v := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1000})
	ring := mustItem(t, s, stock.ItemInput{Name: "Ring", Unit: stock.UnitPiece, Quantity: 2})
	parts := []stock.KitLineInput{{ItemID: ring, Quantity: 1}, {ItemID: oil, Quantity: 400}}
	svc, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v, Title: "Oil service", PerformedOn: time.Now(), Parts: parts})
	if err != nil {
		t.Fatal(err)
	}
	trashID, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err != nil {
		t.Fatal(err)
	}

	h := hold(t, pool, `SELECT 1 FROM vehicles WHERE id = $1 FOR UPDATE`, v)
	var c calls
	var restored, logged error
	c.run(func() { _, restored = s.RestoreTrash(ctx, trashID) })
	xids := waitBlocked(t, pool, 1, []string{h.xid}, "vehicles")
	c.run(func() {
		_, logged = s.CreateService(ctx, stock.ServiceInput{VehicleID: v, Title: "Top up", PerformedOn: time.Now(),
			Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 400}, {ItemID: ring, Quantity: 1}}})
	})
	waitBlocked(t, pool, 2, xids, "vehicles")
	h.release(t)
	c.wait()
	noDeadlock(t, "restore", restored)
	noDeadlock(t, "log", logged)
	if restored != nil || logged != nil {
		t.Fatalf("restore = %v, log = %v", restored, logged)
	}
	if o, r := mustGet(t, s, oil), mustGet(t, s, ring); o.Quantity != 200 || r.Quantity != 0 {
		t.Errorf("oil = %v, ring = %v; want 200 and 0", o.Quantity, r.Quantity)
	}
	assertLedger(t, s)
}

// Restoring a kit while its item's unit is changed: the restore holds the
// line items FOR SHARE, and the unit change finds the kit's lines (or the
// kit in the trash) and is refused, so 100 ml never reads as 100 g.
func TestRestoringAKitAndChangingAnItemsUnitTakeTurns(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	v := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
	ring := mustItem(t, s, stock.ItemInput{Name: "Ring", Unit: stock.UnitPiece})
	kin := stock.KitInput{VehicleID: v, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: oil, Quantity: 100}, {ItemID: ring, Quantity: 1}}}
	kin.Normalize()
	kit, err := s.CreateKit(ctx, kin)
	if err != nil {
		t.Fatal(err)
	}
	trashID, err := s.DeleteKit(ctx, kit, cur(t, s, store.WhatKit, kit))
	if err != nil {
		t.Fatal(err)
	}
	grams := itemV(t, s, oil, stock.ItemInput{Name: "Oil", Unit: stock.UnitGram})
	grams.Normalize()

	// The restore holds the oil and waits for the ring.
	h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR UPDATE`, ring)
	var c calls
	var restored, changed error
	c.run(func() { _, restored = s.RestoreTrash(ctx, trashID) })
	xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
	done := c.run(func() { changed = s.UpdateItem(ctx, oil, grams, time.Now()) })
	if !blocks(t, pool, done, 2, xids, "items") {
		t.Error("the unit changed while the kit's lines were being restored")
	}
	h.release(t)
	c.wait()
	noDeadlock(t, "restore", restored)
	noDeadlock(t, "change unit", changed)
	if restored != nil {
		t.Fatalf("restore: %v", restored)
	}
	wantConflict(t, "unit change", changed, "unit cannot be changed")
	if it := mustGet(t, s, oil); it.Unit != stock.UnitML {
		t.Errorf("oil unit = %s, want ml", it.Unit)
	}
}

// Deleting an item while its shopping entry is ticked off: both lock the
// entry, then the item, so they take turns. Bought first, the item has a
// purchase and is kept; deleted first, the Bought finds the entry in the
// trash with the item.
func TestDeletingAnItemAndTickingOffItsEntryTakeTurns(t *testing.T) {
	for _, first := range []string{"bought", "delete"} {
		t.Run(first+" first", func(t *testing.T) {
			s, pool, ctx := newStorePool(t)
			pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
			entry, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0)})
			if err != nil {
				t.Fatal(err)
			}
			itemV, entryV := cur(t, s, store.WhatItem, pads), cur(t, s, store.WhatShopping, entry)
			buy := func() error {
				_, _, err := s.MarkBought(ctx, entry, entryV, &stock.MovementInput{Delta: 2, TotalPrice: ptr(20.0)})
				return err
			}
			del := func() error { _, err := s.DeleteItem(ctx, pads, itemV); return err }

			// The first holds the entry and waits for the item.
			h := hold(t, pool, `SELECT 1 FROM items WHERE id = $1 FOR NO KEY UPDATE`, pads)
			var c calls
			var bought, deleted error
			if first == "bought" {
				c.run(func() { bought = buy() })
			} else {
				c.run(func() { deleted = del() })
			}
			xids := waitBlocked(t, pool, 1, []string{h.xid}, "items")
			if first == "bought" {
				c.run(func() { deleted = del() })
			} else {
				c.run(func() { bought = buy() })
			}
			waitBlocked(t, pool, 2, xids, "items", "shopping_entries")
			h.release(t)
			c.wait()
			noDeadlock(t, "bought", bought)
			noDeadlock(t, "delete", deleted)

			if first == "bought" {
				if bought != nil {
					t.Fatalf("bought: %v", bought)
				}
				wantConflict(t, "delete after the purchase", deleted, "Archive it instead")
				if it := mustGet(t, s, pads); it.Quantity != 2 {
					t.Errorf("pads = %v, want 2", it.Quantity)
				}
				assertLedger(t, s)
				return
			}
			if deleted != nil {
				t.Fatalf("delete: %v", deleted)
			}
			var stale *store.StaleError
			if !errors.As(bought, &stale) || !stale.Gone || !stale.InTrash {
				t.Errorf("bought = %v, want the entry gone with its item, in the trash", bought)
			}
		})
	}
}
