package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// Deleted things go to the trash: a JSON snapshot of their rows, written in
// the same transaction as the delete and kept until the trash is emptied.
// Restoring puts the rows back with their original ids, so links to them
// work again, and redoes their effect on stock (and the odometer) under the
// same rules as when they were made: it fails rather than leave less than
// nothing on stock. Like DeleteService, a restore changes movements
// together with their effect, so the ledger still adds up.

// Trash kinds.
const (
	TrashService  = "service"
	TrashKit      = "kit"
	TrashMovement = "movement"
	TrashShopping = "shopping"
	TrashItem     = "item"
	TrashVehicle  = "vehicle"
	TrashPhoto    = "photo"
	TrashBought   = "bought"
)

// trashPayload maps table names to snapshots of their rows, plus extra
// values (such as the movement a purchase created).
type trashPayload map[string]json.RawMessage

// snapshotRows returns the rows of table matching where as a JSON array.
// table and where come from this package, never from input.
func snapshotRows(ctx context.Context, tx pgx.Tx, table, where string, args ...any) (json.RawMessage, error) {
	var raw string
	err := tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t)), '[]'::jsonb)::text FROM `+table+` t WHERE `+where, args...).Scan(&raw)
	return json.RawMessage(raw), err
}

// snapshot takes the rows of several tables matching one id, for a trash
// payload: each entry is a table and the column holding the id.
func snapshot(ctx context.Context, tx pgx.Tx, id int64, tables ...[2]string) (trashPayload, error) {
	p := trashPayload{}
	for _, t := range tables {
		rows, err := snapshotRows(ctx, tx, t[0], t[1]+" = $1", id)
		if err != nil {
			return nil, err
		}
		p[t[0]] = rows
	}
	return p, nil
}

// putTrash records a deleted thing and returns its trash id.
func putTrash(ctx context.Context, tx pgx.Tx, kind, label string, p trashPayload) (int64, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO trash (kind, label, payload) VALUES ($1, $2, $3::jsonb) RETURNING id`,
		kind, label, string(b)).Scan(&id)
	return id, err
}

