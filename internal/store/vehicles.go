package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

const vehicleSelect = `
SELECT v.id, v.version, v.name, v.kind, v.make, v.model, v.year, v.plate, v.vin, v.color, v.odometer,
       v.purchased_on, v.odometer_at_purchase, v.notes,
       (SELECT count(*) FROM item_vehicles iv JOIN items i ON i.id = iv.item_id
        WHERE iv.vehicle_id = v.id AND i.archived_at IS NULL),
       v.created_at, v.updated_at, v.archived_at
FROM vehicles v`

func scanVehicle(row pgx.Row) (stock.Vehicle, error) {
	var v stock.Vehicle
	err := row.Scan(&v.ID, &v.Version, &v.Name, &v.Kind, &v.Make, &v.Model, &v.Year, &v.Plate, &v.VIN, &v.Color,
		&v.Odometer, &v.PurchasedOn, &v.OdometerAtPurchase, &v.Notes, &v.ItemCount, &v.CreatedAt, &v.UpdatedAt, &v.ArchivedAt)
	return v, err
}

// ListVehicles returns all vehicles, archived ones included (see
// stock.Vehicle.Archived), in the order they were added.
func (s *Store) ListVehicles(ctx context.Context) ([]stock.Vehicle, error) {
	rows, err := s.pool.Query(ctx, vehicleSelect+` ORDER BY v.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.Vehicle, error) { return scanVehicle(r) })
}

// GetVehicle returns one vehicle.
func (s *Store) GetVehicle(ctx context.Context, id int64) (stock.Vehicle, error) {
	v, err := scanVehicle(s.pool.QueryRow(ctx, vehicleSelect+` WHERE v.id = $1`, id))
	return v, notFound(err)
}

func vehicleConflict(err error) error {
	if isUniqueViolation(err) {
		return &ConflictError{Field: "name", Message: "A vehicle with this name already exists"}
	}
	return err
}

// CreateVehicle stores a new vehicle and returns its id.
func (s *Store) CreateVehicle(ctx context.Context, in stock.VehicleInput) (int64, error) {
	var id int64
	err := s.inFormTx(ctx, in.FormKey, WhatVehicle, &id, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO vehicles (name, kind, make, model, year, plate, vin, color, odometer, purchased_on, odometer_at_purchase, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
			in.Name, in.Kind, in.Make, in.Model, in.Year, in.Plate, in.VIN, in.Color, in.Odometer, in.PurchasedOn, in.OdometerAtPurchase, in.Notes,
		).Scan(&id)
		return vehicleConflict(err)
	})
	return id, err
}

// UpdateVehicle replaces a vehicle's fields. A changed odometer may not be
// lower than a service's reading (see checkVehicleOdometer). A vehicle
// changed since in.Version fails with a *StaleError.
func (s *Store) UpdateVehicle(ctx context.Context, id int64, in stock.VehicleInput) error {
	return s.inFormTx(ctx, in.FormKey, WhatVehicle, &id, func(tx pgx.Tx) error {
		// The lock the update takes anyway (and checkVehicleOdometer), first.
		var version int
		var currentOdo *int
		err := tx.QueryRow(ctx, `SELECT version, odometer FROM vehicles WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&version, &currentOdo)
		if err := lockedVersion(ctx, tx, err, version, in.Version, WhatVehicle, id); err != nil {
			return err
		}
		if !in.KeepOdometer {
			if err := checkVehicleOdometer(ctx, tx, id, currentOdo, in.Odometer); err != nil {
				return err
			}
		} else if in.OdometerAtPurchase != nil && currentOdo != nil && *in.OdometerAtPurchase > *currentOdo {
			return &ConflictError{Field: "odometer_at_purchase", Message: fmt.Sprintf(
				"Higher than the current odometer (%s)", stock.FormatDistance(*currentOdo))}
		}
		if in.OdometerAtPurchase != nil {
			if err := checkVehicleOdometerAtPurchase(ctx, tx, id, *in.OdometerAtPurchase); err != nil {
				return err
			}
		}
		if in.PurchasedOn != nil {
			if err := checkVehiclePurchasedOn(ctx, tx, id, *in.PurchasedOn); err != nil {
				return err
			}
		}
		// A new version: a form opened before this save is stale now.
		tag, err := tx.Exec(ctx, `
			UPDATE vehicles SET version = version + 1,
			       name = $2, kind = $3, make = $4, model = $5, year = $6, plate = $7, vin = $8,
			       color = $9, odometer = CASE WHEN $10 THEN odometer ELSE $11 END,
			       purchased_on = $12, odometer_at_purchase = $13,
			       notes = $14, updated_at = now()
			WHERE id = $1`,
			id, in.Name, in.Kind, in.Make, in.Model, in.Year, in.Plate, in.VIN, in.Color, in.KeepOdometer, in.Odometer,
			in.PurchasedOn, in.OdometerAtPurchase, in.Notes)
		return checkAffected(tag, vehicleConflict(err))
	})
}

