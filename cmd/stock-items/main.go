// Command stock-items runs the Stock Items web app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/config"
	"github.com/sunny-azu-red/stock-items/internal/db"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/web"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = ""

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	migrateOnly := flag.Bool("migrate-only", false, "apply database migrations and exit")
	reset := flag.Bool("reset-schema", false, "drop DB_SCHEMA before starting (only for test_* / e2e_* schemas)")
	debugLog := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debugLog {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *reset {
		if err := resetSchema(ctx, cfg.DB); err != nil {
			return err
		}
	}

	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	logger.Info("database ready", "host", cfg.DB.Host, "database", cfg.DB.Database, "schema", schemaName(cfg.DB))
	if *migrateOnly {
		return nil
	}

	srv, err := web.New(store.New(pool), web.Options{Currency: cfg.Currency, Version: buildVersion(), Logger: logger})
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second, // photo uploads extend their own deadlines
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", "http://"+cfg.Addr())
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		stop() // a second Ctrl+C kills the process right away
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}

func resetSchema(ctx context.Context, cfg config.DB) error {
	if cfg.Schema == "" {
		return db.ErrUnsafeSchema
	}
	plain := cfg
	plain.Schema = ""
	pool, err := db.Open(ctx, plain)
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.DropSchema(ctx, pool, cfg.Schema)
}

func schemaName(cfg config.DB) string {
	if cfg.Schema == "" {
		return "public"
	}
	return cfg.Schema
}

// buildVersion prefers the -ldflags version, then the VCS revision that
// `go build` embeds.
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}
