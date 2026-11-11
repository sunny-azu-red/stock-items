#!/usr/bin/env bash
# Operator-only script for the Postgres server (root@postgres-db).
# Coding agents: NEVER run this script against the real server
# (scripts/test-db-server.sh runs it against a throwaway local Postgres).
#
# Usage: sync_schema.sh SOURCE TARGET
#   Replaces schema TARGET of the Pitbox database with a copy of SOURCE.
#   sync_stock_items_public_to_dev.sh and sync_stock_items_dev_to_public.sh
#   call it. Run it inside tmux or screen: a dropped SSH session stops it.
#
# How it keeps the data safe:
#   - SOURCE is only read, never changed or locked beyond pg_dump's own
#     ACCESS SHARE locks.
#   - It checks first and changes nothing until you confirm. Every check
#     that can't be made stops it. It refuses a SOURCE that is missing,
#     empty or has no migrations; objects in other schemas that depend on
#     TARGET (DROP ... CASCADE would destroy them); grants, comments or
#     publications on TARGET's objects that the copy wouldn't keep; and for
#     TARGET public: a running app (any session of the app user) and any
#     difference in applied migrations. You type OVERWRITE (OVERWRITE public
#     for public).
#   - It backs TARGET up (pg_dump -Fc, read back completely) before
#     touching it, and fingerprints it (the manifest below).
#   - Clearing TARGET and restoring the copy run in ONE transaction, from a
#     file written completely beforehand. It locks TARGET against writes,
#     checks TARGET still matches the fingerprint taken before the backup
#     (nothing was written since), repeats the refusal checks, and commits
#     only if the copy matches SOURCE's staging copy exactly and TARGET's
#     grants and comments are still there. Any error rolls it all back.
#   - It never waits longer than SYNC_LOCK_TIMEOUT for a lock, Ctrl-C or a
#     kill reaches psql (which then rolls back), two runs can't overlap,
#     and at the end it reports what TARGET actually holds.
#
# Settings (environment; the defaults are the server's, tests change them):
#   SYNC_DB, SYNC_PG_HOST, SYNC_PG_PORT, SYNC_PG_USER, SYNC_APP_USER,
#   SYNC_BACKUP_DIR, SYNC_LOCK_TIMEOUT, SYNC_LOCK_FILE
set -euo pipefail
umask 077

SOURCE="${1:-}"
TARGET="${2:-}"
DB_NAME="${SYNC_DB:-stock_items}"
PG_HOST="${SYNC_PG_HOST:-localhost}"
PG_PORT="${SYNC_PG_PORT:-5432}"
PG_USER="${SYNC_PG_USER:-postgres}"
APP_USER="${SYNC_APP_USER:-stock}"
BACKUP_DIR="${SYNC_BACKUP_DIR:-${HOME}/pitbox-sync-backups}"
LOCK_TIMEOUT="${SYNC_LOCK_TIMEOUT:-10s}"
LOCK_FILE="${SYNC_LOCK_FILE:-/run/lock/pitbox-schema-sync.lock}"
STAMP="$(date +%Y%m%d-%H%M%S)"
STAGING_DB="sync_staging_$$"
APP_NAME="pitbox-schema-sync" # our sessions' application_name

# Only these settings decide where we connect and how sessions behave: a
# PGHOSTADDR or PGSERVICE in root's environment must not send us elsewhere.
unset PGHOST PGHOSTADDR PGPORT PGUSER PGDATABASE PGSERVICE PGSERVICEFILE PGOPTIONS PGTZ PGDATESTYLE \
      PGCLIENTENCODING PGTARGETSESSIONATTRS PGREQUIREAUTH PSQLRC
export PGPASSFILE="${PGPASSFILE:-${HOME}/.pgpass}"
export PGAPPNAME="${APP_NAME}"
export PGOPTIONS="-c lock_timeout=${LOCK_TIMEOUT}"

