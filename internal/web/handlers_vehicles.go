package web

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// palette are the suggested chip colours for new vehicles.
var palette = []string{"#f26b0f", "#2563eb", "#16a34a", "#84cc16", "#dc2626", "#0891b2", "#ca8a04", "#db2777"}

type vehicleForm struct {
	ID                                 int64
	Name, Kind, Make, Model, Year      string
	Plate, VIN, Color, Odometer, Notes string
	PurchasedOn, OdometerAtPurchase    string
	OdometerWas                        string // as the edit form was opened
	Stored                             string // the saved name while editing, for the heading
	Version                            int    // as the edit form was opened (see itemForm)
	Stale                              *staleNote
	FormKey                            string // see itemForm
	Errors                             stock.Errors
}

type vehicleFormPage struct {
	Form    vehicleForm
	IsNew   bool
	Kinds   []stock.VehicleKind
	Palette []string
	Makes   []string
	Models  []string
}

func vehicleFormFrom(v stock.Vehicle) vehicleForm {
	var pOn string
	if v.PurchasedOn != nil {
		pOn = v.PurchasedOn.Format("2006-01-02")
	}
	return vehicleForm{
		ID: v.ID, Name: v.Name, Kind: string(v.Kind), Make: v.Make, Model: v.Model, Year: optIntString(v.Year),
		Plate: v.Plate, VIN: v.VIN, Color: v.Color, Odometer: optIntString(v.Odometer),
		PurchasedOn: pOn, OdometerAtPurchase: optIntString(v.OdometerAtPurchase), Notes: v.Notes,
		OdometerWas: optIntString(v.Odometer), Stored: v.Name, Version: v.Version,
	}
}

func parseVehicleForm(r *http.Request, now time.Time) (vehicleForm, stock.VehicleInput) {
	f := vehicleForm{
		Name: formText(r, "name"), Kind: formText(r, "kind"), Make: formText(r, "make"), Model: formText(r, "model"),
		Year: formText(r, "year"), Plate: formText(r, "plate"), VIN: formText(r, "vin"), Color: formText(r, "color"),
		Odometer: formText(r, "odometer"), PurchasedOn: formText(r, "purchased_on"), OdometerAtPurchase: formText(r, "odometer_at_purchase"),
		Notes: formText(r, "notes"), OdometerWas: formText(r, "odometer_was"),
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
	if in.Odometer, err = parseOptInt(f.Odometer); err != nil {
		f.Errors.Add("odometer", err.Error())
	}
	if in.OdometerAtPurchase, err = parseOptInt(f.OdometerAtPurchase); err != nil {
		f.Errors.Add("odometer_at_purchase", err.Error())
	}
	// Not parseEventDate: a vehicle may have been bought before 1990 (its
	// Validate allows 1900 on), but not in the future.
	if in.PurchasedOn, err = parseOptDate(f.PurchasedOn); err != nil {
		f.Errors.Add("purchased_on", err.Error())
	} else if in.PurchasedOn != nil && in.PurchasedOn.After(stock.DateOnly(now).AddDate(0, 0, 1)) {
		f.Errors.Add("purchased_on", "Date cannot be in the future")
	}
	if was, err := parseOptInt(f.OdometerWas); err == nil && r.PostForm.Has("odometer_was") {
		in.KeepOdometer = optIntString(was) == optIntString(in.Odometer)
	}
	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	return f, in
}

func vehiclePath(id int64) string { return fmt.Sprintf("/vehicles/%d", id) }

func (s *Server) renderVehicleForm(w http.ResponseWriter, r *http.Request, status int, f vehicleForm, isNew bool) error {
	ctx := r.Context()
	makes, err := s.store.Makes(ctx)
	if err != nil {
		return err
	}
	models, err := s.store.Models(ctx)
	if err != nil {
		return err
	}
	title := "Add vehicle"
	if !isNew {
		title = "Edit " + f.Stored
	}
	s.render(w, r, status, "vehicle_form", View{Title: title, Nav: "vehicles", Data: vehicleFormPage{
		Form: f, IsNew: isNew, Kinds: stock.VehicleKinds, Palette: palette,
		Makes: makes, Models: models,
	}})
	return nil
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
	kits, err := s.store.ListKits(ctx, store.KitFilter{IncludeArchived: true})
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
			if !kv.Kit.Archived() && kv.Due.State != stock.DueNone && (cards[i].Next == nil || kv.Due.State.Rank() < cards[i].Next.Due.State.Rank()) {
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
	return s.renderVehicleForm(w, r, http.StatusOK, f, true)
}

func (s *Server) vehicleCreate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatVehicle, vehiclePath, "/vehicles"); done || err != nil {
		return err
	}
	f, in := parseVehicleForm(r, s.now())
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
	return s.renderVehicleForm(w, r, http.StatusUnprocessableEntity, f, true)
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
	return s.renderVehicleForm(w, r, http.StatusOK, vehicleFormFrom(v), false)
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
	f, in := parseVehicleForm(r, s.now())
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
		if f.Stale = staleForm(err, vehiclePath(id)+"/edit"); f.Stale == nil && !formConflict(f.Errors, err) {
			return err
		}
	}
	// Deleted meanwhile, the form still shows what was typed.
	f.Stored = cmp.Or(f.Name, "vehicle")
	if v, err := s.store.GetVehicle(r.Context(), id); err == nil {
		f.Stored = v.Name
	}
	return s.renderVehicleForm(w, r, http.StatusUnprocessableEntity, f, false)
}

