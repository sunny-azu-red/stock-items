// Package config loads application settings from the environment and an
// optional .env file.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// DB holds the Postgres connection settings.
type DB struct {
	Host     string
	Port     string
	Database string
	// Schema is the Postgres schema used as search_path. Empty means "public".
	Schema   string
	Username string
	Password string
}

// Config is the full application configuration.
type Config struct {
	DB       DB
	Host     string
	Port     string
	Currency string
}

// Addr is the HTTP listen address.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, c.Port) }

// URL returns a postgres:// connection URL (without search_path).
func (d DB) URL() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(d.Username, d.Password),
		Host:   net.JoinHostPort(d.Host, d.Port),
		Path:   "/" + d.Database,
	}
	return u.String()
}

// Load reads the nearest .env file (searching from the working directory
// upwards, so tests inside packages find the project file) and then the
// process environment. Real environment variables always win.
func Load() (Config, error) {
	if p := findDotenv(); p != "" {
		if err := godotenv.Load(p); err != nil {
			return Config{}, fmt.Errorf("load %s: %w", p, err)
		}
	}
	cfg := Config{
		DB: DB{
			Host:     env("DB_HOST", "127.0.0.1"),
			Port:     env("DB_PORT", "5432"),
			Database: env("DB_DATABASE", "stock_items"),
			Schema:   schema(os.Getenv("DB_SCHEMA")),
			Username: env("DB_USERNAME", "postgres"),
			Password: os.Getenv("DB_PASSWORD"),
		},
		Host:     env("HOST", "0.0.0.0"),
		Port:     env("PORT", "8080"),
		Currency: env("APP_CURRENCY", "EUR"),
	}
	return cfg, nil
}

// schema treats "public" as no schema: the database's own search_path then
// applies, and nothing tries to create public (which needs CREATE on the
// database even though it exists).
func schema(v string) string {
	if v = strings.TrimSpace(v); v == "public" {
		return ""
	}
	return v
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// findDotenv returns the .env in the working directory. Inside a Go module
// (tests run in package directories) it also looks in the parents up to
// the module root, but never above it, so a deployed binary cannot pick up
// an unrelated /opt/.env or /.env.
func findDotenv() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := cwd; ; {
		if p := filepath.Join(dir, ".env"); exists(p) {
			if dir == cwd || insideModule(dir) {
				return p
			}
			return ""
		}
		if exists(filepath.Join(dir, "go.mod")) {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// insideModule reports whether a go.mod exists in dir or above it, so the
// walk from cwd up to dir stayed within one module.
func insideModule(dir string) bool {
	for d := dir; ; {
		if exists(filepath.Join(d, "go.mod")) {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
