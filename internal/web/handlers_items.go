package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// ---- Item form ----

type skuRow struct {
	Code, Kind, Label, Pack, PackUnit string
	Error                             string
}

type itemForm struct {
	ID                          int64
	Name, Brand, Category, Unit string
	Quantity, QuantityUnit      string
	MinQuantity, MinUnit        string
	ExpiresOn, Location, Notes  string
	VehicleIDs                  []int64
	SKUs                        []skuRow
	Errors                      stock.Errors
}

type itemFormPage struct {
	Form       itemForm
	IsNew      bool
	Vehicles   []stock.Vehicle
	Categories []string
	Locations  []string
	Units      []stock.Unit
	SubUnits   []stock.SubUnit
	SKUKinds   []stock.SKUKind
}

func itemFormFromItem(it stock.Item) itemForm {
	f := itemForm{
		ID: it.ID, Name: it.Name, Brand: it.Brand, Category: it.Category, Unit: string(it.Unit),
		Location: it.Location, Notes: it.Notes, VehicleIDs: it.VehicleIDs,
	}
	f.Quantity, f.QuantityUnit = stock.SplitForInput(it.Quantity, it.Unit)
	if it.MinQuantity != nil {
		f.MinQuantity, f.MinUnit = stock.SplitForInput(*it.MinQuantity, it.Unit)
	} else {
		_, f.MinUnit = stock.SplitForInput(0, it.Unit)
	}
	if it.ExpiresOn != nil {
		f.ExpiresOn = it.ExpiresOn.Format("2006-01-02")
	}
	for _, s := range it.SKUs {
		row := skuRow{Code: s.Code, Kind: string(s.Kind), Label: s.Label}
		if s.PackSize != nil {
			row.Pack, row.PackUnit = stock.SplitForInput(*s.PackSize, it.Unit)
		}
		f.SKUs = append(f.SKUs, row)
	}
	return f
}

// parseItemForm reads the posted item form. Problems are recorded in
// form.Errors; the returned input is only meaningful without errors.
func parseItemForm(r *http.Request) (itemForm, stock.ItemInput) {
	f := itemForm{
		Name: formText(r, "name"), Brand: formText(r, "brand"), Category: formText(r, "category"),
		Unit: formText(r, "unit"), Quantity: formText(r, "quantity"), QuantityUnit: formText(r, "quantity_unit"),
		MinQuantity: formText(r, "min_quantity"), MinUnit: formText(r, "min_unit"), ExpiresOn: formText(r, "expires_on"),
		Location: formText(r, "location"), Notes: formText(r, "notes"), VehicleIDs: parseIDs(r.PostForm["vehicle"]),
		Errors: stock.Errors{},
	}
	unit := unitOrDefault(f.Unit)
	in := stock.ItemInput{
		Name: f.Name, Brand: f.Brand, Category: f.Category, Unit: stock.Unit(f.Unit),
		Location: f.Location, Notes: f.Notes, VehicleIDs: append([]int64(nil), f.VehicleIDs...),
	}
	if q, err := parseOptAmount(f.Quantity, unit, f.QuantityUnit); err != nil {
		f.Errors.Add("quantity", err.Error())
	} else if q != nil {
		in.Quantity = *q
	}
	if q, err := parseOptAmount(f.MinQuantity, unit, f.MinUnit); err != nil {
		f.Errors.Add("min_quantity", err.Error())
	} else {
		in.MinQuantity = q
	}
	if d, err := parseOptDate(f.ExpiresOn); err != nil {
		f.Errors.Add("expires_on", err.Error())
	} else {
		in.ExpiresOn = d
	}

	codes, kinds, labels := r.PostForm["sku_code"], r.PostForm["sku_kind"], r.PostForm["sku_label"]
	packs, packUnits := r.PostForm["sku_pack"], r.PostForm["sku_pack_unit"]
	at := func(vals []string, i int) string {
		if i < len(vals) {
			return vals[i]
		}
		return ""
	}
	for i := range codes {
		row := skuRow{Code: at(codes, i), Kind: at(kinds, i), Label: at(labels, i), Pack: at(packs, i), PackUnit: at(packUnits, i)}
		sku := stock.SKU{Code: row.Code, Kind: stock.SKUKind(row.Kind), Label: row.Label}
		if p, err := parseOptAmount(row.Pack, unit, row.PackUnit); err != nil {
			row.Error = "Pack size: " + err.Error()
			f.Errors.Add("skus", row.Error)
		} else {
			sku.PackSize = p
		}
		f.SKUs = append(f.SKUs, row)
		in.SKUs = append(in.SKUs, sku)
	}

	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	// Map per-row SKU errors back onto the visible (non-empty) rows.
	visible := 0
	for i := range f.SKUs {
		if f.SKUs[i].Code == "" && f.SKUs[i].Label == "" && f.SKUs[i].Pack == "" {
			continue
		}
		if msg, ok := f.Errors[fmt.Sprintf("sku_%d", visible)]; ok {
			f.SKUs[i].Error = msg
			f.Errors.Add("skus", "Check the SKU rows")
		}
		visible++
	}
	return f, in
}

