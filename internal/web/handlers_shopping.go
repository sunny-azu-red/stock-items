package web

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// shoppingForm is Add to the list: Item is what was typed or picked, and
// ItemID the stock item it is (0 for something new).
type shoppingForm struct {
	ItemID                     int64
	Item, Quantity, Note, Unit string
	FormKey                    string // see itemForm
	Errors                     stock.Errors
}

// shoppingPage is the shopping list, all of it or a vehicle's part
// (VehicleID): its entries (stock.ShoppingEntry.For) and suggestions, with
// the vehicle's items to add.
type shoppingPage struct {
	Entries     []stock.ShoppingEntry
	Others      int // entries left out: other vehicles'
	Suggestions []stock.Suggestion
	Items       []stock.Item
	Form        shoppingForm
	Vehicles    []stock.Vehicle // the filter chips: the active vehicles
	VehicleID   int64           // the vehicle whose part shows, 0 for all
	VehicleName string
	TitleName   string          // the vehicle the title names: VehicleName, unless the bar names it (worked on)
	chips       []stock.Vehicle // what a row's chips may name: chipVehicles
}

// chipVehicles are the vehicles a shopping row's chips may name: the active
// ones in the sidebar's order, then the archived ones (a row of an archived
// vehicle keeps its chip, as a stock row does), without the sole active
// vehicle, which is every page's scope (soleVehicle).
func chipVehicles(vehicles []stock.Vehicle) []stock.Vehicle {
	sole := soleVehicle(vehicles)
	var out []stock.Vehicle
	for _, archived := range []bool{false, true} {
		for _, v := range vehicles {
			if v.Archived() == archived && v.ID != sole {
				out = append(out, v)
			}
		}
	}
	return out
}

// VehicleQuery is the address of a vehicle's part (0: the whole list).
func (p shoppingPage) VehicleQuery(vehicleID int64) string {
	q := url.Values{}
	setVehicleParam(q, vehicleID)
	return q.Encode()
}

// Chips are the vehicles an entry or a suggestion is for, as the whole
// list shows them (a vehicle's part says its vehicle once, in the bar or
// the title, so its rows have none).
func (p shoppingPage) Chips(ids []int64) []stock.Vehicle {
	if p.VehicleID != 0 {
		return nil
	}
	var out []stock.Vehicle
	for _, v := range p.chips {
		if slices.Contains(ids, v.ID) {
			out = append(out, v)
		}
	}
	return out
}

// shoppingVehicle is the vehicle whose part of the list the request shows:
// the one worked on, else the address's (?v=) when it names a vehicle, and
// redirect reports the address to send the request to instead (the one for
// the vehicle worked on), "" when it is fine.
func shoppingVehicle(r *http.Request, vehicles []stock.Vehicle) (vid int64, redirect string) {
	vid = vehicleParam(r.URL.Query())
	if working := workingOn(r, vehicles); working != 0 && vid != working {
		return working, joinQuery("/shopping", shoppingPage{}.VehicleQuery(working))
	}
	if !slices.ContainsFunc(vehicles, func(v stock.Vehicle) bool { return v.ID == vid }) {
		vid = 0 // a vehicle that doesn't exist isn't chosen
	}
	return vid, ""
}

// shoppingListPath is the list an action on it goes back to: the part its
// form or address names (v), so a list filtered by a chip stays filtered
// (while a vehicle is worked on, /shopping is its part anyway).
func shoppingListPath(r *http.Request) string {
	q := url.Values{}
	setVehicleParam(q, listVehicle(r))
	return joinQuery("/shopping", q.Encode())
}

// listVehicle is the list's vehicle a request carries (v), 0 for none.
func listVehicle(r *http.Request) int64 {
	return vehicleParam(url.Values{"v": {r.FormValue("v")}})
}

// boughtPath is an entry's Bought page, for the list the request came from.
func boughtPath(r *http.Request, id int64) string {
	q := url.Values{}
	setVehicleParam(q, listVehicle(r))
	return joinQuery(fmt.Sprintf("/shopping/%d/bought", id), q.Encode())
}

