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
