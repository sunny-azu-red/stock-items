-- +goose Up
-- Every table keeps one column order: id, then version, then its own
-- columns (related ones together, notes last), then created_at, updated_at,
-- archived_at and deleted_at, whichever it has (TestColumnOrder). Postgres
-- only ever adds a column at the end, so reorder_columns rebuilds a table in
-- the order given, keeping its rows, ids, sequence values, defaults,
-- constraints and indexes under their names, and the foreign keys of other
-- tables pointing at it. A later migration that adds a column calls it
-- right after the ADD COLUMN.
--
-- It refuses (and changes nothing) when the list isn't exactly the table's
-- columns, or the table has something a rebuild would lose without a word:
-- triggers, rules or views, policies, inheritance, grants, comments, and so
-- on. Trash snapshots are jsonb, whose keys have no order, and restore by
-- column name, so they don't change; neither does any value or version.
-- +goose StatementBegin
CREATE FUNCTION reorder_columns(tbl regclass, cols text[]) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    rel      pg_class;
    qname    text;
    have     text[];
    list     text;
    problems text[] := '{}';
    defs     text[] := '{}';
    restore  text[] := '{}'; -- setval, constraints and indexes, in that order
    drops    text[] := '{}'; -- other tables' foreign keys to this one
    adds     text[] := '{}';
    stmt     text;
    r        record;
    last     bigint;
    called   boolean;
