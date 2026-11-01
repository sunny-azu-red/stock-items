package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// These tests post a form twice with the one-time key its page carries:
// at once, after the first save went through, and after it failed, all
// over HTTP, the way a browser retries.

// formKeys returns the keys of the forms posting to action on page, in
// page order (none for forms without one).
func formKeys(t *testing.T, a *testApp, page, action string) []string {
	t.Helper()
	rec := a.get(page)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", page, rec.Code)
	}
	return keysIn(t, rec.Body.String(), action)
}

// keysIn returns the keys of the forms posting to action in body.
func keysIn(t *testing.T, body, action string) []string {
	t.Helper()
	var keys []string
	for _, f := range formsPosting(t, body, action) {
		if k := regexp.MustCompile(`name="form_key" value="([0-9a-f]{32})"`).FindStringSubmatch(f); k != nil {
			keys = append(keys, k[1])
		}
	}
	return keys
}

// formKey opens page and returns the key of its one form posting to action.
func formKey(t *testing.T, a *testApp, page, action string) string {
	t.Helper()
	keys := formKeys(t, a, page, action)
	if len(keys) != 1 {
		t.Fatalf("the form posting to %s on %s carries %d keys", action, page, len(keys))
	}
	return keys[0]
}

// with returns fields plus the form key and version a page's form carried.
func with(fields url.Values, key, version string) url.Values {
	form := url.Values{"form_key": {key}}
	for k, v := range fields {
		form[k] = append([]string(nil), v...)
	}
	if version != "" {
		form.Set("version", version)
	}
	return form
}

// ledger fails if an item's quantity differs from its stock changes.
func (a *testApp) ledger(t *testing.T) {
	t.Helper()
	var off int
	err := a.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM items i
		WHERE i.quantity <> COALESCE((SELECT sum(delta) FROM stock_movements m WHERE m.item_id = i.id), 0)`).Scan(&off)
	if err != nil || off != 0 {
		t.Fatalf("ledger: %d items off (%v)", off, err)
	}
}

func (a *testApp) quantity(t *testing.T, id int64) float64 {
	t.Helper()
	it, err := a.store.GetItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it.Quantity
}

func expectSavedAlready(t *testing.T, rec *httptest.ResponseRecorder, to string) {
	t.Helper()
	expect(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != to {
		t.Errorf("repeat went to %s, want %s", got, to)
	}
	if f := flashOf(t, rec); f.Message != savedAlreadyMsg {
		t.Errorf("repeat flash = %+v, want saved already", f)
	}
}

// Every form that creates or changes something carries a key of its own;
// quick -/+ doesn't.
func TestFormsCarryFormKeys(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	seen := map[string]bool{}
	check := func(page, action string, want int) {
		t.Helper()
		keys := formKeys(t, a, page, action)
		if len(keys) != want {
			t.Errorf("%s: %d forms posting to %s carry a key, want %d", page, len(keys), action, want)
		}
		for _, k := range keys {
			if seen[k] {
				t.Errorf("%s: key %s used twice", page, k)
			}
			seen[k] = true
		}
	}
	check("/items/new", "/items", 1)
	check("/vehicles/new", "/vehicles", 1)
	check("/kits/new", "/kits", 1)
	check("/services/new", "/services", 1)
	check("/shopping", "/shopping", 1)
	for _, r := range records() {
		action, _ := r.make(t, a)
		check(action+"/edit", action, 1)
	}
	oil := itemPath(itemID(t, a.store, "Silkolene"))
	check(oil, oil+"/movements", 3) // bought, used, count
	check(oil, oil+"/shopping", 1)
	check("/stock", oil+"/adjust", 0)
	pads := itemID(t, a.store, "Rear Brake")
	entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads})
	check("/shopping/"+itoa(entry)+"/bought", "/shopping/"+itoa(entry)+"/bought", 1)
	free, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "Zip ties"})
	check("/items/new?shopping="+itoa(free), "/items", 1)
}

// Two posts of one form at once (a double click without JavaScript, or a
// retry while the first is still saving) log one service and take its
// parts once, and both end on its page. The first holds its claim on the
// key while it waits for a lock; the second waits for the first.
func TestConcurrentSavesWithOneKeyMakeOneRecord(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	key := formKey(t, a, "/services/new?vehicle="+itoa(k390), "/services")
	form := with(url.Values{"vehicle_id": {itoa(k390)}, "title": {"Oil change"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil)}, "part_qty": {"100"}}, key, "")

	// Hold the vehicle, which logging a service locks after claiming the key.
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM vehicles WHERE id = $1 FOR UPDATE`, k390); err != nil {
		t.Fatal(err)
	}
	var held string
	if err := tx.QueryRow(ctx, `SELECT transactionid::text FROM pg_locks WHERE pid = pg_backend_pid() AND locktype = 'transactionid'`).
		Scan(&held); err != nil {
		t.Fatal(err)
	}

	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	send := func(i int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs[i] = a.post("/services", form)
		}()
	}
	send(0)
	first := a.waiter(t, held) // claimed the key, waits for the vehicle
	send(1)
	a.waiting(t, 2, "vehicles", held, first) // waits for the first's claim (or, unclaimed, the vehicle)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	services, err := a.store.ListServices(ctx, store.ServiceFilter{})
	if err != nil || len(services) != 1 {
		t.Fatalf("services = %+v, %v; want one", services, err)
	}
	page := servicePath(services[0].ID)
	expect(t, recs[0], http.StatusSeeOther)
	if got := recs[0].Header().Get("Location"); got != page {
		t.Errorf("first went to %s, want %s", got, page)
	}
	if f := flashOf(t, recs[0]); f.Message != "Logged Oil change" {
		t.Errorf("first flash = %+v", f)
	}
	expectSavedAlready(t, recs[1], page)
	if got := a.quantity(t, oil); got != 425 {
		t.Errorf("oil = %v, want 425: the parts taken once", got)
	}
	a.ledger(t)
}

