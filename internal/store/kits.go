package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// KitFilter narrows ListKits.
type KitFilter struct {
	IDs       []int64
	VehicleID int64
	ItemID    int64 // kits that contain this item
	// IncludeArchived also lists archived kits and kits of archived
	// vehicles.
	IncludeArchived bool
}

// ListKits returns service kits with their lines (and current stock) and
// the last service performed with each kit. Archived kits are left out
// unless asked for by id or IncludeArchived, and so are kits of archived
// vehicles unless also asked for by vehicle.
func (s *Store) ListKits(ctx context.Context, f KitFilter) ([]stock.Kit, error) {
	var args sqlArgs
	sql := `
		SELECT k.id, k.vehicle_id, v.name, v.color, k.name, k.interval_km, k.interval_months, k.notes, k.archived_at, k.version,
		       ls.id, ls.performed_on, ls.odometer_km, ls.odometer_at_log, ls.created_at,
		       (SELECT sr.odometer_km FROM service_records sr
		        WHERE sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id AND sr.odometer_km IS NOT NULL
		        ORDER BY sr.performed_on DESC, sr.id DESC LIMIT 1)
		FROM service_kits k
		JOIN vehicles v ON v.id = k.vehicle_id
		LEFT JOIN LATERAL (
			SELECT sr.id, sr.performed_on, sr.odometer_km, sr.odometer_at_log, sr.created_at
			FROM service_records sr
			WHERE sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id -- a kit can be moved to another vehicle
			ORDER BY sr.performed_on DESC, sr.id DESC LIMIT 1
		) ls ON true
		WHERE true`
	if len(f.IDs) > 0 {
		sql += " AND k.id = ANY(" + args.add(f.IDs) + ")"
	} else if !f.IncludeArchived {
		sql += " AND k.archived_at IS NULL"
		if f.VehicleID == 0 {
			sql += " AND v.archived_at IS NULL"
		}
	}
	if f.VehicleID != 0 {
		sql += " AND k.vehicle_id = " + args.add(f.VehicleID)
	}
	if f.ItemID != 0 {
		sql += " AND EXISTS (SELECT 1 FROM service_kit_lines l WHERE l.kit_id = k.id AND l.item_id = " + args.add(f.ItemID) + ")"
	}
	sql += " ORDER BY v.id, lower(k.name), k.id"

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	kits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.Kit, error) {
		var k stock.Kit
		var lsID *int64
		var lsDate, lsLogged *time.Time
		var ls stock.ServiceRef
		err := r.Scan(&k.ID, &k.VehicleID, &k.VehicleName, &k.VehicleColor, &k.Name, &k.IntervalKm, &k.IntervalMonths,
			&k.Notes, &k.ArchivedAt, &k.Version, &lsID, &lsDate, &ls.OdometerKm, &ls.OdometerAtLog, &lsLogged, &k.LastOdometerKm)
		if lsID != nil && lsDate != nil {
			ls.ID, ls.PerformedOn = *lsID, *lsDate
			// The reading when it was logged says where the service was
			// done only if it was logged within a week of being done, the
			// day it was logged counted in the app's zone (time.Local).
			if lsLogged == nil || !stock.LoggedSoonAfter(*lsLogged, *lsDate, time.Local) {
				ls.OdometerAtLog = nil
			}
			k.LastService = &ls
		}
		return k, err
	})
	if err != nil || len(kits) == 0 {
		return kits, err
	}

	ids := make([]int64, len(kits))
	pos := make(map[int64]int, len(kits))
	for i, k := range kits {
		ids[i] = k.ID
		pos[k.ID] = i
	}
	rows, err = s.pool.Query(ctx, `
		SELECT l.kit_id, l.item_id, i.brand, i.name, i.unit, l.quantity, i.quantity, i.archived_at IS NOT NULL
		FROM service_kit_lines l JOIN items i ON i.id = l.item_id
		WHERE l.kit_id = ANY($1)
		ORDER BY l.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kitID int64
		var l stock.KitLine
		var brand, name string
		if err := rows.Scan(&kitID, &l.ItemID, &brand, &name, &l.Unit, &l.Need, &l.Have, &l.Archived); err != nil {
			return nil, err
		}
		l.ItemName = displayName(brand, name)
		k := &kits[pos[kitID]]
		k.Lines = append(k.Lines, l)
	}
	return kits, rows.Err()
}

// GetKit returns one kit.
func (s *Store) GetKit(ctx context.Context, id int64) (stock.Kit, error) {
	kits, err := s.ListKits(ctx, KitFilter{IDs: []int64{id}})
	if err != nil {
		return stock.Kit{}, err
	}
	if len(kits) == 0 {
		return stock.Kit{}, ErrNotFound
	}
	return kits[0], nil
}

// CreateKit stores a new kit and returns its id.
func (s *Store) CreateKit(ctx context.Context, in stock.KitInput) (int64, error) {
	var id int64
	err := s.inFormTx(ctx, in.FormKey, WhatKit, &id, func(tx pgx.Tx) error {
		if err := checkActiveVehicle(ctx, tx, in.VehicleID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO service_kits (vehicle_id, name, interval_km, interval_months, notes)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			in.VehicleID, in.Name, in.IntervalKm, in.IntervalMonths, in.Notes).Scan(&id)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return replaceKitLines(ctx, tx, id, in.Lines)
	})
	return id, err
}

// UpdateKit replaces a kit's fields and lines. A kit changed since
// in.Version fails with a *StaleError.
func (s *Store) UpdateKit(ctx context.Context, id int64, in stock.KitInput) error {
	return s.inFormTx(ctx, in.FormKey, WhatKit, &id, func(tx pgx.Tx) error {
		// The vehicles (the kit's and the one it moves to, in id order)
		// before the kit, as logging a service with the kit locks its
		// vehicle before the kit (checkServiceKit): the two can't deadlock.
		// The kit's vehicle is read without a lock first; a kit moved since
		// has a new version, so the check below refuses it.
		var was int64
		err := tx.QueryRow(ctx, `SELECT vehicle_id FROM service_kits WHERE id = $1`, id).Scan(&was)
		if errors.Is(err, pgx.ErrNoRows) {
			return goneError(ctx, tx, WhatKit, id)
		}
		if err != nil {
			return err
		}
		vehicles, err := tx.Query(ctx, `SELECT id, name, archived_at IS NOT NULL FROM vehicles WHERE id IN ($1, $2) ORDER BY id FOR NO KEY UPDATE`,
			was, in.VehicleID)
		if err != nil {
			return err
		}
		defer vehicles.Close()
		var target string
		var targetArchived, found bool
		for vehicles.Next() {
			var vid int64
			var name string
			var archived bool
			if err := vehicles.Scan(&vid, &name, &archived); err != nil {
				return err
			}
			if vid == in.VehicleID {
				target, targetArchived, found = name, archived, true
			}
		}
		if err := vehicles.Err(); err != nil {
			return err
		}
		var now int64
		var version int
		err = tx.QueryRow(ctx, `SELECT vehicle_id, version FROM service_kits WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&now, &version)
		if err := lockedVersion(ctx, tx, err, version, in.Version, WhatKit, id); err != nil {
			return err
		}
		if now != was {
			return &StaleError{What: WhatKit}
		}
		// A kit can stay with its archived vehicle, but not move to one.
		if was != in.VehicleID {
			if !found {
				return ErrNotFound
			}
			if targetArchived {
				return &ConflictError{Field: "form", Message: target + " is archived; restore it first."}
			}
		}
		// A new version (its lines count as part of the kit): a form opened
		// before this save is stale now.
		tag, err := tx.Exec(ctx, `
			UPDATE service_kits SET vehicle_id = $2, name = $3, interval_km = $4, interval_months = $5, notes = $6,
			       updated_at = now(), version = version + 1
			WHERE id = $1`, id, in.VehicleID, in.Name, in.IntervalKm, in.IntervalMonths, in.Notes)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err := checkAffected(tag, err); err != nil {
			return err
		}
		return replaceKitLines(ctx, tx, id, in.Lines)
	})
}