BEGIN
    SELECT * INTO rel FROM pg_class WHERE oid = tbl;
    qname := format('%I.%I', (SELECT nspname FROM pg_namespace WHERE oid = rel.relnamespace), rel.relname);
    EXECUTE format('LOCK TABLE %s IN ACCESS EXCLUSIVE MODE', qname);

    -- The list is the table's columns, each once.
    SELECT array_agg(attname::text ORDER BY attnum) INTO have
    FROM pg_attribute WHERE attrelid = tbl AND attnum > 0 AND NOT attisdropped;
    IF cols IS NULL OR array_position(cols, NULL) IS NOT NULL THEN
        problems := problems || 'the column list holds NULL'::text;
    END IF;
    SELECT string_agg(c, ', ' ORDER BY n) INTO list FROM unnest(have) WITH ORDINALITY AS u(c, n) WHERE NOT c = ANY(COALESCE(cols, '{}'));
    IF list IS NOT NULL THEN problems := problems || format('columns missing from the list: %s', list); END IF;
    SELECT string_agg(c, ', ' ORDER BY n) INTO list FROM unnest(cols) WITH ORDINALITY AS u(c, n) WHERE NOT c = ANY(have);
    IF list IS NOT NULL THEN problems := problems || format('no such columns: %s', list); END IF;
    SELECT string_agg(c, ', ' ORDER BY c) INTO list FROM (SELECT c FROM unnest(cols) AS u(c) GROUP BY c HAVING count(*) > 1) AS d;
    IF list IS NOT NULL THEN problems := problems || format('columns listed twice: %s', list); END IF;

    -- What a rebuild would lose or change.
    IF rel.relkind <> 'r' OR rel.relpersistence <> 'p' OR rel.reloftype <> 0 THEN
        problems := problems || 'not a plain table'::text;
    END IF;
    IF rel.relispartition OR EXISTS (SELECT 1 FROM pg_inherits WHERE inhrelid = tbl OR inhparent = tbl) THEN
        problems := problems || 'inheritance or partitions'::text;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = tbl AND NOT tgisinternal) THEN
        problems := problems || 'triggers'::text;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_depend WHERE classid = 'pg_rewrite'::regclass AND refclassid = 'pg_class'::regclass AND refobjid = tbl) THEN
        problems := problems || 'rules or views'::text;
    END IF;
    IF rel.relrowsecurity OR EXISTS (SELECT 1 FROM pg_policy WHERE polrelid = tbl) THEN
        problems := problems || 'row security'::text;
    END IF;
    IF pg_get_userbyid(rel.relowner) <> current_user THEN
        problems := problems || format('owned by %s, not %s', pg_get_userbyid(rel.relowner), current_user);
    END IF;
    IF rel.relacl IS NOT NULL OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = tbl AND attacl IS NOT NULL) THEN
        problems := problems || 'grants'::text;
    END IF;
    IF rel.reloptions IS NOT NULL OR rel.relreplident <> 'd' THEN
        problems := problems || 'storage options or a replica identity'::text;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_description
               WHERE (classoid = 'pg_class'::regclass AND objoid IN (SELECT tbl UNION SELECT indexrelid FROM pg_index WHERE indrelid = tbl))
                  OR (classoid = 'pg_constraint'::regclass AND objoid IN (SELECT oid FROM pg_constraint WHERE conrelid = tbl))) THEN
        problems := problems || 'comments'::text;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_publication_rel WHERE prrelid = tbl)
       OR EXISTS (SELECT 1 FROM pg_statistic_ext WHERE stxrelid = tbl) THEN
        problems := problems || 'publications or extended statistics'::text;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_attribute a JOIN pg_type t ON t.oid = a.atttypid
               WHERE a.attrelid = tbl AND a.attnum > 0 AND NOT a.attisdropped
                 AND (a.attgenerated <> '' OR a.attcollation <> t.typcollation)) THEN
        problems := problems || 'generated columns or column collations'::text;
    END IF;
    -- Sequences: only identity columns' own, under the name a new one gets,
    -- with the default options; a serial's sequence would go with the table.
    IF EXISTS (SELECT 1 FROM pg_depend d JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S'
               JOIN pg_sequence q ON q.seqrelid = s.oid
               JOIN pg_attribute a ON a.attrelid = tbl AND a.attnum = d.refobjsubid
               WHERE d.classid = 'pg_class'::regclass AND d.refclassid = 'pg_class'::regclass AND d.refobjid = tbl
                 AND (d.deptype <> 'i' OR s.relname <> format('%s_%s_seq', rel.relname, a.attname)
                      OR s.relnamespace <> rel.relnamespace OR q.seqtypid <> a.atttypid
                      OR q.seqstart <> 1 OR q.seqincrement <> 1 OR q.seqmin <> 1 OR q.seqcache <> 1 OR q.seqcycle)) THEN
        problems := problems || 'sequences other than identity columns'' own with default options'::text;
    END IF;
    IF cardinality(problems) > 0 THEN
        RAISE EXCEPTION 'reorder_columns(%) stopped, nothing was changed: %.', qname, array_to_string(problems, '; ');
    END IF;

    -- The columns in the new order, with their defaults, identity and NOT
    -- NULL (and its constraint name, since Postgres 18 names them).
    FOR r IN
        SELECT a.attname, format_type(a.atttypid, a.atttypmod) AS typ, a.attnotnull, a.attidentity,
               pg_get_expr(d.adbin, d.adrelid) AS def,
               (SELECT c.conname FROM pg_constraint c WHERE c.conrelid = tbl AND c.contype = 'n' AND c.conkey = ARRAY[a.attnum]) AS nn
        FROM unnest(cols) WITH ORDINALITY AS u(c, n)
        JOIN pg_attribute a ON a.attrelid = tbl AND a.attname = u.c
        LEFT JOIN pg_attrdef d ON d.adrelid = tbl AND d.adnum = a.attnum
        ORDER BY u.n
    LOOP
        defs := defs || concat_ws(' ', quote_ident(r.attname), r.typ,
            CASE r.attidentity WHEN 'a' THEN 'GENERATED ALWAYS AS IDENTITY' WHEN 'd' THEN 'GENERATED BY DEFAULT AS IDENTITY' END,
            'DEFAULT ' || r.def,
            CASE WHEN r.attnotnull THEN COALESCE('CONSTRAINT ' || quote_ident(r.nn) || ' ', '') || 'NOT NULL' END);
        IF r.attidentity <> '' THEN
            EXECUTE format('SELECT last_value, is_called FROM %s', pg_get_serial_sequence(qname, r.attname)) INTO last, called;
            restore := restore || format('SELECT setval(pg_get_serial_sequence(%L, %L), %s, %L)', qname, r.attname, last, called);
        END IF;
    END LOOP;
    SELECT string_agg(quote_ident(c), ', ' ORDER BY n) INTO list FROM unnest(cols) WITH ORDINALITY AS u(c, n);

    -- Its constraints (keys before the checks and foreign keys that may
    -- need them), then the indexes no constraint owns.
    FOR r IN
        SELECT conname, pg_get_constraintdef(oid) AS def FROM pg_constraint
        WHERE conrelid = tbl AND contype IN ('p', 'u', 'x', 'c', 'f')
        ORDER BY position(contype::text IN 'puxcf'), conname
    LOOP
        restore := restore || format('ALTER TABLE %s ADD CONSTRAINT %I %s', qname, r.conname, r.def);
    END LOOP;
    FOR r IN
        SELECT pg_get_indexdef(i.indexrelid) AS def FROM pg_index i
        WHERE i.indrelid = tbl
          AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conrelid = tbl AND c.conindid = i.indexrelid AND c.contype IN ('p', 'u', 'x'))
        ORDER BY i.indexrelid::regclass::text
    LOOP
        restore := restore || r.def;
    END LOOP;
    FOR r IN
        SELECT conrelid::regclass::text AS other, conname, pg_get_constraintdef(oid) AS def FROM pg_constraint
        WHERE confrelid = tbl AND conrelid <> tbl AND contype = 'f'
        ORDER BY conrelid::regclass::text, conname
    LOOP
        drops := drops || format('ALTER TABLE %s DROP CONSTRAINT %I', r.other, r.conname);
        adds := adds || format('ALTER TABLE %s ADD CONSTRAINT %I %s', r.other, r.conname, r.def);
    END LOOP;

    -- Rebuild. DROP TABLE without CASCADE fails on anything still depending
    -- on the table that the checks above didn't think of.
    EXECUTE format('CREATE TEMP TABLE pg_temp.reorder_columns_rows AS SELECT * FROM %s', qname);
    FOREACH stmt IN ARRAY drops LOOP EXECUTE stmt; END LOOP;
    EXECUTE format('DROP TABLE %s', qname);
    EXECUTE format('CREATE TABLE %s (%s)', qname, array_to_string(defs, ', '));
    EXECUTE format('INSERT INTO %1$s (%2$s) OVERRIDING SYSTEM VALUE SELECT %2$s FROM pg_temp.reorder_columns_rows', qname, list);
    FOREACH stmt IN ARRAY restore LOOP EXECUTE stmt; END LOOP;
    FOREACH stmt IN ARRAY adds LOOP EXECUTE stmt; END LOOP;
    DROP TABLE pg_temp.reorder_columns_rows;
    EXECUTE format('ANALYZE %s', qname);
