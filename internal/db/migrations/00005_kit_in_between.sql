-- +goose Up
-- A kit's intermediate schedule is no longer two free intervals but the
-- number of services between two full ones (in_between, 0 to 2): they come
-- every full distance ÷ (in_between + 1), at the full schedule's months
-- (stock.Kit.InterStep). So it needs a full distance.
--
-- Each kit converts exactly or not at all: an intermediate distance that
-- divides the full one into 2 or 3 equal steps, and intermediate months
-- empty or equal to the full ones. Anything else (or intermediate parts
-- with no intermediate distance) stops the migration, by kit, changing
-- nothing: edit the kit to fit (or delete it from the trash), then start
-- again. Deleted kits in the trash convert the same way, so they restore.
-- +goose StatementBegin
CREATE FUNCTION kit_converts_00005(full_distance integer, full_months integer, inter_distance integer, inter_months integer, inter_lines boolean)
RETURNS boolean LANGUAGE sql IMMUTABLE AS $f$
    SELECT CASE
        WHEN inter_distance IS NULL AND inter_months IS NULL THEN NOT inter_lines
        ELSE full_distance IS NOT NULL AND inter_distance IS NOT NULL
             AND full_distance % inter_distance = 0 AND full_distance / inter_distance IN (2, 3)
             AND (inter_months IS NULL OR inter_months = full_months)
    END
$f$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(v.name || ': ' || k.name, ', ' ORDER BY k.id) INTO bad
    FROM service_kits k JOIN vehicles v ON v.id = k.vehicle_id
    WHERE NOT kit_converts_00005(k.interval_distance, k.interval_months, k.inter_interval_distance, k.inter_interval_months,
                                   EXISTS (SELECT 1 FROM service_kit_lines l WHERE l.kit_id = k.id AND l.intermediate));
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'kits whose intermediate schedule is not 2 or 3 equal steps of the full distance at its months: %', bad;
    END IF;
    SELECT string_agg(t.label, ', ' ORDER BY t.id) INTO bad
    FROM trash t, jsonb_array_elements(t.payload -> 'service_kits') k
    WHERE NOT kit_converts_00005((k ->> 'interval_distance')::integer, (k ->> 'interval_months')::integer,
                                   (k ->> 'inter_interval_distance')::integer, (k ->> 'inter_interval_months')::integer,
                                   EXISTS (SELECT 1 FROM jsonb_array_elements(t.payload -> 'service_kit_lines') l
                                           WHERE (l ->> 'kit_id')::bigint = (k ->> 'id')::bigint AND (l ->> 'intermediate')::boolean));
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'deleted kits in the trash whose intermediate schedule is not 2 or 3 equal steps of the full distance at its months: %', bad;
    END IF;
END $$;
-- +goose StatementEnd
DROP FUNCTION kit_converts_00005;

ALTER TABLE service_kits ADD COLUMN in_between smallint NOT NULL DEFAULT 0 CHECK (in_between BETWEEN 0 AND 2);
UPDATE service_kits SET in_between = interval_distance / inter_interval_distance - 1 WHERE inter_interval_distance IS NOT NULL;
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_kits}', (
    SELECT jsonb_agg((k - 'inter_interval_distance' - 'inter_interval_months') || jsonb_build_object('in_between',
               CASE WHEN k ->> 'inter_interval_distance' IS NULL THEN 0
                    ELSE (k ->> 'interval_distance')::integer / (k ->> 'inter_interval_distance')::integer - 1 END)
           ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_kits') WITH ORDINALITY AS e (k, ord)))
WHERE jsonb_typeof(t.payload -> 'service_kits') = 'array' AND jsonb_array_length(t.payload -> 'service_kits') > 0;
ALTER TABLE service_kits
    DROP COLUMN inter_interval_distance,
    DROP COLUMN inter_interval_months,
    ADD CONSTRAINT service_kits_in_between_needs_distance CHECK (in_between = 0 OR interval_distance IS NOT NULL);
SELECT reorder_columns('service_kits', ARRAY[
    'id', 'version', 'vehicle_id', 'name', 'interval_distance', 'interval_months', 'in_between',
    'notes', 'created_at', 'updated_at', 'archived_at']);

-- +goose Down
ALTER TABLE service_kits
    DROP CONSTRAINT service_kits_in_between_needs_distance,
    ADD COLUMN inter_interval_distance integer CHECK (inter_interval_distance > 0),
    ADD COLUMN inter_interval_months integer CHECK (inter_interval_months > 0);
UPDATE service_kits SET inter_interval_distance = interval_distance / (in_between + 1), inter_interval_months = interval_months
WHERE in_between > 0;
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_kits}', (
    SELECT jsonb_agg((k - 'in_between') || CASE WHEN COALESCE((k ->> 'in_between')::integer, 0) > 0
               THEN jsonb_build_object('inter_interval_distance', (k ->> 'interval_distance')::integer / ((k ->> 'in_between')::integer + 1),
                                       'inter_interval_months', k -> 'interval_months')
               ELSE '{}'::jsonb END
           ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_kits') WITH ORDINALITY AS e (k, ord)))
WHERE jsonb_typeof(t.payload -> 'service_kits') = 'array' AND jsonb_array_length(t.payload -> 'service_kits') > 0;
ALTER TABLE service_kits DROP COLUMN in_between;
SELECT reorder_columns('service_kits', ARRAY[
    'id', 'version', 'vehicle_id', 'name', 'interval_distance', 'interval_months', 'inter_interval_distance', 'inter_interval_months',
    'notes', 'created_at', 'updated_at', 'archived_at']);
