-- +goose Up
-- A service's parts always point at it, and nothing else points at a
-- service: kind = 'service' exactly when service_record_id is set. The
-- foreign key was ON DELETE SET NULL, so deleting a service or its vehicle
-- straight in the database left its parts as a 'service' change with no
-- service. Now that delete is refused (RESTRICT): the store returns a
-- service's parts and deletes their movements before the service
-- (DeleteService, UpdateService), and refuses to delete a vehicle that has
-- services.
--
-- The block below looks for rows that would break the new CHECK, in the
-- table and in the trash snapshots (which restore into it), and stops the
-- migration if it finds any, rather than fix them without a word.
-- +goose StatementBegin
DO $$
DECLARE
    ids text;
    problems text[] := '{}';
BEGIN
    SELECT string_agg(id::text, ', ' ORDER BY id) INTO ids
    FROM stock_movements WHERE kind = 'service' AND service_record_id IS NULL;
    IF ids IS NOT NULL THEN
        problems := problems || format('service parts without a service: stock_movements %s', ids);
    END IF;
    SELECT string_agg(id::text, ', ' ORDER BY id) INTO ids
    FROM stock_movements WHERE kind <> 'service' AND service_record_id IS NOT NULL;
    IF ids IS NOT NULL THEN
        problems := problems || format('other stock changes pointing at a service: stock_movements %s', ids);
    END IF;
    -- Snapshots hold arrays of rows under each table's name, next to
    -- other values (movement_id, form_key).
    SELECT array_to_string(array_agg(DISTINCT t.id ORDER BY t.id), ', ') INTO ids
    FROM trash t
    CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(t.payload -> 'stock_movements') = 'array'
                                                 THEN t.payload -> 'stock_movements' ELSE '[]' END) AS e(r)
    WHERE jsonb_typeof(e.r) = 'object' AND e.r ? 'kind'
      AND ((e.r ->> 'kind') = 'service') <> COALESCE(jsonb_typeof(e.r -> 'service_record_id') = 'number', false);
    IF ids IS NOT NULL THEN
        problems := problems || format('trash snapshots holding such stock changes: trash %s', ids);
    END IF;
    IF cardinality(problems) > 0 THEN
        RAISE EXCEPTION 'Migration 00014 (service parts need their service) stopped, nothing was changed: %. Correct them by hand (a service''s part whose service is gone can become a ''use''; empty service_record_id on anything else; purge such trash entries), then start Pitbox again.',
            array_to_string(problems, '; ');
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE stock_movements
    ADD CONSTRAINT stock_movements_service_record_check CHECK ((kind = 'service') = (service_record_id IS NOT NULL)),
    DROP CONSTRAINT stock_movements_service_record_id_fkey,
    ADD CONSTRAINT stock_movements_service_record_id_fkey
        FOREIGN KEY (service_record_id) REFERENCES service_records (id) ON DELETE RESTRICT;

-- +goose Down
ALTER TABLE stock_movements
    DROP CONSTRAINT stock_movements_service_record_check,
    DROP CONSTRAINT stock_movements_service_record_id_fkey,
    ADD CONSTRAINT stock_movements_service_record_id_fkey
        FOREIGN KEY (service_record_id) REFERENCES service_records (id) ON DELETE SET NULL;
