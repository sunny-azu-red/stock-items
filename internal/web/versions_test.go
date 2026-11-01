package web

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// These tests open a page, take the version its form carries, change the
// record in "another tab" (another request), and then submit the first
// page's form, all over HTTP, the way a browser does.

// openForm opens page and returns the version carried by its form that
// posts to action ("" when it carries none; TestFormsCarryVersions makes
// sure they all do).
func openForm(t *testing.T, a *testApp, page, action string) string {
	t.Helper()
	rec := a.get(page)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", page, rec.Code)
	}
	if v := versionField.FindStringSubmatch(formsPosting(t, rec.Body.String(), action)[0]); v != nil {
		return v[1]
	}
	return ""
}

// submit posts fields to action with version, as the page it was taken
// from would.
func submit(a *testApp, action, version string, fields url.Values) *httptest.ResponseRecorder {
	form := url.Values{}
	for k, v := range fields {
		form[k] = append([]string(nil), v...)
	}
	form.Set("version", version)
	return a.post(action, form)
}

// saveFresh is another tab: it opens page and saves fields with that
// page's form posting to action.
func saveFresh(t *testing.T, a *testApp, page, action string, fields url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := submit(a, action, openForm(t, a, page, action), fields)
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Kind == "error" {
		t.Fatalf("saving %s in another tab: %s", action, f.Message)
	}
	return rec
}

// record is one kind of record with an edit form, for the tables below.
type record struct {
	name string
	// make creates the record and returns its edit form's action (the
	// edit page is action+"/edit") and the page with its delete button.
	make func(t *testing.T, a *testApp) (action, show string)
	// fields fills in its edit form with note in a free-text field.
	fields func(a *testApp, note string) url.Values
	// note reads that field back, "" when the record is gone.
	note func(a *testApp, action string) string
}

func records() []record {
	ctx := context.Background()
	return []record{
		{
			name: "item",
			make: func(t *testing.T, a *testApp) (string, string) {
				p := itemPath(itemID(t, a.store, "Silkolene"))
				return p, p
			},
			fields: func(a *testApp, note string) url.Values {
				return url.Values{"name": {"15W50 PRO 4 XP"}, "brand": {"Silkolene"}, "unit": {"ml"}, "quantity": {"525"},
					"quantity_unit": {"ml"}, "quantity_was": {"525"}, "notes": {note}}
			},
			note: func(a *testApp, action string) string {
				it, _ := a.store.GetItem(ctx, mustID(a.t, action))
				return it.Notes
			},
		},
		{
			name: "vehicle",
			make: func(t *testing.T, a *testApp) (string, string) {
				p := "/vehicles/" + itoa(a.vehicle(t, "KTM 1290"))
				return p, p
			},
			fields: func(a *testApp, note string) url.Values {
				return url.Values{"name": {"KTM 1290"}, "kind": {"motorcycle"}, "color": {"#2563eb"},
					"odometer_km": {"10000"}, "odometer_was": {"10000"}, "notes": {note}}
			},
			note: func(a *testApp, action string) string {
				v, _ := a.store.GetVehicle(ctx, mustID(a.t, action))
				return v.Notes
			},
		},
		{
			name: "kit",
			make: func(t *testing.T, a *testApp) (string, string) {
				rec := a.post("/kits", url.Values{"vehicle_id": {itoa(a.vehicle(t, "KTM 390"))}, "name": {"Oil service"}})
				expect(t, rec, http.StatusSeeOther)
				p := rec.Header().Get("Location")
				return p, p
			},
			fields: func(a *testApp, note string) url.Values {
				return url.Values{"vehicle_id": {itoa(a.vehicle(a.t, "KTM 390"))}, "name": {"Oil service"}, "notes": {note}}
			},
			note: func(a *testApp, action string) string {
				k, _ := a.store.GetKit(ctx, mustID(a.t, action))
				return k.Notes
			},
		},
		{
			name: "service",
			make: func(t *testing.T, a *testApp) (string, string) {
				rec := a.post("/services", url.Values{"vehicle_id": {itoa(a.vehicle(t, "KTM 390"))}, "title": {"Chain clean"},
					"performed_on": {"2026-10-29"}, "part_item": {itoa(itemID(t, a.store, "Silkolene"))}, "part_qty": {"25"}})
				expect(t, rec, http.StatusSeeOther)
				p := rec.Header().Get("Location")
				return p, p
			},
			fields: func(a *testApp, note string) url.Values {
				return url.Values{"vehicle_id": {itoa(a.vehicle(a.t, "KTM 390"))}, "title": {"Chain clean"}, "performed_on": {"2026-10-29"},
					"part_item": {itoa(itemID(a.t, a.store, "Silkolene"))}, "part_qty": {"25"}, "notes": {note}}
			},
			note: func(a *testApp, action string) string {
				r, _ := a.store.GetService(ctx, mustID(a.t, action))
				return r.Notes
			},
		},
		{
			name: "stock change",
			make: func(t *testing.T, a *testApp) (string, string) {
				oil := itemID(t, a.store, "Silkolene")
				expect(t, a.post(itemPath(oil)+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
					"total_price": {"20"}, "occurred_on": {"2026-10-29"}}), http.StatusSeeOther)
				moves, err := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MovePurchase})
				if err != nil || len(moves) != 1 {
					t.Fatalf("purchases = %+v, %v", moves, err)
				}
				p := "/movements/" + itoa(moves[0].ID)
				return p, p + "/edit"
			},
			fields: func(a *testApp, note string) url.Values {
				return url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"20"}, "occurred_on": {"2026-10-29"},
					"item_id": {itoa(itemID(a.t, a.store, "Silkolene"))}, "kind": {"purchase"}, "note": {note}}
			},
			note: func(a *testApp, action string) string {
				m, _ := a.store.GetMovement(ctx, mustID(a.t, action))
				return m.Note
			},
		},
	}
}

