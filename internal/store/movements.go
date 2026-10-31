package store

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// applyMovement changes an item's quantity and records the movement,
// returning its id. It must run inside a transaction so the ledger and
// quantity never diverge.
//
// Items are locked FOR NO KEY UPDATE throughout the store (only DeleteItem
// takes FOR UPDATE): saving a kit or a movement holds a KEY SHARE lock on
// the item through the foreign key, which FOR UPDATE would conflict with,
// so a kit saved while a service takes the same parts could deadlock.
func applyMovement(ctx context.Context, tx pgx.Tx, in stock.MovementInput) (int64, error) {
	// The columns keep three decimals. Rounding here (not in the database)
	// makes the stored delta exactly the change applied to the quantity.
	in.Delta = math.Round(in.Delta*1000) / 1000
	if in.Delta == 0 {
		return 0, stock.Errors{"amount": "Amount must not be zero"}
	}
	var qty float64
	var name, brand string
	var unit stock.Unit
	var archived bool
	err := tx.QueryRow(ctx, `SELECT quantity, name, brand, unit, archived_at IS NOT NULL FROM items WHERE id = $1 FOR NO KEY UPDATE`, in.ItemID).
		Scan(&qty, &name, &brand, &unit, &archived)
	if err != nil {
		return 0, notFound(err)
	}
	// Services check archived parts themselves (checkArchivedParts): an
	// edit may take an archived part again, as long as no more or less.
	if archived && in.Kind != stock.MoveService {
		return 0, archivedError(brand, name)
	}
	if qty+in.Delta < -0.0005 {
		return 0, &InsufficientStockError{ItemID: in.ItemID, ItemName: displayName(brand, name), Unit: unit, Have: qty, Need: -in.Delta}
	}
	if qty+in.Delta > stock.MaxQuantity {
		return 0, stock.Errors{"amount": "That would be more than " + stock.FormatQuantity(stock.MaxQuantity, unit) + " on stock"}
	}
	// No new version: a stock change is a delta, and the edit form guards
	// the quantity with quantity_was, so an open form stays good.
	if _, err := tx.Exec(ctx, `UPDATE items SET quantity = quantity + $2, updated_at = now() WHERE id = $1`,
		in.ItemID, in.Delta); err != nil {
		return 0, err
	}
	// The pages always pass the app's date; this is for direct store calls.
	occurred := time.Now()
	if in.OccurredOn != nil {
		occurred = *in.OccurredOn
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO stock_movements (item_id, kind, delta, total_price, supplier, note, occurred_on, service_record_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		in.ItemID, in.Kind, in.Delta, in.TotalPrice, in.Supplier, in.Note, occurred, in.ServiceRecordID).Scan(&id)
	return id, err
}

// archivedError refuses a stock change to an archived item.
func archivedError(brand, name string) error {
	return &ConflictError{Field: "form", Message: displayName(brand, name) + " is archived; restore it first."}
}

// AddMovement records a stock change (purchase, use, correction).
func (s *Store) AddMovement(ctx context.Context, in stock.MovementInput) error {
	if errs := in.Validate(); errs.Any() {
		return errs
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		_, err := applyStockChange(ctx, tx, in)
		return err
	})
}

// applyStockChange applies a movement and, for a purchase with a
// best-before date, sets the item's expiry to the new batch's date. It
// returns the movement's id.
func applyStockChange(ctx context.Context, tx pgx.Tx, in stock.MovementInput) (int64, error) {
	id, err := applyMovement(ctx, tx, in)
	if err != nil || in.ExpiresOn == nil {
		return id, err
	}
	// The edit form shows the best-before date: a new version, so a form
	// opened before can't save the old date back.
	_, err = tx.Exec(ctx, `UPDATE items SET expires_on = $2, updated_at = now(), version = version + 1 WHERE id = $1`,
		in.ItemID, *in.ExpiresOn)
	return id, err
}

