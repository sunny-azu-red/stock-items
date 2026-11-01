package web

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	QuantityWas                 string // base units, as the edit form was opened
	MinQuantity, MinUnit        string
	ExpiresOn, Location, Notes  string
	// How the starting stock of a new item was bought (create only), and
	// the free-text shopping entry it was bought from, if any.
	TotalPrice, Supplier, BoughtOn string
	ShoppingID                     int64
	VehicleIDs                     []int64
	SKUs                           []skuRow
	Errors                         stock.Errors
	// Stored is the saved name while editing, for the heading: what is
	// typed may be blank.
	Stored string
	// Version is the item's version as the edit form was opened, posted
	// back unchanged; Stale is set when saving was refused for it.
	Version int
	Stale   *staleNote
	FormKey string // the form's one-time key, kept when it comes back with errors
}

type itemFormPage struct {
	Form       itemForm
	IsNew      bool
	Vehicles   []stock.Vehicle
	Brands     []string
	Categories []string
	Locations  []string
	Units      []stock.Unit
	SubUnits   []stock.SubUnit
	SKUKinds   []stock.SKUKind
	Matches    []stock.Item // for a new item from the shopping list: items with its name
	// ShoppingVersion is the shopping entry's version, for its Matches
	// buttons.
	ShoppingVersion int
}

