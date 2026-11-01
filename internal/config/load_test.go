package config

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// settings are the variables load reads.
var settings = []string{"DB_HOST", "DB_PORT", "DB_DATABASE", "DB_SCHEMA", "DB_USERNAME", "DB_PASSWORD",
	"HOST", "PORT", "APP_CURRENCY", "APP_SKU_SEARCH_URL", "APP_SKU_SEARCH_PREFIX", "TZ", "APP_ALLOWED_HOSTS"}

// cleanEnv unsets every setting until the test ends, and runs it in a new
// directory holding the given files.
func cleanEnv(t *testing.T, files map[string]string) string {
	t.Helper()
	for _, k := range settings {
		t.Setenv(k, "") // put back when the test ends
		os.Unsetenv(k)
	}
	dir := t.TempDir()
	write(t, dir, files)
	t.Chdir(dir)
	return dir
}

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoad(t *testing.T) {
	cleanEnv(t, map[string]string{".env": `DB_HOST=db.lan
DB_PORT=" 5433 "
DB_SCHEMA=" public "
DB_PASSWORD=" p@ss w0rd "
APP_CURRENCY=RON
APP_SKU_SEARCH_PREFIX=" site:de "
APP_ALLOWED_HOSTS=" pitbox.lan, ,192.168.1.5:8080,"
`})
	t.Setenv("APP_CURRENCY", " lei ") // the real environment wins over .env
	t.Setenv("PORT", "  ")            // blank is the default
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := DB{Host: "db.lan", Port: "5433", Database: "stock_items", Schema: "", Username: "postgres", Password: " p@ss w0rd "}
	if cfg.DB != want {
		t.Errorf("DB = %+v, want %+v", cfg.DB, want)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != "8080" || cfg.Addr() != "0.0.0.0:8080" {
		t.Errorf("Host, Port, Addr = %q, %q, %q", cfg.Host, cfg.Port, cfg.Addr())
	}
	if cfg.Currency != "lei" || cfg.SKUSearchPrefix != "site:de" || cfg.SKUSearchURL != DefaultSKUSearchURL {
		t.Errorf("Currency, SKUSearchPrefix, SKUSearchURL = %q, %q, %q", cfg.Currency, cfg.SKUSearchPrefix, cfg.SKUSearchURL)
	}
	if cfg.Location == nil {
		t.Error("no Location")
	}
	if got := cfg.AllowedHosts; !slices.Equal(got, []string{"pitbox.lan", "192.168.1.5:8080"}) {
		t.Errorf("AllowedHosts = %q", got)
	}

	// Settings the app would only fail on later are refused at once;
	// LoadDB, for tests, needs only the database's.
	t.Setenv("APP_SKU_SEARCH_URL", "www.google.com/search?q=")
	if _, err := Load(); err == nil {
		t.Error("Load took a search URL without http(s) and {q}")
	}
	if db, err := LoadDB(); err != nil || db.Host != "db.lan" {
		t.Errorf("LoadDB = %+v, %v", db, err)
	}
}

func TestLoadWithoutDotenv(t *testing.T) {
	cleanEnv(t, nil)
	t.Setenv("DB_HOST", " 10.0.0.5 ")
	t.Setenv("HOST", "::1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.Host != "10.0.0.5" || cfg.DB.Port != "5432" || cfg.Currency != "EUR" || cfg.AllowedHosts != nil {
		t.Errorf("cfg = %+v", cfg)
	}
	if got := cfg.Addr(); got != "[::1]:8080" {
		t.Errorf("Addr() = %q, want [::1]:8080", got)
	}
}

func TestLoadRefusesABrokenDotenv(t *testing.T) {
	cleanEnv(t, map[string]string{".env": "DB_HOST='unterminated\n"})
	if _, err := Load(); err == nil {
		t.Error("Load read a broken .env")
	}
}

// .env is found in the working directory, or above it up to the module
// root, never above that: a deployed binary must not read /opt/.env.
func TestFindDotenv(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		cwd   string
		want  string // "" for none
	}{
		{"in the working directory", map[string]string{"app/.env": ""}, "app", "app/.env"},
		{"at the module root", map[string]string{"mod/go.mod": "module x\n", "mod/.env": ""}, "mod/internal/pkg", "mod/.env"},
		{"nearest wins", map[string]string{"mod/go.mod": "module x\n", "mod/.env": "", "mod/internal/.env": ""}, "mod/internal/pkg", "mod/internal/.env"},
		{"above the module root", map[string]string{"mod/go.mod": "module x\n", ".env": ""}, "mod/internal", ""},
		{"above, outside a module", map[string]string{".env": ""}, "opt/pitbox", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := cleanEnv(t, tt.files)
			cwd := filepath.Join(dir, tt.cwd)
			if err := os.MkdirAll(cwd, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(cwd)
			want := ""
			if tt.want != "" {
				want = filepath.Join(dir, tt.want)
			}
			// The temporary directory may be a symlink (macOS): compare
			// what both point to.
			if got := findDotenv(); !samePath(got, want) {
				t.Errorf("findDotenv() = %q, want %q", got, want)
			}
		})
	}
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// A password with URL syntax in it reaches Postgres as it is.
func TestDBURL(t *testing.T) {
	for _, d := range []DB{
		{Host: "db.lan", Port: "5432", Database: "stock_items", Username: "pitbox", Password: "p@ss:/w?rd#%20&x=y "},
		{Host: "::1", Port: "5433", Database: "my db", Username: "user@corp", Password: ""},
	} {
		cfg, err := pgxpool.ParseConfig(d.URL())
		if err != nil {
			t.Errorf("%+v: %v", d, err)
			continue
		}
		c := cfg.ConnConfig
		if c.Host != d.Host || strconv.Itoa(int(c.Port)) != d.Port || c.Database != d.Database || c.User != d.Username || c.Password != d.Password {
			t.Errorf("URL() = %q: pgx reads host %q port %d database %q user %q password %q", d.URL(), c.Host, c.Port, c.Database, c.User, c.Password)
		}
	}
}
