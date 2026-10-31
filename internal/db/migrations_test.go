package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sunny-azu-red/stock-items/internal/config"
)

// migrationDB opens a throwaway test_* schema with no migrations applied,
// and a goose provider for stepping through them. (internal/testdb
// migrates all the way, and it can't be imported from here.)
func migrationDB(t *testing.T) (*pgxpool.Pool, *goose.Provider) {
	t.Helper()
	dbCfg, err := config.LoadDB()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("DB_HOST") == "" {
		t.Skip("no database configured (set DB_* or create .env)")
	}
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	dbCfg.Schema = "test_" + hex.EncodeToString(buf)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, dbCfg)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := DropSchema(context.Background(), pool, dbCfg.Schema); err != nil {
			t.Errorf("drop schema %s: %v", dbCfg.Schema, err)
		}
		pool.Close()
	})
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { sqlDB.Close() })
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub)
	if err != nil {
		t.Fatal(err)
	}
	return pool, provider
}

// realShape is data shaped like the real database before 00010: items in
// all three units with photos, part numbers and tags, their history of
// stock changes, vehicles, and (not in the real data, but possible) a kit,
// a service, shopping entries and a trash snapshot.
const realShape = `
INSERT INTO vehicles (name, odometer_km) VALUES ('KTM 390', 12000), ('KTM 1290', 30000), ('Golf', NULL);
INSERT INTO items (name, brand, unit, quantity, min_quantity, expires_on) VALUES
  ('Rear Brake Pads', 'OEM', 'piece', 2, 1, NULL),
  ('15W50 PRO 4 XP', 'Silkolene', 'ml', 1525.5, 1000, '2029-06-30'),
  ('Grease', 'Motorex', 'g', 399.25, NULL, NULL);
INSERT INTO item_skus (item_id, code, pack_size) VALUES (1, '77-038-005-044', 2), (2, 'SK-1', 1000.5);
INSERT INTO item_vehicles VALUES (1, 1), (2, 1), (2, 2), (3, 3);
INSERT INTO item_photos (item_id, content_type, image, thumb) SELECT id, 'image/jpeg', '\xffd8ff'::bytea, '\xffd8'::bytea FROM items;
INSERT INTO stock_movements (item_id, kind, delta, total_price, supplier, occurred_on) VALUES
  (1, 'initial', 3, NULL, '', '2026-01-01'), (1, 'use', -1, NULL, '', '2026-02-01'),
  (2, 'initial', 525.5, NULL, '', '2026-01-01'), (2, 'purchase', 1000, 19.90, 'Moto shop', '2026-03-01'),
  (3, 'purchase', 400, 8.50, '', '2026-01-01'), (3, 'correction', -0.75, NULL, '', '2026-04-01');
INSERT INTO service_kits (vehicle_id, name, interval_months) VALUES (1, 'Oil service', 12);
INSERT INTO service_kit_lines (kit_id, item_id, quantity) VALUES (1, 2, 1700);
INSERT INTO service_records (vehicle_id, kit_id, performed_on, title, odometer_km) VALUES (1, 1, '2026-05-01', 'Oil service', 11000);
INSERT INTO shopping_entries (item_id, quantity) VALUES (1, 2);
INSERT INTO shopping_entries (free_text, unit, quantity) VALUES ('Coolant', 'ml', 4000);
INSERT INTO trash (kind, label, payload) VALUES ('shopping', 'Shopping list: Chain lube',
  '{"shopping_entries": [{"id": 99, "item_id": null, "free_text": "Chain lube", "quantity": null, "note": "", "unit": "piece"}]}');
`

// versionedTables are the tables 00010 gives a version.
var versionedTables = []string{"items", "vehicles", "service_kits", "service_records", "stock_movements", "shopping_entries"}