// vehicle finds a seeded vehicle by name.
func (a *testApp) vehicle(t *testing.T, name string) int64 {
	t.Helper()
	vs, err := a.store.ListVehicles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Name == name {
			return v.ID
		}
	}
	t.Fatalf("no vehicle %q", name)
	return 0
}

// An edit form saved in another tab first is refused with what was
// typed still there, a banner and a Reload link; after reloading it
// saves. A form without a version is refused too.
func TestStaleEditFormsAreRefused(t *testing.T) {
	for _, r := range records() {
		t.Run(r.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			action, _ := r.make(t, a)
			page := action + "/edit"
			mine := openForm(t, a, page, action)

			saveFresh(t, a, page, action, r.fields(a, "Theirs"))
			rec := submit(a, action, mine, r.fields(a, "Typed in the first tab"))
			expect(t, rec, http.StatusUnprocessableEntity, "This "+r.name+" was changed since you opened this",
				`href="`+page+`">Reload</a>`, "Typed in the first tab")
			if got := r.note(a, action); got != "Theirs" {
				t.Errorf("saved = %q, want the other tab's", got)
			}
			for _, bad := range []string{"", "abc", "0"} {
				expect(t, submit(a, action, bad, r.fields(a, "No version")), http.StatusUnprocessableEntity, "was changed since you opened this")
			}
			if got := r.note(a, action); got != "Theirs" {
				t.Errorf("saved = %q after forms without a version", got)
			}

			// Reloaded, the same change goes through.
			expect(t, submit(a, action, openForm(t, a, page, action), r.fields(a, "Mine")), http.StatusSeeOther)
			if got := r.note(a, action); got != "Mine" {
				t.Errorf("after reloading, saved = %q", got)
			}
		})
	}
}

// An edit form whose record was deleted in another tab keeps what was
// typed and points to the trash, rather than a 404.
func TestEditFormsOfDeletedRecordsKeepWhatWasTyped(t *testing.T) {
	for _, r := range records() {
		t.Run(r.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			action, show := r.make(t, a)
			page := action + "/edit"
			mine := openForm(t, a, page, action)

			rec := saveFresh(t, a, show, action+"/delete", url.Values{"back": {"/activity"}})
			if flashOf(t, rec).Undo == 0 {
				t.Fatalf("delete offered no Undo: %+v", flashOf(t, rec))
			}
			rec = submit(a, action, mine, r.fields(a, "Typed in the first tab"))
			expect(t, rec, http.StatusUnprocessableEntity, "This "+r.name+" was deleted since you opened this; it&#39;s in the trash.",
				`href="/trash">Trash</a>`, "Typed in the first tab")
			if got := r.note(a, action); got != "" {
				t.Errorf("the deleted %s came back with %q", r.name, got)
			}
		})
	}
}

