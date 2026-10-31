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

// StaleError refuses a form or button whose record changed or vanished
// since it was shown: the version it carries no longer matches. Gone
// means the row no longer exists; InTrash that it can be restored from
// Recently deleted. A gone record also matches ErrNotFound.
type StaleError struct {
	What    string // what the record is, e.g. WhatItem
	Gone    bool
	InTrash bool
}

// Values of StaleError.What.
const (
	WhatItem     = "item"
	WhatVehicle  = "vehicle"
	WhatKit      = "kit"
	WhatService  = "service"
	WhatMovement = "stock change"
	WhatShopping = "shopping list entry"
)

// versionedTables maps each StaleError.What to its table. These are the
// tables with a version column.
var versionedTables = map[string]string{
	WhatItem: "items", WhatVehicle: "vehicles", WhatKit: "service_kits", WhatService: "service_records",
	WhatMovement: "stock_movements", WhatShopping: "shopping_entries",
}

// versioned reports whether table has a version column.
func versioned(table string) bool {
	for _, t := range versionedTables {
		if t == table {
			return true
		}
	}
	return false
}

func (e *StaleError) Error() string {
	switch {
	case e.Gone && e.InTrash:
		return "This " + e.What + " was deleted since you opened this; it's in Recently deleted."
	case e.Gone:
		return "This " + e.What + " was deleted since you opened this."
	}
	return "This " + e.What + " was changed since you opened this (another tab or person). Reload to see the latest, then make your change again."
}

// Is lets errors.Is(err, ErrNotFound) match a gone record.
func (e *StaleError) Is(target error) bool { return e.Gone && target == ErrNotFound }

// checkVersion compares the version read under the row's lock with the
// one the form or button carried. A missing version (0) is stale too.
func checkVersion(have, want int, what string) error {
	if have != want {
		return &StaleError{What: what}
	}
	return nil
}

// goneError explains a row that is not there: a *StaleError saying
// whether it is in the trash.
func goneError(ctx context.Context, q queryRower, what string, id int64) error {
	var trashed bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trash WHERE payload @> `+inTrash+`)`,
		versionedTables[what], "id", id).Scan(&trashed)
	if err != nil {
		return err
	}
	return &StaleError{What: what, Gone: true, InTrash: trashed}
}

// lockedVersion handles the error of a SELECT … FOR UPDATE that read a
// row and its version: a row that is not there is gone, and one with
// another version than want is stale.
func lockedVersion(ctx context.Context, q queryRower, err error, have, want int, what string, id int64) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return goneError(ctx, q, what, id)
	}
	if err != nil {
		return err
	}
	return checkVersion(have, want, what)
}

// versionedUpdate checks a one-row write whose WHERE also matched the
// version ("… AND version = $n"): when it changed nothing, the row was
// either changed meanwhile or is gone. The statement takes the row's lock
// and re-checks the version once a concurrent writer is done, so no extra
// lock is needed.
func versionedUpdate(ctx context.Context, q queryRower, tag pgconn.CommandTag, err error, what string, id int64) error {
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+versionedTables[what]+` WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return &StaleError{What: what}
	}
	return goneError(ctx, q, what, id)
}

// Gone explains a record of kind what (WhatItem, …) that a page found
// missing: a *StaleError saying whether it is in Recently deleted.
func (s *Store) Gone(ctx context.Context, what string, id int64) error {
	if _, ok := versionedTables[what]; !ok {
		return ErrNotFound
	}
	return goneError(ctx, s.pool, what, id)
}

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

// ShortPartsError lists every part of a service that is short, so the
// form can mark them all at once. errors.As also finds the first
// *InsufficientStockError in it.
type ShortPartsError struct {
	Parts []*InsufficientStockError
}

func (e *ShortPartsError) Error() string {
	msgs := make([]string, len(e.Parts))
	for i, p := range e.Parts {
		msgs[i] = p.Error()
	}
	return strings.Join(msgs, "; ")
}

// Unwrap exposes the individual shortages to errors.As.
func (e *ShortPartsError) Unwrap() []error {
	errs := make([]error, len(e.Parts))
	for i, p := range e.Parts {
		errs[i] = p
	}
	return errs
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
