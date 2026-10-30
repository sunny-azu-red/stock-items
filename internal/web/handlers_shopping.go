package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

type shoppingForm struct {
	ItemID                         int64
	FreeText, Quantity, Note, Unit string
	Errors                         stock.Errors
}

type shoppingPage struct {
	Entries     []stock.ShoppingEntry
	Suggestions []stock.Suggestion
	Items       []stock.Item
	Form        shoppingForm
}

func (s *Server) loadShopping(r *http.Request, f shoppingForm) (shoppingPage, error) {
	ctx := r.Context()
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
	onList := map[int64]bool{}
	for _, e := range entries {
		if e.ItemID != nil {
			onList[*e.ItemID] = true
		}
	}
	return shoppingPage{
		Entries: entries, Items: items, Form: f, Suggestions: stock.SuggestPurchases(items, kits, onList),
	}, nil
}

func (s *Server) shoppingList(w http.ResponseWriter, r *http.Request) error {
	p, err := s.loadShopping(r, shoppingForm{})
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "shopping", View{Title: "Shopping list", Nav: "shopping", Data: p})
	return nil
}

func (s *Server) shoppingAdd(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	f := shoppingForm{FreeText: formText(r, "free_text"), Quantity: formText(r, "quantity"), Note: formText(r, "note"), Errors: stock.Errors{}}
	in := stock.ShoppingInput{FreeText: f.FreeText, Note: f.Note}
	unit := stock.UnitPiece
	if id := parseOptID(formText(r, "item_id")); id != nil {
		it, err := s.store.GetItem(r.Context(), *id)
		if err != nil {
			f.Errors.Add("item_id", "That item no longer exists")
		} else {
			f.ItemID, in.ItemID, unit = *id, id, it.Unit
			in.FreeText = ""
		}
	}
	if q, err := parseOptAmount(f.Quantity, unit, ""); err != nil {
		f.Errors.Add("quantity", err.Error())
	} else {
		in.Quantity = q
	}
	in.Normalize()
	for k, v := range in.Validate() {
		f.Errors.Add(k, v)
	}
	if !f.Errors.Any() {
		if _, err := s.store.AddShopping(r.Context(), in); err != nil {
			return err
		}
		s.redirect(w, r, "/shopping", "Added to the shopping list")
		return nil
	}
	p, err := s.loadShopping(r, f)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusUnprocessableEntity, "shopping", View{Title: "Shopping list", Nav: "shopping", Data: p})
	return nil
}

// shoppingAddSuggestions adds one or more suggestions (item_id + qty pairs,
// qty in base units).
func (s *Server) shoppingAddSuggestions(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	ids, qtys := r.PostForm["item_id"], r.PostForm["qty"]
	n := 0
	for i, raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || i >= len(qtys) {
			continue
		}
		q, err := strconv.ParseFloat(qtys[i], 64)
		q = math.Round(q*1000) / 1000 // the column keeps three decimals
		if err != nil || !(q > 0 && q <= stock.MaxQuantity) {
			continue
		}
		err = s.store.EnsureShopping(r.Context(), id, q, "")
		if errors.Is(err, store.ErrNotFound) {
			continue // deleted in the meantime
		}
		if err != nil {
			return err
		}
		n++
	}
	s.redirect(w, r, "/shopping", fmt.Sprintf("Added %d %s to the shopping list", n, pluralWord(n, "item", "items")))
	return nil
}

type boughtPage struct {
	Entry stock.ShoppingEntry
	Move  movementForm
	Units []stock.SubUnit
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
	f := movementForm{OccurredOn: s.now().Format("2006-01-02")}
	if e.Quantity != nil {
		f.Amount, f.AmountUnit = stock.SplitForInput(*e.Quantity, e.ItemUnit)
	} else {
		_, f.AmountUnit = stock.SplitForInput(0, e.ItemUnit)
	}
	s.render(w, r, http.StatusOK, "shopping_bought", View{Title: "Bought " + e.Title(), Nav: "shopping",
		Data: boughtPage{Entry: e, Move: f, Units: stock.SubUnitsFor(e.ItemUnit)}})
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
	e, err := s.store.GetShopping(r.Context(), id)
	if err != nil {
		return err
	}
	if e.ItemID == nil {
		if err := s.store.MarkBought(r.Context(), id, nil); err != nil {
			return err
		}
		s.redirect(w, r, "/shopping", "Ticked off "+e.Title())
		return nil
	}
	f := movementForm{
		Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"), TotalPrice: formText(r, "total_price"),
		Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"), Note: formText(r, "note"),
		Errors: stock.Errors{},
	}
	in := stock.MovementInput{Kind: stock.MovePurchase, Supplier: f.Supplier, Note: f.Note}
	if q, err := stock.ParseAmount(f.Amount, e.ItemUnit, f.AmountUnit); err != nil || q <= 0 {
		f.Errors.Add("amount", "Enter how much you bought, e.g. "+examplesFor(e.ItemUnit))
	} else {
		in.Delta = q
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
		if err := s.store.MarkBought(r.Context(), id, &in); err == nil {
			s.redirect(w, r, "/shopping", "Added "+stock.FormatQuantity(in.Delta, e.ItemUnit)+" of "+e.Title()+" to stock")
			return nil
		} else if !formConflict(f.Errors, err) {
			return err
		}
	}
	s.render(w, r, http.StatusUnprocessableEntity, "shopping_bought", View{Title: "Bought " + e.Title(), Nav: "shopping",
		Data: boughtPage{Entry: e, Move: f, Units: stock.SubUnitsFor(e.ItemUnit)}})
	return nil
}

func (s *Server) shoppingDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.store.DeleteShopping(r.Context(), id); err != nil {
		return err
	}
	s.redirect(w, r, "/shopping", "Removed from the shopping list")
	return nil
}

func (s *Server) shoppingExportText(w http.ResponseWriter, r *http.Request) error {
	entries, err := s.store.ListShopping(r.Context())
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write([]byte(stock.ExportShoppingText(entries)))
	return err
}
