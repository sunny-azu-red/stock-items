-- +goose Up
-- A kit the vehicle's warranty asks for on time (stock.Kit.WarrantyRequired):
-- its break-in or a full service done past its limit and the grace, or not
-- done, loses the warranty (stock.Vehicle.Warranty). No kit is one until it
-- is ticked on its form, so nothing changes here. Deleted kits in the trash
-- restore without it (restoreRows leaves the default).
ALTER TABLE service_kits ADD COLUMN warranty_required boolean NOT NULL DEFAULT false;
SELECT reorder_columns('service_kits', ARRAY[
    'id', 'version', 'vehicle_id', 'name', 'interval_distance', 'interval_months', 'intermediate', 'break_in_at',
    'warranty_required', 'notes', 'created_at', 'updated_at', 'archived_at']);

-- +goose Down
ALTER TABLE service_kits DROP COLUMN warranty_required;
