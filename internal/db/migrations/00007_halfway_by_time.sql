-- +goose Up
-- A kit's intermediate service comes halfway through its distance at its
-- full time, or, on a kit with a time only, halfway through the time
-- (stock.Kit.InterMonths: Coolant every 48 months, its intermediate service
-- at 24). So it needs a distance or a time, no longer a distance. This only
-- loosens the CHECK: every kit passes it already.
ALTER TABLE service_kits
    DROP CONSTRAINT service_kits_intermediate_needs_distance,
    ADD CONSTRAINT service_kits_intermediate_needs_interval CHECK (NOT intermediate OR interval_distance IS NOT NULL OR interval_months IS NOT NULL);

-- +goose Down
-- Stops, changing nothing, while a kit has an intermediate service by time
-- only, live or deleted in the trash (it would not restore): untick Halfway
-- on it first.
-- +goose StatementBegin
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(name, ', ' ORDER BY id) INTO bad
    FROM service_kits WHERE intermediate AND interval_distance IS NULL;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'Migration 00007 Down stopped, nothing was changed: kits % have an intermediate service by time only. Untick Halfway on them first.', bad;
    END IF;
    SELECT string_agg(DISTINCT t.id::text, ', ') INTO bad
    FROM trash t
    WHERE EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(t.payload -> 'service_kits') = 'array' THEN t.payload -> 'service_kits' END) k
                  WHERE k ->> 'intermediate' = 'true' AND COALESCE(jsonb_typeof(k -> 'interval_distance'), 'null') = 'null');
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'Migration 00007 Down stopped, nothing was changed: trash entries % hold a kit with an intermediate service by time only.', bad;
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE service_kits
    DROP CONSTRAINT service_kits_intermediate_needs_interval,
    ADD CONSTRAINT service_kits_intermediate_needs_distance CHECK (NOT intermediate OR interval_distance IS NOT NULL);
