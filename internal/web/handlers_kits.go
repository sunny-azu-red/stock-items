package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// lineRow is one item + quantity row of a kit or service form.
type lineRow struct {
	ItemID int64
	Qty    string
	Unit   string     // sub-unit chosen next to Qty; "" means the item's base unit
	Badge  stockBadge // the chosen item's stock badge (describeRows)
	Short  bool       // a save was refused because there wasn't enough
	Error  string
}

// withEmptyRow ends a repeating list of rows with one empty row to type
// into, on add and edit forms alike; the Add button adds more.
func withEmptyRow(rows []lineRow) []lineRow {
	return append(rows, lineRow{})
}

// partRow is a prefilled row for qty (base units) of an item.
func partRow(itemID int64, qty float64, u stock.Unit) lineRow {
	row := lineRow{ItemID: itemID}
	row.Qty, row.Unit = stock.SplitForInput(qty, u)
	return row
}

// describeRows fills in each row's stock badge. withNeed (a service's
// parts, which saving takes from stock) compares what the rows ask for
// with what is in stock: the rows of one item together, as saving takes
// them all. An amount that can't be read asks for nothing; saving says why.
func describeRows(rows []lineRow, items []stock.Item, withNeed bool) {
	byID := itemsByID(items)
	need := map[int64]float64{}
	if withNeed {
		for _, r := range rows {
			if it, ok := byID[r.ItemID]; ok {
				if q, err := stock.ParseAmount(r.Qty, it.Unit, r.Unit); err == nil {
					need[r.ItemID] = stock.RoundQty(need[r.ItemID] + q)
				}
			}
		}
	}
	for i := range rows {
		if it, ok := byID[rows[i].ItemID]; ok {
			rows[i].Badge = newStockBadge(it, need[rows[i].ItemID], true)
		}
	}
}

// itemGroup groups items in a <select> (vehicle items first).
type itemGroup struct {
	Label string
	Items []stock.Item
}

// pickerItems lists the items to choose from in kit and service forms:
// the active ones, plus archived items already chosen in rows, so saving
// the form never drops a line.
func (s *Server) pickerItems(ctx context.Context, rows []lineRow) ([]stock.Item, error) {
	items, err := s.store.ListItems(ctx, store.ItemFilter{})
	if err != nil {
		return nil, err
	}
	have := itemsByID(items)
	var missing []int64
	for _, r := range rows {
		if _, ok := have[r.ItemID]; r.ItemID != 0 && !ok {
			missing = append(missing, r.ItemID)
		}
	}
	if len(missing) == 0 {
		return items, nil
	}
	more, err := s.store.ListItems(ctx, store.ItemFilter{IDs: missing})
	return append(items, more...), err
}

