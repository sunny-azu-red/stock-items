package store_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

func ptr[T any](v T) *T { return &v }

// cur reads a record's current version, as a page opened now would carry
// it (0 when the record is gone). It is safe to call from goroutines.
func cur(t *testing.T, s *store.Store, what string, id int64) int {
	t.Helper()
	ctx := context.Background()
	var v int
	var err error
	switch what {
	case store.WhatItem:
		var it stock.Item
		it, err = s.GetItem(ctx, id)
		v = it.Version
	case store.WhatVehicle:
		var x stock.Vehicle
		x, err = s.GetVehicle(ctx, id)
		v = x.Version
	case store.WhatKit:
		var k stock.Kit
		k, err = s.GetKit(ctx, id)
		v = k.Version
	case store.WhatService:
		var r stock.ServiceRecord
		r, err = s.GetService(ctx, id)
		v = r.Version
	case store.WhatMovement:
		var m stock.Movement
		m, err = s.GetMovement(ctx, id)
		v = m.Version
	case store.WhatShopping:
		var e stock.ShoppingEntry
		e, err = s.GetShopping(ctx, id)
		v = e.Version
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Error(err)
	}
	return v
}

// itemV, vehicleV, kitV, serviceV and editV give an edit the record's
// current version, like a form opened now.
func itemV(t *testing.T, s *store.Store, id int64, in stock.ItemInput) stock.ItemInput {
	in.Version = cur(t, s, store.WhatItem, id)
	return in
}

func vehicleV(t *testing.T, s *store.Store, id int64, in stock.VehicleInput) stock.VehicleInput {
	in.Version = cur(t, s, store.WhatVehicle, id)
	return in
}

func kitV(t *testing.T, s *store.Store, id int64, in stock.KitInput) stock.KitInput {
	in.Version = cur(t, s, store.WhatKit, id)
	return in
}

func serviceV(t *testing.T, s *store.Store, id int64, in stock.ServiceInput) stock.ServiceInput {
	in.Version = cur(t, s, store.WhatService, id)
	return in
}

func editV(t *testing.T, s *store.Store, id int64, e stock.MovementEdit) stock.MovementEdit {
	e.Version = cur(t, s, store.WhatMovement, id)
	return e
}

func newStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	return store.New(testdb.New(t)), context.Background()
}

