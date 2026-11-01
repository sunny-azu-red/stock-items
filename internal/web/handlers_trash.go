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
		return fmt.Sprintf("/services/%d", res.ID)
	case store.TrashKit:
		return fmt.Sprintf("/kits/%d", res.ID)
	case store.TrashVehicle:
		return fmt.Sprintf("/vehicles/%d", res.ID)
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
				" is on stock, and it would take "+stock.FormatQuantity(short.Need, short.Unit)+".")
		default:
			return err
		}
		s.redirect(w, r, "/trash", "")
		return nil
	}
	msg := "Restored " + strings.TrimSuffix(res.Label, ".") + "."
	switch res.Kind {
	case store.TrashBought:
		msg = "Took back buying " + strings.TrimPrefix(res.Label, "Bought: ") + ". It's back on the shopping list."
	case store.TrashPhoto:
		// Not "Restored Photo: X (replaced)": the label says why it went to
		// the trash, which is no longer news.
		msg = "Restored the photo of " + res.Name + "."
	}
	if res.Dropped > 0 {
		msg += fmt.Sprintf(" Left out %d %s to things permanently deleted.", res.Dropped, pluralWord(res.Dropped, "link", "links"))
	}
	if res.Replaced != 0 {
		// The photo it took the place of went to the trash in turn.
		s.redirectUndo(w, r, restoredPath(res), msg+" The newer photo is in the trash.", res.Replaced)
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
	others, err := s.store.PurgeTrash(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.setFlash(w, "error", "That was restored or permanently deleted already")
		s.redirect(w, r, "/trash", "")
		return nil
	} else if err != nil {
		return err
	}
	msg := "Permanently deleted"
	if others > 0 {
		msg += fmt.Sprintf(", with %d more %s that needed it", others, pluralWord(others, "entry", "entries"))
	}
	s.redirect(w, r, "/trash", msg)
	return nil
}

func (s *Server) trashEmpty(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.EmptyTrash(r.Context()); err != nil {
		return err
	}
	s.redirect(w, r, "/trash", "Emptied. Nothing can be restored now.")
	return nil
}
