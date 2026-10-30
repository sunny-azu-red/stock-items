package web

import (
	"bytes"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

type dashboardPage struct {
	Vehicles        []stock.Vehicle
	VehicleMap      map[int64]stock.Vehicle
	ItemCount       int
	LowItems        []stock.Item
	Expiring        []stock.Item
	Upcoming        []kitView
	KitCount        int
	Recent          []stock.Movement
	Stats           store.Stats
	SuggestionCount int
	ExpiringDays    int
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	items, err := s.store.ListItems(ctx, store.ItemFilter{})
	if err != nil {
		return err
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{})
	if err != nil {
		return err
	}
	recent, err := s.store.ListMovements(ctx, store.MovementFilter{Limit: 8})
	if err != nil {
		return err
	}
	stats, err := s.store.GetStats(ctx, s.now())
	if err != nil {
		return err
	}
	shopping, err := s.store.ListShopping(ctx)
	if err != nil {
		return err
	}

	p := dashboardPage{
		Vehicles: activeVehicles(vehicles), VehicleMap: vehicleMap(vehicles), ItemCount: len(items), KitCount: len(kits),
		Recent: recent, Stats: stats, SuggestionCount: len(stock.SuggestPurchases(items, kits, listedQuantities(shopping))),
		ExpiringDays: store.ExpiringDays,
	}
	today := s.now()
	for _, it := range items {
		if it.IsLow() {
			p.LowItems = append(p.LowItems, it)
		}
		if it.ExpiryState(today, store.ExpiringDays) != "" {
			p.Expiring = append(p.Expiring, it)
		}
	}
	for _, kv := range s.kitViews(kits, vehicles) {
		if kv.Due.State != stock.DueNone {
			p.Upcoming = append(p.Upcoming, kv)
		}
	}
	s.render(w, r, http.StatusOK, "dashboard", View{Title: "Dashboard", Nav: "dashboard", Data: p})
	return nil
}

type activityPage struct {
	Movements []stock.Movement
	Kind      string
	Kinds     []stock.MovementKind
	Item      *stock.Item // set when showing one item's history
	Page      int         // 1 is the newest
	More      bool        // there are older entries
}

// activityPageSize is how many stock changes one Activity page shows.
const activityPageSize = 200

// KindQuery is the query string for the given kind ("" for all), keeping
// the item filter and starting at the newest page.
func (p activityPage) KindQuery(kind string) string {
	q := url.Values{}
	if kind != "" {
		q.Set("kind", kind)
	}
	if p.Item != nil {
		q.Set("item", strconv.FormatInt(p.Item.ID, 10))
	}
	return q.Encode()
}

// PageQuery is the query string for page n with the current filters.
func (p activityPage) PageQuery(n int) string {
	q, _ := url.ParseQuery(p.KindQuery(p.Kind))
	if n > 1 {
		q.Set("page", strconv.Itoa(n))
	}
	return q.Encode()
}

var activityKinds = []stock.MovementKind{stock.MovePurchase, stock.MoveUse, stock.MoveService, stock.MoveCorrection, stock.MoveInitial}

// activityKind reads the ?kind= filter, ignoring unknown kinds.
func activityKind(r *http.Request) stock.MovementKind {
	kind := stock.MovementKind(r.URL.Query().Get("kind"))
	if slices.Contains(activityKinds, kind) {
		return kind
	}
	return ""
}

// activityItem reads the ?item= filter; nil for all items.
func (s *Server) activityItem(r *http.Request) (*stock.Item, error) {
	id := parseOptID(r.URL.Query().Get("item"))
	if id == nil {
		return nil, nil
	}
	it, err := s.store.GetItem(r.Context(), *id)
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) error {
	item, err := s.activityItem(r)
	if err != nil {
		return err
	}
	p := activityPage{Kind: string(activityKind(r)), Kinds: activityKinds, Item: item, Page: 1}
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 1 {
		p.Page = n
	}
	f := store.MovementFilter{Kind: stock.MovementKind(p.Kind), Limit: activityPageSize + 1, Offset: (p.Page - 1) * activityPageSize}
	if item != nil {
		f.ItemID = item.ID
	}
	if p.Movements, err = s.store.ListMovements(r.Context(), f); err != nil {
		return err
	}
	if len(p.Movements) > activityPageSize {
		p.Movements, p.More = p.Movements[:activityPageSize], true
	}
	title := "Activity"
	if item != nil {
		title = "History of " + item.DisplayName()
	}
	s.render(w, r, http.StatusOK, "activity", View{Title: title, Nav: "activity", Data: p})
	return nil
}

// activityExportCSV downloads every stock change (of the chosen kind and
// item).
func (s *Server) activityExportCSV(w http.ResponseWriter, r *http.Request) error {
	item, err := s.activityItem(r)
	if err != nil {
		return err
	}
	f := store.MovementFilter{Kind: activityKind(r)}
	if item != nil {
		f.ItemID = item.ID
	}
	moves, err := s.store.ListMovements(r.Context(), f)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := stock.WriteMovementsCSV(&buf, moves); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="history-`+s.now().Format("2006-01-02")+`.csv"`)
	_, err = buf.WriteTo(w)
	return err
}