// Delete → Undo → the form opened before the delete is refused: the
// restored record has a new version. Its delete button, too. A form
// opened after the Undo works.
func TestUndoMakesOlderFormsStale(t *testing.T) {
	for _, r := range records() {
		t.Run(r.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			action, show := r.make(t, a)
			page := action + "/edit"
			mine := openForm(t, a, page, action)
			oldDelete := openForm(t, a, show, action+"/delete")

			undo := flashOf(t, saveFresh(t, a, show, action+"/delete", url.Values{"back": {"/activity"}})).Undo
			expect(t, a.post("/trash/"+itoa(undo)+"/restore", nil), http.StatusSeeOther)
			expect(t, a.get(page), http.StatusOK)

			expect(t, submit(a, action, mine, r.fields(a, "Typed before the delete")), http.StatusUnprocessableEntity,
				"This "+r.name+" was changed since you opened this", "Typed before the delete")
			rec := submit(a, action+"/delete", oldDelete, url.Values{"back": {"/activity"}})
			expect(t, rec, http.StatusSeeOther)
			if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "was changed since you opened it") {
				t.Errorf("old delete button: flash = %+v", f)
			}
			expect(t, a.get(page), http.StatusOK) // still there

			expect(t, submit(a, action, openForm(t, a, page, action), r.fields(a, "Mine")), http.StatusSeeOther)
			if got := r.note(a, action); got != "Mine" {
				t.Errorf("a form opened after the Undo saved %q", got)
			}
		})
	}
}

