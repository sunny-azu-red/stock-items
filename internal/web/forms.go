package web

import (
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
var groupedIntRe = regexp.MustCompile(`^-?[0-9]{1,3}(?:[ ,._][0-9]{3})+$`)

// parseOptInt parses an optional whole number. Thousands separators are
// fine ("12 345", "12,345", "12.345"), but "1.5" is not read as 15.
func parseOptInt(s string) (*int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if groupedIntRe.MatchString(s) {
		s = strings.NewReplacer(" ", "", ",", "", ".", "", "_", "").Replace(s)
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

// parseOptMoney parses an optional decimal amount, accepting a decimal comma.
func parseOptMoney(s string) (*float64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if s == "" {
		return nil, nil
	}
	// Plain digits only: ParseFloat would also take "NaN", "Inf" and "1e9".
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !moneyRe.MatchString(s) {
		return nil, fmt.Errorf("%q is not a valid amount", s)
	}
	return &f, nil
}

// parseOptDate parses an optional yyyy-mm-dd date.
func parseOptDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("%q is not a valid date", s)
	}
	return &t, nil
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

// safeBack returns the Referer path when it points to this site, so
// non-htmx form posts can return to where the user was.
func safeBack(r *http.Request, fallback string) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host != r.Host {
		return fallback
	}
	// Use the escaped path: the decoded one can turn "%5Cevil.example" into
	// "/\evil.example", which browsers follow to another site.
	p := ref.EscapedPath()
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\t\r\n") {
		return fallback
	}
	if ref.RawQuery != "" {
		return p + "?" + ref.RawQuery
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