// itemOptions splits items into those for vehicleID and the rest.
func itemOptions(items []stock.Item, vehicle *stock.Vehicle) []itemGroup {
	if vehicle == nil {
		return []itemGroup{{Label: "Items", Items: items}}
	}
	mine := itemGroup{Label: "For " + vehicle.Name}
	other := itemGroup{Label: "Other items"}
	for _, it := range items {
		if it.HasVehicle(vehicle.ID) {
			mine.Items = append(mine.Items, it)
		} else {
			other.Items = append(other.Items, it)
		}
	}
	var out []itemGroup
	for _, g := range []itemGroup{mine, other} {
		if len(g.Items) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// parseLines reads parallel <prefix>_item / <prefix>_qty / <prefix>_unit
// fields. A unit typed into the quantity wins over the unit select.
func parseLines(r *http.Request, prefix string, items map[int64]stock.Item, errs stock.Errors) ([]lineRow, []stock.KitLineInput) {
	ids, qtys, units := r.PostForm[prefix+"_item"], r.PostForm[prefix+"_qty"], r.PostForm[prefix+"_unit"]
	var rows []lineRow
	var lines []stock.KitLineInput
	for i, raw := range ids {
		row := lineRow{}
		if i < len(qtys) {
			row.Qty = qtys[i]
		}
		if i < len(units) {
			row.Unit = units[i]
		}
		id, _ := strconv.ParseInt(raw, 10, 64)
		row.ItemID = id
		it, ok := items[id]
		switch {
		case id == 0 && row.Qty == "":
			continue // empty row
		case !ok:
			row.Error = "Choose an item"
		default:
			q, err := stock.ParseAmount(row.Qty, it.Unit, row.Unit)
			switch {
			case errors.Is(err, stock.ErrEmpty):
				row.Error = "Enter a quantity, e.g. " + examplesFor(it.Unit)
			case err != nil:
				row.Error = stock.Sentence(err.Error())
			case q <= 0:
				row.Error = "Quantity must be greater than zero"
			default:
				lines = append(lines, stock.KitLineInput{ItemID: id, Quantity: q, Unit: it.Unit})
			}
		}
		if row.Error != "" {
			errField := "lines"
			if prefix == "inter_line" {
				errField = "inter_lines"
			}
			errs.Add(errField, "Check the highlighted rows")
		}
		rows = append(rows, row)
	}
	return rows, lines
}

func itemsByID(items []stock.Item) map[int64]stock.Item {
	m := make(map[int64]stock.Item, len(items))
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

// ---- Kit form ----

type kitForm struct {
	ID                                         int64
	VehicleID                                  int64
	Name, IntervalDistance, IntervalMonths     string
	InterIntervalDistance, InterIntervalMonths string
	Notes                                      string
	Lines                                      []lineRow
	InterLines                                 []lineRow
	ShowInter                                  bool
	Stored                                     string // the saved name while editing, for the heading
	Version                                    int    // as the edit form was opened (see itemForm)
	Stale                                      *staleNote
	FormKey                                    string // see itemForm
	Errors                                     stock.Errors
}

type kitFormPage struct {
	Form       kitForm
	IsNew      bool
	Vehicles   []stock.Vehicle
	ItemGroups []itemGroup
	KitNames   []string
}

func hasRowInput(rows []lineRow) bool {
	for _, r := range rows {
		if r.ItemID != 0 || r.Qty != "" || r.Error != "" {
			return true
		}
	}
	return false
}

func (s *Server) renderKitForm(w http.ResponseWriter, r *http.Request, status int, f kitForm, isNew bool) error {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	items, err := s.pickerItems(ctx, append(slices.Clone(f.Lines), f.InterLines...))
	if err != nil {
		return err
	}
	names, err := s.store.KitNames(ctx)
	if err != nil {
		return err
	}
	f.ShowInter = f.ShowInter || f.InterIntervalDistance != "" || f.InterIntervalMonths != "" || hasRowInput(f.InterLines) ||
		f.Errors["inter_interval_distance"] != "" || f.Errors["inter_interval_months"] != "" || f.Errors["inter_lines"] != ""
	describeRows(f.Lines, items, false)
	describeRows(f.InterLines, items, false)
	var vehicle *stock.Vehicle
	for i := range vehicles {
		if vehicles[i].ID == f.VehicleID {
			vehicle = &vehicles[i]
		}
	}
	f.Lines = withEmptyRow(f.Lines)
	f.InterLines = withEmptyRow(f.InterLines)
	title := "Add kit"
	if !isNew {
		title = "Edit " + f.Stored
	}
	s.render(w, r, status, "kit_form", View{Title: title, Nav: "services", Data: kitFormPage{
		Form: f, IsNew: isNew, Vehicles: activeVehicles(vehicles, f.VehicleID), ItemGroups: itemOptions(items, vehicle),
		KitNames: names,
	}})
	return nil
}

func (s *Server) parseKitForm(r *http.Request) (kitForm, stock.KitInput, error) {
	items, err := s.store.ListItems(r.Context(), store.ItemFilter{Archived: store.ArchivedAll})
	if err != nil {
		return kitForm{}, stock.KitInput{}, err
	}
	f := kitForm{
		Name: formText(r, "name"), IntervalDistance: formText(r, "interval_distance"), IntervalMonths: formText(r, "interval_months"),
		InterIntervalDistance: formText(r, "inter_interval_distance"), InterIntervalMonths: formText(r, "inter_interval_months"),
		ShowInter: formText(r, "has_inter") == "1",
		Notes:     formText(r, "notes"), Version: parseVersion(r), FormKey: parseFormKey(r), Errors: stock.Errors{},
	}
	checkFormKey(f.Errors, f.FormKey)
	if id := parseOptID(formText(r, "vehicle_id")); id != nil {
		f.VehicleID = *id
	}
	in := stock.KitInput{VehicleID: f.VehicleID, Name: f.Name, Notes: f.Notes, Version: f.Version, FormKey: f.FormKey}
	if in.IntervalDistance, err = parseOptInt(f.IntervalDistance); err != nil {
		f.Errors.Add("interval_distance", err.Error())
	}
	if in.IntervalMonths, err = parseOptInt(f.IntervalMonths); err != nil {
		f.Errors.Add("interval_months", err.Error())
	}
	if in.InterIntervalDistance, err = parseOptInt(f.InterIntervalDistance); err != nil {
		f.Errors.Add("inter_interval_distance", err.Error())
	}
	if in.InterIntervalMonths, err = parseOptInt(f.InterIntervalMonths); err != nil {
		f.Errors.Add("inter_interval_months", err.Error())
	}
	byID := itemsByID(items)
	f.Lines, in.Lines = parseLines(r, "line", byID, f.Errors)
	f.InterLines, in.InterLines = parseLines(r, "inter_line", byID, f.Errors)
	if !f.ShowInter {
		// The intermediate schedule was removed (or never added): its
		// fields are dropped here, not only blanked by the page's script.
		f.InterIntervalDistance, f.InterIntervalMonths, f.InterLines = "", "", nil
		in.InterIntervalDistance, in.InterIntervalMonths, in.InterLines = nil, nil, nil
		for _, k := range []string{"inter_interval_distance", "inter_interval_months", "inter_lines"} {
			delete(f.Errors, k)
		}
	}
	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	return f, in, nil
}

func kitPath(id int64) string { return fmt.Sprintf("/kits/%d", id) }

func kitFormFrom(k stock.Kit) kitForm {
	f := kitForm{
		ID: k.ID, VehicleID: k.VehicleID, Name: k.Name, Stored: k.Name, IntervalDistance: optIntString(k.IntervalDistance),
		IntervalMonths: optIntString(k.IntervalMonths), InterIntervalDistance: optIntString(k.InterIntervalDistance),
		InterIntervalMonths: optIntString(k.InterIntervalMonths), ShowInter: k.HasIntermediate(),
		Notes: k.Notes, Version: k.Version,
	}
	for _, l := range k.Lines {
		f.Lines = append(f.Lines, partRow(l.ItemID, l.Need, l.Unit))
	}
	for _, l := range k.InterLines {
		f.InterLines = append(f.InterLines, partRow(l.ItemID, l.Need, l.Unit))
	}
	return f
}

func (s *Server) kitNew(w http.ResponseWriter, r *http.Request) error {
	f := kitForm{}
	if id := parseOptID(r.URL.Query().Get("vehicle")); id != nil {
		f.VehicleID = *id
	}
	return s.renderKitForm(w, r, http.StatusOK, f, true)
}

func (s *Server) kitCreate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatKit, kitPath, "/services"); done || err != nil {
		return err
	}
	f, in, err := s.parseKitForm(r)
	if err != nil {
		return err
	}
	if !f.Errors.Any() {
		id, err := s.store.CreateKit(r.Context(), in)
		if err == nil {
			s.redirect(w, r, kitPath(id), "Added "+in.Name)
			return nil
		}
		if s.savedAlready(w, r, err, kitPath, "/services") {
			return nil
		}
		if !formConflict(f.Errors, err) {
			return err
		}
	}
	return s.renderKitForm(w, r, http.StatusUnprocessableEntity, f, true)
}

func (s *Server) kitEdit(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	k, err := s.store.GetKit(r.Context(), id)
	if err != nil {
		return err
	}
	return s.renderKitForm(w, r, http.StatusOK, kitFormFrom(k), false)
}

func (s *Server) kitUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	// Saved already comes first: the first save moved the version on.
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatKit, kitPath, "/services"); done || err != nil {
		return err
	}
	f, in, err := s.parseKitForm(r)
	if err != nil {
		return err
	}
	f.ID = id
	if !f.Errors.Any() {
		err := s.store.UpdateKit(r.Context(), id, in)
		if err == nil {
			s.redirect(w, r, kitPath(id), "Saved "+in.Name)
			return nil
		}
		if s.savedAlready(w, r, err, kitPath, "/services") {
			return nil
		}
		if f.Stale = staleForm(err, kitPath(id)+"/edit"); f.Stale == nil && !formConflict(f.Errors, err) {
			return err
		}
	}
	// Deleted meanwhile, the form still shows what was typed.
	f.Stored = cmp.Or(f.Name, "kit")
	if k, err := s.store.GetKit(r.Context(), id); err == nil {
		f.Stored = k.Name
	}
	return s.renderKitForm(w, r, http.StatusUnprocessableEntity, f, false)
}

