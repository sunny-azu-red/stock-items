package store

import (
	"context"
	"errors"
	"fmt"
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
		SELECT k.id, k.version, k.vehicle_id, v.name, v.color, v.odometer, v.purchased_on, v.odometer_at_purchase,
		       v.warranty_ends_on, v.warranty_ends_at,
		       k.name, k.interval_distance, k.interval_months, k.intermediate, k.break_in_at, k.warranty_required, k.notes,
		       ls.id, ls.performed_on, ls.odometer, ls.kit_tier,
		       lf.id, lf.performed_on, lf.odometer,
		       -- An intermediate service after the last full one, in the
		       -- order below.
		       EXISTS (SELECT 1 FROM service_records sr
		        WHERE sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id AND sr.kit_tier = 'intermediate'
		          AND (lf.id IS NULL OR (sr.performed_on, sr.odometer, sr.id) > (lf.performed_on, lf.odometer, lf.id))),
		       -- The earliest service, in the same order.
		       COALESCE((SELECT sr.id FROM service_records sr WHERE sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id
		                 ORDER BY sr.performed_on, sr.odometer, sr.id LIMIT 1), 0),
		       -- The break-in logged (Log service offers one until then;
		       -- stock.Kit.breakInFull).
		       bi.id, bi.performed_on, bi.odometer,
		       k.archived_at
		FROM service_kits k
		JOIN vehicles v ON v.id = k.vehicle_id
		LEFT JOIN LATERAL (
			SELECT sr.id, sr.performed_on, sr.odometer, sr.kit_tier
			FROM service_records sr
			WHERE sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id -- a kit can be moved to another vehicle
			ORDER BY sr.performed_on DESC, sr.odometer DESC, sr.id DESC LIMIT 1
		) ls ON true
		LEFT JOIN LATERAL (
			SELECT sr.id, sr.performed_on, sr.odometer
			FROM service_records sr
			WHERE sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id AND sr.kit_tier = 'full' -- never a break-in
			ORDER BY sr.performed_on DESC, sr.odometer DESC, sr.id DESC LIMIT 1
		) lf ON true
		LEFT JOIN LATERAL (
			SELECT sr.id, sr.performed_on, sr.odometer
			FROM service_records sr
			WHERE sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id AND sr.kit_tier = 'break_in' -- one at most
		) bi ON true
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
		var lsID, lfID, biID *int64
		var lsDate, lfDate, biDate *time.Time
		var lsOdo, lfOdo, biOdo *int
		var lsTier *stock.KitTier
		err := r.Scan(&k.ID, &k.Version, &k.VehicleID, &k.VehicleName, &k.VehicleColor, &k.VehicleOdometer,
			&k.VehiclePurchasedOn, &k.VehicleOdometerAtPurchase, &k.VehicleWarrantyEndsOn, &k.VehicleWarrantyEndsAt,
			&k.Name, &k.IntervalDistance, &k.IntervalMonths, &k.Intermediate, &k.BreakInAt, &k.WarrantyRequired, &k.Notes,
			&lsID, &lsDate, &lsOdo, &lsTier,
			&lfID, &lfDate, &lfOdo,
			&k.InterSinceFull, &k.FirstServiceID, &biID, &biDate, &biOdo, &k.ArchivedAt)
		if err != nil {
			return k, err
		}
		if lsID != nil {
			k.LastService = &stock.ServiceRef{ID: *lsID, PerformedOn: *lsDate, Odometer: *lsOdo, Tier: *lsTier}
		}
		if lfID != nil {
			k.LastFullService = &stock.ServiceRef{ID: *lfID, PerformedOn: *lfDate, Odometer: *lfOdo, Tier: stock.TierFull}
		}
		if biID != nil {
			k.BreakIn = &stock.ServiceRef{ID: *biID, PerformedOn: *biDate, Odometer: *biOdo, Tier: stock.TierBreakIn}
		}
		return k, err
	})
	if err != nil || len(kits) == 0 {
		return kits, err
	}

	ids := make([]int64, len(kits))
	pos := make(map[int64]int, len(kits))
	var vehicleIDs []int64
	for i, k := range kits {
		ids[i] = k.ID
		pos[k.ID] = i
		vehicleIDs = append(vehicleIDs, k.VehicleID)
	}
	warrantyKits, err := loadWarrantyKits(ctx, s.pool, vehicleIDs)
	if err != nil {
		return nil, err
	}
	for i := range kits {
		kits[i].VehicleWarrantyKits = warrantyKits[kits[i].VehicleID]
	}
	rows, err = s.pool.Query(ctx, `
		SELECT l.kit_id, l.item_id, i.brand, i.name, i.unit, l.quantity, i.quantity, i.archived_at, l.intermediate
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
		var inter bool
		if err := rows.Scan(&kitID, &l.ItemID, &brand, &name, &l.Unit, &l.Need, &l.Have, &l.ArchivedAt, &inter); err != nil {
			return nil, err
		}
		l.Archived = l.ArchivedAt != nil
		l.ItemName = displayName(brand, name)
		k := &kits[pos[kitID]]
		if inter {
			k.InterLines = append(k.InterLines, l)
		} else {
			k.Lines = append(k.Lines, l)
		}
	}
	return kits, rows.Err()
}

// loadWarrantyKits returns, for each of the vehicles, its kits the warranty
// asks for on time (stock.WarrantyKit): not archived, ticked, with their
// break-in and full services on the vehicle, oldest first.
func loadWarrantyKits(ctx context.Context, q querier, vehicleIDs []int64) (map[int64][]stock.WarrantyKit, error) {
	out := map[int64][]stock.WarrantyKit{}
	if len(vehicleIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT k.vehicle_id, k.id, k.name, k.interval_distance, k.interval_months, k.break_in_at,
		       sr.id, sr.performed_on, sr.odometer, sr.kit_tier
		FROM service_kits k
		LEFT JOIN service_records sr ON sr.kit_id = k.id AND sr.vehicle_id = k.vehicle_id AND sr.kit_tier IN ('full', 'break_in')
		WHERE k.vehicle_id = ANY($1) AND k.warranty_required AND k.archived_at IS NULL
		ORDER BY k.vehicle_id, k.id, sr.performed_on, sr.odometer, sr.id`, vehicleIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var vid int64
		var k stock.WarrantyKit
		var srID *int64
		var srDate *time.Time
		var srOdo *int
		var srTier *stock.KitTier
		if err := rows.Scan(&vid, &k.KitID, &k.Name, &k.IntervalDistance, &k.IntervalMonths, &k.BreakInAt,
			&srID, &srDate, &srOdo, &srTier); err != nil {
			return nil, err
		}
		list := out[vid]
		if n := len(list); n == 0 || list[n-1].KitID != k.KitID {
			list = append(list, k)
		}
		if srID != nil {
			last := &list[len(list)-1]
			last.Services = append(last.Services, stock.ServiceRef{ID: *srID, PerformedOn: *srDate, Odometer: *srOdo, Tier: *srTier})
		}
		out[vid] = list
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, withReadings(ctx, q, out)
}

