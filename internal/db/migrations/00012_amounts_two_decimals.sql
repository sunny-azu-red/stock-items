-- +goose Up
-- Amounts (pieces, ml, g) keep at most two decimals of their base unit:
-- "1.234 g" would read as 1234 g, and the pages show two decimals, so a
-- third only made "not enough stock: have 0.23 g, need 0.23 g" possible.
-- stock.ParseAmount refuses finer input; from here on the columns hold no
-- more either.
--
-- Changing the type would round a finer value without a word, so the
-- block below looks for one first, in the tables and in the trash
-- snapshots (which restore into these columns), and stops the migration
-- if it finds any. The data audited before this change had none. CHECK
-- constraints stay as they are.
-- +goose StatementBegin
DO $$
DECLARE
    c record;
    n bigint;
    example numeric;
    problems text[] := '{}';
BEGIN
    FOR c IN SELECT * FROM (VALUES
        ('items', 'quantity'), ('items', 'min_quantity'), ('item_skus', 'pack_size'),
        ('service_kit_lines', 'quantity'), ('stock_movements', 'delta'), ('shopping_entries', 'quantity')
    ) AS v(tab, col)
    LOOP
        EXECUTE format('SELECT count(*), min(%1$I) FROM %2$I WHERE %1$I <> round(%1$I, 2)', c.col, c.tab)
            INTO n, example;
        IF n > 0 THEN
            problems := problems || format('%s.%s: %s %s (e.g. %s)', c.tab, c.col, n,
                CASE WHEN n = 1 THEN 'row' ELSE 'rows' END, example);
        END IF;
    END LOOP;
    -- Snapshots hold arrays of rows under each table's name, next to
    -- other values (movement_id, form_key).
    FOR c IN
        SELECT s.tab, a.col, count(*) AS n, min((e.r ->> a.col)::numeric) AS example
        FROM trash t
        CROSS JOIN LATERAL jsonb_each(t.payload) AS s(tab, rows)
        CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(s.rows) = 'array' THEN s.rows ELSE '[]' END) AS e(r)
        CROSS JOIN unnest(ARRAY['quantity', 'min_quantity', 'delta', 'pack_size']) AS a(col)
        WHERE jsonb_typeof(e.r) = 'object' AND jsonb_typeof(e.r -> a.col) = 'number'
          AND (e.r ->> a.col)::numeric <> round((e.r ->> a.col)::numeric, 2)
        GROUP BY s.tab, a.col
        ORDER BY s.tab, a.col
    LOOP
        problems := problems || format('trash snapshots, %s.%s: %s %s (e.g. %s)', c.tab, c.col, c.n,
            CASE WHEN c.n = 1 THEN 'row' ELSE 'rows' END, c.example);
    END LOOP;
    IF cardinality(problems) > 0 THEN
        RAISE EXCEPTION 'Migration 00012 (amounts with two decimals) stopped, nothing was changed: amounts finer than 0.01 found (%). Changing the columns would round them. Correct them by hand (an item''s quantity must stay the sum of its stock changes), then start Pitbox again.',
            array_to_string(problems, '; ');
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE items ALTER COLUMN quantity TYPE numeric(12,2), ALTER COLUMN min_quantity TYPE numeric(12,2);
ALTER TABLE item_skus ALTER COLUMN pack_size TYPE numeric(12,2);
ALTER TABLE service_kit_lines ALTER COLUMN quantity TYPE numeric(12,2);
ALTER TABLE stock_movements ALTER COLUMN delta TYPE numeric(12,2);
ALTER TABLE shopping_entries ALTER COLUMN quantity TYPE numeric(12,2);

-- +goose Down
-- Three decimals hold every two-decimal value: nothing changes.
ALTER TABLE shopping_entries ALTER COLUMN quantity TYPE numeric(12,3);
ALTER TABLE stock_movements ALTER COLUMN delta TYPE numeric(12,3);
ALTER TABLE service_kit_lines ALTER COLUMN quantity TYPE numeric(12,3);
ALTER TABLE item_skus ALTER COLUMN pack_size TYPE numeric(12,3);
ALTER TABLE items ALTER COLUMN quantity TYPE numeric(12,3), ALTER COLUMN min_quantity TYPE numeric(12,3);
