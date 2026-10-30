// Package store implements persistence on top of Postgres (pgx). All
// methods that touch more than one row run in a single transaction.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ConflictError is returned when a unique constraint is violated.
type ConflictError struct {
	Field   string
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// InsufficientStockError is returned when a movement would make a
// quantity negative.
type InsufficientStockError struct {
	ItemID   int64
	ItemName string
	Unit     stock.Unit
	Have     float64
	Need     float64
}

func (e *InsufficientStockError) Error() string {
	return fmt.Sprintf("not enough %s on stock: have %s, need %s", e.ItemName,
		stock.FormatQuantity(e.Have, e.Unit), stock.FormatQuantity(e.Need, e.Unit))
}

// Store gives access to all persisted data.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping checks the database connection.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, fn)
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func checkAffected(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func displayName(brand, name string) string {
	return stock.Item{Brand: brand, Name: name}.DisplayName()
}

// likePattern escapes LIKE wildcards and wraps q in %...%.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// normalizeCode keeps only letters and digits, so "77-038-005-044" finds
// "77038005044" (handy for barcode scanners and copy/paste).
//
// It returns "" (no code search) when that would drop more than
// separators, or leave fewer than three characters: "Ø8" would become "8"
// and match every code with an 8 in it.
func normalizeCode(q string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(q) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case strings.ContainsRune(" -./_", r):
		default:
			return ""
		}
	}
	if b.Len() < 3 {
		return ""
	}
	return b.String()
}

// sqlArgs collects positional query arguments.
type sqlArgs []any

func (a *sqlArgs) add(v any) string {
	*a = append(*a, v)
	return fmt.Sprintf("$%d", len(*a))
}
