package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

func TestVehicles(t *testing.T) {
	s, ctx := newStore(t)
	id := mustVehicle(t, s, "KTM 390")

	dup := stock.VehicleInput{Name: "ktm 390"}
	dup.Normalize()
	var conflict *store.ConflictError
	if _, err := s.CreateVehicle(ctx, dup); !errors.As(err, &conflict) || conflict.Field != "name" {
		t.Fatalf("duplicate name: want ConflictError, got %v", err)
	}

	upd := stock.VehicleInput{Name: "KTM 390 Duke", Kind: stock.KindMotorcycle, Make: "KTM", Year: ptr(2021), Color: "#ff6600", OdometerKm: ptr(12000)}
	if err := s.UpdateVehicle(ctx, id, vehicleV(t, s, id, upd)); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVehicle(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "KTM 390 Duke" || *v.Year != 2021 || *v.OdometerKm != 12000 || v.Subtitle() != "KTM (2021)" {
		t.Errorf("vehicle = %+v", v)
	}
	if err := s.SetOdometer(ctx, id, 12500); err != nil {
		t.Fatal(err)
	}

	itemID := mustItem(t, s, stock.ItemInput{Name: "Oil Filter", Unit: stock.UnitPiece, VehicleIDs: []int64{id}})
	list, _ := s.ListVehicles(ctx)
	if len(list) != 1 || list[0].ItemCount != 1 || *list[0].OdometerKm != 12500 {
		t.Errorf("list = %+v", list)
	}

	if _, err := s.DeleteVehicle(ctx, id, cur(t, s, store.WhatVehicle, id)); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, itemID); len(it.VehicleIDs) != 0 {
		t.Errorf("item should be untagged after vehicle delete, got %v", it.VehicleIDs)
	}
	if _, err := s.GetVehicle(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := s.DeleteVehicle(ctx, id, cur(t, s, store.WhatVehicle, id)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double delete: want ErrNotFound, got %v", err)
	}
}

func TestVehicleCountsActiveItems(t *testing.T) {
	s, ctx := newStore(t)
	k390 := mustVehicle(t, s, "KTM 390")
	mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, VehicleIDs: []int64{k390}})
	old := mustItem(t, s, stock.ItemInput{Name: "Old pads", Unit: stock.UnitPiece, VehicleIDs: []int64{k390}})
	if _, err := s.ArchiveItem(ctx, old, cur(t, s, store.WhatItem, old)); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetVehicle(ctx, k390); v.ItemCount != 1 {
		t.Errorf("item count = %d, want 1", v.ItemCount)
	}
}

func TestOdometerGuards(t *testing.T) {
	s, ctx := newStore(t)
	km := 10000
	vin := stock.VehicleInput{Name: "KTM 390", OdometerKm: &km}
	vin.Normalize()
	v, err := s.CreateVehicle(ctx, vin)
	if err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	service := func(on time.Time, km int) (int64, error) {
		in := stock.ServiceInput{VehicleID: v, Title: "Check", PerformedOn: on, OdometerKm: &km}
		in.Normalize()
		return s.CreateService(ctx, in)
	}
	if _, err := service(day(10), 12000); err != nil {
		t.Fatal(err)
	}
	var conflict *store.ConflictError
	tests := []struct {
		name string
		on   time.Time
		km   int
		ok   bool
	}{
		{"later and higher", day(20), 13000, true},
		{"later but lower", day(25), 11000, false},
		{"earlier and lower", day(1), 11000, true},
		{"earlier but higher", day(5), 12500, false},
		{"same day, either way", day(10), 11900, true},
	}
	for _, tt := range tests {
		_, err := service(tt.on, tt.km)
		if tt.ok && err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		if !tt.ok && (!errors.As(err, &conflict) || conflict.Field != "odometer_km") {
			t.Errorf("%s: want odometer conflict, got %v", tt.name, err)
		}
	}

	// The vehicle's reading can't go below a service's, but other edits
	// still save while it stays as it is.
	if err := s.SetOdometer(ctx, v, 12500); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "13\u202f000 km") {
		t.Errorf("lower than a service: want conflict naming 13,000 km, got %v", err)
	}
	if err := s.SetOdometer(ctx, v, 13500); err != nil {
		t.Fatal(err)
	}
	upd := vin
	upd.Name = "KTM 390 Duke"
	upd.OdometerKm = ptr(13500)
	if err := s.UpdateVehicle(ctx, v, vehicleV(t, s, v, upd)); err != nil {
		t.Errorf("rename: %v", err)
	}
	upd.OdometerKm = ptr(9000)
	if err := s.UpdateVehicle(ctx, v, vehicleV(t, s, v, upd)); !errors.As(err, &conflict) {
		t.Errorf("lowering through the form: want conflict, got %v", err)
	}
}

func TestVehicleFormKeepsANewerOdometer(t *testing.T) {
	s, ctx := newStore(t)
	km := 10000
	in := stock.VehicleInput{Name: "KTM 390", OdometerKm: &km}
	in.Normalize()
	id, err := s.CreateVehicle(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	// The edit form was opened at 10,000; meanwhile the reading went to 12,000.
	if err := s.SetOdometer(ctx, id, 12000); err != nil {
		t.Fatal(err)
	}
	in.Plate, in.KeepOdometer = "B-123", true
	if err := s.UpdateVehicle(ctx, id, vehicleV(t, s, id, in)); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetVehicle(ctx, id); *v.OdometerKm != 12000 || v.Plate != "B-123" {
		t.Errorf("vehicle = %+v", v)
	}
	// Clearing the reading is refused once services have readings.
	svc := stock.ServiceInput{VehicleID: id, Title: "Chain", PerformedOn: time.Now(), OdometerKm: ptr(12000)}
	svc.Normalize()
	if _, err := s.CreateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	in.KeepOdometer, in.OdometerKm = false, nil
	var conflict *store.ConflictError
	if err := s.UpdateVehicle(ctx, id, vehicleV(t, s, id, in)); !errors.As(err, &conflict) || conflict.Field != "odometer_km" {
		t.Errorf("clearing: want conflict, got %v", err)
	}
}