// CorrectStock sets an item's quantity to a counted amount on day on and
// records the difference as a correction. The difference is worked out
// under the row lock, so a change made by someone else in the meantime is
// not undone. It returns the recorded change (0 when the stock already
// matched).
func (s *Store) CorrectStock(ctx context.Context, itemID int64, count float64, note string, on time.Time) (float64, error) {
	var delta float64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var qty float64
		if err := tx.QueryRow(ctx, `SELECT quantity FROM items WHERE id = $1 FOR NO KEY UPDATE`, itemID).Scan(&qty); err != nil {
			return notFound(err)
		}
		var err error
		delta, err = correctTo(ctx, tx, itemID, qty, count, note, on)
		return err
	})
	return delta, err
}

// correctTo records a correction from qty (read under lock) to target.
func correctTo(ctx context.Context, tx pgx.Tx, itemID int64, qty, target float64, note string, on time.Time) (float64, error) {
	delta := math.Round((target-qty)*1000) / 1000
	if delta == 0 {
		return 0, nil
	}
	_, err := applyMovement(ctx, tx, stock.MovementInput{ItemID: itemID, Kind: stock.MoveCorrection, Delta: delta, Note: note, OccurredOn: &on})
	return delta, err
}

// MovementFilter narrows ListMovements.
type MovementFilter struct {
	ID     int64
	ItemID int64
	Kind   stock.MovementKind
	Limit  int
	Offset int // skip this many, for paging
}

// ListMovements returns ledger entries, newest first.
func (s *Store) ListMovements(ctx context.Context, f MovementFilter) ([]stock.Movement, error) {
	var args sqlArgs
	sql := `
		SELECT m.id, m.item_id, i.brand, i.name, i.unit, m.kind, m.delta, m.total_price, m.supplier, m.note,
		       m.occurred_on, m.service_record_id, COALESCE(sr.title, ''), m.created_at, m.version
		FROM stock_movements m
		JOIN items i ON i.id = m.item_id
		LEFT JOIN service_records sr ON sr.id = m.service_record_id
		WHERE true`
	if f.ID != 0 {
		sql += " AND m.id = " + args.add(f.ID)
	}
	if f.ItemID != 0 {
		sql += " AND m.item_id = " + args.add(f.ItemID)
	}
	if f.Kind != "" {
		sql += " AND m.kind = " + args.add(f.Kind)
	}
	sql += " ORDER BY m.occurred_on DESC, m.id DESC"
	if f.Limit > 0 {
		sql += " LIMIT " + args.add(f.Limit)
	}
	if f.Offset > 0 {
		sql += " OFFSET " + args.add(f.Offset)
	}
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.Movement, error) {
		var m stock.Movement
		var brand, name string
		err := r.Scan(&m.ID, &m.ItemID, &brand, &name, &m.ItemUnit, &m.Kind, &m.Delta, &m.TotalPrice, &m.Supplier,
			&m.Note, &m.OccurredOn, &m.ServiceRecordID, &m.ServiceTitle, &m.CreatedAt, &m.Version)
		m.ItemName = displayName(brand, name)
		return m, err
	})
}

// GetMovement returns one stock change.
func (s *Store) GetMovement(ctx context.Context, id int64) (stock.Movement, error) {
	moves, err := s.ListMovements(ctx, MovementFilter{ID: id})
	if err != nil {
		return stock.Movement{}, err
	}
	if len(moves) == 0 {
		return stock.Movement{}, ErrNotFound
	}
	return moves[0], nil
}

// lockedMovement is a stock change and its item's stock, read under lock.
type lockedMovement struct {
	stock.Movement
	qty float64
}

