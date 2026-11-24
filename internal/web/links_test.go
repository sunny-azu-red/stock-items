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

// pageHead is the page's heading and the line under it: the .page-head
// element layout.html writes, up to its buttons (.actions).
func pageHead(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	head := headElement(rec.Body.String())
	if head == "" {
		t.Fatalf("no page header in\n%s", truncate(rec.Body.String()))
	}
	if j := strings.Index(head, `<div class="actions">`); j >= 0 {
		head = head[:j]
	}
	return head
}

// headActions is the header's buttons (.actions, ⋯ and its menu included),
// or "" without any.
func headActions(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	head := headElement(rec.Body.String())
	if j := strings.Index(head, `<div class="actions">`); j >= 0 {
		return head[j:]
	}
	return ""
}

// headElement is the page's .page-head element, from its opening tag to its
// own closing </div> (the divs inside counted), or "" without one.
func headElement(body string) string {
	i := strings.Index(body, `<div class="page-head"`)
	if i < 0 {
		return ""
	}
	depth := 0
	for j := i; j < len(body); {
		open, end := strings.Index(body[j:], "<div"), strings.Index(body[j:], "</div>")
		switch {
		case end < 0:
			return ""
		case open >= 0 && open < end:
			depth++
			j += open + len("<div")
		default:
			depth--
			j += end + len("</div>")
			if depth == 0 {
				return body[i:j]
			}
		}
	}
	return ""
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
	sin := stock.ServiceInput{Odometer: ptr(10000), VehicleID: k390, KitID: &kit, Title: "Air service", PerformedOn: stock.DateOnly(time.Now()),
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

	// A record's page has no back button (design.md#header-buttons): the
	// nav leads to its list, its line's chips up to its vehicle, and the
	// browser's Back to where you were.
	for _, c := range []struct{ path, up string }{
		{"/items/" + itoa(filter), ""},
		{"/services/" + itoa(svc), `href="/vehicles/` + itoa(k390) + `"`},
		{"/kits/" + itoa(kit), `href="/vehicles/` + itoa(k390) + `"`},
		{"/vehicles/" + itoa(k390), ""},
	} {
		t.Run("no way back on "+c.path, func(t *testing.T) {
			rec := a.get(c.path)
			expect(t, rec, http.StatusOK)
			actions := headActions(t, rec)
			if strings.Contains(actions, "btn-ghost") || strings.Contains(actions, "#i-arrow-left") {
				t.Errorf("%s: a back button in the header:\n%s", c.path, actions)
			}
			if head := pageHead(t, rec); !strings.Contains(head, c.up) {
				t.Errorf("%s: the line doesn't lead up to its vehicle (%s):\n%s", c.path, c.up, head)
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

	// Archived, they have none either.
	t.Run("no way back when archived", func(t *testing.T) {
		item, vehicle := "/items/"+itoa(filter), "/vehicles/"+itoa(k390)
		expect(t, a.post(item+"/archive", url.Values{"version": {a.versionOf(item + "/archive")}}), http.StatusSeeOther)
		expect(t, a.post(vehicle+"/archive", url.Values{"version": {a.versionOf(vehicle + "/archive")}}), http.StatusSeeOther)
		for _, path := range []string{item, vehicle} {
			rec := a.get(path)
			expect(t, rec, http.StatusOK, "Restore")
			if actions := headActions(t, rec); strings.Contains(actions, "btn-ghost") {
				t.Errorf("%s: a back button in the header:\n%s", path, actions)
			}
		}
	})
}