// loadShopping reads the shopping list's part for vehicle vid (0: all; a
// vehicle that doesn't exist counts as none).
func (s *Server) loadShopping(r *http.Request, f shoppingForm, vid int64) (shoppingPage, error) {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return shoppingPage{}, err
	}
	entries, err := s.store.ListShopping(ctx)
	if err != nil {
		return shoppingPage{}, err
	}
	items, err := s.store.ListItems(ctx, store.ItemFilter{})
	if err != nil {
		return shoppingPage{}, err
	}
	kits, err := s.store.ListKits(ctx, store.KitFilter{})
	if err != nil {
		return shoppingPage{}, err
	}
	if !slices.ContainsFunc(vehicles, func(v stock.Vehicle) bool { return v.ID == vid }) {
		vid = 0
	}
	p := shoppingPage{Items: items, Form: f, Vehicles: activeVehicles(vehicles), VehicleID: vid, chips: chipVehicles(vehicles),
		// What is listed counts whoever it was added for: a shared item on
		// the list covers every vehicle's need.
		Suggestions: stock.SuggestPurchases(items, kits, listedQuantities(entries), *s.today(), vid)}
	p.Entries, p.Others = stock.ShoppingFor(entries, vid)
	if vid != 0 {
		for _, v := range vehicles {
			if v.ID == vid {
				p.VehicleName = v.Name
			}
		}
		if vid != workingOn(r, vehicles) {
			p.TitleName = p.VehicleName
		}
		// What the form adds shows on this part: the vehicle's items and
		// the General ones (free text always does).
		p.Items = slices.DeleteFunc(p.Items, func(it stock.Item) bool { return !it.Fits(vid) })
	}
	return p, nil
}

// listedQuantities maps each item on the shopping list to its listed
// quantity (nil when the entry has none), for stock.SuggestPurchases.
func listedQuantities(entries []stock.ShoppingEntry) map[int64]*float64 {
	listed := map[int64]*float64{}
	for _, e := range entries {
		if e.ItemID != nil {
			listed[*e.ItemID] = e.Quantity
		}
	}
	return listed
}

func (s *Server) shoppingList(w http.ResponseWriter, r *http.Request) error {
	vehicles, err := s.store.ListVehicles(r.Context())
	if err != nil {
		return err
	}
	// While a vehicle is worked on, the page shows its part, whatever the
	// address asks for.
	vid, to := shoppingVehicle(r, vehicles)
	if to != "" {
		if isHTMX(r) {
			w.Header().Set("HX-Redirect", to)
			return nil
		}
		http.Redirect(w, r, to, http.StatusSeeOther) //nolint:gosec // our own path and a vehicle id
		return nil
	}
	p, err := s.loadShopping(r, shoppingForm{}, vid)
	if err != nil {
		return err
	}
	s.renderShopping(w, r, http.StatusOK, p)
	return nil
}

// renderShopping renders the shopping page, titled with the vehicle it
// shows unless the bar names it.
func (s *Server) renderShopping(w http.ResponseWriter, r *http.Request, status int, p shoppingPage) {
	title := "Shopping"
	if p.TitleName != "" {
		title = p.TitleName + " shopping"
	}
	s.render(w, r, status, "shopping", View{Title: title, Nav: "shopping", Data: p})
}

func (s *Server) shoppingAdd(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	// A form opened before Item replaced them posts free_text or item_id.
	f := shoppingForm{Item: cmp.Or(formText(r, "item"), formText(r, "free_text")), Quantity: formText(r, "quantity"),
		Unit: formText(r, "quantity_unit"), Note: formText(r, "note"), FormKey: parseFormKey(r), Errors: stock.Errors{}}
	if done, err := s.savedBefore(w, r, f.FormKey, "", nil, shoppingListPath(r)); done || err != nil {
		return err
	}
	checkFormKey(f.Errors, f.FormKey)
	// The form posts to /shopping: it was the part of the vehicle worked
	// on, or the one its chip chose (v).
	vid, err := s.working(r)
	if err != nil {
		return err
	}
	if vid == 0 {
		vid = listVehicle(r)
	}
	in := stock.ShoppingInput{FreeText: f.Item, Note: f.Note, FormKey: f.FormKey}
	title := strings.TrimSpace(f.Item) // for the message
	it, gone, err := s.shoppingItem(r, f.Item, vid)
	switch {
	case err != nil:
		return err
	case gone:
		f.Errors.Add("item", "That item was archived or deleted meanwhile")
	case it != nil:
		f.ItemID, in.ItemID, in.Unit, in.FreeText = it.ID, &it.ID, it.Unit, ""
		title = it.DisplayName()
	}
	switch {
	case in.ItemID != nil:
		if q, err := parseOptAmount(f.Quantity, in.Unit, f.Unit); err != nil {
			f.Errors.Add("quantity", err.Error())
		} else {
			in.Quantity = q
		}
	case f.Quantity != "":
		// Something new: a typed suffix ("4 L") or the unit select decides.
		if q, u, err := stock.ParseAmountAnyUnit(f.Quantity, f.Unit); err != nil {
			f.Errors.Add("quantity", err.Error())
		} else {
			in.Quantity, in.Unit = &q, u
		}
	}
	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	if !f.Errors.Any() {
		_, err := s.store.AddShopping(r.Context(), in)
		if err == nil {
			s.redirect(w, r, shoppingListPath(r), "Added "+title+" to the shopping list")
			return nil
		}
		if s.savedAlready(w, r, err, nil, shoppingListPath(r)) {
			return nil
		}
		var invalid stock.Errors
		switch {
		case errors.As(err, &invalid):
			// The item's unit changed since the page was shown.
			for k, v := range invalid {
				f.Errors.Add(k, v)
			}
		case errors.Is(err, store.ErrNotFound):
			f.ItemID = 0
			f.Errors.Add("item", "That item was archived or deleted meanwhile")
		default:
			return err
		}
	}
	p, err := s.loadShopping(r, f, vid)
	if err != nil {
		return err
	}
	s.renderShopping(w, r, http.StatusUnprocessableEntity, p)
	return nil
}

