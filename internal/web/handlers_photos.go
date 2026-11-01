package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/store"
)

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
	// Before the body is read: a refused upload needn't wait for 15 MB to
	// arrive, nor hold them.
	it, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		return s.gone(r, err, store.WhatItem, id)
	}
	if it.Archived() {
		s.setFlash(w, "error", "Restore "+it.DisplayName()+" to change its photo")
		http.Redirect(w, r, itemPath(id), http.StatusSeeOther)
		return nil
	}
	done, ok := photoPlace()
	if !ok {
		s.setFlash(w, "error", "Other photos are being processed right now. Try again in a minute.")
		http.Redirect(w, r, itemPath(id), http.StatusSeeOther)
		return nil
	}
	defer done()
	// A 15 MB photo over a slow phone connection can take minutes. Both
	// deadlines count from the request headers, so extend both.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(photoUploadTimeout))
	_ = rc.SetWriteDeadline(time.Now().Add(photoUploadTimeout + 30*time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil { //nolint:gosec // MaxBytesReader above caps the body
		msg := "Choose a photo to upload"
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			msg = photoTooLarge
		}
		s.setFlash(w, "error", msg)
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther) //nolint:gosec // a local path
		return nil
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		s.setFlash(w, "error", "Choose a photo to upload")
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther) //nolint:gosec // a local path
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
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther) //nolint:gosec // a local path
		return nil
	}
	replaced, err := s.store.SetPhoto(r.Context(), id, "image/jpeg", full, thumb)
	if err != nil {
		return s.gone(r, err, store.WhatItem, id) // deleted while the photo was processed
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
		// No photo: say so, unless the item is gone too (then where it went).
		if _, err := s.store.GetItem(r.Context(), id); err != nil {
			return s.gone(r, err, store.WhatItem, id)
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