END
$$;
-- +goose StatementEnd

SELECT reorder_columns('vehicles', ARRAY['id', 'version', 'name', 'kind', 'make', 'model', 'year', 'plate', 'vin', 'color',
    'odometer', 'purchased_on', 'odometer_at_purchase', 'notes', 'created_at', 'updated_at', 'archived_at']);
SELECT reorder_columns('items', ARRAY['id', 'version', 'name', 'brand', 'category', 'unit', 'quantity', 'min_quantity',
    'expires_on', 'location', 'notes', 'created_at', 'updated_at', 'archived_at']);
SELECT reorder_columns('service_kits', ARRAY['id', 'version', 'vehicle_id', 'name', 'interval_distance', 'interval_months',
    'inter_interval_distance', 'inter_interval_months', 'notes', 'created_at', 'updated_at', 'archived_at']);
SELECT reorder_columns('service_records', ARRAY['id', 'version', 'vehicle_id', 'kit_id', 'kit_intermediate', 'performed_on',
    'odometer', 'odometer_before', 'odometer_moved', 'odometer_at_log', 'title', 'labor_cost', 'notes', 'created_at']);
SELECT reorder_columns('stock_movements', ARRAY['id', 'version', 'item_id', 'kind', 'delta', 'total_price', 'supplier',
    'note', 'occurred_on', 'service_record_id', 'created_at']);
SELECT reorder_columns('shopping_entries', ARRAY['id', 'version', 'item_id', 'free_text', 'quantity', 'unit', 'note',
    'created_at']);

-- +goose Down
-- The tables go back to the order the earlier migrations left them in.
SELECT reorder_columns('vehicles', ARRAY['id', 'name', 'kind', 'make', 'model', 'year', 'plate', 'vin', 'color', 'odometer',
    'notes', 'created_at', 'updated_at', 'archived_at', 'version', 'purchased_on', 'odometer_at_purchase']);
SELECT reorder_columns('items', ARRAY['id', 'name', 'brand', 'category', 'unit', 'quantity', 'min_quantity', 'expires_on',
    'location', 'notes', 'created_at', 'updated_at', 'archived_at', 'version']);
SELECT reorder_columns('service_kits', ARRAY['id', 'vehicle_id', 'name', 'interval_distance', 'interval_months', 'notes',
    'created_at', 'updated_at', 'archived_at', 'version', 'inter_interval_distance', 'inter_interval_months']);
SELECT reorder_columns('service_records', ARRAY['id', 'vehicle_id', 'kit_id', 'performed_on', 'odometer', 'title', 'notes',
    'labor_cost', 'created_at', 'odometer_before', 'odometer_moved', 'odometer_at_log', 'version', 'kit_intermediate']);
SELECT reorder_columns('stock_movements', ARRAY['id', 'item_id', 'kind', 'delta', 'total_price', 'supplier', 'note',
    'occurred_on', 'service_record_id', 'created_at', 'version']);
SELECT reorder_columns('shopping_entries', ARRAY['id', 'item_id', 'free_text', 'quantity', 'note', 'created_at', 'unit',
    'version']);

DROP FUNCTION reorder_columns(regclass, text[]);
