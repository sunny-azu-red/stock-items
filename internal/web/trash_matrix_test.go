package web

import (
	"context"
	"image/color"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// The trash round trip for every kind of record that goes there, all over
// HTTP, the way a browser does it: delete with the record's own button,
// Undo, delete again, Undo again, delete, purge. After every step the
// record shows (or not) where it should, the trash lists it (or not),
// every item's quantity is the sum of its stock changes, and a form
// opened before a delete is stale once Undo brought the record back.

// trashRecord is one record made for the round trip.
type trashRecord struct {
	// page shows the delete button, which posts to button with fields.
	page, button string
	fields       url.Values
	// shown reads the record's pages: is it there?
	shown func() bool
	// staleForm is a form, posting to staleAction with staleFields, that a
	// delete and its Undo make stale: an edit form, or the delete button
	// itself. "" for none (a photo has no version).
	staleForm, staleAction string
	staleFields            url.Values
}

// trashKinds are the kinds of trash entries (store.Trash*), each made the
// way a user would.
func trashKinds() []struct {
	kind  string
	setup func(t *testing.T, a *testApp) trashRecord
} {
	ctx := context.Background()
	rs := records()
	// fromRecord is a record with an edit form (see records).
	fromRecord := func(r record) func(t *testing.T, a *testApp) trashRecord {
		return func(t *testing.T, a *testApp) trashRecord {
			action, show := r.make(t, a)
			return trashRecord{page: show, button: action + "/delete", fields: url.Values{"back": {"/activity"}},
				shown:     func() bool { return a.get(action+"/edit").Code == http.StatusOK },
				staleForm: action + "/edit", staleAction: action, staleFields: r.fields(a, "Typed before the delete")}
		}
	}
	listed := func(a *testApp, page, text string) func() bool {
		return func() bool {
			rec := a.get(page)
			expect(a.t, rec, http.StatusOK)
			return strings.Contains(rec.Body.String(), text)
		}
	}
	return []struct {
		kind  string
		setup func(t *testing.T, a *testApp) trashRecord
	}{
		{store.TrashItem, func(t *testing.T, a *testApp) trashRecord {
			r := fromRecord(rs[0])(t, a)
			stockList := listed(a, "/stock", "15W50 PRO 4 XP")
			page := r.shown
			r.shown = func() bool { return page() && stockList() }
			return r
		}},
		{store.TrashVehicle, func(t *testing.T, a *testApp) trashRecord {
			r := fromRecord(rs[1])(t, a)
			list := listed(a, "/vehicles", "KTM 1290")
			page := r.shown
			r.shown = func() bool { return page() && list() }
			return r
		}},
		{store.TrashKit, fromRecord(rs[2])},
		{store.TrashService, fromRecord(rs[3])},
		{store.TrashMovement, fromRecord(rs[4])},
		{store.TrashShopping, func(t *testing.T, a *testApp) trashRecord {
			expect(t, a.post("/shopping", url.Values{"free_text": {"Zip ties"}}), http.StatusSeeOther)
			list := mustShopping(t, a)
			del := "/shopping/" + itoa(list[0].ID) + "/delete"
			// Its stale form is the remove button itself.
			return trashRecord{page: "/shopping", button: del, shown: listed(a, "/shopping", `action="`+del+`"`),
				staleForm: "/shopping", staleAction: del}
		}},
		{store.TrashBought, func(t *testing.T, a *testApp) trashRecord {
			oil := itemID(t, a.store, "Silkolene")
			expect(t, a.post(itemPath(oil)+"/shopping", url.Values{"quantity": {"1"}, "quantity_unit": {"l"}}), http.StatusSeeOther)
			entry := "/shopping/" + itoa(mustShopping(t, a)[0].ID)
			bought := entry + "/bought"
			buy := url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"20"}}
			// Bought takes the entry off the list: its "delete". (The item's
			// name may stay on the page, among the suggestions.)
			return trashRecord{page: bought, button: bought, fields: buy, shown: listed(a, "/shopping", `action="`+entry+`/delete"`),
				staleForm: bought, staleAction: bought, staleFields: buy}
		}},
		{store.TrashPhoto, func(t *testing.T, a *testApp) trashRecord {
			needVips(t)
			item := itemPath(itemID(t, a.store, "Silkolene"))
			rec := a.do(photoUpload(t, ctx, item, smallPNG(t, color.Black)))
			if f := flashOf(t, rec); f.Message != "Photo saved" {
				t.Fatalf("upload: %+v", f)
			}
			return trashRecord{page: item, button: item + "/photo/delete",
				shown: func() bool { return a.get(item+"/photo").Code == http.StatusOK }}
		}},
	}
}

