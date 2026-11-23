package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"maps"
	"os"
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

// amountColumns hold quantities in base units, at most two decimals.
var amountColumns = [][2]string{{"items", "quantity"}, {"items", "min_quantity"}, {"item_skus", "pack_size"},
	{"service_kit_lines", "quantity"}, {"stock_movements", "delta"}, {"shopping_entries", "quantity"}}

// The rules AGENTS.md leans on the database for, as the migrations leave
// it: amounts keep two decimals, no date column has a default (days are
// decided in Go), a service's parts point at it and can't lose it, and a
// service without labour holds none, never 0.
func TestSchemaInvariants(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range amountColumns {
		var scale int
		if err := pool.QueryRow(ctx, `SELECT numeric_scale FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, c[0], c[1]).Scan(&scale); err != nil {
			t.Fatal(err)
		}
		if scale != 2 {
			t.Errorf("%s.%s keeps %d decimals, want 2", c[0], c[1], scale)
		}
	}
	var dated string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(string_agg(table_name || '.' || column_name, ', ' ORDER BY 1), '')
		FROM information_schema.columns WHERE table_schema = current_schema() AND data_type = 'date' AND column_default IS NOT NULL`).Scan(&dated); err != nil {
		t.Fatal(err)
	}
	if dated != "" {
		t.Errorf("date columns with a default: %s (days are decided in Go)", dated)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('KTM 390', 0, '2020-01-01', 0);
		INSERT INTO items (name, quantity) VALUES ('15W50', 1000);
		INSERT INTO stock_movements (item_id, kind, delta, total_price, occurred_on) VALUES (1, 'bought', 2000, 39.80, '2026-04-20');
		INSERT INTO service_records (vehicle_id, performed_on, title) VALUES (1, '2026-05-01', 'Oil service');
		INSERT INTO stock_movements (item_id, kind, delta, occurred_on, service_record_id) VALUES (1, 'service', -1000, '2026-05-01', 1);
	`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, sql, want string }{
		{"service part without a service", `INSERT INTO stock_movements (item_id, kind, delta, occurred_on) VALUES (1, 'service', -1, '2026-05-01')`,
			"stock_movements_service_record_check"},
		{"service as another kind", `INSERT INTO stock_movements (item_id, kind, delta, occurred_on, service_record_id) VALUES (1, 'used', -1, '2026-05-01', 1)`,
			"stock_movements_service_record_check"},
		{"deleting a service with parts", `DELETE FROM service_records WHERE id = 1`, "stock_movements_service_record_id_fkey"},
		{"deleting its vehicle", `DELETE FROM vehicles WHERE id = 1`, "stock_movements_service_record_id_fkey"},
		{"labour 0", `UPDATE service_records SET labor_cost = 0`, "service_records_labor_cost_check"},
		{"whether it moved the odometer unknown", `UPDATE service_records SET odometer_moved = NULL`, `null value in column "odometer_moved"`},
	} {
		if err := refused(t, pool, tc.sql); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %s to refuse it", tc.name, err, tc.want)
		}
	}
}

// Down takes everything away, the function too, and Up builds the same
// schema again: the migrations' Down sections work.
func TestMigrationsDownUp(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	before := schemaShape(t, pool)
	order := columnOrder(t, pool)
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var left string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(string_agg(name, ', ' ORDER BY name), '') FROM (
			SELECT relname::text AS name FROM pg_class WHERE relnamespace = current_schema()::regnamespace AND relname NOT LIKE 'goose_db_version%'
			UNION ALL SELECT proname::text FROM pg_proc WHERE pronamespace = current_schema()::regnamespace) s`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != "" {
		t.Errorf("after Down: left %s", left)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if got := schemaShape(t, pool); got != before {
		t.Errorf("after Down and Up: the schema differs:\n%s", lineDiff(got, before))
	}
	if got := columnOrder(t, pool); !mapsEqual(got, order) {
		t.Errorf("after Down and Up: columns = %v, want %v", got, order)
	}
}

// 00002 makes odometer_moved NOT NULL (default false): it stops, changing
// nothing, on a row or a trash snapshot holding NULL there, and Down makes
// the column nullable again.
func TestOdometerMovedNotNullMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO vehicles (name) VALUES ('KTM 390');
		INSERT INTO service_records (vehicle_id, performed_on, title, odometer_moved) VALUES (1, '2026-05-01', 'Oil', NULL)`); err != nil {
		t.Fatal(err)
	}
	stopped := func(when, want string) {
		t.Helper()
		_, err := provider.UpTo(ctx, 2)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want it stopped with %q", when, err, want)
		}
		if v, err := provider.GetDBVersion(ctx); err != nil || v != 1 {
			t.Errorf("%s: version %d (%v), want 1", when, v, err)
		}
	}
	stopped("a row with NULL", "odometer_moved")

	if _, err := pool.Exec(ctx, `
		UPDATE service_records SET odometer_moved = false;
		INSERT INTO trash (kind, label, payload) VALUES ('vehicle', 'Vehicle: Golf',
			'{"vehicles": [{"id": 60, "name": "Golf"}], "service_records": [{"id": 61, "title": "Tyres", "odometer_moved": null}]}')`); err != nil {
		t.Fatal(err)
	}
	stopped("a snapshot with null", "nothing was changed: trash entries 1 hold a service")

	if _, err := pool.Exec(ctx, `UPDATE trash SET payload = jsonb_set(payload, '{service_records,0,odometer_moved}', 'false')`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	var moved bool
	if err := pool.QueryRow(ctx, `INSERT INTO service_records (vehicle_id, performed_on, title) VALUES (1, '2026-06-01', 'Chain') RETURNING odometer_moved`).Scan(&moved); err != nil || moved {
		t.Errorf("a new service: odometer_moved = %v (%v), want false by default", moved, err)
	}
	if err := refused(t, pool, `UPDATE service_records SET odometer_moved = NULL`); err == nil {
		t.Error("after Up: NULL was taken")
	}

	if _, err := provider.DownTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE service_records SET odometer_moved = NULL WHERE title = 'Chain'`); err != nil {
		t.Errorf("after Down: NULL refused: %v", err)
	}
}

// 00004 makes a vehicle's odometer, purchase date and purchase reading NOT
// NULL: it stops, changing nothing, on a vehicle or a deleted one in the
// trash without them, and Down makes them nullable again.
func TestVehicleRequiredMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('KTM 390', 12000, '2024-03-01', 0), ('Golf', NULL, '2020-01-01', 0)`); err != nil {
		t.Fatal(err)
	}
	stopped := func(when, want string) {
		t.Helper()
		_, err := provider.UpTo(ctx, 4)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want it stopped with %q", when, err, want)
		}
		if v, err := provider.GetDBVersion(ctx); err != nil || v != 3 {
			t.Errorf("%s: version %d (%v), want 3", when, v, err)
		}
	}
	stopped("a vehicle without an odometer", "vehicles without an odometer, purchase date or purchase reading: Golf")

	if _, err := pool.Exec(ctx, `
		UPDATE vehicles SET odometer = 0 WHERE name = 'Golf';
		INSERT INTO trash (kind, label, payload) VALUES ('vehicle', 'Vehicle: Sold',
			'{"vehicles": [{"id": 60, "name": "Sold", "odometer": 1, "purchased_on": null, "odometer_at_purchase": 0}]}')`); err != nil {
		t.Fatal(err)
	}
	stopped("a deleted vehicle without a purchase date", "deleted vehicles in the trash without an odometer, purchase date or purchase reading: Vehicle: Sold")

	if _, err := pool.Exec(ctx, `UPDATE trash SET payload = jsonb_set(payload, '{vehicles,0,purchased_on}', '"2020-01-01"')`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"odometer", "purchased_on", "odometer_at_purchase"} {
		if err := refused(t, pool, `UPDATE vehicles SET `+col+` = NULL`); err == nil {
			t.Errorf("after Up: NULL %s was taken", col)
		}
	}
	if _, err := provider.DownTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE vehicles SET odometer = NULL, purchased_on = NULL, odometer_at_purchase = NULL WHERE name = 'Golf'`); err != nil {
		t.Errorf("after Down: NULL refused: %v", err)
	}
}

