package config

import (
	"os"
	"testing"
	"time"
)

func TestSchema(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"public", ""},
		{" public ", ""},
		{"e2e_desktop", "e2e_desktop"},
		{" test_ab12 ", "test_ab12"},
	}
	for _, tt := range tests {
		if got := schema(tt.in); got != tt.want {
			t.Errorf("schema(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCheck(t *testing.T) {
	good := Config{Currency: "EUR", SKUSearchURL: "https://www.google.com/search?q="}
	tests := []struct {
		name string
		edit func(*Config)
		ok   bool
	}{
		{"defaults", func(*Config) {}, true},
		{"other engine", func(c *Config) { c.SKUSearchURL = "http://duckduckgo.com/?q=" }, true},
		{"lei", func(c *Config) { c.Currency = "lei" }, true},
		{"no scheme", func(c *Config) { c.SKUSearchURL = "www.google.com/search?q=" }, false},
		{"script", func(c *Config) { c.SKUSearchURL = "javascript:alert(1)//" }, false},
		{"long currency", func(c *Config) { c.Currency = "Romanian lei" }, false},
	}
	for _, tt := range tests {
		c := good
		tt.edit(&c)
		if err := c.check(); (err == nil) != tt.ok {
			t.Errorf("%s: check() = %v, want ok %v", tt.name, err, tt.ok)
		}
	}
}

func TestLocation(t *testing.T) {
	machine := machineZone().String()
	tests := []struct{ tz, want string }{
		{"Europe/Berlin", "Europe/Berlin"},
		{" :America/New_York ", "America/New_York"},
		{"UTC", "UTC"},
		{"", machine},
		{"Nowhere/City", machine},
	}
	for _, tt := range tests {
		t.Setenv("TZ", tt.tz)
		if got := location().String(); got != tt.want {
			t.Errorf("TZ=%q: location() = %q, want %q", tt.tz, got, tt.want)
		}
	}
	t.Setenv("TZ", "")
	os.Unsetenv("TZ")
	if got := location(); got != time.Local {
		t.Errorf("TZ unset: location() = %v, want time.Local", got)
	}
}
