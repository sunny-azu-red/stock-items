-- +goose Up
-- The day a vehicle was first registered (its delivery certificate's
-- Registration date), empty for one bought new. Before the first service
-- with a kit, the kit's schedule counts from the manual's milestones from 0
-- and that day, not from the purchase (stock.Start). It starts empty: no
-- value changes, and the months count from the purchase day until it is
-- entered, but a vehicle bought above 0 has its distance counted from the
-- milestones from 0 at once (00009 then requires it for such a vehicle). It
-- goes before the purchase columns (TestColumnOrder). Trash snapshots of a
-- vehicle taken before this restore with it empty (jsonb_populate_recordset
-- leaves a missing key NULL).
ALTER TABLE vehicles
    ADD COLUMN first_registered_on date,
    ADD CONSTRAINT vehicles_first_registered_on_check CHECK (first_registered_on <= purchased_on);
SELECT reorder_columns('vehicles', ARRAY[
    'id', 'version', 'name', 'kind', 'make', 'model', 'year', 'plate', 'vin', 'color',
    'odometer', 'first_registered_on', 'purchased_on', 'odometer_at_purchase', 'warranty_ends_on', 'warranty_ends_at',
    'notes', 'created_at', 'updated_at', 'archived_at']);

-- +goose Down
ALTER TABLE vehicles DROP COLUMN first_registered_on;
