package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// inOrder checks that each of want appears in body, in this order.
func inOrder(t *testing.T, body string, want ...string) {
	t.Helper()
	at := -1
	for _, w := range want {
		i := strings.Index(body, w)
		if i < 0 {
			t.Errorf("%q not found", w)
			return
		}
		if i < at {
			t.Errorf("%q comes too early in %v", w, want)
		}
		at = i
	}
}

// table is the part of a page from its first table on.
func table(body string) string {
	if i := strings.Index(body, "<table"); i >= 0 {
		return body[i:]
	}
	return ""
}

func TestSortHeads(t *testing.T) {
	q := url.Values{"sort": {"price"}, "kind": {"use"}, "page": {"3"}}
	h := sortHeads{Order: activitySorts.parse(q), Path: "/activity", Query: url.Values{"kind": {"use"}, "page": {"3"}}}
	tests := []struct {
		key, sort, href string
	}{
		// The sorted column flips; the others start in their own direction,
		// and every link keeps the kind and goes back to the first page.
		{"price", "descending", "/activity?dir=asc&kind=use&sort=price"},
		{"date", "none", "/activity?kind=use"},
		{"item", "none", "/activity?kind=use&sort=item"},
		{"change", "none", "/activity?kind=use&sort=change"},
	}
	for _, tt := range tests {
		c := h.Col(tt.key, "X", false)
		if c.Sort != tt.sort || string(c.Href) != tt.href {
			t.Errorf("%s: sort %q href %q, want %q %q", tt.key, c.Sort, c.Href, tt.sort, tt.href)
		}
	}
	h.Order = activitySorts.parse(url.Values{"sort": {"price"}, "dir": {"asc"}})
	if c := h.Col("price", "Price", true); c.Sort != "ascending" || string(c.Href) != "/activity?kind=use&sort=price" {
		t.Errorf("ascending price: %+v", c)
	}

	// An item's history has no item column, and comes back to the table.
	hist := sortHeads{Order: historySorts.parse(url.Values{"sort": {"item"}}), Path: "/items/7", Anchor: "history"}
	if hist.Order.Key != "date" || !hist.Order.Desc {
		t.Errorf("sort=item on an item's history: %+v", hist.Order)
	}
	if c := hist.Col("item", "Item", false); c.Href != "" {
		t.Errorf("item column on an item's history: %+v", c)
	}
	if c := hist.Col("date", "Date", false); c.Sort != "descending" || c.Href != "/items/7?dir=asc#history" {
		t.Errorf("date on an item's history: %+v", c)
	}

	// Unknown values fall back to the default and never come out again.
	for _, raw := range []string{"sort=x%27%3BDROP&dir=up", "sort=&dir=", "dir=DESC"} {
		q, _ := url.ParseQuery(raw)
		o := activitySorts.parse(q)
		if o.Key != "date" || !o.Desc || len(o.Values()) != 0 {
			t.Errorf("%s: %+v %v", raw, o, o.Values())
		}
	}
	// A kit's lines have no default key, so no direction either.
	if o := kitSorts.parse(url.Values{"dir": {"desc"}}); o.Key != "" || o.Desc || len(o.Values()) != 0 {
		t.Errorf("kit without a key: %+v", o)
	}
}

