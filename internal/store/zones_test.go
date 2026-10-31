package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// zoneRule says why SQL that depends on the session's time zone is out.
const zoneRule = "calendar days are decided only in Go, in the app's zone (stock.DateOnly, " +
	"store.appToday); SQL never turns an instant into a day. See the dates rule in AGENTS.md"

// zoneDependent matches SQL whose result depends on the session's time
// zone. now() and timestamps compared or shifted by an interval stay
// instants, so they are fine.
var zoneDependent = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)\bcurrent_date\b`), "current_date"},
	{regexp.MustCompile(`(?i)\bcurrent_time\b`), "current_time"},
	{regexp.MustCompile(`(?i)\blocaltime(stamp)?\b`), "localtime/localtimestamp"},
	{regexp.MustCompile(`(?i)\bat\s+time\s+zone\b`), "AT TIME ZONE"},
	{regexp.MustCompile(`(?i)\b(date_trunc|date_part|extract|timezone|to_char|age)\s*\(`), "a date function"},
	{regexp.MustCompile(`(?i)\bdate\s*\(`), "date()"},
	{regexp.MustCompile(`(?i)::\s*(timestamp|time)\b(\s+without\s+time\s+zone)?`), "a cast to timestamp/time without zone"},
	{regexp.MustCompile(`(?i)\bas\s+(timestamp|time)\s*\)`), "a cast to timestamp/time without zone"},
}

var (
	// dateCast is a cast to date; only a $n parameter (a day Go worked
	// out) may be cast.
	dateCast  = regexp.MustCompile(`(?i)::\s*date\b`)
	paramCast = regexp.MustCompile(`\$\d+\s*$`)
	castAs    = regexp.MustCompile(`(?is)\bcast\s*\(\s*(.*?)\s+as\s+date\s*\)`)
	param     = regexp.MustCompile(`^\$\d+$`)
)

// checkZoneSQL returns what in sql depends on the session's zone.
// afterParam says the literal comes right after a $n placeholder built
// in Go (args.add(…) + "::date …").
func checkZoneSQL(sql string, afterParam bool) []string {
	var found []string
	for _, z := range zoneDependent {
		if m := z.re.FindString(sql); m != "" {
			found = append(found, z.what+" ("+strings.TrimSpace(m)+")")
		}
	}
	for _, loc := range dateCast.FindAllStringIndex(sql, -1) {
		before := sql[:loc[0]]
		if paramCast.MatchString(before) || (afterParam && strings.TrimSpace(before) == "") {
			continue
		}
		found = append(found, "::date on something other than a $n parameter ("+lastWords(before)+"::date)")
	}
	for _, m := range castAs.FindAllStringSubmatch(sql, -1) {
		if !param.MatchString(m[1]) {
			found = append(found, "CAST(… AS date) on something other than a $n parameter ("+m[0]+")")
		}
	}
	return found
}

func lastWords(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 30 {
		s = "…" + s[len(s)-30:]
	}
	return s
}

func TestCheckZoneSQL(t *testing.T) {
	tests := []struct {
		sql        string
		afterParam bool
		bad        bool
	}{
		{`SELECT 1 WHERE d <= current_date`, false, true},
		{`SELECT 1 WHERE d <= CURRENT_DATE + 30`, false, true},
		{`SELECT current_time`, false, true},
		{`SELECT LocalTimestamp`, false, true},
		{`SELECT localtime`, false, true},
		{`SELECT created_at AT TIME ZONE 'Europe/Bucharest'`, false, true},
		{"SELECT created_at at\n time  zone 'UTC'", false, true},
		{`SELECT date_trunc('day', created_at)`, false, true},
		{`SELECT DATE_TRUNC ('month', now())`, false, true},
		{`SELECT extract(day FROM created_at)`, false, true},
		{`SELECT date(created_at)`, false, true},
		{`SELECT now()::date`, false, true},
		{`SELECT NOW() :: DATE`, false, true},
		{`WHERE created_at::date - performed_on <= 7`, false, true},
		{`SELECT CAST(created_at AS date)`, false, true},
		{`SELECT cast(now() as DATE)`, false, true},
		{`SELECT created_at::timestamp`, false, true},
		{`SELECT created_at::timestamp without time zone`, false, true},
		{`SELECT CAST(created_at AS timestamp)`, false, true},
		{`::date + 30`, false, true}, // not after a placeholder
		// Fine: instants, dates Go worked out, and plain date columns.
		{`WHERE created_at < now() - interval '1 day'`, false, false},
		{`UPDATE items SET updated_at = now()`, false, false},
		{`SELECT current_timestamp`, false, false},
		{`WHERE occurred_on > $1::date - interval '12 months' AND occurred_on <= $1::date`, false, false},
		{`WHERE d <= $12 :: date`, false, false},
		{`::date + $3::int`, true, false},
		{`SELECT CAST($2 AS date)`, false, false},
		{`SELECT created_at::timestamptz, performed_on, expires_on`, false, false},
		{`SELECT updated_at, archived_at FROM items`, false, false},
	}
	for _, tt := range tests {
		if got := checkZoneSQL(tt.sql, tt.afterParam); (len(got) > 0) != tt.bad {
			t.Errorf("checkZoneSQL(%q, %v) = %q, want bad = %v", tt.sql, tt.afterParam, got, tt.bad)
		}
	}
}

// TestNoZoneDependentSQL keeps SQL in the store from deciding a calendar
// day: the session is in UTC, which around midnight isn't the app's day.
// It reads every string literal in the store's (non-test) Go files.
func TestNoZoneDependentSQL(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Literals right after a $n placeholder: the right side of a + whose
		// left side ends in a call to add (sqlArgs).
		afterParam := map[*ast.BasicLit]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			if b, ok := n.(*ast.BinaryExpr); ok && b.Op == token.ADD {
				if lit, ok := b.Y.(*ast.BasicLit); ok && endsInAdd(b.X) {
					afterParam[lit] = true
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: %v", fset.Position(lit.Pos()), err)
			}
			checked++
			for _, what := range checkZoneSQL(s, afterParam[lit]) {
				t.Errorf("%s: SQL depends on the session's time zone: %s. %s", fset.Position(lit.Pos()), what, zoneRule)
			}
			return true
		})
	}
	if checked < 100 {
		t.Fatalf("checked only %d string literals; is the test running in internal/store?", checked)
	}
}

// endsInAdd reports whether the rightmost operand of e is a call to a
// method named add, which returns a $n placeholder.
func endsInAdd(e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.BinaryExpr:
			e = x.Y
		case *ast.ParenExpr:
			e = x.X
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			return ok && sel.Sel.Name == "add"
		default:
			return false
		}
	}
}

// A store call made without a date takes today in the app's zone
// (time.Local), not the database's: "expiring" counts from it, and an
// undated stock change is dated it. The zone is picked so that its day
// isn't UTC's right now.
func TestUndatedCallsUseTheAppsDay(t *testing.T) {
	zone := time.FixedZone("UTC-12", -12*60*60)
	if time.Now().UTC().Hour() >= 12 {
		zone = time.FixedZone("UTC+14", 14*60*60)
	}
	saved := time.Local
	time.Local = zone // before the pool opens, so none of its connections is running
	t.Cleanup(func() { time.Local = saved })
	s, ctx := newStore(t)
	today := stock.DateOnly(time.Now())
	if today.Equal(stock.DateOnly(time.Now().UTC())) {
		t.Fatalf("%s is on UTC's day", zone)
	}

	in := func(days int) *time.Time { d := today.AddDate(0, 0, days); return &d }
	mustItem(t, s, stock.ItemInput{Name: "Last day", Unit: stock.UnitML, Quantity: 100, ExpiresOn: in(store.ExpiringDays)})
	mustItem(t, s, stock.ItemInput{Name: "A day later", Unit: stock.UnitML, Quantity: 100, ExpiresOn: in(store.ExpiringDays + 1)})
	id := mustItem(t, s, stock.ItemInput{Name: "Yesterday", Unit: stock.UnitML, Quantity: 100, ExpiresOn: in(-1)})
	items, err := s.ListItems(ctx, store.ItemFilter{Expiring: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(items); !slices.Equal(got, []string{"Last day", "Yesterday"}) {
		t.Errorf("expiring = %v, want Last day and Yesterday", got)
	}

	m := stock.MovementInput{ItemID: id, Kind: stock.MoveCorrection, Delta: 5}
	if err := s.AddMovement(ctx, m); err != nil {
		t.Fatal(err)
	}
	moves, err := s.ListMovements(ctx, store.MovementFilter{ItemID: id})
	if err != nil {
		t.Fatal(err)
	}
	for _, mv := range moves {
		if !mv.OccurredOn.Equal(today) {
			t.Errorf("%s dated %s, want %s", mv.Kind, mv.OccurredOn.Format(time.DateOnly), today.Format(time.DateOnly))
		}
	}
}
