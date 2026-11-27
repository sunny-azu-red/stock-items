package config

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // the named zones, whatever the host has installed
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

// tzif is a zone file (TZif version 1) of a zone that is always offset
// seconds east of UTC.
func tzif(abbr string, offset int32) []byte {
	var b bytes.Buffer
	b.WriteString("TZif\x00")
	b.Write(make([]byte, 15))
	// isutcnt, isstdcnt, leapcnt, timecnt, typecnt, charcnt
	for _, n := range []uint32{0, 0, 0, 0, 1, uint32(len(abbr) + 1)} {
		_ = binary.Write(&b, binary.BigEndian, n)
	}
	_ = binary.Write(&b, binary.BigEndian, offset)
	b.Write([]byte{0, 0}) // not DST, the abbreviation at 0
	b.WriteString(abbr + "\x00")
	return b.Bytes()
}

// writeZone writes a zone file offset seconds east of UTC and returns its
// path.
func writeZone(t *testing.T, abbr string, offset int32) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), abbr)
	if err := os.WriteFile(path, tzif(abbr, offset), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocation(t *testing.T) {
	// The machine's zone is a file of the test's, whatever the host's is.
	machine := writeZone(t, "MCH", 5*3600+1800)
	old := localtime
	localtime = machine
	t.Cleanup(func() { localtime = old })
	file := writeZone(t, "FIL", -7*3600)
	when := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		tz     string
		name   string // "" for any
		offset int
	}{
		{"Europe/Berlin", "Europe/Berlin", 2 * 3600},
		{" :America/New_York ", "America/New_York", -4 * 3600},
		{"UTC", "UTC", 0},
		{file, file, -7 * 3600},
		{":" + file, file, -7 * 3600},
		{"", "", 5*3600 + 1800},
		{"Nowhere/City", "", 5*3600 + 1800},
		{"/no/such/zone", "", 5*3600 + 1800},
		{"../etc/passwd", "", 5*3600 + 1800},
	}
	for _, tt := range tests {
		t.Setenv("TZ", tt.tz)
		loc := location()
		if _, offset := when.In(loc).Zone(); offset != tt.offset || tt.name != "" && loc.String() != tt.name {
			t.Errorf("TZ=%q: location() = %q (offset %d), want %q (offset %d)", tt.tz, loc, offset, tt.name, tt.offset)
		}
	}

	// Without a zone file, the machine's zone is UTC.
	localtime = filepath.Join(t.TempDir(), "none")
	t.Setenv("TZ", "")
	if got := location(); got != time.UTC {
		t.Errorf("no /etc/localtime: location() = %v, want UTC", got)
	}

	os.Unsetenv("TZ")
	if got := location(); got != time.Local {
		t.Errorf("TZ unset: location() = %v, want time.Local", got)
	}
}
