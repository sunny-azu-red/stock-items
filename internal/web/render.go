package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// View is the data every page template receives.
type View struct {
	Title    string
	Nav      string
	Flash    *Flash
	Currency string
	Version  string
	Today    time.Time
	// Theme is "light", "dark" or "" to follow the system.
	Theme string
	// NavVehicles, ShoppingCount and TrashCount feed the sidebar.
	TrashCount    int
	NavVehicles   []stock.Vehicle
	ShoppingCount int
	// Data is the page specific payload.
	Data any
}

// Flash is a one-time message shown after a redirect.
type Flash struct {
	Kind    string `json:"k"` // "success" or "error"
	Message string `json:"m"`
	Undo    int64  `json:"u,omitempty"` // trash id: the message offers to undo it
}

type templates struct {
	pages    map[string]*template.Template
	partials *template.Template
}

func parseTemplates(funcs template.FuncMap) (*templates, error) {
	base := template.New("").Funcs(funcs)
	base, err := base.ParseFS(templateFS, "templates/layout.html", "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	pages, err := fs.Glob(templateFS, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	t := &templates{pages: map[string]*template.Template{}, partials: base}
	for _, p := range pages {
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(templateFS, p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		t.pages[strings.TrimSuffix(path.Base(p), ".html")] = clone
	}
	return t, nil
}

// assets maps static file paths to a short content hash for cache busting.
func hashAssets() (map[string]string, error) {
	hashes := map[string]string{}
	err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		hashes[strings.TrimPrefix(p, "static/")] = hex.EncodeToString(sum[:])[:10]
		return nil
	})
	return hashes, err
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"asset": func(p string) string {
			return "/static/" + p + "?v=" + s.assetHashes[p]
		},
		"icon": func(name string, class ...string) template.HTML {
			c := "icon"
			if len(class) > 0 {
				c += " " + class[0]
			}
			return template.HTML(`<svg class="` + template.HTMLEscapeString(c) + `" aria-hidden="true"><use href="#i-` +
				template.HTMLEscapeString(name) + `"></use></svg>`)
		},
		"qty":   stock.FormatQuantity,
		"delta": stock.FormatDelta,
		"num":   stock.FormatNumber,
		"km":    formatKm,
		"money": s.formatMoney,
		"date":  formatDate,
		"ago":   func(t time.Time) string { return relativeDate(t, s.now()) },
		"deref": derefFloat,
		"dict":  dict,
		"add":   func(a, b int) int { return a + b },
		"pct": func(have, need float64) int {
			if need <= 0 {
				return 100
			}
			return int(min(100, have/need*100))
		},
		"hasID": func(ids []int64, id int64) bool {
			for _, v := range ids {
				if v == id {
					return true
				}
			}
			return false
		},
		"rowFor": func(it stock.Item, vs map[int64]stock.Vehicle, hideChips bool) rowData {
			return rowData{Item: it, Vehicles: vs, HideChips: hideChips}
		},
		"placeholderFor": func(u stock.Unit) string { return "e.g. " + examplesFor(u) },
		"optInt":         optIntString,
		"unitPrice":      s.formatUnitPrice,
		// withQuery joins a path and an already encoded query string
		// (from url.Values.Encode), so html/template does not escape it again.
		"withQuery": func(path, query string) template.URL {
			if query == "" {
				return template.URL(path)
			}
			return template.URL(path + "?" + query)
		},
		// skuSearch links a part number to the configured search engine,
		// with the configured prefix (e.g. "site:ro") added to the query.
		"skuSearch": func(code string) template.URL {
			q := code
			if s.skuPrefix != "" {
				q += " " + s.skuPrefix
			}
			return template.URL(s.skuSearch + url.QueryEscape(q))
		},
		"emptyLine": func() lineRow { return lineRow{} },
		"subUnits":  func() []stock.SubUnit { return stock.SubUnits },
		// step is how much quick -/+ changes an item's stock, e.g. "100 ml".
		"step": func(u stock.Unit) string { return stock.FormatQuantity(stock.DefaultStep(u), u) },
		"join": strings.Join,
		"plural": func(n int, word string) string {
			if n == 1 {
				return "1 " + word
			}
			if strings.HasSuffix(word, "y") && !strings.HasSuffix(word, "ey") {
				return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(word, "y"))
			}
			return fmt.Sprintf("%d %ss", n, word)
		},
		"chipStyle": func(color string) template.CSS {
			if !strings.HasPrefix(color, "#") || len(color) != 7 {
				color = "#888888"
			}
			return template.CSS("--chip:" + color)
		},
	}
}

