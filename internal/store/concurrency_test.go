package store_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// Two services taking the same parts in opposite order must not deadlock.
func TestConcurrentServicesDoNotDeadlock(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	a := mustItem(t, s, stock.ItemInput{Name: "A", Unit: stock.UnitPiece, Quantity: 1000})
	b := mustItem(t, s, stock.ItemInput{Name: "B", Unit: stock.UnitPiece, Quantity: 1000})
	var wg sync.WaitGroup
	errs := make(chan error, 80)
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parts := []stock.KitLineInput{{ItemID: a, Quantity: 1}, {ItemID: b, Quantity: 1}}
			if g%2 == 1 {
				parts[0], parts[1] = parts[1], parts[0]
			}
			for range 20 {
				_, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v, Title: "x", PerformedOn: time.Now(), Parts: parts})
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := mustGet(t, s, a).Quantity; got != 920 {
		t.Errorf("A = %v, want 920", got)
	}
	assertLedger(t, s)
}

// Saving a kit holds key-share locks on its items (the foreign key) while
// services lock the same items; neither may deadlock the other.
func TestKitSavesAndServicesDoNotDeadlock(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	a := mustItem(t, s, stock.ItemInput{Name: "A", Unit: stock.UnitPiece, Quantity: 1000})
	b := mustItem(t, s, stock.ItemInput{Name: "B", Unit: stock.UnitPiece, Quantity: 1000})
	kin := stock.KitInput{VehicleID: v, Name: "Kit"}
	kin.Normalize()
	kit, err := s.CreateKit(ctx, kin)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 80)
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lines := []stock.KitLineInput{{ItemID: b, Quantity: 1}, {ItemID: a, Quantity: 1}}
			for range 20 {
				var err error
				if g%2 == 0 {
					in := stock.KitInput{VehicleID: v, Name: "Kit", Lines: lines}
					err = s.UpdateKit(ctx, kit, kitV(t, s, kit, in))
					if errors.As(err, new(*store.StaleError)) {
						err = nil // the other saver got in between: refused, as two tabs would be
					}
				} else {
					_, err = s.CreateService(ctx, stock.ServiceInput{VehicleID: v, Title: "x", PerformedOn: time.Now(),
						Parts: []stock.KitLineInput{{ItemID: a, Quantity: 1}, {ItemID: b, Quantity: 1}}})
				}
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	assertLedger(t, s)
}

// Services with readings take the vehicle's lock before their parts, as
// deleting one does, so logging and deleting at once can't deadlock.
func TestLoggingAndDeletingServicesDoNotDeadlock(t *testing.T) {
	s, ctx := newStore(t)
	km := 10000
	vin := stock.VehicleInput{Name: "KTM 390", OdometerKm: &km}
	vin.Normalize()
	v, _ := s.CreateVehicle(ctx, vin)
	a := mustItem(t, s, stock.ItemInput{Name: "A", Unit: stock.UnitPiece, Quantity: 1000})
	b := mustItem(t, s, stock.ItemInput{Name: "B", Unit: stock.UnitPiece, Quantity: 1000})
	var reading sync.Mutex
	next := km
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 15 {
				reading.Lock()
				next += 10
				r := next
				reading.Unlock()
				in := stock.ServiceInput{VehicleID: v, Title: "x", PerformedOn: time.Now(), OdometerKm: &r,
					Parts: []stock.KitLineInput{{ItemID: a, Quantity: 1}, {ItemID: b, Quantity: 1}}}
				id, err := s.CreateService(ctx, in)
				if err != nil {
					errs <- err
					continue
				}
				if _, err := s.DeleteService(ctx, id, cur(t, s, store.WhatService, id)); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	assertLedger(t, s)
}

// Photo uploads lock the item first, as deleting it does: uploads and a
// delete at once neither deadlock nor trip over each other's rows.
func TestPhotoUploadsAndDeletesTakeTurns(t *testing.T) {
	s, ctx := newStore(t)
	for range 5 {
		id := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for g := range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var err error
				if g == 3 {
					_, err = s.DeleteItem(ctx, id, cur(t, s, store.WhatItem, id))
				} else {
					_, err = s.SetPhoto(ctx, id, "image/jpeg", []byte{byte(g)}, []byte{byte(g)})
				}
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	}
}
