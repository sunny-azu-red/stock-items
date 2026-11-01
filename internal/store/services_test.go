package store_test

import (
	"context"
	"errors"
	"fmt"
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

	kitIn := stock.KitInput{VehicleID: k390, Name: "Oil service", IntervalKm: ptr(7500), IntervalMonths: ptr(12),
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
	if r := kit.Readiness(); r.Lines != 2 || r.ShortLines != 1 || kit.VehicleName != "KTM 390" || kit.LastService != nil {
		t.Errorf("kit = %+v readiness %+v", kit, r)
	}
	if got := kit.Due(nil, time.Now()).State; got != stock.DueNever {
		t.Errorf("due state = %s", got)
	}

	// Not enough oil: the whole service must roll back.
	svc := stock.ServiceInput{VehicleID: k390, KitID: &kitID, PerformedOn: time.Now(), OdometerKm: ptr(15000), Title: "Oil service",
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
	if v.OdometerKm == nil || *v.OdometerKm != 15000 {
		t.Errorf("odometer = %v", v.OdometerKm)
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
	if got := kit.Due(v.OdometerKm, time.Now()).State; got != stock.DueOK {
		t.Errorf("due after service = %s", got)
	}

	// A lower odometer reading never moves the odometer backwards.
	older := stock.ServiceInput{VehicleID: k390, PerformedOn: time.Now().AddDate(-1, 0, 0), OdometerKm: ptr(9000), Title: "Chain"}
	if _, err := s.CreateService(ctx, older); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetVehicle(ctx, k390)
	if *v.OdometerKm != 15000 {
		t.Errorf("odometer moved backwards to %d", *v.OdometerKm)
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
	in := stock.KitInput{VehicleID: v1, Name: "Oil", IntervalKm: ptr(6000)}
	kit, err := s.CreateKit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v1, KitID: &kit, Title: "Oil", PerformedOn: time.Now(), OdometerKm: ptr(20000)}); err != nil {
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
	vehicle := func(km *int) int64 {
		t.Helper()
		n++
		in := stock.VehicleInput{Name: fmt.Sprintf("Bike %d", n), OdometerKm: km}
		in.Normalize()
		id, err := s.CreateVehicle(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	service := func(vehicleID int64, km int) int64 {
		t.Helper()
		in := stock.ServiceInput{VehicleID: vehicleID, Title: "Chain clean", PerformedOn: time.Now(), OdometerKm: &km}
		in.Normalize()
		id, err := s.CreateService(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	odometer := func(id int64) *int {
		t.Helper()
		v, err := s.GetVehicle(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.OdometerKm
	}
	km := func(v int) *int { return &v }

	tests := []struct {
		name string
		// setup returns the vehicle and the service to delete.
		setup func() (vehicleID, serviceID int64)
		want  *int
	}{
		{"a typo is undone", func() (int64, int64) {
			v := vehicle(km(10000))
			return v, service(v, 120000)
		}, km(10000)},
		{"the newest of two goes back to the older one", func() (int64, int64) {
			v := vehicle(km(10000))
			service(v, 11000)
			return v, service(v, 12000)
		}, km(11000)},
		{"a service at the current reading moved nothing", func() (int64, int64) {
			v := vehicle(km(10000))
			service(v, 9000)
			if err := s.SetOdometer(ctx, v, 12000); err != nil {
				t.Fatal(err)
			}
			return v, service(v, 12000)
		}, km(12000)},
		{"an older service moved nothing", func() (int64, int64) {
			v := vehicle(km(10000))
			service(v, 11000)
			return v, service(v, 10500)
		}, km(11000)},
		{"a reading entered by hand since is kept", func() (int64, int64) {
			v := vehicle(km(10000))
			svc := service(v, 12000)
			if err := s.SetOdometer(ctx, v, 12500); err != nil {
				t.Fatal(err)
			}
			return v, svc
		}, km(12500)},
		{"a vehicle without a reading gets none back", func() (int64, int64) {
			v := vehicle(nil)
			return v, service(v, 12000)
		}, nil},
		{"services from before the change are left alone", func() (int64, int64) {
			v := vehicle(km(10000))
			svc := service(v, 12000)
			if _, err := pool.Exec(ctx, `UPDATE service_records SET odometer_moved = NULL, odometer_before = NULL WHERE id = $1`, svc); err != nil {
				t.Fatal(err)
			}
			return v, svc
		}, km(12000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, svc := tt.setup()
			if _, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc)); err != nil {
				t.Fatal(err)
			}
			got := odometer(v)
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("odometer = %v, want %v", deref(got), deref(tt.want))
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
	in := stock.ServiceInput{VehicleID: k390, Title: "Oil change", PerformedOn: time.Now(),
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
	km := 10000
	vin := stock.VehicleInput{Name: "KTM 390", OdometerKm: &km}
	vin.Normalize()
	k390, err := s.CreateVehicle(ctx, vin)
	if err != nil {
		t.Fatal(err)
	}
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 4000})
	filter := mustItem(t, s, stock.ItemInput{Name: "Filter", Unit: stock.UnitPiece, Quantity: 2})
	ring := mustItem(t, s, stock.ItemInput{Name: "O-ring", Unit: stock.UnitPiece, Quantity: 1})
	typo := 120000
	in := stock.ServiceInput{VehicleID: k390, Title: "Oil service", PerformedOn: time.Now(), OdometerKm: &typo, LaborCost: ptr(500.0),
		Parts: []stock.KitLineInput{{ItemID: oil, Quantity: 1700}, {ItemID: filter, Quantity: 1}}}
	in.Normalize()
	id, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	// Fix the odometer typo and the labour, use less oil, swap the filter for an O-ring.
	fixed := 12000
	upd := stock.ServiceInput{VehicleID: 999, Title: "Oil service", PerformedOn: time.Now(), OdometerKm: &fixed, LaborCost: ptr(50.0),
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
	if v, _ := s.GetVehicle(ctx, k390); *v.OdometerKm != 12000 {
		t.Errorf("odometer = %d, want 12000", *v.OdometerKm)
	}
	svc, _ := s.GetService(ctx, id)
	if svc.VehicleID != k390 || *svc.LaborCost != 50 || len(svc.Parts) != 2 || *svc.OdometerKm != 12000 {
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
	if v, _ := s.GetVehicle(ctx, k390); *v.OdometerKm != 10000 {
		t.Errorf("odometer after delete = %d, want 10000", *v.OdometerKm)
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
	in := stock.ServiceInput{VehicleID: k390, Title: "Service", PerformedOn: time.Now(), Parts: []stock.KitLineInput{
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
	in := stock.ServiceInput{VehicleID: a, KitID: &kitID, Title: "Oil service", PerformedOn: time.Now()}
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

func TestKitCountsFromTheOdometerWhenAServiceHasNoReading(t *testing.T) {
	s, ctx := newStore(t)
	km := 10000
	vin := stock.VehicleInput{Name: "KTM 390", OdometerKm: &km}
	vin.Normalize()
	v, _ := s.CreateVehicle(ctx, vin)
	kin := stock.KitInput{VehicleID: v, Name: "Oil service", IntervalKm: ptr(6000)}
	kin.Normalize()
	kit, err := s.CreateKit(ctx, kin)
	if err != nil {
		t.Fatal(err)
	}
	logService := func(on time.Time, reading *int) int64 {
		t.Helper()
		in := stock.ServiceInput{VehicleID: v, KitID: &kit, Title: "Oil service", PerformedOn: on, OdometerKm: reading}
		in.Normalize()
		id, err := s.CreateService(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	logService(time.Now().AddDate(0, -5, 0), ptr(10000))
	if err := s.SetOdometer(ctx, v, 16000); err != nil {
		t.Fatal(err)
	}
	svc := logService(time.Now(), nil)
	k, err := s.GetKit(ctx, kit)
	if err != nil {
		t.Fatal(err)
	}
	if k.LastService == nil || k.LastService.OdometerAtLog == nil || *k.LastService.OdometerAtLog != 16000 {
		t.Fatalf("last service = %+v", k.LastService)
	}
	if d := k.Due(ptr(16100), time.Now()); d.State != stock.DueOK || *d.DueKm != 22000 {
		t.Errorf("due = %+v, want ok at 22 000 km", d)
	}

	// One logged long after it was done says nothing about where it was
	// done: km count from the last reading again.
	back := logService(time.Now().AddDate(0, 0, 1).AddDate(0, -1, 0), nil)
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, stock.ServiceInput{VehicleID: v, KitID: &kit, Title: "Oil service",
		PerformedOn: time.Now().AddDate(0, -2, 0)})); err != nil {
		t.Fatal(err)
	}
	if k, _ = s.GetKit(ctx, kit); k.LastService.ID != back || k.LastService.OdometerAtLog != nil {
		t.Errorf("backdated: last service = %+v, want %d without a reading at log", k.LastService, back)
	}
	if _, err := s.DeleteService(ctx, back, cur(t, s, store.WhatService, back)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, stock.ServiceInput{VehicleID: v, KitID: &kit, Title: "Oil service", PerformedOn: time.Now()})); err != nil {
		t.Fatal(err)
	}

	// Editing the service later keeps the reading it was logged at, and a
	// reading entered on it wins.
	if err := s.SetOdometer(ctx, v, 17000); err != nil {
		t.Fatal(err)
	}
	edit := stock.ServiceInput{VehicleID: v, KitID: &kit, Title: "Oil service, filter too", PerformedOn: time.Now()}
	edit.Normalize()
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, edit)); err != nil {
		t.Fatal(err)
	}
	if k, _ = s.GetKit(ctx, kit); *k.LastService.OdometerAtLog != 16000 {
		t.Errorf("after edit: at log = %d, want 16000", *k.LastService.OdometerAtLog)
	}
	edit.OdometerKm = ptr(16500)
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, edit)); err != nil {
		t.Fatal(err)
	}
	if k, _ = s.GetKit(ctx, kit); *k.LastService.OdometerKm != 16500 || k.Due(ptr(17000), time.Now()).DueKm == nil ||
		*k.Due(ptr(17000), time.Now()).DueKm != 16500+6000 {
		t.Errorf("with a reading: %+v, due %+v", k.LastService, k.Due(ptr(17000), time.Now()))
	}
	// Removing the reading again counts from the odometer at log once more.
	edit.OdometerKm = nil
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, edit)); err != nil {
		t.Fatal(err)
	}
	if k, _ = s.GetKit(ctx, kit); k.LastService.OdometerAtLog == nil || *k.LastService.OdometerAtLog != 16000 {
		t.Errorf("reading removed: %+v, want at log 16000", k.LastService)
	}
}

// Deleting a service gives its parts back, but not past the maximum.
func TestDeletedServiceRespectsTheMaximum(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	bolt := mustItem(t, s, stock.ItemInput{Name: "Bolt", Unit: stock.UnitPiece, Quantity: 5})
	in := stock.ServiceInput{VehicleID: v, Title: "Bolts", PerformedOn: time.Now(), Parts: []stock.KitLineInput{{ItemID: bolt, Quantity: 5}}}
	in.Normalize()
	svc, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: bolt, Kind: stock.MoveCorrection, Delta: stock.MaxQuantity}); err != nil {
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
