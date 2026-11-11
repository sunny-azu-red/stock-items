#!/usr/bin/env bash
# Tests the operator scripts in scripts/db-server against a throwaway
# Postgres 18 in Docker (never the real server): copies between scrap
# schemas, the public <-> dev wrappers, every refusal and rollback path, and
# the temporary-schema cleanup. Needs docker, psql, pg_dump and Go.
#
# Usage: scripts/test-db-server.sh   (from the repository root)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${TEST_PG_PORT:-55432}"
NAME="pitbox-db-server-test-$$"
WORK="$(mktemp -d)"
export SYNC_PG_HOST=127.0.0.1 SYNC_PG_PORT="${PORT}" SYNC_PG_USER=postgres SYNC_DB=stock_items SYNC_APP_USER=stock
export SYNC_BACKUP_DIR="${WORK}/backups" SYNC_LOCK_FILE="${WORK}/sync.lock" SYNC_LOCK_TIMEOUT=2s
export PGPASSFILE="${WORK}/pgpass"
: >"${PGPASSFILE}"
chmod 600 "${PGPASSFILE}"
unset PGHOST PGPORT PGUSER PGDATABASE PGPASSWORD

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

q() { psql -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items "$@"; }
qa() { q -At "$@"; }

# fingerprint SCHEMA: each table's row count and a hash of its rows, the
# routines, and who owns what; equal fingerprints mean equal schemas.
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
# run_sync SOURCE TARGET answers the prompt (OVERWRITE public for public).
run_sync() {
  local word="OVERWRITE"
  [ "$2" != public ] || word="OVERWRITE public"
  echo "${CONFIRM:-${word}}" | "${ROOT}/scripts/db-server/sync_schema.sh" "$@" >"${WORK}/out.log" 2>&1
}
wrapper() { echo "$2" | "${ROOT}/scripts/db-server/$1" >"${WORK}/out.log" 2>&1; }
# owner SCHEMA.TABLE prints the table's owner.
owner() { qa -c "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = '$1'::regclass"; }
# mark_target gives scrap_dst a row the source doesn't have and takes its
# fingerprint: a sync that wrongly committed would remove the row.
mark_target() {
  q -c "INSERT INTO scrap_dst.shopping_entries (free_text) VALUES ('only in the target $RANDOM')"
  DST_BEFORE="$(fingerprint scrap_dst)"
}
backups() { find "${SYNC_BACKUP_DIR}" -maxdepth 1 -name "stock_items-$1-*.dump" | wc -l; }
said() { grep -q -- "$1" "${WORK}/out.log" || { sed 's/^/       | /' "${WORK}/out.log" | tail -15; return 1; }; }

echo "==> Starting Postgres 18 in Docker on port ${PORT}..."
docker run -d --name "${NAME}" -e POSTGRES_HOST_AUTH_METHOD=trust -p "127.0.0.1:${PORT}:5432" postgres:18 >/dev/null
for _ in $(seq 1 60); do
  psql -X -h 127.0.0.1 -p "${PORT}" -U postgres -d postgres -c 'SELECT 1' >/dev/null 2>&1 && break
  sleep 1
done
psql -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "${PORT}" -U postgres -d postgres <<'SQL'
CREATE ROLE stock LOGIN;
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
CREATE ROLE reader;
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
check "the copy's ids continue from the source's sequences" [ "$(qa -c "SET ROLE stock; INSERT INTO scrap_dst.vehicles (name) VALUES ('New') RETURNING id")" = 4 ]
check "the copy runs reorder_columns" qa -c "SET ROLE stock; BEGIN; SELECT scrap_dst.reorder_columns('scrap_dst.item_vehicles', ARRAY['vehicle_id', 'item_id']); ROLLBACK;"
check "a log of the run is kept" eval "ls \"${SYNC_BACKUP_DIR}\"/sync-*-scrap_src-to-scrap_dst.log >/dev/null"
q -c "DELETE FROM scrap_dst.vehicles WHERE id = 4"