// replaceKitLines replaces a kit's lines. Their items are locked first
// (see shareItems), and a line read in another unit than its item has now
// is refused: 1000 ml would otherwise be saved as 1000 g.
func replaceKitLines(ctx context.Context, tx pgx.Tx, kitID int64, lines []stock.KitLineInput) error {
	ids := make([]int64, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.ItemID)
	}
	units, err := shareItems(ctx, tx, ids)
	if err != nil {
		return err
	}
	for _, l := range lines {
		u, ok := units[l.ItemID]
		if !ok {
			return ErrNotFound
		}
		if l.Unit != "" && l.Unit != u.unit {
			return &ConflictError{Field: "lines", Message: u.name + " is counted in " + string(u.unit) +
				" now (its unit was changed meanwhile). Check its amount and save again."}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_kit_lines WHERE kit_id = $1`, kitID); err != nil {
		return err
	}
	for _, l := range lines {
		_, err := tx.Exec(ctx, `INSERT INTO service_kit_lines (kit_id, item_id, quantity) VALUES ($1, $2, $3)`,
			kitID, l.ItemID, l.Quantity)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// sharedItem is an item a kit line names, as shareItems read it.
type sharedItem struct {
	name string
	unit stock.Unit
}

// shareItems locks the items of kit lines FOR SHARE, in id order, before
// the lines are inserted: inserting alone takes a KEY SHARE lock, which an
// item's edit (FOR NO KEY UPDATE) doesn't wait for, so its unit could
// change past a line it can't see yet. This way the edit waits and then
// sees the line, or the kit waits and sees the new unit. Items come after
// the kit and its vehicle, as everywhere.
func shareItems(ctx context.Context, tx pgx.Tx, ids []int64) (map[int64]sharedItem, error) {
	out := map[int64]sharedItem{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id, brand, name, unit FROM items WHERE id = ANY($1) ORDER BY id FOR SHARE`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var brand, name string
		var it sharedItem
		if err := rows.Scan(&id, &brand, &name, &it.unit); err != nil {
			return nil, err
		}
		it.name = displayName(brand, name)
		out[id] = it
	}
	return out, rows.Err()
}

