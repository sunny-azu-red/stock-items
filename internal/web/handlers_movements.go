package web

import (
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
	Errors                                                     stock.Errors
}

type movementPage struct {
	Movement stock.Movement
	Name     string // "purchase", "use", …
	Undo     string // how deleting it changes the stock, e.g. "−1 L"
	Form     movementEditForm
	Back     template.URL // the page that opened the form, already checked
	Units    []stock.SubUnit
}

// loadMovement returns an editable stock change; parts used by a service
// are not edited here.
func (s *Server) loadMovement(r *http.Request) (stock.Movement, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return stock.Movement{}, err
	}
	m, err := s.store.GetMovement(r.Context(), id)
	if err != nil {
		return m, err
	}
	if !m.Editable() {
		return m, store.ErrNotFound
	}
	return m, nil
}

func (s *Server) renderMovementForm(w http.ResponseWriter, r *http.Request, status int, m stock.Movement, f movementEditForm) {
	s.render(w, r, status, "movement_form", View{Title: "Edit " + changeName(m.Kind), Nav: "stock",
		Data: movementPage{Movement: m, Name: changeName(m.Kind), Undo: stock.FormatDelta(-m.Delta, m.ItemUnit),
			Form: f, Back: template.URL(f.Back), Units: stock.SubUnitsFor(m.ItemUnit)}})
}

func (s *Server) movementEdit(w http.ResponseWriter, r *http.Request) error {
	m, err := s.loadMovement(r)
	if err != nil {
		return err
	}
	f := movementEditForm{Supplier: m.Supplier, Note: m.Note, OccurredOn: m.OccurredOn.Format("2006-01-02"),
		Removed: m.Delta < 0, Back: s.back(r, itemPath(m.ItemID))}
	f.Amount, f.AmountUnit = stock.SplitForInput(math.Abs(m.Delta), m.ItemUnit)
	if m.TotalPrice != nil {
		f.TotalPrice = stock.FormatMoney(*m.TotalPrice)
	}
	s.renderMovementForm(w, r, http.StatusOK, m, f)
	return nil
}

func (s *Server) movementUpdate(w http.ResponseWriter, r *http.Request) error {
	m, err := s.loadMovement(r)
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	f := movementEditForm{
		Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"), TotalPrice: formText(r, "total_price"),
		Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"), Note: formText(r, "note"),
		Removed: formText(r, "direction") == "removed", Back: postedBack(r, itemPath(m.ItemID)), Errors: stock.Errors{},
	}
	e := stock.MovementEdit{Supplier: f.Supplier, Note: f.Note, Removed: f.Removed}
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
		if !movementConflict(f.Errors, err) {
			return err
		}
	}
	s.renderMovementForm(w, r, http.StatusUnprocessableEntity, m, f)
	return nil
}

func (s *Server) movementDelete(w http.ResponseWriter, r *http.Request) error {
	m, err := s.loadMovement(r)
	if err != nil {
		return err
	}
	if err := parseForm(w, r); err != nil {
		return err
	}
	back := postedBack(r, itemPath(m.ItemID))
	deleted, err := s.store.DeleteMovement(r.Context(), m.ID)
	if err == nil {
		it, err := s.store.GetItem(r.Context(), deleted.ItemID)
		if err != nil {
			return err
		}
		s.redirect(w, r, back,
			"Deleted the "+changeName(m.Kind)+". Stock is now "+stock.FormatQuantity(it.Quantity, it.Unit)+".")
		return nil
	}
	f := movementEditForm{Supplier: m.Supplier, Note: m.Note, OccurredOn: m.OccurredOn.Format("2006-01-02"),
		Removed: m.Delta < 0, Back: back, Errors: stock.Errors{}}
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
