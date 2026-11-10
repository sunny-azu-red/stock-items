package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// The message after an action leads with its verb and names what it acted
// on (docs/conventions.md, Words).
func TestMessagesNameWhatTheyActedOn(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene") // 525 ml
	pads := itemID(t, a.store, "Rear Brake")
	flash := func(rec *httptest.ResponseRecorder, want string) {
		t.Helper()
		expect(t, rec, http.StatusSeeOther)
		if f := flashOf(t, rec); f.Message != want {
			t.Errorf("flash = %q, want %q", f.Message, want)
		}
	}

	// Stock changes, from the item page's tabs and the quick -/+.
	flash(a.post(itemPath(oil)+"/movements", url.Values{"kind": {"bought"}, "amount": {"1"}, "amount_unit": {"l"}}),
		"Bought 1 L of Silkolene 15W50 PRO 4 XP")
	flash(a.post(itemPath(oil)+"/movements", url.Values{"kind": {"used"}, "amount": {"25"}}),
		"Used 25 ml of Silkolene 15W50 PRO 4 XP")
	flash(a.post(itemPath(oil)+"/movements", url.Values{"kind": {"adjusted"}, "amount": {"1"}, "amount_unit": {"l"}}),
		"Adjusted Silkolene 15W50 PRO 4 XP: 1 L in stock")
	flash(a.post(itemPath(pads)+"/adjust", url.Values{"dir": {"up"}}), "Adjusted OEM Rear Brake Pads: 2 pcs in stock")
	flash(a.post(itemPath(pads)+"/adjust", url.Values{"dir": {"down"}}), "Used 1 pcs of OEM Rear Brake Pads: 1 pcs in stock")

	// The shopping list.
	flash(a.post(itemPath(pads)+"/shopping", nil), "Added OEM Rear Brake Pads to the shopping list")
	flash(a.post("/shopping", url.Values{"free_text": {"Chain lube"}}), "Added Chain lube to the shopping list")
	list, err := a.store.ListShopping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var lube stock.ShoppingEntry
	for _, e := range list {
		if e.FreeText == "Chain lube" {
			lube = e
		}
	}
	rec := a.postV("/shopping/"+itoa(lube.ID)+"/delete", nil)
	flash(rec, "Removed Chain lube from the shopping list")
	flash(a.post("/trash/"+itoa(flashOf(t, rec).Undo)+"/restore", nil), "Restored Chain lube to the shopping list")

	// The odometer, and the trash.
	flash(a.post("/vehicles/"+itoa(k390)+"/odometer", url.Values{"odometer": {"12500"}}), "Set KTM 390's odometer to 12 500 km")
	flash(a.post("/trash/empty", nil), "Emptied the trash")
}