func (s *Server) itemFormPage(r *http.Request, f itemForm, isNew bool) (itemFormPage, error) {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return itemFormPage{}, err
	}
	cats, err := s.store.Categories(ctx)
	if err != nil {
		return itemFormPage{}, err
	}
	locs, err := s.store.Locations(ctx)
	if err != nil {
		return itemFormPage{}, err
	}
	// Always offer an empty SKU row to type into.
	f.SKUs = append(f.SKUs, skuRow{Kind: string(stock.SKUOEM)})
	return itemFormPage{
		Form: f, IsNew: isNew, Vehicles: vehicles, Categories: cats, Locations: locs,
		Units: stock.Units, SubUnits: stock.SubUnits, SKUKinds: stock.SKUKinds,
	}, nil
}

func (s *Server) itemNew(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := itemForm{Unit: string(stock.UnitPiece), QuantityUnit: "pcs", MinUnit: "pcs", VehicleIDs: parseIDs(q["vehicle"])}
	if u := stock.Unit(q.Get("unit")); u.Valid() {
		f.Unit = string(u)
		_, f.QuantityUnit = stock.SplitForInput(0, u)
		f.MinUnit = f.QuantityUnit
	}
	f.Category = q.Get("category")
	f.Location = q.Get("location")
	page, err := s.itemFormPage(r, f, true)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "item_form", View{Title: "New item", Nav: "stock", Data: page})
	return nil
}

func (s *Server) itemCreate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	f, in := parseItemForm(r)
	if !f.Errors.Any() {
		id, err := s.store.CreateItem(r.Context(), in)
		if err == nil {
			if r.PostFormValue("next") == "new" {
				q := url.Values{"unit": {string(in.Unit)}, "category": {in.Category}, "location": {in.Location}}
				for _, v := range in.VehicleIDs {
					q.Add("vehicle", strconv.FormatInt(v, 10))
				}
				s.redirect(w, r, "/items/new?"+q.Encode(), "Added "+displayInput(in)+". Add the next one.")
				return nil
			}
			s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Added "+displayInput(in))
			return nil
		}
		if !formConflict(f.Errors, err) {
			return err
		}
	}
	page, err := s.itemFormPage(r, f, true)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusUnprocessableEntity, "item_form", View{Title: "New item", Nav: "stock", Data: page})
	return nil
}

func (s *Server) itemEdit(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		return err
	}
	page, err := s.itemFormPage(r, itemFormFromItem(it), false)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "item_form", View{Title: "Edit " + it.DisplayName(), Nav: "stock", Data: page})
	return nil
}

func (s *Server) itemUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	f, in := parseItemForm(r)
	f.ID = id
	if !f.Errors.Any() {
		err := s.store.UpdateItem(r.Context(), id, in)
		if err == nil {
			s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Saved "+displayInput(in))
			return nil
		}
		if !formConflict(f.Errors, err) {
			return err
		}
	}
	page, err := s.itemFormPage(r, f, false)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusUnprocessableEntity, "item_form", View{Title: "Edit item", Nav: "stock", Data: page})
	return nil
}

// formConflict copies store errors that belong to a form field into errs.
func formConflict(errs stock.Errors, err error) bool {
	var conflict *store.ConflictError
	var short *store.InsufficientStockError
	switch {
	case errors.As(err, &conflict):
		errs.Add(conflict.Field, conflict.Message)
		return true
	case errors.As(err, &short):
		errs.Add("parts", short.Error())
		return true
	case errors.Is(err, store.ErrNotFound):
		errs.Add("form", "Something referenced here no longer exists. Reload and try again.")
		return true
	}
	var verrs stock.Errors
	if errors.As(err, &verrs) {
		for k, v := range verrs {
			errs.Add(k, v)
		}
		return true
	}
	return false
}

func displayInput(in stock.ItemInput) string {
	return stock.Item{Brand: in.Brand, Name: in.Name}.DisplayName()
}

func (s *Server) itemDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteItem(r.Context(), id); err != nil {
		return err
	}
	s.redirect(w, r, "/stock", "Deleted "+it.DisplayName())
	return nil
}

func (s *Server) itemDuplicate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	newID, err := s.store.DuplicateItem(r.Context(), id)
	if err != nil {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/items/%d/edit", newID), "Copy created. Adjust it and save.")
	return nil
}

