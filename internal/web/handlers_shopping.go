package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

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
	return shoppingPage{
		Entries: entries, Items: items, Form: f, Suggestions: stock.SuggestPurchases(items, kits, listedQuantities(entries)),
	}, nil
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
	f := shoppingForm{FreeText: formText(r, "free_text"), Quantity: formText(r, "quantity"), Unit: formText(r, "quantity_unit"),
		Note: formText(r, "note"), Errors: stock.Errors{}}
	in := stock.ShoppingInput{FreeText: f.FreeText, Note: f.Note}
	if id := parseOptID(formText(r, "item_id")); id != nil {
		it, err := s.store.GetItem(r.Context(), *id)
		if err != nil {
			f.Errors.Add("item_id", "That item no longer exists")
		} else {
			f.ItemID, in.ItemID, in.Unit = *id, id, it.Unit
			if in.FreeText != "" {
				// Both filled in: keep the text as a note rather than drop it.
				if in.Note == "" {
					in.Note = in.FreeText
				} else {
					in.Note = in.FreeText + "; " + in.Note
				}
			}
			in.FreeText = ""
		}
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
			s.redirect(w, r, "/shopping", "Added to the shopping list")
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		f.Errors.Add("item_id", "That item was archived or deleted meanwhile")
	}
	p, err := s.loadShopping(r, f)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusUnprocessableEntity, "shopping", View{Title: "Shopping list", Nav: "shopping", Data: p})
	return nil
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
		q, err := strconv.ParseFloat(qtys[i], 64)
		q = math.Round(q*1000) / 1000 // the column keeps three decimals
		if err != nil || !(q > 0 && q <= stock.MaxQuantity) {
			continue
		}
		note := ""
		if i < len(notes) {
			note = strings.TrimSpace(notes[i])
		}
		changed, err := s.store.EnsureShopping(r.Context(), id, q, note)
		if errors.Is(err, store.ErrNotFound) {
			continue // deleted or archived in the meantime
		}
		if err != nil {
			return err
		}
		if changed {
			n++
		}
	}
	msg := fmt.Sprintf("Added %d %s to the shopping list", n, pluralWord(n, "item", "items"))
	if n == 0 {
		msg = "Nothing to add: the shopping list already has it"
	}
	s.redirect(w, r, "/shopping", msg)
	return nil
}

type boughtPage struct {
	Entry   stock.ShoppingEntry
	Move    movementForm
	Units   []stock.SubUnit
	Matches []stock.Item // items already on stock with a free-text entry's name
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
	s.render(w, r, http.StatusOK, "shopping_bought", View{Title: "Bought " + e.Title(), Nav: "shopping",
		Data: boughtPage{Entry: e, Move: f, Units: stock.SubUnitsFor(e.ItemUnit), Matches: matches}})
	return nil
}

// shoppingLink records that a free-text entry is an item already on stock,
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
	if itemID == nil {
		return store.ErrNotFound
	}
	it, err := s.store.GetItem(r.Context(), *itemID)
	if err != nil {
		return err
	}
	entry, dropped, err := s.store.LinkShopping(r.Context(), id, parseVersion(r), *itemID)
	if errors.Is(err, store.ErrNotFound) || errors.As(err, new(*store.StaleError)) {
		// Linked already (a second click, which also finds a new version)
		// or ticked off meanwhile.
		if e, err := s.store.GetShopping(r.Context(), id); err == nil && e.ItemID != nil {
			s.redirect(w, r, fmt.Sprintf("/shopping/%d/bought", id), "")
			return nil
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		s.setFlash(w, "error", "That shopping entry is gone")
		s.redirect(w, r, "/shopping", "")
		return nil
	}
	if s.staleButton(w, r, err, fmt.Sprintf("/shopping/%d/bought", id), "/shopping") {
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
	s.redirect(w, r, fmt.Sprintf("/shopping/%d/bought", entry), msg)
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
	if errors.Is(err, store.ErrNotFound) {
		s.shoppingGone(w, r)
		return nil
	} else if err != nil {
		return err
	}
	page := fmt.Sprintf("/shopping/%d/bought", id)
	if e.ItemID != nil && !r.PostForm.Has("amount") && parseVersion(r) != e.Version {
		// "Just tick it off" from a page where the entry was still free
		// text: it has been linked to an item since.
		s.staleButton(w, r, &store.StaleError{What: store.WhatShopping}, page, "/shopping")
		return nil
	}
	if e.ItemID == nil {
		_, trashID, err := s.store.MarkBought(r.Context(), id, parseVersion(r), nil)
		if errors.Is(err, store.ErrNotFound) {
			s.shoppingGone(w, r)
			return nil
		} else if s.staleButton(w, r, err, page, "/shopping") {
			return nil
		} else if err != nil {
			return err
		}
		s.redirectUndo(w, r, "/shopping", "Ticked off "+e.Title(), trashID)
		return nil
	}
	f := movementForm{
		Amount: formText(r, "amount"), AmountUnit: formText(r, "amount_unit"), TotalPrice: formText(r, "total_price"),
		Supplier: formText(r, "supplier"), OccurredOn: formText(r, "occurred_on"), Note: formText(r, "note"),
		ExpiresOn: formText(r, "expires_on"), Version: parseVersion(r), Errors: stock.Errors{},
	}
	in := stock.MovementInput{Kind: stock.MovePurchase, Supplier: f.Supplier, Note: f.Note}
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
			msg := "Added " + stock.FormatQuantity(in.Delta, e.ItemUnit) + " of " + e.Title() + " to stock"
			if left > 0 {
				msg += ". " + stock.FormatQuantity(left, e.ItemUnit) + " still on the list."
			}
			s.redirectUndo(w, r, "/shopping", msg, trashID)
			return nil
		} else if errors.Is(err, store.ErrNotFound) {
			s.shoppingGone(w, r)
			return nil
		} else if f.Stale = staleForm(err, page); f.Stale == nil && !formConflict(f.Errors, err) {
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
	if err := parseForm(w, r); err != nil {
		return err
	}
	trashID, err := s.store.DeleteShopping(r.Context(), id, parseVersion(r))
	if errors.Is(err, store.ErrNotFound) {
		s.shoppingGone(w, r)
		return nil
	} else if s.staleButton(w, r, err, "/shopping", "/shopping") {
		return nil
	} else if err != nil {
		return err
	}
	s.redirectUndo(w, r, "/shopping", "Removed from the shopping list", trashID)
	return nil
}

// shoppingGone answers a post about an entry that was ticked off or
// removed meanwhile, say in another tab, with a message instead of a 404.
func (s *Server) shoppingGone(w http.ResponseWriter, r *http.Request) {
	s.setFlash(w, "error", "That entry is no longer on the list")
	s.redirect(w, r, "/shopping", "")
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
