package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

func TestDashboardSpentThisYear(t *testing.T) {
	a := newApp(t) // the app's day is 29 Oct 2026
	ctx := context.Background()
	k390, _ := a.seed()
	oil := itemID(t, a.store, "Silkolene")
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		on    time.Time
		price float64
	}{{day(2025, 12, 31), 7}, {day(2026, 1, 1), 20}} {
		if err := a.store.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MovePurchase, Delta: 1, TotalPrice: ptr(c.price), OccurredOn: &c.on}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.CreateService(ctx, stock.ServiceInput{VehicleID: k390, Title: "Chain", PerformedOn: day(2026, 3, 1), LaborCost: ptr(30.0)}); err != nil {
		t.Fatal(err)
	}

	// This year's purchase and labour, not last year's purchase; the tile
	// opens the purchases.
	rec := a.get("/")
	expect(t, rec, http.StatusOK, `<a class="card tile" href="/activity?kind=purchase">`, "Spent in 2026", "50.00 EUR")
	expectNot(t, rec, "57.00 EUR", "12 months")
	expect(t, a.get("/activity?kind=purchase"), http.StatusOK, "31 Dec 2025", "1 Jan 2026")
}
