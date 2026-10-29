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
	Username string
	Password string
	// Schema is the Postgres schema used as search_path. Empty means "public".
	Schema string
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
			Username: env("DB_USERNAME", "postgres"),
			Password: os.Getenv("DB_PASSWORD"),
			Schema:   strings.TrimSpace(os.Getenv("DB_SCHEMA")),
		},
		Host:     env("HOST", "0.0.0.0"),
		Port:     env("PORT", "8080"),
		Currency: env("APP_CURRENCY", "EUR"),
	}
	return cfg, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// findDotenv walks up from the working directory looking for a .env file,
// stopping at the first directory that contains go.mod.
func findDotenv() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
