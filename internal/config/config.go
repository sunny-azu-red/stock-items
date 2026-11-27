// Package config loads application settings from the environment and an
// optional .env file.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// Config is what the app needs from its deployment: the database, where to
// listen, and the time zone. Preferences (currency, units, formats, the
// part number search, …) are settings, kept in the database and set on the
// Settings page (stock.Settings), never in the environment.
type Config struct {
	DB   DB
	Host string
	Port string
	// Location decides when "today" starts: the zone named by TZ, or the
	// machine's zone when TZ is unset, empty or unknown.
	Location *time.Location
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
		Location: location(),
	}
	return cfg, nil
}

// location reads TZ. Go itself turns an empty or unknown TZ into UTC; here
// both mean the machine's zone, and a bad name is a warning, not a reason
// to refuse to start.
func location() *time.Location {
	tz, set := os.LookupEnv("TZ")
	if !set {
		return time.Local // Go has read the machine's zone
	}
	if tz = strings.TrimPrefix(strings.TrimSpace(tz), ":"); tz != "" {
		loc, err := loadZone(tz)
		if err == nil {
			return loc
		}
		slog.Warn("unknown TZ, using the machine's time zone", "TZ", tz, "err", err)
	}
	return machineZone()
}

// loadZone loads a zone by name ("Europe/Berlin") or, as the C library
// does, from the absolute path of a zone file
// ("/usr/share/zoneinfo/Europe/Berlin"), which time.LoadLocation refuses.
func loadZone(tz string) (*time.Location, error) {
	if !filepath.IsAbs(tz) {
		return time.LoadLocation(tz)
	}
	b, err := os.ReadFile(tz) //nolint:gosec // TZ is the operator's own setting
	if err != nil {
		return nil, err
	}
	return time.LoadLocationFromTZData(tz, b)
}

// localtime is the machine's zone file (tests point it elsewhere).
var localtime = "/etc/localtime"

// machineZone is the zone in /etc/localtime (in Docker, the host's when
// compose.yaml mounts it), or UTC without one.
func machineZone() *time.Location {
	b, err := os.ReadFile(localtime)
	if err != nil {
		return time.UTC
	}
	loc, err := time.LoadLocationFromTZData("Local", b)
	if err != nil {
		return time.UTC
	}
	return loc
}

// LoadDB reads the database settings like Load: tests need only the
// database.
func LoadDB() (DB, error) {
	cfg, err := Load()
	return cfg.DB, err
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
