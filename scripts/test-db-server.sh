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
run_sync() { echo "${CONFIRM:-OVERWRITE}" | "${ROOT}/scripts/db-server/sync_schema.sh" "$@" >"${WORK}/out.log" 2>&1; }
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
RESET ROLE;
-- Something not owned by stock, which the run_sync hands over.
CREATE TABLE scrap_src.postgres_owned (id int PRIMARY KEY);
INSERT INTO scrap_src.postgres_owned VALUES (1);
SQL
SRC_BEFORE="$(fingerprint scrap_src)"

echo "==> 1. Copy to a new schema"
run_sync scrap_src scrap_dst || { cat "${WORK}/out.log"; }
check "the copy commits" said "Done. scrap_dst is now a copy of scrap_src"
check "nothing to back up for a new schema" said "doesn't exist yet: nothing to back up"
check "the copy holds the same rows, views, functions and sequences" [ "$(fingerprint scrap_dst)" = "$(fingerprint scrap_src | sed 's/scrap_src/scrap_dst/g')" ]
check "stock owns everything in the copy" [ "$(notowned scrap_dst)" = 0 ]
check "stock owns everything in the source too" [ "$(notowned scrap_src)" = 0 ]
check "the source's data is unchanged" [ "$(fingerprint scrap_src)" = "${SRC_BEFORE}" ]
check "the copy's ids continue from the source's sequences" [ "$(qa -c "SET ROLE stock; INSERT INTO scrap_dst.vehicles (name) VALUES ('New') RETURNING id")" = 4 ]
check "the copy runs reorder_columns" qa -c "SET ROLE stock; BEGIN; SELECT scrap_dst.reorder_columns('scrap_dst.item_vehicles', ARRAY['vehicle_id', 'item_id']); ROLLBACK;"
q -c "DELETE FROM scrap_dst.vehicles WHERE id = 4"

echo "==> 2. Copy over an existing schema that holds reorder_columns (the bug of migration 00022)"
DST_BEFORE="$(fingerprint scrap_dst)"
run_sync scrap_src scrap_dst || { cat "${WORK}/out.log"; }
check "the copy commits" said "Done. scrap_dst is now a copy of scrap_src"
BACKUP="$(ls "${SYNC_BACKUP_DIR}"/stock_items-scrap_dst-*.dump 2>/dev/null | head -1)"
check "the old schema was backed up" [ -n "${BACKUP}" ]
check "the backup restores (pg_restore -l)" eval "pg_restore -l \"${BACKUP}\" >/dev/null"
check "the copy holds the same rows" [ "$(fingerprint scrap_dst)" = "$(fingerprint scrap_src | sed 's/scrap_src/scrap_dst/g')" ]

echo "==> 3. Answering anything but OVERWRITE changes nothing"
DST_BEFORE="$(fingerprint scrap_dst)"
CONFIRM=no run_sync scrap_src scrap_dst
check "it says it aborted" said "Aborted. No changes were made"
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 4. An object the clearing doesn't drop rolls everything back"
q -c "CREATE COLLATION scrap_dst.c_bytes (provider = libc, locale = 'C')"
if run_sync scrap_src scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "it names the object" said "c_bytes (collation)"
check "it says nothing was changed" said "scrap_dst is unchanged"
check "the target's tables and rows are still there" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]
q -c "DROP COLLATION scrap_dst.c_bytes"

echo "==> 5. A failure in the middle of the restore rolls back the clearing too"
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
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 6. A lock held by the app makes it stop, not wait"
(psql -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "BEGIN; LOCK TABLE scrap_dst.items IN ACCESS SHARE MODE; SELECT pg_sleep(8); COMMIT;" >/dev/null 2>&1 &)
sleep 1
if run_sync scrap_src scrap_dst; then bad "the sync should have failed"; else ok "the sync fails"; fi
check "on the lock timeout" said "lock timeout"
sleep 7
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 7. Refusals before anything happens"
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
check "the target is unchanged" [ "$(fingerprint scrap_dst)" = "${DST_BEFORE}" ]

