package web

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

type serviceForm struct {
	ID          int64 // set when editing a logged service
	VehicleID   int64
	KitID       int64
	KitName     string
	PerformedOn string
	OdometerKm  string
	Title       string
	Notes       string
	LaborCost   string
	Parts       []lineRow
	Errors      stock.Errors
}

type serviceFormPage struct {
	Form       serviceForm
	Vehicles   []stock.Vehicle
	Vehicle    *stock.Vehicle
	ItemGroups []itemGroup
}

func (s *Server) renderServiceForm(w http.ResponseWriter, r *http.Request, status int, f serviceForm) error {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	items, err := s.pickerItems(ctx, f.Parts)
	if err != nil {
		return err
	}
	p := serviceFormPage{Vehicles: vehicles}
	for i := range vehicles {
		if vehicles[i].ID == f.VehicleID {
			p.Vehicle = &vehicles[i]
		}
	}
	for n := 0; n < 2 || len(f.Parts) == 0; n++ {
		f.Parts = append(f.Parts, lineRow{})
	}
	p.Form = f
	p.ItemGroups = itemOptions(items, p.Vehicle)
	title := "Log service"
	if f.ID != 0 {
		title = "Edit " + f.Title
	}
	s.render(w, r, status, "service_form", View{Title: title, Nav: "services", Data: p})
	return nil
}

func (s *Server) serviceNew(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	f := serviceForm{PerformedOn: s.now().Format("2006-01-02")}
	if id := parseOptID(q.Get("vehicle")); id != nil {
		f.VehicleID = *id
	}
	if id := parseOptID(q.Get("kit")); id != nil {
		k, err := s.store.GetKit(ctx, *id)
		if err != nil {
			return err
		}
		f.KitID, f.KitName, f.VehicleID, f.Title = k.ID, k.Name, k.VehicleID, k.Name
		for _, l := range k.Lines {
			if l.Archived {
				continue // no longer kept; the kit should be edited
			}
			f.Parts = append(f.Parts, lineRow{ItemID: l.ItemID, Qty: stock.FormatQuantity(l.Need, l.Unit)})
		}
	}
	if f.VehicleID != 0 {
		v, err := s.store.GetVehicle(ctx, f.VehicleID)
		if err != nil {
			return err
		}
		f.OdometerKm = optIntString(v.OdometerKm)
	}
	return s.renderServiceForm(w, r, http.StatusOK, f)
}

// parseServiceForm reads the posted service form. Problems are recorded in
// form.Errors; the returned input is only meaningful without errors.
func (s *Server) parseServiceForm(r *http.Request) (serviceForm, stock.ServiceInput, error) {
	items, err := s.store.ListItems(r.Context(), store.ItemFilter{Archived: store.ArchivedAll})
	if err != nil {
		return serviceForm{}, stock.ServiceInput{}, err
	}
	f := serviceForm{
		PerformedOn: formText(r, "performed_on"), OdometerKm: formText(r, "odometer_km"), Title: formText(r, "title"),
		Notes: formText(r, "notes"), LaborCost: formText(r, "labor_cost"), KitName: formText(r, "kit_name"),
		Errors: stock.Errors{},
	}
	in := stock.ServiceInput{Title: f.Title, Notes: f.Notes}
	if id := parseOptID(formText(r, "vehicle_id")); id != nil {
		f.VehicleID, in.VehicleID = *id, *id
	}
	if id := parseOptID(formText(r, "kit_id")); id != nil {
		f.KitID, in.KitID = *id, id
	}
	if d, err := parseEventDate(f.PerformedOn, s.now()); err != nil {
		f.Errors.Add("performed_on", err.Error())
	} else if d != nil {
		in.PerformedOn = *d
	}
	if in.OdometerKm, err = parseOptInt(f.OdometerKm); err != nil {
		f.Errors.Add("odometer_km", err.Error())
	}
	if in.LaborCost, err = parseOptMoney(f.LaborCost); err != nil {
		f.Errors.Add("labor_cost", err.Error())
	}
	f.Parts, in.Parts = parseLines(r, "part", itemsByID(items), f.Errors)
	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	return f, in, nil
}

