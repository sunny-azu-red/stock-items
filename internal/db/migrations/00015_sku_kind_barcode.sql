-- +goose Up
-- Rename SKU kind 'ean' to 'barcode' (matching SKUBarcode / "Barcode").
ALTER TABLE item_skus DROP CONSTRAINT item_skus_kind_check;

UPDATE item_skus SET kind = 'barcode' WHERE kind = 'ean';

UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'item_skus' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				CASE WHEN e.r ->> 'kind' = 'ean'
				THEN jsonb_set(e.r, '{kind}', '"barcode"')
				ELSE e.r END
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE t.payload::text LIKE '%"item_skus"%' AND t.payload::text LIKE '%"ean"%';

ALTER TABLE item_skus ADD CONSTRAINT item_skus_kind_check
    CHECK (kind IN ('oem', 'manufacturer', 'barcode', 'retailer', 'other'));

-- +goose Down
ALTER TABLE item_skus DROP CONSTRAINT item_skus_kind_check;

UPDATE item_skus SET kind = 'ean' WHERE kind = 'barcode';

UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'item_skus' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				CASE WHEN e.r ->> 'kind' = 'barcode'
				THEN jsonb_set(e.r, '{kind}', '"ean"')
				ELSE e.r END
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE t.payload::text LIKE '%"item_skus"%' AND t.payload::text LIKE '%"barcode"%';

ALTER TABLE item_skus ADD CONSTRAINT item_skus_kind_check
    CHECK (kind IN ('oem', 'manufacturer', 'ean', 'retailer', 'other'));
