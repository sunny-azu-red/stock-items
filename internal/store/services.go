package store

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// ServiceFilter narrows ListServices.
type ServiceFilter struct {
	VehicleID int64
	Limit     int
	Offset    int // skip this many, for paging
}

const serviceSelect = `
SELECT sr.id, sr.vehicle_id, v.name, v.color, sr.kit_id, COALESCE(k.name, ''), sr.performed_on, sr.odometer_km,
       sr.title, sr.notes, sr.labor_cost::float8, sr.created_at, sr.version,
       (SELECT count(*) FROM stock_movements m WHERE m.service_record_id = sr.id)
FROM service_records sr
JOIN vehicles v ON v.id = sr.vehicle_id
LEFT JOIN service_kits k ON k.id = sr.kit_id`

func scanService(row pgx.Row) (stock.ServiceRecord, error) {
	var r stock.ServiceRecord
	err := row.Scan(&r.ID, &r.VehicleID, &r.VehicleName, &r.VehicleColor, &r.KitID, &r.KitName, &r.PerformedOn,
		&r.OdometerKm, &r.Title, &r.Notes, &r.LaborCost, &r.CreatedAt, &r.Version, &r.PartCount)
	return r, err
}

// ListServices returns the service log, newest first.
func (s *Store) ListServices(ctx context.Context, f ServiceFilter) ([]stock.ServiceRecord, error) {
	var args sqlArgs
	sql := serviceSelect + " WHERE true"
	if f.VehicleID != 0 {
		sql += " AND sr.vehicle_id = " + args.add(f.VehicleID)
	}
	sql += " ORDER BY sr.performed_on DESC, sr.id DESC"
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
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.ServiceRecord, error) { return scanService(r) })
}

// CountServices counts a vehicle's logged services (all vehicles for 0).
func (s *Store) CountServices(ctx context.Context, vehicleID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM service_records WHERE $1 = 0 OR vehicle_id = $1`, vehicleID).Scan(&n)
	return n, err
}

// LaborTotal sums the labour cost of a vehicle's services (all vehicles
// for 0).
func (s *Store) LaborTotal(ctx context.Context, vehicleID int64) (float64, error) {
	var total float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(labor_cost), 0)::float8 FROM service_records WHERE $1 = 0 OR vehicle_id = $1`, vehicleID).Scan(&total)
	return total, err
}

// GetService returns one service record with the parts it used.
func (s *Store) GetService(ctx context.Context, id int64) (stock.ServiceRecord, error) {
	r, err := scanService(s.pool.QueryRow(ctx, serviceSelect+" WHERE sr.id = $1", id))
	if err != nil {
		return r, notFound(err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.item_id, i.brand, i.name, i.unit, -m.delta
		FROM stock_movements m JOIN items i ON i.id = m.item_id
		WHERE m.service_record_id = $1 ORDER BY m.id`, id)
	if err != nil {
		return r, err
	}
	r.Parts, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (stock.ServicePart, error) {
		var p stock.ServicePart
		var brand, name string
		err := row.Scan(&p.ItemID, &brand, &name, &p.Unit, &p.Quantity)
		p.ItemName = displayName(brand, name)
		return p, err
	})
	return r, err
}

// CreateService logs a performed service. Every part is taken from stock
// (failing with *InsufficientStockError if there is not enough), and the
// vehicle's odometer is moved forward to the service reading. The reading
// it had before is kept, so DeleteService can put it back.
func (s *Store) CreateService(ctx context.Context, in stock.ServiceInput) (int64, error) {
	var id int64
	err := s.inFormTx(ctx, in.FormKey, WhatService, &id, func(tx pgx.Tx) error {
		if err := checkActiveVehicle(ctx, tx, in.VehicleID); err != nil {
			return err
		}
		if err := checkServiceKit(ctx, tx, in); err != nil {
			return err
		}
		if err := checkServiceOdometer(ctx, tx, in, 0); err != nil {
			return err
		}
		// The vehicle is locked (checkActiveVehicle); then the parts, in id
		// order, before checking whether any is archived.
		if err := lockItems(ctx, tx, partIDs(in.Parts)); err != nil {
			return err
		}
		if err := checkPartUnits(ctx, tx, in.Parts); err != nil {
			return err
		}
		if err := checkArchivedParts(ctx, tx, 0, in.Parts); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO service_records (vehicle_id, kit_id, performed_on, odometer_km, title, notes, labor_cost, odometer_at_log)
			VALUES ($1, $2, $3, $4, $5, $6, $7,
			        CASE WHEN $4::integer IS NULL THEN (SELECT odometer_km FROM vehicles WHERE id = $1) END)
			RETURNING id`,
			in.VehicleID, in.KitID, in.PerformedOn, in.OdometerKm, in.Title, in.Notes, in.LaborCost).Scan(&id)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := takeServiceParts(ctx, tx, id, in); err != nil {
			return err
		}
		return moveOdometer(ctx, tx, id, in.VehicleID, in.OdometerKm)
	})
	return id, err
}

