-- +goose Up
-- A kit has one break-in on a vehicle (store.checkKitTier says so first,
-- with a message; this holds it when two saves race). A kit with more stops
-- the migration, by kit, changing nothing: make the others full services on
-- their edit forms, then start again.
-- +goose StatementBegin
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(name, ', ' ORDER BY name) INTO bad FROM (
        SELECT k.name FROM service_records s JOIN service_kits k ON k.id = s.kit_id
        WHERE s.kit_tier = 'break_in'
        GROUP BY s.kit_id, s.vehicle_id, k.name HAVING count(*) > 1) dup;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'kits with more than one break-in on a vehicle: %', bad;
    END IF;
END $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX service_records_one_break_in ON service_records (kit_id, vehicle_id) WHERE kit_tier = 'break_in';

-- +goose Down
DROP INDEX service_records_one_break_in;
