package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// plainChip is the markup of a vehicle's chip as a plain label (kit rows,
// service rows, section headings); linkChip, a stock row's chip.
func plainChip(name string) string { return `<span class="dot"></span>` + name + `</span>` }
func linkChip(name string) string  { return `title="Show ` + name + ` stock"` }

// A page about one vehicle names it once at the top; its rows and headings
// show only the other vehicles' chips. Lists that mix vehicles keep them.
func TestVehicleScopeSaysTheVehicleOnce(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	ctx := context.Background()
	pads := itemID(t, a.store, "Rear Brake Pads")
	kin := stock.KitInput{VehicleID: k390, Name: "Brake service", Lines: []stock.KitLineInput{{ItemID: pads, Quantity: 1, Unit: stock.UnitPiece}}}
	kin.Normalize()
	kit, err := a.store.CreateKit(ctx, kin)
	if err != nil {
		t.Fatal(err)
	}
	other := stock.KitInput{VehicleID: k1290, Name: "Pads for the big one", Lines: []stock.KitLineInput{{ItemID: pads, Quantity: 1, Unit: stock.UnitPiece}}}
	other.Normalize()
	if _, err := a.store.CreateKit(ctx, other); err != nil {
		t.Fatal(err)
	}
	sin := stock.ServiceInput{VehicleID: k390, Title: "Chain clean", PerformedOn: stock.DateOnly(time.Now())}
	sin.Normalize()
	if _, err := a.store.CreateService(ctx, sin); err != nil {
		t.Fatal(err)
	}
	s390, s1290 := plainChip("KTM 390"), plainChip("KTM 1290")

	t.Run("vehicle page", func(t *testing.T) {
		rec := a.get("/vehicles/" + itoa(k390))
		expect(t, rec, http.StatusOK, "Brake service", "Chain clean", "Only this vehicle", "Shared with", s1290)
		expectNot(t, rec, s390, "Only <span")
	})
	t.Run("kit page", func(t *testing.T) {
		rec := a.get("/kits/" + itoa(kit))
		expect(t, rec, http.StatusOK, "Brake service")
		expectNot(t, rec, s390)
	})
	t.Run("services filtered", func(t *testing.T) {
		rec := a.get("/services?v=" + itoa(k390))
		expect(t, rec, http.StatusOK, "<h1>KTM 390 services</h1>", "<title>KTM 390 services", "Chain clean", "Brake service")
		expectNot(t, rec, s390)
	})
	t.Run("services unfiltered keep chips", func(t *testing.T) {
		expect(t, a.get("/services"), http.StatusOK, "<h1>Services</h1>", s390, s1290)
	})
	t.Run("stock filtered to one vehicle", func(t *testing.T) {
		rec := a.get("/stock?v=" + itoa(k390))
		expect(t, rec, http.StatusOK, "Only this vehicle", "Shared with", s1290)
		expectNot(t, rec, s390)
	})
	t.Run("stock list filtered to one vehicle", func(t *testing.T) {
		rec := a.get("/stock?view=list&v=" + itoa(k390))
		expect(t, rec, http.StatusOK, "Rear Brake Pads", linkChip("KTM 1290"), `name="here" value="`+itoa(k390)+`"`)
		expectNot(t, rec, linkChip("KTM 390"))
	})
	t.Run("quick adjust keeps the scope", func(t *testing.T) {
		ring := itemID(t, a.store, "Oil Filter O-Ring")
		rec := a.post("/items/"+itoa(ring)+"/adjust", url.Values{"dir": {"up"}, "here": {itoa(k390)}}, "HX-Request", "true")
		expect(t, rec, http.StatusOK, linkChip("KTM 1290"))
		expectNot(t, rec, linkChip("KTM 390"))
		// Its only vehicle is the scope: no chips at all, not "General".
		rec = a.post("/items/"+itoa(pads)+"/adjust", url.Values{"dir": {"up"}, "here": {itoa(k390)}}, "HX-Request", "true")
		expectNot(t, rec, linkChip("KTM 390"), "Show General stock")
	})
	t.Run("stock with two vehicles keeps chips", func(t *testing.T) {
		rec := a.get("/stock?v=" + itoa(k390) + "&v=" + itoa(k1290))
		expect(t, rec, http.StatusOK, "Only <span", "Shared <span")
		expectNot(t, rec, "Only this vehicle", "Shared with")
	})
	t.Run("stock unfiltered", func(t *testing.T) {
		rec := a.get("/stock")
		expect(t, rec, http.StatusOK, "Only <span", "Shared <span", s390, s1290)
		expectNot(t, rec, "Only this vehicle", "Shared with")
	})
	t.Run("item of one vehicle", func(t *testing.T) {
		// The header names its vehicle; that vehicle's kit has no chip, the
		// other vehicle's kit keeps its own.
		rec := a.get("/items/" + itoa(pads))
		expect(t, rec, http.StatusOK, "Brake service", "Pads for the big one", s1290)
		if n := strings.Count(rec.Body.String(), s390); n != 1 {
			t.Errorf("KTM 390's chip shows %d times, want once (the header)", n)
		}
	})
}

