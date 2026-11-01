package store_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

func TestMovements(t *testing.T) {
	s, ctx := newStore(t)
	id := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1000})

	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: 4000, TotalPrice: ptr(60.0), Supplier: "Shop"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -1700}); err != nil {
		t.Fatal(err)
	}
	var short *store.InsufficientStockError
	err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -5000})
	if !errors.As(err, &short) || short.Have != 3300 || short.Need != 5000 {
		t.Fatalf("want InsufficientStockError, got %v", err)
	}
	if it := mustGet(t, s, id); it.Quantity != 3300 {
		t.Errorf("quantity = %v, want 3300", it.Quantity)
	}
	var verrs stock.Errors
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: -1}); !errors.As(err, &verrs) {
		t.Errorf("want validation errors, got %v", err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: 999999, Kind: stock.MovePurchase, Delta: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)

	price, err := s.LastUnitPrice(ctx, id)
	if err != nil || price == nil || *price != 0.015 {
		t.Errorf("last unit price = %v, %v", price, err)
	}
	st, err := s.GetStats(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if st.StockValue != 49.5 || st.Spent12m != 60 {
		t.Errorf("stats = %+v", st)
	}
	all, _ := s.ListMovements(ctx, store.MovementFilter{Limit: 2})
	if len(all) != 2 || all[0].ItemName != "Oil" {
		t.Errorf("recent movements = %+v", all)
	}
}

func TestPurchaseSetsBestBefore(t *testing.T) {
	s, ctx := newStore(t)
	expired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2029, 6, 30, 0, 0, 0, 0, time.UTC)
	id := mustItem(t, s, stock.ItemInput{Name: "Coolant", Unit: stock.UnitML, Quantity: 500, ExpiresOn: &expired})

	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: 1000}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); !it.ExpiresOn.Equal(expired) {
		t.Errorf("a purchase without a date should keep the expiry, got %v", it.ExpiresOn)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: 1000, ExpiresOn: &fresh}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); !it.ExpiresOn.Equal(fresh) {
		t.Errorf("expiry = %v, want %v", it.ExpiresOn, fresh)
	}
	var verrs stock.Errors
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -100, ExpiresOn: &expired}); !errors.As(err, &verrs) {
		t.Errorf("use with a best-before date: want validation error, got %v", err)
	}

	entry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &id})
	later := fresh.AddDate(1, 0, 0)
	if _, _, err := s.MarkBought(ctx, entry, cur(t, s, store.WhatShopping, entry), &stock.MovementInput{Delta: 1000, ExpiresOn: &later}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); !it.ExpiresOn.Equal(later) {
		t.Errorf("expiry after shopping = %v, want %v", it.ExpiresOn, later)
	}
	assertLedger(t, s)
}

