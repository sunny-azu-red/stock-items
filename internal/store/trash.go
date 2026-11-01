package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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

// restoreRows inserts snapshot rows back into table with their ids. Only
// the columns in the snapshot are set, so columns added since get their
// defaults. A restored row of a versioned table comes back with a new
// version, so a form opened before it was deleted can't save over it (a
// snapshot without one gets the default, 1).
func restoreRows(ctx context.Context, tx pgx.Tx, table string, rows json.RawMessage) error {
	decoded, err := decodeRows(rows)
	if err != nil || len(decoded) == 0 {
		return err
	}
	if versioned(table) {
		for _, r := range decoded {
			if _, ok := r["version"].(json.Number); ok {
				r["version"] = json.Number(strconv.FormatInt(r.int64("version")+1, 10))
			}
		}
		rows = encodeRows(decoded)
	}
	var cols []string
	for _, r := range decoded {
		for k := range r {
			if c := (pgx.Identifier{k}).Sanitize(); !slices.Contains(cols, c) {
				cols = append(cols, c)
			}
		}
	}
	list := strings.Join(cols, ", ")
	_, err = tx.Exec(ctx, `INSERT INTO `+table+` (`+list+`) OVERRIDING SYSTEM VALUE
		SELECT `+list+` FROM jsonb_populate_recordset(NULL::`+table+`, $1::jsonb)`, string(rows))
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

// int64, float and string read a column, giving the zero value for a null
// or missing one.
func (r snapshotRow) int64(col string) int64 {
	n, _ := r[col].(json.Number)
	v, _ := n.Int64()
	return v
}

func (r snapshotRow) float(col string) float64 {
	n, _ := r[col].(json.Number)
	v, _ := n.Float64()
	return v
}

func (r snapshotRow) string(col string) string {
	s, _ := r[col].(string)
	return s
}

// inTrash matches a payload holding a row of table $1 whose column $2 is
// $3, in the containment form trash_payload_idx can serve.
const inTrash = `jsonb_build_object($1::text, jsonb_build_array(jsonb_build_object($2::text, $3::bigint)))`

// needRow checks that row id of table (items, vehicles or service_kits)
// exists. One that is in the trash must be restored first, so nothing is
// lost by restoring in the "wrong" order: that is a *ConflictError naming
// it. One deleted for good reports gone, for the caller to drop the link
// or refuse.
func needRow(ctx context.Context, tx pgx.Tx, table string, id int64) (gone bool, err error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	var label string
	err = tx.QueryRow(ctx, `
		SELECT label FROM trash WHERE payload @> `+inTrash+`
		ORDER BY id DESC LIMIT 1`, table, "id", id).Scan(&label)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, &ConflictError{Field: "form", Message: "Restore " + label + " first."}
}

// needRows is needRow for a row everything else depends on: gone for good
// is a *ConflictError too.
func needRows(ctx context.Context, tx pgx.Tx, table string, ids []int64, what string) error {
	for _, id := range ids {
		gone, err := needRow(ctx, tx, table, id)
		if err != nil {
			return err
		}
		if gone {
			return &ConflictError{Field: "form", Message: what + " has been permanently deleted."}
		}
	}
	return nil
}

// linked keeps the rows whose col refers to a row of table. Links to rows
// deleted for good are dropped and counted in *dropped; one to a row in
// the trash stops the restore until that row is restored (see needRow).
func linked(ctx context.Context, tx pgx.Tx, rows []snapshotRow, col, table string, dropped *int) ([]snapshotRow, error) {
	var out []snapshotRow
	for _, r := range rows {
		gone, err := needRow(ctx, tx, table, r.int64(col))
		if err != nil {
			return nil, err
		}
		if gone {
			*dropped++
			continue
		}
		out = append(out, r)
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
	if err := tx.QueryRow(ctx, `SELECT quantity, name, brand, unit FROM items WHERE id = $1 FOR NO KEY UPDATE`, itemID).
		Scan(&qty, &name, &brand, &unit); err != nil {
		return notFound(err)
	}
	next := stock.RoundQty(qty + delta)
	if next < 0 {
		return &InsufficientStockError{ItemID: itemID, ItemName: displayName(brand, name), Unit: unit, Have: qty, Need: -delta}
	}
	if next > stock.MaxQuantity {
		return &ConflictError{Field: "form", Message: displayName(brand, name) + " would have more than " + stock.FormatQuantity(stock.MaxQuantity, unit)}
	}
	// No new version, like applyMovement.
	_, err := tx.Exec(ctx, `UPDATE items SET quantity = $2, updated_at = now() WHERE id = $1`, itemID, next)
	return err
}

// lockItems locks items in id order, so restores can't deadlock with
// services or each other.
func lockItems(ctx context.Context, tx pgx.Tx, ids []int64) error {
	_, err := lockedItems(ctx, tx, ids)
	return err
}

// lockedItems is lockItems returning the ids it locked: an item deleted
// meanwhile is missing.
func lockedItems(ctx context.Context, tx pgx.Tx, ids []int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM items WHERE id = ANY($1) ORDER BY id FOR NO KEY UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// lostItems explains items that needRows found but their lock didn't:
// deleted meanwhile, they are in the trash or gone for good (a
// *ConflictError naming them), rather than make the restore look gone.
func lostItems(ctx context.Context, tx pgx.Tx, ids []int64, what string) error {
	if err := needRows(ctx, tx, "items", ids, what); err != nil {
		return err
	}
	return &ConflictError{Field: "form", Message: what + " was changed meanwhile. Try again."}
}

// refuseArchived fails if any of the items is archived: stock changes come
// back only to items in use.
func refuseArchived(ctx context.Context, tx pgx.Tx, ids []int64) error {
	var brand, name string
	err := tx.QueryRow(ctx, `SELECT brand, name FROM items WHERE id = ANY($1) AND archived_at IS NOT NULL ORDER BY id LIMIT 1`, ids).
		Scan(&brand, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return archivedError(brand, name)
}

// takeMovements applies the stock effect of snapshot movements and puts
// them back. Every item that would go below zero is reported at once.
// Archived items are refused unless ofService: a service's parts move
// with it, as when it is edited or deleted.
func takeMovements(ctx context.Context, tx pgx.Tx, moves []snapshotRow, ofService bool) error {
	var ids []int64
	for _, m := range moves {
		ids = append(ids, m.int64("item_id"))
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if err := needRows(ctx, tx, "items", ids, "An item it used"); err != nil {
		return err
	}
	locked, err := lockedItems(ctx, tx, ids)
	if err != nil {
		return err
	}
	if len(locked) < len(ids) {
		var lost []int64
		for _, id := range ids {
			if !slices.Contains(locked, id) {
				lost = append(lost, id)
			}
		}
		return lostItems(ctx, tx, lost, "An item it used")
	}
	if !ofService {
		if err := refuseArchived(ctx, tx, ids); err != nil {
			return err
		}
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

// TrashThumb returns the thumbnail of a photo in the trash, from its
// snapshot, so the trash page can tell several photos of one item apart.
// A trash entry never changes, so the result can be cached for good.
func (s *Store) TrashThumb(ctx context.Context, id int64) (Photo, error) {
	var p Photo
	var thumb string
	err := s.pool.QueryRow(ctx, `
		SELECT ph ->> 'content_type', ph ->> 'thumb', t.deleted_at
		FROM trash t, jsonb_array_elements(t.payload -> 'item_photos') ph
		WHERE t.id = $1 AND t.kind = $2 AND ph ->> 'thumb' IS NOT NULL LIMIT 1`, id, TrashPhoto).
		Scan(&p.ContentType, &thumb, &p.UpdatedAt)
	if err != nil {
		return p, notFound(err)
	}
	// to_jsonb writes bytea as "\x" and hex digits.
	p.Data, err = decodeHexBytea(thumb)
	return p, err
}

// decodeHexBytea reads bytea in Postgres' hex output format.
func decodeHexBytea(s string) ([]byte, error) {
	if !strings.HasPrefix(s, `\x`) {
		return nil, fmt.Errorf("bytea %.10q is not in hex format", s)
	}
	return hex.DecodeString(s[2:])
}

// CountTrash counts what can be restored.
func (s *Store) CountTrash(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM trash`).Scan(&n)
	return n, err
}

// hardLinks are the columns that tie a trash entry to an item or vehicle
// it can't be restored without (kit lines and tags are dropped instead).
var hardLinks = map[string][][2]string{
	"items":    {{"stock_movements", "item_id"}, {"item_photos", "item_id"}, {"shopping_entries", "item_id"}},
	"vehicles": {{"service_records", "vehicle_id"}, {"service_kits", "vehicle_id"}},
}

// PurgeTrash deletes one entry for good, together with the entries that
// could only be restored with it: purging an item takes its deleted stock
// changes and list entries along, a vehicle its deleted services and
// kits. others counts those. Only items and vehicles are needed that way,
// so one level is all there is.
//
// The entry and those that go along are locked in id order before any is
// deleted, as EmptyTrash locks, so the two can't deadlock.
func (s *Store) PurgeTrash(ctx context.Context, id int64) (others int, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var raw string
		if err := tx.QueryRow(ctx, `SELECT payload::text FROM trash WHERE id = $1`, id).Scan(&raw); err != nil {
			return notFound(err)
		}
		var p trashPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return err
		}
		// What goes along: payloads holding a row that refers to the
		// purged item or vehicle (trash rows never change, so this holds).
		var along []string
		var args []any
		for _, parent := range []string{"items", "vehicles"} {
			rows, err := decodeRows(p[parent])
			if err != nil {
				return err
			}
			for _, r := range rows {
				for _, link := range hardLinks[parent] {
					n := len(args)
					along = append(along, fmt.Sprintf(`payload @> jsonb_build_object($%d::text, jsonb_build_array(jsonb_build_object($%d::text, $%d::bigint)))`,
						n+2, n+3, n+4))
					args = append(args, link[0], link[1], r.int64("id"))
				}
			}
		}
		where := "id = $1"
		if len(along) > 0 {
			where += " OR " + strings.Join(along, " OR ")
		}
		rows, err := tx.Query(ctx, `SELECT id FROM trash WHERE `+where+` ORDER BY id FOR UPDATE`, append([]any{id}, args...)...)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if !slices.Contains(ids, id) {
			return ErrNotFound // restored or purged meanwhile
		}
		others = len(ids) - 1
		_, err = tx.Exec(ctx, `DELETE FROM trash WHERE id = ANY($1)`, ids)
		return err
	})
	return others, err
}

// EmptyTrash deletes everything in the trash for good. It locks the rows
// in id order first, like PurgeTrash.
func (s *Store) EmptyTrash(ctx context.Context) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM trash ORDER BY id FOR UPDATE`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM trash`)
		return err
	})
}

// Restored says what a restore brought back: its kind and the id of the
// page to show (the item for a stock change or photo). Dropped counts the
// links it lost to things deleted for good since: kit lines and tags, or
// a service's kit. Replaced is the trash id of the photo a restored photo
// took the place of (0 when the item had none).
type Restored struct {
	Kind     string
	ID       int64
	Label    string
	Name     string // a restored photo's item, as named now
	Dropped  int
	Replaced int64
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
		if err := checkSnapshotAmounts(p); err != nil {
			return err
		}
		var err error
		switch out.Kind {
		case TrashService:
			out.ID, err = restoreService(ctx, tx, p, &out.Dropped)
		case TrashKit:
			out.ID, err = restoreKit(ctx, tx, p, &out.Dropped)
		case TrashMovement:
			out.ID, err = restoreMovement(ctx, tx, p)
		case TrashShopping:
			out.ID, err = restoreShopping(ctx, tx, p)
		case TrashItem:
			out.ID, err = restoreItem(ctx, tx, p, &out.Dropped)
		case TrashVehicle:
			out.ID, err = restoreVehicle(ctx, tx, p, &out.Dropped)
		case TrashPhoto:
			out.ID, out.Replaced, out.Name, err = restorePhoto(ctx, tx, p)
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

// amountColumns are the snapshot columns holding amounts: numeric(12,2)
// since migration 00012.
var amountColumns = []string{"quantity", "min_quantity", "delta", "pack_size"}

// checkSnapshotAmounts refuses a snapshot holding an amount finer than
// 0.01. Restoring casts the numbers into the columns' type, which would
// round it, and the stock is moved by the unrounded delta: the ledger
// could stop adding up. Migration 00012 made sure no snapshot holds one,
// and snapshots since come from two-decimal columns; this keeps it so.
func checkSnapshotAmounts(p trashPayload) error {
	for _, raw := range p {
		rows, err := decodeRows(raw)
		if err != nil {
			continue // not a table's rows (e.g. movement_id)
		}
		for _, r := range rows {
			for _, col := range amountColumns {
				n, ok := r[col].(json.Number)
				if !ok {
					continue
				}
				if f, err := n.Float64(); err != nil || stock.CheckQuantity(f, stock.UnitML) != nil {
					return &ConflictError{Field: "form", Message: "It holds an amount finer than 0.01 (" + n.String() +
						"), which Pitbox doesn't store, so it can't be restored."}
				}
			}
		}
	}
	return nil
}

// restoreConflict turns database errors of a restore into messages.
func restoreConflict(label string, err error) error {
	switch {
	case isForeignKeyViolation(err):
		return &ConflictError{Field: "form", Message: label + ": something it belonged to is gone."}
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

func restoreService(ctx context.Context, tx pgx.Tx, p trashPayload, dropped *int) (int64, error) {
	rec, err := one(p["service_records"])
	if err != nil {
		return 0, err
	}
	vehicleID := rec.int64("vehicle_id")
	if err := needRows(ctx, tx, "vehicles", []int64{vehicleID}, "Its vehicle"); err != nil {
		return 0, err
	}
	// The vehicle's lock before the parts, like every service change. An
	// archived vehicle is fine: bringing back a past service isn't new work.
	if _, _, err := lockVehicle(ctx, tx, vehicleID); err != nil {
		return 0, err
	}
	// Its reading must still fit between the services logged since.
	performed, err := time.Parse(time.DateOnly, rec.string("performed_on"))
	if err != nil {
		return 0, err
	}
	var reading *int
	if km, ok := rec["odometer_km"].(json.Number); ok {
		n, _ := km.Int64()
		v := int(n)
		reading = &v
	}
	in := stock.ServiceInput{VehicleID: vehicleID, PerformedOn: performed, OdometerKm: reading}
	if err := checkServiceOdometer(ctx, tx, in, 0); err != nil {
		return 0, err
	}
	// A kit deleted for good since: keep the service without it.
	if _, ok := rec["kit_id"].(json.Number); ok {
		gone, err := needRow(ctx, tx, "service_kits", rec.int64("kit_id"))
		if err != nil {
			return 0, err
		}
		if gone {
			rec["kit_id"] = nil
			*dropped++
		}
	}
	if err := restoreRows(ctx, tx, "service_records", encodeRows([]snapshotRow{rec})); err != nil {
		return 0, err
	}
	moves, err := decodeRows(p["stock_movements"])
	if err != nil {
		return 0, err
	}
	if err := takeMovements(ctx, tx, moves, true); err != nil {
		return 0, err
	}
	id := rec.int64("id")
	return id, moveOdometer(ctx, tx, id, vehicleID, reading)
}

func restoreKit(ctx context.Context, tx pgx.Tx, p trashPayload, dropped *int) (int64, error) {
	kit, err := one(p["service_kits"])
	if err != nil {
		return 0, err
	}
	// An archived vehicle is fine: the kit comes back as it was, and new
	// services for that vehicle stay refused.
	if err := needRows(ctx, tx, "vehicles", []int64{kit.int64("vehicle_id")}, "Its vehicle"); err != nil {
		return 0, err
	}
	if err := restoreRows(ctx, tx, "service_kits", encodeRows([]snapshotRow{kit})); err != nil {
		return 0, err
	}
	lines, err := decodeRows(p["service_kit_lines"])
	if err != nil {
		return 0, err
	}
	if lines, err = linked(ctx, tx, lines, "item_id", "items", dropped); err != nil {
		return 0, err
	}
	// Hold the items FOR SHARE, like saving a kit, so none changes unit
	// past the lines coming back. Their units can't have changed while the
	// lines were in the trash (UpdateItem checks the trash too).
	ids := make([]int64, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.int64("item_id"))
	}
	if _, err := shareItems(ctx, tx, ids); err != nil {
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
	if err := needRows(ctx, tx, "items", []int64{itemID}, "Its item"); err != nil {
		return 0, err
	}
	return itemID, takeMovements(ctx, tx, []snapshotRow{m}, false)
}

func restoreShopping(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	e, err := one(p["shopping_entries"])
	if err != nil {
		return 0, err
	}
	if item, ok := e["item_id"].(json.Number); ok {
		if err := needRows(ctx, tx, "items", []int64{e.int64("item_id")}, "Its item"); err != nil {
			return 0, err
		}
		var archived bool
		err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM items WHERE id = $1 FOR SHARE`, item.String()).Scan(&archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, lostItems(ctx, tx, []int64{e.int64("item_id")}, "Its item")
		}
		if err != nil {
			return 0, err
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

func restoreItem(ctx context.Context, tx pgx.Tx, p trashPayload, dropped *int) (int64, error) {
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
	if tags, err = linked(ctx, tx, tags, "vehicle_id", "vehicles", dropped); err != nil {
		return 0, err
	}
	if err := restoreRows(ctx, tx, "item_vehicles", encodeRows(tags)); err != nil {
		return 0, err
	}
	return it.int64("id"), restoreRows(ctx, tx, "shopping_entries", p["shopping_entries"])
}

func restoreVehicle(ctx context.Context, tx pgx.Tx, p trashPayload, dropped *int) (int64, error) {
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
	// The tagged items after the vehicle and before looking for them, in id
	// order: none can be deleted between the look and its tag coming back.
	ids := make([]int64, 0, len(tags))
	for _, t := range tags {
		ids = append(ids, t.int64("item_id"))
	}
	slices.Sort(ids)
	if err := lockItems(ctx, tx, slices.Compact(ids)); err != nil {
		return 0, err
	}
	if tags, err = linked(ctx, tx, tags, "item_id", "items", dropped); err != nil {
		return 0, err
	}
	ids = ids[:0]
	for _, t := range tags {
		ids = append(ids, t.int64("item_id"))
	}
	// Tags belong to the item's version: a new one, so an edit form opened
	// while the vehicle was in the trash can't save over the tag.
	if _, err := tx.Exec(ctx, `UPDATE items SET version = version + 1 WHERE id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	return v.int64("id"), restoreRows(ctx, tx, "item_vehicles", encodeRows(tags))
}

func restorePhoto(ctx context.Context, tx pgx.Tx, p trashPayload) (itemID, replaced int64, name string, err error) {
	ph, err := one(p["item_photos"])
	if err != nil {
		return 0, 0, "", err
	}
	itemID = ph.int64("item_id")
	if err := needRows(ctx, tx, "items", []int64{itemID}, "Its item"); err != nil {
		return 0, 0, "", err
	}
	// A photo the item has now trades places with it, so neither is lost
	// (this is how replacing a photo is undone). trashPhoto locks the item.
	replaced, err = trashPhoto(ctx, tx, itemID, "replaced")
	if errors.Is(err, ErrNotFound) {
		return 0, 0, "", lostItems(ctx, tx, []int64{itemID}, "Its item")
	}
	if err != nil {
		return 0, 0, "", err
	}
	var brand, item string
	if err := tx.QueryRow(ctx, `SELECT brand, name FROM items WHERE id = $1`, itemID).Scan(&brand, &item); err != nil {
		return 0, 0, "", err
	}
	return itemID, replaced, displayName(brand, item), restoreRows(ctx, tx, "item_photos", encodeRows([]snapshotRow{ph}))
}

// undoBought takes back a purchase made by ticking an entry off: the
// purchase is removed (refused if that stock was used since) and the
// entry asks for that amount again.
func undoBought(ctx context.Context, tx pgx.Tx, p trashPayload) (int64, error) {
	e, err := one(p["shopping_entries"])
	if err != nil {
		return 0, err
	}
	var movementID int64
	if err := json.Unmarshal(p["movement_id"], &movementID); err != nil {
		return 0, err
	}
	// Undone, the purchase was never made: the Bought form it came from may
	// be saved again (if still current), rather than answer "saved already".
	if raw, ok := p["form_key"]; ok {
		var key string
		if err := json.Unmarshal(raw, &key); err != nil {
			return 0, err
		}
		if err := freeFormKey(ctx, tx, key); err != nil {
			return 0, err
		}
	}
	entryID, itemID := e.int64("id"), e.int64("item_id")
	if err := needRows(ctx, tx, "items", []int64{itemID}, "Its item"); err != nil {
		return 0, err
	}
	// The entry before the item, like MarkBought, so the two can't deadlock.
	var current int64
	var listed *float64
	err = tx.QueryRow(ctx, `SELECT id, quantity FROM shopping_entries WHERE id = $1 OR item_id = $2 FOR UPDATE`, entryID, itemID).
		Scan(&current, &listed)
	onList := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	var archived bool
	err = tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM items WHERE id = $1 FOR NO KEY UPDATE`, itemID).Scan(&archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, lostItems(ctx, tx, []int64{itemID}, "Its item")
	}
	if err != nil {
		return 0, err
	}
	if archived {
		return 0, &ConflictError{Field: "form", Message: "Its item is archived; restore it first."}
	}
	var undone *float64 // how much the purchase added, if it still exists
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
		undone = &m.Delta
	}
	var before *float64
	if _, ok := e["quantity"].(json.Number); ok {
		q := e.float("quantity")
		before = &q
	}
	if onList {
		// Still listed (bought in part, or listed again): it needs what the
		// purchase covered once more.
		if listed != nil && undone != nil {
			// It asks for more again: a new version.
			_, err := tx.Exec(ctx, `UPDATE shopping_entries SET quantity = LEAST(quantity + $2, $3), version = version + 1 WHERE id = $1`,
				current, *undone, stock.MaxQuantity)
			return current, err
		}
		return current, nil
	}
	// Gone from the list: it comes back asking for what this purchase
	// covered (no more than it asked for then).
	if before != nil && undone != nil && *undone < *before {
		e["quantity"] = json.Number(strconv.FormatFloat(*undone, 'f', -1, 64))
	}
	return entryID, restoreRows(ctx, tx, "shopping_entries", encodeRows([]snapshotRow{e}))
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