func (s *Server) vehicleDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	page := vehiclePath(id)
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
		s.redirect(w, r, page, "")
		return nil
	} else if err != nil {
		return err
	}
	s.redirectUndo(w, r, "/vehicles", "Deleted "+v.Name+". Its items stay in stock.", trashID)
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
	page := vehiclePath(id)
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
	s.redirect(w, r, page, "Archived "+v.Name+". Its services and kits stay; restore it any time.")
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
	page := vehiclePath(id)
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
	s.redirect(w, r, page, "Restored "+v.Name)
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
	back := s.back(r, vehiclePath(id))
	odo, err := parseOptInt(formText(r, "odometer"))
	if err != nil || odo == nil || *odo < 0 || *odo > stock.MaxOdometer {
		s.setFlash(w, "error", "Enter the odometer reading in "+stock.DistanceUnit)
		s.redirect(w, r, back, "")
		return nil
	}
	var conflict *store.ConflictError
	if err := s.store.SetOdometer(r.Context(), id, *odo); errors.As(err, &conflict) {
		s.setFlash(w, "error", conflict.Message)
		s.redirect(w, r, back, "")
		return nil
	} else if err != nil {
		return err
	}
	msg := "Set the odometer to " + stock.FormatDistance(*odo)
	if v, err := s.store.GetVehicle(r.Context(), id); err == nil {
		msg = "Set " + v.Name + "'s odometer to " + stock.FormatDistance(*odo)
	}
	s.redirect(w, r, back, msg)
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
	// MonthsUncounted names the active kits whose month deadline has no
	// day to count from until the vehicle has a purchase date.
	MonthsUncounted []string
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
		Vehicle: v, Kits: scopeKits(s.kitViews(kits, vehicles), id), Sections: stock.GroupByVehicles(items, vehicles),
		VehicleMap: vehicleMap(vehicles), Services: services, ItemCount: len(items),
		StockQuery: "v=" + strconv.FormatInt(id, 10),
	}
	for _, kv := range p.Kits {
		if kv.MonthsUncounted && !v.Archived() {
			p.MonthsUncounted = append(p.MonthsUncounted, kv.Kit.Name)
		}
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
	FullReadiness   stock.Readiness
	InterReadiness  stock.Readiness
	Due             stock.Due
	FullDue         stock.Due
	InterDue        stock.Due
	NextInter       bool
	VehicleArchived bool
	// MonthsUncounted: the full schedule repeats by months with no day to
	// count from (stock.Kit.MonthsUncounted); the page asks for the
	// vehicle's purchase date.
	MonthsUncounted bool
	// HideVehicle leaves out the kit's vehicle chip on a page that already
	// names that vehicle once at the top (see scopeKits).
	HideVehicle bool
}

// soleVehicle is the id of the only active vehicle, or 0 when there are
// none or several. With just one, every row would show its chip: it is the
// scope of every page, and rows leave it out (a row of an archived vehicle
// keeps its chip).
func soleVehicle(vehicles []stock.Vehicle) int64 {
	var id int64
	for _, v := range vehicles {
		if v.Archived() {
			continue
		}
		if id != 0 {
			return 0
		}
		id = v.ID
	}
	return id
}

// scopeKits hides the vehicle chip of the kits of vehicle here: the page
// says the vehicle once, its rows show only a different one. 0 is no scope.
func scopeKits(kvs []kitView, here int64) []kitView {
	for i := range kvs {
		kvs[i].HideVehicle = here != 0 && kvs[i].Kit.VehicleID == here
	}
	return kvs
}

// kitViews computes readiness and due state, most urgent first.
func (s *Server) kitViews(kits []stock.Kit, vehicles []stock.Vehicle) []kitView {
	byVehicle := vehicleMap(vehicles)
	today := s.now()
	out := make([]kitView, len(kits))
	for i, k := range kits {
		odo := k.VehicleOdometer
		v, ok := byVehicle[k.VehicleID]
		if ok {
			odo = v.Odometer
			if k.VehiclePurchasedOn == nil {
				k.VehiclePurchasedOn = v.PurchasedOn
			}
			if k.VehicleOdometerAtPurchase == nil {
				k.VehicleOdometerAtPurchase = v.OdometerAtPurchase
			}
		}
		out[i] = kitView{
			Kit:             k,
			Readiness:       k.ReadinessAt(odo, today),
			FullReadiness:   stock.ReadinessOf(k.Lines),
			InterReadiness:  stock.ReadinessOf(k.InterLines),
			Due:             k.Due(odo, today),
			FullDue:         k.FullDue(odo, today),
			InterDue:        k.InterDue(odo, today),
			NextInter:       k.NextIsIntermediate(odo, today),
			VehicleArchived: v.Archived(),
			MonthsUncounted: k.MonthsUncounted(odo, today),
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Due.State.Rank() < out[b].Due.State.Rank() })
	return out
}
