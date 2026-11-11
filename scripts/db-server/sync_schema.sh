#!/usr/bin/env bash
# Operator-only script for the Postgres server (root@postgres-db).
# Coding agents: NEVER run this script against the real server
# (scripts/test-db-server.sh runs it against a throwaway local Postgres).
#
# Usage: sync_schema.sh SOURCE TARGET
#   Replaces schema TARGET of the Pitbox database with a copy of SOURCE.
#   sync_stock_items_public_to_dev.sh and sync_stock_items_dev_to_public.sh
#   call it.
#
# How it keeps the data safe:
#   - It checks first and changes nothing until you type OVERWRITE: valid
#     schema names, a SOURCE with tables and a migration version, and never
#     an older migration version over public.
#   - It backs TARGET up (pg_dump -Fc into SYNC_BACKUP_DIR) before touching it.
#   - Clearing TARGET and restoring the copy run in ONE transaction, from a
#     file written completely beforehand. It commits only if the copy matches
#     SOURCE exactly (objects, columns, constraints, indexes, functions,
#     sequences, and every row); any error or mismatch rolls it all back and
#     TARGET stays as it was.
#   - It never waits long for a lock the running app holds (lock_timeout),
#     and two runs can't overlap (flock).
#
# Settings (environment, for tests; the defaults are the server's):
#   SYNC_DB, SYNC_PG_HOST, SYNC_PG_PORT, SYNC_PG_USER, SYNC_APP_USER,
#   SYNC_BACKUP_DIR, SYNC_LOCK_TIMEOUT, SYNC_LOCK_FILE
set -euo pipefail

SOURCE="${1:-}"
TARGET="${2:-}"
DB_NAME="${SYNC_DB:-stock_items}"
PG_HOST="${SYNC_PG_HOST:-localhost}"
PG_PORT="${SYNC_PG_PORT:-5432}"
PG_USER="${SYNC_PG_USER:-postgres}"
APP_USER="${SYNC_APP_USER:-stock}"
BACKUP_DIR="${SYNC_BACKUP_DIR:-${HOME}/pitbox-sync-backups}"
LOCK_TIMEOUT="${SYNC_LOCK_TIMEOUT:-10s}"
LOCK_FILE="${SYNC_LOCK_FILE:-/tmp/pitbox-schema-sync.lock}"
export PGPASSFILE="${PGPASSFILE:-${HOME}/.pgpass}"
STAGING_DB="sync_staging_$$"

die() { echo "ERROR: $*" >&2; exit 1; }

