-- +goose Up
-- A vehicle can record when it was purchased and its odometer at purchase,
-- used as a starting baseline for service intervals when no service has been
-- logged yet.
ALTER TABLE vehicles
    ADD COLUMN purchased_on date,
    ADD COLUMN purchased_km integer CHECK (purchased_km >= 0);

-- +goose Down
UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'vehicles' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(e.r - 'purchased_on' - 'purchased_km' ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE t.payload::text LIKE '%"purchased_on"%'
   OR t.payload::text LIKE '%"purchased_km"%';

ALTER TABLE vehicles
    DROP COLUMN purchased_km,
    DROP COLUMN purchased_on;

