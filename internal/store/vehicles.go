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
SELECT v.id, v.name, v.kind, v.make, v.model, v.year, v.plate, v.vin, v.color, v.odometer_km, v.notes,
       v.created_at, v.updated_at, v.archived_at,
       (SELECT count(*) FROM item_vehicles iv JOIN items i ON i.id = iv.item_id
        WHERE iv.vehicle_id = v.id AND i.archived_at IS NULL)
FROM vehicles v`

func scanVehicle(row pgx.Row) (stock.Vehicle, error) {
	var v stock.Vehicle
	err := row.Scan(&v.ID, &v.Name, &v.Kind, &v.Make, &v.Model, &v.Year, &v.Plate, &v.VIN, &v.Color,
		&v.OdometerKm, &v.Notes, &v.CreatedAt, &v.UpdatedAt, &v.ArchivedAt, &v.ItemCount)
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
	err := s.pool.QueryRow(ctx, `
		INSERT INTO vehicles (name, kind, make, model, year, plate, vin, color, odometer_km, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		in.Name, in.Kind, in.Make, in.Model, in.Year, in.Plate, in.VIN, in.Color, in.OdometerKm, in.Notes,
	).Scan(&id)
	return id, vehicleConflict(err)
}

// UpdateVehicle replaces a vehicle's fields. A changed odometer may not be
// lower than a service's reading (see checkVehicleOdometer).
func (s *Store) UpdateVehicle(ctx context.Context, id int64, in stock.VehicleInput) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if !in.KeepOdometer {
			if err := checkVehicleOdometer(ctx, tx, id, in.OdometerKm); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `
			UPDATE vehicles SET name = $2, kind = $3, make = $4, model = $5, year = $6, plate = $7, vin = $8,
			       color = $9, odometer_km = CASE WHEN $12 THEN odometer_km ELSE $10 END, notes = $11, updated_at = now()
			WHERE id = $1`,
			id, in.Name, in.Kind, in.Make, in.Model, in.Year, in.Plate, in.VIN, in.Color, in.OdometerKm, in.Notes, in.KeepOdometer)
		return checkAffected(tag, vehicleConflict(err))
	})
}

// SetOdometer records the current odometer reading of a vehicle. It may
// not be lower than a service's reading.
func (s *Store) SetOdometer(ctx context.Context, id int64, km int) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := checkVehicleOdometer(ctx, tx, id, &km); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE vehicles SET odometer_km = $2, updated_at = now() WHERE id = $1`, id, km)
		return checkAffected(tag, err)
	})
}

// checkVehicleOdometer refuses to change a vehicle's odometer to less than
// the highest reading of its services: that reading is wrong, or this one.
// An unchanged reading is let through, so other fields can still be edited.
func checkVehicleOdometer(ctx context.Context, tx pgx.Tx, id int64, km *int) error {
	var current *int
	if err := tx.QueryRow(ctx, `SELECT odometer_km FROM vehicles WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&current); err != nil {
		return notFound(err)
	}
	if km == nil {
		// Clearing the reading would leave logged services' readings with
		// nothing to compare against, and km reminders unknown.
		var logged bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM service_records WHERE vehicle_id = $1 AND odometer_km IS NOT NULL)`, id).Scan(&logged)
		if err != nil {
			return err
		}
		if current != nil && logged {
			return &ConflictError{Field: "odometer_km", Message: "Services logged with a reading need one here too"}
		}
		return nil
	}
	if current != nil && *current == *km {
		return nil
	}
	var top int
	var on time.Time
	err := tx.QueryRow(ctx, `
		SELECT odometer_km, performed_on FROM service_records
		WHERE vehicle_id = $1 AND odometer_km IS NOT NULL ORDER BY odometer_km DESC LIMIT 1`, id).Scan(&top, &on)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if *km < top {
		return &ConflictError{Field: "odometer_km", Message: fmt.Sprintf(
			"The service on %s logged %s. Fix or delete that service first.", on.Format("2 Jan 2006"), stock.FormatKm(top))}
	}
	return nil
}

// checkServiceOdometer refuses a service reading lower than one logged on
// an earlier day, or higher than one logged on a later day. Services of
// the same day aren't compared; except skips the service being edited.
// The caller holds the vehicle's lock (lockVehicle), so two services saved
// at once take turns and can't both pass.
func checkServiceOdometer(ctx context.Context, tx pgx.Tx, in stock.ServiceInput, except int64) error {
	if in.OdometerKm == nil {
		return nil
	}
	km, day := *in.OdometerKm, stock.DateOnly(in.PerformedOn)
	var other int
	var on time.Time
	err := tx.QueryRow(ctx, `
		SELECT odometer_km, performed_on FROM service_records
		WHERE vehicle_id = $1 AND id <> $2 AND performed_on < $3 AND odometer_km > $4
		ORDER BY odometer_km DESC LIMIT 1`, in.VehicleID, except, day, km).Scan(&other, &on)
	if err == nil {
		return &ConflictError{Field: "odometer_km", Message: fmt.Sprintf(
			"Lower than the %s logged on %s, before this service", stock.FormatKm(other), on.Format("2 Jan 2006"))}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	err = tx.QueryRow(ctx, `
		SELECT odometer_km, performed_on FROM service_records
		WHERE vehicle_id = $1 AND id <> $2 AND performed_on > $3 AND odometer_km < $4
		ORDER BY odometer_km LIMIT 1`, in.VehicleID, except, day, km).Scan(&other, &on)
	if err == nil {
		return &ConflictError{Field: "odometer_km", Message: fmt.Sprintf(
			"Higher than the %s logged on %s, after this service", stock.FormatKm(other), on.Format("2 Jan 2006"))}
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
// kits and labour costs.
func (s *Store) DeleteVehicle(ctx context.Context, id int64) (trashID int64, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// The tags the delete cascades to before the vehicle: saving an item
		// replaces its tags, then checks each tag's vehicle (KEY SHARE).
		if _, err := tx.Exec(ctx, `SELECT 1 FROM item_vehicles WHERE vehicle_id = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM vehicles WHERE id = $1 FOR UPDATE`, id).Scan(&name); err != nil {
			return notFound(err)
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
// keeping its services, kits and item tags.
func (s *Store) ArchiveVehicle(ctx context.Context, id int64) error {
	return checkAffected(s.pool.Exec(ctx, `UPDATE vehicles SET archived_at = COALESCE(archived_at, now()) WHERE id = $1`, id))
}

// RestoreVehicle brings an archived vehicle back.
func (s *Store) RestoreVehicle(ctx context.Context, id int64) error {
	return checkAffected(s.pool.Exec(ctx, `UPDATE vehicles SET archived_at = NULL WHERE id = $1`, id))
}