// dump returns every row of every table, without the version column, in
// a stable order: equal dumps mean nothing else changed. A trash entry
// labelled except is left out.
func dump(t *testing.T, pool *pgxpool.Pool, except string) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"vehicles", "items", "item_skus", "item_vehicles", "item_photos", "service_kits",
		"service_kit_lines", "service_records", "stock_movements", "shopping_entries", "trash"} {
		where := "true"
		if table == "trash" {
			where = "t.label <> $1" // leaves out the snapshot taken between Up and Down
		}
		var rows string
		err := pool.QueryRow(context.Background(), `
			SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'version' ORDER BY to_jsonb(t)::text), '[]')::text
			FROM `+table+` t WHERE `+where+` OR $1 = ''`, except).Scan(&rows)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(table + ": " + rows + "\n")
	}
	return b.String()
}

// checkLedger fails if an item's quantity differs from its stock changes.
func checkLedger(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var off int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM items i
		WHERE i.quantity <> COALESCE((SELECT sum(delta) FROM stock_movements m WHERE m.item_id = i.id), 0)`).Scan(&off)
	if err != nil || off != 0 {
		t.Fatalf("ledger: %d items off (%v)", off, err)
	}
}

// 00010 adds versions without changing anything else, and its Down
// takes them out again, from trash snapshots too: Up → Down → Up.
func TestVersionsMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, realShape); err != nil {
		t.Fatal(err)
	}
	checkLedger(t, pool)
	before := dump(t, pool, "")

	if _, err := provider.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, table := range versionedTables {
		var rows, ones int
		if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE version = 1) FROM `+table).Scan(&rows, &ones); err != nil {
			t.Fatal(err)
		}
		if rows == 0 || ones != rows {
			t.Errorf("%s: %d of %d rows at version 1", table, ones, rows)
		}
	}
	if got := dump(t, pool, ""); got != before {
		t.Errorf("Up changed data:\n%s\nwant\n%s", got, before)
	}
	checkLedger(t, pool)

	// A snapshot taken after Up holds versions; Down takes them out, so the
	// code before 00010 can still restore it.
	if _, err := pool.Exec(ctx, `
		INSERT INTO trash (kind, label, payload)
		SELECT 'item', 'Item: snapshot', jsonb_build_object('items', jsonb_agg(to_jsonb(i)), 'movement_id', 5,
		                                                    'item_skus', '[]'::jsonb)
		FROM items i WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	for _, table := range versionedTables {
		var has bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = 'version')`, table).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has {
			t.Errorf("%s still has a version after Down", table)
		}
	}
	if got := dump(t, pool, "Item: snapshot"); got != before {
		t.Errorf("Down changed data:\n%s\nwant\n%s", got, before)
	}
	var payload string
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM trash WHERE label = 'Item: snapshot'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, `"version"`) || !strings.Contains(payload, `"movement_id": 5`) ||
		!strings.Contains(payload, `"name": "15W50 PRO 4 XP"`) || !strings.Contains(payload, `"item_skus": []`) {
		t.Errorf("snapshot after Down = %s", payload)
	}

	if _, err := provider.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got := dump(t, pool, "Item: snapshot"); got != before {
		t.Errorf("Up again changed data:\n%s\nwant\n%s", got, before)
	}
	checkLedger(t, pool)
}

