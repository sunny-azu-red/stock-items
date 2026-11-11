#!/usr/bin/env bash
# Operator-only script for the Postgres server (root@postgres-db).
# Coding agents: NEVER run this script against the real server
# (scripts/test-db-server.sh runs it against a throwaway local Postgres).
#
# Usage: sync_schema.sh SOURCE TARGET
#        sync_schema.sh --from-backup FILE TARGET
#   Replaces schema TARGET of the Pitbox database with a copy of schema
#   SOURCE, or with a backup this script made of TARGET earlier.
#   sync_stock_items_public_to_dev.sh and sync_stock_items_dev_to_public.sh
#   call it. Run it inside tmux or screen.
#
#   Needs the superuser (postgres) and the app user (stock) in ~/.pgpass:
#     localhost:5432:*:postgres:<password>
#     localhost:5432:*:stock:<the app's password>
#
# How it keeps the data safe:
#   - No code from the database runs in a superuser session. The schemas
#     belong to the app user, whose functions run in CHECK constraints,
#     index expressions and the like whenever data is restored: everything
#     that restores data logs in as the app user itself. The superuser only
#     reads catalogs and data (search_path pinned), runs pg_dump and makes
#     the staging database.
#   - SOURCE is only read. It is copied into a staging database first, and
#     the checks and the summary you confirm describe that copy, which is
#     exactly what goes into TARGET.
#   - Before asking, it refuses (changing nothing) when a check can't be
#     made, the copy is empty or has no migrations, TARGET holds objects not
#     owned by the app user, objects in other schemas depend on TARGET
#     (DROP ... CASCADE would destroy them), TARGET's tables are published,
#     or the disks are too full; for TARGET public also while the app user
#     is connected, and when the migrations differ (from a backup: when it
#     has any public lacks). You type OVERWRITE (OVERWRITE public).
#   - TARGET is backed up (pg_dump -Fc, read back completely) and
#     fingerprinted (the manifest below) before it is touched.
#   - Clearing TARGET and restoring the copy run in ONE transaction, written
#     to a file beforehand. It locks TARGET's tables, repeats the checks,
#     checks TARGET still matches the fingerprint (nothing was written since
#     the backup), and commits only if the result matches the staging copy
#     exactly, the app user owns everything and no grant or comment TARGET
#     had is lost. Any error rolls it all back.
#   - A lock held longer than SYNC_LOCK_TIMEOUT stops it. Ctrl-C, a kill or
#     a hang-up stop psql and its server session (psql also dies with this
#     script), two runs can't overlap, and the end reports from the
#     transaction's own status whether it committed.
#
# Settings (environment; the defaults are the server's, tests change them):
#   SYNC_DB, SYNC_PG_HOST, SYNC_PG_PORT, SYNC_PG_USER, SYNC_APP_USER,
#   SYNC_BACKUP_DIR, SYNC_LOCK_TIMEOUT, SYNC_SKIP_DATA_DIR_CHECK (tests only:
#   the data directory isn't on this machine)
set -euo pipefail
umask 077
trap '' PIPE

DB_NAME="${SYNC_DB:-stock_items}"
PG_HOST="${SYNC_PG_HOST:-localhost}"
PG_PORT="${SYNC_PG_PORT:-5432}"
PG_USER="${SYNC_PG_USER:-postgres}"
APP_USER="${SYNC_APP_USER:-stock}"
BACKUP_DIR="${SYNC_BACKUP_DIR:-${HOME}/pitbox-sync-backups}"
LOCK_TIMEOUT="${SYNC_LOCK_TIMEOUT:-10s}"
STAMP="$(date +%Y%m%d-%H%M%S)"
STAGING_DB="pitbox_sync_staging_$$"
APP_NAME="pitbox-schema-sync" # our sessions' application_name

# Only these settings decide where we connect and how sessions behave: a
# PGHOSTADDR or PGSERVICE in root's environment must not send us elsewhere.
unset PGHOST PGHOSTADDR PGPORT PGUSER PGDATABASE PGSERVICE PGSERVICEFILE PGOPTIONS PGTZ PGDATESTYLE \
      PGCLIENTENCODING PGTARGETSESSIONATTRS PGREQUIREAUTH PSQLRC
export PGPASSFILE="${PGPASSFILE:-${HOME}/.pgpass}"
export PGAPPNAME="${APP_NAME}"
export PGOPTIONS="-c lock_timeout=${LOCK_TIMEOUT}"

die() { echo "ERROR: $*" >&2; exit 1; }

FROM_BACKUP=""
if [ "${1:-}" = --from-backup ]; then
  FROM_BACKUP="${2:-}"
  SOURCE="backup"
  TARGET="${3:-}"
  [ -n "${FROM_BACKUP}" ] && [ -n "${TARGET}" ] || die "usage: $0 --from-backup FILE TARGET"
  [ -f "${FROM_BACKUP}" ] && [ -r "${FROM_BACKUP}" ] || die "can't read ${FROM_BACKUP}"
else
  SOURCE="${1:-}"
  TARGET="${2:-}"
  [ -n "${SOURCE}" ] && [ -n "${TARGET}" ] || die "usage: $0 SOURCE TARGET   or   $0 --from-backup FILE TARGET"
