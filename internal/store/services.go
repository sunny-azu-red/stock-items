package store

import (
	"cmp"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// ServiceFilter narrows ListServices.
type ServiceFilter struct {
	VehicleID int64
	Limit     int
}

const serviceSelect = `
SELECT sr.id, sr.vehicle_id, v.name, v.color, sr.kit_id, COALESCE(k.name, ''), sr.performed_on, sr.odometer_km,
       sr.title, sr.notes, sr.labor_cost::float8, sr.created_at,
       (SELECT count(*) FROM stock_movements m WHERE m.service_record_id = sr.id)
FROM service_records sr
JOIN vehicles v ON v.id = sr.vehicle_id
LEFT JOIN service_kits k ON k.id = sr.kit_id`

func scanService(row pgx.Row) (stock.ServiceRecord, error) {
	var r stock.ServiceRecord
	err := row.Scan(&r.ID, &r.VehicleID, &r.VehicleName, &r.VehicleColor, &r.KitID, &r.KitName, &r.PerformedOn,
		&r.OdometerKm, &r.Title, &r.Notes, &r.LaborCost, &r.CreatedAt, &r.PartCount)
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
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (stock.ServiceRecord, error) { return scanService(r) })
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
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if in.KitID != nil {
			var kitVehicle int64
			err := tx.QueryRow(ctx, `SELECT vehicle_id FROM service_kits WHERE id = $1`, *in.KitID).Scan(&kitVehicle)
			if err != nil {
				return notFound(err)
			}
			if kitVehicle != in.VehicleID {
				return &ConflictError{Field: "kit_id", Message: "This kit belongs to another vehicle"}
			}
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO service_records (vehicle_id, kit_id, performed_on, odometer_km, title, notes, labor_cost)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			in.VehicleID, in.KitID, in.PerformedOn, in.OdometerKm, in.Title, in.Notes, in.LaborCost).Scan(&id)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// Lock items in id order, so two services sharing parts cannot deadlock.
		parts := slices.Clone(in.Parts)
		slices.SortFunc(parts, func(a, b stock.KitLineInput) int { return cmp.Compare(a.ItemID, b.ItemID) })
		for _, p := range parts {
			err := applyMovement(ctx, tx, stock.MovementInput{
				ItemID: p.ItemID, Kind: stock.MoveService, Delta: -p.Quantity,
				Note: in.Title, OccurredOn: &in.PerformedOn, ServiceRecordID: &id,
			})
			if err != nil {
				return err
			}
		}
		if in.OdometerKm != nil {
			var current *int
			err := tx.QueryRow(ctx, `SELECT odometer_km FROM vehicles WHERE id = $1 FOR UPDATE`, in.VehicleID).Scan(&current)
			if err != nil {
				return notFound(err)
			}
			if current == nil || *current < *in.OdometerKm {
				if _, err := tx.Exec(ctx, `UPDATE vehicles SET odometer_km = $2, updated_at = now() WHERE id = $1`,
					in.VehicleID, *in.OdometerKm); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE service_records SET odometer_before = $2 WHERE id = $1`, id, current); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return id, err
}

// DeleteService removes a service record and puts its parts back on stock.
// It is a deliberate exception to "every change goes through
// applyMovement": it undoes a mistaken entry by deleting its movements
// together with their effect, so the ledger still adds up. If the service
// moved the vehicle's odometer and nothing changed it since, the odometer
// goes back to the highest of its earlier reading and the remaining
// services' readings.
func (s *Store) DeleteService(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var vehicleID int64
		var reading, before *int
		err := tx.QueryRow(ctx, `SELECT vehicle_id, odometer_km, odometer_before FROM service_records WHERE id = $1`, id).
			Scan(&vehicleID, &reading, &before)
		if err != nil {
			return notFound(err)
		}
		// Lock the items in id order first, like CreateService.
		if _, err := tx.Exec(ctx, `
			SELECT id FROM items WHERE id IN (SELECT item_id FROM stock_movements WHERE service_record_id = $1)
			ORDER BY id FOR UPDATE`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE items i SET quantity = i.quantity - m.total, updated_at = now()
			FROM (SELECT item_id, sum(delta) AS total FROM stock_movements WHERE service_record_id = $1 GROUP BY item_id) m
			WHERE i.id = m.item_id`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM stock_movements WHERE service_record_id = $1`, id); err != nil {
			return err
		}
		if err := checkAffected(tx.Exec(ctx, `DELETE FROM service_records WHERE id = $1`, id)); err != nil {
			return err
		}
		if reading == nil {
			return nil
		}
		// GREATEST skips NULLs: an old record without odometer_before falls
		// back to the other services, and with neither nothing changes.
		_, err = tx.Exec(ctx, `
			UPDATE vehicles SET odometer_km = back.km, updated_at = now()
			FROM (SELECT GREATEST($3::int, (SELECT max(odometer_km) FROM service_records WHERE vehicle_id = $1)) AS km) back
			WHERE id = $1 AND odometer_km = $2 AND back.km < $2`, vehicleID, *reading, before)
		return err
	})
}
