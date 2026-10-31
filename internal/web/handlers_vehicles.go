package web

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// palette are the suggested chip colours for new vehicles.
var palette = []string{"#f26b0f", "#2563eb", "#16a34a", "#84cc16", "#dc2626", "#0891b2", "#ca8a04", "#db2777"}

type vehicleForm struct {
	ID                                   int64
	Name, Kind, Make, Model, Year        string
	Plate, VIN, Color, OdometerKm, Notes string
	OdometerWas                          string // as the edit form was opened
	Stored                               string // the saved name while editing, for the heading
	Version                              int    // as the edit form was opened (see itemForm)
	Stale                                *staleNote
	FormKey                              string // see itemForm
	Errors                               stock.Errors
}

type vehicleFormPage struct {
	Form    vehicleForm
	IsNew   bool
	Kinds   []stock.VehicleKind
	Palette []string
}

func vehicleFormFrom(v stock.Vehicle) vehicleForm {
	return vehicleForm{
		ID: v.ID, Name: v.Name, Kind: string(v.Kind), Make: v.Make, Model: v.Model, Year: optIntString(v.Year),
		Plate: v.Plate, VIN: v.VIN, Color: v.Color, OdometerKm: optIntString(v.OdometerKm), Notes: v.Notes,
		OdometerWas: optIntString(v.OdometerKm), Stored: v.Name, Version: v.Version,
	}
}

func parseVehicleForm(r *http.Request) (vehicleForm, stock.VehicleInput) {
	f := vehicleForm{
		Name: formText(r, "name"), Kind: formText(r, "kind"), Make: formText(r, "make"), Model: formText(r, "model"),
		Year: formText(r, "year"), Plate: formText(r, "plate"), VIN: formText(r, "vin"), Color: formText(r, "color"),
		OdometerKm: formText(r, "odometer_km"), Notes: formText(r, "notes"), OdometerWas: formText(r, "odometer_was"),
		Version: parseVersion(r), FormKey: parseFormKey(r), Errors: stock.Errors{},
	}
	checkFormKey(f.Errors, f.FormKey)
	in := stock.VehicleInput{
		Name: f.Name, Kind: stock.VehicleKind(f.Kind), Make: f.Make, Model: f.Model, Plate: f.Plate, VIN: f.VIN,
		Color: f.Color, Notes: f.Notes, Version: f.Version, FormKey: f.FormKey,
	}
	var err error
	if in.Year, err = parseOptInt(f.Year); err != nil {
		f.Errors.Add("year", err.Error())
	}
	if in.OdometerKm, err = parseOptInt(f.OdometerKm); err != nil {
		f.Errors.Add("odometer_km", err.Error())
	}
	if was, err := parseOptInt(f.OdometerWas); err == nil && r.PostForm.Has("odometer_was") {
		in.KeepOdometer = optIntString(was) == optIntString(in.OdometerKm)
	}
	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	return f, in
}

func vehiclePath(id int64) string { return fmt.Sprintf("/vehicles/%d", id) }

func (s *Server) renderVehicleForm(w http.ResponseWriter, r *http.Request, status int, f vehicleForm, isNew bool) {
	title := "New vehicle"
	if !isNew {
		title = "Edit " + f.Stored
	}
	s.render(w, r, status, "vehicle_form", View{Title: title, Nav: "vehicles", Data: vehicleFormPage{
		Form: f, IsNew: isNew, Kinds: stock.VehicleKinds, Palette: palette,
	}})
}

// vehicleCard is one vehicle on the vehicles page.
type vehicleCard struct {
	Vehicle stock.Vehicle
	Kits    int
	Next    *kitView
}

type vehiclesPage struct {
	Cards        []vehicleCard
	Archived     int  // how many vehicles are archived
	ShowArchived bool // listing the archived ones
}

