-- +goose Up
-- A service kit may also carry an intermediate (half-interval) schedule
-- with its own km/month repeat interval and parts list, and a logged
-- service records whether it was the kit's intermediate or full tier.
ALTER TABLE service_kits
    ADD COLUMN inter_interval_km     integer CHECK (inter_interval_km > 0),
    ADD COLUMN inter_interval_months integer CHECK (inter_interval_months > 0);

ALTER TABLE service_kit_lines
    ADD COLUMN intermediate boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT service_kit_lines_kit_id_item_id_key,
    ADD CONSTRAINT service_kit_lines_kit_id_item_id_intermediate_key UNIQUE (kit_id, item_id, intermediate);

ALTER TABLE service_records
    ADD COLUMN kit_intermediate boolean NOT NULL DEFAULT false;

-- +goose Down
UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'service_kits' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(e.r - 'inter_interval_km' - 'inter_interval_months' ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		WHEN x.k = 'service_kit_lines' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(e.r - 'intermediate' ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n)
		      WHERE COALESCE((e.r ->> 'intermediate')::boolean, false) = false)
		WHEN x.k = 'service_records' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(e.r - 'kit_intermediate' ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE t.payload::text LIKE '%"inter_interval_%'
   OR t.payload::text LIKE '%"intermediate"%'
   OR t.payload::text LIKE '%"kit_intermediate"%';

ALTER TABLE service_records DROP COLUMN kit_intermediate;

DELETE FROM service_kit_lines WHERE intermediate;
ALTER TABLE service_kit_lines
    DROP CONSTRAINT service_kit_lines_kit_id_item_id_intermediate_key,
    ADD CONSTRAINT service_kit_lines_kit_id_item_id_key UNIQUE (kit_id, item_id),
    DROP COLUMN intermediate;

ALTER TABLE service_kits
    DROP COLUMN inter_interval_months,
    DROP COLUMN inter_interval_km;