// shoppingItem is the stock item Add to the list's Item is, nil for
// something new: the picked one (item_id) while the text still names it or
// is empty (a form from before Item, which had a select); else the one
// item the text names (stock.NamesItem) among those the form offers
// (active, and on a vehicle's part its own and the General ones), so what
// the line under the field said is what is saved. Several are something
// new too, and Bought offers them. gone reports a picked item deleted or
// archived meanwhile.
func (s *Server) shoppingItem(r *http.Request, text string, vid int64) (it *stock.Item, gone bool, err error) {
	if id := parseOptID(formText(r, "item_id")); id != nil {
		picked, err := s.store.GetItem(r.Context(), *id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, true, nil
		} else if err != nil {
			return nil, false, err
		}
		if strings.TrimSpace(text) == "" || stock.NamesItem(text, picked.Brand, picked.Name) {
			return &picked, picked.Archived(), nil
		}
		// Picked, then changed: the text says what to buy.
	}
	if strings.TrimSpace(text) == "" {
		return nil, false, nil
	}
	named, err := s.store.ItemsNamed(r.Context(), text)
	if err != nil {
		return nil, false, err
	}
	named = slices.DeleteFunc(named, func(it stock.Item) bool { return it.Archived() || (vid != 0 && !it.Fits(vid)) })
	if len(named) != 1 {
		return nil, false, nil
	}
	return &named[0], false, nil
}

// shoppingAddSuggestions adds one or more suggestions (item_id + qty + note
// triples, qty in base units). The note keeps the reason for the suggestion.
func (s *Server) shoppingAddSuggestions(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	ids, qtys, notes := r.PostForm["item_id"], r.PostForm["qty"], r.PostForm["note"]
	n := 0
	for i, raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || i >= len(qtys) {
			continue
		}
		// Not rounded: EnsureShopping refuses fractional pieces and amounts
		// finer than 0.01 ml or g, as ParseAmount does for typed ones.
		q, err := strconv.ParseFloat(qtys[i], 64)
		if err != nil || !(q > 0 && q <= stock.MaxQuantity) {
			continue
		}
		note := ""
		if i < len(notes) {
			note = strings.TrimSpace(notes[i])
		}
		changed, err := s.store.EnsureShopping(r.Context(), id, q, note)
		var invalid stock.Errors
		if errors.Is(err, store.ErrNotFound) || errors.As(err, &invalid) {
			continue // deleted or archived in the meantime, or a forged amount
		}
		if err != nil {
			return err
		}
		if changed {
			n++
		}
	}
	msg := "Added " + plural(n, "item") + " to the shopping list"
	if n == 0 {
		msg = "Everything suggested is already on the shopping list"
	}
	// The service form's short parts button posts with htmx: the form stays,
	// with what was typed, and the message comes as a toast.
	if isHTMX(r) {
		toast(w, "success", msg)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	s.redirect(w, r, shoppingListPath(r), msg)
	return nil
}

type boughtPage struct {
	List      string // the list it came from, for Cancel
	V         int64  // that list's vehicle (v), which its forms carry back
	Entry     stock.ShoppingEntry
	Move      movementForm
	Units     []stock.SubUnit
	Matches   []stock.Item // items already in stock with a free-text entry's name
	Suppliers []string
}