func (s *Server) vehicleList(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	all, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	// The archived ones are listed on their own, from a link under the list.
	showArchived := r.URL.Query().Get("archived") == "1"
	var vehicles []stock.Vehicle
	archived := 0
	for _, v := range all {
		if v.Archived() {
			archived++
		}
		if v.Archived() == showArchived {
			vehicles = append(vehicles, v)
		}
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{IncludeArchived: showArchived})
	if err != nil {
		return err
	}

	views := s.kitViews(kits, vehicles)
	cards := make([]vehicleCard, len(vehicles))
	for i, v := range vehicles {
		cards[i].Vehicle = v
		for j := range views {
			kv := &views[j]
			if kv.Kit.VehicleID != v.ID {
				continue
			}
			cards[i].Kits++
			if kv.Due.State != stock.DueNone && (cards[i].Next == nil || kv.Due.State.Rank() < cards[i].Next.Due.State.Rank()) {
				cards[i].Next = kv
			}
		}
	}
	s.render(w, r, http.StatusOK, "vehicles", View{Title: "Vehicles", Nav: "vehicles",
		Data: vehiclesPage{Cards: cards, Archived: archived, ShowArchived: showArchived}})
	return nil
}

func (s *Server) vehicleNew(w http.ResponseWriter, r *http.Request) error {
	vehicles, err := s.store.ListVehicles(r.Context())
	if err != nil {
		return err
	}
	f := vehicleForm{Kind: string(stock.KindMotorcycle), Color: palette[len(vehicles)%len(palette)]}
	s.renderVehicleForm(w, r, http.StatusOK, f, true)
	return nil
}

func (s *Server) vehicleCreate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatVehicle, vehiclePath, "/vehicles"); done || err != nil {
		return err
	}
	f, in := parseVehicleForm(r)
	if !f.Errors.Any() {
		id, err := s.store.CreateVehicle(r.Context(), in)
		if err == nil {
			s.redirect(w, r, vehiclePath(id), "Added "+in.Name)
			return nil
		}
		if s.savedAlready(w, r, err, vehiclePath, "/vehicles") {
			return nil
		}
		if !formConflict(f.Errors, err) {
			return err
		}
	}
	s.renderVehicleForm(w, r, http.StatusUnprocessableEntity, f, true)
	return nil
}

func (s *Server) vehicleEdit(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	v, err := s.store.GetVehicle(r.Context(), id)
	if err != nil {
		return err
	}
	s.renderVehicleForm(w, r, http.StatusOK, vehicleFormFrom(v), false)
	return nil
}

func (s *Server) vehicleUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	// Saved already comes first: the first save moved the version on.
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatVehicle, vehiclePath, "/vehicles"); done || err != nil {
		return err
	}
	f, in := parseVehicleForm(r)
	f.ID = id
	if !f.Errors.Any() {
		err := s.store.UpdateVehicle(r.Context(), id, in)
		if err == nil {
			s.redirect(w, r, vehiclePath(id), "Saved "+in.Name)
			return nil
		}
		if s.savedAlready(w, r, err, vehiclePath, "/vehicles") {
			return nil
		}
		if f.Stale = staleForm(err, fmt.Sprintf("/vehicles/%d/edit", id)); f.Stale == nil && !formConflict(f.Errors, err) {
			return err
		}
	}
	// Deleted meanwhile, the form still shows what was typed.
	f.Stored = cmp.Or(f.Name, "vehicle")
	if v, err := s.store.GetVehicle(r.Context(), id); err == nil {
		f.Stored = v.Name
	}
	s.renderVehicleForm(w, r, http.StatusUnprocessableEntity, f, false)
	return nil
}

func (s *Server) vehicleDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	page := fmt.Sprintf("/vehicles/%d", id)
	v, err := s.store.GetVehicle(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatVehicle, id), page, "/vehicles") {
		return nil
	} else if err != nil {
		return err
	}
	var conflict *store.ConflictError
	trashID, err := s.store.DeleteVehicle(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, page, "/vehicles") {
		return nil
	} else if errors.As(err, &conflict) {
		s.setFlash(w, "error", conflict.Message)
		s.redirect(w, r, fmt.Sprintf("/vehicles/%d", id), "")
		return nil
	} else if err != nil {
		return err
	}
	s.redirectUndo(w, r, "/vehicles", "Deleted "+v.Name+". Its parts are still on stock.", trashID)
	return nil
}

// vehicleArchive hides a vehicle that has services or kits.
func (s *Server) vehicleArchive(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	page := fmt.Sprintf("/vehicles/%d", id)
	v, err := s.store.GetVehicle(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatVehicle, id), page, "/vehicles") {
		return nil
	} else if err != nil {
		return err
	}
	err = s.store.ArchiveVehicle(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, page, "/vehicles") {
		return nil
	} else if err != nil {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/vehicles/%d", id), "Archived "+v.Name+". Its services and kits stay; restore it any time.")
	return nil
}

