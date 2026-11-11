#!/usr/bin/env bash
# Tests the operator scripts in scripts/db-server against a throwaway
# Postgres 18 in Docker (never the real server): copies between scrap
# schemas, the public <-> dev wrappers, restoring a backup, every refusal
# and rollback path (each against a target that differs from the source,
# so a wrong commit shows), interrupts, writes racing the sync, attempts to
# run code as the superuser, and the temporary-schema cleanup.
# Needs docker, psql, pg_dump, setpriv and Go.
#
# Usage: scripts/test-db-server.sh   (from the repository root)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${TEST_PG_PORT:-55432}"
NAME="pitbox-db-server-test-$$"
WORK="$(mktemp -d)"
export SYNC_PG_HOST=127.0.0.1 SYNC_PG_PORT="${PORT}" SYNC_PG_USER=postgres SYNC_DB=stock_items SYNC_APP_USER=stock
export SYNC_BACKUP_DIR="${WORK}/backups" SYNC_LOCK_TIMEOUT=2s
# The container's data directory isn't on this machine.
export SYNC_SKIP_DATA_DIR_CHECK=1
export PGPASSFILE="${WORK}/pgpass"
: >"${PGPASSFILE}"
chmod 600 "${PGPASSFILE}"
unset PGHOST PGPORT PGUSER PGDATABASE PGPASSWORD
REAL_PSQL="$(command -v psql)"
SYNC="${ROOT}/scripts/db-server/sync_schema.sh"

cleanup() {
  docker rm -f "${NAME}" >/dev/null 2>&1 || true
  rm -rf "${WORK}"
}
trap cleanup EXIT

pass=0
fail=0
ok() { echo "  ok   $*"; pass=$((pass + 1)); }
bad() { echo "  FAIL $*"; fail=$((fail + 1)); }
check() { local what="$1"; shift; if "$@"; then ok "${what}"; else bad "${what}"; fi; }

q() { "${REAL_PSQL}" -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items "$@"; }
qa() { q -At "$@"; }
as_stock() { "${REAL_PSQL}" -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "${PORT}" -U stock -d stock_items "$@"; }