func (s *Server) kitDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	page := kitPath(id)
	k, err := s.store.GetKit(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatKit, id), page, "/services") {
		return nil
	} else if err != nil {
		return err
	}
	var conflict *store.ConflictError
	trashID, err := s.store.DeleteKit(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, page, "/services") {
		return nil
	} else if errors.As(err, &conflict) {
		s.setFlash(w, "error", conflict.Message)
		s.redirect(w, r, page, "")
		return nil
	} else if err != nil {
		return err
	}
	s.redirectUndo(w, r, vehiclePath(k.VehicleID), "Deleted "+k.Name, trashID)
	return nil
}

// kitArchive hides a kit that has logged services.
func (s *Server) kitArchive(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	page := kitPath(id)
	k, err := s.store.GetKit(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatKit, id), page, "/services") {
		return nil
	} else if err != nil {
		return err
	}
	err = s.store.ArchiveKit(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, page, "/services") {
		return nil
	} else if err != nil {
		return err
	}
	s.redirect(w, r, page, "Archived "+k.Name+". Its services keep it; restore it any time.")
	return nil
}

func (s *Server) kitRestore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	page := kitPath(id)
	k, err := s.store.GetKit(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatKit, id), page, "/services") {
		return nil
	} else if err != nil {
		return err
	}
	err = s.store.RestoreKit(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, page, "/services") {
		return nil
	} else if err != nil {
		return err
	}
	s.redirect(w, r, page, "Restored "+k.Name)
	return nil
}

