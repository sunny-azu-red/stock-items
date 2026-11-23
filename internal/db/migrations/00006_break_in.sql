-- +goose Up
-- A service logged with a kit is one of its tiers (kit_tier): a full
-- service, an intermediate one, or the break-in, a new vehicle's first
-- service (its full parts), which the full schedule doesn't count from: the
-- manual's intervals run from the purchase (stock.Kit.FullDue). It replaces
-- kit_intermediate, converted exactly (true is 'intermediate'), in deleted
-- services in the trash too, so they restore. A kit may say at what reading
-- its break-in is due (break_in_at), to remind it on a vehicle bought below
-- it (stock.Kit.NextIsBreakIn). Nothing else changes: no service is a
-- break-in until it is marked so on its edit form.
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

ALTER TABLE service_kits ADD COLUMN break_in_at integer CHECK (break_in_at > 0);
SELECT reorder_columns('service_kits', ARRAY[
    'id', 'version', 'vehicle_id', 'name', 'interval_distance', 'interval_months', 'in_between', 'break_in_at',
    'notes', 'created_at', 'updated_at', 'archived_at']);

-- +goose Down
-- A break-in goes back to a full service, as it was logged before.
ALTER TABLE service_kits DROP COLUMN break_in_at;
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