valid_name() { [[ "$1" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] && [[ "$1" != pg_* ]] && [[ "$1" != information_schema ]]; }
[ -n "${SOURCE}" ] && [ -n "${TARGET}" ] || die "usage: $0 SOURCE TARGET"
valid_name "${SOURCE}" || die "'${SOURCE}' is not a schema this script copies from"
valid_name "${TARGET}" || die "'${TARGET}' is not a schema this script copies to"
[ "${SOURCE}" != "${TARGET}" ] || die "SOURCE and TARGET are the same schema"
for tool in psql pg_dump pg_restore flock; do
  command -v "${tool}" >/dev/null || die "${tool} is not installed"
done

# One run at a time.
exec 9>"${LOCK_FILE}"
flock -n 9 || die "another schema sync is running (${LOCK_FILE})"

q() { psql -X -q -v ON_ERROR_STOP=1 -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" "$@"; }

WORK="$(mktemp -d)"
chmod 700 "${WORK}"
cleanup() {
  q -d postgres -c "DROP DATABASE IF EXISTS ${STAGING_DB};" >/dev/null 2>&1 || true
  rm -rf "${WORK}"
}
trap cleanup EXIT

# summary SCHEMA prints "exists tables rows goose_version" (goose_version is
# "none" without a goose_db_version table).
summary() {
  q -d "$1" -At -F ' ' -v s="$2" <<'SQL'
SELECT (to_regnamespace(:'s') IS NOT NULL)::int,
       (SELECT count(*) FROM pg_class c WHERE c.relnamespace = to_regnamespace(:'s') AND c.relkind IN ('r', 'p')),
       (SELECT COALESCE(sum((xpath('/row/n/text()', query_to_xml(format('SELECT count(*) AS n FROM %I.%I', :'s', c.relname), false, true, '')))[1]::text::bigint), 0)
        FROM pg_class c WHERE c.relnamespace = to_regnamespace(:'s') AND c.relkind IN ('r', 'p')),
       CASE WHEN to_regclass(format('%I.goose_db_version', :'s')) IS NULL THEN 'none'
            ELSE COALESCE((xpath('/row/v/text()', query_to_xml(format('SELECT max(version_id) AS v FROM %I.goose_db_version WHERE is_applied', :'s'), false, true, '')))[1]::text, 'none') END;
SQL
}

# The manifest of a schema: a hash of everything in it (objects, columns in
# order, defaults, constraints, indexes, function bodies, sequence values,
# and every row of every table), then how many entries it covers. Run on the
# staging copy and on the restored TARGET, it must come out the same.
MANIFEST_SQL=$(cat <<'SQL'
SET search_path = pg_catalog;
SELECT md5(string_agg(x, E'\n' ORDER BY x)) || ' ' || count(*) AS manifest FROM (
  SELECT format('rel %s %s', c.relkind, c.relname) AS x FROM pg_class c WHERE c.relnamespace = to_regnamespace(:'s')
  UNION ALL
  SELECT format('col %s %s %s %s %s %s', c.relname, row_number() OVER (PARTITION BY c.oid ORDER BY a.attnum), a.attname,
                format_type(a.atttypid, a.atttypmod), a.attnotnull, a.attidentity)
  FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
  WHERE c.relnamespace = to_regnamespace(:'s') AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'c') AND a.attnum > 0 AND NOT a.attisdropped
  UNION ALL
  SELECT format('def %s %s %s', c.relname, a.attname, pg_get_expr(d.adbin, d.adrelid))
  FROM pg_attrdef d JOIN pg_class c ON c.oid = d.adrelid JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
  WHERE c.relnamespace = to_regnamespace(:'s')
  UNION ALL
  SELECT format('con %s %s %s %s', c.relname, k.conname, k.contype, pg_get_constraintdef(k.oid))
  FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid WHERE c.relnamespace = to_regnamespace(:'s')
  UNION ALL
  SELECT format('idx %s', pg_get_indexdef(i.indexrelid))
  FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid WHERE c.relnamespace = to_regnamespace(:'s')
  UNION ALL
  SELECT format('fn %s(%s) %s %s', p.proname, pg_get_function_identity_arguments(p.oid), p.prokind,
                CASE WHEN p.prokind IN ('f', 'p') THEN md5(pg_get_functiondef(p.oid)) END)
  FROM pg_proc p WHERE p.pronamespace = to_regnamespace(:'s')
  UNION ALL
  SELECT format('type %s %s', t.typname, t.typtype) FROM pg_type t WHERE t.typnamespace = to_regnamespace(:'s')
  UNION ALL
  SELECT format('seq %s %s', s.sequencename, s.last_value) FROM pg_sequences s WHERE s.schemaname = :'s'
  UNION ALL
  SELECT format('rows %s %s %s', c.relname, (xpath('/row/n/text()', r))[1]::text, (xpath('/row/h/text()', r))[1]::text)
  FROM pg_class c,
       LATERAL query_to_xml(format('SELECT count(*) AS n, md5(COALESCE(string_agg(md5(t::text), '''' ORDER BY md5(t::text)), '''')) AS h FROM %I.%I t',
                                   :'s', c.relname), false, true, '') r
  WHERE c.relnamespace = to_regnamespace(:'s') AND c.relkind IN ('r', 'p')
) m
SQL
)

echo "==> Checking ${DB_NAME}.${SOURCE} and ${DB_NAME}.${TARGET}..."
read -r SRC_EXISTS SRC_TABLES SRC_ROWS SRC_GOOSE <<<"$(summary "${DB_NAME}" "${SOURCE}")"
read -r TGT_EXISTS TGT_TABLES TGT_ROWS TGT_GOOSE <<<"$(summary "${DB_NAME}" "${TARGET}")"
[ "${SRC_EXISTS}" = 1 ] || die "schema ${SOURCE} does not exist"
[ "${SRC_TABLES}" -gt 0 ] || die "schema ${SOURCE} has no tables: copying it would leave ${TARGET} empty"
[ "${SRC_GOOSE}" != none ] || die "schema ${SOURCE} has no applied migrations (goose_db_version): not a Pitbox schema"
OLDER=""
if [ "${TGT_GOOSE}" != none ] && [ "${TGT_GOOSE}" -gt "${SRC_GOOSE}" ]; then
  [ "${TARGET}" != public ] || die "public is at migration ${TGT_GOOSE}, ${SOURCE} only at ${SRC_GOOSE}: refusing to take the real data back to an older schema"
  OLDER=" - NOTE: ${TARGET} is at migration ${TGT_GOOSE}, newer than ${SOURCE} (${SRC_GOOSE}); it goes back to ${SRC_GOOSE}."
fi

BACKUP=""
if [ "${TGT_EXISTS}" = 1 ]; then
  BACKUP="${BACKUP_DIR}/${DB_NAME}-${TARGET}-$(date +%Y%m%d-%H%M%S).dump"
fi

cat <<EOF
======================================================================
                     ⚠️   CRITICAL ACTION WARNING   ⚠️
======================================================================
 DATABASE : ${DB_NAME} on ${PG_HOST}:${PG_PORT}
 SOURCE   : ${SOURCE}  (${SRC_TABLES} tables, ${SRC_ROWS} rows, migration ${SRC_GOOSE}; not changed)
 TARGET   : ${TARGET}  (${TGT_TABLES} tables, ${TGT_ROWS} rows, migration ${TGT_GOOSE}; WILL BE REPLACED)

 WHAT WILL HAPPEN:
 - ${TARGET} is backed up first${BACKUP:+ to ${BACKUP}}.
 - Then, in one transaction, everything in ${TARGET} (tables, views,
   sequences, functions, types and data) is dropped and replaced with a
   copy of ${SOURCE}, owned by '${APP_USER}'. It commits only if the copy
   matches ${SOURCE} exactly; otherwise nothing changes.
 - Objects in ${SOURCE} or ${TARGET} not owned by '${APP_USER}' are
   given to '${APP_USER}', so the app can run its migrations.
 - Stop the Pitbox app that uses ${TARGET} first (for public: the
   Portainer stack) and start it again afterwards; if it holds a lock
   longer than ${LOCK_TIMEOUT}, the sync stops and changes nothing.
${OLDER}
======================================================================
EOF

read -rp "Type 'OVERWRITE' to continue (any other input aborts): " CONFIRM
if [ "${CONFIRM}" != "OVERWRITE" ]; then
  echo "==> Aborted. No changes were made to '${DB_NAME}'."
  exit 0
fi

if [ -n "${BACKUP}" ]; then
  echo "==> 1. Backing up ${TARGET} to ${BACKUP}..."
  mkdir -p "${BACKUP_DIR}"
  chmod 700 "${BACKUP_DIR}"
  pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" --schema="${TARGET}" -Fc -f "${BACKUP}"
  pg_restore -l "${BACKUP}" >/dev/null || die "the backup ${BACKUP} can't be read; nothing was changed"
else
  echo "==> 1. ${TARGET} doesn't exist yet: nothing to back up."
fi

echo "==> 2. Copying ${SOURCE} into a staging database as ${TARGET}..."
q -d postgres -c "CREATE DATABASE ${STAGING_DB};" >/dev/null
# The staging database's own empty public schema makes way for the copy.
q -d "${STAGING_DB}" -c "DROP SCHEMA IF EXISTS public CASCADE;" >/dev/null
pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" --schema="${SOURCE}" -f "${WORK}/source.sql"
q -d "${STAGING_DB}" -f "${WORK}/source.sql" >/dev/null
q -d "${STAGING_DB}" -c "ALTER SCHEMA ${SOURCE} RENAME TO ${TARGET};" >/dev/null
EXPECTED="$(printf '%s\n' "${MANIFEST_SQL}" | q -d "${STAGING_DB}" -At -v s="${TARGET}")"
[ -n "${EXPECTED}" ] || die "could not take the manifest of the copy; nothing was changed"

echo "==> 3. Writing the transaction..."
pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${STAGING_DB}" --schema="${TARGET}" --no-owner --no-privileges \
  -f "${WORK}/copy.sql"
{
  cat <<'SQL'
-- Clear TARGET, keeping the schema itself (and its grants).
SET lock_timeout = :'lock_timeout';
CREATE SCHEMA IF NOT EXISTS :"s";
ALTER SCHEMA :"s" OWNER TO :"app_user";
SELECT set_config('sync.target', :'s', true) \g /dev/null
DO $$
DECLARE
    s text := current_setting('sync.target');
    r record;
    leftover text;
BEGIN
    FOR r IN SELECT c.relname, c.relkind FROM pg_class c
             WHERE c.relnamespace = to_regnamespace(s) AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
               AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
    LOOP
        EXECUTE format('DROP %s IF EXISTS %I.%I CASCADE',
            CASE r.relkind WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' WHEN 'f' THEN 'FOREIGN TABLE' ELSE 'TABLE' END, s, r.relname);
    END LOOP;
    FOR r IN SELECT c.relname FROM pg_class c
             WHERE c.relnamespace = to_regnamespace(s) AND c.relkind = 'S'
               AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
    LOOP
        EXECUTE format('DROP SEQUENCE IF EXISTS %I.%I CASCADE', s, r.relname);
    END LOOP;
    FOR r IN SELECT p.proname, p.prokind, pg_get_function_identity_arguments(p.oid) AS args FROM pg_proc p
             WHERE p.pronamespace = to_regnamespace(s) AND p.prokind IN ('f', 'p', 'a')
               AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
    LOOP
        EXECUTE format('DROP %s IF EXISTS %I.%I(%s) CASCADE',
            CASE r.prokind WHEN 'a' THEN 'AGGREGATE' ELSE 'ROUTINE' END, s, r.proname, r.args);
    END LOOP;
    FOR r IN SELECT t.typname, t.typtype FROM pg_type t
             WHERE t.typnamespace = to_regnamespace(s) AND t.typtype IN ('c', 'd', 'e', 'r')
               AND (t.typtype <> 'c' OR (SELECT relkind FROM pg_class WHERE oid = t.typrelid) = 'c')
               AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_type'::regclass AND d.objid = t.oid AND d.deptype = 'e')
    LOOP
        EXECUTE format('DROP %s IF EXISTS %I.%I CASCADE', CASE r.typtype WHEN 'd' THEN 'DOMAIN' ELSE 'TYPE' END, s, r.typname);
    END LOOP;
    -- Anything still there would collide with the copy or stay behind
    -- unnoticed: stop (rolling back) and say what it is.
    SELECT string_agg(o, ', ' ORDER BY o) INTO leftover FROM (
        SELECT format('%s (pg_class %s)', relname, relkind) AS o, oid, 'pg_class'::regclass AS cls FROM pg_class WHERE relnamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (function)', proname), oid, 'pg_proc'::regclass FROM pg_proc WHERE pronamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (type)', typname), oid, 'pg_type'::regclass FROM pg_type WHERE typnamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (collation)', collname), oid, 'pg_collation'::regclass FROM pg_collation WHERE collnamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (operator)', oprname), oid, 'pg_operator'::regclass FROM pg_operator WHERE oprnamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (conversion)', conname), oid, 'pg_conversion'::regclass FROM pg_conversion WHERE connamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (text search configuration)', cfgname), oid, 'pg_ts_config'::regclass FROM pg_ts_config WHERE cfgnamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (text search dictionary)', dictname), oid, 'pg_ts_dict'::regclass FROM pg_ts_dict WHERE dictnamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (operator family)', opfname), oid, 'pg_opfamily'::regclass FROM pg_opfamily WHERE opfnamespace = to_regnamespace(s)
        UNION ALL SELECT format('%s (statistics)', stxname), oid, 'pg_statistic_ext'::regclass FROM pg_statistic_ext WHERE stxnamespace = to_regnamespace(s)
    ) left_over
    WHERE NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = left_over.cls AND d.objid = left_over.oid AND d.deptype = 'e');
    IF leftover IS NOT NULL THEN
        RAISE EXCEPTION 'Clearing % left objects this script does not drop: %. Rolled back: nothing was changed.', s, leftover;
    END IF;