type kitPage struct {
	View         kitView
	Vehicle      stock.Vehicle
	Services     []stock.ServiceRecord
	ServiceCount int             // all services logged with the kit, on any vehicle
	Lines        []stock.KitLine // the kit's lines in the order asked for
	InterLines   []stock.KitLine // the kit's intermediate lines in the order asked for
	Sort         sortHeads
}

func (s *Server) kitShow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	k, err := s.store.GetKit(ctx, id)
	if err != nil {
		return err
	}
	v, err := s.store.GetVehicle(ctx, k.VehicleID)
	if err != nil {
		return err
	}
	all, err := s.store.ListServices(ctx, store.ServiceFilter{VehicleID: k.VehicleID})
	if err != nil {
		return err
	}
	var done []stock.ServiceRecord
	for _, sr := range all {
		if sr.KitID != nil && *sr.KitID == id {
			done = append(done, sr)
		}
	}
	count, err := s.store.KitServices(ctx, id)
	if err != nil {
		return err
	}
	order := kitSorts.parse(r.URL.Query())
	p := kitPage{
		View: s.kitViews([]stock.Kit{k}, []stock.Vehicle{v})[0], Vehicle: v, Services: done, ServiceCount: count,
		Lines:      stock.SortKitLines(k.Lines, order.Key, order.Desc),
		InterLines: stock.SortKitLines(k.InterLines, order.Key, order.Desc),
		Sort:       sortHeads{Order: order, Path: kitPath(id)},
	}
	s.render(w, r, http.StatusOK, "kit_show", View{Title: k.Name + " · " + v.Name, Nav: "services", Data: p})
	return nil
}

// kitShortfall puts every missing part of a kit on the shopping list.
func (s *Server) kitShortfall(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	ctx := r.Context()
	k, err := s.store.GetKit(ctx, id)
	if err != nil {
		return s.gone(r, err, store.WhatKit, id) // deleted in another tab: not a 404
	}
	if k.Archived() {
		// Archived in another tab: no new work for it, its parts included.
		s.setFlash(w, "error", "This kit is archived; restore it first.")
		s.redirect(w, r, kitPath(id), "")
		return nil
	}
	n, missing := 0, 0
	addShort := func(lines []stock.KitLine) error {
		for _, l := range lines {
			if short := l.Short(); short > 0 && !l.Archived {
				changed, err := s.store.EnsureShopping(ctx, l.ItemID, short, "Short for "+k.VehicleName+": "+k.Name) // the note suggestions give
				var invalid stock.Errors
				if errors.As(err, &invalid) {
					continue // an amount the list can't hold, like a line's 1.5 pieces from before they had to be whole
				}
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				if changed {
					n++
				}
				missing++
			}
		}
		return nil
	}
	switch r.FormValue("intermediate") {
	case "0":
		if err := addShort(k.Lines); err != nil {
			return err
		}
	case "1":
		if err := addShort(k.InterLines); err != nil {
			return err
		}
	default:
		if err := addShort(k.ActiveLines(k.VehicleOdometer, s.now())); err != nil {
			return err
		}
		// Nothing missing for the schedule due next: offer the other one's.
		// Parts that are missing but already listed don't count as nothing.
		if missing == 0 && k.HasIntermediate() {
			other := k.InterLines
			if k.NextIsIntermediate(k.VehicleOdometer, s.now()) {
				other = k.Lines
			}
			if err := addShort(other); err != nil {
				return err
			}
		}
	}
	msg := "Nothing is missing for this kit"
	if missing > 0 && n == 0 {
		msg = "Everything missing is already on the shopping list"
	}
	if n > 0 {
		msg = "Added " + plural(n, "missing part") + " to the shopping list"
	}
	s.redirect(w, r, s.back(r, kitPath(id)), msg)
	return nil
}
