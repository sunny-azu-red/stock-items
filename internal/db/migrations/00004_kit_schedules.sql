-- +goose Up
-- Kit schedules, in one step (00004 to 00008 of the work on them never
-- reached a real schema, so they were folded into this one).
--
-- A vehicle's odometer, purchase date and purchase reading are required:
-- every schedule counts from them (stock.Kit.FullDue, InterDue). The
-- vehicle form requires them, with make, model, year and VIN (those are
-- only shown, so the database leaves them be). Nothing is filled in: a
-- vehicle without them fails the migration, which then changes nothing.
-- Fill them in on its edit form first.
ALTER TABLE vehicles
    ALTER COLUMN odometer SET NOT NULL,
    ALTER COLUMN purchased_on SET NOT NULL,
    ALTER COLUMN odometer_at_purchase SET NOT NULL;

-- A kit's intermediate service is one, halfway through its distance, at
-- its months (stock.Kit.InterStep): intermediate replaces the two free
-- intervals, and needs the distance. A kit may say at what reading a new
-- vehicle's break-in is due (break_in_at, stock.Kit.NextIsBreakIn).
-- Deleted kits in the trash convert the same way, so they restore.
ALTER TABLE service_kits
    ADD COLUMN intermediate boolean NOT NULL DEFAULT false,
    ADD COLUMN break_in_at integer CHECK (break_in_at > 0);
UPDATE service_kits SET intermediate = true WHERE inter_interval_distance IS NOT NULL OR inter_interval_months IS NOT NULL;
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_kits}', (
    SELECT jsonb_agg((k - 'inter_interval_distance' - 'inter_interval_months') || jsonb_build_object('intermediate',
               k ->> 'inter_interval_distance' IS NOT NULL OR k ->> 'inter_interval_months' IS NOT NULL)
           ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_kits') WITH ORDINALITY AS e (k, ord)))
WHERE jsonb_typeof(t.payload -> 'service_kits') = 'array' AND jsonb_array_length(t.payload -> 'service_kits') > 0;
ALTER TABLE service_kits
    DROP COLUMN inter_interval_distance,
    DROP COLUMN inter_interval_months,
    ADD CONSTRAINT service_kits_intermediate_needs_distance CHECK (NOT intermediate OR interval_distance IS NOT NULL);
SELECT reorder_columns('service_kits', ARRAY[
    'id', 'version', 'vehicle_id', 'name', 'interval_distance', 'interval_months', 'intermediate', 'break_in_at',
    'notes', 'created_at', 'updated_at', 'archived_at']);

-- A service logged with a kit is one of its tiers (kit_tier): a full
-- service, the intermediate one, or the break-in, a new vehicle's first
-- service (its full parts), which no schedule counts from: the manual's
-- intervals run from the purchase. It replaces kit_intermediate (true is
-- 'intermediate'), in deleted services in the trash too. A kit has one
-- break-in on a vehicle (store.checkKitTier says so first, with a message;
-- the index holds it when two saves race).
ALTER TABLE service_records
    ADD COLUMN kit_tier text NOT NULL DEFAULT 'full' CHECK (kit_tier IN ('full', 'intermediate', 'break_in'));
UPDATE service_records SET kit_tier = 'intermediate' WHERE kit_intermediate;
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_records}', (
    SELECT jsonb_agg((r - 'kit_intermediate') || jsonb_build_object('kit_tier',
               CASE WHEN COALESCE((r ->> 'kit_intermediate')::boolean, false) THEN 'intermediate' ELSE 'full' END)
           ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_records') WITH ORDINALITY AS e (r, ord)))
WHERE jsonb_typeof(t.payload -> 'service_records') = 'array' AND jsonb_array_length(t.payload -> 'service_records') > 0;
ALTER TABLE service_records DROP COLUMN kit_intermediate;
SELECT reorder_columns('service_records', ARRAY[
    'id', 'version', 'vehicle_id', 'kit_id', 'kit_tier', 'performed_on', 'odometer', 'odometer_before', 'odometer_moved',
    'odometer_at_log', 'title', 'labor_cost', 'notes', 'created_at']);
CREATE UNIQUE INDEX service_records_one_break_in ON service_records (kit_id, vehicle_id) WHERE kit_tier = 'break_in';

-- +goose Down
-- A break-in goes back to a full service, as it was logged before, and an
-- intermediate service to an interval of half the distance at the months.
DROP INDEX service_records_one_break_in;
ALTER TABLE service_records ADD COLUMN kit_intermediate boolean NOT NULL DEFAULT false;
UPDATE service_records SET kit_intermediate = kit_tier = 'intermediate';
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_records}', (
    SELECT jsonb_agg((r - 'kit_tier') || jsonb_build_object('kit_intermediate', r ->> 'kit_tier' = 'intermediate') ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_records') WITH ORDINALITY AS e (r, ord)))
WHERE jsonb_typeof(t.payload -> 'service_records') = 'array' AND jsonb_array_length(t.payload -> 'service_records') > 0;
ALTER TABLE service_records DROP COLUMN kit_tier;
SELECT reorder_columns('service_records', ARRAY[
    'id', 'version', 'vehicle_id', 'kit_id', 'kit_intermediate', 'performed_on', 'odometer', 'odometer_before', 'odometer_moved',
    'odometer_at_log', 'title', 'labor_cost', 'notes', 'created_at']);

ALTER TABLE service_kits
    DROP CONSTRAINT service_kits_intermediate_needs_distance,
    ADD COLUMN inter_interval_distance integer CHECK (inter_interval_distance > 0),
    ADD COLUMN inter_interval_months integer CHECK (inter_interval_months > 0);
UPDATE service_kits SET inter_interval_distance = interval_distance / 2, inter_interval_months = interval_months WHERE intermediate;
UPDATE trash t SET payload = jsonb_set(t.payload, '{service_kits}', (
    SELECT jsonb_agg((k - 'intermediate' - 'break_in_at') || CASE WHEN COALESCE((k ->> 'intermediate')::boolean, false)
               THEN jsonb_build_object('inter_interval_distance', (k ->> 'interval_distance')::integer / 2,
                                       'inter_interval_months', k -> 'interval_months')
               ELSE '{}'::jsonb END
           ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'service_kits') WITH ORDINALITY AS e (k, ord)))
WHERE jsonb_typeof(t.payload -> 'service_kits') = 'array' AND jsonb_array_length(t.payload -> 'service_kits') > 0;
ALTER TABLE service_kits DROP COLUMN intermediate, DROP COLUMN break_in_at;
SELECT reorder_columns('service_kits', ARRAY[
    'id', 'version', 'vehicle_id', 'name', 'interval_distance', 'interval_months', 'inter_interval_distance', 'inter_interval_months',
    'notes', 'created_at', 'updated_at', 'archived_at']);

ALTER TABLE vehicles
    ALTER COLUMN odometer DROP NOT NULL,
    ALTER COLUMN purchased_on DROP NOT NULL,
    ALTER COLUMN odometer_at_purchase DROP NOT NULL;
