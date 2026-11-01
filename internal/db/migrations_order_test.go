package db

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"testing"
)

var (
	migrationName = regexp.MustCompile(`^(\d{5})_[a-z0-9]+(?:_[a-z0-9]+)*\.sql$`)
	gooseUp       = regexp.MustCompile(`(?m)^-- \+goose Up[ \t]*\r?$`)
	gooseDown     = regexp.MustCompile(`(?m)^-- \+goose Down[ \t]*\r?$`)
)

// TestMigrationFiles checks the migrations without a database: numbered
// 00001, 00002, … with no gaps or duplicates, named 000NN_snake_case.sql,
// each with an Up and a Down section. goose would only notice some of this
// at start-up, against the real data.
func TestMigrationFiles(t *testing.T) {
	// Every file on disk, so a misnamed one that the embed pattern
	// (*.sql) leaves out is caught too.
	onDisk, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(embedded) != len(onDisk) {
		t.Errorf("%d files in migrations/, %d embedded: every file must be a .sql migration", len(onDisk), len(embedded))
	}
	if len(embedded) == 0 {
		t.Fatal("no migrations embedded")
	}

	for i, e := range onDisk {
		name := e.Name()
		m := migrationName.FindStringSubmatch(name)
		if e.IsDir() || m == nil {
			t.Errorf("%s: want a file named 000NN_snake_case.sql", name)
			continue
		}
		// ReadDir sorts by name, and the numbers have a fixed width, so
		// the i-th file must carry number i+1.
		n, _ := strconv.Atoi(m[1])
		if want := fmt.Sprintf("%05d", i+1); m[1] != want {
			t.Errorf("%s: number %d, want %s (numbers start at 00001 and go up by one, no gaps or duplicates)", name, n, want)
		}
		body, err := fs.ReadFile(migrations, "migrations/"+name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !gooseUp.Match(body) {
			t.Errorf("%s: no \"-- +goose Up\" line", name)
		}
		if !gooseDown.Match(body) {
			t.Errorf("%s: no \"-- +goose Down\" line", name)
		}
	}
}