// render writes a full page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, v View) {
	t, ok := s.tmpl.pages[page]
	if !ok {
		s.serverError(w, r, fmt.Errorf("unknown page template %q", page))
		return
	}
	v.Currency = s.currency
	v.Version = s.version
	v.Today = s.now()
	v.Theme = themeFrom(r)
	if v.Flash == nil {
		v.Flash = s.popFlash(w, r)
	}
	if vs, err := s.store.ListVehicles(r.Context()); err == nil {
		v.NavVehicles = activeVehicles(vs)
	}
	if list, err := s.store.ListShopping(r.Context()); err == nil {
		v.ShoppingCount = len(list)
	}
	if n, err := s.store.CountTrash(r.Context()); err == nil {
		v.TrashCount = n
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", v); err != nil {
		s.serverError(w, r, fmt.Errorf("render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// renderPartial writes a single named template (for htmx swaps).
func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.partials.ExecuteTemplate(&buf, name, data); err != nil {
		s.serverError(w, r, fmt.Errorf("render partial %s: %w", name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "error", View{Title: "Not found", Data: errorPage{
		Code: 404, Title: "Not found", Message: "This page does not exist, or the thing it showed was deleted.",
	}})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	// Avoid templates here: they may be what failed.
	_, _ = fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>Error</title>`+
		`<body style="font-family:system-ui;padding:2rem"><h1>Something went wrong</h1>`+
		`<p>The error was logged. <a href="/">Back to the dashboard</a></p></body>`)
}

type errorPage struct {
	Code    int
	Title   string
	Message string
}

// ---- Flash messages (cookie based) ----

const flashCookie = "flash"

func (s *Server) setFlash(w http.ResponseWriter, kind, msg string) {
	s.writeFlash(w, Flash{Kind: kind, Message: msg})
}

func (s *Server) writeFlash(w http.ResponseWriter, f Flash) {
	b, _ := json.Marshal(f)
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: base64.RawURLEncoding.EncodeToString(b), Path: "/",
		MaxAge: 60, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}
	var f Flash
	if json.Unmarshal(b, &f) != nil || f.Message == "" {
		return nil
	}
	return &f
}

// redirectUndo is redirect with a success message that offers to undo
// the deletion that went to trash entry trashID.
func (s *Server) redirectUndo(w http.ResponseWriter, r *http.Request, url, msg string, trashID int64) {
	s.writeFlash(w, Flash{Kind: "success", Message: msg, Undo: trashID})
	s.redirect(w, r, url, "")
}

// redirect sends the browser to url with a flash message. htmx requests get
// an HX-Redirect header instead of a 303.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, url, flash string) {
	if flash != "" {
		s.setFlash(w, "success", flash)
	}
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// toast attaches a toast message to an htmx response.
func toast(w http.ResponseWriter, kind, msg string) {
	b, _ := json.Marshal(map[string]any{"toast": map[string]string{"kind": kind, "message": msg}})
	w.Header().Set("HX-Trigger", string(b))
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// ---- Formatting helpers ----

func (s *Server) formatMoney(v any) string {
	f, ok := derefFloatOK(v)
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%s %s", formatDecimal(f), s.currency)
}

// formatUnitPrice shows a price per base unit the way shops do:
// per piece, per litre or per kilogram.
func (s *Server) formatUnitPrice(p *float64, u stock.Unit) string {
	if p == nil {
		return "—"
	}
	price, per := stock.PricePer(*p, u)
	return s.formatMoney(price) + " / " + per
}

// formatDecimal renders f with two decimals and thousands separators.
func formatDecimal(f float64) string {
	s := strconv.FormatFloat(math.Abs(f), 'f', 2, 64)
	whole, frac, _ := strings.Cut(s, ".")
	n, err := strconv.Atoi(whole)
	if err != nil { // NaN, Inf or beyond int: show it as is
		return s
	}
	sign := ""
	if f < 0 && s != "0.00" {
		sign = "-"
	}
	return sign + stock.FormatInt(n) + "." + frac
}

func formatKm(v any) string {
	if n, ok := derefIntOK(v); ok {
		return stock.FormatKm(n)
	}
	return "—"
}

func formatDate(v any) string {
	if t, ok := derefTime(v); ok {
		return t.Format("2 Jan 2006")
	}
	return "—"
}

func relativeDate(t, now time.Time) string {
	days := int(stock.DateOnly(now).Sub(stock.DateOnly(t)).Hours() / 24)
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days > 1 && days < 30:
		return fmt.Sprintf("%d days ago", days)
	}
	return t.Format("2 Jan 2006")
}

func derefTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, !t.IsZero()
	case *time.Time:
		if t != nil {
			return *t, true
		}
	}
	return time.Time{}, false
}

func derefFloatOK(v any) (float64, bool) {
	switch f := v.(type) {
	case float64:
		return f, true
	case *float64:
		if f != nil {
			return *f, true
		}
	}
	return 0, false
}

func derefFloat(v any) float64 { f, _ := derefFloatOK(v); return f }

func derefIntOK(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case *int:
		if n != nil {
			return *n, true
		}
	}
	return 0, false
}

func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("dict needs key/value pairs")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, errors.New("dict keys must be strings")
		}
		m[k] = kv[i+1]
	}
	return m, nil
}