echo "==> 2. Copy over an existing schema that holds reorder_columns (the bug of migration 00022)"
run_sync scrap_src scrap_dst || true
check "the copy commits" said "Done. scrap_dst is now a copy of scrap_src"
BACKUP="$(ls "${SYNC_BACKUP_DIR}"/stock_items-scrap_dst-*.dump 2>/dev/null | head -1)"
check "the old schema was backed up" [ -n "${BACKUP}" ]
check "the backup reads back completely" eval "pg_restore -f /dev/null \"${BACKUP}\""
check "no partial backup or work files are left" [ -z "$(find "${SYNC_BACKUP_DIR}" -name '*.partial' -o -name 'work.*')" ]
check "it says how to go back" said "pg_restore -h"
check "the copy holds the same rows" [ "$(fingerprint scrap_dst)" = "$(fingerprint scrap_src | sed 's/scrap_src/scrap_dst/g')" ]
DST_BEFORE="$(fingerprint scrap_dst)"

echo "==> 3. Answering anything but OVERWRITE changes nothing"
mark_target
CONFIRM=no run_sync scrap_src scrap_dst
check "it says it aborted" said "Aborted. No changes were made"
: | "${ROOT}/scripts/db-server/sync_schema.sh" scrap_src scrap_dst >"${WORK}/out.log" 2>&1 || true
check "no answer at all aborts too" said "Aborted. No changes were made"
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 4. An object the clearing doesn't drop rolls everything back"
mark_target
q -c "CREATE COLLATION scrap_dst.c_bytes (provider = libc, locale = 'C')"
if run_sync scrap_src scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "it names the object" said "c_bytes (collation)"
check "it says the target is unchanged" said "scrap_dst is unchanged"
check "the target's tables and rows are still there" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
q -c "DROP COLLATION scrap_dst.c_bytes"

echo "==> 5. A failure in the middle of the restore rolls back the clearing too"
mark_target
q <<'SQL'
CREATE SCHEMA scrap_bad;
CREATE TABLE scrap_bad.goose_db_version (id serial, version_id bigint, is_applied boolean);
INSERT INTO scrap_bad.goose_db_version (version_id, is_applied) VALUES (22, true);
-- Only a superuser may create a function in an untrusted language: restoring
-- it as stock fails after the target was cleared.
CREATE FUNCTION scrap_bad.pg_ver() RETURNS text LANGUAGE internal AS 'pgsql_version';
SQL
if run_sync scrap_bad scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "the error is the restore's" said "permission denied for language internal"
check "it says the target is unchanged" said "scrap_dst is unchanged"
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 6. A lock held by the app makes it stop, not wait"
mark_target
(psql -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "BEGIN; LOCK TABLE scrap_dst.items IN ROW EXCLUSIVE MODE; SELECT pg_sleep(8); COMMIT;" >/dev/null 2>&1 &)
sleep 1
if run_sync scrap_src scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "on the lock timeout" said "lock timeout"
sleep 7
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 7. A lock on the source (the app on public) doesn't hold it up"
(psql -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "BEGIN; LOCK TABLE scrap_src.postgres_owned IN ROW EXCLUSIVE MODE; SELECT pg_sleep(6); COMMIT;" >/dev/null 2>&1 &)
sleep 1
START=$(date +%s)
run_sync scrap_src scrap_dst || true
check "the copy commits" said "Done. scrap_dst is now a copy of scrap_src"
check "without waiting for the source's lock" [ $(($(date +%s) - START)) -lt 5 ]
sleep 5