// withReadings fills in the warranty kits' Readings: every service of their
// vehicle, with any kit or none, oldest first. They date a reading a kit's
// service passed with nothing logged (stock.WarrantyMiss.LostOn).
func withReadings(ctx context.Context, q querier, kits map[int64][]stock.WarrantyKit) error {
	if len(kits) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(kits))
	for id := range kits {
		ids = append(ids, id)
	}
	rows, err := q.Query(ctx, `
		SELECT vehicle_id, id, performed_on, odometer FROM service_records
		WHERE vehicle_id = ANY($1)
		ORDER BY vehicle_id, performed_on, odometer, id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	readings := map[int64][]stock.ServiceRef{}
	for rows.Next() {
		var vid int64
		var r stock.ServiceRef
		if err := rows.Scan(&vid, &r.ID, &r.PerformedOn, &r.Odometer); err != nil {
			return err
		}
		readings[vid] = append(readings[vid], r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for vid, list := range kits {
		for i := range list {
			list[i].Readings = readings[vid]
		}
	}
	return nil
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
		if err := checkKitBreakIn(ctx, tx, in.VehicleID, in.BreakInAt); err != nil {
			return err
		}
		name, err := spell(ctx, tx, kitNameSpellings, 0, "", in.Name)
		if err != nil {
			return err
		}
		in.Name = name
		err = tx.QueryRow(ctx, `
			INSERT INTO service_kits (vehicle_id, name, interval_distance, interval_months, intermediate, break_in_at, warranty_required, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			in.VehicleID, in.Name, in.IntervalDistance, in.IntervalMonths, in.Intermediate, in.BreakInAt, in.WarrantyRequired, in.Notes).Scan(&id)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return replaceKitLines(ctx, tx, id, in.Lines, in.InterLines)
	})
	return id, err
}

