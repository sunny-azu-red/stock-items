package store_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// useSettings puts settings in use until the test ends (the app's copy in
// memory, as main and the Settings page set it): the defaults, as edit
// changes them.
func useSettings(t *testing.T, edit func(*stock.Settings)) {
	t.Helper()
	old := stock.Current()
	set := stock.DefaultSettings()
	edit(&set)
	stock.Use(set)
	t.Cleanup(func() { stock.Use(old) })
}

// The row migration 00010 makes holds the defaults, and the table has the
// columns GetSettings reads, in its order.
func TestSettingsRow(t *testing.T) {
	s, pool, ctx := newStorePool(t)
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := stock.DefaultSettings(); !reflect.DeepEqual(got, want) {
		t.Errorf("the row =\n%+v\nwant the defaults\n%+v", got, want)
	}
	rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'settings' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{"id", "version"}, store.SettingsColumns...), "created_at", "updated_at")
	if !slices.Equal(cols, want) {
		t.Errorf("columns =\n%q\nwant\n%q", cols, want)
	}
	if n := reflect.TypeFor[stock.Settings]().NumField(); n != len(store.SettingsColumns)+1 {
		t.Errorf("stock.Settings has %d fields, want Version and one a column (%d)", n, len(store.SettingsColumns))
	}
}

// A save moves the version on; a form opened before it is stale, and the
// same form posted again was saved already.
func TestUpdateSettings(t *testing.T) {
	s, ctx := newStore(t)
	in := stock.SettingsInput{Settings: stock.DefaultSettings(), FormKey: "0123456789abcdef0123456789abcdef"}
	in.Currency, in.SKUSearchPrefix, in.NumberFormat, in.GracePercent = "LEI", "site:ro", stock.NumberDotComma, 15
	in.Categories = []string{"Lubricants"}
	saved, err := s.UpdateSettings(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 2 || saved.Currency != "LEI" || saved.SKUSearchPrefix != "site:ro" || saved.NumberFormat != stock.NumberDotComma ||
		saved.GracePercent != 15 || !slices.Equal(saved.Categories, []string{"Lubricants"}) {
		t.Errorf("saved = %+v", saved)
	}
	if got, _ := s.GetSettings(ctx); !reflect.DeepEqual(got, saved) {
		t.Errorf("read back %+v, want %+v", got, saved)
	}

	// The same form again: saved already, nothing done.
	if _, err := s.UpdateSettings(ctx, in); !errors.As(err, new(*store.AlreadySavedError)) {
		t.Errorf("the same form again: %v, want AlreadySavedError", err)
	}
	// Another form opened at version 1: stale.
	in.FormKey = "fedcba9876543210fedcba9876543210"
	var stale *store.StaleError
	if _, err := s.UpdateSettings(ctx, in); !errors.As(err, &stale) || stale.Gone {
		t.Errorf("a form from before the save: %v, want StaleError", err)
	} else if stale.Error() != "The settings were changed since you opened them (another tab or person). Reload to see the latest, then make your change again." {
		t.Errorf("message = %q", stale.Error())
	}

}

// The built-in suggestions are the settings' lists: what they hold is
// offered after the spellings in use, and never decides a spelling.
func TestSettingsSuggestionLists(t *testing.T) {
	s, ctx := newStore(t)
	cats, err := s.Categories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cats, "Filters") || !slices.Contains(cats, "Suspension") {
		t.Errorf("categories = %q, want the built-in ones", cats)
	}
	in := stock.SettingsInput{Settings: stock.DefaultSettings()}
	in.Categories = []string{"Lubricants", "Fasteners"}
	in.WorkNames = []string{"Chain Clean"}
	if _, err := s.UpdateSettings(ctx, in); err != nil {
		t.Fatal(err)
	}
	cats, _ = s.Categories(ctx)
	if !slices.Equal(cats, []string{"Fasteners", "Lubricants"}) {
		t.Errorf("categories = %q, want the settings' list", cats)
	}
	names, err := s.KitNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"Chain Clean"}) {
		t.Errorf("kit names = %q, want the settings' list", names)
	}
	// An item's "lubricants" stays as typed: a list's entry isn't a
	// spelling in use.
	id := mustItem(t, s, stock.ItemInput{Name: "Chain lube", Unit: stock.UnitPiece, Category: "lubricants"})
	it, err := s.GetItem(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Category != "lubricants" {
		t.Errorf("category = %q, want it as typed", it.Category)
	}
}

// The break-in window follows the grace setting: a break-in read past
// Break-in at and its grace is refused as overdue.
func TestBreakInWindowFollowsTheGrace(t *testing.T) {
	for _, tt := range []struct {
		grace int
		ok    bool
	}{{10, false}, {20, true}} {
		useSettings(t, func(s *stock.Settings) { s.GracePercent = tt.grace })
		s, ctx := newStore(t)
		v := mustVehicle(t, s, "KTM 390")
		kitIn := stock.KitInput{VehicleID: v, Name: "Oil", IntervalDistance: ptr(10000), BreakInAt: ptr(1000)}
		kitIn.Normalize()
		kit, err := s.CreateKit(ctx, kitIn)
		if err != nil {
			t.Fatal(err)
		}
		in := stock.ServiceInput{VehicleID: v, KitID: &kit, KitTier: stock.TierBreakIn, Title: "Oil",
			PerformedOn: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), Odometer: ptr(1150)}
		in.Normalize()
		_, err = s.CreateService(ctx, in)
		if (err == nil) != tt.ok {
			t.Errorf("a break-in at 1 150 with a %d%% grace: %v, want ok %v", tt.grace, err, tt.ok)
		}
	}
}