// 00011 adds the form keys table without touching anything else, and its
// Down drops it and takes the form key out of Bought snapshots, which
// the code before it doesn't know: Up → Down → Up.
func TestFormSubmissionsMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, realShape); err != nil {
		t.Fatal(err)
	}
	before := dump(t, pool, "")
	tableExists := func() bool {
		t.Helper()
		var has bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('form_submissions') IS NOT NULL`).Scan(&has); err != nil {
			t.Fatal(err)
		}
		return has
	}

	if _, err := provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if !tableExists() {
		t.Fatal("no form_submissions after Up")
	}
	if got := dump(t, pool, ""); got != before {
		t.Errorf("Up changed data:\n%s\nwant\n%s", got, before)
	}
	checkLedger(t, pool)
	// A key, an invalid one, and a Bought snapshot that keeps its form's key.
	if _, err := pool.Exec(ctx, `INSERT INTO form_submissions (key, result_id) VALUES ('0123456789abcdef0123456789abcdef', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO form_submissions (key) VALUES ('not a key')`); err == nil {
		t.Error("an invalid key was stored")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trash (kind, label, payload)
		SELECT 'bought', 'Bought: snapshot', jsonb_build_object('shopping_entries', jsonb_agg(to_jsonb(e)), 'movement_id', 4,
		                                                      'form_key', '0123456789abcdef0123456789abcdef')
		FROM shopping_entries e WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if _, err := provider.DownTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if tableExists() {
		t.Error("form_submissions still there after Down")
	}
	if got := dump(t, pool, "Bought: snapshot"); got != before {
		t.Errorf("Down changed data:\n%s\nwant\n%s", got, before)
	}
	var payload string
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM trash WHERE label = 'Bought: snapshot'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "form_key") || !strings.Contains(payload, `"movement_id": 4`) || !strings.Contains(payload, `"item_id": 1`) {
		t.Errorf("snapshot after Down = %s", payload)
	}

	if _, err := provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if !tableExists() {
		t.Fatal("no form_submissions after Up again")
	}
	if got := dump(t, pool, "Bought: snapshot"); got != before {
		t.Errorf("Up again changed data:\n%s\nwant\n%s", got, before)
	}
	checkLedger(t, pool)
}

// amountColumns are the columns 00012 takes to two decimals.
var amountColumns = [][2]string{{"items", "quantity"}, {"items", "min_quantity"}, {"item_skus", "pack_size"},
	{"service_kit_lines", "quantity"}, {"stock_movements", "delta"}, {"shopping_entries", "quantity"}}

// trailingZeros matches the zeros a numeric's scale adds (1525.500), so
// dumps taken with three and two decimals compare by value.
var (
	trailingZeros = regexp.MustCompile(`(\.[0-9]*[1-9])0+([,}\]])`)
	zeroFraction  = regexp.MustCompile(`\.0+([,}\]])`)
)

func byValue(dump string) string {
	return zeroFraction.ReplaceAllString(trailingZeros.ReplaceAllString(dump, "$1$2"), "$1")
}

// scales returns the number of decimals each amount column keeps.
func scales(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, c := range amountColumns {
		var scale int
		if err := pool.QueryRow(context.Background(), `SELECT numeric_scale FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, c[0], c[1]).Scan(&scale); err != nil {
			t.Fatal(err)
		}
		out[c[0]+"."+c[1]] = scale
	}
	return out
}

func expectScales(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	for col, got := range scales(t, pool) {
		if got != want {
			t.Errorf("%s keeps %d decimals, want %d", col, got, want)
		}
	}
}

