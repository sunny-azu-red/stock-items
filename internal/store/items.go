package store

import (
	"context"
	"strings"

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
	Expiring bool // expired or expiring within ExpiringDays
	InStock  bool // quantity > 0
	Sort     string
}

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
       i.notes, i.created_at, i.updated_at,
       COALESCE((SELECT array_agg(iv.vehicle_id ORDER BY iv.vehicle_id) FROM item_vehicles iv WHERE iv.item_id = i.id), '{}'),
       COALESCE((SELECT json_agg(json_build_object('id', s.id, 'code', s.code, 'kind', s.kind, 'label', s.label,
                                                   'pack_size', s.pack_size) ORDER BY s.id)
                 FROM item_skus s WHERE s.item_id = i.id), '[]'),
       EXISTS (SELECT 1 FROM item_photos p WHERE p.item_id = i.id)
FROM items i`

func scanItem(row pgx.Row) (stock.Item, error) {
	var it stock.Item
	err := row.Scan(&it.ID, &it.Name, &it.Brand, &it.Category, &it.Unit, &it.Quantity, &it.MinQuantity,
		&it.ExpiresOn, &it.Location, &it.Notes, &it.CreatedAt, &it.UpdatedAt, &it.VehicleIDs, &it.SKUs, &it.HasPhoto)
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
		where = append(where, "(SELECT count(*) FROM item_vehicles iv WHERE iv.item_id = i.id AND iv.vehicle_id = ANY("+p+")) = cardinality("+p+"::bigint[])")
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
		where = append(where, "i.expires_on IS NOT NULL AND i.expires_on <= current_date + "+args.add(ExpiringDays)+"::int")
	}
	if f.InStock {
		where = append(where, "i.quantity > 0")
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
		sql += " ORDER BY i.quantity, " + nameOrder
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

// CreateItem stores a new item. A starting quantity is recorded as an
// "initial" stock movement.
func (s *Store) CreateItem(ctx context.Context, in stock.ItemInput) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO items (name, brand, category, unit, min_quantity, expires_on, location, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			in.Name, in.Brand, in.Category, in.Unit, in.MinQuantity, in.ExpiresOn, in.Location, in.Notes,
		).Scan(&id)
		if err != nil {
			return err
		}
		if in.Quantity > 0 {
			if err := applyMovement(ctx, tx, stock.MovementInput{ItemID: id, Kind: stock.MoveInitial, Delta: in.Quantity}); err != nil {
				return err
			}
		}
		return replaceSKUsAndTags(ctx, tx, id, in)
	})
	return id, err
}

// UpdateItem replaces an item's fields, SKUs and tags. A changed quantity
// is recorded as a "correction" movement. Changing the unit of an item
// with history is refused, as old movements would no longer make sense.
func (s *Store) UpdateItem(ctx context.Context, id int64, in stock.ItemInput) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var qty float64
		var unit stock.Unit
		err := tx.QueryRow(ctx, `SELECT quantity, unit FROM items WHERE id = $1 FOR UPDATE`, id).Scan(&qty, &unit)
		if err != nil {
			return notFound(err)
		}
		if unit != in.Unit {
			var moves int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE item_id = $1`, id).Scan(&moves); err != nil {
				return err
			}
			if moves > 0 {
				return &ConflictError{Field: "unit", Message: "The unit cannot be changed once the item has stock history"}
			}
		}
		_, err = tx.Exec(ctx, `
			UPDATE items SET name = $2, brand = $3, category = $4, unit = $5, min_quantity = $6, expires_on = $7,
			       location = $8, notes = $9, updated_at = now()
			WHERE id = $1`,
			id, in.Name, in.Brand, in.Category, in.Unit, in.MinQuantity, in.ExpiresOn, in.Location, in.Notes)
		if err != nil {
			return err
		}
		if delta := in.Quantity - qty; delta != 0 {
			err := applyMovement(ctx, tx, stock.MovementInput{ItemID: id, Kind: stock.MoveCorrection, Delta: delta, Note: "Edited quantity"})
			if err != nil {
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
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteItem removes an item with its SKUs, tags, photo and history.
func (s *Store) DeleteItem(ctx context.Context, id int64) error {
	return checkAffected(s.pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, id))
}

// DuplicateItem copies an item (fields, SKUs and tags, but no stock) and
// returns the new id.
func (s *Store) DuplicateItem(ctx context.Context, id int64) (int64, error) {
	var newID int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO items (name, brand, category, unit, min_quantity, expires_on, location, notes)
			SELECT name || ' (copy)', brand, category, unit, min_quantity, expires_on, location, notes
			FROM items WHERE id = $1 RETURNING id`, id).Scan(&newID)
		if err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO item_skus (item_id, code, kind, label, pack_size)
			SELECT $2, code, kind, label, pack_size FROM item_skus WHERE item_id = $1 ORDER BY id`, id, newID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO item_vehicles (item_id, vehicle_id)
			SELECT $2, vehicle_id FROM item_vehicles WHERE item_id = $1`, id, newID)
		return err
	})
	return newID, err
}

// Categories returns the distinct categories in use, plus common defaults.
func (s *Store) Categories(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, `
		SELECT c FROM (
			SELECT category AS c FROM items WHERE category <> ''
			UNION SELECT unnest(ARRAY['Oil & fluids', 'Filters', 'Brakes', 'Gaskets & O-rings', 'Chain & sprockets',
			                          'Electrical', 'Tyres', 'Cleaning & care', 'Hardware', 'Tools'])
		) x ORDER BY lower(c)`)
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
