-- +goose Up
-- The whole schema. Migrations 00001–00022 were squashed into this one file
-- on 2026-11-21 (the old files are in git history), and reorder_columns
-- stopped using a temporary table; goose has version 1 for it in every
-- schema, public and dev included. Never edit this file: add 00002 and on.
--
-- Every table keeps one column order: id, then version, then its own
-- columns (related ones together, notes last), then created_at,
-- updated_at, archived_at and deleted_at, whichever it has (TestColumnOrder).
-- A migration that adds a column puts it in its place with reorder_columns
-- (below).
--
-- Quantities are in the item's base unit (pieces, millilitres, grams) with
-- at most two decimals, numeric(12,2): "1.234 g" would read as 1234 g, and
-- stock.ParseAmount refuses finer input rather than round it.
-- Calendar days (date columns) have no default: the day is decided in Go,
-- in the app's zone, never by the database (current_date is UTC's day), so
-- an insert that forgets one fails instead of quietly taking UTC's.
-- version is for optimistic concurrency: edit forms and record buttons
-- carry the version they were opened with and the store refuses a stale
-- one; the writes that bump it are listed in AGENTS.md.

-- Vehicles with services or kits are archived instead of deleted, so their
-- history stays. Distances are in the unit APP_DISTANCE names (km unless set
-- otherwise); nothing is converted. purchased_on and odometer_at_purchase
-- are the baseline for service intervals before any service is logged.
CREATE TABLE vehicles (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version integer NOT NULL DEFAULT 1,
    name text NOT NULL,
    kind text NOT NULL DEFAULT 'motorcycle' CHECK (kind IN ('motorcycle', 'car', 'other')),
    make text NOT NULL DEFAULT '',
    model text NOT NULL DEFAULT '',
    year integer CHECK (year BETWEEN 1900 AND 2100),
    plate text NOT NULL DEFAULT '',
    vin text NOT NULL DEFAULT '',
    color text NOT NULL DEFAULT '#f97316' CHECK (color ~ '^#[0-9a-fA-F]{6}$'),
    odometer integer CHECK (odometer >= 0),
    purchased_on date,
    odometer_at_purchase integer CHECK (odometer_at_purchase >= 0),
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);
-- Two vehicles can't share a name, whatever its case.
CREATE UNIQUE INDEX vehicles_name_key ON vehicles (lower(name));

-- Items with history are archived instead of deleted, so services, kits and
-- spending keep referring to them. Every change to quantity is a row in
-- stock_movements, written in the same transaction: the ledger adds up.
CREATE TABLE items (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version integer NOT NULL DEFAULT 1,
    name text NOT NULL CHECK (name <> ''),
    brand text NOT NULL DEFAULT '',
    category text NOT NULL DEFAULT '',
    unit text NOT NULL DEFAULT 'piece' CHECK (unit IN ('piece', 'ml', 'g')),
    quantity numeric(12,2) NOT NULL DEFAULT 0 CONSTRAINT items_quantity_non_negative CHECK (quantity >= 0),
    min_quantity numeric(12,2) CHECK (min_quantity >= 0),
    expires_on date,
    location text NOT NULL DEFAULT '',
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);
CREATE INDEX items_name_idx ON items (lower(name));

-- An item's part numbers and barcodes. brand's NOT NULL keeps the name it
-- got when the column was called label.
CREATE TABLE item_skus (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id bigint NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    code text NOT NULL CHECK (code <> ''),
    kind text NOT NULL DEFAULT 'oem' CHECK (kind IN ('oem', 'manufacturer', 'barcode', 'retailer', 'other')),
    brand text DEFAULT '' CONSTRAINT item_skus_label_not_null NOT NULL,
    pack_size numeric(12,2) CHECK (pack_size > 0),
    UNIQUE (item_id, code)
);
CREATE INDEX item_skus_code_idx ON item_skus (lower(code));