// SetOdometer records the current odometer reading of a vehicle. It may
// not be lower than a service's reading or the vehicle's purchase reading.
func (s *Store) SetOdometer(ctx context.Context, id int64, odo int) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var current, purchasedOdo *int
		if err := tx.QueryRow(ctx, `SELECT odometer, odometer_at_purchase FROM vehicles WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&current, &purchasedOdo); err != nil {
			return notFound(err)
		}
		if purchasedOdo != nil && odo < *purchasedOdo {
			return &ConflictError{Field: "odometer", Message: fmt.Sprintf(
				"Current odometer cannot be lower than purchase odometer (%s)", stock.FormatDistance(*purchasedOdo))}
		}
		if err := checkVehicleOdometer(ctx, tx, id, current, &odo); err != nil {
			return err
		}
		// No new version: the edit form guards the reading with odometer_was,
		// so setting it here (or by a service) leaves an open form good.
		tag, err := tx.Exec(ctx, `UPDATE vehicles SET odometer = $2, updated_at = now() WHERE id = $1`, id, odo)
		return checkAffected(tag, err)
	})
}

// checkVehicleOdometer refuses to change a vehicle's odometer to less than
// the highest reading of its services: that reading is wrong, or this one.
// An unchanged reading is let through, so other fields can still be edited.
func checkVehicleOdometer(ctx context.Context, tx pgx.Tx, id int64, current, odo *int) error {
	if odo == nil {
		// Clearing the reading would leave logged services' readings with
		// nothing to compare against, and distance reminders unknown.
		var logged bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM service_records WHERE vehicle_id = $1 AND odometer IS NOT NULL)`, id).Scan(&logged)
		if err != nil {
			return err
		}
		if current != nil && logged {
			return &ConflictError{Field: "odometer", Message: "Services logged with a reading need one here too"}
		}
		return nil
	}
	if current != nil && *current == *odo {
		return nil
	}
	var top int
	var on time.Time
	err := tx.QueryRow(ctx, `
		SELECT odometer, performed_on FROM service_records
		WHERE vehicle_id = $1 AND odometer IS NOT NULL ORDER BY odometer DESC LIMIT 1`, id).Scan(&top, &on)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if *odo < top {
		return &ConflictError{Field: "odometer", Message: fmt.Sprintf(
			"The service on %s logged %s. Fix or delete that service first.", on.Format("2 Jan 2006"), stock.FormatDistance(top))}
	}
	return nil
}

// checkVehicleOdometerAtPurchase refuses a purchase odometer higher than any service logged.
func checkVehicleOdometerAtPurchase(ctx context.Context, tx pgx.Tx, id int64, odo int) error {
	var lowestOdo int
	var on time.Time
	err := tx.QueryRow(ctx, `
		SELECT odometer, performed_on FROM service_records
		WHERE vehicle_id = $1 AND odometer IS NOT NULL
		ORDER BY odometer ASC LIMIT 1`, id).Scan(&lowestOdo, &on)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if odo > lowestOdo {
		return &ConflictError{Field: "odometer_at_purchase", Message: fmt.Sprintf(
			"Higher than the %s logged on %s. Fix or delete that service first.", stock.FormatDistance(lowestOdo), on.Format("2 Jan 2006"))}
	}
	return nil
}