func itemFormFromItem(it stock.Item) itemForm {
	f := itemForm{
		ID: it.ID, Name: it.Name, Brand: it.Brand, Category: it.Category, Unit: string(it.Unit),
		Location: it.Location, Notes: it.Notes, VehicleIDs: it.VehicleIDs, Stored: it.DisplayName(), Version: it.Version,
	}
	f.Quantity, f.QuantityUnit = stock.SplitForInput(it.Quantity, it.Unit)
	f.QuantityWas = stock.FormatNumber(it.Quantity)
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
// form.Errors; the returned input is only meaningful without errors. The
// purchase fields are only read for a new item (isNew); dates are checked
// against today.
func parseItemForm(r *http.Request, isNew bool, today time.Time) (itemForm, stock.ItemInput) {
	f := itemForm{
		Name: formText(r, "name"), Brand: formText(r, "brand"), Category: formText(r, "category"),
		Unit: formText(r, "unit"), Quantity: formText(r, "quantity"), QuantityUnit: formText(r, "quantity_unit"),
		QuantityWas: formText(r, "quantity_was"),
		MinQuantity: formText(r, "min_quantity"), MinUnit: formText(r, "min_unit"), ExpiresOn: formText(r, "expires_on"),
		Location: formText(r, "location"), Notes: formText(r, "notes"), VehicleIDs: parseIDs(r.PostForm["vehicle"]),
		Version: parseVersion(r), FormKey: parseFormKey(r), Errors: stock.Errors{},
	}
	checkFormKey(f.Errors, f.FormKey)
	unit := unitOrDefault(f.Unit)
	in := stock.ItemInput{
		Name: f.Name, Brand: f.Brand, Category: f.Category, Unit: stock.Unit(f.Unit),
		Location: f.Location, Notes: f.Notes, VehicleIDs: append([]int64(nil), f.VehicleIDs...), Version: f.Version,
		FormKey: f.FormKey,
	}
	if q, err := parseOptAmount(f.Quantity, unit, f.QuantityUnit); err != nil {
		f.Errors.Add("quantity", err.Error())
	} else if q != nil {
		in.Quantity = *q
	} else if !isNew {
		// Blank would mean 0 and silently empty the stock.
		f.Errors.Add("quantity", "Enter the amount on stock (0 if none)")
	}
	if isNew {
		f.TotalPrice, f.Supplier, f.BoughtOn = formText(r, "total_price"), formText(r, "supplier"), formText(r, "bought_on")
		in.Supplier = f.Supplier
		if id := parseOptID(formText(r, "shopping_id")); id != nil {
			f.ShoppingID = *id
		}
		if p, err := parseOptMoney(f.TotalPrice); err != nil {
			f.Errors.Add("total_price", err.Error())
		} else {
			in.TotalPrice = p
		}
		if d, err := parseEventDate(f.BoughtOn, today); err != nil {
			f.Errors.Add("bought_on", err.Error())
		} else {
			in.BoughtOn = d
		}
		if f.ShoppingID != 0 && in.Quantity == 0 && f.Errors["quantity"] == "" {
			// Ticking the entry off with nothing on stock would lose it.
			f.Errors.Add("quantity", "Enter how much you bought")
		}
	}
	if was, err := strconv.ParseFloat(f.QuantityWas, 64); err == nil {
		in.QuantityWas = &was
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
	var kept []bool // whether Normalize keeps row i, to map errors back
	for i := range codes {
		row := skuRow{Code: at(codes, i), Kind: at(kinds, i), Label: at(labels, i), Pack: at(packs, i), PackUnit: at(packUnits, i)}
		sku := stock.SKU{Code: row.Code, Kind: stock.SKUKind(row.Kind), Label: row.Label}
		if p, err := parseOptAmount(row.Pack, unit, row.PackUnit); err != nil {
			row.Error = "Pack size: " + err.Error()
			f.Errors.Add("skus", row.Error)
		} else {
			sku.PackSize = p
		}
		kept = append(kept, strings.TrimSpace(sku.Code) != "" || strings.TrimSpace(sku.Label) != "" || sku.PackSize != nil)
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
		if !kept[i] {
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
	brands, err := s.store.Brands(ctx)
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
		Form: f, IsNew: isNew, Vehicles: activeVehicles(vehicles, f.VehicleIDs...), Brands: brands, Categories: cats, Locations: locs,
		Units: stock.Units, SubUnits: stock.SubUnits, SKUKinds: stock.SKUKinds,
	}, nil
}

func (s *Server) itemNew(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := itemForm{
		Unit: string(stock.UnitPiece), QuantityUnit: "pcs", MinUnit: "pcs", VehicleIDs: parseIDs(q["vehicle"]),
		BoughtOn: s.now().Format("2006-01-02"),
	}
	if u := stock.Unit(q.Get("unit")); u.Valid() {
		f.Unit = string(u)
		_, f.QuantityUnit = stock.SplitForInput(0, u)
		f.MinUnit = f.QuantityUnit
	}
	f.Category = q.Get("category")
	f.Location = q.Get("location")
	var shoppingVersion int
	if id := parseOptID(q.Get("from")); id != nil {
		// Duplicate: start from another item's fields; nothing is saved yet.
		it, err := s.store.GetItem(r.Context(), *id)
		if err != nil {
			return err
		}
		f = duplicateForm(it, f.BoughtOn)
	}
	if id := parseOptID(q.Get("shopping")); id != nil {
		// Something new bought from the shopping list.
		e, err := s.store.GetShopping(r.Context(), *id)
		if err != nil {
			return err
		}
		if e.ItemID != nil {
			return store.ErrNotFound
		}
		f.ShoppingID, f.Name, f.Notes, f.Unit = e.ID, e.FreeText, e.Note, string(e.ItemUnit)
		shoppingVersion = e.Version
		_, f.MinUnit = stock.SplitForInput(0, e.ItemUnit)
		f.QuantityUnit = f.MinUnit
		if e.Quantity != nil {
			f.Quantity, f.QuantityUnit = stock.SplitForInput(*e.Quantity, e.ItemUnit)
		}
	}
	page, err := s.itemFormPage(r, f, true)
	if err != nil {
		return err
	}
	if f.ShoppingID != 0 {
		if page.Matches, err = s.store.ItemsNamed(r.Context(), f.Name); err != nil {
			return err
		}
		page.ShoppingVersion = shoppingVersion
	}
	s.render(w, r, http.StatusOK, "item_form", View{Title: "New item", Nav: "stock", Data: page})
	return nil
}

func (s *Server) itemCreate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	// A repeat goes to the item, whatever the first save changed since.
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatItem, itemPath, "/stock"); done || err != nil {
		return err
	}
	f, in := parseItemForm(r, true, s.now())
	if in.BoughtOn == nil {
		in.BoughtOn = s.today()
	}
	if !f.Errors.Any() {
		var id int64
		var err error
		if f.ShoppingID != 0 {
			id, err = s.store.CreateItemFromShopping(r.Context(), f.ShoppingID, in)
		} else {
			id, err = s.store.CreateItem(r.Context(), in)
		}
		if s.savedAlready(w, r, err, itemPath, "/stock") {
			return nil
		}
		if f.ShoppingID != 0 && errors.Is(err, store.ErrNotFound) {
			// Ticked off elsewhere, or this form was saved already. Keep what
			// was typed, and let the next save add it as a plain new item.
			f.ShoppingID = 0
			f.Errors.Add("form", "The shopping entry is gone (ticked off, or this item was saved already). Check your stock, or save again to add it anyway.")
		} else if err == nil {
			if f.ShoppingID != 0 {
				s.redirect(w, r, "/shopping", "Added "+displayInput(in)+" to stock")
				return nil
			}
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
	if f.ShoppingID != 0 {
		if page.Matches, err = s.store.ItemsNamed(r.Context(), f.Name); err != nil {
			return err
		}
		e, err := s.store.GetShopping(r.Context(), f.ShoppingID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		page.ShoppingVersion = e.Version
	}
	s.render(w, r, http.StatusUnprocessableEntity, "item_form", View{Title: "New item", Nav: "stock", Data: page})
	return nil
}

// duplicateForm is a new-item form that starts from it: the same fields,
// part numbers and vehicles, but no stock, no best-before date and no
// barcodes (a scan should find one item).
func duplicateForm(it stock.Item, boughtOn string) itemForm {
	f := itemFormFromItem(it)
	f.ID, f.QuantityWas, f.ExpiresOn, f.BoughtOn = 0, "", "", boughtOn
	f.Quantity = ""
	_, f.QuantityUnit = stock.SplitForInput(0, it.Unit)
	f.Name = it.Name + " (copy)"
	if utf8.RuneCountInString(f.Name) > 200 {
		f.Name = it.Name
	}
	skus := f.SKUs[:0]
	for _, row := range f.SKUs {
		if row.Kind != string(stock.SKUBarcode) {
			skus = append(skus, row)
		}
	}
	f.SKUs = skus
	return f
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
	s.render(w, r, http.StatusOK, "item_form", View{Title: "Edit " + page.Form.Stored, Nav: "stock", Data: page})
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
	// Saved already comes first: the first save moved the version on.
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatItem, itemPath, "/stock"); done || err != nil {
		return err
	}
	f, in := parseItemForm(r, false, s.now())
	f.ID = id
	if !f.Errors.Any() {
		err := s.store.UpdateItem(r.Context(), id, in, *s.today())
		if err == nil {
			s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Saved "+displayInput(in))
			return nil
		}
		if s.savedAlready(w, r, err, itemPath, "/stock") {
			return nil
		}
		// Changed or deleted meanwhile: say so, and keep what was typed.
		if f.Stale = staleForm(err, itemPath(id)+"/edit"); f.Stale == nil && !formConflict(f.Errors, err) {
			return err
		}
	}
	// Deleted meanwhile, the form still shows what was typed.
	f.Stored = cmp.Or(strings.TrimSpace(f.Brand+" "+f.Name), "item")
	if it, err := s.store.GetItem(r.Context(), id); err == nil {
		f.Stored = it.DisplayName()
	}
	page, err := s.itemFormPage(r, f, false)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusUnprocessableEntity, "item_form", View{Title: "Edit " + f.Stored, Nav: "stock", Data: page})
	return nil
}

// formConflict copies store errors that belong to a form field into errs.
func formConflict(errs stock.Errors, err error) bool {
	var conflict *store.ConflictError
	var short *store.InsufficientStockError
	var stale *store.StaleError
	switch {
	case errors.As(err, &stale):
		// Forms that carry a version show it as a banner (staleForm); this
		// is for any other form, so it never reads as a missing reference.
		errs.Add("form", stale.Error())
		return true
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
	if err := parseForm(w, r); err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatItem, id), itemPath(id), "/stock") {
		return nil
	} else if err != nil {
		return err
	}
	var conflict *store.ConflictError
	trashID, err := s.store.DeleteItem(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, itemPath(id), "/stock") {
		return nil
	} else if errors.As(err, &conflict) {
		// Used since the page was opened: keep it and say why.
		s.setFlash(w, "error", conflict.Message)
		s.redirect(w, r, fmt.Sprintf("/items/%d", id), "")
		return nil
	} else if err != nil {
		return err
	}
	s.redirectUndo(w, r, "/stock", "Deleted "+it.DisplayName(), trashID)
	return nil
}

// itemArchive hides an item that other records refer to.
func (s *Server) itemArchive(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatItem, id), itemPath(id), "/stock") {
		return nil
	} else if err != nil {
		return err
	}
	listTrash, err := s.store.ArchiveItem(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, itemPath(id), "/stock") {
		return nil
	} else if err != nil {
		return err
	}
	msg := "Archived " + it.DisplayName() + ". Its history stays; restore it any time."
	if listTrash != 0 {
		msg = "Archived " + it.DisplayName() + ". Its history stays; restore it any time. Its shopping list entry is in the trash."
	}
	s.redirect(w, r, fmt.Sprintf("/items/%d", id), msg)
	return nil
}

