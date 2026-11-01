package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

func TestMovementsAndAdjust(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	path := "/items/" + itoa(oil)

	expect(t, a.post(path+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"}, "total_price": {"19,90"}, "supplier": {"Moto shop"}}), http.StatusSeeOther)
	expect(t, a.post(path+"/movements", url.Values{"kind": {"use"}, "amount": {"5"}, "amount_unit": {"l"}}), http.StatusUnprocessableEntity, "Only 1\u202f525 ml on stock")
	expect(t, a.post(path+"/movements", url.Values{"kind": {"use"}, "amount": {"25"}, "amount_unit": {"ml"}}), http.StatusSeeOther)
	expect(t, a.post(path+"/movements", url.Values{"kind": {"correction"}, "amount": {"1.2"}, "amount_unit": {"l"}}), http.StatusSeeOther)
	expect(t, a.get(path), http.StatusOK, "1.2 L", "Moto shop", "19.90 EUR")

	// Quick adjust via htmx returns the row and a toast.
	rec := a.post(path+"/adjust", url.Values{"dir": {"down"}}, "HX-Request", "true")
	expect(t, rec, http.StatusOK, `id="item-`+itoa(oil)+`"`, "1.1 L")
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "toast") {
		t.Errorf("missing toast trigger: %v", rec.Header())
	}
	// Header values are read as Latin-1: anything else is escaped.
	pads := stock.ItemInput{Name: "Plăcuțe frână", Unit: stock.UnitPiece, Quantity: 1199}
	pads.Normalize()
	padsID, err := a.store.CreateItem(context.Background(), pads)
	if err != nil {
		t.Fatal(err)
	}
	trigger := a.post("/items/"+itoa(padsID)+"/adjust", url.Values{"dir": {"up"}}, "HX-Request", "true").Header().Get("HX-Trigger")
	for _, r := range trigger {
		if r >= utf8.RuneSelf {
			t.Errorf("HX-Trigger %q is not ASCII", trigger)
			break
		}
	}
	var decoded struct{ Toast struct{ Message string } }
	if err := json.Unmarshal([]byte(trigger), &decoded); err != nil || !strings.Contains(decoded.Toast.Message, "Plăcuțe frână") ||
		!strings.Contains(decoded.Toast.Message, "1\u202f200") {
		t.Errorf("toast = %q, %v", decoded.Toast.Message, err)
	}
	// Without htmx it redirects back.
	req := httptest.NewRequest(http.MethodPost, path+"/adjust", strings.NewReader("dir=up"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "http://example.com/stock?low=1")
	rec = a.do(req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/stock?low=1" {
		t.Errorf("adjust redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	expect(t, a.get("/activity"), http.StatusOK, "Quick adjust", "Purchase")
}

func TestActivityExportCSV(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	expect(t, a.post("/items/"+itoa(oil)+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
		"total_price": {"19.90"}, "supplier": {"Moto shop"}}), http.StatusSeeOther)
	expect(t, a.get("/activity?kind=purchase"), http.StatusOK, `href="/activity/export.csv?kind=purchase"`)

	rec := a.get("/activity/export.csv?kind=purchase")
	expect(t, rec, http.StatusOK, "date,item_id,item,kind,change", "Silkolene 15W50 PRO 4 XP,purchase,1000,'+1 L,ml,19.90,19.90,L,Moto shop")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	if strings.Contains(rec.Body.String(), ",initial,") {
		t.Error("kind filter not applied")
	}
	expect(t, a.get("/activity/export.csv"), http.StatusOK, ",initial,")
}

func TestEditAndDeleteMovement(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	item := "/items/" + itoa(oil)
	expect(t, a.post(item+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
		"total_price": {"199"}, "supplier": {"Moto shop"}, "occurred_on": {"2026-10-29"}}), http.StatusSeeOther)
	purchases, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MovePurchase})
	edit := "/movements/" + itoa(purchases[0].ID)

	// Service parts have no edit link, other rows do.
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Top up"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil)}, "part_qty": {"25"}})
	expect(t, rec, http.StatusSeeOther)
	services, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveService})
	page := a.get(item)
	expect(t, page, http.StatusOK, `href="`+edit+`/edit"`)
	expectNot(t, page, `href="/movements/`+itoa(services[0].ID)+`/edit"`)
	expect(t, a.get("/movements/"+itoa(services[0].ID)+"/edit"), http.StatusNotFound)
	expect(t, a.get("/activity"), http.StatusOK, `href="`+edit+`/edit"`)

	expect(t, a.get(edit+"/edit"), http.StatusOK, "Edit purchase", `value="199.00"`, `value="Moto shop"`, `name="amount" value="1"`)
	expect(t, a.postV(edit, url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"19.90"}, "occurred_on": {"2027-01-01"}}),
		http.StatusUnprocessableEntity, "Date cannot be in the future", `value="19.90"`)
	rec = a.postV(edit, url.Values{"amount": {"2"}, "amount_unit": {"l"}, "total_price": {"39.80"}, "supplier": {"Moto shop"}, "occurred_on": {"2026-10-29"}})
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != item {
		t.Errorf("location = %q, want %q", l, item)
	}
	expect(t, a.get(item), http.StatusOK, "19.90 EUR / L", "2.5 L")

	// Opened from the activity page, the form returns there; a posted
	// "back" pointing off-site falls back to the item.
	req := httptest.NewRequest(http.MethodGet, edit+"/edit", nil)
	req.Header.Set("Referer", "http://example.com/activity?kind=purchase")
	expect(t, a.do(req), http.StatusOK, `href="/activity?kind=purchase">Cancel`, `name="back" value="/activity?kind=purchase"`)
	for back, want := range map[string]string{"/activity?kind=purchase": "/activity?kind=purchase", "//evil.example/": item, "https://evil.example/": item} {
		rec = a.postV(edit, url.Values{"amount": {"2"}, "amount_unit": {"l"}, "total_price": {"39.80"}, "occurred_on": {"2026-10-29"}, "back": {back}})
		if l := rec.Header().Get("Location"); l != want {
			t.Errorf("back %q: location = %q, want %q", back, l, want)
		}
	}

	// Taking away more than is left is refused with a hint.
	expect(t, a.post(item+"/movements", url.Values{"kind": {"use"}, "amount": {"2.4"}, "amount_unit": {"l"}}), http.StatusSeeOther)
	rec = a.postV(edit+"/delete", nil)
	expect(t, rec, http.StatusUnprocessableEntity, "Only 100 ml is left on stock")
	// The form shown with the refusal carries the change's version, so it
	// still saves.
	expect(t, a.post(edit, url.Values{"amount": {"2"}, "amount_unit": {"l"}, "total_price": {"39.80"},
		"occurred_on": {"2026-10-29"}, "version": {formVersion(t, rec.Body.String(), edit)}}), http.StatusSeeOther)

	uses, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveUse})
	expect(t, a.postV("/movements/"+itoa(uses[0].ID)+"/delete", nil), http.StatusSeeOther)
	expect(t, a.postV(edit+"/delete", nil), http.StatusSeeOther)
	if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 500 {
		t.Errorf("quantity = %v, want 500 (525 initial - 25 service)", it.Quantity)
	}
	expect(t, a.get(edit+"/edit"), http.StatusNotFound)
}

