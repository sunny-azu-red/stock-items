// Package db opens the Postgres connection pool and applies migrations.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sunny-azu-red/stock-items/internal/config"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open creates a connection pool whose sessions use UTC. When cfg.Schema
// is set, the schema is created if needed and used as the search_path for
// every connection.
func Open(ctx context.Context, cfg config.DB) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.URL())
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}
	// Timestamps are instants: pinning the session to UTC keeps anything
	// in SQL from deciding a calendar day in the database server's zone.
	// Days are decided in Go, in the app's zone (see AGENTS.md).
	pcfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	if cfg.Schema != "" {
		// Quoted, so a name like "e2e_x,public" cannot widen the search path.
		pcfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{cfg.Schema}.Sanitize()
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping %s:%s/%s: %w", cfg.Host, cfg.Port, cfg.Database, err)
	}
	if cfg.Schema != "" {
		if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{cfg.Schema}.Sanitize()); err != nil {
			pool.Close()
			return nil, fmt.Errorf("create schema %q: %w", cfg.Schema, err)
		}
	}
	return pool, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// ErrUnsafeSchema is returned by DropSchema for schemas that do not look
// like throwaway test schemas.
var ErrUnsafeSchema = errors.New("refusing to drop a schema that is not prefixed with test_ or e2e_")

// DropSchema drops a throwaway schema (and everything in it). It only works
// for schemas named test_* or e2e_*, so it can never touch real data.
func DropSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	if !strings.HasPrefix(schema, "test_") && !strings.HasPrefix(schema, "e2e_") {
		return ErrUnsafeSchema
	}
	_, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	return err
}
