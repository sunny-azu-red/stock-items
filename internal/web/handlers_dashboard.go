package web

import (
	"bytes"
	"cmp"
	"html/template"
	"maps"
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
	// VehicleID is the vehicle the dashboard is about (0 for all), and
	// VehicleName its name; Working the vehicle being worked on.
	VehicleID   int64
	VehicleName string
	Working     int64
}

// Link is the list a tile or a "more" link opens: path with extra (an
// encoded query) for the dashboard's vehicle, so the number on the tile is
// the number of rows it opens. The stock includes General, like the tiles.
func (p dashboardPage) Link(path, extra string) template.URL {
	q, _ := url.ParseQuery(extra)
	if path == "/stock" && p.VehicleID != 0 {
		q.Set("general", "1")
	}
	setVehicleParam(q, p.VehicleID, p.Working)
	return template.URL(joinQuery(path, q.Encode())) //nolint:gosec // our own paths, and a query url.Values encoded
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	working := workingOn(r, vehicles)
	vid, chosen := parseVehicleParam(r.URL.Query())
	if !chosen && working != 0 {
		http.Redirect(w, r, "/?v="+strconv.FormatInt(working, 10), http.StatusSeeOther)
		return nil
	}
	v := activeVehicle(vehicles, vid)
	if v == nil {
		for _, x := range vehicles {
			if x.ID == vid {
				v = &x
			}
		}
	}
	if v == nil {
		vid = 0
	}
	items, err := s.store.ListItems(ctx, store.ItemFilter{})
	if err != nil {
		return err
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{})
	if err != nil {
		return err
	}
	recent, err := s.store.ListMovements(ctx, store.MovementFilter{Limit: 8, VehicleID: vid})
	if err != nil {
		return err
	}
	stats, err := s.store.GetVehicleStats(ctx, s.now(), vid)
	if err != nil {
		return err
	}
	shopping, err := s.store.ListShopping(ctx)
	if err != nil {
		return err
	}

	p := dashboardPage{
		Vehicles: activeVehicles(vehicles), VehicleMap: vehicleMap(vehicles),
		Recent: recent, Stats: stats, SuggestionCount: len(stock.SuggestPurchases(items, kits, listedQuantities(shopping), *s.today())),
		VehicleID: vid, Working: working,
	}
	// The suggestions stay the whole shopping list's; the rest is the
	// vehicle's: what fits it, and its own kits.
	if vid != 0 {
		p.VehicleName = v.Name
		items = slices.DeleteFunc(items, func(it stock.Item) bool { return !it.Fits(vid) })
		kits = slices.DeleteFunc(kits, func(k stock.Kit) bool { return k.VehicleID != vid })
	}
	p.ItemCount, p.KitCount = len(items), len(kits)
	today := s.now()
	for _, it := range items {
		if it.IsLow() {
			p.LowItems = append(p.LowItems, it)
		}
		if it.ExpiryState(today, store.ExpiringDays) != "" {
			p.Expiring = append(p.Expiring, it)
		}
	}
	for _, kv := range scopeKits(s.kitViews(kits, vehicles), cmp.Or(vid, soleVehicle(vehicles))) {
		if kv.Due.State != stock.DueNone {
			p.Upcoming = append(p.Upcoming, kv)
		}
	}
	view := View{Title: "Dashboard", Nav: "dashboard", Data: p}
	if p.VehicleName != "" {
		view.Title = p.VehicleName + " dashboard"
	}
	if working != 0 && vid != working {
		view.WorkingAway = "/"
	}
	s.render(w, r, http.StatusOK, "dashboard", view)
	return nil
}

type activityPage struct {
	Movements []stock.Movement
	Kind      string
	Kinds     []stock.MovementKind
	Year      int         // 0 for every year
	Years     []int       // newest first, for the year picker
	Item      *stock.Item // set when showing one item's history
	Page      int         // 1 is the newest
	More      bool        // there are older entries
	Sort      sortOrder
	// VehicleID is the vehicle whose stock changes are shown (0 for all;
	// ignored with an item), VehicleName its name, Vehicles the active ones
	// for the chips, Working the vehicle being worked on.
	VehicleID   int64
	VehicleName string
	Vehicles    []stock.Vehicle
	Working     int64
}

// VehicleQuery is the query string for the given vehicle (0 for all),
// keeping the kind and the sort, and starting at the first page of every
// year (a vehicle may have none in the one shown).
func (p activityPage) VehicleQuery(id int64) string {
	p.VehicleID = id
	return p.filterValues(p.Kind, 0).Encode()
}

// activityPageSize is how many stock changes one Activity page shows.
const activityPageSize = 200

// KindQuery is the query string for the given kind ("" for all), keeping
// the year, the item filter and the sort, and starting at the first page.
func (p activityPage) KindQuery(kind string) string { return p.filterValues(kind, p.Year).Encode() }

// YearFields are the year picker's hidden fields: the kind, the item filter
// and the sort, so picking a year keeps them (and starts at the first page).
func (p activityPage) YearFields() []hiddenField {
	q := p.filterValues(p.Kind, 0)
	var fields []hiddenField
	for _, k := range slices.Sorted(maps.Keys(q)) {
		for _, v := range q[k] {
			fields = append(fields, hiddenField{Name: k, Value: v})
		}
	}
	return fields
}

// hiddenField is an <input type="hidden">.
type hiddenField struct{ Name, Value string }

// AllQuery drops the kind and the year, keeping the item filter and the sort.
func (p activityPage) AllQuery() string { return p.filterValues("", 0).Encode() }

