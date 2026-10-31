package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// ItemFilter narrows ListItems. The zero value returns everything.
type ItemFilter struct {
	IDs        []int64
	VehicleIDs []int64
	// MatchAll requires every vehicle in VehicleIDs (default: any of them).
	MatchAll bool
	// General includes untagged items. On its own it returns only untagged
	// items; with VehicleIDs it adds them to the result.
	General  bool
	Query    string
	Category string
	LowStock bool
	Expiring bool // with stock left and expired or expiring within ExpiringDays
	InStock  bool // quantity > 0
	Sort     string
	// Today is the date "expiring" is counted from (default: the database's
	// current date).
	Today time.Time
	// Archived chooses between active items ("", the default), archived
	// ones (ArchivedOnly) or both (ArchivedAll). Lookups by IDs always
	// include archived items.
	Archived string
}

// Values of ItemFilter.Archived.
const (
	ArchivedOnly = "only"
	ArchivedAll  = "all"
)

// ExpiringDays is the look-ahead used for the "expiring soon" filter.
const ExpiringDays = 60

// Sort orders understood by ListItems.
const (
	SortName     = "name"
	SortCategory = "category"
	SortRecent   = "recent"
	SortQuantity = "quantity"
)

const itemSelect = `
SELECT i.id, i.name, i.brand, i.category, i.unit, i.quantity, i.min_quantity, i.expires_on, i.location,
       i.notes, i.created_at, i.updated_at, i.archived_at, i.version,
       COALESCE((SELECT array_agg(iv.vehicle_id ORDER BY iv.vehicle_id) FROM item_vehicles iv WHERE iv.item_id = i.id), '{}'),
       COALESCE((SELECT json_agg(json_build_object('id', s.id, 'code', s.code, 'kind', s.kind, 'label', s.label,
                                                   'pack_size', s.pack_size) ORDER BY s.id)
                 FROM item_skus s WHERE s.item_id = i.id), '[]'),
       EXISTS (SELECT 1 FROM item_photos p WHERE p.item_id = i.id)
FROM items i`

func scanItem(row pgx.Row) (stock.Item, error) {
	var it stock.Item
	err := row.Scan(&it.ID, &it.Name, &it.Brand, &it.Category, &it.Unit, &it.Quantity, &it.MinQuantity,
		&it.ExpiresOn, &it.Location, &it.Notes, &it.CreatedAt, &it.UpdatedAt, &it.ArchivedAt, &it.Version, &it.VehicleIDs, &it.SKUs, &it.HasPhoto)
	return it, err
}

// ListItems returns the items matching f.
func (s *Store) ListItems(ctx context.Context, f ItemFilter) ([]stock.Item, error) {
	var args sqlArgs
	var where []string

	if len(f.IDs) > 0 {
		where = append(where, "i.id = ANY("+args.add(f.IDs)+")")
	}
	untagged := "NOT EXISTS (SELECT 1 FROM item_vehicles iv WHERE iv.item_id = i.id)"
	switch {
	case len(f.VehicleIDs) > 0 && f.MatchAll:
		p := args.add(f.VehicleIDs)
		cond := "(SELECT count(*) FROM item_vehicles iv WHERE iv.item_id = i.id AND iv.vehicle_id = ANY(" + p + ")) = cardinality(" + p + "::bigint[])"
		if f.General {
			cond = "(" + cond + " OR " + untagged + ")"
		}
		where = append(where, cond)
	case len(f.VehicleIDs) > 0:
		cond := "EXISTS (SELECT 1 FROM item_vehicles iv WHERE iv.item_id = i.id AND iv.vehicle_id = ANY(" + args.add(f.VehicleIDs) + "))"
		if f.General {
			cond = "(" + cond + " OR " + untagged + ")"
		}
		where = append(where, cond)
	case f.General:
		where = append(where, untagged)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		p := args.add(likePattern(q))
		cond := "i.name ILIKE " + p + " OR i.brand ILIKE " + p + " OR i.category ILIKE " + p +
			" OR i.location ILIKE " + p + " OR i.notes ILIKE " + p +
			" OR (i.brand || ' ' || i.name) ILIKE " + p +
			" OR EXISTS (SELECT 1 FROM item_skus s WHERE s.item_id = i.id AND (s.code ILIKE " + p + " OR s.label ILIKE " + p
		if norm := normalizeCode(q); norm != "" {
			cond += " OR regexp_replace(lower(s.code), '[^a-z0-9]', '', 'g') LIKE " + args.add("%"+norm+"%")
		}
		where = append(where, "("+cond+")))")
	}
	if c := strings.TrimSpace(f.Category); c != "" {
		where = append(where, "lower(i.category) = lower("+args.add(c)+")")
	}
	if f.LowStock {
		where = append(where, "i.min_quantity IS NOT NULL AND i.quantity < i.min_quantity")
	}
	if f.Expiring {
		today := "current_date"
		if !f.Today.IsZero() {
			today = args.add(stock.DateOnly(f.Today)) + "::date"
		}
		where = append(where, "i.quantity > 0 AND i.expires_on IS NOT NULL AND i.expires_on <= "+today+" + "+args.add(ExpiringDays)+"::int")
	}
	if f.InStock {
		where = append(where, "i.quantity > 0")
	}
	switch {
	case f.Archived == ArchivedOnly:
		where = append(where, "i.archived_at IS NOT NULL")
	case f.Archived == ArchivedAll || len(f.IDs) > 0:
	default:
		where = append(where, "i.archived_at IS NULL")
	}

	sql := itemSelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	nameOrder := "lower(btrim(i.brand || ' ' || i.name)), i.id"
	switch f.Sort {
	case SortCategory:
		sql += " ORDER BY lower(i.category) = '', lower(i.category), " + nameOrder
	case SortRecent:
		sql += " ORDER BY i.updated_at DESC, i.id DESC"
	case SortQuantity:
		// Pieces, millilitres and grams don't compare: group by unit first.
		sql += " ORDER BY i.unit, i.quantity, " + nameOrder
	default:
		sql += " ORDER BY " + nameOrder
	}

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.Item, error) { return scanItem(r) })
}