fi
valid_name() { [[ "$1" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] && [[ "$1" != pg_* ]] && [[ "$1" != information_schema ]]; }
[ -n "${FROM_BACKUP}" ] || valid_name "${SOURCE}" || die "'${SOURCE}' is not a schema this script copies from"
valid_name "${TARGET}" || die "'${TARGET}' is not a schema this script copies to"
[ "${SOURCE}" != "${TARGET}" ] || die "SOURCE and TARGET are the same schema"
[[ "${DB_NAME}" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || die "SYNC_DB '${DB_NAME}' is not a plain database name"
[[ "${APP_USER}" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || die "SYNC_APP_USER '${APP_USER}' is not a plain role name"
[[ "${LOCK_TIMEOUT}" =~ ^[1-9][0-9]*(ms|s|min)?$ ]] || die "SYNC_LOCK_TIMEOUT '${LOCK_TIMEOUT}' is not a duration like 10s (0 would mean no limit)"
for tool in psql pg_dump pg_restore flock df stat awk tee setpriv comm; do
  command -v "${tool}" >/dev/null || die "${tool} is not installed"
done

# One run at a time (this also makes the leftover clean-up below safe).
mkdir -p "${BACKUP_DIR}"
exec 9>"${BACKUP_DIR}/.pitbox-schema-sync.lock" || die "can't open the lock file in ${BACKUP_DIR}"
flock -n 9 || die "another schema sync is running (lock in ${BACKUP_DIR})"

# Everything also goes to a log. tee ignores Ctrl-C and hang-ups, and keeps
# writing the log when the terminal is gone, so the end is always logged.
LOG="${BACKUP_DIR}/pitbox-sync-${STAMP}-${SOURCE}-to-${TARGET}.log"
exec > >(trap '' INT HUP TERM; exec tee -i --output-error=warn -a "${LOG}") 2>&1
TEE_PID=$!
echo "==> $(date -Is) sync ${FROM_BACKUP:-${SOURCE}} -> ${TARGET} (log: ${LOG})"

admin() { psql -X -q -w -v ON_ERROR_STOP=1 -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" "$@"; }
app() { psql -X -q -w -v ON_ERROR_STOP=1 -h "${PG_HOST}" -p "${PG_PORT}" -U "${APP_USER}" "$@"; }

# Leftovers of a run that was killed outright: its work files (copies of
# the data), unfinished backups and staging database.
rm -rf "${BACKUP_DIR}"/pitbox-sync-work.* "${BACKUP_DIR}/${DB_NAME}"-*.dump.partial
WORK="$(mktemp -d -p "${BACKUP_DIR}" pitbox-sync-work.XXXXXX)"
BACKUP=""
PSQL_PID=""
stop_transaction() {
  # Stop first, talk afterwards.
  if [ -n "${PSQL_PID}" ] && kill -0 "${PSQL_PID}" 2>/dev/null; then
    kill -TERM "${PSQL_PID}" 2>/dev/null || true
    echo "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = :'d' AND application_name = :'a' AND pid <> pg_backend_pid();" |
      admin -d postgres -At -v d="${DB_NAME}" -v a="${APP_NAME}" >/dev/null 2>&1 || true
    echo "==> Interrupted: stopped psql and its session (the transaction rolls back)." >&2 || true
  fi
}
cleanup() {
  local rc=$?
  stop_transaction
  if ! admin -d postgres -c "DROP DATABASE IF EXISTS ${STAGING_DB} WITH (FORCE);" >/dev/null 2>&1; then
    echo "WARNING: could not drop the staging database ${STAGING_DB} (a copy of the data); drop it by hand." >&2 || true
  fi
  rm -rf "${WORK}"
  [ -z "${BACKUP}" ] || rm -f "${BACKUP}.partial"
  # Let tee write the last lines.
  exec 1>&- 2>&-
  wait "${TEE_PID}" 2>/dev/null || true
  exit "${rc}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

for old in $(admin -d postgres -At -c "SELECT datname FROM pg_database WHERE datname ~ '^pitbox_sync_staging_[0-9]+$'"); do
  echo "==> Dropping ${old}, the staging database of an earlier run that was stopped."
  admin -d postgres -c "DROP DATABASE ${old} WITH (FORCE);" >/dev/null
done

# Both logins must work without a password prompt.
admin -d "${DB_NAME}" -At -c "SELECT 1" >/dev/null || die "can't log in as ${PG_USER} to ${DB_NAME} (~/.pgpass)"
app -d "${DB_NAME}" -At -c "SELECT 1" >/dev/null ||
  die "can't log in as ${APP_USER} to ${DB_NAME}: add 'localhost:5432:*:${APP_USER}:<the app's password>' to ~/.pgpass (the copy is restored as ${APP_USER}, never as the superuser)"
[ "$(admin -d "${DB_NAME}" -At -v a="${APP_USER}" <<<"SELECT rolsuper FROM pg_roles WHERE rolname = :'a';")" = f ] ||
  die "${APP_USER} is a superuser (or missing): refusing, the restore must run without superuser rights"

# info DATABASE SCHEMA prints "exists tables rows goose_max goose_applied
# latest" (applied versions comma-separated; latest is the newest
# created_at/updated_at; "none" for none). Read-only, as the superuser,
# with only pg_catalog on the search path (no function of the schema runs).
info() {
  local out
  out="$(admin -d "$1" -At -F ' ' -v info_schema="$2" <<'SQL'
SET search_path = pg_catalog;
SET statement_timeout = '300s';
WITH ns AS (SELECT pg_catalog.to_regnamespace(:'info_schema') AS oid),
tables AS (SELECT c.relname FROM pg_catalog.pg_class c, ns WHERE c.relnamespace = ns.oid AND c.relkind IN ('r', 'p')),
goose AS (
    SELECT (pg_catalog.xpath('/row/v/text()', pg_catalog.query_to_xml(pg_catalog.format(
        'SELECT pg_catalog.string_agg(version_id::pg_catalog.text, '','' ORDER BY version_id) AS v FROM %I.goose_db_version WHERE is_applied',
        :'info_schema'), false, true, '')))[1]::pg_catalog.text AS applied
    WHERE pg_catalog.to_regclass(pg_catalog.format('%I.goose_db_version', :'info_schema')) IS NOT NULL
),
stamps AS (
    SELECT (pg_catalog.xpath('/row/t/text()', pg_catalog.query_to_xml(pg_catalog.format(
        'SELECT pg_catalog.max(%I) AS t FROM %I.%I', a.attname, :'info_schema', c.relname), false, true, '')))[1]::pg_catalog.text::pg_catalog.timestamptz AS t
    FROM pg_catalog.pg_class c JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid, ns
    WHERE c.relnamespace = ns.oid AND c.relkind IN ('r', 'p') AND a.attname IN ('created_at', 'updated_at') AND NOT a.attisdropped
      AND a.atttypid = 'pg_catalog.timestamptz'::pg_catalog.regtype
)
SELECT (SELECT oid IS NOT NULL FROM ns)::pg_catalog.int4,
       (SELECT pg_catalog.count(*) FROM tables),
       (SELECT COALESCE(pg_catalog.sum((pg_catalog.xpath('/row/n/text()', pg_catalog.query_to_xml(pg_catalog.format(
            'SELECT pg_catalog.count(*) AS n FROM %I.%I', :'info_schema', relname), false, true, '')))[1]::pg_catalog.text::pg_catalog.int8), 0) FROM tables),
       COALESCE((SELECT pg_catalog.split_part(applied, ',', -1) FROM goose WHERE applied IS NOT NULL), 'none'),
       COALESCE((SELECT applied FROM goose WHERE applied IS NOT NULL), 'none'),
       COALESCE((SELECT pg_catalog.to_char(pg_catalog.max(t) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI"Z"') FROM stamps), 'none');
SQL
)" || die "could not read schema $2 (see above); nothing was changed"
  [[ "${out}" =~ ^[01]\ [0-9]+\ [0-9]+\ ([0-9]+|none)\ ([0-9,]+|none)\ ([0-9T:Z-]+|none)$ ]] ||
    die "unexpected answer reading schema $2: '${out}'; nothing was changed"
  echo "${out}"
}

# The manifest of a schema (psql variable pitbox_sync_schema): a hash of
# everything in it (objects, columns in order with their collations,
# defaults, constraints, indexes, views, triggers, rules, policies,
# function bodies, sequence values, and every row of every table), then
# how many entries it covers. Settings that change how values print are
# pinned, and sorting is bytewise, so the server's or database's defaults
# can't make two equal schemas differ. Extension members are left out.
# It only reads catalogs and tables (no function of the schema runs).
MANIFEST_SQL=$(cat <<'SQL'
SET search_path = pg_catalog;
SET TimeZone = 'UTC';
SET DateStyle = 'ISO, YMD';
SET IntervalStyle = 'postgres';
SET extra_float_digits = 1;
SET bytea_output = 'hex';
SELECT COALESCE(md5(string_agg(x, E'\n' ORDER BY x COLLATE "C")), 'none') || ' ' || count(x) AS manifest FROM (
  SELECT format('rel %s %s %s', c.relkind, c.relname, c.relrowsecurity) AS x FROM pg_class c
  WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema')
    AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
  UNION ALL
  SELECT format('col %s %s %s %s %s %s %s', c.relname, row_number() OVER (PARTITION BY c.oid ORDER BY a.attnum), a.attname,
                format_type(a.atttypid, a.atttypmod), a.attnotnull, a.attidentity, a.attcollation::regcollation)
  FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
  WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema') AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'c') AND a.attnum > 0 AND NOT a.attisdropped
  UNION ALL
  SELECT format('def %s %s %s', c.relname, a.attname, pg_get_expr(d.adbin, d.adrelid))
  FROM pg_attrdef d JOIN pg_class c ON c.oid = d.adrelid JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
  WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema')
  UNION ALL
  SELECT format('con %s %s %s %s', c.relname, k.conname, k.contype, pg_get_constraintdef(k.oid))
  FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema')
  UNION ALL
  SELECT format('idx %s %s', pg_get_indexdef(i.indexrelid), i.indisvalid)
  FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema')
  UNION ALL
  SELECT format('view %s %s', c.relname, pg_get_viewdef(c.oid)) FROM pg_class c
  WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema') AND c.relkind IN ('v', 'm')
  UNION ALL
  SELECT format('trg %s', pg_get_triggerdef(t.oid))
  FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema') AND NOT t.tgisinternal
  UNION ALL
  SELECT format('rule %s', pg_get_ruledef(r.oid))
  FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema') AND r.rulename <> '_RETURN'
  UNION ALL
  SELECT format('pol %s %s %s %s %s', c.relname, p.polname, p.polcmd, pg_get_expr(p.polqual, p.polrelid), pg_get_expr(p.polwithcheck, p.polrelid))
  FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema')
  UNION ALL
  SELECT format('fn %s(%s) %s %s', p.proname, pg_get_function_identity_arguments(p.oid), p.prokind,
                CASE WHEN p.prokind IN ('f', 'p') THEN md5(pg_get_functiondef(p.oid)) END)
  FROM pg_proc p WHERE p.pronamespace = to_regnamespace(:'pitbox_sync_schema')
    AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
  UNION ALL
  SELECT format('type %s %s', t.typname, t.typtype) FROM pg_type t WHERE t.typnamespace = to_regnamespace(:'pitbox_sync_schema')
    AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_type'::regclass AND d.objid = t.oid AND d.deptype = 'e')
  UNION ALL
  SELECT format('seq %s %s %s', c.relname, (xpath('/row/l/text()', r))[1]::text, (xpath('/row/c/text()', r))[1]::text)
  FROM pg_class c,
       LATERAL query_to_xml(format('SELECT last_value AS l, is_called AS c FROM %I.%I', :'pitbox_sync_schema', c.relname), false, true, '') r
  WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema') AND c.relkind = 'S'
  UNION ALL
  SELECT format('rows %s %s %s', c.relname, (xpath('/row/n/text()', r))[1]::text, (xpath('/row/h/text()', r))[1]::text)
  FROM pg_class c,
       LATERAL query_to_xml(format('SELECT count(*) AS n, md5(COALESCE(string_agg(md5(t::text), '''' ORDER BY md5(t::text) COLLATE "C"), '''')) AS h FROM %I.%I t',
                                   :'pitbox_sync_schema', c.relname), false, true, '') r
  WHERE c.relnamespace = to_regnamespace(:'pitbox_sync_schema') AND c.relkind IN ('r', 'p')
    AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
) m
SQL
)
manifest() { # manifest DATABASE SCHEMA, read as the superuser
  local out
  out="$(printf '%s\n' "${MANIFEST_SQL}" | admin -d "$1" -At -v pitbox_sync_schema="$2")" || return 1
  [[ "${out}" =~ ^([0-9a-f]{32}|none)\ [0-9]+$ ]] || return 1
  echo "${out}"
}

# TARGET's grants and comments, one per line (in a DO block with ns set).
PRIVS_SQL=$(cat <<'SQL'
    SELECT COALESCE(string_agg(p, E'\n' ORDER BY p COLLATE "C"), '') FROM (
        SELECT format('grant %s %s %s', c.relname, a.grantee::regrole, a.privilege_type) AS p
        FROM pg_class c, aclexplode(c.relacl) a WHERE c.relnamespace = ns AND a.grantee <> c.relowner
        UNION ALL
        SELECT format('grant %s.%s %s %s', c.relname, att.attname, a.grantee::regrole, a.privilege_type)
        FROM pg_attribute att JOIN pg_class c ON c.oid = att.attrelid, aclexplode(att.attacl) a
        WHERE c.relnamespace = ns AND att.attacl IS NOT NULL AND a.grantee <> c.relowner
        UNION ALL
        SELECT format('grant %s(%s) %s %s', p.proname, pg_get_function_identity_arguments(p.oid), a.grantee::regrole, a.privilege_type)
        FROM pg_proc p, aclexplode(p.proacl) a WHERE p.pronamespace = ns AND a.grantee <> p.proowner
        UNION ALL
        SELECT format('grant type %s %s %s', t.typname, a.grantee::regrole, a.privilege_type)
        FROM pg_type t, aclexplode(t.typacl) a WHERE t.typnamespace = ns AND a.grantee <> t.typowner
        UNION ALL
        SELECT format('comment %s: %s', pg_describe_object(d.classoid, d.objoid, d.objsubid), d.description)
        FROM pg_description d
        WHERE (d.classoid = 'pg_class'::regclass AND d.objoid IN (SELECT oid FROM pg_class WHERE relnamespace = ns))
           OR (d.classoid = 'pg_proc'::regclass AND d.objoid IN (SELECT oid FROM pg_proc WHERE pronamespace = ns))
           OR (d.classoid = 'pg_type'::regclass AND d.objoid IN (SELECT oid FROM pg_type WHERE typnamespace = ns))
           OR (d.classoid = 'pg_constraint'::regclass AND d.objoid IN (SELECT oid FROM pg_constraint WHERE connamespace = ns))
    ) privs
SQL
)
# The refusal checks, as one DO block reading pitbox_sync.* settings: run
# before the prompt (read-only, as the superuser) and in the transaction
# (as the app user). It records TARGET's grants and comments in
# pitbox_sync.privs_before for the check after the restore.
CHECKS_SQL=$(cat <<SQL
DO \$\$
DECLARE
    s text := current_setting('pitbox_sync.target');
    app text := current_setting('pitbox_sync.app_user');
    ns oid := pg_catalog.to_regnamespace(s);
    problems text[] := '{}';
    list text;
BEGIN
    SET LOCAL search_path = pg_catalog;
    IF current_setting('pitbox_sync.no_app_sessions') = 'on' THEN
        SELECT string_agg(format('pid %s from %s (%s)', pid, COALESCE(client_addr::text, 'local socket'),
                                 COALESCE(NULLIF(application_name, ''), 'no name')), ', ' ORDER BY pid) INTO list
        FROM pg_stat_activity
        WHERE datname = current_database() AND usename = app AND pid <> pg_backend_pid()
          AND application_name IS DISTINCT FROM '${APP_NAME}';
        IF list IS NOT NULL THEN
            problems := problems || format('%s is connected to %s (%s): stop the Pitbox app first', app, current_database(), list);
        END IF;
    END IF;
    IF ns IS NULL THEN
        IF NOT has_database_privilege(app, current_database(), 'CREATE') THEN
            problems := problems || format('%s can''t create schema %s (GRANT CREATE ON DATABASE %I TO %I)', app, s, current_database(), app);
        END IF;
        PERFORM set_config('pitbox_sync.privs_before', '', false);
    ELSE
        IF NOT has_schema_privilege(app, ns, 'CREATE') OR NOT has_schema_privilege(app, ns, 'USAGE') THEN
            problems := problems || format('%s lacks CREATE or USAGE on schema %s (as the app migrates it, make it the owner: ALTER SCHEMA %I OWNER TO %I)', app, s, s, app);
        END IF;
        -- The restore runs as the app user, which can only drop its own.
        SELECT string_agg(o, ', ' ORDER BY o) INTO list FROM (
            SELECT format('%s (owned by %s)', c.relname, pg_get_userbyid(c.relowner)) AS o FROM pg_class c
            WHERE c.relnamespace = ns AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S') AND pg_get_userbyid(c.relowner) <> app
              AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('a', 'i', 'e'))
            UNION ALL
            SELECT format('%s() (owned by %s)', p.proname, pg_get_userbyid(p.proowner)) FROM pg_proc p
            WHERE p.pronamespace = ns AND pg_get_userbyid(p.proowner) <> app
              AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
            UNION ALL
            SELECT format('type %s (owned by %s)', t.typname, pg_get_userbyid(t.typowner)) FROM pg_type t
            WHERE t.typnamespace = ns AND t.typtype IN ('d', 'e', 'r') AND pg_get_userbyid(t.typowner) <> app
              AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_type'::regclass AND d.objid = t.oid AND d.deptype = 'e')
        ) o;
        IF list IS NOT NULL THEN
            problems := problems || format('%s holds objects %s doesn''t own: %s (ALTER ... OWNER TO %I first)', s, app, list, app);
        END IF;
        -- Objects in other schemas that DROP ... CASCADE would take along.
        SELECT string_agg(DISTINCT pg_describe_object(d.classid, d.objid, d.objsubid), ', ') INTO list
        FROM pg_depend d
        WHERE d.deptype IN ('n', 'a')
          AND ((d.refclassid = 'pg_class'::regclass AND d.refobjid IN (SELECT oid FROM pg_class WHERE relnamespace = ns))
            OR (d.refclassid = 'pg_type'::regclass AND d.refobjid IN (SELECT oid FROM pg_type WHERE typnamespace = ns))
            OR (d.refclassid = 'pg_proc'::regclass AND d.refobjid IN (SELECT oid FROM pg_proc WHERE pronamespace = ns))
            OR (d.refclassid = 'pg_constraint'::regclass AND d.refobjid IN (SELECT oid FROM pg_constraint WHERE connamespace = ns)))
          AND (CASE d.classid
                 WHEN 'pg_class'::regclass THEN (SELECT relnamespace FROM pg_class WHERE oid = d.objid)
                 WHEN 'pg_type'::regclass THEN (SELECT typnamespace FROM pg_type WHERE oid = d.objid)
                 WHEN 'pg_proc'::regclass THEN (SELECT pronamespace FROM pg_proc WHERE oid = d.objid)
                 WHEN 'pg_constraint'::regclass THEN (SELECT COALESCE(c.relnamespace, k.connamespace) FROM pg_constraint k
                                                      LEFT JOIN pg_class c ON c.oid = k.conrelid WHERE k.oid = d.objid)
                 WHEN 'pg_rewrite'::regclass THEN (SELECT c.relnamespace FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class WHERE r.oid = d.objid)
                 WHEN 'pg_trigger'::regclass THEN (SELECT c.relnamespace FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid WHERE t.oid = d.objid)
                 WHEN 'pg_attrdef'::regclass THEN (SELECT c.relnamespace FROM pg_attrdef a JOIN pg_class c ON c.oid = a.adrelid WHERE a.oid = d.objid)
                 WHEN 'pg_policy'::regclass THEN (SELECT c.relnamespace FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid WHERE p.oid = d.objid)
                 WHEN 'pg_statistic_ext'::regclass THEN (SELECT stxnamespace FROM pg_statistic_ext WHERE oid = d.objid)
               END) IS DISTINCT FROM ns;  -- anything else (casts, publications, ...) counts as outside
        IF list IS NOT NULL THEN
            problems := problems || format('objects outside %s depend on it and would be dropped with it: %s', s, list);
        END IF;
        SELECT string_agg(format('%s in publication %s', pr.prrelid::regclass, p.pubname), ', ') INTO list
        FROM pg_publication_rel pr JOIN pg_publication p ON p.oid = pr.prpubid
        WHERE pr.prrelid IN (SELECT oid FROM pg_class WHERE relnamespace = ns);
        IF list IS NOT NULL THEN
            problems := problems || format('tables of %s are published (the copy would leave the publication): %s', s, list);
        END IF;
        PERFORM set_config('pitbox_sync.privs_before', (${PRIVS_SQL}), false);
    END IF;
    IF cardinality(problems) > 0 THEN
        RAISE EXCEPTION 'Refusing to replace %: %. Nothing was changed.', s, array_to_string(problems, '; ');
    END IF;
END \$\$;
SQL
)
NO_APP_SESSIONS=off
[ "${TARGET}" != public ] || NO_APP_SESSIONS=on
run_checks() { # the refusal checks, read-only, as the superuser
  { printf '%s\n' "BEGIN READ ONLY;" \
      "SELECT set_config('pitbox_sync.target', :'t', false), set_config('pitbox_sync.app_user', :'a', false), set_config('pitbox_sync.no_app_sessions', :'n', false) \\g /dev/null"
    printf '%s\n' "${CHECKS_SQL}" "COMMIT;"; } |
    admin -d "${DB_NAME}" -v t="${TARGET}" -v a="${APP_USER}" -v n="${NO_APP_SESSIONS}" ||
    die "the checks above refused or failed; nothing was changed"
}

echo "==> 1. Checking ${DB_NAME}.${TARGET}..."
TGT_INFO="$(info "${DB_NAME}" "${TARGET}")" || exit 1 # (info's die only ends its subshell)
read -r TGT_EXISTS TGT_TABLES TGT_ROWS TGT_GOOSE TGT_APPLIED TGT_LATEST <<<"${TGT_INFO}"
run_checks

echo "==> 2. Copying ${FROM_BACKUP:-${SOURCE}} into a staging database (as ${APP_USER})..."
ENC_LOCALE="$(admin -d "${DB_NAME}" -At -c "
  SELECT format('ENCODING %L LOCALE_PROVIDER %s LC_COLLATE %L LC_CTYPE %L%s', pg_encoding_to_char(encoding),
                CASE datlocprovider WHEN 'i' THEN 'icu' WHEN 'b' THEN 'builtin' ELSE 'libc' END, datcollate, datctype,
                CASE datlocprovider WHEN 'i' THEN format(' ICU_LOCALE %L', datlocale) WHEN 'b' THEN format(' BUILTIN_LOCALE %L', datlocale) ELSE '' END)
  FROM pg_database WHERE datname = current_database()")" || die "could not read the database's locale; nothing was changed"
admin -d postgres -c "CREATE DATABASE ${STAGING_DB} OWNER ${APP_USER} TEMPLATE template0 ${ENC_LOCALE};" >/dev/null ||
  die "could not create the staging database; nothing was changed"
app -d "${STAGING_DB}" -c "DROP SCHEMA public;" >/dev/null || die "could not prepare the staging database; nothing was changed"
if [ -n "${FROM_BACKUP}" ]; then
  # The backup must be of TARGET (a schema of that name and nothing else).
  SCHEMAS="$(pg_restore -l "${FROM_BACKUP}" | awk '$4 == "SCHEMA" { print $6 }' | sort -u | tr '\n' ' ')" ||
    die "can't read ${FROM_BACKUP}; nothing was changed"
  [ "${SCHEMAS}" = "${TARGET} " ] || die "${FROM_BACKUP} holds schema(s) '${SCHEMAS% }', not ${TARGET}; nothing was changed"
  pg_restore --no-owner --no-privileges -f - "${FROM_BACKUP}" | app -d "${STAGING_DB}" >/dev/null ||
    die "loading ${FROM_BACKUP} into staging failed; nothing was changed"
else
  pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -w -d "${DB_NAME}" --schema="${SOURCE}" --no-owner --no-privileges \
    --lock-wait-timeout="${LOCK_TIMEOUT}" | app -d "${STAGING_DB}" >/dev/null ||
    die "copying ${SOURCE} into staging failed; nothing was changed"
  app -d "${STAGING_DB}" -c "ALTER SCHEMA ${SOURCE} RENAME TO ${TARGET};" >/dev/null || die "could not rename the staging copy; nothing was changed"
fi
# From here on, the copy is what was checked and shown: changes to SOURCE
# after this don't reach TARGET.
SRC_INFO="$(info "${STAGING_DB}" "${TARGET}")" || exit 1
read -r _ SRC_TABLES SRC_ROWS SRC_GOOSE SRC_APPLIED SRC_LATEST <<<"${SRC_INFO}"
[ "${SRC_TABLES}" -gt 0 ] || die "${FROM_BACKUP:-schema ${SOURCE}} has no tables: copying it would leave ${TARGET} empty; nothing was changed"
[ "${SRC_GOOSE}" != none ] || die "${FROM_BACKUP:-schema ${SOURCE}} has no applied migrations (goose_db_version): not a Pitbox schema; nothing was changed"
NOTE=""
if [ "${TARGET}" = public ] && [ "${TGT_APPLIED}" != none ] && [ "${TGT_APPLIED}" != "${SRC_APPLIED}" ]; then
  if [ -z "${FROM_BACKUP}" ]; then
    die "public and ${SOURCE} have different migrations applied (public up to ${TGT_GOOSE}, ${SOURCE} up to ${SRC_GOOSE}): the deployed app expects public's. Refusing; nothing was changed."
  fi
  # A backup may be older (the app applies what's missing on start), never
  # hold a migration public lacks.
  EXTRA="$(comm -13 <(tr ',' '\n' <<<"${TGT_APPLIED}" | sort) <(tr ',' '\n' <<<"${SRC_APPLIED}" | sort) | tr '\n' ' ')"
  [ -z "${EXTRA}" ] || die "the backup has migrations public doesn't (${EXTRA% }): refusing; nothing was changed"
  NOTE=" - NOTE: the backup is at migration ${SRC_GOOSE}, public at ${TGT_GOOSE}; the app applies the rest when it starts."
elif [ "${TGT_GOOSE}" != none ] && [ "${TGT_GOOSE}" -gt "${SRC_GOOSE}" ]; then
  NOTE=" - NOTE: ${TARGET} is at migration ${TGT_GOOSE}, newer than the copy (${SRC_GOOSE}); it goes back to ${SRC_GOOSE}."
fi

# Room on each filesystem: the backup and the work files (up to about
# three times the copy, photos as hex) next to the backups; the staging
# copy and the restore in the data directory; their WAL in pg_wal. Needs on
# the same filesystem add up.
size_of() { # size_of DATABASE SCHEMA, in bytes
  local out
  out="$(admin -d "$1" -At -v s="$2" <<<"SELECT COALESCE(sum(pg_total_relation_size(c.oid)), 0) FROM pg_class c WHERE c.relnamespace = to_regnamespace(:'s') AND c.relkind IN ('r', 'p', 'm');")" || return 1
  [[ "${out}" =~ ^[0-9]+$ ]] || return 1
  echo "${out}"
}
SRC_BYTES="$(size_of "${STAGING_DB}" "${TARGET}")" || die "could not read the size of the copy; nothing was changed"
TGT_BYTES="$(size_of "${DB_NAME}" "${TARGET}")" || die "could not read the size of ${TARGET}; nothing was changed"
MB=$((1024 * 1024))
declare -A NEED=() WHERE=()
need() { # need PATH BYTES
  local dev
  dev="$(stat -L -c %d "$1" 2>/dev/null)" || die "can't check the free space of $1; nothing was changed"
  NEED[${dev}]=$(( ${NEED[${dev}]:-0} + $2 ))
  WHERE[${dev}]="${WHERE[${dev}]:+${WHERE[${dev}]}, }$1"
}
need "${BACKUP_DIR}" $((TGT_BYTES + 3 * SRC_BYTES + 256 * MB))
DATA_NOTE="the backup directory has room"
if [ "${SYNC_SKIP_DATA_DIR_CHECK:-}" = 1 ]; then
  DATA_NOTE="the data directory was NOT checked (SYNC_SKIP_DATA_DIR_CHECK=1)"
else
  DATA_DIR="$(admin -d "${DB_NAME}" -At -c "SHOW data_directory")" || die "can't read the data directory's location; nothing was changed"
  [ -d "${DATA_DIR}" ] || die "the data directory ${DATA_DIR} isn't on this machine: run this on the database server; nothing was changed"
  need "${DATA_DIR}" $((2 * SRC_BYTES + 512 * MB))
  need "${DATA_DIR}/pg_wal" $((2 * SRC_BYTES + 256 * MB))
  DATA_NOTE="the backup directory, data directory and pg_wal have room"
fi
for dev in "${!NEED[@]}"; do
  path="${WHERE[${dev}]%%,*}"
  free="$(df -P -B1 "${path}" 2>/dev/null | awk 'NR == 2 { print $4 }')"
  [[ "${free}" =~ ^[0-9]+$ ]] || die "can't read the free space of ${path}; nothing was changed"
  [ "${free}" -ge "${NEED[${dev}]}" ] ||
    die "${WHERE[${dev}]}: $((free / MB)) MB free, the sync needs $((NEED[${dev}] / MB)) MB there; nothing was changed"
done

if [ "${TGT_EXISTS}" = 1 ]; then
  BACKUP="${BACKUP_DIR}/${DB_NAME}-${TARGET}-${STAMP}.dump"
fi
CONFIRM_WORD="OVERWRITE"
[ "${TARGET}" != public ] || CONFIRM_WORD="OVERWRITE public"

cat <<EOF
======================================================================
                     ⚠️   CRITICAL ACTION WARNING   ⚠️
======================================================================
 DATABASE : ${DB_NAME} on ${PG_HOST}:${PG_PORT}
 SOURCE   : ${FROM_BACKUP:-${SOURCE}}  (as copied just now: ${SRC_TABLES} tables, ${SRC_ROWS} rows, migration ${SRC_GOOSE}, latest change ${SRC_LATEST})
 TARGET   : ${TARGET}  (${TGT_TABLES} tables, ${TGT_ROWS} rows, migration ${TGT_GOOSE}, latest change ${TGT_LATEST}; WILL BE REPLACED)

 WHAT WILL HAPPEN:
 - ${TARGET} is backed up first${BACKUP:+ to ${BACKUP}}.
 - Then, in one transaction run as '${APP_USER}', everything in ${TARGET}
   (tables, views, sequences, functions, types and data) is dropped and
   replaced with the copy above. It commits only if nothing was written to
   ${TARGET} since the backup and the result matches the copy exactly;
   otherwise nothing changes.
 - Stop the Pitbox app that uses ${TARGET} first (for public: the
   Portainer stack; this refuses while '${APP_USER}' is connected to it)
   and start it again afterwards. A lock held longer than ${LOCK_TIMEOUT}
   stops the sync, changing nothing.
 - Disk space: ${DATA_NOTE}.
${NOTE}
======================================================================
EOF

read -r -p "Type '${CONFIRM_WORD}' to continue (any other input aborts): " CONFIRM || CONFIRM=""
if [ "${CONFIRM}" != "${CONFIRM_WORD}" ]; then
  echo "==> Aborted. No changes were made to '${DB_NAME}'."
  exit 0
fi

# TARGET's fingerprint before the backup: the transaction checks TARGET
# still has it, so nothing written in between can be lost.
BEFORE="$(manifest "${DB_NAME}" "${TARGET}")" || die "could not fingerprint ${TARGET}; nothing was changed"
if [ -n "${BACKUP}" ]; then
  echo "==> 3. Backing up ${TARGET} to ${BACKUP}..."
  pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -w -d "${DB_NAME}" --schema="${TARGET}" -Fc \
    --lock-wait-timeout="${LOCK_TIMEOUT}" -f "${BACKUP}.partial" || die "the backup failed; nothing was changed"
  # Read it back completely (pg_restore -l only reads the table of contents).
  pg_restore -f /dev/null "${BACKUP}.partial" || die "the backup can't be read back; nothing was changed"
  mv "${BACKUP}.partial" "${BACKUP}"
  AFTER_BACKUP="$(manifest "${DB_NAME}" "${TARGET}")" || die "could not fingerprint ${TARGET}; nothing was changed"
  [ "${AFTER_BACKUP}" = "${BEFORE}" ] || die "${TARGET} changed while it was being backed up (is the app still running?); nothing was changed"
else
  echo "==> 3. ${TARGET} doesn't exist yet: nothing to back up."
fi
EXPECTED="$(manifest "${STAGING_DB}" "${TARGET}")" || die "could not fingerprint the copy; nothing was changed"

echo "==> 4. Writing the transaction..."
TX="${WORK}/transaction.sql"
{
  cat <<'SQL'
-- This transaction's id first (stderr is unbuffered), so the end can ask
-- the server whether it committed.
SELECT pg_catalog.pg_current_xact_id() AS pitbox_sync_xid \gset
\warn pitbox-sync-xid :pitbox_sync_xid
\unset pitbox_sync_xid
-- The settings the rest reads, then the psql variables go: nothing in the
-- restored dump may be taken for one.
SELECT pg_catalog.set_config('pitbox_sync.target', :'pitbox_sync_target', false),
       pg_catalog.set_config('pitbox_sync.app_user', :'pitbox_sync_app_user', false),
       pg_catalog.set_config('pitbox_sync.no_app_sessions', :'pitbox_sync_no_app_sessions', false),
       pg_catalog.set_config('pitbox_sync.lock_timeout', :'pitbox_sync_lock_timeout', false),
       pg_catalog.set_config('pitbox_sync.before', :'pitbox_sync_before', false),
       pg_catalog.set_config('pitbox_sync.expected', :'pitbox_sync_expected', false),
       pg_catalog.set_config('lock_timeout', :'pitbox_sync_lock_timeout', false) \g /dev/null
\unset pitbox_sync_target
\unset pitbox_sync_app_user
\unset pitbox_sync_no_app_sessions
\unset pitbox_sync_lock_timeout
\unset pitbox_sync_before
\unset pitbox_sync_expected
SET search_path = pg_catalog;
SQL
  printf '%s\n' "${CHECKS_SQL}"
  cat <<'SQL'
-- TARGET (created if new) with its tables locked: nobody reads, writes or
-- builds on them until the end.
DO $$
DECLARE
    s text := current_setting('pitbox_sync.target');
    r record;
BEGIN
    IF to_regnamespace(s) IS NULL THEN
        EXECUTE format('CREATE SCHEMA %I', s);
    END IF;
    FOR r IN SELECT c.relname FROM pg_class c WHERE c.relnamespace = to_regnamespace(s) AND c.relkind IN ('r', 'p') ORDER BY c.relname LOOP
        EXECUTE format('LOCK TABLE %I.%I IN ACCESS EXCLUSIVE MODE', s, r.relname);
    END LOOP;
END $$;
SQL
  # The checks again, now that nothing can be added to TARGET's tables.
  printf '%s\n' "${CHECKS_SQL}"
  cat <<'SQL'
SELECT current_setting('pitbox_sync.target') AS pitbox_sync_schema \gset
SQL
  printf '%s \\gset pitbox_sync_now_\n' "${MANIFEST_SQL}"
  cat <<'SQL'
\unset pitbox_sync_schema
SELECT set_config('pitbox_sync.now', :'pitbox_sync_now_manifest', false) \g /dev/null
\unset pitbox_sync_now_manifest
DO $$
BEGIN
    IF current_setting('pitbox_sync.now') IS DISTINCT FROM current_setting('pitbox_sync.before') THEN
        RAISE EXCEPTION '% was changed after it was backed up (is the app still running?). Rolled back: nothing was changed.',
            current_setting('pitbox_sync.target');
    END IF;
END $$;

-- Clear TARGET, keeping the schema itself (and its grants).
DO $$
DECLARE
    s text := current_setting('pitbox_sync.target');
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

-- The copy (from the staging database, dumped by pg_dump), restored in
-- this session of the app user.
SQL
  # The dump's header turns the lock timeout off and creates the schema we
  # kept: both lines go (only in the header, before any data).
  pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -w -d "${STAGING_DB}" --schema="${TARGET}" --no-owner --no-privileges |
    awk -v schema="${TARGET}" '
      !data && /^(CREATE TABLE|COPY) / { data = 1 }
      !data && $0 == "SET lock_timeout = 0;" { next }
      !data && $0 == "CREATE SCHEMA " schema ";" { next }
      !data && index($0, "COMMENT ON SCHEMA " schema " IS ") == 1 { next }
      { print }'
  cat <<'SQL'

SET search_path = pg_catalog;
SELECT set_config('lock_timeout', current_setting('pitbox_sync.lock_timeout'), false) \g /dev/null
DO $$
DECLARE
    s text := current_setting('pitbox_sync.target');
    app text := current_setting('pitbox_sync.app_user');
    ns oid := to_regnamespace(s);
    lost text;
    list text;
BEGIN
    -- The app user owns everything in TARGET, as its migrations need.
    SELECT string_agg(o, ', ') INTO list FROM (
        SELECT relname::text AS o FROM pg_class WHERE relnamespace = ns AND relkind IN ('r', 'p', 'v', 'm', 'f', 'S') AND pg_get_userbyid(relowner) <> app
        UNION ALL SELECT proname FROM pg_proc WHERE pronamespace = ns AND pg_get_userbyid(proowner) <> app
    ) o;
    IF list IS NOT NULL THEN
        RAISE EXCEPTION 'After the restore, % does not own %. Rolled back: nothing was changed.', app, list;
    END IF;
    -- No grant or comment TARGET had before is lost.
    SELECT string_agg(l, '; ') INTO lost
    FROM unnest(string_to_array(current_setting('pitbox_sync.privs_before'), E'\n')) AS l
    WHERE l <> '' AND l <> ALL (string_to_array((
SQL
  printf '%s\n' "${PRIVS_SQL}"
  cat <<'SQL'
    ), E'\n'));
    IF lost IS NOT NULL THEN
        RAISE EXCEPTION 'The copy would lose these grants or comments of %: %. Rolled back: nothing was changed.', s, lost;
    END IF;
END $$;

-- The result must match the staging copy exactly, and the app must still
-- be stopped, or everything rolls back.
SELECT current_setting('pitbox_sync.target') AS pitbox_sync_schema \gset
SQL
  printf '%s \\gset pitbox_sync_got_\n' "${MANIFEST_SQL}"
  cat <<'SQL'
SELECT set_config('pitbox_sync.got', :'pitbox_sync_got_manifest', false) \g /dev/null
DO $$
BEGIN
    IF current_setting('pitbox_sync.got') IS DISTINCT FROM current_setting('pitbox_sync.expected') THEN
        RAISE EXCEPTION 'The restored % does not match the copy (manifest %, expected %). Rolled back: nothing was changed.',
            current_setting('pitbox_sync.target'), current_setting('pitbox_sync.got'), current_setting('pitbox_sync.expected');
    END IF;
END $$;
SQL
  printf '%s\n' "${CHECKS_SQL}"
  echo "-- pitbox-sync: end of transaction"
} >"${TX}" || die "could not write the transaction; nothing was changed"
[ "$(tail -n 1 "${TX}")" = "-- pitbox-sync: end of transaction" ] || die "the transaction file is incomplete; nothing was changed"

echo "==> 5. Replacing ${TARGET} in one transaction (as ${APP_USER})..."
# psql is started directly (its own PID, for the signal handling above) and
# dies with this script (--pdeathsig), so it can't commit on its own. An
# interrupt stops it, then the report below still runs.
trap stop_transaction INT TERM HUP
( exec setpriv --pdeathsig TERM -- psql -X -q -w -v ON_ERROR_STOP=1 -h "${PG_HOST}" -p "${PG_PORT}" -U "${APP_USER}" -d "${DB_NAME}" \
    --single-transaction \
    -v pitbox_sync_target="${TARGET}" -v pitbox_sync_app_user="${APP_USER}" -v pitbox_sync_no_app_sessions="${NO_APP_SESSIONS}" \
    -v pitbox_sync_lock_timeout="${LOCK_TIMEOUT}" -v pitbox_sync_before="${BEFORE}" -v pitbox_sync_expected="${EXPECTED}" \
    -f "${TX}" >"${WORK}/transaction.log" 2>&1 ) &
PSQL_PID=$!
RC=0
wait "${PSQL_PID}" || RC=$?
while kill -0 "${PSQL_PID}" 2>/dev/null; do
  RC=0
  wait "${PSQL_PID}" || RC=$?
done
PSQL_PID=""
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

# What happened, from the server: did this transaction commit?
XID="$(awk '$1 == "pitbox-sync-xid" { print $2; exit }' "${WORK}/transaction.log")"
STATUS="not started"
xact_status() { admin -d "${DB_NAME}" -At -v x="${XID}" <<<"SELECT COALESCE(pg_xact_status(:'x'::xid8), 'unknown');"; }
if [[ "${XID}" =~ ^[0-9]+$ ]]; then
  STATUS="$(xact_status)" || STATUS="unknown"
  if [ "${STATUS}" = "in progress" ]; then
    echo "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = :'d' AND application_name = :'a' AND pid <> pg_backend_pid();" |
      admin -d postgres -At -v d="${DB_NAME}" -v a="${APP_NAME}" >/dev/null 2>&1 || true
    sleep 1
    STATUS="$(xact_status)" || STATUS="unknown"
  fi
fi
NOW="$(manifest "${DB_NAME}" "${TARGET}" || echo unknown)"
RESTORE_HINT="$(readlink -f "$0") --from-backup ${BACKUP} ${TARGET}"
case "${STATUS}" in
  committed)
    if [ "${NOW}" = "${EXPECTED}" ]; then
      NEW_INFO="$(info "${DB_NAME}" "${TARGET}")" || NEW_INFO="? ? ? ? ? ?"
      read -r _ NEW_TABLES NEW_ROWS NEW_GOOSE _ _ <<<"${NEW_INFO}"
      echo "==> Done. ${TARGET} is now a copy of ${FROM_BACKUP:-${SOURCE}}: ${NEW_TABLES} tables, ${NEW_ROWS} rows, migration ${NEW_GOOSE}; verified."
    else
      echo "==> Done: the copy was committed and verified in the transaction, but ${TARGET} has changed since (something wrote to it after the commit)."
    fi
    [ -z "${BACKUP}" ] || echo "    The old ${TARGET} is in ${BACKUP}. To go back (it checks and backs up first, like this run): ${RESTORE_HINT}"
    echo "    Start the Pitbox app that uses ${TARGET} again."
    [ "${RC}" = 0 ] || echo "    (psql exited ${RC} after the commit.)"
    exit 0
    ;;
  aborted | "not started")
    sed 's/^/    | /' "${WORK}/transaction.log" | tail -n 25 >&2
    if [ "${NOW}" = "${BEFORE}" ]; then
      die "the transaction was rolled back (psql exit ${RC}): this sync changed nothing, and ${TARGET} is as it was."
    fi
    die "the transaction was rolled back (psql exit ${RC}): this sync changed nothing. ${TARGET} differs from its backup because something else wrote to it since; do NOT restore the backup over those changes."
    ;;
  *)
    sed 's/^/    | /' "${WORK}/transaction.log" | tail -n 25 >&2
    die "could not find out whether the transaction committed (status: ${STATUS}, psql exit ${RC}). ${TARGET}'s fingerprint is ${NOW}; the copy's was ${EXPECTED}, the old one ${BEFORE}. Check before starting the app${BACKUP:+ (backup: ${BACKUP})}."
    ;;
esac
