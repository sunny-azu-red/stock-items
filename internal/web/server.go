// Package web is the HTTP layer: routing, middleware, handlers and the
// server rendered HTML templates.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/store"
)

// Options configure the web server.
type Options struct {
	Currency string
	// SKUSearchURL is the search URL a part number is appended to
	// (default Google); SKUSearchPrefix is added to every such search.
	SKUSearchURL    string
	SKUSearchPrefix string
	Version         string
	Logger          *slog.Logger
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Server handles HTTP requests.
type Server struct {
	store           *store.Store
	log             *slog.Logger
	tmpl            *templates
	currency        string
	skuSearch       string
	skuSearchPrefix string
	version         string
	assetHashes     map[string]string
	now             func() time.Time
	mux             *http.ServeMux // set by Handler; used by back
}

// New builds a Server and parses all templates.
func New(st *store.Store, opts Options) (*Server, error) {
	s := &Server{
		store:           st,
		log:             opts.Logger,
		currency:        opts.Currency,
		skuSearch:       opts.SKUSearchURL,
		skuSearchPrefix: strings.TrimSpace(opts.SKUSearchPrefix),
		version:         opts.Version,
		now:             opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.currency == "" {
		s.currency = "EUR"
	}
	if s.skuSearch == "" {
		s.skuSearch = "https://www.google.com/search?q="
	}
	var err error
	if s.assetHashes, err = hashAssets(); err != nil {
		return nil, err
	}
	if s.tmpl, err = parseTemplates(s.funcs()); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the root handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.mux = mux
	h := s.wrap

	mux.HandleFunc("GET /{$}", h(s.dashboard))
	mux.HandleFunc("GET /healthz", s.health)
	mux.Handle("GET /static/", s.static())

	mux.HandleFunc("GET /stock", h(s.stockPage))
	mux.HandleFunc("GET /stock/export.txt", h(s.stockExportText))
	mux.HandleFunc("GET /stock/export.csv", h(s.stockExportCSV))

	mux.HandleFunc("GET /items/new", h(s.itemNew))
	mux.HandleFunc("POST /items", h(s.itemCreate))
	mux.HandleFunc("GET /items/{id}", h(s.itemShow))
	mux.HandleFunc("GET /items/{id}/edit", h(s.itemEdit))
	mux.HandleFunc("POST /items/{id}", h(s.itemUpdate))
	mux.HandleFunc("POST /items/{id}/delete", h(s.itemDelete))
	mux.HandleFunc("POST /items/{id}/archive", h(s.itemArchive))
	mux.HandleFunc("POST /items/{id}/restore", h(s.itemRestore))
	mux.HandleFunc("POST /items/{id}/adjust", h(s.itemAdjust))
	mux.HandleFunc("POST /items/{id}/movements", h(s.itemMovement))
	mux.HandleFunc("POST /items/{id}/photo", h(s.itemPhotoUpload))
	mux.HandleFunc("POST /items/{id}/photo/delete", h(s.itemPhotoDelete))
	mux.HandleFunc("GET /items/{id}/photo", h(s.itemPhoto))
	mux.HandleFunc("POST /items/{id}/shopping", h(s.itemToShopping))

	mux.HandleFunc("GET /vehicles", h(s.vehicleList))
	mux.HandleFunc("GET /vehicles/new", h(s.vehicleNew))
	mux.HandleFunc("POST /vehicles", h(s.vehicleCreate))
	mux.HandleFunc("GET /vehicles/{id}", h(s.vehicleShow))
	mux.HandleFunc("GET /vehicles/{id}/edit", h(s.vehicleEdit))
	mux.HandleFunc("POST /vehicles/{id}", h(s.vehicleUpdate))
	mux.HandleFunc("POST /vehicles/{id}/delete", h(s.vehicleDelete))
	mux.HandleFunc("POST /vehicles/{id}/archive", h(s.vehicleArchive))
	mux.HandleFunc("POST /vehicles/{id}/restore", h(s.vehicleRestore))
	mux.HandleFunc("POST /vehicles/{id}/odometer", h(s.vehicleOdometer))

	mux.HandleFunc("GET /kits/new", h(s.kitNew))
	mux.HandleFunc("POST /kits", h(s.kitCreate))
	mux.HandleFunc("GET /kits/{id}", h(s.kitShow))
	mux.HandleFunc("GET /kits/{id}/edit", h(s.kitEdit))
	mux.HandleFunc("POST /kits/{id}", h(s.kitUpdate))
	mux.HandleFunc("POST /kits/{id}/delete", h(s.kitDelete))
	mux.HandleFunc("POST /kits/{id}/archive", h(s.kitArchive))
	mux.HandleFunc("POST /kits/{id}/restore", h(s.kitRestore))
	mux.HandleFunc("POST /kits/{id}/shortfall", h(s.kitShortfall))

	mux.HandleFunc("GET /services", h(s.serviceList))
	mux.HandleFunc("GET /services/new", h(s.serviceNew))
	mux.HandleFunc("POST /services", h(s.serviceCreate))
	mux.HandleFunc("GET /services/{id}", h(s.serviceShow))
	mux.HandleFunc("GET /services/{id}/edit", h(s.serviceEdit))
	mux.HandleFunc("POST /services/{id}", h(s.serviceUpdate))
	mux.HandleFunc("POST /services/{id}/delete", h(s.serviceDelete))

	mux.HandleFunc("GET /shopping", h(s.shoppingList))
	mux.HandleFunc("POST /shopping", h(s.shoppingAdd))
	mux.HandleFunc("GET /shopping/export.txt", h(s.shoppingExportText))
	mux.HandleFunc("POST /shopping/suggestions", h(s.shoppingAddSuggestions))
	mux.HandleFunc("GET /shopping/{id}/bought", h(s.shoppingBoughtForm))
	mux.HandleFunc("POST /shopping/{id}/bought", h(s.shoppingBought))
	mux.HandleFunc("POST /shopping/{id}/link", h(s.shoppingLink))
	mux.HandleFunc("POST /shopping/{id}/delete", h(s.shoppingDelete))

	mux.HandleFunc("GET /movements/{id}/edit", h(s.movementEdit))
	mux.HandleFunc("POST /movements/{id}", h(s.movementUpdate))
	mux.HandleFunc("POST /movements/{id}/delete", h(s.movementDelete))

	mux.HandleFunc("GET /activity", h(s.activity))
	mux.HandleFunc("GET /activity/export.csv", h(s.activityExportCSV))
	mux.HandleFunc("GET /trash", h(s.trashList))
	mux.HandleFunc("POST /trash/{id}/restore", h(s.trashRestore))
	mux.HandleFunc("POST /trash/{id}/purge", h(s.trashPurge))
	mux.HandleFunc("POST /trash/empty", h(s.trashEmpty))
	mux.HandleFunc("POST /theme", h(s.setTheme))

	mux.HandleFunc("/", h(func(w http.ResponseWriter, r *http.Request) error { return store.ErrNotFound }))

	var handler http.Handler = mux
	handler = http.NewCrossOriginProtection().Handler(handler)
	handler = securityHeaders(handler)
	handler = s.logRequests(handler)
	handler = s.recoverPanics(handler)
	return handler
}

// handlerFunc is an http handler that can fail.
type handlerFunc func(http.ResponseWriter, *http.Request) error

// wrap converts errors into error pages.
func (s *Server) wrap(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		switch {
		case err == nil:
		case errors.Is(err, store.ErrNotFound), errors.Is(err, errBadID):
			s.notFound(w, r)
		case errors.As(err, new(*http.MaxBytesError)):
			s.render(w, r, http.StatusRequestEntityTooLarge, "error", View{Title: "Too large", Data: errorPage{
				Code: 413, Title: "Too large", Message: "That form was too large to save. Shorten the notes and try again.",
			}})
		case errors.Is(err, context.Canceled):
		default:
			s.serverError(w, r, err)
		}
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.store.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable","database":"down"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": s.version})
}

func (s *Server) static() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// ---- Middleware ----

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; "+
			"script-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(self)")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
			level = slog.LevelDebug
		}
		if rec.status >= 500 {
			level = slog.LevelError
		}
		s.log.Log(r.Context(), level, "http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration", time.Since(start).Round(time.Microsecond))
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic", "method", r.Method, "path", r.URL.Path, "panic", v)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