-- Which vehicles an item is for (its tags).
CREATE TABLE item_vehicles (
    item_id bigint NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    vehicle_id bigint NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, vehicle_id)
);
CREATE INDEX item_vehicles_vehicle_idx ON item_vehicles (vehicle_id);

CREATE TABLE item_photos (
    item_id bigint PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    content_type text NOT NULL,
    image bytea NOT NULL,
    thumb bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- A named list of parts for a recurring job on one vehicle, repeated every
-- interval_distance and/or interval_months, with an optional intermediate
-- schedule (inter_*) that has its own parts. Kits with logged services are
-- archived instead of deleted, so those services keep their kit.
CREATE TABLE service_kits (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version integer NOT NULL DEFAULT 1,
    vehicle_id bigint NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name <> ''),
    interval_distance integer CHECK (interval_distance > 0),
    interval_months integer CHECK (interval_months > 0),
    inter_interval_distance integer CHECK (inter_interval_distance > 0),
    inter_interval_months integer CHECK (inter_interval_months > 0),
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);
CREATE INDEX service_kits_vehicle_idx ON service_kits (vehicle_id);

-- A kit's parts; intermediate says which schedule they belong to.
CREATE TABLE service_kit_lines (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kit_id bigint NOT NULL REFERENCES service_kits (id) ON DELETE CASCADE,
    item_id bigint NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    quantity numeric(12,2) NOT NULL CHECK (quantity > 0),
    intermediate boolean NOT NULL DEFAULT false,
    UNIQUE (kit_id, item_id, intermediate)
);
-- Which kits use an item: the unique key above can't serve a lookup by item.
CREATE INDEX service_kit_lines_item_idx ON service_kit_lines (item_id);

