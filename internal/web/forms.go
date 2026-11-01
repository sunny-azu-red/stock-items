package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

var errBadID = errors.New("invalid id")

const maxFormBytes = 1 << 20 // 1 MiB for regular forms

// pathID reads a positive integer path parameter.
func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errBadID
	}
	return id, nil
}

// parseForm parses a urlencoded body with a size limit.
func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return r.ParseForm()
}

func formText(r *http.Request, key string) string { return strings.TrimSpace(r.PostFormValue(key)) }

// groupedIntRe matches whole numbers written with thousands separators.
var groupedIntRe = regexp.MustCompile(`^-?[0-9]{1,3}(?:[ ,._\x{00a0}\x{202f}][0-9]{3})+$`)

// parseOptInt parses an optional whole number. Thousands separators are
// fine ("12 345", "12,345", "12.345", or the narrow space the app prints),
// but "1.5" is not read as 15.
func parseOptInt(s string) (*int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if groupedIntRe.MatchString(s) {
		s = strings.NewReplacer(" ", "", ",", "", ".", "", "_", "", "\u00a0", "", "\u202f", "").Replace(s)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("%q is not a whole number", s)
	}
	if n > math.MaxInt32 || n < math.MinInt32 { // the database columns are 32 bit
		return nil, fmt.Errorf("%q is too large", s)
	}
	return &n, nil
}

