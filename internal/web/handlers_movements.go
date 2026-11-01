package web

import (
	"cmp"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// movementEditForm holds the edit form for a recorded stock change.
type movementEditForm struct {
	Amount, AmountUnit, TotalPrice, Supplier, OccurredOn, Note string
	Removed                                                    bool   // a count that took stock away
	Back                                                       string // page to return to
	Version                                                    int    // as the form was opened (see itemForm)
	Stale                                                      *staleNote
	FormKey                                                    string // see itemForm
	Errors                                                     stock.Errors
}

type movementPage struct {
	Movement stock.Movement
	Name     string // "purchase", "use", …
	Undo     string // how deleting it changes the stock, e.g. "−1 L"
	Form     movementEditForm
	Back     template.URL // the page that opened the form, already checked
	Units    []stock.SubUnit
	Gone     bool // deleted since the form was opened: Movement is what the form said
}

// errItemArchived means the stock change belongs to an archived item, whose
// stock can't change until it is restored.
var errItemArchived = errors.New("item is archived")

// errMovementGone means the stock change no longer exists. It matches
// store.ErrNotFound, so pages that show one answer 404.
var errMovementGone = fmt.Errorf("stock change is gone: %w", store.ErrNotFound)

// loadMovement returns an editable stock change; parts used by a service
// are not edited here, nor are an archived item's changes.
func (s *Server) loadMovement(r *http.Request) (stock.Movement, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return stock.Movement{}, err
	}
	m, err := s.store.GetMovement(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return stock.Movement{ID: id}, errMovementGone
	}
	if err != nil {
		return m, err
	}
	if !m.Editable() {
		return m, store.ErrNotFound
	}
	it, err := s.store.GetItem(r.Context(), m.ItemID)
	if err != nil {
		return m, err
	}
	if it.Archived() {
		return m, errItemArchived
	}
	return m, nil
}

// movementRefused sends an archived item's stock change back to the item
// page with a message; it reports whether it did.
func (s *Server) movementRefused(w http.ResponseWriter, r *http.Request, m stock.Movement, err error) bool {
	if !errors.Is(err, errItemArchived) {
		return false
	}
	s.setFlash(w, "error", "Restore "+m.ItemName+" to change its stock history")
	s.redirect(w, r, itemPath(m.ItemID), "")
	return true
}

func (s *Server) renderMovementForm(w http.ResponseWriter, r *http.Request, status int, m stock.Movement, f movementEditForm) {
	s.render(w, r, status, "movement_form", View{Title: "Edit " + changeName(m.Kind), Nav: "stock",
		Data: movementPage{Movement: m, Name: changeName(m.Kind), Undo: stock.FormatDelta(-m.Delta, m.ItemUnit),
			Form: f, Back: template.URL(f.Back), Units: stock.SubUnitsFor(m.ItemUnit)}})
}