// lockMovement locks a stock change and its item for revising. An
// archived item's changes are refused, like new ones.
func lockMovement(ctx context.Context, tx pgx.Tx, id int64) (lockedMovement, error) {
	var m lockedMovement
	var brand, name string
	var archived bool
	err := tx.QueryRow(ctx, `
		SELECT m.id, m.item_id, i.brand, i.name, i.unit, i.quantity, i.archived_at IS NOT NULL, m.kind, m.delta,
		       m.total_price, m.supplier, m.note, m.occurred_on, m.service_record_id, m.version
		FROM stock_movements m JOIN items i ON i.id = m.item_id
		WHERE m.id = $1 FOR NO KEY UPDATE`, id).
		Scan(&m.ID, &m.ItemID, &brand, &name, &m.ItemUnit, &m.qty, &archived, &m.Kind, &m.Delta, &m.TotalPrice,
			&m.Supplier, &m.Note, &m.OccurredOn, &m.ServiceRecordID, &m.Version)
	if err != nil {
		return m, notFound(err)
	}
	m.ItemName = displayName(brand, name)
	if archived {
		return m, archivedError(brand, name)
	}
	if !m.Editable() {
		return m, &ConflictError{Field: "form", Message: "Parts used by a service are changed by editing the service"}
	}
	return m, nil
}

// lockVersionedMovement is lockMovement for a form or button that carried
// the stock change's version: one deleted or changed since is stale.
func lockVersionedMovement(ctx context.Context, tx pgx.Tx, id int64, version int) (lockedMovement, error) {
	m, err := lockMovement(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return m, goneError(ctx, tx, WhatMovement, id)
	}
	if err != nil {
		return m, err
	}
	return m, checkVersion(m.Version, version, WhatMovement)
}

// reviseMovement changes the stock as if m had been recorded with delta
// instead (0 for not at all). Together with updating or deleting the
// movement row, this keeps quantity equal to the sum of the ledger.
func reviseMovement(ctx context.Context, tx pgx.Tx, m lockedMovement, delta float64) error {
	qty := math.Round((m.qty-m.Delta+delta)*1000) / 1000
	if qty < 0 {
		return &InsufficientStockError{ItemID: m.ItemID, ItemName: m.ItemName, Unit: m.ItemUnit, Have: m.qty, Need: m.Delta - delta}
	}
	if qty > stock.MaxQuantity {
		return stock.Errors{"amount": "That would be more than " + stock.FormatQuantity(stock.MaxQuantity, m.ItemUnit) + " on stock"}
	}
	// No new version for the item, like applyMovement.
	_, err := tx.Exec(ctx, `UPDATE items SET quantity = $2, updated_at = now() WHERE id = $1`, m.ItemID, qty)
	return err
}

// UpdateMovement corrects a recorded stock change (see stock.ApplyEdit).
// The item's stock moves by the difference; a correction that would leave
// less than nothing on stock fails with *InsufficientStockError. Like
// DeleteMovement, it is a deliberate exception to "every change goes
// through applyMovement": it rewrites a mistaken entry together with its
// effect, so the ledger still adds up. A stock change changed or deleted
// since e.Version fails with a *StaleError.
func (s *Store) UpdateMovement(ctx context.Context, id int64, e stock.MovementEdit) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		m, err := lockVersionedMovement(ctx, tx, id, e.Version)
		if err != nil {
			return err
		}
		in, errs := stock.ApplyEdit(m.Movement, e)
		if errs.Any() {
			return errs
		}
		in.Delta = math.Round(in.Delta*1000) / 1000
		if in.Delta == 0 {
			return stock.Errors{"amount": "Amount must not be zero"}
		}
		if err := reviseMovement(ctx, tx, m, in.Delta); err != nil {
			return err
		}
		// A new version: an edit form opened before this one is stale now.
		_, err = tx.Exec(ctx, `
			UPDATE stock_movements SET kind = $2, delta = $3, total_price = $4, supplier = $5, note = $6, occurred_on = $7,
			       version = version + 1
			WHERE id = $1`, id, in.Kind, in.Delta, in.TotalPrice, in.Supplier, in.Note, *in.OccurredOn)
		return err
	})
}

