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
	"unicode/utf16"
	"unicode/utf8"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// View is the data every page template receives.
type View struct {
	Title    string
	Nav      string
	Path     string // the request's path
	Flash    *Flash
	Currency string
	Version  string
	Today    time.Time
	// Theme is "light", "dark" or "" to follow the system.
	Theme string
	// Accent is the accent colour chosen on this device (nil: the default).
	Accent *Accent
	// NavVehicles, ShoppingCount and TrashCount feed the sidebar.
	TrashCount    int
	NavVehicles   []stock.Vehicle
	ShoppingCount int
	// WorkingOn is the vehicle being worked on (nil for none), for the bar
	// at the top of every page; WorkingCookie is the cookie as it stands
	// after this response (see workingFor).
	WorkingOn     *stock.Vehicle
	WorkingCookie string
	// Head is the page header, which the layout writes from the page's
	// head-* blocks (see renderHead).
	Head PageHead
	// Data is the page specific payload.
	Data any
}

// PageHead is a page's header, rendered from its template's blocks:
// head-title (required), head-line, head-actions and head-lead. The layout
// writes the header's markup once (docs/design.md#7-page-headers), so every
// page gets the same one; a block that renders nothing leaves no element.
// More is set when a button in Actions may go into the ⋯ menu (data-more),
// so the layout adds the menu only to headers that can use it.
type PageHead struct {
	Lead, Title, Line, Actions template.HTML
	More                       bool
}

