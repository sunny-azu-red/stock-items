package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// pageHead is the page's heading and the line under it: from .page-head to
// its buttons (.actions) or, without buttons, to its closing tag, the first
// </div> at the start of a line (the ones inside are indented).
func pageHead(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body := rec.Body.String()
	i := strings.Index(body, `<div class="page-head"`)
	if i < 0 {
		t.Fatalf("no page header in\n%s", truncate(body))
	}
	head := body[i:]
	for _, end := range []string{`<div class="actions">`, "\n</div>"} {
		if j := strings.Index(head, end); j >= 0 {
			head = head[:j]
		}
	}
	return head
}

// A chip or badge is a link only when it goes up to the record the page
// belongs to (a service's vehicle and kit, a kit's vehicle) or filters the
// stock from a stock row. Everything else, and anything on a form, is a
// plain label (docs/design.md, "When a chip or badge is a link").
func TestChipAndBadgeLinks(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	expect(t, a.post("/items", url.Values{
		"name": {"Air Filter"}, "category": {"Filters"}, "unit": {"piece"}, "quantity": {"2"}, "vehicle": {itoa(k390)},
	}), http.StatusSeeOther)
	filter := itemID(t, a.store, "Air Filter")
	in := stock.KitInput{VehicleID: k390, Name: "Air service", Lines: []stock.KitLineInput{{ItemID: filter, Quantity: 1, Unit: stock.UnitPiece}}}
	in.Normalize()
	kit, err := a.store.CreateKit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	sin := stock.ServiceInput{VehicleID: k390, KitID: &kit, Title: "Air service", PerformedOn: stock.DateOnly(time.Now()),
		Parts: []stock.KitLineInput{{ItemID: filter, Quantity: 1, Unit: stock.UnitPiece}}}
	sin.Normalize()
	svc, err := a.store.CreateService(ctx, sin)
	if err != nil {
		t.Fatal(err)
	}
	vehicleLink := `<a class="chip" href="/vehicles/` + itoa(k390) + `"`
	kitLink := `<a class="badge badge-muted" href="/kits/` + itoa(kit) + `"`

	t.Run("stock rows filter", func(t *testing.T) {
		rec := a.get("/stock?view=list")
		expect(t, rec, http.StatusOK,
			`<a class="chip" href="/stock?v=`+itoa(k390)+`"`, `title="Show KTM 390 stock"`,
			`<a class="badge badge-muted" href="/stock?cat=Filters"`)
	})

	t.Run("item header labels", func(t *testing.T) {
		rec := a.get("/items/" + itoa(filter))
		expect(t, rec, http.StatusOK)
		head := pageHead(t, rec)
		for _, s := range []string{`<span class="chip"`, `<span class="badge badge-muted"><svg class="icon" aria-hidden="true"><use href="#i-tag"></use></svg> Filters</span>`} {
			if !strings.Contains(head, s) {
				t.Errorf("item header has no %s:\n%s", s, head)
			}
		}
		if strings.Contains(head, "<a ") {
			t.Errorf("item header has a link:\n%s", head)
		}
		// The Service kits card names the kit; its vehicle is a label.
		expectNot(t, rec, vehicleLink)
	})

	for _, c := range []struct{ name, path string }{
		{"kit page", "/kits/" + itoa(kit)},
		{"service page", "/services/" + itoa(svc)},
	} {
		t.Run(c.name+" goes up", func(t *testing.T) {
			rec := a.get(c.path)
			expect(t, rec, http.StatusOK)
			head := pageHead(t, rec)
			if !strings.Contains(head, vehicleLink) {
				t.Errorf("%s header doesn't link its vehicle:\n%s", c.path, head)
			}
			if c.name == "service page" && !strings.Contains(head, kitLink) {
				t.Errorf("%s header doesn't link its kit:\n%s", c.path, head)
			}
		})
	}

	moves, err := a.store.ListMovements(ctx, store.MovementFilter{ItemID: filter, Kind: stock.MoveStarting})
	if err != nil || len(moves) != 1 {
		t.Fatalf("starting stock = %v, %v", moves, err)
	}
	for _, path := range []string{
		"/services/new?kit=" + itoa(kit),
		"/services/" + itoa(svc) + "/edit",
		"/movements/" + itoa(moves[0].ID) + "/edit",
	} {
		t.Run("form "+path, func(t *testing.T) {
			rec := a.get(path)
			expect(t, rec, http.StatusOK)
			head := pageHead(t, rec)
			if strings.Contains(head, "<a ") || strings.Contains(head, `target="_blank"`) {
				t.Errorf("%s: a form's header has a link:\n%s", path, head)
			}
		})
	}
}
