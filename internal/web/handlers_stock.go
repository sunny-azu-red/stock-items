package web

import (
	"bytes"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// stockFilter is the stock page filter, kept in the URL, so every view is
// a plain link that works without JavaScript and with Back.
type stockFilter struct {
	VehicleIDs []int64
	MatchAll   bool
	General    bool
	Query      string
	Category   string
	Low        bool
	Expiring   bool
	InStock    bool
	Archived   bool   // show archived items instead of active ones
	View       string // "grouped" (default) or "list"
	Sort       sortOrder
}

func parseStockFilter(q url.Values) stockFilter {
	return stockFilter{
		VehicleIDs: parseIDs(q["v"]),
		MatchAll:   q.Get("match") == "all",
		General:    q.Get("general") == "1",
		Query:      strings.TrimSpace(q.Get("q")),
		Category:   strings.TrimSpace(q.Get("cat")),
		Low:        q.Get("low") == "1",
		Expiring:   q.Get("exp") == "1",
		InStock:    q.Get("instock") == "1",
		Archived:   q.Get("archived") == "1",
		View:       q.Get("view"),
		Sort:       parseStockSort(q),
	}
}

// parseStockSort reads the stock page's sort. The filter form also sends
// the order the page was showing (was_sort, was_dir): when only the key
// was changed, the new key starts in its natural direction (newest first
// for Recently changed, A to Z for Name), as a column header would.
func parseStockSort(q url.Values) sortOrder {
	o := stockSorts.parse(q)
	if was := q.Get("was_sort"); was != "" && was != o.Key && q.Get("dir") == q.Get("was_dir") {
		o.Desc = stockSorts.natural(o.Key)
	}
	return o
}

// shownSort reports whether the request carries the filter form's
// was_sort or was_dir, which only the server reads: the address bar then
// gets the filter's own, shorter URL instead.
func shownSort(q url.Values) bool { return q.Has("was_sort") || q.Has("was_dir") }

func (f stockFilter) store() store.ItemFilter {
	sf := store.ItemFilter{
		VehicleIDs: f.VehicleIDs, MatchAll: f.MatchAll, General: f.General, Query: f.Query, Category: f.Category,
		LowStock: f.Low, Expiring: f.Expiring, InStock: f.InStock, Sort: f.Sort.Key, Desc: f.Sort.Desc,
	}
	if f.Archived {
		sf.Archived = store.ArchivedOnly
	}
	return sf
}

// Values encodes the filter as query parameters.
func (f stockFilter) Values() url.Values {
	q := url.Values{}
	for _, id := range f.VehicleIDs {
		q.Add("v", strconv.FormatInt(id, 10))
	}
	set := func(k string, on bool) {
		if on {
			q.Set(k, "1")
		}
	}
	if f.MatchAll {
		q.Set("match", "all")
	}
	set("general", f.General)
	set("low", f.Low)
	set("exp", f.Expiring)
	set("instock", f.InStock)
	set("archived", f.Archived)
	if f.Query != "" {
		q.Set("q", f.Query)
	}
	if f.Category != "" {
		q.Set("cat", f.Category)
	}
	if f.View == "list" {
		q.Set("view", "list")
	}
	f.Sort.set(q)
	return q
}

// Encode is the query string, e.g. "v=1&low=1".
func (f stockFilter) Encode() string { return f.Values().Encode() }

// Active reports whether any narrowing filter is applied (the view and the
// sort don't narrow anything).
func (f stockFilter) Active() bool {
	return len(f.VehicleIDs) > 0 || f.General || f.Query != "" || f.Category != "" || f.Low || f.Expiring || f.InStock || f.Archived
}

// HasVehicle reports whether vehicle id is selected.
func (f stockFilter) HasVehicle(id int64) bool {
	for _, v := range f.VehicleIDs {
		if v == id {
			return true
		}
	}
	return false
}

// WithView returns the query string for another view mode.
func (f stockFilter) WithView(view string) string {
	f.View = view
	return f.Encode()
}

// AllVehicles returns the query string without the vehicle filter, keeping
// the search, category, toggles, view and sort.
func (f stockFilter) AllVehicles() string {
	f.VehicleIDs, f.General, f.MatchAll = nil, false, false
	return f.Encode()
}

// Cleared returns the query string without any filter, keeping only how
// the stock is shown (view and sort).
func (f stockFilter) Cleared() string {
	return stockFilter{View: f.View, Sort: f.Sort}.Encode()
}

// IsCategory reports whether c is the selected category. The store
// matches categories case-insensitively, and so does this.
func (f stockFilter) IsCategory(c string) bool { return strings.EqualFold(c, f.Category) }

// SortKeys are the choices of Sort by.
func (f stockFilter) SortKeys() []sortKey { return stockSorts.Keys }

// DirLabels names the two directions of the current sort key, ascending
// first: "A–Z" and "Z–A" for names, "Newest first" for Recently changed.
func (f stockFilter) DirLabels() [2]string {
	switch f.Sort.Key {
	case store.SortQuantity:
		return [2]string{"Low first", "High first"}
	case store.SortExpiry:
		return [2]string{"Soonest first", "Latest first"}
	case store.SortRecent:
		return [2]string{"Oldest first", "Newest first"}
	}
	return [2]string{"A–Z", "Z–A"}
}

type stockPage struct {
	Filter      stockFilter
	Vehicles    []stock.Vehicle // active ones, for the filter chips
	AllVehicles []stock.Vehicle // archived too, for grouping and exports
	VehicleMap  map[int64]stock.Vehicle
	Sections    []stock.Section
	Items       []stock.Item
	Categories  []string
	Selected    []stock.Vehicle
}

func (s *Server) loadStock(r *http.Request) (stockPage, error) {
	ctx := r.Context()
	f := parseStockFilter(r.URL.Query())
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return stockPage{}, err
	}
	sf := f.store()
	sf.Today = s.now()
	items, err := s.store.ListItems(ctx, sf)
	if err != nil {
		return stockPage{}, err
	}
	p := stockPage{Filter: f, Vehicles: activeVehicles(vehicles, f.VehicleIDs...), AllVehicles: vehicles, VehicleMap: vehicleMap(vehicles), Items: items}
	for _, v := range vehicles {
		if f.HasVehicle(v.ID) {
			p.Selected = append(p.Selected, v)
		}
	}
	if f.View != "list" {
		p.Sections = stock.GroupByVehicles(items, vehicles)
	}
	return p, nil
}

