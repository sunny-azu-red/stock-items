package web

import (
	"html/template"
	"net/url"
	"slices"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// sortKey is one way a page can be sorted.
type sortKey struct {
	Key   string
	Label string // for the stock page's Sort by
	// Desc is the direction the key starts in: dates and amounts
	// descending, text ascending (pages may choose otherwise, as the stock
	// page does for quantity and best before: the least, the soonest first).
	Desc bool
}

// sortSpec is the whitelist of one page's sort keys. Only these keys ever
// reach the store, which maps them to ORDER BY itself.
type sortSpec struct {
	Keys []sortKey
	// Default is the key used when ?sort= is missing or unknown, in its
	// natural direction. "" keeps the records' own order (a kit's lines).
	Default string
}

func (s *sortSpec) key(k string) (sortKey, bool) {
	if s == nil {
		return sortKey{}, false
	}
	i := slices.IndexFunc(s.Keys, func(x sortKey) bool { return x.Key == k })
	if i < 0 {
		return sortKey{}, false
	}
	return s.Keys[i], true
}

// natural is the direction key starts in.
func (s *sortSpec) natural(key string) bool {
	k, _ := s.key(key)
	return k.Desc
}

// sortOrder is a page's current sort, read with sortSpec.parse.
type sortOrder struct {
	Key  string // a key of the spec, or "" for the records' own order
	Desc bool
	spec *sortSpec
}

// parse reads ?sort= and ?dir= (asc or desc). An unknown key falls back to
// the page's default, and a missing or unknown direction to the key's
// natural one.
func (s *sortSpec) parse(q url.Values) sortOrder {
	o := sortOrder{Key: s.Default, spec: s}
	if _, ok := s.key(q.Get("sort")); ok {
		o.Key = q.Get("sort")
	}
	o.Desc = s.natural(o.Key)
	if o.Key == "" {
		return o // the records' own order has no direction
	}
	switch q.Get("dir") {
	case "asc":
		o.Desc = false
	case "desc":
		o.Desc = true
	}
	return o
}

// Dir is "asc" or "desc".
func (o sortOrder) Dir() string {
	if o.Desc {
		return "desc"
	}
	return "asc"
}

// Natural reports whether o runs in its key's natural direction.
func (o sortOrder) Natural() bool { return o.Desc == o.spec.natural(o.Key) }

// set writes o into q as sort and dir, leaving out the page's default key
// and the key's natural direction, so links stay short.
func (o sortOrder) set(q url.Values) {
	q.Del("sort")
	q.Del("dir")
	if o.Key == "" {
		return
	}
	if o.Key != o.spec.Default {
		q.Set("sort", o.Key)
	}
	if !o.Natural() {
		q.Set("dir", o.Dir())
	}
}

// Values is o as query parameters, for links that keep the sort.
func (o sortOrder) Values() url.Values {
	q := url.Values{}
	o.set(q)
	return q
}

// sortHeads makes the sortable column headers of one table (the
// "sort-th" partial): plain links, so they work without JavaScript.
type sortHeads struct {
	Order  sortOrder
	Path   string
	Query  url.Values // the page's other parameters, which every link keeps
	Anchor string     // e.g. "history", to come back to the table
}

// sortCol is one column header.
type sortCol struct {
	Label string
	Num   bool         // a right-aligned number column
	Href  template.URL // "" for a column the page can't sort by
	Sort  string       // aria-sort: ascending, descending or none
	Title string       // what a click does
}

// Col is the header of the column sorted by key. A click on the sorted
// column flips its direction; another column starts in its natural
// direction. Either way the links go back to the first page.
func (h sortHeads) Col(key, label string, num bool) sortCol {
	c := sortCol{Label: label, Num: num, Sort: "none"}
	if _, ok := h.Order.spec.key(key); !ok {
		return c
	}
	next := h.Order
	if h.Order.Key == key {
		c.Sort = "descending"
		if !h.Order.Desc {
			c.Sort = "ascending"
		}
		next.Desc = !h.Order.Desc
	} else {
		next.Key, next.Desc = key, h.Order.spec.natural(key)
	}
	q := url.Values{}
	for k, v := range h.Query {
		q[k] = v
	}
	q.Del("page")
	next.set(q)
	href := joinQuery(h.Path, q.Encode())
	if h.Anchor != "" {
		href += "#" + h.Anchor
	}
	c.Href = template.URL(href) //nolint:gosec // the page's own path and a url.Values query
	c.Title = "Sort by " + label + ", " + map[bool]string{false: "ascending", true: "descending"}[next.Desc]
	return c
}

// joinQuery joins a path and an already encoded query string (see the
// withQuery template helper).
func joinQuery(path, query string) string {
	if query == "" {
		return path
	}
	return path + "?" + query
}

// The sort keys of each page.
var (
	stockSorts = &sortSpec{Default: store.SortName, Keys: []sortKey{
		{Key: store.SortName, Label: "Name"},
		{Key: store.SortCategory, Label: "Category"},
		{Key: store.SortLocation, Label: "Location"},
		{Key: store.SortQuantity, Label: "Quantity"},
		{Key: store.SortExpiry, Label: "Best before"},
		{Key: store.SortRecent, Label: "Recently changed", Desc: true},
	}}
	activitySorts = &sortSpec{Default: store.MoveSortDate, Keys: []sortKey{
		{Key: store.MoveSortDate, Desc: true},
		{Key: store.MoveSortItem},
		{Key: store.MoveSortKind},
		{Key: store.MoveSortChange, Desc: true},
		{Key: store.MoveSortPrice, Desc: true},
	}}
	// An item's history has no item column.
	historySorts = &sortSpec{Default: store.MoveSortDate, Keys: slices.DeleteFunc(slices.Clone(activitySorts.Keys), func(k sortKey) bool { return k.Key == store.MoveSortItem })}
	kitSorts     = &sortSpec{Keys: []sortKey{
		{Key: stock.KitSortItem},
		{Key: stock.KitSortNeed, Desc: true},
		{Key: stock.KitSortHave, Desc: true},
		{Key: stock.KitSortMissing, Desc: true},
	}}
)
