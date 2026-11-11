#!/usr/bin/env bash
# Operator-only script for the Postgres server (root@postgres-db).
# Coding agents: NEVER run this script.
set -euo pipefail

DB_NAME="stock_items"
STAGING_DB="sync_staging_temp"
PG_HOST="localhost"
PG_PORT="5432"
PG_USER="postgres"
APP_USER="stock"
export PGPASSFILE="${HOME}/.pgpass"

# Display prominent safety warning
cat <<EOF
======================================================================
                     ⚠️   CRITICAL ACTION WARNING   ⚠️
======================================================================
 DATABASE : ${DB_NAME}
 SOURCE   : dev (read-only snapshot)
 TARGET   : public (WILL BE ERASED AND REPLACED)

 WHAT WILL HAPPEN:
 - ALL existing tables, views, sequences, functions, and data in '${DB_NAME}'.public
   will be PERMANENTLY DROPPED.
 - The 'public' schema will then be populated with a fresh replica of
   everything currently in 'dev', owned by '${APP_USER}'.
 - Foreign keys, indexes, types, and sequence ownership will stay intact.
 - The 'dev' schema's data will NOT be altered (any tables/sequences
   in 'dev' owned by '${PG_USER}' will also be reassigned to '${APP_USER}').
======================================================================
EOF

read -rp "Type 'OVERWRITE' to continue (any other input aborts): " CONFIRM
if [ "${CONFIRM}" != "OVERWRITE" ]; then
  echo "==> Aborted. No changes were made to '${DB_NAME}'."
  exit 0
fi

# Always remove staging DB on script exit (even if it crashes or is cancelled)
cleanup() {
  psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d postgres \
    -c "DROP DATABASE IF EXISTS ${STAGING_DB};" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> 1. Setting up fresh staging database..."
psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d postgres -v ON_ERROR_STOP=1 <<SQL
DROP DATABASE IF EXISTS ${STAGING_DB};
CREATE DATABASE ${STAGING_DB};
SQL

echo "==> 2. Dumping 'dev' into staging..."
pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" --schema=dev \
  | psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${STAGING_DB}" -v ON_ERROR_STOP=1

echo "==> 3. Natively converting 'dev' to 'public' inside staging..."
psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${STAGING_DB}" -v ON_ERROR_STOP=1 <<SQL
DROP SCHEMA IF EXISTS public CASCADE;
ALTER SCHEMA dev RENAME TO public;
SQL

echo "==> 4. Clearing old objects inside '${DB_NAME}'.public (schema remains intact)..."
psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" -v app_user="${APP_USER}" -v ON_ERROR_STOP=1 <<'SQL'
DROP SCHEMA IF EXISTS dev_old CASCADE;
CREATE SCHEMA IF NOT EXISTS public;
ALTER SCHEMA public OWNER TO :"app_user";
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN (SELECT table_name FROM information_schema.views WHERE table_schema = 'public') LOOP
        EXECUTE 'DROP VIEW IF EXISTS public.' || quote_ident(r.table_name) || ' CASCADE';
    END LOOP;
    FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
        EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(r.tablename) || ' CASCADE';
    END LOOP;
    FOR r IN (SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = 'public') LOOP
        EXECUTE 'DROP SEQUENCE IF EXISTS public.' || quote_ident(r.sequence_name) || ' CASCADE';
    END LOOP;
    -- Functions and procedures (reorder_columns, migration 00022); an
    -- extension's own are left to the extension.
    FOR r IN (SELECT p.proname, pg_get_function_identity_arguments(p.oid) AS args FROM pg_proc p
              WHERE p.pronamespace = 'public'::regnamespace AND p.prokind IN ('f', 'p')
                AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')) LOOP
        EXECUTE format('DROP ROUTINE IF EXISTS public.%I(%s) CASCADE', r.proname, r.args);
    END LOOP;
    FOR r IN (SELECT typname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'public' AND t.typtype IN ('e', 'c')) LOOP
        EXECUTE 'DROP TYPE IF EXISTS public.' || quote_ident(r.typname) || ' CASCADE';
    END LOOP;
END $$;
SQL

echo "==> 5. Restoring converted objects into '${DB_NAME}'.public as '${APP_USER}'..."
{
  echo "SET ROLE ${APP_USER};"
  pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${STAGING_DB}" --schema=public --no-owner --no-privileges \
    | sed -e '/^CREATE SCHEMA public;/d' -e '/^COMMENT ON SCHEMA public/d'
} | psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" -v ON_ERROR_STOP=1

echo "==> 6. Verifying ownership of objects in 'public' and 'dev' belongs to '${APP_USER}'..."
psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" -v ON_ERROR_STOP=1 <<SQL
DO \$\$
DECLARE
    r RECORD;
BEGIN
    FOR r IN (SELECT schemaname, viewname FROM pg_views WHERE schemaname IN ('public', 'dev') AND viewowner <> '${APP_USER}') LOOP
        EXECUTE format('ALTER VIEW %I.%I OWNER TO %I', r.schemaname, r.viewname, '${APP_USER}');
    END LOOP;
    FOR r IN (SELECT schemaname, tablename FROM pg_tables WHERE schemaname IN ('public', 'dev') AND tableowner <> '${APP_USER}') LOOP
        EXECUTE format('ALTER TABLE %I.%I OWNER TO %I', r.schemaname, r.tablename, '${APP_USER}');
    END LOOP;
    FOR r IN (SELECT sequence_schema, sequence_name FROM information_schema.sequences WHERE sequence_schema IN ('public', 'dev')) LOOP
        EXECUTE format('ALTER SEQUENCE %I.%I OWNER TO %I', r.sequence_schema, r.sequence_name, '${APP_USER}');
    END LOOP;
END \$\$;
SQL

echo "==> Done. Current tables in 'public':"
psql -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" -c "\dt public.*"
