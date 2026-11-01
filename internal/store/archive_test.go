package store_test

import (
	"context"
	"errors"
	"testing"

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