echo "==> 8. The wrappers: public -> dev and dev -> public"
run_sync scrap_src public || { cat "${WORK}/out.log"; }
check "a copy into public commits" said "Done. public is now a copy of scrap_src"
echo OVERWRITE | "${ROOT}/scripts/db-server/sync_stock_items_public_to_dev.sh" >"${WORK}/out.log" 2>&1 || cat "${WORK}/out.log"
check "public -> dev commits" said "Done. dev is now a copy of public"
check "dev holds public's rows" [ "$(fingerprint dev)" = "$(fingerprint public | sed 's/\bpublic\b/dev/g')" ]
q -c "SET ROLE stock; UPDATE dev.items SET notes = 'changed in dev', version = version + 1 WHERE id = 1"
echo OVERWRITE | "${ROOT}/scripts/db-server/sync_stock_items_dev_to_public.sh" >"${WORK}/out.log" 2>&1 || cat "${WORK}/out.log"
check "dev -> public commits" said "Done. public is now a copy of dev"
check "public has dev's change" [ "$(qa -c "SELECT notes FROM public.items WHERE id = 1")" = "changed in dev" ]
check "public's old state was backed up" eval "ls \"${SYNC_BACKUP_DIR}\"/stock_items-public-*.dump >/dev/null"
q -c "DELETE FROM dev.goose_db_version WHERE version_id = (SELECT max(version_id) FROM dev.goose_db_version)"
PUB_BEFORE="$(fingerprint public)"
echo OVERWRITE | "${ROOT}/scripts/db-server/sync_stock_items_dev_to_public.sh" >"${WORK}/out.log" 2>&1 && bad "an older dev over public should be refused" || true
check "an older migration over public is refused" said "refusing to take the real data back"
check "public is unchanged" [ "$(fingerprint public)" = "${PUB_BEFORE}" ]
run_sync dev scrap_dst || true
check "an older migration over another schema goes ahead with a note" said "it goes back to"

echo "==> 9. The manifest the sync checks the copy against notices any change"
# The query exactly as sync_schema.sh has it.
MANIFEST_SQL="$(sed -n "/^MANIFEST_SQL=\$(cat <<'SQL'\$/,/^SQL\$/p" "${ROOT}/scripts/db-server/sync_schema.sh" | sed '1d;$d')"
manifest() { printf '%s\n' "${MANIFEST_SQL}" | qa -v s="$1"; }
M0="$(manifest scrap_dst)"
check "it is the same when nothing changed" [ "$(manifest scrap_dst)" = "${M0}" ]
q -c "UPDATE scrap_dst.items SET notes = notes || '.' WHERE id = 2"
check "one changed value changes it" [ "$(manifest scrap_dst)" != "${M0}" ]
q -c "UPDATE scrap_dst.items SET notes = rtrim(notes, '.') WHERE id = 2"
check "and changing it back restores it" [ "$(manifest scrap_dst)" = "${M0}" ]
q -c "SELECT setval('scrap_dst.items_id_seq', 99)" >/dev/null
check "a sequence value changes it" [ "$(manifest scrap_dst)" != "${M0}" ]
q -c "CREATE OR REPLACE FUNCTION scrap_dst.reorder_columns(tbl regclass, cols text[]) RETURNS void LANGUAGE plpgsql AS 'BEGIN END'"
M1="$(manifest scrap_dst)"
q -c "CREATE OR REPLACE FUNCTION scrap_dst.reorder_columns(tbl regclass, cols text[]) RETURNS void LANGUAGE plpgsql AS 'BEGIN NULL; END'"
check "a function body changes it" [ "$(manifest scrap_dst)" != "${M1}" ]

echo "==> 10. Cleaning up temporary schemas"
for _ in 1 2 3; do q -c "CREATE TEMP TABLE t (x int)"; done
(psql -X -q -h 127.0.0.1 -p "${PORT}" -U postgres -d stock_items -c "CREATE TEMP TABLE busy (x int)" -c "SELECT pg_sleep(6)" >/dev/null 2>&1 &)
sleep 1
TEMP_BEFORE="$(qa -c "SELECT count(*) FROM pg_namespace WHERE nspname ~ '^pg_(toast_)?temp_'")"
SYNC_DB=stock_items "${ROOT}/scripts/db-server/cleanup_temp_schemas.sh" >"${WORK}/out.log" 2>&1 || cat "${WORK}/out.log"
TEMP_AFTER="$(qa -c "SELECT count(*) FROM pg_namespace WHERE nspname ~ '^pg_(toast_)?temp_'")"
check "empty temporary schemas are dropped (${TEMP_BEFORE} -> ${TEMP_AFTER})" [ "${TEMP_AFTER}" -lt "${TEMP_BEFORE}" ]
check "the one in use is kept" said "kept 1 in use"
sleep 6
check "Postgres makes them again when needed" [ "$(qa -c "CREATE TEMP TABLE again (x int); SELECT pg_my_temp_schema() <> 0")" = t ]

echo
echo "${pass} passed, ${fail} failed"
[ "${fail}" = 0 ]