func (p activityPage) filterValues(kind string, year int) url.Values {
	q := p.Sort.Values()
	if kind != "" {
		q.Set("kind", kind)
	}
	if year != 0 {
		q.Set("year", strconv.Itoa(year))
	}
	if p.Item != nil {
		q.Set("item", strconv.FormatInt(p.Item.ID, 10))
	} else {
		setVehicleParam(q, p.VehicleID, p.Working)
	}
	return q
}

// SortNote says how the table is sorted, e.g. "by price, highest first":
// on a phone the sorted column may be scrolled out of view.
func (p activityPage) SortNote() string {
	words := map[string][3]string{
		store.MoveSortDate:   {"", "oldest first", "newest first"},
		store.MoveSortItem:   {"by item, ", "A–Z", "Z–A"},
		store.MoveSortKind:   {"by type, ", "A–Z", "Z–A"},
		store.MoveSortChange: {"by change, ", "smallest first", "largest first"},
		store.MoveSortPrice:  {"by price, ", "lowest first", "highest first"},
	}[p.Sort.Key]
	if p.Sort.Desc {
		return words[0] + words[2]
	}
	return words[0] + words[1]
}

// Heads are the table's column headers, keeping the kind, year and item filters.
func (p activityPage) Heads() sortHeads {
	return sortHeads{Order: p.Sort, Path: "/activity", Query: p.filterValues(p.Kind, p.Year)}
}

// PageQuery is the query string for page n with the current filters.
func (p activityPage) PageQuery(n int) string {
	q := p.filterValues(p.Kind, p.Year)
	if n > 1 {
		q.Set("page", strconv.Itoa(n))
	}
	return q.Encode()
}

var activityKinds = []stock.MovementKind{stock.MoveBought, stock.MoveUsed, stock.MoveService, stock.MoveAdjusted, stock.MoveStarting}

// activityKind reads the ?kind= filter, ignoring unknown kinds. Old names
// (a bookmarked ?kind=purchase) still work.
func activityKind(r *http.Request) stock.MovementKind {
	kind, _ := stock.ParseMovementKind(r.URL.Query().Get("kind"))
	return kind
}

// activityYear reads the ?year= filter, ignoring anything but a year.
func activityYear(r *http.Request) int {
	y, err := strconv.Atoi(r.URL.Query().Get("year"))
	if err != nil || y < 1 || y > 9999 {
		return 0
	}
	return y
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

// activityVehicle reads the ?v= filter of the Activity page and its
// export: with no item and no vehicle in the address, the one worked on
// (defaulted, so the page goes to its address). It also returns the
// vehicles, the one worked on, and the chosen one's name.
func (s *Server) activityVehicle(r *http.Request, item *stock.Item) (vehicles []stock.Vehicle, id, working int64, name string, defaulted bool, err error) {
	if vehicles, err = s.store.ListVehicles(r.Context()); err != nil {
		return
	}
	working = workingOn(r, vehicles)
	if item != nil {
		return vehicles, 0, working, "", false, nil
	}
	id, chosen := parseVehicleParam(r.URL.Query())
	if !chosen && working != 0 {
		id, defaulted = working, true
	}
	for _, v := range vehicles {
		if v.ID == id {
			name = v.Name
		}
	}
	if name == "" {
		id = 0
	}
	return vehicles, id, working, name, defaulted, nil
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) error {
	item, err := s.activityItem(r)
	if err != nil {
		return err
	}
	vehicles, vid, working, vname, defaulted, err := s.activityVehicle(r, item)
	if err != nil {
		return err
	}
	p := activityPage{Kind: string(activityKind(r)), Kinds: activityKinds, Year: activityYear(r), Item: item, Page: parsePage(r),
		Sort: activitySorts.parse(r.URL.Query()), VehicleID: vid, VehicleName: vname, Working: working,
		Vehicles: activeVehicles(vehicles, vid)}
	if defaulted {
		http.Redirect(w, r, joinQuery("/activity", p.PageQuery(p.Page)), http.StatusSeeOther)
		return nil
	}
	var itemID int64
	if item != nil {
		itemID = item.ID
	}
	if p.Years, err = s.store.MovementYears(r.Context(), store.MovementFilter{ItemID: itemID, Kind: stock.MovementKind(p.Kind), VehicleID: vid}); err != nil {
		return err
	}
	// A year with nothing in it yet (this year's first days) still gets
	// its chip, so the filter shows.
	if p.Year != 0 && !slices.Contains(p.Years, p.Year) {
		p.Years = append(p.Years, p.Year)
		slices.SortFunc(p.Years, func(a, b int) int { return b - a })
	}
	f := store.MovementFilter{Kind: stock.MovementKind(p.Kind), Year: p.Year, Limit: activityPageSize + 1, Offset: (p.Page - 1) * activityPageSize,
		Sort: p.Sort.Key, Desc: p.Sort.Desc, VehicleID: vid}
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
	} else if vname != "" {
		title = vname + " activity"
	}
	view := View{Title: title, Nav: "activity", Data: p}
	if working != 0 && item == nil && vid != working {
		away := p
		away.VehicleID, away.Working = 0, 0
		view.WorkingAway = joinQuery("/activity", away.filterValues(p.Kind, p.Year).Encode())
	}
	s.render(w, r, http.StatusOK, "activity", view)
	return nil
}

// activityExportCSV downloads every stock change (of the chosen kind, year
// and item), in the order the page shows them.
func (s *Server) activityExportCSV(w http.ResponseWriter, r *http.Request) error {
	item, err := s.activityItem(r)
	if err != nil {
		return err
	}
	_, vid, _, _, _, err := s.activityVehicle(r, item)
	if err != nil {
		return err
	}
	order := activitySorts.parse(r.URL.Query())
	f := store.MovementFilter{Kind: activityKind(r), Year: activityYear(r), Sort: order.Key, Desc: order.Desc, VehicleID: vid}
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
