-- +goose Up
-- A service without labour stores none (NULL), never 0: the two meant the
-- same (the work was done at home) and a 0 showed as "0.00 labour" on the
-- pages. Zeros become NULL, also in the trash snapshots of deleted services
-- and vehicles, which are restored as they are; then the CHECK keeps 0 out.
-- Dropping the constraint by its name fails rather than guesses if it is
-- named otherwise.
ALTER TABLE service_records DROP CONSTRAINT service_records_labor_cost_check;

UPDATE service_records SET labor_cost = NULL WHERE labor_cost = 0;

UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k = 'service_records' AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				CASE WHEN jsonb_typeof(e.r) = 'object' AND jsonb_typeof(e.r -> 'labor_cost') = 'number'
				          AND (e.r ->> 'labor_cost')::numeric = 0
				THEN jsonb_set(e.r, '{labor_cost}', 'null'::jsonb)
				ELSE e.r END
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE jsonb_typeof(t.payload -> 'service_records') = 'array';

ALTER TABLE service_records ADD CONSTRAINT service_records_labor_cost_check CHECK (labor_cost > 0);

-- +goose Down
-- Only the CHECK goes back: the zeros made NULL stay NULL, since 0 and none
-- meant the same and which was which isn't kept.
ALTER TABLE service_records DROP CONSTRAINT service_records_labor_cost_check;
ALTER TABLE service_records ADD CONSTRAINT service_records_labor_cost_check CHECK (labor_cost >= 0);