-- A performed job. odometer_before is the reading logging it moved the
-- vehicle's odometer from, and odometer_moved whether it did (NULL for
-- services logged before that was kept), so deleting it can put it back.
-- odometer_at_log is the vehicle's reading when a service without its own
-- was logged, for distance reminders. A service without labour has none
-- (NULL), never 0.
CREATE TABLE service_records (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version integer NOT NULL DEFAULT 1,
    vehicle_id bigint NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    kit_id bigint REFERENCES service_kits (id) ON DELETE SET NULL,
    kit_intermediate boolean NOT NULL DEFAULT false,
    performed_on date NOT NULL,
    odometer integer CHECK (odometer >= 0),
    odometer_before integer CHECK (odometer_before >= 0),
    odometer_moved boolean,
    odometer_at_log integer CHECK (odometer_at_log >= 0),
    title text NOT NULL CHECK (title <> ''),
    labor_cost numeric(12,2) CHECK (labor_cost > 0),
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX service_records_vehicle_idx ON service_records (vehicle_id, performed_on DESC);
CREATE INDEX service_records_kit_idx ON service_records (kit_id, performed_on DESC);

-- The ledger: every change to items.quantity. Money figures read only
-- 'bought' rows. A service's parts always point at it, and nothing else
-- points at a service: kind = 'service' exactly when service_record_id is
-- set, and deleting a service with parts is refused (RESTRICT), so the
-- store returns the parts and deletes their rows first.
CREATE TABLE stock_movements (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version integer NOT NULL DEFAULT 1,
    item_id bigint NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('starting', 'bought', 'used', 'adjusted', 'service')),
    delta numeric(12,2) NOT NULL CHECK (delta <> 0),
    total_price numeric(12,2) CHECK (total_price >= 0),
    supplier text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    occurred_on date NOT NULL,
    service_record_id bigint REFERENCES service_records (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT stock_movements_service_record_check CHECK ((kind = 'service') = (service_record_id IS NOT NULL))
);
CREATE INDEX stock_movements_item_idx ON stock_movements (item_id, created_at DESC);
CREATE INDEX stock_movements_service_idx ON stock_movements (service_record_id);
CREATE INDEX stock_movements_created_idx ON stock_movements (created_at DESC);

-- An item, or free text with its own unit (an entry for an item uses the
-- item's). An item is on the list at most once.
CREATE TABLE shopping_entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version integer NOT NULL DEFAULT 1,
    item_id bigint REFERENCES items (id) ON DELETE CASCADE,
    free_text text NOT NULL DEFAULT '',
    quantity numeric(12,2) CHECK (quantity > 0),
    unit text NOT NULL DEFAULT 'piece' CHECK (unit IN ('piece', 'ml', 'g')),
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shopping_entries_check CHECK (item_id IS NOT NULL OR free_text <> '')
);
CREATE UNIQUE INDEX shopping_entries_item_key ON shopping_entries (item_id) WHERE item_id IS NOT NULL;

-- Deleted things, kept as a JSON snapshot of their rows (arrays under each
-- table's name) until the trash is emptied, so they restore with their
-- original ids. Snapshot keys are column names: a migration that renames a
-- column or changes its values rewrites the snapshots too.
CREATE TABLE trash (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('service', 'kit', 'movement', 'shopping', 'item', 'vehicle', 'photo', 'bought')),
    label text NOT NULL,
    payload jsonb NOT NULL,
    deleted_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX trash_deleted_idx ON trash (deleted_at DESC);
-- Restores ask whether a row is itself in the trash by containment
-- (payload @> '{"items": [{"id": 5}]}').
CREATE INDEX trash_payload_idx ON trash USING gin (payload jsonb_path_ops);

-- One-time form keys: a create or edit claims its form's key here first, so
-- posting the same form again (a double click, a retry, Back and Save) saves
-- nothing; result_id is the record the first save made or changed. Kept for
-- a day (claims prune older ones by created_at, an instant).
CREATE TABLE form_submissions (
    key text PRIMARY KEY CHECK (key ~ '^[0-9a-f]{32}$'),
    result_id bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX form_submissions_created_idx ON form_submissions (created_at);

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
-- The rows wait in an ordinary table, never a temporary one (Pitbox makes
-- no temporary tables: Postgres keeps a pg_temp schema pair for every
-- connection slot that ever made one).
-- +goose StatementBegin
CREATE FUNCTION reorder_columns(tbl regclass, cols text[]) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    rel      pg_class;
    qname    text;
    scratch  text; -- where the rows wait while the table is rebuilt
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
    scratch := format('%I.reorder_columns_rows', (SELECT nspname FROM pg_namespace WHERE oid = rel.relnamespace));
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
    IF to_regclass(scratch) IS NOT NULL THEN
        problems := problems || format('%s is in the way', scratch);
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
    -- on the table that the checks above didn't think of. The rows wait in
    -- an ordinary table, never a temporary one (Postgres keeps a pg_temp
    -- schema pair for every connection slot that ever made one); it is
    -- made and dropped in this transaction, so no other session sees it.
    EXECUTE format('CREATE TABLE %s AS SELECT * FROM %s', scratch, qname);
    FOREACH stmt IN ARRAY drops LOOP EXECUTE stmt; END LOOP;
    EXECUTE format('DROP TABLE %s', qname);
    EXECUTE format('CREATE TABLE %s (%s)', qname, array_to_string(defs, ', '));
    EXECUTE format('INSERT INTO %1$s (%2$s) OVERRIDING SYSTEM VALUE SELECT %2$s FROM %3$s', qname, list, scratch);
    FOREACH stmt IN ARRAY restore LOOP EXECUTE stmt; END LOOP;
    FOREACH stmt IN ARRAY adds LOOP EXECUTE stmt; END LOOP;
    EXECUTE format('DROP TABLE %s', scratch);
    EXECUTE format('ANALYZE %s', qname);
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION reorder_columns(regclass, text[]);
DROP TABLE form_submissions;
DROP TABLE trash;
DROP TABLE shopping_entries;
DROP TABLE stock_movements;
DROP TABLE service_records;
DROP TABLE service_kit_lines;
DROP TABLE service_kits;
DROP TABLE item_photos;
DROP TABLE item_vehicles;
DROP TABLE item_skus;
DROP TABLE items;
DROP TABLE vehicles;