var moneyRe = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)$`)

// parseOptMoney parses an optional decimal amount, accepting a decimal
// comma. "1.500" is refused: a price copied from a shop that groups
// thousands with a point would otherwise be saved 1000 times too small.
func parseOptMoney(s string) (*float64, error) {
	typed := strings.TrimSpace(s) // quoted in errors as typed
	if typed == "" {
		return nil, nil
	}
	if err := stock.CheckAmbiguous(typed); err != nil {
		return nil, err
	}
	s = strings.ReplaceAll(typed, ",", ".")
	// Plain digits only: ParseFloat would also take "NaN", "Inf" and "1e9".
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !moneyRe.MatchString(s) {
		return nil, fmt.Errorf("%q is not a valid amount", typed)
	}
	return &f, nil
}

// parseOptDate parses an optional yyyy-mm-dd date.
func parseOptDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	// A calendar day, kept as midnight UTC like stock.DateOnly and a date
	// read from the database, so it compares with today's and is stored
	// as the day typed. Not time.ParseInLocation: midnight in the app's
	// zone would be another day, or hours off, once in UTC.
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("%q is not a valid date", s)
	}
	return &t, nil
}

// today is the app's current date, in its zone, the default for stock
// changes made without one (the database's day may disagree with it).
func (s *Server) today() *time.Time {
	d := stock.DateOnly(s.now())
	return &d
}

// maxPage caps ?page= so the offset it turns into can't overflow.
const maxPage = 100_000

// parsePage reads ?page= (1 for missing or nonsense).
func parsePage(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxPage)
}

// parseEventDate parses an optional date of something that already
// happened (a purchase, stock change or service), refusing dates that are
// surely typos; see stock.CheckEventDate.
func parseEventDate(s string, today time.Time) (*time.Time, error) {
	d, err := parseOptDate(s)
	if err != nil {
		return nil, err
	}
	if msg := stock.CheckEventDate(d, today); msg != "" {
		return nil, errors.New(msg)
	}
	return d, nil
}

// parseOptAmount parses an optional quantity in the given unit.
func parseOptAmount(s string, u stock.Unit, sub string) (*float64, error) {
	v, err := stock.ParseAmount(s, u, sub)
	if errors.Is(err, stock.ErrEmpty) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// parseIDs reads positive ids, dropping duplicates.
func parseIDs(vals []string) []int64 {
	var out []int64
	for _, v := range vals {
		if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && id > 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func parseOptID(s string) *int64 {
	if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && id > 0 {
		return &id
	}
	return nil
}

func optIntString(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// back is safeBack limited to pages that can be shown. A form that failed
// re-renders at its POST-only URL (e.g. /items/5/movements), and going
// back there would land on a 404.
func (s *Server) back(r *http.Request, fallback string) string {
	target := safeBack(r, fallback)
	if target == fallback || s.mux == nil {
		return target
	}
	u, err := url.Parse(target)
	if err != nil {
		return fallback
	}
	if _, pattern := s.mux.Handler(&http.Request{Method: http.MethodGet, URL: u, Host: r.Host}); pattern == "" || pattern == "/" {
		return fallback
	}
	return target
}

// safeBack returns the Referer path when it points to this site, so
// non-htmx form posts can return to where the user was.
func safeBack(r *http.Request, fallback string) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host != r.Host {
		return fallback
	}
	return localPath(ref, fallback)
}

// postedBack returns the form's "back" field when it is a path on this
// site, so a form can return to the page that opened it.
func postedBack(r *http.Request, fallback string) string {
	u, err := url.Parse(r.PostFormValue("back"))
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	return localPath(u, fallback)
}

// localPath returns u's path and query when the path is safe to redirect to.
func localPath(u *url.URL, fallback string) string {
	// Use the escaped path: the decoded one can turn "%5Cevil.example" into
	// "/\evil.example", which browsers follow to another site.
	p := u.EscapedPath()
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\t\r\n") {
		return fallback
	}
	if u.RawQuery != "" {
		return p + "?" + u.RawQuery
	}
	return p
}

// unitOrDefault returns u when valid, otherwise pieces.
func unitOrDefault(u string) stock.Unit {
	if stock.Unit(u).Valid() {
		return stock.Unit(u)
	}
	return stock.UnitPiece
}

// parseVersion reads the version an edit form or button carried (see
// stock.Item.Version). A missing or invalid one is 0, which no record has,
// so the store refuses it as stale.
func parseVersion(r *http.Request) int {
	v, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("version")))
	if err != nil || v < 1 {
		return 0
	}
	return v
}

// staleNote is the banner of an edit form refused because its record
// changed or was deleted since it was opened. What was typed stays.
type staleNote struct {
	Message string
	Href    string // where to go from here ("" for nowhere)
	Link    string // the link's text
}

// staleForm turns a *store.StaleError into the banner of an edit form
// whose fresh copy is at reload; nil for any other error.
func staleForm(err error, reload string) *staleNote {
	var stale *store.StaleError
	if !errors.As(err, &stale) {
		return nil
	}
	n := &staleNote{Message: stale.Error(), Href: reload, Link: "Reload"}
	switch {
	case stale.Gone && stale.InTrash:
		n.Href, n.Link = "/trash", "Trash"
	case stale.Gone:
		n.Href = ""
	}
	return n
}

// gone turns a page's ErrNotFound for the record a button acts on into
// the store's explanation: deleted, and maybe in the trash.
func (s *Server) gone(r *http.Request, err error, what string, id int64) error {
	if errors.Is(err, store.ErrNotFound) {
		return s.store.Gone(r.Context(), what, id)
	}
	return err
}

// staleButton answers a button (delete, archive, restore, …) whose record
// changed or was deleted since its page was shown: nothing was done, and
// the user lands on the record as it is now (page), on the trash page,
// or on list when it is gone for good. It reports whether err was that.
func (s *Server) staleButton(w http.ResponseWriter, r *http.Request, err error, page, list string) bool {
	var stale *store.StaleError
	if !errors.As(err, &stale) {
		return false
	}
	to, msg := page, stale.Error()
	switch {
	case stale.Gone && stale.InTrash:
		to = "/trash"
	case stale.Gone:
		to = list
	default:
		msg = "This " + stale.What + " was changed since you opened it (another tab or person), so nothing was done. " +
			"This is how it is now; try again if you still want to."
	}
	s.setFlash(w, "error", msg)
	s.redirect(w, r, to, "")
	return true
}

// formKeyRe matches the one-time keys newFormKey makes.
var formKeyRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// newFormKey makes the one-time key of a form that creates or changes
// something (see store.AlreadySavedError): 128 random bits.
func newFormKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// parseFormKey reads the form's one-time key; "" when missing or invalid.
func parseFormKey(r *http.Request) string {
	if k := r.PostFormValue("form_key"); formKeyRe.MatchString(k) {
		return k
	}
	return ""
}

// noFormKey refuses a form posted without a key: it can't be told apart
// from a repeat of itself, so rather than risk saving it twice (a service
// would take its parts again), it re-renders with what was typed and a
// key, to save with one more click.
const noFormKey = "This form was opened before Pitbox was updated, so it was not saved. Check it and save again."

// checkFormKey records noFormKey in errs when key is missing.
func checkFormKey(errs stock.Errors, key string) {
	if key == "" {
		errs.Add("form", noFormKey)
	}
}

// savedAlreadyMsg answers a form posted again after its first save went
// through.
const savedAlreadyMsg = "This was saved already (the first Save went through), so it wasn't saved again. " +
	"Changes made in the form since weren't applied; check it here."

// savedBefore answers a form whose key a finished save holds, before the
// handler looks at anything that save changed: it reports whether it did
// (see savedAlready). what is the kind of record the form saves.
func (s *Server) savedBefore(w http.ResponseWriter, r *http.Request, key, what string, page func(int64) string, list string) (bool, error) {
	err := s.store.FormSaved(r.Context(), key, what)
	if s.savedAlready(w, r, err, page, list) {
		return true, nil
	}
	return false, err
}

// savedAlready answers a *store.AlreadySavedError: nothing was saved this
// time, and the user lands where the first save did, page(id) for the
// record it saved, with a message saying so. A record deleted since leads
// to the trash, or to list. It reports whether err was that.
func (s *Server) savedAlready(w http.ResponseWriter, r *http.Request, err error, page func(int64) string, list string) bool {
	var saved *store.AlreadySavedError
	if !errors.As(err, &saved) {
		return false
	}
	switch {
	case saved.Gone != nil && saved.Gone.InTrash:
		s.setFlash(w, "error", "This was saved already, and deleted since; it's in the trash.")
		s.redirect(w, r, "/trash", "")
	case saved.Gone != nil:
		s.setFlash(w, "error", "This was saved already, and deleted since.")
		s.redirect(w, r, list, "")
	default:
		to := list
		if page != nil && saved.ID != 0 {
			to = page(saved.ID)
		}
		s.redirect(w, r, to, savedAlreadyMsg)
	}
	return true
}