// restoreRows inserts snapshot rows back into table with their ids.
func restoreRows(ctx context.Context, tx pgx.Tx, table string, rows json.RawMessage) error {
	if len(bytes.TrimSpace(rows)) == 0 || string(rows) == "[]" || string(rows) == "null" {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO `+table+` OVERRIDING SYSTEM VALUE SELECT * FROM jsonb_populate_recordset(NULL::`+table+`, $1::jsonb)`, string(rows))
	return err
}

// snapshotRow is one decoded row of a snapshot. Numbers stay json.Number,
// so nothing loses precision on the way back.
type snapshotRow map[string]any

func decodeRows(raw json.RawMessage) ([]snapshotRow, error) {
	var rows []snapshotRow
	if len(raw) == 0 {
		return nil, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return rows, d.Decode(&rows)
}

func encodeRows(rows []snapshotRow) json.RawMessage {
	if len(rows) == 0 {
		return json.RawMessage("[]")
	}
	b, _ := json.Marshal(rows)
	return b
}

func (r snapshotRow) int64(col string) int64 {
	n, _ := r[col].(json.Number).Int64()
	return n
}

func (r snapshotRow) float(col string) float64 {
	f, _ := r[col].(json.Number).Float64()
	return f
}

// existing keeps the rows whose col refers to a row of table that still
// exists (e.g. tags of vehicles that were deleted for good are dropped).
func existing(ctx context.Context, tx pgx.Tx, rows []snapshotRow, col, table string) ([]snapshotRow, error) {
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.int64(col))
	}
	if len(ids) == 0 {
		return rows, nil
	}
	found, err := tx.Query(ctx, `SELECT id FROM `+table+` WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	have, err := pgx.CollectRows(found, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	var out []snapshotRow
	for _, r := range rows {
		if slices.Contains(have, r.int64(col)) {
			out = append(out, r)
		}
	}
	return out, nil
}

// adjustStock changes an item's quantity by delta, refusing to leave less
// than nothing or more than the maximum. The caller holds the item's lock
// and records the matching movement, so the ledger adds up.
func adjustStock(ctx context.Context, tx pgx.Tx, itemID int64, delta float64) error {
	var qty float64
	var name, brand string
	var unit stock.Unit
	if err := tx.QueryRow(ctx, `SELECT quantity, name, brand, unit FROM items WHERE id = $1 FOR UPDATE`, itemID).
		Scan(&qty, &name, &brand, &unit); err != nil {
		return notFound(err)
	}
	next := math.Round((qty+delta)*1000) / 1000
	if next < 0 {
		return &InsufficientStockError{ItemID: itemID, ItemName: displayName(brand, name), Unit: unit, Have: qty, Need: -delta}
	}
	if next > stock.MaxQuantity {
		return &ConflictError{Field: "form", Message: displayName(brand, name) + " would have more than " + stock.FormatQuantity(stock.MaxQuantity, unit)}
	}
	_, err := tx.Exec(ctx, `UPDATE items SET quantity = $2, updated_at = now() WHERE id = $1`, itemID, next)
	return err
}

// lockItems locks items in id order, so restores can't deadlock with
// services or each other.
func lockItems(ctx context.Context, tx pgx.Tx, ids []int64) error {
	_, err := tx.Exec(ctx, `SELECT id FROM items WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	return err
}

// takeMovements applies the stock effect of snapshot movements and puts
// them back. Every item that would go below zero is reported at once.
func takeMovements(ctx context.Context, tx pgx.Tx, moves []snapshotRow) error {
	var ids []int64
	for _, m := range moves {
		ids = append(ids, m.int64("item_id"))
	}
	slices.Sort(ids)
	if err := lockItems(ctx, tx, ids); err != nil {
		return err
	}
	var shorts ShortPartsError
	for _, m := range moves {
		err := adjustStock(ctx, tx, m.int64("item_id"), m.float("delta"))
		var short *InsufficientStockError
		if errors.As(err, &short) {
			shorts.Parts = append(shorts.Parts, short)
			continue
		}
		if err != nil {
			return err
		}
	}
	if len(shorts.Parts) > 0 {
		return &shorts
	}
	return restoreRows(ctx, tx, "stock_movements", encodeRows(moves))
}

// ListTrash returns what can be restored, newest first.
func (s *Store) ListTrash(ctx context.Context) ([]stock.TrashEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, label, deleted_at FROM trash ORDER BY deleted_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.TrashEntry, error) {
		var e stock.TrashEntry
		err := r.Scan(&e.ID, &e.Kind, &e.Label, &e.DeletedAt)
		return e, err
	})
}

// CountTrash counts what can be restored.
func (s *Store) CountTrash(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM trash`).Scan(&n)
	return n, err
}

// PurgeTrash deletes one entry for good.
func (s *Store) PurgeTrash(ctx context.Context, id int64) error {
	return checkAffected(s.pool.Exec(ctx, `DELETE FROM trash WHERE id = $1`, id))
}

// EmptyTrash deletes everything in the trash for good.
func (s *Store) EmptyTrash(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM trash`)
	return err
}

// Restored says what a restore brought back: its kind and the id of the
// page to show (the item for a stock change or photo).
type Restored struct {
	Kind  string
	ID    int64
	Label string
}

// RestoreTrash puts a deleted thing back and removes it from the trash.
// Conflicts with what happened since (not enough stock, a name taken, the
// thing it belonged to deleted for good) fail with a clear error and leave
// everything as it was.
func (s *Store) RestoreTrash(ctx context.Context, id int64) (Restored, error) {
	var out Restored
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var raw string
		if err := tx.QueryRow(ctx, `SELECT kind, label, payload::text FROM trash WHERE id = $1 FOR UPDATE`, id).
			Scan(&out.Kind, &out.Label, &raw); err != nil {
			return notFound(err)
		}
		var p trashPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return err
		}
		var err error
		switch out.Kind {
		case TrashService:
			out.ID, err = restoreService(ctx, tx, p)
		case TrashKit:
			out.ID, err = restoreKit(ctx, tx, p)
		case TrashMovement:
			out.ID, err = restoreMovement(ctx, tx, p)
		case TrashShopping:
			out.ID, err = restoreShopping(ctx, tx, p)
		case TrashItem:
			out.ID, err = restoreItem(ctx, tx, p)
		case TrashVehicle:
			out.ID, err = restoreVehicle(ctx, tx, p)
		case TrashPhoto:
			out.ID, err = restorePhoto(ctx, tx, p)
		case TrashBought:
			out.ID, err = undoBought(ctx, tx, p)
		default:
			err = fmt.Errorf("unknown trash kind %q", out.Kind)
		}
		if err != nil {
			return restoreConflict(out.Label, err)
		}
		_, err = tx.Exec(ctx, `DELETE FROM trash WHERE id = $1`, id)
		return err
	})
	return out, err
}

// restoreConflict turns database errors of a restore into messages.
func restoreConflict(label string, err error) error {
	switch {
	case isForeignKeyViolation(err):
		return &ConflictError{Field: "form", Message: label + ": something it belonged to has been deleted for good."}
	case isUniqueViolation(err):
		return &ConflictError{Field: "form", Message: label + ": something with the same name or place exists now."}
	}
	return err
}

func one(raw json.RawMessage) (snapshotRow, error) {
	rows, err := decodeRows(raw)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("snapshot has %d rows, want 1", len(rows))
	}
	return rows[0], nil
}

func restoreService(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	rec, err := one(p["service_records"])
	if err != nil {
		return 0, err
	}
	// A kit deleted for good since: keep the service without it.
	if kit, ok := rec["kit_id"].(json.Number); ok {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM service_kits WHERE id = $1)`, kit.String()).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			rec["kit_id"] = nil
		}
	}
	if err := restoreRows(ctx, tx, "service_records", encodeRows([]snapshotRow{rec})); err != nil {
		return 0, err
	}
	moves, err := decodeRows(p["stock_movements"])
	if err != nil {
		return 0, err
	}
	if err := takeMovements(ctx, tx, moves); err != nil {
		return 0, err
	}
	id := rec.int64("id")
	var reading *int
	if km, ok := rec["odometer_km"].(json.Number); ok {
		n, _ := km.Int64()
		v := int(n)
		reading = &v
	}
	return id, moveOdometer(ctx, tx, id, rec.int64("vehicle_id"), reading)
}

