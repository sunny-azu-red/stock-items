package store_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// TestSpellings: text that differs from a spelling in use only in case is
// saved in that spelling, the one its suggestion list shows; the record
// being saved and built-in defaults don't count.
func TestSpellings(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	col := func(t *testing.T, sql string, id int64) string {
		t.Helper()
		var v string
		if err := pool.QueryRow(ctx, sql, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	item := func(t *testing.T, in stock.ItemInput) int64 {
		t.Helper()
		in.Unit = stock.UnitPiece
		return mustItem(t, s, in)
	}

	t.Run("an item's brand, category, location and SKU brands", func(t *testing.T) {
		item(t, stock.ItemInput{Name: "Oil Filter", Brand: "KTM", Category: "Filters", Location: "Garage shelf 2"})
		id := item(t, stock.ItemInput{Name: "Air Filter", Brand: "ktm", Category: "FILTERS", Location: "garage Shelf 2",
			SKUs: []stock.SKU{{Code: "A1", Kind: stock.SKUOEM, Brand: "ktM"}, {Code: "A2", Kind: stock.SKUOEM, Brand: "motorex"}}})
		got := col(t, `SELECT brand || '|' || category || '|' || location FROM items WHERE id = $1`, id)
		if got != "KTM|Filters|Garage shelf 2" {
			t.Errorf("item = %q, want the spellings in use", got)
		}
		if got := col(t, `SELECT string_agg(brand, ',' ORDER BY code) FROM item_skus WHERE item_id = $1`, id); got != "KTM,motorex" {
			t.Errorf("SKU brands = %q, want KTM (the item's) and motorex (new)", got)
		}
		if got, _ := s.Locations(ctx); !slices.Equal(got, []string{"Garage shelf 2"}) {
			t.Errorf("Locations = %v, want one spelling", got)
		}
	})

	t.Run("the only record with a spelling can change its case", func(t *testing.T) {
		in := stock.ItemInput{Name: "Chain Lube", Brand: "motul", Unit: stock.UnitPiece}
		id := item(t, in)
		in.Brand, in.Version = "Motul", cur(t, s, store.WhatItem, id)
		in.Normalize()
		if err := s.UpdateItem(ctx, id, in, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := col(t, `SELECT brand FROM items WHERE id = $1`, id); got != "Motul" {
			t.Errorf("brand = %q, want Motul", got)
		}
	})

	t.Run("a shop", func(t *testing.T) {
		id := item(t, stock.ItemInput{Name: "Brake Fluid"})
		for _, shop := range []string{"Louis", "LOUIS"} {
			if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveBought, Delta: 1, Supplier: shop}); err != nil {
				t.Fatal(err)
			}
		}
		if got := col(t, `SELECT string_agg(DISTINCT supplier, ',') FROM stock_movements WHERE item_id = $1`, id); got != "Louis" {
			t.Errorf("shops = %q, want Louis twice", got)
		}
		// Edited, a purchase's own shop doesn't count, the other one does.
		ms, err := s.ListMovements(ctx, store.MovementFilter{ItemID: id})
		if err != nil || len(ms) != 2 {
			t.Fatalf("movements = %v, %v", ms, err)
		}
		m := ms[0]
		edit := stock.MovementEdit{Amount: 1, Supplier: "louis", OccurredOn: &m.OccurredOn, Version: m.Version}
		if err := s.UpdateMovement(ctx, m.ID, edit); err != nil {
			t.Fatal(err)
		}
		if got := col(t, `SELECT supplier FROM stock_movements WHERE id = $1`, m.ID); got != "Louis" {
			t.Errorf("edited shop = %q, want Louis", got)
		}
	})

	t.Run("a vehicle's make and model", func(t *testing.T) {
		for i, mm := range [][2]string{{"KTM", "390 Duke"}, {"ktm", "390 DUKE"}} {
			in := stock.VehicleInput{Name: "Bike " + strconv.Itoa(i), Make: mm[0], Model: mm[1]}
			in.Normalize()
			id, err := s.CreateVehicle(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if got := col(t, `SELECT make || '|' || model FROM vehicles WHERE id = $1`, id); got != "KTM|390 Duke" {
				t.Errorf("vehicle = %q, want KTM|390 Duke", got)
			}
		}
	})

	t.Run("a kit's name and a service's title, not the built-in ones", func(t *testing.T) {
		v := mustVehicle(t, s, "KTM 1290")
		kit := func(name string) int64 {
			in := stock.KitInput{VehicleID: v, Name: name}
			in.Normalize()
			id, err := s.CreateKit(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		if id := kit("oil service"); col(t, `SELECT name FROM service_kits WHERE id = $1`, id) != "oil service" {
			t.Error("a built-in spelling counted")
		}
		if id := kit("Oil SERVICE"); col(t, `SELECT name FROM service_kits WHERE id = $1`, id) != "oil service" {
			t.Error("the kit name in use didn't")
		}
		in := stock.ServiceInput{VehicleID: v, Title: "OIL Service", PerformedOn: time.Now()}
		in.Normalize()
		id, err := s.CreateService(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if got := col(t, `SELECT title FROM service_records WHERE id = $1`, id); got != "oil service" {
			t.Errorf("title = %q, want the kit's oil service", got)
		}
	})

	t.Run("spellings saved before differ: the list's wins", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE items SET brand = 'hiflo' WHERE name = 'Oil Filter'`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE items SET brand = 'Hiflo' WHERE name = 'Air Filter'`); err != nil {
			t.Fatal(err)
		}
		id := item(t, stock.ItemInput{Name: "Fuel Filter", Brand: "HIFLO "})
		if got := col(t, `SELECT brand FROM items WHERE id = $1`, id); got != "Hiflo" {
			t.Errorf("brand = %q, want the Hiflo the list shows", got)
		}
		if got, _ := s.Brands(ctx); !slices.Contains(got, "Hiflo") || slices.Contains(got, "hiflo") {
			t.Errorf("Brands = %v, want Hiflo once", got)
		}
	})
}