// renderHead renders the page's head-* blocks with v, trimmed.
func renderHead(t *template.Template, v View) (PageHead, error) {
	var h PageHead
	for _, b := range []struct {
		name string
		dst  *template.HTML
	}{{"head-lead", &h.Lead}, {"head-title", &h.Title}, {"head-line", &h.Line}, {"head-actions", &h.Actions}} {
		if t.Lookup(b.name) == nil {
			if b.name == "head-title" {
				return h, errors.New("the page has no head-title block")
			}
			continue
		}
		var buf bytes.Buffer
		if err := t.ExecuteTemplate(&buf, b.name, v); err != nil {
			return h, err
		}
		//nolint:gosec // html/template's own output, escaped
		*b.dst = template.HTML(strings.TrimSpace(buf.String()))
	}
	h.More = strings.Contains(string(h.Actions), "data-more=")
	return h, nil
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
			//nolint:gosec // both parts are escaped
			return template.HTML(`<svg class="` + template.HTMLEscapeString(c) + `" aria-hidden="true"><use href="#i-` +
				template.HTMLEscapeString(name) + `"></use></svg>`)
		},
		"qty":        stock.FormatQuantity,
		"delta":      stock.FormatDelta,
		"num":        stock.FormatNumber,
		"distance":   formatDistance,
		"distUnit":   func() string { return stock.DistanceUnit },
		"distNum":    formatDistanceNum,
		"money":      s.formatMoney,
		"date":       formatDate,
		"ago":        func(t time.Time) string { return relativeDate(t, s.now()) },
		"deref":      derefFloat,
		"changeName": changeName,
		// expiryState is "expired", "soon" or "" for an item today.
		"expiryState": func(it stock.Item) string { return it.ExpiryState(s.now(), store.ExpiringDays) },
		"dict":        dict,
		"add":         func(a, b int) int { return a + b },
		"hasID": func(ids []int64, id int64) bool {
			for _, v := range ids {
				if v == id {
					return true
				}
			}
			return false
		},
		"rowFor": func(it stock.Item, vs map[int64]stock.Vehicle, hideChips bool, here, working int64) rowData {
			return rowData{Item: it, Vehicles: vs, HideChips: hideChips, Here: here, Working: working}
		},
		"placeholderFor": func(u stock.Unit) string { return "e.g. " + examplesFor(u) },
		// rowPlaceholderFor is a part row's: the number alone, as the unit
		// picker beside it says the unit (and a phone has room for little).
		"rowPlaceholderFor": func(u stock.Unit) string { return "e.g. " + numberExampleFor(u) },
		"optInt":            optIntString,
		"unitPrice":         s.formatUnitPrice,
		// withQuery joins a path and an already encoded query string
		// (from url.Values.Encode), so html/template does not escape it again.
		"withQuery": func(path, query string) template.URL {
			return template.URL(joinQuery(path, query)) //nolint:gosec // our own paths, and a query url.Values encoded
		},
		// skuSearch links a part number (optionally with brand) to the configured search engine,
		// with the configured prefix (e.g. "site:de"), if any, added to the query.
		// A string, not template.URL: html/template still refuses a
		// scheme other than http(s) in the operator's URL.
		"skuSearch": func(code string, brand ...string) string {
			b := ""
			if len(brand) > 0 {
				b = brand[0]
			}
			return skuSearchURL(s.skuSearch, s.skuSearchPrefix, code, b)
		},
		"skuSearchTitle": func() string { return s.skuSearchTitle },
		"emptyLine":      func() lineRow { return lineRow{} },
		// stockBadge is an item's badge for the "stock-badge" partial;
		// withQty puts what is in stock in it (see newStockBadge).
		"stockBadge":       func(it stock.Item, withQty bool) stockBadge { return newStockBadge(it, 0, withQty) },
		"stockBadgeLevels": stockBadgeLevels,
		"stockUnits":       stockUnitsJSON,
		"newFormKey":       newFormKey,
		// photoLimit is checked by app.js before a photo is uploaded: the
		// server's refusal of a larger one can arrive while the browser is
		// still sending, and show as a connection error instead. Side is
		// how far app.js shrinks a larger photo before it goes.
		"photoLimit": func() map[string]any {
			return map[string]any{"Bytes": maxPhotoFileBytes, "Message": photoTooLarge, "Side": photoMaxSide}
		},
		"subUnits": func() []stock.SubUnit { return stock.SubUnits },
		// toList counts a kit's lines missing more than the shopping list asks for.
		"toList": stock.ToList,
		// step is how much quick -/+ changes an item's stock, e.g. "100 ml".
		"step":          func(u stock.Unit) string { return stock.FormatQuantity(stock.DefaultStep(u), u) },
		"join":          strings.Join,
		"plural":        plural,
		"pluralOnly":    pluralOnly,
		"accentPresets": accentPresets,
		"fieldErr":      fieldErr,
		"invalid":       invalidAttrs,
		"chipStyle": func(color string) template.CSS {
			if !stock.IsColor(color) {
				color = "#888888"
			}
			return template.CSS("--chip:" + color) //nolint:gosec // a #rrggbb colour, checked above
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
	v.Accent = accentFrom(r)
	v.Path = r.URL.Path
	if v.Flash == nil {
		v.Flash = s.popFlash(w, r)
	}
	if vs, err := s.store.ListVehicles(r.Context()); err == nil {
		v.NavVehicles = activeVehicles(vs)
		v.WorkingOn, v.WorkingCookie = workingFor(w, r, vs)
	} else {
		// Still the cookie, so app.js doesn't reload a page that can't
		// tell which vehicle it is (it would, over and over).
		v.WorkingCookie = workingCookieValue(r)
	}
	if list, err := s.store.ListShopping(r.Context()); err == nil {
		// The vehicle worked on's part, which the Shopping link opens.
		var working int64
		if v.WorkingOn != nil {
			working = v.WorkingOn.ID
		}
		part, _ := stock.ShoppingFor(list, working)
		v.ShoppingCount = len(part)
	}
	if n, err := s.store.CountTrash(r.Context()); err == nil {
		v.TrashCount = n
	}
	head, err := renderHead(t, v)
	if err != nil {
		s.serverError(w, r, fmt.Errorf("render %s header: %w", page, err))
		return
	}
	v.Head = head
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
		`<body style="font-family:system-ui;padding:2rem"><h1>Pitbox hit an error</h1>`+
		`<p>The error was logged. Go back and try again, or go to <a href="/">the dashboard</a>.</p></body>`)
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
	http.Redirect(w, r, url, http.StatusSeeOther) //nolint:gosec // callers pass our own paths or back's
}