# fingerprint SCHEMA: each table's row count and a hash of its rows, the
# routines, and the sequences; equal fingerprints mean equal schemas.
fingerprint() {
  qa -v s="$1" <<'SQL'
SELECT string_agg(x, E'\n' ORDER BY x) FROM (
  SELECT format('%s %s %s', c.relname, (xpath('/row/n/text()', r))[1]::text, (xpath('/row/h/text()', r))[1]::text) AS x
  FROM pg_class c,
       LATERAL query_to_xml(format('SELECT count(*) AS n, md5(COALESCE(string_agg(to_jsonb(t)::text, '','' ORDER BY to_jsonb(t)::text), '''')) AS h FROM %I.%I t', :'s', c.relname), false, true, '') r
  WHERE c.relnamespace = to_regnamespace(:'s') AND c.relkind IN ('r', 'v')
  UNION ALL SELECT format('fn %s', proname) FROM pg_proc WHERE pronamespace = to_regnamespace(:'s')
  UNION ALL SELECT format('seq %s %s', sequencename, last_value) FROM pg_sequences WHERE schemaname = :'s'
) f;
SQL
}
# notowned SCHEMA counts the tables, sequences, views and routines not owned by stock.
notowned() {
  qa -v s="$1" <<'SQL'
SELECT (SELECT count(*) FROM pg_class WHERE relnamespace = to_regnamespace(:'s') AND relkind IN ('r', 'S', 'v') AND pg_get_userbyid(relowner) <> 'stock')
     + (SELECT count(*) FROM pg_proc WHERE pronamespace = to_regnamespace(:'s') AND pg_get_userbyid(proowner) <> 'stock');
SQL
}
owner() { qa -c "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = '$1'::regclass"; }
superuser() { qa -c "SELECT rolsuper FROM pg_roles WHERE rolname = 'stock'"; }
backups() { find "${SYNC_BACKUP_DIR}" -maxdepth 1 -name "stock_items-$1-*.dump" | wc -l; }
latest_backup() { find "${SYNC_BACKUP_DIR}" -maxdepth 1 -name "stock_items-$1-*.dump" | sort | tail -n 1; }

# run_sync SOURCE TARGET answers the prompt (OVERWRITE public for public).
run_sync() {
  local word="OVERWRITE"
  [ "${!#}" != public ] || word="OVERWRITE public"
  echo "${CONFIRM:-${word}}" | "${SYNC}" "$@" >"${WORK}/out.log" 2>&1
}
wrapper() { echo "$2" | "${ROOT}/scripts/db-server/$1" >"${WORK}/out.log" 2>&1; }
said() { grep -q -- "$1" "${WORK}/out.log" || { sed 's/^/       | /' "${WORK}/out.log" | tail -15; return 1; }; }
not_said() { ! grep -q -- "$1" "${WORK}/out.log" || { sed 's/^/       | /' "${WORK}/out.log" | tail -15; return 1; }; }

# mark_target gives scrap_dst a row the source doesn't have and takes its
# fingerprint: a sync that wrongly committed would remove the row.
mark_target() {
  q -c "INSERT INTO scrap_dst.shopping_entries (free_text) VALUES ('only in the target $RANDOM')"
  DST_BEFORE="$(fingerprint scrap_dst)"
}
# block_target SECONDS holds a lock on scrap_dst.items that the sync's
# transaction waits for (pg_dump and reads don't).
block_target() {
  ("${REAL_PSQL}" -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items \
     -c "BEGIN; LOCK TABLE scrap_dst.items IN ROW EXCLUSIVE MODE; SELECT pg_sleep($1); COMMIT;" >/dev/null 2>&1 &)
  sleep 1
}
# start_sync SOURCE TARGET runs a sync in its own process group (as from a
# terminal) and returns once it is waiting in its transaction. Its lock
# timeout is long, so only the interrupt can stop it before block_target's
# lock is released (and it would then commit).
start_sync() {
  SYNC_LOCK_TIMEOUT=60s setsid "${SYNC}" "$@" < <(echo OVERWRITE) >"${WORK}/out.log" 2>&1 &
  SPID=$!
  for _ in $(seq 1 150); do grep -q "==> 5" "${WORK}/out.log" && break; sleep 0.2; done
  sleep 1
}
# shim TOOL BODY puts a wrapper for TOOL first on PATH (in ${WORK}/shim):
# BODY runs with the arguments in $args, then the real tool.
shim() {
  rm -rf "${WORK}/shim"
  mkdir -p "${WORK}/shim"
  cat >"${WORK}/shim/$1" <<EOF
#!/usr/bin/env bash
args=" \$* "
$2
exec $(command -v "$1") "\$@"
EOF
  chmod +x "${WORK}/shim/$1"
}
# The SQL a shim runs to write into the target behind the sync's back.
late_write() { echo "${REAL_PSQL} -X -q -h 127.0.0.1 -p ${PORT} -U postgres -d stock_items -c \"INSERT INTO scrap_dst.shopping_entries (free_text) VALUES ('$1')\""; }

echo "==> Starting Postgres 18 in Docker on port ${PORT}..."
docker run -d --name "${NAME}" -e POSTGRES_HOST_AUTH_METHOD=trust -p "127.0.0.1:${PORT}:5432" postgres:18 >/dev/null
for _ in $(seq 1 60); do
  "${REAL_PSQL}" -X -h 127.0.0.1 -p "${PORT}" -U postgres -d postgres -c 'SELECT 1' >/dev/null 2>&1 && break
  sleep 1
done
"${REAL_PSQL}" -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "${PORT}" -U postgres -d postgres <<'SQL'
CREATE ROLE stock LOGIN;
CREATE ROLE reader;
CREATE ROLE no_login;
CREATE DATABASE stock_items;
GRANT CREATE ON DATABASE stock_items TO stock;
\c stock_items
-- As on the server: the app user owns public.
ALTER SCHEMA public OWNER TO stock;
SQL

echo "==> Migrating a scrap schema with the app and filling it..."
(cd "${ROOT}" && go build -o "${WORK}/pitbox" ./cmd/pitbox)
(cd "${WORK}" && env -i PATH="${PATH}" HOME="${WORK}" DB_HOST=127.0.0.1 DB_PORT="${PORT}" DB_DATABASE=stock_items \
  DB_USERNAME=stock DB_PASSWORD=x DB_SCHEMA=scrap_src ./pitbox -migrate-only >"${WORK}/migrate.log" 2>&1) || { cat "${WORK}/migrate.log"; exit 1; }
q <<'SQL'
SET search_path = scrap_src;
SET ROLE stock;
INSERT INTO vehicles (name, odometer, purchased_on, odometer_at_purchase) VALUES ('KTM 390', 12000, '2024-03-01', 10), ('Golf', NULL, NULL, NULL), ('Sold', 1, NULL, NULL);
INSERT INTO items (name, brand, unit, quantity, min_quantity, expires_on, location, notes) VALUES
  ('Rear Brake Pads', 'OEM', 'piece', 2, 1, NULL, 'Shelf', E'line one\nline "two"'), ('15W50', 'Silkolene', 'ml', 1525.5, 1000, '2029-06-30', '', ''),
  ('Gone', '', 'g', 0, NULL, NULL, '', '');
INSERT INTO item_skus (item_id, code, kind, brand, pack_size) VALUES (1, '77-038', 'oem', 'KTM', 2), (2, 'SK-1', 'barcode', '', 1000.5);
INSERT INTO item_vehicles (item_id, vehicle_id) VALUES (1, 1), (2, 1), (2, 2);
INSERT INTO item_photos (item_id, content_type, image, thumb) VALUES (1, 'image/jpeg', '\xffd8ff00ff', '\xffd8');
INSERT INTO service_kits (vehicle_id, name, interval_distance, interval_months) VALUES (1, 'Oil service', 7500, 12);
INSERT INTO service_kit_lines (kit_id, item_id, quantity) VALUES (1, 2, 1700);
INSERT INTO service_records (vehicle_id, kit_id, performed_on, odometer, title, labor_cost) VALUES (1, 1, '2026-05-01', 12000, 'Oil service', 80);
INSERT INTO stock_movements (item_id, kind, delta, total_price, supplier, occurred_on, service_record_id) VALUES
  (1, 'starting', 2, NULL, '', '2026-01-01', NULL), (2, 'bought', 2525.5, 19.90, 'Moto shop', '2026-03-01', NULL), (2, 'service', -1000, NULL, '', '2026-05-01', 1);
INSERT INTO shopping_entries (item_id, free_text, quantity, unit, note) VALUES (NULL, 'Coolant', 4000, 'ml', '');
INSERT INTO trash (kind, label, payload) SELECT 'vehicle', 'Vehicle: Sold', jsonb_build_object('vehicles', jsonb_agg(to_jsonb(v))) FROM vehicles v WHERE id = 3;
DELETE FROM vehicles WHERE id = 3;
INSERT INTO form_submissions (key, result_id) VALUES (md5('a'), 1);
-- A dropped column (the copy numbers its columns afresh) and a view.
ALTER TABLE item_skus ADD COLUMN scratch int;
ALTER TABLE item_skus DROP COLUMN scratch;
CREATE VIEW low_stock AS SELECT id, name FROM items WHERE quantity < min_quantity;
-- An array slice that reads like a psql variable (:s), which the restore
-- must not substitute.
CREATE VIEW slice AS SELECT (ARRAY[1, 2, 3])[1:s] AS a FROM (SELECT 2 AS s) x;
RESET ROLE;
-- Something not owned by stock: the copy is stock's, the source stays as it is.
CREATE TABLE scrap_src.postgres_owned (id int PRIMARY KEY);
INSERT INTO scrap_src.postgres_owned VALUES (1);
SQL
SRC_BEFORE="$(fingerprint scrap_src)"

echo "==> 1. Copy to a new schema"
run_sync scrap_src scrap_dst || true
check "the copy commits" said "Done. scrap_dst is now a copy of scrap_src"
check "nothing to back up for a new schema" said "doesn't exist yet: nothing to back up"
check "the copy holds the same rows, views, functions and sequences" [ "$(fingerprint scrap_dst)" = "$(fingerprint scrap_src | sed 's/scrap_src/scrap_dst/g')" ]
check "stock owns everything in the copy" [ "$(notowned scrap_dst)" = 0 ]
check "the source is untouched: its postgres-owned table still is" [ "$(owner scrap_src.postgres_owned)" = postgres ]
check "the source's data is unchanged" [ "$(fingerprint scrap_src)" = "${SRC_BEFORE}" ]
check "the view with [1:s] came through as it was" [ "$(qa -c "SELECT a::text FROM scrap_dst.slice")" = "{1,2}" ]
check "the copy's ids continue from the source's sequences" [ "$(as_stock -At -c "INSERT INTO scrap_dst.vehicles (name) VALUES ('New') RETURNING id")" = 4 ]
check "the copy runs reorder_columns" as_stock -At -c "BEGIN; SELECT scrap_dst.reorder_columns('scrap_dst.item_vehicles', ARRAY['vehicle_id', 'item_id']); ROLLBACK;"
check "a log of the run is kept" eval "ls \"${SYNC_BACKUP_DIR}\"/pitbox-sync-*-scrap_src-to-scrap_dst.log >/dev/null"
check "no staging database is left" [ -z "$(qa -c "SELECT datname FROM pg_database WHERE datname LIKE 'pitbox_sync_staging_%'")" ]
q -c "DELETE FROM scrap_dst.vehicles WHERE id = 4"

echo "==> 2. Copy over an existing schema that holds reorder_columns (the bug of migration 00022)"
run_sync scrap_src scrap_dst || true
check "the copy commits" said "Done. scrap_dst is now a copy of scrap_src"
BACKUP="$(latest_backup scrap_dst)"
check "the old schema was backed up" [ -n "${BACKUP}" ]
check "the backup reads back completely" eval "pg_restore -f /dev/null \"${BACKUP}\""
check "no partial backup or work files are left" [ -z "$(find "${SYNC_BACKUP_DIR}" -name '*.partial' -o -name 'pitbox-sync-work.*')" ]
check "it says how to go back, through this script" said "--from-backup ${BACKUP} scrap_dst"
check "the copy holds the same rows" [ "$(fingerprint scrap_dst)" = "$(fingerprint scrap_src | sed 's/scrap_src/scrap_dst/g')" ]

echo "==> 3. Answering anything but OVERWRITE changes nothing"
mark_target
CONFIRM=no run_sync scrap_src scrap_dst
check "it says it aborted" said "Aborted. No changes were made"
: | "${SYNC}" scrap_src scrap_dst >"${WORK}/out.log" 2>&1 || true
check "no answer at all aborts too" said "Aborted. No changes were made"
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 4. An object the clearing doesn't drop rolls everything back"
mark_target
as_stock -c "CREATE COLLATION scrap_dst.c_bytes (provider = libc, locale = 'C')"
if run_sync scrap_src scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "it names the object" said "c_bytes (collation)"
check "it says this sync changed nothing" said "this sync changed nothing, and scrap_dst is as it was"
check "and gives no restore advice" not_said "from-backup"
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
q -c "DROP COLLATION scrap_dst.c_bytes"

echo "==> 5. Other failures roll everything back"
mark_target
q <<'SQL'
CREATE SCHEMA scrap_bad AUTHORIZATION stock;
CREATE TABLE scrap_bad.goose_db_version (id serial, version_id bigint, is_applied boolean);
INSERT INTO scrap_bad.goose_db_version (version_id, is_applied) VALUES (22, true);
ALTER TABLE scrap_bad.goose_db_version OWNER TO stock;
-- Only a superuser may create a function in an untrusted language: copying
-- it as stock fails (in staging, before anything is touched).
CREATE FUNCTION scrap_bad.pg_ver() RETURNS text LANGUAGE internal AS 'pgsql_version';
SQL
if run_sync scrap_bad scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "the error is the copy's" said "permission denied for language internal"
check "it says nothing was changed" said "nothing was changed"
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
# A failure inside the transaction itself: a grant the copy would lose.
q -c "GRANT SELECT ON scrap_dst.items TO reader; COMMENT ON TABLE scrap_dst.items IS 'parts'"
if run_sync scrap_src scrap_dst; then bad "losing a grant should roll back"; else check "a grant or comment the copy would lose rolls it back" said "would lose these grants or comments"; fi
check "it names them" said "grant items reader SELECT"
check "it says this sync changed nothing" said "this sync changed nothing, and scrap_dst is as it was"
check "the grant is still there" [ "$(qa -c "SELECT has_table_privilege('reader', 'scrap_dst.items', 'SELECT')")" = t ]
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
as_stock -c "ALTER DEFAULT PRIVILEGES IN SCHEMA scrap_dst GRANT SELECT ON TABLES TO reader"
q -c "COMMENT ON TABLE scrap_dst.items IS NULL"
run_sync scrap_src scrap_dst || true
check "grants the schema's default privileges give again are fine" said "Done. scrap_dst is now a copy of scrap_src"
check "and the grant is there after the copy" [ "$(qa -c "SELECT has_table_privilege('reader', 'scrap_dst.items', 'SELECT')")" = t ]
as_stock -c "ALTER DEFAULT PRIVILEGES IN SCHEMA scrap_dst REVOKE SELECT ON TABLES FROM reader" -c "REVOKE SELECT ON ALL TABLES IN SCHEMA scrap_dst FROM reader"

echo "==> 6. A lock held by the app makes it stop, not wait"
mark_target
block_target 8
if run_sync scrap_src scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "on the lock timeout" said "lock timeout"
check "it says this sync changed nothing" said "this sync changed nothing"
sleep 7
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 7. A lock on the source (the app on public) doesn't hold it up"
("${REAL_PSQL}" -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "BEGIN; LOCK TABLE scrap_src.postgres_owned IN ROW EXCLUSIVE MODE; SELECT pg_sleep(6); COMMIT;" >/dev/null 2>&1 &)
sleep 1
START=$(date +%s)
run_sync scrap_src scrap_dst || true
check "the copy commits" said "Done. scrap_dst is now a copy of scrap_src"
check "without waiting for the source's lock" [ $(($(date +%s) - START)) -lt 5 ]
sleep 5

echo "==> 8. Refusals before anything happens"
mark_target
q -c "CREATE SCHEMA scrap_empty AUTHORIZATION stock"
if run_sync scrap_empty scrap_dst; then bad "an empty source should be refused"; else check "an empty source is refused" said "has no tables"; fi
if run_sync scrap_nope scrap_dst; then bad "a missing source should be refused"; else check "a missing source is refused" said "copying scrap_nope into staging failed"; fi
if run_sync scrap_src scrap_src; then bad "the same schema should be refused"; else check "copying a schema onto itself is refused" said "same schema"; fi
if run_sync scrap_src pg_catalog; then bad "pg_catalog should be refused"; else check "a system schema is refused" said "not a schema this script copies to"; fi
if run_sync 'scrap_src; DROP' scrap_dst; then bad "an odd name should be refused"; else check "an odd name is refused" said "not a schema this script copies from"; fi
q -c "UPDATE scrap_bad.goose_db_version SET is_applied = false" -c "DROP FUNCTION scrap_bad.pg_ver()"
if run_sync scrap_bad scrap_dst; then bad "a source without migrations should be refused"; else check "a source without migrations is refused" said "no applied migrations"; fi
if SYNC_LOCK_TIMEOUT=0 run_sync scrap_src scrap_dst; then bad "lock timeout 0 should be refused"; else check "a lock timeout of 0 (no limit) is refused" said "0 would mean no limit"; fi
if SYNC_APP_USER=no_login run_sync scrap_src scrap_dst; then bad "an app user that can't log in should be refused"; else check "it needs to log in as the app user" said "can't log in as no_login"; fi
if SYNC_SKIP_DATA_DIR_CHECK= run_sync scrap_src scrap_dst; then bad "an unchecked data directory should be refused"; else check "a data directory it can't check stops it" said "isn't on this machine"; fi
q -c "CREATE TABLE scrap_dst.root_owned (x int)"
if run_sync scrap_src scrap_dst; then bad "an object stock doesn't own should be refused"; else check "an object in the target stock doesn't own is refused" said "holds objects stock doesn't own: root_owned"; fi
q -c "DROP TABLE scrap_dst.root_owned"
exec 8>"${SYNC_BACKUP_DIR}/.pitbox-schema-sync.lock"; flock 8
if run_sync scrap_src scrap_dst; then bad "a second run should be refused"; else check "a second run at the same time is refused" said "another schema sync is running"; fi
exec 8>&-
# A failing query about the target (here: too many connections) must stop
# it before asking, not skip the backup and the checks.
BACKUPS_BEFORE="$(backups scrap_dst)"
shim psql 'case "${args}" in *" -d stock_items "*" info_schema=scrap_dst "*) echo "psql: FATAL:  sorry, too many clients already" >&2; exit 2 ;; esac'
if PATH="${WORK}/shim:${PATH}" run_sync scrap_src scrap_dst; then bad "a failing check should stop it"; else check "a failing query about the target stops it" said "could not read schema scrap_dst"; fi
check "before asking anything" not_said "CRITICAL ACTION"
# Objects in another schema that depend on the target (CASCADE would drop
# them, and a column's data).
q <<'SQL'
CREATE SCHEMA scrap_other;
CREATE VIEW scrap_other.v AS SELECT id FROM scrap_dst.items;
CREATE TABLE scrap_other.t (item_id bigint REFERENCES scrap_dst.items (id), snap scrap_dst.vehicles);
INSERT INTO scrap_other.t (item_id, snap) SELECT 1, v FROM scrap_dst.vehicles v WHERE id = 1;
SQL
if run_sync scrap_src scrap_dst; then bad "dependents elsewhere should be refused"; else check "objects elsewhere that depend on the target are refused" said "objects outside scrap_dst depend on it"; fi
check "they are named" said "view scrap_other.v"
check "and kept, with their data" [ "$(qa -c "SELECT count(*) FROM scrap_other.v") $(qa -c "SELECT count(snap) FROM scrap_other.t")" = "3 1" ]
q -c "DROP SCHEMA scrap_other CASCADE"
check "no backup was taken by the refused runs" [ "$(backups scrap_dst)" = "${BACKUPS_BEFORE}" ]
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 9. No code from the database runs as the superuser"
run_sync scrap_src scrap_evil || true
as_stock <<'SQL'
-- Tries to undo SET ROLE, then makes stock a superuser if it can.
CREATE FUNCTION scrap_evil.probe(x int) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    BEGIN EXECUTE 'RESET ROLE'; EXCEPTION WHEN OTHERS THEN NULL; END;
    BEGIN EXECUTE 'RESET SESSION AUTHORIZATION'; EXCEPTION WHEN OTHERS THEN NULL; END;
    IF (SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user) THEN
        EXECUTE 'ALTER ROLE stock SUPERUSER';
    END IF;
    RETURN true;
END $$;
CREATE TABLE scrap_evil.trap (x int CHECK (scrap_evil.probe(x)));
INSERT INTO scrap_evil.trap VALUES (1);
CREATE INDEX trap_idx ON scrap_evil.trap ((scrap_evil.probe(x)));
SQL
run_sync scrap_evil scrap_evil_copy || true
check "a source whose CHECKs and indexes run code is copied" said "Done. scrap_evil_copy is now a copy of scrap_evil"
check "without that code ever running as a superuser" [ "$(superuser)" = f ]
run_sync scrap_evil scrap_evil_copy || true
check "also when copied over a schema holding it" [ "$(superuser)" = f ]
# An aggregate named max in a schema stock owns, for a column named
# created_at of its own type (a text domain whose value reads as a time),
# which the summary before the prompt reads.
as_stock <<'SQL'
CREATE DOMAIN public.stamp AS text;
CREATE FUNCTION public.evil_max(m public.stamp, n public.stamp) RETURNS public.stamp LANGUAGE plpgsql AS $$
BEGIN
    IF (SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user) THEN
        EXECUTE 'ALTER ROLE stock SUPERUSER';
    END IF;
    RETURN n;
END $$;
CREATE AGGREGATE public.max(public.stamp) (sfunc = public.evil_max, stype = public.stamp);
CREATE TABLE scrap_dst.trap (created_at public.stamp);
INSERT INTO scrap_dst.trap VALUES ('2026-01-01 00:00:00+00');
SQL
CONFIRM=no run_sync scrap_src scrap_dst || true
check "the summary before the prompt runs none of it" [ "$(superuser)" = f ]
as_stock -c "DROP TABLE scrap_dst.trap" -c "DROP AGGREGATE public.max(public.stamp)" -c "DROP FUNCTION public.evil_max(public.stamp, public.stamp)" -c "DROP DOMAIN public.stamp"
q -c "DROP SCHEMA scrap_evil CASCADE" -c "DROP SCHEMA scrap_evil_copy CASCADE"

echo "==> 10. Writes to the target after the backup make it roll back, losing none"
mark_target
# A write just before the transaction starts (after the backup): the
# transaction's own check must catch it.
shim psql "case \"\${args}\" in *\" --single-transaction \"*) $(late_write 'late write') ;; esac"
if PATH="${WORK}/shim:${PATH}" run_sync scrap_src scrap_dst; then bad "a target written to after the backup should not be replaced"; else ok "a target written to after the backup is not replaced"; fi
check "the transaction notices" said "was changed after it was backed up"
check "the end says this sync changed nothing, something else wrote" said "something else wrote to it since; do NOT restore the backup"
check "and gives no restore advice" not_said "from-backup"
check "the late write is still there" [ "$(qa -c "SELECT count(*) FROM scrap_dst.shopping_entries WHERE free_text = 'late write'")" = 1 ]
q -c "DELETE FROM scrap_dst.shopping_entries WHERE free_text = 'late write'"
# A write while the backup is taken.
shim pg_dump "case \"\${args}\" in *\" -Fc \"*) $(late_write 'write during backup') ;; esac"
if PATH="${WORK}/shim:${PATH}" run_sync scrap_src scrap_dst; then bad "a target written to during the backup should not be replaced"; else check "a write during the backup stops it" said "changed while it was being backed up"; fi
check "the write is still there" [ "$(qa -c "SELECT count(*) FROM scrap_dst.shopping_entries WHERE free_text = 'write during backup'")" = 1 ]
q -c "DELETE FROM scrap_dst.shopping_entries WHERE free_text = 'write during backup'"

echo "==> 11. Interrupts during the transaction roll it back and are reported"
for sig in INT HUP TERM; do
  mark_target
  block_target 10
  start_sync scrap_src scrap_dst
  if [ "${sig}" = TERM ]; then kill -TERM "${SPID}"; else kill "-${sig}" -- "-${SPID}"; fi
  wait "${SPID}" && bad "${sig}: the sync should fail" || ok "${sig}: the sync fails"
  # Ctrl-C reaches psql too, which cancels and rolls back by itself.
  [ "${sig}" = INT ] || check "${sig}: it stops psql" said "Interrupted: stopped psql"
  LOG="$(ls -t "${SYNC_BACKUP_DIR}"/pitbox-sync-*.log | head -n 1)"
  check "${sig}: the end says this sync changed nothing, in the log too" grep -q "this sync changed nothing" "${LOG}"
  sleep 9
  check "${sig}: the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
done
# psql must not be able to commit on its own: killed outright (kill -9), the
# script can't stop it, but psql dies with it.
mark_target
block_target 8
start_sync scrap_src scrap_dst
kill -KILL "${SPID}"
wait "${SPID}" || true
sleep 8
check "kill -9: the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
run_sync scrap_src scrap_dst || true
check "kill -9: the next run clears the staging database left behind" said "Dropping pitbox_sync_staging_${SPID}"
check "kill -9: and works" said "Done. scrap_dst is now a copy of scrap_src"
check "kill -9: no work files are left" [ -z "$(find "${SYNC_BACKUP_DIR}" -name 'pitbox-sync-work.*')" ]
# When terminating the session fails too, psql still dies, its session
# never commits, and the end says it can't tell yet.
mark_target
block_target 8
shim psql 'case "${args}" in *" a=pitbox-schema-sync "*) exit 2 ;; esac'
PATH="${WORK}/shim:${PATH}" start_sync scrap_src scrap_dst
kill -TERM "${SPID}"
wait "${SPID}" || true
check "a failed terminate: it says it can't tell whether it committed" said "could not find out whether the transaction committed"
sleep 8
check "a failed terminate: the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 12. Settings that change how values print don't matter"
q -c "ALTER DATABASE stock_items SET timezone = 'Pacific/Chatham'" -c "ALTER DATABASE stock_items SET bytea_output = 'escape'" -c "ALTER DATABASE stock_items SET DateStyle = 'SQL, DMY'"
PGHOSTADDR=10.255.255.1 PGOPTIONS='-c statement_timeout=1ms' PGDATABASE=postgres run_sync scrap_src scrap_dst || true
check "database settings and PG* variables in the environment don't change it" said "Done. scrap_dst is now a copy of scrap_src"
q -c "ALTER DATABASE stock_items RESET ALL"
q -d postgres -c "CREATE DATABASE pitbox_sync_staging_424242"
mkdir -p "${SYNC_BACKUP_DIR}/pitbox-sync-work.leftover"; touch "${SYNC_BACKUP_DIR}/stock_items-scrap_dst-x.dump.partial" "${SYNC_BACKUP_DIR}/work.someone-elses"
run_sync scrap_src scrap_dst || true
check "a leftover staging database is dropped" said "Dropping pitbox_sync_staging_424242"
check "leftover work files are removed" [ -z "$(find "${SYNC_BACKUP_DIR}" -name 'pitbox-sync-work.leftover' -o -name '*.partial')" ]
check "files that aren't its own are left alone" [ -e "${SYNC_BACKUP_DIR}/work.someone-elses" ]

echo "==> 13. The wrappers, public -> dev and dev -> public, and restoring a backup"
run_sync scrap_src public || true
check "a copy into public commits" said "Done. public is now a copy of scrap_src"
run_sync scrap_src dev || true
DEV_BEFORE="$(fingerprint dev)"
# public gains a table dev's backup won't have (with a foreign key to items).
as_stock -c "CREATE TABLE public.extra (item_id bigint REFERENCES public.items (id))" -c "INSERT INTO public.extra VALUES (1)" -c "INSERT INTO public.goose_db_version (version_id, is_applied) VALUES (23, true)"
wrapper sync_stock_items_public_to_dev.sh OVERWRITE || true
check "public -> dev commits" said "Done. dev is now a copy of public"
check "dev holds public's rows" [ "$(fingerprint dev)" = "$(fingerprint public | sed 's/\bpublic\b/dev/g')" ]
DEV_BACKUP="$(latest_backup dev)"
echo OVERWRITE | "${SYNC}" --from-backup "${DEV_BACKUP}" dev >"${WORK}/out.log" 2>&1 || true
check "restoring dev's backup over it works (the new table and all)" said "Done. dev is now a copy of ${DEV_BACKUP}"
check "dev is back as it was" [ "$(fingerprint dev)" = "${DEV_BEFORE}" ]
echo OVERWRITE | "${SYNC}" --from-backup "${DEV_BACKUP}" scrap_dst >"${WORK}/out.log" 2>&1 && bad "a backup of another schema should be refused" || true
check "a backup of another schema is refused" said "holds schema(s) 'dev', not scrap_dst"
wrapper sync_stock_items_public_to_dev.sh OVERWRITE || true
as_stock -c "UPDATE dev.items SET notes = 'changed in dev', version = version + 1 WHERE id = 1"
PUB_BEFORE="$(fingerprint public)"
wrapper sync_stock_items_dev_to_public.sh OVERWRITE || true
check "public needs 'OVERWRITE public'" said "Aborted. No changes were made"
("${REAL_PSQL}" -X -q -h 127.0.0.1 -p "${PORT}" -U stock -d stock_items -c "SELECT pg_sleep(5)" >/dev/null 2>&1 &)
sleep 1
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" && bad "public should be refused while the app is connected" || true
check "public is refused while the app user is connected" said "stock is connected to stock_items"
sleep 5
check "public is unchanged" [ "$(fingerprint public)" = "${PUB_BEFORE}" ]
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" || true
check "dev -> public commits" said "Done. public is now a copy of dev"
check "public has dev's change" [ "$(qa -c "SELECT notes FROM public.items WHERE id = 1")" = "changed in dev" ]
check "public's old state was backed up" [ "$(backups public)" -ge 1 ]
# Changes to the source while the prompt waits don't reach the target: the
# copy shown is the copy made.
mkfifo "${WORK}/answer"
"${ROOT}/scripts/db-server/sync_stock_items_dev_to_public.sh" <"${WORK}/answer" >"${WORK}/out.log" 2>&1 &
SPID=$!
exec 7>"${WORK}/answer"
for _ in $(seq 1 150); do grep -q "CRITICAL ACTION" "${WORK}/out.log" && break; sleep 0.2; done
as_stock -c "INSERT INTO dev.goose_db_version (version_id, is_applied) VALUES (999, true)" -c "ALTER TABLE dev.items ADD COLUMN sneaked int"
echo "OVERWRITE public" >&7
exec 7>&-
wait "${SPID}" || true
check "a sync confirmed after the source changed commits what was shown" said "Done. public is now a copy of dev"
check "without the later migration or column" [ "$(qa -c "SELECT count(*) FROM pg_attribute WHERE attrelid = 'public.items'::regclass AND attname = 'sneaked'")-$(qa -c "SELECT max(version_id) FROM public.goose_db_version")" = "0-23" ]
PUB_BEFORE="$(fingerprint public)"
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" && bad "a dev with unreleased migrations over public should be refused" || true
check "a migration public doesn't have is refused" said "different migrations applied"
as_stock -c "DELETE FROM dev.goose_db_version WHERE version_id IN (999, 23)" -c "ALTER TABLE dev.items DROP COLUMN sneaked"
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" && bad "an older dev over public should be refused" || true
check "an older migration set over public is refused" said "different migrations applied"
check "public is unchanged" [ "$(fingerprint public)" = "${PUB_BEFORE}" ]
as_stock -c "INSERT INTO scrap_dst.goose_db_version (version_id, is_applied) VALUES (30, true)"
run_sync dev scrap_dst || true
check "an older migration over another schema goes ahead with a note" said "scrap_dst is at migration 30, newer than the copy (22); it goes back to 22"
PUB_BACKUP="$(latest_backup public)"
echo "OVERWRITE public" | "${SYNC}" --from-backup "${PUB_BACKUP}" public >"${WORK}/out.log" 2>&1 || true
check "public can go back to its backup" said "Done. public is now a copy of ${PUB_BACKUP}"

echo "==> 14. The manifest the sync checks the copy against notices any change"
# The query exactly as sync_schema.sh has it.
MANIFEST_SQL="$(sed -n "/^MANIFEST_SQL=\$(cat <<'SQL'\$/,/^SQL\$/p" "${SYNC}" | sed '1d;$d')"
manifest() { printf '%s\n' "${MANIFEST_SQL}" | qa -v pitbox_sync_schema="$1"; }
M0="$(manifest scrap_dst)"
check "it is the same when nothing changed" [ "$(manifest scrap_dst)" = "${M0}" ]
check "and doesn't depend on the session's time zone" [ "$(PGTZ=Asia/Kathmandu manifest scrap_dst)" = "${M0}" ]
q -c "UPDATE scrap_dst.items SET notes = notes || '.' WHERE id = 2"
check "one changed value changes it" [ "$(manifest scrap_dst)" != "${M0}" ]
q -c "UPDATE scrap_dst.items SET notes = rtrim(notes, '.') WHERE id = 2"
check "and changing it back restores it" [ "$(manifest scrap_dst)" = "${M0}" ]
as_stock -c "CREATE OR REPLACE VIEW scrap_dst.low_stock AS SELECT id, name FROM scrap_dst.items WHERE quantity <= min_quantity"
check "a view's definition changes it" [ "$(manifest scrap_dst)" != "${M0}" ]
q -c "SELECT setval('scrap_dst.items_id_seq', 99)" >/dev/null
M1="$(manifest scrap_dst)"
q -c "SELECT setval('scrap_dst.items_id_seq', 99, false)" >/dev/null
check "a sequence's is_called changes it" [ "$(manifest scrap_dst)" != "${M1}" ]
as_stock -c "CREATE OR REPLACE FUNCTION scrap_dst.reorder_columns(tbl regclass, cols text[]) RETURNS void LANGUAGE plpgsql AS 'BEGIN END'"
M1="$(manifest scrap_dst)"
as_stock -c "CREATE OR REPLACE FUNCTION scrap_dst.reorder_columns(tbl regclass, cols text[]) RETURNS void LANGUAGE plpgsql AS 'BEGIN NULL; END'"
check "a function body changes it" [ "$(manifest scrap_dst)" != "${M1}" ]

echo "==> 15. Cleaning up temporary schemas"
for _ in 1 2 3; do q -c "CREATE TEMP TABLE t (x int)"; done
# A session that used a temp table and keeps its (now empty) temp schema.
("${REAL_PSQL}" -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "CREATE TEMP TABLE busy (x int)" -c "DROP TABLE busy" -c "SELECT pg_sleep(5)" >/dev/null 2>&1 &)
sleep 1
TEMP_BEFORE="$(qa -c "SELECT count(*) FROM pg_namespace WHERE nspname ~ '^pg_(toast_)?temp_'")"
"${ROOT}/scripts/db-server/cleanup_temp_schemas.sh" >"${WORK}/out.log" 2>&1 && bad "the clean-up should refuse while a session is connected" || true
check "it refuses while another session is connected" said "Other sessions are connected"
check "and drops nothing" [ "$(qa -c "SELECT count(*) FROM pg_namespace WHERE nspname ~ '^pg_(toast_)?temp_'")" = "${TEMP_BEFORE}" ]
sleep 5
"${ROOT}/scripts/db-server/cleanup_temp_schemas.sh" >"${WORK}/out.log" 2>&1 || true
check "with no one connected it drops them" said "Dropped ${TEMP_BEFORE} temporary schemas"
check "none are left" [ "$(qa -c "SELECT count(*) FROM pg_namespace WHERE nspname ~ '^pg_(toast_)?temp_'")" = 0 ]
check "and no catalog row is left without its schema" said "No catalog rows without a schema"
check "pg_dump still works" eval "pg_dump -h 127.0.0.1 -p \"${PORT}\" -U postgres -d stock_items --schema=scrap_src -f /dev/null"
check "Postgres makes them again when needed" [ "$(qa -c "CREATE TEMP TABLE again (x int); SELECT pg_my_temp_schema() <> 0")" = t ]

echo
echo "${pass} passed, ${fail} failed"
[ "${fail}" = 0 ]
