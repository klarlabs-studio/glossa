#!/usr/bin/env bash
# The first half of the nightly Postgres backup (the init container
# "dump" of <fullname>-postgres-backup, postgres image): a plain-SQL
# pg_dump of the application database, gzipped into the Job's scratch
# volume and verified before the "upload" container ships it. As in v0.3
# (its backup CronJob, deleted with v0.3), what gets uploaded is checked
# first: a pg_dump that dies after the header still exits 0 through a
# pipe, and a job that ships 20 bytes every night reports success until
# the day it is needed.
#
# Then it is encrypted (age, to the offline recovery key and to the drill
# key) and the drill key must open the encrypted file to the same bytes,
# before the plaintext is removed. What is uploaded is <name>.age.
#
# Environment (set by the chart):
#   PGHOST, PGPORT, PGDATABASE, PGUSER, PGPASSWORD   the backup role's connection
#   OUT_DIR     the scratch volume shared with the upload container
#   PREFIX      dump file prefix: <PREFIX>-<UTC timestamp>.sql.gz
#   AGE_RECOVERY_RECIPIENT   the offline recovery key's public half
#   TOOLS       the directory with age and age-keygen (fetch-age)
#   DRILL_IDENTITY           the drill key's identity file
set -euo pipefail

log() { printf 'postgres-dump: %s\n' "$*"; }
fail() { printf 'postgres-dump: ERROR: %s\n' "$*" >&2; exit 1; }

# 0. Wait for the server. A new pod's first connections can be refused
#    for a moment while the network policy for it is programmed (k3s);
#    giving up on the first one fails a night's backup for nothing.
for _ in $(seq 1 30); do pg_isready -q -t 2 && break; sleep 1; done
pg_isready -q -t 2 || fail "Postgres at $PGHOST:$PGPORT does not answer"

# 1. pg_dump must be the server's major version. A newer pg_dump writes
#    settings an older server cannot parse (pg_dump 17 emits
#    `SET transaction_timeout`, unknown to 16), so its dump restores ZERO
#    tables under ON_ERROR_STOP. v0.3 shipped exactly that for weeks: the
#    dumps looked perfect from this side (2026-09-13, commit ace78d5).
client=$(pg_dump --version | sed -E 's/^[^0-9]*([0-9]+).*/\1/')
server=$(psql -X -At -v ON_ERROR_STOP=1 -c 'SHOW server_version_num')
server=$((server / 10000))
[[ $client == "$server" ]] ||
  fail "pg_dump $client against a Postgres $server server; postgres.image must be the server's major version"
log "pg_dump $client, server $server"

ts=$(date -u +%Y%m%dT%H%M%SZ)
name="$PREFIX-$ts.sql.gz"
partial="$OUT_DIR/$name.partial"

# 2. Dump. pipefail makes a failing pg_dump fail the pipe.
pg_dump --no-password | gzip -6 >"$partial"

# 3. Verify: an intact archive, pg_dump's own completion trailer (absent
#    when it died midway), and one CREATE TABLE per table of the live
#    database (a dump without schema is worthless however well it gzips).
gzip -t "$partial" || fail "the archive is corrupt"
gunzip -c "$partial" | tail -n 5 | grep -q '^-- PostgreSQL database dump complete' ||
  fail "the dump is truncated (no completion trailer)"
ddl=$(gunzip -c "$partial" | grep -cE '^CREATE (UNLOGGED )?TABLE ' || true)
live=$(psql -X -At -v ON_ERROR_STOP=1 -c \
  "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
((ddl >= 1)) || fail "the dump has no CREATE TABLE statements"
((ddl == live)) || fail "the dump has $ddl CREATE TABLE statements, the database $live tables"

# 4. Encrypt to both recipients, then prove the drill key reads back exactly
#    the dump. A backup that cannot be decrypted is not a backup.
drill=$("$TOOLS/age-keygen" -y "$DRILL_IDENTITY") || fail "the drill identity does not load"
"$TOOLS/age" -r "$AGE_RECOVERY_RECIPIENT" -r "$drill" -o "$OUT_DIR/$name.age" "$partial"
[[ $("$TOOLS/age" -d -i "$DRILL_IDENTITY" "$OUT_DIR/$name.age" | sha256sum) == "$(sha256sum <"$partial")" ]] ||
  fail "$name.age does not decrypt to the dump"
rm -f "$partial"
name="$name.age"
printf '%s\n' "$name" >"$OUT_DIR/dump.name"
log "$name: $(stat -c %s "$OUT_DIR/$name") bytes, encrypted, $ddl tables"
