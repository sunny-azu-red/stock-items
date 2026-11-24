-- +goose Up
-- Every service has a reading (ServiceInput.Validate requires one): the
-- schedules count from it, so a service's odometer is NOT NULL, and
-- odometer_at_log (the vehicle's reading when one was logged without, for
-- the schedules to fall back on) goes. odometer_moved goes too: logging a
-- service sets odometer_before exactly when it moved the odometer
-- (store.moveOdometer), so odometer_before IS NOT NULL says it.
--
-- Nothing is filled in. The block below stops the migration, changing
-- nothing, on a service without a reading (enter one on its edit form
-- first), on one that says it moved the odometer without saying from what,
-- and on a trash snapshot that would not restore after it: a service like
-- those, or a vehicle without the odometer and purchase 00004 made
-- required (its NOT NULL left deleted vehicles as they were).
-- +goose StatementBegin
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(id::text, ', ' ORDER BY id) INTO bad
    FROM service_records WHERE odometer IS NULL OR (odometer_moved AND odometer_before IS NULL);
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'Migration 00005 stopped, nothing was changed: services % have no reading (or moved the odometer from none).', bad;
    END IF;
    SELECT string_agg(DISTINCT t.id::text, ', ') INTO bad
    FROM trash t
    WHERE EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(t.payload -> 'service_records') = 'array' THEN t.payload -> 'service_records' END) sr
                  WHERE COALESCE(jsonb_typeof(sr -> 'odometer'), 'null') = 'null'
                     OR (sr ->> 'odometer_moved' = 'true' AND COALESCE(jsonb_typeof(sr -> 'odometer_before'), 'null') = 'null'))
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(t.payload -> 'vehicles') = 'array' THEN t.payload -> 'vehicles' END) v
                  WHERE COALESCE(jsonb_typeof(v -> 'odometer'), 'null') = 'null'
                     OR COALESCE(jsonb_typeof(v -> 'purchased_on'), 'null') = 'null'
                     OR COALESCE(jsonb_typeof(v -> 'odometer_at_purchase'), 'null') = 'null');
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'Migration 00005 stopped, nothing was changed: trash entries % hold a service without a reading or a vehicle without its odometer and purchase.', bad;
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE service_records
    ALTER COLUMN odometer SET NOT NULL,
    DROP COLUMN odometer_moved,
    DROP COLUMN odometer_at_log;
-- A snapshot restores every key it holds (store.restoreRows).
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_records}', (
    SELECT jsonb_agg(r - 'odometer_moved' - 'odometer_at_log' ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_records') WITH ORDINALITY AS e (r, ord)))
WHERE jsonb_typeof(t.payload -> 'service_records') = 'array' AND jsonb_array_length(t.payload -> 'service_records') > 0;

-- +goose Down
ALTER TABLE service_records
    ALTER COLUMN odometer DROP NOT NULL,
    ADD COLUMN odometer_moved boolean NOT NULL DEFAULT false,
    ADD COLUMN odometer_at_log integer CHECK (odometer_at_log >= 0);
UPDATE service_records SET odometer_moved = odometer_before IS NOT NULL;
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_records}', (
    SELECT jsonb_agg(r || jsonb_build_object('odometer_moved', COALESCE(jsonb_typeof(r -> 'odometer_before'), 'null') <> 'null') ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_records') WITH ORDINALITY AS e (r, ord)))
WHERE jsonb_typeof(t.payload -> 'service_records') = 'array' AND jsonb_array_length(t.payload -> 'service_records') > 0;
SELECT reorder_columns('service_records', ARRAY[
    'id', 'version', 'vehicle_id', 'kit_id', 'kit_tier', 'performed_on', 'odometer', 'odometer_before', 'odometer_moved',
    'odometer_at_log', 'title', 'labor_cost', 'notes', 'created_at']);