echo "==> 8. Refusals before anything happens"
mark_target
q -c "CREATE SCHEMA scrap_empty"
if run_sync scrap_empty scrap_dst; then bad "an empty source should be refused"; else check "an empty source is refused" said "has no tables"; fi
if run_sync scrap_nope scrap_dst; then bad "a missing source should be refused"; else check "a missing source is refused" said "does not exist"; fi
if run_sync scrap_src scrap_src; then bad "the same schema should be refused"; else check "copying a schema onto itself is refused" said "same schema"; fi
if run_sync scrap_src pg_catalog; then bad "pg_catalog should be refused"; else check "a system schema is refused" said "not a schema this script copies to"; fi
if run_sync 'scrap_src; DROP' scrap_dst; then bad "an odd name should be refused"; else check "an odd name is refused" said "not a schema this script copies from"; fi
q -c "UPDATE scrap_bad.goose_db_version SET is_applied = false"
if run_sync scrap_bad scrap_dst; then bad "a source without migrations should be refused"; else check "a source without migrations is refused" said "no applied migrations"; fi
exec 8>"${SYNC_LOCK_FILE}"; flock 8
if run_sync scrap_src scrap_dst; then bad "a second run should be refused"; else check "a second run at the same time is refused" said "another schema sync is running"; fi
exec 8>&-
# A failing query about the target (here: too many connections) must stop
# it, not skip the backup and the checks (audit finding).
mkdir -p "${WORK}/shim"
cat >"${WORK}/shim/psql" <<EOF
#!/usr/bin/env bash
case " \$* " in *" info_schema=scrap_dst "*) echo "psql: FATAL:  sorry, too many clients already" >&2; exit 2 ;; esac
exec $(command -v psql) "\$@"
EOF
chmod +x "${WORK}/shim/psql"
BACKUPS_BEFORE="$(backups scrap_dst)"
if PATH="${WORK}/shim:${PATH}" run_sync scrap_src scrap_dst; then bad "a failing check should stop it"; else check "a failing query about the target stops it" said "could not read schema scrap_dst"; fi
check "before asking anything" eval "! grep -q 'CRITICAL ACTION' \"${WORK}/out.log\""
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
# Objects in another schema that depend on the target (CASCADE would drop
# them and a column's data; audit finding).
q <<'SQL'
CREATE SCHEMA scrap_other;
CREATE VIEW scrap_other.v AS SELECT id FROM scrap_dst.items;
CREATE TABLE scrap_other.t (item_id bigint REFERENCES scrap_dst.items (id), snap scrap_dst.vehicles);
INSERT INTO scrap_other.t (item_id, snap) SELECT 1, v FROM scrap_dst.vehicles v WHERE id = 1;
SQL
if run_sync scrap_src scrap_dst; then bad "dependents elsewhere should be refused"; else check "objects elsewhere that depend on the target are refused" said "objects outside scrap_dst depend on it"; fi
check "they are named" said "view scrap_other.v"
check "and kept, with their data" [ "$(qa -c "SELECT count(*) FROM scrap_other.v") $(qa -c "SELECT count(snap) FROM scrap_other.t")" = "3 1" ]
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
q -c "DROP SCHEMA scrap_other CASCADE"
check "no backup was taken by the refused runs" [ "$(backups scrap_dst)" = "${BACKUPS_BEFORE}" ]

echo "==> 9. Grants and comments on the target are kept, or it rolls back"
mark_target
q -c "GRANT SELECT ON scrap_dst.items TO reader; COMMENT ON TABLE scrap_dst.items IS 'parts'"
if run_sync scrap_src scrap_dst; then bad "losing a grant should roll back"; else check "a grant the copy would lose rolls it back" said "would lose these grants or comments"; fi
check "it names them" said "grant items reader SELECT"
check "the grant is still there" [ "$(qa -c "SELECT has_table_privilege('reader', 'scrap_dst.items', 'SELECT')")" = t ]
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
q -c "SET ROLE stock; ALTER DEFAULT PRIVILEGES IN SCHEMA scrap_dst GRANT SELECT ON TABLES TO reader" -c "COMMENT ON TABLE scrap_dst.items IS NULL"
q -c "COMMENT ON TABLE scrap_src.items IS NULL"
run_sync scrap_src scrap_dst || true
check "grants the schema's default privileges give again are fine" said "Done. scrap_dst is now a copy of scrap_src"
check "and the grant is there after the copy" [ "$(qa -c "SELECT has_table_privilege('reader', 'scrap_dst.items', 'SELECT')")" = t ]
q -c "SET ROLE stock; ALTER DEFAULT PRIVILEGES IN SCHEMA scrap_dst REVOKE SELECT ON TABLES FROM reader" -c "REVOKE SELECT ON ALL TABLES IN SCHEMA scrap_dst FROM reader"
run_sync scrap_src scrap_dst || true
DST_BEFORE="$(fingerprint scrap_dst)"

echo "==> 10. Writes to the target after the backup make it roll back, losing none"
(for i in $(seq 1 40); do
   psql -X -q -h 127.0.0.1 -p "${PORT}" -U stock -d stock_items -c "INSERT INTO scrap_dst.shopping_entries (free_text) VALUES ('writer $i')" >/dev/null 2>&1
   sleep 0.1
 done; touch "${WORK}/writer.done") &