// waiter waits until a database session waits for transaction xid, and
// returns that session's own transaction id ("" for none yet).
func (a *testApp) waiter(t *testing.T, xid string) string {
	t.Helper()
	ctx := context.Background()
	for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(5 * time.Millisecond) {
		var own string
		err := a.pool.QueryRow(ctx, `
			SELECT COALESCE(o.transactionid::text, '') FROM pg_locks w
			LEFT JOIN pg_locks o ON o.pid = w.pid AND o.locktype = 'transactionid' AND o.granted

			WHERE w.locktype = 'transactionid' AND NOT w.granted AND w.transactionid::text = $1`, xid).Scan(&own)
		if err == nil {
			return own
		}
	}
	t.Fatalf("nothing waits for transaction %s", xid)
	return ""
}

// waiting waits until n database sessions wait for any of the transactions
// xids, or for a row of table (behind another session waiting for it).
func (a *testApp) waiting(t *testing.T, n int, table string, xids ...string) {
	t.Helper()
	ctx := context.Background()
	var got int
	for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(5 * time.Millisecond) {
		err := a.pool.QueryRow(ctx, `SELECT count(DISTINCT pid) FROM pg_locks WHERE NOT granted
			AND (locktype = 'transactionid' AND transactionid::text = ANY($1) OR locktype = 'tuple' AND relation = $2::regclass)`,
			xids, table).Scan(&got)

		if err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
	}
	t.Fatalf("%d sessions wait for transactions %v, want %d", got, xids, n)
}

