package store_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

// Archiving and restoring an item, vehicle or kit: a stale version is
// refused, one deleted since is gone (in the trash), and a change sets
// updated_at, which "recently changed" sorts by.
func TestArchiveAndRestoreAnswers(t *testing.T) {
	type calls struct {
		what, table     string
		make            func(t *testing.T, s *store.Store) int64
		archive, revive func(s *store.Store, id int64, version int) error
		del             func(s *store.Store, id int64, version int) error
	}
	ctx := context.Background()
	for _, tc := range []calls{
		{
			what: store.WhatItem, table: "items",
			make: func(t *testing.T, s *store.Store) int64 {
				return mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
			},
			archive: func(s *store.Store, id int64, v int) error { _, err := s.ArchiveItem(ctx, id, v); return err },
			revive:  func(s *store.Store, id int64, v int) error { return s.RestoreItem(ctx, id, v) },
			del:     func(s *store.Store, id int64, v int) error { _, err := s.DeleteItem(ctx, id, v); return err },
		},
		{
			what: store.WhatVehicle, table: "vehicles",
			make:    func(t *testing.T, s *store.Store) int64 { return mustVehicle(t, s, "KTM 390") },
			archive: func(s *store.Store, id int64, v int) error { return s.ArchiveVehicle(ctx, id, v) },
			revive:  func(s *store.Store, id int64, v int) error { return s.RestoreVehicle(ctx, id, v) },
			del:     func(s *store.Store, id int64, v int) error { _, err := s.DeleteVehicle(ctx, id, v); return err },
		},
		{
			what: store.WhatKit, table: "service_kits",
			make: func(t *testing.T, s *store.Store) int64 {
				in := stock.KitInput{VehicleID: mustVehicle(t, s, "KTM 390"), Name: "Oil service"}
				in.Normalize()
				id, err := s.CreateKit(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				return id
			},
			archive: func(s *store.Store, id int64, v int) error { return s.ArchiveKit(ctx, id, v) },
			revive:  func(s *store.Store, id int64, v int) error { return s.RestoreKit(ctx, id, v) },
			del:     func(s *store.Store, id int64, v int) error { _, err := s.DeleteKit(ctx, id, v); return err },
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			pool := testdb.New(t)
			s := store.New(pool)
			id := tc.make(t, s)
			// age makes the record look last changed yesterday; changed says
			// whether it was changed since.
			age := func() {
				t.Helper()
				if _, err := pool.Exec(ctx, `UPDATE `+tc.table+` SET updated_at = now() - interval '1 day' WHERE id = $1`, id); err != nil {
					t.Fatal(err)
				}
			}
			changed := func() bool {
				t.Helper()
				var recent bool
				if err := pool.QueryRow(ctx, `SELECT updated_at > now() - interval '1 hour' FROM `+tc.table+` WHERE id = $1`, id).
					Scan(&recent); err != nil {
					t.Fatal(err)
				}
				return recent
			}
			stale := func(name string, err error, gone bool) {
				t.Helper()
				var se *store.StaleError
				if !errors.As(err, &se) || se.Gone != gone || se.InTrash != gone || se.What != tc.what {
					t.Errorf("%s: got %v (%+v), want stale, gone %v", name, err, se, gone)
				}
			}

			for _, step := range []struct {
				name string
				call func(*store.Store, int64, int) error
			}{{"archive", tc.archive}, {"restore", tc.revive}} {
				age()
				v := cur(t, s, tc.what, id)
				stale(step.name+" with an old version", step.call(s, id, v-1), false)
				stale(step.name+" without a version", step.call(s, id, 0), false)
				if changed() {
					t.Errorf("%s: a stale %s changed the record", tc.what, step.name)
				}
				if err := step.call(s, id, v); err != nil {
					t.Fatalf("%s: %v", step.name, err)
				}
				if !changed() {
					t.Errorf("%s: updated_at not set", step.name)
				}
			}

			v := cur(t, s, tc.what, id)
			if err := tc.del(s, id, v); err != nil {
				t.Fatal(err)
			}
			stale("archive after the delete", tc.archive(s, id, v), true)
			stale("restore after the delete", tc.revive(s, id, v), true)
		})
	}
}