// A button whose record changed in another tab does nothing and says so
// on the record's page; one whose record was deleted points to Recently
// deleted.
func TestStaleButtonsAreRefused(t *testing.T) {
	type button struct {
		name  string
		setup func(t *testing.T, a *testApp) (show, action string, change func(), unchanged func() bool)
	}
	ctx := context.Background()
	edit := func(t *testing.T, a *testApp, r record, action string) func() {
		return func() { saveFresh(t, a, action+"/edit", action, r.fields(a, "Changed meanwhile")) }
	}
	rs := records()
	item, vehicle, kit, service, movement := rs[0], rs[1], rs[2], rs[3], rs[4]
	archived := func(a *testApp, action string) bool {
		switch {
		case strings.HasPrefix(action, "/items/"):
			it, _ := a.store.GetItem(ctx, mustID(a.t, action))
			return it.Archived()
		case strings.HasPrefix(action, "/vehicles/"):
			v, _ := a.store.GetVehicle(ctx, mustID(a.t, action))
			return v.Archived()
		}
		k, _ := a.store.GetKit(ctx, mustID(a.t, action))
		return k.Archived()
	}
	// inUse logs a service with the record (an item, vehicle or kit), so
	// it is archived instead of deleted.
	inUse := func(t *testing.T, a *testApp, action string) {
		form := url.Values{"vehicle_id": {itoa(a.vehicle(t, "KTM 390"))}, "title": {"Used"}, "performed_on": {"2026-10-29"}}
		switch {
		case strings.HasPrefix(action, "/items/"):
			form.Set("part_item", itoa(mustID(t, action)))
			form.Set("part_qty", "1")
		case strings.HasPrefix(action, "/vehicles/"):
			form.Set("vehicle_id", itoa(mustID(t, action)))
		default:
			form.Set("kit_id", itoa(mustID(t, action)))
		}
		expect(t, a.post("/services", form), http.StatusSeeOther)
	}
	var buttons []button
	for _, r := range []record{item, vehicle, kit} {
		buttons = append(buttons,
			button{r.name + " delete", func(t *testing.T, a *testApp) (string, string, func(), func() bool) {
				action, show := r.make(t, a)
				return show, action + "/delete", edit(t, a, r, action), func() bool { return r.note(a, action) == "Changed meanwhile" }
			}},
			button{r.name + " archive", func(t *testing.T, a *testApp) (string, string, func(), func() bool) {
				action, show := r.make(t, a)
				inUse(t, a, action)
				return show, action + "/archive", edit(t, a, r, action), func() bool { return !archived(a, action) }
			}},
			button{r.name + " restore", func(t *testing.T, a *testApp) (string, string, func(), func() bool) {
				action, show := r.make(t, a)
				inUse(t, a, action)
				saveFresh(t, a, show, action+"/archive", nil)
				return show, action + "/restore", edit(t, a, r, action), func() bool { return archived(a, action) }
			}},
		)
	}
	buttons = append(buttons,
		button{"service delete", func(t *testing.T, a *testApp) (string, string, func(), func() bool) {
			action, show := service.make(t, a)
			return show, action + "/delete", edit(t, a, service, action), func() bool {
				it, _ := a.store.GetItem(ctx, itemID(t, a.store, "Silkolene"))
				return service.note(a, action) == "Changed meanwhile" && it.Quantity == 500
			}
		}},
		button{"stock change delete", func(t *testing.T, a *testApp) (string, string, func(), func() bool) {
			action, show := movement.make(t, a)
			return show, action + "/delete", edit(t, a, movement, action), func() bool {
				it, _ := a.store.GetItem(ctx, itemID(t, a.store, "Silkolene"))
				return movement.note(a, action) == "Changed meanwhile" && it.Quantity == 1525
			}
		}},
		button{"shopping list remove", func(t *testing.T, a *testApp) (string, string, func(), func() bool) {
			oil := itemID(t, a.store, "Silkolene")
			entry, err := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(1000.0)})
			if err != nil {
				t.Fatal(err)
			}
			more := func() {
				expect(t, a.post(itemPath(oil)+"/shopping", url.Values{"quantity": {"1"}, "quantity_unit": {"l"}}), http.StatusSeeOther)
			}
			return "/shopping", "/shopping/" + itoa(entry) + "/delete", more, func() bool {
				e, err := a.store.GetShopping(ctx, entry)
				return err == nil && *e.Quantity == 2000
			}
		}},
		button{"tick off", func(t *testing.T, a *testApp) (string, string, func(), func() bool) {
			entry, err := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "silkolene 15w50 pro 4 xp"})
			if err != nil {
				t.Fatal(err)
			}
			page := "/shopping/" + itoa(entry) + "/bought"
			link := func() {
				saveFresh(t, a, page, "/shopping/"+itoa(entry)+"/link", url.Values{"item_id": {itoa(itemID(t, a.store, "Silkolene"))}})
			}
			return page, page, link, func() bool {
				_, err := a.store.GetShopping(ctx, entry)
				return err == nil
			}
		}},
	)
	for _, b := range buttons {
		t.Run(b.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			show, action, change, unchanged := b.setup(t, a)
			version := openForm(t, a, show, action)
			change()
			rec := submit(a, action, version, url.Values{"back": {"/activity"}})
			expect(t, rec, http.StatusSeeOther)
			if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "was changed since you opened it") || f.Undo != 0 {
				t.Errorf("flash = %+v", f)
			}
			if !unchanged() {
				t.Error("the button did something")
			}
			if l := rec.Header().Get("Location"); l == "" || a.get(l).Code != http.StatusOK {
				t.Errorf("went to %q", l)
			}
		})
	}
}

// A delete button whose record is already in the trash says so and goes
// there, rather than to a 404.
func TestButtonsOfDeletedRecordsPointToTheTrash(t *testing.T) {
	for _, r := range records() {
		t.Run(r.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			action, show := r.make(t, a)
			version := openForm(t, a, show, action+"/delete")
			saveFresh(t, a, show, action+"/delete", url.Values{"back": {"/activity"}})
			rec := submit(a, action+"/delete", version, url.Values{"back": {"/activity"}})
			expect(t, rec, http.StatusSeeOther)
			if l := rec.Header().Get("Location"); l != "/trash" {
				t.Errorf("went to %q, want /trash", l)
			}
			if f := flashOf(t, rec); f.Kind != "error" || f.Message != "This "+r.name+" was deleted since you opened this; it's in the trash." {
				t.Errorf("flash = %+v", f)
			}
		})
	}
}

// What changes the edit form's fields makes it stale; stock changes and
// odometer readings don't (see TestItemEditFormDoesNotUndoStockChanges
// and TestVehicleEditFormKeepsANewerOdometer).
func TestABestBeforeDateMakesTheItemFormStale(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	action := itemPath(oil)
	mine := openForm(t, a, action+"/edit", action)
	expect(t, a.post(action+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
		"expires_on": {"2030-01-31"}}), http.StatusSeeOther)
	expect(t, submit(a, action, mine, records()[0].fields(a, "Mine")), http.StatusUnprocessableEntity, "was changed since you opened this")
	if it, _ := a.store.GetItem(context.Background(), oil); it.ExpiresOn == nil || it.ExpiresOn.Format("2006-01-02") != "2030-01-31" {
		t.Errorf("expires = %v, want the new batch's date", it.ExpiresOn)
	}
}

