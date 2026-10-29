package store

import (
	"context"
	"time"
)

// Photo is a stored item image.
type Photo struct {
	ContentType string
	Data        []byte
	UpdatedAt   time.Time
}

// SetPhoto stores (or replaces) the photo of an item.
func (s *Store) SetPhoto(ctx context.Context, itemID int64, contentType string, image, thumb []byte) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO item_photos (item_id, content_type, image, thumb) VALUES ($1, $2, $3, $4)
		ON CONFLICT (item_id) DO UPDATE SET content_type = $2, image = $3, thumb = $4, updated_at = now()`,
		itemID, contentType, image, thumb)
	if isForeignKeyViolation(err) {
		return ErrNotFound
	}
	return err
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

// DeletePhoto removes an item's photo.
func (s *Store) DeletePhoto(ctx context.Context, itemID int64) error {
	return checkAffected(s.pool.Exec(ctx, `DELETE FROM item_photos WHERE item_id = $1`, itemID))
}