func (s *Server) shoppingBoughtForm(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	e, err := s.store.GetShopping(r.Context(), id)
	if err != nil {
		return err
	}
	f := movementForm{OccurredOn: s.now().Format("2006-01-02"), Version: e.Version}
	if e.Quantity != nil {
		f.Amount, f.AmountUnit = stock.SplitForInput(*e.Quantity, e.ItemUnit)
	} else {
		_, f.AmountUnit = stock.SplitForInput(0, e.ItemUnit)
	}
	var matches []stock.Item
	if e.ItemID == nil {
		if matches, err = s.store.ItemsNamed(r.Context(), e.FreeText); err != nil {
			return err
		}
	}
	suppliers, err := s.store.Suppliers(r.Context())
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "shopping_bought", View{Title: "Bought " + e.Title(), Nav: "shopping",
		Data: boughtPage{List: shoppingListPath(r), V: listVehicle(r), Entry: e, Move: f, Units: stock.SubUnitsFor(e.ItemUnit), Matches: matches, Suppliers: suppliers}})
	return nil
}

// shoppingLink records that a free-text entry is an item already in stock,
// then opens the usual Bought form for it.
func (s *Server) shoppingLink(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	itemID := parseOptID(formText(r, "item_id"))
	var it stock.Item
	if itemID == nil {
		err = store.ErrNotFound
	} else {
		it, err = s.store.GetItem(r.Context(), *itemID)
	}
	if errors.Is(err, store.ErrNotFound) {
		// Deleted in another tab since the page offered it: back to the
		// entry's page, which offers what is left (or to the list, when
		// the entry is gone too).
		to := boughtPath(r, id)
		if _, err := s.store.GetShopping(r.Context(), id); errors.Is(err, store.ErrNotFound) {
			to = shoppingListPath(r)
		}
		s.setFlash(w, "error", "That item was deleted meanwhile, so nothing was linked. Choose another, or add it as a new item.")
		s.redirect(w, r, to, "")
		return nil
	} else if err != nil {
		return err
	}
	entry, dropped, err := s.store.LinkShopping(r.Context(), id, parseVersion(r), *itemID)
	if errors.Is(err, store.ErrNotFound) || errors.As(err, new(*store.StaleError)) {
		// Linked already (a second click, which also finds a new version)
		// or ticked off meanwhile.
		if e, err := s.store.GetShopping(r.Context(), id); err == nil && e.ItemID != nil {
			s.redirect(w, r, boughtPath(r, id), "")
			return nil
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		s.setFlash(w, "error", "That entry is no longer on the shopping list")
		s.redirect(w, r, shoppingListPath(r), "")
		return nil
	}
	if s.staleButton(w, r, err, boughtPath(r, id), shoppingListPath(r)) {
		return nil
	} else if err != nil {
		return err
	}
	var notes []string
	if it.Archived() {
		notes = append(notes, "Restored "+it.DisplayName()+".")
	}
	if dropped {
		notes = append(notes, "The listed amount was in another unit than "+it.DisplayName()+"'s, so enter what you bought.")
	}
	msg := strings.Join(notes, " ")
	s.redirect(w, r, boughtPath(r, entry), msg)
	return nil
}

func (s *Server) shoppingBought(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	// Before the entry is looked at: the first save ticked it off.
	if done, err := s.savedBefore(w, r, parseFormKey(r), "", nil, shoppingListPath(r)); done || err != nil {
		return err
	}
	e, err := s.store.GetShopping(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.shoppingGone(w, r)
		return nil
	} else if err != nil {
		return err
	}
	page := boughtPath(r, id)
	if e.ItemID != nil && !r.PostForm.Has("amount") && parseVersion(r) != e.Version {
		// "Just tick it off" from a page where the entry was still free
		// text: it has been linked to an item since.
		s.staleButton(w, r, &store.StaleError{What: store.WhatShopping}, page, shoppingListPath(r))
		return nil
	}
	if e.ItemID == nil {
		_, trashID, err := s.store.MarkBought(r.Context(), id, parseVersion(r), nil)
		if errors.Is(err, store.ErrNotFound) {
			s.shoppingGone(w, r)
			return nil
		} else if s.staleButton(w, r, err, page, shoppingListPath(r)) {
			return nil
		} else if err != nil {
			return err
		}
		s.redirectUndo(w, r, shoppingListPath(r), "Ticked off "+e.Title(), trashID)
		return nil
	}
	f := movementForm{
		Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"), TotalPrice: formText(r, "total_price"),
		Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"), Note: formText(r, "note"),
		ExpiresOn: formText(r, "expires_on"), Version: parseVersion(r), FormKey: parseFormKey(r), Errors: stock.Errors{},
	}
	checkFormKey(f.Errors, f.FormKey)
	in := stock.MovementInput{Kind: stock.MoveBought, Supplier: f.Supplier, Note: f.Note, FormKey: f.FormKey}
	if q, err := stock.ParseAmount(f.Amount, e.ItemUnit, f.AmountUnit); err != nil || q <= 0 {
		f.Errors.Add("amount", amountError(err, e.ItemUnit, "Enter how much you bought"))
	} else {
		in.Delta = q
	}
	if p, err := parseOptMoney(f.TotalPrice); err != nil {
		f.Errors.Add("total_price", err.Error())
	} else {
		in.TotalPrice = p
	}
	if d, err := parseEventDate(f.OccurredOn, s.now()); err != nil {
		f.Errors.Add("occurred_on", err.Error())
	} else if d != nil {
		in.OccurredOn = d
	} else {
		in.OccurredOn = s.today()
	}
	if d, err := parseOptDate(f.ExpiresOn); err != nil {
		f.Errors.Add("expires_on", err.Error())
	} else {
		in.ExpiresOn = d
	}
	if !f.Errors.Any() {
		if left, trashID, err := s.store.MarkBought(r.Context(), id, f.Version, &in); err == nil {
			msg := "Bought " + stock.FormatQuantity(in.Delta, e.ItemUnit) + " of " + e.Title()
			if left > 0 {
				msg += ". " + stock.FormatQuantity(left, e.ItemUnit) + " still on the list."
			}
			s.redirectUndo(w, r, shoppingListPath(r), msg, trashID)
			return nil
		} else if s.savedAlready(w, r, err, nil, shoppingListPath(r)) {
			return nil
		} else if errors.Is(err, store.ErrNotFound) {
			s.shoppingGone(w, r)
			return nil
		} else if f.Stale = staleForm(err, page); f.Stale == nil && !formConflict(f.Errors, err) {
			return err
		}
	}
	suppliers, err := s.store.Suppliers(r.Context())
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusUnprocessableEntity, "shopping_bought", View{Title: "Bought " + e.Title(), Nav: "shopping",
		Data: boughtPage{List: shoppingListPath(r), V: listVehicle(r), Entry: e, Move: f, Units: stock.SubUnitsFor(e.ItemUnit), Suppliers: suppliers}})
	return nil
}