func restoreKit(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	kit, err := one(p["service_kits"])
	if err != nil {
		return 0, err
	}
	if err := restoreRows(ctx, tx, "service_kits", encodeRows([]snapshotRow{kit})); err != nil {
		return 0, err
	}
	lines, err := decodeRows(p["service_kit_lines"])
	if err != nil {
		return 0, err
	}
	if lines, err = existing(ctx, tx, lines, "item_id", "items"); err != nil {
		return 0, err
	}
	return kit.int64("id"), restoreRows(ctx, tx, "service_kit_lines", encodeRows(lines))
}

func restoreMovement(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	m, err := one(p["stock_movements"])
	if err != nil {
		return 0, err
	}
	itemID := m.int64("item_id")
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM items WHERE id = $1`, itemID).Scan(&archived); err != nil {
		return 0, &ConflictError{Field: "form", Message: "Its item has been deleted for good."}
	}
	if archived {
		return 0, &ConflictError{Field: "form", Message: "Restore its item first."}
	}
	return itemID, takeMovements(ctx, tx, []snapshotRow{m})
}

func restoreShopping(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	e, err := one(p["shopping_entries"])
	if err != nil {
		return 0, err
	}
	if item, ok := e["item_id"].(json.Number); ok {
		var archived bool
		if err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM items WHERE id = $1 FOR SHARE`, item.String()).Scan(&archived); err != nil {
			return 0, &ConflictError{Field: "form", Message: "Its item has been deleted for good."}
		}
		if archived {
			return 0, &ConflictError{Field: "form", Message: "Its item is archived; restore it first."}
		}
		var listed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM shopping_entries WHERE item_id = $1)`, item.String()).Scan(&listed); err != nil {
			return 0, err
		}
		if listed {
			return 0, &ConflictError{Field: "form", Message: "It is on the shopping list again."}
		}
	}
	return e.int64("id"), restoreRows(ctx, tx, "shopping_entries", encodeRows([]snapshotRow{e}))
}

func restoreItem(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	it, err := one(p["items"])
	if err != nil {
		return 0, err
	}
	// The item comes back with its quantity and the movements that add up
	// to it, so the ledger holds without redoing them.
	for _, table := range []string{"items", "item_skus", "item_photos", "stock_movements"} {
		if err := restoreRows(ctx, tx, table, p[table]); err != nil {
			return 0, err
		}
	}
	tags, err := decodeRows(p["item_vehicles"])
	if err != nil {
		return 0, err
	}
	if tags, err = existing(ctx, tx, tags, "vehicle_id", "vehicles"); err != nil {
		return 0, err
	}
	if err := restoreRows(ctx, tx, "item_vehicles", encodeRows(tags)); err != nil {
		return 0, err
	}
	return it.int64("id"), restoreRows(ctx, tx, "shopping_entries", p["shopping_entries"])
}

func restoreVehicle(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	v, err := one(p["vehicles"])
	if err != nil {
		return 0, err
	}
	if err := restoreRows(ctx, tx, "vehicles", encodeRows([]snapshotRow{v})); err != nil {
		if isUniqueViolation(err) {
			return 0, &ConflictError{Field: "form", Message: fmt.Sprintf("A vehicle named %v exists now; rename it first.", v["name"])}
		}
		return 0, err
	}
	tags, err := decodeRows(p["item_vehicles"])
	if err != nil {
		return 0, err
	}
	if tags, err = existing(ctx, tx, tags, "item_id", "items"); err != nil {
		return 0, err
	}
	return v.int64("id"), restoreRows(ctx, tx, "item_vehicles", encodeRows(tags))
}

func restorePhoto(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	ph, err := one(p["item_photos"])
	if err != nil {
		return 0, err
	}
	itemID := ph.int64("item_id")
	var has bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM item_photos WHERE item_id = $1)`, itemID).Scan(&has); err != nil {
		return 0, err
	}
	if has {
		return 0, &ConflictError{Field: "form", Message: "The item has another photo now; remove it first."}
	}
	return itemID, restoreRows(ctx, tx, "item_photos", encodeRows([]snapshotRow{ph}))
}