func TestTrashRoundTrip(t *testing.T) {
	kinds := trashKinds()
	if len(kinds) != 8 {
		t.Fatalf("%d kinds: add the new kind of trash entry here", len(kinds))
	}
	for _, k := range kinds {
		t.Run(k.kind, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			r := k.setup(t, a)
			step := func(name string, shown bool) {
				t.Helper()
				if got := r.shown(); got != shown {
					t.Fatalf("after %s: shown = %v, want %v", name, got, shown)
				}
				a.ledger(t)
			}
			step("making it", true)

			for round := 1; round <= 3; round++ {
				before := a.quantities(t)
				stale := ""
				if r.staleForm != "" {
					stale = openForm(t, a, r.staleForm, r.staleAction)
				}

				// Delete, with the version the page's button carries.
				rec := saveFresh(t, a, r.page, r.button, r.fields)
				f := flashOf(t, rec)
				if f.Undo == 0 {
					t.Fatalf("round %d: the delete offered no Undo: %+v", round, f)
				}
				step("the delete", false)
				trash := a.get("/trash")
				expect(t, trash, http.StatusOK, `action="/trash/`+itoa(f.Undo)+`/restore"`)
				if round == 3 {
					// Purge: gone for good, and the Undo button does nothing.
					expect(t, a.post("/trash/"+itoa(f.Undo)+"/purge", nil), http.StatusSeeOther)
					step("the purge", false)
					expectNot(t, a.get("/trash"), `action="/trash/`+itoa(f.Undo)+`/`)
					rec = a.post("/trash/"+itoa(f.Undo)+"/restore", nil)
					if f := flashOf(t, rec); rec.Code != http.StatusSeeOther || !strings.Contains(f.Message, "permanently deleted already") {
						t.Errorf("Undo after the purge: %d %+v", rec.Code, f)
					}
					step("an Undo after the purge", false)
					break
				}

				// Undo, from the flash.
				rec = a.post("/trash/"+itoa(f.Undo)+"/restore", nil)
				expect(t, rec, http.StatusSeeOther)
				if f := flashOf(t, rec); f.Kind == "error" {
					t.Fatalf("round %d: Undo: %s", round, f.Message)
				}
				step("the Undo", true)
				expectNot(t, a.get("/trash"), `action="/trash/`+itoa(f.Undo)+`/`)
				if after := a.quantities(t); !mapsEqual(after, before) {
					t.Errorf("round %d: stock after the Undo = %v, want as before the delete %v", round, after, before)
				}

				// The form opened before the delete is stale now, and does
				// nothing.
				if r.staleForm != "" {
					expectStale(t, submit(a, r.staleAction, stale, r.staleFields))
					step("the stale form", true)
					if after := a.quantities(t); !mapsEqual(after, before) {
						t.Errorf("round %d: the stale form changed the stock: %v, want %v", round, after, before)
					}
				}
			}
		})
	}
}

// An item with purchases, or a vehicle with a service, is archived, not
// deleted: the page offers Archive, a hand-made delete takes nothing to
// the trash, and Restore brings it back to the lists.
func TestInUseIsArchivedNotDeleted(t *testing.T) {
	for _, tt := range []struct {
		name string
		use  func(t *testing.T, a *testApp) (path, list, text string)
	}{
		{"item with a purchase", func(t *testing.T, a *testApp) (string, string, string) {
			item := itemPath(itemID(t, a.store, "Silkolene"))
			expect(t, a.post(item+"/movements", url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
				"total_price": {"20"}}), http.StatusSeeOther)
			return item, "/stock", "15W50 PRO 4 XP"
		}},
		{"vehicle with a service", func(t *testing.T, a *testApp) (string, string, string) {
			vehicle := "/vehicles/" + itoa(a.vehicle(t, "KTM 1290"))
			expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(a.vehicle(t, "KTM 1290"))}, "title": {"Chain clean"},
				"performed_on": {"2026-10-29"}}), http.StatusSeeOther)
			return vehicle, "/vehicles", "KTM 1290"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			path, list, text := tt.use(t, a)
			page := a.get(path)
			expect(t, page, http.StatusOK, `action="`+path+`/archive"`)
			expectNot(t, page, `action="`+path+`/delete"`)

			// A delete posted by hand (an old page) archives nothing and
			// deletes nothing.
			expect(t, a.post(path+"/delete", url.Values{"version": {a.versionOf(path + "/archive")}}), http.StatusSeeOther)
			expect(t, a.get(path), http.StatusOK)
			expect(t, a.get(list), http.StatusOK, text)

			before := a.quantities(t)
			rec := saveFresh(t, a, path, path+"/archive", nil)
			if f := flashOf(t, rec); f.Kind != "success" {
				t.Fatalf("archive: %+v", f)
			}
			expect(t, a.get(path), http.StatusOK, "Archived", `action="`+path+`/restore"`)
			expectNot(t, a.get(list), text)
			if trash, _ := a.store.ListTrash(context.Background()); len(trash) != 0 {
				t.Errorf("archiving put %+v in the trash", trash)
			}
			a.ledger(t)

			saveFresh(t, a, path, path+"/restore", nil)
			expect(t, a.get(list), http.StatusOK, text)
			expectNot(t, a.get(path), `action="`+path+`/restore"`)
			if after := a.quantities(t); !mapsEqual(after, before) {
				t.Errorf("stock = %v, want %v", after, before)
			}
			a.ledger(t)
		})
	}
}

// expectStale checks that a stale form or button was refused, saying so.
func expectStale(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	switch rec.Code {
	case http.StatusUnprocessableEntity: // an edit form, with what was typed
		expect(t, rec, http.StatusUnprocessableEntity, "was changed since you opened this")
	case http.StatusSeeOther: // a button
		if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "was changed since you opened it") {
			t.Errorf("stale button: flash = %+v", f)
		}
	default:
		t.Errorf("stale form: status %d", rec.Code)
	}
}

// quantities reads every item's stock, archived ones too.
func (a *testApp) quantities(t *testing.T) map[int64]float64 {
	t.Helper()
	rows, err := a.pool.Query(context.Background(), `SELECT id, quantity FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	q := map[int64]float64{}
	for rows.Next() {
		var id int64
		var qty float64
		if err := rows.Scan(&id, &qty); err != nil {
			t.Fatal(err)
		}
		q[id] = stock.RoundQty(qty)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return q
}

func mapsEqual(a, b map[int64]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
