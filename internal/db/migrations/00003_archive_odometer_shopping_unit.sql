-- +goose Up
-- Items with history are archived instead of deleted, so services, kits and
-- spending keep referring to them.
ALTER TABLE items ADD COLUMN archived_at timestamptz;
-- The vehicle odometer before a service moved it forward, so deleting the
-- service can put it back.
ALTER TABLE service_records ADD COLUMN odometer_before integer CHECK (odometer_before >= 0);
-- The unit of a free-text entry's quantity (linked entries use the item's).
ALTER TABLE shopping_entries ADD COLUMN unit text NOT NULL DEFAULT 'piece'
    CHECK (unit IN ('piece', 'ml', 'g'));

-- +goose Down
ALTER TABLE shopping_entries DROP COLUMN unit;
ALTER TABLE service_records DROP COLUMN odometer_before;
ALTER TABLE items DROP COLUMN archived_at;
