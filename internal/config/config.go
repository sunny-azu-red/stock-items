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
	"unicode/utf8"

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
	// SKUSearchURL is the search engine URL of part number links: the
	// number goes in place of {q}, e.g. "https://www.google.com/search?q={q}".
	SKUSearchURL string
	// SKUSearchPrefix is added to every part number search, e.g. "site:de".
	// Empty means none.
	SKUSearchPrefix string
	// Location decides when "today" starts: the zone named by TZ, or the
	// machine's zone when TZ is unset, empty or unknown.
	Location *time.Location
	// AllowedHosts are the host names and IPs the server answers to
	// (APP_ALLOWED_HOSTS, comma-separated, any port ignored); empty means
	// any. Loopback always works.
	AllowedHosts []string
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
	cfg, err := load()
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.check()
}

// load reads the settings without checking them.
func load() (Config, error) {
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
		Host:            env("HOST", "0.0.0.0"),
		Port:            env("PORT", "8080"),
		Currency:        env("APP_CURRENCY", "EUR"),
		SKUSearchURL:    env("APP_SKU_SEARCH_URL", DefaultSKUSearchURL),
		SKUSearchPrefix: strings.TrimSpace(os.Getenv("APP_SKU_SEARCH_PREFIX")),
		Location:        location(),
		AllowedHosts:    list(os.Getenv("APP_ALLOWED_HOSTS")),
	}
	return cfg, nil
}

// list splits a comma-separated setting, dropping blanks.
func list(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
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
	b, err := os.ReadFile(tz)
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

// DefaultSKUSearchURL is the part number search used when
// APP_SKU_SEARCH_URL is unset.
const DefaultSKUSearchURL = "https://www.google.com/search?q={q}"

// check refuses settings that would otherwise only show up later, as
// broken part number links or an odd price label.
func (c Config) check() error {
	u, err := url.Parse(strings.ReplaceAll(c.SKUSearchURL, "{q}", "x"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("APP_SKU_SEARCH_URL must be a full http(s) URL such as %s, not %q", DefaultSKUSearchURL, c.SKUSearchURL)
	}
	if err := checkPlaceholder(c.SKUSearchURL); err != nil {
		return err
	}
	if utf8.RuneCountInString(c.Currency) > 10 {
		return fmt.Errorf("APP_CURRENCY %q is too long (at most 10 characters)", c.Currency)
	}
	for _, h := range c.AllowedHosts {
		// A URL would never match a Host header, and every page would be
		// refused.
		if strings.ContainsAny(h, "/@?# ") {
			return fmt.Errorf("APP_ALLOWED_HOSTS takes host names or IPs such as pitbox.lan,192.168.1.5, not %q", h)
		}
	}
	return nil
}

// checkPlaceholder wants exactly one {q} in the part number search URL:
// that is where the number goes. A URL ending in "=" (the old form, which
// had the number appended) gets the fixed value suggested.
func checkPlaceholder(searchURL string) error {
	switch n := strings.Count(searchURL, "{q}"); {
	case n == 0 && strings.HasSuffix(searchURL, "="):
		return fmt.Errorf("APP_SKU_SEARCH_URL must mark where the part number goes with {q}; add it: %s{q}", searchURL)
	case n == 0:
		return fmt.Errorf("APP_SKU_SEARCH_URL must mark where the part number goes with {q}, as in %s, not %q", DefaultSKUSearchURL, searchURL)
	case n > 1:
		return fmt.Errorf("APP_SKU_SEARCH_URL must contain {q} once, not %d times: %q", n, searchURL)
	}
	return nil
}

// LoadDB reads the database settings like Load, without checking the app's
// other settings: tests need only the database, and a part number search
// URL the app would refuse shouldn't stop them.
func LoadDB() (DB, error) {
	cfg, err := load()
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