// serviceConflict maps a store error onto the service form.
func serviceConflict(f serviceForm, err error) bool {
	var short *store.InsufficientStockError
	if errors.As(err, &short) {
		for i := range f.Parts {
			if f.Parts[i].ItemID == short.ItemID {
				f.Parts[i].Error = "Only " + stock.FormatQuantity(short.Have, short.Unit) + " on stock"
			}
		}
	}
	return formConflict(f.Errors, err)
}

func (s *Server) serviceCreate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	f, in, err := s.parseServiceForm(r)
	if err != nil {
		return err
	}
	if !f.Errors.Any() {
		id, err := s.store.CreateService(r.Context(), in)
		if err == nil {
			s.redirect(w, r, fmt.Sprintf("/services/%d", id), "Logged "+in.Title)
			return nil
		}
		if !serviceConflict(f, err) {
			return err
		}
	}
	return s.renderServiceForm(w, r, http.StatusUnprocessableEntity, f)
}

func (s *Server) serviceEdit(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rec, err := s.store.GetService(r.Context(), id)
	if err != nil {
		return err
	}
	f := serviceForm{
		ID: rec.ID, VehicleID: rec.VehicleID, KitName: rec.KitName, PerformedOn: rec.PerformedOn.Format("2006-01-02"),
		OdometerKm: optIntString(rec.OdometerKm), Title: rec.Title, Notes: rec.Notes,
	}
	if rec.KitID != nil {
		f.KitID = *rec.KitID
	}
	if rec.LaborCost != nil {
		f.LaborCost = stock.FormatMoney(*rec.LaborCost)
	}
	for _, p := range rec.Parts {
		f.Parts = append(f.Parts, lineRow{ItemID: p.ItemID, Qty: stock.FormatQuantity(p.Quantity, p.Unit)})
	}
	return s.renderServiceForm(w, r, http.StatusOK, f)
}

func (s *Server) serviceUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	rec, err := s.store.GetService(r.Context(), id)
	if err != nil {
		return err
	}
	f, in, err := s.parseServiceForm(r)
	if err != nil {
		return err
	}
	f.ID, f.VehicleID = id, rec.VehicleID // the vehicle can't change
	if !f.Errors.Any() {
		err := s.store.UpdateService(r.Context(), id, in)
		if err == nil {
			s.redirect(w, r, fmt.Sprintf("/services/%d", id), "Saved "+in.Title)
			return nil
		}
		if !serviceConflict(f, err) {
			return err
		}
	}
	return s.renderServiceForm(w, r, http.StatusUnprocessableEntity, f)
}

func (s *Server) serviceShow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rec, err := s.store.GetService(r.Context(), id)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "service_show", View{Title: rec.Title, Nav: "services", Data: rec})
	return nil
}

func (s *Server) serviceDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rec, err := s.store.GetService(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteService(r.Context(), id); err != nil {
		return err
	}
	msg := "Deleted " + rec.Title
	if len(rec.Parts) > 0 {
		msg += " and put its parts back on stock"
	}
	s.redirect(w, r, fmt.Sprintf("/vehicles/%d", rec.VehicleID), msg)
	return nil
}

type servicesPage struct {
	Kits      []kitView
	Services  []stock.ServiceRecord
	Vehicles  []stock.Vehicle
	VehicleID int64
}

func (s *Server) serviceList(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := servicesPage{}
	if id := parseOptID(r.URL.Query().Get("v")); id != nil {
		p.VehicleID = *id
	}
	var err error
	if p.Vehicles, err = s.store.ListVehicles(ctx); err != nil {
		return err
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{VehicleID: p.VehicleID})
	if err != nil {
		return err
	}
	p.Kits = s.kitViews(kits, p.Vehicles)
	if p.Services, err = s.store.ListServices(ctx, store.ServiceFilter{VehicleID: p.VehicleID, Limit: 200}); err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "services", View{Title: "Services", Nav: "services", Data: p})
	return nil
}