func (s *Server) itemRestore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if s.staleButton(w, r, s.gone(r, err, store.WhatItem, id), itemPath(id), "/stock") {
		return nil
	} else if err != nil {
		return err
	}
	err = s.store.RestoreItem(r.Context(), id, parseVersion(r))
	if s.staleButton(w, r, err, itemPath(id), "/stock") {
		return nil
	} else if err != nil {
		return err
	}
	s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Restored "+it.DisplayName())
	return nil
}

// ---- Item detail ----

type movementForm struct {
	Kind, Amount, AmountUnit, TotalPrice, Supplier, OccurredOn, Note string
	ExpiresOn                                                        string // purchases only
	Errors                                                           stock.Errors
	// The shopping entry's version and a refusal for it, on the Bought
	// form (see itemForm); the item page's stock changes don't have one.
	Version int
	Stale   *staleNote
	FormKey string // see itemForm
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
	PurchaseCount int
	Usage         stock.ItemUsage
	HistoryCapped bool   // Movements holds only the first itemHistory entries
	Today         string // yyyy-mm-dd, the default date of a stock change
	History       sortHeads
}

// AllEntriesQuery is the query string of the item's full history on the
// Activity page, in the same order.
func (p itemPage) AllEntriesQuery() string {
	q := p.History.Order.Values()
	q.Set("item", strconv.FormatInt(p.Item.ID, 10))
	return q.Encode()
}

