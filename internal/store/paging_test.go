package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// Pages of stock changes and services follow each other without gaps or
// repeats, also where several share a day; the counts and labour total
// match what is listed.
func TestPagingAndTotals(t *testing.T) {
	s, ctx := newStore(t)
	day := func(d int) *time.Time { v := time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC); return &v }
	k390, k1290 := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	for i := range 7 {
		// Two a day, so ties are broken by id.
		if err := s.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MovePurchase, Delta: 1, OccurredOn: day(1 + i/2)}); err != nil {
			t.Fatal(err)
		}
	}
	labor := 0.0
	for i := range 5 {
		vehicle, cost := k390, ptr(float64(10*(i+1)))
		if i == 4 {
			vehicle, cost = k1290, nil
		} else {
			labor += *cost
		}
		in := stock.ServiceInput{VehicleID: vehicle, Title: "Check", PerformedOn: *day(1 + i/2), LaborCost: cost}
		in.Normalize()
		if _, err := s.CreateService(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ListMovements(ctx, store.MovementFilter{ItemID: pads})
	if err != nil || len(all) != 7 {
		t.Fatalf("movements = %d, %v", len(all), err)
	}
	var paged []int64
	for off := 0; off < 10; off += 3 {
		page, err := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Limit: 3, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		if want := min(3, max(0, 7-off)); len(page) != want {
			t.Errorf("movements at %d: %d, want %d", off, len(page), want)
		}
		for _, m := range page {
			paged = append(paged, m.ID)
		}
	}
	var ids []int64
	for _, m := range all {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(paged, ids) {
		t.Errorf("movement pages = %v, want %v", paged, ids)
	}

	svcs, err := s.ListServices(ctx, store.ServiceFilter{VehicleID: k390})
	if err != nil || len(svcs) != 4 {
		t.Fatalf("services = %d, %v", len(svcs), err)
	}
	var want, got []int64
	for _, r := range svcs {
		want = append(want, r.ID)
	}
	for off := 0; off < 6; off += 2 {
		page, err := s.ListServices(ctx, store.ServiceFilter{VehicleID: k390, Limit: 2, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page {
			got = append(got, r.ID)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("service pages = %v, want %v", got, want)
	}

	for _, tc := range []struct {
		vehicle int64
		count   int
		labor   float64
	}{
		{k390, 4, labor},
		{k1290, 1, 0},
		{0, 5, labor}, // every vehicle
		{k1290 + 100, 0, 0},
	} {
		n, err := s.CountServices(ctx, tc.vehicle)
		if err != nil || n != tc.count {
			t.Errorf("CountServices(%d) = %d, %v; want %d", tc.vehicle, n, err, tc.count)
		}
		total, err := s.LaborTotal(ctx, tc.vehicle)
		if err != nil || total != tc.labor {
			t.Errorf("LaborTotal(%d) = %v, %v; want %v", tc.vehicle, total, err, tc.labor)
		}
	}
}
