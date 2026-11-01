package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

// With APP_ALLOWED_HOSTS set, a request for any other host is refused
// before it does anything: a page whose name was pointed at the server
// (DNS rebinding) can't read or change the data.
func TestAllowedHosts(t *testing.T) {
	st := store.New(testdb.New(t))
	handler := func(hosts ...string) http.Handler {
		srv, err := New(st, Options{Version: "test", AllowedHosts: hosts, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err != nil {
			t.Fatal(err)
		}
		return srv.Handler()
	}
	h := handler("Pitbox.lan", "192.168.1.5:8080", "[fe80::1]")
	for _, tt := range []struct {
		host, path string
		want       int
	}{
		{"pitbox.lan", "/", http.StatusOK},
		{"PITBOX.LAN:8080", "/stock", http.StatusOK},
		{"pitbox.lan.", "/", http.StatusOK},
		{"192.168.1.5:8080", "/", http.StatusOK},
		{"192.168.1.5:9999", "/", http.StatusOK}, // the port doesn't matter
		{"[fe80::1]:8080", "/", http.StatusOK},
		{"127.0.0.1:8080", "/", http.StatusOK}, // loopback always works
		{"127.0.0.2", "/", http.StatusOK},
		{"localhost:8080", "/", http.StatusOK},
		{"[::1]:8080", "/", http.StatusOK},
		{"evil.example", "/healthz", http.StatusOK}, // the Docker healthcheck asks on HOST
		{"evil.example", "/", http.StatusMisdirectedRequest},
		{"evil.example:8080", "/stock/export.csv", http.StatusMisdirectedRequest},
		{"pitbox.lan.evil.example", "/", http.StatusMisdirectedRequest},
		{"evil.localhost", "/", http.StatusMisdirectedRequest},
		{"192.168.1.6", "/", http.StatusMisdirectedRequest},
		{"", "/", http.StatusMisdirectedRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		req.Host = tt.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("Host %q, GET %s = %d, want %d", tt.host, tt.path, rec.Code, tt.want)
		}
		if tt.want == http.StatusMisdirectedRequest && !strings.Contains(rec.Body.String(), "APP_ALLOWED_HOSTS") {
			t.Errorf("Host %q: the refusal doesn't say what to set: %q", tt.host, rec.Body.String())
		}
	}

	// A same-origin form post from the rebound page saves nothing.
	post := func(h http.Handler, host string) int {
		form := url.Values{"name": {"From " + host}, "form_key": {newFormKey()}}
		req := httptest.NewRequest(http.MethodPost, "/vehicles", strings.NewReader(form.Encode()))
		req.Host = host
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", "http://"+host)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post(h, "evil.example:8080"); code != http.StatusMisdirectedRequest {
		t.Errorf("rebound POST = %d, want 421", code)
	}
	if code := post(h, "pitbox.lan:8080"); code != http.StatusSeeOther {
		t.Errorf("POST from an allowed host = %d, want 303", code)
	}
	if vs, _ := st.ListVehicles(context.Background()); len(vs) != 1 || vs[0].Name != "From pitbox.lan:8080" {
		t.Errorf("vehicles = %+v, want only the allowed host's", vs)
	}

	// Unset, as before: any host.
	if code := post(handler(), "evil.example:8080"); code != http.StatusSeeOther {
		t.Errorf("without APP_ALLOWED_HOSTS, POST = %d, want 303", code)
	}
}

func TestHostName(t *testing.T) {
	for in, want := range map[string]string{
		"pitbox.lan":        "pitbox.lan",
		"Pitbox.LAN:8080":   "pitbox.lan",
		"pitbox.lan.":       "pitbox.lan",
		"192.168.1.5:80":    "192.168.1.5",
		"[::1]:8080":        "::1",
		"[FE80::1]":         "fe80::1",
		"fe80:0::1":         "fe80::1",
		"[::ffff:10.0.0.1]": "10.0.0.1",
		"":                  "",
	} {
		if got := hostName(in); got != want {
			t.Errorf("hostName(%q) = %q, want %q", in, got, want)
		}
	}
}