func TestLastPurchases(t *testing.T) {
	s, ctx := newStore(t)
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 2})
	older := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, in := range []stock.MovementInput{
		{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, TotalPrice: ptr(20.0), Supplier: "A", OccurredOn: &newer},
		{ItemID: oil, Kind: stock.MovePurchase, Delta: 4000, TotalPrice: ptr(60.0), Supplier: "B", OccurredOn: &older},
		{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, OccurredOn: &newer}, // no price: ignored
	} {
		if err := s.AddMovement(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LastPurchases(ctx, []int64{oil, pads})
	if err != nil {
		t.Fatal(err)
	}
	lp, ok := got[oil]
	if len(got) != 1 || !ok || lp.UnitPrice != 0.02 || lp.Supplier != "A" || !lp.On.Equal(newer) {
		t.Errorf("last purchases = %+v", got)
	}
	if empty, err := s.LastPurchases(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no ids: %v, %v", empty, err)
	}
}

func TestUpdateAndDeleteMovements(t *testing.T) {
	s, ctx := newStore(t)
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 500})
	day := stock.DateOnly(time.Now())
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, TotalPrice: ptr(670.0), OccurredOn: &day}); err != nil {
		t.Fatal(err)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MovePurchase})
	purchase := moves[0].ID

	// The price typo is fixed, and every money figure follows.
	if err := s.UpdateMovement(ctx, purchase, editV(t, s, purchase, stock.MovementEdit{Amount: 1000, TotalPrice: ptr(67.0), Supplier: "moto24"})); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.LastUnitPrice(ctx, oil); p == nil || *p != 0.067 {
		t.Errorf("last unit price = %v, want 0.067", p)
	}
	if st, _ := s.GetStats(ctx, time.Now()); math.Abs(st.StockValue-100.5) > 0.001 || math.Abs(st.Spent12m-67) > 0.001 {
		t.Errorf("stats = %+v, want value 100.5 and spent 67", st)
	}

	// The amount moves the stock by the difference.
	if err := s.UpdateMovement(ctx, purchase, editV(t, s, purchase, stock.MovementEdit{Amount: 4000, TotalPrice: ptr(67.0)})); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 4500 {
		t.Errorf("quantity = %v, want 4500", it.Quantity)
	}

	// Using 4.2 L leaves 300 ml: shrinking the purchase below what was used fails.
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MoveUse, Delta: -4200}); err != nil {
		t.Fatal(err)
	}
	var short *store.InsufficientStockError
	if err := s.UpdateMovement(ctx, purchase, editV(t, s, purchase, stock.MovementEdit{Amount: 500})); !errors.As(err, &short) || short.Have != 300 || short.Need != 3500 {
		t.Errorf("shrink used-up purchase: want InsufficientStockError(300, 3500), got %v", err)
	}
	if _, _, err := s.DeleteMovement(ctx, purchase, cur(t, s, store.WhatMovement, purchase)); !errors.As(err, &short) {
		t.Errorf("delete used-up purchase: want InsufficientStockError, got %v", err)
	}

	// Deleting the use puts it back; then the purchase can go too.
	uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveUse})
	if _, _, err := s.DeleteMovement(ctx, uses[0].ID, cur(t, s, store.WhatMovement, uses[0].ID)); err != nil {
		t.Fatal(err)
	}
	m, _, err := s.DeleteMovement(ctx, purchase, cur(t, s, store.WhatMovement, purchase))
	if err != nil || m.ItemID != oil || m.Kind != stock.MovePurchase {
		t.Fatalf("delete purchase = %+v, %v", m, err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 500 {
		t.Errorf("quantity = %v, want 500", it.Quantity)
	}
	if n, _ := s.CountPurchases(ctx, oil); n != 0 {
		t.Errorf("purchases = %d, want 0", n)
	}
	if _, err := s.GetMovement(ctx, purchase); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted movement: want ErrNotFound, got %v", err)
	}
	if err := s.UpdateMovement(ctx, purchase, editV(t, s, purchase, stock.MovementEdit{Amount: 1})); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update deleted movement: want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)
}

func TestServiceMovementsChangeOnlyThroughTheService(t *testing.T) {
	s, ctx := newStore(t)
	k390 := mustVehicle(t, s, "KTM 390")
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 2})
	in := stock.ServiceInput{VehicleID: k390, Title: "Brakes", PerformedOn: time.Now(), Parts: []stock.KitLineInput{{ItemID: pads, Quantity: 1}}}
	in.Normalize()
	if _, err := s.CreateService(ctx, in); err != nil {
		t.Fatal(err)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MoveService})
	var conflict *store.ConflictError
	if err := s.UpdateMovement(ctx, moves[0].ID, editV(t, s, moves[0].ID, stock.MovementEdit{Amount: 2})); !errors.As(err, &conflict) {
		t.Errorf("update: want ConflictError, got %v", err)
	}
	if _, _, err := s.DeleteMovement(ctx, moves[0].ID, cur(t, s, store.WhatMovement, moves[0].ID)); !errors.As(err, &conflict) {
		t.Errorf("delete: want ConflictError, got %v", err)
	}
	if it := mustGet(t, s, pads); it.Quantity != 1 {
		t.Errorf("quantity = %v, want 1", it.Quantity)
	}
	assertLedger(t, s)
}
