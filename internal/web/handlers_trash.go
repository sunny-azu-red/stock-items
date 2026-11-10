package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// trashPage lists what was deleted and can be restored.
func (s *Server) trashList(w http.ResponseWriter, r *http.Request) error {
	entries, err := s.store.ListTrash(r.Context())
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "trash", View{Title: "Trash", Nav: "trash", Data: entries})
	return nil
}

// trashThumb serves the thumbnail of a photo in the trash. A trash entry
// never changes (restoring or purging removes it, and ids aren't reused),
// so the browser may keep it for good.
func (s *Server) trashThumb(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.store.TrashThumb(r.Context(), id)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, err = w.Write(p.Data)
	return err
}

// restoredPath is the page that shows what a restore brought back.
func restoredPath(res store.Restored) string {
	switch res.Kind {
	case store.TrashService:
		return servicePath(res.ID)
	case store.TrashKit:
		return kitPath(res.ID)
	case store.TrashVehicle:
		return vehiclePath(res.ID)
	case store.TrashShopping, store.TrashBought:
		return "/shopping"
	}
	return itemPath(res.ID) // item, movement, photo
}

func (s *Server) trashRestore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := s.store.RestoreTrash(r.Context(), id)
	if err != nil {
		var conflict *store.ConflictError
		var short *store.InsufficientStockError
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.setFlash(w, "error", "That was restored or permanently deleted already")
		case errors.As(err, &conflict):
			s.setFlash(w, "error", "Can't restore: "+conflict.Message)
		case errors.As(err, &short):
			s.setFlash(w, "error", "Can't restore: only "+stock.FormatQuantity(short.Have, short.Unit)+" of "+short.ItemName+
				" in stock, and it needs "+stock.FormatQuantity(short.Need, short.Unit))
		default:
			return err
		}
		s.redirect(w, r, s.back(r, "/trash"), "")
		return nil
	}
	// The record's name, not its trash label ("Item: Brake Pads"); a stock
	// change keeps its label, which says what kind it was.
	_, name, _ := strings.Cut(res.Label, ": ")
	msg := "Restored " + name
	switch res.Kind {
	case store.TrashMovement:
		msg = "Restored " + res.Label
	case store.TrashShopping:
		msg = "Restored " + name + " to the shopping list"
	case store.TrashBought:
		msg = "Undid buying " + name + ". It's back on the shopping list."
	case store.TrashPhoto:
		// Not "Restored Photo: X (replaced)": the label says why it went to
		// the trash, which is no longer news.
		msg = "Restored the photo of " + res.Name
	}
	if res.Dropped > 0 {
		msg = strings.TrimSuffix(msg, ".") + ". Left out " + plural(res.Dropped, "link") + " to what was permanently deleted."
	}
	if res.Replaced != 0 {
		// The photo it took the place of went to the trash in turn.
		s.redirectUndo(w, r, restoredPath(res), strings.TrimSuffix(msg, ".")+". The newer photo is in the trash.", res.Replaced)
		return nil
	}
	s.redirect(w, r, restoredPath(res), msg)
	return nil
}

func (s *Server) trashPurge(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	label, err := s.store.TrashLabel(r.Context(), id) // for the message
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	_, name, _ := strings.Cut(label, ": ")
	others, err := s.store.PurgeTrash(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.setFlash(w, "error", "That was restored or permanently deleted already")
		s.redirect(w, r, s.back(r, "/trash"), "")
		return nil
	} else if err != nil {
		return err
	}
	msg := "Permanently deleted " + name
	if others > 0 {
		msg += fmt.Sprintf(", and %d more %s in the trash that needed it", others, pluralOnly(others, "thing"))
	}
	s.redirect(w, r, "/trash", msg)
	return nil
}

func (s *Server) trashEmpty(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.EmptyTrash(r.Context()); err != nil {
		return err
	}
	s.redirect(w, r, "/trash", "Emptied the trash")
	return nil
}