// 00012 takes every amount column to two decimals without changing a
// value, and back: Up → Down → Up on data shaped like the real one. Its
// guard stops it, changing nothing, when an amount finer than 0.01 is in
// a table or a trash snapshot, rather than let the new type round it.
func TestAmountsMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, realShape); err != nil {
		t.Fatal(err)
	}
	// A snapshot with amounts in it: a deleted stock change and its item's
	// part numbers, as the store takes them.
	if _, err := pool.Exec(ctx, `
		INSERT INTO trash (kind, label, payload)
		SELECT 'item', 'Item: snapshot', jsonb_build_object('items', (SELECT jsonb_agg(to_jsonb(i)) FROM items i WHERE id = 2),
		       'item_skus', (SELECT jsonb_agg(to_jsonb(s)) FROM item_skus s WHERE item_id = 2),
		       'stock_movements', (SELECT jsonb_agg(to_jsonb(m)) FROM stock_movements m WHERE item_id = 2), 'movement_id', 4)`); err != nil {
		t.Fatal(err)
	}
	checkLedger(t, pool)
	expectScales(t, pool, 3)
	before := byValue(dump(t, pool, ""))
	same := func(step string) {
		t.Helper()
		if got := byValue(dump(t, pool, "")); got != before {
			t.Errorf("%s changed data:\n%s\nwant\n%s", step, got, before)
		}
		checkLedger(t, pool)
	}

	if _, err := provider.UpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	expectScales(t, pool, 2)
	same("Up")
	// Postgres keeps it so from now on (rounding what it is given).
	var q float64
	if err := pool.QueryRow(ctx, `UPDATE items SET quantity = 1.234 WHERE id = 3 RETURNING quantity::float8`).Scan(&q); err != nil || q != 1.23 {
		t.Errorf("1.234 stored as %v (%v)", q, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE items SET quantity = 399.25 WHERE id = 3`); err != nil {
		t.Fatal(err)
	}

	if _, err := provider.DownTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	expectScales(t, pool, 3)
	same("Down")
	if _, err := provider.UpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	expectScales(t, pool, 2)
	same("Up again")
	if _, err := provider.DownTo(ctx, 11); err != nil {
		t.Fatal(err)
	}

	// The guard: a finer amount anywhere stops the migration, naming where.
	for _, tt := range []struct {
		name, seed, undo string
		want             []string
	}{
		{"in a table",
			`UPDATE items SET quantity = quantity + 0.005 WHERE id = 3;
			 INSERT INTO stock_movements (item_id, kind, delta, occurred_on) VALUES (3, 'correction', 0.005, '2026-10-01');
			 UPDATE item_skus SET pack_size = 2.125 WHERE id = 1`,
			`UPDATE items SET quantity = quantity - 0.005 WHERE id = 3;
			 DELETE FROM stock_movements WHERE delta = 0.005;
			 UPDATE item_skus SET pack_size = 2 WHERE id = 1`,
			[]string{"items.quantity: 1 row (e.g. 399.255)", "stock_movements.delta: 1 row (e.g. 0.005)",
				"item_skus.pack_size: 1 row (e.g. 2.125)"}},
		{"in a trash snapshot",
			`INSERT INTO trash (kind, label, payload) VALUES ('movement', 'Stock change: fine',
			   '{"stock_movements": [{"id": 50, "item_id": 3, "kind": "use", "delta": -0.005}, {"id": 51, "item_id": 3, "kind": "use", "delta": -1.5}],
			     "movement_id": 1.5, "items": [{"id": 3, "quantity": 2, "min_quantity": 0.125}]}')`,
			`DELETE FROM trash WHERE label = 'Stock change: fine'`,
			[]string{"trash snapshots, stock_movements.delta: 1 row (e.g. -0.005)", "trash snapshots, items.min_quantity: 1 row (e.g. 0.125)"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.seed); err != nil {
				t.Fatal(err)
			}
			checkLedger(t, pool)
			seeded := dump(t, pool, "")
			_, err := provider.UpTo(ctx, 12)
			if err == nil {
				t.Fatal("the migration went through with an amount finer than 0.01")
			}
			for _, w := range append(tt.want, "Migration 00012", "nothing was changed") {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error does not say %q: %v", w, err)
				}
			}
			if v, err := provider.GetDBVersion(ctx); err != nil || v != 11 {
				t.Errorf("version after the guard = %d, %v; want 11", v, err)
			}
			expectScales(t, pool, 3)
			if got := dump(t, pool, ""); got != seeded {
				t.Errorf("the guard changed data:\n%s\nwant\n%s", got, seeded)
			}
			if _, err := pool.Exec(ctx, tt.undo); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := provider.UpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	same("Up after fixing")
}