// With one active vehicle, it is the scope of every page: rows leave out its
// chip everywhere, while General and an archived vehicle keep theirs.
func TestSoleVehicleIsTheScope(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	ctx := context.Background()
	pads := itemID(t, a.store, "Rear Brake Pads")
	kin := stock.KitInput{VehicleID: k390, Name: "Brake service", IntervalMonths: ptr(12),
		Lines: []stock.KitLineInput{{ItemID: pads, Quantity: 1, Unit: stock.UnitPiece}}}
	kin.Normalize()
	if _, err := a.store.CreateKit(ctx, kin); err != nil {
		t.Fatal(err)
	}
	sin := stock.ServiceInput{VehicleID: k390, Title: "Chain clean", PerformedOn: stock.DateOnly(time.Now())}
	sin.Normalize()
	if _, err := a.store.CreateService(ctx, sin); err != nil {
		t.Fatal(err)
	}
	gin := stock.ItemInput{Name: "Chain lube", Unit: stock.UnitPiece, Quantity: 2}
	gin.Normalize()
	lube, err := a.store.CreateItem(ctx, gin)
	if err != nil {
		t.Fatal(err)
	}
	s390 := plainChip("KTM 390")

	// Two active vehicles: the rows still tell them apart.
	expect(t, a.get("/services"), http.StatusOK, s390)

	if err := a.store.ArchiveVehicle(ctx, k1290, cur(t, a.store, store.WhatVehicle, k1290)); err != nil {
		t.Fatal(err)
	}
	t.Run("stock list", func(t *testing.T) {
		rec := a.get("/stock?view=list")
		expect(t, rec, http.StatusOK, "Rear Brake Pads", "Show General stock", linkChip("KTM 1290"), `name="here" value="`+itoa(k390)+`"`)
		expectNot(t, rec, linkChip("KTM 390"))
	})
	t.Run("stock sections keep their heading chip", func(t *testing.T) {
		expect(t, a.get("/stock"), http.StatusOK, "Only <span", s390)
	})
	t.Run("services", func(t *testing.T) {
		rec := a.get("/services")
		expect(t, rec, http.StatusOK, "<h1>Services</h1>", "Chain clean", "Brake service")
		expectNot(t, rec, s390)
	})
	t.Run("dashboard", func(t *testing.T) {
		rec := a.get("/")
		expect(t, rec, http.StatusOK, "Brake service")
		expectNot(t, rec, s390)
	})
	t.Run("general item's kits", func(t *testing.T) {
		kin := stock.KitInput{VehicleID: k390, Name: "Chain service", Lines: []stock.KitLineInput{{ItemID: lube, Quantity: 1, Unit: stock.UnitPiece}}}
		kin.Normalize()
		if _, err := a.store.CreateKit(ctx, kin); err != nil {
			t.Fatal(err)
		}
		rec := a.get("/items/" + itoa(lube))
		expect(t, rec, http.StatusOK, "Chain service")
		expectNot(t, rec, s390)
	})
}
