package store

import (
	"context"
	"errors"
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
		if replacedTrashID, err = trashPhoto(ctx, tx, itemID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO item_photos (item_id, content_type, image, thumb) VALUES ($1, $2, $3, $4)`,
			itemID, contentType, image, thumb)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
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
		trashID, err = trashPhoto(ctx, tx, itemID)
		return err
	})
	return trashID, err
}

// trashPhoto moves an item's photo to the trash (ErrNotFound without one).
func trashPhoto(ctx context.Context, tx pgx.Tx, itemID int64) (int64, error) {
	var name, brand string
	err := tx.QueryRow(ctx, `
		SELECT i.name, i.brand FROM item_photos p JOIN items i ON i.id = p.item_id WHERE p.item_id = $1 FOR UPDATE OF p`, itemID).
		Scan(&name, &brand)
	if err != nil {
		return 0, notFound(err)
	}
	p, err := snapshot(ctx, tx, itemID, [2]string{"item_photos", "item_id"})
	if err != nil {
		return 0, err
	}
	trashID, err := putTrash(ctx, tx, TrashPhoto, trashLabel("Photo", displayName(brand, name)), p)
	if err != nil {
		return 0, err
	}
	return trashID, checkAffected(tx.Exec(ctx, `DELETE FROM item_photos WHERE item_id = $1`, itemID))
}