func TestActivitySortHeaders(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	pads, oil := itemID(t, a.store, "Rear Brake"), itemID(t, a.store, "Silkolene")
	on := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, in := range []stock.MovementInput{
		{ItemID: pads, Kind: stock.MovePurchase, Delta: 1, TotalPrice: ptr(12.5), Supplier: "Pads shop", OccurredOn: &on},
		{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, TotalPrice: ptr(30.0), Supplier: "Oil shop", OccurredOn: &on},
	} {
		if err := a.store.AddMovement(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	rec := a.get("/activity")
	expect(t, rec, http.StatusOK,
		`<th aria-sort="descending"><a class="sort-link sorted" href="/activity?dir=asc" title="Sort by Date, ascending">Date`,
		`<th aria-sort="none"><a class="sort-link" href="/activity?sort=item"`,
		`<th aria-sort="none"><a class="sort-link" href="/activity?sort=kind"`,
		`<th class="num" aria-sort="none"><a class="sort-link" href="/activity?sort=change"`,
		`<th class="num" aria-sort="none"><a class="sort-link" href="/activity?sort=price"`,
		"Every change to your stock, newest first.")

	// By price, with a kind chosen: the links keep the kind, the chips and
	// the CSV keep the sort.
	rec = a.get("/activity?sort=price&kind=purchase")
	expect(t, rec, http.StatusOK,
		`<th class="num" aria-sort="descending"><a class="sort-link sorted" href="/activity?dir=asc&amp;kind=purchase&amp;sort=price"`,
		`<th aria-sort="none"><a class="sort-link" href="/activity?kind=purchase" title="Sort by Date, descending">`,
		`href="/activity?sort=price">Everything</a>`,
		`href="/activity?kind=use&amp;sort=price">Used</a>`,
		`href="/activity/export.csv?kind=purchase&amp;sort=price"`)
	expect(t, rec, http.StatusOK, "Every change to your stock, by price, highest first.")
	inOrder(t, table(rec.Body.String()), "Oil shop", "Pads shop")
	rec = a.get("/activity?sort=price&dir=asc&kind=purchase")
	expect(t, rec, http.StatusOK, `<th class="num" aria-sort="ascending"><a class="sort-link sorted" href="/activity?kind=purchase&amp;sort=price"`,
		"by price, lowest first.")
	inOrder(t, table(rec.Body.String()), "Pads shop", "Oil shop")

	// The CSV comes in the same order.
	rec = a.get("/activity/export.csv?sort=price&kind=purchase")
	expect(t, rec, http.StatusOK)
	inOrder(t, rec.Body.String(), "Oil shop", "Pads shop")
	rec = a.get("/activity/export.csv?sort=price&dir=asc&kind=purchase")
	inOrder(t, rec.Body.String(), "Pads shop", "Oil shop")

	// Unknown values are ignored, and don't end up in any link.
	rec = a.get("/activity?sort=m.id%3B+DROP+TABLE+items&dir=sideways")
	expect(t, rec, http.StatusOK, `<th aria-sort="descending"><a class="sort-link sorted" href="/activity?dir=asc"`)
	if b := rec.Body.String(); strings.Contains(b, "DROP") || strings.Contains(b, "sideways") {
		t.Error("an unknown sort came back in the page")
	}

	// An item's own Activity keeps the item in its links.
	expect(t, a.get("/activity?item="+itoa(pads)+"&sort=change"), http.StatusOK,
		`<th class="num" aria-sort="descending"><a class="sort-link sorted" href="/activity?dir=asc&amp;item=`+itoa(pads)+`&amp;sort=change"`)
}

func TestItemHistorySort(t *testing.T) {
	a := newApp(t)
	a.seed()
	ctx := context.Background()
	pads := itemID(t, a.store, "Rear Brake")
	for i, in := range []stock.MovementInput{
		{ItemID: pads, Kind: stock.MovePurchase, Delta: 2, TotalPrice: ptr(40.0), Supplier: "Dear shop"},
		{ItemID: pads, Kind: stock.MovePurchase, Delta: 2, TotalPrice: ptr(9.0), Supplier: "Cheap shop"},
	} {
		on := time.Date(2026, 10, 10+i, 0, 0, 0, 0, time.UTC)
		in.OccurredOn = &on
		if err := a.store.AddMovement(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	path := "/items/" + itoa(pads)

	rec := a.get(path)
	expect(t, rec, http.StatusOK, `id="history"`,
		`<th aria-sort="descending"><a class="sort-link sorted" href="`+path+`?dir=asc#history"`,
		`<th class="num" aria-sort="none"><a class="sort-link" href="`+path+`?sort=price#history"`)
	if strings.Contains(rec.Body.String(), "sort=item") {
		t.Error("an item's history offers to sort by item")
	}
	inOrder(t, table(rec.Body.String()), "Cheap shop", "Dear shop")

	rec = a.get(path + "?sort=price")
	expect(t, rec, http.StatusOK, `<th class="num" aria-sort="descending"><a class="sort-link sorted" href="`+path+`?dir=asc&amp;sort=price#history"`)
	inOrder(t, table(rec.Body.String()), "Dear shop", "Cheap shop")
	rec = a.get(path + "?sort=price&dir=asc")
	inOrder(t, table(rec.Body.String()), "Cheap shop", "Dear shop")
	// There is no item column to sort by here: the default instead.
	expect(t, a.get(path+"?sort=item"), http.StatusOK, `<th aria-sort="descending"><a class="sort-link sorted" href="`+path+`?dir=asc#history"`)
}

func TestKitPartsSort(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	pads, oil, ring := itemID(t, a.store, "Rear Brake"), itemID(t, a.store, "Silkolene"), itemID(t, a.store, "O-Ring")
	in := stock.KitInput{VehicleID: k390, Name: "Oil change", Lines: []stock.KitLineInput{
		{ItemID: ring, Quantity: 1},   // 6 on stock
		{ItemID: pads, Quantity: 2},   // 1 on stock: 1 short
		{ItemID: oil, Quantity: 1000}, // 525 ml: 475 ml short
	}}
	in.Normalize()
	id, err := a.store.CreateKit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	path := "/kits/" + itoa(id)

	// The kit's own order, with every column ready to sort.
	rec := a.get(path)
	expect(t, rec, http.StatusOK,
		`<th aria-sort="none"><a class="sort-link" href="`+path+`?sort=item"`,
		`<th class="num" aria-sort="none"><a class="sort-link" href="`+path+`?sort=need"`,
		`<th class="num" aria-sort="none"><a class="sort-link" href="`+path+`?sort=have"`,
		`<th class="num" aria-sort="none"><a class="sort-link" href="`+path+`?sort=missing"`)
	if strings.Contains(rec.Body.String(), "Kit order") {
		t.Error(`"Kit order" link on the kit's own order`)
	}
	inOrder(t, table(rec.Body.String()), "O-Ring", "Rear Brake Pads", "Silkolene")

	// What is missing first, then a way back to the kit's order.
	rec = a.get(path + "?sort=missing")
	expect(t, rec, http.StatusOK,
		`<th class="num" aria-sort="descending"><a class="sort-link sorted" href="`+path+`?dir=asc&amp;sort=missing"`,
		`href="`+path+`" title="Show the parts in the order of the kit">`)
	inOrder(t, table(rec.Body.String()), "Silkolene", "Rear Brake Pads", "O-Ring")

	rec = a.get(path + "?sort=item&dir=desc")
	expect(t, rec, http.StatusOK, `<th aria-sort="descending"><a class="sort-link sorted" href="`+path+`?sort=item"`)
	inOrder(t, table(rec.Body.String()), "Silkolene", "OEM Rear Brake Pads", "OEM Oil Filter O-Ring")
	inOrder(t, table(a.get(path+"?sort=item").Body.String()), "OEM Oil Filter O-Ring", "OEM Rear Brake Pads", "Silkolene")
	inOrder(t, table(a.get(path+"?sort=have").Body.String()), "Silkolene", "O-Ring", "Rear Brake Pads")

	// Unknown: the kit's own order.
	inOrder(t, table(a.get(path+"?sort=bogus&dir=desc").Body.String()), "O-Ring", "Rear Brake Pads", "Silkolene")
}

func TestStockSortBy(t *testing.T) {
	a := newApp(t)
	a.seed()

	// The select and the direction show the sort, and every link and export
	// keeps it; sorting isn't a filter.
	rec := a.get("/stock?sort=expiry&dir=desc&view=list")
	expect(t, rec, http.StatusOK,
		`<option value="expiry" selected>Best before</option>`,
		`<input type="radio" id="dir-desc" name="dir" value="desc" checked>`,
		`<input type="hidden" name="was_sort" value="expiry"><input type="hidden" name="was_dir" value="desc">`,
		"Soonest first", "Latest first",
		`href="/stock?dir=desc&amp;sort=expiry">Grouped</a>`,
		`href="/stock?dir=desc&amp;sort=expiry&amp;view=list">List</a>`,
		`href="/stock/export.txt?dir=desc&amp;sort=expiry&amp;view=list"`,
		`href="/stock/export.csv?dir=desc&amp;sort=expiry&amp;view=list"`,
		`id="filter-all" href="/stock?dir=desc&amp;sort=expiry&amp;view=list"`)
	if strings.Contains(rec.Body.String(), "clear filters") {
		t.Error("a sort counts as a filter")
	}
	expect(t, a.get("/stock?q=oil&sort=quantity"), http.StatusOK, `href="/stock?sort=quantity">clear filters</a>`)

	// Without a direction, Recently changed starts newest first.
	expect(t, a.get("/stock?sort=recent"), http.StatusOK,
		`<option value="recent" selected>`, `<input type="radio" id="dir-desc" name="dir" value="desc" checked>`, "Newest first")
	// Unknown values: by name, A to Z.
	rec = a.get("/stock?sort=bogus&dir=up")
	expect(t, rec, http.StatusOK, `<option value="name" selected>Name</option>`, `<input type="radio" id="dir-asc" name="dir" value="asc" checked>`, `href="/stock/export.txt"`)

	// The list, and each section of the grouped view, in the chosen order;
	// the sections themselves stay where they are.
	list := "/stock?view=list"
	inOrder(t, a.get(list).Body.String(), "10W50 Power Synt 4T", "M3.0 Ready To Use", "Oil Filter O-Ring", "Rear Brake Pads", "15W50 PRO 4 XP")
	inOrder(t, a.get(list+"&dir=desc").Body.String(), "15W50 PRO 4 XP", "Rear Brake Pads", "Oil Filter O-Ring", "M3.0 Ready To Use", "10W50 Power Synt 4T")
	inOrder(t, a.get("/stock").Body.String(), `aria-label="KTM 390"`, "Rear Brake Pads", "15W50 PRO 4 XP", `aria-label="KTM 1290"`, "section-shared", "M3.0 Ready To Use", "Oil Filter O-Ring")
	inOrder(t, a.get("/stock?dir=desc").Body.String(), `aria-label="KTM 390"`, "15W50 PRO 4 XP", "Rear Brake Pads", `aria-label="KTM 1290"`, "section-shared", "Oil Filter O-Ring", "M3.0 Ready To Use")

	// The exports follow the sort.
	inOrder(t, a.get("/stock/export.csv?dir=desc").Body.String(), "15W50 PRO 4 XP", "Rear Brake Pads", "Oil Filter O-Ring", "M3.0 Ready To Use", "10W50 Power Synt 4T")
	inOrder(t, a.get("/stock/export.csv").Body.String(), "10W50 Power Synt 4T", "M3.0 Ready To Use", "Oil Filter O-Ring", "Rear Brake Pads", "15W50 PRO 4 XP")
	inOrder(t, a.get("/stock/export.txt?dir=desc").Body.String(), "15W50 PRO 4 XP", "Rear Brake Pads", "Oil Filter O-Ring", "M3.0 Ready To Use")
	inOrder(t, a.get("/stock/export.txt").Body.String(), "Rear Brake Pads", "15W50 PRO 4 XP", "M3.0 Ready To Use", "Oil Filter O-Ring")

	// The form sends what the page showed: a new key starts in its own
	// direction, a new direction is kept, and the address bar gets the
	// filter's own URL.
	tests := []struct{ query, want string }{
		{"sort=recent&dir=asc&was_sort=name&was_dir=asc", "/stock?sort=recent"},
		{"sort=name&dir=asc&was_sort=recent&was_dir=desc", "/stock"},
		{"sort=name&dir=desc&was_sort=name&was_dir=asc", "/stock?dir=desc"},
		{"sort=recent&dir=asc&was_sort=name&was_dir=desc", "/stock?dir=asc&sort=recent"},
		{"q=oil&low=1&sort=quantity&dir=desc&was_sort=quantity&was_dir=asc&view=list", "/stock?dir=desc&low=1&q=oil&sort=quantity&view=list"},
	}
	for _, tt := range tests {
		rec := a.get("/stock?" + tt.query)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tt.want {
			t.Errorf("%s: %d to %q, want %q", tt.query, rec.Code, rec.Header().Get("Location"), tt.want)
		}
		req := httptest.NewRequest(http.MethodGet, "/stock?"+tt.query, nil)
		req.Header.Set("HX-Request", "true")
		rec = a.do(req)
		if rec.Code != http.StatusOK || rec.Header().Get("HX-Push-Url") != tt.want {
			t.Errorf("%s with htmx: %d, HX-Push-Url %q, want %q", tt.query, rec.Code, rec.Header().Get("HX-Push-Url"), tt.want)
		}
	}
	// The new key's direction shows, labelled for it.
	req := httptest.NewRequest(http.MethodGet, "/stock?sort=recent&dir=asc&was_sort=name&was_dir=asc", nil)
	req.Header.Set("HX-Request", "true")
	expect(t, a.do(req), http.StatusOK, `<input type="radio" id="dir-desc" name="dir" value="desc" checked>`, `name="was_sort" value="recent"`, "Newest first")
}
