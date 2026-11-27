-- +goose Up
-- A vehicle is bought new or used (the vehicle form's Bought: New · Used or
-- demo). Bought new, its first registration is its purchase and it was at
-- 0: first_registered_on is empty and odometer_at_purchase 0. Bought used
-- (or as a demo), both are entered. So an empty first_registered_on means
-- bought new, and a vehicle bought above 0 has one (stock.Vehicle.BoughtNew).
--
-- A vehicle bought above 0 without one (entered before 00008) gets its
-- purchase day, which is what its schedules counted its months from
-- (stock.Start): nothing they say changes, and the date shows on its page
-- and edit form, to be corrected to its Registration date. Its version
-- moves on, as its edit form now shows a date. Trash snapshots of such a
-- vehicle get the same, so they still restore.
UPDATE vehicles SET first_registered_on = purchased_on, version = version + 1
WHERE first_registered_on IS NULL AND odometer_at_purchase > 0;
UPDATE trash t SET payload = jsonb_set(t.payload, '{vehicles}', (
    SELECT jsonb_agg(CASE
        WHEN COALESCE(jsonb_typeof(v -> 'first_registered_on'), 'null') = 'null'
         AND jsonb_typeof(v -> 'odometer_at_purchase') = 'number' AND (v ->> 'odometer_at_purchase')::numeric > 0
         AND jsonb_typeof(v -> 'purchased_on') = 'string'
        THEN v || jsonb_build_object('first_registered_on', v -> 'purchased_on')
        ELSE v END ORDER BY ord)
    FROM jsonb_array_elements(t.payload -> 'vehicles') WITH ORDINALITY AS e (v, ord)))
WHERE jsonb_typeof(t.payload -> 'vehicles') = 'array' AND jsonb_array_length(t.payload -> 'vehicles') > 0;
ALTER TABLE vehicles
    ADD CONSTRAINT vehicles_bought_new_at_0_check CHECK (first_registered_on IS NOT NULL OR odometer_at_purchase = 0);

-- +goose Down
-- The dates filled in stay: they were what the schedules counted from.
ALTER TABLE vehicles DROP CONSTRAINT vehicles_bought_new_at_0_check;