END $$;

-- Restore the copy as the app user, so it owns everything.
SET ROLE :"app_user";
SQL
  sed -E -e "/^CREATE SCHEMA ${TARGET};\$/d" -e "/^COMMENT ON SCHEMA ${TARGET} IS /d" "${WORK}/copy.sql"
  cat <<'SQL'

RESET ROLE;
-- Whatever isn't the app user's yet becomes so (in both schemas), as the
-- app runs migrations on them.
SELECT set_config('sync.schemas', :'source' || ',' || :'s', true) \g /dev/null
SELECT set_config('sync.app_user', :'app_user', true) \g /dev/null
DO $$
DECLARE
    app text := current_setting('sync.app_user');
    r record;
BEGIN
    FOR r IN SELECT n.nspname, c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE n.nspname = ANY (string_to_array(current_setting('sync.schemas'), ','))
               AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S') AND pg_get_userbyid(c.relowner) <> app
               AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('a', 'i', 'e'))
             ORDER BY c.relkind = 'S'
    LOOP
        EXECUTE format('ALTER %s %I.%I OWNER TO %I',
            CASE r.relkind WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' WHEN 'f' THEN 'FOREIGN TABLE' WHEN 'S' THEN 'SEQUENCE' ELSE 'TABLE' END,
            r.nspname, r.relname, app);
    END LOOP;
    FOR r IN SELECT n.nspname, p.proname, p.prokind, pg_get_function_identity_arguments(p.oid) AS args
             FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
             WHERE n.nspname = ANY (string_to_array(current_setting('sync.schemas'), ','))
               AND p.prokind IN ('f', 'p', 'a') AND pg_get_userbyid(p.proowner) <> app
               AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
    LOOP
        EXECUTE format('ALTER %s %I.%I(%s) OWNER TO %I',
            CASE r.prokind WHEN 'a' THEN 'AGGREGATE' ELSE 'ROUTINE' END, r.nspname, r.proname, r.args, app);
    END LOOP;