// GetItem returns one item with its SKUs and vehicle tags.
func (s *Store) GetItem(ctx context.Context, id int64) (stock.Item, error) {
	items, err := s.ListItems(ctx, ItemFilter{IDs: []int64{id}})
	if err != nil {
		return stock.Item{}, err
	}
	if len(items) == 0 {
		return stock.Item{}, ErrNotFound
	}
	return items[0], nil
}

// CreateItem stores a new item. Its starting quantity is recorded as a
// purchase or as "initial" stock (see stock.ItemInput.StartMovement).
func (s *Store) CreateItem(ctx context.Context, in stock.ItemInput) (int64, error) {
	var id int64
	err := s.inFormTx(ctx, in.FormKey, WhatItem, &id, func(tx pgx.Tx) error {
		var err error
		id, err = createItem(ctx, tx, in)
		return err
	})
	return id, err
}

// CreateItemFromShopping stores a new item bought from a free-text shopping
// list entry and removes that entry, in one transaction. It returns
// ErrNotFound when the entry is gone or is linked to an existing item.
func (s *Store) CreateItemFromShopping(ctx context.Context, entryID int64, in stock.ItemInput) (int64, error) {
	var id int64
	err := s.inFormTx(ctx, in.FormKey, WhatItem, &id, func(tx pgx.Tx) error {
		var itemID *int64
		if err := tx.QueryRow(ctx, `SELECT item_id FROM shopping_entries WHERE id = $1 FOR UPDATE`, entryID).Scan(&itemID); err != nil {
			return notFound(err)
		}
		if itemID != nil {
			return ErrNotFound
		}
		var err error
		if id, err = createItem(ctx, tx, in); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM shopping_entries WHERE id = $1`, entryID)
		return err
	})
	return id, err
}

// createItem inserts an item with its starting stock, SKUs and tags.
func createItem(ctx context.Context, tx pgx.Tx, in stock.ItemInput) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO items (name, brand, category, unit, min_quantity, expires_on, location, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		in.Name, in.Brand, in.Category, in.Unit, in.MinQuantity, in.ExpiresOn, in.Location, in.Notes,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	if in.Quantity > 0 {
		if _, err := applyMovement(ctx, tx, in.StartMovement(id)); err != nil {
			return 0, err
		}
	}
	return id, replaceSKUsAndTags(ctx, tx, id, in)
}

// UpdateItem replaces an item's fields, SKUs and tags. A changed quantity
// is recorded as a "correction" movement; when in.QuantityWas says the
// quantity field was left as it was, the stock is not touched (it may have
// changed since the form was opened). Changing the unit is refused once the
// item has history or is used in a kit or on the shopping list, as those
// amounts would no longer make sense, and so is a change while any of
// those is in the trash, as it would come back in the old unit. A
// correction is dated on. An item changed since in.Version fails with a
// *StaleError.
func (s *Store) UpdateItem(ctx context.Context, id int64, in stock.ItemInput, on time.Time) error {
	return s.inFormTx(ctx, in.FormKey, WhatItem, &id, func(tx pgx.Tx) error {
		var qty float64
		var unit stock.Unit
		var archived bool
		var version int
		err := tx.QueryRow(ctx, `SELECT quantity, unit, archived_at IS NOT NULL, version FROM items WHERE id = $1 FOR NO KEY UPDATE`, id).
			Scan(&qty, &unit, &archived, &version)
		if err := lockedVersion(ctx, tx, err, version, in.Version, WhatItem, id); err != nil {
			return err
		}
		quantityEdited := in.QuantityWas == nil || in.Quantity != *in.QuantityWas
		if archived && quantityEdited {
			return &ConflictError{Field: "quantity", Message: "Restore this item to change its stock"}
		}
		if unit != in.Unit {
			var used bool
			err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM stock_movements WHERE item_id = $1)
				    OR EXISTS (SELECT 1 FROM service_kit_lines WHERE item_id = $1)
				    OR EXISTS (SELECT 1 FROM shopping_entries WHERE item_id = $1 AND quantity IS NOT NULL)
				    -- What is in the trash comes back in the unit it was recorded in.
				    OR EXISTS (SELECT 1 FROM trash t WHERE t.payload @> jsonb_build_object('stock_movements', jsonb_build_array(jsonb_build_object('item_id', $1::bigint)))
				                                     OR t.payload @> jsonb_build_object('service_kit_lines', jsonb_build_array(jsonb_build_object('item_id', $1::bigint))))
				    OR EXISTS (SELECT 1 FROM trash t, jsonb_array_elements(t.payload -> 'shopping_entries') e
				               WHERE t.payload @> jsonb_build_object('shopping_entries', jsonb_build_array(jsonb_build_object('item_id', $1::bigint)))
				                 AND (e ->> 'item_id')::bigint = $1 AND e -> 'quantity' <> 'null'::jsonb)`, id).Scan(&used)
			if err != nil {
				return err
			}
			if used {
				return &ConflictError{Field: "unit", Message: "The unit cannot be changed once the item has stock history or is used in a kit or on the shopping list, including in Recently deleted"}
			}
		}
		// A new version: a form opened before this save is stale now.
		_, err = tx.Exec(ctx, `
			UPDATE items SET name = $2, brand = $3, category = $4, unit = $5, min_quantity = $6, expires_on = $7,
			       location = $8, notes = $9, updated_at = now(), version = version + 1
			WHERE id = $1`,
			id, in.Name, in.Brand, in.Category, in.Unit, in.MinQuantity, in.ExpiresOn, in.Location, in.Notes)
		if err != nil {
			return err
		}
		if quantityEdited {
			if _, err := correctTo(ctx, tx, id, qty, in.Quantity, "Edited quantity", on); err != nil {
				return err
			}
		}
		return replaceSKUsAndTags(ctx, tx, id, in)
	})
}