// undoBought takes back a purchase made by ticking an entry off: the
// purchase is removed (refused if that stock was used since) and the entry
// is put back as it was.
func undoBought(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	e, err := one(p["shopping_entries"])
	if err != nil {
		return 0, err
	}
	var movementID int64
	if err := json.Unmarshal(p["movement_id"], &movementID); err != nil {
		return 0, err
	}
	m, err := lockMovement(ctx, tx, movementID)
	switch {
	case errors.Is(err, ErrNotFound):
		// The purchase was deleted meanwhile: only the entry comes back.
	case err != nil:
		return 0, err
	default:
		if err := reviseMovement(ctx, tx, m, 0); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM stock_movements WHERE id = $1`, movementID); err != nil {
			return 0, err
		}
	}
	// Bought in part, the entry is still there; otherwise it comes back.
	var qty *float64
	if _, ok := e["quantity"].(json.Number); ok {
		q := e.float("quantity")
		qty = &q
	}
	tag, err := tx.Exec(ctx, `UPDATE shopping_entries SET quantity = $2, note = $3 WHERE id = $1 OR item_id = $4`,
		e.int64("id"), qty, e["note"], e.int64("item_id"))
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() > 0 {
		return e.int64("id"), nil
	}
	return e.int64("id"), restoreRows(ctx, tx, "shopping_entries", encodeRows([]snapshotRow{e}))
}

// trashLabel formats labels like "Service: Oil service (KTM 390, 30 Oct 2026)".
func trashLabel(what, name string, details ...string) string {
	label := what + ": " + name
	var parts []string
	for _, d := range details {
		if d != "" {
			parts = append(parts, d)
		}
	}
	if len(parts) > 0 {
		label += " (" + strings.Join(parts, ", ") + ")"
	}
	return label
}

func day(t time.Time) string { return t.Format("2 Jan 2006") }