END $$;

-- The copy must match the source exactly, or everything rolls back.
SQL
  printf '%s \\gset sync_\n' "${MANIFEST_SQL}"
  cat <<'SQL'
SELECT set_config('sync.got', :'sync_manifest', true), set_config('sync.expected', :'expected', true) \g /dev/null
DO $$
BEGIN
    IF current_setting('sync.got') IS DISTINCT FROM current_setting('sync.expected') THEN
        RAISE EXCEPTION 'The restored % does not match the source (manifest %, expected %). Rolled back: nothing was changed.',
            current_setting('sync.target'), current_setting('sync.got'), current_setting('sync.expected');
    END IF;
END $$;
SQL
} >"${WORK}/transaction.sql"

echo "==> 4. Replacing ${TARGET} in one transaction..."
if ! q -d "${DB_NAME}" --single-transaction -v s="${TARGET}" -v source="${SOURCE}" -v app_user="${APP_USER}" \
     -v lock_timeout="${LOCK_TIMEOUT}" -v expected="${EXPECTED}" -f "${WORK}/transaction.sql" >"${WORK}/transaction.log" 2>&1; then
  grep -E 'ERROR|FATAL' "${WORK}/transaction.log" >&2 || tail -20 "${WORK}/transaction.log" >&2
  die "the transaction was rolled back: ${TARGET} is unchanged${BACKUP:+ (backup: ${BACKUP})}"
fi

read -r _ NEW_TABLES NEW_ROWS NEW_GOOSE <<<"$(summary "${DB_NAME}" "${TARGET}")"
echo "==> Done. ${TARGET} is now a copy of ${SOURCE}: ${NEW_TABLES} tables, ${NEW_ROWS} rows, migration ${NEW_GOOSE}; verified against the source."
[ -z "${BACKUP}" ] || echo "    The old ${TARGET} is in ${BACKUP} (pg_restore it to go back)."
echo "    Start the Pitbox app that uses ${TARGET} again."
