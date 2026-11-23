-- +goose Up
-- A vehicle's warranty: the last day it covers and the odometer reading it
-- ends past, either or both; whichever comes first ends it
-- (stock.WarrantyOf). Both start empty, so no value changes. They go with
-- the purchase columns, before notes (TestColumnOrder). Trash snapshots of
-- a vehicle taken before this restore with both empty (jsonb_populate_recordset
-- leaves a missing key NULL).
ALTER TABLE vehicles
    ADD COLUMN warranty_ends_on date,
    ADD COLUMN warranty_ends_at integer CHECK (warranty_ends_at > 0);
SELECT reorder_columns('vehicles', ARRAY[
    'id', 'version', 'name', 'kind', 'make', 'model', 'year', 'plate', 'vin', 'color',
    'odometer', 'purchased_on', 'odometer_at_purchase', 'warranty_ends_on', 'warranty_ends_at',
    'notes', 'created_at', 'updated_at', 'archived_at']);

-- +goose Down
ALTER TABLE vehicles DROP COLUMN warranty_ends_on, DROP COLUMN warranty_ends_at;
