package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

func TestKitsAndServices(t *testing.T) {
	s, ctx := newStore(t)
	k390, k1290 := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
	filter := mustItem(t, s, stock.ItemInput{Name: "Oil Filter", Brand: "OEM", Unit: stock.UnitPiece, Quantity: 2, VehicleIDs: []int64{k390}})
	oil := mustItem(t, s, stock.ItemInput{Name: "15W50", Brand: "Silkolene", Unit: stock.UnitML, Quantity: 525, VehicleIDs: []int64{k390}})

	kitIn := stock.KitInput{VehicleID: k390, Name: "Oil service", IntervalDistance: ptr(7500), IntervalMonths: ptr(12),
		Lines: []stock.KitLineInput{{ItemID: filter, Quantity: 1}, {ItemID: oil, Quantity: 1700}}}
	kitIn.Normalize()
	kitID, err := s.CreateKit(ctx, kitIn)
	if err != nil {
		t.Fatal(err)
	}
	kit, err := s.GetKit(ctx, kitID)
	if err != nil {
		t.Fatal(err)
	}
	if r := kit.ReadinessAt(kit.VehicleOdometer, store.AppToday()); r.Lines != 2 || r.ShortLines != 1 || kit.VehicleName != "KTM 390" || kit.LastService != nil {
		t.Errorf("kit = %+v readiness %+v", kit, r)
	}
	// Never done: it counts from the purchase (1 Jan 2020, anchored), so
	// 12 months on it is overdue.
	if got := kit.Due(0, time.Now()).State; got != stock.DueOverdue {
		t.Errorf("due state = %s, want overdue", got)
	}

	// Not enough oil: the whole service must roll back.
	svc := stock.ServiceInput{VehicleID: k390, KitID: &kitID, PerformedOn: time.Now(), Odometer: ptr(15000), Title: "Oil service",
		Parts: []stock.KitLineInput{{ItemID: filter, Quantity: 1}, {ItemID: oil, Quantity: 1700}}}
	var short *store.InsufficientStockError
	if _, err := s.CreateService(ctx, svc); !errors.As(err, &short) || short.ItemName != "Silkolene 15W50" {
		t.Fatalf("want InsufficientStockError, got %v", err)
	}
	if it := mustGet(t, s, filter); it.Quantity != 2 {
		t.Errorf("filter quantity after rollback = %v", it.Quantity)
	}
	if list, _ := s.ListServices(ctx, store.ServiceFilter{}); len(list) != 0 {
		t.Errorf("service should not exist after rollback: %+v", list)
	}

	// Shortfall goes to the shopping list.
	for _, l := range kit.Lines {
		if short := l.Short(); short > 0 {
			if _, err := s.EnsureShopping(ctx, l.ItemID, short, "For "+kit.Name); err != nil {
				t.Fatal(err)
			}
		}
	}
	list, _ := s.ListShopping(ctx)
	if len(list) != 1 || *list[0].Quantity != 1175 || list[0].ItemName != "Silkolene 15W50" {
		t.Fatalf("shopping = %+v", list)
	}
	if _, _, err := s.MarkBought(ctx, list[0].ID, cur(t, s, store.WhatShopping, list[0].ID), &stock.MovementInput{Delta: 4000, TotalPrice: ptr(55.0)}); err != nil {
		t.Fatal(err)
	}

	// Kit from another vehicle is rejected.
	wrong := svc
	wrong.VehicleID = k1290
	var conflict *store.ConflictError
	if _, err := s.CreateService(ctx, wrong); !errors.As(err, &conflict) {
		t.Errorf("want ConflictError for foreign kit, got %v", err)
	}

	svcID, err := s.CreateService(ctx, svc)
	if err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 2825 {
		t.Errorf("oil after service = %v, want 2825", it.Quantity)
	}
	v, _ := s.GetVehicle(ctx, k390)
	if v.Odometer != 15000 {
		t.Errorf("odometer = %v", v.Odometer)
	}
	rec, err := s.GetService(ctx, svcID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Parts) != 2 || rec.Parts[1].Quantity != 1700 || rec.KitName != "Oil service" || rec.PartCount != 2 {
		t.Errorf("service = %+v", rec)
	}
	kit, _ = s.GetKit(ctx, kitID)
	if kit.LastService == nil || kit.LastService.ID != svcID {
		t.Errorf("last service = %+v", kit.LastService)
	}
	if got := kit.Due(v.Odometer, time.Now()).State; got != stock.DueOK {
		t.Errorf("due after service = %s", got)
	}

	// A lower odometer reading never moves the odometer backwards.
	older := stock.ServiceInput{VehicleID: k390, PerformedOn: time.Now().AddDate(-1, 0, 0), Odometer: ptr(9000), Title: "Chain"}
	if _, err := s.CreateService(ctx, older); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetVehicle(ctx, k390)
	if v.Odometer != 15000 {
		t.Errorf("odometer moved backwards to %d", v.Odometer)
	}
	assertLedger(t, s)

	if _, err := s.DeleteService(ctx, svcID, cur(t, s, store.WhatService, svcID)); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 4525 {
		t.Errorf("oil after deleting service = %v, want 4525", it.Quantity)
	}
	if it := mustGet(t, s, filter); it.Quantity != 2 {
		t.Errorf("filter after deleting service = %v, want 2", it.Quantity)
	}
	assertLedger(t, s)

	using, _ := s.ListKits(ctx, store.KitFilter{ItemID: oil})
	if len(using) != 1 {
		t.Errorf("kits using oil = %d", len(using))
	}
	if _, err := s.DeleteKit(ctx, kitID, cur(t, s, store.WhatKit, kitID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKit(ctx, kitID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// A kit moved to another vehicle starts without the old vehicle's history.
func TestKitMovedToAnotherVehicle(t *testing.T) {
	s, ctx := newStore(t)
	v1, v2 := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
	in := stock.KitInput{VehicleID: v1, Name: "Oil", IntervalDistance: ptr(6000)}
	kit, err := s.CreateKit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v1, KitID: &kit, Title: "Oil", PerformedOn: time.Now(), Odometer: ptr(20000)}); err != nil {
		t.Fatal(err)
	}
	in.VehicleID = v2
	if err := s.UpdateKit(ctx, kit, kitV(t, s, kit, in)); err != nil {
		t.Fatal(err)
	}
	k, err := s.GetKit(ctx, kit)
	if err != nil {
		t.Fatal(err)
	}
	if k.LastService != nil {
		t.Errorf("moved kit kept the other vehicle's last service: %+v", k.LastService)
	}
}

func TestDeleteServicePutsOdometerBack(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	n := 0
	vehicle := func(odo *int) int64 {
		t.Helper()
		n++
		in := stock.VehicleInput{Name: fmt.Sprintf("Bike %d", n), Odometer: odo}
		in.Normalize()
		id, err := s.CreateVehicle(ctx, anchored(in))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	service := func(vehicleID int64, odo int) int64 {
		t.Helper()
		in := stock.ServiceInput{VehicleID: vehicleID, Title: "Chain clean", PerformedOn: time.Now(), Odometer: &odo}
		in.Normalize()
		id, err := s.CreateService(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	odometer := func(id int64) int {
		t.Helper()
		v, err := s.GetVehicle(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.Odometer
	}
	odo := func(v int) *int { return &v }

	tests := []struct {
		name string
		// setup returns the vehicle and the service to delete.
		setup func() (vehicleID, serviceID int64)
		want  int
	}{
		{"a typo is undone", func() (int64, int64) {
			v := vehicle(odo(10000))
			return v, service(v, 120000)
		}, 10000},
		{"the newest of two goes back to the older one", func() (int64, int64) {
			v := vehicle(odo(10000))
			service(v, 11000)
			return v, service(v, 12000)
		}, 11000},
		{"a service at the current reading moved nothing", func() (int64, int64) {
			v := vehicle(odo(10000))
			service(v, 9000)
			if err := s.SetOdometer(ctx, v, 12000); err != nil {
				t.Fatal(err)
			}
			return v, service(v, 12000)
		}, 12000},
		{"an older service moved nothing", func() (int64, int64) {
			v := vehicle(odo(10000))
			service(v, 11000)
			return v, service(v, 10500)
		}, 11000},
		{"a reading entered by hand since is kept", func() (int64, int64) {
			v := vehicle(odo(10000))
			svc := service(v, 12000)
			if err := s.SetOdometer(ctx, v, 12500); err != nil {
				t.Fatal(err)
			}
			return v, svc
		}, 12500},
		// Every vehicle has a reading (migration 00004): one with no other
		// service goes back to its purchase reading.
		{"the only service goes back to the purchase reading", func() (int64, int64) {
			v := vehicle(nil)
			return v, service(v, 12000)
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, svc := tt.setup()
			if _, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc)); err != nil {
				t.Fatal(err)
			}
			got := odometer(v)
			if got != tt.want {
				t.Errorf("odometer = %d, want %d", got, tt.want)
			}
		})
	}
}

// A service's parts always point at it: the database refuses to delete a
// service (or the vehicle it cascades from) while it has parts, and every
// store path returns or moves the parts before the service.
func TestServicePartsAlwaysPointAtTheirService(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	k390 := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 4000})
	filter := mustItem(t, s, stock.ItemInput{Name: "Filter", Unit: stock.UnitPiece, Quantity: 2})
	in := stock.ServiceInput{Odometer: ptr(0), VehicleID: k390, Title: "Oil change", PerformedOn: time.Now(),
		Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 1700}, {ItemID: filter, Quantity: 1}}}
	in.Normalize()
	svc, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	onStock := func(wantOil, wantFilter float64) {
		t.Helper()
		if o, f := mustGet(t, s, oil).Quantity, mustGet(t, s, filter).Quantity; o != wantOil || f != wantFilter {
			t.Errorf("oil %v, filter %v; want %v, %v", o, f, wantOil, wantFilter)
		}
		var off int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_movements
			WHERE (kind = 'service') <> (service_record_id IS NOT NULL)`).Scan(&off); err != nil || off != 0 {
			t.Errorf("%d stock changes break the rule (%v)", off, err)
		}
		assertLedger(t, s)
	}
	onStock(2300, 1)

	for _, sql := range []string{`DELETE FROM service_records WHERE id = $1`, `DELETE FROM vehicles WHERE id = (SELECT vehicle_id FROM service_records WHERE id = $1)`} {
		if _, err := pool.Exec(ctx, sql, svc); err == nil || !strings.Contains(err.Error(), "stock_movements_service_record_id_fkey") {
			t.Errorf("%s: %v, want it refused", sql, err)
		}
	}
	var conflict *store.ConflictError
	if _, err := s.DeleteVehicle(ctx, k390, cur(t, s, store.WhatVehicle, k390)); !errors.As(err, &conflict) {
		t.Errorf("delete a vehicle with a service: %v, want a ConflictError", err)
	}

	upd := in
	upd.Parts = []stock.KitLineInput{{ItemID: oil, Quantity: 1500}}
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, upd)); err != nil {
		t.Fatal(err)
	}
	onStock(2500, 2)

	trashID, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err != nil {
		t.Fatal(err)
	}
	onStock(4000, 2)
	mustRestore(t, s, trashID)
	onStock(2500, 2)
	if r, err := s.GetService(ctx, svc); err != nil || len(r.Parts) != 1 {
		t.Errorf("restored service = %+v, %v", r, err)
	}

	trashID, err = s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	onStock(4000, 2)
	// With no services left, the vehicle goes, and then for good.
	vehicleTrash, err := s.DeleteVehicle(ctx, k390, cur(t, s, store.WhatVehicle, k390))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeTrash(ctx, vehicleTrash); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountTrash(ctx); n != 0 {
		t.Errorf("trash = %d, want empty", n)
	}
	onStock(4000, 2)
}

func TestUpdateService(t *testing.T) {
	s, ctx := newStore(t)
	odo := 10000
	vin := stock.VehicleInput{Name: "KTM 390", Odometer: &odo}
	vin.Normalize()
	k390, err := s.CreateVehicle(ctx, anchored(vin))
	if err != nil {
		t.Fatal(err)
	}
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 4000})
	filter := mustItem(t, s, stock.ItemInput{Name: "Filter", Unit: stock.UnitPiece, Quantity: 2})
	ring := mustItem(t, s, stock.ItemInput{Name: "O-ring", Unit: stock.UnitPiece, Quantity: 1})
	typo := 120000
	in := stock.ServiceInput{VehicleID: k390, Title: "Oil service", PerformedOn: time.Now(), Odometer: &typo, LaborCost: ptr(500.0),
		Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 1700}, {ItemID: filter, Quantity: 1}}}
	in.Normalize()
	id, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	// Fix the odometer typo and the labour, use less oil, swap the filter for an O-ring.
	fixed := 12000
	upd := stock.ServiceInput{VehicleID: 999, Title: "Oil service", PerformedOn: time.Now(), Odometer: &fixed, LaborCost: ptr(50.0),
		Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 1500}, {ItemID: ring, Quantity: 1}}}
	upd.Normalize()
	if err := s.UpdateService(ctx, id, serviceV(t, s, id, upd)); err != nil {
		t.Fatal(err)
	}
	for item, want := range map[int64]float64{oil: 2500, filter: 2, ring: 0} {
		if got := mustGet(t, s, item).Quantity; got != want {
			t.Errorf("item %d = %v, want %v", item, got, want)
		}
	}
	if v, _ := s.GetVehicle(ctx, k390); v.Odometer != 12000 {
		t.Errorf("odometer = %d, want 12000", v.Odometer)
	}
	svc, _ := s.GetService(ctx, id)
	if svc.VehicleID != k390 || *svc.LaborCost != 50 || len(svc.Parts) != 2 || svc.Odometer != 12000 {
		t.Errorf("service = %+v", svc)
	}

	// A part that is short leaves the service as it was.
	short := upd
	short.Parts = []stock.KitLineInput{{ItemID: oil, Quantity: 9000}}
	var insufficient *store.InsufficientStockError
	if err := s.UpdateService(ctx, id, serviceV(t, s, id, short)); !errors.As(err, &insufficient) || insufficient.ItemID != oil {
		t.Fatalf("want InsufficientStockError for oil, got %v", err)
	}
	if got := mustGet(t, s, oil).Quantity; got != 2500 {
		t.Errorf("oil after failed update = %v, want 2500", got)
	}

	// Deleting it now puts the odometer back to before the service.
	if _, err := s.DeleteService(ctx, id, cur(t, s, store.WhatService, id)); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetVehicle(ctx, k390); v.Odometer != 10000 {
		t.Errorf("odometer after delete = %d, want 10000", v.Odometer)
	}
	if err := s.UpdateService(ctx, id, serviceV(t, s, id, upd)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update deleted service: want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)
}

func TestServiceReportsEveryShortPart(t *testing.T) {
	s, ctx := newStore(t)
	k390 := mustVehicle(t, s, "KTM 390")
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 500})
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 1})
	filter := mustItem(t, s, stock.ItemInput{Name: "Filter", Unit: stock.UnitPiece, Quantity: 1})
	in := stock.ServiceInput{Odometer: ptr(0), VehicleID: k390, Title: "Service", PerformedOn: time.Now(), Parts: []stock.KitLineInput{
		{ItemID: oil, Quantity: 1700}, {ItemID: filter, Quantity: 1}, {ItemID: pads, Quantity: 2}}}
	in.Normalize()
	_, err := s.CreateService(ctx, in)
	var shorts *store.ShortPartsError
	if !errors.As(err, &shorts) || len(shorts.Parts) != 2 || shorts.Parts[0].ItemID != oil || shorts.Parts[1].ItemID != pads {
		t.Fatalf("want oil and pads short, got %v", err)
	}
	var first *store.InsufficientStockError
	if !errors.As(err, &first) {
		t.Error("errors.As should find an InsufficientStockError")
	}
	if it := mustGet(t, s, filter); it.Quantity != 1 {
		t.Errorf("filter = %v: nothing should be taken", it.Quantity)
	}
	assertLedger(t, s)
}

func TestEditAServiceWhoseKitMoved(t *testing.T) {
	s, ctx := newStore(t)
	a, b := mustVehicle(t, s, "Bike A"), mustVehicle(t, s, "Bike B")
	kit := stock.KitInput{VehicleID: a, Name: "Oil service"}
	kit.Normalize()
	kitID, err := s.CreateKit(ctx, kit)
	if err != nil {
		t.Fatal(err)
	}
	in := stock.ServiceInput{Odometer: ptr(0), VehicleID: a, KitID: &kitID, Title: "Oil service", PerformedOn: time.Now()}
	in.Normalize()
	id, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	kit.VehicleID = b
	if err := s.UpdateKit(ctx, kitID, kitV(t, s, kitID, kit)); err != nil {
		t.Fatal(err)
	}
	in.Notes = "fixed a typo"
	if err := s.UpdateService(ctx, id, serviceV(t, s, id, in)); err != nil {
		t.Errorf("editing the service: %v", err)
	}
}

// Deleting a service gives its parts back, but not past the maximum.
func TestDeletedServiceRespectsTheMaximum(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	bolt := mustItem(t, s, stock.ItemInput{Name: "Bolt", Unit: stock.UnitPiece, Quantity: 5})
	in := stock.ServiceInput{Odometer: ptr(0), VehicleID: v, Title: "Bolts", PerformedOn: time.Now(), Parts: []stock.KitLineInput{{ItemID: bolt, Quantity: 5}}}
	in.Normalize()
	svc, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: bolt, Kind: stock.MoveAdjusted, Delta: stock.MaxQuantity}); err != nil {
		t.Fatal(err)
	}
	// An edit that leaves the parts as they were is fine.
	in.Title = "Bolts, checked"
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, in)); err != nil {
		t.Errorf("title edit near the maximum: %v", err)
	}
	_, err = s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	wantConflict(t, "delete", err, "Bolt would have more than")
	if _, err := s.GetService(ctx, svc); err != nil {
		t.Errorf("service should stay: %v", err)
	}
	assertLedger(t, s)
}

func TestKitNamesAndServiceTitles(t *testing.T) {
	s, ctx := newStore(t)
	names, err := s.KitNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantDefaults := []string{
		"Air Filter",
		"Brake Fluid",
		"Brake Pads (Front)",
		"Brake Pads (Rear)",
		"Cabin Filter",
		"Clutch Fluid",
		"Coolant",
		"Oil Service",
		"Spark Plugs",
		"Valve Check",
	}
	if !slices.Equal(names, wantDefaults) {
		t.Errorf("defaults = %v, want %v", names, wantDefaults)
	}

	// Dynamic suggestions learn from custom kit names and service titles.
	v := mustVehicle(t, s, "KTM 390")
	kitIn := stock.KitInput{VehicleID: v, Name: "Fork seals"}
	kitIn.Normalize()
	if _, err := s.CreateKit(ctx, kitIn); err != nil {
		t.Fatal(err)
	}

	svcIn := stock.ServiceInput{Odometer: ptr(0), VehicleID: v, Title: "Brembo rebuild", PerformedOn: time.Now()}
	svcIn.Normalize()
	if _, err := s.CreateService(ctx, svcIn); err != nil {
		t.Fatal(err)
	}

	after, err := s.KitNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(after, "Fork seals") || !slices.Contains(after, "Brembo rebuild") {
		t.Errorf("expected custom names in %v", after)
	}

	// ServiceTitles returns the same unified list.
	titles, err := s.ServiceTitles(ctx)
	if err != nil || !slices.Equal(titles, after) {
		t.Errorf("ServiceTitles = %v, want %v (err %v)", titles, after, err)
	}

	// User casing overrides default Title Case.
	lowerIn := stock.KitInput{VehicleID: v, Name: "cabin filter"}
	lowerIn.Normalize()
	if _, err := s.CreateKit(ctx, lowerIn); err != nil {
		t.Fatal(err)
	}
	withLower, err := s.KitNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(withLower, "cabin filter") || slices.Contains(withLower, "Cabin Filter") {
		t.Errorf("user casing should override default, got %v", withLower)
	}
}

func TestKitIntermediateStoreFlow(t *testing.T) {
	s, ctx := newStore(t)
	// Under warranty: the full service keeps its own deadline.
	vin := stock.VehicleInput{Name: "KTM 1290", HasWarranty: true, WarrantyEndsOn: ptr(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))}
	vin.Normalize()
	k1290, err := s.CreateVehicle(ctx, anchored(vin))
	if err != nil {
		t.Fatal(err)
	}
	oemKit := mustItem(t, s, stock.ItemInput{Name: "Oil Filter Service Kit", Brand: "OEM", Unit: stock.UnitPiece, Quantity: 2, VehicleIDs: []int64{k1290}})
	hiflo := mustItem(t, s, stock.ItemInput{Name: "HF650", Brand: "Hiflofiltro", Unit: stock.UnitPiece, Quantity: 2, VehicleIDs: []int64{k1290}})
	oring := mustItem(t, s, stock.ItemInput{Name: "Cap O-Ring", Brand: "OEM", Unit: stock.UnitPiece, Quantity: 4, VehicleIDs: []int64{k1290}})
	oil := mustItem(t, s, stock.ItemInput{Name: "10W50", Brand: "Motorex", Unit: stock.UnitML, Quantity: 10000, VehicleIDs: []int64{k1290}})

	kitIn := stock.KitInput{
		VehicleID: k1290, Name: "Oil service",
		IntervalDistance: ptr(4000), IntervalMonths: ptr(12),
		Intermediate: true, // every 2 000 km
		Lines:        []stock.KitLineInput{{ItemID: oemKit, Quantity: 1}, {ItemID: oil, Quantity: 3600}},
		InterLines:   []stock.KitLineInput{{ItemID: hiflo, Quantity: 1}, {ItemID: oring, Quantity: 2}, {ItemID: oil, Quantity: 3600}},
	}
	kitIn.Normalize()
	kitID, err := s.CreateKit(ctx, kitIn)
	if err != nil {
		t.Fatal(err)
	}

	kit, err := s.GetKit(ctx, kitID)
	if err != nil {
		t.Fatal(err)
	}
	if !kit.Intermediate || len(kit.Lines) != 2 || len(kit.InterLines) != 3 {
		t.Fatalf("kit = %+v", kit)
	}
	today := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	if kit.VehicleWarrantyEndsOn == nil || !kit.UnderWarranty(kit.VehicleOdometer, today) {
		t.Fatalf("the kit doesn't see its vehicle's warranty: %v", kit.VehicleWarrantyEndsOn)
	}
	if kit.NextIsIntermediate(kit.VehicleOdometer, today) {
		t.Errorf("want NextIsIntermediate = false before any service")
	}

	// Log Full service at 10,000 km on 2026-01-10.
	fullSvc := stock.ServiceInput{
		VehicleID: k1290, KitID: &kitID, KitTier: stock.TierFull,
		PerformedOn: today, Odometer: ptr(10000), Title: "Oil service",
		Parts: []stock.KitLineInput{{ItemID: oemKit, Quantity: 1}, {ItemID: oil, Quantity: 3600}},
	}
	fullSvc.Normalize()
	fullID, err := s.CreateService(ctx, fullSvc)
	if err != nil {
		t.Fatal(err)
	}

	kit, _ = s.GetKit(ctx, kitID)
	if kit.LastService == nil || kit.LastService.ID != fullID || kit.LastService.Intermediate() {
		t.Fatalf("LastService = %+v", kit.LastService)
	}
	if kit.LastFullService == nil || kit.LastFullService.ID != fullID {
		t.Fatalf("LastFullService = %+v", kit.LastFullService)
	}
	if !kit.NextIsIntermediate(kit.VehicleOdometer, today) {
		t.Errorf("want NextIsIntermediate = true after full service")
	}

	// Log Intermediate service at 12,000 km on 2026-06-10.
	interDate := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	interSvc := stock.ServiceInput{
		VehicleID: k1290, KitID: &kitID, KitTier: stock.TierIntermediate,
		PerformedOn: interDate, Odometer: ptr(12000), Title: "Oil service (Intermediate)",
		Parts: []stock.KitLineInput{{ItemID: hiflo, Quantity: 1}, {ItemID: oring, Quantity: 2}, {ItemID: oil, Quantity: 3600}},
	}
	interSvc.Normalize()
	interID, err := s.CreateService(ctx, interSvc)
	if err != nil {
		t.Fatal(err)
	}

	kit, _ = s.GetKit(ctx, kitID)
	if kit.LastService == nil || kit.LastService.ID != interID || !kit.LastService.Intermediate() {
		t.Fatalf("LastService = %+v", kit.LastService)
	}
	if kit.LastFullService == nil || kit.LastFullService.ID != fullID || kit.LastFullService.Odometer != 10000 {
		t.Fatalf("LastFullService = %+v", kit.LastFullService)
	}
	if !kit.InterSinceFull {
		t.Error("InterSinceFull = false, want true")
	}
	if kit.NextIsIntermediate(kit.VehicleOdometer, interDate) {
		t.Errorf("want NextIsIntermediate = false after intermediate service")
	}
	due := kit.Due(kit.VehicleOdometer, interDate)
	if due.DueOdometer == nil || *due.DueOdometer != 14000 || due.DueDate == nil || !due.DueDate.Equal(time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("due after intermediate = %+v, want 14000 km / 2027-01-10", due)
	}

	// Deleting the intermediate service reverts the schedule back to Intermediate and odometer back to 10,000 km.
	trashID, err := s.DeleteService(ctx, interID, cur(t, s, store.WhatService, interID))
	if err != nil {
		t.Fatal(err)
	}
	kit, _ = s.GetKit(ctx, kitID)
	if !kit.NextIsIntermediate(kit.VehicleOdometer, interDate) || kit.VehicleOdometer != 10000 || kit.InterSinceFull {
		t.Errorf("after deleting intermediate: NextIsIntermediate = %v, odo = %v", kit.NextIsIntermediate(kit.VehicleOdometer, interDate), kit.VehicleOdometer)
	}

	// Restoring from trash restores its tier and the odometer.
	if _, err := s.RestoreTrash(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	kit, _ = s.GetKit(ctx, kitID)
	if kit.NextIsIntermediate(kit.VehicleOdometer, interDate) || kit.VehicleOdometer != 12000 {
		t.Errorf("after restoring intermediate: NextIsIntermediate = %v, odo = %v", kit.NextIsIntermediate(kit.VehicleOdometer, interDate), kit.VehicleOdometer)
	}
	assertLedger(t, s)
}

// A service's tier is checked against its kit only when it changes: an
// intermediate service saves unchanged after its kit dropped its
// intermediate service. A kit has one break-in on a vehicle: a second is
// refused, also from the trash, and a kit moved to another vehicle may
// have its own there; a kit without a Break-in at has none.
func TestServiceTierChecks(t *testing.T) {
	s, ctx := newStore(t)
	v1 := mustVehicle(t, s, "KTM 390")
	v2 := mustVehicle(t, s, "KTM 690")
	kitIn := stock.KitInput{VehicleID: v1, Name: "Oil", IntervalDistance: ptr(6000), Intermediate: true, BreakInAt: ptr(1000)}
	kitIn.Normalize()
	kit, err := s.CreateKit(ctx, kitIn)
	if err != nil {
		t.Fatal(err)
	}
	log := func(v int64, tier stock.KitTier, on time.Time, odo int) (int64, error) {
		in := stock.ServiceInput{VehicleID: v, KitID: &kit, KitTier: tier, Title: "Oil", PerformedOn: on, Odometer: ptr(odo)}
		in.Normalize()
		return s.CreateService(ctx, in)
	}
	day := func(m time.Month, d int) time.Time { return time.Date(2025, m, d, 0, 0, 0, 0, time.UTC) }

	// An intermediate service (after a Full: a break-in kit's first
	// service can't be one), then the kit drops its intermediate service.
	if _, err := log(v1, stock.TierIntermediate, day(2, 1), 2000); !errors.As(err, new(*store.ConflictError)) {
		t.Errorf("an intermediate as the break-in kit's first service: %v, want it refused", err)
	}
	if _, err := log(v1, stock.TierFull, day(2, 1), 2000); err != nil {
		t.Fatal(err)
	}
	inter, err := log(v1, stock.TierIntermediate, day(3, 1), 3000)
	if err != nil {
		t.Fatal(err)
	}
	kitIn.Intermediate = false
	if err := s.UpdateKit(ctx, kit, kitV(t, s, kit, kitIn)); err != nil {
		t.Fatal(err)
	}
	edit := stock.ServiceInput{VehicleID: v1, KitID: &kit, KitTier: stock.TierIntermediate, Title: "Oil", Notes: "typo fixed", PerformedOn: day(3, 1), Odometer: ptr(3000)}
	if err := s.UpdateService(ctx, inter, serviceV(t, s, inter, edit)); err != nil {
		t.Errorf("an unchanged intermediate service: %v, want it saved", err)
	}
	// Made Full and back: refused, the kit has none now.
	edit.KitTier = stock.TierFull
	if err := s.UpdateService(ctx, inter, serviceV(t, s, inter, edit)); err != nil {
		t.Fatal(err)
	}
	edit.KitTier = stock.TierIntermediate
	var conflict *store.ConflictError
	if err := s.UpdateService(ctx, inter, serviceV(t, s, inter, edit)); !errors.As(err, &conflict) || conflict.Field != "kit_id" {
		t.Errorf("made intermediate on a kit without: %v, want it refused", err)
	}

	// One break-in on a vehicle; deleted, another logged, the first can't
	// come back from the trash.
	first, err := log(v1, stock.TierBreakIn, day(1, 20), 900)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log(v1, stock.TierBreakIn, day(1, 21), 950); !errors.As(err, &conflict) || conflict.Field != "kit_tier" {
		t.Errorf("a second break-in: %v, want it refused", err)
	}
	trash, err := s.DeleteService(ctx, first, cur(t, s, store.WhatService, first))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log(v1, stock.TierBreakIn, day(1, 22), 980); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, trash); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "break-in again") {
		t.Errorf("restoring a second break-in: %v, want it refused", err)
	}

	// The kit moved to another vehicle: a break-in of its own there.
	kitIn.VehicleID = v2
	if err := s.UpdateKit(ctx, kit, kitV(t, s, kit, kitIn)); err != nil {
		t.Fatal(err)
	}
	if _, err := log(v2, stock.TierBreakIn, day(4, 1), 400); err != nil {
		t.Errorf("a break-in on the vehicle the kit moved to: %v, want it saved", err)
	}

	// A kit without a Break-in at has no break-in, with a distance or not.
	for _, other := range []stock.KitInput{
		{VehicleID: v1, Name: "Coolant", IntervalMonths: ptr(48)},
		{VehicleID: v1, Name: "Air Filter", IntervalDistance: ptr(15000)},
	} {
		other.Normalize()
		id, err := s.CreateKit(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		in := stock.ServiceInput{VehicleID: v1, KitID: &id, KitTier: stock.TierBreakIn, Title: other.Name, PerformedOn: day(5, 1), Odometer: ptr(5000)}
		in.Normalize()
		if _, err := s.CreateService(ctx, in); !errors.As(err, &conflict) || conflict.Field != "kit_tier" || conflict.Message != "This kit has no break-in on this vehicle" {
			t.Errorf("a break-in on %s without a Break-in at: %v, want it refused", other.Name, err)
		}
	}
}

// A kit's Break-in at is above its vehicle's purchase reading: a break-in
// is a new vehicle's, and Pitbox tracks the work since it was bought. A kit
// save refuses one at or below it (also one the kit already holds, saved
// before the check, which a break-in can't be logged with either), moving a
// kit to a vehicle bought past it is refused, and so is raising a
// vehicle's purchase reading past a kit's.
func TestBreakInAtAboveThePurchase(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	usedIn := stock.VehicleInput{Name: "LittleJoy", Odometer: ptr(2500), OdometerAtPurchase: ptr(2148)}
	usedIn.Normalize()
	used, err := s.CreateVehicle(ctx, anchored(usedIn))
	if err != nil {
		t.Fatal(err)
	}
	newBike := mustVehicle(t, s, "GreyJoy") // bought at 0
	var conflict *store.ConflictError
	for _, at := range []int{1000, 2148} {
		in := stock.KitInput{VehicleID: used, Name: "Oil", IntervalDistance: ptr(8000), BreakInAt: ptr(at)}
		in.Normalize()
		if _, err := s.CreateKit(ctx, in); !errors.As(err, &conflict) || conflict.Field != "break_in_at" ||
			conflict.Message != "LittleJoy was bought at 2\u202f148 km, past its break-in" {
			t.Errorf("Break-in at %d on a vehicle bought at 2 148: %v, want it refused", at, err)
		}
	}
	in := stock.KitInput{VehicleID: used, Name: "Oil", IntervalDistance: ptr(8000), BreakInAt: ptr(3000)}
	in.Normalize()
	if _, err := s.CreateKit(ctx, in); err != nil {
		t.Errorf("Break-in at 3 000 on a vehicle bought at 2 148: %v, want it saved", err)
	}

	// A value saved before the check: no break-in with it, and the kit's
	// next save asks to clear it.
	in = stock.KitInput{VehicleID: used, Name: "Chain", IntervalDistance: ptr(8000)}
	in.Normalize()
	kit, err := s.CreateKit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE service_kits SET break_in_at = 1000 WHERE id = $1`, kit); err != nil {
		t.Fatal(err)
	}
	svc := stock.ServiceInput{VehicleID: used, KitID: &kit, KitTier: stock.TierBreakIn, Title: "Chain", PerformedOn: time.Now(), Odometer: ptr(2500)}
	svc.Normalize()
	if _, err := s.CreateService(ctx, svc); !errors.As(err, &conflict) || conflict.Message != "This kit has no break-in on this vehicle" {
		t.Errorf("a break-in with a Break-in at below the purchase: %v, want it refused", err)
	}
	in.BreakInAt = ptr(1000)
	if err := s.UpdateKit(ctx, kit, kitV(t, s, kit, in)); !errors.As(err, &conflict) || conflict.Field != "break_in_at" {
		t.Errorf("saving the kit as it is: %v, want it refused", err)
	}
	in.BreakInAt = nil
	if err := s.UpdateKit(ctx, kit, kitV(t, s, kit, in)); err != nil {
		t.Errorf("saving the kit with it cleared: %v", err)
	}

	// A new bike's kit with a break-in can't move to the used one, and the
	// new bike's purchase reading can't go past it.
	in = stock.KitInput{VehicleID: newBike, Name: "Oil", IntervalDistance: ptr(8000), BreakInAt: ptr(1000)}
	in.Normalize()
	oil, err := s.CreateKit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.VehicleID = used
	if err := s.UpdateKit(ctx, oil, kitV(t, s, oil, in)); !errors.As(err, &conflict) || conflict.Field != "break_in_at" {
		t.Errorf("moving it to a vehicle bought past it: %v, want it refused", err)
	}
	if err := s.SetOdometer(ctx, newBike, 5000); err != nil {
		t.Fatal(err)
	}
	bike := stock.VehicleInput{Name: "GreyJoy", OdometerAtPurchase: ptr(1000)}
	bike.Normalize()
	if err := s.UpdateVehicle(ctx, newBike, anchored(vehicleV(t, s, newBike, bike))); !errors.As(err, &conflict) ||
		conflict.Field != "odometer_at_purchase" || conflict.Message != "Its kit Oil has a break-in at 1\u202f000 km: clear it there first" {
		t.Errorf("purchase reading at the kit's break-in: %v, want it refused", err)
	}
	bike.OdometerAtPurchase = ptr(999)
	if err := s.UpdateVehicle(ctx, newBike, anchored(vehicleV(t, s, newBike, bike))); err != nil {
		t.Errorf("purchase reading below the kit's break-in: %v, want it saved", err)
	}
}