func TestStockChangesDefaultToTheAppDate(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	expect(t, a.post("/items/"+itoa(pads)+"/movements", url.Values{"kind": {"use"}, "amount": {"1"}}), http.StatusSeeOther)
	expect(t, a.post("/items/"+itoa(pads)+"/adjust", url.Values{"dir": {"up"}}), http.StatusSeeOther)
	moves, _ := a.store.ListMovements(context.Background(), store.MovementFilter{ItemID: pads})
	for _, m := range moves {
		if m.Kind != stock.MoveInitial && m.OccurredOn.Format("2006-01-02") != "2026-10-29" {
			t.Errorf("%s dated %s, want the app's 2026-10-29", m.Kind, m.OccurredOn.Format("2006-01-02"))
		}
	}
}

func TestEditACount(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	// Quick + records a count of +100 ml.
	expect(t, a.post("/items/"+itoa(oil)+"/adjust", url.Values{"dir": {"up"}}), http.StatusSeeOther)
	counts, _ := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveCorrection})
	edit := "/movements/" + itoa(counts[0].ID)
	expect(t, a.get(edit+"/edit"), http.StatusOK, "Edit count", `name="direction" value="added" checked`, `name="amount" value="100"`)
	// It should have been a quick −: 100 ml taken away.
	expect(t, a.postV(edit, url.Values{"direction": {"removed"}, "amount": {"100"}, "amount_unit": {"ml"}, "occurred_on": {"2026-10-29"}, "note": {"Quick adjust"}}),
		http.StatusSeeOther)
	if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 425 {
		t.Errorf("oil = %v, want 425", it.Quantity)
	}
}

func TestActivityPagesAndItemHistory(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	pads := itemID(t, a.store, "Rear Brake")
	for range 205 {
		if err := a.store.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MoveUse, Delta: -1}); err != nil {
			if err := a.store.AddMovement(ctx, stock.MovementInput{ItemID: pads, Kind: stock.MovePurchase, Delta: 1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	item := "/items/" + itoa(pads)
	expect(t, a.get(item), http.StatusOK, `href="/activity?item=`+itoa(pads)+`">All entries →</a>`)
	page := a.get("/activity?item=" + itoa(pads))
	expect(t, page, http.StatusOK, "History of OEM Rear Brake Pads", `href="/activity?item=`+itoa(pads)+`&amp;page=2"`,
		`href="/activity/export.csv?item=`+itoa(pads)+`"`)
	expectNot(t, page, "Newer")
	expect(t, a.get("/activity?item="+itoa(pads)+"&page=2"), http.StatusOK, "Newer")
	csv := a.get("/activity/export.csv?item=" + itoa(pads))
	if n := strings.Count(csv.Body.String(), "\n"); n != 207 { // header + 205 + initial stock
		t.Errorf("csv lines = %d, want 207", n)
	}
}