// partIDs lists the items of a service's parts.
func partIDs(parts []stock.KitLineInput) []int64 {
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		ids = append(ids, p.ItemID)
	}
	return ids
}

// UpdateService changes a logged service. Its vehicle stays; everything
// else may change. The old parts go back on stock and the new ones are
// taken, and the odometer change is redone, all in one transaction, so a
// part that is short leaves the service as it was. Like DeleteService it
// rewrites recorded movements together with their effect, so the ledger
// still adds up. A service changed or deleted since in.Version fails with
// a *StaleError.
func (s *Store) UpdateService(ctx context.Context, id int64, in stock.ServiceInput) error {
	return s.inFormTx(ctx, in.FormKey, WhatService, &id, func(tx pgx.Tx) error {
		var kitWas *int64
		var version int
		err := tx.QueryRow(ctx, `SELECT vehicle_id, kit_id, version FROM service_records WHERE id = $1 FOR UPDATE`, id).
			Scan(&in.VehicleID, &kitWas, &version)
		if err := lockedVersion(ctx, tx, err, version, in.Version, WhatService, id); err != nil {
			return err
		}
		// The service, its vehicle, then its parts: the order every service
		// change locks in, so none can deadlock with another.
		if _, _, err := lockVehicle(ctx, tx, in.VehicleID); err != nil {
			return err
		}
		// The service keeps its kit even if the kit has moved to another
		// vehicle since; only a newly chosen kit must fit the vehicle.
		if in.KitID == nil || kitWas == nil || *in.KitID != *kitWas {
			if err := checkServiceKit(ctx, tx, in); err != nil {
				return err
			}
		}
		if err := checkServiceOdometer(ctx, tx, in, id); err != nil {
			return err
		}
		// Lock the old and new parts together in id order, like CreateService.
		rows, err := tx.Query(ctx, `
			SELECT id FROM items
			WHERE id = ANY($2) OR id IN (SELECT item_id FROM stock_movements WHERE service_record_id = $1)
			ORDER BY id FOR NO KEY UPDATE`, id, partIDs(in.Parts))
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if err := checkPartUnits(ctx, tx, in.Parts); err != nil {
			return err
		}
		if err := checkArchivedParts(ctx, tx, id, in.Parts); err != nil {
			return err
		}
		if err := returnServiceParts(ctx, tx, id); err != nil {
			return err
		}
		if err := restoreOdometer(ctx, tx, id); err != nil {
			return err
		}
		// A new version (its parts count as part of the service, and get new
		// movement ids): a form opened before this save is stale now.
		_, err = tx.Exec(ctx, `
			UPDATE service_records SET kit_id = $2, performed_on = $3, odometer_km = $4, title = $5, notes = $6, labor_cost = $7,
			       odometer_at_log = CASE WHEN $4::integer IS NULL THEN odometer_at_log END, version = version + 1
			WHERE id = $1`, id, in.KitID, in.PerformedOn, in.OdometerKm, in.Title, in.Notes, in.LaborCost)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := takeServiceParts(ctx, tx, id, in); err != nil {
			return err
		}
		// A part the service now takes less of gets the rest back.
		if err := refuseOverMax(ctx, tx, ids); err != nil {
			return err
		}
		return moveOdometer(ctx, tx, id, in.VehicleID, in.OdometerKm)
	})
}