// skuSearchURL is the search link of a part number: the number (and the
// prefix, if any) escaped and put in place of the {q} in base (New refuses
// a base without exactly one). In the query it is query-escaped, so "A+B"
// and "A&B=C" stay one value; in the path or the fragment it is
// path-escaped, so "12/34" stays one segment.
func skuSearchURL(base, prefix, code, brand string) string {
	q := strings.TrimSpace(code)
	if strings.TrimSpace(brand) != "" {
		q = strings.TrimSpace(brand) + " " + q
	}
	if prefix != "" {
		q += " " + prefix
	}
	before, after, _ := strings.Cut(base, "{q}")
	query, _, _ := strings.Cut(before, "#")
	if strings.Contains(query, "?") && len(query) == len(before) {
		return before + url.QueryEscape(q) + after
	}
	return before + url.PathEscape(q) + after
}

// searchTitle says where part number links search: "Search on
// google.com (site:de)".
func searchTitle(searchURL, prefix string) string {
	title := "Search for this part number"
	if u, err := url.Parse(searchURL); err == nil && u.Host != "" {
		title = "Search on " + strings.TrimPrefix(u.Hostname(), "www.")
	}
	if prefix != "" {
		title += " (" + prefix + ")"
	}
	return title
}

// toast attaches a toast message to an htmx response.
func toast(w http.ResponseWriter, kind, msg string) {
	b, _ := json.Marshal(map[string]any{"toast": map[string]string{"kind": kind, "message": msg}})
	w.Header().Set("HX-Trigger", asciiJSON(b))
}

// asciiJSON escapes everything but ASCII in JSON as \uXXXX. Browsers read
// header values as Latin-1, so "Plăcuțe" sent as UTF-8 turned to mojibake.
func asciiJSON(b []byte) string {
	var sb strings.Builder
	for _, r := range string(b) {
		if r < utf8.RuneSelf {
			sb.WriteRune(r)
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&sb, `\u%04x`, u)
		}
	}
	return sb.String()
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

func formatDistance(v any) string {
	if n, ok := derefIntOK(v); ok {
		return stock.FormatDistance(n)
	}
	return "—"
}

// formatDistanceNum is a distance's figure without its unit, "—" for none.
func formatDistanceNum(v any) string {
	if n, ok := derefIntOK(v); ok {
		return stock.FormatInt(n)
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

func plural(n int, word string) string {
	return fmt.Sprintf("%d %s", n, pluralOnly(n, word))
}

func pluralOnly(n int, word string) string {
	if n == 1 {
		return word
	}
	if strings.HasSuffix(word, "y") && !strings.HasSuffix(word, "ey") {
		return strings.TrimSuffix(word, "y") + "ies"
	}
	return word + "s"
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// isMessage reports whether a template value is an error message to show:
// a non-empty string (a missing map key is ""; "and" with a false
// condition is false).
func isMessage(v any) bool {
	switch m := v.(type) {
	case string:
		return m != ""
	case template.HTML:
		return m != ""
	default:
		return false
	}
}

// fieldErr is a field's error message for the "field-err" partial, with
// the id its control's aria-describedby points to: the control's id and
// "-err".
func fieldErr(msg any, id string) map[string]any {
	if !isMessage(msg) {
		return map[string]any{"Msg": "", "ID": ""}
	}
	return map[string]any{"Msg": msg, "ID": id + "-err"}
}

// invalidAttrs are a form control's aria-invalid and aria-describedby: its
// error message (see fieldErr) when there is one, then its hints (ids; a
// false or empty one is left out, so a hint can depend on the form).
// aria-invalid is always said, "false" too, so a required field left empty
// isn't announced as invalid before the form has been sent.
func invalidAttrs(msg any, id string, hints ...any) template.HTMLAttr {
	var ids []string
	bad := isMessage(msg)
	if bad {
		ids = append(ids, id+"-err")
	}
	for _, h := range hints {
		if s, ok := h.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}
	a := `aria-invalid="false"`
	if bad {
		a = `aria-invalid="true"`
	}
	if len(ids) > 0 {
		a += ` aria-describedby="` + template.HTMLEscapeString(strings.Join(ids, " ")) + `"`
	}
	return template.HTMLAttr(a) //nolint:gosec // fixed names, and escaped ids
}