// UpdateKit replaces a kit's fields and lines. A kit stays with its
// vehicle: its services stay there, so one for another vehicle is a
// duplicate (a *ConflictError if in names another). An archived kit can't
// be ticked Required by the warranty: the warranty counts only kits that
// aren't archived (ArchiveKit refuses the other way round). A kit changed
// since in.Version fails with a *StaleError.
func (s *Store) UpdateKit(ctx context.Context, id int64, in stock.KitInput) error {
	return s.inFormTx(ctx, in.FormKey, WhatKit, &id, func(tx pgx.Tx) error {
		// Its vehicle before the kit, as logging a service with the kit
		// locks its vehicle before the kit (checkServiceKit): the two can't
		// deadlock. A kit's vehicle never changes, so reading it unlocked
		// first is safe.
		var vehicle int64
		var nameWas string
		err := tx.QueryRow(ctx, `SELECT vehicle_id, name FROM service_kits WHERE id = $1`, id).Scan(&vehicle, &nameWas)
		if errors.Is(err, pgx.ErrNoRows) {
			return goneError(ctx, tx, WhatKit, id)
		}
		if err != nil {
			return err
		}
		if _, _, err := lockVehicle(ctx, tx, vehicle); err != nil {
			return err
		}
		var version int
		var archived, required bool
		err = tx.QueryRow(ctx, `SELECT version, archived_at IS NOT NULL, warranty_required FROM service_kits WHERE id = $1 FOR NO KEY UPDATE`, id).
			Scan(&version, &archived, &required)
		if err := lockedVersion(ctx, tx, err, version, in.Version, WhatKit, id); err != nil {
			return err
		}
		if in.VehicleID != vehicle {
			return &ConflictError{Field: "form", Message: "A kit stays with its vehicle: duplicate it for another vehicle."}
		}
		if archived && in.WarrantyRequired && !required {
			return &ConflictError{Field: "form", Message: "Restore " + nameWas + ` before ticking "Required by the warranty".`}
		}
		if err := checkKitBreakIn(ctx, tx, vehicle, in.BreakInAt); err != nil {
			return err
		}
		name, err := spell(ctx, tx, kitNameSpellings, id, nameWas, in.Name)
		if err != nil {
			return err
		}
		in.Name = name
		// A new version (its lines count as part of the kit): a form opened
		// before this save is stale now.
		tag, err := tx.Exec(ctx, `
			UPDATE service_kits SET version = version + 1, name = $2, interval_distance = $3,
			       interval_months = $4, intermediate = $5, break_in_at = $6, warranty_required = $7, notes = $8,
			       updated_at = now()
			WHERE id = $1`, id, in.Name, in.IntervalDistance, in.IntervalMonths, in.Intermediate, in.BreakInAt,
			in.WarrantyRequired, in.Notes)
		if err := checkAffected(tag, err); err != nil {
			return err
		}
		return replaceKitLines(ctx, tx, id, in.Lines, in.InterLines)
	})
}

// checkKitBreakIn refuses a Break-in at that can't apply: at or below the
// reading the kit's vehicle was bought at (a break-in is a new vehicle's,
// stock.Kit.HasBreakIn). A value the kit already holds is refused too, so
// one saved before this check is cleared on the kit's next save. The
// caller holds the vehicle's lock, as UpdateVehicle takes it to change the
// purchase reading (checkVehicleBreakIns).
func checkKitBreakIn(ctx context.Context, tx pgx.Tx, vehicleID int64, breakInAt *int) error {
	if breakInAt == nil {
		return nil
	}
	var name string
	var bought int
	if err := tx.QueryRow(ctx, `SELECT name, odometer_at_purchase FROM vehicles WHERE id = $1`, vehicleID).Scan(&name, &bought); err != nil {
		return notFound(err)
	}
	if *breakInAt <= bought {
		return &ConflictError{Field: "break_in_at", Message: fmt.Sprintf("%s was bought at %s, past its break-in", name, stock.FormatDistance(bought))}
	}
	return nil
}