// The Bought form of an entry that asks for more since keeps what was
// typed and buys nothing.
func TestStaleBoughtFormBuysNothing(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	entry, err := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(1000.0)})
	if err != nil {
		t.Fatal(err)
	}
	page := "/shopping/" + itoa(entry) + "/bought"
	mine := openForm(t, a, page, page)
	expect(t, a.post(itemPath(oil)+"/shopping", url.Values{"quantity": {"1"}, "quantity_unit": {"l"}}), http.StatusSeeOther)
	rec := submit(a, page, mine, url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"23.45"}})
	expect(t, rec, http.StatusUnprocessableEntity, "This shopping list entry was changed since you opened this",
		`href="`+page+`">Reload</a>`, `value="23.45"`)
	if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 525 {
		t.Errorf("oil = %v, want 525: nothing bought", it.Quantity)
	}
	if e, err := a.store.GetShopping(ctx, entry); err != nil || *e.Quantity != 2000 {
		t.Errorf("entry = %+v, %v", e, err)
	}
	f := flashOf(t, saveFresh(t, a, page, page, url.Values{"amount": {"2"}, "amount_unit": {"l"}}))
	if f.Undo == 0 {
		t.Errorf("bought after reloading: flash = %+v", f)
	}
}

// Every edit form and every button that acts on a record carries its
// version.
func TestFormsCarryVersions(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	check := func(page string, actions ...string) {
		t.Helper()
		for _, action := range actions {
			if openForm(t, a, page, action) == "" {
				t.Errorf("the form posting to %s on %s carries no version", action, page)
			}
		}
	}
	for _, r := range records() {
		action, show := r.make(t, a)
		check(action+"/edit", action)
		check(show, action+"/delete")
	}
	oil := itemPath(itemID(t, a.store, "Silkolene"))
	pads := itemID(t, a.store, "Rear Brake")
	check(oil, oil+"/archive") // it has a purchase now
	saveFresh(t, a, oil, oil+"/archive", nil)
	check(oil, oil+"/restore")
	entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads})
	check("/shopping", "/shopping/"+itoa(entry)+"/delete")
	check("/shopping/"+itoa(entry)+"/bought", "/shopping/"+itoa(entry)+"/bought")
	free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "rear brake pads"})
	bought := "/shopping/" + itoa(free) + "/bought"
	check(bought, bought, "/shopping/"+itoa(free)+"/link")
	check("/items/new?shopping="+itoa(free), "/shopping/"+itoa(free)+"/link")
}