// ---- Item detail ----

type movementForm struct {
	Kind, Amount, AmountUnit, TotalPrice, Supplier, OccurredOn, Note string
	Errors                                                           stock.Errors
}

type itemPage struct {
	Item          stock.Item
	Vehicles      []stock.Vehicle
	VehicleMap    map[int64]stock.Vehicle
	Movements     []stock.Movement
	Kits          []stock.Kit
	UnitPrice     *float64
	StockValue    *float64
	Move          movementForm
	OnShopping    bool
	SubUnits      []stock.SubUnit
	ExpiryState   string
	PhotoVersion  int64
	PurchaseCount int
}

func (s *Server) loadItemPage(r *http.Request, id int64, move movementForm) (itemPage, error) {
	ctx := r.Context()
	it, err := s.store.GetItem(ctx, id)
	if err != nil {
		return itemPage{}, err
	}
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return itemPage{}, err
	}
	moves, err := s.store.ListMovements(ctx, store.MovementFilter{ItemID: id, Limit: 100})
	if err != nil {
		return itemPage{}, err
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{ItemID: id})
	if err != nil {
		return itemPage{}, err
	}
	price, err := s.store.LastUnitPrice(ctx, id)
	if err != nil {
		return itemPage{}, err
	}
	shopping, err := s.store.ListShopping(ctx)
	if err != nil {
		return itemPage{}, err
	}
	p := itemPage{
		Item: it, Vehicles: vehicles, VehicleMap: vehicleMap(vehicles), Movements: moves, Kits: kits,
		UnitPrice: price, Move: move, SubUnits: stock.SubUnitsFor(it.Unit),
		ExpiryState: it.ExpiryState(s.now(), store.ExpiringDays), PhotoVersion: it.UpdatedAt.Unix(),
	}
	if price != nil {
		v := *price * it.Quantity
		p.StockValue = &v
	}
	for _, e := range shopping {
		if e.ItemID != nil && *e.ItemID == id {
			p.OnShopping = true
		}
	}
	for _, m := range moves {
		if m.Kind == stock.MovePurchase {
			p.PurchaseCount++
		}
	}
	if p.Move.Kind == "" {
		p.Move.Kind = string(stock.MovePurchase)
	}
	if p.Move.AmountUnit == "" {
		_, p.Move.AmountUnit = stock.SplitForInput(0, it.Unit)
	}
	if p.Move.OccurredOn == "" {
		p.Move.OccurredOn = s.now().Format("2006-01-02")
	}
	return p, nil
}

func (s *Server) itemShow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.loadItemPage(r, id, movementForm{})
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "item_show", View{Title: p.Item.DisplayName(), Nav: "stock", Data: p})
	return nil
}

// itemMovement records a purchase, usage or correction from the item page.
func (s *Server) itemMovement(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		return err
	}
	f := movementForm{
		Kind: formText(r, "kind"), Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"),
		TotalPrice: formText(r, "total_price"), Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"),
		Note: formText(r, "note"), Errors: stock.Errors{},
	}
	in := stock.MovementInput{ItemID: id, Kind: stock.MovementKind(f.Kind), Supplier: f.Supplier, Note: f.Note}
	amount, err := stock.ParseAmount(f.Amount, it.Unit, f.AmountUnit)
	if err != nil {
		f.Errors.Add("amount", "Enter an amount, e.g. "+examplesFor(it.Unit))
	}
	switch in.Kind {
	case stock.MoveUse:
		in.Delta = -amount
	case stock.MoveCorrection:
		// Corrections set the stock to the counted amount.
		in.Delta = amount - it.Quantity
		if in.Delta == 0 && !f.Errors.Any() {
			s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Stock already matches the count")
			return nil
		}
	default:
		in.Delta = amount
	}
	if p, err := parseOptMoney(f.TotalPrice); err != nil {
		f.Errors.Add("total_price", err.Error())
	} else {
		in.TotalPrice = p
	}
	if d, err := parseOptDate(f.OccurredOn); err != nil {
		f.Errors.Add("occurred_on", err.Error())
	} else {
		in.OccurredOn = d
	}
	if !f.Errors.Any() {
		err := s.store.AddMovement(r.Context(), in)
		if err == nil {
			msg := map[stock.MovementKind]string{
				stock.MovePurchase:   "Added " + stock.FormatQuantity(in.Delta, it.Unit) + " to stock",
				stock.MoveUse:        "Took " + stock.FormatQuantity(-in.Delta, it.Unit) + " from stock",
				stock.MoveCorrection: "Stock corrected to " + stock.FormatQuantity(amount, it.Unit),
			}[in.Kind]
			s.redirect(w, r, fmt.Sprintf("/items/%d", id), msg)
			return nil
		}
		var short *store.InsufficientStockError
		if errors.As(err, &short) {
			f.Errors.Add("amount", "Only "+stock.FormatQuantity(short.Have, short.Unit)+" on stock")
		} else if !formConflict(f.Errors, err) {
			return err
		}
	}
	p, err := s.loadItemPage(r, id, f)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusUnprocessableEntity, "item_show", View{Title: p.Item.DisplayName(), Nav: "stock", Data: p})
	return nil
}

