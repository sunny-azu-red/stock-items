package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// lineRow is one item + quantity row of a kit or service form: the item
// picked or named (ItemID), and Text, its name as typed or shown.
type lineRow struct {
	ItemID int64
	Text   string
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

// describeRows fills in each row's stock badge, and the name of a row's
// item where nothing was typed (a saved row). withNeed (a service's
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
			if rows[i].Text == "" {
				rows[i].Text = it.DisplayName()
			}
		}
	}
}

// pickerItems lists the items to choose from in kit and service forms:
// the active ones, plus archived items already chosen in rows, so saving
// the form never drops a line. All vehicles' items: the form's list
// follows its Vehicle field (itemPicker).
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

// parseLines reads parallel <prefix>_item / <prefix>_item_text /
// <prefix>_qty / <prefix>_unit fields: the item picked (its id) or named
// (lineItem), and its quantity. A unit typed into the quantity wins over
// the unit select. A form from before the item text posts no text. A
// part must fit the form's vehicle (nil: none chosen yet), unless the
// record had it (had: a service's own parts; nil for none).
func parseLines(r *http.Request, prefix string, items map[int64]stock.Item, vehicle *stock.Vehicle, had map[int64]bool, errs stock.Errors) ([]lineRow, []stock.KitLineInput) {
	ids, texts := r.PostForm[prefix+"_item"], r.PostForm[prefix+"_item_text"]
	qtys, units := r.PostForm[prefix+"_qty"], r.PostForm[prefix+"_unit"]
	var rows []lineRow
	var lines []stock.KitLineInput
	for i, raw := range ids {
		row := lineRow{}
		if i < len(texts) {
			row.Text = strings.TrimSpace(texts[i])
		}
		if i < len(qtys) {
			row.Qty = qtys[i]
		}
		if i < len(units) {
			row.Unit = units[i]
		}
		id, _ := strconv.ParseInt(raw, 10, 64)
		it, msg := lineItem(id, row.Text, items, vehicle, had[id])
		row.ItemID = it.ID
		switch {
		case id == 0 && row.Text == "" && row.Qty == "":
			continue // empty row
		case msg != "":
			row.Error = msg
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
				lines = append(lines, stock.KitLineInput{ItemID: it.ID, Quantity: q, Unit: it.Unit})
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

// lineItem is the item of a part row: the one picked (id), which the page
// clears as soon as the text no longer names it, so a posted one stands
// even when the item was renamed since (and an archived one a row has
// stays); else the one active item the text names (stock.NamesItem): an
// archived item is in the page's list only for the rows that have it, and
// isn't matched by name there either. Either way it must fit the vehicle
// (stock.Item.Fits; nil: none chosen), as the page offers only those, and
// a name counts only the items that do; a picked item the record had
// (had) stays all the same. Otherwise msg says why there is none: a part
// must be an item in stock.
func lineItem(id int64, text string, items map[int64]stock.Item, vehicle *stock.Vehicle, had bool) (it stock.Item, msg string) {
	fits := func(it stock.Item) bool { return vehicle == nil || it.Fits(vehicle.ID) }
	if it, ok := items[id]; ok {
		if !fits(it) && !had {
			return stock.Item{}, notForVehicle(it.DisplayName(), vehicle)
		}
		return it, ""
	}
	if text == "" {
		return stock.Item{}, "Choose an item"
	}
	var named []stock.Item
	others := 0 // named, but for other vehicles
	for _, it := range items {
		if it.Archived() || !stock.NamesItem(text, it.Brand, it.Name) {
			continue
		}
		if fits(it) {
			named = append(named, it)
		} else {
			others++
		}
	}
	switch {
	case len(named) == 1:
		return named[0], ""
	case len(named) == 0 && others > 0:
		return stock.Item{}, notForVehicle(text, vehicle)
	case len(named) == 0:
		return stock.Item{}, "No item is called “" + text + "”: pick one from the list"
	default:
		return stock.Item{}, strconv.Itoa(len(named)) + " items are called “" + text + "”: pick one from the list"
	}
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
	ID                                     int64
	VehicleID                              int64
	Name, IntervalDistance, IntervalMonths string
	Intermediate                           bool // see stock.Kit.Intermediate
	BreakInAt                              string
	Notes                                  string
	Lines                                  []lineRow
	InterLines                             []lineRow
	Stored                                 string // the saved name while editing, for the heading
	Version                                int    // as the edit form was opened (see itemForm)
	Stale                                  *staleNote
	FormKey                                string // see itemForm
	Errors                                 stock.Errors
}

type kitFormPage struct {
	Form     kitForm
	IsNew    bool
	Vehicles []stock.Vehicle
	Picker   itemPicker // the part rows' items
	KitNames []string
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
	describeRows(f.Lines, items, false)
	describeRows(f.InterLines, items, false)
	f.Lines = withEmptyRow(f.Lines)
	f.InterLines = withEmptyRow(f.InterLines)
	title := "Add kit"
	if !isNew {
		title = "Edit " + f.Stored
	}
	s.render(w, r, status, "kit_form", View{Title: title, Nav: "services", Data: kitFormPage{
		Form: f, IsNew: isNew, Vehicles: activeVehicles(vehicles, f.VehicleID), Picker: newItemPicker("part-items", items, vehicles, nil, true),
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
		Intermediate: formText(r, "intermediate") == "1", BreakInAt: formText(r, "break_in_at"),
		Notes: formText(r, "notes"), Version: parseVersion(r), FormKey: parseFormKey(r), Errors: stock.Errors{},
	}
	checkFormKey(f.Errors, f.FormKey)
	if id := parseOptID(formText(r, "vehicle_id")); id != nil {
		f.VehicleID = *id
	}
	in := stock.KitInput{VehicleID: f.VehicleID, Name: f.Name, Intermediate: f.Intermediate, Notes: f.Notes, Version: f.Version, FormKey: f.FormKey}
	if in.IntervalDistance, err = parseOptInt(f.IntervalDistance); err != nil {
		f.Errors.Add("interval_distance", err.Error())
	}
	if in.IntervalMonths, err = parseOptInt(f.IntervalMonths); err != nil {
		f.Errors.Add("interval_months", err.Error())
	}
	if in.BreakInAt, err = parseOptInt(f.BreakInAt); err != nil {
		f.Errors.Add("break_in_at", err.Error())
	}
	vehicles, err := s.store.ListVehicles(r.Context())
	if err != nil {
		return kitForm{}, stock.KitInput{}, err
	}
	byID, vehicle := itemsByID(items), findVehicle(vehicles, f.VehicleID)
	// A kit is for jobs to come: its parts must fit its vehicle, so one
	// retagged since is flagged at its row rather than kept.
	f.Lines, in.Lines = parseLines(r, "line", byID, vehicle, nil, f.Errors)
	f.InterLines, in.InterLines = parseLines(r, "inter_line", byID, vehicle, nil, f.Errors)
	if !in.Intermediate {
		// No intermediate service (or no longer): its parts are dropped
		// here, not only hidden by the page.
		f.InterLines, in.InterLines = nil, nil
		delete(f.Errors, "inter_lines")
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
		IntervalMonths: optIntString(k.IntervalMonths), Intermediate: k.Intermediate, BreakInAt: optIntString(k.BreakInAt),
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
	var f kitForm
	wid, err := s.working(r)
	if err != nil {
		return err
	}
	f.VehicleID = wid
	if id := parseOptID(r.URL.Query().Get("vehicle")); id != nil {
		if wid != 0 && *id != wid {
			// While a vehicle is worked on, new work is for it.
			http.Redirect(w, r, "/kits/new", http.StatusSeeOther)
			return nil
		}
		f.VehicleID = *id
	}
	if id := parseOptID(r.URL.Query().Get("from")); id != nil {
		// Duplicate: start from another kit's fields; nothing is saved yet.
		k, err := s.store.GetKit(r.Context(), *id)
		if err != nil {
			return err
		}
		// Another vehicle's kit is out of view while one is worked on, as
		// its own page is.
		if done, err := s.elsewhere(w, r, k.VehicleID, vehiclePath); done || err != nil {
			return err
		}
		f = duplicateKitForm(k, f.VehicleID)
	}
	if f.VehicleID == 0 {
		// With one vehicle, the kit is for it.
		if f.VehicleID, err = s.soleVehicleID(r.Context()); err != nil {
			return err
		}
	}
	if f.VehicleID != 0 {
		// A copy (Duplicate, often for another vehicle) keeps only the
		// parts that fit its vehicle, as the form offers only those.
		if f.Lines, err = s.fittingRows(r.Context(), f.Lines, f.VehicleID); err != nil {
			return err
		}
		if f.InterLines, err = s.fittingRows(r.Context(), f.InterLines, f.VehicleID); err != nil {
			return err
		}
	}
	return s.renderKitForm(w, r, http.StatusOK, f, true)
}

// duplicateKitForm is a new kit's form holding k's name, schedule, parts
// and notes, usually for another vehicle: the one worked on, else none yet,
// so the form asks. Parts whose item is archived are left out, as new work
// doesn't use them.
func duplicateKitForm(k stock.Kit, vehicleID int64) kitForm {
	f := kitFormFrom(k)
	f.ID, f.Version, f.Stored, f.VehicleID = 0, 0, "", vehicleID
	f.Lines, f.InterLines = nil, nil
	for _, l := range k.Lines {
		if !l.Archived {
			f.Lines = append(f.Lines, partRow(l.ItemID, l.Need, l.Unit))
		}
	}
	for _, l := range k.InterLines {
		if !l.Archived {
			f.InterLines = append(f.InterLines, partRow(l.ItemID, l.Need, l.Unit))
		}
	}
	return f
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
	// New work is for the vehicle worked on (a form opened before a switch).
	msg, err := s.otherWork(r, in.VehicleID, "add this kit", false)
	if err != nil {
		return err
	}
	if msg != "" {
		f.Errors.Add("vehicle_id", msg)
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
	// Kits are on their vehicle's page.
	if done, err := s.elsewhere(w, r, k.VehicleID, vehiclePath); done || err != nil {
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
	ServiceCount int               // all services logged with the kit, on any vehicle
	Lines        []stock.KitLine   // the kit's lines in the order asked for
	InterLines   []stock.KitLine   // the kit's intermediate lines in the order asked for
	Listed       map[int64]float64 // what the shopping list asks for of each item (stock.KitLine.Listed)
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
	if done, err := s.elsewhere(w, r, k.VehicleID, vehiclePath); done || err != nil {
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
	shopping, err := s.store.ListShopping(ctx)
	if err != nil {
		return err
	}
	listed := map[int64]float64{}
	for _, e := range shopping {
		if e.ItemID != nil && e.Quantity != nil {
			listed[*e.ItemID] = *e.Quantity
		}
	}
	order := kitSorts.parse(r.URL.Query())
	p := kitPage{
		Listed: listed,
		View:   s.kitViews([]stock.Kit{k}, []stock.Vehicle{v})[0], Vehicle: v, Services: done, ServiceCount: count,
		Lines:      stock.SortKitLines(k.Lines, order.Key, order.Desc),
		InterLines: stock.SortKitLines(k.InterLines, order.Key, order.Desc),
		Sort:       sortHeads{Order: order, Path: kitPath(id)},
	}
	s.render(w, r, http.StatusOK, "kit_show", View{Title: k.Name + " · " + v.Name, Nav: "services", Data: p})
	return nil
}

// kitShortfall puts the missing parts of a kit on the shopping list: of the
// schedule a parts card posts (intermediate 0 or 1), or, from a page opened
// before the cards had the button, of the schedule due next.
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
				changed, err := s.store.EnsureShopping(ctx, l.ItemID, short, stock.ShortNote(k.Name, "kit")) // the note suggestions give
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
		if missing == 0 && k.Intermediate {
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