// Every Undo and Restore still works over HTTP, with versions on every
// form, and what comes back can be edited from a freshly opened form.
func TestUndoPathsStillWork(t *testing.T) {
	ctx := context.Background()
	undo := func(t *testing.T, a *testApp, rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		t.Helper()
		f := flashOf(t, rec)
		if f.Undo == 0 {
			t.Fatalf("no Undo: %+v", f)
		}
		rec = a.post("/trash/"+itoa(f.Undo)+"/restore", nil)
		expect(t, rec, http.StatusSeeOther)
		if f := flashOf(t, rec); f.Kind == "error" {
			t.Fatalf("undo: %s", f.Message)
		}
		return rec
	}

	t.Run("delete", func(t *testing.T) {
		for _, r := range records() {
			a := newApp(t)
			a.seed()
			action, show := r.make(t, a)
			undo(t, a, saveFresh(t, a, show, action+"/delete", url.Values{"back": {"/activity"}}))
			saveFresh(t, a, action+"/edit", action, r.fields(a, "Back again"))
			if got := r.note(a, action); got != "Back again" {
				t.Errorf("%s: saved %q after the Undo", r.name, got)
			}
			a.ledger(t)
		}
	})

	t.Run("bought", func(t *testing.T) {
		a := newApp(t)
		a.seed()
		oil := itemID(t, a.store, "Silkolene")
		entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(2000.0)})
		page := "/shopping/" + itoa(entry) + "/bought"
		undo(t, a, saveFresh(t, a, page, page, url.Values{"amount": {"1"}, "amount_unit": {"l"}}))
		if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 525 {
			t.Errorf("oil = %v after undoing the purchase", it.Quantity)
		}
		if e, err := a.store.GetShopping(ctx, entry); err != nil || *e.Quantity != 2000 {
			t.Errorf("entry = %+v, %v", e, err)
		}
		// Ticked off entirely, then undone, it can be bought again.
		undo(t, a, saveFresh(t, a, page, page, url.Values{"amount": {"2"}, "amount_unit": {"l"}}))
		saveFresh(t, a, page, page, url.Values{"amount": {"2"}, "amount_unit": {"l"}})
		if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 2525 {
			t.Errorf("oil = %v after buying again", it.Quantity)
		}
	})

	t.Run("shopping list remove and tick off", func(t *testing.T) {
		a := newApp(t)
		a.seed()
		entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Zip ties"})
		del := "/shopping/" + itoa(entry) + "/delete"
		undo(t, a, saveFresh(t, a, "/shopping", del, nil))
		bought := "/shopping/" + itoa(entry) + "/bought"
		undo(t, a, saveFresh(t, a, bought, bought, nil))
		saveFresh(t, a, "/shopping", del, nil)
	})

	t.Run("photo replace and remove", func(t *testing.T) {
		needVips(t)
		a := newApp(t)
		a.seed()
		item := itemPath(itemID(t, a.store, "Silkolene"))
		upload := func(c color.Color) *httptest.ResponseRecorder {
			var img, body bytes.Buffer
			m := image.NewRGBA(image.Rect(0, 0, 8, 8))
			for i := range m.Pix {
				m.Pix[i] = 255
			}
			m.Set(0, 0, c)
			if err := png.Encode(&img, m); err != nil {
				t.Fatal(err)
			}
			w := multipart.NewWriter(&body)
			fw, _ := w.CreateFormFile("photo", "p.png")
			if _, err := fw.Write(img.Bytes()); err != nil {
				t.Fatal(err)
			}
			w.Close()
			req := httptest.NewRequest(http.MethodPost, item+"/photo", &body)
			req.Header.Set("Content-Type", w.FormDataContentType())
			rec := a.do(req)
			expect(t, rec, http.StatusSeeOther)
			return rec
		}
		photo := func() string { return a.get(item + "/photo").Body.String() }
		upload(color.Black)
		first := photo()
		undo(t, a, upload(color.White)) // replaced, then undone
		if photo() != first {
			t.Error("undoing the replace did not bring the first photo back")
		}
		undo(t, a, saveFresh(t, a, item, item+"/photo/delete", nil))
		if photo() != first {
			t.Error("undoing the remove did not bring the photo back")
		}
	})

	t.Run("an archived item's list entry", func(t *testing.T) {
		a := newApp(t)
		a.seed()
		oil := itemID(t, a.store, "Silkolene")
		item := itemPath(oil)
		expect(t, a.post(item+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"}, "total_price": {"20"}}),
			http.StatusSeeOther) // in use: archived, not deleted
		expect(t, a.post(item+"/shopping", url.Values{"quantity": {"1"}, "quantity_unit": {"l"}}), http.StatusSeeOther)
		rec := saveFresh(t, a, item, item+"/archive", nil)
		if f := flashOf(t, rec); !strings.Contains(f.Message, "Its shopping list entry is in the trash") {
			t.Errorf("archive flash = %+v", f)
		}
		saveFresh(t, a, item, item+"/restore", nil)
		trash, _ := a.store.ListTrash(ctx)
		if len(trash) != 1 || trash[0].Kind != store.TrashShopping {
			t.Fatalf("trash = %+v", trash)
		}
		expect(t, a.post("/trash/"+itoa(trash[0].ID)+"/restore", nil), http.StatusSeeOther)
		list, _ := a.store.ListShopping(ctx)
		if len(list) != 1 || list[0].ItemID == nil || *list[0].ItemID != oil {
			t.Fatalf("shopping = %+v", list)
		}
		// The restored entry can be bought from a fresh page.
		page := "/shopping/" + itoa(list[0].ID) + "/bought"
		saveFresh(t, a, page, page, url.Values{"amount": {"1"}, "amount_unit": {"l"}})
	})
}
