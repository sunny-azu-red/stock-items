package web

import (
	"bytes"
	"net/http"
	"slices"

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

func (s *Server) activity(w http.ResponseWriter, r *http.Request) error {
	kind, kinds := activityKind(r), activityKinds
	moves, err := s.store.ListMovements(r.Context(), store.MovementFilter{Kind: kind, Limit: 300})
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "activity", View{Title: "Activity", Nav: "activity", Data: activityPage{
		Movements: moves, Kind: string(kind), Kinds: kinds,
	}})
	return nil
}

// activityExportCSV downloads every stock change (of the chosen kind).
func (s *Server) activityExportCSV(w http.ResponseWriter, r *http.Request) error {
	moves, err := s.store.ListMovements(r.Context(), store.MovementFilter{Kind: activityKind(r)})
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := stock.WriteMovementsCSV(&buf, moves); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="history-`+s.now().Format("2006-01-02")+`.csv"`)
	_, err = buf.WriteTo(w)
	return err
}