// The same form posted again after its first save went through (Back and
// Save, or a retry whose first try did arrive) changes nothing and goes to
// what the first save made, even with other values typed meanwhile.
func TestRepeatAfterTheSaveChangesNothing(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name         string
		page, action string
		fields       func(a *testApp) url.Values
		changed      func(a *testApp) url.Values // what was typed before the retry
		to           func(a *testApp) string
		check        func(t *testing.T, a *testApp)
	}{
		{
			name: "purchase", page: "/items/{oil}", action: "/items/{oil}/movements",
			fields: func(a *testApp) url.Values {
				return url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"}, "total_price": {"20"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"kind": {"purchase"}, "amount": {"2"}, "amount_unit": {"l"}, "total_price": {"40"}}
			},
			to: func(a *testApp) string { return itemPath(itemID(a.t, a.store, "Silkolene")) },
			check: func(t *testing.T, a *testApp) {
				if got := a.quantity(t, itemID(t, a.store, "Silkolene")); got != 1525 {
					t.Errorf("oil = %v, want 1525", got)
				}
			},
		},
		{
			name: "use", page: "/items/{oil}", action: "/items/{oil}/movements",
			fields: func(a *testApp) url.Values {
				return url.Values{"kind": {"use"}, "amount": {"100"}, "amount_unit": {"ml"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"kind": {"use"}, "amount": {"200"}, "amount_unit": {"ml"}}
			},
			to: func(a *testApp) string { return itemPath(itemID(a.t, a.store, "Silkolene")) },
			check: func(t *testing.T, a *testApp) {
				if got := a.quantity(t, itemID(t, a.store, "Silkolene")); got != 425 {
					t.Errorf("oil = %v, want 425", got)
				}
			},
		},
		{
			// Counted 500, then 400 used and the retry arrives: it must not set
			// the stock back to 500.
			name: "count", page: "/items/{oil}", action: "/items/{oil}/movements",
			fields: func(a *testApp) url.Values {
				return url.Values{"kind": {"correction"}, "amount": {"500"}, "amount_unit": {"ml"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"kind": {"correction"}, "amount": {"500"}, "amount_unit": {"ml"}}
			},
			to: func(a *testApp) string { return itemPath(itemID(a.t, a.store, "Silkolene")) },
			check: func(t *testing.T, a *testApp) {
				oil := itemID(t, a.store, "Silkolene")
				if got := a.quantity(t, oil); got != 500 {
					t.Errorf("oil = %v, want 500", got)
				}
			},
		},
		{
			name: "service", page: "/services/new", action: "/services",
			fields: func(a *testApp) url.Values {
				return url.Values{"vehicle_id": {itoa(a.vehicle(a.t, "KTM 390"))}, "title": {"Oil change"}, "performed_on": {"2026-10-29"},
					"part_item": {itoa(itemID(a.t, a.store, "Silkolene"))}, "part_qty": {"500"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"vehicle_id": {itoa(a.vehicle(a.t, "KTM 390"))}, "title": {"Oil change!"}, "performed_on": {"2026-10-29"},
					"part_item": {itoa(itemID(a.t, a.store, "Silkolene"))}, "part_qty": {"20"}}
			},
			to: func(a *testApp) string {
				s, _ := a.store.ListServices(ctx, store.ServiceFilter{})
				return servicePath(s[0].ID)
			},
			check: func(t *testing.T, a *testApp) {
				if s, _ := a.store.ListServices(ctx, store.ServiceFilter{}); len(s) != 1 || s[0].Title != "Oil change" {
					t.Errorf("services = %+v", s)
				}
				if got := a.quantity(t, itemID(t, a.store, "Silkolene")); got != 25 {
					t.Errorf("oil = %v, want 25", got)
				}
			},
		},
		{
			name: "new item", page: "/items/new", action: "/items",
			fields: func(a *testApp) url.Values {
				return url.Values{"name": {"Chain lube"}, "unit": {"ml"}, "quantity": {"400"}, "quantity_unit": {"ml"}, "total_price": {"12"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"name": {"Chain lube"}, "unit": {"ml"}, "quantity": {"800"}, "quantity_unit": {"ml"}, "next": {"new"}}
			},
			to: func(a *testApp) string { return itemPath(itemID(a.t, a.store, "Chain lube")) },
			check: func(t *testing.T, a *testApp) {
				items, _ := a.store.ListItems(ctx, store.ItemFilter{Query: "Chain lube"})
				if len(items) != 1 || items[0].Quantity != 400 {
					t.Errorf("items = %+v", items)
				}
			},
		},
		{
			name: "vehicle", page: "/vehicles/new", action: "/vehicles",
			fields: func(a *testApp) url.Values {
				return url.Values{"name": {"Golf"}, "kind": {"car"}, "color": {"#2563eb"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"name": {"Golf 2"}, "kind": {"car"}, "color": {"#2563eb"}}
			},
			to: func(a *testApp) string { return vehiclePath(a.vehicle(a.t, "Golf")) },
			check: func(t *testing.T, a *testApp) {
				if vs, _ := a.store.ListVehicles(ctx); len(vs) != 3 {
					t.Errorf("vehicles = %d, want 3", len(vs))
				}
			},
		},
		{
			name: "kit", page: "/kits/new", action: "/kits",
			fields: func(a *testApp) url.Values {
				return url.Values{"vehicle_id": {itoa(a.vehicle(a.t, "KTM 390"))}, "name": {"Oil service"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"vehicle_id": {itoa(a.vehicle(a.t, "KTM 390"))}, "name": {"Oil"}}
			},
			to: func(a *testApp) string {
				k, _ := a.store.ListKits(ctx, store.KitFilter{})
				return kitPath(k[0].ID)
			},
			check: func(t *testing.T, a *testApp) {
				if k, _ := a.store.ListKits(ctx, store.KitFilter{}); len(k) != 1 || k[0].Name != "Oil service" {
					t.Errorf("kits = %+v", k)
				}
			},
		},
		{
			name: "shopping list", page: "/shopping", action: "/shopping",
			fields: func(a *testApp) url.Values {
				return url.Values{"item_id": {itoa(itemID(a.t, a.store, "Silkolene"))}, "quantity": {"1"}, "quantity_unit": {"l"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"item_id": {itoa(itemID(a.t, a.store, "Silkolene"))}, "quantity": {"1"}, "quantity_unit": {"l"}}
			},
			to: func(a *testApp) string { return "/shopping" },
			check: func(t *testing.T, a *testApp) {
				if l := mustShopping(t, a); len(l) != 1 || *l[0].Quantity != 1000 {
					t.Errorf("shopping = %+v, want 1 L once", l)
				}
			},
		},
		{
			name: "item to the shopping list", page: "/items/{oil}", action: "/items/{oil}/shopping",
			fields:  func(a *testApp) url.Values { return url.Values{"quantity": {"1"}, "quantity_unit": {"l"}} },
			changed: func(a *testApp) url.Values { return url.Values{"quantity": {"1"}, "quantity_unit": {"l"}} },
			to:      func(a *testApp) string { return itemPath(itemID(a.t, a.store, "Silkolene")) },
			check: func(t *testing.T, a *testApp) {
				if l := mustShopping(t, a); len(l) != 1 || *l[0].Quantity != 1000 {
					t.Errorf("shopping = %+v, want 1 L once", l)
				}
			},
		},
		{
			name: "bought", page: "/shopping/{entry}/bought", action: "/shopping/{entry}/bought",
			fields: func(a *testApp) url.Values {
				return url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"20"}}
			},
			changed: func(a *testApp) url.Values {
				return url.Values{"amount": {"2"}, "amount_unit": {"l"}, "total_price": {"20"}}
			},
			to: func(a *testApp) string { return "/shopping" },
			check: func(t *testing.T, a *testApp) {
				if got := a.quantity(t, itemID(t, a.store, "Silkolene")); got != 1525 {
					t.Errorf("oil = %v, want 1525", got)
				}
				if l := mustShopping(t, a); len(l) != 1 || *l[0].Quantity != 1000 {
					t.Errorf("shopping = %+v, want 1 L left", l)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			oil := itemID(t, a.store, "Silkolene")
			entry, err := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(2000.0)})
			if err != nil {
				t.Fatal(err)
			}
			if c.name != "bought" {
				if _, err := a.store.DeleteShopping(ctx, entry, 1); err != nil {
					t.Fatal(err)
				}
			}
			fill := strings.NewReplacer("{oil}", itoa(oil), "{entry}", itoa(entry))
			page, action := fill.Replace(c.page), fill.Replace(c.action)
			keys := formKeys(t, a, page, action)
			if len(keys) == 0 {
				t.Fatalf("no key on %s", page)
			}
			fields := c.fields(a)
			key := keys[0]
			if kind := fields.Get("kind"); kind == "use" {
				key = keys[1]
			} else if kind == "correction" {
				key = keys[2]
			}
			version := ""
			if c.name == "bought" {
				version = "1"
			}
			first := a.post(action, with(fields, key, version))
			expect(t, first, http.StatusSeeOther)
			if f := flashOf(t, first); f.Kind == "error" || f.Message == savedAlreadyMsg {
				t.Fatalf("first save: %+v", f)
			}
			if c.name == "count" {
				// Used meanwhile: a repeated count must not undo it.
				expect(t, a.post(itemPath(oil)+"/movements", url.Values{"kind": {"use"}, "amount": {"100"}, "amount_unit": {"ml"}}),
					http.StatusSeeOther)
			}
			expectSavedAlready(t, a.post(action, with(c.changed(a), key, version)), c.to(a))
			if c.name == "count" {
				if got := a.quantity(t, oil); got != 400 {
					t.Errorf("oil = %v after the repeat, want 400: the count once, then the use", got)
				}
			} else {
				c.check(t, a)
			}
			a.ledger(t)
		})
	}
}

// A save that failed (a shortfall, a field to fix, a name taken) saved
// nothing: the form comes back with the same key, and saving it again
// goes through.
func TestRepeatAfterAFailedSaveSaves(t *testing.T) {
	ctx := context.Background()
	t.Run("not enough on stock", func(t *testing.T) {
		a := newApp(t)
		a.seed()
		oil := itemID(t, a.store, "Silkolene")
		page := itemPath(oil)
		key := formKeys(t, a, page, page+"/movements")[1] // used
		rec := a.post(page+"/movements", with(url.Values{"kind": {"use"}, "amount": {"1"}, "amount_unit": {"l"}}, key, ""))
		expect(t, rec, http.StatusUnprocessableEntity, "Only 525 ml on stock")
		if keys := keysIn(t, rec.Body.String(), page+"/movements"); len(keys) != 3 || keys[1] != key {
			t.Errorf("keys after the error = %v, want %s kept on the use form", keys, key)
		}
		rec = a.post(page+"/movements", with(url.Values{"kind": {"use"}, "amount": {"500"}, "amount_unit": {"ml"}}, key, ""))
		expect(t, rec, http.StatusSeeOther)
		if f := flashOf(t, rec); f.Message != "Took 500 ml from stock" {
			t.Errorf("flash = %+v", f)
		}
		if got := a.quantity(t, oil); got != 25 {
			t.Errorf("oil = %v, want 25", got)
		}
		a.ledger(t)
	})
	t.Run("a part short", func(t *testing.T) {
		a := newApp(t)
		k390, _ := a.seed()
		oil := itemID(t, a.store, "Silkolene")
		key := formKey(t, a, "/services/new", "/services")
		fields := url.Values{"vehicle_id": {itoa(k390)}, "title": {"Oil change"}, "performed_on": {"2026-10-29"},
			"part_item": {itoa(oil)}, "part_qty": {"2"}, "part_unit": {"l"}}
		rec := a.post("/services", with(fields, key, ""))
		expect(t, rec, http.StatusUnprocessableEntity, "1 part is short")
		if keys := keysIn(t, rec.Body.String(), "/services"); len(keys) != 1 || keys[0] != key {
			t.Errorf("keys after the error = %v, want %s", keys, key)
		}
		fields.Set("part_qty", "500")
		fields.Set("part_unit", "ml")
		rec = a.post("/services", with(fields, key, ""))
		expect(t, rec, http.StatusSeeOther)
		if s, _ := a.store.ListServices(ctx, store.ServiceFilter{}); len(s) != 1 {
			t.Errorf("services = %+v", s)
		}
		a.ledger(t)
	})
	t.Run("a field to fix", func(t *testing.T) {
		a := newApp(t)
		a.seed()
		key := formKey(t, a, "/vehicles/new", "/vehicles")
		rec := a.post("/vehicles", with(url.Values{"name": {"KTM 390"}, "kind": {"motorcycle"}}, key, ""))
		expect(t, rec, http.StatusUnprocessableEntity, "A vehicle with this name already exists", key)
		rec = a.post("/vehicles", with(url.Values{"name": {"KTM 690"}, "kind": {"motorcycle"}}, key, ""))
		expect(t, rec, http.StatusSeeOther)
		a.vehicle(t, "KTM 690")
		rec = a.post("/vehicles", with(url.Values{"name": {""}, "kind": {"motorcycle"}}, newFormKey(), ""))
		expect(t, rec, http.StatusUnprocessableEntity, "Name is required")
	})
}

// An edit saved, then posted again (Back and Save, a retry): its own first
// save moved the version on, but the answer is "saved already", not
// "changed since you opened this", and the second post changes nothing.
func TestEditRetriedAfterItsSaveIsNotStale(t *testing.T) {
	for _, r := range records() {
		t.Run(r.name, func(t *testing.T) {
			a := newApp(t)
			a.seed()
			action, _ := r.make(t, a)
			version := openForm(t, a, action+"/edit", action)
			key := formKey(t, a, action+"/edit", action)
			first := a.post(action, with(r.fields(a, "First"), key, version))
			expect(t, first, http.StatusSeeOther)
			if f := flashOf(t, first); f.Kind == "error" || f.Message == savedAlreadyMsg {
				t.Fatalf("first save: %+v", f)
			}
			expectSavedAlready(t, a.post(action, with(r.fields(a, "Second"), key, version)), first.Header().Get("Location"))
			if got := r.note(a, action); got != "First" {
				t.Errorf("note = %q after the repeat, want the first save's", got)
			}
			a.ledger(t)
		})
	}
}

// A form without a key (opened before keys existed) is not saved: it comes
// back with what was typed and a key, and saves with one more click.
func TestFormsWithoutAKeyAreNotSaved(t *testing.T) {
	a := newApp(t)
	a.seed()
	oil := itemID(t, a.store, "Silkolene")
	page := itemPath(oil)
	rec := a.post(page+"/movements", url.Values{"form_key": {""}, "kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"},
		"total_price": {"23.45"}})
	expect(t, rec, http.StatusUnprocessableEntity, noFormKey, `value="23.45"`)
	if got := a.quantity(t, oil); got != 525 {
		t.Errorf("oil = %v, want 525: nothing saved", got)
	}
	keys := keysIn(t, rec.Body.String(), page+"/movements")
	if len(keys) != 3 {
		t.Fatalf("keys = %v", keys)
	}
	expect(t, a.post(page+"/movements", with(url.Values{"kind": {"purchase"}, "amount": {"1"}, "amount_unit": {"l"}}, keys[0], "")),
		http.StatusSeeOther)
	if got := a.quantity(t, oil); got != 1525 {
		t.Errorf("oil = %v, want 1525", got)
	}

	rec = a.post("/vehicles", url.Values{"form_key": {"not a key"}, "name": {"Golf"}, "kind": {"car"}})
	expect(t, rec, http.StatusUnprocessableEntity, noFormKey, `value="Golf"`)
	if vs, _ := a.store.ListVehicles(context.Background()); len(vs) != 2 {
		t.Errorf("vehicles = %d, want 2", len(vs))
	}
	expect(t, a.post(page+"/shopping", url.Values{"form_key": {""}}), http.StatusSeeOther)
	if l := mustShopping(t, a); len(l) != 0 {
		t.Errorf("shopping = %+v, want nothing added", l)
	}
	a.ledger(t)
}

// Undo and keys: a record deleted after it was saved is not made again by
// a repeat, which says where it went, and once restored the repeat goes
// to it. Undoing a Bought takes its purchase back, so its form is not
// "saved already"; it is stale, as the restored entry is newer.
func TestRepeatsAfterUndo(t *testing.T) {
	ctx := context.Background()
	t.Run("deleted, then restored", func(t *testing.T) {
		a := newApp(t)
		k390, _ := a.seed()
		oil := itemID(t, a.store, "Silkolene")
		key := formKey(t, a, "/services/new", "/services")
		form := with(url.Values{"vehicle_id": {itoa(k390)}, "title": {"Oil change"}, "performed_on": {"2026-10-29"},
			"part_item": {itoa(oil)}, "part_qty": {"500"}}, key, "")
		rec := a.post("/services", form)
		expect(t, rec, http.StatusSeeOther)
		page := rec.Header().Get("Location")
		rec = a.postV(page+"/delete", nil)
		undo := flashOf(t, rec).Undo
		if undo == 0 {
			t.Fatalf("delete flash = %+v", flashOf(t, rec))
		}

		rec = a.post("/services", form)
		expect(t, rec, http.StatusSeeOther)
		if got := rec.Header().Get("Location"); got != "/trash" {
			t.Errorf("repeat after the delete went to %s, want /trash", got)
		}
		if f := flashOf(t, rec); !strings.Contains(f.Message, "saved already, and deleted since; it's in the trash") {
			t.Errorf("flash = %+v", f)
		}
		if s, _ := a.store.ListServices(ctx, store.ServiceFilter{}); len(s) != 0 {
			t.Errorf("services = %+v, want none: not made again", s)
		}
		if got := a.quantity(t, oil); got != 525 {
			t.Errorf("oil = %v, want 525", got)
		}

		expect(t, a.post("/trash/"+itoa(undo)+"/restore", nil), http.StatusSeeOther)
		expectSavedAlready(t, a.post("/services", form), page)
		if s, _ := a.store.ListServices(ctx, store.ServiceFilter{}); len(s) != 1 {
			t.Errorf("services = %+v, want the restored one", s)
		}
		if got := a.quantity(t, oil); got != 25 {
			t.Errorf("oil = %v, want 25", got)
		}
		a.ledger(t)
	})
	t.Run("bought, then undone", func(t *testing.T) {
		a := newApp(t)
		a.seed()
		oil := itemID(t, a.store, "Silkolene")
		entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil, Quantity: ptr(1000.0)})
		page := "/shopping/" + itoa(entry) + "/bought"
		key := formKey(t, a, page, page)
		form := with(url.Values{"amount": {"1"}, "amount_unit": {"l"}, "total_price": {"23.45"}}, key, openForm(t, a, page, page))
		rec := a.post(page, form)
		expect(t, rec, http.StatusSeeOther)
		expect(t, a.post("/trash/"+itoa(flashOf(t, rec).Undo)+"/restore", nil), http.StatusSeeOther)

		rec = a.post(page, form)
		expect(t, rec, http.StatusUnprocessableEntity, "This shopping list entry was changed since you opened this", `value="23.45"`)
		if got := a.quantity(t, oil); got != 525 {
			t.Errorf("oil = %v, want 525: the purchase undone, not made again", got)
		}
		saveFresh(t, a, page, page, url.Values{"amount": {"1"}, "amount_unit": {"l"}})
		if got := a.quantity(t, oil); got != 1525 {
			t.Errorf("oil = %v, want 1525 after buying from a fresh page", got)
		}
		a.ledger(t)
	})
}

// Keys are kept for a day: a claim prunes older ones.
func TestOldFormKeysArePruned(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	old, recent := newFormKey(), newFormKey()
	if _, err := a.pool.Exec(ctx, `INSERT INTO form_submissions (key, created_at) VALUES ($1, now() - interval '25 hours'),
		($2, now() - interval '23 hours')`, old, recent); err != nil {
		t.Fatal(err)
	}
	expect(t, a.post("/vehicles", url.Values{"name": {"Golf"}, "kind": {"car"}}), http.StatusSeeOther)
	var keys []string
	rows, err := a.pool.Query(ctx, `SELECT key FROM form_submissions`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if len(keys) != 2 || strings.Contains(strings.Join(keys, " "), old) || !strings.Contains(strings.Join(keys, " "), recent) {
		t.Errorf("keys = %v, want the recent one and the new one", keys)
	}
}
