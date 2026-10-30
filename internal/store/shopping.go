package store

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

const shoppingSelect = `
SELECT e.id, e.item_id, COALESCE(i.brand, ''), COALESCE(i.name, ''), COALESCE(i.unit, e.unit), e.free_text,
       e.quantity, e.note, e.created_at
FROM shopping_entries e
LEFT JOIN items i ON i.id = e.item_id`

func scanShopping(row pgx.Row) (stock.ShoppingEntry, error) {
	var e stock.ShoppingEntry
	var brand, name string
	err := row.Scan(&e.ID, &e.ItemID, &brand, &name, &e.ItemUnit, &e.FreeText, &e.Quantity, &e.Note, &e.CreatedAt)
	if name != "" {
		e.ItemName = displayName(brand, name)
	}
	return e, err
}

// ListShopping returns the shopping list, oldest first.
func (s *Store) ListShopping(ctx context.Context) ([]stock.ShoppingEntry, error) {
	rows, err := s.pool.Query(ctx, shoppingSelect+" ORDER BY e.created_at, e.id")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.ShoppingEntry, error) { return scanShopping(r) })
}

// GetShopping returns one shopping list entry.
func (s *Store) GetShopping(ctx context.Context, id int64) (stock.ShoppingEntry, error) {
	e, err := scanShopping(s.pool.QueryRow(ctx, shoppingSelect+" WHERE e.id = $1", id))
	return e, notFound(err)
}

// AddShopping adds an entry. Adding an item that is already on the list
// increases the quantity of the existing entry instead. Missing and
// archived items are refused with ErrNotFound.
func (s *Store) AddShopping(ctx context.Context, in stock.ShoppingInput) (int64, error) {
	if in.Unit == "" {
		in.Unit = stock.UnitPiece
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shopping_entries (item_id, free_text, quantity, note, unit)
		SELECT $1, $2, $3, $4, $6
		WHERE $1::bigint IS NULL OR EXISTS (SELECT 1 FROM items WHERE id = $1 AND archived_at IS NULL)
		ON CONFLICT (item_id) WHERE item_id IS NOT NULL DO UPDATE
		SET quantity = CASE WHEN shopping_entries.quantity IS NULL OR excluded.quantity IS NULL
		                    THEN COALESCE(excluded.quantity, shopping_entries.quantity)
		                    ELSE LEAST(shopping_entries.quantity + excluded.quantity, $5) END,
		    note = CASE WHEN excluded.note = '' OR strpos(shopping_entries.note, excluded.note) > 0 THEN shopping_entries.note
		                WHEN shopping_entries.note = '' THEN excluded.note
		                ELSE shopping_entries.note || '; ' || excluded.note END
		RETURNING id`,
		in.ItemID, in.FreeText, in.Quantity, in.Note, stock.MaxQuantity, in.Unit).Scan(&id)
	if isForeignKeyViolation(err) || errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// EnsureShopping puts an item on the list with at least qty, without
// adding up (used for kit shortfalls and low stock suggestions). changed
// reports whether the list changed: it doesn't when the entry already
// asks for as much. Missing and archived items are refused with
// ErrNotFound.
func (s *Store) EnsureShopping(ctx context.Context, itemID int64, qty float64, note string) (changed bool, err error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO shopping_entries (item_id, quantity, note)
		SELECT id, $2, $3 FROM items WHERE id = $1 AND archived_at IS NULL
		ON CONFLICT (item_id) WHERE item_id IS NOT NULL DO UPDATE
		SET quantity = excluded.quantity
		WHERE shopping_entries.quantity IS NULL OR shopping_entries.quantity < excluded.quantity`,
		itemID, qty, note)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	// Nothing written: either the entry already covers it, or the item
	// can't go on the list.
	var active bool
	err = s.pool.QueryRow(ctx, `SELECT archived_at IS NULL FROM items WHERE id = $1`, itemID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return false, ErrNotFound
	}
	return false, err
}

// DeleteShopping removes an entry.
func (s *Store) DeleteShopping(ctx context.Context, id int64) error {
	return checkAffected(s.pool.Exec(ctx, `DELETE FROM shopping_entries WHERE id = $1`, id))
}

// MarkBought ticks an entry off the list. For entries linked to an item,
// the purchase is recorded as a stock movement in the same transaction
// (purchase.ItemID is filled in from the entry). When less was bought than
// the entry asks for, the entry stays on the list with the rest; left is
// that remaining quantity (0 when the entry was removed).
func (s *Store) MarkBought(ctx context.Context, id int64, purchase *stock.MovementInput) (left float64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var itemID *int64
		var qty *float64
		if err := tx.QueryRow(ctx, `SELECT item_id, quantity FROM shopping_entries WHERE id = $1 FOR UPDATE`, id).Scan(&itemID, &qty); err != nil {
			return notFound(err)
		}
		if itemID != nil && purchase != nil {
			in := *purchase
			in.ItemID = *itemID
			in.Kind = stock.MovePurchase
			if errs := in.Validate(); errs.Any() {
				return errs
			}
			if err := applyStockChange(ctx, tx, in); err != nil {
				return err
			}
			if qty != nil {
				left = math.Round((*qty-in.Delta)*1000) / 1000
			}
		}
		if left > 0 {
			_, err := tx.Exec(ctx, `UPDATE shopping_entries SET quantity = $2 WHERE id = $1`, id, left)
			return err
		}
		left = 0
		_, err := tx.Exec(ctx, `DELETE FROM shopping_entries WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return 0, err
	}
	return left, nil
}
