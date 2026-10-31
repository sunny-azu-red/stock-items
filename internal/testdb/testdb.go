// Package testdb gives integration tests an isolated, migrated Postgres
// schema on the configured database. Each call creates a fresh schema
// named test_<random> that is dropped when the test finishes.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunny-azu-red/stock-items/internal/config"
	"github.com/sunny-azu-red/stock-items/internal/db"
)

// New returns a pool bound to a fresh, migrated schema. The test is
// skipped when no database is configured (DB_HOST unset and no .env).
func New(t testing.TB) *pgxpool.Pool {
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
	pool, err := db.Open(ctx, dbCfg)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.DropSchema(ctx, pool, dbCfg.Schema); err != nil {
			t.Errorf("drop schema %s: %v", dbCfg.Schema, err)
		}
		pool.Close()
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}
