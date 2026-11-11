package web

import (
	"bytes"
	"net/http"
	"net/url"
	"slices"
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
	// All is v=all: every vehicle, chosen on purpose while one is worked on.
	All bool
	// Working is the vehicle being worked on (0 for none); never read from
	// the address.
	Working int64
}

func parseStockFilter(q url.Values) stockFilter {
	return stockFilter{
		VehicleIDs: parseIDs(q["v"]),
		All:        slices.Contains(q["v"], "all"),
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

// fromFilterForm reports whether the request comes from the filter form,
// which always sends was_sort and was_dir: only the server reads them, and
// the address bar gets the filter's own, shorter URL instead. A form with
// no vehicle ticked means every vehicle, not the one worked on.
func fromFilterForm(q url.Values) bool { return q.Has("was_sort") || q.Has("was_dir") }

// chosen reports whether the address chooses the vehicles (some, General,
// or all): then it wins over the vehicle being worked on.
func (f stockFilter) chosen() bool { return len(f.VehicleIDs) > 0 || f.General || f.All }

// followsWorking reports whether the filter is the vehicle being worked on
// with General, the stock it starts at.
func (f stockFilter) followsWorking() bool {
	return f.Working != 0 && !f.All && !f.MatchAll && f.General && slices.Equal(f.VehicleIDs, []int64{f.Working})
}

// narrowed reports whether anything but the vehicles narrows the stock.
func (f stockFilter) narrowed() bool {
	return f.Query != "" || f.Category != "" || f.Low || f.Expiring || f.InStock || f.Archived
}

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
	if len(f.VehicleIDs) == 0 && !f.General && f.Working != 0 {
		q.Set("v", "all")
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
// sort don't narrow anything, and neither does the vehicle being worked on:
// Clear filters goes back to it).
func (f stockFilter) Active() bool {
	vehicles := (len(f.VehicleIDs) > 0 || f.General) && !f.followsWorking()
	return vehicles || f.narrowed()
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
	f.VehicleIDs, f.General, f.MatchAll, f.All = nil, false, false, true
	return f.Encode()
}

// Away is the address of this view for the vehicle being worked on: the
// same filters with no vehicle, which goes to it.
func (f stockFilter) Away() string {
	f.VehicleIDs, f.General, f.MatchAll, f.All, f.Working = nil, false, false, false, 0
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
	Sole        int64 // soleVehicle
	// Others is how many more items the search and filters find in other
	// vehicles, while the stock follows the vehicle being worked on.
	Others int
	// defaulted: the address chose no vehicle and got the one worked on.
	defaulted bool
}

// Here is the one vehicle the stock is filtered to, or 0: the page names it
// once in its title, its rows and headings leave out its chip.
func (p stockPage) Here() int64 {
	if len(p.Selected) == 1 {
		return p.Selected[0].ID
	}
	return 0
}

// RowScope is the vehicle whose chip the list rows leave out: Here, else
// the only active vehicle.
func (p stockPage) RowScope() int64 {
	if h := p.Here(); h != 0 {
		return h
	}
	return p.Sole
}

func (s *Server) loadStock(r *http.Request) (stockPage, error) {
	ctx := r.Context()
	q := r.URL.Query()
	f := parseStockFilter(q)
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return stockPage{}, err
	}
	f.Working = workingOn(r, vehicles)
	defaulted := false
	switch {
	case f.chosen():
	case fromFilterForm(q):
		f.All = true
	case f.Working != 0:
		f.VehicleIDs, f.General, defaulted = []int64{f.Working}, true, true
	}
	if len(f.VehicleIDs) > 0 || f.General {
		f.All = false
	}
	sf := f.store()
	sf.Today = s.now()
	items, err := s.store.ListItems(ctx, sf)
	if err != nil {
		return stockPage{}, err
	}
	p := stockPage{Filter: f, Vehicles: activeVehicles(vehicles, f.VehicleIDs...), AllVehicles: vehicles, VehicleMap: vehicleMap(vehicles), Items: items,
		Sole: soleVehicle(vehicles), defaulted: defaulted}
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
	if p.defaulted {
		http.Redirect(w, r, joinQuery("/stock", p.Filter.Encode()), http.StatusSeeOther)
		return nil
	}
	if fromFilterForm(r.URL.Query()) {
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
	f := p.Filter
	if f.followsWorking() && f.narrowed() {
		// A search while working on a vehicle says what it found elsewhere,
		// so the vehicle never hides an item without saying so.
		sf := f.store()
		sf.VehicleIDs, sf.General, sf.Today = nil, false, s.now()
		all, err := s.store.ListItems(r.Context(), sf)
		if err != nil {
			return err
		}
		p.Others = len(all) - len(p.Items)
	}
	title := "Stock"
	if len(p.Selected) == 1 {
		title = p.Selected[0].Name + " stock"
	}
	v := View{Title: title, Nav: "stock", Data: p}
	if f.Working != 0 && !f.followsWorking() {
		v.WorkingAway = joinQuery("/stock", f.Away())
	}
	s.render(w, r, http.StatusOK, "stock", v)
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
