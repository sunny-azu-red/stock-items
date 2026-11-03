-- +goose Up
-- Rename SKU column 'label' to 'brand' (matching items.brand).
ALTER TABLE item_skus RENAME COLUMN label TO brand;

UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'item_skus' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				(e.r - 'label') || jsonb_build_object('brand', COALESCE(e.r -> 'label', '""'::jsonb))
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE t.payload::text LIKE '%"item_skus"%' AND t.payload::text LIKE '%"label"%';

-- +goose Down
ALTER TABLE item_skus RENAME COLUMN brand TO label;

UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'item_skus' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				(e.r - 'brand') || jsonb_build_object('label', COALESCE(e.r -> 'brand', '""'::jsonb))
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE t.payload::text LIKE '%"item_skus"%' AND t.payload::text LIKE '%"brand"%';