// replaceKitLines replaces a kit's full and intermediate lines. Their items
// are locked first (see shareItems), and a line read in another unit than
// its item has now is refused: 1000 ml would otherwise be saved as 1000 g.
func replaceKitLines(ctx context.Context, tx pgx.Tx, kitID int64, lines, interLines []stock.KitLineInput) error {
	ids := make([]int64, 0, len(lines)+len(interLines))
	for _, l := range lines {
		ids = append(ids, l.ItemID)
	}
	for _, l := range interLines {
		ids = append(ids, l.ItemID)
	}
	units, err := shareItems(ctx, tx, ids)
	if err != nil {
		return err
	}
	checkUnits := func(ls []stock.KitLineInput, field string) error {
		for _, l := range ls {
			u, ok := units[l.ItemID]
			if !ok {
				return ErrNotFound
			}
			if l.Unit != "" && l.Unit != u.unit {
				return &ConflictError{Field: field, Message: u.name + " is counted in " + u.unit.Short() +
					" now (its unit was changed meanwhile). Check its amount and save again."}
			}
		}
		return nil
	}
	if err := checkUnits(lines, "lines"); err != nil {
		return err
	}
	if err := checkUnits(interLines, "inter_lines"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_kit_lines WHERE kit_id = $1`, kitID); err != nil {
		return err
	}
	insertLines := func(ls []stock.KitLineInput, inter bool) error {
		for _, l := range ls {
			_, err := tx.Exec(ctx, `INSERT INTO service_kit_lines (kit_id, item_id, quantity, intermediate) VALUES ($1, $2, $3, $4)`,
				kitID, l.ItemID, l.Quantity, inter)
			if isForeignKeyViolation(err) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err := insertLines(lines, false); err != nil {
		return err
	}
	return insertLines(interLines, true)
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
			SELECT k.version, k.name, v.name FROM service_kits k JOIN vehicles v ON v.id = k.vehicle_id WHERE k.id = $1 FOR UPDATE OF k`, id).
			Scan(&have, &name, &vehicle)
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
// A kit ticked Required by the warranty fails with a *ConflictError: the
// warranty counts only kits that aren't archived, so archiving it would
// drop what it missed; unticking it says the warranty no longer asks for
// it.
func (s *Store) ArchiveKit(ctx context.Context, id int64, version int) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// The lock the UPDATE below takes anyway, taken first to check.
		var have int
		var name string
		var required bool
		err := tx.QueryRow(ctx, `SELECT version, name, warranty_required FROM service_kits WHERE id = $1 FOR NO KEY UPDATE`, id).
			Scan(&have, &name, &required)
		if err := lockedVersion(ctx, tx, err, have, version, WhatKit, id); err != nil {
			return err
		}
		if required {
			return &ConflictError{Field: "form", Message: `Untick "Required by the warranty" before archiving ` + name + ": its services count for the warranty."}
		}
		// Archiving changes the kit's page: a new version.
		return checkAffected(tx.Exec(ctx, `UPDATE service_kits SET version = version + 1, updated_at = now(), archived_at = COALESCE(archived_at, now())
			WHERE id = $1`, id))
	})
}

// RestoreKit brings an archived kit back. A kit changed since version
// fails with a *StaleError.
func (s *Store) RestoreKit(ctx context.Context, id int64, version int) error {
	tag, err := s.pool.Exec(ctx, `UPDATE service_kits SET version = version + 1, updated_at = now(), archived_at = NULL WHERE id = $1 AND version = $2`, id, version)
	return versionedUpdate(ctx, s.pool, tag, err, WhatKit, id)
}

// KitNames returns the distinct kit names and service titles in use, plus common defaults.
func (s *Store) KitNames(ctx context.Context) ([]string, error) {
	return s.suggestions(ctx, kitNameSpellings)
}

// ServiceTitles returns the distinct service titles and kit names in use, plus common defaults.
func (s *Store) ServiceTitles(ctx context.Context) ([]string, error) {
	return s.KitNames(ctx)
}
