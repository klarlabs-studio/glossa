#!/bin/sh
# v0-restore.sh — restore a Glossa v0.3 backup into a NEW database and
# mark it as a restore, so `glossa import --from v0 --v0-db` will read it
# (RFC 0006 §7.2).
#
#   PGHOST=… PGUSER=postgres scripts/v0-restore.sh glossa-20261001.sql.gz glossa_v0_restore
#
# The dump is v0.3's backup: plain pg_dump SQL, gzipped or not. The
# target database must not exist: the script creates it, and createdb
# fails on an existing database, so an existing database — v0.3's own
# included — is never marked. Run it as a superuser of a scratch
# Postgres 16 server, never of v0.3's production server. Connection
# settings come from the usual libpq variables (PGHOST, PGPORT, PGUSER,
# PGPASSWORD).
#
# Ownership and privilege statements are dropped from the dump: the
# restoring role owns everything, and v0.3's roles need not exist here.
# The restore runs in one transaction and stops at the first error.
set -eu

if [ $# -ne 2 ]; then
  echo "usage: $0 <v0.3-dump.sql[.gz]> <new-database>" >&2
  exit 2
fi
dump=$1
db=$2
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
marker=${GLOSSA_V0_MARKER_SQL:-$here/v0-restore-marker.sql}

[ -r "$dump" ] || { echo "v0-restore: can't read $dump" >&2; exit 2; }
[ -r "$marker" ] || { echo "v0-restore: can't read the marker SQL $marker" >&2; exit 2; }

if command -v sha256sum >/dev/null 2>&1; then
  sha=$(sha256sum "$dump" | cut -d' ' -f1)
else
  sha=$(shasum -a 256 "$dump" | cut -d' ' -f1)
fi

createdb "$db"

case $dump in
  *.gz) gunzip -c "$dump" ;;
  *) cat "$dump" ;;
esac |
  sed -E '/^(GRANT|REVOKE) /d; /^ALTER DEFAULT PRIVILEGES /d; /^ALTER [A-Z ]+ .+ OWNER TO /d' |
  psql -X -q -v ON_ERROR_STOP=1 --single-transaction -d "$db"

psql -X -q -v ON_ERROR_STOP=1 -d "$db" \
  -v dump_name="$(basename -- "$dump")" -v dump_sha256="$sha" -f "$marker"

echo "v0-restore: restored $(basename -- "$dump") (sha256 $sha) into $db, marked as a restore and read-only"
