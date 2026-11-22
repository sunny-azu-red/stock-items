package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// A suggestion list's spellings (docs/design.md, suggestion lists): each
// source selects the spellings in use as v with pref 0, and built-in
// defaults with pref 1, leaving out the record $1 (a bigint; 0 for none).
// The list shows one spelling for each, ignoring case: a spelling in use
// before a built-in one, then the capitalized one (COLLATE "C", the same
// in every Postgres locale). Saving takes the spelling in use for text
// that differs from it only in case (spell), so "ktm" is saved as the
// "KTM" the list shows. Built-in ones don't count (a user's "Oil service"
// stays theirs), nor does the record being saved, so the only record with
// a spelling can change its case.
const (
	brandSpellings = `
		SELECT brand AS v, 0 AS pref FROM items WHERE brand <> '' AND id <> $1
		UNION SELECT brand, 0 FROM item_skus WHERE brand <> '' AND item_id <> $1`
	categorySpellings = `
		SELECT category AS v, 0 AS pref FROM items WHERE category <> '' AND id <> $1
		UNION SELECT unnest(ARRAY['Oil & Fluids', 'Filters', 'Brakes', 'Gaskets & O-Rings', 'Chain & Sprockets',
		                          'Electrical', 'Tyres', 'Cleaning & Care', 'Hardware', 'Tools',
		                          'Maintenance Kits', 'Bearings & Seals', 'Controls & Cables', 'Suspension']), 1`
	locationSpellings = `SELECT location AS v, 0 AS pref FROM items WHERE location <> '' AND id <> $1`
	supplierSpellings = `SELECT supplier AS v, 0 AS pref FROM stock_movements WHERE supplier <> '' AND id <> $1`
	makeSpellings     = `SELECT make AS v, 0 AS pref FROM vehicles WHERE make <> '' AND id <> $1`
	modelSpellings    = `SELECT model AS v, 0 AS pref FROM vehicles WHERE model <> '' AND id <> $1`
	// A kit's name and a service's title (Work performed) share a list;
	// these leave out a kit, and a service.
	kitNameSpellings = `
		SELECT name AS v, 0 AS pref FROM service_kits WHERE name <> '' AND id <> $1
		UNION SELECT title, 0 FROM service_records WHERE title <> ''` + workDefaults
	serviceTitleSpellings = `
		SELECT name AS v, 0 AS pref FROM service_kits WHERE name <> ''
		UNION SELECT title, 0 FROM service_records WHERE title <> '' AND id <> $1` + workDefaults
	workDefaults = `
		UNION SELECT unnest(ARRAY['Oil Service', 'Air Filter', 'Brake Pads (Front)', 'Brake Pads (Rear)',
		                          'Brake Fluid', 'Cabin Filter', 'Clutch Fluid', 'Coolant',
		                          'Spark Plugs', 'Valve Check']), 1`
)

// suggestions is a suggestion list: one spelling for each, by name.
func (s *Store) suggestions(ctx context.Context, spellings string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (lower(v)) v FROM (`+spellings+`) x
		ORDER BY lower(v), pref, v COLLATE "C"`, int64(0))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// spell is text as the suggestion list spells it, leaving out the record
// self (0 for a new one) and the built-in spellings: text itself when no
// other spelling in use differs from it only in case. A record's text that
// is exactly what it had (was) stays as it is: saving a record never
// changes a field it didn't change, even one whose spelling differs from
// the list's.
func spell(ctx context.Context, q queryRower, spellings string, self int64, was, text string) (string, error) {
	if strings.TrimSpace(text) == "" || (self != 0 && text == was) {
		return text, nil
	}
	var v string
	err := q.QueryRow(ctx, `
		SELECT v FROM (`+spellings+`) x WHERE pref = 0 AND lower(v) = lower($2)
		ORDER BY v COLLATE "C" LIMIT 1`, self, text).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return text, nil
	}
	return v, err
}

// spelledField is a field for spellFields: its text, its list's spellings
// and what the record had (was; "" for a new one).
type spelledField struct {
	text      *string
	spellings string
	was       string
}

// spellFields applies spell to each field of the record self.
func spellFields(ctx context.Context, q queryRower, self int64, fields ...spelledField) error {
	for _, f := range fields {
		v, err := spell(ctx, q, f.spellings, self, f.was, *f.text)
		if err != nil {
			return err
		}
		*f.text = v
	}
	return nil
}
