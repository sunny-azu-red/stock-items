package db

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// timestampColumns end a table, in this order, whichever it has.
var timestampColumns = []string{"created_at", "updated_at", "archived_at", "deleted_at"}

// columnOrder returns each table's columns in their order (goose's own table
// left out).
func columnOrder(t *testing.T, pool *pgxpool.Pool) map[string][]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname, array_agg(a.attname::text ORDER BY a.attnum)
		FROM pg_class c JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE c.relnamespace = current_schema()::regnamespace AND c.relkind = 'r' AND c.relname <> 'goose_db_version'
		GROUP BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	order := map[string][]string{}
	for rows.Next() {
		var table string
		var cols []string
		if err := rows.Scan(&table, &cols); err != nil {
			t.Fatal(err)
		}
		order[table] = cols
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return order
}

// Every table, as the migrations leave it, has id first, then version, and
// the timestamps last in their order (AGENTS.md, "Column order"). A column
// added with ADD COLUMN lands after the timestamps: call reorder_columns in
// the same migration.
func TestColumnOrder(t *testing.T) {
	pool, provider := migrationDB(t)
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	order := columnOrder(t, pool)
	if len(order) == 0 {
		t.Fatal("no tables")
	}
	for table, cols := range order {
		var want []string // id, version, the rest, the timestamps
		for _, c := range []string{"id", "version"} {
			if slices.Contains(cols, c) {
				want = append(want, c)
			}
		}
		for _, c := range cols {
			if !slices.Contains(want, c) && !slices.Contains(timestampColumns, c) {
				want = append(want, c)
			}
		}
		for _, c := range timestampColumns {
			if slices.Contains(cols, c) {
				want = append(want, c)
			}
		}
		if !slices.Equal(cols, want) {
			t.Errorf("%s: columns %s, want %s (id, version, its own columns, then %s; add a column with reorder_columns, see AGENTS.md)",
				table, strings.Join(cols, ", "), strings.Join(want, ", "), strings.Join(timestampColumns, ", "))
		}
	}
}