// KitServices counts the services logged with a kit.
func (s *Store) KitServices(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM service_records WHERE kit_id = $1`, id).Scan(&n)
	return n, err
}

// DeleteKit removes a kit that was never used. A kit with logged services
// fails with a *ConflictError and should be archived instead: deleting it
// would cut the link from its services and lose its history. A kit
// changed since version fails with a *StaleError.
func (s *Store) DeleteKit(ctx context.Context, id int64, version int) (trashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var name, vehicle string
		var have int
		err := tx.QueryRow(ctx, `
			SELECT k.name, v.name, k.version FROM service_kits k JOIN vehicles v ON v.id = k.vehicle_id WHERE k.id = $1 FOR UPDATE OF k`, id).
			Scan(&name, &vehicle, &have)
		if err := lockedVersion(ctx, tx, err, have, version, WhatKit, id); err != nil {
			return err
		}
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM service_records WHERE kit_id = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return &ConflictError{Field: "form", Message: name + " has logged services. Archive it instead."}
		}
		p, err := snapshot(ctx, tx, id, [2]string{"service_kits", "id"}, [2]string{"service_kit_lines", "kit_id"})
		if err != nil {
			return err
		}
		if trashID, err = putTrash(ctx, tx, TrashKit, trashLabel("Kit", name, vehicle), p); err != nil {
			return err
		}
		return checkAffected(tx.Exec(ctx, `DELETE FROM service_kits WHERE id = $1`, id))
	})
	return trashID, err
}

// ArchiveKit hides a kit from lists, reminders and suggestions while its
// services keep it. A kit changed since version fails with a *StaleError.
func (s *Store) ArchiveKit(ctx context.Context, id int64, version int) error {
	// Archiving changes the kit's page: a new version.
	tag, err := s.pool.Exec(ctx, `UPDATE service_kits SET archived_at = COALESCE(archived_at, now()), updated_at = now(), version = version + 1
		WHERE id = $1 AND version = $2`, id, version)
	return versionedUpdate(ctx, s.pool, tag, err, WhatKit, id)
}

// RestoreKit brings an archived kit back. A kit changed since version
// fails with a *StaleError.
func (s *Store) RestoreKit(ctx context.Context, id int64, version int) error {
	tag, err := s.pool.Exec(ctx, `UPDATE service_kits SET archived_at = NULL, updated_at = now(), version = version + 1 WHERE id = $1 AND version = $2`, id, version)
	return versionedUpdate(ctx, s.pool, tag, err, WhatKit, id)
}