func (s *Server) shoppingDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	e, err := s.store.GetShopping(r.Context(), id) // for the message
	if errors.Is(err, store.ErrNotFound) {
		s.shoppingGone(w, r)
		return nil
	} else if err != nil {
		return err
	}
	trashID, err := s.store.DeleteShopping(r.Context(), id, parseVersion(r))
	if errors.Is(err, store.ErrNotFound) {
		s.shoppingGone(w, r)
		return nil
	} else if s.staleButton(w, r, err, shoppingListPath(r), shoppingListPath(r)) {
		return nil
	} else if err != nil {
		return err
	}
	s.redirectUndo(w, r, shoppingListPath(r), "Removed "+e.Title()+" from the shopping list", trashID)
	return nil
}

// shoppingGone answers a post about an entry that was ticked off or
// removed meanwhile, say in another tab, with a message instead of a 404.
func (s *Server) shoppingGone(w http.ResponseWriter, r *http.Request) {
	s.setFlash(w, "error", "That entry is no longer on the shopping list")
	s.redirect(w, r, shoppingListPath(r), "")
}

// shoppingExportText is the list as text: the part its address asks for
// (?v=), whatever vehicle is worked on, as exports follow their address
// only. The whole list says whose each entry is.
func (s *Server) shoppingExportText(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vehicles, err := s.store.ListVehicles(ctx)
	if err != nil {
		return err
	}
	entries, err := s.store.ListShopping(ctx)
	if err != nil {
		return err
	}
	// Its address only: shoppingVehicle without the vehicle worked on.
	vid := vehicleParam(r.URL.Query())
	if !slices.ContainsFunc(vehicles, func(v stock.Vehicle) bool { return v.ID == vid }) {
		vid = 0
	}
	entries, _ = stock.ShoppingFor(entries, vid)
	var names map[int64]string
	if vid == 0 {
		names = map[int64]string{}
		for _, v := range chipVehicles(vehicles) {
			names[v.ID] = v.Name
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write([]byte(stock.ExportShoppingText(entries, names))) //nolint:gosec // text/plain; the address only picks the entries

	return err
}
