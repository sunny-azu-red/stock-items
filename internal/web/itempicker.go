package web

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// itemPicker is the list of stock items an item field offers (the
// "item-picker" partial: a kit or service form's part rows, Shopping's
// Item). Every picker lists items the same way (docs/design.md, Item
// lists): with a vehicle in scope, the items that fit it, "For" the
// vehicle (its own and shared ones), then "General"; with none, every item
// in the stock page's sections ("Only …", "Shared …", "General"). The page
// sends the items in those sections, each with its vehicles; app.js offers
// and heads them for the vehicle in scope, which may change while the page
// is open (a form's Vehicle chips).
type itemPicker struct {
	ID      string
	Options []pickerOption
	// For is the vehicle in scope when the page fixes it and no field on
	// the form says it (a vehicle's part of the shopping list); nil
	// otherwise: app.js reads a form's vehicle_id.
	For  *stock.Vehicle
	Rows bool // a part row's list: its quantity hint is the number alone
}

// pickerOption is one item of a picker: Group is its section's heading
// with no vehicle in scope, Vehicles its tags ("" for General), for app.js
// to tell what fits a vehicle (stock.Item.Fits).
type pickerOption struct {
	stock.Item
	Group    string
	Vehicles string
}

// newItemPicker lists items in the stock page's sections (vehicles: all of
// them, as the stock page groups by, so an item of an archived vehicle
// isn't headed General).
func newItemPicker(id string, items []stock.Item, vehicles []stock.Vehicle, scope *stock.Vehicle, rows bool) itemPicker {
	p := itemPicker{ID: id, For: scope, Rows: rows}
	for _, sec := range stock.GroupByVehicles(items, vehicles) {
		for _, it := range sec.Items {
			ids := make([]string, len(it.VehicleIDs))
			for i, v := range it.VehicleIDs {
				ids[i] = strconv.FormatInt(v, 10)
			}
			p.Options = append(p.Options, pickerOption{Item: it, Group: sec.PickerTitle(), Vehicles: strings.Join(ids, " ")})
		}
	}
	return p
}

// findVehicle is the vehicle with id among vehicles, nil for none.
func findVehicle(vehicles []stock.Vehicle, id int64) *stock.Vehicle {
	for i := range vehicles {
		if vehicles[i].ID == id {
			return &vehicles[i]
		}
	}
	return nil
}

// notForVehicle is the error for an item that doesn't fit the vehicle a
// form is for: the pickers don't offer it (another vehicle's part).
func notForVehicle(name string, v *stock.Vehicle) string {
	return "“" + name + "” isn't for " + v.Name + ": pick one from the list"
}

// soleVehicleID is the one active vehicle, 0 unless there is exactly one
// (soleVehicle): a new kit or service is for it.
func (s *Server) soleVehicleID(ctx context.Context) (int64, error) {
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return 0, err
	}
	return soleVehicle(vehicles), nil
}

// fittingRows keeps the rows whose item fits the vehicle (and empty ones):
// a form prefilled from elsewhere (a duplicated kit) offers only those.
func (s *Server) fittingRows(ctx context.Context, rows []lineRow, vehicleID int64) ([]lineRow, error) {
	var ids []int64
	for _, r := range rows {
		if r.ItemID != 0 {
			ids = append(ids, r.ItemID)
		}
	}
	if len(ids) == 0 {
		return rows, nil
	}
	items, err := s.store.ListItems(ctx, store.ItemFilter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := itemsByID(items)
	return slices.DeleteFunc(rows, func(r lineRow) bool {
		it, ok := byID[r.ItemID]
		return ok && !it.Fits(vehicleID)
	}), nil
}

// matchRow is an item already in stock with a free-text shopping entry's
// name ("Already have it?"): Chips tells whether its row shows its vehicles,
// as a stock row does (rowData.ShowChips).
type matchRow struct {
	stock.Item
	Chips bool
}

// matchRows are the items to offer for a free-text entry, on the list's part
// for vehicle here (0: the whole list): there only the items that fit it,
// as every item list offers, each without the chip of here alone.
func matchRows(items []stock.Item, here int64) []matchRow {
	var out []matchRow
	for _, it := range items {
		if here != 0 && !it.Fits(here) {
			continue
		}
		out = append(out, matchRow{Item: it, Chips: rowData{Item: it, Here: here}.ShowChips()})
	}
	return out
}

// shoppingMatches are the items named like a free-text entry, as a new
// item's form from it offers them (the whole list's: it isn't a vehicle's),
// and the vehicles their chips name.
func (s *Server) shoppingMatches(ctx context.Context, name string) ([]matchRow, map[int64]stock.Vehicle, error) {
	named, err := s.store.ItemsNamed(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return nil, nil, err
	}
	return matchRows(named, 0), vehicleMap(vehicles), nil
}