sleep 0.5
if run_sync scrap_src scrap_dst; then bad "a target being written to should not be replaced"; else check "a target being written to is not replaced" said "changed"; fi
while [ ! -e "${WORK}/writer.done" ]; do sleep 0.2; done
check "every write is still there" [ "$(qa -c "SELECT count(*) FROM scrap_dst.shopping_entries WHERE free_text LIKE 'writer %'")" = 40 ]
q -c "DELETE FROM scrap_dst.shopping_entries WHERE free_text LIKE 'writer %'"
run_sync scrap_src scrap_dst || true
DST_BEFORE="$(fingerprint scrap_dst)"

echo "==> 11. A kill during the transaction rolls it back (audit finding: psql used to commit)"
mark_target
(psql -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "BEGIN; LOCK TABLE scrap_dst.items IN ROW EXCLUSIVE MODE; SELECT pg_sleep(10); COMMIT;" >/dev/null 2>&1 &)
sleep 1
SYNC_LOCK_TIMEOUT=30s "${ROOT}/scripts/db-server/sync_schema.sh" scrap_src scrap_dst < <(echo OVERWRITE) >"${WORK}/out.log" 2>&1 &
SPID=$!
for _ in $(seq 1 100); do grep -q "==> 4" "${WORK}/out.log" && break; sleep 0.2; done
sleep 1
kill -TERM "${SPID}"
wait "${SPID}" && bad "a killed sync should fail" || ok "a killed sync fails"
check "it stops psql and says the target is unchanged" said "scrap_dst is unchanged"
sleep 9
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
check "no copy of the data is left behind" [ -z "$(find "${SYNC_BACKUP_DIR}" -name 'work.*')$(qa -c "SELECT datname FROM pg_database WHERE datname LIKE 'sync_staging_%'")" ]

echo "==> 12. Settings that change how values print don't matter"
q -c "ALTER DATABASE stock_items SET timezone = 'Pacific/Chatham'" -c "ALTER DATABASE stock_items SET bytea_output = 'escape'" -c "ALTER DATABASE stock_items SET DateStyle = 'SQL, DMY'"
PGHOSTADDR=10.255.255.1 PGOPTIONS='-c statement_timeout=1ms' PGDATABASE=postgres run_sync scrap_src scrap_dst || true
check "database settings and PG* variables in the environment don't change it" said "Done. scrap_dst is now a copy of scrap_src"
q -c "ALTER DATABASE stock_items RESET ALL"
# Leftovers of a run killed outright (kill -9) are cleared by the next one.
q -d postgres -c "CREATE DATABASE sync_staging_424242"
mkdir -p "${SYNC_BACKUP_DIR}/work.leftover"; touch "${SYNC_BACKUP_DIR}/stock_items-scrap_dst-x.dump.partial"
run_sync scrap_src scrap_dst || true
check "a leftover staging database is dropped" said "Dropping sync_staging_424242"
check "leftover work files are removed" [ -z "$(find "${SYNC_BACKUP_DIR}" -name 'work.leftover' -o -name '*.partial')" ]

