package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

const shoppingSelect = `
SELECT e.id, e.item_id, COALESCE(i.brand, ''), COALESCE(i.name, ''), COALESCE(i.unit, e.unit), e.free_text,
       e.quantity, e.note, e.created_at, e.version
FROM shopping_entries e
LEFT JOIN items i ON i.id = e.item_id`

func scanShopping(row pgx.Row) (stock.ShoppingEntry, error) {
	var e stock.ShoppingEntry
	var brand, name string
	err := row.Scan(&e.ID, &e.ItemID, &brand, &name, &e.ItemUnit, &e.FreeText, &e.Quantity, &e.Note, &e.CreatedAt, &e.Version)
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
// archived items are refused with ErrNotFound. For an item, in.Unit (when
// set) is the unit its quantity was read in: one the item no longer has,
// or an amount its unit can't hold, is refused with stock.Errors.
func (s *Store) AddShopping(ctx context.Context, in stock.ShoppingInput) (int64, error) {
	var id int64
	// The list is where its forms return to, whatever became of the entry.
	err := s.inFormTx(ctx, in.FormKey, "", &id, func(tx pgx.Tx) error {
		if in.ItemID != nil {
			// The item's entry before the item, like MarkBought, so the two
			// can't deadlock.
			if _, err := tx.Exec(ctx, `SELECT id FROM shopping_entries WHERE item_id = $1 FOR UPDATE`, *in.ItemID); err != nil {
				return err
			}
			// FOR SHARE: an item being archived right now waits, or is seen
			// archived, so it can't end up back on the list; and its unit
			// can't change until this commits.
			var archived bool
			var unit stock.Unit
			err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL, unit FROM items WHERE id = $1 FOR SHARE`, *in.ItemID).Scan(&archived, &unit)
			if err != nil {
				return notFound(err)
			}
			if archived {
				return ErrNotFound
			}
			// The unit read under the lock, like EnsureShopping: the amount
			// was read in the unit the page showed.
			if in.Quantity != nil {
				if in.Unit != "" && in.Unit != unit {
					return stock.Errors{"quantity": "The item's unit was changed meanwhile. Check the amount and add it again."}
				}
				if err := stock.CheckQuantity(*in.Quantity, unit); err != nil {
					return stock.Errors{"quantity": err.Error()}
				}
			}
			in.Unit = unit
		} else if in.Unit == "" {
			in.Unit = stock.UnitPiece
		}
		// Adding to an entry changes what its Bought form shows: a new version.
		return tx.QueryRow(ctx, `
			INSERT INTO shopping_entries (item_id, free_text, quantity, note, unit) VALUES ($1, $2, $3, $4, $6)
			ON CONFLICT (item_id) WHERE item_id IS NOT NULL DO UPDATE
			SET version = shopping_entries.version + 1,
			    quantity = CASE WHEN shopping_entries.quantity IS NULL OR excluded.quantity IS NULL
			                    THEN COALESCE(excluded.quantity, shopping_entries.quantity)
			                    ELSE LEAST(shopping_entries.quantity + excluded.quantity, $5) END,
			    note = CASE WHEN excluded.note = '' OR strpos(shopping_entries.note, excluded.note) > 0 THEN shopping_entries.note
			                WHEN shopping_entries.note = '' THEN excluded.note
			                ELSE shopping_entries.note || '; ' || excluded.note END
			RETURNING id`,
			in.ItemID, in.FreeText, in.Quantity, in.Note, stock.MaxQuantity, in.Unit).Scan(&id)
	})
	return id, err
}

// EnsureShopping puts an item on the list with at least qty, without
// adding up (used for kit shortfalls and low stock suggestions). changed
// reports whether the list changed: it doesn't when the entry already
// asks for as much. Missing and archived items are refused with
// ErrNotFound, and an amount the item's unit can't hold (fractional
// pieces, finer than 0.01 ml or g) with stock.Errors.
func (s *Store) EnsureShopping(ctx context.Context, itemID int64, qty float64, note string) (changed bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// Entry, then item, like AddShopping; FOR SHARE keeps the item from
		// being archived meanwhile.
		if _, err := tx.Exec(ctx, `SELECT id FROM shopping_entries WHERE item_id = $1 FOR UPDATE`, itemID); err != nil {
			return err
		}
		var archived bool
		var unit stock.Unit
		if err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL, unit FROM items WHERE id = $1 FOR SHARE`, itemID).Scan(&archived, &unit); err != nil {
			return notFound(err)
		}
		if archived {
			return ErrNotFound
		}
		// The unit read under the lock: the amount came from a hidden field.
		if err := stock.CheckQuantity(qty, unit); err != nil {
			return stock.Errors{"quantity": err.Error()}
		}
		// A new version when the entry changes, like AddShopping.
		tag, err := tx.Exec(ctx, `
			INSERT INTO shopping_entries (item_id, quantity, note) VALUES ($1, $2, $3)
			ON CONFLICT (item_id) WHERE item_id IS NOT NULL DO UPDATE
			SET version = shopping_entries.version + 1,
			    quantity = excluded.quantity,
			    note = CASE WHEN excluded.note = '' OR strpos(shopping_entries.note, excluded.note) > 0 THEN shopping_entries.note
			                WHEN shopping_entries.note = '' THEN excluded.note
			                ELSE shopping_entries.note || '; ' || excluded.note END
			WHERE shopping_entries.quantity IS NULL OR shopping_entries.quantity < excluded.quantity`,
			itemID, qty, note)
		changed = tag.RowsAffected() > 0 // nothing written: the entry already asks for as much
		return err
	})
	return changed, err
}

// DeleteShopping removes an entry. An entry changed since version fails
// with a *StaleError.
func (s *Store) DeleteShopping(ctx context.Context, id int64, version int) (trashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		trashID, err = trashShopping(ctx, tx, id)
		if errors.Is(err, ErrNotFound) {
			return goneError(ctx, tx, WhatShopping, id)
		}
		if err != nil {
			return err
		}
		// The delete locks the entry and checks its version; one changed
		// since the snapshot rolls the snapshot back too.
		tag, err := tx.Exec(ctx, `DELETE FROM shopping_entries WHERE id = $1 AND version = $2`, id, version)
		return versionedUpdate(ctx, tx, tag, err, WhatShopping, id)
	})
	return trashID, err
}

