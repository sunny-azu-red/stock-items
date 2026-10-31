package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Photo is a stored item image.
type Photo struct {
	ContentType string
	Data        []byte
	UpdatedAt   time.Time
}

// SetPhoto stores the photo of an item. A photo it replaces goes to the
// trash; replacedTrashID is that entry (0 when there was none).
func (s *Store) SetPhoto(ctx context.Context, itemID int64, contentType string, image, thumb []byte) (replacedTrashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if replacedTrashID, err = trashPhoto(ctx, tx, itemID, "replaced"); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO item_photos (item_id, content_type, image, thumb) VALUES ($1, $2, $3, $4)`,
			itemID, contentType, image, thumb)
		return err
	})
	return replacedTrashID, err
}

// GetPhoto returns the full image, or the thumbnail when thumb is true.
func (s *Store) GetPhoto(ctx context.Context, itemID int64, thumb bool) (Photo, error) {
	col := "image"
	if thumb {
		col = "thumb"
	}
	var p Photo
	err := s.pool.QueryRow(ctx, `SELECT content_type, `+col+`, updated_at FROM item_photos WHERE item_id = $1`, itemID).
		Scan(&p.ContentType, &p.Data, &p.UpdatedAt)
	return p, notFound(err)
}

// DeletePhoto removes an item's photo, keeping it in the trash.
func (s *Store) DeletePhoto(ctx context.Context, itemID int64) (trashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		trashID, err = trashPhoto(ctx, tx, itemID, "")
		if err == nil && trashID == 0 {
			return ErrNotFound
		}
		return err
	})
	return trashID, err
}

// trashPhoto moves an item's photo to the trash, returning 0 when it has
// none (ErrNotFound when the item is gone). A detail ("replaced") goes in
// the label, telling it apart from the photo that took its place. It
// locks the item first, as deleting the item does before cascading to its
// photo, so the two can't deadlock, and two uploads at once take turns.
func trashPhoto(ctx context.Context, tx pgx.Tx, itemID int64, detail string) (int64, error) {
	var name, brand string
	if err := tx.QueryRow(ctx, `SELECT name, brand FROM items WHERE id = $1 FOR NO KEY UPDATE`, itemID).Scan(&name, &brand); err != nil {
		return 0, notFound(err)
	}
	var has bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM item_photos WHERE item_id = $1)`, itemID).Scan(&has); err != nil || !has {
		return 0, err
	}
	p, err := snapshot(ctx, tx, itemID, [2]string{"item_photos", "item_id"})
	if err != nil {
		return 0, err
	}
	trashID, err := putTrash(ctx, tx, TrashPhoto, trashLabel("Photo", displayName(brand, name), detail), p)
	if err != nil {
		return 0, err
	}
	return trashID, checkAffected(tx.Exec(ctx, `DELETE FROM item_photos WHERE item_id = $1`, itemID))
}
