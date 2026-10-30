package store

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// applyMovement changes an item's quantity and records the movement. It
// must run inside a transaction so the ledger and quantity never diverge.
func applyMovement(ctx context.Context, tx pgx.Tx, in stock.MovementInput) error {
	// The columns keep three decimals. Rounding here (not in the database)
	// makes the stored delta exactly the change applied to the quantity.
	in.Delta = math.Round(in.Delta*1000) / 1000
	if in.Delta == 0 {
		return stock.Errors{"amount": "Amount must not be zero"}
	}
	var qty float64
	var name, brand string
	var unit stock.Unit
	err := tx.QueryRow(ctx, `SELECT quantity, name, brand, unit FROM items WHERE id = $1 FOR UPDATE`, in.ItemID).
		Scan(&qty, &name, &brand, &unit)
	if err != nil {
		return notFound(err)
	}
	if qty+in.Delta < -0.0005 {
		return &InsufficientStockError{ItemID: in.ItemID, ItemName: displayName(brand, name), Unit: unit, Have: qty, Need: -in.Delta}
	}
	if qty+in.Delta > stock.MaxQuantity {
		return stock.Errors{"amount": "That would be more than " + stock.FormatQuantity(stock.MaxQuantity, unit) + " on stock"}
	}
	if _, err := tx.Exec(ctx, `UPDATE items SET quantity = quantity + $2, updated_at = now() WHERE id = $1`,
		in.ItemID, in.Delta); err != nil {
		return err
	}
	occurred := time.Now()
	if in.OccurredOn != nil {
		occurred = *in.OccurredOn
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO stock_movements (item_id, kind, delta, total_price, supplier, note, occurred_on, service_record_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		in.ItemID, in.Kind, in.Delta, in.TotalPrice, in.Supplier, in.Note, occurred, in.ServiceRecordID)
	return err
}

// AddMovement records a stock change (purchase, use, correction).
func (s *Store) AddMovement(ctx context.Context, in stock.MovementInput) error {
	if errs := in.Validate(); errs.Any() {
		return errs
	}
	return s.inTx(ctx, func(tx pgx.Tx) error { return applyMovement(ctx, tx, in) })
}

// CorrectStock sets an item's quantity to a counted amount and records the
// difference as a correction. The difference is worked out under the row
// lock, so a change made by someone else in the meantime is not undone. It
// returns the recorded change (0 when the stock already matched).
func (s *Store) CorrectStock(ctx context.Context, itemID int64, count float64, note string) (float64, error) {
	var delta float64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var qty float64
		if err := tx.QueryRow(ctx, `SELECT quantity FROM items WHERE id = $1 FOR UPDATE`, itemID).Scan(&qty); err != nil {
			return notFound(err)
		}
		var err error
		delta, err = correctTo(ctx, tx, itemID, qty, count, note)
		return err
	})
	return delta, err
}

// correctTo records a correction from qty (read under lock) to target.
func correctTo(ctx context.Context, tx pgx.Tx, itemID int64, qty, target float64, note string) (float64, error) {
	delta := math.Round((target-qty)*1000) / 1000
	if delta == 0 {
		return 0, nil
	}
	return delta, applyMovement(ctx, tx, stock.MovementInput{ItemID: itemID, Kind: stock.MoveCorrection, Delta: delta, Note: note})
}

// MovementFilter narrows ListMovements.
type MovementFilter struct {
	ItemID int64
	Kind   stock.MovementKind
	Limit  int
}

// ListMovements returns ledger entries, newest first.
func (s *Store) ListMovements(ctx context.Context, f MovementFilter) ([]stock.Movement, error) {
	var args sqlArgs
	sql := `
		SELECT m.id, m.item_id, i.brand, i.name, i.unit, m.kind, m.delta, m.total_price, m.supplier, m.note,
		       m.occurred_on, m.service_record_id, COALESCE(sr.title, ''), m.created_at
		FROM stock_movements m
		JOIN items i ON i.id = m.item_id
		LEFT JOIN service_records sr ON sr.id = m.service_record_id
		WHERE true`
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
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.Movement, error) {
		var m stock.Movement
		var brand, name string
		err := r.Scan(&m.ID, &m.ItemID, &brand, &name, &m.ItemUnit, &m.Kind, &m.Delta, &m.TotalPrice, &m.Supplier,
			&m.Note, &m.OccurredOn, &m.ServiceRecordID, &m.ServiceTitle, &m.CreatedAt)
		m.ItemName = displayName(brand, name)
		return m, err
	})
}

// Stats are the money figures shown on the dashboard.
type Stats struct {
	// StockValue is the value of current stock at the last purchase price
	// of each item (items never bought with a price are not counted).
	StockValue float64
	// Spent12m is purchases plus labour over the last 12 months.
	Spent12m float64
}

// GetStats computes the dashboard money figures.
func (s *Store) GetStats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT sum(i.quantity * lp.unit_price) FROM items i
		            JOIN LATERAL (SELECT m.total_price / m.delta AS unit_price FROM stock_movements m
		                          WHERE m.item_id = i.id AND m.kind = 'purchase' AND m.total_price IS NOT NULL AND m.delta > 0
		                          ORDER BY m.occurred_on DESC, m.id DESC LIMIT 1) lp ON true), 0)::float8,
		  (COALESCE((SELECT sum(total_price) FROM stock_movements
		             WHERE kind = 'purchase' AND occurred_on > current_date - interval '12 months'), 0)
		 + COALESCE((SELECT sum(labor_cost) FROM service_records
		             WHERE performed_on > current_date - interval '12 months'), 0))::float8`).
		Scan(&st.StockValue, &st.Spent12m)
	return st, err
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
