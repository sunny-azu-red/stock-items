package store_test

import (
	"errors"
	"slices"
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

func TestVehicleMakesAndModels(t *testing.T) {
	s, ctx := newStore(t)
	makes, err := s.Makes(ctx)
	if err != nil || len(makes) != 0 {
		t.Fatalf("want empty makes, got %v (err %v)", makes, err)
	}
	models, err := s.Models(ctx)
	if err != nil || len(models) != 0 {
		t.Fatalf("want empty models, got %v (err %v)", models, err)
	}

	for _, v := range []stock.VehicleInput{
		{Name: "Bike 1", Make: "KTM", Model: "390 Adventure"},
		{Name: "Bike 2", Make: "KTM", Model: "1290 Super Adventure S"},
		{Name: "Car", Make: "Mitsubishi", Model: "Outlander PHEV"},
		{Name: "Blank", Make: "", Model: ""},
	} {
		v.Normalize()
		if _, err := s.CreateVehicle(ctx, v); err != nil {
			t.Fatal(err)
		}
	}

	gotMakes, err := s.Makes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantMakes := []string{"KTM", "Mitsubishi"}
	if !slices.Equal(gotMakes, wantMakes) {
		t.Errorf("Makes = %v, want %v", gotMakes, wantMakes)
	}

	gotModels, err := s.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantModels := []string{"1290 Super Adventure S", "390 Adventure", "Outlander PHEV"}
	if !slices.Equal(gotModels, wantModels) {
		t.Errorf("Models = %v, want %v", gotModels, wantModels)
	}
}

func TestVehiclePurchaseBaseline(t *testing.T) {
	s, ctx := newStore(t)
	pDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	pKm := 0
	odoKm := 1000
	in := stock.VehicleInput{
		Name:        "GreyJoy",
		Kind:        stock.KindMotorcycle,
		PurchasedOn: &pDate,
		PurchasedKm: &pKm,
		OdometerKm:  &odoKm,
	}
	in.Normalize()
	id, err := s.CreateVehicle(ctx, in)
	if err != nil {
		t.Fatalf("CreateVehicle: %v", err)
	}

	v, err := s.GetVehicle(ctx, id)
	if err != nil {
		t.Fatalf("GetVehicle: %v", err)
	}
	if v.PurchasedOn == nil || !v.PurchasedOn.Equal(pDate) {
		t.Errorf("PurchasedOn = %v, want %v", v.PurchasedOn, pDate)
	}
	if v.PurchasedKm == nil || *v.PurchasedKm != 0 {
		t.Errorf("PurchasedKm = %v, want 0", v.PurchasedKm)
	}

	// ListVehicles also scans them
	list, err := s.ListVehicles(ctx)
	if err != nil {
		t.Fatalf("ListVehicles: %v", err)
	}
	if len(list) != 1 || list[0].PurchasedOn == nil || !list[0].PurchasedOn.Equal(pDate) || list[0].PurchasedKm == nil || *list[0].PurchasedKm != 0 {
		t.Fatalf("ListVehicles = %+v", list)
	}

	// Create a kit for this vehicle
	kitID, err := s.CreateKit(ctx, stock.KitInput{
		VehicleID:  id,
		Name:       "Valve check",
		IntervalKm: ptr(15000),
	})
	if err != nil {
		t.Fatalf("CreateKit: %v", err)
	}
	kit, err := s.GetKit(ctx, kitID)
	if err != nil {
		t.Fatalf("GetKit: %v", err)
	}
	if kit.VehiclePurchasedOn == nil || !kit.VehiclePurchasedOn.Equal(pDate) {
		t.Errorf("kit.VehiclePurchasedOn = %v, want %v", kit.VehiclePurchasedOn, pDate)
	}
	if kit.VehiclePurchasedKm == nil || *kit.VehiclePurchasedKm != 0 {
		t.Errorf("kit.VehiclePurchasedKm = %v, want 0", kit.VehiclePurchasedKm)
	}
	// Reminders count from 0 km: at 1000 km, 14000 km left -> DueOK
	due := kit.Due(v.OdometerKm, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if due.State != stock.DueOK || due.KmLeft == nil || *due.KmLeft != 14000 {
		t.Errorf("kit.Due = %+v, want DueOK with 14000 km left", due)
	}

	// Log a service at 5000 km on 2026-06-01
	svcDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	svcKm := 5000
	_, err = s.CreateService(ctx, stock.ServiceInput{
		VehicleID:   id,
		Title:       "Oil service",
		PerformedOn: svcDate,
		OdometerKm:  &svcKm,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	// Try to update PurchasedKm higher than the service reading (5000 km)
	badKm := 6000
	upd := vehicleV(t, s, id, stock.VehicleInput{
		Name:        "GreyJoy",
		Kind:        stock.KindMotorcycle,
		PurchasedOn: &pDate,
		PurchasedKm: &badKm,
		OdometerKm:  &svcKm,
	})
	var conflict *store.ConflictError
	if err := s.UpdateVehicle(ctx, id, upd); !errors.As(err, &conflict) || conflict.Field != "purchased_km" {
		t.Errorf("want ConflictError for purchased_km > service km, got %v", err)
	}

	// Try to update PurchasedOn later than the service date (2026-06-01)
	badDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	upd = vehicleV(t, s, id, stock.VehicleInput{
		Name:        "GreyJoy",
		Kind:        stock.KindMotorcycle,
		PurchasedOn: &badDate,
		PurchasedKm: &pKm,
		OdometerKm:  &svcKm,
	})
	if err := s.UpdateVehicle(ctx, id, upd); !errors.As(err, &conflict) || conflict.Field != "purchased_on" {
		t.Errorf("want ConflictError for purchased_on > service date, got %v", err)
	}

	// Good update: earlier date and valid km
	goodDate := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	upd = stock.VehicleInput{
		Name:        "GreyJoy",
		Kind:        stock.KindMotorcycle,
		Color:       "#f97316",
		PurchasedOn: &goodDate,
		PurchasedKm: &pKm,
		OdometerKm:  &svcKm,
	}
	upd.Normalize()
	if err := s.UpdateVehicle(ctx, id, vehicleV(t, s, id, upd)); err != nil {
		t.Fatalf("good UpdateVehicle: %v", err)
	}
	v, _ = s.GetVehicle(ctx, id)
	if !v.PurchasedOn.Equal(goodDate) {
		t.Errorf("updated PurchasedOn = %v, want %v", v.PurchasedOn, goodDate)
	}

	// Trash and restore on a standalone vehicle preserves purchased_on and purchased_km
	standalone := stock.VehicleInput{
		Name:        "KillJoy",
		Kind:        stock.KindMotorcycle,
		Color:       "#2563eb",
		PurchasedOn: &goodDate,
		PurchasedKm: &pKm,
	}
	standalone.Normalize()
	sID, err := s.CreateVehicle(ctx, standalone)
	if err != nil {
		t.Fatalf("CreateVehicle standalone: %v", err)
	}
	vStand, _ := s.GetVehicle(ctx, sID)
	trashID, err := s.DeleteVehicle(ctx, sID, vStand.Version)
	if err != nil {
		t.Fatalf("DeleteVehicle: %v", err)
	}
	if _, err := s.RestoreTrash(ctx, trashID); err != nil {
		t.Fatalf("RestoreTrash: %v", err)
	}
	vStand, err = s.GetVehicle(ctx, sID)
	if err != nil {
		t.Fatalf("GetVehicle after restore: %v", err)
	}
	if vStand.PurchasedOn == nil || !vStand.PurchasedOn.Equal(goodDate) || vStand.PurchasedKm == nil || *vStand.PurchasedKm != 0 {
		t.Errorf("restored vehicle = %+v", vStand)
	}
}