// dateDefaults returns the defaults of the two date columns 00013 changes.
func dateDefaults(t *testing.T, pool *pgxpool.Pool) [2]string {
	t.Helper()
	var out [2]string
	for i, c := range [][2]string{{"service_records", "performed_on"}, {"stock_movements", "occurred_on"}} {
		var def *string
		if err := pool.QueryRow(context.Background(), `SELECT column_default FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, c[0], c[1]).Scan(&def); err != nil {
			t.Fatal(err)
		}
		if def != nil {
			out[i] = *def
		}
	}
	return out
}

// 00013 takes current_date away as the default of a service's and a stock
// change's date, without changing any data, and Down puts it back:
// Up → Down → Up on data shaped like the real one.
func TestNoDatabaseDatesMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, realShape); err != nil {
		t.Fatal(err)
	}
	checkLedger(t, pool)
	before := dump(t, pool, "")
	if got := dateDefaults(t, pool); got != [2]string{"CURRENT_DATE", "CURRENT_DATE"} {
		t.Fatalf("defaults before = %q", got)
	}
	// An insert without a date (rolled back): refused after Up, dated by
	// the database after Down.
	undated := func() error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `INSERT INTO stock_movements (item_id, kind, delta) VALUES (1, 'use', -1)`)
		return err
	}
	same := func(step string) {
		t.Helper()
		if got := dump(t, pool, ""); got != before {
			t.Errorf("%s changed data:\n%s\nwant\n%s", step, got, before)
		}
		checkLedger(t, pool)
	}

	if _, err := provider.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	if got := dateDefaults(t, pool); got != [2]string{} {
		t.Errorf("defaults after Up = %q, want none", got)
	}
	if err := undated(); err == nil || !strings.Contains(err.Error(), "occurred_on") {
		t.Errorf("an undated stock change after Up: %v, want a not-null error", err)
	}
	same("Up")

	if _, err := provider.DownTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	if got := dateDefaults(t, pool); got != [2]string{"CURRENT_DATE", "CURRENT_DATE"} {
		t.Errorf("defaults after Down = %q", got)
	}
	if err := undated(); err != nil {
		t.Errorf("an undated stock change after Down: %v", err)
	}
	same("Down")

	if _, err := provider.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	if got := dateDefaults(t, pool); got != [2]string{} {
		t.Errorf("defaults after Up again = %q, want none", got)
	}
	same("Up again")
}

// Every session Open makes is in UTC, whatever the server's default:
// nothing in SQL can then decide a day in the database's zone.
func TestOpenPinsTheSessionToUTC(t *testing.T) {
	pool, _ := migrationDB(t)
	ctx := context.Background()
	conns := make([]*pgxpool.Conn, 3) // more than one connection of the pool
	for i := range conns {
		c, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = c
		var tz string
		if err := c.QueryRow(ctx, `SHOW timezone`).Scan(&tz); err != nil || tz != "UTC" {
			t.Errorf("connection %d: timezone = %q, %v; want UTC", i, tz, err)
		}
	}
	for _, c := range conns {
		c.Release()
	}
}

// serviceParts is realShape's service taking its kit's oil (after a
// purchase that covers it), and a deleted service with its parts in the
// trash, as DeleteService snapshots them.
const serviceParts = `
INSERT INTO stock_movements (item_id, kind, delta, total_price, supplier, occurred_on) VALUES (2, 'purchase', 2000, 39.80, 'Moto shop', '2026-04-20');
INSERT INTO stock_movements (item_id, kind, delta, note, occurred_on, service_record_id) VALUES (2, 'service', -1700, 'Oil service', '2026-05-01', 1);
UPDATE items SET quantity = quantity + 300 WHERE id = 2;
INSERT INTO trash (kind, label, payload) VALUES ('service', 'Service: Chain', '{
  "service_records": [{"id": 70, "vehicle_id": 1, "kit_id": null, "performed_on": "2026-06-01", "title": "Chain", "notes": "", "version": 1}],
  "stock_movements": [{"id": 71, "item_id": 3, "kind": "service", "delta": -20, "note": "Chain", "occurred_on": "2026-06-01", "service_record_id": 70, "version": 1}]}');
`

// serviceFK returns what deleting a service does to its parts' foreign
// key ('r' restrict, 'n' set null), and whether the CHECK is there.
func serviceFK(t *testing.T, pool *pgxpool.Pool) (onDelete string, check bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT (SELECT confdeltype::text FROM pg_constraint WHERE conname = 'stock_movements_service_record_id_fkey'
		          AND conrelid = 'stock_movements'::regclass),
		       EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'stock_movements_service_record_check'
		          AND conrelid = 'stock_movements'::regclass)`).Scan(&onDelete, &check)
	if err != nil {
		t.Fatal(err)
	}
	return onDelete, check
}

// refused runs sql in a transaction that is rolled back, and returns its
// error.
func refused(t *testing.T, pool *pgxpool.Pool, sql string) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, sql)
	return err
}