func (s *Server) stockPage(w http.ResponseWriter, r *http.Request) error {
	p, err := s.loadStock(r)
	if err != nil {
		return err
	}
	if shownSort(r.URL.Query()) {
		canonical := joinQuery("/stock", p.Filter.Encode())
		if !isHTMX(r) {
			http.Redirect(w, r, canonical, http.StatusSeeOther)
			return nil
		}
		w.Header().Set("HX-Push-Url", canonical)
	}
	if p.Categories, err = s.store.Categories(r.Context()); err != nil {
		return err
	}
	title := "Stock"
	if len(p.Selected) == 1 {
		title = p.Selected[0].Name + " stock"
	}
	s.render(w, r, http.StatusOK, "stock", View{Title: title, Nav: "stock", Data: p})
	return nil
}

func (s *Server) stockExportText(w http.ResponseWriter, r *http.Request) error {
	p, err := s.loadStock(r)
	if err != nil {
		return err
	}
	sections := p.Sections
	if sections == nil {
		sections = stock.GroupByVehicles(p.Items, p.AllVehicles)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write([]byte(stock.ExportText(sections)))
	return err
}

func (s *Server) stockExportCSV(w http.ResponseWriter, r *http.Request) error {
	p, err := s.loadStock(r)
	if err != nil {
		return err
	}
	ids := make([]int64, len(p.Items))
	for i, it := range p.Items {
		ids[i] = it.ID
	}
	last, err := s.store.LastPurchases(r.Context(), ids)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := stock.WriteCSV(&buf, p.Items, p.AllVehicles, last); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="stock-`+s.now().Format("2006-01-02")+`.csv"`)
	_, err = buf.WriteTo(w)
	return err
}