// trashShopping puts a snapshot of a shopping entry in the trash.
func trashShopping(ctx context.Context, tx pgx.Tx, id int64) (int64, error) {
	var title string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(btrim(i.brand || ' ' || i.name), ''), e.free_text)
		FROM shopping_entries e LEFT JOIN items i ON i.id = e.item_id WHERE e.id = $1`, id).Scan(&title)
	if err != nil {
		return 0, notFound(err)
	}
	p, err := snapshot(ctx, tx, id, [2]string{"shopping_entries", "id"})
	if err != nil {
		return 0, err
	}
	return putTrash(ctx, tx, TrashShopping, trashLabel("Shopping list", title), p)
}

// MarkBought ticks an entry off the list. For entries linked to an item,
// the purchase is recorded as a stock movement in the same transaction
// (purchase.ItemID is filled in from the entry). When less was bought than
// the entry asks for, the entry stays on the list with the rest; left is
// that remaining quantity (0 when the entry was removed). An entry changed
// since version fails with a *StaleError. The purchase's FormKey is the
// Bought form's one-time key; undoing the purchase frees it.
func (s *Store) MarkBought(ctx context.Context, id int64, version int, purchase *stock.MovementInput) (left float64, trashID int64, err error) {
	var key string
	if purchase != nil {
		key = purchase.FormKey
	}
	err = s.inFormTx(ctx, key, "", nil, func(tx pgx.Tx) error {
		var itemID *int64
		var qty *float64
		var have int
		err := tx.QueryRow(ctx, `SELECT item_id, quantity, version FROM shopping_entries WHERE id = $1 FOR UPDATE`, id).Scan(&itemID, &qty, &have)
		if err := lockedVersion(ctx, tx, err, have, version, WhatShopping, id); err != nil {
			return err
		}
		// The entry as it was, so ticking it off can be undone.
		before, err := snapshotRows(ctx, tx, "shopping_entries", "id = $1", id)
		if err != nil {
			return err
		}
		if itemID != nil && purchase != nil {
			in := *purchase
			in.ItemID = *itemID
			in.Kind = stock.MovePurchase
			if errs := in.Validate(); errs.Any() {
				return errs
			}
			// Rounded like applyMovement rounds it, so left matches what was
			// recorded.
			in.Delta = stock.RoundQty(in.Delta)
			movementID, err := applyStockChange(ctx, tx, in)
			if err != nil {
				return err
			}
			if qty != nil {
				left = stock.RoundQty(*qty - in.Delta)
			}
			if trashID, err = putBought(ctx, tx, id, before, movementID, in.FormKey); err != nil {
				return err
			}
		} else if trashID, err = trashShopping(ctx, tx, id); err != nil {
			return err
		}
		if left > 0 {
			// What is left to buy is new: a new version.
			_, err := tx.Exec(ctx, `UPDATE shopping_entries SET quantity = $2, version = version + 1 WHERE id = $1`, id, left)
			return err
		}
		left = 0
		_, err = tx.Exec(ctx, `DELETE FROM shopping_entries WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	return left, trashID, nil
}

