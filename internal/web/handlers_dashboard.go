package web

import (
	"net/http"

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
	stats, err := s.store.GetStats(ctx)
	if err != nil {
		return err
	}
	shopping, err := s.store.ListShopping(ctx)
	if err != nil {
		return err
	}
	onList := map[int64]bool{}
	for _, e := range shopping {
		if e.ItemID != nil {
			onList[*e.ItemID] = true
		}
	}

	p := dashboardPage{
		Vehicles: vehicles, VehicleMap: vehicleMap(vehicles), ItemCount: len(items), KitCount: len(kits),
		Recent: recent, Stats: stats, SuggestionCount: len(stock.SuggestPurchases(items, kits, onList)),
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

func (s *Server) activity(w http.ResponseWriter, r *http.Request) error {
	kind := stock.MovementKind(r.URL.Query().Get("kind"))
	kinds := []stock.MovementKind{stock.MovePurchase, stock.MoveUse, stock.MoveService, stock.MoveCorrection, stock.MoveInitial}
	valid := false
	for _, k := range kinds {
		valid = valid || k == kind
	}
	if !valid {
		kind = ""
	}
	moves, err := s.store.ListMovements(r.Context(), store.MovementFilter{Kind: kind, Limit: 300})
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "activity", View{Title: "Activity", Nav: "activity", Data: activityPage{
		Movements: moves, Kind: string(kind), Kinds: kinds,
	}})
	return nil
}