func TestArchiveInsteadOfDelete(t *testing.T) {
	s, ctx := newStore(t)
	k390 := mustVehicle(t, s, "KTM 390")
	filter := mustItem(t, s, stock.ItemInput{Name: "Old filter", Unit: stock.UnitPiece, Quantity: 3})
	spare := mustItem(t, s, stock.ItemInput{Name: "Spare bolt", Unit: stock.UnitPiece, Quantity: 3})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: filter, Kind: stock.MovePurchase, Delta: 2, TotalPrice: ptr(20.0)}); err != nil {
		t.Fatal(err)
	}
	kitIn := stock.KitInput{VehicleID: k390, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: filter, Quantity: 1}}}
	kitIn.Normalize()
	kitID, err := s.CreateKit(ctx, kitIn)
	if err != nil {
		t.Fatal(err)
	}
	svcIn := stock.ServiceInput{VehicleID: k390, Title: "Oil service", PerformedOn: time.Now(), Parts: []stock.KitLineInput{{ItemID: filter, Quantity: 1}}}
	svcIn.Normalize()
	svcID, err := s.CreateService(ctx, svcIn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &filter}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetStats(ctx, time.Now())

	u, err := s.ItemUsage(ctx, filter)
	if err != nil || u != (stock.ItemUsage{Purchases: 1, Services: 1, Kits: 1}) {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	var conflict *store.ConflictError
	if _, err := s.DeleteItem(ctx, filter, cur(t, s, store.WhatItem, filter)); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "Archive it instead") {
		t.Fatalf("delete used item: want ConflictError, got %v", err)
	}

	listTrash, err := s.ArchiveItem(ctx, filter, cur(t, s, store.WhatItem, filter))
	if err != nil || listTrash == 0 {
		t.Fatalf("archive = %v, %v; want it taken off the shopping list", listTrash, err)
	}
	if got := ids(mustList(t, s, store.ItemFilter{})); !slices.Equal(got, []string{"Spare bolt"}) {
		t.Errorf("active items = %v", got)
	}
	if got := ids(mustList(t, s, store.ItemFilter{Archived: store.ArchivedOnly})); !slices.Equal(got, []string{"Old filter"}) {
		t.Errorf("archived items = %v", got)
	}
	if it := mustGet(t, s, filter); !it.Archived() {
		t.Error("GetItem should find the archived item")
	}
	if list, _ := s.ListShopping(ctx); len(list) != 0 {
		t.Errorf("shopping = %+v", list)
	}

	// Everything that referred to it is unchanged.
	if svc, _ := s.GetService(ctx, svcID); svc.PartCount != 1 || len(svc.Parts) != 1 {
		t.Errorf("service parts = %+v", svc.Parts)
	}
	if kit, _ := s.GetKit(ctx, kitID); len(kit.Lines) != 1 || !kit.Lines[0].Archived {
		t.Errorf("kit lines = %+v", kit.Lines)
	}
	after, _ := s.GetStats(ctx, time.Now())
	if after.Spent12m != before.Spent12m || after.StockValue != 0 || before.StockValue != 40 {
		t.Errorf("stats before %+v, after %+v: spending kept, archived stock not valued", before, after)
	}

	if err := s.RestoreItem(ctx, filter, cur(t, s, store.WhatItem, filter)); err != nil {
		t.Fatal(err)
	}
	if got := ids(mustList(t, s, store.ItemFilter{})); len(got) != 2 {
		t.Errorf("after restore = %v", got)
	}

	// An item nothing refers to is deleted with its own history.
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: spare, Kind: stock.MoveUse, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteItem(ctx, spare, cur(t, s, store.WhatItem, spare)); err != nil {
		t.Fatalf("delete unused item: %v", err)
	}
	if err := s.RestoreItem(ctx, spare, cur(t, s, store.WhatItem, spare)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("restore deleted: want ErrNotFound, got %v", err)
	}
	if _, err := s.ArchiveItem(ctx, spare, cur(t, s, store.WhatItem, spare)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("archive deleted: want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)
}

func TestArchiveVehicleInsteadOfDelete(t *testing.T) {
	s, ctx := newStore(t)
	old, spare := mustVehicle(t, s, "Old bike"), mustVehicle(t, s, "Spare")
	in := stock.ServiceInput{VehicleID: old, Title: "Chain", PerformedOn: time.Now(), LaborCost: ptr(80.0)}
	in.Normalize()
	if _, err := s.CreateService(ctx, in); err != nil {
		t.Fatal(err)
	}
	kit := stock.KitInput{VehicleID: old, Name: "Oil service", IntervalMonths: ptr(12)}
	kit.Normalize()
	if _, err := s.CreateKit(ctx, kit); err != nil {
		t.Fatal(err)
	}
	var conflict *store.ConflictError
	if _, err := s.DeleteVehicle(ctx, old, cur(t, s, store.WhatVehicle, old)); !errors.As(err, &conflict) {
		t.Fatalf("delete with history: want ConflictError, got %v", err)
	}
	if svc, kits, _ := s.VehicleUsage(ctx, old); svc != 1 || kits != 1 {
		t.Errorf("usage = %d services, %d kits", svc, kits)
	}
	if err := s.ArchiveVehicle(ctx, old, cur(t, s, store.WhatVehicle, old)); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetVehicle(ctx, old); !v.Archived() {
		t.Error("vehicle should be archived")
	}
	if kits, _ := s.ListKits(ctx, store.KitFilter{}); len(kits) != 0 {
		t.Errorf("kits of archived vehicles should be left out: %+v", kits)
	}
	if kits, _ := s.ListKits(ctx, store.KitFilter{VehicleID: old}); len(kits) != 1 {
		t.Errorf("the vehicle's own kits = %+v", kits)
	}
	if st, _ := s.GetStats(ctx, time.Now()); st.Spent12m != 80 {
		t.Errorf("labour of an archived vehicle still counts: %+v", st)
	}
	if err := s.RestoreVehicle(ctx, old, cur(t, s, store.WhatVehicle, old)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteVehicle(ctx, spare, cur(t, s, store.WhatVehicle, spare)); err != nil {
		t.Errorf("delete without history: %v", err)
	}
}

func TestArchiveKitWithServices(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	kit := stock.KitInput{VehicleID: v, Name: "Oil service", IntervalMonths: ptr(12)}
	kit.Normalize()
	used, err := s.CreateKit(ctx, kit)
	if err != nil {
		t.Fatal(err)
	}
	kit.Name = "Never used"
	unused, err := s.CreateKit(ctx, kit)
	if err != nil {
		t.Fatal(err)
	}
	in := stock.ServiceInput{VehicleID: v, KitID: &used, Title: "Oil service", PerformedOn: time.Now()}
	in.Normalize()
	svc, err := s.CreateService(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var conflict *store.ConflictError
	if _, err := s.DeleteKit(ctx, used, cur(t, s, store.WhatKit, used)); !errors.As(err, &conflict) {
		t.Fatalf("delete a used kit: want ConflictError, got %v", err)
	}
	if n, _ := s.KitServices(ctx, used); n != 1 {
		t.Errorf("services = %d", n)
	}
	if err := s.ArchiveKit(ctx, used, cur(t, s, store.WhatKit, used)); err != nil {
		t.Fatal(err)
	}
	if kits, _ := s.ListKits(ctx, store.KitFilter{}); len(kits) != 1 || kits[0].ID != unused {
		t.Errorf("active kits = %+v", kits)
	}
	if kits, _ := s.ListKits(ctx, store.KitFilter{VehicleID: v}); len(kits) != 1 {
		t.Errorf("a vehicle's active kits = %+v", kits)
	}
	k, _ := s.GetKit(ctx, used)
	if !k.Archived() || k.Due(nil, time.Now()).State != stock.DueNone {
		t.Errorf("archived kit = %+v", k)
	}
	if r, _ := s.GetService(ctx, svc); r.KitID == nil || *r.KitID != used {
		t.Errorf("service lost its kit: %+v", r)
	}
	if err := s.RestoreKit(ctx, used, cur(t, s, store.WhatKit, used)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteKit(ctx, unused, cur(t, s, store.WhatKit, unused)); err != nil {
		t.Errorf("delete an unused kit: %v", err)
	}
}

func wantConflict(t *testing.T, what string, err error, contains string) {
	t.Helper()
	var conflict *store.ConflictError
	if !errors.As(err, &conflict) || !strings.Contains(conflict.Message, contains) {
		t.Errorf("%s: want ConflictError %q, got %v", what, contains, err)
	}
}

// The store itself refuses stock changes to an archived item, not just
// the pages: they would change stock nothing shows.
func TestArchivedItemsTakeNoStockChanges(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	filter := mustItem(t, s, stock.ItemInput{Name: "Filter", Unit: stock.UnitPiece, Quantity: 5})
	bolt := mustItem(t, s, stock.ItemInput{Name: "Bolt", Unit: stock.UnitPiece, Quantity: 5})
	service := func(qty float64) stock.ServiceInput {
		in := stock.ServiceInput{VehicleID: v, Title: "Oil service", PerformedOn: time.Now(),
			Parts: []stock.KitLineInput{{ItemID: filter, Quantity: qty}, {ItemID: bolt, Quantity: 1}}}
		in.Normalize()
		return in
	}
	svc, err := s.CreateService(ctx, service(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: filter, Kind: stock.MoveUse, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: filter, Kind: stock.MoveUse})
	use := moves[0].ID
	_, useTrash, err := s.DeleteMovement(ctx, use, cur(t, s, store.WhatMovement, use))
	if err != nil {
		t.Fatal(err)
	}
	serviceTrash, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err != nil {
		t.Fatal(err)
	}
	svc, err = s.CreateService(ctx, service(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: filter, Kind: stock.MoveUse, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	moves, _ = s.ListMovements(ctx, store.MovementFilter{ItemID: filter, Kind: stock.MoveUse})
	use = moves[0].ID
	if _, err := s.ArchiveItem(ctx, filter, cur(t, s, store.WhatItem, filter)); err != nil {
		t.Fatal(err)
	}
	before := mustGet(t, s, filter).Quantity

	_, _, delErr := s.DeleteMovement(ctx, use, cur(t, s, store.WhatMovement, use))
	_, restoreUseErr := s.RestoreTrash(ctx, useTrash)
	_, newSvcErr := s.CreateService(ctx, service(1))
	_, correctErr := s.CorrectStock(ctx, filter, 10, "", time.Now(), "")
	for what, err := range map[string]error{
		"add":             s.AddMovement(ctx, stock.MovementInput{ItemID: filter, Kind: stock.MovePurchase, Delta: 1}),
		"correct":         correctErr,
		"edit a change":   s.UpdateMovement(ctx, use, editV(t, s, use, stock.MovementEdit{Amount: 2})),
		"delete a change": delErr,
		"restore change":  restoreUseErr,
		"new service":     newSvcErr,
		"more in service": s.UpdateService(ctx, svc, serviceV(t, s, svc, service(2))),
	} {
		wantConflict(t, what, err, "Filter is archived")
	}
	if got := mustGet(t, s, filter).Quantity; got != before {
		t.Errorf("archived stock changed: %v, want %v", got, before)
	}

	// A deleted service comes back with its parts, archived or not, as its
	// delete gave them back: like an edit, it may keep them as they were.
	if _, err := s.RestoreTrash(ctx, serviceTrash); err != nil {
		t.Errorf("restore a service whose part was archived since: %v", err)
	}
	if got := mustGet(t, s, filter).Quantity; got != before-1 {
		t.Errorf("after restoring the service: %v, want %v", got, before-1)
	}
	before = mustGet(t, s, filter).Quantity

	// Editing the service otherwise still works, archived part and all.
	edit := service(1)
	edit.Title = "Oil service, checked bolts"
	edit.Parts[1].Quantity = 2
	if err := s.UpdateService(ctx, svc, serviceV(t, s, svc, edit)); err != nil {
		t.Fatalf("edit keeping the archived part: %v", err)
	}
	if got := mustGet(t, s, filter).Quantity; got != before {
		t.Errorf("archived stock changed by the edit: %v, want %v", got, before)
	}
	assertLedger(t, s)
}

// Nothing new is logged for an archived vehicle or with an archived kit.
func TestArchivedVehiclesAndKitsTakeNothingNew(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	other := mustVehicle(t, s, "Duke")
	kin := stock.KitInput{VehicleID: v, Name: "Chain"}
	kin.Normalize()
	kit, _ := s.CreateKit(ctx, kin)
	old, _ := s.CreateKit(ctx, kin)
	oldTrash, err := s.DeleteKit(ctx, old, cur(t, s, store.WhatKit, old))
	if err != nil {
		t.Fatal(err)
	}
	svcIn := stock.ServiceInput{VehicleID: v, Title: "Chain clean", PerformedOn: time.Now()}
	svcIn.Normalize()
	svc, _ := s.CreateService(ctx, svcIn)
	svcTrash, err := s.DeleteService(ctx, svc, cur(t, s, store.WhatService, svc))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ArchiveKit(ctx, kit, cur(t, s, store.WhatKit, kit)); err != nil {
		t.Fatal(err)
	}
	withKit := svcIn
	withKit.KitID = &kit
	_, err = s.CreateService(ctx, withKit)
	wantConflict(t, "service with an archived kit", err, "kit is archived")

	if err := s.ArchiveVehicle(ctx, v, cur(t, s, store.WhatVehicle, v)); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateService(ctx, svcIn)
	wantConflict(t, "service", err, "KTM 390 is archived")
	_, err = s.CreateKit(ctx, kin)
	wantConflict(t, "kit", err, "KTM 390 is archived")
	// Bringing back a past service or kit isn't new work: Undo still works.
	mustRestore(t, s, svcTrash)
	mustRestore(t, s, oldTrash)
	moved := stock.KitInput{VehicleID: v, Name: "Brakes"}
	moved.Normalize()
	otherKit := stock.KitInput{VehicleID: other, Name: "Brakes"}
	otherKit.Normalize()
	brakes, _ := s.CreateKit(ctx, otherKit)
	wantConflict(t, "move a kit to it", s.UpdateKit(ctx, brakes, kitV(t, s, brakes, moved)), "KTM 390 is archived")

	// A kit staying with its archived vehicle can still be edited.
	kin.Name = "Chain kit"
	if err := s.UpdateKit(ctx, kit, kitV(t, s, kit, kin)); err != nil {
		t.Errorf("edit a kit of an archived vehicle: %v", err)
	}
	if err := s.RestoreVehicle(ctx, v, cur(t, s, store.WhatVehicle, v)); err != nil {
		t.Fatal(err)
	}
}