// itemHistory is how many stock changes the item page shows.
const itemHistory = 100

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
	order := historySorts.parse(r.URL.Query())
	moves, err := s.store.ListMovements(ctx, store.MovementFilter{ItemID: id, Limit: itemHistory + 1, Sort: order.Key, Desc: order.Desc})
	if err != nil {
		return itemPage{}, err
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{ItemID: id, IncludeArchived: true})
	if err != nil {
		return itemPage{}, err
	}
	price, err := s.store.LastUnitPrice(ctx, id)
	if err != nil {
		return itemPage{}, err
	}
	purchases, err := s.store.CountPurchases(ctx, id)
	if err != nil {
		return itemPage{}, err
	}
	usage, err := s.store.ItemUsage(ctx, id)
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
		ExpiryState: it.ExpiryState(s.now(), store.ExpiringDays), PurchaseCount: purchases, Usage: usage,
		HistoryCapped: len(moves) > itemHistory, Today: s.now().Format("2006-01-02"),
		History: sortHeads{Order: order, Path: itemPath(id), Anchor: "history"},
	}
	if p.HistoryCapped {
		p.Movements = moves[:itemHistory]
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
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatItem, itemPath, "/stock"); done || err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		return err
	}
	if it.Archived() {
		s.setFlash(w, "error", "Restore "+it.DisplayName()+" to record stock changes")
		s.redirect(w, r, itemPath(id), "")
		return nil
	}
	f := movementForm{
		Kind: formText(r, "kind"), Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"),
		TotalPrice: formText(r, "total_price"), Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"),
		Note: formText(r, "note"), ExpiresOn: formText(r, "expires_on"), FormKey: parseFormKey(r), Errors: stock.Errors{},
	}
	checkFormKey(f.Errors, f.FormKey)
	in := stock.MovementInput{ItemID: id, Kind: stock.MovementKind(f.Kind), Note: f.Note, FormKey: f.FormKey}
	switch in.Kind {
	case stock.MovePurchase, stock.MoveUse, stock.MoveCorrection:
	default:
		// Only these three have a form; "initial" and "service" are recorded
		// by the app itself.
		in.Kind, f.Kind = stock.MovePurchase, string(stock.MovePurchase)
		f.Errors.Add("kind", "Unknown kind of stock change")
	}
	amount, err := stock.ParseAmount(f.Amount, it.Unit, f.AmountUnit)
	if err != nil {
		f.Errors.Add("amount", amountError(err, it.Unit, "Enter an amount"))
	}
	switch in.Kind {
	case stock.MoveUse:
		in.Delta = -amount
	case stock.MoveCorrection:
		// Corrections set the stock to the counted amount; the store works
		// out the change under a row lock.
		in.Delta = amount
	default:
		in.Delta = amount
	}
	if d, err := parseEventDate(f.OccurredOn, s.now()); err != nil {
		f.Errors.Add("occurred_on", err.Error())
	} else if d != nil {
		in.OccurredOn = d
	} else {
		in.OccurredOn = s.today()
	}
	// Only the Bought form has a price, shop and best-before date.
	if in.Kind == stock.MovePurchase {
		in.Supplier = f.Supplier
		if p, err := parseOptMoney(f.TotalPrice); err != nil {
			f.Errors.Add("total_price", err.Error())
		} else {
			in.TotalPrice = p
		}
		if d, err := parseOptDate(f.ExpiresOn); err != nil {
			f.Errors.Add("expires_on", err.Error())
		} else {
			in.ExpiresOn = d
		}
	}
	if !f.Errors.Any() {
		var err error
		if in.Kind == stock.MoveCorrection {
			var delta float64
			delta, err = s.store.CorrectStock(r.Context(), id, amount, in.Note, *in.OccurredOn, f.FormKey)
			if err == nil && delta == 0 {
				s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Stock already matches the count")
				return nil
			}
		} else {
			err = s.store.AddMovement(r.Context(), in)
		}
		if err == nil {
			msg := map[stock.MovementKind]string{
				stock.MovePurchase:   "Added " + stock.FormatQuantity(in.Delta, it.Unit) + " to stock",
				stock.MoveUse:        "Took " + stock.FormatQuantity(-in.Delta, it.Unit) + " from stock",
				stock.MoveCorrection: "Stock corrected to " + stock.FormatQuantity(amount, it.Unit),
			}[in.Kind]
			s.redirect(w, r, fmt.Sprintf("/items/%d", id), msg)
			return nil
		}
		if s.savedAlready(w, r, err, itemPath, "/stock") {
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

// amountError explains why an amount was refused: the parser's reason
// ("pieces must be a whole number"), or for a blank or zero amount, the
// prompt with an example of what to type.
func amountError(err error, u stock.Unit, prompt string) string {
	if err == nil || errors.Is(err, stock.ErrEmpty) {
		return prompt + ", e.g. " + examplesFor(u)
	}
	msg := err.Error()
	return strings.ToUpper(msg[:1]) + msg[1:]
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
	if it.Archived() {
		return s.adjustDone(w, r, it, "error", "Restore "+it.DisplayName()+" to record stock changes")
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
	in.OccurredOn = s.today()
	if err := s.store.AddMovement(ctx, in); err != nil {
		var short *store.InsufficientStockError
		var verrs stock.Errors
		switch {
		case errors.As(err, &short):
			return s.adjustDone(w, r, it, "error", short.Error())
		case errors.As(err, &verrs):
			return s.adjustDone(w, r, it, "error", verrs["amount"])
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
		http.Redirect(w, r, s.back(r, "/stock"), http.StatusSeeOther)
		return nil
	}
	vehicles, err := s.store.ListVehicles(r.Context())
	if err != nil {
		return err
	}
	toast(w, kind, msg)
	s.renderPartial(w, r, "item-row", rowData{Item: it, Vehicles: vehicleMap(vehicles), Flash: kind == "success",
		HideChips: r.PostFormValue("hide_chips") == "1"})
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
	back := s.back(r, itemPath(id))
	key := parseFormKey(r)
	if done, err := s.savedBefore(w, r, key, "", nil, back); done || err != nil {
		return err
	}
	it, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		return err
	}
	if key == "" {
		s.setFlash(w, "error", noFormKey)
		s.redirect(w, r, back, "")
		return nil
	}
	in := stock.ShoppingInput{ItemID: &id, FormKey: key}
	if q, err := parseOptAmount(formText(r, "quantity"), it.Unit, formText(r, "quantity_unit")); err == nil && q != nil && *q > 0 {
		in.Quantity = q
	}
	if in.Quantity == nil {
		// What the item needs, for its minimum or any kit, like the suggestions.
		kits, err := s.store.ListKits(r.Context(), store.KitFilter{ItemID: id})
		if err != nil {
			return err
		}
		if sugg := stock.SuggestPurchases([]stock.Item{it}, kits, nil); len(sugg) == 1 {
			in.Quantity, in.Note = &sugg[0].Quantity, strings.Join(sugg[0].Reasons, "; ")
		}
	}
	if _, err := s.store.AddShopping(r.Context(), in); s.savedAlready(w, r, err, nil, back) {
		return nil
	} else if errors.Is(err, store.ErrNotFound) {
		s.setFlash(w, "error", it.DisplayName()+" was archived or deleted meanwhile")
		s.redirect(w, r, back, "")
		return nil
	} else if err != nil {
		return err
	}
	s.redirect(w, r, back, it.DisplayName()+" is on the shopping list")
	return nil
}

// ---- Photos ----

const (
	maxPhotoBytes      = 15 << 20
	photoUploadTimeout = 5 * time.Minute
	photoTooLarge      = "The photo is too large (max 15 MB)"
	// maxPhotoFileBytes leaves room for the form around the file, for the
	// check app.js makes before uploading.
	maxPhotoFileBytes = maxPhotoBytes - 64<<10
)

func (s *Server) itemPhotoUpload(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	// A 15 MB photo over a slow phone connection can take minutes. Both
	// deadlines count from the request headers, so extend both.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(photoUploadTimeout))
	_ = rc.SetWriteDeadline(time.Now().Add(photoUploadTimeout + 30*time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		msg := "Choose a photo to upload"
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			msg = photoTooLarge
		}
		s.setFlash(w, "error", msg)
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
	full, thumb, err := processPhotoQueued(r.Context(), buf.Bytes())
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err // the client went away while waiting for its turn
	}
	if err != nil {
		msg := photoError(err)
		if errors.Is(err, errVipsMissing) {
			s.log.Error("photo upload refused: install libvips-tools", "item", id, "err", err)
		} else {
			s.log.Warn("photo upload refused", "item", id, "err", err)
		}
		s.setFlash(w, "error", msg)
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther)
		return nil
	}
	replaced, err := s.store.SetPhoto(r.Context(), id, "image/jpeg", full, thumb)
	if err != nil {
		return err
	}
	if replaced != 0 {
		s.redirectUndo(w, r, fmt.Sprintf("/items/%d", id), "Photo replaced", replaced)
		return nil
	}
	s.redirect(w, r, fmt.Sprintf("/items/%d", id), "Photo saved")
	return nil
}

// photoError is what the user is told about a photo that wasn't saved.
// What vips said goes to the log only.
func photoError(err error) string {
	switch {
	case errors.Is(err, errPhotoTooBig):
		return fmt.Sprintf("The photo has too many pixels (max %d megapixels)", photoMaxPixels/1_000_000)
	case errors.Is(err, errPhotoTooHeavy):
		return "The photo is too large to process (a very large HEIC, GIF, interlaced PNG or progressive JPEG). Save it smaller and try again."
	case errors.Is(err, errPhotoTimeout):
		return "Processing the photo took too long. Try a smaller photo."
	case errors.Is(err, errVipsMissing):
		return "Photos can't be processed: this server is missing libvips (libvips-tools). The photo was not saved."
	case errors.Is(err, errPhotoNotImage):
		return "That file is not a JPEG, PNG, GIF, WebP or HEIC image"
	}
	return "The photo could not be processed; the file may be damaged. Try another one."
}

func (s *Server) itemPhotoDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	trashID, err := s.store.DeletePhoto(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		// No photo: say so, unless the item is gone too (then 404).
		if _, err := s.store.GetItem(r.Context(), id); err != nil {
			return err
		}
		s.redirect(w, r, fmt.Sprintf("/items/%d", id), "There was no photo to remove")
		return nil
	}
	if err != nil {
		return err
	}
	s.redirectUndo(w, r, fmt.Sprintf("/items/%d", id), "Photo removed", trashID)
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
	// Restoring a photo brings back an older one, which If-Modified-Since
	// alone would take for the cached newer one. The tag tells them apart.
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, p.UpdatedAt.UnixMicro()))
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