// 00014 makes a service's parts point at it for good: a CHECK that only
// they do, and a foreign key that refuses to delete a service (or the
// vehicle it cascades from) while it has parts, instead of leaving them
// without one. Up → Down → Up changes no data, and the guard stops it,
// changing nothing, when a row or a trash snapshot breaks the rule.
func TestServicePartsMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, realShape+serviceParts); err != nil {
		t.Fatal(err)
	}
	checkLedger(t, pool)
	before := dump(t, pool, "")
	same := func(step string) {
		t.Helper()
		if got := dump(t, pool, ""); got != before {
			t.Errorf("%s changed data:\n%s\nwant\n%s", step, got, before)
		}
		checkLedger(t, pool)
	}
	if onDelete, check := serviceFK(t, pool); onDelete != "n" || check {
		t.Fatalf("before: on delete %q, check %v", onDelete, check)
	}

	if _, err := provider.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	if onDelete, check := serviceFK(t, pool); onDelete != "r" || !check {
		t.Errorf("after Up: on delete %q, check %v; want restrict and the check", onDelete, check)
	}
	same("Up")
	for _, tt := range []struct{ name, sql, want string }{
		{"delete a service with parts", `DELETE FROM service_records WHERE id = 1`, "stock_movements_service_record_id_fkey"},
		{"delete its vehicle", `DELETE FROM vehicles WHERE id = 1`, "stock_movements_service_record_id_fkey"},
		{"a service part without a service", `INSERT INTO stock_movements (item_id, kind, delta, occurred_on) VALUES (1, 'service', -1, '2026-06-01')`,
			"stock_movements_service_record_check"},
		{"a use pointing at a service", `INSERT INTO stock_movements (item_id, kind, delta, occurred_on, service_record_id) VALUES (1, 'use', -1, '2026-06-01', 1)`,
			"stock_movements_service_record_check"},
		{"take a part from its service", `UPDATE stock_movements SET service_record_id = NULL WHERE kind = 'service'`,
			"stock_movements_service_record_check"},
	} {
		if err := refused(t, pool, tt.sql); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want it refused by %s", tt.name, err, tt.want)
		}
	}
	// What the store does still works: the parts go first, then the
	// service; a vehicle without services goes as before.
	if err := refused(t, pool, `DELETE FROM stock_movements WHERE service_record_id = 1; DELETE FROM service_records WHERE id = 1`); err != nil {
		t.Errorf("delete a service after its parts: %v", err)
	}
	if err := refused(t, pool, `DELETE FROM vehicles WHERE id = 3`); err != nil {
		t.Errorf("delete a vehicle without services: %v", err)
	}

	if _, err := provider.DownTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	if onDelete, check := serviceFK(t, pool); onDelete != "n" || check {
		t.Errorf("after Down: on delete %q, check %v; want set null and no check", onDelete, check)
	}
	same("Down")
	if _, err := provider.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	if onDelete, check := serviceFK(t, pool); onDelete != "r" || !check {
		t.Errorf("after Up again: on delete %q, check %v", onDelete, check)
	}
	same("Up again")
	if _, err := provider.DownTo(ctx, 13); err != nil {
		t.Fatal(err)
	}

	// The guard. The first orphan is made as before 00014: deleting a
	// vehicle straight in the database set its service's parts to no service.
	var orphan, stray int64
	for _, tt := range []struct {
		name, seed, undo string
		want             func() []string
	}{
		{"a part of a deleted vehicle's service",
			`INSERT INTO vehicles (name) VALUES ('Sold bike');
			 INSERT INTO service_records (vehicle_id, performed_on, title) SELECT id, '2026-07-01', 'Brakes' FROM vehicles WHERE name = 'Sold bike';
			 INSERT INTO stock_movements (item_id, kind, delta, occurred_on, service_record_id)
			   SELECT 1, 'service', -1, '2026-07-01', id FROM service_records WHERE title = 'Brakes';
			 UPDATE items SET quantity = quantity - 1 WHERE id = 1;
			 DELETE FROM vehicles WHERE name = 'Sold bike'`,
			`UPDATE items SET quantity = quantity + 1 WHERE id = 1;
			 DELETE FROM stock_movements WHERE kind = 'service' AND service_record_id IS NULL`,
			func() []string {
				return []string{"service parts without a service: stock_movements " + strconv.FormatInt(orphan, 10)}
			}},
		{"a use pointing at a service",
			`INSERT INTO stock_movements (item_id, kind, delta, occurred_on, service_record_id) VALUES (3, 'use', -0.25, '2026-07-02', 1);
			 UPDATE items SET quantity = quantity - 0.25 WHERE id = 3`,
			`UPDATE items SET quantity = quantity + 0.25 WHERE id = 3;
			 DELETE FROM stock_movements WHERE kind = 'use' AND service_record_id IS NOT NULL`,
			func() []string {
				return []string{"other stock changes pointing at a service: stock_movements " + strconv.FormatInt(stray, 10)}
			}},
		{"in trash snapshots",
			`INSERT INTO trash (id, kind, label, payload) OVERRIDING SYSTEM VALUE VALUES
			   (900, 'movement', 'Stock change: orphan', '{"stock_movements": [{"id": 80, "item_id": 1, "kind": "service", "delta": -1, "service_record_id": null}], "movement_id": 80}'),
			   (901, 'item', 'Item: no id', '{"items": [{"id": 9}], "stock_movements": [{"id": 81, "item_id": 9, "kind": "service", "delta": -1}]}'),
			   (902, 'item', 'Item: stray', '{"items": [{"id": 8}], "stock_movements": [{"id": 82, "item_id": 8, "kind": "use", "delta": -1, "service_record_id": 1}]}')`,
			`DELETE FROM trash WHERE id >= 900`,
			func() []string { return []string{"trash snapshots holding such stock changes: trash 900, 901, 902"} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.seed); err != nil {
				t.Fatal(err)
			}
			_ = pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM stock_movements WHERE kind = 'service' AND service_record_id IS NULL`).Scan(&orphan)
			_ = pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM stock_movements WHERE kind <> 'service' AND service_record_id IS NOT NULL`).Scan(&stray)
			checkLedger(t, pool)
			seeded := dump(t, pool, "")
			_, err := provider.UpTo(ctx, 14)
			if err == nil {
				t.Fatal("the migration went through")
			}
			for _, w := range append(tt.want(), "Migration 00014", "nothing was changed") {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error does not say %q: %v", w, err)
				}
			}
			if v, err := provider.GetDBVersion(ctx); err != nil || v != 13 {
				t.Errorf("version after the guard = %d, %v; want 13", v, err)
			}
			if onDelete, check := serviceFK(t, pool); onDelete != "n" || check {
				t.Errorf("after the guard: on delete %q, check %v", onDelete, check)
			}
			if got := dump(t, pool, ""); got != seeded {
				t.Errorf("the guard changed data:\n%s\nwant\n%s", got, seeded)
			}
			if _, err := pool.Exec(ctx, tt.undo); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := provider.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	same("Up after fixing")
}