func replaceSKUsAndTags(ctx context.Context, tx pgx.Tx, id int64, in stock.ItemInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM item_skus WHERE item_id = $1`, id); err != nil {
		return err
	}
	for _, sku := range in.SKUs {
		_, err := tx.Exec(ctx, `INSERT INTO item_skus (item_id, code, kind, label, pack_size) VALUES ($1, $2, $3, $4, $5)`,
			id, sku.Code, sku.Kind, sku.Label, sku.PackSize)
		if isUniqueViolation(err) {
			return &ConflictError{Field: "skus", Message: "SKU " + sku.Code + " is listed twice"}
		}
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM item_vehicles WHERE item_id = $1`, id); err != nil {
		return err
	}
	if len(in.VehicleIDs) > 0 {
		_, err := tx.Exec(ctx, `
			INSERT INTO item_vehicles (item_id, vehicle_id)
			SELECT $1, v.id FROM vehicles v WHERE v.id = ANY($2)`, id, in.VehicleIDs)
		if isForeignKeyViolation(err) {
			// A vehicle deleted after this statement saw it: the tag's key
			// check waited for the delete and then found it gone.
			return &ConflictError{Field: "form", Message: "A vehicle you picked was deleted meanwhile. Check the vehicles and save again."}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ItemUsage counts the records that refer to an item.
func (s *Store) ItemUsage(ctx context.Context, id int64) (stock.ItemUsage, error) {
	return itemUsage(ctx, s.pool, id)
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func itemUsage(ctx context.Context, q queryRower, id int64) (stock.ItemUsage, error) {
	var u stock.ItemUsage
	err := q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM stock_movements WHERE item_id = $1 AND kind = 'purchase'),
		       (SELECT count(DISTINCT service_record_id) FROM stock_movements WHERE item_id = $1 AND kind = 'service'),
		       (SELECT count(*) FROM service_kit_lines WHERE item_id = $1)`, id).
		Scan(&u.Purchases, &u.Services, &u.Kits)
	return u, err
}

// DeleteItem removes an item with its own history (initial stock, uses,
// counts), SKUs, tags and photo. An item that other records refer to
// (purchases, services, kits) is not deleted, as that would change them;
// it fails with a *ConflictError and should be archived instead. An item
// changed since version fails with a *StaleError.
func (s *Store) DeleteItem(ctx context.Context, id int64, version int) (trashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// The shopping entry the delete cascades to before the item, like
		// MarkBought, so the two can't deadlock.
		if _, err := tx.Exec(ctx, `SELECT id FROM shopping_entries WHERE item_id = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		var name, brand string
		var have int
		// The item before the stock changes the delete cascades to, as
		// editing or deleting one of them locks (lockMovement).
		err := tx.QueryRow(ctx, `SELECT name, brand, version FROM items WHERE id = $1 FOR UPDATE`, id).Scan(&name, &brand, &have)
		if err := lockedVersion(ctx, tx, err, have, version, WhatItem, id); err != nil {
			return err
		}
		u, err := itemUsage(ctx, tx, id)
		if err != nil {
			return err
		}
		if u.InUse() {
			return &ConflictError{Field: "form", Message: displayName(brand, name) + " is used in " + u.String() + ". Archive it instead."}
		}
		p, err := snapshot(ctx, tx, id, [2]string{"items", "id"}, [2]string{"item_skus", "item_id"},
			[2]string{"item_vehicles", "item_id"}, [2]string{"item_photos", "item_id"},
			[2]string{"stock_movements", "item_id"}, [2]string{"shopping_entries", "item_id"})
		if err != nil {
			return err
		}
		if trashID, err = putTrash(ctx, tx, TrashItem, trashLabel("Item", displayName(brand, name)), p); err != nil {
			return err
		}
		return checkAffected(tx.Exec(ctx, `DELETE FROM items WHERE id = $1`, id))
	})
	return trashID, err
}

