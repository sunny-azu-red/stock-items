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

// stockFilter is the stock page filter, kept in the URL so every view can
// be bookmarked and shared.
type stockFilter struct {
	VehicleIDs []int64
	MatchAll   bool
	General    bool
	Query      string
	Category   string
	Low        bool
	Expiring   bool
	InStock    bool
	View       string // "grouped" (default) or "list"
	Sort       string
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
		View:       q.Get("view"),
		Sort:       q.Get("sort"),
	}
}

func (f stockFilter) store() store.ItemFilter {
	return store.ItemFilter{
		VehicleIDs: f.VehicleIDs, MatchAll: f.MatchAll, General: f.General, Query: f.Query, Category: f.Category,
		LowStock: f.Low, Expiring: f.Expiring, InStock: f.InStock, Sort: f.Sort,
	}
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
	if f.Query != "" {
		q.Set("q", f.Query)
	}
	if f.Category != "" {
		q.Set("cat", f.Category)
	}
	if f.View == "list" {
		q.Set("view", "list")
	}
	if f.Sort != "" {
		q.Set("sort", f.Sort)
	}
	return q
}

// Encode is the query string, e.g. "v=1&low=1".
func (f stockFilter) Encode() string { return f.Values().Encode() }

// Active reports whether any narrowing filter is applied.
func (f stockFilter) Active() bool {
	return len(f.VehicleIDs) > 0 || f.General || f.Query != "" || f.Category != "" || f.Low || f.Expiring || f.InStock
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

type stockPage struct {
	Filter     stockFilter
	Vehicles   []stock.Vehicle
	VehicleMap map[int64]stock.Vehicle
	Sections   []stock.Section
	Items      []stock.Item
	Categories []string
	Selected   []stock.Vehicle
}

func (s *Server) loadStock(r *http.Request) (stockPage, error) {
	ctx := r.Context()
	f := parseStockFilter(r.URL.Query())
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return stockPage{}, err
	}
	items, err := s.store.ListItems(ctx, f.store())
	if err != nil {
		return stockPage{}, err
	}
	p := stockPage{Filter: f, Vehicles: vehicles, VehicleMap: vehicleMap(vehicles), Items: items}
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
		sections = stock.GroupByVehicles(p.Items, p.Vehicles)
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
	if err := stock.WriteCSV(&buf, p.Items, p.Vehicles, last); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="stock-`+s.now().Format("2006-01-02")+`.csv"`)
	_, err = buf.WriteTo(w)
	return err
}