// goneMovementForm shows the edit form of a stock change deleted since it
// was opened, as it was posted, saying where the change went. The page no
// longer knows the change, so it goes by what the form said it was.
func (s *Server) goneMovementForm(w http.ResponseWriter, r *http.Request, id int64) error {
	m := stock.Movement{ID: id, Kind: stock.MovementKind(formText(r, "kind"))}
	if id := parseOptID(formText(r, "item_id")); id != nil {
		m.ItemID = *id
	}
	it, err := s.store.GetItem(r.Context(), m.ItemID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	m.ItemName, m.ItemUnit = cmp.Or(it.DisplayName(), "the item"), it.Unit
	gone := s.store.Gone(r.Context(), store.WhatMovement, id)
	f := movementEditForm{
		Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"), TotalPrice: formText(r, "total_price"),
		Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"), Note: formText(r, "note"),
		Removed: formText(r, "direction") == "removed", Back: postedBack(r, movementBack(m)),
		Version: parseVersion(r), FormKey: parseFormKey(r), Errors: stock.Errors{}, Stale: staleForm(gone, ""),
	}
	if f.Stale == nil {
		return gone
	}
	units := stock.SubUnitsFor(m.ItemUnit)
	if !m.ItemUnit.Valid() {
		units = stock.SubUnits
	}
	s.render(w, r, http.StatusUnprocessableEntity, "movement_form", View{Title: "Edit " + changeName(m.Kind), Nav: "stock",
		Data: movementPage{Movement: m, Name: changeName(m.Kind), Form: f, Back: template.URL(f.Back), Units: units, Gone: true}})
	return nil
}

func (s *Server) movementEdit(w http.ResponseWriter, r *http.Request) error {
	m, err := s.loadMovement(r)
	if s.movementRefused(w, r, m, err) {
		return nil
	}
	if err != nil {
		return err
	}
	f := movementEditForm{Supplier: m.Supplier, Note: m.Note, OccurredOn: m.OccurredOn.Format("2006-01-02"),
		Removed: m.Delta < 0, Back: s.back(r, itemPath(m.ItemID)), Version: m.Version}
	f.Amount, f.AmountUnit = stock.SplitForInput(math.Abs(m.Delta), m.ItemUnit)
	if m.TotalPrice != nil {
		f.TotalPrice = stock.FormatMoney(*m.TotalPrice)
	}
	s.renderMovementForm(w, r, http.StatusOK, m, f)
	return nil
}

func (s *Server) movementUpdate(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	// Saved already comes first: the first save moved the version on. A
	// repeat returns where the form does, as the first save did.
	posted := stock.Movement{}
	if id := parseOptID(formText(r, "item_id")); id != nil {
		posted.ItemID = *id
	}
	back := postedBack(r, movementBack(posted))
	page := func(int64) string { return back }
	if done, err := s.savedBefore(w, r, parseFormKey(r), store.WhatMovement, page, back); done || err != nil {
		return err
	}
	m, err := s.loadMovement(r)
	if s.movementRefused(w, r, m, err) {
		return nil
	}
	if errors.Is(err, errMovementGone) {
		// Deleted meanwhile: keep what was typed, and say where it went.
		return s.goneMovementForm(w, r, m.ID)
	}
	if err != nil {
		return err
	}
	f := movementEditForm{
		Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"), TotalPrice: formText(r, "total_price"),
		Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"), Note: formText(r, "note"),
		Removed: formText(r, "direction") == "removed", Back: postedBack(r, itemPath(m.ItemID)),
		Version: parseVersion(r), FormKey: parseFormKey(r), Errors: stock.Errors{},
	}
	checkFormKey(f.Errors, f.FormKey)
	e := stock.MovementEdit{Supplier: f.Supplier, Note: f.Note, Removed: f.Removed, Version: f.Version, FormKey: f.FormKey}
	if q, err := stock.ParseAmount(f.Amount, m.ItemUnit, f.AmountUnit); err != nil {
		f.Errors.Add("amount", amountError(err, m.ItemUnit, "Enter an amount"))
	} else {
		e.Amount = q
	}
	if p, err := parseOptMoney(f.TotalPrice); err != nil {
		f.Errors.Add("total_price", err.Error())
	} else {
		e.TotalPrice = p
	}
	if d, err := parseEventDate(f.OccurredOn, s.now()); err != nil {
		f.Errors.Add("occurred_on", err.Error())
	} else {
		e.OccurredOn = d
	}
	if !f.Errors.Any() {
		err := s.store.UpdateMovement(r.Context(), m.ID, e)
		if err == nil {
			s.redirect(w, r, f.Back, "Updated the "+changeName(m.Kind))
			return nil
		}
		if s.savedAlready(w, r, err, func(int64) string { return f.Back }, f.Back) {
			return nil
		}
		if f.Stale = staleForm(err, fmt.Sprintf("/movements/%d/edit", m.ID)); f.Stale == nil && !movementConflict(f.Errors, err) {
			return err
		}
	}
	s.renderMovementForm(w, r, http.StatusUnprocessableEntity, m, f)
	return nil
}

func (s *Server) movementDelete(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	m, err := s.loadMovement(r)
	if s.movementRefused(w, r, m, err) {
		return nil
	}
	back := postedBack(r, movementBack(m))
	if errors.Is(err, errMovementGone) {
		err = s.store.Gone(r.Context(), store.WhatMovement, m.ID)
	}
	if s.staleButton(w, r, err, fmt.Sprintf("/movements/%d/edit", m.ID), back) {
		return nil
	} else if err != nil {
		return err
	}
	deleted, trashID, err := s.store.DeleteMovement(r.Context(), m.ID, parseVersion(r))
	if s.staleButton(w, r, err, fmt.Sprintf("/movements/%d/edit", m.ID), back) {
		return nil
	}
	if err == nil {
		it, err := s.store.GetItem(r.Context(), deleted.ItemID)
		if err != nil {
			return err
		}
		s.redirectUndo(w, r, back,
			"Deleted the "+changeName(m.Kind)+". Stock is now "+stock.FormatQuantity(it.Quantity, it.Unit)+".", trashID)
		return nil
	}
	f := movementEditForm{Supplier: m.Supplier, Note: m.Note, OccurredOn: m.OccurredOn.Format("2006-01-02"),
		Removed: m.Delta < 0, Back: back, Version: m.Version, Errors: stock.Errors{}}
	f.Amount, f.AmountUnit = stock.SplitForInput(math.Abs(m.Delta), m.ItemUnit)
	if m.TotalPrice != nil {
		f.TotalPrice = stock.FormatMoney(*m.TotalPrice)
	}
	if !movementConflict(f.Errors, err) {
		return err
	}
	s.renderMovementForm(w, r, http.StatusUnprocessableEntity, m, f)
	return nil
}

func itemPath(id int64) string { return fmt.Sprintf("/items/%d", id) }

// movementBack is where a stock change's form returns by default: its
// item, or the activity log for one that is gone.
func movementBack(m stock.Movement) string {
	if m.ItemID == 0 {
		return "/activity"
	}
	return itemPath(m.ItemID)
}

// changeName names a kind of stock change in a sentence.
func changeName(k stock.MovementKind) string {
	switch k {
	case stock.MovePurchase:
		return "purchase"
	case stock.MoveUse:
		return "use"
	case stock.MoveCorrection:
		return "count"
	case stock.MoveInitial:
		return "initial stock"
	}
	return "stock change"
}

// movementConflict maps store errors of a revised stock change onto the
// form; running short means the stock was used in the meantime.
func movementConflict(errs stock.Errors, err error) bool {
	var short *store.InsufficientStockError
	if errors.As(err, &short) {
		errs.Add("amount", "Only "+stock.FormatQuantity(short.Have, short.Unit)+
			" is left on stock, so this change can't take away "+stock.FormatQuantity(short.Need, short.Unit)+
			". Record what was used first, or do a count.")
		return true
	}
	return formConflict(errs, err)
}