func examplesFor(u stock.Unit) string {
	switch u {
	case stock.UnitML:
		return "500 ml or 1.5 L"
	case stock.UnitGram:
		return "400 g or 1 kg"
	}
	return "2"
}

// itemAdjust is the quick -/+ on stock rows.
func (s *Server) itemAdjust(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	ctx := r.Context()
	it, err := s.store.GetItem(ctx, id)
	if err != nil {
		return err
	}
	step := stock.DefaultStep(it.Unit)
	var in stock.MovementInput
	if r.PostFormValue("dir") == "down" {
		if it.Quantity <= 0 {
			return s.adjustDone(w, r, it, "error", "Nothing left to take")
		}
		in = stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -min(step, it.Quantity), Note: "Quick adjust"}
	} else {
		in = stock.MovementInput{ItemID: id, Kind: stock.MoveCorrection, Delta: step, Note: "Quick adjust"}
	}
	if err := s.store.AddMovement(ctx, in); err != nil {
		var short *store.InsufficientStockError
		if errors.As(err, &short) {
			return s.adjustDone(w, r, it, "error", short.Error())
		}
		return err
	}
	if it, err = s.store.GetItem(ctx, id); err != nil {
		return err
	}
	return s.adjustDone(w, r, it, "success", it.DisplayName()+": "+stock.FormatQuantity(it.Quantity, it.Unit))
}

func (s *Server) adjustDone(w http.ResponseWriter, r *http.Request, it stock.Item, kind, msg string) error {
	if !isHTMX(r) {
		s.setFlash(w, kind, msg)
		http.Redirect(w, r, safeBack(r, "/stock"), http.StatusSeeOther)
		return nil
	}
	vehicles, err := s.store.ListVehicles(r.Context())
	if err != nil {
		return err
	}
	toast(w, kind, msg)
	s.renderPartial(w, r, "item-row", rowData{Item: it, Vehicles: vehicleMap(vehicles), Flash: kind == "success"})
	return nil
}

func (s *Server) itemToShopping(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		return err
	}
	in := stock.ShoppingInput{ItemID: &id}
	if q, err := parseOptAmount(formText(r, "quantity"), it.Unit, formText(r, "quantity_unit")); err == nil {
		in.Quantity = q
	}
	if in.Quantity == nil && it.MinQuantity != nil && *it.MinQuantity > it.Quantity {
		need := *it.MinQuantity - it.Quantity
		in.Quantity = &need
	}
	if _, err := s.store.AddShopping(r.Context(), in); err != nil {
		return err
	}
	s.redirect(w, r, safeBack(r, fmt.Sprintf("/items/%d", id)), it.DisplayName()+" is on the shopping list")
	return nil
}

// ---- Photos ----

const maxPhotoBytes = 15 << 20

func (s *Server) itemPhotoUpload(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		s.setFlash(w, "error", "The photo is too large (max 15 MB)")
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther)
		return nil
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		s.setFlash(w, "error", "Choose a photo to upload")
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther)
		return nil
	}
	defer file.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(file); err != nil {
		return err
	}
	full, thumb, err := processPhoto(buf.Bytes())
	if err != nil {
		s.setFlash(w, "error", "That file is not a JPEG, PNG, GIF or WebP image")
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther)
		return nil
	}
	if err := s.store.SetPhoto(r.Context(), id, "image/jpeg", full, thumb); err != nil {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Photo saved")
	return nil
}

func (s *Server) itemPhotoDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.store.DeletePhoto(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Photo removed")
	return nil
}

func (s *Server) itemPhoto(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.store.GetPhoto(r.Context(), id, r.URL.Query().Get("size") == "thumb")
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	http.ServeContent(w, r, "", p.UpdatedAt, bytes.NewReader(p.Data))
	return nil
}

// ---- Shared helpers ----

// rowData feeds the "item-row" partial.
type rowData struct {
	Item      stock.Item
	Vehicles  map[int64]stock.Vehicle
	HideChips bool
	Flash     bool
}

func vehicleMap(vs []stock.Vehicle) map[int64]stock.Vehicle {
	m := make(map[int64]stock.Vehicle, len(vs))
	for _, v := range vs {
		m[v.ID] = v
	}
	return m
}