func (s *Server) vehicleRestore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	page := fmt.Sprintf("/vehicles/%d", id)
	v, err := s.store.GetVehicle(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatVehicle, id), page, "/vehicles") {
		return nil
	} else if err != nil {
		return err
	}
	err = s.store.RestoreVehicle(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, page, "/vehicles") {
		return nil
	} else if err != nil {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/vehicles/%d", id), "Restored "+v.Name)
	return nil
}

func (s *Server) vehicleOdometer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	back := s.back(r, fmt.Sprintf("/vehicles/%d", id))
	km, err := parseOptInt(formText(r, "odometer_km"))
	if err != nil || km == nil || *km < 0 || *km > stock.MaxOdometerKm {
		s.setFlash(w, "error", "Enter the odometer reading in km")
		s.redirect(w, r, back, "")
		return nil
	}
	var conflict *store.ConflictError
	if err := s.store.SetOdometer(r.Context(), id, *km); errors.As(err, &conflict) {
		s.setFlash(w, "error", conflict.Message)
		s.redirect(w, r, back, "")
		return nil
	} else if err != nil {
		return err
	}
	s.redirect(w, r, back, "Odometer set to "+stock.FormatKm(*km))
	return nil
}

type vehiclePage struct {
	Vehicle    stock.Vehicle
	Kits       []kitView
	Sections   []stock.Section
	VehicleMap map[int64]stock.Vehicle
	Services   []stock.ServiceRecord
	ItemCount  int
	StockQuery string
	// What refers to the vehicle: with any, it is archived, not deleted.
	ServiceCount, KitCount int
	Labor                  float64 // labour cost of all its services
}

func (s *Server) vehicleShow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	v, err := s.store.GetVehicle(ctx, id)
	if err != nil {
		return err
	}
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{VehicleID: id, IncludeArchived: true})
	if err != nil {
		return err
	}
	items, err := s.store.ListItems(ctx, store.ItemFilter{VehicleIDs: []int64{id}})
	if err != nil {
		return err
	}
	services, err := s.store.ListServices(ctx, store.ServiceFilter{VehicleID: id, Limit: 10})
	if err != nil {
		return err
	}
	serviceCount, kitCount, err := s.store.VehicleUsage(ctx, id)
	if err != nil {
		return err
	}
	labor, err := s.store.LaborTotal(ctx, id)
	if err != nil {
		return err
	}
	p := vehiclePage{
		ServiceCount: serviceCount, KitCount: kitCount, Labor: labor,
		Vehicle: v, Kits: s.kitViews(kits, vehicles), Sections: stock.GroupByVehicles(items, vehicles),
		VehicleMap: vehicleMap(vehicles), Services: services, ItemCount: len(items),
		StockQuery: "v=" + strconv.FormatInt(id, 10),
	}
	s.render(w, r, http.StatusOK, "vehicle_show", View{Title: v.Name, Nav: "vehicles", Data: p})
	return nil
}

// activeVehicles leaves archived vehicles out of a list or picker, except
// the ones in keep (e.g. already chosen), so saving a form never drops them.
func activeVehicles(vehicles []stock.Vehicle, keep ...int64) []stock.Vehicle {
	var out []stock.Vehicle
	for _, v := range vehicles {
		if !v.Archived() || slices.Contains(keep, v.ID) {
			out = append(out, v)
		}
	}
	return out
}

// kitView is a kit with its computed readiness and due state.
// VehicleArchived hides the reminder of a kit whose vehicle is archived.
type kitView struct {
	Kit             stock.Kit
	Readiness       stock.Readiness
	Due             stock.Due
	VehicleArchived bool
}

// kitViews computes readiness and due state, most urgent first.
func (s *Server) kitViews(kits []stock.Kit, vehicles []stock.Vehicle) []kitView {
	odo := map[int64]*int{}
	archived := map[int64]bool{}
	for _, v := range vehicles {
		odo[v.ID] = v.OdometerKm
		archived[v.ID] = v.Archived()
	}
	out := make([]kitView, len(kits))
	for i, k := range kits {
		out[i] = kitView{Kit: k, Readiness: k.Readiness(), Due: k.Due(odo[k.VehicleID], s.now()), VehicleArchived: archived[k.VehicleID]}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Due.State.Rank() < out[b].Due.State.Rank() })
	return out
}
