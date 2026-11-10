package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A build for the screenshots pins the clock (-X main.date, set by
// scripts/screenshots.sh through e2e/playwright.config.ts), so the pages
// show the same days on every run. It changes the e2e database's own
// timestamps too (pinTimestamps), outside the migrations, so it only ever
// runs on a throwaway e2e_* schema.

// checkSimulatedDate refuses a pinned-date build on any schema but an e2e_*
// one, and a date that isn't RFC 3339. It returns the pinned instant, or
// the zero time for a normal build.
func checkSimulatedDate(date, schema string) (time.Time, error) {
	if date == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, date)
	if err != nil {
		return time.Time{}, fmt.Errorf("main.date %q is not RFC 3339: %w", date, err)
	}
	if !strings.HasPrefix(schema, "e2e_") {
		return time.Time{}, errors.New("this build has a pinned date (-X main.date) for the screenshots, " +
			"which rewrites the database's timestamp defaults: it runs only on an e2e_* DB_SCHEMA, not " + schema)
	}
	return t, nil
}

// simulatedClock runs on from pinned as real time passes since start.
func simulatedClock(pinned, start time.Time) func() time.Time {
	return func() time.Time { return pinned.Add(time.Since(start)) }
}

// pinTimestamps makes the timestamps the database fills in itself
// (created_at, updated_at, archived_at, deleted_at) follow the simulated
// clock: column defaults and update triggers in the e2e schema, which is
// dropped and made again on the next run. updated_at runs a day ahead, so
// an item page shows an Updated day after the day it was added.
func pinTimestamps(ctx context.Context, pool *pgxpool.Pool, schema string, pinned, start time.Time) error {
	if !strings.HasPrefix(schema, "e2e_") {
		return fmt.Errorf("pinTimestamps: refusing schema %q", schema)
	}
	const layout = "2006-01-02 15:04:05.000000-07"
	ts, serverStart := pinned.UTC().Format(layout), start.UTC().Format(layout)
	simNow := fmt.Sprintf("('%s'::timestamptz + (clock_timestamp() - '%s'::timestamptz))", ts, serverStart)
	simUpdated := fmt.Sprintf("('%s'::timestamptz + interval '24 hours' + (clock_timestamp() - '%s'::timestamptz))", ts, serverStart)
	var stmts []string
	for _, table := range []string{"items", "vehicles", "service_kits", "service_records", "stock_movements", "shopping_entries"} {
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN created_at SET DEFAULT %s", table, simNow))
	}
	for _, table := range []string{"items", "vehicles", "service_kits"} {
		stmts = append(stmts,
			fmt.Sprintf("ALTER TABLE %s ALTER COLUMN updated_at SET DEFAULT %s", table, simUpdated),
			// archived_at only when the update archives the row; later
			// updates of an archived row keep the day it was archived.
			fmt.Sprintf(`CREATE OR REPLACE FUNCTION _pin_%[1]s_timestamps() RETURNS trigger AS $$
				BEGIN
					NEW.updated_at := %[2]s;
					IF NEW.archived_at IS NOT NULL AND NEW.archived_at IS DISTINCT FROM OLD.archived_at THEN
						NEW.archived_at := %[3]s;
					END IF;
					RETURN NEW;
				END;
				$$ LANGUAGE plpgsql`, table, simUpdated, simNow),
			fmt.Sprintf("DROP TRIGGER IF EXISTS _pin_%[1]s_timestamps ON %[1]s", table),
			fmt.Sprintf("CREATE TRIGGER _pin_%[1]s_timestamps BEFORE UPDATE ON %[1]s FOR EACH ROW EXECUTE FUNCTION _pin_%[1]s_timestamps()", table))
	}
	stmts = append(stmts, fmt.Sprintf("ALTER TABLE trash ALTER COLUMN deleted_at SET DEFAULT %s", simNow))
	for _, sql := range stmts {
		if _, err := pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("pin timestamps: %w", err)
		}
	}
	return nil
}
