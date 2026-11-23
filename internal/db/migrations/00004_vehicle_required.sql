-- +goose Up
-- A vehicle's odometer, purchase date and purchase reading are required:
-- every schedule counts from them (stock.Kit.FullDue, InterDue), so none
-- is ever missing. The vehicle form requires them, with make, model, year
-- and VIN (stock.VehicleInput.Validate; those are only shown, so the
-- database leaves them be). Nothing is filled in: a vehicle, or a deleted
-- one in the trash, without them stops the migration. Fill them in on its
-- edit form (restore a deleted one first, or empty it from the trash), then
-- start again.
-- +goose StatementBegin
DO $$
DECLARE
    missing text;
BEGIN
    SELECT string_agg(name, ', ' ORDER BY id) INTO missing FROM vehicles
    WHERE odometer IS NULL OR purchased_on IS NULL OR odometer_at_purchase IS NULL;
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'vehicles without an odometer, purchase date or purchase reading: %', missing;
    END IF;
    SELECT string_agg(t.label, ', ' ORDER BY t.id) INTO missing FROM trash t
    WHERE t.kind = 'vehicle' AND EXISTS (
        SELECT 1 FROM jsonb_array_elements(t.payload -> 'vehicles') v
        WHERE v -> 'odometer' IS NULL OR v -> 'odometer' = 'null'
           OR v -> 'purchased_on' IS NULL OR v -> 'purchased_on' = 'null'
           OR v -> 'odometer_at_purchase' IS NULL OR v -> 'odometer_at_purchase' = 'null');
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'deleted vehicles in the trash without an odometer, purchase date or purchase reading: %', missing;
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE vehicles
    ALTER COLUMN odometer SET NOT NULL,
    ALTER COLUMN purchased_on SET NOT NULL,
    ALTER COLUMN odometer_at_purchase SET NOT NULL;

-- +goose Down
ALTER TABLE vehicles
    ALTER COLUMN odometer DROP NOT NULL,
    ALTER COLUMN purchased_on DROP NOT NULL,
    ALTER COLUMN odometer_at_purchase DROP NOT NULL;