echo "==> 13. The wrappers: public -> dev and dev -> public"
run_sync scrap_src public || true
check "a copy into public commits" said "Done. public is now a copy of scrap_src"
wrapper sync_stock_items_public_to_dev.sh OVERWRITE || true
check "public -> dev commits" said "Done. dev is now a copy of public"
check "dev holds public's rows" [ "$(fingerprint dev)" = "$(fingerprint public | sed 's/\bpublic\b/dev/g')" ]
q -c "SET ROLE stock; UPDATE dev.items SET notes = 'changed in dev', version = version + 1 WHERE id = 1"
PUB_BEFORE="$(fingerprint public)"
wrapper sync_stock_items_dev_to_public.sh OVERWRITE || true
check "public needs 'OVERWRITE public'" said "Aborted. No changes were made"
(psql -X -q -h 127.0.0.1 -p "${PORT}" -U stock -d stock_items -c "SELECT pg_sleep(5)" >/dev/null 2>&1 &)
sleep 1
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" && bad "public should be refused while the app is connected" || true
check "public is refused while the app user is connected" said "stock is connected to stock_items"
sleep 5
check "public is unchanged" [ "$(fingerprint public)" = "${PUB_BEFORE}" ]
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" || true
check "dev -> public commits" said "Done. public is now a copy of dev"
check "public has dev's change" [ "$(qa -c "SELECT notes FROM public.items WHERE id = 1")" = "changed in dev" ]
check "public's old state was backed up" [ "$(backups public)" -ge 1 ]
q -c "DELETE FROM dev.goose_db_version WHERE version_id = (SELECT max(version_id) FROM dev.goose_db_version)"
PUB_BEFORE="$(fingerprint public)"
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" && bad "an older dev over public should be refused" || true
check "an older migration over public is refused" said "different migrations applied"
q -c "INSERT INTO dev.goose_db_version (version_id, is_applied) SELECT max(version_id) + 2, true FROM dev.goose_db_version"
wrapper sync_stock_items_dev_to_public.sh "OVERWRITE public" && bad "a dev with unreleased migrations over public should be refused" || true
check "a migration public doesn't have is refused too" said "different migrations applied"
check "public is unchanged" [ "$(fingerprint public)" = "${PUB_BEFORE}" ]
q -c "DELETE FROM dev.goose_db_version WHERE version_id = (SELECT max(version_id) FROM dev.goose_db_version)"
run_sync dev scrap_dst || true
check "an older migration over another schema goes ahead with a note" said "it goes back to"

echo "==> 14. The manifest the sync checks the copy against notices any change"
# The query exactly as sync_schema.sh has it.
MANIFEST_SQL="$(sed -n "/^MANIFEST_SQL=\$(cat <<'SQL'\$/,/^SQL\$/p" "${ROOT}/scripts/db-server/sync_schema.sh" | sed '1d;$d')"
manifest() { printf '%s\n' "${MANIFEST_SQL}" | qa -v pitbox_sync_schema="$1"; }
M0="$(manifest scrap_dst)"
check "it is the same when nothing changed" [ "$(manifest scrap_dst)" = "${M0}" ]
check "and doesn't depend on the session's time zone" [ "$(PGTZ=Asia/Kathmandu manifest scrap_dst)" = "${M0}" ]
q -c "UPDATE scrap_dst.items SET notes = notes || '.' WHERE id = 2"
check "one changed value changes it" [ "$(manifest scrap_dst)" != "${M0}" ]
q -c "UPDATE scrap_dst.items SET notes = rtrim(notes, '.') WHERE id = 2"
check "and changing it back restores it" [ "$(manifest scrap_dst)" = "${M0}" ]
q -c "CREATE OR REPLACE VIEW scrap_dst.low_stock AS SELECT id, name FROM scrap_dst.items WHERE quantity <= min_quantity"
check "a view's definition changes it" [ "$(manifest scrap_dst)" != "${M0}" ]
q -c "SELECT setval('scrap_dst.items_id_seq', 99)" >/dev/null
M1="$(manifest scrap_dst)"
q -c "SELECT setval('scrap_dst.items_id_seq', 99, false)" >/dev/null
check "a sequence's is_called changes it" [ "$(manifest scrap_dst)" != "${M1}" ]
q -c "CREATE OR REPLACE FUNCTION scrap_dst.reorder_columns(tbl regclass, cols text[]) RETURNS void LANGUAGE plpgsql AS 'BEGIN END'"
M1="$(manifest scrap_dst)"
q -c "CREATE OR REPLACE FUNCTION scrap_dst.reorder_columns(tbl regclass, cols text[]) RETURNS void LANGUAGE plpgsql AS 'BEGIN NULL; END'"
check "a function body changes it" [ "$(manifest scrap_dst)" != "${M1}" ]

echo "==> 15. Cleaning up temporary schemas"
for _ in 1 2 3; do q -c "CREATE TEMP TABLE t (x int)"; done
# A session that used a temp table and keeps its (now empty) temp schema.
(psql -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "CREATE TEMP TABLE busy (x int)" -c "DROP TABLE busy" -c "SELECT pg_sleep(5)" >/dev/null 2>&1 &)
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