// ArchiveItem hides an item from stock, pickers and suggestions while
// keeping its history, services, kits and spending. Its shopping list
// entry, if any, goes to the trash; listTrashID is that trash entry (0
// when it wasn't on the list). An item changed since version fails with
// a *StaleError.
func (s *Store) ArchiveItem(ctx context.Context, id int64, version int) (listTrashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// The entry before the item, like MarkBought, so the two can't deadlock.
		if _, err := tx.Exec(ctx, `SELECT id FROM shopping_entries WHERE item_id = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		// Archiving changes the item's page: a new version.
		tag, err := tx.Exec(ctx, `UPDATE items SET archived_at = COALESCE(archived_at, now()), version = version + 1
			WHERE id = $1 AND version = $2`, id, version)
		if err := versionedUpdate(ctx, tx, tag, err, WhatItem, id); err != nil {
			return err
		}
		// Looked up again under the item's lock: an AddShopping that held
		// the item has committed its entry by now.
		var entryID int64
		err = tx.QueryRow(ctx, `SELECT id FROM shopping_entries WHERE item_id = $1 FOR UPDATE`, id).Scan(&entryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if listTrashID, err = trashShopping(ctx, tx, entryID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM shopping_entries WHERE id = $1`, entryID)
		return err
	})
	return listTrashID, err
}

// RestoreItem brings an archived item back. An item changed since
// version fails with a *StaleError.
func (s *Store) RestoreItem(ctx context.Context, id int64, version int) error {
	// Restoring changes the item's page: a new version.
	tag, err := s.pool.Exec(ctx, `UPDATE items SET archived_at = NULL, version = version + 1 WHERE id = $1 AND version = $2`, id, version)
	return versionedUpdate(ctx, s.pool, tag, err, WhatItem, id)
}

// Categories returns the distinct categories in use, plus common defaults.
func (s *Store) Categories(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, `
		SELECT DISTINCT ON (lower(c)) c FROM (
			SELECT category AS c, 0 AS pref FROM items WHERE category <> ''
			UNION SELECT unnest(ARRAY['Oil & fluids', 'Filters', 'Brakes', 'Gaskets & O-rings', 'Chain & sprockets',
			                          'Electrical', 'Tyres', 'Cleaning & care', 'Hardware', 'Tools']), 1
		) x ORDER BY lower(c), pref, c`)
}

// Brands returns the distinct brands in use, plus OEM.
func (s *Store) Brands(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, `
		SELECT DISTINCT ON (lower(b)) b FROM (SELECT brand AS b, 0 AS pref FROM items WHERE brand <> '' UNION SELECT 'OEM', 1) x
		ORDER BY lower(b), pref, b`)
}

// Locations returns the distinct storage locations in use.
func (s *Store) Locations(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, `SELECT DISTINCT location FROM items WHERE location <> '' ORDER BY location`)
}

func (s *Store) distinct(ctx context.Context, sql string) ([]string, error) {
	rows, err := s.pool.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
