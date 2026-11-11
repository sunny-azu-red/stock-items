#!/usr/bin/env bash
# Operator-only script for the Postgres server (root@postgres-db).
# Coding agents: NEVER run this script against the real server
# (scripts/test-db-server.sh runs it against a throwaway local Postgres).
#
# Drops the pg_temp_N and pg_toast_temp_N schemas of the Pitbox database.
# Postgres makes a pair for each connection slot that ever creates a
# temporary table (migration 00022's reorder_columns does, and the Go tests
# run it on many connections at once) and keeps them for reuse. They are
# harmless (at most about max_connections pairs) and Postgres makes them
# again when needed; this only tidies the schema list. Needs the superuser.
#
# A session that has used a temporary table keeps its (possibly empty)
# temp schema cached; dropping it under the session would leave catalog
# rows without a schema, which break pg_dump. So this refuses while any
# other session is connected to the database: stop the Pitbox app (and
# anything else using it) first. It drops in one transaction, which a
# temp schema that isn't empty fails and rolls back, and checks afterwards
# that no catalog row was left without its schema.
#
# Settings (environment, for tests): SYNC_DB, SYNC_PG_HOST, SYNC_PG_PORT, SYNC_PG_USER
set -euo pipefail

DB_NAME="${SYNC_DB:-stock_items}"
PG_HOST="${SYNC_PG_HOST:-localhost}"
PG_PORT="${SYNC_PG_PORT:-5432}"
PG_USER="${SYNC_PG_USER:-postgres}"
unset PGHOST PGHOSTADDR PGPORT PGUSER PGDATABASE PGSERVICE PGSERVICEFILE PGOPTIONS PSQLRC
export PGPASSFILE="${PGPASSFILE:-${HOME}/.pgpass}"
export PGAPPNAME="pitbox-cleanup-temp-schemas"

psql -X -q -v ON_ERROR_STOP=1 --single-transaction -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" <<'SQL'
SET lock_timeout = '5s';
DO $$
DECLARE
    r record;
    dropped int := 0;
    others text;
BEGIN
    SELECT string_agg(format('pid %s %s from %s (%s)', pid, usename, COALESCE(client_addr::text, 'local socket'),
                             COALESCE(NULLIF(application_name, ''), 'no name')), ', ' ORDER BY pid) INTO others
    FROM pg_stat_activity
    WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend';
    IF others IS NOT NULL THEN
        RAISE EXCEPTION 'Other sessions are connected to % (%): stop the Pitbox app and anything else using the database first. Nothing was dropped.',
            current_database(), others;
    END IF;
    FOR r IN SELECT nspname FROM pg_namespace
             WHERE nspname ~ '^pg_(toast_)?temp_[0-9]+$'
               AND oid <> pg_my_temp_schema()
               AND nspname IS DISTINCT FROM 'pg_toast_' || substr((SELECT nspname FROM pg_namespace WHERE oid = pg_my_temp_schema()), 4)
             ORDER BY length(nspname), nspname
    LOOP
        -- Without CASCADE: a temp schema that still holds something fails
        -- here and the whole clean-up rolls back.
        EXECUTE format('DROP SCHEMA %I', r.nspname);
        dropped := dropped + 1;
    END LOOP;
    RAISE NOTICE 'Dropped % temporary schemas.', dropped;
END $$;
SQL

ORPHANS="$(psql -X -At -v ON_ERROR_STOP=1 -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" -c "
  SELECT count(*) FROM pg_class c WHERE NOT EXISTS (SELECT 1 FROM pg_namespace n WHERE n.oid = c.relnamespace)")"
if [ "${ORPHANS}" != 0 ]; then
  echo "WARNING: ${ORPHANS} catalog rows in ${DB_NAME} have no schema (a session used a temp schema while it was dropped); pg_dump fails until they are gone." >&2
  exit 1
fi
echo "==> No catalog rows without a schema."
