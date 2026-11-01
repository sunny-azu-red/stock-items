package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

// loggedServer is a Server on st whose log is kept in the returned buffer.
func loggedServer(t *testing.T, st *store.Store) (*Server, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	srv, err := New(st, Options{Version: "test", Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return srv, &buf
}

func TestMalformedFormsAre400(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	for _, tt := range []struct{ path, contentType, body string }{
		{"/vehicles", "application/x-www-form-urlencoded", "name=%zz"},
		{"/vehicles/" + itoa(k390), "application/x-www-form-urlencoded", "name=a&notes=%"},
		{"/items", "application/x-www-form-urlencoded", "name=%G0"},
		{"/shopping", ";;;", "free_text=Oil"},
	} {
		req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
		req.Header.Set("Content-Type", tt.contentType)
		expect(t, a.do(req), http.StatusBadRequest, "could not be read")
	}
	// Too large is still 413.
	TestOversizedFormIs413(t)
}

func TestARequestTheClientLeftIsLogged499(t *testing.T) {
	srv, log := loggedServer(t, store.New(testdb.New(t)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stock", nil).WithContext(ctx))
	if !strings.Contains(log.String(), "status=499") {
		t.Errorf("log = %q, want status=499", log)
	}
	if strings.Contains(log.String(), "level=ERROR") {
		t.Errorf("a client that went away is not an error: %q", log)
	}
}

func TestHealthWithTheDatabaseDown(t *testing.T) {
	// Nothing listens on port 1: every connection is refused at once.
	pool, err := pgxpool.New(context.Background(), "postgres://pitbox:x@127.0.0.1:1/none?connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	srv, _ := loggedServer(t, store.New(pool))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	expect(t, rec, http.StatusServiceUnavailable, `"database":"down"`)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestHtmxGetsHXRedirect(t *testing.T) {
	a := newApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"VW Golf"}}, "HX-Request", "true")
	expect(t, rec, http.StatusNoContent)
	to := rec.Header().Get("HX-Redirect")
	if !strings.HasPrefix(to, "/vehicles/") || rec.Header().Get("Location") != "" {
		t.Errorf("HX-Redirect = %q, Location = %q; want only HX-Redirect to the vehicle", to, rec.Header().Get("Location"))
	}
	if f := flashOf(t, rec); f.Kind != "success" || f.Message == "" {
		t.Errorf("flash = %+v, want a success message for the page it goes to", f)
	}
	// Without htmx, the same answer is a 303.
	rec = a.post("/vehicles", url.Values{"name": {"VW Polo"}})
	expect(t, rec, http.StatusSeeOther)
	if rec.Header().Get("HX-Redirect") != "" {
		t.Error("a plain form got an HX-Redirect")
	}
}

func TestRecoverPanics(t *testing.T) {
	srv, log := loggedServer(t, nil)
	h := srv.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	expect(t, rec, http.StatusInternalServerError, "internal server error")
	if !strings.Contains(log.String(), "panic=boom") || !strings.Contains(log.String(), "path=/x") {
		t.Errorf("log = %q, want the panic and the path", log)
	}

	// http.ErrAbortHandler is how a handler aborts on purpose: the server
	// must see it, so it goes on up.
	h = srv.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler passed on", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	t.Error("ErrAbortHandler was swallowed")
}

func TestQuickAdjustOfADeletedItem(t *testing.T) {
	a := newApp(t)
	a.seed()
	pads := itemID(t, a.store, "Brake Pads")
	path := "/items/" + itoa(pads)
	expect(t, a.postV(path+"/delete", nil), http.StatusSeeOther)

	// htmx swaps no 404 page into the row: it goes where the item went,
	// and says why there.
	rec := a.post(path+"/adjust", url.Values{"dir": {"up"}}, "HX-Request", "true")
	expect(t, rec, http.StatusNoContent)
	if to := rec.Header().Get("HX-Redirect"); to != "/trash" {
		t.Errorf("HX-Redirect = %q, want /trash", to)
	}
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "deleted") {
		t.Errorf("flash = %+v, want that it was deleted", f)
	}
	// Without htmx too, rather than a 404.
	rec = a.post(path+"/adjust", url.Values{"dir": {"down"}})
	expect(t, rec, http.StatusSeeOther)
	if to := rec.Header().Get("Location"); to != "/trash" {
		t.Errorf("Location = %q, want /trash", to)
	}
}

func TestKitShortfallSkipsAnAmountTheListRefuses(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	pads, oil := itemID(t, a.store, "Brake Pads"), itemID(t, a.store, "Silkolene")
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"},
		"line_item": {itoa(pads), itoa(oil)}, "line_qty": {"2", "1 L"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	// A line saved before pieces had to be whole: 1.5 pads short, which
	// the shopping list refuses (stock.CheckQuantity).
	if _, err := a.pool.Exec(ctx, `UPDATE service_kit_lines SET quantity = 2.5 WHERE kit_id = $1 AND item_id = $2`, mustID(t, kit), pads); err != nil {
		t.Fatal(err)
	}
	rec = a.post(kit+"/shortfall", nil)
	expect(t, rec, http.StatusSeeOther)
	if f := flashOf(t, rec); f.Message != "Added 1 missing part to the shopping list" {
		t.Errorf("flash = %+v, want the oil added", f)
	}
	list := mustShopping(t, a)
	if len(list) != 1 || list[0].ItemID == nil || *list[0].ItemID != oil {
		t.Errorf("shopping list = %+v, want only the oil", list)
	}
}

func TestLinkingADeletedItemSaysSo(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{FreeText: "silkolene 15w50 pro 4 xp"})
	page := "/shopping/" + itoa(entry) + "/bought"
	link := "/shopping/" + itoa(entry) + "/link"
	version := a.versionOf(link) // the page offers the oil

	// Deleted in another tab before the click.
	expect(t, a.postV("/items/"+itoa(oil)+"/delete", nil), http.StatusSeeOther)
	rec := a.post(link, url.Values{"item_id": {itoa(oil)}, "version": {version}})
	expect(t, rec, http.StatusSeeOther)
	if to := rec.Header().Get("Location"); to != page {
		t.Errorf("Location = %q, want the entry's Bought page %s", to, page)
	}
	if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "deleted") {
		t.Errorf("flash = %+v, want that the item was deleted", f)
	}
	expect(t, a.get(page), http.StatusOK)
	if e, _ := a.store.GetShopping(ctx, entry); e.ItemID != nil {
		t.Errorf("entry linked to %v", *e.ItemID)
	}
	// A forged click without an item, and one of an entry that is gone too.
	rec = a.post(link, url.Values{"version": {version}})
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.postV("/shopping/"+itoa(entry)+"/delete", nil), http.StatusSeeOther)
	rec = a.post(link, url.Values{"item_id": {itoa(oil)}, "version": {version}})
	expect(t, rec, http.StatusSeeOther)
	if to := rec.Header().Get("Location"); to != "/shopping" {
		t.Errorf("entry gone too: Location = %q, want /shopping", to)
	}
}

// The item page's forms and buttons, and a kit's, of a record deleted in
// another tab say where it went rather than show Not found.
func TestFormsOfDeletedRecordsGoToTheTrash(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	item := itemPath(itemID(t, a.store, "Brake Pads"))
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	expect(t, a.postV(item+"/delete", nil), http.StatusSeeOther)
	expect(t, a.postV(kit+"/delete", nil), http.StatusSeeOther)
	for _, tt := range []struct {
		action string
		form   url.Values
	}{
		{item + "/movements", url.Values{"kind": {"purchase"}, "amount": {"2"}}},
		{item + "/shopping", url.Values{"quantity": {"2"}}},
		{item + "/photo/delete", nil},
		{kit + "/shortfall", nil},
	} {
		rec := a.post(tt.action, tt.form)
		if to := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || to != "/trash" {
			t.Errorf("%s: %d to %q, want 303 to /trash", tt.action, rec.Code, to)
		}
		if f := flashOf(t, rec); f.Kind != "error" || !strings.Contains(f.Message, "was deleted since you opened this") {
			t.Errorf("%s: flash = %+v", tt.action, f)
		}
	}
}
