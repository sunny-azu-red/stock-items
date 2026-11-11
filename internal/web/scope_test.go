package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
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
		expect(t, rec, http.StatusOK, "Brake service", "Chain clean", "This vehicle only", "Shared with", s1290)
		expectNot(t, rec, s390, "Vehicle <span")
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
		expect(t, rec, http.StatusOK, "This vehicle only", "Shared with", s1290)
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
		expect(t, rec, http.StatusOK, "Vehicle <span", "KTM 390</span> only", "Shared <span")
		expectNot(t, rec, "This vehicle only", "Shared with")
	})
	t.Run("stock unfiltered", func(t *testing.T) {
		rec := a.get("/stock")
		expect(t, rec, http.StatusOK, "Vehicle <span", "Shared <span", s390, s1290)
		expectNot(t, rec, "</span> only", "This vehicle only", "Shared with")
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