die() { echo "ERROR: $*" >&2; exit 1; }

valid_name() { [[ "$1" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] && [[ "$1" != pg_* ]] && [[ "$1" != information_schema ]]; }
[ -n "${SOURCE}" ] && [ -n "${TARGET}" ] || die "usage: $0 SOURCE TARGET"
valid_name "${SOURCE}" || die "'${SOURCE}' is not a schema this script copies from"
valid_name "${TARGET}" || die "'${TARGET}' is not a schema this script copies to"
[ "${SOURCE}" != "${TARGET}" ] || die "SOURCE and TARGET are the same schema"
[[ "${LOCK_TIMEOUT}" =~ ^[0-9]+(ms|s|min)?$ ]] || die "SYNC_LOCK_TIMEOUT '${LOCK_TIMEOUT}' is not a duration like 10s"
for tool in psql pg_dump pg_restore flock df awk tee; do
  command -v "${tool}" >/dev/null || die "${tool} is not installed"
done

# One run at a time (this also makes the leftover clean-up below safe).
exec 9>"${LOCK_FILE}" || die "can't open the lock file ${LOCK_FILE}"
flock -n 9 || die "another schema sync is running (${LOCK_FILE})"

mkdir -p "${BACKUP_DIR}"
LOG="${BACKUP_DIR}/sync-${STAMP}-${SOURCE}-to-${TARGET}.log"
exec > >(tee -a "${LOG}") 2>&1
TEE_PID=$!
echo "==> $(date -Is) sync ${SOURCE} -> ${TARGET} (log: ${LOG})"

q() { psql -X -q -v ON_ERROR_STOP=1 -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" "$@"; }

# Leftovers of a run that was killed: its work files (copies of the data)
# and its staging database.
rm -rf "${BACKUP_DIR}"/work.* "${BACKUP_DIR}"/*.dump.partial
WORK="$(mktemp -d -p "${BACKUP_DIR}" work.XXXXXX)"
BACKUP=""
cleanup() {
  local rc=$?
  if ! q -d postgres -c "DROP DATABASE IF EXISTS ${STAGING_DB} WITH (FORCE);" >/dev/null 2>&1; then
    echo "WARNING: could not drop the staging database ${STAGING_DB} (a copy of ${SOURCE}); drop it by hand." >&2
  fi
  rm -rf "${WORK}"
  [ -z "${BACKUP}" ] || rm -f "${BACKUP}.partial"
  # Let tee write the last lines to the terminal and the log.
  exec 1>&- 2>&-
  wait "${TEE_PID}" 2>/dev/null || true
  exit "${rc}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

for old in $(q -d postgres -At -c "SELECT datname FROM pg_database WHERE datname ~ '^sync_staging_[0-9]+$'"); do
  echo "==> Dropping ${old}, the staging database of an earlier run that was stopped."
  q -d postgres -c "DROP DATABASE ${old} WITH (FORCE);" >/dev/null
done

# info SCHEMA prints "exists tables rows goose_max goose_applied latest":
# goose_applied is the applied migration versions (comma-separated) and
# latest the newest created_at/updated_at in the schema; "none" for none.
info() {
  local out
  out="$(q -d "${DB_NAME}" -At -F ' ' -v info_schema="$1" <<'SQL'
SET statement_timeout = '120s';
WITH ns AS (SELECT to_regnamespace(:'info_schema') AS oid),
tables AS (SELECT c.relname FROM pg_class c, ns WHERE c.relnamespace = ns.oid AND c.relkind IN ('r', 'p')),
goose AS (
    SELECT (xpath('/row/v/text()', query_to_xml(format(
        'SELECT string_agg(version_id::text, '','' ORDER BY version_id) AS v FROM %I.goose_db_version WHERE is_applied', :'info_schema'), false, true, '')))[1]::text AS applied
    WHERE to_regclass(format('%I.goose_db_version', :'info_schema')) IS NOT NULL
),
stamps AS (
    SELECT (xpath('/row/t/text()', query_to_xml(format('SELECT max(%I) AS t FROM %I.%I', a.attname, :'info_schema', c.relname), false, true, '')))[1]::text::timestamptz AS t
    FROM pg_class c JOIN pg_attribute a ON a.attrelid = c.oid, ns
    WHERE c.relnamespace = ns.oid AND c.relkind IN ('r', 'p') AND a.attname IN ('created_at', 'updated_at') AND NOT a.attisdropped
)
SELECT (SELECT oid IS NOT NULL FROM ns)::int,
       (SELECT count(*) FROM tables),
       (SELECT COALESCE(sum((xpath('/row/n/text()', query_to_xml(format('SELECT count(*) AS n FROM %I.%I', :'info_schema', relname), false, true, '')))[1]::text::bigint), 0) FROM tables),
       COALESCE((SELECT split_part(applied, ',', -1) FROM goose WHERE applied IS NOT NULL), 'none'),
       COALESCE((SELECT applied FROM goose WHERE applied IS NOT NULL), 'none'),
       COALESCE((SELECT to_char(max(t) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI"Z"') FROM stamps), 'none');
SQL
)" || die "could not read schema $1 (see above); nothing was changed"
  [[ "${out}" =~ ^[01]\ [0-9]+\ [0-9]+\ ([0-9]+|none)\ ([0-9,]+|none)\ ([0-9T:Z-]+|none)$ ]] ||
    die "unexpected answer reading schema $1: '${out}'; nothing was changed"
  echo "${out}"
}

# The manifest of a schema (psql variable pitbox_sync_schema): a hash of
# everything in it (objects, columns in order with their collations,
# defaults, constraints, indexes, views, triggers, rules, policies,
# function bodies, sequence values, and every row of every table), then
# how many entries it covers. Settings that change how values print are
# pinned, and sorting is bytewise, so the server's or database's defaults
# can't make two equal schemas differ. Extension members are left out.
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
manifest() { # manifest DATABASE SCHEMA
  local out
  out="$(printf '%s\n' "${MANIFEST_SQL}" | q -d "$1" -At -v pitbox_sync_schema="$2")" || return 1
  [[ "${out}" =~ ^([0-9a-f]{32}|none)\ [0-9]+$ ]] || return 1
  echo "${out}"
}

# The refusal checks, as one DO block reading pitbox_sync.* settings:
# reused before the prompt (read-only) and inside the transaction. It
# also records TARGET's grants and comments in pitbox_sync.privs_before,
# for the check after the restore.
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
CHECKS_SQL=$(cat <<SQL
DO \$\$
DECLARE
    s text := current_setting('pitbox_sync.target');
    app text := current_setting('pitbox_sync.app_user');
    ns oid := to_regnamespace(s);
    problems text[] := '{}';
    list text;
BEGIN
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
    IF ns IS NOT NULL THEN
        IF NOT has_schema_privilege(app, ns, 'CREATE') OR NOT has_schema_privilege(app, ns, 'USAGE') THEN
            problems := problems || format('%s lacks CREATE or USAGE on schema %s (as the app migrates it, make %s its owner: ALTER SCHEMA %I OWNER TO %I)', app, s, app, s, app);
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
    ELSE
        PERFORM set_config('pitbox_sync.privs_before', '', false);
    END IF;
    IF cardinality(problems) > 0 THEN
        RAISE EXCEPTION 'Refusing to replace %: %. Nothing was changed.', s, array_to_string(problems, '; ');
    END IF;
END \$\$;
SQL
)

echo "==> Checking ${DB_NAME}.${SOURCE} and ${DB_NAME}.${TARGET}..."
# (A failing info exits only its subshell: "|| exit 1" stops the script.)
SRC_INFO="$(info "${SOURCE}")" || exit 1
TGT_INFO="$(info "${TARGET}")" || exit 1
read -r SRC_EXISTS SRC_TABLES SRC_ROWS SRC_GOOSE SRC_APPLIED SRC_LATEST <<<"${SRC_INFO}"
read -r TGT_EXISTS TGT_TABLES TGT_ROWS TGT_GOOSE TGT_APPLIED TGT_LATEST <<<"${TGT_INFO}"
[ "${SRC_EXISTS}" = 1 ] || die "schema ${SOURCE} does not exist"
[ "${SRC_TABLES}" -gt 0 ] || die "schema ${SOURCE} has no tables: copying it would leave ${TARGET} empty"
[ "${SRC_GOOSE}" != none ] || die "schema ${SOURCE} has no applied migrations (goose_db_version): not a Pitbox schema"
NO_APP_SESSIONS=off
NOTE=""
if [ "${TARGET}" = public ]; then
  NO_APP_SESSIONS=on
  if [ "${TGT_APPLIED}" != none ] && [ "${TGT_APPLIED}" != "${SRC_APPLIED}" ]; then
    die "public and ${SOURCE} have different migrations applied (public up to ${TGT_GOOSE}, ${SOURCE} up to ${SRC_GOOSE}): the deployed app expects public's. Refusing; nothing was changed."
  fi
elif [ "${TGT_GOOSE}" != none ] && [ "${TGT_GOOSE}" -gt "${SRC_GOOSE}" ]; then
  NOTE=" - NOTE: ${TARGET} is at migration ${TGT_GOOSE}, newer than ${SOURCE} (${SRC_GOOSE}); it goes back to ${SRC_GOOSE}."
fi

# The refusal checks, read-only, before asking.
{ printf '%s\n' "BEGIN READ ONLY;" \
    "SELECT set_config('pitbox_sync.target', :'t', false), set_config('pitbox_sync.app_user', :'a', false), set_config('pitbox_sync.no_app_sessions', :'n', false) \\g /dev/null"
  printf '%s\n' "${CHECKS_SQL}" "COMMIT;"; } |
  q -d "${DB_NAME}" -v t="${TARGET}" -v a="${APP_USER}" -v n="${NO_APP_SESSIONS}" ||
  die "the checks above refused or failed; nothing was changed"

# Room for the backup, the work files (up to about three times the
# schema, photos as hex) and, in the data directory, the staging copy
# and the restore (about twice, plus WAL).
SRC_BYTES="$(echo "SELECT COALESCE(sum(pg_total_relation_size(c.oid)), 0) FROM pg_class c WHERE c.relnamespace = to_regnamespace(:'s') AND c.relkind IN ('r', 'p', 'm');" |
  q -d "${DB_NAME}" -At -v s="${SOURCE}")" || SRC_BYTES=""
[[ "${SRC_BYTES}" =~ ^[0-9]+$ ]] || die "could not read the size of ${SOURCE}; nothing was changed"
free_bytes() { df -P -B1 "$1" 2>/dev/null | awk 'NR == 2 { print $4 }'; }
NEED_WORK=$((4 * SRC_BYTES + 256 * 1024 * 1024))
HAVE_WORK="$(free_bytes "${BACKUP_DIR}")"
[[ "${HAVE_WORK}" =~ ^[0-9]+$ ]] || die "could not read the free space of ${BACKUP_DIR}"
[ "${HAVE_WORK}" -ge "${NEED_WORK}" ] || die "${BACKUP_DIR} has $((HAVE_WORK / 1048576)) MB free, needs $((NEED_WORK / 1048576)) MB; nothing was changed"
DATA_DIR="$(q -d "${DB_NAME}" -At -c "SHOW data_directory" 2>/dev/null || true)"
DATA_NOTE="Postgres data directory: free space not checked (not on this machine)."
if [ -n "${DATA_DIR}" ] && [ -d "${DATA_DIR}" ]; then
  NEED_DATA=$((3 * SRC_BYTES + 512 * 1024 * 1024))
  HAVE_DATA="$(free_bytes "${DATA_DIR}")"
  [[ "${HAVE_DATA}" =~ ^[0-9]+$ ]] && [ "${HAVE_DATA}" -ge "${NEED_DATA}" ] ||
    die "the Postgres data directory ${DATA_DIR} has too little free space ($((${HAVE_DATA:-0} / 1048576)) MB, needs $((NEED_DATA / 1048576)) MB); nothing was changed"
  DATA_NOTE="Postgres data directory: $((HAVE_DATA / 1048576)) MB free."
fi

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
 SOURCE   : ${SOURCE}  (${SRC_TABLES} tables, ${SRC_ROWS} rows, migration ${SRC_GOOSE}, latest change ${SRC_LATEST}; not changed)
 TARGET   : ${TARGET}  (${TGT_TABLES} tables, ${TGT_ROWS} rows, migration ${TGT_GOOSE}, latest change ${TGT_LATEST}; WILL BE REPLACED)

 WHAT WILL HAPPEN:
 - ${TARGET} is backed up first${BACKUP:+ to ${BACKUP}}.
 - Then, in one transaction, everything in ${TARGET} (tables, views,
   sequences, functions, types and data) is dropped and replaced with a
   copy of ${SOURCE}, owned by '${APP_USER}'. It commits only if nothing
   was written to ${TARGET} since the backup and the copy matches
   ${SOURCE} exactly; otherwise nothing changes.
 - Stop the Pitbox app that uses ${TARGET} first (for public: the
   Portainer stack; this script refuses while '${APP_USER}' is connected)
   and start it again afterwards. A lock held longer than ${LOCK_TIMEOUT}
   stops the sync, changing nothing.
 - ${DATA_NOTE}
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
  echo "==> 1. Backing up ${TARGET} to ${BACKUP}..."
  pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" --schema="${TARGET}" -Fc \
    --lock-wait-timeout="${LOCK_TIMEOUT}" -f "${BACKUP}.partial" || die "the backup failed; nothing was changed"
  # Read it back completely (pg_restore -l only reads the table of contents).
  pg_restore -f /dev/null "${BACKUP}.partial" || die "the backup can't be read back; nothing was changed"
  mv "${BACKUP}.partial" "${BACKUP}"
  AFTER_BACKUP="$(manifest "${DB_NAME}" "${TARGET}")" || die "could not fingerprint ${TARGET}; nothing was changed"
  [ "${AFTER_BACKUP}" = "${BEFORE}" ] || die "${TARGET} changed while it was being backed up (is the app still running?); nothing was changed"
else
  echo "==> 1. ${TARGET} doesn't exist yet: nothing to back up."
fi

echo "==> 2. Copying ${SOURCE} into a staging database as ${TARGET}..."
ENC_LOCALE="$(q -d "${DB_NAME}" -At -c "
  SELECT format('ENCODING %L LOCALE_PROVIDER %s LC_COLLATE %L LC_CTYPE %L%s', pg_encoding_to_char(encoding),
                CASE datlocprovider WHEN 'i' THEN 'icu' WHEN 'b' THEN 'builtin' ELSE 'libc' END, datcollate, datctype,
                CASE datlocprovider WHEN 'i' THEN format(' ICU_LOCALE %L', datlocale) WHEN 'b' THEN format(' BUILTIN_LOCALE %L', datlocale) ELSE '' END)
  FROM pg_database WHERE datname = current_database()")" || die "could not read the database's locale; nothing was changed"
q -d postgres -c "CREATE DATABASE ${STAGING_DB} TEMPLATE template0 ${ENC_LOCALE};" >/dev/null
# The staging database's own empty public schema makes way for the copy.
q -d "${STAGING_DB}" -c "DROP SCHEMA public CASCADE;" >/dev/null
pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${DB_NAME}" --schema="${SOURCE}" --lock-wait-timeout="${LOCK_TIMEOUT}" |
  q -d "${STAGING_DB}" >/dev/null || die "copying ${SOURCE} into staging failed; nothing was changed"
q -d "${STAGING_DB}" -c "ALTER SCHEMA ${SOURCE} RENAME TO ${TARGET};" >/dev/null
EXPECTED="$(manifest "${STAGING_DB}" "${TARGET}")" || die "could not fingerprint the copy; nothing was changed"

echo "==> 3. Writing the transaction..."
TX="${WORK}/transaction.sql"
{
  cat <<'SQL'
-- The settings the rest reads, then the psql variables go: nothing in the
-- restored dump may be taken for one.
SELECT set_config('pitbox_sync.target', :'pitbox_sync_target', false),
       set_config('pitbox_sync.app_user', :'pitbox_sync_app_user', false),
       set_config('pitbox_sync.no_app_sessions', :'pitbox_sync_no_app_sessions', false),
       set_config('pitbox_sync.lock_timeout', :'pitbox_sync_lock_timeout', false),
       set_config('pitbox_sync.before', :'pitbox_sync_before', false),
       set_config('pitbox_sync.expected', :'pitbox_sync_expected', false),
       set_config('lock_timeout', :'pitbox_sync_lock_timeout', false) \g /dev/null
\unset pitbox_sync_target
\unset pitbox_sync_app_user
\unset pitbox_sync_no_app_sessions
\unset pitbox_sync_lock_timeout
\unset pitbox_sync_before
\unset pitbox_sync_expected
SQL
  printf '%s\n' "${CHECKS_SQL}"
  cat <<'SQL'
-- TARGET (created for the app user if new) locked against writes, then
-- still as it was before the backup.
DO $$
DECLARE
    s text := current_setting('pitbox_sync.target');
    r record;
BEGIN
    IF to_regnamespace(s) IS NULL THEN
        EXECUTE format('CREATE SCHEMA %I AUTHORIZATION %I', s, current_setting('pitbox_sync.app_user'));
    END IF;
    FOR r IN SELECT c.relname FROM pg_class c WHERE c.relnamespace = to_regnamespace(s) AND c.relkind IN ('r', 'p') ORDER BY c.relname LOOP
        EXECUTE format('LOCK TABLE %I.%I IN SHARE MODE', s, r.relname);
    END LOOP;
END $$;
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

-- Restore the copy as the app user, so it owns everything.
SELECT set_config('role', current_setting('pitbox_sync.app_user'), false) \g /dev/null
SQL
  pg_dump -h "${PG_HOST}" -p "${PG_PORT}" -U "${PG_USER}" -d "${STAGING_DB}" --schema="${TARGET}" --no-owner --no-privileges |
    sed -E -e "/^CREATE SCHEMA ${TARGET};\$/d" -e "/^COMMENT ON SCHEMA ${TARGET} IS /d"
  cat <<'SQL'

RESET ROLE;
-- The dump turned the lock timeout off; it applies again.
SELECT set_config('lock_timeout', current_setting('pitbox_sync.lock_timeout'), false) \g /dev/null
SELECT set_config('statement_timeout', '0', false) \g /dev/null
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

-- The copy must match the staging copy exactly, and the app must still be
-- stopped, or everything rolls back.
SELECT current_setting('pitbox_sync.target') AS pitbox_sync_schema \gset
SQL
  printf '%s \\gset pitbox_sync_got_\n' "${MANIFEST_SQL}"
  cat <<'SQL'
SELECT set_config('pitbox_sync.got', :'pitbox_sync_got_manifest', false) \g /dev/null
DO $$
BEGIN
    IF current_setting('pitbox_sync.got') IS DISTINCT FROM current_setting('pitbox_sync.expected') THEN
        RAISE EXCEPTION 'The restored % does not match the source (manifest %, expected %). Rolled back: nothing was changed.',
            current_setting('pitbox_sync.target'), current_setting('pitbox_sync.got'), current_setting('pitbox_sync.expected');
    END IF;
END $$;
SQL
  printf '%s\n' "${CHECKS_SQL}"
  echo "-- pitbox-sync: end of transaction"
} >"${TX}"
[ "$(tail -n 1 "${TX}")" = "-- pitbox-sync: end of transaction" ] || die "the transaction file is incomplete; nothing was changed"

echo "==> 4. Replacing ${TARGET} in one transaction..."
# psql runs in the background so Ctrl-C, a kill or a hang-up can stop it:
# psql gets SIGTERM (a script's background job ignores SIGINT) and its
# server session is terminated, so the transaction rolls back at once
# instead of after a lock wait.
q -d "${DB_NAME}" --single-transaction \
  -v pitbox_sync_target="${TARGET}" -v pitbox_sync_app_user="${APP_USER}" -v pitbox_sync_no_app_sessions="${NO_APP_SESSIONS}" \
  -v pitbox_sync_lock_timeout="${LOCK_TIMEOUT}" -v pitbox_sync_before="${BEFORE}" -v pitbox_sync_expected="${EXPECTED}" \
  -f "${TX}" >"${WORK}/transaction.log" 2>&1 &
PSQL_PID=$!
stop_transaction() {
  echo "==> Interrupted: stopping psql (the transaction rolls back)..." >&2
  kill -TERM "${PSQL_PID}" 2>/dev/null || true
  echo "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = :'d' AND application_name = :'a' AND pid <> pg_backend_pid();" |
    q -d postgres -At -v d="${DB_NAME}" -v a="${APP_NAME}" >/dev/null 2>&1 || true
}
trap stop_transaction INT TERM HUP
RC=0
wait "${PSQL_PID}" || RC=$?
while kill -0 "${PSQL_PID}" 2>/dev/null; do
  RC=0
  wait "${PSQL_PID}" || RC=$?
done
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

# Say what TARGET actually holds now, whatever psql's status says (a lost
# connection during COMMIT leaves the outcome unknown to psql).
NOW="$(manifest "${DB_NAME}" "${TARGET}" || echo unknown)"
RESTORE_HINT="pg_restore -h ${PG_HOST} -p ${PG_PORT} -U ${PG_USER} -d ${DB_NAME} --single-transaction --exit-on-error --clean --if-exists ${BACKUP}"
if [ "${RC}" = 0 ] && [ "${NOW}" = "${EXPECTED}" ]; then
  NEW_INFO="$(info "${TARGET}")" || NEW_INFO="? ? ? ? ? ?"
  read -r _ NEW_TABLES NEW_ROWS NEW_GOOSE _ _ <<<"${NEW_INFO}"
  echo "==> Done. ${TARGET} is now a copy of ${SOURCE}: ${NEW_TABLES} tables, ${NEW_ROWS} rows, migration ${NEW_GOOSE}; verified against the source."
  [ -z "${BACKUP}" ] || echo "    The old ${TARGET} is in ${BACKUP}. To go back: ${RESTORE_HINT}"
  echo "    Start the Pitbox app that uses ${TARGET} again."
  exit 0
fi
sed 's/^/    | /' "${WORK}/transaction.log" | tail -n 25 >&2
if [ "${NOW}" = "${BEFORE}" ]; then
  die "the transaction did not complete (psql exit ${RC}): ${TARGET} is unchanged${BACKUP:+ (backup: ${BACKUP})}"
fi
if [ "${NOW}" = "${EXPECTED}" ]; then
  die "psql reported a failure (exit ${RC}), but ${TARGET} holds the verified copy of ${SOURCE}: the commit went through${BACKUP:+ (the old ${TARGET} is in ${BACKUP})}"
fi
die "${TARGET} matches neither its old state nor the copy (psql exit ${RC}). Check it before starting the app${BACKUP:+; to go back: ${RESTORE_HINT}}"
