-- +goose Up
-- Optimistic concurrency: every edit and delete form carries the version
-- of the record it was opened with, and the store refuses it when the
-- version has moved on (the record was changed or deleted meanwhile, in
-- another tab or by someone else). The writes that bump it are listed in
-- AGENTS.md. Existing rows start at 1, as do rows restored from trash
-- snapshots taken before this migration.
ALTER TABLE items ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE vehicles ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE service_kits ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE service_records ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE stock_movements ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE shopping_entries ADD COLUMN version integer NOT NULL DEFAULT 1;

-- +goose Down
-- Snapshots taken since hold the column; restoring them would fail
-- without it, so it goes from the trash too. Nothing else changes.
UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k IN ('items', 'vehicles', 'service_kits', 'service_records', 'stock_movements', 'shopping_entries')
		     AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(e.r - 'version' ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE t.payload::text LIKE '%"version"%';

ALTER TABLE shopping_entries DROP COLUMN version;
ALTER TABLE stock_movements DROP COLUMN version;
ALTER TABLE service_records DROP COLUMN version;
ALTER TABLE service_kits DROP COLUMN version;
ALTER TABLE vehicles DROP COLUMN version;
ALTER TABLE items DROP COLUMN version;
