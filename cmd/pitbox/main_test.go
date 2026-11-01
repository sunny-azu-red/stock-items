package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"sync/atomic"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/config"
)

func TestCheckSchema(t *testing.T) {
	tests := []struct {
		schema, version string
		allow, wantErr  bool
	}{
		{"public", "", false, true},
		{"public", "", true, false},
		{"public", "docker", false, false},
		{"public", "v1.2.3", false, false},
		{"public", "v1.2.3-4-gabc1234", false, false},
		{"public", "v1.2.3-dirty", false, true},
		{"public", "abc1234-dirty", true, false},
		{"dev", "", false, false},
		{"dev", "v1.2.3-dirty", false, false},
		{"e2e_desktop", "", false, false},
	}
	for _, tt := range tests {
		err := checkSchema(tt.schema, tt.version, tt.allow)
		if (err != nil) != tt.wantErr {
			t.Errorf("checkSchema(%q, %q, %v) = %v, want error %v", tt.schema, tt.version, tt.allow, err, tt.wantErr)
		}
	}
}

func TestSchemaName(t *testing.T) {
	for in, want := range map[string]string{"": "public", "dev": "dev", "e2e_desktop": "e2e_desktop"} {
		if got := schemaName(config.DB{Schema: in}); got != want {
			t.Errorf("schemaName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v1.2.3"
	if got := buildVersion(); got != "v1.2.3" {
		t.Errorf("buildVersion() = %q, want the -ldflags version", got)
	}
	version = ""
	if got := buildVersion(); got == "" {
		t.Error("buildVersion() is empty without -ldflags")
	}

	rev := debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"}
	for _, tt := range []struct {
		settings []debug.BuildSetting
		want     string
	}{
		{nil, "dev"},
		{[]debug.BuildSetting{rev}, "0123456"},
		{[]debug.BuildSetting{rev, {Key: "vcs.modified", Value: "false"}}, "0123456"},
		{[]debug.BuildSetting{{Key: "vcs.modified", Value: "true"}, rev}, "0123456-dirty"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}, "abc"},
	} {
		if got := vcsVersion(tt.settings); got != tt.want {
			t.Errorf("vcsVersion(%v) = %q, want %q", tt.settings, got, tt.want)
		}
	}
}

// The Docker healthcheck asks the server on its own port; a server
// listening on every address ("", 0.0.0.0, ::) is asked on 127.0.0.1.
func TestCheckHealth(t *testing.T) {
	// Set and read across the server's goroutines.
	var status atomic.Int32
	var asked atomic.Value
	status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Store(r.Host)
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"", "0.0.0.0", "::", "127.0.0.1"} {
		if err := checkHealth(config.Config{Host: h, Port: port}); err != nil {
			t.Errorf("HOST=%q: %v", h, err)
		}
		if host := asked.Load(); host != net.JoinHostPort("127.0.0.1", port) {
			t.Errorf("HOST=%q: asked %q", h, host)
		}
	}

	status.Store(http.StatusServiceUnavailable)
	if err := checkHealth(config.Config{Host: "0.0.0.0", Port: port}); err == nil {
		t.Error("a 503 passed the healthcheck")
	}
	srv.Close()
	if err := checkHealth(config.Config{Host: "0.0.0.0", Port: port}); err == nil {
		t.Error("a server that is down passed the healthcheck")
	}
}