// checkServiceKit refuses a kit that belongs to another vehicle or is
// archived. It holds the kit FOR SHARE, so it can't be archived or moved
// before the service commits (inserting the service only takes a KEY
// SHARE lock, which archiving doesn't wait for). The caller holds the
// vehicle; the kit comes after it and before the parts, the order UpdateKit
// locks in too.
func checkServiceKit(ctx context.Context, tx pgx.Tx, in stock.ServiceInput) error {
	if in.KitID == nil {
		return nil
	}
	var kitVehicle int64
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT vehicle_id, archived_at IS NOT NULL FROM service_kits WHERE id = $1 FOR SHARE`, *in.KitID).
		Scan(&kitVehicle, &archived); err != nil {
		return notFound(err)
	}
	if kitVehicle != in.VehicleID {
		return &ConflictError{Field: "kit_id", Message: "This kit belongs to another vehicle"}
	}
	if archived {
		return &ConflictError{Field: "form", Message: "This kit is archived; restore it first."}
	}
	return nil
}

// checkActiveVehicle locks a vehicle (see lockVehicle) and refuses to log
// services or add kits for it if it is archived, as lists and reminders no
// longer show it. The lock keeps it from being archived meanwhile.
func checkActiveVehicle(ctx context.Context, tx pgx.Tx, id int64) error {
	name, archived, err := lockVehicle(ctx, tx, id)
	if err != nil {
		return err
	}
	if archived {
		return &ConflictError{Field: "form", Message: name + " is archived; restore it first."}
	}
	return nil
}

// lockVehicle locks a vehicle FOR NO KEY UPDATE (inserting a service or kit
// holds a KEY SHARE lock on it, which FOR UPDATE would conflict with).
// Service changes lock the vehicle before any parts, so they can't
// deadlock with each other over the odometer.
func lockVehicle(ctx context.Context, tx pgx.Tx, id int64) (name string, archived bool, err error) {
	err = tx.QueryRow(ctx, `SELECT name, archived_at IS NOT NULL FROM vehicles WHERE id = $1 FOR NO KEY UPDATE`, id).
		Scan(&name, &archived)
	return name, archived, notFound(err)
}

// checkPartUnits refuses a part read in another unit than its item has
// now, like replaceKitLines. The caller holds the items' locks.
func checkPartUnits(ctx context.Context, tx pgx.Tx, parts []stock.KitLineInput) error {
	for _, p := range parts {
		if p.Unit == "" {
			continue
		}
		var brand, name string
		var unit stock.Unit
		err := tx.QueryRow(ctx, `SELECT brand, name, unit FROM items WHERE id = $1`, p.ItemID).Scan(&brand, &name, &unit)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // the parts' movements report it
		}
		if err != nil {
			return err
		}
		if unit != p.Unit {
			return &ConflictError{Field: "parts", Message: displayName(brand, name) + " is counted in " + string(unit) +
				" now (its unit was changed meanwhile). Check its amount and save again."}
		}
	}
	return nil
}

// checkArchivedParts refuses to take an archived item for a service, or a
// different amount of one than the service took before (serviceID 0 for
// a new service): editing an old service whose part was archived since
// still works as long as that part stays as it was.
func checkArchivedParts(ctx context.Context, tx pgx.Tx, serviceID int64, parts []stock.KitLineInput) error {
	want := map[int64]float64{}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		want[p.ItemID] += p.Quantity
		ids = append(ids, p.ItemID)
	}
	rows, err := tx.Query(ctx, `
		SELECT i.id, i.brand, i.name, COALESCE(-old.total, 0)::float8
		FROM items i
		LEFT JOIN (SELECT item_id, sum(delta) AS total FROM stock_movements WHERE service_record_id = $1 GROUP BY item_id) old
		       ON old.item_id = i.id
		WHERE i.archived_at IS NOT NULL AND (i.id = ANY($2) OR old.item_id IS NOT NULL)
		ORDER BY i.id`, serviceID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var brand, name string
		var had float64
		if err := rows.Scan(&id, &brand, &name, &had); err != nil {
			return err
		}
		if math.Round(want[id]*1000) != math.Round(had*1000) {
			return archivedError(brand, name)
		}
	}
	return rows.Err()
}

// takeServiceParts takes a service's parts from stock, in item id order
// so two services sharing parts cannot deadlock. Parts that are short are
// all reported in one *ShortPartsError; the caller rolls back.
func takeServiceParts(ctx context.Context, tx pgx.Tx, serviceID int64, in stock.ServiceInput) error {
	parts := slices.Clone(in.Parts)
	slices.SortFunc(parts, func(a, b stock.KitLineInput) int { return cmp.Compare(a.ItemID, b.ItemID) })
	var shorts ShortPartsError
	for _, p := range parts {
		_, err := applyMovement(ctx, tx, stock.MovementInput{
			ItemID: p.ItemID, Kind: stock.MoveService, Delta: -p.Quantity,
			Note: in.Title, OccurredOn: &in.PerformedOn, ServiceRecordID: &serviceID,
		})
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
	return nil
}

// returnServiceParts puts a service's parts back on stock and deletes
// their movements. The caller holds the item locks, and checks the
// maximum on stock once it is done (refuseOverMax).
func returnServiceParts(ctx context.Context, tx pgx.Tx, serviceID int64) error {
	// No new version for the items: stock changes don't stale their forms
	// (see applyMovement).
	if _, err := tx.Exec(ctx, `
		UPDATE items i SET quantity = i.quantity - m.total, updated_at = now()
		FROM (SELECT item_id, sum(delta) AS total FROM stock_movements WHERE service_record_id = $1 GROUP BY item_id) m
		WHERE i.id = m.item_id`, serviceID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM stock_movements WHERE service_record_id = $1`, serviceID)
	return err
}