// putBought records a purchase made by ticking an entry off, with the
// entry as it was before, so it can be undone, and the key of the form
// that made it, which undoing it frees.
func putBought(ctx context.Context, tx pgx.Tx, entryID int64, before json.RawMessage, movementID int64, formKey string) (int64, error) {
	var title string
	err := tx.QueryRow(ctx, `
		SELECT btrim(i.brand || ' ' || i.name) FROM shopping_entries e JOIN items i ON i.id = e.item_id WHERE e.id = $1`, entryID).Scan(&title)
	if err != nil {
		return 0, err
	}
	m, _ := json.Marshal(movementID)
	p := trashPayload{"shopping_entries": before, "movement_id": m}
	if formKey != "" {
		p["form_key"], _ = json.Marshal(formKey)
	}
	return putTrash(ctx, tx, TrashBought, trashLabel("Bought", title), p)
}

// ItemsNamed returns the items, archived ones included, whose name or
// "brand name" is text (ignoring case and surrounding spaces): candidates
// for a free-text entry that is already on stock.
func (s *Store) ItemsNamed(ctx context.Context, text string) ([]stock.Item, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM items
		WHERE lower(btrim(name)) = lower(btrim($1)) OR lower(btrim(brand || ' ' || name)) = lower(btrim($1))
		ORDER BY archived_at IS NOT NULL, id`, text)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return s.ListItems(ctx, ItemFilter{IDs: ids})
}

// LinkShopping turns a free-text entry into an entry for an existing item,
// restoring the item if it was archived, and returns the entry to mark
// bought. If the item is on the list already, the free-text entry is
// merged into that one. A quantity in another unit than the item's is
// dropped (dropped reports it). ErrNotFound means the entry is gone or
// already linked, or the item is gone. An entry changed since version
// fails with a *StaleError.
func (s *Store) LinkShopping(ctx context.Context, entryID int64, version int, itemID int64) (id int64, dropped bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var linked *int64
		var unit stock.Unit
		var qty *float64
		var note string
		var have int
		err := tx.QueryRow(ctx, `SELECT item_id, unit, quantity, note, version FROM shopping_entries WHERE id = $1 FOR UPDATE`, entryID).
			Scan(&linked, &unit, &qty, &note, &have)
		if err := lockedVersion(ctx, tx, err, have, version, WhatShopping, entryID); err != nil {
			return err
		}
		if linked != nil {
			return ErrNotFound
		}
		// Lock the item's own entry before the item, like MarkBought, so the
		// two can't deadlock.
		err = tx.QueryRow(ctx, `SELECT id FROM shopping_entries WHERE item_id = $1 FOR UPDATE`, itemID).Scan(&id)
		merge := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var itemUnit stock.Unit
		if err := tx.QueryRow(ctx, `SELECT unit FROM items WHERE id = $1 FOR NO KEY UPDATE`, itemID).Scan(&itemUnit); err != nil {
			return notFound(err)
		}
		// Looked up again under the item's lock, like ArchiveItem: locking
		// an entry that wasn't there yet locked nothing, and an AddShopping
		// or EnsureShopping that held the item has committed its entry by
		// now (it can't add one while this holds the item). NOWAIT: this
		// entry comes after the item, against the usual order, so one that
		// is being ticked off right now (MarkBought holds it and waits for
		// the item) is refused instead of deadlocking.
		if !merge {
			err = tx.QueryRow(ctx, `SELECT id FROM shopping_entries WHERE item_id = $1 FOR UPDATE NOWAIT`, itemID).Scan(&id)
			switch {
			case err == nil:
				merge = true
			case isLockNotAvailable(err):
				return &ConflictError{Field: "form", Message: "The shopping list changed meanwhile. Try again."}
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		}
		// Un-archiving changes the item's page: a new version, only then.
		if _, err := tx.Exec(ctx, `UPDATE items SET archived_at = NULL, version = version + 1 WHERE id = $1 AND archived_at IS NOT NULL`,
			itemID); err != nil {
			return err
		}
		if itemUnit != unit && qty != nil {
			qty, dropped = nil, true
		}
		if !merge {
			id = entryID
			// It is another entry now: a new version.
			_, err = tx.Exec(ctx, `UPDATE shopping_entries SET item_id = $2, free_text = '', quantity = $3, unit = $4, version = version + 1
				WHERE id = $1`, entryID, itemID, qty, itemUnit)
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM shopping_entries WHERE id = $1`, entryID); err != nil {
			return err
		}
		// The merged entry asks for more: a new version.
		_, err = tx.Exec(ctx, `
			UPDATE shopping_entries SET
			  version = version + 1,
			  quantity = CASE WHEN quantity IS NULL OR $2::numeric IS NULL THEN COALESCE($2, quantity)
			                  ELSE LEAST(quantity + $2, $4) END,
			  note = CASE WHEN $3 = '' OR strpos(note, $3) > 0 THEN note WHEN note = '' THEN $3 ELSE note || '; ' || $3 END
			WHERE id = $1`, id, qty, note, stock.MaxQuantity)
		return err
	})
	return id, dropped, err
}
