#!/usr/bin/env bash
# Operator-only script for the Postgres server (root@postgres-db).
# Coding agents: NEVER run this script against the real server.
#
# Drops the empty pg_temp_N and pg_toast_temp_N schemas of the Pitbox
# database. Postgres makes a pair for each connection slot that ever creates
# a temporary table (migration 00022's reorder_columns does, and the Go tests
# run it on many connections at once) and keeps them, empty, for reuse; they
# are harmless, at most about max_connections of each, and Postgres makes
# them again when needed. Only a superuser can drop them.
#
# Only empty ones go: DROP without CASCADE fails on a schema still holding a
# session's temporary tables, which is then skipped. Run it while no tests
# are running against the server.
#
# Settings (environment, for tests): SYNC_DB, SYNC_PG_HOST, SYNC_PG_PORT, SYNC_PG_USER
set -euo pipefail

DB_NAME="${SYNC_DB:-stock_items}"
PG_HOST="${SYNC_PG_HOST:-localhost}"
PG_PORT="${SYNC_PG_PORT:-5432}"
PG_USER="${SYNC_PG_USER:-postgres}"
export PGPASSFILE="${PGPASSFILE:-${HOME}/.pgpass}"

psql -X -q -v ON_ERROR_STOP=1 -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" <<'SQL'
DO $$
DECLARE
    r record;
    dropped int := 0;
    kept text[] := '{}';
BEGIN
    -- Each pg_temp_N with its pg_toast_temp_N, together: a session using
    -- one keeps both (its toast schema may be empty while it is in use).
    FOR r IN SELECT nspname, substr(nspname, 9) AS num FROM pg_namespace
             WHERE nspname ~ '^pg_temp_[0-9]+$' AND oid <> pg_my_temp_schema()
             ORDER BY length(nspname), nspname
    LOOP
        BEGIN
            EXECUTE format('DROP SCHEMA %I', r.nspname);
            dropped := dropped + 1;
            IF to_regnamespace('pg_toast_temp_' || r.num) IS NOT NULL THEN
                EXECUTE format('DROP SCHEMA %I', 'pg_toast_temp_' || r.num);
                dropped := dropped + 1;
            END IF;
        EXCEPTION WHEN dependent_objects_still_exist THEN
            kept := kept || r.nspname::text; -- in use: a session's temporary tables
        END;
    END LOOP;
    -- A toast schema whose pg_temp_N is gone already.
    FOR r IN SELECT nspname FROM pg_namespace
             WHERE nspname ~ '^pg_toast_temp_[0-9]+$' AND to_regnamespace('pg_temp_' || substr(nspname, 15)) IS NULL
    LOOP
        BEGIN
            EXECUTE format('DROP SCHEMA %I', r.nspname);
            dropped := dropped + 1;
        EXCEPTION WHEN dependent_objects_still_exist THEN
            kept := kept || r.nspname::text;
        END;
    END LOOP;
    RAISE NOTICE 'Dropped % empty temporary schemas%.', dropped,
        CASE WHEN cardinality(kept) > 0 THEN format('; kept %s in use (%s)', cardinality(kept), array_to_string(kept, ', ')) ELSE '' END;
END $$;
SQL
