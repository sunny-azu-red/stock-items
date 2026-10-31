package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
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
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("DB_HOST") == "" {
		t.Skip("no database configured (set DB_* or create .env)")
	}
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	cfg.DB.Schema = "test_" + hex.EncodeToString(buf)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, cfg.DB)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := DropSchema(context.Background(), pool, cfg.DB.Schema); err != nil {
			t.Errorf("drop schema %s: %v", cfg.DB.Schema, err)
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