// refuseOverMax fails if any of the items has more than the maximum on
// stock, like adjustStock: after parts came back from a service.
func refuseOverMax(ctx context.Context, tx pgx.Tx, ids []int64) error {
	var brand, name string
	var unit stock.Unit
	err := tx.QueryRow(ctx, `SELECT brand, name, unit FROM items WHERE id = ANY($1) AND quantity > $2 ORDER BY id LIMIT 1`,
		ids, stock.MaxQuantity).Scan(&brand, &name, &unit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return &ConflictError{Field: "form", Message: displayName(brand, name) + " would have more than " + stock.FormatQuantity(stock.MaxQuantity, unit)}
}

// moveOdometer moves the vehicle's odometer forward to a service's
// reading and records on the service whether it did, and from what.
func moveOdometer(ctx context.Context, tx pgx.Tx, serviceID, vehicleID int64, reading *int) error {
	var current *int
	if reading != nil {
		// NO KEY UPDATE: inserting the service already holds a KEY SHARE lock
		// on the vehicle (the foreign key), which FOR UPDATE would conflict
		// with, deadlocking two services logged at once.
		err := tx.QueryRow(ctx, `SELECT odometer_km FROM vehicles WHERE id = $1 FOR NO KEY UPDATE`, vehicleID).Scan(&current)
		if err != nil {
			return notFound(err)
		}
	}
	moved := reading != nil && (current == nil || *current < *reading)
	if moved {
		// No new version for the vehicle: its edit form guards the reading
		// with odometer_was, so logging a service leaves an open form good.
		if _, err := tx.Exec(ctx, `UPDATE vehicles SET odometer_km = $2, updated_at = now() WHERE id = $1`, vehicleID, *reading); err != nil {
			return err
		}
	} else {
		current = nil
	}
	_, err := tx.Exec(ctx, `UPDATE service_records SET odometer_moved = $2, odometer_before = $3 WHERE id = $1`, serviceID, moved, current)
	return err
}

// restoreOdometer undoes moveOdometer for a service that is being deleted
// or changed: if the service moved the odometer and nothing changed it
// since, it goes back to the highest of its earlier reading and the
// readings of the vehicle's other services (NULL if there are none).
// Services logged before odometer_moved existed are left alone.
func restoreOdometer(ctx context.Context, tx pgx.Tx, serviceID int64) error {
	// No new version, like moveOdometer.
	_, err := tx.Exec(ctx, `
		UPDATE vehicles v SET odometer_km = back.km, updated_at = now()
		FROM service_records sr,
		     LATERAL (SELECT GREATEST(sr.odometer_before,
		                              (SELECT max(o.odometer_km) FROM service_records o
		                               WHERE o.vehicle_id = sr.vehicle_id AND o.id <> sr.id)) AS km) back
		WHERE sr.id = $1 AND v.id = sr.vehicle_id AND sr.odometer_moved
		  AND v.odometer_km = sr.odometer_km AND (back.km IS NULL OR back.km < sr.odometer_km)`, serviceID)
	return err
}

// DeleteService removes a service record and puts its parts back on stock.
// It is a deliberate exception to "every change goes through
// applyMovement": it undoes a mistaken entry by deleting its movements
// together with their effect, so the ledger still adds up. The odometer
// change it made is undone too (see restoreOdometer). A service changed
// since version fails with a *StaleError.
func (s *Store) DeleteService(ctx context.Context, id int64, version int) (trashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var title, vehicle string
		var vehicleID int64
		var on time.Time
		var have int
		// Lock the service, its vehicle, then its items, like UpdateService.
		err := tx.QueryRow(ctx, `
			SELECT sr.title, sr.performed_on, sr.vehicle_id, v.name, sr.version FROM service_records sr JOIN vehicles v ON v.id = sr.vehicle_id
			WHERE sr.id = $1 FOR UPDATE OF sr`, id).Scan(&title, &on, &vehicleID, &vehicle, &have)
		if err := lockedVersion(ctx, tx, err, have, version, WhatService, id); err != nil {
			return err
		}
		if _, _, err := lockVehicle(ctx, tx, vehicleID); err != nil {
			return err
		}
		p, err := snapshot(ctx, tx, id, [2]string{"service_records", "id"}, [2]string{"stock_movements", "service_record_id"})
		if err != nil {
			return err
		}
		if trashID, err = putTrash(ctx, tx, TrashService, trashLabel("Service", title, vehicle, day(on)), p); err != nil {
			return err
		}
		// Lock the items in id order, like CreateService.
		rows, err := tx.Query(ctx, `
			SELECT id FROM items WHERE id IN (SELECT item_id FROM stock_movements WHERE service_record_id = $1)
			ORDER BY id FOR NO KEY UPDATE`, id)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if err := returnServiceParts(ctx, tx, id); err != nil {
			return err
		}
		if err := refuseOverMax(ctx, tx, ids); err != nil {
			return err
		}
		if err := restoreOdometer(ctx, tx, id); err != nil {
			return err
		}
		return checkAffected(tx.Exec(ctx, `DELETE FROM service_records WHERE id = $1`, id))
	})
	return trashID, err
}
