#!/usr/bin/env bash
# The weekly restore drill (the container "restore" of
# <fullname>-postgres-restore-test, postgres image). The backup Job
# verifies what it uploads; that is a different claim from "this file
# loads into a Postgres and comes back as the database". A backup that
# has never been restored is a hypothesis (v0.3's drill found its dumps
# restored zero tables, 2026-09-13).
#
# Unlike v0.3's drill, this never connects to the live database: it
# restores into a throwaway Postgres of the same image (so the same major
# version as the server) started inside this container on the Job's
# scratch volume, then asserts the post-state:
#   - the whole dump loads under ON_ERROR_STOP in one transaction,
#   - one table per CREATE TABLE in the dump came back,
#   - schema_migrations holds a clean (not dirty) version,
#   - every table in NON_EMPTY_TABLES has rows.
#
# Environment (set by the chart):
#   IN_DIR            the scratch volume with dump.name and the dump
#   ROLES             roles the dump references (owner, glossa_app, glossa_system)
#   OWNER             the schema owner (owns the scratch database)
#   NON_EMPTY_TABLES  space-separated, may be empty
set -euo pipefail

log() { printf 'restore-test: %s\n' "$*"; }
fail() { printf 'restore-test: ERROR: %s\n' "$*" >&2; exit 1; }

name=$(cat "$IN_DIR/dump.name") || fail "no dump.name: the fetch container did not finish"
dump="$IN_DIR/$name"
[[ -f $dump ]] || fail "the dump $name is missing"

# 1. A throwaway Postgres: no TCP, a unix socket in /tmp, no durability.
export PGDATA="$IN_DIR/pgdata" PGHOST=/tmp/restore-socket PGUSER=postgres
rm -rf "$PGDATA"
mkdir -p "$PGHOST"
initdb --username=postgres --auth=trust --no-sync --no-instructions >/dev/null
pg_ctl --wait --timeout=120 --log=/tmp/restore-postgres.log \
  -o "-c listen_addresses='' -k $PGHOST -c fsync=off -c full_page_writes=off -c synchronous_commit=off" start >/dev/null ||
  { cat /tmp/restore-postgres.log >&2; fail "the throwaway Postgres did not start"; }
trap 'pg_ctl --mode=immediate stop >/dev/null 2>&1 || true' EXIT
log "throwaway $(postgres --version) started"

q() { psql -X -q -At -v ON_ERROR_STOP=1 "$@"; }

# 2. The roles the dump grants to or assigns ownership to are cluster-wide
#    and not in a database dump; the scratch database mirrors the live one.
for role in $ROLES; do
  q -d postgres -v r="$role" <<'SQL'
SELECT format('CREATE ROLE %I NOLOGIN', :'r') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'r') \gexec
SQL
done
q -d postgres -v owner="$OWNER" <<'SQL'
SELECT format('CREATE DATABASE restore_test OWNER %I', :'owner') \gexec
SQL

# 3. Restore: all or nothing, stopping at the first error.
gunzip -c "$dump" | psql -X -q -v ON_ERROR_STOP=1 --single-transaction -d restore_test >/dev/null ||
  fail "$name does not restore (see the psql error above)"

# 4. ASSERT THE POST-STATE, never that psql exited 0: an empty schema
#    restores perfectly and is worthless.
ddl=$(gunzip -c "$dump" | grep -cE '^CREATE (UNLOGGED )?TABLE ' || true)
tables=$(q -d restore_test -c \
  "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
((ddl >= 1)) || fail "$name has no CREATE TABLE statements"
((tables == ddl)) || fail "$tables tables restored, the dump creates $ddl"

migration=$(q -d restore_test -c 'SELECT version, dirty FROM schema_migrations') ||
  fail "schema_migrations is missing: the restored schema is not glossa's"
if ! [[ $migration =~ ^[0-9]+\|f$ ]] || ((${migration%|*} == 0)); then
  fail "schema_migrations is '$migration': no clean migration version"
fi

for table in ${NON_EMPTY_TABLES:-}; do
  n=$(q -d restore_test -v t="$table" <<'SQL'
SELECT format('SELECT count(*) FROM %I', :'t') \gexec
SQL
  ) || fail "table $table is missing"
  ((n >= 1)) || fail "table $table came back empty"
  log "$table: $n rows"
done

rows=$(q -d restore_test -c "
  SELECT coalesce(sum((xpath('/row/n/text()',
           query_to_xml(format('SELECT count(*) AS n FROM %I.%I', schemaname, tablename), false, true, '')))[1]::text::bigint), 0)
    FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
log "restore drill OK ($name): $tables tables, $rows rows, schema version ${migration%|*}"