// DeleteMovement removes a mistaken stock change and undoes its effect on
// the stock. It fails with *InsufficientStockError when that would leave
// less than nothing, e.g. for a purchase that has been used up since. A
// best-before date the purchase set is kept. A stock change changed or
// deleted since version fails with a *StaleError.
func (s *Store) DeleteMovement(ctx context.Context, id int64, version int) (deleted stock.Movement, trashID int64, err error) {
	var m lockedMovement
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if m, err = lockVersionedMovement(ctx, tx, id, version); err != nil {
			return err
		}
		if err := reviseMovement(ctx, tx, m, 0); err != nil {
			return err
		}
		p, err := snapshot(ctx, tx, id, [2]string{"stock_movements", "id"})
		if err != nil {
			return err
		}
		label := trashLabel(m.Kind.Label(), m.ItemName, stock.FormatDelta(m.Delta, m.ItemUnit), m.OccurredOn.Format("2 Jan 2006"))
		if trashID, err = putTrash(ctx, tx, TrashMovement, label, p); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM stock_movements WHERE id = $1`, id)
		return err
	})
	return m.Movement, trashID, err
}

// Stats are the money figures shown on the dashboard.
type Stats struct {
	// StockValue is the value of current stock at the last purchase price
	// of each item (archived items and items never bought with a price
	// are not counted).
	StockValue float64
	// Spent12m is purchases plus labour over the last 12 months.
	Spent12m float64
}

// GetStats computes the dashboard money figures, with the last 12 months
// counted back from today (a purchase dated tomorrow is not counted yet).
func (s *Store) GetStats(ctx context.Context, today time.Time) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT sum(i.quantity * lp.unit_price) FROM items i
		            JOIN LATERAL (SELECT m.total_price / m.delta AS unit_price FROM stock_movements m
		                          WHERE m.item_id = i.id AND m.kind = 'purchase' AND m.total_price IS NOT NULL AND m.delta > 0
		                          ORDER BY m.occurred_on DESC, m.id DESC LIMIT 1) lp ON true
		            WHERE i.archived_at IS NULL), 0)::float8,
		  (COALESCE((SELECT sum(total_price) FROM stock_movements
		             WHERE kind = 'purchase' AND occurred_on > $1::date - interval '12 months' AND occurred_on <= $1::date), 0)
		 + COALESCE((SELECT sum(labor_cost) FROM service_records
		             WHERE performed_on > $1::date - interval '12 months' AND performed_on <= $1::date), 0))::float8`, stock.DateOnly(today)).
		Scan(&st.StockValue, &st.Spent12m)
	return st, err
}

// CountPurchases returns how many purchases have been recorded for an item.
func (s *Store) CountPurchases(ctx context.Context, itemID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE item_id = $1 AND kind = 'purchase'`, itemID).Scan(&n)
	return n, err
}

// LastPurchases returns the most recent priced purchase of each of the
// given items. Items never bought with a price are missing from the map.
func (s *Store) LastPurchases(ctx context.Context, itemIDs []int64) (map[int64]stock.LastPurchase, error) {
	out := map[int64]stock.LastPurchase{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (item_id) item_id, (total_price / delta)::float8, occurred_on, supplier
		FROM stock_movements
		WHERE item_id = ANY($1) AND kind = 'purchase' AND total_price IS NOT NULL AND delta > 0
		ORDER BY item_id, occurred_on DESC, id DESC`, itemIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var lp stock.LastPurchase
		if err := rows.Scan(&id, &lp.UnitPrice, &lp.On, &lp.Supplier); err != nil {
			return nil, err
		}
		out[id] = lp
	}
	return out, rows.Err()
}

// LastUnitPrice returns the price per base unit of the item's most recent
// priced purchase, or nil.
func (s *Store) LastUnitPrice(ctx context.Context, itemID int64) (*float64, error) {
	var p *float64
	err := s.pool.QueryRow(ctx, `
		SELECT (total_price / delta)::float8 FROM stock_movements
		WHERE item_id = $1 AND kind = 'purchase' AND total_price IS NOT NULL AND delta > 0
		ORDER BY occurred_on DESC, id DESC LIMIT 1`, itemID).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}
