package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

const vehicleSelect = `
SELECT v.id, v.name, v.kind, v.make, v.model, v.year, v.plate, v.vin, v.color, v.odometer_km, v.notes,
       v.created_at, v.updated_at,
       (SELECT count(*) FROM item_vehicles iv JOIN items i ON i.id = iv.item_id
        WHERE iv.vehicle_id = v.id AND i.archived_at IS NULL)
FROM vehicles v`

func scanVehicle(row pgx.Row) (stock.Vehicle, error) {
	var v stock.Vehicle
	err := row.Scan(&v.ID, &v.Name, &v.Kind, &v.Make, &v.Model, &v.Year, &v.Plate, &v.VIN, &v.Color,
		&v.OdometerKm, &v.Notes, &v.CreatedAt, &v.UpdatedAt, &v.ItemCount)
	return v, err
}

// ListVehicles returns all vehicles in the order they were added.
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

// UpdateVehicle replaces a vehicle's fields.
func (s *Store) UpdateVehicle(ctx context.Context, id int64, in stock.VehicleInput) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE vehicles SET name = $2, kind = $3, make = $4, model = $5, year = $6, plate = $7, vin = $8,
		       color = $9, odometer_km = $10, notes = $11, updated_at = now()
		WHERE id = $1`,
		id, in.Name, in.Kind, in.Make, in.Model, in.Year, in.Plate, in.VIN, in.Color, in.OdometerKm, in.Notes)
	return checkAffected(tag, vehicleConflict(err))
}

// SetOdometer records the current odometer reading of a vehicle.
func (s *Store) SetOdometer(ctx context.Context, id int64, km int) error {
	tag, err := s.pool.Exec(ctx, `UPDATE vehicles SET odometer_km = $2, updated_at = now() WHERE id = $1`, id, km)
	return checkAffected(tag, err)
}

// DeleteVehicle removes a vehicle. Its items stay but lose this tag; its
// kits and service history are removed.
func (s *Store) DeleteVehicle(ctx context.Context, id int64) error {
	return checkAffected(s.pool.Exec(ctx, `DELETE FROM vehicles WHERE id = $1`, id))
}
