-- +goose Up
-- Logging a service always records whether it moved the vehicle's odometer
-- (moveOdometer, in the same transaction as the insert), so odometer_moved
-- is never NULL, and the column says so: a new row starts as false until
-- moveOdometer sets it.
--
-- SET NOT NULL fails, changing nothing, on a row that holds NULL. A trash
-- snapshot of a service (alone or with its vehicle) restores into this
-- column too, so the block below stops the migration, changing nothing,
-- when one holds a null there.
-- +goose StatementBegin
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(DISTINCT t.id::text, ', ') INTO bad
    FROM trash t, jsonb_array_elements(t.payload -> 'service_records') sr
    WHERE jsonb_typeof(sr -> 'odometer_moved') = 'null';
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'Migration 00002 stopped, nothing was changed: trash entries % hold a service with odometer_moved null.', bad;
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE service_records ALTER COLUMN odometer_moved SET DEFAULT false, ALTER COLUMN odometer_moved SET NOT NULL;

-- +goose Down
ALTER TABLE service_records ALTER COLUMN odometer_moved DROP NOT NULL, ALTER COLUMN odometer_moved DROP DEFAULT;
