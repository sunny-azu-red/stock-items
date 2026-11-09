-- +goose Up
-- The kinds of stock change are stored under the words the pages use, so a
-- kind reads the same in the database, the code and the pages:
-- initial → starting, purchase → bought, use → used, correction → adjusted
-- (Counted became Adjusted at the same time); service stays. The trash
-- snapshots of deleted stock changes carry the kind too and are restored
-- as they are, so they are rewritten with the table.
ALTER TABLE stock_movements DROP CONSTRAINT stock_movements_kind_check;

UPDATE stock_movements SET kind = CASE kind
    WHEN 'initial' THEN 'starting'
    WHEN 'purchase' THEN 'bought'
    WHEN 'use' THEN 'used'
    WHEN 'correction' THEN 'adjusted'
    ELSE kind END
WHERE kind IN ('initial', 'purchase', 'use', 'correction');

UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'stock_movements' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				CASE WHEN jsonb_typeof(e.r) = 'object' AND e.r ->> 'kind' IN ('initial', 'purchase', 'use', 'correction')
				THEN jsonb_set(e.r, '{kind}', to_jsonb(CASE e.r ->> 'kind'
					WHEN 'initial' THEN 'starting'
					WHEN 'purchase' THEN 'bought'
					WHEN 'use' THEN 'used'
					ELSE 'adjusted' END))
				ELSE e.r END
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE jsonb_typeof(t.payload -> 'stock_movements') = 'array';

ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_kind_check
    CHECK (kind IN ('starting', 'bought', 'used', 'adjusted', 'service'));

-- +goose Down
ALTER TABLE stock_movements DROP CONSTRAINT stock_movements_kind_check;

UPDATE stock_movements SET kind = CASE kind
    WHEN 'starting' THEN 'initial'
    WHEN 'bought' THEN 'purchase'
    WHEN 'used' THEN 'use'
    WHEN 'adjusted' THEN 'correction'
    ELSE kind END
WHERE kind IN ('starting', 'bought', 'used', 'adjusted');

UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'stock_movements' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				CASE WHEN jsonb_typeof(e.r) = 'object' AND e.r ->> 'kind' IN ('starting', 'bought', 'used', 'adjusted')
				THEN jsonb_set(e.r, '{kind}', to_jsonb(CASE e.r ->> 'kind'
					WHEN 'starting' THEN 'initial'
					WHEN 'bought' THEN 'purchase'
					WHEN 'used' THEN 'use'
					ELSE 'correction' END))
				ELSE e.r END
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE jsonb_typeof(t.payload -> 'stock_movements') = 'array';

ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_kind_check
    CHECK (kind IN ('initial', 'purchase', 'use', 'correction', 'service'));