// schemaShape lists the tables' columns (as a set: name, type, NOT NULL,
// default, identity), constraints, indexes and sequence values: everything
// but the order of the columns. Equal shapes mean a rebuild kept it all.
func schemaShape(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var shape string
	err := pool.QueryRow(context.Background(), `
		SELECT string_agg(x, E'\n' ORDER BY x) FROM (
			SELECT format('column %s.%s %s notnull=%s default=%s identity=%s', c.relname, a.attname,
			              format_type(a.atttypid, a.atttypmod), a.attnotnull, pg_get_expr(d.adbin, d.adrelid), a.attidentity) AS x
			FROM pg_class c
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
			LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
			WHERE c.relnamespace = current_schema()::regnamespace AND c.relkind = 'r' AND c.relname <> 'goose_db_version'
			UNION ALL
			SELECT format('constraint %s.%s %s %s', conrelid::regclass, conname, contype, pg_get_constraintdef(oid))
			FROM pg_constraint WHERE connamespace = current_schema()::regnamespace AND conrelid <> 'goose_db_version'::regclass
			UNION ALL
			SELECT format('index %s', indexdef) FROM pg_indexes
			WHERE schemaname = current_schema() AND tablename <> 'goose_db_version'
			UNION ALL
			SELECT format('sequence %s %s', sequencename, last_value) FROM pg_sequences
			WHERE schemaname = current_schema() AND sequencename NOT LIKE 'goose%'
		) s`).Scan(&shape)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

// Another column order for six tables, one that moves every column of
// each: a rebuild into it and back must keep everything.
var oldOrder = []struct {
	table string
	cols  []string
}{
	{"vehicles", []string{"id", "name", "kind", "make", "model", "year", "plate", "vin", "color", "odometer",
		"notes", "created_at", "updated_at", "archived_at", "version", "purchased_on", "odometer_at_purchase",
		"warranty_ends_on", "warranty_ends_at"}},
	{"items", []string{"id", "name", "brand", "category", "unit", "quantity", "min_quantity", "expires_on",
		"location", "notes", "created_at", "updated_at", "archived_at", "version"}},
	{"service_kits", []string{"id", "vehicle_id", "name", "interval_distance", "interval_months", "notes",
		"created_at", "updated_at", "archived_at", "version", "in_between", "break_in_at"}},
	{"service_records", []string{"id", "vehicle_id", "kit_id", "performed_on", "odometer", "title", "notes",
		"labor_cost", "created_at", "odometer_before", "odometer_moved", "odometer_at_log", "version", "kit_tier"}},
	{"stock_movements", []string{"id", "item_id", "kind", "delta", "total_price", "supplier", "note",
		"occurred_on", "service_record_id", "created_at", "version"}},
	{"shopping_entries", []string{"id", "item_id", "free_text", "quantity", "note", "created_at", "unit", "version"}},
}

// reorder rebuilds each table in the order given, in one transaction, and
// fails if that gave the session a temporary schema (reorder_columns keeps
// the rows in an ordinary table).
func reorder(t *testing.T, pool *pgxpool.Pool, orders map[string][]string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, o := range oldOrder { // the tables in a fixed order
		if cols, ok := orders[o.table]; ok {
			if _, err := tx.Exec(ctx, `SELECT reorder_columns($1::text::regclass, $2::text[])`, o.table, cols); err != nil {
				t.Fatalf("reorder_columns(%s): %v", o.table, err)
			}
		}
	}
	var temp int64
	if err := tx.QueryRow(ctx, `SELECT pg_my_temp_schema()::bigint`).Scan(&temp); err != nil || temp != 0 {
		t.Errorf("the rebuild gave the session a temporary schema (%d, %v)", temp, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// reorder_columns only moves columns: the rows, ids, sequences, defaults,
// constraints, indexes and foreign keys (also other tables' to the rebuilt
// ones) stay as they were, putting the order back restores it exactly, and
// a trash snapshot taken before still restores.
func TestReorderColumnsKeepsEverything(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// Every rebuilt table and each table pointing at one, with deleted last
	// rows, so the sequences are past the highest id (the trash holds them).
	if _, err := pool.Exec(ctx, `
		INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES
			('KTM 390', 12000, '2024-03-01', 10), ('Golf', 0, '2020-01-01', 0), ('Sold', 1, '2020-01-01', 0);
		INSERT INTO items (name, brand, unit, quantity, min_quantity, expires_on, location) VALUES
			('Rear Brake Pads', 'OEM', 'piece', 2, 1, NULL, 'Shelf'), ('15W50', 'Silkolene', 'ml', 1525.5, 1000, '2029-06-30', ''),
			('Gone', '', 'g', 0, NULL, NULL, '');
		INSERT INTO item_skus (item_id, code, kind, brand, pack_size) VALUES (1, '77-038', 'oem', 'KTM', 2), (2, 'SK-1', 'barcode', '', 1000.5);
		INSERT INTO item_vehicles (item_id, vehicle_id) VALUES (1, 1), (2, 1), (2, 2);
		INSERT INTO item_photos (item_id, content_type, image, thumb) VALUES (1, 'image/jpeg', '\xffd8ff', '\xffd8');
		INSERT INTO service_kits (vehicle_id, name, interval_distance, interval_months, in_between, archived_at)
			VALUES (1, 'Oil service', 7500, 12, 1, NULL), (1, 'Old kit', NULL, 6, 0, now());
		INSERT INTO service_kit_lines (kit_id, item_id, quantity, intermediate) VALUES (1, 2, 1700, false), (1, 2, 1000, true);
		INSERT INTO service_records (vehicle_id, kit_id, kit_tier, performed_on, odometer, odometer_before, odometer_moved, title, labor_cost)
			VALUES (1, 1, 'intermediate', '2026-05-01', 12000, 11000, true, 'Oil service', 80), (2, NULL, 'full', '2026-05-02', NULL, NULL, false, 'Wash', NULL);
		INSERT INTO stock_movements (item_id, kind, delta, total_price, supplier, occurred_on, service_record_id) VALUES
			(1, 'starting', 2, NULL, '', '2026-01-01', NULL), (2, 'bought', 2525.5, 19.90, 'Moto shop', '2026-03-01', NULL),
			(2, 'service', -1000, NULL, '', '2026-05-01', 1);
		INSERT INTO shopping_entries (item_id, free_text, quantity, unit, note) VALUES (1, '', 2, 'piece', 'pads'), (NULL, 'Coolant', 4000, 'ml', '');
		UPDATE items SET version = 4 WHERE id = 1;
		INSERT INTO trash (kind, label, payload) SELECT 'vehicle', 'Vehicle: Sold', jsonb_build_object('vehicles', jsonb_agg(to_jsonb(v))) FROM vehicles v WHERE id = 3;
		DELETE FROM vehicles WHERE id = 3;
		DELETE FROM items WHERE id = 3;
	`); err != nil {
		t.Fatal(err)
	}
	before, order, data := schemaShape(t, pool), columnOrder(t, pool), dump(t, pool, "")

	unchanged := func(when string) {
		t.Helper()
		if got := schemaShape(t, pool); got != before {
			t.Errorf("%s: the schema changed beyond the column order:\n%s", when, lineDiff(got, before))
		}
		if got := dump(t, pool, ""); got != data {
			t.Errorf("%s: the data changed:\n%s\nwant:\n%s", when, got, data)
		}
		checkLedger(t, pool)
	}

	moved := map[string][]string{}
	for _, o := range oldOrder {
		moved[o.table] = o.cols
	}
	reorder(t, pool, moved)
	unchanged("after moving the columns")
	got := columnOrder(t, pool)
	for table, cols := range order {
		want := cols
		if m, ok := moved[table]; ok {
			want = m
		}
		if !slices.Equal(got[table], want) {
			t.Errorf("after moving the columns: %s columns = %v, want %v", table, got[table], want)
		}
	}
	// The foreign keys still do what they did: deleting an item takes its
	// SKUs, tags, photo, kit lines and stock changes; a service with parts
	// can't go; a vehicle still can't be named twice.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var left int
	if _, err := tx.Exec(ctx, `DELETE FROM items WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM item_skus WHERE item_id = 2) + (SELECT count(*) FROM item_vehicles WHERE item_id = 2)
		+ (SELECT count(*) FROM service_kit_lines WHERE item_id = 2) + (SELECT count(*) FROM stock_movements WHERE item_id = 2)`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("after moving the columns: deleting an item left %d rows pointing at it", left)
	}
	_ = tx.Rollback(ctx)
	if err := refused(t, pool, `DELETE FROM service_records WHERE id = 1`); err == nil || !strings.Contains(err.Error(), "stock_movements_service_record_id_fkey") {
		t.Errorf("after moving the columns: deleting a service with parts: %v, want the foreign key to refuse it", err)
	}
	if err := refused(t, pool, `INSERT INTO vehicles (id, name, odometer, purchased_on, odometer_at_purchase) OVERRIDING SYSTEM VALUE VALUES (100, 'golf', 0, '2020-01-01', 0)`); err == nil || !strings.Contains(err.Error(), "vehicles_name_key") {
		t.Errorf("after moving the columns: a second Golf: %v, want the unique index to refuse it", err)
	}
	if err := refused(t, pool, `UPDATE items SET quantity = -1 WHERE id = 1`); err == nil || !strings.Contains(err.Error(), "items_quantity_non_negative") {
		t.Errorf("after moving the columns: a negative quantity: %v, want the CHECK to refuse it", err)
	}

	reorder(t, pool, order)
	unchanged("after moving them back")
	if got := columnOrder(t, pool); !mapsEqual(got, order) {
		t.Errorf("after moving them back: columns = %v, want %v", got, order)
	}
	// A new row takes the id after the deleted one, and the deleted one
	// restores from its snapshot, as restoreRows does it.
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('New', 0, '2020-01-01', 0) RETURNING id`).Scan(&id); err != nil || id != 4 {
		t.Errorf("new vehicle id = %d (%v), want 4", id, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO vehicles (id, version, name, kind, odometer, purchased_on, odometer_at_purchase, created_at) OVERRIDING SYSTEM VALUE
		SELECT id, version, name, kind, odometer, purchased_on, odometer_at_purchase, created_at
		FROM jsonb_populate_recordset(NULL::vehicles, (SELECT payload -> 'vehicles' FROM trash WHERE label = 'Vehicle: Sold'))`); err != nil {
		t.Errorf("restoring the snapshot taken before: %v", err)
	}
}

// lineDiff lists the lines only got has (+) and only want has (-).
func lineDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	var out []string
	for _, l := range g {
		if !slices.Contains(w, l) {
			out = append(out, "+ "+l)
		}
	}
	for _, l := range w {
		if !slices.Contains(g, l) {
			out = append(out, "- "+l)
		}
	}
	return strings.Join(out, "\n")
}

func mapsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// reorder_columns refuses (changing nothing) a list that isn't exactly the
// table's columns, and a table with something a rebuild would lose; it
// rebuilds a table without an identity column, keeping its key.
func TestReorderColumns(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	const all = `ARRAY['id', 'item_id', 'code', 'kind', 'brand', 'pack_size']`
	for _, tc := range []struct{ name, sql, want string }{
		{"missing", `SELECT reorder_columns('item_skus', ARRAY['id', 'item_id', 'code'])`, "columns missing from the list: kind, brand, pack_size"},
		{"unknown", `SELECT reorder_columns('item_skus', ` + all + ` || 'label'::text)`, "no such columns: label"},
		{"twice", `SELECT reorder_columns('item_skus', ` + all + ` || 'code'::text)`, "columns listed twice: code"},
		{"null", `SELECT reorder_columns('item_skus', ` + all + ` || NULL::text)`, "the column list holds NULL"},
		{"trigger", `CREATE FUNCTION t() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END';
			CREATE TRIGGER t BEFORE INSERT ON item_skus FOR EACH ROW EXECUTE FUNCTION t();
			SELECT reorder_columns('item_skus', ` + all + `)`, "triggers"},
		{"view", `CREATE VIEW v AS SELECT code FROM item_skus; SELECT reorder_columns('item_skus', ` + all + `)`, "rules or views"},
		{"comment", `COMMENT ON COLUMN item_skus.code IS 'x'; SELECT reorder_columns('item_skus', ` + all + `)`, "comments"},
		{"grant", `GRANT SELECT ON item_skus TO PUBLIC; SELECT reorder_columns('item_skus', ` + all + `)`, "grants"},
		{"scratch table", `CREATE TABLE reorder_columns_rows (x int); SELECT reorder_columns('item_skus', ` + all + `)`, "reorder_columns_rows is in the way"},
	} {
		err := refused(t, pool, tc.sql)
		if err == nil || !strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want it refused with %q", tc.name, err, tc.want)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('KTM', 0, '2020-01-01', 0);
		INSERT INTO items (name) VALUES ('Pads');
		INSERT INTO item_vehicles (item_id, vehicle_id) VALUES (1, 1);
		SELECT reorder_columns('item_vehicles', ARRAY['vehicle_id', 'item_id'])`); err != nil {
		t.Fatal(err)
	}
	var cols, pkey string
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT string_agg(attname, ',' ORDER BY attnum) FROM pg_attribute WHERE attrelid = 'item_vehicles'::regclass AND attnum > 0 AND NOT attisdropped),
		       (SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'item_vehicles'::regclass AND conname = 'item_vehicles_pkey')`).Scan(&cols, &pkey); err != nil {
		t.Fatal(err)
	}
	if cols != "vehicle_id,item_id" || pkey != "PRIMARY KEY (item_id, vehicle_id)" {
		t.Errorf("item_vehicles: columns %s, key %s", cols, pkey)
	}
	var temp int64
	if err := tx.QueryRow(ctx, `SELECT pg_my_temp_schema()::bigint`).Scan(&temp); err != nil || temp != 0 {
		t.Errorf("the rebuild gave the session a temporary schema (%d, %v)", temp, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO item_vehicles (item_id, vehicle_id) VALUES (1, 1)`); err == nil {
		t.Error("item_vehicles: the key no longer refuses a repeat")
	}
}