// 00005 turns a kit's two intermediate intervals into in_between: it stops,
// changing nothing, on a kit or a deleted one in the trash that isn't 2 or
// 3 equal steps of the full distance at its months, converts the rest (the
// trash's too, so they restore), and Down brings the intervals back.
func TestKitInBetweenMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('KTM 390', 12000, '2024-03-01', 0);
		INSERT INTO service_kits (vehicle_id, name, interval_distance, interval_months, inter_interval_distance, inter_interval_months) VALUES
			(1, 'Oil', 8000, 12, 4000, 12), (1, 'Thirds', 15000, 12, 5000, NULL), (1, 'Plugs', 15000, NULL, NULL, NULL),
			(1, 'Odd', 15000, 12, 4000, 12)`); err != nil {
		t.Fatal(err)
	}
	stopped := func(when, want string) {
		t.Helper()
		_, err := provider.UpTo(ctx, 5)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want it stopped with %q", when, err, want)
		}
		if v, err := provider.GetDBVersion(ctx); err != nil || v != 4 {
			t.Errorf("%s: version %d (%v), want 4", when, v, err)
		}
	}
	stopped("4 000 of 15 000", "kits whose intermediate schedule is not 2 or 3 equal steps of the full distance at its months: KTM 390: Odd")

	if _, err := pool.Exec(ctx, `
		UPDATE service_kits SET inter_interval_distance = 7500 WHERE name = 'Odd';
		INSERT INTO trash (kind, label, payload) VALUES ('kit', 'Kit: Gone',
			'{"service_kits": [{"id": 60, "vehicle_id": 1, "name": "Gone", "interval_distance": 10000, "interval_months": 12, "inter_interval_distance": 5000, "inter_interval_months": 6}], "service_kit_lines": []}')`); err != nil {
		t.Fatal(err)
	}
	stopped("a deleted kit with other months", "deleted kits in the trash whose intermediate schedule is not 2 or 3 equal steps of the full distance at its months: Kit: Gone")

	if _, err := pool.Exec(ctx, `UPDATE trash SET payload = jsonb_set(payload, '{service_kits,0,inter_interval_months}', '12')`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	rows, err := pool.Query(ctx, `SELECT name, in_between FROM service_kits`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			t.Fatal(err)
		}
		got[name] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"Oil": 1, "Thirds": 2, "Plugs": 0, "Odd": 1}; !maps.Equal(got, want) {
		t.Errorf("in_between = %v, want %v", got, want)
	}
	var snapshot string
	if err := pool.QueryRow(ctx, `SELECT payload -> 'service_kits' -> 0 ->> 'in_between' FROM trash`).Scan(&snapshot); err != nil || snapshot != "1" {
		t.Errorf("trash snapshot in_between = %q (%v), want 1", snapshot, err)
	}
	if err := refused(t, pool, `INSERT INTO service_kits (vehicle_id, name, interval_months, in_between) VALUES (1, 'Months only', 12, 1)`); err == nil {
		t.Error("after Up: in between without a distance was taken")
	}
	if err := refused(t, pool, `UPDATE service_kits SET in_between = 3`); err == nil {
		t.Error("after Up: 3 in between was taken")
	}

	if _, err := provider.DownTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	var dist, months *int
	if err := pool.QueryRow(ctx, `SELECT inter_interval_distance, inter_interval_months FROM service_kits WHERE name = 'Thirds'`).Scan(&dist, &months); err != nil ||
		dist == nil || *dist != 5000 || months == nil || *months != 12 {
		t.Errorf("after Down: Thirds every %v / %v months (%v), want 5 000 / 12", dist, months, err)
	}
}

// 00006 turns kit_intermediate into kit_tier, in the trash's services too
// (so they restore), adds break_in_at, and Down turns a break-in back into
// a full service.
func TestServiceKitTierMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('KTM 390', 12000, '2024-03-01', 0);
		INSERT INTO service_kits (vehicle_id, name, interval_distance, interval_months, in_between) VALUES (1, 'Oil', 8000, 12, 1);
		INSERT INTO service_records (vehicle_id, kit_id, kit_intermediate, performed_on, title) VALUES
			(1, 1, false, '2025-01-01', 'Oil'), (1, 1, true, '2025-06-01', 'Oil (Intermediate)'), (1, NULL, false, '2025-07-01', 'Wash');
		INSERT INTO trash (kind, label, payload) VALUES ('service', 'Service: Old',
			'{"service_records": [{"id": 60, "vehicle_id": 1, "kit_id": 1, "kit_intermediate": true, "performed_on": "2025-03-01", "title": "Old"}]}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	var tiers string
	if err := pool.QueryRow(ctx, `SELECT string_agg(kit_tier, ',' ORDER BY id) FROM service_records`).Scan(&tiers); err != nil || tiers != "full,intermediate,full" {
		t.Errorf("kit_tier = %q (%v), want full,intermediate,full", tiers, err)
	}
	var snapshot string
	if err := pool.QueryRow(ctx, `SELECT payload -> 'service_records' -> 0 ->> 'kit_tier' FROM trash`).Scan(&snapshot); err != nil || snapshot != "intermediate" {
		t.Errorf("trash snapshot kit_tier = %q (%v), want intermediate", snapshot, err)
	}
	if err := refused(t, pool, `UPDATE service_records SET kit_tier = 'half'`); err == nil {
		t.Error("after Up: an unknown tier was taken")
	}
	if _, err := pool.Exec(ctx, `UPDATE service_records SET kit_tier = 'break_in' WHERE id = 1; UPDATE service_kits SET break_in_at = 1000`); err != nil {
		t.Fatal(err)
	}

	if _, err := provider.DownTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	var inter string
	if err := pool.QueryRow(ctx, `SELECT string_agg(kit_intermediate::text, ',' ORDER BY id) FROM service_records`).Scan(&inter); err != nil || inter != "false,true,false" {
		t.Errorf("after Down: kit_intermediate = %q (%v), want false,true,false", inter, err)
	}
	if err := pool.QueryRow(ctx, `SELECT payload -> 'service_records' -> 0 ->> 'kit_intermediate' FROM trash`).Scan(&snapshot); err != nil || snapshot != "true" {
		t.Errorf("after Down: trash snapshot kit_intermediate = %q (%v), want true", snapshot, err)
	}
}

// 00007 lets a kit with months only have intermediate services; Down stops
// on one, changing nothing.
func TestKitInBetweenMonthsMigration(t *testing.T) {
	pool, provider := migrationDB(t)
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('KTM 390', 12000, '2024-03-01', 0)`); err != nil {
		t.Fatal(err)
	}
	coolant := `INSERT INTO service_kits (vehicle_id, name, interval_months, in_between) VALUES (1, 'Coolant', 48, 1)`
	if err := refused(t, pool, coolant); err == nil {
		t.Fatal("before 00007: months only with one in between was taken")
	}
	if _, err := provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, coolant); err != nil {
		t.Fatalf("after Up: months only with one in between refused: %v", err)
	}
	if err := refused(t, pool, `INSERT INTO service_kits (vehicle_id, name, in_between) VALUES (1, 'Pads', 1)`); err == nil {
		t.Error("after Up: one in between with no interval was taken")
	}
	if _, err := provider.DownTo(ctx, 6); err == nil || !strings.Contains(err.Error(), "kits with intermediate services and months only: Coolant") {
		t.Errorf("Down with a months-only kit: %v, want it stopped", err)
	}
	if v, err := provider.GetDBVersion(ctx); err != nil || v != 7 {
		t.Errorf("after the stopped Down: version %d (%v), want 7", v, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE service_kits SET in_between = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
}
