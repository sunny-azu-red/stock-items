package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// lineRow is one item + quantity row of a kit or service form.
type lineRow struct {
	ItemID int64
	Qty    string
	Unit   string // sub-unit chosen next to Qty; "" means the item's base unit
	Have   string // what is on stock of the chosen item, shown next to the row
	Error  string
}

// partRow is a prefilled row for qty (base units) of an item.
func partRow(itemID int64, qty float64, u stock.Unit) lineRow {
	row := lineRow{ItemID: itemID}
	row.Qty, row.Unit = stock.SplitForInput(qty, u)
	return row
}

// describeRows fills in what is on stock for each row's item.
func describeRows(rows []lineRow, items []stock.Item) {
	byID := itemsByID(items)
	for i := range rows {
		if it, ok := byID[rows[i].ItemID]; ok {
			rows[i].Have = stock.FormatQuantity(it.Quantity, it.Unit)
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
				row.Error = err.Error()
			case q <= 0:
				row.Error = "Quantity must be greater than zero"
			default:
				lines = append(lines, stock.KitLineInput{ItemID: id, Quantity: q})
			}
		}
		if row.Error != "" {
			errs.Add("lines", "Check the highlighted rows")
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
	ID                               int64
	VehicleID                        int64
	Name, IntervalKm, IntervalMonths string
	Notes                            string
	Lines                            []lineRow
	Stored                           string // the saved name while editing, for the heading
	Errors                           stock.Errors
}

type kitFormPage struct {
	Form       kitForm
	IsNew      bool
	Vehicles   []stock.Vehicle
	ItemGroups []itemGroup
}

func (s *Server) renderKitForm(w http.ResponseWriter, r *http.Request, status int, f kitForm, isNew bool) error {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	items, err := s.pickerItems(ctx, f.Lines)
	if err != nil {
		return err
	}
	describeRows(f.Lines, items)
	var vehicle *stock.Vehicle
	for i := range vehicles {
		if vehicles[i].ID == f.VehicleID {
			vehicle = &vehicles[i]
		}
	}
	// Offer a few empty rows to fill in (more can be added with JS).
	for n := 0; n < 3 || len(f.Lines) == 0; n++ {
		f.Lines = append(f.Lines, lineRow{})
	}
	title := "New service kit"
	if !isNew {
		title = "Edit " + f.Stored
	}
	s.render(w, r, status, "kit_form", View{Title: title, Nav: "services", Data: kitFormPage{
		Form: f, IsNew: isNew, Vehicles: activeVehicles(vehicles, f.VehicleID), ItemGroups: itemOptions(items, vehicle),
	}})
	return nil
}

func (s *Server) parseKitForm(r *http.Request) (kitForm, stock.KitInput, error) {
	items, err := s.store.ListItems(r.Context(), store.ItemFilter{Archived: store.ArchivedAll})
	if err != nil {
		return kitForm{}, stock.KitInput{}, err
	}
	f := kitForm{
		Name: formText(r, "name"), IntervalKm: formText(r, "interval_km"), IntervalMonths: formText(r, "interval_months"),
		Notes: formText(r, "notes"), Errors: stock.Errors{},
	}
	if id := parseOptID(formText(r, "vehicle_id")); id != nil {
		f.VehicleID = *id
	}
	in := stock.KitInput{VehicleID: f.VehicleID, Name: f.Name, Notes: f.Notes}
	if in.IntervalKm, err = parseOptInt(f.IntervalKm); err != nil {
		f.Errors.Add("interval_km", err.Error())
	}
	if in.IntervalMonths, err = parseOptInt(f.IntervalMonths); err != nil {
		f.Errors.Add("interval_months", err.Error())
	}
	f.Lines, in.Lines = parseLines(r, "line", itemsByID(items), f.Errors)
	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	return f, in, nil
}

func kitFormFrom(k stock.Kit) kitForm {
	f := kitForm{
		ID: k.ID, VehicleID: k.VehicleID, Name: k.Name, Stored: k.Name, IntervalKm: optIntString(k.IntervalKm),
		IntervalMonths: optIntString(k.IntervalMonths), Notes: k.Notes,
	}
	for _, l := range k.Lines {
		f.Lines = append(f.Lines, partRow(l.ItemID, l.Need, l.Unit))
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
	f, in, err := s.parseKitForm(r)
	if err != nil {
		return err
	}
	if !f.Errors.Any() {
		id, err := s.store.CreateKit(r.Context(), in)
		if err == nil {
			s.redirect(w, r, fmt.Sprintf("/kits/%d", id), "Created kit "+in.Name)
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
	f, in, err := s.parseKitForm(r)
	if err != nil {
		return err
	}
	f.ID = id
	if !f.Errors.Any() {
		err := s.store.UpdateKit(r.Context(), id, in)
		if err == nil {
			s.redirect(w, r, fmt.Sprintf("/kits/%d", id), "Saved kit "+in.Name)
			return nil
		}
		if !formConflict(f.Errors, err) {
			return err
		}
	}
	k, err := s.store.GetKit(r.Context(), id)
	if err != nil {
		return err
	}
	f.Stored = k.Name
	return s.renderKitForm(w, r, http.StatusUnprocessableEntity, f, false)
}

func (s *Server) kitDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	k, err := s.store.GetKit(r.Context(), id)
	if err != nil {
		return err
	}
	var conflict *store.ConflictError
	trashID, err := s.store.DeleteKit(r.Context(), id)
	if errors.As(err, &conflict) {
		s.setFlash(w, "error", conflict.Message)
		s.redirect(w, r, fmt.Sprintf("/kits/%d", id), "")
		return nil
	} else if err != nil {
		return err
	}
	s.redirectUndo(w, r, fmt.Sprintf("/vehicles/%d", k.VehicleID), "Deleted kit "+k.Name, trashID)
	return nil
}

// kitArchive hides a kit that has logged services.
func (s *Server) kitArchive(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	k, err := s.store.GetKit(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.store.ArchiveKit(r.Context(), id); err != nil {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/kits/%d", id), "Archived "+k.Name+". Its services keep it; restore it any time.")
	return nil
}

func (s *Server) kitRestore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	k, err := s.store.GetKit(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.store.RestoreKit(r.Context(), id); err != nil {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/kits/%d", id), "Restored "+k.Name)
	return nil
}

type kitPage struct {
	View         kitView
	Vehicle      stock.Vehicle
	Services     []stock.ServiceRecord
	ServiceCount int // all services logged with the kit, on any vehicle
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
	p := kitPage{View: s.kitViews([]stock.Kit{k}, []stock.Vehicle{v})[0], Vehicle: v, Services: done, ServiceCount: count}
	s.render(w, r, http.StatusOK, "kit_show", View{Title: k.Name + " · " + v.Name, Nav: "services", Data: p})
	return nil
}

// kitShortfall puts every missing part of a kit on the shopping list.
func (s *Server) kitShortfall(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	k, err := s.store.GetKit(ctx, id)
	if err != nil {
		return err
	}
	n, missing := 0, 0
	for _, l := range k.Lines {
		if short := l.Short(); short > 0 && !l.Archived {
			changed, err := s.store.EnsureShopping(ctx, l.ItemID, short, "For "+k.VehicleName+": "+k.Name)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if changed {
				n++
			}
			missing++
		}
	}
	msg := "Nothing is missing for this kit"
	if missing > 0 && n == 0 {
		msg = "Everything missing is already on the shopping list"
	}
	if n > 0 {
		msg = fmt.Sprintf("Added %d missing %s to the shopping list", n, pluralWord(n, "part", "parts"))
	}
	s.redirect(w, r, s.back(r, fmt.Sprintf("/kits/%d", id)), msg)
	return nil
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