// checkVehiclePurchasedOn refuses a purchase date later than any service logged.
func checkVehiclePurchasedOn(ctx context.Context, tx pgx.Tx, id int64, purchasedOn time.Time) error {
	var earliestDate time.Time
	err := tx.QueryRow(ctx, `
		SELECT performed_on FROM service_records
		WHERE vehicle_id = $1
		ORDER BY performed_on ASC LIMIT 1`, id).Scan(&earliestDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if stock.DateOnly(purchasedOn).After(stock.DateOnly(earliestDate)) {
		return &ConflictError{Field: "purchased_on", Message: fmt.Sprintf(
			"Later than the service logged on %s. Fix or delete that service first.", earliestDate.Format("2 Jan 2006"))}
	}
	return nil
}

// checkServiceOdometer refuses a service reading lower than one logged on
// an earlier day, or higher than one logged on a later day, or before the
// vehicle's purchase date or purchase odometer. Services of the same day
// aren't compared; except skips the service being edited.
// The caller holds the vehicle's lock (lockVehicle), so two services saved
// at once take turns and can't both pass.
func checkServiceOdometer(ctx context.Context, tx pgx.Tx, in stock.ServiceInput, except int64) error {
	var purchasedOn *time.Time
	var purchasedOdo *int
	if err := tx.QueryRow(ctx, `SELECT purchased_on, odometer_at_purchase FROM vehicles WHERE id = $1`, in.VehicleID).Scan(&purchasedOn, &purchasedOdo); err != nil {
		return notFound(err)
	}
	day := stock.DateOnly(in.PerformedOn)
	if purchasedOn != nil && day.Before(stock.DateOnly(*purchasedOn)) {
		return &ConflictError{Field: "performed_on", Message: fmt.Sprintf(
			"Earlier than %s, when the vehicle was purchased", purchasedOn.Format("2 Jan 2006"))}
	}
	if in.Odometer == nil {
		return nil
	}
	odo := *in.Odometer
	if purchasedOdo != nil && odo < *purchasedOdo {
		return &ConflictError{Field: "odometer", Message: fmt.Sprintf(
			"Lower than %s, the vehicle's purchase odometer", stock.FormatDistance(*purchasedOdo))}
	}
	var other int
	var on time.Time
	err := tx.QueryRow(ctx, `
		SELECT odometer, performed_on FROM service_records
		WHERE vehicle_id = $1 AND id <> $2 AND performed_on < $3 AND odometer > $4
		ORDER BY odometer DESC LIMIT 1`, in.VehicleID, except, day, odo).Scan(&other, &on)
	if err == nil {
		return &ConflictError{Field: "odometer", Message: fmt.Sprintf(
			"Lower than the %s logged on %s, before this service", stock.FormatDistance(other), on.Format("2 Jan 2006"))}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	err = tx.QueryRow(ctx, `
		SELECT odometer, performed_on FROM service_records
		WHERE vehicle_id = $1 AND id <> $2 AND performed_on > $3 AND odometer < $4
		ORDER BY odometer LIMIT 1`, in.VehicleID, except, day, odo).Scan(&other, &on)
	if err == nil {
		return &ConflictError{Field: "odometer", Message: fmt.Sprintf(
			"Higher than the %s logged on %s, after this service", stock.FormatDistance(other), on.Format("2 Jan 2006"))}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

// VehicleUsage counts what refers to a vehicle: a vehicle with services
// or kits is archived instead of deleted.
func (s *Store) VehicleUsage(ctx context.Context, id int64) (services, kits int, err error) {
	return vehicleUsage(ctx, s.pool, id)
}

func vehicleUsage(ctx context.Context, q queryRower, id int64) (services, kits int, err error) {
	err = q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM service_records WHERE vehicle_id = $1),
		       (SELECT count(*) FROM service_kits WHERE vehicle_id = $1)`, id).Scan(&services, &kits)
	return services, kits, err
}

// DeleteVehicle removes a vehicle that has no services or kits; its items
// stay but lose this tag. A vehicle with history fails with a
// *ConflictError and should be archived instead, which keeps its services,
// kits and labour costs. A vehicle changed since version fails with a
// *StaleError.
func (s *Store) DeleteVehicle(ctx context.Context, id int64, version int) (trashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// The tags the delete cascades to before the vehicle: saving an item
		// replaces its tags, then checks each tag's vehicle (KEY SHARE).
		if _, err := tx.Exec(ctx, `SELECT 1 FROM item_vehicles WHERE vehicle_id = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		var name string
		var have int
		err := tx.QueryRow(ctx, `SELECT version, name FROM vehicles WHERE id = $1 FOR UPDATE`, id).Scan(&have, &name)
		if err := lockedVersion(ctx, tx, err, have, version, WhatVehicle, id); err != nil {
			return err
		}
		services, kits, err := vehicleUsage(ctx, tx, id)
		if err != nil {
			return err
		}
		if services+kits > 0 {
			return &ConflictError{Field: "form", Message: name + " has services or kits. Archive it instead."}
		}
		p, err := snapshot(ctx, tx, id, [2]string{"vehicles", "id"}, [2]string{"item_vehicles", "vehicle_id"})
		if err != nil {
			return err
		}
		if trashID, err = putTrash(ctx, tx, TrashVehicle, trashLabel("Vehicle", name), p); err != nil {
			return err
		}
		return checkAffected(tx.Exec(ctx, `DELETE FROM vehicles WHERE id = $1`, id))
	})
	return trashID, err
}

// ArchiveVehicle hides a vehicle from lists, pickers and reminders while
// keeping its services, kits and item tags. A vehicle changed since
// version fails with a *StaleError.
func (s *Store) ArchiveVehicle(ctx context.Context, id int64, version int) error {
	// Archiving changes the vehicle's page: a new version.
	tag, err := s.pool.Exec(ctx, `UPDATE vehicles SET version = version + 1, updated_at = now(), archived_at = COALESCE(archived_at, now())
		WHERE id = $1 AND version = $2`, id, version)
	return versionedUpdate(ctx, s.pool, tag, err, WhatVehicle, id)
}

// RestoreVehicle brings an archived vehicle back. A vehicle changed since
// version fails with a *StaleError.
func (s *Store) RestoreVehicle(ctx context.Context, id int64, version int) error {
	tag, err := s.pool.Exec(ctx, `UPDATE vehicles SET version = version + 1, updated_at = now(), archived_at = NULL WHERE id = $1 AND version = $2`, id, version)
	return versionedUpdate(ctx, s.pool, tag, err, WhatVehicle, id)
}

// Makes returns the distinct vehicle makes in use.
func (s *Store) Makes(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, `SELECT DISTINCT ON (lower(make)) make FROM vehicles WHERE make <> '' ORDER BY lower(make), make COLLATE "C"`)
}

// Models returns the distinct vehicle models in use.
func (s *Store) Models(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, `SELECT DISTINCT ON (lower(model)) model FROM vehicles WHERE model <> '' ORDER BY lower(model), model COLLATE "C"`)
}