func mustVehicle(t *testing.T, s *store.Store, name string) int64 {
	t.Helper()
	in := stock.VehicleInput{Name: name}
	in.Normalize()
	id, err := s.CreateVehicle(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustItem(t *testing.T, s *store.Store, in stock.ItemInput) int64 {
	t.Helper()
	in.Normalize()
	if errs := in.Validate(); errs.Any() {
		t.Fatal(errs)
	}
	id, err := s.CreateItem(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustGet(t *testing.T, s *store.Store, id int64) stock.Item {
	t.Helper()
	it, err := s.GetItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// assertLedger checks that every item's quantity equals the sum of its
// stock movements.
func assertLedger(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	items, err := s.ListItems(ctx, store.ItemFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		moves, err := s.ListMovements(ctx, store.MovementFilter{ItemID: it.ID})
		if err != nil {
			t.Fatal(err)
		}
		sum := 0.0
		for _, m := range moves {
			sum += m.Delta
		}
		// Compare in thousandths: float sums of 0.1 + 0.2 are not exact.
		if math.Round(sum*1000) != math.Round(it.Quantity*1000) {
			t.Errorf("ledger mismatch for %s: quantity %v, movements sum %v", it.Name, it.Quantity, sum)
		}
	}
}

func ids(items []stock.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func mustList(t *testing.T, s *store.Store, f store.ItemFilter) []stock.Item {
	t.Helper()
	items, err := s.ListItems(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestDatesComeFromTheAppClock(t *testing.T) {
	s, ctx := newStore(t)
	expires := time.Date(2030, 3, 1, 0, 0, 0, 0, time.UTC)
	mustItem(t, s, stock.ItemInput{Name: "Coolant", Unit: stock.UnitML, Quantity: 1, ExpiresOn: &expires})
	bought := time.Date(2029, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: mustList(t, s, store.ItemFilter{})[0].ID, Kind: stock.MovePurchase,
		Delta: 1, TotalPrice: ptr(10.0), OccurredOn: &bought}); err != nil {
		t.Fatal(err)
	}
	later := time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC)
	if got := mustList(t, s, store.ItemFilter{Expiring: true, Today: later}); len(got) != 1 {
		t.Errorf("expiring on %s = %v", later.Format("2006-01-02"), ids(got))
	}
	if got := mustList(t, s, store.ItemFilter{Expiring: true}); len(got) != 0 {
		t.Errorf("expiring today = %v", ids(got))
	}
	// A purchase dated the day after doesn't count yet.
	dayAfter := later.AddDate(0, 0, 1)
	id := mustList(t, s, store.ItemFilter{})[0].ID
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase,
		Delta: 1, TotalPrice: ptr(5.0), OccurredOn: &dayAfter}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.GetStats(ctx, later); st.Spent12m != 10 {
		t.Errorf("spent in the 12 months before %s = %v, want 10", later.Format("2006-01-02"), st.Spent12m)
	}

	// Counts and edited quantities are dated by the caller.
	counted := time.Date(2029, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.CorrectStock(ctx, id, 10, "", counted, ""); err != nil {
		t.Fatal(err)
	}
	edited := time.Date(2029, 7, 2, 0, 0, 0, 0, time.UTC)
	in := stock.ItemInput{Name: "Coolant", Unit: stock.UnitML, Quantity: 12, ExpiresOn: &expires}
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, in), edited); err != nil {
		t.Fatal(err)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: id, Kind: stock.MoveCorrection})
	if len(moves) != 2 || !moves[0].OccurredOn.Equal(edited) || !moves[1].OccurredOn.Equal(counted) {
		t.Errorf("corrections = %+v", moves)
	}
}

// Versions move on with every write that changes what a record's edit
// form or page shows, and not with stock changes or odometer readings,
// which their forms guard otherwise (quantity_was, odometer_was).
// Restoring from the trash gives a new version.
func TestVersions(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1000})
	step := func(what string, id int64, want int, why string) {
		t.Helper()
		if got := cur(t, s, what, id); got != want {
			t.Errorf("%s: %s version = %d, want %d", why, what, got, want)
		}
	}
	step(store.WhatItem, oil, 1, "created")
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MoveUse, Delta: -100}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CorrectStock(ctx, oil, 800, "", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	step(store.WhatItem, oil, 1, "after stock changes")
	expires := time.Date(2030, 1, 31, 0, 0, 0, 0, time.UTC)
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, ExpiresOn: &expires}); err != nil {
		t.Fatal(err)
	}
	step(store.WhatItem, oil, 2, "after a purchase with a best-before date")

	upd := stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1800, QuantityWas: ptr(1800.0), Notes: "x", Version: 1}
	var stale *store.StaleError
	if err := s.UpdateItem(ctx, oil, upd, time.Now()); !errors.As(err, &stale) || stale.Gone {
		t.Errorf("stale update: %v", err)
	}
	if err := s.UpdateItem(ctx, oil, itemV(t, s, oil, upd), time.Now()); err != nil {
		t.Fatal(err)
	}
	step(store.WhatItem, oil, 3, "after an edit")

	// Odometer readings, set or moved by a service, leave the vehicle's
	// version alone.
	if err := s.SetOdometer(ctx, v, 12000); err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v, Title: "Chain", PerformedOn: time.Now(), OdometerKm: ptr(12500)})
	if err != nil {
		t.Fatal(err)
	}
	step(store.WhatVehicle, v, 1, "after odometer readings")
	if _, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc)); err != nil {
		t.Fatal(err)
	}
	step(store.WhatVehicle, v, 1, "after deleting a service that moved the odometer")

	// The shopping entry: adding to it is a change, asking for less than
	// it has is not.
	entry, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(1000.0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(500.0)}); err != nil {
		t.Fatal(err)
	}
	step(store.WhatShopping, entry, 2, "after adding to it")
	if _, err := s.EnsureShopping(ctx, oil, 100, ""); err != nil {
		t.Fatal(err)
	}
	step(store.WhatShopping, entry, 2, "after asking for less")
	if _, _, err := s.MarkBought(ctx, entry, 2, &stock.MovementInput{Delta: 500}); err != nil {
		t.Fatal(err)
	}
	step(store.WhatShopping, entry, 3, "after buying part of it")

	// Deleted, then restored: a new version, so a form opened before the
	// delete is stale; while deleted, the record is gone and in the trash.
	trashID, err := s.DeleteShopping(ctx, entry, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MarkBought(ctx, entry, 3, nil); !errors.As(err, &stale) || !stale.Gone || !stale.InTrash ||
		!errors.Is(err, store.ErrNotFound) {
		t.Errorf("bought while deleted: %v", err)
	}
	if _, err := s.RestoreTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	step(store.WhatShopping, entry, 4, "after the Undo")
	if _, err := s.DeleteShopping(ctx, entry, 3); !errors.As(err, &stale) || stale.Gone {
		t.Errorf("delete with the version before the Undo: %v", err)
	}
	assertLedger(t, s)
}
