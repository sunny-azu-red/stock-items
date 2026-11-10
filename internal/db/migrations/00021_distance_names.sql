-- +goose Up
-- Distances are in the unit APP_DISTANCE names (km unless set otherwise), so
-- their columns no longer say km: vehicles.odometer and
-- odometer_at_purchase, service_records.odometer, service_kits.
-- interval_distance and inter_interval_distance. Only names change, never a
-- value. Renaming a constraint by its name fails rather than guesses if it is
-- named otherwise.
ALTER TABLE vehicles RENAME COLUMN odometer_km TO odometer;
ALTER TABLE vehicles RENAME COLUMN purchased_km TO odometer_at_purchase;
ALTER TABLE service_records RENAME COLUMN odometer_km TO odometer;
ALTER TABLE service_kits RENAME COLUMN interval_km TO interval_distance;
ALTER TABLE service_kits RENAME COLUMN inter_interval_km TO inter_interval_distance;

ALTER TABLE vehicles RENAME CONSTRAINT vehicles_odometer_km_check TO vehicles_odometer_check;
ALTER TABLE vehicles RENAME CONSTRAINT vehicles_purchased_km_check TO vehicles_odometer_at_purchase_check;
ALTER TABLE service_records RENAME CONSTRAINT service_records_odometer_km_check TO service_records_odometer_check;
ALTER TABLE service_kits RENAME CONSTRAINT service_kits_interval_km_check TO service_kits_interval_distance_check;
ALTER TABLE service_kits RENAME CONSTRAINT service_kits_inter_interval_km_check TO service_kits_inter_interval_distance_check;

-- The trash restores a snapshot's keys as column names, so deleted vehicles,
-- services and kits get the new names too. Only the keys of those tables'
-- rows change; everything else in a payload, and the order of its rows,
-- stays as it was.
UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k IN ('vehicles', 'service_records', 'service_kits') AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				CASE WHEN jsonb_typeof(e.r) = 'object'
				THEN (SELECT COALESCE(jsonb_object_agg(COALESCE(m.new, f.k), f.v), '{}'::jsonb)
				      FROM jsonb_each(e.r) AS f(k, v)
				      LEFT JOIN (VALUES
				        ('vehicles', 'odometer_km', 'odometer'),
				        ('vehicles', 'purchased_km', 'odometer_at_purchase'),
				        ('service_records', 'odometer_km', 'odometer'),
				        ('service_kits', 'interval_km', 'interval_distance'),
				        ('service_kits', 'inter_interval_km', 'inter_interval_distance')
				      ) AS m(tbl, old, new) ON m.tbl = x.k AND m.old = f.k)
				ELSE e.r END
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE jsonb_typeof(t.payload -> 'vehicles') = 'array'
   OR jsonb_typeof(t.payload -> 'service_records') = 'array'
   OR jsonb_typeof(t.payload -> 'service_kits') = 'array';

-- An edit form or button opened before this posts the old field names, which
-- would save a vehicle without its readings: a new version makes it stale,
-- so it says "changed since you opened it" and keeps what was typed.
UPDATE vehicles SET version = version + 1;
UPDATE service_records SET version = version + 1;
UPDATE service_kits SET version = version + 1;

-- +goose Down
-- The names go back, also in the trash; the versions stay, as they only grow.
UPDATE trash t SET payload = (
	SELECT jsonb_object_agg(x.k, CASE
		WHEN x.k IN ('vehicles', 'service_records', 'service_kits') AND jsonb_typeof(x.v) = 'array'
		THEN (SELECT COALESCE(jsonb_agg(
				CASE WHEN jsonb_typeof(e.r) = 'object'
				THEN (SELECT COALESCE(jsonb_object_agg(COALESCE(m.old, f.k), f.v), '{}'::jsonb)
				      FROM jsonb_each(e.r) AS f(k, v)
				      LEFT JOIN (VALUES
				        ('vehicles', 'odometer_km', 'odometer'),
				        ('vehicles', 'purchased_km', 'odometer_at_purchase'),
				        ('service_records', 'odometer_km', 'odometer'),
				        ('service_kits', 'interval_km', 'interval_distance'),
				        ('service_kits', 'inter_interval_km', 'inter_interval_distance')
				      ) AS m(tbl, old, new) ON m.tbl = x.k AND m.new = f.k)
				ELSE e.r END
				ORDER BY e.n), '[]'::jsonb)
		      FROM jsonb_array_elements(x.v) WITH ORDINALITY AS e(r, n))
		ELSE x.v END)
	FROM jsonb_each(t.payload) AS x(k, v))
WHERE jsonb_typeof(t.payload -> 'vehicles') = 'array'
   OR jsonb_typeof(t.payload -> 'service_records') = 'array'
   OR jsonb_typeof(t.payload -> 'service_kits') = 'array';

ALTER TABLE service_kits RENAME CONSTRAINT service_kits_inter_interval_distance_check TO service_kits_inter_interval_km_check;
ALTER TABLE service_kits RENAME CONSTRAINT service_kits_interval_distance_check TO service_kits_interval_km_check;
ALTER TABLE service_records RENAME CONSTRAINT service_records_odometer_check TO service_records_odometer_km_check;
ALTER TABLE vehicles RENAME CONSTRAINT vehicles_odometer_at_purchase_check TO vehicles_purchased_km_check;
ALTER TABLE vehicles RENAME CONSTRAINT vehicles_odometer_check TO vehicles_odometer_km_check;

ALTER TABLE service_kits RENAME COLUMN inter_interval_distance TO inter_interval_km;
ALTER TABLE service_kits RENAME COLUMN interval_distance TO interval_km;
ALTER TABLE service_records RENAME COLUMN odometer TO odometer_km;
ALTER TABLE vehicles RENAME COLUMN odometer_at_purchase TO purchased_km;
ALTER TABLE vehicles RENAME COLUMN odometer TO odometer_km;
